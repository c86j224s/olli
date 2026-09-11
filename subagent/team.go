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
	Status       string             `json:"status"`
	Phase        TeamPhase          `json:"phase"`
	Plan         *DevelopmentPlan   `json:"plan,omitempty"`
	CodeReports  []CodeReport       `json:"code_reports,omitempty"`
	TestReports  []TestReport       `json:"test_reports,omitempty"`
	Reviews      []ReviewReport     `json:"reviews,omitempty"`
	Verification *TestReport        `json:"verification,omitempty"`
	Transitions  []TeamPhase        `json:"transitions"`
	Failure      string             `json:"failure,omitempty"`
	FixRounds    int                `json:"fix_rounds"`
	Graph        *agentgraph.Result `json:"graph,omitempty"`
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
	return r.runGraph(ctx, objective)
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
