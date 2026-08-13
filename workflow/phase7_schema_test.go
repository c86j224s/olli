package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupStrictEngine(t *testing.T, doc map[string]any) *Engine {
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
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", doc["name"].(string)+".oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(root, testCatalog{{Name: "echo", WorkflowCallable: true, RetrySafe: true, Schema: map[string]any{"$schema": schemaDraft202012, "type": "object", "additionalProperties": false, "required": []any{"value"}, "properties": map[string]any{"value": map[string]any{"type": "string"}}}}}, &testExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func writeSchemaCopy(t *testing.T, root, relative string, mutate func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "workflows", "agent", relative))
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeJSONDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	object := value.(map[string]any)
	mutate(object)
	data, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", relative), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalSchemaDraftAndIDs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	for _, tc := range []struct{ file, key, value string }{
		{"oaw.schema.json", "$schema", "https://json-schema.org/draft/2019-09/schema"},
		{"oaw.schema.json", "$id", "https://example.invalid/schema"},
		{"oaw-event.schema.json", "$schema", "https://json-schema.org/draft/2019-09/schema"},
		{"oaw-event.schema.json", "$id", "https://example.invalid/event"},
	} {
		writeSchemaCopy(t, root, tc.file, func(object map[string]any) { object[tc.key] = tc.value })
		if _, err := NewEngine(root, testCatalog{}, &testExecutor{}); err == nil {
			t.Fatalf("accepted non-canonical %s %s", tc.file, tc.key)
		}
		testSchema(t, root)
	}
}

func TestFilenameListAndWorkflowNameMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	doc := workflowDoc("actual", map[string]any{"value": "{{inputs.value}}"})
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "wrong.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "UPPER.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "plain.txt"), data, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(root, testCatalog{}, &testExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Load("wrong"); err == nil {
		t.Fatal("filename/name mismatch accepted")
	}
	names, err := engine.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "wrong" {
		t.Fatalf("filename filtering mismatch: %v", names)
	}
}

func TestSchemaUnknownFieldsAtRepresentativeLevels(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		mutate     func(map[string]any)
	}{
		{"top", "top", func(doc map[string]any) { doc["unknown"] = true }},
		{"input", "inputs.value", func(doc map[string]any) { doc["inputs"].(map[string]any)["value"].(map[string]any)["unknown"] = true }},
		{"limits", "limits", func(doc map[string]any) { doc["limits"].(map[string]any)["unknown"] = true }},
		{"step", "steps[0]", func(doc map[string]any) { doc["steps"].([]any)[0].(map[string]any)["unknown"] = true }},
		{"retry", "steps[0].retry", func(doc map[string]any) {
			doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 1, "when": []any{"tool_error"}, "unknown": true}
		}},
	} {
		doc := workflowDoc("unknown-"+tc.name, map[string]any{"value": "{{inputs.value}}"})
		tc.mutate(doc)
		engine := setupStrictEngine(t, doc)
		if err := engine.Validate(doc["name"].(string)); err == nil {
			t.Fatalf("unknown field %s accepted", tc.path)
		}
	}
}

func TestReferenceValidationMissingFutureAndCycle(t *testing.T) {
	cases := []struct{ name, ref string }{
		{"missing", "{{steps.nope.result.value}}"},
		{"future", "{{steps.done.result.value}}"},
		{"cycle", "{{steps.gate.result.value}}"},
	}
	for _, tc := range cases {
		doc := workflowDoc("ref-"+tc.name, map[string]any{"value": tc.ref})
		if tc.name == "cycle" {
			doc["steps"].([]any)[0].(map[string]any)["arguments"] = map[string]any{"value": "{{steps.gate.result.value}}"}
			doc["steps"].([]any)[1] = map[string]any{"id": "gate", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{steps.call.result.value}}"}}
		}
		engine := setupEngine(t, doc, &testExecutor{})
		if err := engine.Validate(doc["name"].(string)); err == nil {
			t.Fatalf("%s reference accepted", tc.name)
		}
	}
}

func TestInputTypeEnumBoundsAndDefaults(t *testing.T) {
	doc := workflowDoc("input-matrix", map[string]any{"value": "{{inputs.value}}"})
	inputs := doc["inputs"].(map[string]any)
	inputs["value"] = map[string]any{"type": "string", "required": false, "default": "default", "enum": []any{"default", "other"}, "min_length": 3, "max_length": 8}
	engine := setupEngine(t, doc, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }})
	for _, supplied := range []map[string]any{{}, {"value": 1}, {"value": "no"}, {"value": "not-allowed"}} {
		result := engine.Run(context.Background(), "input-matrix", supplied, allow)
		if supplied["value"] == nil && result.Status != "succeeded" {
			t.Fatalf("default rejected: %#v", result)
		}
		if supplied["value"] != nil && result.Status == "succeeded" {
			t.Fatalf("invalid input accepted: %#v", supplied)
		}
	}
}

func TestToolUnknownAndResolvedWrongArgumentAndCatalog(t *testing.T) {
	doc := workflowDoc("tool-matrix", map[string]any{"value": "{{inputs.value}}", "extra": "x"})
	engine := setupEngine(t, doc, &testExecutor{})
	if err := engine.Validate("tool-matrix"); err == nil || !strings.Contains(err.Error(), "unknown argument") {
		t.Fatal("unknown tool argument accepted")
	}
	doc = workflowDoc("resolved-wrong", map[string]any{"value": "{{inputs.value}}"})
	doc["inputs"].(map[string]any)["value"].(map[string]any)["type"] = "integer"
	engine = setupEngine(t, doc, &testExecutor{})
	if result := engine.Run(context.Background(), "resolved-wrong", map[string]any{"value": 4}, allow); result.Status == "succeeded" {
		t.Fatal("resolved wrong tool argument accepted")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	data, _ := json.Marshal(workflowDoc("unregistered", map[string]any{"value": "x"}))
	_ = os.WriteFile(filepath.Join(root, "workflows", "agent", "unregistered.oaw.json"), data, 0600)
	if _, err := NewEngine(root, testCatalog{}, &testExecutor{}); err != nil {
		t.Fatal(err)
	}
	_, err := NewEngine(root, testCatalog{{Name: "echo", WorkflowCallable: true, Schema: map[string]any{"type": "object"}}, {Name: "echo", WorkflowCallable: true, Schema: map[string]any{"type": "object"}}}, &testExecutor{})
	if err == nil {
		t.Fatal("duplicate catalog registration accepted")
	}
}

func TestDecisionBackwardMissingNoReturnAndReturnValues(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"backward", func(doc map[string]any) {
			doc["steps"].([]any)[0] = map[string]any{"id": "gate", "kind": "decision", "condition": map[string]any{"ref": "{{inputs.value}}", "operator": "exists"}, "on_true": "gate", "on_false": "done"}
		}},
		{"missing-target", func(doc map[string]any) {
			doc["steps"].([]any)[0] = map[string]any{"id": "gate", "kind": "decision", "condition": map[string]any{"ref": "{{inputs.value}}", "operator": "exists"}, "on_true": "missing", "on_false": "done"}
		}},
		{"no-return", func(doc map[string]any) {
			doc["steps"] = []any{map[string]any{"id": "call", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{inputs.value}}"}}}
		}},
		{"return-values", func(doc map[string]any) {
			doc["steps"].([]any)[1].(map[string]any)["values"] = map[string]any{"result": "{{inputs.value}}"}
		}},
	}
	for _, tc := range cases {
		doc := workflowDoc("dag-"+tc.name, map[string]any{"value": "{{inputs.value}}"})
		tc.mutate(doc)
		engine := setupEngine(t, doc, &testExecutor{})
		if err := engine.Validate(doc["name"].(string)); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
}

func TestOnFailureReportTerminalIsNonSuccess(t *testing.T) {
	doc := workflowDoc("report-terminal", map[string]any{"value": "{{inputs.value}}"})
	doc["on_failure"] = "report"
	engine := setupEngine(t, doc, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return "", ToolError{Err: os.ErrPermission}
	}})
	result := engine.Run(context.Background(), "report-terminal", map[string]any{"value": "x"}, allow)
	if result.Status == "succeeded" || result.Failure == nil {
		t.Fatalf("report terminal status invalid: %#v", result)
	}
	events, err := engine.ReadLog(result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last["event"] != "workflow_completed" || last["status"] == "succeeded" {
		t.Fatalf("terminal event invalid: %#v", last)
	}
}
