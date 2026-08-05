package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
	ConfirmToolCallWithAction func(toolName string, args map[string]interface{}) (bool, bool)
	OnSubagentThinkingStart   func(subType string)
	OnSubagentThinkingToken   func(token string)
	OnSubagentThinkingEnd     func()
	OnSubagentToolCall        func(subType string, toolName string, args map[string]interface{}, result string, execErr error)
}

type Agent struct {
	client     *ollama.Client
	model      string
	systemMsg  string
	numCtx     int
	history    []ollama.Message
	toolMode   ToolMode
	summary    string
	activeGoal string
	initialDir string
	currentDir string
	registry   *tools.Registry
	sessMgr    *session.Manager
	cfg        *config.Config
	activeCB   Callbacks
}

func FormatArgs(args map[string]interface{}) string {
	if len(args) == 0 {
		return "{}"
	}
	b, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func New(client *ollama.Client, model string, systemMsg string, sessMgr *session.Manager, cfg *config.Config) *Agent {
	initialDir, err := os.Getwd()
	if err != nil {
		initialDir = "."
	}

	reg := tools.NewRegistry()
	reg.SetWorkspace(initialDir)

	numCtx := 32768
	if cfg != nil && cfg.NumCtx > 0 {
		numCtx = cfg.NumCtx
	}

	ag := &Agent{
		client:     client,
		model:      model,
		systemMsg:  systemMsg,
		numCtx:     numCtx,
		history:    make([]ollama.Message, 0),
		toolMode:   ModeAuto,
		summary:    "Session started.",
		activeGoal: "",
		initialDir: initialDir,
		currentDir: initialDir,
		registry:   reg,
		sessMgr:    sessMgr,
		cfg:        cfg,
	}

	ag.registerBuiltinTools()

	if sessMgr != nil {
		sessInfo, err := sessMgr.CreateSession("", model)
		if err == nil {
			ag.summary = fmt.Sprintf("Session '%s' created.", sessInfo.ID)
			sessMgr.AppendEvent(ollama.Message{
				Role:    "system",
				Content: fmt.Sprintf("📌 [Workspace Directory Initialized]: %s", initialDir),
			})
		}
	}

	return ag
}

func (a *Agent) registerBuiltinTools() {
	// Register CD tool
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "cd",
			Description: "Change active workspace working directory",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"path": {
						Type:        "string",
						Description: "Target directory path to navigate into (e.g. '~/llm-pg', '..', '/Users/allthatcode')",
					},
				},
				Required: []string{"path"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		path, _ := args["path"].(string)
		resolvedPath := a.registry.ResolvePath(path)

		if err := tools.IsWorkspaceLocationSafe(resolvedPath); err != nil {
			return "", fmt.Errorf("permission denied: directory '%s' is not allowed: %w", resolvedPath, err)
		}

		info, err := os.Stat(resolvedPath)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("directory '%s' does not exist or is not a directory", resolvedPath)
		}

		a.SetCurrentDir(resolvedPath)
		return fmt.Sprintf("Working directory successfully changed to '%s'", resolvedPath), nil
	})

	// Also alias change_directory
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "change_directory",
			Description: "Alias for cd tool to change active working directory",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"path": {
						Type:        "string",
						Description: "Target directory path",
					},
				},
				Required: []string{"path"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		path, _ := args["path"].(string)
		resolvedPath := a.registry.ResolvePath(path)

		if err := tools.IsWorkspaceLocationSafe(resolvedPath); err != nil {
			return "", fmt.Errorf("permission denied: directory '%s' is not allowed: %w", resolvedPath, err)
		}

		info, err := os.Stat(resolvedPath)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("directory '%s' does not exist or is not a directory", resolvedPath)
		}

		a.SetCurrentDir(resolvedPath)
		return fmt.Sprintf("Working directory successfully changed to '%s'", resolvedPath), nil
	})

	// Register get_agent_status tool
	a.registry.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "get_agent_status",
			Description: "Get current agent status, session info, launch directory, working directory, and tool mode",
			Parameters: ollama.FunctionParamSchema{
				Type:       "object",
				Properties: map[string]ollama.FunctionParamProperty{},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		sessID := "none"
		sessFile := "none"
		if a.sessMgr != nil {
			sessID = a.sessMgr.GetCurrentID()
			sessFile = a.sessMgr.GetCurrentPath()
		}

		statusInfo := map[string]string{
			"initial_launch_directory": a.initialDir,
			"current_working_directory": a.currentDir,
			"active_model":              a.model,
			"tool_mode":                 string(a.toolMode),
			"session_id":                sessID,
			"session_file":              sessFile,
			"active_goal":              a.activeGoal,
			"num_ctx":                  fmt.Sprintf("%d", a.numCtx),
		}
		b, _ := json.MarshalIndent(statusInfo, "", "  ")
		return string(b), nil
	})
}

func (a *Agent) GetConfig() *config.Config { return a.cfg }
func (a *Agent) SetModel(m string)        { a.model = m }
func (a *Agent) GetModel() string         { return a.model }
func (a *Agent) SetNumCtx(n int)          { a.numCtx = n }
func (a *Agent) GetNumCtx() int           { return a.numCtx }
func (a *Agent) SetToolMode(m ToolMode)   { a.toolMode = m }
func (a *Agent) GetToolMode() ToolMode    { return a.toolMode }

func (a *Agent) SetCurrentDir(d string) {
	a.currentDir = d
	if a.registry != nil {
		a.registry.SetWorkspace(d)
	}
	if a.sessMgr != nil {
		a.sessMgr.AppendEvent(ollama.Message{
			Role:    "system",
			Content: fmt.Sprintf("📌 [Workspace Directory Updated]: %s", d),
		})
	}
}

func (a *Agent) GetCurrentDir() string { return a.currentDir }
func (a *Agent) GetInitialDir() string { return a.initialDir }
func (a *Agent) SetSummary(s string)   { a.summary = s }
func (a *Agent) GetSummary() string    { return a.summary }

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

	messages, resolvedID, lastDir, err := a.sessMgr.LoadSession(nameOrID)
	if err != nil {
		return "", err
	}

	a.history = messages
	if lastDir != "" {
		a.currentDir = lastDir
		if a.registry != nil {
			a.registry.SetWorkspace(lastDir)
		}
		a.summary = fmt.Sprintf("Loaded session '%s' with %d messages. Restored active working directory: %s", resolvedID, len(messages), lastDir)
	} else {
		a.summary = fmt.Sprintf("Loaded session '%s' with %d messages.", resolvedID, len(messages))
	}
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
				Content: "⚠️ Agent generation was interrupted by user (ESC Key).",
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
			Options: &ollama.Options{
				NumCtx: a.numCtx,
			},
		}

		resp, err := a.client.ChatStreamFullWithContext(ctx, req, ollama.StreamCallbacks{
			OnThinking: func(token string) {
				if step == 0 && cb.OnThinkingToken != nil {
					cb.OnThinkingToken(token)
				}
			},
			OnContent: func(token string) {
				if cb.OnContentToken != nil {
					cb.OnContentToken(token)
				}
			},
		})

		if err != nil {
			if ctx.Err() == context.Canceled || err == context.Canceled {
				cancelMsg := ollama.Message{
					Role:    "system",
					Content: "⚠️ Agent generation was interrupted by user (ESC Key).",
				}
				a.history = append(a.history, cancelMsg)
				if a.sessMgr != nil {
					a.sessMgr.AppendEvent(cancelMsg)
				}
				return "", context.Canceled
			}
			return "", err
		}

		if len(resp.ToolCalls) > 0 {
			a.history = append(a.history, *resp)
			if a.sessMgr != nil {
				a.sessMgr.AppendEvent(*resp)
			}

			for _, tc := range resp.ToolCalls {
				if ctx.Err() == context.Canceled {
					return "", context.Canceled
				}

				var toolRes string
				var tErr error

				if a.ShouldRequirePermission(tc.Function.Name) {
					if cb.ConfirmToolCallWithAction != nil {
						allowed, always := cb.ConfirmToolCallWithAction(tc.Function.Name, tc.Function.Arguments)
						if always && a.cfg != nil {
							_ = a.cfg.AddWhitelist(tc.Function.Name)
							_ = a.cfg.Save()
						}
						if !allowed {
							tErr = fmt.Errorf("user denied execution of tool '%s'", tc.Function.Name)
						} else {
							toolRes, tErr = a.registry.Execute(tc.Function.Name, tc.Function.Arguments)
						}
					} else {
						tErr = fmt.Errorf("permission check required for '%s' but no prompt callback set", tc.Function.Name)
					}
				} else {
					toolRes, tErr = a.registry.Execute(tc.Function.Name, tc.Function.Arguments)
				}

				if cb.OnToolCall != nil {
					cb.OnToolCall(tc.Function.Name, tc.Function.Arguments, toolRes, tErr)
				}

				resContent := toolRes
				if tErr != nil {
					resContent = fmt.Sprintf("Error executing tool %s: %v", tc.Function.Name, tErr)
				}

				toolMsg := ollama.Message{Role: "tool", Content: resContent}
				a.history = append(a.history, toolMsg)
				if a.sessMgr != nil {
					a.sessMgr.AppendEvent(toolMsg)
				}
			}
			continue
		}

		lastContent = resp.Content
		a.history = append(a.history, ollama.Message{Role: "assistant", Content: lastContent})
		if a.sessMgr != nil {
			a.sessMgr.AppendEvent(ollama.Message{Role: "assistant", Content: lastContent})
		}
		break
	}

	return lastContent, nil
}
