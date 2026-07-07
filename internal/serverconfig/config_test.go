package serverconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var configEnvKeys = []string{
	"TMA_HTTP_ADDR",
	"TMA_DATABASE_URL",
	"TMA_TURN_QUEUE_SIZE",
	"TMA_TURN_TIMEOUT_MS",
	"TMA_DEFAULT_CONTEXT_WINDOW_TOKENS",
	"TMA_LLM_PROVIDER",
	"TMA_LLM_PROVIDER_TYPE",
	"TMA_LLM_MODEL",
	"TMA_LLM_BASE_URL",
	"TMA_LLM_API_KEY_ENV",
	"TMA_LLM_API_KEY",
	"TMA_LLM_API_KEY_CUSTOM",
}

func TestFromEnvUsesDefaults(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}

	if config.HTTPAddr != DefaultHTTPAddr {
		t.Fatalf("expected default http addr %q, got %q", DefaultHTTPAddr, config.HTTPAddr)
	}
	if config.Turn.QueueSize != DefaultTurnQueueSize {
		t.Fatalf("expected default queue size %d, got %d", DefaultTurnQueueSize, config.Turn.QueueSize)
	}
	if config.LLM.Provider != DefaultLLMProvider {
		t.Fatalf("expected default llm provider %q, got %q", DefaultLLMProvider, config.LLM.Provider)
	}
	if config.Context.DefaultWindowTokens != DefaultContextWindowTokens {
		t.Fatalf("expected default context window tokens %d, got %d", DefaultContextWindowTokens, config.Context.DefaultWindowTokens)
	}
	if config.LLM.Model != DefaultLLMModel {
		t.Fatalf("expected default llm model %q, got %q", DefaultLLMModel, config.LLM.Model)
	}
	if config.LLM.BaseURL != DefaultLLMBaseURL {
		t.Fatalf("expected default llm base url %q, got %q", DefaultLLMBaseURL, config.LLM.BaseURL)
	}
	if config.LLM.APIKeyEnv != DefaultLLMAPIKeyEnv {
		t.Fatalf("expected default llm api key env %q, got %q", DefaultLLMAPIKeyEnv, config.LLM.APIKeyEnv)
	}
}

func TestLoadReadsDotEnv(t *testing.T) {
	clearConfigEnv(t)
	path := writeDotEnv(t, `
TMA_HTTP_ADDR=:18080
TMA_DATABASE_URL=postgres://dotenv
TMA_TURN_TIMEOUT_MS=1234
TMA_DEFAULT_CONTEXT_WINDOW_TOKENS=4096
TMA_LLM_PROVIDER=fake
TMA_LLM_PROVIDER_TYPE=openai
TMA_LLM_MODEL=fake-dotenv
TMA_LLM_BASE_URL=http://dotenv.example/v1
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY_CUSTOM
TMA_LLM_API_KEY_CUSTOM=dotenv-key
`)

	config, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if config.HTTPAddr != ":18080" {
		t.Fatalf("expected dotenv http addr, got %q", config.HTTPAddr)
	}
	if config.DatabaseURL != "postgres://dotenv" {
		t.Fatalf("expected dotenv database url, got %q", config.DatabaseURL)
	}
	if config.Turn.Timeout != 1234*time.Millisecond {
		t.Fatalf("expected dotenv turn timeout 1234ms, got %s", config.Turn.Timeout)
	}
	if config.Context.DefaultWindowTokens != 4096 {
		t.Fatalf("expected dotenv context window tokens 4096, got %d", config.Context.DefaultWindowTokens)
	}
	if config.LLM.Provider != "fake" || config.LLM.Model != "fake-dotenv" {
		t.Fatalf("expected dotenv llm config, got provider=%q model=%q", config.LLM.Provider, config.LLM.Model)
	}
	if config.LLM.ProviderType != "openai" {
		t.Fatalf("expected dotenv llm provider type, got %q", config.LLM.ProviderType)
	}
	if config.LLM.BaseURL != "http://dotenv.example/v1" || config.LLM.APIKey != "dotenv-key" {
		t.Fatalf("expected dotenv llm transport config, got base_url=%q api_key=%q", config.LLM.BaseURL, config.LLM.APIKey)
	}
	if config.LLM.APIKeyEnv != "TMA_LLM_API_KEY_CUSTOM" {
		t.Fatalf("expected dotenv llm api key env, got %q", config.LLM.APIKeyEnv)
	}
}

func TestLoadKeepsShellEnvPrecedence(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://shell")
	t.Setenv("TMA_TURN_TIMEOUT_MS", "5678")
	t.Setenv("TMA_LLM_MODEL", "fake-shell")
	t.Setenv("TMA_LLM_PROVIDER_TYPE", "shell-type")
	t.Setenv("TMA_LLM_API_KEY_ENV", "TMA_LLM_API_KEY_CUSTOM")
	t.Setenv("TMA_LLM_API_KEY_CUSTOM", "shell-key")
	path := writeDotEnv(t, `
TMA_DATABASE_URL=postgres://dotenv
TMA_TURN_TIMEOUT_MS=1234
TMA_LLM_MODEL=fake-dotenv
TMA_LLM_PROVIDER_TYPE=dotenv-type
TMA_LLM_API_KEY=dotenv-key
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
`)

	config, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if config.DatabaseURL != "postgres://shell" {
		t.Fatalf("expected shell env to win, got %q", config.DatabaseURL)
	}
	if config.Turn.Timeout != 5678*time.Millisecond {
		t.Fatalf("expected shell turn timeout to win, got %s", config.Turn.Timeout)
	}
	if config.LLM.Model != "fake-shell" {
		t.Fatalf("expected shell llm model to win, got %q", config.LLM.Model)
	}
	if config.LLM.ProviderType != "shell-type" {
		t.Fatalf("expected shell llm provider type to win, got %q", config.LLM.ProviderType)
	}
	if config.LLM.APIKey != "shell-key" {
		t.Fatalf("expected shell llm api key to win, got %q", config.LLM.APIKey)
	}
	if config.LLM.APIKeyEnv != "TMA_LLM_API_KEY_CUSTOM" {
		t.Fatalf("expected shell llm api key env to win, got %q", config.LLM.APIKeyEnv)
	}
}

func TestFromEnvParsesLLMConfig(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_LLM_PROVIDER", "fake")
	t.Setenv("TMA_LLM_PROVIDER_TYPE", "openai")
	t.Setenv("TMA_LLM_MODEL", "fake-custom")
	t.Setenv("TMA_LLM_BASE_URL", "http://custom.example/v1")
	t.Setenv("TMA_LLM_API_KEY_ENV", "TMA_LLM_API_KEY_CUSTOM")
	t.Setenv("TMA_LLM_API_KEY_CUSTOM", "custom-key")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}

	if config.LLM.Provider != "fake" {
		t.Fatalf("expected llm provider fake, got %q", config.LLM.Provider)
	}
	if config.LLM.ProviderType != "openai" {
		t.Fatalf("expected llm provider type openai, got %q", config.LLM.ProviderType)
	}
	if config.LLM.Model != "fake-custom" {
		t.Fatalf("expected llm model fake-custom, got %q", config.LLM.Model)
	}
	if config.LLM.BaseURL != "http://custom.example/v1" {
		t.Fatalf("expected llm base url custom, got %q", config.LLM.BaseURL)
	}
	if config.LLM.APIKey != "custom-key" {
		t.Fatalf("expected llm api key custom, got %q", config.LLM.APIKey)
	}
	if config.LLM.APIKeyEnv != "TMA_LLM_API_KEY_CUSTOM" {
		t.Fatalf("expected llm api key env custom, got %q", config.LLM.APIKeyEnv)
	}
}

func TestFromEnvParsesTurnTimeout(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_TURN_TIMEOUT_MS", "1234")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}

	if config.Turn.Timeout != 1234*time.Millisecond {
		t.Fatalf("expected turn timeout 1234ms, got %s", config.Turn.Timeout)
	}
}

func TestFromEnvInvalidIntUsesDefault(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_TURN_QUEUE_SIZE", "nope")
	t.Setenv("TMA_TURN_TIMEOUT_MS", "-1")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}

	if config.Turn.QueueSize != DefaultTurnQueueSize {
		t.Fatalf("expected default queue size, got %d", config.Turn.QueueSize)
	}
	if config.Turn.TimeoutMillis != DefaultTurnTimeoutMS {
		t.Fatalf("expected default turn timeout millis, got %d", config.Turn.TimeoutMillis)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()

	original := make(map[string]*string)
	for _, key := range configEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			copied := value
			original[key] = &copied
		} else {
			original[key] = nil
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

	t.Cleanup(func() {
		for _, key := range configEnvKeys {
			value := original[key]
			if value == nil {
				_ = os.Unsetenv(key)
				continue
			}
			_ = os.Setenv(key, *value)
		}
	})
}

func writeDotEnv(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write dotenv: %v", err)
	}
	return path
}
