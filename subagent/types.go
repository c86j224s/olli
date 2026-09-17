package subagent

import (
	"errors"
	"sort"
	"strings"
	"time"

	agentloop "github.com/c86j224s/olli/loop"
)

type SubagentType string

const (
	TypePlanner       SubagentType = "Planner"
	TypeResearcher    SubagentType = "Researcher"
	TypeCoder         SubagentType = "Coder"
	TypeTester        SubagentType = "Tester"
	TypeReviewer      SubagentType = "Reviewer"
	TypeDocumenter    SubagentType = "Documenter"
	TypePresenter     SubagentType = "Presenter"
	TypePresenterPlan SubagentType = "PresenterPlan"
)

type SubagentCallbacks struct {
	OnThinkingStart  func(subType string)
	OnThinkingToken  func(token string)
	OnThinkingEnd    func()
	OnToolCall       func(subType string, toolName string, args map[string]interface{}, result string, execErr error)
	OnModelHeartbeat func(subType string, elapsed time.Duration)
}

type successfulToolCall struct {
	Name           string
	Arguments      map[string]interface{}
	ProgressMarker string
	Result         string
	Succeeded      bool
	ExitCode       int
}

type requiredToolCall struct {
	Name        string
	Fingerprint string
	Description string
	Attempted   bool
}

type executionEvidence struct {
	ToolCallsAttempted int
	ToolCallsSucceeded int
	SuccessfulTools    map[string]int
	AttemptedCalls     []successfulToolCall
	SuccessfulCalls    []successfulToolCall
	RequiredTools      map[string]int
	RequiredAnyTools   []string
	RequiredCalls      []requiredToolCall
	ProgressMarker     func(toolName string, arguments map[string]interface{}, result string) string
	ProgressState      func() string
	CompletionReady    func() bool
}

func (e *executionEvidence) recordAttempt(toolName string, arguments map[string]interface{}, result string, execErr error) successfulToolCall {
	if e == nil {
		return successfulToolCall{}
	}
	e.ToolCallsAttempted++
	call := e.toolCall(toolName, arguments, result)
	call.Succeeded = execErr == nil
	call.ExitCode = commandExitCode(execErr)
	e.AttemptedCalls = append(e.AttemptedCalls, call)
	fingerprint := agentloop.ActionFingerprint(toolName, copiedArguments(call.Arguments))
	for index := range e.RequiredCalls {
		if e.RequiredCalls[index].Name == toolName && e.RequiredCalls[index].Fingerprint == fingerprint {
			e.RequiredCalls[index].Attempted = true
		}
	}
	return call
}

func copiedArguments(arguments map[string]interface{}) map[string]interface{} {
	copied := make(map[string]interface{}, len(arguments))
	for key, value := range arguments {
		copied[key] = value
	}
	return copied
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

func (e *executionEvidence) recordSuccess(call successfulToolCall) {
	if e == nil {
		return
	}
	e.ToolCallsSucceeded++
	if e.SuccessfulTools == nil {
		e.SuccessfulTools = make(map[string]int)
	}
	e.SuccessfulTools[call.Name]++
	e.SuccessfulCalls = append(e.SuccessfulCalls, call)
}

func (e *executionEvidence) missingRequiredTools() []string {
	if e == nil {
		return nil
	}
	var missing []string
	for name, count := range e.RequiredTools {
		if e.SuccessfulTools[name] < count {
			missing = append(missing, name)
		}
	}
	if len(e.RequiredAnyTools) > 0 {
		satisfied := false
		for _, name := range e.RequiredAnyTools {
			if e.SuccessfulTools[name] > 0 {
				satisfied = true
				break
			}
		}
		if !satisfied {
			missing = append(missing, "one of "+strings.Join(e.RequiredAnyTools, "/"))
		}
	}
	for _, call := range e.RequiredCalls {
		if !call.Attempted {
			missing = append(missing, call.Description)
		}
	}
	sort.Strings(missing)
	return missing
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
	return successfulToolCall{Name: toolName, Arguments: copied, ProgressMarker: marker, Result: result}
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
