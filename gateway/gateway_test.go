package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func fakeTransport(model string) http.RoundTripper {
	return roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload any
		switch request.URL.Path {
		case "/api/tags":
			payload = map[string]any{"models": []map[string]any{{"name": model}}}
		case "/api/chat":
			payload = ollama.ChatResponseChunk{Done: true, Message: ollama.Message{Role: "assistant", Content: "ok"}}
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewBufferString("not found")), Header: make(http.Header)}, nil
		}
		encoded, _ := json.Marshal(payload)
		encoded = append(encoded, '\n')
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(encoded)), Header: make(http.Header)}, nil
	})
}

func installFakeTransports(g *Gateway, models map[string]string) {
	for _, node := range g.nodes {
		node.client.HTTPClient.Transport = fakeTransport(models[node.id])
	}
}

func gatewayConfig(nodes ...config.AIGatewayNodeConfig) config.AIGatewayConfig {
	return config.AIGatewayConfig{Enabled: true, Strategy: config.AIGatewayStrategyLeastLoaded, HealthIntervalSeconds: 60, FailureThreshold: 2, CooldownSeconds: 60, Nodes: nodes}
}

func testNode(id, endpoint, model string, roles []string, concurrency int) config.AIGatewayNodeConfig {
	return config.AIGatewayNodeConfig{ID: id, Endpoint: endpoint, Models: []string{model}, Roles: roles, MaxConcurrency: concurrency, Weight: 100, InsecureAllowHTTP: true}
}

func TestGatewayRoutesByModelAndRole(t *testing.T) {
	g, err := New(gatewayConfig(
		testNode("coder", "https://coder.internal", "qwen:27b", []string{"coder"}, 1),
		testNode("reviewer", "https://reviewer.internal", "gemma:12b", []string{"reviewer"}, 2),
	))
	if err != nil {
		t.Fatal(err)
	}
	installFakeTransports(g, map[string]string{"coder": "qwen:27b", "reviewer": "gemma:12b"})
	g.Refresh(context.Background())
	lease, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer.logic", Model: "gemma:12b"})
	if err != nil {
		t.Fatal(err)
	}
	if lease.NodeID() != "reviewer" {
		t.Fatalf("routed to %q, want reviewer", lease.NodeID())
	}
	lease.Release(nil)
}

func TestGatewayHealthDiscoveryCannotBroadenConfiguredModels(t *testing.T) {
	g, err := New(gatewayConfig(testNode("limited", "https://limited.internal", "allowed", []string{"reviewer"}, 1)))
	if err != nil {
		t.Fatal(err)
	}
	installFakeTransports(g, map[string]string{"limited": "unexpected"})
	g.Refresh(context.Background())
	if _, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer", Model: "unexpected"}); err == nil {
		t.Fatal("health discovery broadened configured model allowlist")
	}
}

func TestGatewayMainRoleDoesNotUseSpecialistOnlyNode(t *testing.T) {
	g, err := New(gatewayConfig(
		testNode("main", "https://main.internal", "chat", []string{"main"}, 1),
		testNode("reviewer", "https://reviewer.internal", "chat", []string{"reviewer"}, 1),
	))
	if err != nil {
		t.Fatal(err)
	}
	installFakeTransports(g, map[string]string{"main": "chat", "reviewer": "chat"})
	lease, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "main", Model: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release(nil)
	if lease.NodeID() != "main" {
		t.Fatalf("main chat routed to specialist node %q", lease.NodeID())
	}
}

func TestGatewayLeastLoadedUsesAvailableNode(t *testing.T) {
	g, err := New(gatewayConfig(
		testNode("a", "https://a.internal", "gemma:12b", []string{"reviewer"}, 1),
		testNode("b", "https://b.internal", "gemma:12b", []string{"reviewer"}, 1),
	))
	if err != nil {
		t.Fatal(err)
	}
	installFakeTransports(g, map[string]string{"a": "gemma:12b", "b": "gemma:12b"})
	one, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer.logic", Model: "gemma:12b"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer.tests", Model: "gemma:12b"})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Release(nil)
	defer two.Release(nil)
	if one.NodeID() == two.NodeID() {
		t.Fatalf("both leases used saturated node %q", one.NodeID())
	}
}

func TestGatewayConcurrencyWaitsAndHonorsContext(t *testing.T) {
	g, err := New(gatewayConfig(testNode("only", "https://only.internal", "gemma:12b", []string{"reviewer"}, 1)))
	if err != nil {
		t.Fatal(err)
	}
	installFakeTransports(g, map[string]string{"only": "gemma:12b"})
	lease, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer", Model: "gemma:12b"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx, ollama.RouteRequest{Role: "reviewer", Model: "gemma:12b"}); err == nil {
		t.Fatal("expected saturated gateway acquire to time out")
	}
	lease.Release(nil)
}

func TestGatewayCircuitBreakerAndDrain(t *testing.T) {
	g, err := New(gatewayConfig(testNode("only", "https://only.internal", "gemma:12b", []string{"reviewer"}, 1)))
	if err != nil {
		t.Fatal(err)
	}
	installFakeTransports(g, map[string]string{"only": "gemma:12b"})
	for i := 0; i < 2; i++ {
		lease, acquireErr := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer", Model: "gemma:12b"})
		if acquireErr != nil {
			t.Fatal(acquireErr)
		}
		lease.Release(context.DeadlineExceeded)
	}
	if _, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer", Model: "gemma:12b"}); err == nil {
		t.Fatal("expected open circuit to reject route")
	}
	g.nodes[0].mu.Lock()
	g.nodes[0].circuitUntil = time.Time{}
	g.nodes[0].failures = 0
	g.nodes[0].mu.Unlock()
	if err := g.SetDrain("only", true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer", Model: "gemma:12b"}); err == nil {
		t.Fatal("expected drained node rejection")
	}
}

func TestGatewayRejectsUnsafeEndpointsAndDuplicateIDs(t *testing.T) {
	for _, endpoint := range []string{"file:///tmp/ollama", "http://example.com:11434/path", "http://user:pass@example.com:11434", "https://example.com:99999", "http://0.0.0.0:11434"} {
		_, err := New(gatewayConfig(testNode("bad", endpoint, "model", nil, 1)))
		if err == nil {
			t.Fatalf("unsafe endpoint %q was accepted", endpoint)
		}
	}
	n := testNode("same", "http://127.0.0.1:11434", "model", nil, 1)
	if _, err := New(gatewayConfig(n, n)); err == nil {
		t.Fatal("duplicate node ids were accepted")
	}
}

func TestGatewayRejectsUnsafeHeaderNames(t *testing.T) {
	t.Setenv("GATEWAY_SECRET", "secret")
	node := testNode("bad-header", "https://example.internal", "model", nil, 1)
	node.HeadersFromEnv = map[string]string{"X-Test\r\nInjected": "GATEWAY_SECRET"}
	if _, err := New(gatewayConfig(node)); err == nil {
		t.Fatal("unsafe header name was accepted")
	}
}

func TestGatewayConcurrentLeasesRemainBounded(t *testing.T) {
	g, err := New(gatewayConfig(testNode("only", "https://only.internal", "gemma:12b", []string{"reviewer"}, 2)))
	if err != nil {
		t.Fatal(err)
	}
	installFakeTransports(g, map[string]string{"only": "gemma:12b"})
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			lease, acquireErr := g.Acquire(context.Background(), ollama.RouteRequest{Role: "reviewer", Model: "gemma:12b"})
			if acquireErr != nil {
				t.Errorf("acquire: %v", acquireErr)
				return
			}
			time.Sleep(5 * time.Millisecond)
			lease.Release(nil)
		}()
	}
	close(start)
	wg.Wait()
	status := g.Status()[0]
	if status.Active != 0 || len(g.nodes[0].semaphore) != 0 {
		t.Fatalf("gateway leaked leases: %#v", status)
	}
}
