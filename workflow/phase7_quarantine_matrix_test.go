package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func phase7Writer(t *testing.T, workflow, runID string) (*eventWriter, string, []byte, os.FileInfo) {
	t.Helper()
	engine := setupEngine(t, workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	writer, err := engine.openEventWriter(runID, workflow)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.close)
	if err := writer.append(eventBase(runID, workflow, "workflow_started", "started", 0), false); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(engine.root, "sessions", "workflows", writer.partial)
	before, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Lstat(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	return writer, partialPath, before, beforeInfo
}

func phase7SkippedEvent(runID, workflow string, sequence int64) map[string]any {
	event := eventBase(runID, workflow, "step_skipped", "skipped", sequence)
	event["step_id"] = "tail"
	event["attempt"] = 1
	return event
}

func phase7InvalidPath(writer *eventWriter) string {
	return strings.TrimSuffix(writer.partial, ".partial") + ".invalid"
}

func phase7ExpectedPrefix(before []byte, event map[string]any, written int) []byte {
	payload, err := json.Marshal(event)
	if err != nil {
		panic(err)
	}
	line := append(payload, '\n')
	return append(append([]byte(nil), before...), line[:written]...)
}

func phase7AssertState(t *testing.T, writer *eventWriter, wantBytes, wantSequence int64, wantTerminal bool, wantOffset int64) {
	t.Helper()
	gotOffset, err := writer.f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	if writer.bytes != wantBytes || writer.sequence != wantSequence || writer.terminal != wantTerminal || gotOffset != wantOffset {
		t.Fatalf("writer state changed: bytes=%d sequence=%d terminal=%t offset=%d; want bytes=%d sequence=%d terminal=%t offset=%d", writer.bytes, writer.sequence, writer.terminal, gotOffset, wantBytes, wantSequence, wantTerminal, wantOffset)
	}
}

func phase7PartialWrite(t *testing.T, injected error) func(*os.File, []byte) (int, error) {
	t.Helper()
	return func(file *os.File, data []byte) (int, error) {
		written, err := file.Write(data[:len(data)-1])
		if err != nil {
			t.Fatal(err)
		}
		return written, injected
	}
}

func phase7PartialWriteNoFatal(injected error) func(*os.File, []byte) (int, error) {
	return func(file *os.File, data []byte) (int, error) {
		written, err := file.Write(data[:len(data)-1])
		if err != nil {
			return written, err
		}
		return written, injected
	}
}

func phase7AssertSameInode(t *testing.T, left, right string) {
	t.Helper()
	leftInfo, err := os.Lstat(left)
	if err != nil {
		t.Fatal(err)
	}
	rightInfo, err := os.Lstat(right)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(leftInfo, rightInfo) {
		t.Fatalf("paths do not reference the same inode: %s and %s", left, right)
	}
}

func TestPhase7ShortWriteRollbackPreservesAllWriterState(t *testing.T) {
	writer, partialPath, before, _ := phase7Writer(t, "phase7-short-write", "oaw_00000000000000000000000000000051")
	beforeBytes, beforeSequence, beforeTerminal := writer.bytes, writer.sequence, writer.terminal
	beforeOffset, err := writer.f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	writer.ops.write = phase7PartialWriteNoFatal(nil)

	err = writer.append(phase7SkippedEvent("oaw_00000000000000000000000000000051", "phase7-short-write", 1), false)
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v, want io.ErrShortWrite", err)
	}
	after, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("partial bytes changed after rollback: got %d bytes, want %d", len(after), len(before))
	}
	phase7AssertState(t, writer, beforeBytes, beforeSequence, beforeTerminal, beforeOffset)
}

func TestPhase7WriteErrorRollbackPreservesAllWriterState(t *testing.T) {
	writer, partialPath, before, _ := phase7Writer(t, "phase7-write-error", "oaw_00000000000000000000000000000052")
	beforeBytes, beforeSequence, beforeTerminal := writer.bytes, writer.sequence, writer.terminal
	beforeOffset, err := writer.f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("injected write error")
	writer.ops.write = phase7PartialWriteNoFatal(writeErr)

	err = writer.append(phase7SkippedEvent("oaw_00000000000000000000000000000052", "phase7-write-error", 1), false)
	if !errors.Is(err, writeErr) {
		t.Fatalf("write error = %v, want %v", err, writeErr)
	}
	after, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("partial bytes changed after rollback: got %d bytes, want %d", len(after), len(before))
	}
	phase7AssertState(t, writer, beforeBytes, beforeSequence, beforeTerminal, beforeOffset)
}

func TestPhase7TruncateFailureQuarantinesUsingPartialDerivedInvalidName(t *testing.T) {
	writer, partialPath, before, beforeInfo := phase7Writer(t, "phase7-truncate-failure", "oaw_00000000000000000000000000000053")
	invalidPath := filepath.Join(filepath.Dir(partialPath), phase7InvalidPath(writer))
	event := phase7SkippedEvent("oaw_00000000000000000000000000000053", "phase7-truncate-failure", 1)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	writer.ops.write = phase7PartialWriteNoFatal(nil)
	truncateErr := errors.New("injected truncate failure")
	writer.ops.truncate = func(*os.File, int64) error { return truncateErr }

	if err := writer.append(event, false); err == nil {
		t.Fatal("truncate failure was not surfaced")
	}
	if _, err := os.Lstat(partialPath); !os.IsNotExist(err) {
		t.Fatalf("partial still exists after quarantine: %v", err)
	}
	invalid, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	want := phase7ExpectedPrefix(before, event, len(payload))
	if !bytes.Equal(invalid, want) {
		t.Fatalf("quarantined content mismatch: got %d bytes, want %d", len(invalid), len(want))
	}
	invalidInfo, err := os.Lstat(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, invalidInfo) {
		t.Fatal("quarantine replaced the staging inode")
	}
}

func TestPhase7RollbackSeekFailureQuarantinesTruncatedStaging(t *testing.T) {
	writer, partialPath, before, beforeInfo := phase7Writer(t, "phase7-seek-failure", "oaw_00000000000000000000000000000054")
	invalidPath := filepath.Join(filepath.Dir(partialPath), phase7InvalidPath(writer))
	seekErr := errors.New("injected rollback seek failure")
	seekCalls := 0
	writer.ops.seek = func(file *os.File, offset int64, whence int) (int64, error) {
		seekCalls++
		if seekCalls == 2 {
			return 0, seekErr
		}
		return file.Seek(offset, whence)
	}
	writer.ops.write = phase7PartialWriteNoFatal(nil)

	if err := writer.append(phase7SkippedEvent("oaw_00000000000000000000000000000054", "phase7-seek-failure", 1), false); err == nil {
		t.Fatal("rollback seek failure was not surfaced")
	}
	if _, err := os.Lstat(partialPath); !os.IsNotExist(err) {
		t.Fatalf("partial still exists after quarantine: %v", err)
	}
	invalid, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(invalid, before) {
		t.Fatalf("quarantine content changed after successful truncate: got %d bytes, want %d", len(invalid), len(before))
	}
	invalidInfo, err := os.Lstat(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, invalidInfo) {
		t.Fatal("quarantine replaced the staging inode")
	}
}

func TestPhase7PreExistingInvalidIsPreservedWithoutReplacement(t *testing.T) {
	writer, partialPath, before, beforeInfo := phase7Writer(t, "phase7-existing-invalid", "oaw_00000000000000000000000000000055")
	invalidPath := filepath.Join(filepath.Dir(partialPath), phase7InvalidPath(writer))
	preserved := []byte("pre-existing invalid content")
	if err := os.WriteFile(invalidPath, preserved, 0600); err != nil {
		t.Fatal(err)
	}
	preservedInfo, err := os.Lstat(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	event := phase7SkippedEvent("oaw_00000000000000000000000000000055", "phase7-existing-invalid", 1)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	writer.ops.write = phase7PartialWriteNoFatal(nil)
	writer.ops.truncate = func(*os.File, int64) error { return errors.New("injected truncate failure") }

	if err := writer.append(event, false); err == nil {
		t.Fatal("pre-existing invalid collision was not surfaced")
	}
	gotPreserved, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPreserved, preserved) {
		t.Fatalf("pre-existing invalid content changed: %q", gotPreserved)
	}
	gotInfo, err := os.Lstat(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(preservedInfo, gotInfo) {
		t.Fatal("pre-existing invalid inode was replaced")
	}
	partial, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	wantPartial := phase7ExpectedPrefix(before, event, len(payload))
	if !bytes.Equal(partial, wantPartial) {
		t.Fatalf("staging content changed unexpectedly: got %d bytes, want %d", len(partial), len(wantPartial))
	}
	partialInfo, err := os.Lstat(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, partialInfo) {
		t.Fatal("staging inode changed")
	}
}

func TestPhase7QuarantineRenameFailureLeavesStagingIntact(t *testing.T) {
	writer, partialPath, before, beforeInfo := phase7Writer(t, "phase7-rename-failure", "oaw_00000000000000000000000000000056")
	invalidPath := filepath.Join(filepath.Dir(partialPath), phase7InvalidPath(writer))
	event := phase7SkippedEvent("oaw_00000000000000000000000000000056", "phase7-rename-failure", 1)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	writer.ops.write = phase7PartialWriteNoFatal(nil)
	writer.ops.truncate = func(*os.File, int64) error { return errors.New("injected truncate failure") }
	renameErr := errors.New("injected quarantine rename failure")
	writer.ops.renameNoReplace = func(int, string, int, string) error { return renameErr }

	if err := writer.append(event, false); err == nil {
		t.Fatal("quarantine rename failure was not surfaced")
	}
	partial, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	want := phase7ExpectedPrefix(before, event, len(payload))
	if !bytes.Equal(partial, want) {
		t.Fatalf("staging content changed after quarantine rename failure: got %d bytes, want %d", len(partial), len(want))
	}
	partialInfo, err := os.Lstat(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, partialInfo) {
		t.Fatal("staging inode changed after quarantine rename failure")
	}
	if _, err := os.Lstat(invalidPath); !os.IsNotExist(err) {
		t.Fatalf("invalid quarantine unexpectedly exists: %v", err)
	}
}

func TestPhase7QuarantineAtomicRenamePreservesStagingInode(t *testing.T) {
	writer, partialPath, before, beforeInfo := phase7Writer(t, "phase7-atomic-rename", "oaw_00000000000000000000000000000057")
	invalidPath := filepath.Join(filepath.Dir(partialPath), phase7InvalidPath(writer))
	event := phase7SkippedEvent("oaw_00000000000000000000000000000057", "phase7-atomic-rename", 1)
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	writer.ops.write = phase7PartialWriteNoFatal(nil)
	writer.ops.truncate = func(*os.File, int64) error { return errors.New("injected truncate failure") }

	if err := writer.append(event, false); err == nil {
		t.Fatal("rollback failure was not surfaced")
	}
	if _, err := os.Lstat(partialPath); !os.IsNotExist(err) {
		t.Fatalf("partial remained after atomic quarantine rename: %v", err)
	}
	invalid, err := os.ReadFile(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	want := phase7ExpectedPrefix(before, event, len(payload))
	if !bytes.Equal(invalid, want) {
		t.Fatalf("atomic quarantine content differs: got=%d want=%d", len(invalid), len(want))
	}
	invalidInfo, err := os.Lstat(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, invalidInfo) {
		t.Fatal("atomic quarantine changed the staging inode")
	}
}
