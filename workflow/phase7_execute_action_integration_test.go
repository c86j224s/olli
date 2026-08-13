package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/c86j224s/olli/tools"
)

type phase7ExecuteActionAdapter struct {
	registry *tools.Registry
}

func (a phase7ExecuteActionAdapter) ListTools() []ToolDefinition {
	definitions := a.registry.GetDefinitions()
	out := make([]ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		properties := make(map[string]any, len(definition.Function.Parameters.Properties))
		for name, property := range definition.Function.Parameters.Properties {
			entry := map[string]any{"type": property.Type}
			if property.Description != "" {
				entry["description"] = property.Description
			}
			if len(property.Enum) != 0 {
				enum := make([]any, len(property.Enum))
				for index, value := range property.Enum {
					enum[index] = value
				}
				entry["enum"] = enum
			}
			properties[name] = entry
		}
		required := make([]any, len(definition.Function.Parameters.Required))
		for index, name := range definition.Function.Parameters.Required {
			required[index] = name
		}
		metadata, _ := a.registry.GetMetadata(definition.Function.Name)
		out = append(out, ToolDefinition{
			Name: definition.Function.Name,
			Schema: map[string]any{
				"$schema":              "https://json-schema.org/draft/2020-12/schema",
				"type":                 "object",
				"additionalProperties": false,
				"properties":           properties,
				"required":             required,
			},
			RetrySafe:        metadata.RetrySafe,
			WorkflowCallable: metadata.WorkflowCallable,
		})
	}
	return out
}

func (a phase7ExecuteActionAdapter) ExecuteContext(ctx context.Context, name string, args map[string]any) (string, error) {
	return a.registry.ExecuteContext(ctx, name, args)
}

func TestPhase7ExecuteActionCallerDeadlineKillsSandboxedChild(t *testing.T) {
	phase7ExecuteActionCallerDeadlineKillsSandboxedChild(t)
}

func phase7ExecuteActionCallerDeadlineKillsSandboxedChild(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec integration is macOS-only")
	}
	if info, err := os.Stat("/usr/bin/sandbox-exec"); err != nil || info.IsDir() {
		t.Skip("/usr/bin/sandbox-exec is unavailable")
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	phase7WriteExecuteActionSchemas(t, root)
	phase7WriteExecuteActionHelper(t, root)

	startedPath := filepath.Join(root, "phase7-started.marker")
	pidPath := filepath.Join(root, "phase7-child.pid")
	completedPath := filepath.Join(root, "phase7-completed.marker")
	t.Setenv("OAW_PHASE7_TRIGGER", "0")
	t.Setenv("OAW_PHASE7_ROOT", root)
	t.Setenv("OAW_PHASE7_STARTED", startedPath)
	t.Setenv("OAW_PHASE7_PID", pidPath)
	t.Setenv("OAW_PHASE7_COMPLETED", completedPath)

	registry := tools.NewRegistry()
	registry.SetWorkspace(root)
	registry.SetWorkspaceRoot(root)
	adapter := phase7ExecuteActionAdapter{registry: registry}
	engine, err := NewEngine(root, adapter, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	// Warm up compilation with the helper's trigger disabled. This call is the
	// same approved execute_action path used by the workflow below.
	if _, err := registry.ExecuteContext(context.Background(), "execute_action", map[string]interface{}{
		"action": "go_test",
		"target": "./...",
	}); err != nil {
		t.Fatalf("warm-up go_test failed: %v", err)
	}
	if _, err := os.Stat(startedPath); !os.IsNotExist(err) {
		t.Fatalf("warm-up unexpectedly started helper: %v", err)
	}

	// Change package contents after warm-up so go test cannot reuse its cached
	// successful result when the trigger is enabled.
	if err := os.WriteFile(filepath.Join(root, "phase7_cache_bust.go"), []byte("package phase7helper\n\nconst phase7CacheBust = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	phase7WriteExecuteActionWorkflow(t, root)
	t.Setenv("OAW_PHASE7_TRIGGER", "1")

	var authorizations atomic.Int32
	caller, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- engine.Run(caller, "phase7-execute-action-deadline", nil, func(ctx context.Context, tool string, _ map[string]any, _ string, _ int) bool {
			if ctx == nil || tool != "execute_action" {
				return false
			}
			authorizations.Add(1)
			return true
		})
	}()

	pid, started := phase7WaitForChildStart(startedPath, pidPath, 3*time.Second)
	if !started {
		t.Fatalf("sandboxed helper did not start within bound")
	}
	defer phase7KillIfAlive(pid)

	var result RunResult
	select {
	case result = <-runDone:
	case <-time.After(8 * time.Second):
		phase7KillIfAlive(pid)
		select {
		case result = <-runDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Engine.Run did not return after caller deadline and child cleanup")
		}
	}

	if result.Status != "timed_out" {
		t.Fatalf("caller deadline was not mapped to timed_out: %#v", result)
	}
	if got := authorizations.Load(); got != 1 {
		t.Fatalf("execute_action was retried or not authorized exactly once: %d", got)
	}

	events, err := engine.ReadLog(result.RunID)
	if err != nil {
		t.Fatalf("ReadLog(%q): %v", result.RunID, err)
	}
	phase7AssertDeadlineEvents(t, events)

	if _, err := os.Stat(completedPath); !os.IsNotExist(err) {
		t.Fatalf("helper reached completion after caller deadline: %v", err)
	}
	if !phase7WaitForChildExit(pid, 3*time.Second) {
		t.Fatalf("sandboxed helper child pid %d remained alive after Engine.Run returned", pid)
	}
}

func phase7WriteExecuteActionSchemas(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"oaw.schema.json", "oaw-event.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "workflows", "agent", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func phase7WriteExecuteActionHelper(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module phase7helper\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `package phase7helper

import (
	"os"
	"strconv"
	"testing"
	"time"
)

func TestPhase7Helper(t *testing.T) {
	if os.Getenv("OAW_PHASE7_TRIGGER") != "1" {
		return
	}
	root := os.Getenv("OAW_PHASE7_ROOT")
	started := os.Getenv("OAW_PHASE7_STARTED")
	pidFile := os.Getenv("OAW_PHASE7_PID")
	completed := os.Getenv("OAW_PHASE7_COMPLETED")
	if root == "" || started == "" || pidFile == "" || completed == "" {
		t.Fatal("helper marker configuration is missing")
	}
	if err := os.WriteFile(started, []byte("started\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
	if err := os.WriteFile(completed, []byte("completed\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(root, "phase7_helper_test.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func phase7WriteExecuteActionWorkflow(t *testing.T, root string) {
	t.Helper()
	doc := map[string]any{
		"oaw_version": "0.1",
		"name":        "phase7-execute-action-deadline",
		"description": "caller deadline execute_action integration",
		"inputs":      map[string]any{},
		"limits": map[string]any{
			"max_steps":             2,
			"max_tool_calls":        1,
			"max_attempts_per_step": 1,
			"timeout_seconds":       30,
		},
		"steps": []any{
			map[string]any{
				"id":        "run",
				"kind":      "tool",
				"tool":      "execute_action",
				"arguments": map[string]any{"action": "go_test", "target": "./..."},
			},
			map[string]any{"id": "done", "kind": "return"},
		},
		"outputs":    map[string]any{"text": "{{steps.run.result.text}}"},
		"on_failure": "report",
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", doc["name"].(string)+".oaw.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func phase7WaitForChildStart(startedPath, pidPath string, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(startedPath); err == nil {
			data, readErr := os.ReadFile(pidPath)
			if readErr == nil {
				pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
				if parseErr == nil && pid > 0 {
					return pid, true
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return 0, false
}

func phase7ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func phase7WaitForChildExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !phase7ProcessAlive(pid) {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return !phase7ProcessAlive(pid)
}

func phase7KillIfAlive(pid int) {
	if phase7ProcessAlive(pid) {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Signal(syscall.SIGKILL)
		}
	}
}

func phase7AssertDeadlineEvents(t *testing.T, events []map[string]any) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("ReadLog returned no events")
	}
	var toolTimedOut, workflowTimedOut bool
	for _, event := range events {
		if event["event"] == "tool_completed" {
			if event["status"] != "timed_out" || event["outcome_category"] != "timeout" {
				t.Fatalf("unexpected execute_action terminal event: %#v", event)
			}
			toolTimedOut = true
		}
		if event["event"] == "workflow_completed" {
			if event["status"] != "timed_out" || event["outcome_category"] != "timeout" {
				t.Fatalf("unexpected workflow terminal event: %#v", event)
			}
			workflowTimedOut = true
		}
	}
	if !toolTimedOut || !workflowTimedOut {
		t.Fatalf("deadline events missing: %#v", events)
	}
}
