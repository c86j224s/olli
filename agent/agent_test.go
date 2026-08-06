package agent_test

import (
	"os"
	"path/filepath"
	"strings"
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

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir to temp workspace: %v", err)
	}
	defer os.Chdir(originalWD)

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

	// Use the temporary workspace as the target so a future regression cannot
	// accidentally delete the user's home directory while proving the guard path.
	dangerousCommands := []string{
		`rm -rf "$PWD"`,
		`echo ok | rm -rf "$PWD"`,
	}

	for _, command := range dangerousCommands {
		_, execErr := reg.Execute("run_terminal_command", map[string]interface{}{"command": command})
		if execErr == nil {
			t.Fatalf("expected dangerous command %q to be BLOCKED by security guard, but it executed", command)
		}
		if !strings.Contains(execErr.Error(), "SECURITY BLOCK") {
			t.Fatalf("expected security block for %q, got: %v", command, execErr)
		}
	}
}

func TestAgentHonorsConfigDefaultMode(t *testing.T) {
	tempDir := t.TempDir()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer os.Chdir(originalWD)

	cfg, err := config.LoadConfig(filepath.Join(tempDir, "config.json"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	cfg.DefaultMode = "ask"

	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", nil, cfg)
	if ag.GetToolMode() != agent.ModeAsk {
		t.Fatalf("expected ask mode from config, got %s", ag.GetToolMode())
	}
}

func TestSensitiveToolsRequirePermissionEvenWhenWhitelisted(t *testing.T) {
	tempDir := t.TempDir()
	cfg, err := config.LoadConfig(filepath.Join(tempDir, "config.json"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if err := cfg.AddWhitelist("run_terminal_command"); err != nil {
		t.Fatalf("failed to whitelist terminal: %v", err)
	}

	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", nil, cfg)
	ag.SetToolMode(agent.ModeAuto)

	if !ag.ShouldRequirePermission("run_terminal_command") {
		t.Fatal("expected terminal tool to require permission even in auto mode")
	}
	if !ag.ShouldRequirePermission("delegate_coder") {
		t.Fatal("expected mutation-capable delegate to require permission")
	}
	if ag.ShouldRequirePermission("calculator") {
		t.Fatal("expected calculator to remain auto-allowed in auto mode")
	}
}

func TestLoadSessionIgnoresWorkspaceOutsideInitialRoot(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer os.Chdir(originalWD)

	cfg, err := config.LoadConfig(filepath.Join(tempDir, "config.json"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	sessMgr, err := session.NewManager(filepath.Join(tempDir, "sessions"))
	if err != nil {
		t.Fatalf("failed to create session manager: %v", err)
	}
	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", sessMgr, cfg)

	if _, err := sessMgr.CreateSession("unsafe_restore", "qwen3.5:0.8b"); err != nil {
		t.Fatalf("failed to create unsafe session: %v", err)
	}
	if err := sessMgr.AppendEvent(ollama.Message{
		Role:    "system",
		Content: "📌 [Workspace Directory Updated]: " + outsideDir,
	}); err != nil {
		t.Fatalf("failed to append event: %v", err)
	}

	if _, err := ag.LoadSession("unsafe_restore"); err != nil {
		t.Fatalf("failed to load session: %v", err)
	}
	if ag.GetCurrentDir() == outsideDir {
		t.Fatalf("unsafe restored directory was applied: %s", outsideDir)
	}
	if !strings.Contains(ag.GetSummary(), "Ignored unsafe restored working directory") {
		t.Fatalf("expected unsafe restore summary, got: %s", ag.GetSummary())
	}
}
