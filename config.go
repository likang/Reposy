package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type RepositoryConfig struct {
	Type       string `json:"type"`
	Skip       bool	  `json:"skip"`
	Raw        []byte `json:"raw"`
	IgnoreCase *bool  `json:"ignore_case"`
}

func (repo *RepositoryConfig) UnmarshalJSON(data []byte) error {
	config := struct {
		Type string `json:"type"`
		Skip bool   `json:"skip"`
	}{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to unmarshal repository config: %w", err)
	}
	if config.Type == "s3" {
		repo.Type = config.Type
		repo.Skip = config.Skip
		repo.Raw = data
		return nil
	} else {
		return fmt.Errorf("unknown repository type: %s", config.Type)
	}
}

type Config struct {
	Version      int                          `json:"version"`
	SyncInterval int                          `json:"sync_interval"`
	Repositories map[string]*RepositoryConfig `json:"repositories"`
	S3           S3Config                     `json:"s3"`
	IgnoreCase   *bool                        `json:"ignore_case"`
}

func ConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	confDir := filepath.Join(homeDir, ".config")
	if err := os.MkdirAll(confDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}
	return filepath.Join(homeDir, ".config", "reposy.json"), nil
}

func LoadConfig() (*Config, error) {
	configPath, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	config.SyncInterval = max(config.SyncInterval, 10)
	if config.IgnoreCase == nil {
		// default true if running on macOS or Windows
		ignoreCase := false
		goos := runtime.GOOS
		if goos == "darwin" || goos == "windows" {
			ignoreCase = true
		}
		config.IgnoreCase = &ignoreCase
	}
	for _, repo := range config.Repositories {
		if (repo.IgnoreCase == nil) {
			repo.IgnoreCase = config.IgnoreCase
		}

	}

	return &config, nil
}
