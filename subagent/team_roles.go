package subagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

const coderTeamPrompt = `ROLE: The only writer in a deterministic small-model development team.

RULES:
- Implement exactly one supplied plan step.
- Modify only allowed_files. Never run tests or commands.
- Inspect each target before editing.
- Prefer small targeted edits; do not rewrite unrelated code.
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
- Compare the validated plan, changed files, and current code.
- Report only correctness, security, required-behavior, or unsafe-test defects.
- Every finding requires a real file, line, concise defect, and concrete failure scenario.
- Do not report style preferences or vague concerns.
- Return JSON only, matching ReviewReport.`

type TeamModels struct {
	Planner  string
	Coder    string
	Tester   string
	Reviewer string
}

type ModelTeamRoles struct {
	runner *SubagentRunner
	models TeamModels
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
	return m
}

func (m *ModelTeamRoles) Plan(ctx context.Context, objective string) (*DevelopmentPlan, error) {
	_, plan, err := m.runner.withModel(m.models.Planner).RunPlannerWithContext(ctx, objective)
	return plan, err
}

func (m *ModelTeamRoles) Code(ctx context.Context, task CodeTask) (*CodeReport, error) {
	payload, err := json.Marshal(task)
	if err != nil {
		return nil, err
	}
	reg := m.runner.newRoleRegistry()
	registerTeamCoderTools(reg, task.Step.AllowedFiles)
	temperature := 0.1
	evidence := &executionEvidence{ProgressMarker: coderProgressMarker(m.runner.workspace)}
	evidence.ProgressState = func() string { return evidenceProgressSet(evidence) }
	report, err := m.runner.withModel(m.models.Coder).executeSubagentLoopWithFormat(ctx, newSubagentID("team-coder"), string(TypeCoder), string(payload), coderTeamPrompt, reg, codeReportSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("coder loop %s: %s", report.Termination, report.Summary)
	}
	if evidence.SuccessfulTools["edit_file"] == 0 {
		return nil, fmt.Errorf("coder returned without a successful edit_file call")
	}
	codeReport, err := parseCodeReport(report.Summary)
	if err != nil {
		return nil, err
	}
	if err := validateCodeReport(task.Step, codeReport); err != nil {
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
	commands = uniqueStrings(commands)
	if len(commands) == 0 {
		return nil, fmt.Errorf("tester requires verification commands")
	}
	payload, _ := json.Marshal(map[string]any{"required_commands": commands})
	reg := m.runner.newRoleRegistry()
	registerTeamTesterTools(reg)
	temperature := 0.0
	evidence := &executionEvidence{ProgressMarker: commandProgressMarker}
	evidence.ProgressState = func() string { return evidenceAttemptProgressSet(evidence) }
	report, err := m.runner.withModel(m.models.Tester).executeSubagentLoopWithFormat(ctx, newSubagentID(role), string(TypeTester), string(payload), testerTeamPrompt, reg, testReportSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("tester loop %s: %s", report.Termination, report.Summary)
	}
	if evidence.ToolCallsAttempted == 0 {
		return nil, fmt.Errorf("tester returned without an execute_action call")
	}
	testReport, err := parseTestReport(report.Summary)
	if err != nil {
		return nil, err
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

func (m *ModelTeamRoles) Review(ctx context.Context, plan *DevelopmentPlan, codeReports []CodeReport) (*ReviewReport, error) {
	payload, err := json.Marshal(map[string]any{"plan": plan, "code_reports": codeReports})
	if err != nil {
		return nil, err
	}
	reg := m.runner.newRoleRegistry()
	registerTeamReviewerTools(reg)
	temperature := 0.1
	evidence := &executionEvidence{ProgressMarker: inspectedFileProgressMarker}
	evidence.ProgressState = func() string { return evidenceProgressSet(evidence) }
	report, err := m.runner.withModel(m.models.Reviewer).executeSubagentLoopWithFormat(ctx, newSubagentID("team-reviewer"), string(TypeReviewer), string(payload), reviewerTeamPrompt, reg, reviewReportSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("reviewer loop %s: %s", report.Termination, report.Summary)
	}
	if err := requireReviewerFileEvidence(codeReports, evidence); err != nil {
		return nil, err
	}
	reviewReport, err := parseReviewReport(report.Summary)
	if err != nil {
		return nil, err
	}
	if err := validateReviewReport(plan, reviewReport); err != nil {
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
		if toolName != "edit_file" {
			return ""
		}
		path, _ := arguments["file_path"].(string)
		path, err := normalizePlanPath(path)
		if err != nil {
			return ""
		}
		data, err := os.ReadFile(filepath.Join(workspace, path))
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

func requireReviewerFileEvidence(codeReports []CodeReport, evidence *executionEvidence) error {
	required := make(map[string]struct{})
	for _, report := range codeReports {
		for _, path := range report.ChangedFiles {
			required[path] = struct{}{}
		}
	}
	viewed := make(map[string]struct{})
	for _, call := range evidence.SuccessfulCalls {
		if call.Name != "view_file" {
			continue
		}
		path, _ := call.Arguments["file_path"].(string)
		if normalized, err := normalizePlanPath(path); err == nil {
			viewed[normalized] = struct{}{}
		}
	}
	for path := range required {
		if _, exists := viewed[path]; !exists {
			return fmt.Errorf("reviewer did not successfully inspect changed file %q", path)
		}
	}
	return nil
}

func requireCoderEditEvidence(report *CodeReport, evidence *executionEvidence) error {
	edited := make(map[string]struct{})
	for _, call := range evidence.SuccessfulCalls {
		if call.Name != "edit_file" {
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
		return tools.ViewFile(path, tools.ParseOptionalInt(args, "start_line"), tools.ParseOptionalInt(args, "end_line"), reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "edit_file", Description: "Replace a target chunk in one allowed file", Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
		"file_path": {Type: "string", Description: "Allowed workspace-relative file"}, "target_content": {Type: "string", Description: "Exact existing content"}, "replacement_content": {Type: "string", Description: "Replacement content"},
	}, Required: []string{"file_path", "target_content", "replacement_content"}}}}, func(args map[string]interface{}) (string, error) {
		path, _ := args["file_path"].(string)
		if err := check(path); err != nil {
			return "", err
		}
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(path, target, replacement, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})
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
	registerPlannerTools(reg)
}

func formatCodeTask(task CodeTask) string {
	parts := []string{task.Step.ID, task.Step.Objective, strings.Join(task.Step.AllowedFiles, ", ")}
	return strings.Join(parts, " | ")
}
