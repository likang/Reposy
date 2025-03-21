package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type RepositoryConfig struct {
	Type string
	Skip bool
	Raw  []byte
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

	return &config, nil
}
