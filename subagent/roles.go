package subagent

import (
	"context"
	"fmt"
	"time"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

func (r *SubagentRunner) RunResearcher(task string) (*ResultReport, error) {
	return r.RunResearcherWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunResearcherWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_researcher_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Web Researcher Subagent. Your goal is to gather information using web search and reading web pages, then synthesize a clear, concise report."

	reg := tools.NewRegistry()
	reg.SetWorkspace(r.workspace)

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

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeResearcher), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunCoder(task string) (*ResultReport, error) {
	return r.RunCoderWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunCoderWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_coder_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Software Coder Subagent. Your goal is to inspect code, search files, and perform edits or code refactoring as requested."

	reg := tools.NewRegistry()
	reg.SetWorkspace(r.workspace)

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View lines of code from a file",
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
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "edit_file",
			Description: "Create or replace content in a code file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":           {Type: "string", Description: "File path to edit"},
					"target_content":      {Type: "string", Description: "Target string to replace (empty for new file)"},
					"replacement_content": {Type: "string", Description: "Replacement content string"},
				},
				Required: []string{"file_path", "replacement_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(fp, target, replacement, reg.GetWorkspace())
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
		return tools.GrepSearch(q, sp, reg.GetWorkspace())
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
		return tools.ListDir(dp, reg.GetWorkspace())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeCoder), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunTester(task string) (*ResultReport, error) {
	return r.RunTesterWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunTesterWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_tester_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Software Tester Subagent. Your goal is to dynamically execute test commands (e.g. go test ./...), build scripts, and verify runtime correctness."

	reg := tools.NewRegistry()
	reg.SetWorkspace(r.workspace)

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
		output, newWs, err := tools.ExecuteCommandWithWorkspace(ctx, cmdStr, reg.GetWorkspace())
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
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeTester), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunReviewer(task string) (*ResultReport, error) {
	return r.RunReviewerWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunReviewerWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_reviewer_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Code Reviewer Subagent. Your goal is to inspect code style, security vulnerabilities, edge cases, and architectural clean code principles."

	reg := tools.NewRegistry()
	reg.SetWorkspace(r.workspace)

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
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace())
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
		return tools.GrepSearch(q, sp, reg.GetWorkspace())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeReviewer), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunDocumenter(task string) (*ResultReport, error) {
	return r.RunDocumenterWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunDocumenterWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_documenter_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Technical Documenter Subagent. Your goal is to write comprehensive Markdown documentation, API specs, READMEs, and architecture docs."

	reg := tools.NewRegistry()
	reg.SetWorkspace(r.workspace)

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "edit_file",
			Description: "Create or update Markdown documentation file",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":           {Type: "string", Description: "Doc file path"},
					"target_content":      {Type: "string", Description: "Target string"},
					"replacement_content": {Type: "string", Description: "Replacement content"},
				},
				Required: []string{"file_path", "replacement_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(fp, target, replacement, reg.GetWorkspace())
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View existing documentation or source code file",
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
		return tools.ViewFile(fp, int(start), int(end), reg.GetWorkspace())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypeDocumenter), task, sysPrompt, reg)
}

func (r *SubagentRunner) RunPresenter(task string) (*ResultReport, error) {
	return r.RunPresenterWithContext(context.Background(), task)
}

func (r *SubagentRunner) RunPresenterWithContext(ctx context.Context, task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_presenter_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Presenter Subagent. Your goal is to generate interactive HTML presentation slides with modern CSS glassmorphism, animations, and clean layouts."

	reg := tools.NewRegistry()
	reg.SetWorkspace(r.workspace)

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
		return tools.EditFile(fp, target, replacement, reg.GetWorkspace())
	})

	return r.executeSubagentLoopWithContext(ctx, subID, string(TypePresenter), task, sysPrompt, reg)
}
