package subagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

func TestCoderSmallModelSmoke(t *testing.T) {
	if os.Getenv("OLLI_MODEL_SMOKE") != "1" {
		t.Skip("set OLLI_MODEL_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	path := filepath.Join(root, "feature.go")
	if err := os.WriteFile(path, []byte("package demo\n\nconst Value = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	roles := smokeModelRoles(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	report, err := roles.Code(ctx, CodeTask{Goal: "change Value to 2", Step: PlanStep{ID: "step-1", Objective: "change Value to 2", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"Value equals 2"}}, Attempt: 1})
	if err != nil {
		t.Fatalf("coder smoke failed: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package demo\n\nconst Value = 2\n" || len(report.ChangedFiles) != 1 || report.ChangedFiles[0] != "feature.go" {
		t.Fatalf("unexpected coder result: %q %#v", content, report)
	}
}

func TestTesterSmallModelSmoke(t *testing.T) {
	if os.Getenv("OLLI_MODEL_SMOKE") != "1" {
		t.Skip("set OLLI_MODEL_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	roles := smokeModelRoles(t, root)
	reg := roles.runner.newRoleRegistry()
	reg.RegisterContext(ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "execute_action", Description: "Execute one approved test action", Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{"action": {Type: "string", Enum: []string{"go_test"}}, "target": {Type: "string"}}, Required: []string{"action"}}}}, tools.ToolMetadata{}, func(context.Context, map[string]interface{}) (string, error) {
		return "ok   example/demo", nil
	})
	roles.testerRegistry = func() *tools.Registry { return reg }
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	parsed, err := roles.runTester(ctx, "smoke-tester", []string{"go_test ./..."})
	if err != nil {
		t.Fatalf("tester smoke failed: %v", err)
	}
	if !parsed.Passed || len(parsed.Commands) != 1 || parsed.Commands[0].ExitCode != 0 {
		t.Fatalf("tester smoke returned no passing execution evidence: %#v", parsed)
	}
}

func TestReviewerSmallModelSmoke(t *testing.T) {
	if os.Getenv("OLLI_MODEL_SMOKE") != "1" {
		t.Skip("set OLLI_MODEL_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "feature.go"), []byte("package demo\n\nfunc Divide(a, b int) int { return a / b }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	roles := smokeModelRoles(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	plan := &DevelopmentPlan{Goal: "safe division", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "implement division", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"zero divisor handled"}}}, FinalVerification: []string{"go_test ./..."}}
	review, err := roles.Review(ctx, ReviewTask{Dimension: ReviewDimensionLogic, Context: ReviewContext{Plan: plan, CodeReports: []CodeReport{{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"division added"}}}}})
	if err != nil {
		t.Fatalf("reviewer smoke failed: %v", err)
	}
	if len(review.Findings) == 0 || review.Findings[0].File != "feature.go" || review.Findings[0].ID == "" || review.Findings[0].RequiredOutcome == "" {
		t.Fatalf("reviewer missed divide-by-zero defect: %#v", review)
	}
}

func smokeModelRoles(t *testing.T, root string) *ModelTeamRoles {
	t.Helper()
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.NumCtx = 8192
	model := os.Getenv("OLLI_SMOKE_MODEL")
	if model == "" {
		model = "gemma4:e4b"
	}
	runner := NewRunner(ollama.NewClient("http://127.0.0.1:11434"), model, cfg, root, "", SubagentCallbacks{}, root)
	roles, err := NewModelTeamRolesWithModels(runner, TeamModels{Planner: model, Coder: model, Tester: model, Reviewer: model})
	if err != nil {
		t.Fatal(err)
	}
	return roles
}
