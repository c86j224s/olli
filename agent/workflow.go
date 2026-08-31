package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
	"github.com/c86j224s/olli/workflow"
)

var errWorkflowsDisabled = errors.New("workflows are not enabled")

var workflowControlNames = [...]string{"list_workflows", "get_workflow", "run_workflow"}

type registryWorkflowCatalog struct {
	registry *tools.Registry
}

func (c registryWorkflowCatalog) ListTools() []workflow.ToolDefinition {
	definitions := c.registry.GetDefinitions()
	out := make([]workflow.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		metadata, _ := c.registry.GetMetadata(definition.Function.Name)
		out = append(out, workflow.ToolDefinition{
			Name:             definition.Function.Name,
			Schema:           ollamaSchema(definition.Function.Parameters),
			RetrySafe:        metadata.RetrySafe,
			WorkflowCallable: metadata.WorkflowCallable,
		})
	}
	return out
}

type registryWorkflowExecutor struct {
	registry *tools.Registry
}

func (e registryWorkflowExecutor) ExecuteContext(ctx context.Context, name string, args map[string]any) (string, error) {
	result, err := e.registry.ExecuteContext(ctx, name, args)
	if err == nil {
		return result, nil
	}
	if ctx != nil && ctx.Err() != nil {
		return result, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return result, workflow.HandlerTimeout{Err: err}
	}
	return result, workflow.ToolError{Err: err}
}

func ollamaSchema(schema ollama.FunctionParamSchema) map[string]any {
	properties := make(map[string]any, len(schema.Properties))
	for name, property := range schema.Properties {
		item := map[string]any{
			"type":        stringClone(property.Type),
			"description": stringClone(property.Description),
		}
		if property.Enum != nil {
			values := make([]any, len(property.Enum))
			for i, value := range property.Enum {
				values[i] = stringClone(value)
			}
			item["enum"] = values
		}
		properties[stringClone(name)] = item
	}
	required := make([]any, len(schema.Required))
	for i, value := range schema.Required {
		required[i] = stringClone(value)
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 stringClone(schema.Type),
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func stringClone(value string) string { return string([]byte(value)) }

func (a *Agent) EnableWorkflows(root string) error {
	a.workflowMu.Lock()
	defer a.workflowMu.Unlock()

	if a.workflowEngine != nil {
		return errors.New("workflows are already enabled")
	}
	for _, name := range workflowControlNames {
		if _, ok := a.registry.GetDefinition(name); ok {
			return fmt.Errorf("workflow control tool %q is already registered", name)
		}
	}

	engine, err := workflow.NewEngine(root, registryWorkflowCatalog{registry: a.registry}, registryWorkflowExecutor{registry: a.registry})
	if err != nil {
		return fmt.Errorf("enable workflows: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = engine.Close()
		}
	}()

	controls := []struct {
		tool    ollama.Tool
		handler tools.ContextToolHandler
	}{
		{
			tool: ollama.Tool{Type: "function", Function: ollama.FunctionDef{
				Name: "list_workflows", Description: "List available agent workflows", Parameters: ollama.FunctionParamSchema{
					Type: "object", Properties: map[string]ollama.FunctionParamProperty{}, Required: []string{},
				},
			}},
			handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
				names, err := a.ListWorkflows()
				if err != nil {
					return "", err
				}
				return marshalWorkflowResult(names)
			},
		},
		{
			tool: ollama.Tool{Type: "function", Function: ollama.FunctionDef{
				Name: "get_workflow", Description: "Get an agent workflow definition", Parameters: ollama.FunctionParamSchema{
					Type: "object", Properties: map[string]ollama.FunctionParamProperty{
						"name": {Type: "string", Description: "Workflow name"},
					}, Required: []string{"name"},
				},
			}},
			handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
				name, ok := args["name"].(string)
				if !ok {
					return "", errors.New("workflow name is required")
				}
				definition, err := a.GetWorkflow(name)
				if err != nil {
					return "", err
				}
				return marshalWorkflowResult(definition)
			},
		},
		{
			tool: ollama.Tool{Type: "function", Function: ollama.FunctionDef{
				Name: "run_workflow", Description: "Run an agent workflow", Parameters: ollama.FunctionParamSchema{
					Type: "object", Properties: map[string]ollama.FunctionParamProperty{
						"name":   {Type: "string", Description: "Workflow name"},
						"inputs": {Type: "object", Description: "Workflow input values"},
					}, Required: []string{"name", "inputs"},
				},
			}},
			handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
				name, ok := args["name"].(string)
				if !ok {
					return "", errors.New("workflow name is required")
				}
				inputs, ok := args["inputs"].(map[string]interface{})
				if !ok {
					return "", errors.New("workflow inputs are required")
				}
				cb := callbacksFromContext(ctx)
				result := a.RunWorkflow(ctx, name, inputs, cb.ConfirmToolCallWithActionContext)
				return marshalWorkflowResult(result)
			},
		},
	}
	for _, control := range controls {
		if err := a.registry.RegisterContext(control.tool, tools.ToolMetadata{WorkflowCallable: false}, control.handler); err != nil {
			return fmt.Errorf("register workflow control: %w", err)
		}
	}
	a.workflowEngine = engine
	closeOnError = false
	return nil
}

func marshalWorkflowResult(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal workflow result: %w", err)
	}
	return string(data), nil
}

func (a *Agent) ListWorkflows() ([]string, error) {
	a.workflowMu.RLock()
	defer a.workflowMu.RUnlock()
	if a.workflowEngine == nil {
		return nil, errWorkflowsDisabled
	}
	names, err := a.workflowEngine.List()
	if err != nil {
		return nil, err
	}
	available := names[:0]
	for _, name := range names {
		if err := a.workflowEngine.Validate(name); err == nil {
			available = append(available, name)
		}
	}
	return available, nil
}

func (a *Agent) GetWorkflow(name string) (map[string]any, error) {
	a.workflowMu.RLock()
	defer a.workflowMu.RUnlock()
	if a.workflowEngine == nil {
		return nil, errWorkflowsDisabled
	}
	return a.workflowEngine.Show(name)
}

func (a *Agent) ValidateWorkflow(name string) error {
	a.workflowMu.RLock()
	defer a.workflowMu.RUnlock()
	if a.workflowEngine == nil {
		return errWorkflowsDisabled
	}
	return a.workflowEngine.Validate(name)
}

func (a *Agent) RunWorkflow(ctx context.Context, name string, inputs map[string]any, confirm func(context.Context, string, map[string]interface{}) (bool, bool)) workflow.RunResult {
	a.workflowMu.RLock()
	defer a.workflowMu.RUnlock()
	if a.workflowEngine == nil {
		return workflow.RunResult{Status: "failed", Error: errWorkflowsDisabled.Error()}
	}
	if ctx == nil {
		return workflow.RunResult{Status: "failed", Error: "nil context"}
	}
	interactiveSteps := map[string]bool{}
	authorize := func(authCtx context.Context, toolName string, args map[string]any, stepID string, attempt int) bool {
		requiresInteractive := a.ShouldRequirePermission(toolName)
		if attempt == 1 && requiresInteractive {
			interactiveSteps[stepID] = true
		}
		if !requiresInteractive && !(attempt > 1 && interactiveSteps[stepID]) {
			return true
		}
		if confirm == nil {
			return false
		}
		allowed, always := confirm(authCtx, toolName, args)
		if authCtx.Err() != nil {
			return false
		}
		if !allowed {
			return false
		}
		if always {
			if a.cfg == nil || a.cfg.AddWhitelist(toolName) != nil {
				return false
			}
		}
		return true
	}
	return a.workflowEngine.Run(ctx, name, inputs, authorize)
}

func (a *Agent) Close() error {
	a.workflowMu.Lock()
	defer a.workflowMu.Unlock()
	engine := a.workflowEngine
	a.workflowEngine = nil
	if engine == nil {
		return nil
	}
	return engine.Close()
}
