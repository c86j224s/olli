package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

type clientRoundTripFunc func(*http.Request) (*http.Response, error)

func (f clientRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientAppliesConfiguredHeadersToModelsAndChat(t *testing.T) {
	client := NewClient("https://ollama.internal")
	client.Headers = http.Header{"Authorization": []string{"Bearer secret"}}
	seen := make(map[string]string)
	client.HTTPClient.Transport = clientRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen[request.URL.Path] = request.Header.Get("Authorization")
		var payload any
		if request.URL.Path == "/api/tags" {
			payload = map[string]any{"models": []map[string]any{{"name": "model"}}}
		} else {
			payload = ChatResponseChunk{Done: true, Message: Message{Role: "assistant", Content: "{}"}}
		}
		encoded, _ := json.Marshal(payload)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encoded))}, nil
	})
	if _, err := client.ListModelsWithContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ChatStreamFullWithContext(context.Background(), ChatRequest{Model: "model", Format: map[string]any{"type": "object"}}, StreamCallbacks{}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/tags", "/api/chat"} {
		if seen[path] != "Bearer secret" {
			t.Fatalf("header not applied to %s: %q", path, seen[path])
		}
	}
}
