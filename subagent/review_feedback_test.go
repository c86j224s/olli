package subagent

import "testing"

func TestValidateReviewReportTracksPreviousFindingResolution(t *testing.T) {
	plan := &DevelopmentPlan{Files: []string{"feature.go"}}
	finding := Finding{ID: "finding-1", Severity: "high", File: "feature.go", Line: 10, Summary: "panic", FailureScenario: "nil input panics", RequiredOutcome: "nil input is handled", Verification: []string{"go_test ./..."}}
	previous := []ReviewReport{{Findings: []Finding{finding}}}

	resolved := &ReviewReport{FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "resolved", Evidence: "test now passes"}}}
	if err := validateReviewReport(plan, previous, resolved); err != nil {
		t.Fatalf("resolved finding report rejected: %v", err)
	}

	missing := &ReviewReport{}
	if err := validateReviewReport(plan, previous, missing); err != nil {
		t.Fatalf("missing prior-finding resolution was not retained conservatively: %v", err)
	}
	if len(missing.Findings) != 1 || len(missing.FindingResolutions) != 1 || missing.FindingResolutions[0].Status != "unresolved" {
		t.Fatalf("missing prior-finding resolution did not preserve active finding: %#v", missing)
	}

	unresolvedWithoutFinding := &ReviewReport{FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "unresolved", Evidence: "still panics"}}}
	if err := validateReviewReport(plan, previous, unresolvedWithoutFinding); err != nil {
		t.Fatalf("omitted unresolved finding was not restored: %v", err)
	}
	if len(unresolvedWithoutFinding.Findings) != 1 || unresolvedWithoutFinding.Findings[0].ID != "finding-1" {
		t.Fatalf("restored unresolved finding is missing: %#v", unresolvedWithoutFinding)
	}

	unresolved := &ReviewReport{Findings: []Finding{finding}, FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "unresolved", Evidence: "still panics"}}}
	if err := validateReviewReport(plan, previous, unresolved); err != nil {
		t.Fatalf("valid unresolved finding report rejected: %v", err)
	}
	invalidStatus := &ReviewReport{FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "fixed", Evidence: "claims fixed"}}}
	if err := validateReviewReport(plan, previous, invalidStatus); err == nil {
		t.Fatal("invalid resolution status was accepted")
	}
}

func TestUnresolvedFindingIDsKeepsCurrentUnresolvedFinding(t *testing.T) {
	finding := Finding{ID: "finding-1"}
	reviews := []ReviewReport{
		{Findings: []Finding{finding}},
		{Findings: []Finding{finding}, FindingResolutions: []FindingResolution{{ID: "finding-1", Status: "unresolved", Evidence: "still broken"}}},
	}
	if _, exists := unresolvedFindingIDs(reviews)["finding-1"]; !exists {
		t.Fatal("current unresolved finding disappeared from active ids")
	}
}

func TestValidateReviewReportRejectsProseVerification(t *testing.T) {
	plan := &DevelopmentPlan{Files: []string{"feature.go"}}
	report := &ReviewReport{Findings: []Finding{{
		ID: "finding-1", Severity: "high", File: "feature.go", Line: 1, Summary: "broken",
		FailureScenario: "input fails", RequiredOutcome: "input works", Verification: []string{"Run the game manually"},
	}}}
	if err := validateReviewReport(plan, nil, report); err == nil {
		t.Fatal("prose finding verification accepted")
	}
}

func TestValidateReviewReportRejectsNilPlan(t *testing.T) {
	if err := validateReviewReport(nil, nil, &ReviewReport{}); err == nil {
		t.Fatal("nil review plan accepted")
	}
}

func TestValidateAddressedFindingsRequiresEveryFindingDisposition(t *testing.T) {
	required := []Finding{{ID: "finding-1"}, {ID: "finding-2"}}
	addressed := []AddressedFinding{{ID: "finding-1", Status: "addressed", Evidence: "added guard"}}
	if err := validateAddressedFindings(required, addressed); err == nil {
		t.Fatal("missing coder finding disposition accepted")
	}
	addressed = append(addressed, AddressedFinding{ID: "finding-2", Status: "not_addressed", Evidence: "requires design decision"})
	if err := validateAddressedFindings(required, addressed); err != nil {
		t.Fatalf("complete finding dispositions rejected: %v", err)
	}
	addressed = append(addressed, AddressedFinding{ID: "finding-3", Status: "addressed", Evidence: "unrequested change"})
	if err := validateAddressedFindings(required, addressed); err == nil {
		t.Fatal("unknown finding disposition accepted")
	}
}

func TestBuildReviewContextPrioritizesLatestTestReport(t *testing.T) {
	report := &DevelopmentTeamReport{
		Plan: &DevelopmentPlan{Goal: "feature"},
		TestReports: []TestReport{
			{Passed: false, Commands: []CommandResult{{Command: "go_test ./a", ExitCode: 1, Output: "old"}}},
			{Passed: false, Commands: []CommandResult{{Command: "go_test ./b", ExitCode: 2, Output: "latest"}}},
		},
		Reviews: []ReviewRound{{StepID: "step-1", Dimensions: []DimensionReview{{Dimension: ReviewDimensionLogic, Report: ReviewReport{Summary: "previous"}}}}},
	}
	previous := []DimensionReview{{Dimension: ReviewDimensionLogic, Report: ReviewReport{Summary: "previous"}}}
	context := buildReviewContext(report, "step-1", []string{"feature.go"}, previous)
	if context.LatestTestReport == nil || context.LatestTestReport.Commands[0].Output != "latest" {
		t.Fatalf("latest test report not prioritized: %#v", context)
	}
	if len(context.PreviousTestSummaries) != 1 || len(context.PreviousReviews) != 1 {
		t.Fatalf("review history not summarized correctly: %#v", context)
	}
	if len(context.ReviewScope) != 1 || context.ReviewScope[0] != "feature.go" {
		t.Fatalf("review scope not preserved: %#v", context)
	}
}
