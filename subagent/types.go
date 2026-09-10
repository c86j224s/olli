package subagent

import (
	"errors"

	agentloop "github.com/c86j224s/olli/loop"
)

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
	Name           string
	Arguments      map[string]interface{}
	ProgressMarker string
	Succeeded      bool
	ExitCode       int
}

type executionEvidence struct {
	ToolCallsAttempted int
	ToolCallsSucceeded int
	SuccessfulTools    map[string]int
	AttemptedCalls     []successfulToolCall
	SuccessfulCalls    []successfulToolCall
	ProgressMarker     func(toolName string, arguments map[string]interface{}, result string) string
	ProgressState      func() string
}

func (e *executionEvidence) recordAttempt(toolName string, arguments map[string]interface{}, result string, execErr error) {
	if e == nil {
		return
	}
	e.ToolCallsAttempted++
	call := e.toolCall(toolName, arguments, result)
	call.Succeeded = execErr == nil
	call.ExitCode = commandExitCode(execErr)
	e.AttemptedCalls = append(e.AttemptedCalls, call)
}

func commandExitCode(execErr error) int {
	if execErr == nil {
		return 0
	}
	var exitCoder interface{ ExitCode() int }
	if errors.As(execErr, &exitCoder) {
		return exitCoder.ExitCode()
	}
	return -1
}

func (e *executionEvidence) recordSuccess(toolName string, arguments map[string]interface{}, result string) {
	if e == nil {
		return
	}
	e.ToolCallsSucceeded++
	if e.SuccessfulTools == nil {
		e.SuccessfulTools = make(map[string]int)
	}
	e.SuccessfulTools[toolName]++
	e.SuccessfulCalls = append(e.SuccessfulCalls, e.toolCall(toolName, arguments, result))
}

func (e *executionEvidence) toolCall(toolName string, arguments map[string]interface{}, result string) successfulToolCall {
	copied := make(map[string]interface{}, len(arguments))
	for key, value := range arguments {
		copied[key] = value
	}
	marker := ""
	if e.ProgressMarker != nil {
		marker = e.ProgressMarker(toolName, copied, result)
	}
	return successfulToolCall{Name: toolName, Arguments: copied, ProgressMarker: marker}
}

type ResultReport struct {
	SubagentID    string                      `json:"subagent_id"`
	Type          string                      `json:"type"`
	Task          string                      `json:"task"`
	Status        string                      `json:"status"`
	Summary       string                      `json:"summary"`
	JSONLFile     string                      `json:"jsonl_file"`
	ToolCallsRun  int                         `json:"tool_calls_run"`
	WorkingDir    string                      `json:"working_dir"`
	Termination   agentloop.TerminationReason `json:"termination,omitempty"`
	LoopMetrics   *agentloop.Metrics          `json:"loop_metrics,omitempty"`
	ArtifactFiles []string                    `json:"artifact_files,omitempty"`
	CreatedFiles  []string                    `json:"created_files,omitempty"`
}
