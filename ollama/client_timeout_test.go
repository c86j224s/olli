package ollama

import (
	"testing"
	"time"
)

func TestClientTimeoutDoesNotUndercutLongestRoleBudget(t *testing.T) {
	client := NewClient("http://localhost:11434")
	if client.HTTPClient.Timeout != 40*time.Minute {
		t.Fatalf("unexpected Ollama client timeout: %v", client.HTTPClient.Timeout)
	}
}
