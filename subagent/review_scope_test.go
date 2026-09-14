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
		{ID: "step-1", Objective: "first", AllowedFiles: []string{"first.go"}, Acceptance: []string{"first complete"}},
		{ID: "step-2", Objective: "second", AllowedFiles: []string{"second.go"}, Acceptance: []string{"second complete"}},
	}
	finding := Finding{ID: "finding-1", Severity: "high", File: "second.go", Line: 1, Summary: "broken", FailureScenario: "test fails", RequiredOutcome: "test passes", Verification: []string{"go_test ./..."}}
	base := &scriptedTeamRoles{
		plan: plan,
		codeReports: []*CodeReport{
			{StepID: "step-1", ChangedFiles: []string{"first.go"}, Completed: []string{"first complete"}},
			{StepID: "step-2", ChangedFiles: []string{"second.go"}, Completed: []string{"second draft"}},
			{StepID: "step-review-fix-1", ChangedFiles: []string{"second.go"}, Completed: []string{"second fixed"}, AddressedFindings: []AddressedFinding{{ID: "finding-1", Status: "addressed", Evidence: "fixed"}}},
		},
		testReports: []*TestReport{
			{Passed: true, Commands: []CommandResult{passingCommand("go_test ./...")}},
		},
		reviews: []*ReviewReport{
			{Findings: []Finding{finding}},
			{FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "resolved", Evidence: "passes"}}},
		},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	roles := &scopeCapturingRoles{scriptedTeamRoles: base}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	if report := runner.Run(context.Background(), "implement both steps"); report.Status != "SUCCESS" {
		t.Fatalf("team failed: %#v", report)
	}
	if len(roles.scopes) != 5 {
		t.Fatalf("unexpected review scope count: %#v", roles.scopes)
	}
	for _, scope := range roles.scopes {
		if len(scope.files) != 1 || scope.files[0] != "second.go" {
			t.Fatalf("review received scope %#v, want second.go", scope.files)
		}
	}
	wantDimensions := []ReviewDimension{
		ReviewDimensionRequirements, ReviewDimensionLogic, ReviewDimensionSafety, ReviewDimensionTests,
		ReviewDimensionRequirements,
	}
	for index, want := range wantDimensions {
		if roles.scopes[index].dimension != want {
			t.Fatalf("review %d used dimension %q, want %q", index, roles.scopes[index].dimension, want)
		}
	}
}
