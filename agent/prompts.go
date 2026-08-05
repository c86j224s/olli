package agent

import (
	"fmt"
	"time"

	"github.com/c86j224s/olli/ollama"
)

func (a *Agent) buildMessagesPayload() []ollama.Message {
	msgs := make([]ollama.Message, 0, len(a.history)+1)

	fullSystemPrompt := a.systemMsg

	subagentProtocol := "\n\n🤖 [SUBAGENT DELEGATION PROTOCOL]:\n" +
		"- For writing, editing, or refactoring code: Call 'delegate_coder(task_description)'.\n" +
		"- For running tests (go test), build verification, or runtime testing: Call 'delegate_tester(task_description)'.\n" +
		"- For static code review, code style checks, or architecture inspection: Call 'delegate_reviewer(task_description)'.\n" +
		"- For technical Markdown documentation, READMEs, or manuals: Call 'delegate_documenter(task_description)'.\n" +
		"- For creating interactive HTML PPT slide presentations: Call 'delegate_presenter(task_description)'.\n" +
		"- For web searching or URL reading: Call 'delegate_researcher(task_description)'."

	fullSystemPrompt += subagentProtocol

	now := time.Now()
	timeZone, _ := now.Zone()
	envContext := fmt.Sprintf("\n\n🌐 [ENVIRONMENT & TEMPORAL CONTEXT]:\n- Current Local Time: %s (%s, %s)\n- Active Workspace Working Directory: %s\n(CRITICAL REQUIREMENT: All relative file paths, terminal commands, and tool calls operate strictly within this Active Workspace Working Directory: '%s')",
		now.Format("2006-01-02 15:04:05"),
		timeZone,
		now.Format("Monday"),
		a.currentDir,
		a.currentDir,
	)
	fullSystemPrompt += envContext

	if a.IsGoalActive() {
		fullSystemPrompt += fmt.Sprintf("\n\n🎯 [ACTIVE GOAL / MISSION STEERING]:\n\"%s\"\n\nCRITICAL INSTRUCTION: Stay focused on achieving this objective. Once completed, call 'complete_goal'.", a.activeGoal)
	}

	msgs = append(msgs, ollama.Message{
		Role:    "system",
		Content: fullSystemPrompt,
	})

	if a.summary != "" && len(a.history) > 10 {
		msgs = append(msgs, ollama.Message{
			Role:    "system",
			Content: fmt.Sprintf("📜 [Active Conversation Summary]: %s", a.summary),
		})
	}

	msgs = append(msgs, a.history...)
	return msgs
}
