package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testCatalog []ToolDefinition

func (c testCatalog) ListTools() []ToolDefinition { return c }

type testExecutor struct {
	calls atomic.Int32
	fn    func(context.Context, string, map[string]any) (string, error)
}

func (e *testExecutor) ExecuteContext(ctx context.Context, name string, args map[string]any) (string, error) {
	e.calls.Add(1)
	return e.fn(ctx, name, args)
}

func testSchema(t *testing.T, root string) {
	t.Helper()
	b := []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://olli.local/schemas/oaw.schema.json","type":"object","additionalProperties":false,"required":["oaw_version","name","description","inputs","limits","steps","outputs","on_failure"],"properties":{"oaw_version":{"const":"0.1"},"name":{"type":"string","pattern":"^[a-z][a-z0-9_-]{0,63}$"},"description":{"type":"string"},"inputs":{"type":"object","additionalProperties":{"type":"object","additionalProperties":false,"required":["type"],"properties":{"type":{"enum":["string","integer","number","boolean"]},"required":{"type":"boolean"},"default":{},"enum":{"type":"array"},"minimum":{"type":"number"},"maximum":{"type":"number"},"min_length":{"type":"integer"},"max_length":{"type":"integer"}}}},"limits":{"type":"object","additionalProperties":false,"required":["max_steps","max_tool_calls","max_attempts_per_step","timeout_seconds"],"properties":{"max_steps":{"type":"integer"},"max_tool_calls":{"type":"integer"},"max_attempts_per_step":{"type":"integer"},"timeout_seconds":{"type":"integer"}}},"steps":{"type":"array","minItems":1,"items":{"type":"object","additionalProperties":false,"required":["id","kind"],"properties":{"id":{"type":"string"},"kind":{"enum":["tool","decision","return"]},"tool":{"type":"string"},"arguments":{"type":"object"},"retry":{"type":"object"},"condition":{"type":"object"},"on_true":{"type":"string"},"on_false":{"type":"string"}}}},"outputs":{"type":"object"},"on_failure":{"enum":["stop","report"]}}}`)
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "oaw.schema.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	event, err := os.ReadFile(filepath.Join("..", "workflows", "agent", "oaw-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "oaw-event.schema.json"), event, 0600); err != nil {
		t.Fatal(err)
	}
}
func workflowDoc(name string, args map[string]any) map[string]any {
	return map[string]any{"oaw_version": "0.1", "name": name, "description": "test", "inputs": map[string]any{"value": map[string]any{"type": "string", "required": true}}, "limits": map[string]any{"max_steps": 2, "max_tool_calls": 1, "max_attempts_per_step": 1, "timeout_seconds": 5}, "steps": []any{map[string]any{"id": "call", "kind": "tool", "tool": "echo", "arguments": args}, map[string]any{"id": "done", "kind": "return"}}, "outputs": map[string]any{"result": "{{steps.call.result.value}}"}, "on_failure": "stop"}
}
func setupEngine(t *testing.T, doc map[string]any, ex *testExecutor) *Engine {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", doc["name"].(string)+".oaw.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(root, testCatalog{{Name: "echo", WorkflowCallable: true, RetrySafe: true, Schema: map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "additionalProperties": false, "required": []any{"value"}, "properties": map[string]any{"value": map[string]any{"type": "string"}}}}}, ex)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e
}
func allow(context.Context, string, map[string]any, string, int) bool { return true }

func TestWorkflowLoaderRejectsDuplicateObjectKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	data := []byte(`{"oaw_version":"0.1","name":"duplicate-key","name":"other"}`)
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "duplicate-key.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(root, testCatalog{}, &testExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if _, err := engine.Load("duplicate-key"); err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
		t.Fatalf("duplicate workflow key was accepted: %v", err)
	}
}

func TestValidationRejectsStaticallyMissingRequiredToolArgument(t *testing.T) {
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, workflowDoc("missing-required-argument", map[string]any{}), executor)
	if err := engine.Validate("missing-required-argument"); err == nil || !strings.Contains(err.Error(), "missing required argument value") {
		t.Fatalf("statically missing required tool argument was accepted: %v", err)
	}
	result := engine.Run(context.Background(), "missing-required-argument", map[string]any{"value": "x"}, allow)
	if result.Status != "failed" || executor.calls.Load() != 0 {
		t.Fatalf("missing required argument reached executor: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestRunValidationErrorHasNoToolSideEffect(t *testing.T) {
	ex := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	e := setupEngine(t, workflowDoc("no-side-effect", map[string]any{"value": "{{inputs.missing}}"}), ex)
	r := e.Run(context.Background(), "no-side-effect", map[string]any{"value": "x"}, allow)
	if r.Status != "failed" || ex.calls.Load() != 0 {
		t.Fatalf("validation must fail before calls: %#v calls=%d", r, ex.calls.Load())
	}
	events := readWorkflowEvents(t, e.root, r, e)
	if len(events) != 2 || events[0]["event"] != "workflow_started" || events[1]["event"] != "workflow_completed" || events[1]["outcome_category"] != "validation" {
		t.Fatalf("validation failure log is incomplete: %#v", events)
	}
}
func TestValidationRejectsReachableFallthroughBranch(t *testing.T) {
	doc := workflowDoc("branch-fallthrough", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"] = map[string]any{"max_steps": 4, "max_tool_calls": 2, "max_attempts_per_step": 1, "timeout_seconds": 5}
	doc["steps"] = []any{
		map[string]any{"id": "call", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{inputs.value}}"}},
		map[string]any{"id": "gate", "kind": "decision", "condition": map[string]any{"ref": "{{steps.call.status}}", "operator": "eq", "value": "succeeded"}, "on_true": "done", "on_false": "tail"},
		map[string]any{"id": "done", "kind": "return"},
		map[string]any{"id": "tail", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "tail"}},
	}
	doc["outputs"] = map[string]any{"result": "{{steps.call.result.value}}"}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, doc, executor)
	if err := engine.Validate("branch-fallthrough"); err == nil || !strings.Contains(err.Error(), "without return or stop") {
		t.Fatalf("reachable fallthrough path was accepted: %v", err)
	}
}

func TestValidationRejectsLiteralAndStepMetadataOutputs(t *testing.T) {
	for _, output := range []any{"literal", "{{steps.call.status}}", "{{steps.call.attempt}}", map[string]any{"nested": 1}} {
		doc := workflowDoc("invalid-output", map[string]any{"value": "{{inputs.value}}"})
		doc["outputs"] = map[string]any{"result": output}
		executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
		engine := setupEngine(t, doc, executor)
		if err := engine.Validate("invalid-output"); err == nil || !strings.Contains(err.Error(), "output value") {
			t.Fatalf("invalid output binding accepted: %#v err=%v", output, err)
		}
	}
}

func TestValidationRejectsOutputFromConditionallySkippedTool(t *testing.T) {
	doc := workflowDoc("conditional-output", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"] = map[string]any{"max_steps": 4, "max_tool_calls": 1, "max_attempts_per_step": 1, "timeout_seconds": 5}
	doc["steps"] = []any{
		map[string]any{"id": "gate", "kind": "decision", "condition": map[string]any{"ref": "{{inputs.value}}", "operator": "eq", "value": "run"}, "on_true": "conditional", "on_false": "done"},
		map[string]any{"id": "conditional", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{inputs.value}}"}},
		map[string]any{"id": "merge", "kind": "decision", "condition": map[string]any{"ref": "{{steps.conditional.status}}", "operator": "exists"}, "on_true": "done", "on_false": "done"},
		map[string]any{"id": "done", "kind": "return"},
	}
	doc["outputs"] = map[string]any{"result": "{{steps.conditional.result.value}}"}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, doc, executor)
	if err := engine.Validate("conditional-output"); err == nil || !strings.Contains(err.Error(), "not available on every return path") {
		t.Fatalf("conditionally skipped output source was accepted: %v", err)
	}
}

func TestReturnOnlyWorkflowDoesNotRequireAuthorizer(t *testing.T) {
	doc := workflowDoc("return-only", map[string]any{"value": "unused"})
	doc["limits"] = map[string]any{"max_steps": 1, "max_tool_calls": 0, "max_attempts_per_step": 1, "timeout_seconds": 5}
	doc["steps"] = []any{map[string]any{"id": "done", "kind": "return"}}
	doc["outputs"] = map[string]any{"result": "{{inputs.value}}"}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return "", errors.New("must not execute")
	}}
	engine := setupEngine(t, doc, executor)
	result := engine.Run(context.Background(), "return-only", map[string]any{"value": "x"}, nil)
	if result.Status != "succeeded" || result.Outputs["result"] != "x" || executor.calls.Load() != 0 {
		t.Fatalf("return-only workflow required permission or executed tool: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestRunResolvesNativeReferenceAndOutput(t *testing.T) {
	ex := &testExecutor{fn: func(ctx context.Context, _ string, args map[string]any) (string, error) {
		if args["value"] != "x" {
			t.Fatalf("args=%#v", args)
		}
		return `{"value":"ok"}`, nil
	}}
	e := setupEngine(t, workflowDoc("native-reference", map[string]any{"value": "{{inputs.value}}"}), ex)
	r := e.Run(context.Background(), "native-reference", map[string]any{"value": "x"}, allow)
	if r.Status != "succeeded" || r.Outputs["result"] != "ok" {
		t.Fatalf("unexpected result %#v", r)
	}
}
func TestSecuritySymlinkParentsRejectedWithinTempBlastRadius(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "workflows")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(root, testCatalog{}, &testExecutor{}); err == nil {
		t.Fatal("symlink workflow parent must be rejected during engine initialization")
	}
}
func TestEventWriterCollisionAndTerminalOrdering(t *testing.T) {
	// Blast radius if the guard regresses: this test writes only below a t.TempDir workspace.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	engine, err := NewEngine(root, testCatalog{}, &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return "", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	runID := "oaw_00000000000000000000000000000001"
	writer, err := engine.openEventWriter(runID, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.append(eventBase(runID, "demo", "workflow_started", "started", 0), false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.openEventWriter(runID, "demo"); err == nil {
		t.Fatal("partial collision accepted")
	}
	terminal := eventBase(runID, "demo", "workflow_completed", "failed", 1)
	terminal["outcome_category"] = "validation"
	terminal["summary"] = "test terminal"
	if err := writer.append(terminal, true); err != nil {
		t.Fatal(err)
	}
	if err := writer.append(eventBase(runID, "demo", "workflow_started", "started", 2), false); err == nil {
		t.Fatal("post-terminal event accepted")
	}
	writer.abort()
}
func TestRetryOnlyTypedHandlerErrorsAndAuthorizesEachAttempt(t *testing.T) {
	var approvals atomic.Int32
	var ex *testExecutor
	ex = &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		if ex.calls.Load() == 1 {
			return "", ToolError{Err: errors.New("temporary")}
		}
		return `{"value":"ok"}`, nil
	}}
	d := workflowDoc("retry", map[string]any{"value": "{{inputs.value}}"})
	d["limits"].(map[string]any)["max_attempts_per_step"] = 2
	d["limits"].(map[string]any)["max_tool_calls"] = 2
	d["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 2, "when": []any{"tool_error"}}
	e := setupEngine(t, d, ex)
	r := e.Run(context.Background(), "retry", map[string]any{"value": "x"}, func(context.Context, string, map[string]any, string, int) bool { approvals.Add(1); return true })
	if r.Status != "succeeded" || approvals.Load() != 2 {
		t.Fatalf("retry result %#v approvals=%d", r, approvals.Load())
	}
}

func TestAuthorizationAndRetriesCannotMutateCanonicalArguments(t *testing.T) {
	var executor *testExecutor
	var seen []string
	executor = &testExecutor{fn: func(_ context.Context, _ string, args map[string]any) (string, error) {
		seen = append(seen, args["value"].(string))
		args["value"] = "handler-mutated"
		if executor.calls.Load() == 1 {
			return "", ToolError{Err: errors.New("retry")}
		}
		return `{"value":"ok"}`, nil
	}}
	doc := workflowDoc("argument-isolation", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"].(map[string]any)["max_attempts_per_step"] = 2
	doc["limits"].(map[string]any)["max_tool_calls"] = 2
	doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 2, "when": []any{"tool_error"}}
	engine := setupEngine(t, doc, executor)
	result := engine.Run(context.Background(), "argument-isolation", map[string]any{"value": "original"}, func(_ context.Context, _ string, args map[string]any, _ string, _ int) bool {
		args["value"] = "authorizer-mutated"
		return true
	})
	if result.Status != "succeeded" || len(seen) != 2 || seen[0] != "original" || seen[1] != "original" {
		t.Fatalf("canonical arguments were mutated across boundary: %#v seen=%#v", result, seen)
	}
}

func TestMutuallyExclusiveBranchesUseMaximumPathCallLimit(t *testing.T) {
	doc := workflowDoc("branch-call-limit", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"] = map[string]any{"max_steps": 6, "max_tool_calls": 2, "max_attempts_per_step": 1, "timeout_seconds": 5}
	doc["steps"] = []any{
		map[string]any{"id": "call", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{inputs.value}}"}},
		map[string]any{"id": "gate", "kind": "decision", "condition": map[string]any{"ref": "{{steps.call.status}}", "operator": "eq", "value": "succeeded"}, "on_true": "left", "on_false": "right"},
		map[string]any{"id": "left", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "left"}},
		map[string]any{"id": "merge", "kind": "decision", "condition": map[string]any{"ref": "{{steps.left.status}}", "operator": "exists"}, "on_true": "done", "on_false": "done"},
		map[string]any{"id": "right", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "right"}},
		map[string]any{"id": "done", "kind": "return"},
	}
	doc["outputs"] = map[string]any{"result": "{{steps.call.result.value}}"}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, doc, executor)
	if err := engine.Validate("branch-call-limit"); err != nil {
		t.Fatalf("mutually exclusive branches were summed instead of taking max path: %v", err)
	}
}

func TestValidationAccountsForRetryToolCalls(t *testing.T) {
	doc := workflowDoc("retry-call-limit", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"].(map[string]any)["max_attempts_per_step"] = 2
	doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 2, "when": []any{"tool_error"}}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, doc, executor)
	if err := engine.Validate("retry-call-limit"); err == nil || !strings.Contains(err.Error(), "max_tool_calls") {
		t.Fatalf("retry attempts were omitted from static call limit: %v", err)
	}
}

func TestNonJSONInputsFailBeforeToolCall(t *testing.T) {
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, workflowDoc("non-json-input", map[string]any{"value": "{{inputs.value}}"}), executor)
	result := engine.Run(context.Background(), "non-json-input", map[string]any{"value": math.NaN()}, allow)
	if result.Status != "failed" || executor.calls.Load() != 0 || !strings.Contains(result.Error, "valid JSON") {
		t.Fatalf("non-JSON input reached executor: %#v calls=%d", result, executor.calls.Load())
	}
}

func readWorkflowEvents(t *testing.T, root string, result RunResult, engine *Engine) []map[string]any {
	t.Helper()
	if result.LogPath == "" {
		t.Fatalf("workflow result has no finalized log: %#v", result)
	}
	file, err := os.Open(filepath.Join(root, result.LogPath))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid JSONL event: %v", err)
		}
		if err := engine.eventSchema.Validate(event); err != nil {
			t.Fatalf("event failed schema validation: %v\n%#v", err, event)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestRunFinalizesStrictRedactedEventLog(t *testing.T) {
	secret := "TOP-SECRET-INPUT"
	rawResult := "TOP-SECRET-RESULT"
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return `{"value":"` + rawResult + `"}`, nil
	}}
	engine := setupEngine(t, workflowDoc("redacted-log", map[string]any{"value": "{{inputs.value}}"}), executor)
	result := engine.Run(context.Background(), "redacted-log", map[string]any{"value": secret}, allow)
	if result.Status != "succeeded" {
		t.Fatalf("unexpected result: %#v", result)
	}
	events := readWorkflowEvents(t, engine.root, result, engine)
	if len(events) < 5 || events[0]["event"] != "workflow_started" || events[len(events)-1]["event"] != "workflow_completed" {
		t.Fatalf("unexpected event sequence: %#v", events)
	}
	data, err := os.ReadFile(filepath.Join(engine.root, result.LogPath))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, secret) || strings.Contains(text, rawResult) {
		t.Fatalf("workflow log leaked raw input or result: %s", text)
	}
	if _, err := os.Stat(filepath.Join(engine.root, "sessions", "workflows", "."+result.RunID+".jsonl.partial")); !os.IsNotExist(err) {
		t.Fatalf("partial workflow log remained after finalization: %v", err)
	}
}

func TestOptionalInputReferenceOmitsWholeProperty(t *testing.T) {
	doc := workflowDoc("optional-omit", map[string]any{"value": "{{inputs.value}}", "optional": "{{inputs.optional}}"})
	doc["inputs"].(map[string]any)["optional"] = map[string]any{"type": "string"}
	var seen map[string]any
	executor := &testExecutor{fn: func(_ context.Context, _ string, args map[string]any) (string, error) {
		seen = args
		return `{"value":"ok"}`, nil
	}}
	engine := setupEngine(t, doc, executor)
	definition := engine.catalog["echo"]
	definition.Schema.(map[string]any)["properties"].(map[string]any)["optional"] = map[string]any{"type": "string"}
	engine.catalog["echo"] = definition
	result := engine.Run(context.Background(), "optional-omit", map[string]any{"value": "x"}, allow)
	if result.Status != "succeeded" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, exists := seen["optional"]; exists {
		t.Fatalf("missing optional input was not omitted: %#v", seen)
	}
}

func TestEmbeddedMissingReferenceFailsBeforeToolCall(t *testing.T) {
	doc := workflowDoc("embedded-missing", map[string]any{"value": "prefix {{inputs.optional}}"})
	doc["inputs"].(map[string]any)["optional"] = map[string]any{"type": "string"}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, doc, executor)
	result := engine.Run(context.Background(), "embedded-missing", map[string]any{"value": "x"}, allow)
	if result.Status != "failed" || executor.calls.Load() != 0 {
		t.Fatalf("embedded missing reference must fail before call: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestDecisionJumpRecordsSkippedStep(t *testing.T) {
	doc := workflowDoc("decision-skip", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"] = map[string]any{"max_steps": 4, "max_tool_calls": 2, "max_attempts_per_step": 1, "timeout_seconds": 5}
	doc["steps"] = []any{
		map[string]any{"id": "call", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{inputs.value}}"}},
		map[string]any{"id": "gate", "kind": "decision", "condition": map[string]any{"ref": "{{steps.call.result.missing}}", "operator": "exists"}, "on_true": "unused", "on_false": "done"},
		map[string]any{"id": "unused", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "never"}},
		map[string]any{"id": "done", "kind": "return"},
	}
	doc["outputs"] = map[string]any{"result": "{{steps.call.result.value}}"}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, doc, executor)
	result := engine.Run(context.Background(), "decision-skip", map[string]any{"value": "x"}, allow)
	if result.Status != "succeeded" || executor.calls.Load() != 1 {
		t.Fatalf("unexpected result: %#v calls=%d", result, executor.calls.Load())
	}
	events := readWorkflowEvents(t, engine.root, result, engine)
	foundSkipped := false
	for _, event := range events {
		if event["event"] == "step_skipped" && event["step_id"] == "unused" {
			foundSkipped = true
		}
	}
	if !foundSkipped {
		t.Fatalf("skipped step event missing: %#v", events)
	}
}

func TestOnFailureReportReturnsStructuredNonSuccess(t *testing.T) {
	doc := workflowDoc("failure-report", map[string]any{"value": "{{inputs.value}}"})
	doc["on_failure"] = "report"
	secret := "SECRET-HANDLER-ERROR"
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return "", ToolError{Err: errors.New(secret)}
	}}
	engine := setupEngine(t, doc, executor)
	result := engine.Run(context.Background(), "failure-report", map[string]any{"value": "x"}, allow)
	if result.Status == "succeeded" || result.Failure == nil || result.Failure.Code != "tool_error" || result.Outputs != nil {
		t.Fatalf("unexpected structured failure: %#v", result)
	}
	readWorkflowEvents(t, engine.root, result, engine)
	log, err := os.ReadFile(filepath.Join(engine.root, result.LogPath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), secret) {
		t.Fatalf("handler error leaked to event log: %s", log)
	}
}

func TestCancellationDuringAuthorizationIsCancelled(t *testing.T) {
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, workflowDoc("authorization-cancel", map[string]any{"value": "{{inputs.value}}"}), executor)
	ctx, cancel := context.WithCancel(context.Background())
	result := engine.Run(ctx, "authorization-cancel", map[string]any{"value": "x"}, func(context.Context, string, map[string]any, string, int) bool {
		cancel()
		return true
	})
	if result.Status != "cancelled" || executor.calls.Load() != 0 {
		t.Fatalf("authorization cancellation was misclassified: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestCallerCancellationStopsWithoutRetry(t *testing.T) {
	doc := workflowDoc("caller-cancel", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"].(map[string]any)["max_attempts_per_step"] = 3
	doc["limits"].(map[string]any)["max_tool_calls"] = 3
	doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 3, "when": []any{"timeout"}}
	ctx, cancel := context.WithCancel(context.Background())
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		cancel()
		return "", context.Canceled
	}}
	engine := setupEngine(t, doc, executor)
	result := engine.Run(ctx, "caller-cancel", map[string]any{"value": "x"}, allow)
	if result.Status != "cancelled" || executor.calls.Load() != 1 {
		t.Fatalf("caller cancellation retried or changed status: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestHandlerTimeoutRetriesWhileWorkflowActive(t *testing.T) {
	doc := workflowDoc("handler-timeout", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"].(map[string]any)["max_attempts_per_step"] = 2
	doc["limits"].(map[string]any)["max_tool_calls"] = 2
	doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 2, "when": []any{"timeout"}}
	var executor *testExecutor
	executor = &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		if executor.calls.Load() == 1 {
			return "", HandlerTimeout{Err: errors.New("local timeout")}
		}
		return `{"value":"ok"}`, nil
	}}
	engine := setupEngine(t, doc, executor)
	result := engine.Run(context.Background(), "handler-timeout", map[string]any{"value": "x"}, allow)
	if result.Status != "succeeded" || executor.calls.Load() != 2 {
		t.Fatalf("handler timeout was not retried: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestWorkflowDeadlineDoesNotRetry(t *testing.T) {
	doc := workflowDoc("workflow-timeout", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"].(map[string]any)["timeout_seconds"] = 1
	doc["limits"].(map[string]any)["max_attempts_per_step"] = 2
	doc["limits"].(map[string]any)["max_tool_calls"] = 2
	doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 2, "when": []any{"timeout"}}
	executor := &testExecutor{fn: func(ctx context.Context, _ string, _ map[string]any) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}}
	engine := setupEngine(t, doc, executor)
	start := time.Now()
	result := engine.Run(context.Background(), "workflow-timeout", map[string]any{"value": "x"}, allow)
	if result.Status != "timed_out" || executor.calls.Load() != 1 || time.Since(start) > 3*time.Second {
		t.Fatalf("workflow deadline retried or hung: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestEventSchemaRejectsUnknownAndWorkflowStepFields(t *testing.T) {
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, workflowDoc("event-strict", map[string]any{"value": "{{inputs.value}}"}), executor)
	base := eventBase("oaw_00000000000000000000000000000001", "event-strict", "workflow_started", "started", 0)
	if err := engine.eventSchema.Validate(base); err != nil {
		t.Fatalf("valid workflow event rejected: %v", err)
	}
	base["unexpected"] = true
	if err := engine.eventSchema.Validate(base); err == nil {
		t.Fatal("unknown event field accepted")
	}
	delete(base, "unexpected")
	base["step_id"] = "bad"
	base["attempt"] = 1
	if err := engine.eventSchema.Validate(base); err == nil {
		t.Fatal("workflow-level event accepted step fields")
	}
	delete(base, "step_id")
	delete(base, "attempt")
	base["summary"] = "not allowed"
	base["outcome_category"] = "success"
	if err := engine.eventSchema.Validate(base); err == nil {
		t.Fatal("workflow_started accepted terminal-only fields")
	}
}

func TestEngineSnapshotsWorkflowSchema(t *testing.T) {
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, workflowDoc("workflow-schema-snapshot", map[string]any{"value": "{{inputs.value}}"}), executor)
	if err := os.WriteFile(filepath.Join(engine.root, "workflows", "agent", "oaw.schema.json"), []byte(`{"not":"a valid replacement"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.Validate("workflow-schema-snapshot"); err != nil {
		t.Fatalf("workflow schema changed after engine initialization: %v", err)
	}
}

func TestEngineSnapshotsToolSchema(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	doc := workflowDoc("schema-snapshot", map[string]any{"value": "{{inputs.value}}"})
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "schema-snapshot.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	property := map[string]any{"type": "string"}
	schema := map[string]any{"type": "object", "additionalProperties": false, "required": []any{"value"}, "properties": map[string]any{"value": property}}
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine, err := NewEngine(root, testCatalog{{Name: "echo", WorkflowCallable: true, Schema: schema}}, executor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	property["type"] = "integer"
	schema["required"] = []any{}
	result := engine.Run(context.Background(), "schema-snapshot", map[string]any{"value": "x"}, allow)
	if result.Status != "succeeded" {
		t.Fatalf("external schema mutation changed engine snapshot: %#v", result)
	}
}

func TestNewEngineRejectsExternalToolSchemaReference(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	_, err := NewEngine(root, testCatalog{{Name: "echo", WorkflowCallable: true, Schema: map[string]any{"$ref": "https://example.com/tool.json"}}}, &testExecutor{})
	if err == nil || !strings.Contains(err.Error(), "external schema reference") {
		t.Fatalf("external tool schema reference was not rejected: %v", err)
	}
}

func TestClosedEngineFailsWithoutToolSideEffect(t *testing.T) {
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) { return `{"value":"ok"}`, nil }}
	engine := setupEngine(t, workflowDoc("closed-engine", map[string]any{"value": "{{inputs.value}}"}), executor)
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	result := engine.Run(context.Background(), "closed-engine", map[string]any{"value": "x"}, allow)
	if result.Status != "failed" || executor.calls.Load() != 0 || !strings.Contains(result.Error, "closed") {
		t.Fatalf("closed engine did not fail before side effects: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestIntegerInputsAcceptIntegralJSONFloatOnly(t *testing.T) {
	executor := &testExecutor{fn: func(_ context.Context, _ string, args map[string]any) (string, error) {
		if args["value"] != float64(4) {
			t.Fatalf("integral JSON float was changed: %#v", args["value"])
		}
		return `{"value":"ok"}`, nil
	}}
	doc := workflowDoc("json-integer", map[string]any{"value": "{{inputs.value}}"})
	doc["inputs"].(map[string]any)["value"] = map[string]any{"type": "integer", "required": true}
	engine := setupEngine(t, doc, executor)
	definition := engine.catalog["echo"]
	definition.Schema.(map[string]any)["properties"].(map[string]any)["value"] = map[string]any{"type": "integer"}
	engine.catalog["echo"] = definition
	compiled, err := compileToolSchema(definition)
	if err != nil {
		t.Fatal(err)
	}
	engine.toolSchemas["echo"] = compiled
	result := engine.Run(context.Background(), "json-integer", map[string]any{"value": float64(4)}, allow)
	if result.Status != "succeeded" {
		t.Fatalf("integral JSON float rejected: %#v", result)
	}
	result = engine.Run(context.Background(), "json-integer", map[string]any{"value": 4.5}, allow)
	if result.Status != "failed" {
		t.Fatalf("fractional JSON float accepted as integer: %#v", result)
	}
}

func TestCumulativeRetainedResultsRespectRunCap(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	testSchema(t, root)
	doc := workflowDoc("cumulative-result-cap", map[string]any{"value": "{{inputs.value}}"})
	doc["limits"] = map[string]any{"max_steps": 3, "max_tool_calls": 2, "max_attempts_per_step": 1, "timeout_seconds": 5}
	doc["steps"] = []any{
		map[string]any{"id": "first", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{inputs.value}}"}},
		map[string]any{"id": "second", "kind": "tool", "tool": "echo", "arguments": map[string]any{"value": "{{inputs.value}}"}},
		map[string]any{"id": "done", "kind": "return"},
	}
	doc["outputs"] = map[string]any{"result": "{{steps.second.result.value}}"}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "cumulative-result-cap.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 5<<20)
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return `{"value":"` + payload + `"}`, nil
	}}
	engine, err := NewEngine(root, testCatalog{{Name: "echo", WorkflowCallable: true, Schema: map[string]any{"type": "object", "additionalProperties": false, "required": []any{"value"}, "properties": map[string]any{"value": map[string]any{"type": "string"}}}}}, executor)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	result := engine.Run(context.Background(), "cumulative-result-cap", map[string]any{"value": "x"}, allow)
	if result.Status != "failed" || result.Outputs != nil || executor.calls.Load() != 2 {
		t.Fatalf("cumulative retained result cap was not enforced: %#v calls=%d", result, executor.calls.Load())
	}
}

func TestOversizedRetainedResultFailsWithoutOutputs(t *testing.T) {
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return strings.Repeat("x", maxResultBytes+1), nil
	}}
	engine := setupEngine(t, workflowDoc("result-cap", map[string]any{"value": "{{inputs.value}}"}), executor)
	result := engine.Run(context.Background(), "result-cap", map[string]any{"value": "x"}, allow)
	if result.Status != "failed" || result.Outputs != nil || executor.calls.Load() != 1 {
		t.Fatalf("oversized result was retained: %#v calls=%d", result, executor.calls.Load())
	}
}
