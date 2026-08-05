package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c86j224s/olli/agent"
	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
)

func TestApprovedToolStillBlocksDangerousCommands(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agent_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfgPath := filepath.Join(tempDir, "config.json")
	cfg, _ := config.LoadConfig(cfgPath)

	// Whitelist run_terminal_command explicitly
	cfg.AddWhitelist("run_terminal_command")

	sessMgr, _ := session.NewManager(tempDir)
	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", sessMgr, cfg)

	// Set mode to Auto (all approved)
	ag.SetToolMode(agent.ModeAuto)

	reg := ag.GetRegistry()

	// Execute dangerous command even though tool is whitelisted & auto-approved
	_, execErr := reg.Execute("run_terminal_command", map[string]interface{}{"command": "rm -rf ~"})
	if execErr == nil {
		t.Fatalf("expected dangerous command 'rm -rf ~' to be BLOCKED by security guard, but it executed!")
	}

	_, execErr2 := reg.Execute("run_terminal_command", map[string]interface{}{"command": "rm -rf /"})
	if execErr2 == nil {
		t.Fatalf("expected dangerous command 'rm -rf /' to be BLOCKED by security guard, but it executed!")
	}
}
