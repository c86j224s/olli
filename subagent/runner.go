package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
	"github.com/c86j224s/olli/tools"
)

type SubagentRunner struct {
	client        *ollama.Client
	model         string
	cfg           *config.Config
	outputDir     string
	workspace     string
	workspaceRoot string
	sessionFile   string
	callbacks     SubagentCallbacks
}

func NewRunner(client *ollama.Client, model string, cfg *config.Config, workspace string, sessionFile string, callbacks SubagentCallbacks, workspaceRootArg ...string) *SubagentRunner {
	if workspace == "" {
		workspace = "."
	}
	workspaceRoot := workspace
	if len(workspaceRootArg) > 0 && strings.TrimSpace(workspaceRootArg[0]) != "" {
		workspaceRoot = workspaceRootArg[0]
	}

	if safeRoot, err := tools.IsPathSafeFrom(".", workspaceRoot, workspaceRoot); err == nil {
		workspaceRoot = safeRoot
	}
	if safeWorkspace, err := tools.IsPathSafeFrom(".", workspace, workspaceRoot); err == nil {
		workspace = safeWorkspace
	} else {
		workspace = workspaceRoot
	}
	outDir, err := tools.IsPathSafeFrom(filepath.Join("sessions", "subagents"), workspace, workspaceRoot)
	if err == nil {
		_ = os.MkdirAll(outDir, 0755)
	} else {
		outDir = ""
	}

	return &SubagentRunner{
		client:        client,
		model:         model,
		cfg:           cfg,
		outputDir:     outDir,
		workspace:     workspace,
		workspaceRoot: workspaceRoot,
		sessionFile:   sessionFile,
		callbacks:     callbacks,
	}
}

func (r *SubagentRunner) GetSessionFile() string {
	return r.sessionFile
}

func (r *SubagentRunner) GetWorkspaceRoot() string {
	return r.workspaceRoot
}

func (r *SubagentRunner) newRoleRegistry() *tools.Registry {
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(r.workspaceRoot)
	reg.SetWorkspace(r.workspace)
	reg.SetSessionFile(r.sessionFile)
	return reg
}

func (r *SubagentRunner) executeSubagentLoop(subID string, subType string, task string, sysPrompt string, reg *tools.Registry) (*ResultReport, error) {
	return r.executeSubagentLoopWithContext(context.Background(), subID, subType, task, sysPrompt, reg)
}

func (r *SubagentRunner) executeSubagentLoopWithContext(ctx context.Context, subID string, subType string, task string, sysPrompt string, reg *tools.Registry) (*ResultReport, error) {
	if r.outputDir == "" {
		return nil, fmt.Errorf("subagent output directory is not safely contained within the workspace root")
	}
	jsonlPath := filepath.Join(r.outputDir, subID+".jsonl")
	jsonlFile, err := os.OpenFile(jsonlPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create subagent jsonl: %w", err)
	}
	defer jsonlFile.Close()

	logEvent := func(role string, content string, toolCalls []ollama.ToolCall) {
		evt := session.Event{
			Timestamp: time.Now().Format(time.RFC3339),
			Role:      role,
			Content:   content,
			ToolCalls: toolCalls,
		}
		data, _ := json.Marshal(evt)
		jsonlFile.Write(append(data, '\n'))
		jsonlFile.Sync()
	}

	messages := []ollama.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: task},
	}
	logEvent("system", sysPrompt, nil)
	logEvent("user", task, nil)

	numCtx := 32768
	if r.cfg != nil && r.cfg.NumCtx > 0 {
		numCtx = r.cfg.NumCtx
	}

	req := ollama.ChatRequest{
		Model:    r.model,
		Messages: messages,
		Tools:    reg.GetDefinitions(),
		Options: &ollama.Options{
			NumCtx: numCtx,
		},
	}

	toolCallsRun := 0
	var finalAnswer string

	thinkingActive := false
	streamCB := ollama.StreamCallbacks{
		OnThinking: func(token string) {
			if !thinkingActive {
				thinkingActive = true
				if r.callbacks.OnThinkingStart != nil {
					r.callbacks.OnThinkingStart(subType)
				}
			}
			if r.callbacks.OnThinkingToken != nil {
				r.callbacks.OnThinkingToken(token)
			}
		},
		OnContent: func(token string) {
			if thinkingActive {
				thinkingActive = false
				if r.callbacks.OnThinkingEnd != nil {
					r.callbacks.OnThinkingEnd()
				}
			}
		},
	}

	for turn := 0; turn < 5; turn++ {
		select {
		case <-ctx.Done():
			logEvent("system", "⚠️ Subagent execution canceled by user interrupt (ESC Key)", nil)
			return &ResultReport{
				SubagentID:   subID,
				Type:         subType,
				Task:         task,
				Status:       "INTERRUPTED",
				Summary:      "⚠️ Subagent execution was interrupted by user (ESC Key).",
				JSONLFile:    jsonlPath,
				ToolCallsRun: toolCallsRun,
				WorkingDir:   r.workspace,
			}, nil
		default:
		}

		resp, err := r.client.ChatStreamFullWithContext(ctx, req, streamCB)
		if err != nil {
			if ctx.Err() == context.Canceled || err == context.Canceled {
				logEvent("system", "⚠️ Subagent LLM stream canceled by user interrupt (ESC Key)", nil)
				return &ResultReport{
					SubagentID:   subID,
					Type:         subType,
					Task:         task,
					Status:       "INTERRUPTED",
					Summary:      "⚠️ Subagent execution was interrupted by user (ESC Key).",
					JSONLFile:    jsonlPath,
					ToolCallsRun: toolCallsRun,
					WorkingDir:   r.workspace,
				}, nil
			}
			return nil, fmt.Errorf("subagent LLM stream failed: %w", err)
		}

		if thinkingActive {
			thinkingActive = false
			if r.callbacks.OnThinkingEnd != nil {
				r.callbacks.OnThinkingEnd()
			}
		}

		if len(resp.ToolCalls) > 0 {
			messages = append(messages, *resp)
			logEvent("assistant", resp.Content, resp.ToolCalls)

			for _, tc := range resp.ToolCalls {
				if ctx.Err() == context.Canceled {
					logEvent("system", "⚠️ Subagent tool execution canceled by user interrupt (ESC Key)", nil)
					return &ResultReport{
						SubagentID:   subID,
						Type:         subType,
						Task:         task,
						Status:       "INTERRUPTED",
						Summary:      "⚠️ Subagent execution was interrupted by user (ESC Key).",
						JSONLFile:    jsonlPath,
						ToolCallsRun: toolCallsRun,
						WorkingDir:   r.workspace,
					}, nil
				}

				toolCallsRun++
				toolRes, tErr := reg.Execute(tc.Function.Name, tc.Function.Arguments)
				resContent := toolRes
				if tErr != nil {
					resContent = fmt.Sprintf("Error executing tool %s: %v", tc.Function.Name, tErr)
				}

				if r.callbacks.OnToolCall != nil {
					r.callbacks.OnToolCall(subType, tc.Function.Name, tc.Function.Arguments, resContent, tErr)
				}

				toolMsg := ollama.Message{Role: "tool", Content: resContent}
				messages = append(messages, toolMsg)
				logEvent("tool", resContent, nil)
			}

			req.Messages = messages
			continue
		}

		finalAnswer = resp.Content
		logEvent("assistant", finalAnswer, nil)
		break
	}

	if finalAnswer == "" {
		finalAnswer = "Subagent task completed tool execution."
	}

	report := &ResultReport{
		SubagentID:   subID,
		Type:         subType,
		Task:         task,
		Status:       "SUCCESS",
		Summary:      strings.TrimSpace(finalAnswer),
		JSONLFile:    jsonlPath,
		ToolCallsRun: toolCallsRun,
		WorkingDir:   r.workspace,
	}

	return report, nil
}
