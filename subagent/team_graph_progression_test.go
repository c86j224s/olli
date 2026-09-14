package subagent

import (
	"context"
	"testing"
)

func TestDevelopmentTeamContinuesAfterFixingEarlyPlanStep(t *testing.T) {
	plan := teamTestPlan()
	plan.Files = []string{"first.go", "second.go"}
	plan.Steps = []PlanStep{
		{ID: "step-1", Objective: "first", AllowedFiles: []string{"first.go"}, Acceptance: []string{"first complete"}, Verification: []string{"go_test ./..."}},
		{ID: "step-2", Objective: "second", AllowedFiles: []string{"second.go"}, Acceptance: []string{"second complete"}},
	}
	finding := Finding{ID: "finding-1", Severity: "high", File: "first.go", Line: 1, Summary: "first broken", FailureScenario: "test fails", RequiredOutcome: "first passes", Verification: []string{"go_test ./..."}}
	roles := &scriptedTeamRoles{
		plan: plan,
		codeReports: []*CodeReport{
			{StepID: "step-1", ChangedFiles: []string{"first.go"}, Completed: []string{"first draft"}},
			{StepID: "step-review-fix-1", ChangedFiles: []string{"first.go"}, Completed: []string{"first fixed"}, AddressedFindings: []AddressedFinding{{ID: "finding-1", Status: "addressed", Evidence: "fixed test"}}},
			{StepID: "step-2", ChangedFiles: []string{"second.go"}, Completed: []string{"second complete"}},
		},
		testReports: []*TestReport{
			{Passed: false, Commands: []CommandResult{{Command: "go_test ./...", ExitCode: 1, Output: "first failed"}}, Summary: "failed"},
			{Passed: true, Commands: []CommandResult{passingCommand("go_test ./...")}},
		},
		reviews: []*ReviewReport{
			{Findings: []Finding{finding}},
			{FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "resolved", Evidence: "test passes"}}, Summary: "resolved"},
			{Summary: "clean"},
		},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement both steps")
	if report.Status != "SUCCESS" || roles.codeCalls != 3 {
		t.Fatalf("later plan step was skipped after fix: %#v calls=%d", report, roles.codeCalls)
	}
}

func TestDevelopmentTeamVerifiesAfterCleanReviewWithoutStepVerification(t *testing.T) {
	plan := teamTestPlan()
	plan.Steps[0].Verification = nil
	roles := &scriptedTeamRoles{
		plan:         plan,
		codeReports:  []*CodeReport{{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"done"}}},
		reviews:      []*ReviewReport{{Summary: "clean"}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "SUCCESS" || roles.codeCalls != 1 {
		t.Fatalf("clean review skipped final verification or recoded the step: %#v calls=%d", report, roles.codeCalls)
	}
}

func TestDevelopmentTeamReviewsEveryPlanStep(t *testing.T) {
	plan := teamTestPlan()
	plan.Files = []string{"first.go", "second.go"}
	plan.Steps = []PlanStep{
		{ID: "step-1", Objective: "first", AllowedFiles: []string{"first.go"}, Acceptance: []string{"first complete"}},
		{ID: "step-2", Objective: "second", AllowedFiles: []string{"second.go"}, Acceptance: []string{"second complete"}},
	}
	roles := &scriptedTeamRoles{
		plan: plan,
		codeReports: []*CodeReport{
			{StepID: "step-1", ChangedFiles: []string{"first.go"}, Completed: []string{"first complete"}},
			{StepID: "step-2", ChangedFiles: []string{"second.go"}, Completed: []string{"second complete"}},
		},
		reviews:      []*ReviewReport{{Summary: "first clean"}, {Summary: "second clean"}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement both steps")
	if report.Status != "SUCCESS" || len(report.Reviews) != 2 || roles.reviewCalls != 8 {
		t.Fatalf("not every step received the full reviewer pool: %#v calls=%d", report, roles.reviewCalls)
	}
}

func TestDevelopmentTeamRejectsCleanReviewForFailedTest(t *testing.T) {
	roles := &scriptedTeamRoles{
		plan:        teamTestPlan(),
		codeReports: []*CodeReport{{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"draft"}}},
		testReports: []*TestReport{{Passed: false, Commands: []CommandResult{{Command: "go_test ./...", ExitCode: 1, Output: "failure"}}, Summary: "failed"}},
		reviews:     []*ReviewReport{{Summary: "no finding"}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "FAILED" || report.Failure == "" {
		t.Fatalf("clean review incorrectly bypassed failed test: %#v", report)
	}
}
