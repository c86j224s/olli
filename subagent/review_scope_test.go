package subagent

import (
	"context"
	"testing"
)

type scopeCapture struct {
	dimension ReviewDimension
	files     []string
}

type scopeCapturingRoles struct {
	*scriptedTeamRoles
	scopes []scopeCapture
}

func (s *scopeCapturingRoles) Review(_ context.Context, task ReviewTask) (*ReviewReport, error) {
	s.scopes = append(s.scopes, scopeCapture{dimension: task.Dimension, files: append([]string(nil), task.Context.ReviewScope...)})
	return s.scriptedTeamRoles.Review(context.Background(), task)
}

func TestDevelopmentTeamReviewsOnlyCurrentFixScope(t *testing.T) {
	plan := teamTestPlan()
	plan.Files = []string{"first.go", "second.go"}
	plan.Steps = []PlanStep{
		{ID: "step-1", Objective: "first", AllowedFiles: []string{"first.go"}, Acceptance: []string{"first complete"}, Verification: []string{"go_test ./..."}},
		{ID: "step-2", Objective: "second", AllowedFiles: []string{"second.go"}, Acceptance: []string{"second complete"}},
	}
	finding := Finding{ID: "finding-1", Severity: "high", File: "first.go", Line: 1, Summary: "broken", FailureScenario: "test fails", RequiredOutcome: "test passes", Verification: []string{"go_test ./..."}}
	base := &scriptedTeamRoles{
		plan: plan,
		codeReports: []*CodeReport{
			{StepID: "step-1", ChangedFiles: []string{"first.go"}, Completed: []string{"first draft"}},
			{StepID: "step-review-fix-1", ChangedFiles: []string{"first.go"}, Completed: []string{"first fixed"}, AddressedFindings: []AddressedFinding{{ID: "finding-1", Status: "addressed", Evidence: "fixed"}}},
			{StepID: "step-2", ChangedFiles: []string{"second.go"}, Completed: []string{"second complete"}},
		},
		testReports: []*TestReport{
			{Passed: false, Commands: []CommandResult{{Command: "go_test ./...", ExitCode: 1, Output: "failed"}}, Summary: "failed"},
			{Passed: true, Commands: []CommandResult{passingCommand("go_test ./...")}},
		},
		reviews: []*ReviewReport{
			{Findings: []Finding{finding}},
			{FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "resolved", Evidence: "passes"}}},
			{Summary: "clean"},
		},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	roles := &scopeCapturingRoles{scriptedTeamRoles: base}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	if report := runner.Run(context.Background(), "implement both steps"); report.Status != "SUCCESS" {
		t.Fatalf("team failed: %#v", report)
	}
	if len(roles.scopes) != 9 {
		t.Fatalf("unexpected review scope count: %#v", roles.scopes)
	}
	for index, scope := range roles.scopes {
		wantFile := "first.go"
		if index >= 5 {
			wantFile = "second.go"
		}
		if len(scope.files) != 1 || scope.files[0] != wantFile {
			t.Fatalf("review %d received scope %#v, want %q", index, scope.files, wantFile)
		}
	}
	wantDimensions := []ReviewDimension{
		ReviewDimensionRequirements, ReviewDimensionLogic, ReviewDimensionSafety, ReviewDimensionTests,
		ReviewDimensionRequirements,
		ReviewDimensionRequirements, ReviewDimensionLogic, ReviewDimensionSafety, ReviewDimensionTests,
	}
	for index, want := range wantDimensions {
		if roles.scopes[index].dimension != want {
			t.Fatalf("review %d used dimension %q, want %q", index, roles.scopes[index].dimension, want)
		}
	}
}
