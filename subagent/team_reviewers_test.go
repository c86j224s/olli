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
