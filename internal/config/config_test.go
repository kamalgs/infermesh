package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	yaml := `
nats:
  url: "nats://localhost:4222"

models:
  "gpt-4o":
    provider: openai
    upstream_model: "gpt-4o-2024-08-06"
  "llama3":
    provider: ollama
    upstream_model: "llama3:70b"

providers:
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "sk-test-key"
  ollama:
    base_url: "http://localhost:11434"
`
	path := writeTempFile(t, yaml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATS.URL != "nats://localhost:4222" {
		t.Errorf("nats.url: got %q", cfg.NATS.URL)
	}

	if len(cfg.Models) != 2 {
		t.Fatalf("models: got %d, want 2", len(cfg.Models))
	}

	gpt := cfg.Models["gpt-4o"]
	if gpt.Provider != "openai" {
		t.Errorf("gpt-4o provider: got %q", gpt.Provider)
	}
	if gpt.UpstreamModel != "gpt-4o-2024-08-06" {
		t.Errorf("gpt-4o upstream_model: got %q", gpt.UpstreamModel)
	}

	openai := cfg.Providers["openai"]
	if openai.APIKey != "sk-test-key" {
		t.Errorf("openai api_key: got %q", openai.APIKey)
	}
}

func TestLoadEnvExpansion(t *testing.T) {
	t.Setenv("TEST_API_KEY", "sk-from-env")
	t.Setenv("TEST_NATS_URL", "nats://custom:4222")

	yaml := `
nats:
  url: "${TEST_NATS_URL}"
providers:
  openai:
    base_url: "https://api.openai.com/v1"
    api_key: "${TEST_API_KEY}"
`
	path := writeTempFile(t, yaml)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.NATS.URL != "nats://custom:4222" {
		t.Errorf("nats.url: got %q, want nats://custom:4222", cfg.NATS.URL)
	}
	if cfg.Providers["openai"].APIKey != "sk-from-env" {
		t.Errorf("api_key: got %q, want sk-from-env", cfg.Providers["openai"].APIKey)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTempFile(t, `{{{invalid yaml`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
