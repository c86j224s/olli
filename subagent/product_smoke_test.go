package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c86j224s/olli/tools"
)

func TestDevelopmentTeamProductSmoke(t *testing.T) {
	if os.Getenv("OLLI_PRODUCT_SMOKE") != "1" {
		t.Skip("set OLLI_PRODUCT_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example/counter\n\ngo 1.26\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}

	roles := smokeModelRoles(t, root)
	roles.runner.cfg.NumCtx = 16384
	roles.runner.heartbeatInterval = 10 * time.Second
	roles.runner.budgetOverrides = map[SubagentType]roleBudget{
		TypePlanner:  {NumPredict: 1536, Timeout: defaultPlannerTimeout},
		TypeCoder:    {NumPredict: defaultCoderNumPredict, Timeout: defaultCoderTimeout},
		TypeTester:   {NumPredict: defaultTesterNumPredict, Timeout: defaultTesterTimeout},
		TypeReviewer: {NumPredict: defaultReviewerNumPredict, Timeout: defaultReviewerTimeout},
	}
	roles.runner.callbacks.OnModelHeartbeat = func(role string, elapsed time.Duration) {
		t.Logf("%s model response still generating after %s", role, elapsed)
	}
	coderModel := os.Getenv("OLLI_PRODUCT_CODER_MODEL")
	if coderModel == "" {
		coderModel = "qwen3.8:27b"
	}
	reviewerModel := os.Getenv("OLLI_PRODUCT_REVIEWER_MODEL")
	if reviewerModel == "" {
		reviewerModel = "gemma4:12b"
	}
	testCoderModel := os.Getenv("OLLI_PRODUCT_TEST_CODER_MODEL")
	if testCoderModel == "" {
		testCoderModel = reviewerModel
	}
	roles.models.Coder = coderModel
	roles.models.TestCoder = testCoderModel
	roles.models.Reviewer = reviewerModel
	roles.models.RequirementReviewer = reviewerModel
	roles.models.LogicReviewer = reviewerModel
	roles.models.SafetyReviewer = reviewerModel
	roles.models.TestReviewer = reviewerModel
	roles.models.Cassandra = roles.models.Planner
	roles.models.DetailPlanner = roles.models.Planner
	thinking := false
	roles.models.CoderThinking = &thinking
	roles.models.ReviewerThinking = &thinking
	roles.runner.callbacks.OnToolCall = func(role, toolName string, _ map[string]interface{}, _ string, execErr error) {
		t.Logf("%s tool=%s err=%v", role, toolName, execErr)
	}
	roles.testerRegistry = func() *tools.Registry { return newTetrisSmokeTesterRegistry(root) }

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Minute)
	defer cancel()
	team, err := NewDevelopmentTeamRunner(roles, 2)
	if err != nil {
		t.Fatal(err)
	}
	result := team.Run(ctx, `Build a tiny deterministic integer counter in Go using only the standard library.
Requirements:
- Use exactly three ordered architecture work packages in package main with unique ownership: (1) counter.go defines Counter and NewCounter(initial), Value(), Increment(), Decrement(), and Reset(); (2) main.go remains a small entry point that constructs and exercises Counter without reading input; (3) counter_test.go contains concise table-driven tests for initialization, increment/decrement, and reset.
- Counter methods use pointer receivers and mutate one integer value. No concurrency, networking, filesystem access, shell commands, external packages, flags, or interactive input.
- Every work package must leave package main parseable. Detail-plan each approved package into exactly one complete milestone.
- Use final_verification ["go_test ./...", "go_vet ./..."].`)
	if result.Status != "SUCCESS" {
		t.Fatalf("development team failed: %s; planning=%+v; plan=%+v; graph=%+v\n\ngenerated sources:\n%s", result.Failure, result.Planning, result.Plan, result.Graph, readTetrisSources(root))
	}
	if err := inspectCounterWorkspace(root); err != nil {
		t.Fatalf("generated counter failed inspection: %v\n\n%s", err, readTetrisSources(root))
	}
}

func inspectCounterWorkspace(root string) error {
	if err := parseAndTypeCheckWorkspace(root); err != nil {
		return err
	}
	sources := readTetrisSources(root)
	for _, marker := range []string{"type Counter", "func NewCounter", "Increment", "Decrement", "Reset", "func main(", "func Test"} {
		if !strings.Contains(sources, marker) {
			return fmt.Errorf("missing required implementation marker %q", marker)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "counter.go")); err != nil {
		return fmt.Errorf("counter.go missing: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, "counter_test.go")); err != nil {
		return fmt.Errorf("counter_test.go missing: %w", err)
	}
	return nil
}
