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
	return cfg, nil
}

func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(c.filePath, data, 0644)
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
	for _, t := range c.WhitelistTools {
		if t == toolName {
			c.mu.Unlock()
			return nil // Already whitelisted
		}
	}
	c.WhitelistTools = append(c.WhitelistTools, toolName)
	c.mu.Unlock()

	return c.Save()
}

func (c *Config) RemoveWhitelist(toolName string) error {
	c.mu.Lock()
	newList := make([]string, 0, len(c.WhitelistTools))
	found := false
	for _, t := range c.WhitelistTools {
		if t == toolName {
			found = true
			continue
		}
		newList = append(newList, t)
	}
	if found {
		c.WhitelistTools = newList
	}
	c.mu.Unlock()

	if found {
		return c.Save()
	}
	return nil
}

func (c *Config) GetFilePath() string {
	return c.filePath
}
