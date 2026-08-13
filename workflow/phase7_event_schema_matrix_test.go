package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const phase7EventSchemaResource = "https://olli.local/schemas/oaw-event-v0.1.schema.json"

type phase7EventCase struct {
	name      string
	valid     map[string]any
	required  []string
	forbidden []string
	outcomes  []string
}

func phase7EventBase(event, status string) map[string]any {
	return map[string]any{
		"event_schema_version": "0.1",
		"event":                event,
		"timestamp":            "2026-08-13T00:00:00Z",
		"sequence":             0,
		"run_id":               "oaw_00000000000000000000000000000001",
		"workflow":             "demo_workflow",
		"status":               status,
	}
}

func phase7EventWith(base map[string]any, fields map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(fields))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func phase7EventSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "workflows", "agent", "oaw-event.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read canonical event schema %s: %v", path, err)
	}
	document, err := decodeJSONDocument(data)
	if err != nil {
		t.Fatalf("decode canonical event schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(phase7EventSchemaResource, document); err != nil {
		t.Fatalf("add canonical event schema resource: %v", err)
	}
	schema, err := compiler.Compile(phase7EventSchemaResource)
	if err != nil {
		t.Fatalf("compile canonical event schema: %v", err)
	}
	return schema
}

func phase7StepEvent(base map[string]any, event, status string) map[string]any {
	return phase7EventWith(phase7EventWith(base, map[string]any{
		"event":  event,
		"status": status,
	}), map[string]any{
		"step_id": "call",
		"attempt": 1,
	})
}

func phase7ToolEvent(base map[string]any, event, status, outcome string, summary bool) map[string]any {
	fields := map[string]any{
		"event":            event,
		"status":           status,
		"step_id":          "call",
		"attempt":          1,
		"tool":             "echo",
		"outcome_category": outcome,
	}
	if summary {
		fields["summary"] = "tool result"
	}
	return phase7EventWith(base, fields)
}

func phase7SchemaCases() []phase7EventCase {
	common := []string{"event_schema_version", "event", "timestamp", "sequence", "run_id", "workflow", "status"}
	stepRequired := append(append([]string{}, common...), "step_id", "attempt")
	permissionRequired := append(append([]string{}, stepRequired...), "tool")
	toolOutcomeRequired := append(append([]string{}, permissionRequired...), "outcome_category")
	failedToolRequired := append(append([]string{}, toolOutcomeRequired...), "summary")
	failedToolRequired[len(failedToolRequired)-1], failedToolRequired[len(failedToolRequired)-2] = failedToolRequired[len(failedToolRequired)-2], failedToolRequired[len(failedToolRequired)-1]

	return []phase7EventCase{
		{
			name:      "workflow_started",
			valid:     phase7EventBase("workflow_started", "started"),
			required:  common,
			forbidden: []string{"step_id", "attempt", "tool", "summary", "outcome_category", "unexpected"},
			outcomes:  []string{"success", "tool_error"},
		},
		{
			name:      "step_started",
			valid:     phase7StepEvent(phase7EventBase("step_started", "started"), "step_started", "started"),
			required:  stepRequired,
			forbidden: []string{"tool", "summary", "outcome_category", "unexpected"},
			outcomes:  []string{"success", "tool_error"},
		},
		{
			name: "permission_requested",
			valid: phase7EventWith(phase7StepEvent(phase7EventBase("permission_requested", "allowed"), "permission_requested", "allowed"), map[string]any{
				"tool": "echo",
			}),
			required:  permissionRequired,
			forbidden: []string{"summary", "outcome_category", "unexpected"},
			outcomes:  []string{"success", "tool_error"},
		},
		{
			name:      "tool_completed_succeeded",
			valid:     phase7ToolEvent(phase7EventBase("tool_completed", "succeeded"), "tool_completed", "succeeded", "success", false),
			required:  toolOutcomeRequired,
			forbidden: []string{"summary", "unexpected"},
			outcomes:  []string{"tool_error", "timeout", "cancelled"},
		},
		{
			name:      "tool_completed_failed",
			valid:     phase7ToolEvent(phase7EventBase("tool_completed", "failed"), "tool_completed", "failed", "tool_error", true),
			required:  failedToolRequired,
			forbidden: []string{"unexpected"},
			outcomes:  []string{"success", "timeout", "cancelled"},
		},
		{
			name:      "tool_completed_cancelled",
			valid:     phase7ToolEvent(phase7EventBase("tool_completed", "cancelled"), "tool_completed", "cancelled", "cancelled", true),
			required:  failedToolRequired,
			forbidden: []string{"unexpected"},
			outcomes:  []string{"success", "tool_error", "timeout"},
		},
		{
			name:      "tool_completed_timed_out",
			valid:     phase7ToolEvent(phase7EventBase("tool_completed", "timed_out"), "tool_completed", "timed_out", "timeout", true),
			required:  failedToolRequired,
			forbidden: []string{"unexpected"},
			outcomes:  []string{"success", "tool_error", "cancelled"},
		},
		{
			name: "step_retried_tool_error",
			valid: phase7EventWith(phase7StepEvent(phase7EventBase("step_retried", "retrying"), "step_retried", "retrying"), map[string]any{
				"tool": "echo", "outcome_category": "tool_error",
			}),
			required:  append(append(append([]string{}, stepRequired...), "tool"), "outcome_category"),
			forbidden: []string{"summary", "unexpected"},
			outcomes:  []string{"cancelled", "success"},
		},
		{
			name: "step_retried_timeout",
			valid: phase7EventWith(phase7StepEvent(phase7EventBase("step_retried", "retrying"), "step_retried", "retrying"), map[string]any{
				"tool": "echo", "outcome_category": "timeout",
			}),
			required:  append(append(append([]string{}, stepRequired...), "tool"), "outcome_category"),
			forbidden: []string{"summary", "unexpected"},
			outcomes:  []string{"cancelled", "success"},
		},
	}
}

func phase7RemainingSchemaCases() []phase7EventCase {
	common := []string{"event_schema_version", "event", "timestamp", "sequence", "run_id", "workflow", "status"}
	failedStepRequired := append(append(append(append([]string{}, common...), "step_id", "attempt"), "summary"), "outcome_category")
	stepRequired := append(append([]string{}, common...), "step_id", "attempt")
	workflowOutcomeRequired := append(append([]string{}, common...), "outcome_category")
	workflowFailureRequired := append(append(append([]string{}, common...), "summary"), "outcome_category")
	cases := []phase7EventCase{
		{
			name:      "step_skipped",
			valid:     phase7StepEvent(phase7EventBase("step_skipped", "skipped"), "step_skipped", "skipped"),
			required:  stepRequired,
			forbidden: []string{"tool", "summary", "outcome_category", "unexpected"},
			outcomes:  []string{"success", "tool_error"},
		},
	}
	for _, category := range []string{"tool_error", "timeout", "denied", "validation"} {
		cases = append(cases, phase7EventCase{
			name: "step_failed_" + category,
			valid: phase7EventWith(phase7StepEvent(phase7EventBase("step_failed", "failed"), "step_failed", "failed"), map[string]any{
				"summary": "step failed", "outcome_category": category,
			}),
			required:  failedStepRequired,
			forbidden: []string{"tool", "unexpected"},
			outcomes:  []string{"success", "cancelled"},
		})
	}
	cases = append(cases, phase7EventCase{
		name:      "workflow_completed_succeeded",
		valid:     phase7EventWith(phase7EventBase("workflow_completed", "succeeded"), map[string]any{"outcome_category": "success"}),
		required:  workflowOutcomeRequired,
		forbidden: []string{"step_id", "attempt", "tool", "summary", "unexpected"},
		outcomes:  []string{"tool_error", "cancelled"},
	})
	for _, category := range []string{"tool_error", "timeout", "denied", "log_limit", "validation"} {
		cases = append(cases, phase7EventCase{
			name: "workflow_completed_failed_" + category,
			valid: phase7EventWith(phase7EventBase("workflow_completed", "failed"), map[string]any{
				"summary": "workflow failed", "outcome_category": category,
			}),
			required:  workflowFailureRequired,
			forbidden: []string{"step_id", "attempt", "tool", "unexpected"},
			outcomes:  []string{"success", "cancelled"},
		})
	}
	cases = append(cases, phase7EventCase{
		name: "workflow_completed_timed_out",
		valid: phase7EventWith(phase7EventBase("workflow_completed", "timed_out"), map[string]any{
			"summary": "workflow timed out", "outcome_category": "timeout",
		}),
		required:  workflowFailureRequired,
		forbidden: []string{"step_id", "attempt", "tool", "unexpected"},
		outcomes:  []string{"success", "tool_error"},
	})
	cases = append(cases, phase7EventCase{
		name: "workflow_cancelled",
		valid: phase7EventWith(phase7EventBase("workflow_cancelled", "cancelled"), map[string]any{
			"summary": "workflow cancelled", "outcome_category": "cancelled",
		}),
		required:  workflowFailureRequired,
		forbidden: []string{"step_id", "attempt", "tool", "unexpected"},
		outcomes:  []string{"success", "timeout"},
	})
	return cases
}

func phase7InvalidValue(field string) any {
	switch field {
	case "event_schema_version":
		return "0.2"
	case "event":
		return "workflow_started"
	case "timestamp":
		return "not-a-date"
	case "sequence":
		return -1
	case "run_id":
		return "oaw_0000000000000000000000000000000G"
	case "workflow":
		return "Demo_Workflow"
	case "status":
		return "invalid"
	case "step_id":
		return "Bad Step"
	case "attempt":
		return 0
	case "tool":
		return "Bad Tool"
	case "summary":
		return "unexpected summary"
	case "outcome_category":
		return "not-a-category"
	default:
		return true
	}
}

func phase7RejectEvent(t *testing.T, schema interface{ Validate(any) error }, label string, event map[string]any) {
	t.Helper()
	if err := schema.Validate(event); err == nil {
		t.Errorf("%s: invalid event accepted: %#v", label, event)
	}
}

func phase7MismatchedEvent(event string) string {
	if event == "workflow_started" {
		return "step_started"
	}
	return "workflow_started"
}

func TestPhase7EventSchemaMatrix(t *testing.T) {
	schema := phase7EventSchema(t)
	cases := append(phase7SchemaCases(), phase7RemainingSchemaCases()...)

	branches := map[string]bool{}
	for _, testCase := range cases {
		branches[testCase.valid["event"].(string)] = true
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			if err := schema.Validate(testCase.valid); err != nil {
				t.Fatalf("minimal valid event rejected: %v\nevent=%#v", err, testCase.valid)
			}

			for _, field := range testCase.required {
				invalid := phase7EventWith(testCase.valid, nil)
				delete(invalid, field)
				phase7RejectEvent(t, schema, "remove required "+field, invalid)
			}
			for _, field := range testCase.forbidden {
				invalid := phase7EventWith(testCase.valid, map[string]any{field: phase7InvalidValue(field)})
				phase7RejectEvent(t, schema, "add forbidden "+field, invalid)
			}

			phase7RejectEvent(t, schema, "invalid status", phase7EventWith(testCase.valid, map[string]any{"status": "invalid"}))
			phase7RejectEvent(t, schema, "event discriminator mismatch", phase7EventWith(testCase.valid, map[string]any{
				"event": phase7MismatchedEvent(testCase.valid["event"].(string)),
			}))
			for _, outcome := range testCase.outcomes {
				phase7RejectEvent(t, schema, fmt.Sprintf("invalid or mismatched outcome %q", outcome), phase7EventWith(testCase.valid, map[string]any{
					"outcome_category": outcome,
				}))
			}
		})
	}
	if len(branches) != 9 {
		t.Fatalf("expected nine event branches, got %d (%v)", len(branches), branches)
	}
}

func TestPhase7CanonicalEventSchemaPatterns(t *testing.T) {
	schema := phase7EventSchema(t)
	valid := phase7EventBase("workflow_started", "started")
	if err := schema.Validate(valid); err != nil {
		t.Fatalf("canonical event rejected: %v", err)
	}
	for _, test := range []struct {
		name  string
		field string
		value any
	}{
		{name: "event_schema_version", field: "event_schema_version", value: "0.2"},
		{name: "run_id", field: "run_id", value: "oaw_0000000000000000000000000000000G"},
		{name: "workflow", field: "workflow", value: "Demo_Workflow"},
	} {
		t.Run(test.name, func(t *testing.T) {
			phase7RejectEvent(t, schema, "non-canonical "+test.field, phase7EventWith(valid, map[string]any{test.field: test.value}))
		})
	}
}
