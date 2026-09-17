package subagent

import "testing"

func TestPlanningReportAtLimitCarriesUnresolvedRisk(t *testing.T) {
	architecture := &ArchitecturePlan{Goal: "feature", Packages: []ArchitectureWork{{ID: "package-1", Objective: "implement", Files: []string{"feature.go"}, Acceptance: []string{"works"}}}}
	finding := ArchitectureFinding{ID: "CASSANDRA-1", PackageID: "package-1", Summary: "contract concern", FailureScenario: "model concern", RequiredOutcome: "clarify contract"}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{finding}, Summary: "still concerned"}
	planning := planningReportAtLimit(architecture, nil, []ArchitectureReview{*review}, review)
	if !planning.ReviewLimitReached || len(planning.UnresolvedFindings) != 1 || planning.UnresolvedFindings[0].ID != finding.ID {
		t.Fatalf("bounded architecture fallback lost unresolved risk: %#v", planning)
	}
}
