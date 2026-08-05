package cli

import (
	"fmt"
	"strings"

	"github.com/c86j224s/olli/agent"
	"github.com/c86j224s/olli/session"
)

func PrintBanner(ag *agent.Agent, models []string, sessionID string) {
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
	fmt.Printf("• %sSubagents Available (6):%s Researcher, Coder, Tester, Reviewer, Documenter, Presenter\n", ColorGreen, ColorReset)
	fmt.Printf("• %sRegistered Tools (%d):%s %s\n", ColorMagenta, len(registeredTools), ColorReset, strings.Join(toolNames, ", "))
	fmt.Printf("• %sCommands:%s /mode <auto|ask|accept-edit>, /config allow <tool>, /goal set, /exit\n", ColorGray, ColorReset)
	fmt.Println(strings.Repeat("─", 70))
	fmt.Println()
}

func BuildPrompt(ag *agent.Agent, sessMgr *session.Manager) string {
	goalStatus := ""
	if ag.IsGoalActive() {
		goalStatus = " | 🎯 Goal Active"
	}
	return fmt.Sprintf("%sUser [%s | %s | Mode:%s%s]> %s", MainBold, ag.GetModel(), sessMgr.GetCurrentID(), ag.GetToolMode(), goalStatus, ColorReset)
}
