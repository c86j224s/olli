package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
	"github.com/c86j224s/olli/workflow"
)

func TestRegistryExecutorClassifiesLocalDeadline(t *testing.T) {
	registry := tools.NewEmptyRegistry()
	tool := testWorkflowTool()
	if err := registry.RegisterContext(tool, tools.ToolMetadata{WorkflowCallable: true}, func(context.Context, map[string]interface{}) (string, error) {
		return "", context.DeadlineExceeded
	}); err != nil {
		t.Fatal(err)
	}
	executor := registryWorkflowExecutor{registry: registry}
	_, err := executor.ExecuteContext(context.Background(), tool.Function.Name, map[string]any{"value": "x"})
	var timeout workflow.HandlerTimeout
	if !errors.As(err, &timeout) {
		t.Fatalf("handler-local deadline was not typed as timeout: %T %v", err, err)
	}
}

func TestOllamaSchemaCatalogMappingClonesJSONValues(t *testing.T) {
	source := ollama.FunctionParamSchema{
		Type: "object",
		Properties: map[string]ollama.FunctionParamProperty{
			"choice": {Type: "string", Description: "pick one", Enum: []string{"a", "b"}},
		},
		Required: []string{"choice"},
	}
	mapped := ollamaSchema(source)
	if mapped["$schema"] != "https://json-schema.org/draft/2020-12/schema" || mapped["type"] != "object" || mapped["additionalProperties"] != false {
		t.Fatalf("unexpected schema markers: %#v", mapped)
	}
	if _, ok := mapped["required"].([]any); !ok {
		t.Fatalf("required must be []any: %#v", mapped["required"])
	}
	properties := mapped["properties"].(map[string]any)
	property := properties["choice"].(map[string]any)
	if _, ok := property["enum"].([]any); !ok {
		t.Fatalf("enum must be []any: %#v", property["enum"])
	}
	if _, err := json.Marshal(mapped); err != nil {
		t.Fatalf("mapped schema is not JSON compatible: %v", err)
	}

	source.Required[0] = "changed"
	source.Properties["choice"] = ollama.FunctionParamProperty{Type: "integer"}
	if mapped["required"].([]any)[0] != "choice" || property["type"] != "string" {
		t.Fatalf("mapped schema aliases source values: %#v", mapped)
	}
}

func TestEnableWorkflowsRegistersNonCallableControlsAndRunsRegistryTool(t *testing.T) {
	root := setupAgentWorkflowRoot(t)
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	var calls atomic.Int32
	if err := ag.GetRegistry().RegisterContext(testWorkflowTool(), tools.ToolMetadata{RetrySafe: true, WorkflowCallable: true}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		calls.Add(1)
		if args["value"] != "input" {
			t.Fatalf("unexpected workflow arguments: %#v", args)
		}
		return `{"value":"output"}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	var catalogDefinition *workflow.ToolDefinition
	for _, definition := range (registryWorkflowCatalog{registry: ag.GetRegistry()}).ListTools() {
		if definition.Name == "test_workflow_tool" {
			catalogDefinition = &definition
			break
		}
	}
	if catalogDefinition == nil || !catalogDefinition.RetrySafe || !catalogDefinition.WorkflowCallable {
		t.Fatalf("catalog metadata mapping failed: %#v", catalogDefinition)
	}
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	ag.SetToolMode(ModeAuto)
	if err := ag.EnableWorkflows(root); err == nil {
		t.Fatal("expected duplicate EnableWorkflows to fail")
	}
	for _, name := range workflowControlNames {
		metadata, ok := ag.GetRegistry().GetMetadata(name)
		if !ok || metadata.WorkflowCallable {
			t.Fatalf("control tool %q must be non-callable: %#v %v", name, metadata, ok)
		}
	}
	if err := ag.ValidateWorkflow("bridge"); err != nil {
		t.Fatalf("workflow validation failed: %v", err)
	}
	result := ag.RunWorkflow(context.Background(), "bridge", map[string]any{"value": "input"}, nil)
	if result.Status != "succeeded" || result.Outputs["result"] != "output" || calls.Load() != 1 {
		t.Fatalf("unexpected workflow result: %#v calls=%d", result, calls.Load())
	}
}

func TestRunWorkflowDeniesRequiredPermissionWithoutCallingHandler(t *testing.T) {
	root := setupAgentWorkflowRoot(t)
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	var calls atomic.Int32
	if err := ag.GetRegistry().RegisterContext(testWorkflowTool(), tools.ToolMetadata{WorkflowCallable: true}, func(context.Context, map[string]interface{}) (string, error) {
		calls.Add(1)
		return `{"value":"output"}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	ag.SetToolMode(ModeAsk)
	result := ag.RunWorkflow(context.Background(), "bridge", map[string]any{"value": "input"}, nil)
	if result.Status != "failed" || result.Failure != nil && result.Failure.Code != "denied" || calls.Load() != 0 {
		t.Fatalf("permission should deny before handler call: %#v calls=%d", result, calls.Load())
	}
}

func TestRunWorkflowPersistsOnlyExplicitAlwaysOutsideEngine(t *testing.T) {
	root := setupAgentWorkflowRoot(t)
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	if err := ag.GetRegistry().RegisterContext(testWorkflowTool(), tools.ToolMetadata{WorkflowCallable: true}, func(context.Context, map[string]interface{}) (string, error) {
		return `{"value":"output"}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	ag.SetToolMode(ModeAsk)
	confirm := func(context.Context, string, map[string]interface{}) (bool, bool) { return true, false }
	if result := ag.RunWorkflow(context.Background(), "bridge", map[string]any{"value": "input"}, confirm); result.Status != "succeeded" {
		t.Fatalf("explicit non-always approval failed: %#v", result)
	}
	if cfg.IsWhitelisted("test_workflow_tool") {
		t.Fatal("always=false must not persist whitelist")
	}
	confirm = func(context.Context, string, map[string]interface{}) (bool, bool) { return true, true }
	if result := ag.RunWorkflow(context.Background(), "bridge", map[string]any{"value": "input"}, confirm); result.Status != "succeeded" {
		t.Fatalf("explicit always approval failed: %#v", result)
	}
	if !cfg.IsWhitelisted("test_workflow_tool") {
		t.Fatal("always=true must persist whitelist in agent layer")
	}
}

func TestBundledImageWorkflowValidatesAgainstRegisteredTools(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ImageGeneration.Enabled = true
	cfg.ImageInspection.Enabled = true
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ag.Close() })
	if err := ag.ValidateWorkflow("image-generate-verify"); err != nil {
		t.Fatalf("bundled workflow does not match the registered tool catalog: %v", err)
	}
}

func TestListWorkflowsHidesWorkflowsWithDisabledTools(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ImageGeneration.Enabled = false
	cfg.ImageInspection.Enabled = false
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ag.Close() })

	names, err := ag.ListWorkflows()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name == "image-generate-verify" {
			t.Fatal("workflow requiring disabled media tools must not be listed")
		}
	}
}

func TestLateAlwaysApprovalAfterDeadlineDoesNotPersist(t *testing.T) {
	root := setupAgentWorkflowRoot(t)
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	if err := ag.GetRegistry().RegisterContext(testWorkflowTool(), tools.ToolMetadata{WorkflowCallable: true}, func(context.Context, map[string]interface{}) (string, error) {
		return `{"value":"output"}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	ag.SetToolMode(ModeAsk)
	ctx, cancel := context.WithCancel(context.Background())
	result := ag.RunWorkflow(ctx, "bridge", map[string]any{"value": "input"}, func(context.Context, string, map[string]interface{}) (bool, bool) {
		cancel()
		return true, true
	})
	if result.Status != "cancelled" || cfg.IsWhitelisted("test_workflow_tool") {
		t.Fatalf("late Always approval persisted after cancellation: %#v whitelisted=%v", result, cfg.IsWhitelisted("test_workflow_tool"))
	}
}

func TestWorkflowDeadlineReturnsWhileAuthorizerIsBlocked(t *testing.T) {
	root := setupAgentWorkflowRoot(t)
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	if err := ag.GetRegistry().RegisterContext(testWorkflowTool(), tools.ToolMetadata{WorkflowCallable: true}, func(context.Context, map[string]interface{}) (string, error) {
		return `{"value":"output"}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "workflows", "agent", "bridge.oaw.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["limits"].(map[string]any)["timeout_seconds"] = float64(1)
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "bridge.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	ag.SetToolMode(ModeAsk)
	result := ag.RunWorkflow(context.Background(), "bridge", map[string]any{"value": "input"}, func(ctx context.Context, _ string, _ map[string]interface{}) (bool, bool) {
		<-ctx.Done()
		return false, false
	})
	if result.Status != "timed_out" {
		t.Fatalf("blocked authorizer prevented workflow deadline: %#v", result)
	}
}

func TestAlwaysApprovalDoesNotAutoApproveRetry(t *testing.T) {
	root := setupAgentWorkflowRoot(t)
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	var calls atomic.Int32
	if err := ag.GetRegistry().RegisterContext(testWorkflowTool(), tools.ToolMetadata{RetrySafe: true, WorkflowCallable: true}, func(context.Context, map[string]interface{}) (string, error) {
		if calls.Add(1) == 1 {
			return "", errors.New("retry")
		}
		return `{"value":"output"}`, nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "workflows", "agent", "bridge.oaw.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["limits"].(map[string]any)["max_attempts_per_step"] = float64(2)
	doc["limits"].(map[string]any)["max_tool_calls"] = float64(2)
	doc["steps"].([]any)[0].(map[string]any)["retry"] = map[string]any{"max_attempts": 2, "when": []any{"tool_error"}}
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "bridge.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	ag.SetToolMode(ModeAcceptEdit)
	var approvals atomic.Int32
	result := ag.RunWorkflow(context.Background(), "bridge", map[string]any{"value": "input"}, func(context.Context, string, map[string]interface{}) (bool, bool) {
		approvals.Add(1)
		return true, true
	})
	if result.Status != "succeeded" || approvals.Load() != 2 || calls.Load() != 2 {
		t.Fatalf("retry reused first Always approval: %#v approvals=%d calls=%d", result, approvals.Load(), calls.Load())
	}
}

func TestCloseIsIdempotentAndDisablesWorkflowOperations(t *testing.T) {
	root := setupAgentWorkflowRoot(t)
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	ag := New(ollama.NewClient("http://127.0.0.1:1"), "test", "test", nil, cfg)
	if err := ag.EnableWorkflows(root); err != nil {
		t.Fatal(err)
	}
	if err := ag.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ag.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.ListWorkflows(); err == nil {
		t.Fatal("operations after Close must fail")
	}
	if result := ag.RunWorkflow(context.Background(), "bridge", nil, nil); result.Status != "failed" {
		t.Fatalf("run after Close must fail: %#v", result)
	}
}

func setupAgentWorkflowRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "workflows", "agent"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"oaw.schema.json", "oaw-event.schema.json"} {
		data, err := os.ReadFile(filepath.Join("..", "workflows", "agent", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "workflows", "agent", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	doc := map[string]any{
		"oaw_version": "0.1", "name": "bridge", "description": "registry bridge test",
		"inputs": map[string]any{"value": map[string]any{"type": "string", "required": true}},
		"limits": map[string]any{"max_steps": 2, "max_tool_calls": 1, "max_attempts_per_step": 1, "timeout_seconds": 5},
		"steps": []any{
			map[string]any{"id": "call", "kind": "tool", "tool": "test_workflow_tool", "arguments": map[string]any{"value": "{{inputs.value}}"}},
			map[string]any{"id": "done", "kind": "return"},
		},
		"outputs": map[string]any{"result": "{{steps.call.result.value}}"}, "on_failure": "stop",
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflows", "agent", "bridge.oaw.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func testWorkflowTool() ollama.Tool {
	return ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "test_workflow_tool", Description: "test", Parameters: ollama.FunctionParamSchema{
		Type: "object", Properties: map[string]ollama.FunctionParamProperty{"value": {Type: "string", Description: "value"}}, Required: []string{"value"},
	}}}
}
