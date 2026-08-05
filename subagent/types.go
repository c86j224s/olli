package subagent

type SubagentType string

const (
	TypeResearcher SubagentType = "Researcher"
	TypeCoder      SubagentType = "Coder"
	TypeTester     SubagentType = "Tester"
	TypeReviewer   SubagentType = "Reviewer"
	TypeDocumenter SubagentType = "Documenter"
	TypePresenter  SubagentType = "Presenter"
)

type SubagentCallbacks struct {
	OnThinkingStart func(subType string)
	OnThinkingToken func(token string)
	OnThinkingEnd   func()
	OnToolCall      func(subType string, toolName string, args map[string]interface{}, result string, execErr error)
}

type ResultReport struct {
	SubagentID   string `json:"subagent_id"`
	Type         string `json:"type"`
	Task         string `json:"task"`
	Status       string `json:"status"`
	Summary      string `json:"summary"`
	JSONLFile    string `json:"jsonl_file"`
	ToolCallsRun int    `json:"tool_calls_run"`
}
