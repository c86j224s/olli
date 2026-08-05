package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	DefaultMode    string   `json:"default_mode"`
	NumCtx         int      `json:"num_ctx"`
	WhitelistTools []string `json:"whitelist_tools"`
	filePath       string
	mu             sync.RWMutex
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
		DefaultMode: "accept-edit",
		NumCtx:      32768,
		WhitelistTools: []string{
			"calculator",
			"get_current_time",
			"get_system_info",
			"search_session_history",
			"run_terminal_command",
			"cd",
			"change_directory",
			"view_file",
			"list_dir",
			"grep_search",
		},
		filePath: absPath,
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

	// Ensure default safe tools exist in whitelist
	for _, defaultTool := range []string{"cd", "change_directory", "run_terminal_command", "view_file", "list_dir", "grep_search"} {
		if !cfg.IsWhitelisted(defaultTool) {
			cfg.AddWhitelist(defaultTool)
		}
	}

	return cfg, nil
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
