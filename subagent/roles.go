package subagent

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

func (r *SubagentRunner) RunResearcher(task string) (*ResultReport, error) {
	return r.RunResearcherWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunResearcherWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_researcher_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Web Researcher Subagent. Your goal is to gather information using web search, URL content reading, subagent report inspection, and past session log retrieval, then synthesize a clear report."

	reg := r.newRoleRegistry()

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "web_search",
			Description: "Search the web for news, documentation, or technical information",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {Type: "string", Description: "Search query string"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		return tools.WebSearch(q)
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "read_url_content",
			Description: "Fetch content from a web URL and convert to clean text",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"url": {Type: "string", Description: "Target web URL"},
				},
				Required: []string{"url"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		u, _ := args["url"].(string)
		return tools.ReadURLContent(u)
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "search_session_history",
			Description: "Search current active conversation session log by keyword to retrieve specific past messages or user directives",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {Type: "string", Description: "Keyword to search in active session log"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		targetPath := reg.GetSessionFile()
		if targetPath == "" {
			targetPath = filepath.Join(reg.GetWorkspaceRoot(), "sessions")
		}
		matches, err := tools.SearchSessionLogs(targetPath, q, reg.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No session log matches found for query '%s'", q), nil
		}
		return fmt.Sprintf("Found %d session log matches for '%s':\n%s", len(matches), q, matches), nil
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "list_subagent_reports",
			Description: "List all past subagent research, code analysis, testing, or review logs saved in workspace",
			Parameters: ollama.FunctionParamSchema{
				Type:       "object",
				Properties: map[string]ollama.FunctionParamProperty{},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		return tools.ListSubagentReports(reg.GetWorkspaceRoot(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_subagent_report",
			Description: "Read contents of a past subagent investigation report file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"report_filename": {Type: "string", Description: "Filename of the report in sessions/subagents (e.g. subagent_coder_...)"},
				},
				Required: []string{"report_filename"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		rf, _ := args["report_filename"].(string)
		return tools.ViewSubagentReport(reg.GetWorkspaceRoot(), rf, reg.GetWorkspaceRoot())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeResearcher), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunCoder(task string) (*ResultReport, error) {
	return r.RunCoderWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunCoderWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_coder_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Software Coder Subagent. Your goal is to inspect code, search files, query session history and past subagent investigation reports, and perform targeted edits, middle insertions ('insert_content'), incremental appends ('append_file'), or code refactoring as requested."

	reg := r.newRoleRegistry()

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View specific lines of code from a file (sliced by start_line and end_line)",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":  {Type: "string", Description: "File path to view"},
					"start_line": {Type: "number", Description: "Start line (1-indexed)"},
					"end_line":   {Type: "number", Description: "End line"},
				},
				Required: []string{"file_path"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		start, _ := args["start_line"].(float64)
		end, _ := args["end_line"].(float64)
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "edit_file",
			Description: "Targeted replace of a specific code chunk or section in a file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":           {Type: "string", Description: "File path to edit"},
					"target_content":      {Type: "string", Description: "Target code chunk to replace (empty to create/overwrite whole file)"},
					"replacement_content": {Type: "string", Description: "Replacement content string"},
				},
				Required: []string{"file_path", "replacement_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(fp, target, replacement, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "insert_content",
			Description: "Insert new code or text right before or right after an anchor line in the middle of a file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":       {Type: "string", Description: "Target file path"},
					"anchor_content":  {Type: "string", Description: "Target line or text chunk in the middle of the file to position against"},
					"insert_position": {Type: "string", Description: "Position relative to anchor: 'after' or 'before'"},
					"new_content":     {Type: "string", Description: "Content text to insert"},
				},
				Required: []string{"file_path", "anchor_content", "new_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		ac, _ := args["anchor_content"].(string)
		pos, _ := args["insert_position"].(string)
		nc, _ := args["new_content"].(string)
		return tools.InsertContent(fp, ac, pos, nc, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "append_file",
			Description: "Append new code or content to the end of a file incrementally without overwriting existing content",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":      {Type: "string", Description: "Target file path to append to"},
					"append_content": {Type: "string", Description: "Content text or code section to append to the end of the file"},
				},
				Required: []string{"file_path", "append_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		ac, _ := args["append_content"].(string)
		return tools.AppendFile(fp, ac, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "grep_search",
			Description: "Search code pattern across workspace files",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query":       {Type: "string", Description: "Keyword to search"},
					"search_path": {Type: "string", Description: "Path to search within"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		sp, _ := args["search_path"].(string)
		return tools.GrepSearch(q, sp, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "list_dir",
			Description: "List directory files and folders",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"dir_path": {Type: "string", Description: "Directory path"},
				},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		dp, _ := args["dir_path"].(string)
		return tools.ListDir(dp, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "search_session_history",
			Description: "Search active conversation session log by keyword to retrieve specific past user preferences or prior code details",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {Type: "string", Description: "Keyword to search in active session log"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		targetPath := reg.GetSessionFile()
		if targetPath == "" {
			targetPath = filepath.Join(reg.GetWorkspaceRoot(), "sessions")
		}
		matches, err := tools.SearchSessionLogs(targetPath, q, reg.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No session log matches found for query '%s'", q), nil
		}
		return fmt.Sprintf("Found %d session log matches for '%s':\n%s", len(matches), q, matches), nil
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "list_subagent_reports",
			Description: "List all past subagent research, code analysis, testing, or review logs saved in workspace",
			Parameters: ollama.FunctionParamSchema{
				Type:       "object",
				Properties: map[string]ollama.FunctionParamProperty{},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		return tools.ListSubagentReports(reg.GetWorkspaceRoot(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_subagent_report",
			Description: "Read contents of a past subagent investigation report file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"report_filename": {Type: "string", Description: "Filename of the report in sessions/subagents"},
				},
				Required: []string{"report_filename"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		rf, _ := args["report_filename"].(string)
		return tools.ViewSubagentReport(reg.GetWorkspaceRoot(), rf, reg.GetWorkspaceRoot())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeCoder), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunTester(task string) (*ResultReport, error) {
	return r.RunTesterWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunTesterWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_tester_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Software Tester Subagent. Your goal is to dynamically execute test commands (e.g. go test ./...), build scripts, query session history, and verify runtime correctness."

	reg := r.newRoleRegistry()

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "run_terminal_command",
			Description: "Execute test or build terminal commands safely within workspace (e.g. 'go test ./...'). Supports 'cd <dir>' to switch working directory.",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"command": {Type: "string", Description: "Full command string to execute (e.g. 'go test ./...' or 'cd ~/llm-pg')"},
				},
				Required: []string{"command"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		cmdStr := tools.ParseCommandArgs(args)
		if cmdStr == "" {
			return "", fmt.Errorf("missing or empty 'command' argument")
		}
		output, newWs, err := tools.ExecuteCommandWithWorkspace(ctx, cmdStr, reg.GetWorkspace(), reg.GetWorkspaceRoot())
		if newWs != reg.GetWorkspace() {
			reg.SetWorkspace(newWs)
		}
		return output, err
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View test log files or test source files",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":  {Type: "string", Description: "File path to view"},
					"start_line": {Type: "number", Description: "Start line"},
					"end_line":   {Type: "number", Description: "End line"},
				},
				Required: []string{"file_path"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		start, _ := args["start_line"].(float64)
		end, _ := args["end_line"].(float64)
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "search_session_history",
			Description: "Search active conversation session log by keyword to retrieve specific past test expectations or details",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {Type: "string", Description: "Keyword to search in active session log"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		targetPath := reg.GetSessionFile()
		if targetPath == "" {
			targetPath = filepath.Join(reg.GetWorkspaceRoot(), "sessions")
		}
		matches, err := tools.SearchSessionLogs(targetPath, q, reg.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No session log matches found for query '%s'", q), nil
		}
		return fmt.Sprintf("Found %d session log matches for '%s':\n%s", len(matches), q, matches), nil
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeTester), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunReviewer(task string) (*ResultReport, error) {
	return r.RunReviewerWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunReviewerWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_reviewer_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Code Reviewer Subagent. Your goal is to inspect code style, security vulnerabilities, edge cases, session history, and architectural clean code principles."

	reg := r.newRoleRegistry()

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View file contents for review",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":  {Type: "string", Description: "File path"},
					"start_line": {Type: "number", Description: "Start line"},
					"end_line":   {Type: "number", Description: "End line"},
				},
				Required: []string{"file_path"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		start, _ := args["start_line"].(float64)
		end, _ := args["end_line"].(float64)
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "grep_search",
			Description: "Search code patterns for review",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query":       {Type: "string", Description: "Pattern query"},
					"search_path": {Type: "string", Description: "Search path"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		sp, _ := args["search_path"].(string)
		return tools.GrepSearch(q, sp, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "search_session_history",
			Description: "Search active conversation session log by keyword to retrieve specific past requirements",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {Type: "string", Description: "Keyword to search in active session log"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		targetPath := reg.GetSessionFile()
		if targetPath == "" {
			targetPath = filepath.Join(reg.GetWorkspaceRoot(), "sessions")
		}
		matches, err := tools.SearchSessionLogs(targetPath, q, reg.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No session log matches found for query '%s'", q), nil
		}
		return fmt.Sprintf("Found %d session log matches for '%s':\n%s", len(matches), q, matches), nil
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeReviewer), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunDocumenter(task string) (*ResultReport, error) {
	return r.RunDocumenterWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunDocumenterWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_documenter_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Technical Documenter Subagent. Your goal is to write comprehensive Markdown documentation, API specs, and READMEs. FLEXIBLE EDITING INSTRUCTION: You can view specific line ranges with 'view_file(path, start, end)', replace targeted sections with 'edit_file(path, target_content, replacement_content)', insert new sections in the middle with 'insert_content(path, anchor_content, insert_position, new_content)', or append new sections to the end with 'append_file(path, append_content)'. Before writing, inspect subagent investigation findings ('list_subagent_reports' / 'view_subagent_report'), active session history ('search_session_history'), and real source code."

	reg := r.newRoleRegistry()

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "list_subagent_reports",
			Description: "List all past subagent research, code analysis, testing, or review investigation logs saved in workspace",
			Parameters: ollama.FunctionParamSchema{
				Type:       "object",
				Properties: map[string]ollama.FunctionParamProperty{},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		return tools.ListSubagentReports(reg.GetWorkspaceRoot(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_subagent_report",
			Description: "Read contents of a past subagent investigation report file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"report_filename": {Type: "string", Description: "Filename of the report in sessions/subagents"},
				},
				Required: []string{"report_filename"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		rf, _ := args["report_filename"].(string)
		return tools.ViewSubagentReport(reg.GetWorkspaceRoot(), rf, reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "search_session_history",
			Description: "Search active conversation session log by keyword to retrieve specific past discussion details for technical documentation",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {Type: "string", Description: "Keyword to search in active session log"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		targetPath := reg.GetSessionFile()
		if targetPath == "" {
			targetPath = filepath.Join(reg.GetWorkspaceRoot(), "sessions")
		}
		matches, err := tools.SearchSessionLogs(targetPath, q, reg.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No session log matches found for query '%s'", q), nil
		}
		return fmt.Sprintf("Found %d session log matches for '%s':\n%s", len(matches), q, matches), nil
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "list_dir",
			Description: "List directory files and folders to explore project structure",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"dir_path": {Type: "string", Description: "Directory path"},
				},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		dp, _ := args["dir_path"].(string)
		return tools.ListDir(dp, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View existing documentation or source code file by line-range chunk (start_line, end_line)",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":  {Type: "string", Description: "File path"},
					"start_line": {Type: "number", Description: "Start line (1-indexed)"},
					"end_line":   {Type: "number", Description: "End line"},
				},
				Required: []string{"file_path"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		start, _ := args["start_line"].(float64)
		end, _ := args["end_line"].(float64)
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "grep_search",
			Description: "Search code patterns across workspace files",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query":       {Type: "string", Description: "Keyword to search"},
					"search_path": {Type: "string", Description: "Path to search within"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		sp, _ := args["search_path"].(string)
		return tools.GrepSearch(q, sp, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "edit_file",
			Description: "Targeted replace of a specific section/chunk or create/overwrite a file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":           {Type: "string", Description: "Doc file path"},
					"target_content":      {Type: "string", Description: "Target section chunk to replace (empty to create new file)"},
					"replacement_content": {Type: "string", Description: "Replacement content string"},
				},
				Required: []string{"file_path", "replacement_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(fp, target, replacement, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "insert_content",
			Description: "Insert new content or section right before or right after an anchor line/text in the middle of a file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":       {Type: "string", Description: "Target file path"},
					"anchor_content":  {Type: "string", Description: "Target line or text chunk in the middle of the file to position against"},
					"insert_position": {Type: "string", Description: "Position relative to anchor: 'after' or 'before'"},
					"new_content":     {Type: "string", Description: "Content text to insert"},
				},
				Required: []string{"file_path", "anchor_content", "new_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		ac, _ := args["anchor_content"].(string)
		pos, _ := args["insert_position"].(string)
		nc, _ := args["new_content"].(string)
		return tools.InsertContent(fp, ac, pos, nc, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "append_file",
			Description: "Append new markdown section or content to the end of a file incrementally without overwriting existing content",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":      {Type: "string", Description: "Target file path to append to"},
					"append_content": {Type: "string", Description: "Content text or markdown section to append to the end of the file"},
				},
				Required: []string{"file_path", "append_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		ac, _ := args["append_content"].(string)
		return tools.AppendFile(fp, ac, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeDocumenter), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunPresenter(task string) (*ResultReport, error) {
	return r.RunPresenterWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunPresenterWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_presenter_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Presenter Subagent. Your goal is to generate interactive HTML presentation slides with modern CSS glassmorphism, animations, and query session logs for content."

	reg := r.newRoleRegistry()

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "edit_file",
			Description: "Create or update HTML presentation file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":           {Type: "string", Description: "HTML file path"},
					"target_content":      {Type: "string", Description: "Target string"},
					"replacement_content": {Type: "string", Description: "Replacement HTML content"},
				},
				Required: []string{"file_path", "replacement_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(fp, target, replacement, reg.GetWorkspace(), reg.GetWorkspaceRoot())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "search_session_history",
			Description: "Search active conversation session log by keyword to retrieve specific presentation topics",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {Type: "string", Description: "Keyword to search in past session logs"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		targetPath := reg.GetSessionFile()
		if targetPath == "" {
			targetPath = filepath.Join(reg.GetWorkspaceRoot(), "sessions")
		}
		matches, err := tools.SearchSessionLogs(targetPath, q, reg.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No session log matches found for query '%s'", q), nil
		}
		return fmt.Sprintf("Found %d session log matches for '%s':\n%s", len(matches), q, matches), nil
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypePresenter), task, sysPrompt, reg)
}
