package subagent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	agentgraph "github.com/c86j224s/olli/graph"
	"github.com/c86j224s/olli/tools"
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
	runner                *DevelopmentTeamRunner
	objective             string
	report                *DevelopmentTeamReport
	stepIndex             int
	currentStep           PlanStep
	stepFixRounds         int
	fixing                bool
	staticFix             bool
	milestoneReview       bool
	semanticReviewStarted bool
	reviewHistory         []DimensionReview
}

func (r *DevelopmentTeamRunner) runGraph(ctx context.Context, objective string) DevelopmentTeamReport {
	report := DevelopmentTeamReport{Status: "FAILED", Phase: TeamPhasePlanning}
	r.emitRunEvent(RunEvent{Kind: "run_started", GraphID: "development-team", Status: "running", Message: "Development team run started"})
	defer func() {
		kind := "run_completed"
		status := report.Status
		if status != "SUCCESS" {
			kind = "run_failed"
		}
		r.emitRunEvent(RunEvent{Kind: kind, GraphID: "development-team", Phase: string(report.Phase), Status: status, Message: report.Failure})
	}()
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
	maxFixVisits := maxPlanSteps * r.maxFixRounds
	maxNodeVisits := maxPlanSteps + maxFixVisits
	policy := agentgraph.Policy{
		MaxTransitions: maxPlanSteps*2 + maxFixVisits*3 + 8,
		MaxNodeVisits:  maxNodeVisits,
		NodeVisitLimit: map[string]int{
			teamNodePlanning:  1,
			teamNodeCoding:    maxPlanSteps,
			teamNodePreflight: maxPlanSteps + maxFixVisits,
			teamNodeTesting:   maxPlanSteps + maxFixVisits,
			teamNodeReviewing: maxFixVisits + 1,
			teamNodeFixing:    maxFixVisits,
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
			{From: teamNodeCoding, Route: "review", To: teamNodeReviewing},
			{From: teamNodePreflight, Route: "test", To: teamNodeTesting},
			{From: teamNodePreflight, Route: "review", To: teamNodeReviewing},
			{From: teamNodePreflight, Route: "fix", To: teamNodeFixing},
			{From: teamNodePreflight, Route: "code", To: teamNodeCoding},
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
	var plan *DevelopmentPlan
	if pipeline, ok := state.runner.roles.(architecturePlanningRoles); ok {
		var planning *PlanningReport
		plan, planning, err = pipeline.PlanArchitecture(ctx, state.objective)
		state.report.Planning = planning
	} else {
		plan, err = state.runner.roles.Plan(ctx, state.objective)
	}
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("planning failed: %v", err)
	}
	if err := validateDevelopmentPlan(plan); err != nil {
		return agentgraph.NodeResult{}, state.nodeError("planning produced an invalid plan: %v", err)
	}
	state.report.Plan = plan
	state.stepIndex = 0
	state.stepFixRounds = 0
	return agentgraph.NodeResult{Route: "planned"}, nil
}

func runTeamCodingNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhaseCoding)
	if state.report.Plan == nil {
		return agentgraph.NodeResult{}, state.nodeError("coding plan is missing")
	}
	if state.stepIndex >= len(state.report.Plan.Steps) {
		if !state.semanticReviewStarted {
			beginSemanticReview(state)
			return agentgraph.NodeResult{Route: "review"}, nil
		}
		return agentgraph.NodeResult{}, state.nodeError("coding step index is invalid")
	}
	step := state.report.Plan.Steps[state.stepIndex]
	state.currentStep = step
	state.fixing = false
	state.staticFix = false
	state.milestoneReview = false
	readOnlyFiles := existingPlanContextFiles(state.report.Plan, step.AllowedFiles, state.runner.workspace)
	snapshots := reviewSourceSnapshots(readOnlyFiles, state.runner.workspace)
	codeReport, err := state.runner.roles.Code(ctx, CodeTask{Goal: state.report.Plan.Goal, Step: step, ReadOnlyFiles: readOnlyFiles, SourceSnapshots: snapshots, Attempt: 1})
	if err != nil {
		retryStep := step
		retryStep.Objective = fmt.Sprintf("%s. Retry the same milestone after the previous Coder failed: %v. Work only in allowed_files and make at least one successful edit.", step.Objective, err)
		codeReport, err = state.runner.roles.Code(ctx, CodeTask{Goal: state.report.Plan.Goal, Step: retryStep, ReadOnlyFiles: readOnlyFiles, SourceSnapshots: snapshots, Attempt: 2})
	}
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("coding %s failed after one retry: %v", step.ID, err)
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

func existingPlanContextFiles(plan *DevelopmentPlan, writable []string, workspace string) []string {
	if plan == nil || strings.TrimSpace(workspace) == "" {
		return nil
	}
	writableSet := make(map[string]struct{}, len(writable))
	for _, path := range writable {
		writableSet[path] = struct{}{}
	}
	var files []string
	for _, path := range plan.Files {
		if _, isWritable := writableSet[path]; isWritable {
			continue
		}
		absolute, err := normalizeExistingPlanFile(path, workspace)
		if err != nil {
			continue
		}
		if info, err := os.Lstat(absolute); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			files = append(files, path)
		}
	}
	return uniqueStrings(files)
}

func normalizeExistingPlanFile(path, workspace string) (string, error) {
	return tools.IsPathSafeFrom(path, workspace, workspace)
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
		if state.stepFixRounds >= state.runner.maxFixRounds {
			return agentgraph.NodeResult{}, state.nodeError("static preflight still failed after %d fix rounds for milestone %d: %s", state.stepFixRounds, state.stepIndex+1, preflight.Commands[0].Output)
		}
		failedStepID := state.currentStep.ID
		finding := preflightFinding(state.currentStep, preflight)
		state.report.FixRounds++
		state.stepFixRounds++
		state.currentStep = PlanStep{
			ID:           fmt.Sprintf("step-review-fix-%d", state.report.FixRounds),
			Objective:    "Fix deterministic static preflight failure",
			AllowedFiles: findingFiles([]Finding{finding}),
			Acceptance:   findingSummaries([]Finding{finding}),
			Verification: state.report.Plan.FinalVerification,
		}
		state.fixing = true
		state.staticFix = true
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
	if state.staticFix {
		state.fixing = false
		state.staticFix = false
		state.reviewHistory = nil
		if state.stepIndex+1 < len(state.report.Plan.Steps) {
			state.stepIndex++
			state.stepFixRounds = 0
			return agentgraph.NodeResult{Route: "code"}, nil
		}
		state.stepIndex = len(state.report.Plan.Steps)
		return agentgraph.NodeResult{Route: "review"}, nil
	}
	if len(state.currentStep.Verification) > 0 {
		return agentgraph.NodeResult{Route: "test"}, nil
	}
	if !state.fixing && state.stepIndex+1 < len(state.report.Plan.Steps) {
		state.stepIndex++
		state.stepFixRounds = 0
		return agentgraph.NodeResult{Route: "code"}, nil
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
		if state.stepFixRounds >= state.runner.maxFixRounds {
			return agentgraph.NodeResult{}, state.nodeError("testing %s still failed after %d fix rounds: %s", state.currentStep.ID, state.stepFixRounds, testReport.Summary)
		}
		if !state.semanticReviewStarted {
			state.milestoneReview = true
		}
		return agentgraph.NodeResult{Route: "review"}, nil
	}
	if state.fixing {
		return agentgraph.NodeResult{Route: "review"}, nil
	}
	state.stepIndex++
	state.stepFixRounds = 0
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

func beginSemanticReview(state *developmentTeamState) {
	state.semanticReviewStarted = true
	state.stepFixRounds = 0
	state.currentStep = PlanStep{
		ID:           "step-final-review",
		Objective:    "Review the assembled implementation",
		AllowedFiles: append([]string(nil), state.report.Plan.Files...),
		Acceptance:   []string{"All planned behavior is implemented and safely verified"},
		Verification: append([]string(nil), state.report.Plan.FinalVerification...),
	}
}

func runDimensionReviews(ctx context.Context, roles DevelopmentTeamRoles, dimensions []ReviewDimension, reviewContext ReviewContext) ([]DimensionReview, error) {
	if len(dimensions) == 0 {
		return nil, nil
	}
	parallel, ok := roles.(interface{ ParallelReviewsEnabled() bool })
	if !ok || !parallel.ParallelReviewsEnabled() {
		reviews := make([]DimensionReview, 0, len(dimensions))
		for _, dimension := range dimensions {
			review, err := roles.Review(ctx, ReviewTask{Dimension: dimension, Context: reviewContext})
			if err != nil {
				return nil, fmt.Errorf("%s review failed: %w", dimension, err)
			}
			reviews = append(reviews, DimensionReview{Dimension: dimension, Report: *review})
		}
		return reviews, nil
	}
	type result struct {
		index  int
		review *ReviewReport
		err    error
	}
	results := make(chan result, len(dimensions))
	var wg sync.WaitGroup
	for index, dimension := range dimensions {
		index, dimension := index, dimension
		wg.Add(1)
		go func() {
			defer wg.Done()
			review, err := roles.Review(ctx, ReviewTask{Dimension: dimension, Context: reviewContext})
			if err != nil {
				err = fmt.Errorf("%s review failed: %w", dimension, err)
			}
			results <- result{index: index, review: review, err: err}
		}()
	}
	wg.Wait()
	close(results)
	ordered := make([]DimensionReview, len(dimensions))
	errorsByIndex := make([]error, len(dimensions))
	for item := range results {
		if item.err != nil {
			errorsByIndex[item.index] = item.err
			continue
		}
		if item.review == nil {
			errorsByIndex[item.index] = fmt.Errorf("%s review returned no report", dimensions[item.index])
			continue
		}
		ordered[item.index] = DimensionReview{Dimension: dimensions[item.index], Report: *item.review}
	}
	for _, err := range errorsByIndex {
		if err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func runTeamReviewingNode(ctx context.Context, raw agentgraph.State) (agentgraph.NodeResult, error) {
	state, err := teamState(raw)
	if err != nil {
		return agentgraph.NodeResult{}, err
	}
	state.transition(TeamPhaseReviewing)
	if !state.semanticReviewStarted && !state.milestoneReview {
		beginSemanticReview(state)
	}
	reviewContext := buildReviewContext(state.report, state.currentStep.ID, state.currentStep.AllowedFiles, state.reviewHistory)
	dimensions := reviewDimensionsForContext(reviewContext, state.fixing)
	reviews, err := runDimensionReviews(ctx, state.runner.roles, dimensions, reviewContext)
	if err != nil {
		return agentgraph.NodeResult{}, state.nodeError("review failed: %v", err)
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
		if state.milestoneReview {
			state.milestoneReview = false
			state.stepIndex++
			state.stepFixRounds = 0
			return agentgraph.NodeResult{Route: "code"}, nil
		}
		return agentgraph.NodeResult{Route: "verify"}, nil
	}
	if state.stepFixRounds >= state.runner.maxFixRounds {
		return agentgraph.NodeResult{}, state.nodeError("review still has %d findings after %d fix rounds", len(round.Findings), state.stepFixRounds)
	}
	state.report.FixRounds++
	state.stepFixRounds++
	state.currentStep = PlanStep{
		ID:           fmt.Sprintf("step-review-fix-%d", state.report.FixRounds),
		Objective:    "Fix confirmed review findings",
		AllowedFiles: findingFiles(round.Findings),
		Acceptance:   findingSummaries(round.Findings),
		Verification: state.report.Plan.FinalVerification,
	}
	state.fixing = true
	state.staticFix = false
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
	readOnlyFiles := existingPlanContextFiles(state.report.Plan, step.AllowedFiles, state.runner.workspace)
	snapshots := reviewSourceSnapshots(readOnlyFiles, state.runner.workspace)
	codeReport, err := state.runner.roles.Code(ctx, CodeTask{Goal: state.report.Plan.Goal, Step: step, ReadOnlyFiles: readOnlyFiles, SourceSnapshots: snapshots, ReviewFixes: latestReview.Findings, Attempt: state.report.FixRounds + 1})
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
	s.runner.emitRunEvent(RunEvent{Kind: "phase_changed", GraphID: "development-team", NodeID: string(phase), Phase: string(phase), Status: "running"})
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
