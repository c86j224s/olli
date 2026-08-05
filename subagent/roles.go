package subagent

import (
	"fmt"
	"time"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

// RunResearcher Task creates a dedicated Researcher subagent with web tools
func (r *SubagentRunner) RunResearcher(task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_researcher_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Web Researcher Subagent. Your goal is to gather information using web search and reading web pages, then synthesize a clear, concise report."

	reg := tools.NewRegistry()
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

	return r.executeSubagentLoop(subID, string(TypeResearcher), task, sysPrompt, reg)
}

// RunCoder Task creates a dedicated Coder subagent with file reading/editing tools
func (r *SubagentRunner) RunCoder(task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_coder_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Software Coder Subagent. Your goal is to inspect code, search files, and perform edits or code refactoring as requested."

	reg := tools.NewRegistry()
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
		return tools.ViewFile(fp, int(start), int(end), r.workspace)
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
		return tools.EditFile(fp, target, replacement, r.workspace)
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
		return tools.GrepSearch(q, sp, r.workspace)
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
		return tools.ListDir(dp, r.workspace)
	})

	return r.executeSubagentLoop(subID, string(TypeCoder), task, sysPrompt, reg)
}

// RunTester Task creates a dedicated Tester subagent to execute builds & tests
func (r *SubagentRunner) RunTester(task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_tester_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Software Tester Subagent. Your goal is to dynamically execute test commands (e.g. go test ./...), build scripts, and verify runtime correctness."

	reg := tools.NewRegistry()
	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "run_terminal_command",
			Description: "Execute test or build terminal commands safely within workspace (e.g. 'go test ./...')",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"command": {Type: "string", Description: "Full command string to execute (e.g. 'go test ./...')"},
				},
				Required: []string{"command"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		cmdStr := tools.ParseCommandArgs(args)
		if cmdStr == "" {
			return "", fmt.Errorf("missing or empty 'command' argument")
		}
		if err := tools.ValidateCommandSafety(cmdStr, r.workspace); err != nil {
			return "", fmt.Errorf("security block: %w", err)
		}
		return tools.ExecuteCommand(cmdStr, r.workspace)
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
		return tools.ViewFile(fp, int(start), int(end), r.workspace)
	})

	return r.executeSubagentLoop(subID, string(TypeTester), task, sysPrompt, reg)
}

// RunReviewer Task creates a dedicated Reviewer subagent to perform static code reviews
func (r *SubagentRunner) RunReviewer(task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_reviewer_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Code Reviewer Subagent. Your goal is to statically inspect code diffs, review code readability, check edge cases, and verify architectural alignment."

	reg := tools.NewRegistry()
	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View lines of code from a file for static review",
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
		return tools.ViewFile(fp, int(start), int(end), r.workspace)
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "grep_search",
			Description: "Search code patterns across workspace files for code review",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query":       {Type: "string", Description: "Keyword or pattern to search"},
					"search_path": {Type: "string", Description: "Path to search within"},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		q, _ := args["query"].(string)
		sp, _ := args["search_path"].(string)
		return tools.GrepSearch(q, sp, r.workspace)
	})

	return r.executeSubagentLoop(subID, string(TypeReviewer), task, sysPrompt, reg)
}

// RunDocumenter Task creates a dedicated Documenter subagent to write clean Markdown documentation
func (r *SubagentRunner) RunDocumenter(task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_documenter_%s", time.Now().Format("20060102_150405"))
	sysPrompt := "You are a specialized Technical Documenter Subagent. Your goal is to write well-structured Markdown documentation, READMEs, API specs, and technical manuals using edit_file."

	reg := tools.NewRegistry()
	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View source files or existing documentation files",
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
		return tools.ViewFile(fp, int(start), int(end), r.workspace)
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "edit_file",
			Description: "Create or update Markdown documentation files (.md)",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":           {Type: "string", Description: "Markdown file path (e.g. README.md or docs/manual.md)"},
					"target_content":      {Type: "string", Description: "Target text to replace (empty for new file)"},
					"replacement_content": {Type: "string", Description: "Markdown content to write"},
				},
				Required: []string{"file_path", "replacement_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(fp, target, replacement, r.workspace)
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "list_dir",
			Description: "List workspace files to organize documentation structure",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"dir_path": {Type: "string", Description: "Directory path"},
				},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		dp, _ := args["dir_path"].(string)
		return tools.ListDir(dp, r.workspace)
	})

	return r.executeSubagentLoop(subID, string(TypeDocumenter), task, sysPrompt, reg)
}

// RunPresenter Task creates a dedicated Presenter subagent to generate Interactive HTML PPT Slides
func (r *SubagentRunner) RunPresenter(task string) (*ResultReport, error) {
	subID := fmt.Sprintf("subagent_presenter_%s", time.Now().Format("20060102_150405"))
	sysPrompt := `You are a specialized Presentation Designer Subagent. Your goal is to transform Markdown documents, technical specs, or meeting notes into a self-contained Interactive HTML PPT Slide presentation.
The generated HTML file should feature:
- Modern dark glassmorphism aesthetic CSS with smooth slide transitions.
- Interactive keyboard arrow navigation (Left Arrow ⬅️ / Right Arrow ➡️) and clickable Prev/Next buttons.
- Dynamic Slide Indicators (e.g., Slide 1 of N) and progress bar.
Write the final standalone HTML file using edit_file.`

	reg := tools.NewRegistry()
	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "view_file",
			Description: "View source Markdown or text files to build slides from",
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
		return tools.ViewFile(fp, int(start), int(end), r.workspace)
	})

	reg.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "edit_file",
			Description: "Create or write the interactive HTML PPT presentation slide file (.html)",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"file_path":           {Type: "string", Description: "Target HTML presentation file path (e.g., presentation.html)"},
					"target_content":      {Type: "string", Description: "Target content to replace (empty for new file)"},
					"replacement_content": {Type: "string", Description: "Complete HTML/CSS/JS slide deck content"},
				},
				Required: []string{"file_path", "replacement_content"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		fp, _ := args["file_path"].(string)
		target, _ := args["target_content"].(string)
		replacement, _ := args["replacement_content"].(string)
		return tools.EditFile(fp, target, replacement, r.workspace)
	})

	return r.executeSubagentLoop(subID, string(TypePresenter), task, sysPrompt, reg)
}
