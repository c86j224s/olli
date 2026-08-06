package subagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
)

func TestSubagentRunnerAllTypes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "subagent_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	client := ollama.NewClient("http://localhost:11434")
	cfg, _ := config.LoadConfig(filepath.Join(tempDir, "config.json"))

	runner := NewRunner(client, "qwen3.5:0.8b", cfg, tempDir, "", SubagentCallbacks{})

	if runner == nil {
		t.Fatalf("expected non-nil runner")
	}
}

func TestSubagentRoleRegistryStartsEmpty(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	client := ollama.NewClient("http://localhost:11434")
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	runner := NewRunner(client, "qwen3.5:0.8b", cfg, workspace, "", SubagentCallbacks{}, root)
	reg := runner.newRoleRegistry()
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("failed to canonicalize root: %v", err)
	}
	if runner.GetWorkspaceRoot() != wantRoot {
		t.Fatalf("expected runner root %s, got %s", wantRoot, runner.GetWorkspaceRoot())
	}
	if _, err := reg.Execute("run_terminal_command", map[string]interface{}{"command": "pwd"}); err == nil {
		t.Fatal("expected base role registry not to inherit terminal tool")
	}
}
