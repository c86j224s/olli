package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ergochat/readline"

	"github.com/c86j224s/olli/agent"
	"github.com/c86j224s/olli/cli"
	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
)

func main() {
	// Restore standard POSIX terminal termios settings (ONLCR) to fix staircase newline drifting
	_ = exec.Command("stty", "sane").Run()

	client := ollama.NewClient("http://localhost:11434")

	models, err := client.ListModels()
	if err != nil {
		fmt.Printf("%s[Error]%s Failed to connect to Ollama: %v\n", cli.ColorRed, cli.ColorReset, err)
		fmt.Println("Please make sure Ollama is running (`ollama serve`).")
		os.Exit(1)
	}

	if len(models) == 0 {
		fmt.Printf("%s[Error]%s No local models found in Ollama.\n", cli.ColorRed, cli.ColorReset)
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
		fmt.Printf("%s[Error]%s Failed to initialize session manager: %v\n", cli.ColorRed, cli.ColorReset, err)
		os.Exit(1)
	}

	cfg, err := config.LoadConfig("./config.json")
	if err != nil {
		fmt.Printf("%s[Error]%s Failed to load config.json: %v\n", cli.ColorRed, cli.ColorReset, err)
		os.Exit(1)
	}

	ag := agent.New(client, defaultModel, "You are an intelligent AI assistant equipped with Goal Steering and Subagent Delegation capabilities. Stay focused on achieving active goals.", sessMgr, cfg)

	cli.PrintBanner(ag, models, sessMgr.GetCurrentID())

	completer := readline.NewPrefixCompleter(
		readline.PcItem("/help"),
		readline.PcItem("/cd"),
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
		Prompt:          cli.BuildPrompt(ag, sessMgr),
		HistoryFile:     historyFile,
		AutoComplete:    completer,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	}

	rl, err := readline.NewEx(rlConfig)
	if err != nil {
		fmt.Printf("%s[Error]%s Failed to initialize readline: %v\n", cli.ColorRed, cli.ColorReset, err)
		os.Exit(1)
	}
	defer func() {
		rl.Close()
		_ = exec.Command("stty", "sane").Run()
	}()

	for {
		rl.SetPrompt(cli.BuildPrompt(ag, sessMgr))

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
			if cli.HandleCommand(input, ag, client, models) {
				break
			}
			continue
		}

		fmt.Println()
		fmt.Printf("%s💬 User:%s %s\n\n", cli.ColorBold+cli.ColorCyan, cli.ColorReset, input)

		contentStarted := false
		subThinkingActive := false

		callbacks := agent.Callbacks{
			OnThinkingStart: func() {
				fmt.Printf("%s🧠 [Thinking]%s %s", cli.MainItalic, cli.ColorReset, cli.ColorGray)
			},
			OnThinkingToken: func(token string) {
				fmt.Print(token)
			},
			OnThinkingEnd: func() {
				fmt.Printf("%s\n\n", cli.ColorReset)
			},
			OnContentToken: func(token string) {
				if !contentStarted {
					contentStarted = true
					fmt.Printf("%sAgent (%s):%s\n", cli.ColorBold+cli.ColorGreen, ag.GetModel(), cli.ColorReset)
				}
				fmt.Print(token)
			},
			OnToolCall: func(toolName string, args map[string]interface{}, result string, execErr error) {
				fmt.Printf("\n%s⚙️  [Main Tool]%s %s%s%s(%s)\n", cli.MainBold, cli.ColorReset, cli.ColorBold, toolName, cli.ColorReset, agent.FormatArgs(args))
				if execErr != nil {
					fmt.Printf("%s   ❌ [Error/Rejection]%s %v\n", cli.ColorRed, cli.ColorReset, execErr)
				} else {
					truncRes := result
					if len(truncRes) > 200 {
						truncRes = truncRes[:200] + "... [truncated]"
					}
					fmt.Printf("%s   📥 [Output Summary]%s %s\n", cli.ColorGray, cli.ColorReset, truncRes)
				}
				fmt.Println()
			},
			ConfirmToolCallWithAction: func(toolName string, args map[string]interface{}) (bool, bool) {
				prompt := fmt.Sprintf("\n%s❓ [Permission Required]%s Tool %s%s%s(%s).\n   Options: %s[y]%s Yes (once)  |  %s[a]%s Always (add to config.json whitelist)  |  %s[n]%s No (deny)\n   Choice [y/a/N]: ",
					cli.ColorYellow, cli.ColorReset, cli.ColorBold, toolName, cli.ColorReset, agent.FormatArgs(args),
					cli.ColorBold, cli.ColorReset, cli.ColorGreen, cli.ColorReset, cli.ColorRed, cli.ColorReset)
				rl.SetPrompt(prompt)
				ansLine, pErr := rl.Readline()
				rl.SetPrompt(cli.BuildPrompt(ag, sessMgr))
				if pErr != nil {
					return false, false
				}
				ans := strings.TrimSpace(strings.ToLower(ansLine))
				if ans == "a" || ans == "always" {
					fmt.Printf("%s[Config]%s Added '%s%s%s' to config.json whitelist for future executions.\n", cli.ColorGreen, cli.ColorReset, cli.ColorBold, toolName, cli.ColorReset)
					return true, true
				}
				if ans == "y" || ans == "yes" {
					return true, false
				}
				return false, false
			},
			OnSubagentThinkingStart: func(subType string) {
				subThinkingActive = true
				boldColor, italicColor := cli.GetSubagentPalette(subType)
				fmt.Printf("   %s↳ 🧠 [%s Thinking]%s %s", boldColor, subType, cli.ColorReset, italicColor)
			},
			OnSubagentThinkingToken: func(token string) {
				fmt.Print(token)
			},
			OnSubagentThinkingEnd: func() {
				if subThinkingActive {
					subThinkingActive = false
					fmt.Printf("%s\n", cli.ColorReset)
				}
			},
			OnSubagentToolCall: func(subType string, toolName string, args map[string]interface{}, result string, execErr error) {
				boldColor, dimColor := cli.GetSubagentPalette(subType)
				fmt.Printf("   %s↳ ⚙️  [%s Tool Executed]%s %s%s%s(%s)\n", boldColor, subType, cli.ColorReset, cli.ColorBold, toolName, cli.ColorReset, agent.FormatArgs(args))
				if execErr != nil {
					fmt.Printf("   %s    ❌ [Error/Rejection]%s %v\n\n", cli.ColorRed, cli.ColorReset, execErr)
				} else {
					truncRes := result
					if len(truncRes) > 150 {
						truncRes = truncRes[:150] + "... [truncated]"
					}
					fmt.Printf("   %s    📥 [Output]%s %s\n\n", dimColor, cli.ColorReset, truncRes)
				}
			},
		}

		askCtx, cancel := context.WithCancel(context.Background())
		doneChan := make(chan struct{})

		cli.StartInterruptListener(cancel, doneChan)

		_, err = ag.AskWithContext(askCtx, input, callbacks)
		close(doneChan)
		cancel()

		if contentStarted {
			fmt.Println()
		}

		if err != nil {
			if err == context.Canceled {
				fmt.Printf("\n%s⚠️ [Interrupted]%s Generation canceled by user (Ctrl+C).\n", cli.ColorYellow, cli.ColorReset)
			} else {
				fmt.Printf("\n%s[Error]%s %v\n", cli.ColorRed, cli.ColorReset, err)
			}
		}
		fmt.Println()
	}

	fmt.Printf("%sGoodbye! Session saved in %s. 👋%s\n", cli.ColorYellow, sessMgr.GetCurrentPath(), cli.ColorReset)
}
