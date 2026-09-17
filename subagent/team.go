package subagent

import (
	"context"
	"fmt"
	"strings"

	agentgraph "github.com/c86j224s/olli/graph"
)

type TeamPhase string

const (
	TeamPhasePlanning  TeamPhase = "planning"
	TeamPhaseCoding    TeamPhase = "coding"
	TeamPhasePreflight TeamPhase = "preflight"
	TeamPhaseTesting   TeamPhase = "testing"
	TeamPhaseReviewing TeamPhase = "reviewing"
	TeamPhaseFixing    TeamPhase = "fixing"
	TeamPhaseVerifying TeamPhase = "verifying"
	TeamPhaseDone      TeamPhase = "done"
	TeamPhaseFailed    TeamPhase = "failed"
)

const defaultMaxTeamFixRounds = 2

type CodeTask struct {
	Goal            string            `json:"goal"`
	Step            PlanStep          `json:"step"`
	ReadOnlyFiles   []string          `json:"read_only_files,omitempty"`
	SourceSnapshots map[string]string `json:"source_snapshots,omitempty"`
	ReviewFixes     []Finding         `json:"review_fixes,omitempty"`
	Attempt         int               `json:"attempt"`
}

type CodeReport struct {
	StepID            string             `json:"step_id"`
	ChangedFiles      []string           `json:"changed_files"`
	Completed         []string           `json:"completed"`
	Unresolved        []string           `json:"unresolved"`
	AddressedFindings []AddressedFinding `json:"addressed_findings,omitempty"`
	EvidenceDerived   bool               `json:"evidence_derived,omitempty"`
}

type AddressedFinding struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type CommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
}

type TestReport struct {
	Passed   bool            `json:"passed"`
	Commands []CommandResult `json:"commands"`
	Summary  string          `json:"summary"`
}

type Finding struct {
	ID              string          `json:"id"`
	Dimension       ReviewDimension `json:"-"`
	Reviewers       []string        `json:"-"`
	Severity        string          `json:"severity"`
	File            string          `json:"file"`
	Line            int             `json:"line"`
	Summary         string          `json:"summary"`
	FailureScenario string          `json:"failure_scenario"`
	RequiredOutcome string          `json:"required_outcome"`
	Verification    []string        `json:"verification"`
}

type FindingResolution struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}

type ReviewReport struct {
	Findings           []Finding           `json:"findings"`
	FindingResolutions []FindingResolution `json:"finding_resolutions"`
	Summary            string              `json:"summary"`
}

type DevelopmentTeamReport struct {
	Status       string             `json:"status"`
	Phase        TeamPhase          `json:"phase"`
	Plan         *DevelopmentPlan   `json:"plan,omitempty"`
	Planning     *PlanningReport    `json:"planning,omitempty"`
	CodeReports  []CodeReport       `json:"code_reports,omitempty"`
	TestReports  []TestReport       `json:"test_reports,omitempty"`
	Preflights   []TestReport       `json:"preflights,omitempty"`
	Reviews      []ReviewRound      `json:"reviews,omitempty"`
	Verification *TestReport        `json:"verification,omitempty"`
	Transitions  []TeamPhase        `json:"transitions"`
	Failure      string             `json:"failure,omitempty"`
	FixRounds    int                `json:"fix_rounds"`
	Graph        *agentgraph.Result `json:"graph,omitempty"`
}

type ReviewContext struct {
	StepID                string            `json:"step_id"`
	Plan                  *DevelopmentPlan  `json:"plan"`
	CodeReports           []CodeReport      `json:"code_reports"`
	LatestTestReport      *TestReport       `json:"latest_test_report,omitempty"`
	PreviousTestSummaries []TestSummary     `json:"previous_test_summaries"`
	PreviousReviews       []DimensionReview `json:"previous_reviews"`
	ReviewScope           []string          `json:"review_scope,omitempty"`
	SourceSnapshots       map[string]string `json:"source_snapshots,omitempty"`
}

type TestSummary struct {
	Round    int      `json:"round"`
	Passed   bool     `json:"passed"`
	Commands []string `json:"commands"`
}

type DevelopmentTeamRunner struct {
	roles        DevelopmentTeamRoles
	maxFixRounds int
	workspace    string
}

func NewDevelopmentTeamRunner(roles DevelopmentTeamRoles, maxFixRounds int) (*DevelopmentTeamRunner, error) {
	if roles == nil {
		return nil, fmt.Errorf("development team roles are required")
	}
	if maxFixRounds <= 0 {
		maxFixRounds = defaultMaxTeamFixRounds
	}
	if maxFixRounds > defaultMaxTeamFixRounds {
		return nil, fmt.Errorf("development team fix rounds cannot exceed %d", defaultMaxTeamFixRounds)
	}
	workspace := ""
	if provider, ok := roles.(interface{ TeamWorkspace() string }); ok {
		workspace = strings.TrimSpace(provider.TeamWorkspace())
	}
	return &DevelopmentTeamRunner{roles: roles, maxFixRounds: maxFixRounds, workspace: workspace}, nil
}

func (r *DevelopmentTeamRunner) WithWorkspace(workspace string) *DevelopmentTeamRunner {
	if r == nil {
		return nil
	}
	clone := *r
	clone.workspace = strings.TrimSpace(workspace)
	return &clone
}

func (r *DevelopmentTeamRunner) Run(ctx context.Context, objective string) DevelopmentTeamReport {
	return r.runGraph(ctx, objective)
}

func validateCodeReport(step PlanStep, reviewFixes []Finding, report *CodeReport) error {
	if report == nil {
		return fmt.Errorf("code report is required")
	}
	if report.StepID != step.ID {
		return fmt.Errorf("code report step id %q does not match %q", report.StepID, step.ID)
	}
	report.ChangedFiles = uniqueStrings(report.ChangedFiles)
	report.Completed = uniqueStrings(report.Completed)
	report.Unresolved = uniqueStrings(report.Unresolved)
	if len(report.ChangedFiles) == 0 {
		return fmt.Errorf("code report requires changed_files")
	}
	if len(report.Completed) == 0 {
		return fmt.Errorf("code report requires completed outcomes")
	}
	if len(report.Unresolved) != 0 && !report.EvidenceDerived {
		return fmt.Errorf("code report has unresolved work: %s", strings.Join(report.Unresolved, ", "))
	}
	if report.EvidenceDerived && len(report.Unresolved) == 0 {
		return fmt.Errorf("evidence-derived code report must require verification")
	}
	if err := validateAddressedFindings(reviewFixes, report.AddressedFindings); err != nil {
		return err
	}
	allowed := make(map[string]struct{}, len(step.AllowedFiles))
	for _, path := range step.AllowedFiles {
		allowed[path] = struct{}{}
	}
	for index, path := range report.ChangedFiles {
		normalized, err := normalizePlanPath(path)
		if err != nil {
			return fmt.Errorf("changed file %q: %w", path, err)
		}
		report.ChangedFiles[index] = normalized
		if _, exists := allowed[normalized]; !exists {
			return fmt.Errorf("changed file %q is outside allowed_files", normalized)
		}
	}
	return nil
}

func validateAddressedFindings(required []Finding, addressed []AddressedFinding) error {
	if len(required) == 0 {
		return nil
	}
	byID := make(map[string]AddressedFinding, len(addressed))
	for _, item := range addressed {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Evidence) == "" {
			return fmt.Errorf("addressed finding requires id and evidence")
		}
		if item.Status != "addressed" && item.Status != "not_addressed" {
			return fmt.Errorf("addressed finding %q has invalid status %q", item.ID, item.Status)
		}
		if _, duplicate := byID[item.ID]; duplicate {
			return fmt.Errorf("addressed finding %q is duplicated", item.ID)
		}
		byID[item.ID] = item
	}
	requiredIDs := make(map[string]struct{}, len(required))
	for _, finding := range required {
		requiredIDs[finding.ID] = struct{}{}
		if _, exists := byID[finding.ID]; !exists {
			return fmt.Errorf("coder did not report disposition for finding %q", finding.ID)
		}
	}
	for id := range byID {
		if _, exists := requiredIDs[id]; !exists {
			return fmt.Errorf("coder reported disposition for unknown finding %q", id)
		}
	}
	return nil
}

func validateTestReport(report *TestReport) error {
	if report == nil {
		return fmt.Errorf("test report is required")
	}
	if len(report.Commands) == 0 {
		return fmt.Errorf("test report requires command evidence")
	}
	for _, command := range report.Commands {
		if strings.TrimSpace(command.Command) == "" {
			return fmt.Errorf("test command is required")
		}
		if report.Passed && command.ExitCode != 0 {
			return fmt.Errorf("test report cannot pass with exit code %d", command.ExitCode)
		}
	}
	return nil
}

func requireVerificationCommands(required []string, report *TestReport) error {
	executed := make(map[string]struct{}, len(report.Commands))
	for _, command := range report.Commands {
		executed[strings.TrimSpace(command.Command)] = struct{}{}
	}
	for _, command := range required {
		command = strings.TrimSpace(command)
		if _, exists := executed[command]; !exists {
			return fmt.Errorf("required command %q was not executed", command)
		}
	}
	return nil
}

func unresolvedFindings(reviews []ReviewReport) map[string]Finding {
	active := make(map[string]Finding)
	for _, review := range reviews {
		for _, resolution := range review.FindingResolutions {
			if resolution.Status == "resolved" {
				delete(active, resolution.ID)
			}
		}
		for _, finding := range review.Findings {
			active[finding.ID] = finding
		}
	}
	return active
}

func unresolvedFindingIDs(reviews []ReviewReport) map[string]struct{} {
	active := unresolvedFindings(reviews)
	ids := make(map[string]struct{}, len(active))
	for id := range active {
		ids[id] = struct{}{}
	}
	return ids
}

func validateReviewReport(plan *DevelopmentPlan, previous []ReviewReport, report *ReviewReport) error {
	return validateDimensionReviewReport(plan, "", previous, report)
}

func validateDimensionReviewReport(plan *DevelopmentPlan, dimension ReviewDimension, previous []ReviewReport, report *ReviewReport) error {
	if report == nil {
		return fmt.Errorf("review report is required")
	}
	if plan == nil {
		return fmt.Errorf("review plan is required")
	}
	allowed := make(map[string]struct{}, len(plan.Files))
	for _, path := range plan.Files {
		allowed[path] = struct{}{}
	}
	seenIDs := make(map[string]int, len(report.Findings))
	for index := range report.Findings {
		finding := &report.Findings[index]
		finding.ID = strings.TrimSpace(finding.ID)
		if finding.ID == "" {
			return fmt.Errorf("review finding id is required")
		}
		if dimension != "" {
			prefix := strings.ToUpper(string(dimension)) + "-"
			if !strings.HasPrefix(strings.ToUpper(finding.ID), prefix) {
				finding.ID = prefix + finding.ID
			}
			finding.Dimension = dimension
			finding.Reviewers = uniqueStrings(append(finding.Reviewers, string(dimension)))
		}
		baseID := finding.ID
		seenIDs[baseID]++
		if seenIDs[baseID] > 1 {
			finding.ID = fmt.Sprintf("%s-%d", baseID, seenIDs[baseID])
		}
		path, err := normalizePlanPath(finding.File)
		if err != nil {
			return fmt.Errorf("review finding file %q: %w", finding.File, err)
		}
		finding.File = path
		if _, exists := allowed[path]; !exists {
			return fmt.Errorf("review finding file %q is outside the planned file set", path)
		}
		finding.Verification = uniqueStrings(finding.Verification)
		if finding.Line <= 0 || strings.TrimSpace(finding.Summary) == "" || strings.TrimSpace(finding.FailureScenario) == "" || strings.TrimSpace(finding.RequiredOutcome) == "" || len(finding.Verification) == 0 {
			return fmt.Errorf("review finding %q requires line, summary, failure_scenario, required_outcome, and verification", finding.ID)
		}
		for verificationIndex, command := range finding.Verification {
			canonical, err := normalizeVerificationCommand(command)
			if err != nil {
				return fmt.Errorf("review finding %q verification %q: %w", finding.ID, command, err)
			}
			finding.Verification[verificationIndex] = canonical
		}
	}
	previousFindings := unresolvedFindings(previous)
	previousIDs := make(map[string]struct{}, len(previousFindings))
	for id := range previousFindings {
		previousIDs[id] = struct{}{}
	}
	resolutionByID := make(map[string]string, len(report.FindingResolutions))
	resolutionIndexByID := make(map[string]int, len(report.FindingResolutions))
	uniqueResolutions := report.FindingResolutions[:0]
	for index := range report.FindingResolutions {
		resolution := &report.FindingResolutions[index]
		resolution.ID = strings.TrimSpace(resolution.ID)
		if _, exists := previousIDs[resolution.ID]; !exists && dimension != "" {
			prefix := strings.ToUpper(string(dimension)) + "-"
			if !strings.HasPrefix(strings.ToUpper(resolution.ID), prefix) {
				resolution.ID = prefix + resolution.ID
			}
		}
		if _, exists := previousIDs[resolution.ID]; !exists {
			if matched := matchingKnownResolutionID(resolutionByID, resolution.ID); matched != "" {
				resolution.ID = matched
			} else if matched := matchingPriorFindingID(previousIDs, resolution.ID); matched != "" {
				resolution.ID = matched
			} else if len(previousIDs) == 1 {
				for onlyID := range previousIDs {
					resolution.ID = onlyID
				}
			} else {
				continue
			}
		}
		if resolution.Status != "resolved" && resolution.Status != "unresolved" {
			return fmt.Errorf("resolution for finding %q has invalid status %q", resolution.ID, resolution.Status)
		}
		if strings.TrimSpace(resolution.Evidence) == "" {
			return fmt.Errorf("resolution for finding %q lacks evidence", resolution.ID)
		}
		if priorIndex, duplicate := resolutionIndexByID[resolution.ID]; duplicate {
			prior := &uniqueResolutions[priorIndex]
			prior.Evidence = strings.TrimSpace(prior.Evidence + "; " + resolution.Evidence)
			if prior.Status == "unresolved" || resolution.Status == "unresolved" {
				prior.Status = "unresolved"
				resolutionByID[resolution.ID] = "unresolved"
			}
			continue
		}
		resolutionIndexByID[resolution.ID] = len(uniqueResolutions)
		uniqueResolutions = append(uniqueResolutions, *resolution)
		resolutionByID[resolution.ID] = resolution.Status
	}
	report.FindingResolutions = uniqueResolutions
	for id, status := range resolutionByID {
		if status != "resolved" {
			continue
		}
		if _, remains := seenIDs[id]; !remains {
			continue
		}
		filtered := report.Findings[:0]
		for _, finding := range report.Findings {
			if finding.ID != id {
				filtered = append(filtered, finding)
			}
		}
		report.Findings = filtered
		delete(seenIDs, id)
	}
	for id := range previousIDs {
		status, exists := resolutionByID[id]
		if !exists {
			status = "unresolved"
			resolutionByID[id] = status
			report.FindingResolutions = append(report.FindingResolutions, FindingResolution{
				ID:       id,
				Status:   status,
				Evidence: "reviewer omitted the required disposition; host conservatively retained the finding",
			})
		}
		_, remains := seenIDs[id]
		if status == "unresolved" && !remains {
			finding := previousFindings[id]
			finding.Dimension = dimension
			finding.Reviewers = uniqueStrings(append(finding.Reviewers, string(dimension)))
			report.Findings = append(report.Findings, finding)
			seenIDs[id] = 1
			remains = true
		}
		if status == "resolved" && remains {
			return fmt.Errorf("resolved finding %q remained after normalization", id)
		}
	}
	return nil
}

func matchingKnownResolutionID(known map[string]string, candidate string) string {
	ids := make(map[string]struct{}, len(known))
	for id := range known {
		ids[id] = struct{}{}
	}
	return matchingPriorFindingID(ids, candidate)
}

func matchingPriorFindingID(previous map[string]struct{}, candidate string) string {
	candidateKey := findingIdentityKey(candidate)
	if candidateKey == "" {
		return ""
	}
	matched := ""
	for id := range previous {
		if findingIdentityKey(id) != candidateKey {
			continue
		}
		if matched != "" {
			return ""
		}
		matched = id
	}
	return matched
}

func findingIdentityKey(id string) string {
	parts := strings.Split(strings.ToUpper(strings.TrimSpace(id)), "-")
	for len(parts) > 0 && isReviewDimensionToken(parts[0]) {
		parts = parts[1:]
	}
	return strings.Join(parts, "-")
}

func isReviewDimensionToken(value string) bool {
	switch value {
	case "REQUIREMENTS", "LOGIC", "SAFETY", "TESTS":
		return true
	default:
		return false
	}
}

func findingFiles(findings []Finding) []string {
	files := make([]string, 0, len(findings))
	for _, finding := range findings {
		files = append(files, finding.File)
	}
	return uniqueStrings(files)
}

func findingSummaries(findings []Finding) []string {
	summaries := make([]string, 0, len(findings))
	for _, finding := range findings {
		summaries = append(summaries, finding.Summary)
	}
	return uniqueStrings(summaries)
}
