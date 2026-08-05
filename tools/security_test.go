package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathSafeTildeExpand(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home dir: %v", err)
	}

	currentDir, _ := os.Getwd()

	// Test 1: ~/llm-pg path
	target := "~/llm-pg"
	safePath, err := IsPathSafe(target, currentDir)
	if err != nil {
		t.Fatalf("IsPathSafe failed for '%s': %v", target, err)
	}

	expected := filepath.Join(homeDir, "llm-pg")
	if safePath != expected {
		t.Errorf("expected '%s', got '%s'", expected, safePath)
	}

	// Test 2: ExecuteCommand with cd ~/llm-pg
	res, err := ExecuteCommand("cd ~/llm-pg && pwd", currentDir)
	if err != nil {
		t.Fatalf("ExecuteCommand failed: %v", err)
	}
	t.Logf("ExecuteCommand output: %s", res)
}
