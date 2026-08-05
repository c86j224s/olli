package session_test

import (
	"os"
	"testing"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
)

func TestSessionManager(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "session_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr, err := session.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create session manager: %v", err)
	}

	info, err := mgr.CreateSession("test_session", "qwen3.5:0.8b")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	if info.ID != "test_session" {
		t.Errorf("expected session ID 'test_session', got '%s'", info.ID)
	}

	err = mgr.AppendEvent(ollama.Message{
		Role:    "user",
		Content: "Hello world",
	})
	if err != nil {
		t.Fatalf("failed to append event: %v", err)
	}

	messages, resolvedID, _, err := mgr.LoadSession("test_session")
	if err != nil {
		t.Fatalf("failed to load session: %v", err)
	}

	if resolvedID != "test_session" {
		t.Errorf("expected resolved ID 'test_session', got '%s'", resolvedID)
	}

	if len(messages) != 1 {
		t.Errorf("expected 1 message in session, got %d", len(messages))
	}
}
