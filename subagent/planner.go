package subagent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	agentloop "github.com/c86j224s/olli/loop"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

const plannerSystemPrompt = `ROLE: Read-only software planner for a small-model development team.

TASK:
Inspect only the files needed to understand the delegated objective. Produce a short implementation plan for one writer agent.

RULES:
- Never modify files and never run commands.
- Use list_dir and view_file to inspect relevant files. Use grep_search only when file names are unknown.
- After two relevant files have been read successfully, stop calling tools and return the final JSON.
- Plan 1-24 minimal sequential milestones that cumulatively satisfy the ENTIRE delegated objective.
- Decompose substantial work as finely as practical: one cohesive state change or behavior per milestone, normally 3-8 milestones for a feature and up to 12 for a larger objective.
- A file may appear in multiple milestones. Every milestone must leave all touched source parseable and must be independently inspectable by deterministic static preflight.
- Prefer one milestone only for a genuinely small edit. Never combine unrelated data structures, algorithms, I/O, and UI behavior merely because they share one file.
- Never reduce the requested scope to setup, scaffolding, a foundation, or a partial implementation. Never defer a requested requirement outside the plan.
- The final step's acceptance criteria must cover every user-visible requirement not already completed by earlier steps.
- Every step must name exact workspace-relative allowed_files and observable acceptance criteria.
- Verification entries are machine commands, never prose. Allowed exact forms: "go_test", "go_test ./path", "go_vet", "go_vet ./path", "git_status", "git_diff", or "git_diff file".
- final_verification must include "go_test ./..." and "go_vet ./...".
- Keep verification empty for an intermediate step that cannot be tested independently.
- Do not create verification-only steps; final_verification handles whole-repository checks.
- Do not duplicate objectives or steps.
- Do not invent files or APIs that you did not inspect.
- Return JSON only, matching the supplied schema.`

func (r *SubagentRunner) RunPlanner(task string) (*ResultReport, *DevelopmentPlan, error) {
	return r.RunPlannerWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunPlannerWithContext(ctx context.Context, task string) (*ResultReport, *DevelopmentPlan, error) {
	subID := newSubagentID("planner")
	reg := r.newRoleRegistry()
	registerPlannerTools(reg)

	temperature := 0.1
	evidence := &executionEvidence{ProgressMarker: plannerProgressMarker, RequiredCalls: requiredPlannerViewCalls(task)}
	evidence.ProgressState = func() string { return evidenceProgressSet(evidence) }
	evidence.CompletionReady = func() bool { return len(evidence.missingRequiredTools()) == 0 }
	report, err := r.withRole("planner").executeSubagentLoopWithFormat(ctx, subID, string(TypePlanner), task, plannerSystemPrompt, reg, developmentPlanSchema(), &temperature, evidence)
	if err != nil {
		return nil, nil, err
	}
	if report.Status != "SUCCESS" {
		return report, nil, fmt.Errorf("planner returned %s: %s", report.Status, report.Summary)
	}
	if evidence.ToolCallsSucceeded == 0 {
		return report, nil, fmt.Errorf("planner returned a plan without successfully inspecting workspace evidence")
	}
	plan, err := parseDevelopmentPlanForWorkspace(report.Summary, r.workspace)
	if err == nil {
		return report, plan, nil
	}
	repaired, repairErr := r.withRole("planner").repairDevelopmentPlan(ctx, task, report.Summary, err)
	if repairErr != nil {
		return report, nil, fmt.Errorf("planner output validation failed (%v), repair failed: %w", err, repairErr)
	}
	plan, err = parseDevelopmentPlanForWorkspace(repaired, r.workspace)
	if err != nil {
		return report, nil, fmt.Errorf("planner output remained invalid after one repair: %w", err)
	}
	report.Summary = repaired
	if report.LoopMetrics != nil {
		report.LoopMetrics.FormatRepairs++
	}
	return report, plan, nil
}

func (r *SubagentRunner) repairDevelopmentPlan(ctx context.Context, task string, invalid string, validationErr error) (completion string, runErr error) {
	if r != nil && r.leaseProvider != nil {
		role := r.role
		if role == "" {
			role = "planner"
		}
		routed, release, err := r.routed(ctx, role)
		if err != nil {
			return "", fmt.Errorf("route planner repair model %s: %w", r.model, err)
		}
		defer func() { release(runErr) }()
		return routed.repairDevelopmentPlan(ctx, task, invalid, validationErr)
	}
	numCtx := 32768
	if r.cfg != nil && r.cfg.NumCtx > 0 {
		numCtx = r.cfg.NumCtx
	}
	numPredict := defaultPlannerNumPredict
	request := ollama.ChatRequest{
		Model: r.model,
		Messages: []ollama.Message{
			{Role: "system", Content: plannerSystemPrompt},
			{Role: "user", Content: task},
			{Role: "assistant", Content: invalid},
			{Role: "system", Content: fmt.Sprintf("The plan was rejected by deterministic validation: %v. Correct only the JSON plan. Do not call tools. Return one JSON object. Keep genuinely small edits in one milestone; decompose substantial work into minimal cohesive parseable milestones, up to 24.", validationErr)},
		},
		Format: developmentPlanSchema(),
		Options: &ollama.Options{
			NumCtx:      numCtx,
			NumPredict:  &numPredict,
			Temperature: 0.0,
		},
		Think: r.think,
	}
	response, err := r.client.ChatStreamFullWithContext(ctx, request, ollama.StreamCallbacks{})
	if err != nil {
		return "", err
	}
	if !looksLikeJSONObject(response.Content) {
		return "", fmt.Errorf("repair did not return a JSON object")
	}
	return response.Content, nil
}

func requiredPlannerViewCalls(task string) []requiredToolCall {
	path := singleExplicitTaskFile(task)
	if path == "" {
		return nil
	}
	arguments := map[string]interface{}{"file_path": path}
	return []requiredToolCall{{
		Name:        "view_file",
		Fingerprint: agentloop.ActionFingerprint("view_file", arguments),
		Description: "view_file " + path,
	}}
}

func singleExplicitTaskFile(task string) string {
	words := strings.FieldsFunc(task, func(r rune) bool {
		switch r {
		case ' ', '\t', '\r', '\n', '`', '\'', '"', '[', ']', '(', ')', '{', '}', ',', ';', ':':
			return true
		default:
			return false
		}
	})
	var candidates []string
	for _, word := range words {
		word = strings.Trim(strings.TrimSpace(word), ".!?")
		if _, err := normalizePlanPath(word); err != nil || filepath.Ext(word) == "" {
			continue
		}
		if strings.EqualFold(filepath.Base(word), "go.mod") || strings.EqualFold(filepath.Base(word), "go.sum") {
			continue
		}
		candidates = append(candidates, filepath.Clean(word))
	}
	candidates = uniqueStrings(candidates)
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

func registerPlannerTools(reg *tools.Registry) {
	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{
		Name: "list_dir", Description: "List files and directories inside the workspace",
		Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
			"dir_path": {Type: "string", Description: "Workspace-relative directory path; empty means current workspace"},
		}},
	}}, func(args map[string]interface{}) (string, error) {
		dir, _ := args["dir_path"].(string)
		result, err := tools.ListDir(dir, reg.GetWorkspace(), reg.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}
		return result + "\nUse workspace-relative paths exactly as listed when calling tools and in the final plan.", nil
	})

	reg.Register(ollama.Tool{Type: "function", Function: ollama.FunctionDef{
		Name: "grep_search", Description: "Search source code for symbols or text; use only when relevant file names are unknown",
		Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
			"query":       {Type: "string", Description: "Exact symbol or text to find"},
			"search_path": {Type: "string", Description: "Optional workspace-relative directory"},
		}, Required: []string{"query"}},
	}}, func(args map[string]interface{}) (string, error) {
		query, _ := args["query"].(string)
		searchPath, _ := args["search_path"].(string)
		searchPath = normalizePlannerToolPath(searchPath, reg.GetWorkspace())
		return tools.GrepSearch(query, searchPath, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

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

func normalizePlannerToolPath(path string, workspace string) string {
	path = strings.TrimSpace(path)
	workspace = filepath.Clean(workspace)
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(workspace, filepath.Clean(path))
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return relative
		}
		return path
	}

	clean := filepath.Clean(path)
	trimmedWorkspace := strings.TrimPrefix(workspace, string(filepath.Separator))
	if strings.HasPrefix(clean, trimmedWorkspace+string(filepath.Separator)) {
		candidate := string(filepath.Separator) + clean
		if relative, err := filepath.Rel(workspace, candidate); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return relative
		}
	}
	return clean
}
