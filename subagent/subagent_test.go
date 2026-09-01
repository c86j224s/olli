package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

func TestSubagentRunnerAllTypes(t *testing.T) {
	tempDir := t.TempDir()

	client := ollama.NewClient("http://localhost:11434")
	cfg, _ := config.LoadConfig(filepath.Join(tempDir, "config.json"))

	runner := NewRunner(client, "qwen3.5:0.8b", cfg, tempDir, "", SubagentCallbacks{})

	if runner == nil {
		t.Fatalf("expected non-nil runner")
	}
}

func TestSubagentRoleRegistryStartsEmpty(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0755); err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	client := ollama.NewClient("http://localhost:11434")
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	runner := NewRunner(client, "qwen3.5:0.8b", cfg, workspace, "", SubagentCallbacks{}, root)
	reg := runner.newRoleRegistry()
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("failed to canonicalize root: %v", err)
	}
	if runner.GetWorkspaceRoot() != wantRoot {
		t.Fatalf("expected runner root %s, got %s", wantRoot, runner.GetWorkspaceRoot())
	}
	if _, err := reg.Execute("execute_action", map[string]interface{}{"action": "pwd"}); err == nil {
		t.Fatal("expected base role registry not to inherit terminal action tool")
	}
}

func TestValidateResultArtifactsRequiresPresenterHTML(t *testing.T) {
	root := t.TempDir()
	report := &ResultReport{
		Type:   string(TypePresenter),
		Status: "SUCCESS",
	}

	if err := ValidateResultArtifacts(report, root); err == nil {
		t.Fatal("expected presenter report without HTML artifact to fail")
	}

	htmlPath := filepath.Join(root, "deck.html")
	if err := os.WriteFile(htmlPath, []byte("<!doctype html><title>Deck</title>"), 0600); err != nil {
		t.Fatalf("failed to create temp HTML artifact: %v", err)
	}
	report.ArtifactFiles = []string{htmlPath}
	if err := ValidateResultArtifacts(report, root); err != nil {
		t.Fatalf("expected valid HTML artifact to pass: %v", err)
	}

	mdPath := filepath.Join(root, "deck.md")
	if err := os.WriteFile(mdPath, []byte("# Deck"), 0600); err != nil {
		t.Fatalf("failed to create temp Markdown artifact: %v", err)
	}
	report.ArtifactFiles = []string{mdPath}
	if err := ValidateResultArtifacts(report, root); err == nil {
		t.Fatal("expected wrong artifact extension to fail")
	}
}

func TestValidateResultArtifactsRejectsSymlinkArtifactTempOnly(t *testing.T) {
	root := t.TempDir()
	realArtifact := filepath.Join(root, "real.html")
	if err := os.WriteFile(realArtifact, []byte("<!doctype html>"), 0600); err != nil {
		t.Fatalf("failed to create temp real artifact: %v", err)
	}
	linkPath := filepath.Join(root, "linked.html")
	if err := os.Symlink(realArtifact, linkPath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	report := &ResultReport{
		Type:          string(TypePresenter),
		Status:        "SUCCESS",
		ArtifactFiles: []string{linkPath},
	}
	if err := ValidateResultArtifacts(report, root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink artifact to fail, got: %v", err)
	}
}

func TestArtifactCandidatePathTracksNewAndExistingFiles(t *testing.T) {
	root := t.TempDir()
	args := map[string]interface{}{"file_path": "deck.html"}
	path, existed, ok := artifactCandidatePath(args, root, root)
	if !ok {
		t.Fatal("expected artifact candidate path")
	}
	if existed {
		t.Fatal("expected missing file to be reported as newly creatable")
	}

	if err := os.WriteFile(path, []byte("<!doctype html>"), 0600); err != nil {
		t.Fatalf("failed to create temp artifact: %v", err)
	}
	gotPath, existed, ok := artifactCandidatePath(args, root, root)
	if !ok || !existed || gotPath != path {
		t.Fatalf("expected existing artifact candidate %s, got path=%s existed=%v ok=%v", path, gotPath, existed, ok)
	}
}

func TestSubagentLogCreationRejectsSymlinkTempOnly(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "sessions", "subagents")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("failed to create subagent output dir: %v", err)
	}

	outsideLog := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outsideLog, []byte("{}\n"), 0600); err != nil {
		t.Fatalf("failed to create temp outside log: %v", err)
	}
	subID := "subagent_presenter_20000101_000000"
	if err := os.Symlink(outsideLog, filepath.Join(outputDir, subID+".jsonl")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	runner := &SubagentRunner{
		client:        ollama.NewClient("http://localhost:11434"),
		model:         "qwen3.5:0.8b",
		outputDir:     outputDir,
		workspace:     root,
		workspaceRoot: root,
	}
	_, err := runner.executeSubagentLoopWithContext(context.Background(), subID, string(TypePresenter), "task", "system", tools.NewEmptyRegistry())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink log rejection before LLM call, got: %v", err)
	}
}

func TestNewRunnerRejectsSymlinkSubagentOutputDirTempOnly(t *testing.T) {
	root := t.TempDir()
	realSessions := filepath.Join(root, "real-sessions")
	if err := os.Mkdir(realSessions, 0755); err != nil {
		t.Fatalf("failed to create temp real sessions dir: %v", err)
	}
	if err := os.Symlink(realSessions, filepath.Join(root, "sessions")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	client := ollama.NewClient("http://localhost:11434")
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	runner := NewRunner(client, "qwen3.5:0.8b", cfg, root, "", SubagentCallbacks{}, root)
	if runner.outputDir != "" {
		t.Fatalf("expected symlinked subagent output dir to be rejected, got %s", runner.outputDir)
	}
}
