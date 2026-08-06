package session_test

import (
	"os"
	"path/filepath"
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

func TestSessionManagerRejectsTraversalNames(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	badNames := []string{"../evil", "a/b", `a\b`, "..", ".", "has space"}
	for _, name := range badNames {
		if _, err := mgr.CreateSession(name, "model"); err == nil {
			t.Fatalf("expected CreateSession(%q) to fail", name)
		}
	}

	if _, _, _, err := mgr.LoadSession("../evil"); err == nil {
		t.Fatal("expected traversal load query to fail")
	}
}

func TestSessionManagerRejectsSymlinkSessionFile(t *testing.T) {
	dir := t.TempDir()
	mgr, err := session.NewManager(dir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("failed to create outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.jsonl")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	if _, _, _, err := mgr.LoadSession("linked"); err == nil {
		t.Fatal("expected symlink session file to be rejected")
	}
}

func TestSessionManagerRejectsSymlinkSessionsDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.Symlink(outside, sessionsDir); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	if _, err := session.NewManager(sessionsDir, root); err == nil {
		t.Fatal("expected symlinked sessions dir to be rejected")
	}
}

func TestExtractLastWorkingDirOnlyTrustsSystemMarkers(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0755); err != nil {
		t.Fatalf("failed to create inside dir: %v", err)
	}
	outside := t.TempDir()

	events := []session.Event{
		{Role: "system", Content: "📌 [Workspace Directory Updated]: " + inside},
		{Role: "assistant", Content: "Working directory successfully changed to '" + outside + "'"},
		{Role: "assistant", ToolCalls: []ollama.ToolCall{{
			Function: ollama.ToolCallFunction{
				Name:      "cd",
				Arguments: map[string]interface{}{"path": outside},
			},
		}}},
	}

	got := session.ExtractLastWorkingDir(events)
	if got != inside {
		t.Fatalf("expected trusted system marker dir %s, got %s", inside, got)
	}
}
