package subagent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/subagent"
)

func TestSubagentRunner(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "subagent_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	client := ollama.NewClient("http://localhost:11434")
	cfg, _ := config.LoadConfig(filepath.Join(tempDir, "config.json"))

	runner := subagent.NewRunner(client, "qwen3.5:0.8b", cfg, tempDir)

	// Verify runner creation
	if runner == nil {
		t.Fatalf("expected non-nil runner")
	}
}
