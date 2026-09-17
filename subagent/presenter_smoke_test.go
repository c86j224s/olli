package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
)

func TestPresenterTemplateSmoke(t *testing.T) {
	if os.Getenv("OLLI_MODEL_SMOKE") != "1" {
		t.Skip("set OLLI_MODEL_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	cfg, err := config.LoadConfig(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.NumCtx = 16384
	cfg.Presenter = config.PresenterConfig{DefaultTemplate: config.PresenterTemplateAuto, MaxSlides: 8}
	thinking := false
	model := os.Getenv("OLLI_PRESENTER_MODEL")
	if model == "" {
		model = "gemma4:12b"
	}
	runner := NewRunner(ollama.NewClient("http://127.0.0.1:11434"), model, cfg, root, "", SubagentCallbacks{
		OnModelHeartbeat: func(role string, elapsed time.Duration) { t.Logf("%s still generating after %s", role, elapsed) },
	}, root).WithThinking(thinking)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	report, err := runner.RunPresenterWithTemplateContext(ctx, "Create a 6-slide engineering presentation about bounded multi-agent software delivery. Cover layered planning, static preflight, specialist review, safe execution, Counter and Tetris evidence, and a concise conclusion. Do not invent metrics.", config.PresenterTemplateTechnicalEditorial)
	if err != nil {
		t.Fatalf("Presenter smoke failed: %v", err)
	}
	if report.Status != "SUCCESS" {
		t.Fatalf("Presenter returned %s: %s", report.Status, report.Summary)
	}
	if err := ValidateResultArtifacts(report, root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(report.ArtifactFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	if !strings.Contains(html, "theme-technical") || !strings.Contains(html, `role="progressbar"`) || strings.Contains(html, "<script src=") {
		t.Fatalf("Presenter generated an invalid deck: %s", html)
	}
}
