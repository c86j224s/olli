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

	// Add new tool to whitelist
	if err := cfg.AddWhitelist("run_terminal_command"); err != nil {
		t.Fatalf("failed to add whitelist: %v", err)
	}

	if !cfg.IsWhitelisted("run_terminal_command") {
		t.Errorf("expected run_terminal_command to be whitelisted after add")
	}

	// Reload config from file to test persistence
	reloaded, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if !reloaded.IsWhitelisted("run_terminal_command") {
		t.Errorf("expected reloaded config to retain run_terminal_command in whitelist")
	}

	// Remove tool from whitelist
	if err := cfg.RemoveWhitelist("run_terminal_command"); err != nil {
		t.Fatalf("failed to remove whitelist: %v", err)
	}
	if cfg.IsWhitelisted("run_terminal_command") {
		t.Errorf("expected run_terminal_command to be removed from whitelist")
	}
}
