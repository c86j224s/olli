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
	tempDir := t.TempDir()

	var err error

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
	_, execErr := reg.Execute("execute_action", map[string]interface{}{"action": "go_test", "target": "pkg; rm -rf ."})
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

func TestAgentHonorsDisabledMediaTools(t *testing.T) {
	tempDir := t.TempDir()
	cfg, err := config.LoadConfig(filepath.Join(tempDir, "config.json"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	cfg.ImageGeneration.Enabled = false
	cfg.ImageInspection.Enabled = false
	cfg.AudioGeneration.Enabled = false

	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", nil, cfg)
	reg := ag.GetRegistry()

	for _, toolName := range []string{"image_generate", "inspect_image", "audio_generate"} {
		if _, ok := reg.GetDefinition(toolName); ok {
			t.Fatalf("expected %s to be disabled by config", toolName)
		}
	}
	if _, ok := reg.GetDefinition("calculator"); !ok {
		t.Fatal("expected non-media tools to remain registered")
	}
}

func TestAgentRegistersGoalTools(t *testing.T) {
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

	client := ollama.NewClient("http://localhost:11434")
	ag := agent.New(client, "qwen3.5:0.8b", "Test prompt", nil, cfg)
	reg := ag.GetRegistry()

	if _, err := reg.Execute("set_active_goal", map[string]interface{}{"goal_description": "ship artifact checks"}); err != nil {
		t.Fatalf("expected set_active_goal to be registered: %v", err)
	}
	if ag.GetGoal() != "ship artifact checks" {
		t.Fatalf("expected active goal to be set, got %q", ag.GetGoal())
	}
	if _, err := reg.Execute("complete_goal", map[string]interface{}{}); err != nil {
		t.Fatalf("expected complete_goal with empty args to be registered and accepted: %v", err)
	}
	if ag.GetGoal() != "" {
		t.Fatalf("expected active goal to be cleared, got %q", ag.GetGoal())
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
	if err := cfg.AddWhitelist("image_generate"); err != nil {
		t.Fatalf("failed to whitelist image generation: %v", err)
	}
	if err := cfg.AddWhitelist("audio_generate"); err != nil {
		t.Fatalf("failed to whitelist audio generation: %v", err)
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
	if !ag.ShouldRequirePermission("image_generate") {
		t.Fatal("expected image_generate to require permission even in auto mode")
	}
	if !ag.ShouldRequirePermission("audio_generate") {
		t.Fatal("expected audio_generate to require permission even in auto mode")
	}
	if ag.ShouldRequirePermission("calculator") {
		t.Fatal("expected calculator to remain auto-allowed in auto mode")
	}
	if ag.ShouldRequirePermission("inspect_image") {
		t.Fatal("expected read-only inspect_image to remain auto-allowed in auto mode")
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
