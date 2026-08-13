package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const phase7CanonicalDraft = "https://json-schema.org/draft/2020-12/schema"

func phase7CopyCanonicalSchemas(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"oaw.schema.json", "oaw-event.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "workflows", "agent", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func phase7SchemaRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	phase7CopyCanonicalSchemas(t, root)
	return root
}

func phase7LoaderEngine(t *testing.T, doc map[string]any) (*Engine, *testExecutor) {
	t.Helper()
	root := phase7SchemaRoot(t)
	if doc != nil {
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		name := doc["name"].(string)
		if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name+".oaw.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return `{"value":"ok"}`, nil
	}}
	engine, err := NewEngine(root, testCatalog{{Name: "echo", WorkflowCallable: true, RetrySafe: true, Schema: map[string]any{
		"$schema": phase7CanonicalDraft,
		"type":    "object", "additionalProperties": false,
		"required":   []any{"value"},
		"properties": map[string]any{"value": map[string]any{"type": "string"}},
	}}}, executor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, executor
}

func phase7WriteSchemaMutation(t *testing.T, root, name string, mutate func(map[string]any)) {
	t.Helper()
	path := filepath.Join(root, "workflows", "agent", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeJSONDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema %s is not an object", name)
	}
	mutate(doc)
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func phase7NewSchemaEngine(t *testing.T, mutate func(string, map[string]any)) error {
	t.Helper()
	root := phase7SchemaRoot(t)
	if mutate != nil {
		for _, name := range []string{"oaw.schema.json", "oaw-event.schema.json"} {
			data, err := os.ReadFile(filepath.Join(root, "workflows", "agent", name))
			if err != nil {
				t.Fatal(err)
			}
			value, err := decodeJSONDocument(data)
			if err != nil {
				t.Fatal(err)
			}
			doc := value.(map[string]any)
			mutate(name, doc)
			data, err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	engine, err := NewEngine(root, testCatalog{}, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return "", nil
	}})
	if engine != nil {
		_ = engine.Close()
	}
	return err
}

func TestPhase7EventSchemaRejectsInvalidUTF8(t *testing.T) {
	root := phase7SchemaRoot(t)
	path := filepath.Join(root, "workflows", "agent", "oaw-event.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, 0xff)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := func() error {
		engine, err := NewEngine(root, testCatalog{}, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return "", nil }})
		if engine != nil {
			defer engine.Close()
		}
		return err
	}(); err == nil {
		t.Fatal("accepted event schema containing invalid UTF-8")
	}
}

func TestPhase7CanonicalSchemaDraftAndIDs(t *testing.T) {
	if err := phase7NewSchemaEngine(t, nil); err != nil {
		t.Fatalf("canonical schemas rejected: %v", err)
	}
	for _, tc := range []struct {
		name, file, key, value string
	}{
		{"workflow wrong draft", "oaw.schema.json", "$schema", "https://json-schema.org/draft/2019-09/schema"},
		{"workflow missing draft", "oaw.schema.json", "$schema", ""},
		{"workflow wrong id", "oaw.schema.json", "$id", "https://example.invalid/workflow"},
		{"workflow missing id", "oaw.schema.json", "$id", ""},
		{"event wrong draft", "oaw-event.schema.json", "$schema", "https://json-schema.org/draft/2019-09/schema"},
		{"event missing draft", "oaw-event.schema.json", "$schema", ""},
		{"event wrong id", "oaw-event.schema.json", "$id", "https://example.invalid/event"},
		{"event missing id", "oaw-event.schema.json", "$id", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := phase7NewSchemaEngine(t, func(file string, doc map[string]any) {
				if file != tc.file {
					return
				}
				if tc.value == "" {
					delete(doc, tc.key)
				} else {
					doc[tc.key] = tc.value
				}
			})
			if err == nil {
				t.Fatalf("accepted non-canonical %s %s", tc.file, tc.key)
			}
		})
	}
}

func TestPhase7SchemaRejectsExternalRefs(t *testing.T) {
	for _, file := range []string{"oaw.schema.json", "oaw-event.schema.json"} {
		t.Run(file, func(t *testing.T) {
			err := phase7NewSchemaEngine(t, func(current string, doc map[string]any) {
				if current != file {
					return
				}
				defs, ok := doc["$defs"].(map[string]any)
				if !ok {
					defs = map[string]any{}
					doc["$defs"] = defs
				}
				defs["external"] = map[string]any{"$ref": "https://example.invalid/schema.json"}
			})
			if err == nil || !strings.Contains(err.Error(), "external schema reference") {
				t.Fatalf("external ref was accepted: %v", err)
			}
		})
	}
}

func TestPhase7LoaderRejectsUTF8SizeTrailingAndDuplicateKeys(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"invalid utf8", []byte{'{', '"', 'n', 'a', 'm', 'e', '"', ':', '"', 0xff, '"', '}'}},
		{"oversized", append([]byte{'{'}, make([]byte, maxWorkflowFile)...)},
		{"trailing json", []byte(`{"name":"loader-trailing"} {}`)},
		{"duplicate keys", []byte(`{"name":"loader-duplicate","name":"other"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, _ := phase7LoaderEngine(t, workflowDoc("loader-"+strings.ReplaceAll(tc.name, " ", "-"), map[string]any{"value": "x"}))
			name := engineNameFromLoaderCase(tc.name)
			path := filepath.Join(engine.Root(), "workflows", "agent", name+".oaw.json")
			if tc.name == "oversized" {
				path = filepath.Join(engine.Root(), "workflows", "agent", "loader-oversized.oaw.json")
			}
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Load(name); err == nil {
				t.Fatalf("accepted %s workflow", tc.name)
			}
		})
	}
}

func engineNameFromLoaderCase(name string) string {
	return "loader-" + strings.ReplaceAll(name, " ", "-")
}

func TestPhase7FilenameMismatchAndListFiltering(t *testing.T) {
	root := phase7SchemaRoot(t)
	doc, err := json.Marshal(workflowDoc("actual-name", map[string]any{"value": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zeta.oaw.json", "alpha.oaw.json", "UPPER.oaw.json", ".hidden.oaw.json", "plain.txt", "suffix.oaw.json.bak"} {
		if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name), doc, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "workflows", "agent", "directory.oaw.json"), 0700); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(root, testCatalog{}, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Load("zeta"); err == nil {
		t.Fatal("filename/name mismatch accepted")
	}
	names, err := engine.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "zeta"}
	if len(names) != len(want) || !equalStringSlices(names, want) || !sort.StringsAreSorted(names) {
		t.Fatalf("unexpected filtered list: %v", names)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPhase7LoadRejectsFIFOWithoutBlocking(t *testing.T) {
	root := phase7SchemaRoot(t)
	name := "loader-fifo"
	path := filepath.Join(root, "workflows", "agent", name+".oaw.json")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(root, testCatalog{}, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	result := make(chan error, 1)
	go func() {
		_, err := engine.Load(name)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("FIFO accepted as workflow file")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Load blocked on FIFO")
	}
}

func TestPhase7ListRejectsSymlinkEntry(t *testing.T) {
	root := phase7SchemaRoot(t)
	regular := filepath.Join(root, "workflows", "agent", "visible.oaw.json")
	if err := os.WriteFile(regular, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(regular, filepath.Join(root, "workflows", "agent", "linked.oaw.json")); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(root, testCatalog{}, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.List(); err == nil {
		t.Fatal("symlink entry was accepted")
	}
}

func phase7AssertPreflightFailure(t *testing.T, engine *Engine, executor *testExecutor, name string) {
	t.Helper()
	result := engine.Run(context.Background(), name, map[string]any{"value": "x"}, allow)
	if result.Status != "failed" || executor.calls.Load() != 0 {
		t.Fatalf("preflight failure reached tool: %#v calls=%d", result, executor.calls.Load())
	}
	if !runIDPattern.MatchString(result.RunID) || result.LogPath == "" || result.LogUnavailable {
		t.Fatalf("preflight failure did not finalize a log: %#v", result)
	}
	events, err := engine.ReadLog(result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0]["event"] != "workflow_started" || events[1]["event"] != "workflow_completed" || events[1]["outcome_category"] != "validation" {
		t.Fatalf("unexpected validation log: %#v", events)
	}
}

func TestPhase7UnknownFieldsRejectAndRunPreflightLogs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"top", func(doc map[string]any) { doc["unknown"] = true }},
		{"input", func(doc map[string]any) { doc["inputs"].(map[string]any)["value"].(map[string]any)["unknown"] = true }},
		{"limits", func(doc map[string]any) { doc["limits"].(map[string]any)["unknown"] = true }},
		{"tool", func(doc map[string]any) { doc["steps"].([]any)[0].(map[string]any)["unknown"] = true }},
		{"retry", func(doc map[string]any) {
			doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 1, "when": []any{"tool_error"}, "unknown": true}
		}},
		{"decision-condition", func(doc map[string]any) {
			doc["limits"].(map[string]any)["max_steps"] = 3
			doc["steps"] = []any{
				map[string]any{"id": "call", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "x"}},
				map[string]any{"id": "gate", "kind": "decision", "condition": map[string]any{"ref": "{{steps.call.status}}", "operator": "exists", "unknown": true}, "on_true": "done", "on_false": "done"},
				map[string]any{"id": "done", "kind": "return"},
			}
		}},
		{"return", func(doc map[string]any) { doc["steps"].([]any)[1].(map[string]any)["unknown"] = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := workflowDoc("unknown-"+tc.name, map[string]any{"value": "{{inputs.value}}"})
			target, executor := phase7LoaderEngine(t, doc)
			tc.mutate(doc)
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target.Root(), "workflows", "agent", doc["name"].(string)+".oaw.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := target.Validate(doc["name"].(string)); err == nil {
				t.Fatal("unknown field accepted")
			}
			phase7AssertPreflightFailure(t, target, executor, doc["name"].(string))
		})
	}
}

func TestPhase7ProtocolVersionAndStepIDsRejectWithPreflightLog(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"protocol-version", func(doc map[string]any) { doc["oaw_version"] = "0.2" }},
		{"duplicate-step-id", func(doc map[string]any) { doc["steps"].([]any)[1].(map[string]any)["id"] = "call" }},
		{"invalid-step-id", func(doc map[string]any) { doc["steps"].([]any)[0].(map[string]any)["id"] = "Bad.ID" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := workflowDoc("ids-"+tc.name, map[string]any{"value": "{{inputs.value}}"})
			tc.mutate(doc)
			engine, executor := phase7LoaderEngine(t, doc)
			if err := engine.Validate(doc["name"].(string)); err == nil {
				t.Fatal("invalid protocol or step ID accepted")
			}
			phase7AssertPreflightFailure(t, engine, executor, doc["name"].(string))
		})
	}
}

func TestPhase7LimitsMinMaxRejectWithPreflightLog(t *testing.T) {
	cases := []struct {
		field string
		low   int
		high  int
	}{
		{"max_steps", 0, 65},
		{"max_tool_calls", -1, 65},
		{"max_attempts_per_step", 0, 4},
		{"timeout_seconds", 0, 3601},
	}
	for _, tc := range cases {
		for _, value := range []int{tc.low, tc.high} {
			t.Run(tc.field+"-"+strconv.Itoa(value), func(t *testing.T) {
				name := "limits-" + strings.ReplaceAll(tc.field, "_", "-") + "-" + strconv.Itoa(value)
				doc := workflowDoc(name, map[string]any{"value": "{{inputs.value}}"})
				doc["limits"].(map[string]any)[tc.field] = value
				engine, executor := phase7LoaderEngine(t, doc)
				if err := engine.Validate(name); err == nil {
					t.Fatalf("accepted out-of-range %s=%d", tc.field, value)
				}
				phase7AssertPreflightFailure(t, engine, executor, name)
			})
		}
	}
}
