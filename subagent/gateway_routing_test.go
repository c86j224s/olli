package subagent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

type recordingLeaseProvider struct {
	mu       sync.Mutex
	requests []ollama.RouteRequest
	client   ollama.ChatClient
}

func (p *recordingLeaseProvider) ListModels() ([]string, error) { return []string{"model"}, nil }
func (p *recordingLeaseProvider) ListModelsWithContext(context.Context) ([]string, error) {
	return p.ListModels()
}
func (p *recordingLeaseProvider) ChatStreamFull(ollama.ChatRequest, ollama.StreamCallbacks) (*ollama.Message, error) {
	return nil, fmt.Errorf("gateway direct chat should not be used by routed subagent")
}
func (p *recordingLeaseProvider) ChatStreamFullWithContext(context.Context, ollama.ChatRequest, ollama.StreamCallbacks) (*ollama.Message, error) {
	return nil, fmt.Errorf("gateway direct chat should not be used by routed subagent")
}
func (p *recordingLeaseProvider) Acquire(_ context.Context, request ollama.RouteRequest) (ollama.ClientLease, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	return &recordingLease{client: p.client, nodeID: "node-a"}, nil
}

type recordingLease struct {
	client ollama.ChatClient
	nodeID string
}

func (l *recordingLease) Client() ollama.ChatClient { return l.client }
func (l *recordingLease) NodeID() string            { return l.nodeID }
func (l *recordingLease) Release(error)             {}

type staticChatClient struct{}

func (staticChatClient) ListModels() ([]string, error) { return []string{"model"}, nil }
func (staticChatClient) ListModelsWithContext(context.Context) ([]string, error) {
	return []string{"model"}, nil
}
func (staticChatClient) ChatStreamFull(request ollama.ChatRequest, callbacks ollama.StreamCallbacks) (*ollama.Message, error) {
	return staticChatClient{}.ChatStreamFullWithContext(context.Background(), request, callbacks)
}
func (staticChatClient) ChatStreamFullWithContext(_ context.Context, _ ollama.ChatRequest, callbacks ollama.StreamCallbacks) (*ollama.Message, error) {
	if callbacks.OnContent != nil {
		callbacks.OnContent(`{"ok":true}`)
	}
	return &ollama.Message{Role: "assistant", Content: `{"ok":true}`}, nil
}

func TestSubagentRunnerRoutesAndReportsLeaseNode(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.LoadConfig(root + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	provider := &recordingLeaseProvider{client: staticChatClient{}}
	runner := NewRunner(provider, "model", cfg, root, "", SubagentCallbacks{}, root).withRole("reviewer.logic")
	report, err := runner.executeSubagentLoopWithFormat(context.Background(), "subagent-test", string(TypeReviewer), "task", "system", tools.NewEmptyRegistry(), map[string]any{"type": "object"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.RouteNodeID != "node-a" {
		t.Fatalf("route node was not reported: %#v", report)
	}
	if len(provider.requests) != 1 || provider.requests[0].Role != "reviewer.logic" || provider.requests[0].Model != "model" {
		t.Fatalf("unexpected route request: %#v", provider.requests)
	}
}
