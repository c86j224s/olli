package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ergochat/readline"

	"github.com/c86j224s/olli/agent"
	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
)

const (
	ColorReset   = "\033[0m"
	ColorBold    = "\033[1m"
	ColorDim     = "\033[2m"
	ColorItalic  = "\033[3m"
	ColorCyan    = "\033[36m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorRed     = "\033[31m"
	ColorMagenta = "\033[35m"
	ColorBlue    = "\033[34m"
	ColorGray    = "\033[90m"

	// Single-Hue Palette for Main Agent (Cyan Family)
	MainBold   = "\033[1;36m"
	MainNormal = "\033[0;36m"
	MainDim    = "\033[2;36m"
	MainItalic = "\033[3;36m"

	// Single-Hue Palette for Researcher Subagent (Yellow/Amber Family)
	ResBold   = "\033[1;33m"
	ResNormal = "\033[0;33m"
	ResDim    = "\033[2;33m"
	ResItalic = "\033[3;33m"

	// Single-Hue Palette for Coder Subagent (Magenta/Purple Family)
	CoderBold   = "\033[1;35m"
	CoderNormal = "\033[0;35m"
	CoderDim    = "\033[2;35m"
	CoderItalic = "\033[3;35m"

	// Single-Hue Palette for Tester Subagent (Green/Emerald Family)
	TesterBold   = "\033[1;32m"
	TesterNormal = "\033[0;32m"
	TesterDim    = "\033[2;32m"
	TesterItalic = "\033[3;32m"

	// Single-Hue Palette for Reviewer Subagent (Blue Family)
	ReviewerBold   = "\033[1;34m"
	ReviewerNormal = "\033[0;34m"
	ReviewerDim    = "\033[2;34m"
	ReviewerItalic = "\033[3;34m"
)

func main() {
	client := ollama.NewClient("http://localhost:11434")

	models, err := client.ListModels()
	if err != nil {
		fmt.Printf("%s[Error]%s Failed to connect to Ollama: %v\n", ColorRed, ColorReset, err)
		fmt.Println("Please make sure Ollama is running (`ollama serve`).")
		os.Exit(1)
	}

	if len(models) == 0 {
		fmt.Printf("%s[Error]%s No local models found in Ollama.\n", ColorRed, ColorReset)
		os.Exit(1)
	}

	defaultModel := models[0]
	for _, m := range models {
		if strings.HasPrefix(m, "qwen3.5:0.8b") || strings.HasPrefix(m, "gemma4:12b") {
			defaultModel = m
			break
		}
	}

	sessMgr, err := session.NewManager("./sessions")
	if err != nil {
		fmt.Printf("%s[Error]%s Failed to initialize session manager: %v\n", ColorRed, ColorReset, err)
		os.Exit(1)
	}

	cfg, err := config.LoadConfig("./config.json")
	if err != nil {
		fmt.Printf("%s[Error]%s Failed to load config.json: %v\n", ColorRed, ColorReset, err)
		os.Exit(1)
	}

	ag := agent.New(client, defaultModel, "You are an intelligent AI assistant equipped with Goal Steering and Subagent Delegation capabilities. Stay focused on achieving active goals.", sessMgr, cfg)

	printBanner(ag, models, sessMgr.GetCurrentID())

	completer := readline.NewPrefixCompleter(
		readline.PcItem("/help"),
		readline.PcItem("/mode",
			readline.PcItem("auto"),
			readline.PcItem("ask"),
			readline.PcItem("accept-edit"),
		),
		readline.PcItem("/config",
			readline.PcItem("whitelist"),
			readline.PcItem("allow"),
			readline.PcItem("deny"),
		),
		readline.PcItem("/goal",
			readline.PcItem("set"),
			readline.PcItem("clear"),
			readline.PcItem("status"),
		),
		readline.PcItem("/session",
			readline.PcItem("list"),
			readline.PcItem("new"),
			readline.PcItem("load"),
			readline.PcItem("rename"),
			readline.PcItem("current"),
			readline.PcItem("delete"),
		),
		readline.PcItem("/summary"),
		readline.PcItem("/summarize"),
		readline.PcItem("/numctx"),
		readline.PcItem("/tools"),
		readline.PcItem("/models"),
		readline.PcItem("/model"),
		readline.PcItem("/clear"),
		readline.PcItem("/exit"),
		readline.PcItem("/quit"),
	)

	historyFile := filepath.Join(os.TempDir(), ".toy_agent_readline_history")
	rlConfig := &readline.Config{
		Prompt:          buildPrompt(ag, sessMgr),
		HistoryFile:     historyFile,
		AutoComplete:    completer,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	}

	rl, err := readline.NewEx(rlConfig)
	if err != nil {
		fmt.Printf("%s[Error]%s Failed to initialize readline: %v\n", ColorRed, ColorReset, err)
		os.Exit(1)
	}
	defer rl.Close()

	for {
		rl.SetPrompt(buildPrompt(ag, sessMgr))

		line, err := rl.Readline()
		if err == readline.ErrInterrupt {
			if len(line) == 0 {
				break
			}
			continue
		} else if err == io.EOF {
			break
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/") {
			if handleCommand(input, ag, client, models) {
				break
			}
			continue
		}

		fmt.Println()

		contentStarted := false
		subThinkingActive := false

		callbacks := agent.Callbacks{
			OnThinkingStart: func() {
				fmt.Printf("%s🧠 [Thinking]%s %s", MainItalic, ColorReset, ColorGray)
			},
			OnThinkingToken: func(token string) {
				fmt.Print(token)
			},
			OnThinkingEnd: func() {
				fmt.Printf("%s\n\n", ColorReset)
			},
			OnContentToken: func(token string) {
				if !contentStarted {
					contentStarted = true
					fmt.Printf("%sAgent (%s):%s\n", ColorBold+ColorGreen, ag.GetModel(), ColorReset)
				}
				fmt.Print(token)
			},
			OnToolCall: func(toolName string, args map[string]interface{}, result string, execErr error) {
				fmt.Printf("\n%s⚙️  [Main Tool]%s %s%s%s(%s)\n", MainBold, ColorReset, ColorBold, toolName, ColorReset, agent.FormatArgs(args))
				if execErr != nil {
					fmt.Printf("%s   ❌ [Error/Rejection]%s %v\n", ColorRed, ColorReset, execErr)
				} else {
					truncRes := result
					if len(truncRes) > 200 {
						truncRes = truncRes[:200] + "... [truncated]"
					}
					fmt.Printf("%s   📥 [Output Summary]%s %s\n", ColorGray, ColorReset, truncRes)
				}
				fmt.Println()
			},
			ConfirmToolCallWithAction: func(toolName string, args map[string]interface{}) (bool, bool) {
				prompt := fmt.Sprintf("\n%s❓ [Permission Required]%s Tool %s%s%s(%s).\n   Options: %s[y]%s Yes (once)  |  %s[a]%s Always (add to config.json whitelist)  |  %s[n]%s No (deny)\n   Choice [y/a/N]: ",
					ColorYellow, ColorReset, ColorBold, toolName, ColorReset, agent.FormatArgs(args),
					ColorBold, ColorReset, ColorGreen, ColorReset, ColorRed, ColorReset)
				rl.SetPrompt(prompt)
				ansLine, pErr := rl.Readline()
				rl.SetPrompt(buildPrompt(ag, sessMgr))
				if pErr != nil {
					return false, false
				}
				ans := strings.TrimSpace(strings.ToLower(ansLine))
				if ans == "a" || ans == "always" {
					fmt.Printf("%s[Config]%s Added '%s%s%s' to config.json whitelist for future executions.\n", ColorGreen, ColorReset, ColorBold, toolName, ColorReset)
					return true, true
				}
				if ans == "y" || ans == "yes" {
					return true, false
				}
				return false, false
			},
			OnSubagentThinkingStart: func(subType string) {
				subThinkingActive = true
				boldColor, italicColor := getSubagentPalette(subType)
				fmt.Printf("   %s↳ 🧠 [%s Thinking]%s %s", boldColor, subType, ColorReset, italicColor)
			},
			OnSubagentThinkingToken: func(token string) {
				fmt.Print(token)
			},
			OnSubagentThinkingEnd: func() {
				if subThinkingActive {
					subThinkingActive = false
					fmt.Printf("%s\n", ColorReset)
				}
			},
			OnSubagentToolCall: func(subType string, toolName string, args map[string]interface{}, result string, execErr error) {
				boldColor, dimColor := getSubagentPalette(subType)
				fmt.Printf("   %s↳ ⚙️  [%s Tool Executed]%s %s%s%s(%s)\n", boldColor, subType, ColorReset, ColorBold, toolName, ColorReset, agent.FormatArgs(args))
				if execErr != nil {
					fmt.Printf("   %s    ❌ [Error/Rejection]%s %v\n\n", ColorRed, ColorReset, execErr)
				} else {
					truncRes := result
					if len(truncRes) > 150 {
						truncRes = truncRes[:150] + "... [truncated]"
					}
					fmt.Printf("   %s    📥 [Output]%s %s\n\n", dimColor, ColorReset, truncRes)
				}
			},
		}

		_, err = ag.Ask(input, callbacks)
		if contentStarted {
			fmt.Println()
		}

		if err != nil {
			fmt.Printf("\n%s[Error]%s %v\n", ColorRed, ColorReset, err)
		}
		fmt.Println()
	}

	fmt.Printf("%sGoodbye! Session saved in %s. 👋%s\n", ColorYellow, sessMgr.GetCurrentPath(), ColorReset)
}

func getSubagentPalette(subType string) (boldColor string, secondaryColor string) {
	switch strings.ToLower(subType) {
	case "researcher":
		return ResBold, ResItalic
	case "coder":
		return CoderBold, CoderItalic
	case "tester":
		return TesterBold, TesterItalic
	case "reviewer":
		return ReviewerBold, ReviewerItalic
	default:
		return ResBold, ResDim
	}
}

func buildPrompt(ag *agent.Agent, sessMgr *session.Manager) string {
	goalStatus := ""
	if ag.IsGoalActive() {
		goalStatus = " | 🎯 Goal Active"
	}
	return fmt.Sprintf("%sUser [%s | %s | Mode:%s%s]> %s", MainBold, ag.GetModel(), sessMgr.GetCurrentID(), ag.GetToolMode(), goalStatus, ColorReset)
}

func printBanner(ag *agent.Agent, models []string, sessionID string) {
	registeredTools := ag.GetRegistry().GetDefinitions()

	fmt.Println(strings.Repeat("─", 70))
	fmt.Printf("%s  🤖 O.L.L.I. - Ollama-based Local LLM Interface  %s\n", MainBold, ColorReset)
	fmt.Println(strings.Repeat("─", 70))
	fmt.Printf("• %sActive Model:%s %s (%sContext: %d / 32K%s)\n", ColorBold, ColorReset, ag.GetModel(), ColorYellow, ag.GetNumCtx(), ColorReset)
	fmt.Printf("• %sTool Mode:%s %s%s%s (switch via '/mode <auto|ask|accept-edit>')\n", ColorBold, ColorReset, ColorMagenta, ag.GetToolMode(), ColorReset)
	fmt.Printf("• %sConfig File:%s ./config.json (dynamic whitelist)\n", ColorGray, ColorReset)
	fmt.Printf("• %sActive Session:%s %s (stored in ./sessions/)\n", ColorGreen, ColorReset, sessionID)

	if ag.IsGoalActive() {
		fmt.Printf("• %s🎯 Active Goal:%s %s%s%s\n", ColorBold, ColorMagenta, ColorBold, ag.GetGoal(), ColorReset)
	} else {
		fmt.Printf("• %s🎯 Active Goal:%s %sNone (use '/goal set <objective>')%s\n", ColorGray, ColorReset, ColorGray, ColorReset)
	}

	fmt.Printf("• %sAvailable Models (%d):%s %s\n", ColorGray, len(models), ColorReset, strings.Join(models, ", "))

	toolNames := make([]string, 0, len(registeredTools))
	for _, t := range registeredTools {
		toolNames = append(toolNames, t.Function.Name)
	}
	fmt.Printf("• %sSubagents Available:%s Researcher, Coder, Tester, Reviewer\n", ColorGreen, ColorReset)
	fmt.Printf("• %sRegistered Tools (%d):%s %s\n", ColorMagenta, len(registeredTools), ColorReset, strings.Join(toolNames, ", "))
	fmt.Printf("• %sCommands:%s /mode <auto|ask|accept-edit>, /config allow <tool>, /goal set, /exit\n", ColorGray, ColorReset)
	fmt.Println(strings.Repeat("─", 70))
	fmt.Println()
}

func handleCommand(cmd string, ag *agent.Agent, client *ollama.Client, availableModels []string) bool {
	parts := strings.Fields(cmd)
	command := parts[0]

	switch command {
	case "/exit", "/quit":
		return true

	case "/help":
		fmt.Println("\nAvailable Commands:")
		fmt.Println("  /mode <auto|ask|accept-edit> - Change Tool Execution Mode:")
		fmt.Println("                                   • auto        : Run ALL tools automatically without prompt")
		fmt.Println("                                   • ask         : Prompt user for ALL tool executions (y/N/a)")
		fmt.Println("                                   • accept-edit : Auto-run tools in config.json whitelist, prompt for others")
		fmt.Println("  /config whitelist     - List whitelisted tools in config.json")
		fmt.Println("  /config allow <tool>  - Add a tool to config.json whitelist for auto execution")
		fmt.Println("  /config deny <tool>   - Remove a tool from whitelist (require permission)")
		fmt.Println("  /goal set <text>      - Set active goal for agent steering")
		fmt.Println("  /goal clear           - Clear active goal")
		fmt.Println("  /goal status          - Show active goal status")
		fmt.Println("  /session list         - List all saved JSONL session files")
		fmt.Println("  /session new [name]   - Start a new session with custom name")
		fmt.Println("  /session load <name>  - Resume an existing session by name or ID")
		fmt.Println("  /session rename <name>- Rename active session")
		fmt.Println("  /summary              - View current conversation memory summary")
		fmt.Println("  /summarize            - Trigger LLM auto-summarization of history")
		fmt.Println("  /numctx [size]        - Show or change context window size")
		fmt.Println("  /tools                - List all registered agent tools")
		fmt.Println("  /models               - List all available local Ollama models")
		fmt.Println("  /model <name>         - Switch active model")
		fmt.Println("  /clear                - Clear in-memory conversation history")
		fmt.Println("  /exit                 - Quit the agent")
		fmt.Println()

	case "/mode":
		if len(parts) < 2 {
			fmt.Printf("%sCurrent Tool Mode: %s%s%s. Available: auto, ask, accept-edit%s\n\n", ColorYellow, ColorBold, ag.GetToolMode(), ColorYellow, ColorReset)
			return false
		}
		targetMode := agent.ToolMode(strings.ToLower(parts[1]))
		switch targetMode {
		case agent.ModeAuto, agent.ModeAsk, agent.ModeAcceptEdit:
			ag.SetToolMode(targetMode)
			fmt.Printf("%s[Agent]%s Tool Execution Mode switched to: %s%s%s\n\n", ColorGreen, ColorReset, ColorBold, targetMode, ColorReset)
		default:
			fmt.Printf("%sInvalid mode '%s'. Available: auto, ask, accept-edit%s\n\n", ColorRed, parts[1], ColorReset)
		}

	case "/config":
		handleConfigSubcommands(parts[1:], ag)

	case "/goal":
		handleGoalSubcommands(parts[1:], ag)

	case "/session", "/sessions":
		handleSessionSubcommands(parts[1:], ag)

	case "/summary":
		fmt.Printf("\n🧠 %sActive Conversation Memory Summary:%s\n%s\n\n", ColorBold, ColorReset, ag.GetSummary())

	case "/summarize":
		fmt.Printf("%s[Agent]%s Generating conversation summary using LLM...\n", ColorYellow, ColorReset)
		summary, err := ag.AutoSummarize()
		if err != nil {
			fmt.Printf("%s[Error]%s Failed to summarize: %v\n\n", ColorRed, ColorReset, err)
		} else {
			fmt.Printf("%s[Agent]%s Memory summary updated:\n%s%s%s\n\n", ColorGreen, ColorReset, ColorItalic, summary, ColorReset)
		}

	case "/tools":
		toolsList := ag.GetRegistry().GetDefinitions()
		fmt.Printf("\n🛠️  Registered Agent Tools (%d):\n", len(toolsList))
		for _, t := range toolsList {
			fmt.Printf("  • %s%s%s: %s\n", ColorBold, t.Function.Name, ColorReset, t.Function.Description)
		}
		fmt.Println()

	case "/models":
		models, err := client.ListModels()
		if err != nil {
			fmt.Printf("%s[Error]%s %v\n", ColorRed, ColorReset, err)
		} else {
			fmt.Printf("\nAvailable Local Models:\n")
			for _, m := range models {
				active := ""
				if m == ag.GetModel() {
					active = fmt.Sprintf(" %s(active)%s", ColorGreen, ColorReset)
				}
				fmt.Printf("  - %s%s\n", m, active)
			}
			fmt.Println()
		}

	case "/model":
		if len(parts) < 2 {
			fmt.Printf("%sUsage: /model <model_name>%s\n\n", ColorYellow, ColorReset)
			return false
		}
		targetModel := parts[1]
		ag.SetModel(targetModel)
		fmt.Printf("%s[Agent]%s Model switched to: %s%s%s\n\n", ColorGreen, ColorReset, ColorBold, targetModel, ColorReset)

	case "/numctx":
		if len(parts) < 2 {
			fmt.Printf("%sCurrent num_ctx: %d tokens. Usage: /numctx <size_in_tokens> (e.g. /numctx 16384)%s\n\n", ColorYellow, ag.GetNumCtx(), ColorReset)
			return false
		}
		var val int
		if _, err := fmt.Sscanf(parts[1], "%d", &val); err != nil || val <= 0 {
			fmt.Printf("%sInvalid num_ctx value '%s'.%s\n\n", ColorRed, parts[1], ColorReset)
			return false
		}
		ag.SetNumCtx(val)
		fmt.Printf("%s[Agent]%s Context window (num_ctx) set to: %s%d tokens%s\n\n", ColorGreen, ColorReset, ColorBold, val, ColorReset)

	case "/clear":
		ag.ClearHistory()
		fmt.Printf("%s[Agent]%s In-memory conversation history cleared.\n\n", ColorGreen, ColorReset)

	default:
		fmt.Printf("%sUnknown command '%s'. Type /help for assistance.%s\n\n", ColorYellow, command, ColorReset)
	}

	return false
}

func handleConfigSubcommands(args []string, ag *agent.Agent) {
	cfg := ag.GetConfig()
	if cfg == nil {
		fmt.Printf("%s[Error]%s Config is not initialized.\n\n", ColorRed, ColorReset)
		return
	}

	sub := "whitelist"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "whitelist", "list":
		fmt.Printf("\n📜 Whitelisted Tools in config.json (%d):\n", len(cfg.WhitelistTools))
		for _, t := range cfg.WhitelistTools {
			fmt.Printf("  • %s%s%s (auto-runs in accept-edit mode)\n", ColorGreen, t, ColorReset)
		}
		fmt.Println()

	case "allow", "add":
		if len(args) < 2 {
			fmt.Printf("%sUsage: /config allow <tool_name>%s\n\n", ColorYellow, ColorReset)
			return
		}
		toolName := args[1]
		if err := cfg.AddWhitelist(toolName); err != nil {
			fmt.Printf("%s[Error]%s %v\n\n", ColorRed, ColorReset, err)
		} else {
			fmt.Printf("%s[Config]%s Added '%s%s%s' to config.json whitelist.\n\n", ColorGreen, ColorReset, ColorBold, toolName, ColorReset)
		}

	case "deny", "remove":
		if len(args) < 2 {
			fmt.Printf("%sUsage: /config deny <tool_name>%s\n\n", ColorYellow, ColorReset)
			return
		}
		toolName := args[1]
		if err := cfg.RemoveWhitelist(toolName); err != nil {
			fmt.Printf("%s[Error]%s %v\n\n", ColorRed, ColorReset, err)
		} else {
			fmt.Printf("%s[Config]%s Removed '%s%s%s' from whitelist (will require permission in accept-edit mode).\n\n", ColorGreen, ColorReset, ColorBold, toolName, ColorReset)
		}

	default:
		fmt.Printf("%sUnknown config subcommand '%s'. Available: whitelist, allow <tool>, deny <tool>%s\n\n", ColorYellow, sub, ColorReset)
	}
}

func handleGoalSubcommands(args []string, ag *agent.Agent) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "set":
		if len(args) < 2 {
			fmt.Printf("%sUsage: /goal set <goal description>%s\n\n", ColorYellow, ColorReset)
			return
		}
		goalText := strings.Join(args[1:], " ")
		ag.SetGoal(goalText)
		fmt.Printf("%s🎯 [Goal]%s Active Goal set to: %s%s%s\n\n", ColorMagenta, ColorReset, ColorBold, goalText, ColorReset)

	case "clear", "unset":
		ag.ClearGoal()
		fmt.Printf("%s🎯 [Goal]%s Active Goal cleared.\n\n", ColorGreen, ColorReset)

	case "status":
		if ag.IsGoalActive() {
			fmt.Printf("\n🎯 %sCurrent Active Goal:%s\n\"%s\"\n\n", ColorBold, ColorReset, ag.GetGoal())
		} else {
			fmt.Printf("\n🎯 %sNo goal currently active.%s Use '/goal set <text>' to set one.\n\n", ColorGray, ColorReset)
		}

	default:
		fmt.Printf("%sUnknown goal subcommand '%s'. Available: set, clear, status%s\n\n", ColorYellow, sub, ColorReset)
	}
}

func handleSessionSubcommands(args []string, ag *agent.Agent) {
	mgr := ag.GetSessionManager()
	if mgr == nil {
		fmt.Printf("%s[Error]%s Session manager is not enabled.\n\n", ColorRed, ColorReset)
		return
	}

	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "list":
		list, err := mgr.ListSessions()
		if err != nil {
			fmt.Printf("%s[Error]%s %v\n\n", ColorRed, ColorReset, err)
			return
		}
		fmt.Printf("\n📁 Saved Sessions in ./sessions/ (%d):\n", len(list))
		for _, s := range list {
			active := ""
			if s.ID == mgr.GetCurrentID() {
				active = fmt.Sprintf(" %s(active)%s", ColorGreen, ColorReset)
			}
			fmt.Printf("  • %s%-35s%s | %d msgs | %s%s\n", ColorBold, s.ID, ColorReset, s.MessageCount, s.UpdatedAt.Format("2006-01-02 15:04:05"), active)
		}
		fmt.Println()

	case "new":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		info, err := mgr.CreateSession(name, ag.GetModel())
		if err != nil {
			fmt.Printf("%s[Error]%s %v\n\n", ColorRed, ColorReset, err)
			return
		}
		ag.ClearHistory()
		fmt.Printf("%s[Session]%s Created and switched to new session: %s%s%s\n\n", ColorGreen, ColorReset, ColorBold, info.ID, ColorReset)

	case "load":
		if len(args) < 2 {
			fmt.Printf("%sUsage: /session load <session_name_or_id>%s\n\n", ColorYellow, ColorReset)
			return
		}
		targetQuery := args[1]
		resolvedID, err := ag.LoadSession(targetQuery)
		if err != nil {
			fmt.Printf("%s[Error]%s Failed to load session '%s': %v\n\n", ColorRed, ColorReset, targetQuery, err)
			return
		}
		fmt.Printf("%s[Session]%s Successfully loaded session: %s%s%s (%d messages restored into memory)\n\n", ColorGreen, ColorReset, ColorBold, resolvedID, ColorReset, ag.GetHistoryCount())

	case "rename":
		if len(args) < 2 {
			fmt.Printf("%sUsage: /session rename <new_name> OR /session rename <old_id> <new_name>%s\n\n", ColorYellow, ColorReset)
			return
		}
		var oldTarget, newName string
		if len(args) == 2 {
			oldTarget = ""
			newName = args[1]
		} else {
			oldTarget = args[1]
			newName = args[2]
		}
		resName, err := mgr.RenameSession(oldTarget, newName)
		if err != nil {
			fmt.Printf("%s[Error]%s Failed to rename session: %v\n\n", ColorRed, ColorReset, err)
			return
		}
		fmt.Printf("%s[Session]%s Session successfully renamed to: %s%s%s\n\n", ColorGreen, ColorReset, ColorBold, resName, ColorReset)

	case "current":
		fmt.Printf("\n📌 Current Active Session:\n")
		fmt.Printf("  • ID/Name: %s%s%s\n", ColorBold, mgr.GetCurrentID(), ColorReset)
		fmt.Printf("  • File: %s\n", mgr.GetCurrentPath())
		fmt.Printf("  • Memory Messages: %d\n\n", ag.GetHistoryCount())

	case "delete":
		if len(args) < 2 {
			fmt.Printf("%sUsage: /session delete <session_name_or_id>%s\n\n", ColorYellow, ColorReset)
			return
		}
		targetID := args[1]
		err := mgr.DeleteSession(targetID)
		if err != nil {
			fmt.Printf("%s[Error]%s %v\n\n", ColorRed, ColorReset, err)
			return
		}
		fmt.Printf("%s[Session]%s Session '%s' deleted.\n\n", ColorGreen, ColorReset, targetID)

	default:
		fmt.Printf("%sUnknown session subcommand '%s'. Available: list, new, load, rename, current, delete%s\n\n", ColorYellow, sub, ColorReset)
	}
}
