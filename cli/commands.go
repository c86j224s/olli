package cli

import (
	"fmt"
	"strings"

	"github.com/c86j224s/olli/agent"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

func HandleCommand(cmd string, ag *agent.Agent, client *ollama.Client, availableModels []string) bool {
	parts := strings.Fields(cmd)
	command := parts[0]

	switch command {
	case "/exit", "/quit":
		return true

	case "/help":
		fmt.Println("\nAvailable Commands:")
		fmt.Println("  /cd <path>           - Jump to another project working directory")
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

	case "/cd":
		if len(parts) < 2 {
			fmt.Printf("%sCurrent Working Directory: %s\nUsage: /cd <target_directory_path>%s\n\n", ColorYellow, ag.GetCurrentDir(), ColorReset)
			return false
		}
		targetDir := parts[1]
		safeDir, err := tools.IsPathSafe(targetDir, ag.GetCurrentDir())
		if err != nil {
			fmt.Printf("%s[Security Block]%s Cannot change directory to '%s': %v\n\n", ColorRed, ColorReset, targetDir, err)
			return false
		}
		ag.SetCurrentDir(safeDir)
		fmt.Printf("%s[Agent]%s Working directory changed to: %s%s%s\n\n", ColorGreen, ColorReset, ColorBold, safeDir, ColorReset)

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
		fmt.Printf("%sUnknown session subcommand '%s'. Type /help for assistance.%s\n\n", ColorYellow, sub, ColorReset)
	}
}
