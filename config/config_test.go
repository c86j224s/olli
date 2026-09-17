package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c86j224s/olli/config"
)

func TestConfigWhitelistManagement(t *testing.T) {
	tempDir := t.TempDir()

	cfgPath := filepath.Join(tempDir, "config.json")
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if !cfg.IsWhitelisted("calculator") {
		t.Errorf("expected calculator to be whitelisted by default")
	}
	if cfg.DefaultMode != "ask" {
		t.Fatalf("expected default mode ask, got %s", cfg.DefaultMode)
	}
	for _, highRisk := range []string{"execute_action", "cd", "change_directory"} {
		if cfg.IsWhitelisted(highRisk) {
			t.Fatalf("expected high-risk tool %s not to be whitelisted by default", highRisk)
		}
	}
	if cfg.IsWhitelisted("image_generate") {
		t.Fatal("expected image_generate not to be whitelisted by default")
	}
	if cfg.IsWhitelisted("audio_generate") {
		t.Fatal("expected audio_generate not to be whitelisted by default")
	}
	if !cfg.IsWhitelisted("inspect_image") {
		t.Fatal("expected read-only inspect_image to be whitelisted by default")
	}
	if cfg.ImageGeneration.ComfyUI.Endpoint != "http://127.0.0.1:8188" {
		t.Fatalf("expected default ComfyUI endpoint, got %s", cfg.ImageGeneration.ComfyUI.Endpoint)
	}
	if !cfg.ImageGeneration.Enabled {
		t.Fatal("expected image generation to be enabled by default")
	}
	if cfg.ImageGeneration.ComfyUI.OutputDir != "artifacts/images" {
		t.Fatalf("expected default image output dir, got %s", cfg.ImageGeneration.ComfyUI.OutputDir)
	}
	if cfg.ImageGeneration.ComfyUI.TimeoutSeconds != 300 {
		t.Fatalf("expected default image timeout 300, got %d", cfg.ImageGeneration.ComfyUI.TimeoutSeconds)
	}
	if cfg.ImageGeneration.ComfyUI.MaxImageBytes != 67108864 {
		t.Fatalf("expected default max image bytes, got %d", cfg.ImageGeneration.ComfyUI.MaxImageBytes)
	}
	if len(cfg.ImageGeneration.ComfyUI.Workflows) != 0 {
		t.Fatalf("expected no default ComfyUI workflows, got %d", len(cfg.ImageGeneration.ComfyUI.Workflows))
	}
	if cfg.ImageInspection.Ollama.Endpoint != "http://127.0.0.1:11434" {
		t.Fatalf("expected default Ollama inspection endpoint, got %s", cfg.ImageInspection.Ollama.Endpoint)
	}
	if !cfg.ImageInspection.Enabled {
		t.Fatal("expected image inspection to be enabled by default")
	}
	if cfg.ImageInspection.Ollama.Model != "gemma4:12b" {
		t.Fatalf("expected default inspection model gemma4:12b, got %s", cfg.ImageInspection.Ollama.Model)
	}
	if cfg.ImageInspection.Ollama.TimeoutSeconds != 300 {
		t.Fatalf("expected default inspection timeout 300, got %d", cfg.ImageInspection.Ollama.TimeoutSeconds)
	}
	if cfg.ImageInspection.Ollama.MaxImageBytes != 67108864 {
		t.Fatalf("expected default inspection max bytes, got %d", cfg.ImageInspection.Ollama.MaxImageBytes)
	}
	if cfg.Presenter.DefaultTemplate != config.PresenterTemplateAuto || cfg.Presenter.MaxSlides != 10 {
		t.Fatalf("unexpected Presenter defaults: %#v", cfg.Presenter)
	}
	if cfg.AudioGeneration.ACEStep.Endpoint != "http://127.0.0.1:8001" {
		t.Fatalf("expected default ACE-Step endpoint, got %s", cfg.AudioGeneration.ACEStep.Endpoint)
	}
	if !cfg.AudioGeneration.Enabled {
		t.Fatal("expected audio generation to be enabled by default")
	}
	if cfg.AudioGeneration.ACEStep.OutputDir != "artifacts/audio" {
		t.Fatalf("expected default audio output dir, got %s", cfg.AudioGeneration.ACEStep.OutputDir)
	}
	if cfg.AudioGeneration.ACEStep.TimeoutSeconds != 900 {
		t.Fatalf("expected default audio timeout 900, got %d", cfg.AudioGeneration.ACEStep.TimeoutSeconds)
	}
	if cfg.AudioGeneration.ACEStep.MaxAudioBytes != 209715200 {
		t.Fatalf("expected default max audio bytes, got %d", cfg.AudioGeneration.ACEStep.MaxAudioBytes)
	}
	if cfg.AudioGeneration.ACEStep.PollIntervalMS != 1000 {
		t.Fatalf("expected default audio poll interval 1000, got %d", cfg.AudioGeneration.ACEStep.PollIntervalMS)
	}
	if cfg.AudioGeneration.ACEStep.MaxDurationSeconds != 600 {
		t.Fatalf("expected default max duration 600, got %d", cfg.AudioGeneration.ACEStep.MaxDurationSeconds)
	}

	// Add new tool to whitelist
	if err := cfg.AddWhitelist("execute_action"); err != nil {
		t.Fatalf("failed to add whitelist: %v", err)
	}

	if !cfg.IsWhitelisted("execute_action") {
		t.Errorf("expected execute_action to be whitelisted after add")
	}

	// Reload config from file to test persistence
	reloaded, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if !reloaded.IsWhitelisted("execute_action") {
		t.Errorf("expected reloaded config to retain execute_action in whitelist")
	}

	// Remove tool from whitelist
	if err := cfg.RemoveWhitelist("execute_action"); err != nil {
		t.Fatalf("failed to remove whitelist: %v", err)
	}
	if cfg.IsWhitelisted("execute_action") {
		t.Errorf("expected execute_action to be removed from whitelist")
	}
}

func TestConfigImageGenerationDefaultsFilledForPartialConfig(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"default_mode":"ask","num_ctx":4096,"whitelist_tools":["calculator"],"image_generation":{"comfyui":{"workflows":{"fast":{"path":"workflow.json"}}}}}`), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.ImageGeneration.ComfyUI.Endpoint != "http://127.0.0.1:8188" {
		t.Fatalf("expected default endpoint to be filled, got %s", cfg.ImageGeneration.ComfyUI.Endpoint)
	}
	if !cfg.ImageGeneration.Enabled {
		t.Fatal("expected omitted image generation enabled flag to preserve legacy enabled behavior")
	}
	if cfg.ImageGeneration.ComfyUI.OutputDir != "artifacts/images" {
		t.Fatalf("expected default output dir to be filled, got %s", cfg.ImageGeneration.ComfyUI.OutputDir)
	}
	if cfg.ImageGeneration.ComfyUI.TimeoutSeconds != 300 {
		t.Fatalf("expected default timeout to be filled, got %d", cfg.ImageGeneration.ComfyUI.TimeoutSeconds)
	}
	if cfg.ImageGeneration.ComfyUI.MaxImageBytes != 67108864 {
		t.Fatalf("expected default max bytes to be filled, got %d", cfg.ImageGeneration.ComfyUI.MaxImageBytes)
	}
	if cfg.ImageGeneration.ComfyUI.Workflows["fast"].Path != "workflow.json" {
		t.Fatalf("expected configured workflow to be preserved")
	}
}

func TestConfigImageInspectionDefaultsFilledForPartialConfig(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"default_mode":"ask","num_ctx":4096,"whitelist_tools":["calculator"],"image_inspection":{"ollama":{"model":"custom-vision"}}}`), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.ImageInspection.Ollama.Endpoint != "http://127.0.0.1:11434" {
		t.Fatalf("expected default inspection endpoint, got %s", cfg.ImageInspection.Ollama.Endpoint)
	}
	if !cfg.ImageInspection.Enabled {
		t.Fatal("expected omitted image inspection enabled flag to preserve legacy enabled behavior")
	}
	if cfg.ImageInspection.Ollama.Model != "custom-vision" {
		t.Fatalf("expected configured inspection model to be preserved, got %s", cfg.ImageInspection.Ollama.Model)
	}
	if cfg.ImageInspection.Ollama.TimeoutSeconds != 300 {
		t.Fatalf("expected default inspection timeout, got %d", cfg.ImageInspection.Ollama.TimeoutSeconds)
	}
	if cfg.ImageInspection.Ollama.MaxImageBytes != 67108864 {
		t.Fatalf("expected default inspection max bytes, got %d", cfg.ImageInspection.Ollama.MaxImageBytes)
	}
}

func TestConfigAudioGenerationDefaultsFilledForPartialConfig(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"default_mode":"ask","num_ctx":4096,"whitelist_tools":["calculator"],"audio_generation":{"ace_step":{"endpoint":"http://localhost:8001"}}}`), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.AudioGeneration.ACEStep.Endpoint != "http://localhost:8001" {
		t.Fatalf("expected configured ACE-Step endpoint to be preserved, got %s", cfg.AudioGeneration.ACEStep.Endpoint)
	}
	if !cfg.AudioGeneration.Enabled {
		t.Fatal("expected omitted audio generation enabled flag to preserve legacy enabled behavior")
	}
	if cfg.AudioGeneration.ACEStep.OutputDir != "artifacts/audio" {
		t.Fatalf("expected default audio output dir to be filled, got %s", cfg.AudioGeneration.ACEStep.OutputDir)
	}
	if cfg.AudioGeneration.ACEStep.TimeoutSeconds != 900 {
		t.Fatalf("expected default audio timeout to be filled, got %d", cfg.AudioGeneration.ACEStep.TimeoutSeconds)
	}
	if cfg.AudioGeneration.ACEStep.MaxAudioBytes != 209715200 {
		t.Fatalf("expected default max audio bytes to be filled, got %d", cfg.AudioGeneration.ACEStep.MaxAudioBytes)
	}
	if cfg.AudioGeneration.ACEStep.PollIntervalMS != 1000 {
		t.Fatalf("expected default poll interval to be filled, got %d", cfg.AudioGeneration.ACEStep.PollIntervalMS)
	}
	if cfg.AudioGeneration.ACEStep.MaxDurationSeconds != 600 {
		t.Fatalf("expected default max duration to be filled, got %d", cfg.AudioGeneration.ACEStep.MaxDurationSeconds)
	}
}

func TestConfigMediaToolsCanBeDisabled(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	data := []byte(`{
		"default_mode":"ask",
		"num_ctx":4096,
		"whitelist_tools":["calculator"],
		"image_generation":{"enabled":false},
		"image_inspection":{"enabled":false},
		"audio_generation":{"enabled":false}
	}`)
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.ImageGeneration.Enabled || cfg.ImageInspection.Enabled || cfg.AudioGeneration.Enabled {
		t.Fatal("expected all media tools to remain disabled")
	}
	if cfg.IsWhitelisted("inspect_image") {
		t.Fatal("expected disabled image inspection not to be added to the default whitelist")
	}
}

func TestDevelopmentTeamConfigUsesConfiguredModelsAndFallback(t *testing.T) {
	cfg := config.DevelopmentTeamConfig{
		PlannerModel: "gemma4:e4b", ReviewerModel: "gemma4:12b", LogicReviewerModel: "qwen3.8:27b",
	}.WithFallback("fallback")
	if cfg.PlannerModel != "gemma4:e4b" || cfg.ReviewerModel != "gemma4:12b" || cfg.CoderModel != "fallback" || cfg.TestCoderModel != "fallback" || cfg.TesterModel != "fallback" {
		t.Fatalf("unexpected development team config: %#v", cfg)
	}
	if cfg.CassandraModel != "gemma4:12b" || cfg.DetailPlannerModel != "gemma4:e4b" {
		t.Fatalf("unexpected planning role fallback: %#v", cfg)
	}
	if cfg.RequirementReviewerModel != "gemma4:12b" || cfg.LogicReviewerModel != "qwen3.8:27b" || cfg.SafetyReviewerModel != "gemma4:12b" || cfg.TestReviewerModel != "gemma4:12b" {
		t.Fatalf("unexpected specialist reviewer fallback: %#v", cfg)
	}
}

func TestSubagentModelConfigUsesRoleModelAndFallback(t *testing.T) {
	thinking := false
	configured := (config.SubagentModelConfig{Model: "gemma4:12b", Thinking: &thinking}).WithFallback("fallback")
	if configured.Model != "gemma4:12b" || configured.Thinking == nil || *configured.Thinking {
		t.Fatalf("configured role was not preserved: %#v", configured)
	}
	fallback := (config.SubagentModelConfig{}).WithFallback("qwen3.8:27b")
	if fallback.Model != "qwen3.8:27b" || fallback.Thinking != nil {
		t.Fatalf("role fallback is wrong: %#v", fallback)
	}
}

func TestPresenterConfigNormalizesInvalidValues(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"presenter":{"default_template":"invented","max_slides":99}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Presenter.DefaultTemplate != config.PresenterTemplateAuto || cfg.Presenter.MaxSlides != 10 {
		t.Fatalf("invalid Presenter config was not normalized: %#v", cfg.Presenter)
	}
}

func TestConfigInvalidDefaultModeFallsBackToAsk(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"default_mode":"invalid","num_ctx":4096,"whitelist_tools":["calculator"]}`), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.DefaultMode != "ask" {
		t.Fatalf("expected invalid default mode to fall back to ask, got %s", cfg.DefaultMode)
	}
}
