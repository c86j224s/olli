package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func readLogMatrixRunID(n int) string {
	return fmt.Sprintf("oaw_%032x", n)
}

func readLogMatrixFinalPath(engine *Engine, runID string) string {
	return filepath.Join(engine.root, "sessions", "workflows", runID+".jsonl")
}

func readLogMatrixWriteFinal(t *testing.T, engine *Engine, runID string, content []byte) {
	t.Helper()
	finalPath := readLogMatrixFinalPath(engine, runID)
	file, err := os.OpenFile(finalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(filepath.Dir(finalPath), markerName(runID))
	if err := os.Link(finalPath, markerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(finalPath, 0400); err != nil {
		t.Fatal(err)
	}
}

func readLogMatrixMarshalEvents(t *testing.T, events ...map[string]any) []byte {
	t.Helper()
	var content bytes.Buffer
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		content.Write(data)
		content.WriteByte('\n')
	}
	return content.Bytes()
}

func readLogMatrixValidEvents(runID, workflow string) (map[string]any, map[string]any) {
	started := eventBase(runID, workflow, "workflow_started", "started", 0)
	completed := eventBase(runID, workflow, "workflow_completed", "succeeded", 1)
	completed["outcome_category"] = "success"
	return started, completed
}

func readLogMatrixStepStarted(runID, workflow string, sequence int64) map[string]any {
	step := eventBase(runID, workflow, "step_started", "started", sequence)
	step["step_id"] = "call"
	step["attempt"] = 1
	return step
}

func TestReadLogAcceptsOnlyCanonicalCompleteFixture(t *testing.T) {
	const workflow = "readlog-canonical"
	runID := readLogMatrixRunID(701)
	engine := setupEngine(t, workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	started, completed := readLogMatrixValidEvents(runID, workflow)
	readLogMatrixWriteFinal(t, engine, runID, readLogMatrixMarshalEvents(t, started, completed))

	events, err := engine.ReadLog(runID)
	if err != nil {
		t.Fatalf("canonical complete log rejected: %v", err)
	}
	if len(events) != 2 || events[0]["event"] != "workflow_started" || events[1]["event"] != "workflow_completed" {
		t.Fatalf("unexpected canonical events: %#v", events)
	}
}

func TestReadLogRejectsMalformedFinalizedFixtures(t *testing.T) {
	const workflow = "readlog-matrix"
	tests := []struct {
		name    string
		content func(t *testing.T, runID string) []byte
	}{
		{
			name:    "empty",
			content: func(_ *testing.T, _ string) []byte { return nil },
		},
		{
			name: "missing-newline",
			content: func(t *testing.T, runID string) []byte {
				started, completed := readLogMatrixValidEvents(runID, workflow)
				return bytes.TrimSuffix(readLogMatrixMarshalEvents(t, started, completed), []byte{'\n'})
			},
		},
		{
			name: "blank-interior-line",
			content: func(t *testing.T, runID string) []byte {
				started, completed := readLogMatrixValidEvents(runID, workflow)
				return append(readLogMatrixMarshalEvents(t, started), append([]byte{'\n'}, readLogMatrixMarshalEvents(t, completed)...)...)
			},
		},
		{
			name: "line-over-64-kib",
			content: func(_ *testing.T, _ string) []byte {
				return append(bytes.Repeat([]byte{'x'}, maxLineBytes), '\n')
			},
		},
		{
			name: "total-over-8-mib",
			content: func(_ *testing.T, _ string) []byte {
				return bytes.Repeat([]byte{'x'}, maxLogBytes+1)
			},
		},
		{
			name: "duplicate-json-key",
			content: func(_ *testing.T, runID string) []byte {
				return []byte(fmt.Sprintf(`{"event_schema_version":"0.1","event":"workflow_started","event":"workflow_started","timestamp":"2025-01-01T00:00:00Z","sequence":0,"run_id":%q,"workflow":%q,"status":"started"}
`, runID, workflow))
			},
		},
		{
			name: "invalid-schema",
			content: func(t *testing.T, runID string) []byte {
				started := eventBase(runID, workflow, "workflow_started", "not-started", 0)
				return readLogMatrixMarshalEvents(t, started)
			},
		},
		{
			name: "sequence-gap",
			content: func(t *testing.T, runID string) []byte {
				started := eventBase(runID, workflow, "workflow_started", "started", 0)
				completed := eventBase(runID, workflow, "workflow_completed", "succeeded", 2)
				completed["outcome_category"] = "success"
				return readLogMatrixMarshalEvents(t, started, completed)
			},
		},
		{
			name: "sequence-duplicate",
			content: func(t *testing.T, runID string) []byte {
				started := eventBase(runID, workflow, "workflow_started", "started", 0)
				completed := eventBase(runID, workflow, "workflow_completed", "succeeded", 0)
				completed["outcome_category"] = "success"
				return readLogMatrixMarshalEvents(t, started, completed)
			},
		},
		{
			name: "sequence-regression",
			content: func(t *testing.T, runID string) []byte {
				started := eventBase(runID, workflow, "workflow_started", "started", 0)
				step := readLogMatrixStepStarted(runID, workflow, 1)
				completed := eventBase(runID, workflow, "workflow_completed", "succeeded", 0)
				completed["outcome_category"] = "success"
				return readLogMatrixMarshalEvents(t, started, step, completed)
			},
		},
		{
			name: "first-event-not-started",
			content: func(t *testing.T, runID string) []byte {
				completed := eventBase(runID, workflow, "workflow_completed", "succeeded", 0)
				completed["outcome_category"] = "success"
				return readLogMatrixMarshalEvents(t, completed)
			},
		},
		{
			name: "second-start-followed-by-valid-terminal",
			content: func(t *testing.T, runID string) []byte {
				started := eventBase(runID, workflow, "workflow_started", "started", 0)
				secondStarted := eventBase(runID, workflow, "workflow_started", "started", 1)
				completed := eventBase(runID, workflow, "workflow_completed", "succeeded", 2)
				completed["outcome_category"] = "success"
				return readLogMatrixMarshalEvents(t, started, secondStarted, completed)
			},
		},
		{
			name: "run-mismatch",
			content: func(t *testing.T, runID string) []byte {
				started, completed := readLogMatrixValidEvents(runID, workflow)
				completed["run_id"] = readLogMatrixRunID(799)
				return readLogMatrixMarshalEvents(t, started, completed)
			},
		},
		{
			name: "workflow-mismatch",
			content: func(t *testing.T, runID string) []byte {
				started, completed := readLogMatrixValidEvents(runID, workflow)
				completed["workflow"] = "other-workflow"
				return readLogMatrixMarshalEvents(t, started, completed)
			},
		},
		{
			name: "no-terminal",
			content: func(t *testing.T, runID string) []byte {
				started := eventBase(runID, workflow, "workflow_started", "started", 0)
				return readLogMatrixMarshalEvents(t, started)
			},
		},
		{
			name: "multiple-terminals",
			content: func(t *testing.T, runID string) []byte {
				started, completed := readLogMatrixValidEvents(runID, workflow)
				cancelled := eventBase(runID, workflow, "workflow_cancelled", "cancelled", 2)
				cancelled["outcome_category"] = "cancelled"
				cancelled["summary"] = "cancelled"
				return readLogMatrixMarshalEvents(t, started, completed, cancelled)
			},
		},
		{
			name: "terminal-not-last",
			content: func(t *testing.T, runID string) []byte {
				started, completed := readLogMatrixValidEvents(runID, workflow)
				step := readLogMatrixStepStarted(runID, workflow, 2)
				return readLogMatrixMarshalEvents(t, started, completed, step)
			},
		},
		{
			name:    "truncated-json",
			content: func(_ *testing.T, _ string) []byte { return []byte(`{"event":` + "\n") },
		},
	}

	for index, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runID := readLogMatrixRunID(710 + index)
			engine := setupEngine(t, workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
			readLogMatrixWriteFinal(t, engine, runID, tc.content(t, runID))
			if _, err := engine.ReadLog(runID); err == nil {
				t.Fatalf("malformed finalized fixture accepted: %s", tc.name)
			}
		})
	}
}

func TestReadLogRejectsInvalidRunID(t *testing.T) {
	engine := setupEngine(t, workflowDoc("readlog-runid", map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	for _, runID := range []string{"", "oaw_not-a-run-id", "oaw_0000000000000000000000000000000", "../escape"} {
		if _, err := engine.ReadLog(runID); err == nil {
			t.Fatalf("invalid run ID accepted: %q", runID)
		}
	}
}

func TestReadLogRejectsFIFOWithoutBlocking(t *testing.T) {
	workflow := "readlog-fifo"
	runID := readLogMatrixRunID(749)
	engine := setupEngine(t, workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	path := readLogMatrixFinalPath(engine, runID)
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := engine.ReadLog(runID)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("FIFO accepted as finalized log")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadLog blocked on FIFO")
	}
}

func TestReadLogRejectsNonregularFinalizedFixtures(t *testing.T) {
	for index, kind := range []string{"symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			workflow := "readlog-nonregular"
			runID := readLogMatrixRunID(750 + index)
			engine := setupEngine(t, workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
			path := readLogMatrixFinalPath(engine, runID)
			switch kind {
			case "symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := engine.ReadLog(runID); err == nil {
				t.Fatalf("nonregular finalized %s accepted", kind)
			}
		})
	}
}

func TestReadLogDoesNotAddressPartialOrInvalidFiles(t *testing.T) {
	for index, suffix := range []string{".jsonl.partial", ".jsonl.invalid"} {
		t.Run(suffix, func(t *testing.T) {
			workflow := "readlog-hidden"
			runID := readLogMatrixRunID(760 + index)
			engine := setupEngine(t, workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
			path := filepath.Join(engine.root, "sessions", "workflows", "."+runID+suffix)
			if err := os.WriteFile(path, []byte("not-a-log\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.ReadLog(runID); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("hidden %s file was addressable through ReadLog: %v", suffix, err)
			}
		})
	}
}

func TestReadLogRejectsInvalidUTF8InOtherwiseValidTerminalSummary(t *testing.T) {
	const workflow = "readlog-invalid-utf8"
	runID := readLogMatrixRunID(780)
	engine := setupEngine(t, workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	started, completed := readLogMatrixValidEvents(runID, workflow)
	completed["status"] = "failed"
	completed["outcome_category"] = "validation"
	completed["summary"] = "workflow failed"
	terminal, err := json.Marshal(completed)
	if err != nil {
		t.Fatal(err)
	}
	invalidSummary := append([]byte("workflow "), 0xff)
	invalidSummary = append(invalidSummary, []byte("failed")...)
	terminal = bytes.Replace(terminal, []byte("workflow failed"), invalidSummary, 1)
	content := append(readLogMatrixMarshalEvents(t, started), append(terminal, '\n')...)
	readLogMatrixWriteFinal(t, engine, runID, content)
	if _, err := engine.ReadLog(runID); err == nil {
		t.Fatal("accepted invalid UTF-8 in terminal summary")
	}
}
