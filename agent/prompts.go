package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/c86j224s/olli/ollama"
)

func (a *Agent) buildMessagesPayload() []ollama.Message {
	msgs := make([]ollama.Message, 0, len(a.history)+1)

	fullSystemPrompt := a.systemPrompt

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
