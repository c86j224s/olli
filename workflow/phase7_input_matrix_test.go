package workflow

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

type phase7InputExecutor struct {
	calls   atomic.Int32
	effects atomic.Int32
	last    any
}

func (e *phase7InputExecutor) ExecuteContext(_ context.Context, _ string, args map[string]any) (string, error) {
	e.calls.Add(1)
	e.effects.Add(1)
	e.last = args["value"]
	return `{"value":"ok"}`, nil
}

type phase7InputCatalog []ToolDefinition

func (c phase7InputCatalog) ListTools() []ToolDefinition { return c }

func phase7InputSpec(typ string, extra map[string]any) map[string]any {
	spec := map[string]any{"type": typ}
	for key, value := range extra {
		spec[key] = value
	}
	return spec
}

func phase7InputDocument(name string, spec map[string]any) map[string]any {
	return map[string]any{
		"oaw_version": "0.1",
		"name":        name,
		"description": "phase 7 input validation",
		"inputs":      map[string]any{"value": spec},
		"limits": map[string]any{
			"max_steps":             2,
			"max_tool_calls":        1,
			"max_attempts_per_step": 1,
			"timeout_seconds":       5,
		},
		"steps": []any{
			map[string]any{
				"id":        "call",
				"kind":      "tool",
				"tool":      "phase7_echo",
				"arguments": map[string]any{"value": "{{inputs.value}}"},
			},
			map[string]any{"id": "done", "kind": "return"},
		},
		"outputs":    map[string]any{"result": "{{steps.call.result.value}}"},
		"on_failure": "stop",
	}
}

func phase7InputSetup(t *testing.T, doc map[string]any, executor *phase7InputExecutor) *Engine {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"oaw.schema.json", "oaw-event.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "workflows", "agent", file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "workflows", "agent", file), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	name := doc["name"].(string)
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name+".oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	catalog := phase7InputCatalog{{
		Name:             "phase7_echo",
		WorkflowCallable: true,
		RetrySafe:        true,
		Schema: map[string]any{
			"$schema":              schemaDraft202012,
			"type":                 "object",
			"additionalProperties": false,
			"required":             []any{"value"},
			"properties":           map[string]any{"value": map[string]any{}},
		},
	}}
	engine, err := NewEngine(root, catalog, executor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func phase7InputAllow(context.Context, string, map[string]any, string, int) bool { return true }

func TestPhase7InputValidationMatrix(t *testing.T) {
	type testCase struct {
		name     string
		spec     map[string]any
		supplied map[string]any
		want     any
		valid    bool
	}

	cases := []testCase{
		{name: "string correct type", spec: phase7InputSpec("string", nil), supplied: map[string]any{"value": "hello"}, want: "hello", valid: true},
		{name: "integer correct type", spec: phase7InputSpec("integer", nil), supplied: map[string]any{"value": int64(7)}, want: int64(7), valid: true},
		{name: "number correct type", spec: phase7InputSpec("number", nil), supplied: map[string]any{"value": float64(2.5)}, want: float64(2.5), valid: true},
		{name: "boolean correct type", spec: phase7InputSpec("boolean", nil), supplied: map[string]any{"value": true}, want: true, valid: true},
		{name: "string wrong type", spec: phase7InputSpec("string", nil), supplied: map[string]any{"value": true}},
		{name: "integer wrong type", spec: phase7InputSpec("integer", nil), supplied: map[string]any{"value": "7"}},
		{name: "number wrong type", spec: phase7InputSpec("number", nil), supplied: map[string]any{"value": "2.5"}},
		{name: "boolean wrong type", spec: phase7InputSpec("boolean", nil), supplied: map[string]any{"value": 1}},
		{name: "string enum accepted", spec: phase7InputSpec("string", map[string]any{"enum": []any{"red", "blue"}}), supplied: map[string]any{"value": "blue"}, want: "blue", valid: true},
		{name: "string enum rejected", spec: phase7InputSpec("string", map[string]any{"enum": []any{"red", "blue"}}), supplied: map[string]any{"value": "green"}},
		{name: "integer enum accepted", spec: phase7InputSpec("integer", map[string]any{"enum": []any{1, 2}}), supplied: map[string]any{"value": float64(2)}, want: float64(2), valid: true},
		{name: "integer enum rejected", spec: phase7InputSpec("integer", map[string]any{"enum": []any{1, 2}}), supplied: map[string]any{"value": float64(3)}},
		{name: "number enum accepted", spec: phase7InputSpec("number", map[string]any{"enum": []any{1.5, 2.5}}), supplied: map[string]any{"value": float64(2.5)}, want: float64(2.5), valid: true},
		{name: "number enum rejected", spec: phase7InputSpec("number", map[string]any{"enum": []any{1.5, 2.5}}), supplied: map[string]any{"value": float64(3.5)}},
		{name: "boolean enum accepted", spec: phase7InputSpec("boolean", map[string]any{"enum": []any{true, false}}), supplied: map[string]any{"value": false}, want: false, valid: true},
		{name: "boolean enum rejected", spec: phase7InputSpec("boolean", map[string]any{"enum": []any{true}}), supplied: map[string]any{"value": false}},
		{name: "duplicate enum declaration", spec: phase7InputSpec("string", map[string]any{"enum": []any{"same", "same"}}), supplied: map[string]any{"value": "same"}},
		{name: "minimum inclusive", spec: phase7InputSpec("number", map[string]any{"minimum": 1}), supplied: map[string]any{"value": float64(1)}, want: float64(1), valid: true},
		{name: "maximum inclusive", spec: phase7InputSpec("number", map[string]any{"maximum": 3}), supplied: map[string]any{"value": float64(3)}, want: float64(3), valid: true},
		{name: "minimum rejected", spec: phase7InputSpec("integer", map[string]any{"minimum": 2}), supplied: map[string]any{"value": int(1)}},
		{name: "maximum rejected", spec: phase7InputSpec("integer", map[string]any{"maximum": 2}), supplied: map[string]any{"value": int(3)}},
		{name: "string rune length inclusive", spec: phase7InputSpec("string", map[string]any{"min_length": 2, "max_length": 2}), supplied: map[string]any{"value": "é界"}, want: "é界", valid: true},
		{name: "string rune minimum rejected", spec: phase7InputSpec("string", map[string]any{"min_length": 2}), supplied: map[string]any{"value": "é"}},
		{name: "string rune maximum rejected", spec: phase7InputSpec("string", map[string]any{"max_length": 2}), supplied: map[string]any{"value": "é界a"}},
		{name: "string default applied", spec: phase7InputSpec("string", map[string]any{"default": "default-string"}), supplied: map[string]any{}, want: "default-string", valid: true},
		{name: "integer default applied", spec: phase7InputSpec("integer", map[string]any{"default": 7}), supplied: map[string]any{}, want: json.Number("7"), valid: true},
		{name: "number default applied", spec: phase7InputSpec("number", map[string]any{"default": 2.5}), supplied: map[string]any{}, want: json.Number("2.5"), valid: true},
		{name: "boolean default applied", spec: phase7InputSpec("boolean", map[string]any{"default": true}), supplied: map[string]any{}, want: true, valid: true},
		{name: "required missing", spec: phase7InputSpec("string", map[string]any{"required": true}), supplied: map[string]any{}},
		{name: "unknown supplied input", spec: phase7InputSpec("string", nil), supplied: map[string]any{"other": "unexpected"}},
		{name: "inverted numeric bounds", spec: phase7InputSpec("number", map[string]any{"minimum": 3, "maximum": 2}), supplied: map[string]any{"value": float64(2.5)}},
		{name: "inverted length bounds", spec: phase7InputSpec("string", map[string]any{"min_length": 3, "max_length": 2}), supplied: map[string]any{"value": "abc"}},
		{name: "required default schema rejection", spec: phase7InputSpec("boolean", map[string]any{"required": true, "default": true}), supplied: map[string]any{"value": true}},
		{name: "integral float accepted as integer", spec: phase7InputSpec("integer", nil), supplied: map[string]any{"value": float64(4)}, want: float64(4), valid: true},
		{name: "fractional float rejected as integer", spec: phase7InputSpec("integer", nil), supplied: map[string]any{"value": 4.25}},
		{name: "nan rejected as number", spec: phase7InputSpec("number", nil), supplied: map[string]any{"value": math.NaN()}},
		{name: "positive infinity rejected as number", spec: phase7InputSpec("number", nil), supplied: map[string]any{"value": math.Inf(1)}},
		{name: "negative infinity rejected as integer", spec: phase7InputSpec("integer", nil), supplied: map[string]any{"value": math.Inf(-1)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := "phase7-" + strings.TrimPrefix(strings.ReplaceAll(t.Name(), "/", "-"), "TestPhase7InputValidationMatrix-")
			executor := &phase7InputExecutor{}
			engine := phase7InputSetup(t, phase7InputDocument(name, tc.spec), executor)
			result := engine.Run(context.Background(), name, tc.supplied, phase7InputAllow)
			if got := result.Status == "succeeded"; got != tc.valid {
				t.Fatalf("success = %v, want %v; result = %#v", got, tc.valid, result)
			}
			if !tc.valid {
				if got := executor.calls.Load(); got != 0 {
					t.Fatalf("failed validation made %d executor calls", got)
				}
				if got := executor.effects.Load(); got != 0 {
					t.Fatalf("failed validation caused %d executor side effects", got)
				}
				return
			}
			if got := executor.calls.Load(); got != 1 {
				t.Fatalf("successful validation made %d executor calls, want 1", got)
			}
			if got := executor.effects.Load(); got != 1 {
				t.Fatalf("successful run caused %d executor side effects, want 1", got)
			}
			if !reflect.DeepEqual(reflect.TypeOf(executor.last), reflect.TypeOf(tc.want)) || !reflect.DeepEqual(executor.last, tc.want) {
				t.Fatalf("executor value = %#v (%T), want %#v (%T)", executor.last, executor.last, tc.want, tc.want)
			}
		})
	}
}
