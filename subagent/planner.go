package subagent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

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
- Plan 1-4 small sequential steps.
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
	evidence := &executionEvidence{}
	report, err := r.executeSubagentLoopWithFormat(ctx, subID, string(TypePlanner), task, plannerSystemPrompt, reg, developmentPlanSchema(), &temperature, evidence)
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
	if err != nil {
		return report, nil, err
	}
	return report, plan, nil
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
