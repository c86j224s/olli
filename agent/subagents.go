package agent

import (
	"context"
	"fmt"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/subagent"
)

func (a *Agent) buildSubagentCallbacks() subagent.SubagentCallbacks {
	return subagent.SubagentCallbacks{
		OnThinkingStart: func(subType string) {
			if a.activeCB.OnSubagentThinkingStart != nil {
				a.activeCB.OnSubagentThinkingStart(subType)
			}
		},
		OnThinkingToken: func(token string) {
			if a.activeCB.OnSubagentThinkingToken != nil {
				a.activeCB.OnSubagentThinkingToken(token)
			}
		},
		OnThinkingEnd: func() {
			if a.activeCB.OnSubagentThinkingEnd != nil {
				a.activeCB.OnSubagentThinkingEnd()
			}
		},
		OnToolCall: func(subType string, toolName string, args map[string]interface{}, result string, execErr error) {
			if a.activeCB.OnSubagentToolCall != nil {
				a.activeCB.OnSubagentToolCall(subType, toolName, args, result, execErr)
			}
		},
	}
}

func (a *Agent) registerSubagentToolsWithContext(ctx context.Context) {
	syncWorkingDir := func(report *subagent.ResultReport) {
		if report != nil && report.WorkingDir != "" && report.WorkingDir != a.currentDir {
			a.SetCurrentDir(report.WorkingDir)
		}
	}

	// 1. delegate_researcher
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_researcher",
			Description: "[PREFERRED TOOL FOR WEB RESEARCH] Delegate web searching and web page reading to a specialized Web Researcher Subagent",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {
						Type:        "string",
						Description: "Detailed research topic or web search task description",
					},
				},
				Required: []string{"task_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		subCB := a.buildSubagentCallbacks()
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, subCB)
		report, err := runner.RunResearcherWithContext(ctx, task)
		if err != nil {
			return "", fmt.Errorf("researcher subagent failed: %w", err)
		}
		syncWorkingDir(report)
		return fmt.Sprintf("🔍 [Researcher Subagent Report]\nTask: %s\nStatus: %s\nSummary: %s\nWorking Dir: %s\nTurn Log Saved To: %s\n(Tool calls run: %d)",
			report.Task, report.Status, report.Summary, report.WorkingDir, report.JSONLFile, report.ToolCallsRun), nil
	})

	// 2. delegate_coder
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_coder",
			Description: "[PREFERRED TOOL FOR CODE IMPLEMENTATION & FILE EDITING] Delegate code writing, editing, file creation, or refactoring to a specialized Coder Subagent",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {
						Type:        "string",
						Description: "Detailed code modification or writing task description",
					},
				},
				Required: []string{"task_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		subCB := a.buildSubagentCallbacks()
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, subCB)
		report, err := runner.RunCoderWithContext(ctx, task)
		if err != nil {
			return "", fmt.Errorf("coder subagent failed: %w", err)
		}
		syncWorkingDir(report)
		return fmt.Sprintf("💻 [Coder Subagent Report]\nTask: %s\nStatus: %s\nSummary: %s\nWorking Dir: %s\nTurn Log Saved To: %s\n(Tool calls run: %d)",
			report.Task, report.Status, report.Summary, report.WorkingDir, report.JSONLFile, report.ToolCallsRun), nil
	})

	// 3. delegate_tester
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_tester",
			Description: "[PREFERRED TOOL FOR TESTING & BUILD VERIFICATION] Delegate dynamic build verification, test running (go test ./...), and runtime error checking to a specialized Tester Subagent",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {
						Type:        "string",
						Description: "Detailed testing or build verification task description",
					},
				},
				Required: []string{"task_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		subCB := a.buildSubagentCallbacks()
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, subCB)
		report, err := runner.RunTesterWithContext(ctx, task)
		if err != nil {
			return "", fmt.Errorf("tester subagent failed: %w", err)
		}
		syncWorkingDir(report)
		return fmt.Sprintf("🧪 [Tester Subagent Report]\nTask: %s\nStatus: %s\nSummary: %s\nWorking Dir: %s\nTurn Log Saved To: %s\n(Tool calls run: %d)",
			report.Task, report.Status, report.Summary, report.WorkingDir, report.JSONLFile, report.ToolCallsRun), nil
	})

	// 4. delegate_reviewer
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_reviewer",
			Description: "[PREFERRED TOOL FOR STATIC CODE REVIEW] Delegate static code analysis, code style review, readability checks, and edge-case code review to a specialized Reviewer Subagent",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {
						Type:        "string",
						Description: "Detailed code review task description",
					},
				},
				Required: []string{"task_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		subCB := a.buildSubagentCallbacks()
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, subCB)
		report, err := runner.RunReviewerWithContext(ctx, task)
		if err != nil {
			return "", fmt.Errorf("reviewer subagent failed: %w", err)
		}
		syncWorkingDir(report)
		return fmt.Sprintf("🧐 [Reviewer Subagent Report]\nTask: %s\nStatus: %s\nSummary: %s\nWorking Dir: %s\nTurn Log Saved To: %s\n(Tool calls run: %d)",
			report.Task, report.Status, report.Summary, report.WorkingDir, report.JSONLFile, report.ToolCallsRun), nil
	})

	// 5. delegate_documenter
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_documenter",
			Description: "[PREFERRED TOOL FOR MARKDOWN DOCUMENTATION] Delegate writing technical Markdown docs, READMEs, architecture specs, or manuals to a specialized Documenter Subagent",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {
						Type:        "string",
						Description: "Detailed documentation writing task description",
					},
				},
				Required: []string{"task_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		subCB := a.buildSubagentCallbacks()
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, subCB)
		report, err := runner.RunDocumenterWithContext(ctx, task)
		if err != nil {
			return "", fmt.Errorf("documenter subagent failed: %w", err)
		}
		syncWorkingDir(report)
		return fmt.Sprintf("📝 [Documenter Subagent Report]\nTask: %s\nStatus: %s\nSummary: %s\nWorking Dir: %s\nTurn Log Saved To: %s\n(Tool calls run: %d)",
			report.Task, report.Status, report.Summary, report.WorkingDir, report.JSONLFile, report.ToolCallsRun), nil
	})

	// 6. delegate_presenter
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_presenter",
			Description: "[PREFERRED TOOL FOR INTERACTIVE HTML PPT SLIDES] Delegate creating interactive HTML PPT slide presentations from docs or specs to a specialized Presenter Subagent",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {
						Type:        "string",
						Description: "Detailed PPT slide deck generation task description",
					},
				},
				Required: []string{"task_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		subCB := a.buildSubagentCallbacks()
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, subCB)
		report, err := runner.RunPresenterWithContext(ctx, task)
		if err != nil {
			return "", fmt.Errorf("presenter subagent failed: %w", err)
		}
		syncWorkingDir(report)
		return fmt.Sprintf("📊 [Presenter Subagent Report]\nTask: %s\nStatus: %s\nSummary: %s\nWorking Dir: %s\nTurn Log Saved To: %s\n(Tool calls run: %d)",
			report.Task, report.Status, report.Summary, report.WorkingDir, report.JSONLFile, report.ToolCallsRun), nil
	})
}
