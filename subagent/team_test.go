package subagent

import (
	"context"
	"fmt"
	"testing"
)

type scriptedTeamRoles struct {
	plan          *DevelopmentPlan
	codeReports   []*CodeReport
	testReports   []*TestReport
	reviews       []*ReviewReport
	verification  *TestReport
	codeCalls     int
	testCalls     int
	reviewCalls   int
	transitionLog []string
}

func (s *scriptedTeamRoles) Plan(context.Context, string) (*DevelopmentPlan, error) {
	return s.plan, nil
}
func (s *scriptedTeamRoles) Code(_ context.Context, task CodeTask) (*CodeReport, error) {
	s.transitionLog = append(s.transitionLog, "code:"+task.Step.ID)
	if s.codeCalls >= len(s.codeReports) {
		return nil, fmt.Errorf("unexpected code call")
	}
	report := s.codeReports[s.codeCalls]
	s.codeCalls++
	return report, nil
}
func (s *scriptedTeamRoles) Test(_ context.Context, step PlanStep) (*TestReport, error) {
	s.transitionLog = append(s.transitionLog, "test:"+step.ID)
	if s.testCalls >= len(s.testReports) {
		return nil, fmt.Errorf("unexpected test call")
	}
	report := s.testReports[s.testCalls]
	s.testCalls++
	return report, nil
}
func (s *scriptedTeamRoles) Review(context.Context, *DevelopmentPlan, []CodeReport) (*ReviewReport, error) {
	if s.reviewCalls >= len(s.reviews) {
		return nil, fmt.Errorf("unexpected review call")
	}
	report := s.reviews[s.reviewCalls]
	s.reviewCalls++
	return report, nil
}
func (s *scriptedTeamRoles) Verify(context.Context, []string) (*TestReport, error) {
	return s.verification, nil
}

func passingCommand(command string) CommandResult {
	return CommandResult{Command: command, ExitCode: 0}
}

func teamTestPlan() *DevelopmentPlan {
	return &DevelopmentPlan{
		Goal:              "Implement feature",
		Files:             []string{"feature.go", "feature_test.go"},
		Steps:             []PlanStep{{ID: "step-1", Objective: "Implement", AllowedFiles: []string{"feature.go", "feature_test.go"}, Acceptance: []string{"feature works"}, Verification: []string{"safe-test ./..."}}},
		FinalVerification: []string{"safe-test ./...", "safe-vet ./..."},
	}
}

func TestDevelopmentTeamRunnerSuccessOrder(t *testing.T) {
	roles := &scriptedTeamRoles{
		plan:         teamTestPlan(),
		codeReports:  []*CodeReport{{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"feature works"}}},
		testReports:  []*TestReport{{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./...")}}},
		reviews:      []*ReviewReport{{Findings: nil, Summary: "clean"}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./..."), passingCommand("safe-vet ./...")}},
	}
	runner, err := NewDevelopmentTeamRunner(roles, 2)
	if err != nil {
		t.Fatal(err)
	}
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "SUCCESS" || report.Phase != TeamPhaseDone {
		t.Fatalf("unexpected team result: %#v", report)
	}
	want := []TeamPhase{TeamPhasePlanning, TeamPhaseCoding, TeamPhaseTesting, TeamPhaseReviewing, TeamPhaseVerifying, TeamPhaseDone}
	if fmt.Sprint(report.Transitions) != fmt.Sprint(want) {
		t.Fatalf("unexpected transitions: %v", report.Transitions)
	}
}

func TestDevelopmentTeamRunnerFixesReviewedFindingOnce(t *testing.T) {
	roles := &scriptedTeamRoles{
		plan: teamTestPlan(),
		codeReports: []*CodeReport{
			{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"initial implementation"}},
			{StepID: "step-review-fix-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"nil case fixed"}},
		},
		testReports: []*TestReport{
			{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./...")}},
			{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./...")}},
		},
		reviews: []*ReviewReport{
			{Findings: []Finding{{Severity: "high", File: "feature.go", Line: 10, Summary: "nil input panics", FailureScenario: "nil input reaches dereference"}}},
			{Findings: nil, Summary: "clean"},
		},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./..."), passingCommand("safe-vet ./...")}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "SUCCESS" || report.FixRounds != 1 || len(report.Reviews) != 2 {
		t.Fatalf("unexpected fixed team result: %#v", report)
	}
}

func TestDevelopmentTeamRunnerRejectsCoderScopeEscape(t *testing.T) {
	roles := &scriptedTeamRoles{
		plan:         teamTestPlan(),
		codeReports:  []*CodeReport{{StepID: "step-1", ChangedFiles: []string{"unplanned.go"}, Completed: []string{"done"}}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./..."), passingCommand("safe-vet ./...")}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "FAILED" || report.Phase != TeamPhaseFailed {
		t.Fatalf("scope escape was accepted: %#v", report)
	}
}

func TestDevelopmentTeamRunnerRequiresEveryFinalVerificationCommand(t *testing.T) {
	roles := &scriptedTeamRoles{
		plan:         teamTestPlan(),
		codeReports:  []*CodeReport{{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"done"}}},
		testReports:  []*TestReport{{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./...")}}},
		reviews:      []*ReviewReport{{Findings: nil}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("safe-test ./...")}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "FAILED" || report.Phase != TeamPhaseFailed {
		t.Fatalf("incomplete final verification was accepted: %#v", report)
	}
}

func TestValidateTestReportTrustsExitCodesNotSelfAssessment(t *testing.T) {
	report := &TestReport{Passed: true, Commands: []CommandResult{{Command: "safe-test ./...", ExitCode: 1}}}
	if err := validateTestReport(report); err == nil {
		t.Fatal("passing report with failing exit code was accepted")
	}
}

func TestDevelopmentTeamRunnerStopsAfterFixLimit(t *testing.T) {
	finding := Finding{Severity: "high", File: "feature.go", Line: 10, Summary: "still broken", FailureScenario: "input crashes"}
	roles := &scriptedTeamRoles{
		plan: teamTestPlan(),
		codeReports: []*CodeReport{
			{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"initial"}},
			{StepID: "step-review-fix-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"fix one"}},
		},
		testReports: []*TestReport{
			{Passed: true, Commands: []CommandResult{passingCommand("safe-test")}},
			{Passed: true, Commands: []CommandResult{passingCommand("safe-test")}},
		},
		reviews: []*ReviewReport{{Findings: []Finding{finding}}, {Findings: []Finding{finding}}},
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 1)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "FAILED" || report.FixRounds != 1 {
		t.Fatalf("fix limit was not enforced: %#v", report)
	}
}
