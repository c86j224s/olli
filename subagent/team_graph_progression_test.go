package subagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestExistingPlanContextFilesIncludesExistingReadOnlyFiles(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"main.go", "prior.go"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte("package main\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	plan := &DevelopmentPlan{Files: []string{"main.go", "prior.go", "current.go", "future.go"}}
	got := existingPlanContextFiles(plan, []string{"current.go"}, root)
	if fmt.Sprint(got) != fmt.Sprint([]string{"main.go", "prior.go"}) {
		t.Fatalf("unexpected existing plan context files: %v", got)
	}
}

func TestDevelopmentTeamRetriesOneFailedCoderAttempt(t *testing.T) {
	base := &scriptedTeamRoles{
		plan:         teamTestPlan(),
		codeReports:  []*CodeReport{{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"done"}}},
		testReports:  []*TestReport{{Passed: true, Commands: []CommandResult{passingCommand("go_test ./...")}}},
		reviews:      []*ReviewReport{{Summary: "clean"}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	roles := &failFirstCoderRoles{scriptedTeamRoles: base}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "SUCCESS" || roles.attempts != 2 || base.codeCalls != 1 {
		t.Fatalf("failed Coder attempt was not retried once: %#v attempts=%d calls=%d", report, roles.attempts, base.codeCalls)
	}
}

type failFirstCoderRoles struct {
	*scriptedTeamRoles
	attempts int
}

func (f *failFirstCoderRoles) Code(ctx context.Context, task CodeTask) (*CodeReport, error) {
	f.attempts++
	if f.attempts == 1 {
		return nil, fmt.Errorf("no progress")
	}
	if task.Attempt != 2 || !strings.Contains(task.Step.Objective, "previous Coder failed") {
		return nil, fmt.Errorf("retry context missing")
	}
	return f.scriptedTeamRoles.Code(ctx, task)
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

func TestDevelopmentTeamDefersReviewerPoolUntilFinalMilestone(t *testing.T) {
	plan := teamTestPlan()
	plan.Files = []string{"feature.go"}
	plan.Steps = []PlanStep{
		{ID: "step-1", Objective: "first milestone", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"parseable foundation"}},
		{ID: "step-2", Objective: "second milestone", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"complete behavior"}},
	}
	roles := &scriptedTeamRoles{
		plan: plan,
		codeReports: []*CodeReport{
			{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"foundation"}},
			{StepID: "step-2", ChangedFiles: []string{"feature.go"}, Completed: []string{"complete"}},
		},
		reviews:      []*ReviewReport{{Summary: "final clean"}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement in milestones")
	if report.Status != "SUCCESS" || len(report.Preflights) != 2 || len(report.Reviews) != 1 || roles.reviewCalls != 4 {
		t.Fatalf("intermediate milestone ran semantic reviewer pool: %#v calls=%d", report, roles.reviewCalls)
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
