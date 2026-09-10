package subagent

import (
	"context"
	"testing"
	"time"
)

func TestContextTerminationDistinguishesDeadlineAndCancellation(t *testing.T) {
	deadlineCtx, cancelDeadline := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancelDeadline()
	<-deadlineCtx.Done()
	reason, status, _ := contextTermination(deadlineCtx)
	if reason != LoopTerminationTimedOut || status != "TIMED_OUT" {
		t.Fatalf("deadline misclassified: %s %s", reason, status)
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	reason, status, _ = contextTermination(cancelCtx)
	if reason != LoopTerminationCancelled || status != "INTERRUPTED" {
		t.Fatalf("cancellation misclassified: %s %s", reason, status)
	}
}

func TestLoopMetricsCarryTerminationReason(t *testing.T) {
	guard, _ := newLoopGuard(DefaultLoopPolicy(false))
	guard.beginIteration()
	guard.recordModelCall()
	guard.recordToolCall("view_file", map[string]interface{}{"file_path": "a.go"})
	metrics := guard.terminate(LoopTerminationRepeatedAction)
	if metrics.Termination != LoopTerminationRepeatedAction || metrics.Iterations != 1 || metrics.ModelCalls != 1 || metrics.ToolCalls != 1 {
		t.Fatalf("unexpected loop metrics: %#v", metrics)
	}
}
