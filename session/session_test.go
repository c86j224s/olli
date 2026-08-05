package session_test

import (
	"os"
	"testing"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/session"
)

func TestSessionManagerAndRename(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "session_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr, err := session.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to init session manager: %v", err)
	}

	info, err := mgr.CreateSession("my_coding_session", "qwen3.5:0.8b")
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	if info.ID != "my_coding_session" {
		t.Errorf("expected session ID my_coding_session, got %s", info.ID)
	}

	msg1 := ollama.Message{Role: "user", Content: "Hello session test"}
	if err := mgr.AppendEvent(msg1); err != nil {
		t.Fatalf("append msg1 failed: %v", err)
	}

	// Rename active session
	newID, err := mgr.RenameSession("my_coding_session", "project_alpha")
	if err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if newID != "project_alpha" {
		t.Errorf("expected project_alpha, got %s", newID)
	}

	// Load by fuzzy name/substring "alpha"
	loadedMsgs, resolvedID, err := mgr.LoadSession("alpha")
	if err != nil {
		t.Fatalf("failed to load by fuzzy name: %v", err)
	}
	if resolvedID != "project_alpha" {
		t.Errorf("expected resolved ID project_alpha, got %s", resolvedID)
	}
	if len(loadedMsgs) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(loadedMsgs))
	}
}
