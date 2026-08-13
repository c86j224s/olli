package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/sys/unix"
)

const eventSchemaName = "https://olli.local/schemas/oaw-event-v0.1.schema.json"

var errLogLimit = errors.New("workflow log limit reached")

type eventWriter struct {
	mu       sync.Mutex
	f        *os.File
	parent   *os.File
	partial  string
	bytes    int64
	terminal bool
	schema   *jsonschema.Schema
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
	document, err := decodeJSONDocument(data)
	if err != nil {
		return fmt.Errorf("event schema JSON: %w", err)
	}
	if err := rejectExternalRefs(document); err != nil {
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
	return &eventWriter{f: file, parent: parent, partial: partial, schema: e.eventSchema}, nil
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
	offset, err := w.f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	written, writeErr := w.f.Write(line)
	if writeErr != nil || written != len(line) {
		rollbackErr := w.rollback(offset)
		if rollbackErr != nil {
			w.quarantine()
			return fmt.Errorf("workflow log write failed and rollback failed: %w", rollbackErr)
		}
		if writeErr != nil {
			return writeErr
		}
		return io.ErrShortWrite
	}
	w.bytes += int64(written)
	if terminal {
		w.terminal = true
	}
	return nil
}

func (w *eventWriter) rollback(offset int64) error {
	if err := w.f.Truncate(offset); err != nil {
		return err
	}
	_, err := w.f.Seek(offset, io.SeekStart)
	return err
}

func (w *eventWriter) quarantine() {
	if w == nil || w.parent == nil || w.partial == "" {
		return
	}
	invalid := stringsTrimSuffix(w.partial, ".partial") + ".invalid"
	_ = unix.Renameat(int(w.parent.Fd()), w.partial, int(w.parent.Fd()), invalid)
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
	if err := w.f.Sync(); err != nil {
		return "", err
	}
	finalName := runID + ".jsonl"
	var stat unix.Stat_t
	err := unix.Fstatat(int(w.parent.Fd()), finalName, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return "", errors.New("final workflow log already exists")
	}
	if !errors.Is(err, syscall.ENOENT) {
		return "", err
	}
	if err := unix.Linkat(int(w.parent.Fd()), w.partial, int(w.parent.Fd()), finalName, 0); err != nil {
		return "", err
	}
	cleanupFinal := func() error {
		if err := unix.Unlinkat(int(w.parent.Fd()), finalName, 0); err != nil {
			invalid := finalName + ".invalid"
			if renameErr := unix.Renameat(int(w.parent.Fd()), finalName, int(w.parent.Fd()), invalid); renameErr != nil {
				return fmt.Errorf("remove final log: %v; quarantine final log: %w", err, renameErr)
			}
		}
		return w.parent.Sync()
	}
	if err := unix.Unlinkat(int(w.parent.Fd()), w.partial, 0); err != nil {
		if cleanupErr := cleanupFinal(); cleanupErr != nil {
			return "", fmt.Errorf("remove staging log: %v; cleanup final log: %w", err, cleanupErr)
		}
		return "", err
	}
	if err := w.parent.Sync(); err != nil {
		if cleanupErr := cleanupFinal(); cleanupErr != nil {
			return "", fmt.Errorf("sync promoted log directory: %v; cleanup final log: %w", err, cleanupErr)
		}
		return "", err
	}
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
	if w.parent != nil && w.partial != "" {
		invalid := stringsTrimSuffix(w.partial, ".partial") + ".invalid"
		if err := unix.Renameat(int(w.parent.Fd()), w.partial, int(w.parent.Fd()), invalid); err != nil {
			_ = unix.Unlinkat(int(w.parent.Fd()), w.partial, 0)
		}
		_ = w.parent.Sync()
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
}
