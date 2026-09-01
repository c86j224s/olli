package subagent

import (
	"context"
	"fmt"
	"strings"
)

type TeamPhase string

const (
	TeamPhasePlanning  TeamPhase = "planning"
	TeamPhaseCoding    TeamPhase = "coding"
	TeamPhaseTesting   TeamPhase = "testing"
	TeamPhaseReviewing TeamPhase = "reviewing"
	TeamPhaseFixing    TeamPhase = "fixing"
	TeamPhaseVerifying TeamPhase = "verifying"
	TeamPhaseDone      TeamPhase = "done"
	TeamPhaseFailed    TeamPhase = "failed"
)

const defaultMaxTeamFixRounds = 2

type CodeTask struct {
	Goal        string    `json:"goal"`
	Step        PlanStep  `json:"step"`
	ReviewFixes []Finding `json:"review_fixes,omitempty"`
	Attempt     int       `json:"attempt"`
}

type CodeReport struct {
	StepID       string   `json:"step_id"`
	ChangedFiles []string `json:"changed_files"`
	Completed    []string `json:"completed"`
	Unresolved   []string `json:"unresolved"`
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
	Severity        string `json:"severity"`
	File            string `json:"file"`
	Line            int    `json:"line"`
	Summary         string `json:"summary"`
	FailureScenario string `json:"failure_scenario"`
}

type ReviewReport struct {
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}

type DevelopmentTeamReport struct {
	Status       string           `json:"status"`
	Phase        TeamPhase        `json:"phase"`
	Plan         *DevelopmentPlan `json:"plan,omitempty"`
	CodeReports  []CodeReport     `json:"code_reports,omitempty"`
	TestReports  []TestReport     `json:"test_reports,omitempty"`
	Reviews      []ReviewReport   `json:"reviews,omitempty"`
	Verification *TestReport      `json:"verification,omitempty"`
	Transitions  []TeamPhase      `json:"transitions"`
	Failure      string           `json:"failure,omitempty"`
	FixRounds    int              `json:"fix_rounds"`
}

type DevelopmentTeamRoles interface {
	Plan(context.Context, string) (*DevelopmentPlan, error)
	Code(context.Context, CodeTask) (*CodeReport, error)
	Test(context.Context, PlanStep) (*TestReport, error)
	Review(context.Context, *DevelopmentPlan, []CodeReport) (*ReviewReport, error)
	Verify(context.Context, []string) (*TestReport, error)
}

type DevelopmentTeamRunner struct {
	roles        DevelopmentTeamRoles
	maxFixRounds int
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
	return &DevelopmentTeamRunner{roles: roles, maxFixRounds: maxFixRounds}, nil
}

func (r *DevelopmentTeamRunner) Run(ctx context.Context, objective string) DevelopmentTeamReport {
	report := DevelopmentTeamReport{Status: "FAILED", Phase: TeamPhasePlanning}
	transition := func(phase TeamPhase) {
		report.Phase = phase
		report.Transitions = append(report.Transitions, phase)
	}
	fail := func(format string, args ...any) DevelopmentTeamReport {
		report.Status = "FAILED"
		report.Failure = fmt.Sprintf(format, args...)
		transition(TeamPhaseFailed)
		return report
	}

	if ctx == nil {
		return fail("development team context is required")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return fail("development team objective is required")
	}

	transition(TeamPhasePlanning)
	plan, err := r.roles.Plan(ctx, objective)
	if err != nil {
		return fail("planning failed: %v", err)
	}
	if err := validateDevelopmentPlan(plan); err != nil {
		return fail("planning produced an invalid plan: %v", err)
	}
	report.Plan = plan

	for _, step := range plan.Steps {
		transition(TeamPhaseCoding)
		codeReport, err := r.roles.Code(ctx, CodeTask{Goal: plan.Goal, Step: step, Attempt: 1})
		if err != nil {
			return fail("coding %s failed: %v", step.ID, err)
		}
		if err := validateCodeReport(step, codeReport); err != nil {
			return fail("coding %s produced an invalid report: %v", step.ID, err)
		}
		report.CodeReports = append(report.CodeReports, *codeReport)

		if len(step.Verification) > 0 {
			transition(TeamPhaseTesting)
			testReport, err := r.roles.Test(ctx, step)
			if err != nil {
				return fail("testing %s failed: %v", step.ID, err)
			}
			if err := validateTestReport(testReport); err != nil {
				return fail("testing %s produced an invalid report: %v", step.ID, err)
			}
			report.TestReports = append(report.TestReports, *testReport)
			if !testReport.Passed {
				return fail("testing %s did not pass: %s", step.ID, testReport.Summary)
			}
		}
	}

	for {
		transition(TeamPhaseReviewing)
		review, err := r.roles.Review(ctx, plan, report.CodeReports)
		if err != nil {
			return fail("review failed: %v", err)
		}
		if err := validateReviewReport(plan, review); err != nil {
			return fail("review produced an invalid report: %v", err)
		}
		report.Reviews = append(report.Reviews, *review)
		if len(review.Findings) == 0 {
			break
		}
		if report.FixRounds >= r.maxFixRounds {
			return fail("review still has %d findings after %d fix rounds", len(review.Findings), report.FixRounds)
		}

		report.FixRounds++
		transition(TeamPhaseFixing)
		fixStep := PlanStep{
			ID:           fmt.Sprintf("step-review-fix-%d", report.FixRounds),
			Objective:    "Fix confirmed review findings",
			AllowedFiles: findingFiles(review.Findings),
			Acceptance:   findingSummaries(review.Findings),
			Verification: plan.FinalVerification,
		}
		codeReport, err := r.roles.Code(ctx, CodeTask{Goal: plan.Goal, Step: fixStep, ReviewFixes: review.Findings, Attempt: report.FixRounds + 1})
		if err != nil {
			return fail("review fix round %d failed: %v", report.FixRounds, err)
		}
		if err := validateCodeReport(fixStep, codeReport); err != nil {
			return fail("review fix round %d produced an invalid report: %v", report.FixRounds, err)
		}
		report.CodeReports = append(report.CodeReports, *codeReport)

		transition(TeamPhaseTesting)
		testReport, err := r.roles.Test(ctx, fixStep)
		if err != nil {
			return fail("review fix testing failed: %v", err)
		}
		if err := validateTestReport(testReport); err != nil {
			return fail("review fix testing produced an invalid report: %v", err)
		}
		report.TestReports = append(report.TestReports, *testReport)
		if !testReport.Passed {
			return fail("review fix testing did not pass: %s", testReport.Summary)
		}
	}

	transition(TeamPhaseVerifying)
	verification, err := r.roles.Verify(ctx, plan.FinalVerification)
	if err != nil {
		return fail("final verification failed: %v", err)
	}
	if err := validateTestReport(verification); err != nil {
		return fail("final verification produced an invalid report: %v", err)
	}
	if err := requireVerificationCommands(plan.FinalVerification, verification); err != nil {
		return fail("final verification evidence is incomplete: %v", err)
	}
	report.Verification = verification
	if !verification.Passed {
		return fail("final verification did not pass: %s", verification.Summary)
	}

	report.Status = "SUCCESS"
	transition(TeamPhaseDone)
	return report
}

func validateCodeReport(step PlanStep, report *CodeReport) error {
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
	if len(report.Unresolved) != 0 {
		return fmt.Errorf("code report has unresolved work: %s", strings.Join(report.Unresolved, ", "))
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

func validateReviewReport(plan *DevelopmentPlan, report *ReviewReport) error {
	if report == nil {
		return fmt.Errorf("review report is required")
	}
	allowed := make(map[string]struct{}, len(plan.Files))
	for _, path := range plan.Files {
		allowed[path] = struct{}{}
	}
	for index := range report.Findings {
		finding := &report.Findings[index]
		path, err := normalizePlanPath(finding.File)
		if err != nil {
			return fmt.Errorf("review finding file %q: %w", finding.File, err)
		}
		finding.File = path
		if _, exists := allowed[path]; !exists {
			return fmt.Errorf("review finding file %q is outside the planned file set", path)
		}
		if finding.Line <= 0 || strings.TrimSpace(finding.Summary) == "" || strings.TrimSpace(finding.FailureScenario) == "" {
			return fmt.Errorf("review finding for %q requires line, summary, and failure_scenario", path)
		}
	}
	return nil
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
