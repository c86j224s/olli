package subagent

import (
	"strings"
	"testing"

	"github.com/c86j224s/olli/tools"
)

func TestDecodePlanningJSONAcceptsOneFencedObject(t *testing.T) {
	var value map[string]any
	if err := decodePlanningJSON("```json\n{\"ok\":true}\n```", &value); err != nil {
		t.Fatal(err)
	}
	if value["ok"] != true {
		t.Fatalf("unexpected decoded value: %#v", value)
	}
}

func TestRegisterArchitectToolsNarrowsExplicitFileTask(t *testing.T) {
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspace(t.TempDir())
	reg.SetWorkspaceRoot(reg.GetWorkspace())
	registerArchitectTools(reg, []requiredToolCall{{Name: "view_file"}})
	if _, ok := reg.GetDefinition("view_file"); !ok {
		t.Fatal("explicit-file architect is missing view_file")
	}
	for _, forbidden := range []string{"list_dir", "grep_search"} {
		if _, ok := reg.GetDefinition(forbidden); ok {
			t.Fatalf("explicit-file architect received distracting tool %s", forbidden)
		}
	}
}

func TestNormalizeArchitectureJSONAcceptsWorkPackagesAlias(t *testing.T) {
	raw := `{"goal":"g","work_packages":[],"final_verification":[]}`
	normalized := normalizeArchitectureJSON(raw)
	if strings.Contains(normalized, "work_packages") || !strings.Contains(normalized, `"packages"`) {
		t.Fatalf("architecture alias was not normalized: %s", normalized)
	}
}

func TestNormalizeArchitectureJSONRepairsCommonSmallModelShapes(t *testing.T) {
	raw := `{"goal":"g","packages":[{"id":1,"objective":"o","files":["main.go"],"depends_on":[],"acceptance":"works"}],"final_verification":"go test ./... and go vet ./..."}`
	var plan ArchitecturePlan
	if err := decodePlanningJSON(normalizeArchitectureJSON(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Packages[0].ID != "package-1" || len(plan.Packages[0].Acceptance) != 1 || len(plan.FinalVerification) != 2 {
		t.Fatalf("common architecture shapes were not normalized: %#v", plan)
	}
}

func TestValidateArchitecturePlanNormalizesGoCommandSpelling(t *testing.T) {
	plan := &ArchitecturePlan{Goal: "g", Packages: []ArchitectureWork{{ID: "package-1", Objective: "o", Files: []string{"main.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go test ./...", "go vet ./..."}}
	if err := validateArchitecturePlan(plan); err != nil {
		t.Fatal(err)
	}
	if plan.FinalVerification[0] != "go_test ./..." || plan.FinalVerification[1] != "go_vet ./..." {
		t.Fatalf("verification spelling was not normalized: %#v", plan.FinalVerification)
	}
}

func TestNormalizeArchitectureJSONRemapsNamedDependencies(t *testing.T) {
	raw := `{"goal":"g","packages":[{"id":"board","objective":"state","files":["board.go"],"depends_on":[],"acceptance":"state"},{"id":"rules","objective":"rules","files":["rules.go"],"depends_on":["board"],"acceptance":"rules"}],"final_verification":["go_test ./..."]}`
	var plan ArchitecturePlan
	if err := decodePlanningJSON(normalizeArchitectureJSON(raw), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Packages[1].DependsOn[0] != "package-1" {
		t.Fatalf("named dependency was not remapped: %#v", plan)
	}
}

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
