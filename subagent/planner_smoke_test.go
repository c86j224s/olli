package subagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
)

func TestPlannerSmallModelSmoke(t *testing.T) {
	if os.Getenv("OLLI_MODEL_SMOKE") != "1" {
		t.Skip("set OLLI_MODEL_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "calculator.go"), []byte("package demo\n\nfunc Add(a, b int) int { return a + b }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "calculator_test.go"), []byte("package demo\n\n// Tests will be added later.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.NumCtx = 8192
	model := os.Getenv("OLLI_SMOKE_MODEL")
	if model == "" {
		model = "qwen3.5:0.8b"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	runner := NewRunner(ollama.NewClient("http://127.0.0.1:11434"), model, cfg, root, "", SubagentCallbacks{}, root)
	report, plan, err := runner.RunPlannerWithContext(ctx, "Add a Sub(a, b int) int function to calculator.go and tests to calculator_test.go. Read exactly those two files, then return the plan immediately. Plan only; do not edit files.")
	if err != nil {
		if report != nil {
			t.Fatalf("planner smoke failed for %s: %v; summary=%q; log=%s", model, err, report.Summary, report.JSONLFile)
		}
		t.Fatalf("planner smoke failed for %s: %v", model, err)
	}
	if report.ToolCallsRun == 0 || len(plan.Steps) == 0 {
		t.Fatalf("planner smoke returned no evidence or steps: %#v %#v", report, plan)
	}
	for _, step := range plan.Steps {
		for _, path := range step.AllowedFiles {
			if path != "calculator.go" && path != "calculator_test.go" {
				t.Fatalf("planner invented unexpected file %q: %#v", path, plan)
			}
		}
	}
}
