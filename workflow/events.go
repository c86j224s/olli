package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/sys/unix"
)

var errLogLimit = errors.New("workflow log limit reached")

type eventOps struct {
	write           func(*os.File, []byte) (int, error)
	seek            func(*os.File, int64, int) (int64, error)
	truncate        func(*os.File, int64) error
	syncFile        func(*os.File) error
	fstat           func(*os.File, *unix.Stat_t) error
	fstatat         func(int, string, *unix.Stat_t, int) error
	linkat          func(int, string, int, string, int) error
	unlinkat        func(int, string, int) error // retained for existing append seams
	renameNoReplace func(int, string, int, string) error
	chmod           func(*os.File, uint32) error
	fchmod          func(*os.File, uint32) error // compatibility alias for focused seams
	syncDir         func(*os.File) error
}

func defaultEventOps() eventOps {
	fchmod := func(f *os.File, mode uint32) error { return unix.Fchmod(int(f.Fd()), mode) }
	return eventOps{
		write:           (*os.File).Write,
		seek:            (*os.File).Seek,
		truncate:        (*os.File).Truncate,
		syncFile:        (*os.File).Sync,
		fstat:           func(f *os.File, st *unix.Stat_t) error { return unix.Fstat(int(f.Fd()), st) },
		fstatat:         unix.Fstatat,
		linkat:          unix.Linkat,
		unlinkat:        unix.Unlinkat,
		renameNoReplace: renameNoReplaceAt,
		chmod:           fchmod,
		fchmod:          fchmod,
		syncDir:         (*os.File).Sync,
	}
}

type eventWriter struct {
	mu        sync.Mutex
	root      *os.File
	f         *os.File
	parent    *os.File
	partial   string
	bytes     int64
	committed bool
	sequence  int64
	started   bool
	terminal  bool
	schema    *jsonschema.Schema
	ops       eventOps
}

func sameStat(a, b *unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino
}

func regularStat(st *unix.Stat_t) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFREG
}

func directoryStat(st *unix.Stat_t) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFDIR
}

func noReplaceLinkat(parent *os.File, source, target string, ops eventOps) error {
	return ops.linkat(int(parent.Fd()), source, int(parent.Fd()), target, 0)
}

func quarantineNames(parent *os.File, source, invalid string, ops eventOps) error {
	return ops.renameNoReplace(int(parent.Fd()), source, int(parent.Fd()), invalid)
}

func markerName(runID string) string { return "." + runID + ".jsonl.commit" }

func (w *eventWriter) verifyMarker(runID string, opened *unix.Stat_t) error {
	var marker unix.Stat_t
	if err := w.ops.fstatat(int(w.parent.Fd()), markerName(runID), &marker, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if !regularStat(&marker) || !sameStat(opened, &marker) {
		return errors.New("workflow commit marker changed")
	}
	return nil
}

func (w *eventWriter) verifyParent() error {
	fresh, err := openRelativeDirFrom(w.root, []string{"sessions", "workflows"})
	if err != nil {
		return err
	}
	defer fresh.Close()
	var expected, actual unix.Stat_t
	if err := w.ops.fstat(w.parent, &expected); err != nil {
		return err
	}
	if err := w.ops.fstat(fresh, &actual); err != nil {
		return err
	}
	if !directoryStat(&expected) || !sameStat(&expected, &actual) {
		return errors.New("workflow log parent changed")
	}
	return nil
}

func (w *eventWriter) verifyStaging() error {
	var opened, path unix.Stat_t
	if err := w.ops.fstat(w.f, &opened); err != nil {
		return err
	}
	if !regularStat(&opened) {
		return errors.New("workflow staging log is not regular")
	}
	if err := w.ops.fstatat(int(w.parent.Fd()), w.partial, &path, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if !regularStat(&path) || !sameStat(&opened, &path) {
		return errors.New("workflow staging log changed")
	}
	return nil
}

func (w *eventWriter) verifyFinal(runID string, opened *unix.Stat_t) error {
	var final unix.Stat_t
	if err := w.ops.fstatat(int(w.parent.Fd()), runID+".jsonl", &final, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if !regularStat(&final) || !sameStat(opened, &final) {
		return errors.New("workflow final log changed")
	}
	return nil
}

func openRelativeDirFrom(root *os.File, parts []string) (*os.File, error) {
	if root == nil {
		return nil, errors.New("workflow root descriptor is closed")
	}
	current, err := dupFile(root)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		next, openErr := openNoFollowDir(current, part)
		_ = current.Close()
		if openErr != nil {
			return nil, openErr
		}
		current = next
	}
	return current, nil
}

func dupFile(file *os.File) (*os.File, error) {
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	result := os.NewFile(uintptr(fd), file.Name())
	if result == nil {
		_ = unix.Close(fd)
		return nil, errors.New("failed to duplicate file descriptor")
	}
	return result, nil
}

func (w *eventWriter) validateEventShape(event map[string]any, terminal bool) error {
	sequence, ok := event["sequence"].(json.Number)
	if ok {
		if string(sequence) != fmt.Sprint(w.sequence) {
			return errors.New("event sequence is not monotonic")
		}
	} else {
		valid := false
		switch value := event["sequence"].(type) {
		case int:
			valid = int64(value) == w.sequence
		case int8:
			valid = int64(value) == w.sequence
		case int16:
			valid = int64(value) == w.sequence
		case int32:
			valid = int64(value) == w.sequence
		case int64:
			valid = value == w.sequence
		case uint:
			valid = uint64(value) == uint64(w.sequence)
		case uint8:
			valid = uint64(value) == uint64(w.sequence)
		case uint16:
			valid = uint64(value) == uint64(w.sequence)
		case uint32:
			valid = uint64(value) == uint64(w.sequence)
		case uint64:
			valid = value == uint64(w.sequence)
		case float64:
			valid = value == float64(w.sequence)
		}
		if !valid {
			return errors.New("event sequence is invalid")
		}
	}
	name, _ := event["event"].(string)
	if w.sequence == 0 && name != "workflow_started" {
		return errors.New("workflow log must start with workflow_started")
	}
	if w.sequence > 0 && name == "workflow_started" {
		return errors.New("workflow_started is only valid at sequence zero")
	}
	isTerminal := name == "workflow_completed" || name == "workflow_cancelled"
	if terminal != isTerminal {
		return errors.New("terminal flag does not match event")
	}
	return nil
}

func strictEventDocument(line []byte) (map[string]any, error) {
	if !utf8.Valid(line) {
		return nil, errors.New("workflow event is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("workflow event must be an object")
	}
	return object, nil
}

func (e *Engine) duplicateRoot() (*os.File, error) {
	e.closeMu.RLock()
	defer e.closeMu.RUnlock()
	if e.closed || e.rootFD == nil {
		return nil, errors.New("workflow engine is closed")
	}
	fd, err := unix.Dup(int(e.rootFD.Fd()))
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), e.root)
	if f == nil {
		_ = unix.Close(fd)
		return nil, errors.New("failed to duplicate workspace descriptor")
	}
	return f, nil
}

func (e *Engine) ensureDirs(parts []string) error {
	current, err := e.duplicateRoot()
	if err != nil {
		return err
	}
	for _, part := range parts {
		if !validComponent(part) {
			_ = current.Close()
			return errors.New("invalid directory component")
		}
		next, openErr := openNoFollowDir(current, part)
		if openErr != nil {
			if !errors.Is(openErr, syscall.ENOENT) {
				_ = current.Close()
				return openErr
			}
			if err := unix.Mkdirat(int(current.Fd()), part, 0700); err != nil && !errors.Is(err, syscall.EEXIST) {
				_ = current.Close()
				return err
			}
			next, openErr = openNoFollowDir(current, part)
			if openErr != nil {
				_ = current.Close()
				return openErr
			}
		}
		_ = current.Close()
		current = next
	}
	return current.Close()
}

func eventBase(runID, workflowName, event, status string, sequence int64) map[string]any {
	return map[string]any{
		"event_schema_version": "0.1",
		"event":                event,
		"timestamp":            time.Now().UTC().Format(time.RFC3339Nano),
		"sequence":             sequence,
		"run_id":               runID,
		"workflow":             workflowName,
		"status":               status,
	}
}

func (e *Engine) compileEventSchema() error {
	data, err := e.readRelative([]string{"workflows", "agent", "oaw-event.schema.json"}, maxWorkflowFile)
	if err != nil {
		return fmt.Errorf("event schema: %w", err)
	}
	if !utf8.Valid(data) {
		return errors.New("event schema: file is not valid UTF-8")
	}
	document, err := decodeJSONDocument(data)
	if err != nil {
		return fmt.Errorf("event schema JSON: %w", err)
	}
	if err := rejectExternalRefs(document); err != nil {
		return fmt.Errorf("event schema: %w", err)
	}
	if err := validateCanonicalSchema(document, eventSchemaName); err != nil {
		return fmt.Errorf("event schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(eventSchemaName, document); err != nil {
		return err
	}
	schema, err := compiler.Compile(eventSchemaName)
	if err != nil {
		return err
	}
	e.eventSchema = schema
	return nil
}

func (e *Engine) openEventWriter(runID, workflowName string) (*eventWriter, error) {
	if !runIDPattern.MatchString(runID) || !namePattern.MatchString(workflowName) {
		return nil, errors.New("invalid workflow log identity")
	}
	if e.eventSchema == nil {
		return nil, errors.New("event schema is unavailable")
	}
	parent, err := e.openRelativeDir([]string{"sessions", "workflows"})
	if err != nil {
		return nil, err
	}
	partial := "." + runID + ".jsonl.partial"
	fd, err := unix.Openat(int(parent.Fd()), partial, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		_ = parent.Close()
		return nil, err
	}
	file := os.NewFile(uintptr(fd), partial)
	if file == nil {
		_ = unix.Close(fd)
		_ = parent.Close()
		return nil, errors.New("failed to create workflow event log")
	}
	root, err := e.duplicateRoot()
	if err != nil {
		_ = file.Close()
		_ = parent.Close()
		return nil, err
	}
	return &eventWriter{root: root, f: file, parent: parent, partial: partial, schema: e.eventSchema, ops: defaultEventOps()}, nil
}

func (w *eventWriter) append(event map[string]any, terminal bool) error {
	if w == nil {
		return errors.New("workflow event writer is closed")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil || w.parent == nil {
		return errors.New("workflow event writer is closed")
	}
	if w.terminal {
		return errors.New("event after terminal event")
	}
	if err := w.validateEventShape(event, terminal); err != nil {
		return err
	}
	if err := w.schema.Validate(event); err != nil {
		return fmt.Errorf("event schema validation: %w", err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line := append(payload, '\n')
	if len(line) > maxLineBytes {
		return errors.New("workflow event line exceeds 64 KiB")
	}
	limit := int64(maxLogBytes - terminalReserve)
	if terminal {
		limit = maxLogBytes
	}
	if w.bytes+int64(len(line)) > limit {
		return errLogLimit
	}
	offset, err := w.ops.seek(w.f, 0, io.SeekCurrent)
	if err != nil {
		return err
	}
	written, writeErr := w.ops.write(w.f, line)
	if writeErr != nil || written != len(line) {
		rollbackErr := w.rollback(offset)
		if rollbackErr != nil {
			if quarantineErr := w.quarantine(); quarantineErr != nil {
				return fmt.Errorf("workflow log write failed and rollback failed: %v; quarantine failed: %w", rollbackErr, quarantineErr)
			}
			return fmt.Errorf("workflow log write failed and rollback failed: %w", rollbackErr)
		}
		if writeErr != nil {
			return writeErr
		}
		return io.ErrShortWrite
	}
	w.bytes += int64(written)
	w.sequence++
	if event["event"] == "workflow_started" {
		w.started = true
	}
	if terminal {
		w.terminal = true
	}
	return nil
}

func (w *eventWriter) rollback(offset int64) error {
	if err := w.ops.truncate(w.f, offset); err != nil {
		return err
	}
	_, err := w.ops.seek(w.f, offset, io.SeekStart)
	return err
}

func (w *eventWriter) quarantine() error {
	if w == nil || w.parent == nil || w.partial == "" {
		return nil
	}
	invalid := stringsTrimSuffix(w.partial, ".partial") + ".invalid"
	return quarantineNames(w.parent, w.partial, invalid, w.ops)
}

func stringsTrimSuffix(value, suffix string) string {
	if len(value) >= len(suffix) && value[len(value)-len(suffix):] == suffix {
		return value[:len(value)-len(suffix)]
	}
	return value
}

func (w *eventWriter) finalize(runID string) (string, error) {
	if w == nil {
		return "", errors.New("workflow event writer is closed")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil || w.parent == nil {
		return "", errors.New("workflow event writer is closed")
	}
	if !w.terminal {
		return "", errors.New("terminal event is required")
	}
	if err := w.verifyParent(); err != nil {
		return "", err
	}
	if err := w.ops.syncFile(w.f); err != nil {
		return "", err
	}
	if err := w.verifyStaging(); err != nil {
		return "", err
	}
	finalName := runID + ".jsonl"
	commitName := markerName(runID)
	var opened unix.Stat_t
	if err := w.ops.fstat(w.f, &opened); err != nil {
		return "", err
	}
	if !regularStat(&opened) {
		return "", errors.New("workflow staging log is not regular")
	}
	var stat unix.Stat_t
	for _, name := range []string{finalName, commitName} {
		err := w.ops.fstatat(int(w.parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			return "", fmt.Errorf("workflow publication target already exists: %s", name)
		}
		if !errors.Is(err, syscall.ENOENT) {
			return "", err
		}
	}
	if err := noReplaceLinkat(w.parent, w.partial, commitName, w.ops); err != nil {
		return "", err
	}
	if err := w.verifyMarker(runID, &opened); err != nil {
		return "", err
	}
	if err := w.ops.renameNoReplace(int(w.parent.Fd()), w.partial, int(w.parent.Fd()), finalName); err != nil {
		return "", err
	}
	if err := w.verifyFinal(runID, &opened); err != nil {
		return "", err
	}
	if err := w.verifyMarker(runID, &opened); err != nil {
		return "", err
	}
	if err := w.verifyParent(); err != nil {
		return "", err
	}
	if err := w.ops.syncDir(w.parent); err != nil {
		return "", err
	}
	if err := w.verifyParent(); err != nil {
		return "", err
	}
	if err := w.verifyFinal(runID, &opened); err != nil {
		return "", err
	}
	if err := w.verifyMarker(runID, &opened); err != nil {
		return "", err
	}
	chmod := w.ops.chmod
	if chmod == nil {
		chmod = w.ops.fchmod
	}
	if chmod == nil {
		return "", errors.New("workflow chmod seam is unavailable")
	}
	if err := chmod(w.f, 0400); err != nil {
		return "", err
	}
	w.committed = true
	return filepath.Join("sessions", "workflows", finalName), nil
}

func (w *eventWriter) abort() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	if !w.committed && w.parent != nil && w.partial != "" {
		_ = w.quarantine()
		_ = w.ops.syncDir(w.parent)
	}
	w.closeLocked()
}

func (w *eventWriter) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closeLocked()
}

func (w *eventWriter) closeLocked() {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	if w.parent != nil {
		_ = w.parent.Close()
		w.parent = nil
	}
	if w.root != nil {
		_ = w.root.Close()
		w.root = nil
	}
}
