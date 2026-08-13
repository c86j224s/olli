package config

import (
	"path/filepath"
	"testing"
)

func TestAddWhitelistRollsBackOnSaveFailure(t *testing.T) {
	root := t.TempDir()
	cfg, err := LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.filePath = filepath.Join(root, "missing", "config.json")
	if err := cfg.AddWhitelist("transient_tool"); err == nil {
		t.Fatal("expected whitelist save to fail")
	}
	if cfg.IsWhitelisted("transient_tool") {
		t.Fatal("failed whitelist persistence left in-memory approval behind")
	}
}
