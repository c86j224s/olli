package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c86j224s/olli/config"
)

func TestConfigWhitelistManagement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "config_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfgPath := filepath.Join(tempDir, "config.json")
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if !cfg.IsWhitelisted("calculator") {
		t.Errorf("expected calculator to be whitelisted by default")
	}
	if cfg.DefaultMode != "ask" {
		t.Fatalf("expected default mode ask, got %s", cfg.DefaultMode)
	}
	for _, highRisk := range []string{"execute_action", "cd", "change_directory"} {
		if cfg.IsWhitelisted(highRisk) {
			t.Fatalf("expected high-risk tool %s not to be whitelisted by default", highRisk)
		}
	}

	// Add new tool to whitelist
	if err := cfg.AddWhitelist("execute_action"); err != nil {
		t.Fatalf("failed to add whitelist: %v", err)
	}

	if !cfg.IsWhitelisted("execute_action") {
		t.Errorf("expected execute_action to be whitelisted after add")
	}

	// Reload config from file to test persistence
	reloaded, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if !reloaded.IsWhitelisted("execute_action") {
		t.Errorf("expected reloaded config to retain execute_action in whitelist")
	}

	// Remove tool from whitelist
	if err := cfg.RemoveWhitelist("execute_action"); err != nil {
		t.Fatalf("failed to remove whitelist: %v", err)
	}
	if cfg.IsWhitelisted("execute_action") {
		t.Errorf("expected execute_action to be removed from whitelist")
	}
}

func TestConfigInvalidDefaultModeFallsBackToAsk(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"default_mode":"invalid","num_ctx":4096,"whitelist_tools":["calculator"]}`), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.DefaultMode != "ask" {
		t.Fatalf("expected invalid default mode to fall back to ask, got %s", cfg.DefaultMode)
	}
}
