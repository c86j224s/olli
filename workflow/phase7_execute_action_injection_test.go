package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c86j224s/olli/tools"
)

func TestPhase7ExecuteActionInjectionMatrix(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0o700); err != nil {
		t.Fatal(err)
	}
	phase7WriteExecuteActionSchemas(t, root)

	registry := toolsRegistryForPhase7(root)
	adapter := phase7ExecuteActionAdapter{registry: registry}
	engine, err := NewEngine(root, adapter, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	outside := t.TempDir()
	base := filepath.Join(outside, "phase7-marker")
	malicious := []struct {
		name       string
		action     string
		targetFunc func(string) string
	}{
		{name: "semicolon", action: "cat", targetFunc: func(secret string) string { return base + secret + ";touch" }},
		{name: "pipe", action: "cat", targetFunc: func(secret string) string { return base + secret + "|touch" }},
		{name: "command-substitution", action: "cat", targetFunc: func(secret string) string { return base + secret + "$(touch)" }},
		{name: "backticks", action: "cat", targetFunc: func(secret string) string { return base + secret + "`touch`" }},
		{name: "newline", action: "cat", targetFunc: func(secret string) string { return base + secret + "\ntouch" }},
		{name: "parent-escape", action: "cat", targetFunc: func(secret string) string {
			return filepath.Join("..", filepath.Base(outside), "phase7-marker-"+secret)
		}},
		{name: "absolute-outside", action: "cat", targetFunc: func(secret string) string { return base + "-" + secret }},
		{name: "unsupported-action", action: "unsupported-phase7", targetFunc: func(secret string) string { return "phase7-target-" + secret }},
		{name: "raw-shell-command", action: "touch " + base, targetFunc: func(secret string) string { return "phase7-target-" + secret }},
	}

	for _, tc := range malicious {
		t.Run(tc.name, func(t *testing.T) {
			secret := "phase7-secret-" + tc.name
			name := "phase7-inject-" + tc.name
			phase7WriteInjectionWorkflow(t, root, name, tc.action, tc.targetFunc(secret))

			result := engine.Run(context.Background(), name, nil, phase7InjectionAllow)
			if result.Status == "succeeded" {
				t.Fatalf("malicious input unexpectedly succeeded: %#v", result)
			}
			if result.Failure == nil {
				t.Fatalf("non-success must have structured failure: %#v", result)
			}
			if len(result.Outputs) != 0 {
				t.Fatalf("failed run exposed outputs: %#v", result.Outputs)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("malicious input created outside side effects: %v", entries)
			}
			events, err := engine.ReadLog(result.RunID)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(events)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("finalized log leaked injected secret %q", secret)
			}
		})
	}

	benignFile := filepath.Join(root, "phase7-read-only.txt")
	const benignContent = "phase7-benign-content"
	if err := os.WriteFile(benignFile, []byte(benignContent+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	phase7WriteInjectionWorkflow(t, root, "phase7-benign-read", "cat", filepath.Base(benignFile))
	result := engine.Run(context.Background(), "phase7-benign-read", nil, phase7InjectionAllow)
	if result.Status != "succeeded" || result.Failure != nil {
		t.Fatalf("permitted read-only action failed: %#v", result)
	}
	if got := fmt.Sprint(result.Outputs["content"]); !strings.Contains(got, benignContent) {
		t.Fatalf("read-only result missing content: %q", got)
	}
	if _, err := engine.ReadLog(result.RunID); err != nil {
		t.Fatal(err)
	}
}

func toolsRegistryForPhase7(root string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.SetWorkspace(root)
	registry.SetWorkspaceRoot(root)
	return registry
}

func phase7InjectionAllow(context.Context, string, map[string]any, string, int) bool { return true }

func phase7WriteInjectionWorkflow(t *testing.T, root, name, action, target string) {
	t.Helper()
	doc := map[string]any{
		"oaw_version": "0.1",
		"name":        name,
		"description": "execute_action injection integration",
		"inputs":      map[string]any{},
		"limits": map[string]any{
			"max_steps": 2, "max_tool_calls": 1, "max_attempts_per_step": 1, "timeout_seconds": 10,
		},
		"steps": []any{
			map[string]any{"id": "call", "kind": "tool", "tool": "execute_action", "arguments": map[string]any{"action": action, "target": target}},
			map[string]any{"id": "done", "kind": "return"},
		},
		"outputs":    map[string]any{"content": "{{steps.call.result.text}}"},
		"on_failure": "report",
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name+".oaw.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
