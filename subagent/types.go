package subagent

type SubagentType string

const (
	TypePlanner    SubagentType = "Planner"
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

type successfulToolCall struct {
	Name      string
	Arguments map[string]interface{}
}

type executionEvidence struct {
	ToolCallsSucceeded int
	SuccessfulTools    map[string]int
	SuccessfulCalls    []successfulToolCall
}

func (e *executionEvidence) recordSuccess(toolName string, arguments map[string]interface{}) {
	if e == nil {
		return
	}
	e.ToolCallsSucceeded++
	if e.SuccessfulTools == nil {
		e.SuccessfulTools = make(map[string]int)
	}
	e.SuccessfulTools[toolName]++
	copied := make(map[string]interface{}, len(arguments))
	for key, value := range arguments {
		copied[key] = value
	}
	e.SuccessfulCalls = append(e.SuccessfulCalls, successfulToolCall{Name: toolName, Arguments: copied})
}

type ResultReport struct {
	SubagentID    string                `json:"subagent_id"`
	Type          string                `json:"type"`
	Task          string                `json:"task"`
	Status        string                `json:"status"`
	Summary       string                `json:"summary"`
	JSONLFile     string                `json:"jsonl_file"`
	ToolCallsRun  int                   `json:"tool_calls_run"`
	WorkingDir    string                `json:"working_dir"`
	Termination   LoopTerminationReason `json:"termination,omitempty"`
	LoopMetrics   *LoopMetrics          `json:"loop_metrics,omitempty"`
	ArtifactFiles []string              `json:"artifact_files,omitempty"`
	CreatedFiles  []string              `json:"created_files,omitempty"`
}
