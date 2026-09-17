package subagent

import (
	"context"
	"testing"
)

type scopeCapture struct {
	dimension ReviewDimension
	stepID    string
	files     []string
}

type scopeCapturingRoles struct {
	*scriptedTeamRoles
	scopes []scopeCapture
}

func (s *scopeCapturingRoles) Review(_ context.Context, task ReviewTask) (*ReviewReport, error) {
	s.scopes = append(s.scopes, scopeCapture{dimension: task.Dimension, stepID: task.Context.StepID, files: append([]string(nil), task.Context.ReviewScope...)})
	return s.scriptedTeamRoles.Review(context.Background(), task)
}

func TestDevelopmentTeamReviewsAssembledImplementationAndNarrowsFixScope(t *testing.T) {
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
		testReports: []*TestReport{{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}}},
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
	for _, scope := range roles.scopes[:4] {
		if scope.stepID != "step-final-review" || len(scope.files) != 2 || scope.files[0] != "first.go" || scope.files[1] != "second.go" {
			t.Fatalf("initial semantic review did not cover assembled implementation: %#v", scope)
		}
	}
	fixScope := roles.scopes[4]
	if fixScope.stepID != "step-review-fix-1" || len(fixScope.files) != 1 || fixScope.files[0] != "second.go" {
		t.Fatalf("fix review did not narrow to finding files: %#v", fixScope)
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
