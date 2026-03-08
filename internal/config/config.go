package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	NATS      NATSConfig               `yaml:"nats"`
	Models    map[string]ModelConfig    `yaml:"models"`
	Providers map[string]ProviderConfig `yaml:"providers"`
}

type NATSConfig struct {
	URL string `yaml:"url"`
}

type ModelConfig struct {
	Provider      string `yaml:"provider"`
	UpstreamModel string `yaml:"upstream_model"`
}

type ProviderConfig struct {
	BaseURL string `yaml:"base_url"`
	APIKey  string `yaml:"api_key"`
}

// Load reads a YAML config file and expands environment variables.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand ${ENV_VAR} references
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}
