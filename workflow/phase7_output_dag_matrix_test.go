package workflow

import (
	"context"
	"reflect"
	"testing"
)

func phase7OutputDAGDoc(name string, steps []any, outputs map[string]any, maxCalls int) map[string]any {
	return map[string]any{
		"oaw_version": "0.1",
		"name":        name,
		"description": "phase 7 output and DAG matrix",
		"inputs": map[string]any{
			"value": map[string]any{"type": "string", "required": true},
		},
		"limits": map[string]any{
			"max_steps":             len(steps),
			"max_tool_calls":        maxCalls,
			"max_attempts_per_step": 1,
			"timeout_seconds":       5,
		},
		"steps":      steps,
		"outputs":    outputs,
		"on_failure": "stop",
	}
}

func phase7OutputTool(id string, value any) map[string]any {
	return map[string]any{
		"id": id, "kind": "tool", "tool": "echo",
		"arguments": map[string]any{"value": value},
	}
}

func phase7OutputReturn(id string) map[string]any {
	return map[string]any{"id": id, "kind": "return"}
}

func phase7OutputDecision(id, ref, onTrue, onFalse string) map[string]any {
	return map[string]any{
		"id": id, "kind": "decision",
		"condition": map[string]any{"ref": ref, "operator": "eq", "value": "take"},
		"on_true":   onTrue, "on_false": onFalse,
	}
}

func phase7AssertInvalidBeforeExecutor(t *testing.T, doc map[string]any) {
	t.Helper()
	ex := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		t.Fatal("invalid workflow reached executor")
		return "", nil
	}}
	e := setupEngine(t, doc, ex)
	result := e.Run(context.Background(), doc["name"].(string), map[string]any{"value": "x"}, allow)
	if result.Status != "failed" {
		t.Fatalf("expected failed validation, got %#v", result)
	}
	if ex.calls.Load() != 0 {
		t.Fatalf("validation had executor side effects: %d calls", ex.calls.Load())
	}
}

func TestPhase7TopLevelOutputsAreAuthorityAndReturnIsMarker(t *testing.T) {
	ex := &testExecutor{fn: func(_ context.Context, name string, args map[string]any) (string, error) {
		if name != "echo" || args["value"] != "x" {
			t.Fatalf("unexpected executor call name=%q args=%#v", name, args)
		}
		return `{"value":"ok"}`, nil
	}}
	doc := phase7OutputDAGDoc("phase7-output-authority", []any{
		phase7OutputTool("call", "{{inputs.value}}"),
		phase7OutputReturn("done"),
	}, map[string]any{"published": "{{steps.call.result.value}}"}, 1)
	e := setupEngine(t, doc, ex)
	result := e.Run(context.Background(), "phase7-output-authority", map[string]any{"value": "x"}, allow)
	want := map[string]any{"published": "ok"}
	if result.Status != "succeeded" || !reflect.DeepEqual(result.Outputs, want) {
		t.Fatalf("top-level outputs were not authoritative: got %#v want %#v", result.Outputs, want)
	}
	if ex.calls.Load() != 1 {
		t.Fatalf("unexpected executor side effects: %d calls", ex.calls.Load())
	}
}

func TestPhase7ReturnValuesRejected(t *testing.T) {
	doc := phase7OutputDAGDoc("phase7-return-values", []any{
		phase7OutputTool("call", "{{inputs.value}}"),
		map[string]any{"id": "done", "kind": "return", "values": map[string]any{"published": "ok"}},
	}, map[string]any{"published": "{{steps.call.result.value}}"}, 1)
	phase7AssertInvalidBeforeExecutor(t, doc)
}

func TestPhase7LiteralStatusAndAttemptOutputsRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"literal", "literal"},
		{"status", "{{steps.call.status}}"},
		{"attempt", "{{steps.call.attempt}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := phase7OutputDAGDoc("phase7-output-"+tc.name, []any{
				phase7OutputTool("call", "{{inputs.value}}"),
				phase7OutputReturn("done"),
			}, map[string]any{"published": tc.value}, 1)
			phase7AssertInvalidBeforeExecutor(t, doc)
		})
	}
}

func TestPhase7AbsentOptionalWholeOutputIsOmitted(t *testing.T) {
	ex := &testExecutor{fn: func(_ context.Context, _ string, _ map[string]any) (string, error) {
		return `{"value":"ok"}`, nil
	}}
	doc := phase7OutputDAGDoc("phase7-optional-output", []any{
		phase7OutputReturn("done"),
	}, map[string]any{
		"present":  "{{inputs.value}}",
		"optional": "{{inputs.optional}}",
	}, 0)
	doc["inputs"].(map[string]any)["optional"] = map[string]any{"type": "string"}
	e := setupEngine(t, doc, ex)
	result := e.Run(context.Background(), "phase7-optional-output", map[string]any{"value": "x"}, allow)
	if result.Status != "succeeded" {
		t.Fatalf("optional output failed: %#v", result)
	}
	want := map[string]any{"present": "x"}
	if !reflect.DeepEqual(result.Outputs, want) {
		t.Fatalf("optional output mismatch: got %#v want %#v", result.Outputs, want)
	}
}

func TestPhase7InputAndToolResultOutputsAcceptNestedStructures(t *testing.T) {
	ex := &testExecutor{fn: func(_ context.Context, name string, args map[string]any) (string, error) {
		if name != "echo" || args["value"] != "x" {
			t.Fatalf("unexpected executor call name=%q args=%#v", name, args)
		}
		return `{"value":"ok","nested":{"leaf":"deep"}}`, nil
	}}
	doc := phase7OutputDAGDoc("phase7-nested-outputs", []any{
		phase7OutputTool("call", "{{inputs.value}}"),
		phase7OutputReturn("done"),
	}, map[string]any{
		"input": "{{inputs.value}}",
		"result": map[string]any{
			"direct": "{{steps.call.result.value}}",
			"nested": []any{
				"{{steps.call.result.nested.leaf}}",
				map[string]any{"again": "{{steps.call.result.value}}"},
			},
		},
	}, 1)
	e := setupEngine(t, doc, ex)
	result := e.Run(context.Background(), "phase7-nested-outputs", map[string]any{"value": "x"}, allow)
	want := map[string]any{
		"input": "x",
		"result": map[string]any{
			"direct": "ok",
			"nested": []any{"deep", map[string]any{"again": "ok"}},
		},
	}
	if result.Status != "succeeded" || !reflect.DeepEqual(result.Outputs, want) {
		t.Fatalf("nested outputs mismatch: got %#v want %#v", result.Outputs, want)
	}
	if ex.calls.Load() != 1 {
		t.Fatalf("unexpected executor side effects: %d calls", ex.calls.Load())
	}
}

func TestPhase7ConditionalSkippedResultOutputRejected(t *testing.T) {
	doc := phase7OutputDAGDoc("phase7-skipped-result", []any{
		phase7OutputDecision("choose", "{{inputs.value}}", "taken", "fallback"),
		phase7OutputTool("taken", "{{inputs.value}}"),
		phase7OutputReturn("fallback"),
	}, map[string]any{"published": "{{steps.taken.result.value}}"}, 1)
	phase7AssertInvalidBeforeExecutor(t, doc)
}

func TestPhase7MissingFutureSameStepAndNonToolResultRefsRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []any
		outs  map[string]any
	}{
		{
			"missing",
			[]any{phase7OutputTool("call", "{{steps.missing.result.value}}"), phase7OutputReturn("done")},
			map[string]any{"published": "{{inputs.value}}"},
		},
		{
			"future",
			[]any{phase7OutputTool("first", "{{steps.later.result.value}}"), phase7OutputTool("later", "{{inputs.value}}"), phase7OutputReturn("done")},
			map[string]any{"published": "{{inputs.value}}"},
		},
		{
			"same-step",
			[]any{phase7OutputTool("call", "{{steps.call.result.value}}"), phase7OutputReturn("done")},
			map[string]any{"published": "{{inputs.value}}"},
		},
		{
			"non-tool",
			[]any{phase7OutputTool("call", "{{inputs.value}}"), phase7OutputReturn("done")},
			map[string]any{"published": "{{steps.done.result.value}}"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phase7AssertInvalidBeforeExecutor(t, phase7OutputDAGDoc("phase7-ref-"+tc.name, tc.steps, tc.outs, 1))
		})
	}
}

func TestPhase7MissingBackwardSelfAndCyclicDecisionTargetsRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []any
	}{
		{
			"missing",
			[]any{phase7OutputDecision("choose", "{{inputs.value}}", "missing", "done"), phase7OutputReturn("done")},
		},
		{
			"backward",
			[]any{phase7OutputReturn("prior"), phase7OutputDecision("choose", "{{inputs.value}}", "prior", "done"), phase7OutputReturn("done")},
		},
		{
			"self",
			[]any{phase7OutputDecision("choose", "{{inputs.value}}", "choose", "done"), phase7OutputReturn("done")},
		},
		{
			"cyclic",
			[]any{phase7OutputDecision("first", "{{inputs.value}}", "second", "done"), phase7OutputDecision("second", "{{inputs.value}}", "first", "done"), phase7OutputReturn("done")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phase7AssertInvalidBeforeExecutor(t, phase7OutputDAGDoc("phase7-decision-"+tc.name, tc.steps, map[string]any{}, 0))
		})
	}
}

func TestPhase7ReachableNoReturnAndFallthroughRejected(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []any
	}{
		{
			"reachable-no-return",
			[]any{phase7OutputDecision("choose", "{{inputs.value}}", "stop", "stop"), phase7OutputReturn("unreachable")},
		},
		{"fallthrough", []any{phase7OutputTool("call", "{{inputs.value}}")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			phase7AssertInvalidBeforeExecutor(t, phase7OutputDAGDoc("phase7-termination-"+tc.name, tc.steps, map[string]any{}, 1))
		})
	}
}

func TestPhase7StopYieldsFailedWithoutOutputs(t *testing.T) {
	ex := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		t.Fatal("stop branch reached executor")
		return "", nil
	}}
	doc := phase7OutputDAGDoc("phase7-stop", []any{
		phase7OutputDecision("choose", "{{inputs.value}}", "stop", "done"),
		phase7OutputReturn("done"),
	}, map[string]any{"published": "{{inputs.value}}"}, 0)
	e := setupEngine(t, doc, ex)
	result := e.Run(context.Background(), "phase7-stop", map[string]any{"value": "take"}, allow)
	if result.Status != "failed" {
		t.Fatalf("stop must fail the run: %#v", result)
	}
	if result.Outputs != nil {
		t.Fatalf("stop must not produce outputs: %#v", result.Outputs)
	}
	if ex.calls.Load() != 0 {
		t.Fatalf("stop had executor side effects: %d calls", ex.calls.Load())
	}
}

func TestPhase7MutuallyExclusiveMaxPathPreserved(t *testing.T) {
	var selected string
	ex := &testExecutor{fn: func(_ context.Context, name string, args map[string]any) (string, error) {
		selected = name
		if args["value"] != "take" {
			t.Fatalf("unexpected executor args: %#v", args)
		}
		return `{"value":"ok"}`, nil
	}}
	doc := phase7OutputDAGDoc("phase7-max-path", []any{
		phase7OutputDecision("choose", "{{inputs.value}}", "yes", "no"),
		phase7OutputTool("yes", "{{inputs.value}}"),
		phase7OutputReturn("yes-done"),
		phase7OutputTool("no", "{{inputs.value}}"),
		phase7OutputReturn("no-done"),
	}, map[string]any{"published": "{{inputs.value}}"}, 1)
	e := setupEngine(t, doc, ex)
	result := e.Run(context.Background(), "phase7-max-path", map[string]any{"value": "take"}, allow)
	if result.Status != "succeeded" || result.Outputs["published"] != "take" {
		t.Fatalf("selected branch did not succeed: %#v", result)
	}
	if ex.calls.Load() != 1 || selected != "echo" {
		t.Fatalf("expected exactly one selected-branch executor call, calls=%d tool=%q", ex.calls.Load(), selected)
	}
}
