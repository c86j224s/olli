package subagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentloop "github.com/c86j224s/olli/loop"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

const coderTeamPrompt = `ROLE: The only writer in a deterministic small-model development team.

RULES:
- Implement exactly one supplied plan step.
- Reviewer findings are outcome requirements, not patches. Reread the current file and choose the implementation yourself; never copy an exact replacement string from Reviewer text.
- When review_fixes are present, report each finding id in addressed_findings as addressed or not_addressed with concise evidence. This is a Coder claim; Reviewer and Tester make the final resolution decision.
- When fixing compiler or test failures, correct those failures as well as the review findings before returning.
- Modify only allowed_files. Never run tests or commands.
- Inspect each target before editing.
- You MUST make at least one successful edit_file or replace_file call before returning CodeReport. Reading a file is not completion.
- For the first milestone on a whole-file starter, use replace_file with complete parseable content. Do not implement later milestones early.
- For later milestones, use edit_file with exact target_content copied from view_file; use replace_file only if a targeted edit cannot safely preserve parseability.
- Keep the write bounded to the supplied step acceptance criteria. Never regenerate an already implemented file wholesale just to add one milestone.
- If edit_file says the target chunk is missing, do not retry guessed target text. Read the latest file and use replace_file with the complete corrected file.
- Prefer small targeted edits unless the step explicitly requires completing a whole-file starter.
- Return JSON only, matching CodeReport.
- changed_files must list every file you actually changed.
- unresolved must be empty only when every acceptance criterion is implemented.`

const testerTeamPrompt = `ROLE: Read-only tester in a deterministic small-model development team.

RULES:
- Never modify files.
- Run only the verification commands listed in the task.
- Each required command is the canonical execute_action form: action or "action target" (example: "go_test ./...").
- Use execute_action; never invent raw shell commands.
- Record that exact canonical form as command, the real exit status, and bounded output.
- passed is true only when every required command exits successfully.
- Return JSON only, matching TestReport.`

const reviewerTeamPrompt = `ROLE: Read-only reviewer in a deterministic small-model development team.

RULES:
- Never modify code. Give structured findings to the Coder; the Coder alone implements fixes.
- Compare the validated plan, changed files, latest test report, prior review findings, and current code.
- Report only correctness, security, required-behavior, or unsafe-test defects.
- Use view_file immediately to read every changed file completely. Use one unbounded call for a small file or enough non-overlapping ranges to cover a large file, then stop calling tools.
- Every new finding needs a stable id, real file and line, concise defect, concrete failure scenario, required outcome, and canonical verification commands.
- Verification entries may only be: "go_test", "go_test ./path", "go_vet", "go_vet ./path", "git_status", "git_diff", or "git_diff file". Never return go run or prose instructions.
- For every previous finding id, return resolved or unresolved with concrete current evidence. Unresolved previous findings must also remain in findings with the same id.
- Describe outcomes, not exact replacement strings or patches. The Coder must reread the current file and choose the implementation.
- Do not report style preferences or vague concerns.
- Return JSON only, matching ReviewReport.`

type TeamModels struct {
	Planner             string
	Coder               string
	Tester              string
	Reviewer            string
	Cassandra           string
	DetailPlanner       string
	RequirementReviewer string
	LogicReviewer       string
	SafetyReviewer      string
	TestReviewer        string
	CoderThinking       *bool
	ReviewerThinking    *bool
}

type ModelTeamRoles struct {
	runner         *SubagentRunner
	models         TeamModels
	testerRegistry func() *tools.Registry
}

func NewModelTeamRoles(runner *SubagentRunner) (*ModelTeamRoles, error) {
	return NewModelTeamRolesWithModels(runner, TeamModels{})
}

func NewModelTeamRolesWithModels(runner *SubagentRunner, models TeamModels) (*ModelTeamRoles, error) {
	if runner == nil || runner.client == nil {
		return nil, fmt.Errorf("subagent runner with client is required")
	}
	models = models.withFallback(runner.model)
	return &ModelTeamRoles{runner: runner, models: models}, nil
}

func (m TeamModels) withFallback(fallback string) TeamModels {
	if strings.TrimSpace(m.Planner) == "" {
		m.Planner = fallback
	}
	if strings.TrimSpace(m.Coder) == "" {
		m.Coder = fallback
	}
	if strings.TrimSpace(m.Tester) == "" {
		m.Tester = fallback
	}
	if strings.TrimSpace(m.Reviewer) == "" {
		m.Reviewer = fallback
	}
	if strings.TrimSpace(m.Cassandra) == "" {
		m.Cassandra = m.Reviewer
	}
	if strings.TrimSpace(m.DetailPlanner) == "" {
		m.DetailPlanner = m.Planner
	}
	if strings.TrimSpace(m.RequirementReviewer) == "" {
		m.RequirementReviewer = m.Reviewer
	}
	if strings.TrimSpace(m.LogicReviewer) == "" {
		m.LogicReviewer = m.Reviewer
	}
	if strings.TrimSpace(m.SafetyReviewer) == "" {
		m.SafetyReviewer = m.Reviewer
	}
	if strings.TrimSpace(m.TestReviewer) == "" {
		m.TestReviewer = m.Reviewer
	}
	return m
}

func (m TeamModels) reviewerModel(dimension ReviewDimension) string {
	switch dimension {
	case ReviewDimensionRequirements:
		return m.RequirementReviewer
	case ReviewDimensionLogic:
		return m.LogicReviewer
	case ReviewDimensionSafety:
		return m.SafetyReviewer
	case ReviewDimensionTests:
		return m.TestReviewer
	default:
		return m.Reviewer
	}
}

func (m *ModelTeamRoles) TeamWorkspace() string {
	if m == nil || m.runner == nil {
		return ""
	}
	return m.runner.workspace
}

func (m *ModelTeamRoles) Plan(ctx context.Context, objective string) (*DevelopmentPlan, error) {
	plan, _, err := m.PlanArchitecture(ctx, objective)
	return plan, err
}

func (m *ModelTeamRoles) PlanArchitecture(ctx context.Context, objective string) (*DevelopmentPlan, *PlanningReport, error) {
	roleCtx, cancel := withRoleTimeout(ctx, roleBudget{Timeout: planningPipelineTimeout})
	defer cancel()
	architecture, err := m.createArchitecture(roleCtx, objective, nil)
	if err != nil {
		return nil, nil, err
	}
	review, err := m.reviewArchitecture(roleCtx, objective, architecture)
	if err != nil {
		return nil, nil, err
	}
	reviews := []ArchitectureReview{*review}
	if !review.Passed {
		architecture, err = m.createArchitecture(roleCtx, objective, review.Findings)
		if err != nil {
			return nil, nil, fmt.Errorf("architect repair failed: %w", err)
		}
		review, err = m.reviewArchitecture(roleCtx, objective, architecture)
		if err != nil {
			return nil, nil, err
		}
		reviews = append(reviews, *review)
		if !review.Passed {
			return nil, &PlanningReport{Architecture: *architecture, Reviews: reviews}, fmt.Errorf("cassandra rejected repaired architecture: %s", review.Summary)
		}
	}
	details := make([]DetailPlan, 0, len(architecture.Packages))
	for _, work := range architecture.Packages {
		detail, err := m.detailArchitectureWork(roleCtx, architecture, work)
		if err != nil {
			return nil, nil, fmt.Errorf("detail planning %s failed: %w", work.ID, err)
		}
		details = append(details, *detail)
	}
	plan, err := flattenArchitecturePlan(*architecture, details)
	if err != nil {
		return nil, nil, err
	}
	return plan, &PlanningReport{Architecture: *architecture, Reviews: reviews}, nil
}

func (m *ModelTeamRoles) Code(ctx context.Context, task CodeTask) (*CodeReport, error) {
	roleCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypeCoder))
	defer cancel()
	payload, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}
	reg := m.runner.newRoleRegistry()
	registerTeamCoderTools(reg, task.Step.AllowedFiles)
	temperature := 0.1
	evidence := &executionEvidence{ProgressMarker: coderProgressMarker(m.runner.workspace), RequiredAnyTools: []string{"edit_file", "replace_file"}}
	evidence.ProgressState = func() string { return evidenceProgressSet(evidence) }
	evidence.CompletionReady = func() bool { return len(evidence.missingRequiredTools()) == 0 }
	coderRunner := m.runner.withModel(m.models.Coder)
	if m.models.CoderThinking != nil {
		coderRunner = coderRunner.withThinking(*m.models.CoderThinking)
	}
	report, err := coderRunner.executeSubagentLoopWithFormat(roleCtx, newSubagentID("team-coder"), string(TypeCoder), string(payload), coderTeamPrompt, reg, codeReportSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	writes := evidence.SuccessfulTools["edit_file"] + evidence.SuccessfulTools["replace_file"]
	if writes == 0 {
		return nil, fmt.Errorf("coder loop %s: coder returned without a successful edit_file or replace_file call", report.Termination)
	}
	codeReport, parseErr := parseCodeReport(report.Summary)
	if codeReport != nil {
		codeReport.EvidenceDerived = false
	}
	if report.Status != "SUCCESS" || parseErr != nil {
		if len(task.ReviewFixes) > 0 {
			return nil, fmt.Errorf("coder fix loop %s did not produce a valid finding disposition report", report.Termination)
		}
		if report.Termination != agentloop.TerminationInvalidOutput && parseErr == nil {
			return nil, fmt.Errorf("coder loop %s: %s", report.Termination, report.Summary)
		}
		codeReport = codeReportFromEvidence(task.Step, evidence)
	}
	if err := validateCodeReport(task.Step, task.ReviewFixes, codeReport); err != nil {
		return nil, err
	}
	if err := requireCoderEditEvidence(codeReport, evidence); err != nil {
		return nil, err
	}
	return codeReport, nil
}

func (m *ModelTeamRoles) Test(ctx context.Context, step PlanStep) (*TestReport, error) {
	return m.runTester(ctx, "team-tester", step.Verification)
}

func (m *ModelTeamRoles) Verify(ctx context.Context, commands []string) (*TestReport, error) {
	return m.runTester(ctx, "team-verifier", commands)
}

func (m *ModelTeamRoles) runTester(ctx context.Context, role string, commands []string) (*TestReport, error) {
	roleCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypeTester))
	defer cancel()
	commands = uniqueStrings(commands)
	if len(commands) == 0 {
		return nil, fmt.Errorf("tester requires verification commands")
	}
	payload, _ := json.Marshal(map[string]any{"required_commands": commands})
	var reg *tools.Registry
	if m.testerRegistry != nil {
		reg = m.testerRegistry()
	} else {
		reg = m.runner.newRoleRegistry()
		registerTeamTesterTools(reg)
	}
	temperature := 0.0
	evidence := &executionEvidence{ProgressMarker: commandProgressMarker, RequiredCalls: requiredCommandCalls(commands)}
	evidence.ProgressState = func() string { return evidenceAttemptProgressSet(evidence) }
	evidence.CompletionReady = func() bool { return len(evidence.missingRequiredTools()) == 0 }
	report, err := m.runner.withModel(m.models.Tester).executeSubagentLoopWithFormat(roleCtx, newSubagentID(role), string(TypeTester), string(payload), testerTeamPrompt, reg, testReportSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	if evidence.ToolCallsAttempted == 0 {
		return nil, fmt.Errorf("tester loop %s: tester returned without an execute_action call", report.Termination)
	}
	testReport, parseErr := parseTestReport(report.Summary)
	if report.Status != "SUCCESS" || parseErr != nil {
		if report.Termination != agentloop.TerminationInvalidOutput && report.Termination != agentloop.TerminationNoProgress && parseErr == nil {
			return nil, fmt.Errorf("tester loop %s: %s", report.Termination, report.Summary)
		}
		testReport = testReportFromEvidence(commands, evidence)
	}
	if err := validateTestReport(testReport); err != nil {
		return nil, err
	}
	if err := requireVerificationCommands(commands, testReport); err != nil {
		return nil, err
	}
	if err := requireExecutedActionEvidence(commands, testReport, evidence); err != nil {
		return nil, err
	}
	return testReport, nil
}

func (m *ModelTeamRoles) Review(ctx context.Context, task ReviewTask) (*ReviewReport, error) {
	roleCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypeReviewer))
	defer cancel()
	if err := validateReviewDimension(task.Dimension); err != nil {
		return nil, err
	}
	reviewContext := task.Context
	payload, err := json.Marshal(reviewContext)
	if err != nil {
		return nil, err
	}
	reg := m.runner.newRoleRegistry()
	registerTeamReviewerTools(reg)
	temperature := 0.1
	reviewFiles := reviewContext.ReviewScope
	if len(reviewFiles) == 0 {
		reviewFiles = changedFilesFromCodeReports(reviewContext.CodeReports)
	}
	evidence := &executionEvidence{ProgressMarker: inspectedFileProgressMarker, RequiredTools: map[string]int{"view_file": 1}}
	evidence.ProgressState = func() string { return evidenceProgressSet(evidence) }
	evidence.CompletionReady = func() bool {
		return len(evidence.missingRequiredTools()) == 0 && requireReviewerFileEvidence(reviewFiles, evidence, m.runner.workspace) == nil
	}
	reviewerRunner := m.runner.withModel(m.models.reviewerModel(task.Dimension))
	if m.models.ReviewerThinking != nil {
		reviewerRunner = reviewerRunner.withThinking(*m.models.ReviewerThinking)
	}
	prompt, err := reviewerPromptForDimension(task.Dimension)
	if err != nil {
		return nil, err
	}
	report, err := reviewerRunner.executeSubagentLoopWithFormat(roleCtx, newSubagentID("team-reviewer-"+string(task.Dimension)), string(TypeReviewer), string(payload), prompt, reg, reviewReportSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("reviewer loop %s: %s", report.Termination, report.Summary)
	}
	if err := requireReviewerFileEvidence(reviewFiles, evidence, m.runner.workspace); err != nil {
		return nil, err
	}
	reviewReport, err := parseReviewReport(report.Summary)
	if err != nil {
		return nil, err
	}
	previous := dimensionReviewHistory(reviewContext.PreviousReviews, task.Dimension)
	if len(previous) == 0 {
		reviewReport.FindingResolutions = nil
	}
	if err := validateDimensionReviewReport(reviewContext.Plan, task.Dimension, previous, reviewReport); err != nil {
		return nil, err
	}
	return reviewReport, nil
}

func coderProgressMarker(workspace string) func(string, map[string]interface{}, string) string {
	fileMarker := fileProgressMarker(workspace)
	return func(toolName string, arguments map[string]interface{}, result string) string {
		if toolName == "view_file" {
			return inspectedFileProgressMarker(toolName, arguments, result)
		}
		return fileMarker(toolName, arguments, result)
	}
}

func fileProgressMarker(workspace string) func(string, map[string]interface{}, string) string {
	return func(toolName string, arguments map[string]interface{}, _ string) string {
		if toolName != "edit_file" && toolName != "replace_file" {
			return ""
		}
		path, _ := arguments["file_path"].(string)
		path, err := normalizePlanPath(path)
		if err != nil {
			return ""
		}
		safePath, err := tools.IsPathSafeFrom(path, workspace, workspace)
		if err != nil {
			return ""
		}
		info, err := os.Lstat(safePath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return ""
		}
		data, err := os.ReadFile(safePath)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(data)
		return "file:" + path + ":" + hex.EncodeToString(sum[:])
	}
}

func plannerProgressMarker(toolName string, arguments map[string]interface{}, _ string) string {
	switch toolName {
	case "list_dir":
		path, _ := arguments["dir_path"].(string)
		path = strings.TrimSpace(path)
		if path == "" {
			path = "."
		}
		return "listed:" + filepath.Clean(path)
	case "grep_search":
		query, _ := arguments["query"].(string)
		path, _ := arguments["search_path"].(string)
		return "searched:" + strings.TrimSpace(query) + ":" + filepath.Clean(strings.TrimSpace(path))
	default:
		return inspectedFileProgressMarker(toolName, arguments, "")
	}
}

func inspectedFileProgressMarker(toolName string, arguments map[string]interface{}, _ string) string {
	if toolName != "view_file" {
		return ""
	}
	path, _ := arguments["file_path"].(string)
	path, err := normalizePlanPath(path)
	if err != nil {
		return ""
	}
	start := optionalNumberMarker(arguments["start_line"])
	end := optionalNumberMarker(arguments["end_line"])
	return "viewed:" + path + ":" + start + ":" + end
}

func optionalNumberMarker(value interface{}) string {
	if value == nil {
		return "all"
	}
	return fmt.Sprint(value)
}

func commandProgressMarker(toolName string, arguments map[string]interface{}, _ string) string {
	if toolName != "execute_action" {
		return ""
	}
	action, _ := arguments["action"].(string)
	target, _ := arguments["target"].(string)
	return "command:" + canonicalActionCommand(action, target)
}

func evidenceProgressSet(evidence *executionEvidence) string {
	if evidence == nil {
		return ""
	}
	return progressSet(evidence.SuccessfulCalls)
}

func evidenceAttemptProgressSet(evidence *executionEvidence) string {
	if evidence == nil {
		return ""
	}
	return progressSet(evidence.AttemptedCalls)
}

func progressSet(calls []successfulToolCall) string {
	seen := make(map[string]struct{})
	for _, call := range calls {
		if call.ProgressMarker != "" {
			seen[call.ProgressMarker] = struct{}{}
		}
	}
	markers := make([]string, 0, len(seen))
	for marker := range seen {
		markers = append(markers, marker)
	}
	sort.Strings(markers)
	return strings.Join(markers, "|")
}

func requiredCommandCalls(commands []string) []requiredToolCall {
	calls := make([]requiredToolCall, 0, len(commands))
	for _, command := range commands {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			continue
		}
		arguments := map[string]interface{}{"action": fields[0]}
		if len(fields) > 1 {
			arguments["target"] = fields[1]
		}
		calls = append(calls, requiredToolCall{
			Name:        "execute_action",
			Fingerprint: agentloop.ActionFingerprint("execute_action", arguments),
			Description: command,
		})
	}
	return calls
}

func testReportFromEvidence(required []string, evidence *executionEvidence) *TestReport {
	commands := make([]CommandResult, 0, len(required))
	byCommand := make(map[string]successfulToolCall)
	for _, call := range evidence.AttemptedCalls {
		if call.Name != "execute_action" {
			continue
		}
		action, _ := call.Arguments["action"].(string)
		target, _ := call.Arguments["target"].(string)
		byCommand[canonicalActionCommand(action, target)] = call
	}
	passed := true
	for _, command := range required {
		call, exists := byCommand[command]
		if !exists {
			commands = append(commands, CommandResult{Command: command, ExitCode: -1, Output: "command was not executed"})
			passed = false
			continue
		}
		commands = append(commands, CommandResult{Command: command, ExitCode: call.ExitCode, Output: call.Result})
		if call.ExitCode != 0 {
			passed = false
		}
	}
	summary := "all required commands passed"
	if !passed {
		summary = "one or more required commands failed"
	}
	return &TestReport{Passed: passed, Commands: commands, Summary: summary}
}

func codeReportFromEvidence(step PlanStep, evidence *executionEvidence) *CodeReport {
	var changed []string
	for _, call := range evidence.SuccessfulCalls {
		if call.Name != "edit_file" && call.Name != "replace_file" {
			continue
		}
		path, _ := call.Arguments["file_path"].(string)
		if normalized, err := normalizePlanPath(path); err == nil {
			changed = append(changed, normalized)
		}
	}
	return &CodeReport{
		StepID:          step.ID,
		ChangedFiles:    uniqueStrings(changed),
		Completed:       []string{"runtime observed successful writes"},
		Unresolved:      []string{"model did not provide a valid completion report; verification required"},
		EvidenceDerived: true,
	}
}

func changedFilesFromCodeReports(codeReports []CodeReport) []string {
	var files []string
	for _, report := range codeReports {
		files = append(files, report.ChangedFiles...)
	}
	return uniqueStrings(files)
}

func requireReviewerFileEvidence(requiredFiles []string, evidence *executionEvidence, workspace string) error {
	required := make(map[string]struct{}, len(requiredFiles))
	for _, path := range requiredFiles {
		normalized, err := normalizePlanPath(path)
		if err != nil {
			return fmt.Errorf("reviewer required file %q: %w", path, err)
		}
		required[normalized] = struct{}{}
	}
	type coverage struct {
		whole  bool
		ranges [][2]int
	}
	viewed := make(map[string]*coverage)
	for _, call := range evidence.SuccessfulCalls {
		if call.Name != "view_file" {
			continue
		}
		path, _ := call.Arguments["file_path"].(string)
		normalized, err := normalizePlanPath(path)
		if err != nil {
			continue
		}
		entry := viewed[normalized]
		if entry == nil {
			entry = &coverage{}
			viewed[normalized] = entry
		}
		start := tools.ParseOptionalInt(call.Arguments, "start_line")
		end := tools.ParseOptionalInt(call.Arguments, "end_line")
		if start <= 0 && end <= 0 && !strings.Contains(call.Result, "[Truncated at 800 lines limit]") {
			entry.whole = true
		} else {
			if start <= 0 {
				start = 1
			}
			if strings.Contains(call.Result, "[Truncated at 800 lines limit]") && (end <= 0 || end > start+799) {
				end = start + 799
			}
			entry.ranges = append(entry.ranges, [2]int{start, end})
		}
	}
	for path := range required {
		entry := viewed[path]
		if entry == nil {
			return fmt.Errorf("reviewer did not inspect changed file %q", path)
		}
		if entry.whole {
			continue
		}
		data, err := os.ReadFile(filepath.Join(workspace, path))
		if err != nil {
			return fmt.Errorf("reviewer coverage could not read %q: %w", path, err)
		}
		lineCount := sourceLineCount(data)
		if !rangesCoverLines(entry.ranges, lineCount) {
			for line := 1; line <= lineCount; line++ {
				if !rangesCoverLines(entry.ranges, line) {
					return fmt.Errorf("reviewer did not fully inspect %q; line %d is uncovered", path, line)
				}
			}
			return fmt.Errorf("reviewer did not fully inspect %q", path)
		}
	}
	return nil
}

func requireCoderEditEvidence(report *CodeReport, evidence *executionEvidence) error {
	edited := make(map[string]struct{})
	for _, call := range evidence.SuccessfulCalls {
		if call.Name != "edit_file" && call.Name != "replace_file" {
			continue
		}
		path, _ := call.Arguments["file_path"].(string)
		if normalized, err := normalizePlanPath(path); err == nil {
			edited[normalized] = struct{}{}
		}
	}
	reported := make(map[string]struct{}, len(report.ChangedFiles))
	for _, path := range report.ChangedFiles {
		reported[path] = struct{}{}
		if _, exists := edited[path]; !exists {
			return fmt.Errorf("changed file %q has no successful edit_file evidence", path)
		}
	}
	for path := range edited {
		if _, exists := reported[path]; !exists {
			return fmt.Errorf("successfully edited file %q is missing from changed_files", path)
		}
	}
	return nil
}

func requireExecutedActionEvidence(required []string, report *TestReport, evidence *executionEvidence) error {
	executed := make(map[string]int)
	for _, call := range evidence.AttemptedCalls {
		if call.Name != "execute_action" {
			continue
		}
		action, _ := call.Arguments["action"].(string)
		target, _ := call.Arguments["target"].(string)
		executed[canonicalActionCommand(action, target)] = call.ExitCode
	}
	reported := make(map[string]int, len(report.Commands))
	for _, command := range report.Commands {
		reported[strings.TrimSpace(command.Command)] = command.ExitCode
	}
	for _, command := range required {
		command = strings.TrimSpace(command)
		actualExitCode, exists := executed[command]
		if !exists {
			return fmt.Errorf("required command %q has no execute_action evidence", command)
		}
		reportedExitCode, exists := reported[command]
		if !exists {
			return fmt.Errorf("required command %q is missing from the tester report", command)
		}
		if actualExitCode != reportedExitCode {
			return fmt.Errorf("required command %q reported exit code %d but execution returned %d", command, reportedExitCode, actualExitCode)
		}
	}
	return nil
}

func canonicalActionCommand(action string, target string) string {
	action = strings.TrimSpace(action)
	target = strings.TrimSpace(target)
	if target == "" {
		return action
	}
	return action + " " + target
}

func registerTeamCoderTools(reg *tools.Registry, allowedFiles []string) {
	allowed := make(map[string]struct{}, len(allowedFiles))
	inspected := make(map[string][][2]int)
	for _, path := range allowedFiles {
		allowed[path] = struct{}{}
	}
	check := func(path string) error {
		path, err := normalizePlanPath(path)
		if err != nil {
			return err
		}
		if _, exists := allowed[path]; !exists {
			return fmt.Errorf("coder path %q is outside allowed_files", path)
		}
		return nil
	}

	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "view_file", Description: "View one allowed source file", Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
		"file_path": {Type: "string", Description: "Allowed workspace-relative file"}, "start_line": {Type: "integer", Description: "Optional start line"}, "end_line": {Type: "integer", Description: "Optional end line"},
	}, Required: []string{"file_path"}}}}, func(args map[string]interface{}) (string, error) {
		path, _ := args["file_path"].(string)
		if err := check(path); err != nil {
			return "", err
		}
		start := tools.ParseOptionalInt(args, "start_line")
		end := tools.ParseOptionalInt(args, "end_line")
		result, err := tools.ViewFile(path, start, end, reg.GetWorkspace(), reg.GetWorkspaceRoot())
		if err == nil {
			normalized, normalizeErr := normalizePlanPath(path)
			if normalizeErr != nil {
				return "", normalizeErr
			}
			if start <= 0 {
				start = 1
			}
			if end <= 0 {
				if strings.Contains(result, "[Truncated at 800 lines limit]") {
					end = start + 799
				} else {
					end = int(^uint(0) >> 1)
				}
			}
			inspected[normalized] = append(inspected[normalized], [2]int{start, end})
		}
		return result, err
	})

	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "edit_file", Description: "Replace one exact existing chunk in an allowed file", Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
		"file_path": {Type: "string", Description: "Allowed workspace-relative file"}, "target_content": {Type: "string", Description: "Exact non-empty existing content copied from view_file"}, "replacement_content": {Type: "string", Description: "Replacement content"},
	}, Required: []string{"file_path", "target_content", "replacement_content"}}}}, func(args map[string]interface{}) (string, error) {
		path, _ := args["file_path"].(string)
		if err := check(path); err != nil {
			return "", err
		}
		normalized, _ := normalizePlanPath(path)
		if len(inspected[normalized]) == 0 {
			return "", fmt.Errorf("coder must successfully view %q before editing it", path)
		}
		target, _ := args["target_content"].(string)
		if target == "" {
			return "", fmt.Errorf("edit_file requires non-empty target_content; use replace_file for a whole-file implementation")
		}
		replacement, _ := args["replacement_content"].(string)
		if filepath.Ext(path) == ".go" {
			if err := validateTeamGoEdit(path, target, replacement, reg.GetWorkspace(), reg.GetWorkspaceRoot()); err != nil {
				return "", err
			}
		}
		return tools.EditFile(path, target, replacement, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "replace_file", Description: "Replace the complete contents of one allowed file", Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
		"file_path": {Type: "string", Description: "Allowed workspace-relative file"}, "content": {Type: "string", Description: "Complete replacement file content"},
	}, Required: []string{"file_path", "content"}}}}, func(args map[string]interface{}) (string, error) {
		path, _ := args["file_path"].(string)
		if err := check(path); err != nil {
			return "", err
		}
		normalized, _ := normalizePlanPath(path)
		safePath, safeErr := tools.IsPathSafeFrom(path, reg.GetWorkspace(), reg.GetWorkspaceRoot())
		if safeErr != nil {
			return "", safeErr
		}
		if info, statErr := os.Lstat(safePath); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return "", fmt.Errorf("replace_file target must be a regular non-symlink file")
			}
			data, readErr := os.ReadFile(safePath)
			if readErr != nil {
				return "", readErr
			}
			lineCount := sourceLineCount(data)
			if !rangesCoverLines(inspected[normalized], lineCount) {
				return "", fmt.Errorf("coder must inspect all %d lines of %q before replacing it", lineCount, path)
			}
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
		content, _ := args["content"].(string)
		if strings.TrimSpace(content) == "" {
			return "", fmt.Errorf("replace_file requires non-empty complete content")
		}
		if strings.EqualFold(filepath.Ext(path), ".go") {
			files := token.NewFileSet()
			if _, err := parser.ParseFile(files, path, content, parser.AllErrors); err != nil {
				return "", fmt.Errorf("replace_file rejected invalid Go source before write: %w", err)
			}
		}
		return tools.EditFile(path, "", content, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})
}

func sourceLineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	count := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		count++
	}
	return count
}

func rangesCoverLines(ranges [][2]int, lineCount int) bool {
	if lineCount == 0 {
		return true
	}
	covered := make([]bool, lineCount+1)
	for _, interval := range ranges {
		start, end := interval[0], interval[1]
		if start < 1 {
			start = 1
		}
		if end > lineCount {
			end = lineCount
		}
		for line := start; line <= end; line++ {
			covered[line] = true
		}
	}
	for line := 1; line <= lineCount; line++ {
		if !covered[line] {
			return false
		}
	}
	return true
}

func validateTeamGoEdit(path, target, replacement, workspace, root string) error {
	safePath, err := tools.IsPathSafeFrom(path, workspace, root)
	if err != nil {
		return err
	}
	info, err := os.Lstat(safePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("team Go edit target must be a regular non-symlink file")
	}
	data, err := os.ReadFile(safePath)
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, target) {
		return fmt.Errorf("target content chunk not found in file %q", safePath)
	}
	candidate := strings.Replace(content, target, replacement, 1)
	files := token.NewFileSet()
	if _, err := parser.ParseFile(files, path, candidate, parser.AllErrors); err != nil {
		return fmt.Errorf("edit_file would make Go source invalid; original preserved: %w", err)
	}
	return nil
}

func registerTeamTesterTools(reg *tools.Registry) {
	reg.RegisterContext(ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "execute_action", Description: "Execute one read-only approved test, vet, or git inspection action", Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
		"action": {Type: "string", Description: "go_test, go_vet, git_status, or git_diff", Enum: []string{"go_test", "go_vet", "git_status", "git_diff"}}, "target": {Type: "string", Description: "Optional safe target"},
	}, Required: []string{"action"}}}}, tools.ToolMetadata{}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		action, _ := args["action"].(string)
		if _, allowed := allowedVerificationActions[action]; !allowed {
			return "", fmt.Errorf("team tester action %q is not read-only approved", action)
		}
		target, _ := args["target"].(string)
		return tools.ExecuteActionWithWorkspace(ctx, action, target, 0, 0, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})
}

func registerTeamReviewerTools(reg *tools.Registry) {
	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{
		Name: "view_file", Description: "Read a bounded line range from a source file",
		Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
			"file_path":  {Type: "string", Description: "Workspace-relative source file"},
			"start_line": {Type: "integer", Description: "Optional 1-based start line"},
			"end_line":   {Type: "integer", Description: "Optional 1-based end line"},
		}, Required: []string{"file_path"}},
	}}, func(args map[string]interface{}) (string, error) {
		path, _ := args["file_path"].(string)
		path = normalizePlannerToolPath(path, reg.GetWorkspace())
		start := tools.ParseOptionalInt(args, "start_line")
		end := tools.ParseOptionalInt(args, "end_line")
		return tools.ViewFile(path, start, end, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})
}

func formatCodeTask(task CodeTask) string {
	parts := []string{task.Step.ID, task.Step.Objective, strings.Join(task.Step.AllowedFiles, ", ")}
	return strings.Join(parts, " | ")
}
