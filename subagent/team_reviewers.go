package subagent

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type ReviewDimension string

const (
	ReviewDimensionRequirements ReviewDimension = "requirements"
	ReviewDimensionLogic        ReviewDimension = "logic"
	ReviewDimensionSafety       ReviewDimension = "safety"
	ReviewDimensionTests        ReviewDimension = "tests"
)

var defaultReviewDimensions = []ReviewDimension{
	ReviewDimensionRequirements,
	ReviewDimensionLogic,
	ReviewDimensionSafety,
	ReviewDimensionTests,
}

type DimensionReview struct {
	Dimension ReviewDimension `json:"dimension"`
	Report    ReviewReport    `json:"report"`
}

type ReviewRound struct {
	StepID     string            `json:"step_id"`
	Dimensions []DimensionReview `json:"dimensions"`
	Findings   []Finding         `json:"findings"`
	Summary    string            `json:"summary"`
}

type ReviewBundle struct {
	Dimensions []DimensionReview `json:"dimensions"`
	Findings   []Finding         `json:"findings"`
	Summary    string            `json:"summary"`
}

type ReviewTask struct {
	Dimension ReviewDimension `json:"dimension"`
	Context   ReviewContext   `json:"context"`
}

type DevelopmentTeamRoles interface {
	Plan(context.Context, string) (*DevelopmentPlan, error)
	Code(context.Context, CodeTask) (*CodeReport, error)
	Test(context.Context, PlanStep) (*TestReport, error)
	Review(context.Context, ReviewTask) (*ReviewReport, error)
	Verify(context.Context, []string) (*TestReport, error)
}

func validateReviewDimension(dimension ReviewDimension) error {
	switch dimension {
	case ReviewDimensionRequirements, ReviewDimensionLogic, ReviewDimensionSafety, ReviewDimensionTests:
		return nil
	default:
		return fmt.Errorf("unknown review dimension %q", dimension)
	}
}

func reviewerPromptForDimension(dimension ReviewDimension) (string, error) {
	if err := validateReviewDimension(dimension); err != nil {
		return "", err
	}
	var focus string
	switch dimension {
	case ReviewDimensionRequirements:
		focus = `FOCUS: Requirement coverage only.
- Trace every acceptance criterion and requested behavior to current code.
- Look for omitted, substituted, or incorrectly mapped user-visible behavior.
- Do not report internal algorithm defects unless they visibly violate a requirement.`
	case ReviewDimensionLogic:
		focus = `FOCUS: Runtime correctness and state invariants only.
- Check boundary conditions, state transitions, indexing, mutation order, and error paths.
- Use concrete inputs or states that produce a wrong result, panic, corruption, or deadlock.
- Do not report style or merely missing tests.`
	case ReviewDimensionSafety:
		focus = `FOCUS: Security and execution safety only.
- Check scope escapes, unsafe process or network use, filesystem access, symlink issues, unbounded execution, and destructive behavior.
- Report only a concrete safety consequence supported by current code.
- Do not duplicate ordinary functional defects.`
	case ReviewDimensionTests:
		focus = `FOCUS: Verification adequacy only.
- Compare acceptance criteria and risky behavior with current tests and latest command evidence.
- Report a finding only when a concrete defect can escape the current verification.
- The required outcome must name observable coverage, not an implementation patch.`
	}
	return reviewerTeamPrompt + "\n\n" + focus + "\n- Prefix every new finding id with " + strings.ToUpper(string(dimension)) + "-.", nil
}

func reviewDimensionsForContext(reviewContext ReviewContext, fixing bool) []ReviewDimension {
	if !fixing {
		return append([]ReviewDimension(nil), defaultReviewDimensions...)
	}
	active := activeFindingsByDimension(reviewContext.PreviousReviews)
	if len(active) == 0 {
		return nil
	}
	if reviewContext.LatestTestReport != nil && !reviewContext.LatestTestReport.Passed {
		active[ReviewDimensionTests] = nil
	}
	seen := make(map[ReviewDimension]struct{}, len(active))
	for dimension := range active {
		seen[dimension] = struct{}{}
	}
	result := make([]ReviewDimension, 0, len(seen))
	for _, dimension := range defaultReviewDimensions {
		if _, exists := seen[dimension]; exists {
			result = append(result, dimension)
		}
	}
	return result
}

func activeFindingsByDimension(reviews []DimensionReview) map[ReviewDimension][]Finding {
	active := make(map[ReviewDimension]map[string]Finding)
	for _, review := range reviews {
		if active[review.Dimension] == nil {
			active[review.Dimension] = make(map[string]Finding)
		}
		for _, resolution := range review.Report.FindingResolutions {
			if resolution.Status == "resolved" {
				delete(active[review.Dimension], resolution.ID)
			}
		}
		for _, finding := range review.Report.Findings {
			active[review.Dimension][finding.ID] = finding
		}
	}
	result := make(map[ReviewDimension][]Finding)
	for dimension, findings := range active {
		ids := make([]string, 0, len(findings))
		for id := range findings {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			result[dimension] = append(result[dimension], findings[id])
		}
	}
	return result
}

func dimensionReviewHistory(reviews []DimensionReview, dimension ReviewDimension) []ReviewReport {
	var history []ReviewReport
	for _, review := range reviews {
		if review.Dimension == dimension {
			history = append(history, review.Report)
		}
	}
	return history
}

func mergeDimensionReviews(reviews []DimensionReview) ReviewBundle {
	bundle := ReviewBundle{Dimensions: append([]DimensionReview(nil), reviews...)}
	byKey := make(map[string]Finding)
	for _, review := range reviews {
		for _, finding := range review.Report.Findings {
			finding.Dimension = review.Dimension
			finding.Reviewers = uniqueStrings(append(finding.Reviewers, string(review.Dimension)))
			key := findingMergeKey(finding)
			if existing, exists := byKey[key]; exists {
				byKey[key] = mergeFinding(existing, finding)
			} else {
				byKey[key] = finding
			}
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bundle.Findings = append(bundle.Findings, byKey[key])
	}
	if len(bundle.Findings) == 0 {
		bundle.Summary = "all review dimensions are clean"
	} else {
		bundle.Summary = fmt.Sprintf("%d unique findings from %d review dimensions", len(bundle.Findings), len(reviews))
	}
	return bundle
}

func findingMergeKey(finding Finding) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(finding.File),
		fmt.Sprint(finding.Line),
		normalizeFindingText(finding.Summary),
	}, "\x00"))
}

func normalizeFindingText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func mergeFinding(existing Finding, incoming Finding) Finding {
	if severityRank(incoming.Severity) > severityRank(existing.Severity) {
		existing.Severity = incoming.Severity
	}
	existing.Verification = uniqueStrings(append(existing.Verification, incoming.Verification...))
	existing.Reviewers = uniqueStrings(append(existing.Reviewers, incoming.Reviewers...))
	existing.Reviewers = uniqueStrings(append(existing.Reviewers, string(existing.Dimension), string(incoming.Dimension)))
	return existing
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
