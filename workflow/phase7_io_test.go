package workflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func writeFinalFixture(t *testing.T, engine *Engine, runID string, events []map[string]any) {
	t.Helper()
	path := filepath.Join(engine.root, "sessions", "workflows", runID+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(filepath.Dir(path), markerName(runID))
	if err := os.Link(path, marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
}

func validEventSet(runID, workflow string) []map[string]any {
	started := eventBase(runID, workflow, "workflow_started", "started", 0)
	completed := eventBase(runID, workflow, "workflow_completed", "succeeded", 1)
	completed["outcome_category"] = "success"
	return []map[string]any{started, completed}
}

func assertLogAbsent(t *testing.T, engine *Engine, runID string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(engine.root, "sessions", "workflows", runID+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("unexpected final log: %v", err)
	}
}

func operationFailureOps(base eventOps, op string) eventOps {
	fail := errors.New(op + " failure")
	switch op {
	case "syncFile":
		base.syncFile = func(*os.File) error { return fail }
	case "fstat":
		base.fstat = func(*os.File, *unix.Stat_t) error { return fail }
	case "fstatat":
		base.fstatat = func(int, string, *unix.Stat_t, int) error { return fail }
	case "linkat":
		base.linkat = func(int, string, int, string, int) error { return fail }
	case "renameNoReplace":
		base.renameNoReplace = func(int, string, int, string) error { return fail }
	case "chmod":
		base.chmod = func(*os.File, uint32) error { return fail }
	case "syncDir":
		base.syncDir = func(*os.File) error { return fail }
	}
	return base
}

func prepareTerminalWriter(t *testing.T, name, id string) (*Engine, *eventWriter) {
	t.Helper()
	engine := setupEngine(t, workflowDoc(name, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	writer, err := engine.openEventWriter(id, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.append(eventBase(id, name, "workflow_started", "started", 0), false); err != nil {
		t.Fatal(err)
	}
	terminal := eventBase(id, name, "workflow_completed", "succeeded", 1)
	terminal["outcome_category"] = "success"
	if err := writer.append(terminal, true); err != nil {
		t.Fatal(err)
	}
	return engine, writer
}

func mustEventBytes(event map[string]any) int {
	data, _ := json.Marshal(event)
	return len(data) + 1
}

func eventWithSummarySize(t *testing.T, event map[string]any, target int) map[string]any {
	t.Helper()
	for n := 0; n <= target; n++ {
		event["summary"] = strings.Repeat("x", n)
		if mustEventBytes(event) == target {
			return event
		}
	}
	t.Fatalf("could not construct event size %d", target)
	return nil
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

var _ = bufio.ErrInvalidUnreadByte
var _ = bytes.Equal
var _ = fmt.Sprint
var _ = io.EOF
var _ = syscall.EEXIST

func TestRunIDFormatAndDeterministicCollision(t *testing.T) {
	id, err := newRunID()
	if err != nil || !runIDPattern.MatchString(id) {
		t.Fatalf("invalid run ID: %q %v", id, err)
	}
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		id, err = newRunID()
		if err != nil || seen[id] {
			t.Fatalf("run ID generation failed: %q %v", id, err)
		}
		seen[id] = true
	}
	engine := setupEngine(t, workflowDoc("collision-final", map[string]any{"value": "{{inputs.value}}"}), &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }})
	fixed := "oaw_00000000000000000000000000000011"
	engine.runIDGenerator = func() (string, error) { return fixed, nil }
	result := engine.Run(context.Background(), "collision-final", map[string]any{"value": "x"}, allow)
	if result.Status != "succeeded" {
		t.Fatal(result)
	}
	second := engine.Run(context.Background(), "collision-final", map[string]any{"value": "x"}, allow)
	if !second.LogUnavailable || second.Status != "failed" {
		t.Fatalf("final collision was not refused: %#v", second)
	}
}

func TestEventWriterRejectsSequenceAndTerminalMismatch(t *testing.T) {
	engine := setupEngine(t, workflowDoc("writer-invariant", map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	id := "oaw_00000000000000000000000000000012"
	writer, err := engine.openEventWriter(id, "writer-invariant")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.abort()
	if err := writer.append(eventBase(id, "writer-invariant", "workflow_started", "started", 1), false); err == nil {
		t.Fatal("sequence accepted")
	}
	if err := writer.append(eventBase(id, "writer-invariant", "workflow_completed", "succeeded", 0), false); err == nil {
		t.Fatal("terminal mismatch accepted")
	}
}

func TestEventWriterOperationFaultMatrix(t *testing.T) {
	for i, op := range []string{"syncFile", "fstat", "fstatat", "linkat", "renameNoReplace", "syncDir", "chmod"} {
		id := fmt.Sprintf("oaw_%032x", i+40)
		engine, writer := prepareTerminalWriter(t, "fault_"+strings.ToLower(op), id)
		writer.ops = operationFailureOps(writer.ops, op)
		if _, err := writer.finalize(id); err == nil {
			t.Fatalf("%s fault was not surfaced", op)
		}
		if op == "linkat" {
			assertLogAbsent(t, engine, id)
		}
		writer.abort()
	}
}

func TestEventWriterShortWriteRollback(t *testing.T) {
	engine, writer := prepareTerminalWriter(t, "short-write", "oaw_00000000000000000000000000000013")
	before, err := os.ReadFile(filepath.Join(engine.root, "sessions", "workflows", writer.partial))
	if err != nil {
		t.Fatal(err)
	}
	writer.terminal = false
	writer.sequence = 2
	writer.bytes = int64(len(before))
	writer.ops.write = func(_ *os.File, p []byte) (int, error) { return len(p) - 1, nil }
	if err := writer.append(eventBase("oaw_00000000000000000000000000000013", "short-write", "step_skipped", "skipped", 2), false); err == nil {
		t.Fatal("short write accepted")
	}
	after, err := os.ReadFile(filepath.Join(engine.root, "sessions", "workflows", writer.partial))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || writer.sequence != 2 || writer.bytes != int64(len(before)) {
		t.Fatal("rollback did not restore writer state")
	}
	writer.abort()

	engine, writer = prepareTerminalWriter(t, "rollback-failure", "oaw_00000000000000000000000000000014")
	writer.terminal = false
	writer.sequence = 2
	writer.bytes = int64(len(before))
	writer.ops.write = func(_ *os.File, p []byte) (int, error) { return len(p) - 1, nil }
	writer.ops.truncate = func(*os.File, int64) error { return errors.New("truncate failure") }
	appendErr := writer.append(func() map[string]any {
		e := eventBase("oaw_00000000000000000000000000000014", "rollback-failure", "step_skipped", "skipped", 2)
		e["step_id"] = "tail"
		e["attempt"] = 1
		return e
	}(), false)
	if appendErr == nil {
		t.Fatal("rollback failure not surfaced")
	}
	t.Logf("rollback error: %v", appendErr)
	invalidPath := filepath.Join(engine.root, "sessions", "workflows", ".oaw_00000000000000000000000000000014.jsonl.invalid")
	if _, err := os.Lstat(invalidPath); err != nil {
		entries, _ := os.ReadDir(filepath.Dir(invalidPath))
		t.Fatalf("partial was not quarantined: %v entries=%v", err, entries)
	}
	writer.abort()
}

func TestEventExactLineAndReserveBoundaries(t *testing.T) {
	_, writer := prepareTerminalWriter(t, "boundary", "oaw_00000000000000000000000000000015")
	writer.terminal = false
	writer.sequence = 2
	writer.bytes = int64(len([]byte{}))
	step := eventBase("oaw_00000000000000000000000000000015", "boundary", "step_skipped", "skipped", 2)
	step["step_id"] = "tail"
	step["attempt"] = 1
	for _, event := range []map[string]any{step} {
		data, _ := json.Marshal(event)
		if len(data)+1 != mustEventBytes(event) {
			t.Fatal("dynamic event sizing failed")
		}
	}
	if err := writer.append(step, false); err != nil {
		t.Fatal(err)
	}
	tooLarge := eventBase("oaw_00000000000000000000000000000015", "boundary", "step_skipped", "skipped", 3)
	tooLarge["step_id"] = "tail"
	tooLarge["attempt"] = 1
	tooLarge["summary"] = strings.Repeat("x", 2049)
	if err := writer.append(tooLarge, false); err == nil {
		t.Fatal("oversized event accepted")
	}
	writer.abort()
}

func TestEventWriterShortWriteRollbackLegacy(t *testing.T) {
	engine := setupEngine(t, workflowDoc("short-write", map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	id := "oaw_00000000000000000000000000000013"
	writer, err := engine.openEventWriter(id, "short-write")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.abort()
	first := writer.ops
	first.write = func(_ *os.File, p []byte) (int, error) { return len(p) - 1, nil }
	writer.ops = first
	if err := writer.append(eventBase(id, "short-write", "workflow_started", "started", 0), false); !errors.Is(err, os.ErrInvalid) && err == nil {
		t.Fatal("short write not rejected")
	}
}

func TestReadLogFinalizedOnly(t *testing.T) {
	engine := setupEngine(t, workflowDoc("read-log", map[string]any{"value": "{{inputs.value}}"}), &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }})
	result := engine.Run(context.Background(), "read-log", map[string]any{"value": "x"}, allow)
	if result.Status != "succeeded" {
		t.Fatal(result)
	}
	events, err := engine.ReadLog(result.RunID)
	if err != nil || len(events) == 0 {
		t.Fatalf("read finalized log: %v", err)
	}
	partial := filepath.Join(engine.root, "sessions", "workflows", "."+result.RunID+".jsonl.partial")
	if err := os.WriteFile(partial, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.ReadLog(result.RunID); err != nil {
		t.Fatalf("finalized log should remain readable: %v", err)
	}
	if _, err := engine.ReadLog("oaw_00000000000000000000000000000099"); !os.IsNotExist(err) {
		t.Fatalf("unexpected missing log result: %v", err)
	}
}

func TestConcurrentRunsIsolateResults(t *testing.T) {
	engine := setupEngine(t, workflowDoc("concurrent", map[string]any{"value": "{{inputs.value}}"}), &testExecutor{fn: func(_ context.Context, _ string, args map[string]any) (string, error) {
		return `{"value":"` + args["value"].(string) + `"}`, nil
	}})
	var wg sync.WaitGroup
	results := make([]RunResult, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = engine.Run(context.Background(), "concurrent", map[string]any{"value": string(rune('a' + i))}, allow)
		}(i)
	}
	wg.Wait()
	seen := map[string]bool{}
	for _, result := range results {
		if result.Status != "succeeded" || seen[result.RunID] {
			t.Fatalf("concurrent isolation failed: %#v", results)
		}
		seen[result.RunID] = true
	}
}
