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
}

func TestLoadReadsDotEnv(t *testing.T) {
	clearConfigEnv(t)
	path := writeDotEnv(t, `
TMA_HTTP_ADDR=:18080
TMA_DATABASE_URL=postgres://dotenv
TMA_TURN_TIMEOUT_MS=1234
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
}

func TestLoadKeepsShellEnvPrecedence(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://shell")
	t.Setenv("TMA_TURN_TIMEOUT_MS", "5678")
	path := writeDotEnv(t, `
TMA_DATABASE_URL=postgres://dotenv
TMA_TURN_TIMEOUT_MS=1234
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
