package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/c86j224s/olli/ollama"
)

func TestRegistryDuplicateRegistrationPreservesFirstDefinitionHandlerAndMetadata(t *testing.T) {
	reg := NewEmptyRegistry()
	firstTool := ollama.Tool{Type: "function", Function: ollama.FunctionDef{
		Name:        "duplicate",
		Description: "first",
		Parameters:  ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{}},
	}}
	secondTool := firstTool
	secondTool.Function.Description = "second"
	firstErr := errors.New("first handler")
	if err := reg.RegisterContext(firstTool, ToolMetadata{RetrySafe: true, WorkflowCallable: false}, func(context.Context, map[string]interface{}) (string, error) {
		return "first", firstErr
	}); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}
	if err := reg.RegisterContext(secondTool, ToolMetadata{RetrySafe: false, WorkflowCallable: true}, func(context.Context, map[string]interface{}) (string, error) {
		return "second", nil
	}); err == nil {
		t.Fatal("expected duplicate registration error")
	}

	got, err := reg.ExecuteContext(context.Background(), "duplicate", nil)
	if !errors.Is(err, firstErr) || got != "first" {
		t.Fatalf("duplicate registration replaced first handler: got %q, %v", got, err)
	}
	definition, ok := reg.GetDefinition("duplicate")
	if !ok || definition.Function.Description != "first" {
		t.Fatalf("duplicate registration replaced first definition: %#v, %v", definition, ok)
	}
	metadata, ok := reg.GetMetadata("duplicate")
	if !ok || metadata != (ToolMetadata{RetrySafe: true, WorkflowCallable: false}) {
		t.Fatalf("duplicate registration replaced first metadata: %#v, %v", metadata, ok)
	}
	if got := len(reg.GetDefinitions()); got != 1 {
		t.Fatalf("duplicate registration appended a definition: got %d", got)
	}
}

func TestRegistryExecuteContextPassesCallerContext(t *testing.T) {
	reg := NewEmptyRegistry()
	type contextKey string
	key := contextKey("key")
	if err := reg.RegisterContext(ollama.Tool{Function: ollama.FunctionDef{Name: "context_tool"}}, ToolMetadata{}, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		if got := ctx.Value(key); got != "value" {
			t.Fatalf("handler received wrong context value: %v", got)
		}
		return "ok", nil
	}); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	ctx := context.WithValue(context.Background(), key, "value")
	if got, err := reg.ExecuteContext(ctx, "context_tool", nil); err != nil || got != "ok" {
		t.Fatalf("context execution failed: %q, %v", got, err)
	}
	if _, err := reg.ExecuteContext(nil, "context_tool", nil); err == nil {
		t.Fatal("expected nil context rejection")
	}
}

func TestRegistryLegacyRegisterUsesConvenienceContextAndMetadataDefaults(t *testing.T) {
	reg := NewEmptyRegistry()
	if err := reg.Register(ollama.Tool{Function: ollama.FunctionDef{Name: "legacy"}}, func(map[string]interface{}) (string, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	if got, err := reg.Execute("legacy", nil); err != nil || got != "ok" {
		t.Fatalf("legacy execution failed: %q, %v", got, err)
	}
	metadata, ok := reg.GetMetadata("legacy")
	if !ok || metadata != (ToolMetadata{WorkflowCallable: true}) {
		t.Fatalf("unexpected legacy metadata: %#v, %v", metadata, ok)
	}
}
