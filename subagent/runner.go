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
	client    *ollama.Client
	model     string
	cfg       *config.Config
	outputDir string
	workspace string
	callbacks SubagentCallbacks
}

func NewRunner(client *ollama.Client, model string, cfg *config.Config, workspace string, callbacks SubagentCallbacks) *SubagentRunner {
	if workspace == "" {
		workspace = "."
	}
	outDir := filepath.Join(workspace, "sessions", "subagents")
	os.MkdirAll(outDir, 0755)

	return &SubagentRunner{
		client:    client,
		model:     model,
		cfg:       cfg,
		outputDir: outDir,
		workspace: workspace,
		callbacks: callbacks,
	}
}

func (r *SubagentRunner) executeSubagentLoop(subID string, subType string, task string, sysPrompt string, reg *tools.Registry) (*ResultReport, error) {
	return r.executeSubagentLoopWithContext(context.Background(), subID, subType, task, sysPrompt, reg)
}

func (r *SubagentRunner) executeSubagentLoopWithContext(ctx context.Context, subID string, subType string, task string, sysPrompt string, reg *tools.Registry) (*ResultReport, error) {
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

	req := ollama.ChatRequest{
		Model:    r.model,
		Messages: messages,
		Tools:    reg.GetDefinitions(),
		Options: &ollama.Options{
			NumCtx: 16384,
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
	}

	return report, nil
}
