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

	// Whitelist execute_action explicitly
	cfg.AddWhitelist("execute_action")

	sessMgr, _ := session.NewManager(tempDir)
	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", sessMgr, cfg)

	// Set mode to Auto (all approved)
	ag.SetToolMode(agent.ModeAuto)

	reg := ag.GetRegistry()

	// Unapproved action or malicious target must be blocked
	unapprovedActions := []string{
		"unapproved_action",
		"rm",
	}

	for _, action := range unapprovedActions {
		_, execErr := reg.Execute("execute_action", map[string]interface{}{"action": action})
		if execErr == nil {
			t.Fatalf("expected unapproved action %q to be BLOCKED, but it executed", action)
		}
	}

	// Shell metacharacters in target must be blocked
	_, execErr := reg.Execute("execute_action", map[string]interface{}{"action": "go_test", "target": "pkg; rm -rf ." })
	if execErr == nil || !strings.Contains(execErr.Error(), "security block") {
		t.Fatalf("expected security block for shell metacharacters in target, got: %v", execErr)
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
	if err := cfg.AddWhitelist("execute_action"); err != nil {
		t.Fatalf("failed to whitelist action execution: %v", err)
	}

	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", nil, cfg)
	ag.SetToolMode(agent.ModeAuto)

	if !ag.ShouldRequirePermission("execute_action") {
		t.Fatal("expected execute_action to require permission even in auto mode")
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
