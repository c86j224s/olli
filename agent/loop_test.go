package agent

import (
	"testing"

	agentloop "github.com/c86j224s/olli/loop"
	"github.com/c86j224s/olli/ollama"
)

func TestAppendSkippedToolResultsCompletesBatch(t *testing.T) {
	agent := &Agent{}
	calls := []ollama.ToolCall{
		{Function: ollama.ToolCallFunction{Name: "first"}},
		{Function: ollama.ToolCallFunction{Name: "second"}},
	}
	appendSkippedToolResults(agent, calls, "was skipped")
	if len(agent.history) != 2 || agent.history[0].Role != "tool" || agent.history[1].Role != "tool" {
		t.Fatalf("tool batch was not completed: %#v", agent.history)
	}
}

func TestContextLoopTerminationDistinguishesTimeout(t *testing.T) {
	if reason := contextLoopTermination(nil); reason != agentloop.TerminationCancelled {
		t.Fatalf("nil context misclassified: %s", reason)
	}
}

func TestMainLoopRunningSentinelDoesNotStopToolBatch(t *testing.T) {
	termination := agentloop.TerminationRunning
	if termination != agentloop.TerminationRunning {
		t.Fatal("running loop would stop a valid tool batch")
	}
	termination = agentloop.TerminationDenied
	if termination == agentloop.TerminationRunning {
		t.Fatal("denied loop would continue a tool batch")
	}
}

func TestMainLoopProgressUsesUniqueActionFingerprints(t *testing.T) {
	a := agentloop.ActionFingerprint("get_agent_status", map[string]interface{}{})
	b := agentloop.ActionFingerprint("get_current_time", map[string]interface{}{})
	seen := map[string]struct{}{a: {}}
	seen[a] = struct{}{}
	if len(seen) != 1 {
		t.Fatal("repeated successful action changed progress")
	}
	seen[b] = struct{}{}
	if len(seen) != 2 {
		t.Fatal("new successful action did not change progress")
	}
}
