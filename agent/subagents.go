package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/subagent"
	"github.com/c86j224s/olli/tools"
)

func (a *Agent) buildSubagentCallbacks(ctx context.Context) subagent.SubagentCallbacks {
	cb := callbacksFromContext(ctx)
	return subagent.SubagentCallbacks{
		OnThinkingStart: func(subType string) {
			if cb.OnSubagentThinkingStart != nil {
				cb.OnSubagentThinkingStart(subType)
			}
		},
		OnThinkingToken: func(token string) {
			if cb.OnSubagentThinkingToken != nil {
				cb.OnSubagentThinkingToken(token)
			}
		},
		OnThinkingEnd: func() {
			if cb.OnSubagentThinkingEnd != nil {
				cb.OnSubagentThinkingEnd()
			}
		},
		OnToolCall: func(subType string, toolName string, args map[string]interface{}, result string, execErr error) {
			if cb.OnSubagentToolCall != nil {
				cb.OnSubagentToolCall(subType, toolName, args, result, execErr)
			}
		},
	}
}

func (a *Agent) buildEnrichedSubagentTask(rawTask string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 [CONTEXT FROM MAIN AGENT]:\n- Active Working Directory: %s\n", a.currentDir))

	if a.activeGoal != "" {
		sb.WriteString(fmt.Sprintf("- Active Goal / Mission: %s\n", a.activeGoal))
	}
	if a.summary != "" {
		sb.WriteString(fmt.Sprintf("- Conversation Memory Summary: %s\n", a.summary))
	}

	sb.WriteString(fmt.Sprintf("\n🎯 [DELEGATED TASK OBJECTIVE]:\n%s", rawTask))
	return sb.String()
}

func (a *Agent) getSessionFilePath() string {
	if a.sessMgr != nil {
		return a.sessMgr.GetCurrentPath()
	}
	return ""
}

func (a *Agent) getWorkspaceRoot() string {
	if a.registry != nil {
		return a.registry.GetWorkspaceRoot()
	}
	return a.initialDir
}

func formatSubagentReport(title string, report *subagent.ResultReport) string {
	return fmt.Sprintf("%s\nTask: %s\nStatus: %s\nSummary: %s\nWorking Dir: %s\nArtifact Files: %s\nCreated Files: %s\nTurn Log Saved To: %s\n(Tool calls run: %d)",
		title,
		report.Task,
		report.Status,
		report.Summary,
		report.WorkingDir,
		formatSubagentPathList(report.ArtifactFiles),
		formatSubagentPathList(report.CreatedFiles),
		report.JSONLFile,
		report.ToolCallsRun)
}

func formatSubagentPathList(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}
	return strings.Join(paths, ", ")
}

func validateRequiredSubagentArtifacts(label string, report *subagent.ResultReport, workspaceRoot string) error {
	if report == nil {
		return fmt.Errorf("%s subagent returned no report", label)
	}
	if report.Status != "SUCCESS" {
		return fmt.Errorf("%s subagent returned %s: %s (log: %s)", label, report.Status, report.Summary, report.JSONLFile)
	}
	if err := subagent.ValidateResultArtifacts(report, workspaceRoot); err != nil {
		return fmt.Errorf("%s subagent artifact verification failed: %w (log: %s)", label, err, report.JSONLFile)
	}
	return nil
}

func (a *Agent) registerSubagentTools() {
	// 1. delegate_dev_team
	a.mustRegisterContext(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_dev_team",
			Description: "[PREFERRED TOOL FOR END-TO-END DEVELOPMENT] Run a deterministic Planner -> Coder -> Tester -> Reviewer -> Verifier team with one writer and bounded fix rounds",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {Type: "string", Description: "Focused implementation objective, constraints, and acceptance criteria"},
				},
				Required: []string{"task_description"},
			},
		},
	}, tools.ToolMetadata{WorkflowCallable: false}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), a.buildSubagentCallbacks(ctx), a.getWorkspaceRoot())
		roles, err := subagent.NewModelTeamRoles(runner)
		if err != nil {
			return "", err
		}
		team, err := subagent.NewDevelopmentTeamRunner(roles, 2)
		if err != nil {
			return "", err
		}
		report := team.Run(ctx, a.buildEnrichedSubagentTask(task))
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return "", fmt.Errorf("development team result serialization failed: %w", err)
		}
		return string(data), nil
	})

	// 2. delegate_planner
	a.mustRegisterContext(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "delegate_planner",
			Description: "[PREFERRED FIRST STEP FOR MULTI-FILE DEVELOPMENT] Delegate read-only code inspection and produce a validated step-by-step implementation plan",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"task_description": {Type: "string", Description: "Focused development objective and constraints to plan"},
				},
				Required: []string{"task_description"},
			},
		},
	}, tools.ToolMetadata{WorkflowCallable: false}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		enrichedTask := a.buildEnrichedSubagentTask(task)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), a.buildSubagentCallbacks(ctx), a.getWorkspaceRoot())
		report, plan, err := runner.RunPlannerWithContext(ctx, enrichedTask)
		if err != nil {
			return "", fmt.Errorf("planner subagent failed: %w", err)
		}
		planJSON, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return "", fmt.Errorf("planner result serialization failed: %w", err)
		}
		return formatSubagentReport("🧭 [Planner Subagent Report]", report) + "\nValidated Plan:\n" + string(planJSON), nil
	})

	// 2. delegate_researcher
	a.mustRegisterContext(ollama.Tool{
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
	}, tools.ToolMetadata{WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		enrichedTask := a.buildEnrichedSubagentTask(task)
		subCB := a.buildSubagentCallbacks(ctx)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), subCB, a.getWorkspaceRoot())
		report, err := runner.RunResearcherWithContext(ctx, enrichedTask)
		if err != nil {
			return "", fmt.Errorf("researcher subagent failed: %w", err)
		}
		return formatSubagentReport("🔍 [Researcher Subagent Report]", report), nil
	})

	// 2. delegate_coder
	a.mustRegisterContext(ollama.Tool{
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
	}, tools.ToolMetadata{WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		enrichedTask := a.buildEnrichedSubagentTask(task)
		subCB := a.buildSubagentCallbacks(ctx)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), subCB, a.getWorkspaceRoot())
		report, err := runner.RunCoderWithContext(ctx, enrichedTask)
		if err != nil {
			return "", fmt.Errorf("coder subagent failed: %w", err)
		}
		return formatSubagentReport("💻 [Coder Subagent Report]", report), nil
	})

	// 3. delegate_tester
	a.mustRegisterContext(ollama.Tool{
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
	}, tools.ToolMetadata{WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		enrichedTask := a.buildEnrichedSubagentTask(task)
		subCB := a.buildSubagentCallbacks(ctx)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), subCB, a.getWorkspaceRoot())
		report, err := runner.RunTesterWithContext(ctx, enrichedTask)
		if err != nil {
			return "", fmt.Errorf("tester subagent failed: %w", err)
		}
		return formatSubagentReport("🧪 [Tester Subagent Report]", report), nil
	})

	// 4. delegate_reviewer
	a.mustRegisterContext(ollama.Tool{
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
	}, tools.ToolMetadata{WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		enrichedTask := a.buildEnrichedSubagentTask(task)
		subCB := a.buildSubagentCallbacks(ctx)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), subCB, a.getWorkspaceRoot())
		report, err := runner.RunReviewerWithContext(ctx, enrichedTask)
		if err != nil {
			return "", fmt.Errorf("reviewer subagent failed: %w", err)
		}
		return formatSubagentReport("🧐 [Reviewer Subagent Report]", report), nil
	})

	// 5. delegate_documenter
	a.mustRegisterContext(ollama.Tool{
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
	}, tools.ToolMetadata{WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		enrichedTask := a.buildEnrichedSubagentTask(task)
		subCB := a.buildSubagentCallbacks(ctx)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), subCB, a.getWorkspaceRoot())
		report, err := runner.RunDocumenterWithContext(ctx, enrichedTask)
		if err != nil {
			return "", fmt.Errorf("documenter subagent failed: %w", err)
		}
		if err := validateRequiredSubagentArtifacts("documenter", report, a.getWorkspaceRoot()); err != nil {
			return "", err
		}
		return formatSubagentReport("📝 [Documenter Subagent Report]", report), nil
	})

	// 6. delegate_presenter
	a.mustRegisterContext(ollama.Tool{
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
	}, tools.ToolMetadata{WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		task, _ := args["task_description"].(string)
		enrichedTask := a.buildEnrichedSubagentTask(task)
		subCB := a.buildSubagentCallbacks(ctx)
		runner := subagent.NewRunner(a.client, a.model, a.cfg, a.currentDir, a.getSessionFilePath(), subCB, a.getWorkspaceRoot())
		report, err := runner.RunPresenterWithContext(ctx, enrichedTask)
		if err != nil {
			return "", fmt.Errorf("presenter subagent failed: %w", err)
		}
		if err := validateRequiredSubagentArtifacts("presenter", report, a.getWorkspaceRoot()); err != nil {
			return "", err
		}
		return formatSubagentReport("📊 [Presenter Subagent Report]", report), nil
	})
}

func (a *Agent) mustRegisterContext(tool ollama.Tool, metadata tools.ToolMetadata, handler tools.ContextToolHandler) {
	if err := a.registry.RegisterContext(tool, metadata, handler); err != nil {
		panic(err)
	}
}
