package subagent

import "testing"

func TestValidateArchitecturePlanAcceptsCohesiveFilePackages(t *testing.T) {
	plan := &ArchitecturePlan{
		Goal: "build game",
		Packages: []ArchitectureWork{
			{ID: "package-1", Objective: "domain state", Files: []string{"game/state.go", "game/pieces.go"}, Acceptance: []string{"state and pieces are defined"}},
			{ID: "package-2", Objective: "game rules", Files: []string{"game/rules.go"}, DependsOn: []string{"package-1"}, Acceptance: []string{"movement works"}},
		},
		FinalVerification: []string{"go_test ./...", "go_vet ./..."},
	}
	if err := validateArchitecturePlan(plan); err != nil {
		t.Fatalf("cohesive architecture rejected: %v", err)
	}
}

func TestValidateArchitecturePlanRejectsForwardDependency(t *testing.T) {
	plan := &ArchitecturePlan{
		Goal: "build game",
		Packages: []ArchitectureWork{
			{ID: "package-1", Objective: "entry point", Files: []string{"main.go"}, DependsOn: []string{"package-2"}, Acceptance: []string{"main works"}},
			{ID: "package-2", Objective: "rules", Files: []string{"rules.go"}, Acceptance: []string{"rules work"}},
		},
		FinalVerification: []string{"go_test ./..."},
	}
	if err := validateArchitecturePlan(plan); err == nil {
		t.Fatal("forward architecture dependency accepted")
	}
}

func TestValidateArchitectureReviewNormalizesCassandraFinding(t *testing.T) {
	plan := &ArchitecturePlan{Goal: "g", Packages: []ArchitectureWork{{ID: "package-1", Objective: "o", Files: []string{"main.go"}, Acceptance: []string{"a"}}}, FinalVerification: []string{"go_test ./..."}}
	review := &ArchitectureReview{Passed: false, Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "too broad", FailureScenario: "coder context grows", RequiredOutcome: "split files"}}}
	if err := validateArchitectureReview(plan, review); err != nil {
		t.Fatal(err)
	}
	if review.Findings[0].ID != "CASSANDRA-1" {
		t.Fatalf("Cassandra id was not normalized: %#v", review)
	}
}

func TestValidateArchitectureReviewRejectsContradictoryPass(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	review := &ArchitectureReview{Passed: true, Findings: []ArchitectureFinding{{ID: "CASSANDRA-1", PackageID: "package-1", Summary: "bad", FailureScenario: "fails", RequiredOutcome: "fix"}}}
	if err := validateArchitectureReview(plan, review); err == nil {
		t.Fatal("passing Cassandra review with findings accepted")
	}
}

func TestFlattenArchitecturePlanOrdersDetailMilestones(t *testing.T) {
	architecture := ArchitecturePlan{
		Goal: "build game",
		Packages: []ArchitectureWork{
			{ID: "package-1", Objective: "state", Files: []string{"state.go"}, Acceptance: []string{"state"}},
			{ID: "package-2", Objective: "rules", Files: []string{"rules.go"}, DependsOn: []string{"package-1"}, Acceptance: []string{"rules"}},
		},
		FinalVerification: []string{"go_test ./...", "go_vet ./..."},
	}
	details := []DetailPlan{
		{PackageID: "package-1", Steps: []PlanStep{{Objective: "types", AllowedFiles: []string{"state.go"}, Acceptance: []string{"types compile"}}, {Objective: "init", AllowedFiles: []string{"state.go"}, Acceptance: []string{"state initializes"}}}},
		{PackageID: "package-2", Steps: []PlanStep{{Objective: "movement", AllowedFiles: []string{"rules.go"}, Acceptance: []string{"movement works"}}}},
	}
	plan, err := flattenArchitecturePlan(architecture, details)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 3 || plan.Steps[0].ID != "step-1" || plan.Steps[2].ID != "step-3" || len(plan.Files) != 2 {
		t.Fatalf("architecture details were not flattened deterministically: %#v", plan)
	}
}

func TestValidateDetailPlanRejectsPackageScopeEscape(t *testing.T) {
	work := ArchitectureWork{ID: "package-1", Files: []string{"state.go"}, Acceptance: []string{"state"}}
	detail := &DetailPlan{PackageID: "package-1", Steps: []PlanStep{{Objective: "escape", AllowedFiles: []string{"main.go"}, Acceptance: []string{"bad"}}}}
	if err := validateDetailPlan(work, detail); err == nil {
		t.Fatal("detail planner escaped architecture package files")
	}
}
