package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
	"github.com/c86j224s/olli/tools"
)

type ToolMode string

const (
	ModeAuto       ToolMode = "auto"
	ModeAsk        ToolMode = "ask"
	ModeAcceptEdit ToolMode = "accept-edit"
)

type Callbacks struct {
	OnThinkingStart           func()
	OnThinkingToken           func(token string)
	OnThinkingEnd             func()
	OnContentToken            func(token string)
	OnToolCall                func(toolName string, args map[string]interface{}, result string, execErr error)
	ConfirmToolCall           func(toolName string, args map[string]interface{}) bool
	ConfirmToolCallWithAction func(toolName string, args map[string]interface{}) (allowed bool, addWhitelist bool)
	OnSubagentThinkingStart   func(subType string)
	OnSubagentThinkingToken   func(token string)
	OnSubagentThinkingEnd     func()
	OnSubagentToolCall        func(subType string, toolName string, args map[string]interface{}, result string, execErr error)
}

type Agent struct {
	client       *ollama.Client
	model        string
	numCtx       int
	toolMode     ToolMode
	cfg          *config.Config
	systemPrompt string
	summary      string
	activeGoal   string
	initialDir   string
	currentDir   string
	history      []ollama.Message
	registry     *tools.Registry
	sessMgr      *session.Manager
	activeCB     Callbacks
}

func New(client *ollama.Client, model string, systemPrompt string, sessMgr *session.Manager, cfg *config.Config) *Agent {
	if systemPrompt == "" {
		systemPrompt = "You are an intelligent AI assistant equipped with Goal Steering and Subagent Delegation capabilities. Always delegate specialized tasks to Subagents (Researcher, Coder, Tester, Reviewer, Documenter, Presenter)."
	}

	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	if cfg == nil {
		cfg, _ = config.LoadConfig("./config.json")
	}

	ag := &Agent{
		client:       client,
		model:        model,
		numCtx:       cfg.NumCtx,
		toolMode:     ToolMode(cfg.DefaultMode),
		cfg:          cfg,
		systemPrompt: systemPrompt,
		summary:      "Conversation just started.",
		initialDir:   wd,
		currentDir:   wd,
		history:      make([]ollama.Message, 0),
		registry:     tools.NewRegistry(),
		sessMgr:      sessMgr,
	}

	ag.registerGoalTools()
	ag.registerSubagentToolsWithContext(context.Background())

	if sessMgr != nil && sessMgr.GetCurrentID() == "" {
		sessMgr.CreateSession("auto", model)
	}

	return ag
}

func (a *Agent) GetConfig() *config.Config { return a.cfg }
func (a *Agent) SetModel(m string)        { a.model = m }
func (a *Agent) GetModel() string         { return a.model }
func (a *Agent) SetNumCtx(n int)          { a.numCtx = n }
func (a *Agent) GetNumCtx() int           { return a.numCtx }
func (a *Agent) SetToolMode(m ToolMode)   { a.toolMode = m }
func (a *Agent) GetToolMode() ToolMode    { return a.toolMode }
func (a *Agent) SetCurrentDir(d string)   { a.currentDir = d }
func (a *Agent) GetCurrentDir() string    { return a.currentDir }
func (a *Agent) GetInitialDir() string    { return a.initialDir }
func (a *Agent) SetSummary(s string)      { a.summary = s }
func (a *Agent) GetSummary() string       { return a.summary }

func (a *Agent) ClearHistory() {
	a.history = make([]ollama.Message, 0)
	a.summary = "Conversation history cleared."
}

func (a *Agent) GetHistoryCount() int          { return len(a.history) }
func (a *Agent) GetRegistry() *tools.Registry  { return a.registry }
func (a *Agent) GetSessionManager() *session.Manager { return a.sessMgr }

func (a *Agent) ShouldRequirePermission(toolName string) bool {
	switch a.toolMode {
	case ModeAuto:
		return false
	case ModeAsk:
		return true
	case ModeAcceptEdit:
		if a.cfg != nil {
			return !a.cfg.IsWhitelisted(toolName)
		}
		return true
	default:
		return true
	}
}

func (a *Agent) LoadSession(nameOrID string) (string, error) {
	if a.sessMgr == nil {
		return "", fmt.Errorf("session manager not initialized")
	}

	messages, resolvedID, err := a.sessMgr.LoadSession(nameOrID)
	if err != nil {
		return "", err
	}

	a.history = messages
	a.summary = fmt.Sprintf("Loaded session '%s' with %d messages.", resolvedID, len(messages))
	return resolvedID, nil
}

func (a *Agent) Ask(userInput string, cb Callbacks) (string, error) {
	return a.AskWithContext(context.Background(), userInput, cb)
}

func (a *Agent) AskWithContext(ctx context.Context, userInput string, cb Callbacks) (string, error) {
	a.activeCB = cb
	a.registerSubagentToolsWithContext(ctx)

	userMsg := ollama.Message{Role: "user", Content: userInput}
	a.history = append(a.history, userMsg)

	if a.sessMgr != nil {
		a.sessMgr.AppendEvent(userMsg)
	}

	var lastContent string

	for step := 0; step < 10; step++ {
		select {
		case <-ctx.Done():
			cancelMsg := ollama.Message{
				Role:    "system",
				Content: "⚠️ Agent generation was interrupted by user (Ctrl+C).",
			}
			a.history = append(a.history, cancelMsg)
			if a.sessMgr != nil {
				a.sessMgr.AppendEvent(cancelMsg)
			}
			return "", context.Canceled
		default:
		}

		req := ollama.ChatRequest{
			Model:    a.model,
			Messages: a.buildMessagesPayload(),
			Tools:    a.registry.GetDefinitions(),
			Options:  &ollama.Options{NumCtx: a.numCtx},
		}

		thinkingActive := false
		streamCB := ollama.StreamCallbacks{
			OnThinking: func(token string) {
				if !thinkingActive {
					thinkingActive = true
					if cb.OnThinkingStart != nil {
						cb.OnThinkingStart()
					}
				}
				if cb.OnThinkingToken != nil {
					cb.OnThinkingToken(token)
				}
			},
			OnContent: func(token string) {
				if thinkingActive {
					thinkingActive = false
					if cb.OnThinkingEnd != nil {
						cb.OnThinkingEnd()
					}
				}
				if cb.OnContentToken != nil {
					cb.OnContentToken(token)
				}
			},
		}

		assistantMsg, err := a.client.ChatStreamFullWithContext(ctx, req, streamCB)
		if err != nil {
			if ctx.Err() == context.Canceled || err == context.Canceled {
				cancelMsg := ollama.Message{
					Role:    "system",
					Content: "⚠️ Agent generation was interrupted by user (Ctrl+C).",
				}
				a.history = append(a.history, cancelMsg)
				if a.sessMgr != nil {
					a.sessMgr.AppendEvent(cancelMsg)
				}
				return "", context.Canceled
			}
			return "", fmt.Errorf("chat stream failed: %w", err)
		}

		if thinkingActive {
			thinkingActive = false
			if cb.OnThinkingEnd != nil {
				cb.OnThinkingEnd()
			}
		}

		lastContent = assistantMsg.Content

		if len(assistantMsg.ToolCalls) == 0 {
			finalAssMsg := ollama.Message{
				Role:     "assistant",
				Content:  strings.TrimSpace(assistantMsg.Content),
				Thinking: assistantMsg.Thinking,
			}
			a.history = append(a.history, finalAssMsg)
			if a.sessMgr != nil {
				a.sessMgr.AppendEvent(finalAssMsg)
			}
			return assistantMsg.Content, nil
		}

		a.history = append(a.history, *assistantMsg)
		if a.sessMgr != nil {
			a.sessMgr.AppendEvent(*assistantMsg)
		}

		for _, toolCall := range assistantMsg.ToolCalls {
			if ctx.Err() == context.Canceled {
				cancelMsg := ollama.Message{
					Role:    "system",
					Content: "⚠️ Agent generation was interrupted by user during tool execution (Ctrl+C).",
				}
				a.history = append(a.history, cancelMsg)
				if a.sessMgr != nil {
					a.sessMgr.AppendEvent(cancelMsg)
				}
				return "", context.Canceled
			}

			toolName := toolCall.Function.Name
			args := toolCall.Function.Arguments

			if a.ShouldRequirePermission(toolName) {
				allowed, addWhitelist := false, false

				if cb.ConfirmToolCallWithAction != nil {
					allowed, addWhitelist = cb.ConfirmToolCallWithAction(toolName, args)
				} else if cb.ConfirmToolCall != nil {
					allowed = cb.ConfirmToolCall(toolName, args)
				} else {
					allowed, addWhitelist = promptConsolePermissionAction(toolName, args)
				}

				if addWhitelist && a.cfg != nil {
					a.cfg.AddWhitelist(toolName)
				}

				if !allowed {
					toolMsg := ollama.Message{
						Role:    "tool",
						Content: fmt.Sprintf("Tool execution for '%s' was REJECTED by user.", toolName),
					}
					a.history = append(a.history, toolMsg)
					if a.sessMgr != nil {
						a.sessMgr.AppendEvent(toolMsg)
					}
					if cb.OnToolCall != nil {
						cb.OnToolCall(toolName, args, "Tool execution rejected by user.", fmt.Errorf("permission denied by user"))
					}
					continue
				}
			}

			result, execErr := a.registry.Execute(toolName, args)

			if cb.OnToolCall != nil {
				cb.OnToolCall(toolName, args, result, execErr)
			}

			toolContent := result
			if execErr != nil {
				toolContent = fmt.Sprintf("Error executing tool %s: %v", toolName, execErr)
			}

			toolMsg := ollama.Message{Role: "tool", Content: toolContent}
			a.history = append(a.history, toolMsg)
			if a.sessMgr != nil {
				a.sessMgr.AppendEvent(toolMsg)
			}
		}
	}

	return lastContent, nil
}

func promptConsolePermissionAction(toolName string, args map[string]interface{}) (allowed bool, addWhitelist bool) {
	fmt.Printf("\n❓ [Permission Required] Tool '%s'(%s).\n", toolName, FormatArgs(args))
	fmt.Print("   Options: [y] Yes (once)  |  [a] Always (add to whitelist)  |  [n] No (deny)\n")
	fmt.Print("   Choice [y/a/N]: ")
	reader := bufio.NewReader(os.Stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))

	if ans == "a" || ans == "always" {
		return true, true
	}
	if ans == "y" || ans == "yes" {
		return true, false
	}
	return false, false
}

func FormatArgs(args map[string]interface{}) string {
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	return string(b)
}
