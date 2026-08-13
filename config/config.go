package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	DefaultMode     string                `json:"default_mode"`
	NumCtx          int                   `json:"num_ctx"`
	WhitelistTools  []string              `json:"whitelist_tools"`
	ImageGeneration ImageGenerationConfig `json:"image_generation"`
	ImageInspection ImageInspectionConfig `json:"image_inspection"`
	AudioGeneration AudioGenerationConfig `json:"audio_generation"`
	filePath        string
	mu              sync.RWMutex
}

type ImageGenerationConfig struct {
	ComfyUI ComfyUIConfig `json:"comfyui"`
}

type ComfyUIConfig struct {
	Endpoint       string                           `json:"endpoint"`
	OutputDir      string                           `json:"output_dir"`
	TimeoutSeconds int                              `json:"timeout_seconds"`
	MaxImageBytes  int64                            `json:"max_image_bytes"`
	Workflows      map[string]ComfyUIWorkflowConfig `json:"workflows"`
}

type ComfyUIWorkflowConfig struct {
	Path                 string `json:"path"`
	PromptNodeID         string `json:"prompt_node_id"`
	PromptInput          string `json:"prompt_input"`
	NegativePromptNodeID string `json:"negative_prompt_node_id"`
	NegativePromptInput  string `json:"negative_prompt_input"`
	WidthNodeID          string `json:"width_node_id"`
	WidthInput           string `json:"width_input"`
	HeightNodeID         string `json:"height_node_id"`
	HeightInput          string `json:"height_input"`
	StepsNodeID          string `json:"steps_node_id"`
	StepsInput           string `json:"steps_input"`
	SeedNodeID           string `json:"seed_node_id"`
	SeedInput            string `json:"seed_input"`
}

type ImageInspectionConfig struct {
	Ollama OllamaImageInspectionConfig `json:"ollama"`
}

type OllamaImageInspectionConfig struct {
	Endpoint       string `json:"endpoint"`
	Model          string `json:"model"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxImageBytes  int64  `json:"max_image_bytes"`
}

type AudioGenerationConfig struct {
	ACEStep ACEStepConfig `json:"ace_step"`
}

type ACEStepConfig struct {
	Endpoint           string `json:"endpoint"`
	OutputDir          string `json:"output_dir"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	MaxAudioBytes      int64  `json:"max_audio_bytes"`
	PollIntervalMS     int    `json:"poll_interval_ms"`
	MaxDurationSeconds int    `json:"max_duration_seconds"`
}

func LoadConfig(filePath string) (*Config, error) {
	if filePath == "" {
		filePath = "./config.json"
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("invalid config path: %w", err)
	}

	cfg := &Config{
		DefaultMode: "ask",
		NumCtx:      32768,
		WhitelistTools: []string{
			"calculator",
			"get_current_time",
			"get_system_info",
			"get_agent_status",
			"search_session_history",
			"inspect_image",
			"view_file",
			"list_dir",
			"grep_search",
		},
		ImageGeneration: DefaultImageGenerationConfig(),
		ImageInspection: DefaultImageInspectionConfig(),
		AudioGeneration: DefaultAudioGenerationConfig(),
		filePath:        absPath,
	}

	// If config file does not exist, create default config.json
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("failed to create initial config.json: %w", err)
		}
		return cfg, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config.json: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config.json: %w", err)
	}

	cfg.filePath = absPath
	cfg.DefaultMode = safeDefaultMode(cfg.DefaultMode)
	cfg.ImageGeneration = normalizeImageGenerationConfig(cfg.ImageGeneration)
	cfg.ImageInspection = normalizeImageInspectionConfig(cfg.ImageInspection)
	cfg.AudioGeneration = normalizeAudioGenerationConfig(cfg.AudioGeneration)

	// Ensure default safe tools exist in whitelist
	for _, defaultTool := range []string{"view_file", "list_dir", "grep_search", "get_agent_status", "inspect_image"} {
		if !cfg.IsWhitelisted(defaultTool) {
			cfg.AddWhitelist(defaultTool)
		}
	}

	return cfg, nil
}

func DefaultImageGenerationConfig() ImageGenerationConfig {
	return ImageGenerationConfig{
		ComfyUI: ComfyUIConfig{
			Endpoint:       "http://127.0.0.1:8188",
			OutputDir:      "artifacts/images",
			TimeoutSeconds: 300,
			MaxImageBytes:  67108864,
			Workflows:      map[string]ComfyUIWorkflowConfig{},
		},
	}
}

func DefaultImageInspectionConfig() ImageInspectionConfig {
	return ImageInspectionConfig{
		Ollama: OllamaImageInspectionConfig{
			Endpoint:       "http://127.0.0.1:11434",
			Model:          "gemma4:12b",
			TimeoutSeconds: 300,
			MaxImageBytes:  67108864,
		},
	}
}

func DefaultAudioGenerationConfig() AudioGenerationConfig {
	return AudioGenerationConfig{
		ACEStep: ACEStepConfig{
			Endpoint:           "http://127.0.0.1:8001",
			OutputDir:          "artifacts/audio",
			TimeoutSeconds:     900,
			MaxAudioBytes:      209715200,
			PollIntervalMS:     1000,
			MaxDurationSeconds: 600,
		},
	}
}

func normalizeImageGenerationConfig(cfg ImageGenerationConfig) ImageGenerationConfig {
	defaults := DefaultImageGenerationConfig()
	if cfg.ComfyUI.Endpoint == "" {
		cfg.ComfyUI.Endpoint = defaults.ComfyUI.Endpoint
	}
	if cfg.ComfyUI.OutputDir == "" {
		cfg.ComfyUI.OutputDir = defaults.ComfyUI.OutputDir
	}
	if cfg.ComfyUI.TimeoutSeconds <= 0 {
		cfg.ComfyUI.TimeoutSeconds = defaults.ComfyUI.TimeoutSeconds
	}
	if cfg.ComfyUI.MaxImageBytes <= 0 {
		cfg.ComfyUI.MaxImageBytes = defaults.ComfyUI.MaxImageBytes
	}
	if cfg.ComfyUI.Workflows == nil {
		cfg.ComfyUI.Workflows = map[string]ComfyUIWorkflowConfig{}
	}
	return cfg
}

func normalizeImageInspectionConfig(cfg ImageInspectionConfig) ImageInspectionConfig {
	defaults := DefaultImageInspectionConfig()
	if cfg.Ollama.Endpoint == "" {
		cfg.Ollama.Endpoint = defaults.Ollama.Endpoint
	}
	if cfg.Ollama.Model == "" {
		cfg.Ollama.Model = defaults.Ollama.Model
	}
	if cfg.Ollama.TimeoutSeconds <= 0 {
		cfg.Ollama.TimeoutSeconds = defaults.Ollama.TimeoutSeconds
	}
	if cfg.Ollama.MaxImageBytes <= 0 {
		cfg.Ollama.MaxImageBytes = defaults.Ollama.MaxImageBytes
	}
	return cfg
}

func normalizeAudioGenerationConfig(cfg AudioGenerationConfig) AudioGenerationConfig {
	defaults := DefaultAudioGenerationConfig()
	if cfg.ACEStep.Endpoint == "" {
		cfg.ACEStep.Endpoint = defaults.ACEStep.Endpoint
	}
	if cfg.ACEStep.OutputDir == "" {
		cfg.ACEStep.OutputDir = defaults.ACEStep.OutputDir
	}
	if cfg.ACEStep.TimeoutSeconds <= 0 {
		cfg.ACEStep.TimeoutSeconds = defaults.ACEStep.TimeoutSeconds
	}
	if cfg.ACEStep.MaxAudioBytes <= 0 {
		cfg.ACEStep.MaxAudioBytes = defaults.ACEStep.MaxAudioBytes
	}
	if cfg.ACEStep.PollIntervalMS <= 0 {
		cfg.ACEStep.PollIntervalMS = defaults.ACEStep.PollIntervalMS
	}
	if cfg.ACEStep.MaxDurationSeconds <= 0 {
		cfg.ACEStep.MaxDurationSeconds = defaults.ACEStep.MaxDurationSeconds
	}
	return cfg
}

func safeDefaultMode(mode string) string {
	switch mode {
	case "ask", "accept-edit", "auto":
		return mode
	default:
		return "ask"
	}
}

func (c *Config) IsWhitelisted(toolName string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, t := range c.WhitelistTools {
		if t == toolName {
			return true
		}
	}
	return false
}

func (c *Config) AddWhitelist(toolName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if toolName == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	for _, t := range c.WhitelistTools {
		if t == toolName {
			return nil // already whitelisted
		}
	}

	c.WhitelistTools = append(c.WhitelistTools, toolName)
	return c.saveUnlocked()
}

func (c *Config) RemoveWhitelist(toolName string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	newTools := make([]string, 0, len(c.WhitelistTools))
	found := false
	for _, t := range c.WhitelistTools {
		if t == toolName {
			found = true
			continue
		}
		newTools = append(newTools, t)
	}

	if !found {
		return fmt.Errorf("tool '%s' is not in whitelist", toolName)
	}

	c.WhitelistTools = newTools
	return c.saveUnlocked()
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveUnlocked()
}

func (c *Config) saveUnlocked() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(c.filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	return nil
}
