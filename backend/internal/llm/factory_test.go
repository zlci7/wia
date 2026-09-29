package llm

import (
	"os"
	"path/filepath"
	"testing"

	"gameagent/backend/internal/llm/fake"
)

func TestNewProviderFromConfigFileCreatesFakeProvider(t *testing.T) {
	path := writeConfig(t, `{
		"provider": "fake",
		"model": "fake"
	}`)

	provider, config, err := NewProviderFromConfigFile(path)
	if err != nil {
		t.Fatalf("NewProviderFromConfigFile failed: %v", err)
	}

	if _, ok := provider.(fake.Provider); !ok {
		t.Fatalf("provider = %T, want fake.Provider", provider)
	}
	if config.Provider != "fake" {
		t.Fatalf("provider config = %q, want fake", config.Provider)
	}
}

func TestNewProviderRequiresAPIKeyForDeepSeek(t *testing.T) {
	_, err := NewProvider(Config{
		Provider: "deepseek",
		Model:    "deepseek-v4-flash",
		APIKey:   "env:MISSING_DEEPSEEK_API_KEY_FOR_TEST",
	})
	if err == nil {
		t.Fatal("NewProvider succeeded, want missing api key error")
	}
}

func TestNewProviderRejectsInlineAPIKey(t *testing.T) {
	_, err := NewProvider(Config{
		Provider: "deepseek",
		Model:    "deepseek-v4-flash",
		APIKey:   "sk-do-not-put-real-keys-in-config",
	})
	if err == nil {
		t.Fatal("NewProvider succeeded, want inline api key error")
	}
}

func TestNewProviderReadsAPIKeyFromEnvReference(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY_FOR_TEST", "test-key")

	provider, err := NewProvider(Config{
		Provider: "deepseek",
		Model:    "deepseek-v4-flash",
		APIKey:   "env:DEEPSEEK_API_KEY_FOR_TEST",
	})
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	if provider == nil {
		t.Fatal("provider is nil")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "model.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	return path
}
