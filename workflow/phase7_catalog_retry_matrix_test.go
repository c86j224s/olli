package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func phase7CatalogTool(name string, retrySafe, workflowCallable bool) ToolDefinition {
	return ToolDefinition{
		Name:             name,
		RetrySafe:        retrySafe,
		WorkflowCallable: workflowCallable,
		Schema: map[string]any{
			"$schema":              "https://json-schema.org/draft/2020-12/schema",
			"type":                 "object",
			"additionalProperties": false,
			"required":             []any{"value"},
			"properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
	}
}

func phase7CatalogDoc(name, tool string, maxCalls, maxAttempts int, retryWhen ...string) map[string]any {
	when := make([]any, len(retryWhen))
	for i, category := range retryWhen {
		when[i] = category
	}
	return map[string]any{
		"oaw_version": "0.1",
		"name":        name,
		"description": "phase 7 catalog and retry matrix",
		"inputs": map[string]any{
			"value": map[string]any{"type": "string", "required": true},
		},
		"limits": map[string]any{
			"max_steps":             2,
			"max_tool_calls":        maxCalls,
			"max_attempts_per_step": maxAttempts,
			"timeout_seconds":       5,
		},
		"steps": []any{
			map[string]any{
				"id":        "call",
				"kind":      "tool",
				"tool":      tool,
				"arguments": map[string]any{"value": "{{inputs.value}}"},
				"retry": map[string]any{
					"max_attempts": maxAttempts,
					"when":         when,
				},
			},
			map[string]any{"id": "done", "kind": "return"},
		},
		"outputs":    map[string]any{"result": "{{steps.call.result.value}}"},
		"on_failure": "stop",
	}
}

func phase7Root(t *testing.T, doc map[string]any) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	eventSchema, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "workflows", "agent", "oaw-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "oaw-event.schema.json"), eventSchema, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", doc["name"].(string)+".oaw.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func phase7Engine(t *testing.T, doc map[string]any, catalog testCatalog, executor ToolExecutor) *Engine {
	t.Helper()
	root := phase7Root(t, doc)
	e, err := NewEngine(root, catalog, executor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}

func phase7CatalogAllow(context.Context, string, map[string]any, string, int) bool { return true }

func TestPhase7UnregisteredAndNonCallableToolsHaveNoSideEffect(t *testing.T) {
	tests := []struct {
		name    string
		catalog testCatalog
		tool    string
	}{
		{
			name:    "unregistered",
			catalog: testCatalog{phase7CatalogTool("echo", true, true)},
			tool:    "missing",
		},
		{
			name:    "workflow_callable_false",
			catalog: testCatalog{phase7CatalogTool("echo", true, false)},
			tool:    "echo",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ex := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
				return `{"value":"unexpected"}`, nil
			}}
			doc := phase7CatalogDoc("phase7-"+tc.name, tc.tool, 1, 1)
			e := phase7Engine(t, doc, tc.catalog, ex)
			if err := e.Validate(doc["name"].(string)); err == nil {
				t.Fatal("Validate accepted a tool that is not workflow callable")
			}
			r := e.Run(context.Background(), doc["name"].(string), map[string]any{"value": "input"}, phase7CatalogAllow)
			if r.Status != "failed" {
				t.Fatalf("Run status = %q, want failed: %#v", r.Status, r)
			}
			if ex.calls.Load() != 0 {
				t.Fatalf("executor calls = %d, want 0", ex.calls.Load())
			}
		})
	}
}

func TestPhase7DuplicateCatalogRejectedBeforeResourceCreation(t *testing.T) {
	root := t.TempDir()
	ex := &testExecutor{}
	_, err := NewEngine(root, testCatalog{
		phase7CatalogTool("duplicate", true, true),
		phase7CatalogTool("duplicate", false, false),
	}, ex)
	if err == nil {
		t.Fatal("NewEngine accepted duplicate catalog name")
	}
	for _, path := range []string{"sessions", "workflows"} {
		if _, statErr := os.Stat(filepath.Join(root, path)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("resource %q exists after rejected constructor: %v", path, statErr)
		}
	}
}

func TestPhase7RetrySafeFalseUsesOneStaticCallAndOneAttempt(t *testing.T) {
	var attempts []int
	var mu sync.Mutex
	ex := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return "", ToolError{Err: errors.New("temporary")}
	}}
	doc := phase7CatalogDoc("phase7-unsafe-retry", "echo", 1, 3, "tool_error")
	e := phase7Engine(t, doc, testCatalog{phase7CatalogTool("echo", false, true)}, ex)
	if err := e.Validate(doc["name"].(string)); err != nil {
		t.Fatalf("Validate rejected one-call accounting for non-retry-safe tool: %v", err)
	}
	authorize := func(_ context.Context, _ string, _ map[string]any, _ string, attempt int) bool {
		mu.Lock()
		attempts = append(attempts, attempt)
		mu.Unlock()
		return true
	}
	r := e.Run(context.Background(), doc["name"].(string), map[string]any{"value": "input"}, authorize)
	if r.Status != "failed" {
		t.Fatalf("Run status = %q, want failed: %#v", r.Status, r)
	}
	if ex.calls.Load() != 1 {
		t.Fatalf("executor calls = %d, want 1", ex.calls.Load())
	}
	mu.Lock()
	gotAttempts := append([]int(nil), attempts...)
	mu.Unlock()
	if fmt.Sprint(gotAttempts) != "[1]" {
		t.Fatalf("authorization attempts = %v, want [1]", gotAttempts)
	}
}

func TestPhase7RetryMatrixTypedErrorsAndWhen(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		when       string
		wantStatus string
		wantCalls  int32
		wantAuth   []int
	}{
		{
			name:       "tool_error_matching_when",
			err:        ToolError{Err: errors.New("tool failure")},
			when:       "tool_error",
			wantStatus: "succeeded",
			wantCalls:  2,
			wantAuth:   []int{1, 2},
		},
		{
			name:       "tool_error_nonmatching_when",
			err:        ToolError{Err: errors.New("tool failure")},
			when:       "timeout",
			wantStatus: "failed",
			wantCalls:  1,
			wantAuth:   []int{1},
		},
		{
			name:       "handler_timeout_matching_when",
			err:        HandlerTimeout{Err: errors.New("deadline")},
			when:       "timeout",
			wantStatus: "succeeded",
			wantCalls:  2,
			wantAuth:   []int{1, 2},
		},
		{
			name:       "handler_timeout_nonmatching_when",
			err:        HandlerTimeout{Err: errors.New("deadline")},
			when:       "tool_error",
			wantStatus: "failed",
			wantCalls:  1,
			wantAuth:   []int{1},
		},
		{
			name:       "untyped_error_never_retries",
			err:        errors.New("untyped failure"),
			when:       "tool_error",
			wantStatus: "failed",
			wantCalls:  1,
			wantAuth:   []int{1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var attempts []int
			var mu sync.Mutex
			ex := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
				if calls.Add(1) == 1 {
					return "", tc.err
				}
				return `{"value":"ok"}`, nil
			}}
			doc := phase7CatalogDoc("phase7-"+tc.name, "echo", 2, 2, tc.when)
			e := phase7Engine(t, doc, testCatalog{phase7CatalogTool("echo", true, true)}, ex)
			authorize := func(_ context.Context, _ string, _ map[string]any, _ string, attempt int) bool {
				mu.Lock()
				attempts = append(attempts, attempt)
				mu.Unlock()
				return true
			}
			r := e.Run(context.Background(), doc["name"].(string), map[string]any{"value": "input"}, authorize)
			if r.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q: %#v", r.Status, tc.wantStatus, r)
			}
			if calls.Load() != tc.wantCalls {
				t.Fatalf("executor calls = %d, want %d", calls.Load(), tc.wantCalls)
			}
			mu.Lock()
			gotAttempts := append([]int(nil), attempts...)
			mu.Unlock()
			if fmt.Sprint(gotAttempts) != fmt.Sprint(tc.wantAuth) {
				t.Fatalf("authorization attempts = %v, want %v", gotAttempts, tc.wantAuth)
			}
		})
	}
}

func TestPhase7AuthorizerAndHandlerMutationsAreIsolated(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var authorizedValues, handlerValues []string
	ex := &testExecutor{fn: func(_ context.Context, _ string, args map[string]any) (string, error) {
		mu.Lock()
		handlerValues = append(handlerValues, args["value"].(string))
		mu.Unlock()
		args["value"] = "handler-mutated"
		if calls.Add(1) == 1 {
			return "", ToolError{Err: errors.New("retry")}
		}
		return `{"value":"ok"}`, nil
	}}
	doc := phase7CatalogDoc("phase7-mutation-isolation", "echo", 2, 2, "tool_error")
	e := phase7Engine(t, doc, testCatalog{phase7CatalogTool("echo", true, true)}, ex)
	authorize := func(_ context.Context, _ string, args map[string]any, _ string, _ int) bool {
		mu.Lock()
		authorizedValues = append(authorizedValues, args["value"].(string))
		mu.Unlock()
		args["value"] = "authorizer-mutated"
		return true
	}
	r := e.Run(context.Background(), doc["name"].(string), map[string]any{"value": "original"}, authorize)
	if r.Status != "succeeded" || r.Outputs["result"] != "ok" {
		t.Fatalf("unexpected result: %#v", r)
	}
	if calls.Load() != 2 {
		t.Fatalf("executor calls = %d, want 2", calls.Load())
	}
	mu.Lock()
	gotAuthorized := append([]string(nil), authorizedValues...)
	gotHandler := append([]string(nil), handlerValues...)
	mu.Unlock()
	if fmt.Sprint(gotAuthorized) != "[original original]" {
		t.Fatalf("authorizer values = %v, want [original original]", gotAuthorized)
	}
	if fmt.Sprint(gotHandler) != "[original original]" {
		t.Fatalf("handler values = %v, want [original original]", gotHandler)
	}
}

func TestPhase7ConcurrentRunsKeepIndependentStateResultsAndLogs(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	ex := &testExecutor{fn: func(ctx context.Context, _ string, args map[string]any) (string, error) {
		value := args["value"].(string)
		entered <- value
		if calls.Add(1) == 2 {
			releaseOnce.Do(func() { close(release) })
		}
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return `{"value":"` + value + `"}`, nil
	}}
	doc := phase7CatalogDoc("phase7-concurrent", "echo", 2, 1)
	e := phase7Engine(t, doc, testCatalog{phase7CatalogTool("echo", true, true)}, ex)
	type runOutcome struct{ result RunResult }
	results := make(chan runOutcome, 2)
	go func() {
		results <- runOutcome{result: e.Run(context.Background(), doc["name"].(string), map[string]any{"value": "first"}, phase7CatalogAllow)}
	}()
	go func() {
		results <- runOutcome{result: e.Run(context.Background(), doc["name"].(string), map[string]any{"value": "second"}, phase7CatalogAllow)}
	}()
	seenInputs := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case value := <-entered:
			seenInputs[value] = true
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent runs did not interleave at tool execution")
		}
	}
	if len(seenInputs) != 2 || !seenInputs["first"] || !seenInputs["second"] {
		t.Fatalf("executor saw inputs %v, want first and second", seenInputs)
	}

	got := make([]RunResult, 0, 2)
	for i := 0; i < 2; i++ {
		select {
		case outcome := <-results:
			got = append(got, outcome.result)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent run did not finish")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("executor calls = %d, want 2", calls.Load())
	}
	ids := map[string]bool{}
	logs := map[string]bool{}
	for _, r := range got {
		if r.Status != "succeeded" {
			t.Fatalf("status = %q, want succeeded: %#v", r.Status, r)
		}
		value, ok := r.Outputs["result"].(string)
		if !ok || (value != "first" && value != "second") {
			t.Fatalf("unexpected output %#v", r.Outputs)
		}
		if r.RunID == "" || ids[r.RunID] {
			t.Fatalf("run IDs are not independent: %q", r.RunID)
		}
		ids[r.RunID] = true
		if r.LogPath == "" || logs[r.LogPath] {
			t.Fatalf("log paths are not independent: %q", r.LogPath)
		}
		logs[r.LogPath] = true
		b, err := os.ReadFile(filepath.Join(e.Root(), r.LogPath))
		if err != nil {
			t.Fatalf("read log %q: %v", r.LogPath, err)
		}
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		if len(lines) < 1 {
			t.Fatalf("log %q is empty", r.LogPath)
		}
		for _, line := range lines {
			var event map[string]any
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatalf("invalid event in %q: %v", r.LogPath, err)
			}
			if event["run_id"] != r.RunID {
				t.Fatalf("event run_id %v does not match result %q", event["run_id"], r.RunID)
			}
		}
	}
	if len(ids) != 2 || len(logs) != 2 {
		t.Fatalf("got %d run IDs and %d log paths, want two each", len(ids), len(logs))
	}
}
