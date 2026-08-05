package cli

import (
	"fmt"
	"strings"

	"github.com/c86j224s/olli/agent"
	"github.com/c86j224s/olli/ollama"
)

func HandleCommand(cmdStr string, ag *agent.Agent, client *ollama.Client, models []string) bool {
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return false
	}

	command := parts[0]
	args := parts[1:]

	switch command {
	case "/exit", "/quit":
		return true

	case "/help":
		printHelp()

	case "/cd":
		if len(args) == 0 {
			fmt.Printf("%sUsage: /cd <directory_path>%s\n\n", ColorYellow, ColorReset)
			return false
		}
		target := strings.Join(args, " ")
		res, err := ag.GetRegistry().Execute("cd", map[string]interface{}{"path": target})
		if err != nil {
			fmt.Printf("%s[Error]%s %v\n\n", ColorRed, ColorReset, err)
		} else {
			fmt.Printf("%s[Workspace]%s %s\n\n", ColorGreen, ColorReset, res)
		}

	case "/summary", "/summarize":
		fmt.Printf("\n📜 %sMemory Summary:%s\n%s\n\n", ColorBold, ColorReset, ag.GetSummary())

	case "/numctx":
		if len(args) == 0 {
			fmt.Printf("\n⚙️ Current NumCtx (Context Window Size): %s%d%s tokens\n\n", ColorBold, ag.GetNumCtx(), ColorReset)
			return false
		}
		var val int
		_, err := fmt.Sscanf(args[0], "%d", &val)
		if err != nil || val <= 0 {
			fmt.Printf("%s[Error]%s Invalid token size: %s. Must be a positive integer.\n\n", ColorRed, ColorReset, args[0])
			return false
		}
		ag.SetNumCtx(val)
		fmt.Printf("%s[Config]%s Updated NumCtx to %s%d%s tokens.\n\n", ColorGreen, ColorReset, ColorBold, val, ColorReset)

	case "/mode":
		if len(args) == 0 {
			fmt.Printf("\n⚙️ Current Tool Mode: %s%s%s\nAvailable modes: auto, ask, accept-edit\n\n", ColorBold, ag.GetToolMode(), ColorReset)
			return false
		}
		mode := agent.ToolMode(args[0])
		if mode != agent.ModeAuto && mode != agent.ModeAsk && mode != agent.ModeAcceptEdit {
			fmt.Printf("%s[Error]%s Invalid mode '%s'. Available modes: auto, ask, accept-edit\n\n", ColorRed, ColorReset, args[0])
			return false
		}
		ag.SetToolMode(mode)
		fmt.Printf("%s[Config]%s Tool execution mode set to: %s%s%s\n\n", ColorGreen, ColorReset, ColorBold, mode, ColorReset)

	case "/config":
		handleConfigSubcommands(args, ag)

	case "/goal":
		handleGoalSubcommands(args, ag)

	case "/tools":
		fmt.Printf("\n🛠️ Registered Tools (%d):\n", len(ag.GetRegistry().GetDefinitions()))
		for _, t := range ag.GetRegistry().GetDefinitions() {
			fmt.Printf("  • %s%s%s: %s\n", ColorBold, t.Function.Name, ColorReset, t.Function.Description)
		}
		fmt.Println()

	case "/models":
		fmt.Printf("\n📦 Available Ollama Models (%d):\n", len(models))
		for _, m := range models {
			current := ""
			if m == ag.GetModel() {
				current = fmt.Sprintf(" %s(active)%s", ColorGreen, ColorReset)
			}
			fmt.Printf("  • %s%s\n", m, current)
		}
		fmt.Println()

	case "/model":
		if len(args) == 0 {
			fmt.Printf("\n🤖 Active Model: %s%s%s\n\n", ColorBold, ag.GetModel(), ColorReset)
			return false
		}
		targetModel := args[0]
		found := false
		for _, m := range models {
			if m == targetModel {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("%s[Warning]%s Model '%s' not listed in local Ollama models. Setting anyway...\n", ColorYellow, ColorReset, targetModel)
		}
		ag.SetModel(targetModel)
		fmt.Printf("%s[Config]%s Active model switched to: %s%s%s\n\n", ColorGreen, ColorReset, ColorBold, targetModel, ColorReset)

	case "/session":
		handleSessionSubcommands(args, ag)

	case "/clear":
		ag.ClearHistory()
		fmt.Printf("%s[Agent]%s Context history cleared.\n\n", ColorYellow, ColorReset)

	default:
		fmt.Printf("%sUnknown command '%s'. Type '/help' for assistance.%s\n\n", ColorYellow, command, ColorReset)
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
		fmt.Printf("%s[Session]%s Successfully loaded session: %s%s%s (%d messages restored)\n", ColorGreen, ColorReset, ColorBold, resolvedID, ColorReset, ag.GetHistoryCount())
		fmt.Printf("   📂 %sRestored Working Directory:%s %s\n\n", ColorBold, ColorReset, ag.GetCurrentDir())

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
		fmt.Printf("  • Working Directory: %s%s%s\n", ColorBold, ag.GetCurrentDir(), ColorReset)
		fmt.Printf("  • Memory Messages: %d\n\n", ag.GetHistoryCount())

	case "delete":
		if len(args) < 2 {
			fmt.Printf("%sUsage: /session delete <session_name_or_id>%s\n\n", ColorYellow, ColorReset)
			return
		}
		targetID := args[1]
		resID, err := mgr.DeleteSession(targetID)
		if err != nil {
			fmt.Printf("%s[Error]%s %v\n\n", ColorRed, ColorReset, err)
			return
		}
		fmt.Printf("%s[Session]%s Session '%s' deleted.\n\n", ColorGreen, ColorReset, resID)

	default:
		fmt.Printf("%sUnknown session subcommand '%s'. Type /help for assistance.%s\n\n", ColorYellow, sub, ColorReset)
	}
}

func printHelp() {
	fmt.Printf("\n📖 %sOllama Toy Agent Help Guide%s\n", ColorBold, ColorReset)
	fmt.Println("Commands:")
	fmt.Println("  /cd <dir>                   : Change active workspace working directory")
	fmt.Println("  /mode [auto|ask|accept-edit]: View or change tool execution mode")
	fmt.Println("  /config [whitelist|allow|deny]: Manage auto-approved tool whitelist")
	fmt.Println("  /goal [set|clear|status]    : Manage goal steering")
	fmt.Println("  /session [list|new|load|rename|current|delete]: Manage persistent sessions")
	fmt.Println("  /summary                    : View agent conversation memory summary")
	fmt.Println("  /numctx [tokens]            : View or update context window size")
	fmt.Println("  /tools                      : View registered tools")
	fmt.Println("  /models                     : List available Ollama models")
	fmt.Println("  /model <name>               : Switch active Ollama model")
	fmt.Println("  /clear                      : Clear conversation context")
	fmt.Println("  /exit, /quit                : Exit agent")
	fmt.Println()
}
