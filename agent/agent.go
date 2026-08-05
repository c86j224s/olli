package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

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
	OnThinkingStart            func()
	OnThinkingToken            func(token string)
	OnThinkingEnd              func()
	OnContentToken             func(token string)
	OnToolCall                 func(toolName string, args map[string]interface{}, result string, execErr error)
	ConfirmToolCall            func(toolName string, args map[string]interface{}) bool
	ConfirmToolCallWithAction  func(toolName string, args map[string]interface{}) (allowed bool, addWhitelist bool)
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
}

func New(client *ollama.Client, model string, systemPrompt string, sessMgr *session.Manager, cfg *config.Config) *Agent {
	if systemPrompt == "" {
		systemPrompt = "You are an intelligent AI assistant equipped with Goal Steering and Tool Calling capabilities. Stay focused on achieving active goals."
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

	if sessMgr != nil && sessMgr.GetCurrentID() == "" {
		sessMgr.CreateSession("auto", model)
	}

	return ag
}

func (a *Agent) registerGoalTools() {
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "set_active_goal",
			Description: "Set or update the agent's active goal to stay focused on achieving a multi-step objective",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"goal_description": {
						Type:        "string",
						Description: "Description of the objective or goal to accomplish",
					},
				},
				Required: []string{"goal_description"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		g, ok := args["goal_description"].(string)
		if !ok || g == "" {
			return "", fmt.Errorf("invalid goal description")
		}
		a.SetGoal(g)
		return fmt.Sprintf("Active Goal set to: '%s'", g), nil
	})

	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "complete_goal",
			Description: "Mark the active goal as completed and clear the goal state after achieving all steps",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"completion_summary": {
						Type:        "string",
						Description: "Summary of achievements and completed goal results",
					},
				},
				Required: []string{"completion_summary"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		summary, _ := args["completion_summary"].(string)
		prevGoal := a.activeGoal
		a.ClearGoal()
		return fmt.Sprintf("🎉 Goal '%s' marked as COMPLETED! Summary: %s", prevGoal, summary), nil
	})
}

func (a *Agent) GetConfig() *config.Config {
	return a.cfg
}

func (a *Agent) SetModel(model string) {
	a.model = model
}

func (a *Agent) GetModel() string {
	return a.model
}

func (a *Agent) SetNumCtx(n int) {
	a.numCtx = n
}

func (a *Agent) GetNumCtx() int {
	return a.numCtx
}

func (a *Agent) SetToolMode(mode ToolMode) {
	a.toolMode = mode
}

func (a *Agent) GetToolMode() ToolMode {
	return a.toolMode
}

func (a *Agent) SetCurrentDir(dir string) {
	a.currentDir = dir
}

func (a *Agent) GetCurrentDir() string {
	return a.currentDir
}

func (a *Agent) GetInitialDir() string {
	return a.initialDir
}

func (a *Agent) SetGoal(goal string) {
	a.activeGoal = strings.TrimSpace(goal)
}

func (a *Agent) ClearGoal() {
	a.activeGoal = ""
}

func (a *Agent) GetGoal() string {
	return a.activeGoal
}

func (a *Agent) IsGoalActive() bool {
	return a.activeGoal != ""
}

func (a *Agent) SetSummary(sum string) {
	a.summary = sum
}

func (a *Agent) GetSummary() string {
	return a.summary
}

func (a *Agent) ClearHistory() {
	a.history = make([]ollama.Message, 0)
	a.summary = "Conversation history cleared."
}

func (a *Agent) GetHistoryCount() int {
	return len(a.history)
}

func (a *Agent) GetRegistry() *tools.Registry {
	return a.registry
}

func (a *Agent) GetSessionManager() *session.Manager {
	return a.sessMgr
}

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

func (a *Agent) AutoSummarize() (string, error) {
	if len(a.history) < 2 {
		return a.summary, nil
	}

	promptMsg := append(a.history, ollama.Message{
		Role:    "user",
		Content: "Summarize the key points, user preferences, and topics discussed in this conversation in 3 concise bullet points for long-term memory reference.",
	})

	req := ollama.ChatRequest{
		Model:    a.model,
		Messages: promptMsg,
		Options: &ollama.Options{
			NumCtx: 4096,
		},
	}

	resp, err := a.client.ChatStreamFull(req, ollama.StreamCallbacks{})
	if err != nil {
		return "", err
	}

	summaryText := strings.TrimSpace(resp.Content)
	if summaryText != "" {
		a.summary = summaryText
	}
	return a.summary, nil
}

func (a *Agent) Ask(userInput string, cb Callbacks) (string, error) {
	userMsg := ollama.Message{
		Role:    "user",
		Content: userInput,
	}
	a.history = append(a.history, userMsg)

	if a.sessMgr != nil {
		a.sessMgr.AppendEvent(userMsg)
	}

	req := ollama.ChatRequest{
		Model:    a.model,
		Messages: a.buildMessagesPayload(),
		Tools:    a.registry.GetDefinitions(),
		Options: &ollama.Options{
			NumCtx: a.numCtx,
		},
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

	assistantMsg, err := a.client.ChatStreamFull(req, streamCB)
	if err != nil {
		return "", fmt.Errorf("chat stream failed: %w", err)
	}

	if thinkingActive {
		thinkingActive = false
		if cb.OnThinkingEnd != nil {
			cb.OnThinkingEnd()
		}
	}

	if len(assistantMsg.ToolCalls) > 0 {
		a.history = append(a.history, *assistantMsg)
		if a.sessMgr != nil {
			a.sessMgr.AppendEvent(*assistantMsg)
		}

		for _, toolCall := range assistantMsg.ToolCalls {
			toolName := toolCall.Function.Name
			args := toolCall.Function.Arguments

			if a.ShouldRequirePermission(toolName) {
				allowed := false
				addWhitelist := false

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

			toolMsg := ollama.Message{
				Role:    "tool",
				Content: toolContent,
			}
			a.history = append(a.history, toolMsg)
			if a.sessMgr != nil {
				a.sessMgr.AppendEvent(toolMsg)
			}
		}

		followUpReq := ollama.ChatRequest{
			Model:    a.model,
			Messages: a.buildMessagesPayload(),
			Options: &ollama.Options{
				NumCtx: a.numCtx,
			},
		}

		finalThinkingActive := false
		followUpCB := ollama.StreamCallbacks{
			OnThinking: func(token string) {
				if !finalThinkingActive {
					finalThinkingActive = true
					if cb.OnThinkingStart != nil {
						cb.OnThinkingStart()
					}
				}
				if cb.OnThinkingToken != nil {
					cb.OnThinkingToken(token)
				}
			},
			OnContent: func(token string) {
				if finalThinkingActive {
					finalThinkingActive = false
					if cb.OnThinkingEnd != nil {
						cb.OnThinkingEnd()
					}
				}
				if cb.OnContentToken != nil {
					cb.OnContentToken(token)
				}
			},
		}

		finalMsg, err := a.client.ChatStreamFull(followUpReq, followUpCB)
		if err != nil {
			return "", fmt.Errorf("followup stream failed: %w", err)
		}

		if finalThinkingActive {
			if cb.OnThinkingEnd != nil {
				cb.OnThinkingEnd()
			}
		}

		finalAssMsg := ollama.Message{
			Role:     "assistant",
			Content:  strings.TrimSpace(finalMsg.Content),
			Thinking: finalMsg.Thinking,
		}
		a.history = append(a.history, finalAssMsg)
		if a.sessMgr != nil {
			a.sessMgr.AppendEvent(finalAssMsg)
		}

		return finalMsg.Content, nil
	}

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

func (a *Agent) buildMessagesPayload() []ollama.Message {
	msgs := make([]ollama.Message, 0, len(a.history)+1)

	fullSystemPrompt := a.systemPrompt

	now := time.Now()
	timeZone, _ := now.Zone()
	envContext := fmt.Sprintf("\n\n🌐 [ENVIRONMENT & TEMPORAL CONTEXT]:\n- Current Local Time: %s (%s, %s)\n- Initial Session Directory: %s\n- Current Working Directory: %s",
		now.Format("2006-01-02 15:04:05"),
		timeZone,
		now.Format("Monday"),
		a.initialDir,
		a.currentDir,
	)
	fullSystemPrompt += envContext

	if a.IsGoalActive() {
		fullSystemPrompt += fmt.Sprintf("\n\n🎯 [ACTIVE GOAL / MISSION STEERING]:\n\"%s\"\n\nCRITICAL INSTRUCTION: Stay focused on achieving this objective. Once completed, call 'complete_goal'.", a.activeGoal)
	}

	if a.summary != "" {
		fullSystemPrompt += fmt.Sprintf("\n\n[Active Conversation Memory Summary]:\n%s\n\n[Memory Retrieval Guidance]:\nIf you need exact historical details, call 'search_session_history'.", a.summary)
	}

	msgs = append(msgs, ollama.Message{
		Role:    "system",
		Content: fullSystemPrompt,
	})
	msgs = append(msgs, a.history...)
	return msgs
}

func FormatArgs(args map[string]interface{}) string {
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	return string(b)
}
