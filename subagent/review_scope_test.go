package subagent

import (
	"context"
	"testing"
)

type scopeCapturingRoles struct {
	*scriptedTeamRoles
	scopes [][]string
}

func (s *scopeCapturingRoles) Review(_ context.Context, reviewContext ReviewContext) (*ReviewReport, error) {
	s.scopes = append(s.scopes, append([]string(nil), reviewContext.ReviewScope...))
	return s.scriptedTeamRoles.Review(context.Background(), reviewContext)
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
	want := [][]string{{"first.go"}, {"first.go"}, {"second.go"}}
	if len(roles.scopes) != len(want) {
		t.Fatalf("unexpected review scope count: %#v", roles.scopes)
	}
	for index := range want {
		if len(roles.scopes[index]) != 1 || roles.scopes[index][0] != want[index][0] {
			t.Fatalf("review %d received scope %#v, want %#v", index, roles.scopes[index], want[index])
		}
	}
}
