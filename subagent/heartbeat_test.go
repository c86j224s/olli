package subagent

import (
	"context"
	"testing"
	"time"
)

func TestHeartbeatIntervalIsVisibleButBounded(t *testing.T) {
	if modelHeartbeatInterval < 10*time.Second || modelHeartbeatInterval > time.Minute {
		t.Fatalf("unexpected heartbeat interval: %v", modelHeartbeatInterval)
	}
}

func TestRunnerRoleBudgetOverridesDefaults(t *testing.T) {
	runner := &SubagentRunner{budgetOverrides: map[SubagentType]roleBudget{TypeCoder: {NumPredict: 64, Timeout: time.Second}}}
	budget := runner.roleBudget(TypeCoder)
	if budget.NumPredict != 64 || budget.Timeout != time.Second {
		t.Fatalf("runner budget override was ignored: %#v", budget)
	}
	if runner.roleBudget(TypeReviewer) != defaultRoleBudget(TypeReviewer) {
		t.Fatal("unconfigured role did not use default budget")
	}
}

func TestCanceledContextClassifiesAsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, status, _ := contextTermination(ctx)
	if status != "INTERRUPTED" {
		t.Fatalf("canceled model call was not interrupted: %s", status)
	}
}
