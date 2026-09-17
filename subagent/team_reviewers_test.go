package subagent

import (
	"strings"
	"testing"
)

func TestReviewerPromptsHaveDistinctResponsibilities(t *testing.T) {
	checks := map[ReviewDimension]string{
		ReviewDimensionRequirements: "Requirement coverage only",
		ReviewDimensionLogic:        "Runtime correctness and state invariants only",
		ReviewDimensionSafety:       "Security and execution safety only",
		ReviewDimensionTests:        "Verification adequacy only",
	}
	for dimension, marker := range checks {
		prompt, err := reviewerPromptForDimension(dimension)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(prompt, marker) || !strings.Contains(prompt, strings.ToUpper(string(dimension))+"-") {
			t.Fatalf("%s prompt lacks specialization or id prefix: %q", dimension, prompt)
		}
	}
}

func TestReviewDimensionsRerunOnlyActiveOwnersAfterFix(t *testing.T) {
	finding := Finding{ID: "LOGIC-1"}
	context := ReviewContext{PreviousReviews: []DimensionReview{
		{Dimension: ReviewDimensionRequirements, Report: ReviewReport{Summary: "clean"}},
		{Dimension: ReviewDimensionLogic, Report: ReviewReport{Findings: []Finding{finding}}},
		{Dimension: ReviewDimensionSafety, Report: ReviewReport{Summary: "clean"}},
		{Dimension: ReviewDimensionTests, Report: ReviewReport{Summary: "clean"}},
	}}
	initial := reviewDimensionsForContext(context, false)
	if len(initial) != len(defaultReviewDimensions) {
		t.Fatalf("initial review did not run every dimension: %v", initial)
	}
	rerun := reviewDimensionsForContext(context, true)
	if len(rerun) != 1 || rerun[0] != ReviewDimensionLogic {
		t.Fatalf("fix review did not target finding owner: %v", rerun)
	}
}

func TestFailedFixVerificationAddsTestReviewer(t *testing.T) {
	context := ReviewContext{
		PreviousReviews:  []DimensionReview{{Dimension: ReviewDimensionLogic, Report: ReviewReport{Findings: []Finding{{ID: "LOGIC-1"}}}}},
		LatestTestReport: &TestReport{Passed: false},
	}
	dimensions := reviewDimensionsForContext(context, true)
	if len(dimensions) != 2 || dimensions[0] != ReviewDimensionLogic || dimensions[1] != ReviewDimensionTests {
		t.Fatalf("failed verification did not add test reviewer: %v", dimensions)
	}
}

func TestValidateDimensionReviewPrefixesFindingIDAndOwner(t *testing.T) {
	plan := &DevelopmentPlan{Files: []string{"main.go"}}
	report := &ReviewReport{Findings: []Finding{{
		ID: "001", Severity: "high", File: "main.go", Line: 10, Summary: "wrong state",
		FailureScenario: "input corrupts state", RequiredOutcome: "state remains valid", Verification: []string{"go_test ./..."},
	}}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionLogic, nil, report); err != nil {
		t.Fatal(err)
	}
	finding := report.Findings[0]
	if finding.ID != "LOGIC-001" || finding.Dimension != ReviewDimensionLogic || len(finding.Reviewers) != 1 || finding.Reviewers[0] != "logic" {
		t.Fatalf("finding ownership was not normalized: %#v", finding)
	}
}

func TestValidateDimensionReviewPrefixesResolutionID(t *testing.T) {
	plan := &DevelopmentPlan{Files: []string{"main.go"}}
	previous := []ReviewReport{{Findings: []Finding{{ID: "LOGIC-001"}}}}
	report := &ReviewReport{FindingResolutions: []FindingResolution{{ID: "001", Status: "resolved", Evidence: "fixed"}}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionLogic, previous, report); err != nil {
		t.Fatal(err)
	}
	if report.FindingResolutions[0].ID != "LOGIC-001" {
		t.Fatalf("resolution id was not normalized: %#v", report)
	}
}

func TestMatchingPriorFindingIDRecoversNestedDimensionPrefix(t *testing.T) {
	previous := map[string]struct{}{"LOGIC-1": {}, "LOGIC-main.go-entry-point": {}}
	for candidate, want := range map[string]string{
		"LOGIC-SAFETY-1":                                "LOGIC-1",
		"LOGIC-REQUIREMENTS-main.go-entry-point":        "LOGIC-main.go-entry-point",
		"SAFETY-LOGIC-REQUIREMENTS-main.go-entry-point": "LOGIC-main.go-entry-point",
	} {
		if got := matchingPriorFindingID(previous, candidate); got != want {
			t.Fatalf("nested dimension prefix %q was recovered as %q, want %q", candidate, got, want)
		}
	}
	if got := matchingPriorFindingID(map[string]struct{}{"LOGIC-1": {}, "SAFETY-1": {}}, "TESTS-1"); got != "" {
		t.Fatalf("ambiguous identity was incorrectly recovered: %q", got)
	}
}

func TestValidateDimensionReviewConservativelyRetainsMissingResolution(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	old := Finding{ID: "LOGIC-001", File: "feature.go", Line: 1, Summary: "broken", FailureScenario: "fails", RequiredOutcome: "fix", Verification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{old}}}
	report := &ReviewReport{}
	if err := validateDimensionReviewReport(plan, ReviewDimensionLogic, previous, report); err != nil {
		t.Fatal(err)
	}
	if len(report.FindingResolutions) != 1 || report.FindingResolutions[0].Status != "unresolved" || len(report.Findings) != 1 {
		t.Fatalf("missing resolution was not retained conservatively: %#v", report)
	}
}

func TestValidateDimensionReviewRestoresOmittedUnresolvedFinding(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	old := Finding{ID: "REQUIREMENTS-2", File: "feature.go", Line: 1, Summary: "still broken", FailureScenario: "fails", RequiredOutcome: "fix it", Verification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{old}}}
	report := &ReviewReport{FindingResolutions: []FindingResolution{{ID: "REQUIREMENTS-2", Status: "unresolved", Evidence: "still fails"}}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionRequirements, previous, report); err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].ID != old.ID {
		t.Fatalf("omitted unresolved Reviewer finding was not restored: %#v", report)
	}
}

func TestValidateDimensionReviewDropsFindingDeclaredResolved(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"main.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"main.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	old := Finding{ID: "REQUIREMENTS-main.go-loop", File: "main.go", Line: 1, Summary: "loop missing", FailureScenario: "no loop", RequiredOutcome: "add loop", Verification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{old}}}
	report := &ReviewReport{Findings: []Finding{old}, FindingResolutions: []FindingResolution{{ID: old.ID, Status: "resolved", Evidence: "loop added"}}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionRequirements, previous, report); err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("resolved finding remained after normalization: %#v", report.Findings)
	}
}

func TestValidateDimensionReviewMergesNestedDuplicateResolution(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	old := Finding{ID: "LOGIC-2", File: "feature.go", Line: 1, Summary: "broken", FailureScenario: "fails", RequiredOutcome: "fix", Verification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{old}}}
	report := &ReviewReport{FindingResolutions: []FindingResolution{
		{ID: "LOGIC-2", Status: "resolved", Evidence: "fixed"},
		{ID: "LOGIC-SAFETY-2", Status: "resolved", Evidence: "also verified"},
	}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionLogic, previous, report); err != nil {
		t.Fatal(err)
	}
	if len(report.FindingResolutions) != 1 || report.FindingResolutions[0].ID != "LOGIC-2" {
		t.Fatalf("nested duplicate resolution was not merged: %#v", report.FindingResolutions)
	}
}

func TestValidateDimensionReviewMergesDuplicateResolutionsConservatively(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	old := Finding{ID: "LOGIC-1", File: "feature.go", Line: 1, Summary: "broken", FailureScenario: "fails", RequiredOutcome: "fix", Verification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{old}}}
	report := &ReviewReport{FindingResolutions: []FindingResolution{
		{ID: "LOGIC-1", Status: "resolved", Evidence: "first says fixed"},
		{ID: "LOGIC-1", Status: "unresolved", Evidence: "second still reproduces"},
	}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionLogic, previous, report); err != nil {
		t.Fatal(err)
	}
	if len(report.FindingResolutions) != 1 || report.FindingResolutions[0].Status != "unresolved" || len(report.Findings) != 1 {
		t.Fatalf("duplicate resolutions were not merged conservatively: %#v", report)
	}
}

func TestValidateDimensionReviewMapsMultipleWrongResolutionsToOnlyActiveFinding(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	old := Finding{ID: "LOGIC-9", File: "feature.go", Line: 1, Summary: "broken", FailureScenario: "fails", RequiredOutcome: "fix", Verification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{old}}}
	report := &ReviewReport{FindingResolutions: []FindingResolution{
		{ID: "LOGIC-REQUIREMENTS-main_loop_missing", Status: "resolved", Evidence: "fixed"},
		{ID: "SAFETY-other", Status: "resolved", Evidence: "also fixed"},
	}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionLogic, previous, report); err != nil {
		t.Fatal(err)
	}
	if len(report.FindingResolutions) != 1 || report.FindingResolutions[0].ID != "LOGIC-9" {
		t.Fatalf("wrong resolutions were not mapped to only active finding: %#v", report.FindingResolutions)
	}
}

func TestValidateDimensionReviewMapsSingleWrongResolutionID(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{{ID: "REQUIREMENTS-1", File: "feature.go", Line: 1, Summary: "old", FailureScenario: "old failure", RequiredOutcome: "fix", Verification: []string{"go_test ./..."}}}}}
	report := &ReviewReport{FindingResolutions: []FindingResolution{{ID: "LOGIC-1", Status: "resolved", Evidence: "fixed"}}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionRequirements, previous, report); err != nil {
		t.Fatalf("single unambiguous resolution ID was not recovered: %v", err)
	}
	if report.FindingResolutions[0].ID != "REQUIREMENTS-1" {
		t.Fatalf("resolution ID was not mapped to the only active finding: %#v", report.FindingResolutions)
	}
}

func TestValidateDimensionReviewIgnoresUnknownResolutionWhenMultipleFindingsAreActive(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"io.go", "main.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"io.go", "main.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{
		{ID: "REQUIREMENTS-main-loop", File: "main.go", Line: 1, Summary: "loop missing", FailureScenario: "program exits", RequiredOutcome: "run loop", Verification: []string{"go_test ./..."}},
		{ID: "REQUIREMENTS-render-board", File: "io.go", Line: 1, Summary: "render missing", FailureScenario: "board hidden", RequiredOutcome: "render board", Verification: []string{"go_test ./..."}},
	}}}
	report := &ReviewReport{FindingResolutions: []FindingResolution{{ID: "LOGIC-RENDER-OVERLAY", Status: "resolved", Evidence: "unrelated reviewer finding"}}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionRequirements, previous, report); err != nil {
		t.Fatalf("unknown cross-dimension resolution should not invalidate the review: %v", err)
	}
	if len(report.FindingResolutions) != 2 || len(report.Findings) != 2 {
		t.Fatalf("active findings were not retained conservatively: %#v", report)
	}
	for _, resolution := range report.FindingResolutions {
		if resolution.ID == "REQUIREMENTS-LOGIC-RENDER-OVERLAY" {
			t.Fatalf("unknown resolution was retained: %#v", report.FindingResolutions)
		}
		if resolution.Status != "unresolved" {
			t.Fatalf("active finding was not retained unresolved: %#v", report.FindingResolutions)
		}
	}
}

func TestValidateDimensionReviewRenumbersDuplicateIDs(t *testing.T) {
	plan := &DevelopmentPlan{Goal: "feature", Files: []string{"feature.go"}, Steps: []PlanStep{{ID: "step-1", Objective: "feature", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"works"}}}, FinalVerification: []string{"go_test ./..."}}
	report := &ReviewReport{Findings: []Finding{
		{ID: "step-1", Severity: "high", File: "feature.go", Line: 1, Summary: "first", FailureScenario: "first fails", RequiredOutcome: "fix first", Verification: []string{"go_test ./..."}},
		{ID: "step-1", Severity: "medium", File: "feature.go", Line: 2, Summary: "second", FailureScenario: "second fails", RequiredOutcome: "fix second", Verification: []string{"go_test ./..."}},
	}}
	if err := validateDimensionReviewReport(plan, ReviewDimensionRequirements, nil, report); err != nil {
		t.Fatal(err)
	}
	if report.Findings[0].ID != "REQUIREMENTS-step-1" || report.Findings[1].ID != "REQUIREMENTS-step-1-2" {
		t.Fatalf("duplicate Reviewer IDs were not stabilized: %#v", report.Findings)
	}
}

func TestMergeDimensionReviewsDeduplicatesDeterministically(t *testing.T) {
	base := Finding{ID: "REQUIREMENTS-1", Severity: "medium", File: "main.go", Line: 20, Summary: "Current piece is not rendered", FailureScenario: "board looks empty", RequiredOutcome: "show current piece", Verification: []string{"go_test ./..."}}
	other := base
	other.ID = "LOGIC-2"
	other.Severity = "high"
	other.Verification = []string{"go_vet ./..."}
	bundle := mergeDimensionReviews([]DimensionReview{
		{Dimension: ReviewDimensionRequirements, Report: ReviewReport{Findings: []Finding{base}}},
		{Dimension: ReviewDimensionLogic, Report: ReviewReport{Findings: []Finding{other}}},
	})
	if len(bundle.Findings) != 1 {
		t.Fatalf("duplicate findings were not merged: %#v", bundle)
	}
	finding := bundle.Findings[0]
	if finding.Severity != "high" || len(finding.Verification) != 2 || len(finding.Reviewers) != 2 {
		t.Fatalf("merged finding lost evidence: %#v", finding)
	}
}

func TestActiveFindingsByDimensionHonorsResolutions(t *testing.T) {
	reviews := []DimensionReview{
		{Dimension: ReviewDimensionLogic, Report: ReviewReport{Findings: []Finding{{ID: "LOGIC-1"}}}},
		{Dimension: ReviewDimensionLogic, Report: ReviewReport{FindingResolutions: []FindingResolution{{ID: "LOGIC-1", Status: "resolved", Evidence: "fixed"}}}},
		{Dimension: ReviewDimensionTests, Report: ReviewReport{Findings: []Finding{{ID: "TESTS-1"}}}},
	}
	active := activeFindingsByDimension(reviews)
	if len(active[ReviewDimensionLogic]) != 0 || len(active[ReviewDimensionTests]) != 1 {
		t.Fatalf("finding ownership state is wrong: %#v", active)
	}
}
