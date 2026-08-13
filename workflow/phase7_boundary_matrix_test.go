package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boundaryLineSize(t *testing.T, event map[string]any) int {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return len(data) + 1
}

func boundarySizedEvent(t *testing.T, event map[string]any, target int) map[string]any {
	t.Helper()
	const timestampPrefix = "2026-01-01T00:00:00."
	const timestampSuffix = "Z"

	base := "2026-01-01T00:00:00Z"
	event["timestamp"] = base
	baseSize := boundaryLineSize(t, event)
	if target <= baseSize+1 {
		t.Fatalf("target line size %d is too small for event base size %d", target, baseSize)
	}
	fractionLen := target - baseSize - 1
	for {
		event["timestamp"] = timestampPrefix + strings.Repeat("0", fractionLen) + timestampSuffix
		actual := boundaryLineSize(t, event)
		if actual == target {
			return event
		}
		delta := target - actual
		fractionLen += delta
		if fractionLen < 1 {
			t.Fatalf("could not size event line to %d bytes; actual size %d", target, actual)
		}
	}
}

func boundaryRunEvent(runID, workflow, kind, status string, sequence int64) map[string]any {
	return eventBase(runID, workflow, kind, status, sequence)
}

func boundaryWriter(t *testing.T, name, runID string) (*Engine, *eventWriter) {
	t.Helper()
	engine := setupEngine(t, workflowDoc(name, map[string]any{"value": "{{inputs.value}}"}), &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return `{"value":"ok"}`, nil
	}})
	writer, err := engine.openEventWriter(runID, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.abort)
	return engine, writer
}

func boundaryAppendSized(t *testing.T, writer *eventWriter, event map[string]any, terminal bool, target int) {
	t.Helper()
	boundarySizedEvent(t, event, target)
	if err := writer.append(event, terminal); err != nil {
		t.Fatal(err)
	}
}

func boundaryFillNonterminal(t *testing.T, writer *eventWriter, runID, workflow string, count int) {
	t.Helper()
	for sequence := 0; sequence < count; sequence++ {
		if sequence == 0 {
			event := boundaryRunEvent(runID, workflow, "workflow_started", "started", int64(sequence))
			boundaryAppendSized(t, writer, event, false, maxLineBytes)
			continue
		}
		event := boundaryRunEvent(runID, workflow, "step_skipped", "skipped", int64(sequence))
		event["step_id"] = "tail"
		event["attempt"] = 1
		boundaryAppendSized(t, writer, event, false, maxLineBytes)
	}
}

func TestBoundaryCompleteJSONLLineAtMaxBytesAccepted(t *testing.T) {
	const (
		workflow = "boundary-complete-line"
		runID    = "oaw_00000000000000000000000000000051"
	)
	engine, writer := boundaryWriter(t, workflow, runID)
	event := boundaryRunEvent(runID, workflow, "workflow_started", "started", 0)
	boundaryAppendSized(t, writer, event, false, maxLineBytes)

	path := filepath.Join(engine.root, "sessions", "workflows", writer.partial)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != maxLineBytes || data[len(data)-1] != '\n' {
		t.Fatalf("complete JSONL line size is %d, want %d", len(data), maxLineBytes)
	}
	if writer.bytes != int64(maxLineBytes) || writer.sequence != 1 || !writer.started || writer.terminal {
		t.Fatalf("writer state after max line: bytes=%d sequence=%d started=%t terminal=%t", writer.bytes, writer.sequence, writer.started, writer.terminal)
	}
}

func TestBoundaryCompleteJSONLLineOverMaxBytesPreservesState(t *testing.T) {
	const (
		workflow = "boundary-over-line"
		runID    = "oaw_00000000000000000000000000000052"
	)
	engine, writer := boundaryWriter(t, workflow, runID)
	path := filepath.Join(engine.root, "sessions", "workflows", writer.partial)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, beforeSequence := writer.bytes, writer.sequence
	beforeStarted, beforeTerminal := writer.started, writer.terminal

	event := boundaryRunEvent(runID, workflow, "workflow_started", "started", 0)
	boundarySizedEvent(t, event, maxLineBytes+1)
	if got := boundaryLineSize(t, event); got != maxLineBytes+1 {
		t.Fatalf("constructed line size %d, want %d", got, maxLineBytes+1)
	}
	if err := writer.append(event, false); err == nil {
		t.Fatal("65,537-byte JSONL line was accepted")
	} else if !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("wrong oversized-line error: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || writer.bytes != beforeBytes || writer.sequence != beforeSequence || writer.started != beforeStarted || writer.terminal != beforeTerminal {
		t.Fatalf("oversized-line rejection changed writer state or file: bytes=%d sequence=%d started=%t terminal=%t", writer.bytes, writer.sequence, writer.started, writer.terminal)
	}
}

func TestBoundaryNonterminalExactReserveAccepted(t *testing.T) {
	const (
		workflow = "boundary-reserve-exact"
		runID    = "oaw_00000000000000000000000000000053"
	)
	_, writer := boundaryWriter(t, workflow, runID)
	limit := int64(maxLogBytes - terminalReserve)
	if limit%int64(maxLineBytes) != 0 {
		t.Fatalf("test requires integral max-line fill count: limit=%d line=%d", limit, maxLineBytes)
	}
	boundaryFillNonterminal(t, writer, runID, workflow, int(limit/int64(maxLineBytes)))
	if writer.bytes != limit {
		t.Fatalf("nonterminal reserve boundary is %d, want %d", writer.bytes, limit)
	}
}

func TestBoundaryNonterminalOneByteOverflowPreservesState(t *testing.T) {
	const (
		workflow = "boundary-reserve-overflow"
		runID    = "oaw_00000000000000000000000000000054"
	)
	engine, writer := boundaryWriter(t, workflow, runID)
	limit := int64(maxLogBytes - terminalReserve)
	fullLines := int(limit/int64(maxLineBytes)) - 1
	boundaryFillNonterminal(t, writer, runID, workflow, fullLines)

	sequence := writer.sequence
	small := boundaryRunEvent(runID, workflow, "step_skipped", "skipped", sequence)
	small["step_id"] = "tail"
	small["attempt"] = 1
	if err := writer.append(small, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(engine.root, "sessions", "workflows", writer.partial))
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, beforeSequence := writer.bytes, writer.sequence
	remaining := limit - writer.bytes
	if remaining <= 1 || remaining >= int64(maxLineBytes) {
		t.Fatalf("unexpected remaining reserve %d", remaining)
	}

	overflow := boundaryRunEvent(runID, workflow, "step_skipped", "skipped", writer.sequence)
	overflow["step_id"] = "tail"
	overflow["attempt"] = 1
	boundarySizedEvent(t, overflow, int(remaining+1))
	if got := boundaryLineSize(t, overflow); got != int(remaining+1) {
		t.Fatalf("constructed overflow line size %d, want %d", got, remaining+1)
	}
	if err := writer.append(overflow, false); !errors.Is(err, errLogLimit) {
		t.Fatalf("one-byte reserve overflow returned %v, want %v", err, errLogLimit)
	}
	after, err := os.ReadFile(filepath.Join(engine.root, "sessions", "workflows", writer.partial))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || writer.bytes != beforeBytes || writer.sequence != beforeSequence || writer.terminal {
		t.Fatalf("reserve overflow changed writer state or file: bytes=%d sequence=%d terminal=%t", writer.bytes, writer.sequence, writer.terminal)
	}
}

func TestBoundaryTerminalExactlyFillsRemainingBudgetAndFinalizes(t *testing.T) {
	const (
		workflow = "boundary-terminal-budget"
		runID    = "oaw_00000000000000000000000000000055"
	)
	engine, writer := boundaryWriter(t, workflow, runID)
	limit := int64(maxLogBytes - terminalReserve)
	boundaryFillNonterminal(t, writer, runID, workflow, int(limit/int64(maxLineBytes)))
	if writer.bytes != limit {
		t.Fatalf("nonterminal prefix is %d, want %d", writer.bytes, limit)
	}

	terminal := boundaryRunEvent(runID, workflow, "workflow_completed", "succeeded", writer.sequence)
	terminal["outcome_category"] = "success"
	boundaryAppendSized(t, writer, terminal, true, maxLogBytes-int(limit))
	if writer.bytes != int64(maxLogBytes) || !writer.terminal {
		t.Fatalf("terminal boundary state: bytes=%d terminal=%t", writer.bytes, writer.terminal)
	}
	logPath, err := writer.finalize(runID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(engine.root, logPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != maxLogBytes {
		t.Fatalf("finalized log size %d, want %d", len(data), maxLogBytes)
	}
	if _, err := engine.ReadLog(runID); err != nil {
		t.Fatalf("exact-budget terminal log was not readable: %v", err)
	}
}

func TestLogLimitEnginePathFinalizesReservedTerminal(t *testing.T) {
	const workflow = "engine-log-limit-path"
	doc := workflowDoc(workflow, map[string]any{"value": "{{inputs.value}}"})
	doc["on_failure"] = "report"
	engine := setupEngine(t, doc, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return `{"value":"ok"}`, nil
	}})
	engine.eventWriterSetup = func(writer *eventWriter) {
		write := writer.ops.write
		first := true
		writer.ops.write = func(file *os.File, payload []byte) (int, error) {
			written, err := write(file, payload)
			if first && err == nil && written == len(payload) {
				first = false
				writer.bytes = int64(maxLogBytes - terminalReserve)
			}
			return written, err
		}
	}
	result := engine.Run(context.Background(), workflow, map[string]any{"value": "x"}, allow)
	if result.Status != "failed" || result.Failure == nil || result.Failure.Code != "log_limit" {
		t.Fatalf("Engine log-limit result was not finalized: %#v", result)
	}
	if result.LogPath == "" {
		t.Fatalf("Engine log-limit result has no finalized log: %#v", result)
	}
	events, err := engine.ReadLog(result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0]["event"] != "workflow_started" {
		t.Fatalf("log-limit path emitted a nonterminal overflow event: %#v", events)
	}
	terminal := events[len(events)-1]
	if terminal["event"] != "workflow_completed" || terminal["status"] != "failed" || terminal["outcome_category"] != "log_limit" {
		t.Fatalf("wrong finalized log-limit terminal event: %#v", terminal)
	}
	data, err := os.ReadFile(filepath.Join(engine.root, result.LogPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= maxLogBytes {
		t.Fatalf("log-limit path wrote beyond the physical log budget: %d", len(data))
	}
}
