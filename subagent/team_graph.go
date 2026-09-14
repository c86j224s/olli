package subagent

import (
	"context"
	"fmt"
	"strings"

	agentgraph "github.com/c86j224s/olli/graph"
)

const (
	teamNodePlanning  = "planning"
	teamNodeCoding    = "coding"
	teamNodePreflight = "preflight"
	teamNodeTesting   = "testing"
	teamNodeReviewing = "reviewing"
	teamNodeFixing    = "fixing"
	teamNodeVerifying = "verifying"
)

type developmentTeamState struct {
	runner        *DevelopmentTeamRunner
	objective     string
	report        *DevelopmentTeamReport
	stepIndex     int
	currentStep   PlanStep
	fixing        bool
	reviewHistory []DimensionReview
}

func (r *DevelopmentTeamRunner) runGraph(ctx context.Context, objective string) DevelopmentTeamReport {
	report := DevelopmentTeamReport{Status: "FAILED", Phase: TeamPhasePlanning}
	if ctx == nil {
		report.Phase = TeamPhaseFailed
		report.Failure = "development team context is required"
		report.Transitions = []TeamPhase{TeamPhaseFailed}
		return report
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		report.Phase = TeamPhaseFailed
		report.Failure = "development team objective is required"
		report.Transitions = []TeamPhase{TeamPhaseFailed}
		return report
	}

	state := &developmentTeamState{runner: r, objective: objective, report: &report}
	definition := developmentTeamGraphDefinition()
	policy := agentgraph.Policy{
		MaxTransitions: 32,
		MaxNodeVisits:  8,
		NodeVisitLimit: map[string]int{
			teamNodePlanning:  1,
			teamNodeCoding:    maxPlanSteps,
			teamNodePreflight: maxPlanSteps + defaultMaxTeamFixRounds,
			teamNodeTesting:   maxPlanSteps + defaultMaxTeamFixRounds,
			teamNodeReviewing: defaultMaxTeamFixRounds*2 + 1,
			teamNodeFixing:    defaultMaxTeamFixRounds,
			teamNodeVerifying: 1,
		},
	}
	graphRunner, err := agentgraph.NewRunner(definition, policy)
	if err != nil {
		return state.fail("development team graph is invalid: %v", err)
	}
	graphResult := graphRunner.Run(ctx, state)
	report.Graph = &graphResult
	if graphResult.Status != agentgraph.StatusSucceeded {
		if report.Failure == "" {
			report.Failure = graphResult.Failure
		}
		if graphResult.Status == agentgraph.StatusCancelled {
			report.Failure = "development team was cancelled"
		} else if graphResult.Status == agentgraph.StatusTimedOut {
			report.Failure = "development team timed out"
		}
		state.transition(TeamPhaseFailed)
		return report
	}
	report.Status = "SUCCESS"
	state.transition(TeamPhaseDone)
	return report
}

func developmentTeamGraphDefinition() agentgraph.Definition {
	return agentgraph.Definition{
		ID:    "development-team",
		Start: teamNodePlanning,
		Nodes: map[string]agentgraph.Node{
			teamNodePlanning:  agentgraph.NodeFunc(runTeamPlanningNode),
			teamNodeCoding:    agentgraph.NodeFunc(runTeamCodingNode),
			teamNodePreflight: agentgraph.NodeFunc(runTeamPreflightNode),
			teamNodeTesting:   agentgraph.NodeFunc(runTeamTestingNode),
			teamNodeReviewing: agentgraph.NodeFunc(runTeamReviewingNode),
			teamNodeFixing:    agentgraph.NodeFunc(runTeamFixingNode),
			teamNodeVerifying: agentgraph.NodeFunc(runTeamVerifyingNode),
		},
		Edges: []agentgraph.Edge{
			{From: teamNodePlanning, Route: "planned", To: teamNodeCoding},
			{From: teamNodeCoding, Route: "preflight", To: teamNodePreflight},
			{From: teamNodePreflight, Route: "test", To: teamNodeTesting},
			{From: teamNodePreflight, Route: "review", To: teamNodeReviewing},
			{From: teamNodePreflight, Route: "fix", To: teamNodeFixing},
			{From: teamNodeCoding, Route: "code", To: teamNodeCoding},
			{From: teamNodeTesting, Route: "code", To: teamNodeCoding},
			{From: teamNodeTesting, Route: "review", To: teamNodeReviewing},
			{From: teamNodeReviewing, Route: "fix", To: teamNodeFixing},
			{From: teamNodeReviewing, Route: "code", To: teamNodeCoding},
			{From: teamNodeReviewing, Route: "verify", To: teamNodeVerifying},
			{From: teamNodeFixing, Route: "preflight", To: teamNodePreflight},
			{From: teamNodeVerifying, Route: "done", To: agentgraph.End},
		},
	}
}

func teamState(raw agentgraph.State) (*developmentTeamState, error) {
	state, ok := raw.(*developmentTeamState)
	if !ok || state == nil || state.runner == nil || state.report == nil {
		return nil, fmt.Errorf("development team graph state is invalid")
	}
	return state, nil
}

func runTeamPlanningNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhasePlanning)
	plan, err := state.runner.roles.Plan(ctx, state.objective)
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("planning failed: %v", err)
	}
	if err := validateDevelopmentPlan(plan); err != nil {
		return agentgraph.NodeResult{}, state.nodeError("planning produced an invalid plan: %v", err)
	}
	state.report.Plan = plan
	state.stepIndex = 0
	return agentgraph.NodeResult{Route: "planned"}, nil
}

func runTeamCodingNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhaseCoding)
	if state.report.Plan == nil || state.stepIndex >= len(state.report.Plan.Steps) {
		return agentgraph.NodeResult{}, state.nodeError("coding step index is invalid")
	}
	step := state.report.Plan.Steps[state.stepIndex]
	state.currentStep = step
	state.fixing = false
	codeReport, err := state.runner.roles.Code(ctx, CodeTask{Goal: state.report.Plan.Goal, Step: step, Attempt: 1})
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("coding %s failed: %v", step.ID, err)
	}
	if err := validateCodeReport(step, nil, codeReport); err != nil {
		return agentgraph.NodeResult{}, state.nodeError("coding %s produced an invalid report: %v", step.ID, err)
	}
	state.report.CodeReports = append(state.report.CodeReports, *codeReport)
	if codeReport.EvidenceDerived && len(step.Verification) == 0 {
		step.Verification = state.report.Plan.FinalVerification
		state.currentStep = step
	}
	return agentgraph.NodeResult{Route: "preflight"}, nil
}

func runTeamPreflightNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhasePreflight)
	preflight := &TestReport{Passed: true, Commands: []CommandResult{{Command: "static_preflight", ExitCode: 0, Output: "static preflight skipped because no workspace was configured"}}, Summary: "static preflight skipped"}
	if strings.TrimSpace(state.runner.workspace) != "" {
		preflight = runStaticPreflight(ctx, state.runner.workspace, state.currentStep.AllowedFiles)
	}
	state.report.Preflights = append(state.report.Preflights, *preflight)
	if !preflight.Passed {
		if state.report.FixRounds >= state.runner.maxFixRounds {
			return agentgraph.NodeResult{}, state.nodeError("static preflight still failed after %d fix rounds: %s", state.report.FixRounds, preflight.Commands[0].Output)
		}
		failedStepID := state.currentStep.ID
		finding := preflightFinding(state.currentStep, preflight)
		state.report.FixRounds++
		state.currentStep = PlanStep{
			ID:           fmt.Sprintf("step-review-fix-%d", state.report.FixRounds),
			Objective:    "Fix deterministic static preflight failure",
			AllowedFiles: findingFiles([]Finding{finding}),
			Acceptance:   findingSummaries([]Finding{finding}),
			Verification: state.report.Plan.FinalVerification,
		}
		state.fixing = true
		staticReview := DimensionReview{Dimension: ReviewDimensionTests, Report: ReviewReport{Findings: []Finding{finding}, Summary: "deterministic static preflight failure"}}
		state.reviewHistory = []DimensionReview{staticReview}
		state.report.Reviews = append(state.report.Reviews, ReviewRound{
			StepID:     failedStepID,
			Dimensions: []DimensionReview{staticReview},
			Findings:   []Finding{finding},
			Summary:    "deterministic static preflight failure",
		})
		return agentgraph.NodeResult{Route: "fix"}, nil
	}
	if len(state.currentStep.Verification) > 0 {
		return agentgraph.NodeResult{Route: "test"}, nil
	}
	return agentgraph.NodeResult{Route: "review"}, nil
}

func preflightFinding(step PlanStep, report *TestReport) Finding {
	file := "unknown"
	if len(step.AllowedFiles) > 0 {
		file = step.AllowedFiles[0]
	}
	output := "static preflight failed"
	if report != nil && len(report.Commands) > 0 && strings.TrimSpace(report.Commands[0].Output) != "" {
		output = report.Commands[0].Output
	}
	return Finding{
		ID:              "TESTS-STATIC-PREFLIGHT",
		Dimension:       ReviewDimensionTests,
		Reviewers:       []string{string(ReviewDimensionTests)},
		Severity:        "high",
		File:            file,
		Line:            1,
		Summary:         "Static parse or type checking failed",
		FailureScenario: output,
		RequiredOutcome: "All changed Go files parse and type-check successfully before semantic review",
		Verification:    []string{"go_test ./...", "go_vet ./..."},
	}
}

func runTeamTestingNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhaseTesting)
	testReport, err := state.runner.roles.Test(ctx, state.currentStep)
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("testing %s failed: %v", state.currentStep.ID, err)
	}
	if err := validateTestReport(testReport); err != nil {
		return agentgraph.NodeResult{}, state.nodeError("testing %s produced an invalid report: %v", state.currentStep.ID, err)
	}
	state.report.TestReports = append(state.report.TestReports, *testReport)
	if !testReport.Passed {
		if state.report.FixRounds >= state.runner.maxFixRounds {
			return agentgraph.NodeResult{}, state.nodeError("testing %s still failed after %d fix rounds: %s", state.currentStep.ID, state.report.FixRounds, testReport.Summary)
		}
		return agentgraph.NodeResult{Route: "review"}, nil
	}
	if state.fixing {
		return agentgraph.NodeResult{Route: "review"}, nil
	}
	state.stepIndex++
	if state.stepIndex < len(state.report.Plan.Steps) {
		return agentgraph.NodeResult{Route: "code"}, nil
	}
	return agentgraph.NodeResult{Route: "review"}, nil
}

func buildReviewContext(report *DevelopmentTeamReport, stepID string, reviewScope []string, previousReviews []DimensionReview) ReviewContext {
	context := ReviewContext{
		StepID:          stepID,
		Plan:            report.Plan,
		CodeReports:     append([]CodeReport(nil), report.CodeReports...),
		PreviousReviews: append([]DimensionReview(nil), previousReviews...),
		ReviewScope:     uniqueStrings(reviewScope),
	}
	if len(report.TestReports) > 0 {
		latestIndex := len(report.TestReports) - 1
		context.LatestTestReport = &report.TestReports[latestIndex]
		for index, testReport := range report.TestReports[:latestIndex] {
			commands := make([]string, 0, len(testReport.Commands))
			for _, command := range testReport.Commands {
				commands = append(commands, command.Command)
			}
			context.PreviousTestSummaries = append(context.PreviousTestSummaries, TestSummary{Round: index + 1, Passed: testReport.Passed, Commands: commands})
		}
	}
	return context
}

func runTeamReviewingNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhaseReviewing)
	reviewContext := buildReviewContext(state.report, state.currentStep.ID, state.currentStep.AllowedFiles, state.reviewHistory)
	dimensions := reviewDimensionsForContext(reviewContext, state.fixing)
	var reviews []DimensionReview
	for _, dimension := range dimensions {
		review, err := state.runner.roles.Review(ctx, ReviewTask{Dimension: dimension, Context: reviewContext})
		if err != nil {
			return agentgraph.NodeResult{}, state.nodeError("%s review failed: %v", dimension, err)
		}
		reviews = append(reviews, DimensionReview{Dimension: dimension, Report: *review})
	}
	bundle := mergeDimensionReviews(reviews)
	round := ReviewRound{StepID: state.currentStep.ID, Dimensions: bundle.Dimensions, Findings: bundle.Findings, Summary: bundle.Summary}
	state.report.Reviews = append(state.report.Reviews, round)
	state.reviewHistory = append(state.reviewHistory, round.Dimensions...)
	if len(round.Findings) == 0 {
		if state.latestTestFailed() {
			return agentgraph.NodeResult{}, state.nodeError("review found no actionable defect for failed testing of %s", state.currentStep.ID)
		}
		if state.fixing && len(activeFindingsByDimension(state.reviewHistory)) > 0 {
			return agentgraph.NodeResult{}, state.nodeError("review omitted resolution for one or more active findings")
		}
		if state.fixing {
			state.fixing = false
			state.reviewHistory = nil
		}
		state.stepIndex++
		if state.stepIndex < len(state.report.Plan.Steps) {
			return agentgraph.NodeResult{Route: "code"}, nil
		}
		return agentgraph.NodeResult{Route: "verify"}, nil
	}
	if state.report.FixRounds >= state.runner.maxFixRounds {
		return agentgraph.NodeResult{}, state.nodeError("review still has %d findings after %d fix rounds", len(round.Findings), state.report.FixRounds)
	}
	state.report.FixRounds++
	state.currentStep = PlanStep{
		ID:           fmt.Sprintf("step-review-fix-%d", state.report.FixRounds),
		Objective:    "Fix confirmed review findings",
		AllowedFiles: findingFiles(round.Findings),
		Acceptance:   findingSummaries(round.Findings),
		Verification: state.report.Plan.FinalVerification,
	}
	state.fixing = true
	return agentgraph.NodeResult{Route: "fix"}, nil
}

func runTeamFixingNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhaseFixing)
	step := state.currentStep
	latestReview := state.report.Reviews[len(state.report.Reviews)-1]
	codeReport, err := state.runner.roles.Code(ctx, CodeTask{Goal: state.report.Plan.Goal, Step: step, ReviewFixes: latestReview.Findings, Attempt: state.report.FixRounds + 1})
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("review fix round %d failed: %v", state.report.FixRounds, err)
	}
	if err := validateCodeReport(step, latestReview.Findings, codeReport); err != nil {
		return agentgraph.NodeResult{}, state.nodeError("review fix round %d produced an invalid report: %v", state.report.FixRounds, err)
	}
	state.report.CodeReports = append(state.report.CodeReports, *codeReport)
	return agentgraph.NodeResult{Route: "preflight"}, nil
}

func runTeamVerifyingNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhaseVerifying)
	verification, err := state.runner.roles.Verify(ctx, state.report.Plan.FinalVerification)
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("final verification failed: %v", err)
	}
	if err := validateTestReport(verification); err != nil {
		return agentgraph.NodeResult{}, state.nodeError("final verification produced an invalid report: %v", err)
	}
	if err := requireVerificationCommands(state.report.Plan.FinalVerification, verification); err != nil {
		return agentgraph.NodeResult{}, state.nodeError("final verification evidence is incomplete: %v", err)
	}
	state.report.Verification = verification
	if !verification.Passed {
		return agentgraph.NodeResult{}, state.nodeError("final verification did not pass: %s", verification.Summary)
	}
	return agentgraph.NodeResult{Route: "done"}, nil
}

func (s *developmentTeamState) latestTestFailed() bool {
	if len(s.report.TestReports) == 0 {
		return false
	}
	return !s.report.TestReports[len(s.report.TestReports)-1].Passed
}

func (s *developmentTeamState) transition(phase TeamPhase) {
	s.report.Phase = phase
	s.report.Transitions = append(s.report.Transitions, phase)
}

func (s *developmentTeamState) nodeError(format string, args ...any) error {
	s.report.Failure = fmt.Sprintf(format, args...)
	return fmt.Errorf("%s", s.report.Failure)
}

func (s *developmentTeamState) fail(format string, args ...any) DevelopmentTeamReport {
	s.report.Status = "FAILED"
	s.report.Failure = fmt.Sprintf(format, args...)
	s.transition(TeamPhaseFailed)
	return *s.report
}
