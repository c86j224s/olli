package subagent

import (
	"encoding/json"
	"fmt"
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

func TestDecodePlanningJSONExtractsObjectAroundProse(t *testing.T) {
	var value map[string]any
	raw := "Here is the plan:\n```json\n{\"text\":\"brace } and escaped \\\" quote\",\"nested\":{\"ok\":true}}\n```\nDone."
	if err := decodePlanningJSON(raw, &value); err != nil {
		t.Fatal(err)
	}
	if value["text"] != "brace } and escaped \" quote" {
		t.Fatalf("JSON object was not extracted safely: %#v", value)
	}
}

func TestArchitectRepairPayloadIsCompact(t *testing.T) {
	findings := []ArchitectureFinding{{ID: "CASSANDRA-1", PackageID: "package-1", Summary: strings.Repeat("verbose", 50), FailureScenario: strings.Repeat("failure", 50), RequiredOutcome: "split"}}
	payload, err := json.Marshal(map[string]any{"findings": compactArchitectureFindings(findings)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "verbose") || strings.Contains(string(payload), "failure") {
		t.Fatalf("Architect repair payload retained verbose review history: %s", payload)
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

func TestValidateArchitecturePlanMovesDuplicateTestToTestOwner(t *testing.T) {
	plan := &ArchitecturePlan{
		Goal: "build game",
		Packages: []ArchitectureWork{
			{ID: "package-1", Objective: "rules", Files: []string{"rules.go", "rules_test.go"}, Acceptance: []string{"rules work"}},
			{ID: "package-2", Objective: "test rules", Files: []string{"rules_test.go"}, DependsOn: []string{"package-1"}, Acceptance: []string{"tests cover rules"}},
		},
		FinalVerification: []string{"go_test ./..."},
	}
	if err := validateArchitecturePlan(plan); err != nil {
		t.Fatalf("duplicate test ownership was not normalized: %v", err)
	}
	if containsString(plan.Packages[0].Files, "rules_test.go") || !containsString(plan.Packages[1].Files, "rules_test.go") {
		t.Fatalf("test file did not move to test-owning package: %#v", plan.Packages)
	}
}

func TestValidateArchitecturePlanRejectsConflictingFileOwnership(t *testing.T) {
	plan := &ArchitecturePlan{
		Goal: "build game",
		Packages: []ArchitectureWork{
			{ID: "package-1", Objective: "movement", Files: []string{"mechanics.go"}, Acceptance: []string{"movement"}},
			{ID: "package-2", Objective: "locking", Files: []string{"mechanics.go"}, DependsOn: []string{"package-1"}, Acceptance: []string{"locking"}},
		},
		FinalVerification: []string{"go_test ./..."},
	}
	if err := validateArchitecturePlan(plan); err == nil || !strings.Contains(err.Error(), "conflicting ownership") {
		t.Fatalf("duplicate file ownership was accepted: %v", err)
	}
}

func TestValidateArchitecturePlanTopologicallyOrdersForwardDependency(t *testing.T) {
	plan := &ArchitecturePlan{
		Goal: "build game",
		Packages: []ArchitectureWork{
			{ID: "package-1", Objective: "entry point", Files: []string{"main.go"}, DependsOn: []string{"package-2"}, Acceptance: []string{"main works"}},
			{ID: "package-2", Objective: "rules", Files: []string{"rules.go"}, Acceptance: []string{"rules work"}},
		},
		FinalVerification: []string{"go_test ./..."},
	}
	if err := validateArchitecturePlan(plan); err != nil {
		t.Fatalf("acyclic forward dependency was not normalized: %v", err)
	}
	if plan.Packages[0].Files[0] != "rules.go" || plan.Packages[0].ID != "package-1" || plan.Packages[1].DependsOn[0] != "package-1" {
		t.Fatalf("architecture was not stably topologically ordered: %#v", plan.Packages)
	}
}

func TestValidateArchitecturePlanRejectsDependencyCycle(t *testing.T) {
	plan := &ArchitecturePlan{
		Goal: "build game",
		Packages: []ArchitectureWork{
			{ID: "package-1", Objective: "one", Files: []string{"one.go"}, DependsOn: []string{"package-2"}, Acceptance: []string{"one"}},
			{ID: "package-2", Objective: "two", Files: []string{"two.go"}, DependsOn: []string{"package-1"}, Acceptance: []string{"two"}},
		},
		FinalVerification: []string{"go_test ./..."},
	}
	if err := validateArchitecturePlan(plan); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("architecture dependency cycle was accepted: %v", err)
	}
}

func TestValidateArchitectureReviewNormalizesCassandraFinding(t *testing.T) {
	plan := &ArchitecturePlan{Goal: "g", Packages: []ArchitectureWork{{ID: "package-1", Objective: "o", Files: []string{"main.go"}, Acceptance: []string{"a"}}}, FinalVerification: []string{"go_test ./..."}}
	review := &ArchitectureReview{Passed: false, Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "too broad", FailureScenario: "coder context grows", RequiredOutcome: "split files"}}}
	if err := validateArchitectureReview(plan, nil, review); err != nil {
		t.Fatal(err)
	}
	if review.Findings[0].ID != "CASSANDRA-1" {
		t.Fatalf("Cassandra id was not normalized: %#v", review)
	}
}

func TestValidateArchitectureReviewRejectsContradictoryPass(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	review := &ArchitectureReview{Passed: true, Findings: []ArchitectureFinding{{ID: "CASSANDRA-1", PackageID: "package-1", Summary: "bad", FailureScenario: "fails", RequiredOutcome: "fix"}}}
	if err := validateArchitectureReview(plan, nil, review); err == nil {
		t.Fatal("passing Cassandra review with findings accepted")
	}
}

func TestValidateArchitectureReviewNormalizesGlobalPackage(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	for _, alias := range []string{"all", "global", "architecture", "N/A", "none", "not_applicable"} {
		review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: alias, Summary: "global gap", FailureScenario: "objective fails", RequiredOutcome: "cover objective"}}}
		if err := validateArchitectureReview(plan, nil, review); err != nil {
			t.Fatalf("global package alias %q failed: %v", alias, err)
		}
		if review.Findings[0].PackageID != "" {
			t.Fatalf("global package marker %q was not normalized: %#v", alias, review)
		}
	}
}

func TestValidateArchitectureReviewCapsFindingsAtThree(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{
		{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"},
	}}
	if err := validateArchitectureReview(plan, nil, review); err == nil {
		t.Fatal("Cassandra returned more than three findings")
	}
}

func TestValidateArchitectureReviewTracksPriorResolutions(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	previous := []ArchitectureFinding{{ID: "CASSANDRA-1"}}
	resolved := &ArchitectureReview{Passed: true, FindingResolutions: []ArchitectureFindingResolution{{ID: "1", Status: "resolved", Evidence: "split package"}}}
	if err := validateArchitectureReview(plan, previous, resolved); err != nil {
		t.Fatalf("resolved Cassandra finding rejected: %v", err)
	}
	missing := &ArchitectureReview{Passed: true}
	if err := validateArchitectureReview(plan, previous, missing); err == nil {
		t.Fatal("missing Cassandra resolution accepted")
	}
	deferred := &ArchitectureReview{MoreSuspected: true}
	if err := validateArchitectureReview(plan, previous, deferred); err != nil {
		t.Fatalf("capped Cassandra review could not defer a prior finding: %v", err)
	}
	unresolved := &ArchitectureReview{
		Findings:           []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "still broad", FailureScenario: "large context", RequiredOutcome: "split"}},
		FindingResolutions: []ArchitectureFindingResolution{{ID: "1", Status: "unresolved", Evidence: "still one file"}},
	}
	if err := validateArchitectureReview(plan, previous, unresolved); err != nil {
		t.Fatalf("valid unresolved Cassandra finding rejected: %v", err)
	}
}

func TestNormalizeCassandraIDAcceptsNumericAliases(t *testing.T) {
	for input, want := range map[string]string{"package-3": "CASSANDRA-3", "CASSANDRA-003": "CASSANDRA-3", "finding_2": "CASSANDRA-2"} {
		if got := normalizeCassandraID(input); got != want {
			t.Fatalf("finding alias %q was normalized to %q, want %q", input, got, want)
		}
	}
}

func TestValidateArchitectureReviewMapsSingleUnknownResolutionToPriorFinding(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	previous := []ArchitectureFinding{{ID: "CASSANDRA-9"}}
	review := &ArchitectureReview{Passed: true, FindingResolutions: []ArchitectureFindingResolution{{ID: "wrong-label", Status: "resolved", Evidence: "current plan covers it"}}}
	if err := validateArchitectureReview(plan, previous, review); err != nil {
		t.Fatalf("single unambiguous resolution id was not recovered: %v", err)
	}
	if review.FindingResolutions[0].ID != "CASSANDRA-9" {
		t.Fatalf("resolution was not mapped to prior id: %#v", review.FindingResolutions)
	}
}

func TestValidateArchitectureReviewDropsFindingDeclaredResolved(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	previous := []ArchitectureFinding{{ID: "CASSANDRA-1"}}
	review := &ArchitectureReview{
		Passed:             true,
		Findings:           []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "stale finding", FailureScenario: "old scenario", RequiredOutcome: "old outcome"}},
		FindingResolutions: []ArchitectureFindingResolution{{ID: "1", Status: "resolved", Evidence: "current plan now covers it"}},
	}
	if err := validateArchitectureReview(plan, previous, review); err != nil {
		t.Fatalf("self-contradictory resolved finding was not normalized: %v", err)
	}
	if len(review.Findings) != 0 || len(review.DismissedFindings) != 1 {
		t.Fatalf("resolved finding remained active: %#v", review)
	}
}

func TestArchitectureReviewMoreSuspectedPreventsPass(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1"}}}
	review := &ArchitectureReview{Passed: true, MoreSuspected: true}
	if err := validateArchitectureReview(plan, nil, review); err == nil {
		t.Fatal("Cassandra passed while more_suspected was true")
	}
}

func TestSanitizeArchitectureReviewDismissesInventedDependencyCycle(t *testing.T) {
	objective := "Build a turn-based command game. Shared Go structs in one package are allowed."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{
		{ID: "package-1", Files: []string{"types.go"}},
		{ID: "package-2", Files: []string{"input.go"}, DependsOn: []string{"package-1"}},
		{ID: "package-3", Files: []string{"logic.go"}, DependsOn: []string{"package-1"}},
		{ID: "package-4", Files: []string{"main.go"}, DependsOn: []string{"package-2", "package-3"}},
	}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-4", Summary: "dependency cycle risk", FailureScenario: "controller passes input to logic", RequiredOutcome: "add an import dependency"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.Findings) != 0 || len(review.DismissedFindings) != 1 {
		t.Fatalf("invented dependency cycle was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewResolvesDismissedPriorFinding(t *testing.T) {
	objective := "Use blocking turn-based input. Do not add real-time or asynchronous input."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"input.go"}}}}
	previous := []ArchitectureFinding{{ID: "CASSANDRA-1"}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "blocking input", FailureScenario: "no autonomous gravity", RequiredOutcome: "use non-blocking real-time input"}}, FindingResolutions: []ArchitectureFindingResolution{{ID: "1", Status: "unresolved", Evidence: "still blocking"}}}
	sanitizeArchitectureReview(objective, plan, previous, review)
	if !review.Passed || len(review.Findings) != 0 || len(review.FindingResolutions) != 1 || review.FindingResolutions[0].Status != "resolved" {
		t.Fatalf("dismissed prior finding was not resolved: %#v", review)
	}
	if err := validateArchitectureReview(plan, previous, review); err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeArchitectureReviewDismissesStrictStateMutationContract(t *testing.T) {
	objective := "Cohesive files may share plain Go structs in the same package; strict getter/setter encapsulation is not required."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"state.go"}}, {ID: "package-2", Files: []string{"logic.go"}, DependsOn: []string{"package-1"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "missing state modification contract", FailureScenario: "logic modifies shared state", RequiredOutcome: "define public methods or authorized struct fields"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("strict state mutation contract was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewDismissesSharedStructDataContractDemand(t *testing.T) {
	objective := "Cohesive files may share plain Go structs in the same package."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"locking.go"}}, {ID: "package-2", Files: []string{"clear.go"}, DependsOn: []string{"package-1"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-2", Summary: "state handoff ambiguous", FailureScenario: "clear sees locked board", RequiredOutcome: "define an explicit data contract or function signature"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("shared-struct data contract demand was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewDismissesStrongerAnyPieceGameOverRule(t *testing.T) {
	objective := "Game over means the next piece cannot be placed at the top."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"logic.go"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "game-over too narrow", FailureScenario: "some other shape could fit", RequiredOutcome: "check whether any piece can be placed or whether the full top row is blocked"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("stronger any-piece game-over rule was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewDismissesTurnBasedBlockingAsStall(t *testing.T) {
	objective := "Build a turn-based game with blocking Enter-terminated commands."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"input.go"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "input stalls the loop", FailureScenario: "blocking read waits indefinitely", RequiredOutcome: "avoid the blocking nature of input"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("turn-based blocking false positive was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewKeepsRequiredGameOverTest(t *testing.T) {
	objective := "Provide tests for game-over behavior."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"game_test.go"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "game-over is too stateful", FailureScenario: "game-over is stateful and loop-dependent", RequiredOutcome: "remove the game-over test"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("attempt to remove required game-over test was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewDismissesSharedStatePartitionDemand(t *testing.T) {
	objective := "Cohesive files may share plain Go structs in the same package."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"types.go"}}, {ID: "package-2", Files: []string{"logic.go"}, DependsOn: []string{"package-1"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "state management ambiguity", FailureScenario: "logic modifies shared state", RequiredOutcome: "mutable global state variables must be explicitly partitioned for encapsulation"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("shared state partition demand was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewDismissesAlreadyPresentDependency(t *testing.T) {
	objective := "Build and test a game."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"logic.go"}}, {ID: "package-2", Files: []string{"logic_test.go"}, DependsOn: []string{"package-1"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-2", Summary: "missing dependency", FailureScenario: "tests cannot access logic", RequiredOutcome: "Add 'package-1' to depends_on"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("already-present dependency finding was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewDismissesAlreadyCoveredRotationBoundary(t *testing.T) {
	objective := "Build Tetris with rotation and collision."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Objective: "rotation rules", Files: []string{"logic.go"}, Acceptance: []string{"Rotated pieces must remain within board boundaries and pass collision checks."}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "rotation validation missing", FailureScenario: "rotation may leave board", RequiredOutcome: "Validate the entire rotated piece against board boundaries and collision before accepting rotation."}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("already-covered rotation boundary was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewDismissesCoveredRuntimeFlow(t *testing.T) {
	objective := "Cohesive files may share plain Go structs in the same package."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Objective: "orchestrate game loop", Files: []string{"main.go"}, Acceptance: []string{"Read input action, update state with game logic, render the result, and repeat until quit."}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "input output is not sequenced", FailureScenario: "input may not feed into update", RequiredOutcome: "Explicitly sequence input output into state update before calling render."}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if !review.Passed || len(review.DismissedFindings) != 1 {
		t.Fatalf("already-covered runtime flow was not dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewKeepsRealMissingRequirement(t *testing.T) {
	objective := "Build Tetris with rotation and tests."
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"logic.go"}}}}
	review := &ArchitectureReview{Findings: []ArchitectureFinding{{ID: "1", PackageID: "package-1", Summary: "rotation missing", FailureScenario: "w cannot rotate", RequiredOutcome: "cover rotation in acceptance"}}}
	sanitizeArchitectureReview(objective, plan, nil, review)
	if review.Passed || len(review.Findings) != 1 || len(review.DismissedFindings) != 0 {
		t.Fatalf("real missing requirement was dismissed: %#v", review)
	}
}

func TestSanitizeArchitectureReviewRestoresOmittedUnresolvedFinding(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"main.go"}}}}
	previous := []ArchitectureFinding{{ID: "CASSANDRA-1", PackageID: "package-1", Summary: "input termination unclear", FailureScenario: "EOF cannot terminate", RequiredOutcome: "clarify input termination"}}
	review := &ArchitectureReview{FindingResolutions: []ArchitectureFindingResolution{{ID: "1", Status: "unresolved", Evidence: "still ambiguous"}}}
	sanitizeArchitectureReview("build a command game", plan, previous, review)
	if review.Passed || len(review.Findings) != 1 || review.Findings[0].ID != "CASSANDRA-1" {
		t.Fatalf("omitted unresolved finding was not restored: %#v", review)
	}
	if err := validateArchitectureReview(plan, previous, review); err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeArchitectureReviewDefersOmittedUnresolvedPastCap(t *testing.T) {
	plan := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-1", Files: []string{"main.go"}}}}
	previous := []ArchitectureFinding{{ID: "CASSANDRA-4", PackageID: "package-1", Summary: "fourth", FailureScenario: "fourth", RequiredOutcome: "fourth outcome"}}
	review := &ArchitectureReview{
		Findings: []ArchitectureFinding{
			{ID: "1", Summary: "one", FailureScenario: "one", RequiredOutcome: "one"},
			{ID: "2", Summary: "two", FailureScenario: "two", RequiredOutcome: "two"},
			{ID: "3", Summary: "three", FailureScenario: "three", RequiredOutcome: "three"},
		},
		FindingResolutions: []ArchitectureFindingResolution{{ID: "4", Status: "unresolved", Evidence: "still present"}},
	}
	sanitizeArchitectureReview("build feature", plan, previous, review)
	if len(review.Findings) != 3 || !review.MoreSuspected || review.Passed {
		t.Fatalf("overflowing unresolved finding was not deferred: %#v", review)
	}
	if err := validateArchitectureReview(plan, previous, review); err != nil {
		t.Fatal(err)
	}
}

func TestMergeArchitectureUnresolvedRetainsDeferredFindings(t *testing.T) {
	previous := []ArchitectureFinding{{ID: "CASSANDRA-1"}, {ID: "CASSANDRA-2"}}
	review := &ArchitectureReview{
		Findings:           []ArchitectureFinding{{ID: "CASSANDRA-3"}},
		FindingResolutions: []ArchitectureFindingResolution{{ID: "CASSANDRA-1", Status: "resolved", Evidence: "fixed"}},
		MoreSuspected:      true,
	}
	merged := mergeArchitectureUnresolved(previous, review)
	if len(merged) != 2 || merged[0].ID != "CASSANDRA-2" || merged[1].ID != "CASSANDRA-3" {
		t.Fatalf("deferred architecture findings were lost: %#v", merged)
	}
}

func TestRewriteArchitectureFindingTargetsUsesStableFiles(t *testing.T) {
	architecture := &ArchitecturePlan{Packages: []ArchitectureWork{{ID: "package-2", Files: []string{"sequence.go"}}}}
	findings := rewriteArchitectureFindingTargets([]ArchitectureFinding{{ID: "CASSANDRA-1", PackageID: "package-2", RequiredOutcome: "own sequence state"}}, architecture)
	if findings[0].PackageID != "" || !strings.Contains(findings[0].RequiredOutcome, "sequence.go") {
		t.Fatalf("finding target was not stabilized across replans: %#v", findings)
	}
}

func TestCompactArchitectureFindingsDropsVerboseHistory(t *testing.T) {
	findings := []ArchitectureFinding{{ID: "CASSANDRA-1", PackageID: "package-2", Summary: "verbose", FailureScenario: "very verbose", RequiredOutcome: "split rules"}}
	compacted := compactArchitectureFindings(findings)
	if len(compacted) != 1 || len(compacted[0]) != 3 || compacted[0]["required_outcome"] != "split rules" {
		t.Fatalf("architecture finding was not compacted: %#v", compacted)
	}
	if _, exists := compacted[0]["failure_scenario"]; exists {
		t.Fatalf("verbose history leaked into repair payload: %#v", compacted)
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
		{PackageID: "package-1", Steps: []PlanStep{{Objective: "types and initialization", AllowedFiles: []string{"state.go"}, Acceptance: []string{"types compile", "state initializes"}}}},
		{PackageID: "package-2", Steps: []PlanStep{{Objective: "movement", AllowedFiles: []string{"rules.go"}, Acceptance: []string{"movement works"}}}},
	}
	plan, err := flattenArchitecturePlan(architecture, details)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].ID != "step-1" || plan.Steps[1].ID != "step-2" || len(plan.Files) != 2 {
		t.Fatalf("architecture details were not flattened deterministically: %#v", plan)
	}
}

func TestValidateDetailPlanInheritsFullPackageScope(t *testing.T) {
	work := ArchitectureWork{ID: "package-1", Files: []string{"logic.go", "logic_test.go"}, Acceptance: []string{"rotation works", "line clearing works"}}
	detail := &DetailPlan{PackageID: "package-1", Steps: []PlanStep{{Objective: "implement logic", AllowedFiles: []string{"logic.go"}, Acceptance: []string{"rotation works"}}}}
	if err := validateDetailPlan(work, detail); err != nil {
		t.Fatal(err)
	}
	step := detail.Steps[0]
	if len(step.AllowedFiles) != 2 || len(step.Acceptance) != 2 {
		t.Fatalf("detail milestone did not inherit full architecture package: %#v", step)
	}
}

func TestValidateDetailPlanRequiresOneCompleteMilestone(t *testing.T) {
	work := ArchitectureWork{ID: "package-1", Files: []string{"state.go"}, Acceptance: []string{"state"}}
	detail := &DetailPlan{PackageID: "package-1"}
	for index := 0; index < 2; index++ {
		detail.Steps = append(detail.Steps, PlanStep{Objective: fmt.Sprintf("part %d", index), AllowedFiles: []string{"state.go"}, Acceptance: []string{"works"}})
	}
	if err := validateDetailPlan(work, detail); err == nil {
		t.Fatal("detail planner split one package into incremental milestones")
	}
}

func TestValidateDetailPlanRejectsPackageScopeEscape(t *testing.T) {
	work := ArchitectureWork{ID: "package-1", Files: []string{"state.go"}, Acceptance: []string{"state"}}
	detail := &DetailPlan{PackageID: "package-1", Steps: []PlanStep{{Objective: "escape", AllowedFiles: []string{"main.go"}, Acceptance: []string{"bad"}}}}
	if err := validateDetailPlan(work, detail); err == nil {
		t.Fatal("detail planner escaped architecture package files")
	}
}
