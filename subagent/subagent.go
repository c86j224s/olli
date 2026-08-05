package subagent

import (
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

type SubagentType string

const (
	TypeResearcher SubagentType = "Researcher"
	TypeCoder      SubagentType = "Coder"
)

type SubagentCallbacks struct {
	OnThinkingStart func(subType string)
	OnThinkingToken func(token string)
	OnThinkingEnd   func()
	OnToolCall      func(subType string, toolName string, args map[string]interface{}, result string, execErr error)
}

type ResultReport struct {
	SubagentID   string `json:"subagent_id"`
	Type         string `json:"type"`
	Task         string `json:"task"`
	Status       string `json:"status"`
	Summary      string `json:"summary"`
	JSONLFile    string `json:"jsonl_file"`
	ToolCallsRun int    `json:"tool_calls_run"`
}

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

func (r *SubagentRunner) executeSubagentLoop(subID string, subType string, task string, sysPrompt string, reg *tools.Registry) (*ResultReport, error) {
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
		resp, err := r.client.ChatStreamFull(req, streamCB)
		if err != nil {
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
