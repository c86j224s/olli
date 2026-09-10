package subagent

import (
	"context"
	"testing"
	"time"

	agentloop "github.com/c86j224s/olli/loop"
)

func TestContextTerminationDistinguishesDeadlineAndCancellation(t *testing.T) {
	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelDeadline()
	<-deadlineCtx.Done()
	reason, status, _ := contextTermination(deadlineCtx)
	if reason != agentloop.TerminationTimedOut || status != "TIMED_OUT" {
		t.Fatalf("deadline misclassified: %s %s", reason, status)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	reason, status, _ = contextTermination(cancelCtx)
	if reason != agentloop.TerminationCancelled || status != "INTERRUPTED" {
		t.Fatalf("cancellation misclassified: %s %s", reason, status)
	}
}

func TestLoopMetricsCarryTerminationReason(t *testing.T) {
	guard, _ := agentloop.NewController(agentloop.DefaultPolicy(false))
	guard.BeginIteration()
	guard.RecordModelCall()
	guard.RecordToolCall("view_file", map[string]interface{}{"file_path": "a.go"})
	metrics := guard.Terminate(agentloop.TerminationRepeatedAction)
	if metrics.Termination != agentloop.TerminationRepeatedAction || metrics.Iterations != 1 || metrics.ModelCalls != 1 || metrics.ToolCalls != 1 {
		t.Fatalf("unexpected loop metrics: %#v", metrics)
	}
}
