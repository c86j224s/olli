package subagent

import (
	"context"
	"testing"
)

type pipelinePlanningRoles struct {
	*scriptedTeamRoles
	planningCalled bool
	planning       *PlanningReport
}

func (p *pipelinePlanningRoles) PlanArchitecture(context.Context, string) (*DevelopmentPlan, *PlanningReport, error) {
	p.planningCalled = true
	return p.plan, p.planning, nil
}

func TestDevelopmentTeamUsesLayeredPlanningWhenAvailable(t *testing.T) {
	architecture := ArchitecturePlan{Goal: "feature", Packages: []ArchitectureWork{{ID: "package-1", Objective: "state", Files: []string{"feature.go"}, Acceptance: []string{"feature works"}}}, FinalVerification: []string{"go_test ./...", "go_vet ./..."}}
	base := &scriptedTeamRoles{
		plan:         teamTestPlan(),
		codeReports:  []*CodeReport{{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"done"}}},
		testReports:  []*TestReport{{Passed: true, Commands: []CommandResult{passingCommand("go_test ./...")}}},
		reviews:      []*ReviewReport{{Summary: "clean"}},
		verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
	}
	roles := &pipelinePlanningRoles{scriptedTeamRoles: base, planning: &PlanningReport{Architecture: architecture, Reviews: []ArchitectureReview{{Passed: true, Summary: "sound"}}}}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	report := runner.Run(context.Background(), "implement feature")
	if report.Status != "SUCCESS" || !roles.planningCalled || report.Planning == nil || len(report.Planning.Reviews) != 1 {
		t.Fatalf("layered planning pipeline was not used: %#v called=%v", report, roles.planningCalled)
	}
}
