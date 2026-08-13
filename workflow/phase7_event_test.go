package workflow

import (
	"context"
	"testing"
)

func TestEventSchemaBranchMatrix(t *testing.T) {
	engine := setupEngine(t, workflowDoc("event-branches", map[string]any{"value": "{{inputs.value}}"}), &testExecutor{})
	id := "oaw_00000000000000000000000000000041"
	valid := []map[string]any{
		eventBase(id, "event-branches", "workflow_started", "started", 0),
		func() map[string]any {
			e := eventBase(id, "event-branches", "step_started", "started", 1)
			e["step_id"] = "call"
			e["attempt"] = 1
			return e
		}(),
		func() map[string]any {
			e := eventBase(id, "event-branches", "permission_requested", "allowed", 2)
			e["step_id"] = "call"
			e["attempt"] = 1
			e["tool"] = "echo"
			return e
		}(),
		func() map[string]any {
			e := eventBase(id, "event-branches", "tool_completed", "succeeded", 3)
			e["step_id"] = "call"
			e["attempt"] = 1
			e["tool"] = "echo"
			e["outcome_category"] = "success"
			return e
		}(),
		func() map[string]any {
			e := eventBase(id, "event-branches", "step_retried", "retrying", 4)
			e["step_id"] = "call"
			e["attempt"] = 1
			e["tool"] = "echo"
			e["outcome_category"] = "tool_error"
			return e
		}(),
		func() map[string]any {
			e := eventBase(id, "event-branches", "step_failed", "failed", 5)
			e["step_id"] = "call"
			e["attempt"] = 1
			e["outcome_category"] = "tool_error"
			e["summary"] = "failed"
			return e
		}(),
		func() map[string]any {
			e := eventBase(id, "event-branches", "step_skipped", "skipped", 6)
			e["step_id"] = "tail"
			e["attempt"] = 1
			return e
		}(),
		func() map[string]any {
			e := eventBase(id, "event-branches", "workflow_completed", "succeeded", 7)
			e["outcome_category"] = "success"
			return e
		}(),
		func() map[string]any {
			e := eventBase(id, "event-branches", "workflow_cancelled", "cancelled", 8)
			e["outcome_category"] = "cancelled"
			e["summary"] = "cancelled"
			return e
		}(),
	}
	for _, event := range valid {
		if err := engine.eventSchema.Validate(event); err != nil {
			t.Fatalf("valid event rejected %s: %v", event["event"], err)
		}
	}
	for _, event := range valid {
		copy := map[string]any{}
		for key, value := range event {
			copy[key] = value
		}
		copy["status"] = "invalid"
		if err := engine.eventSchema.Validate(copy); err == nil {
			t.Fatalf("invalid status accepted for %s", event["event"])
		}
	}
}

func TestActualRunOutcomeEvents(t *testing.T) {
	cases := []struct {
		name            string
		executor        *testExecutor
		authorize       Authorizer
		status, outcome string
	}{
		{"success", &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}, allow, "succeeded", "success"},
		{"tool-error", &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return "", ToolError{} }}, allow, "failed", "tool_error"},
		{"timeout", &testExecutor{fn: func(ctx context.Context, _ string, _ map[string]any) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}, allow, "timed_out", "timeout"},
		{"denied", &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}, func(context.Context, string, map[string]any, string, int) bool { return false }, "failed", "denied"},
	}
	for _, tc := range cases {
		doc := workflowDoc("outcome-"+tc.name, map[string]any{"value": "{{inputs.value}}"})
		if tc.name == "timeout" {
			doc["limits"].(map[string]any)["timeout_seconds"] = 1
		}
		engine := setupEngine(t, doc, tc.executor)
		result := engine.Run(context.Background(), doc["name"].(string), map[string]any{"value": "x"}, tc.authorize)
		if result.Status != tc.status {
			t.Fatalf("%s status: %#v", tc.name, result)
		}
		events, err := engine.ReadLog(result.RunID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, event := range events {
			if event["outcome_category"] == tc.outcome {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s outcome event missing", tc.name)
		}
	}
}
