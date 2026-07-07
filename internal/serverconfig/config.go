package serverconfig

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultHTTPAddr            = ":8080"
	DefaultTurnQueueSize       = 16
	DefaultTurnTimeoutMS       = 3600000
	DefaultLLMProvider         = "fake"
	DefaultLLMModel            = "fake-demo"
	DefaultLLMBaseURL          = "https://api.openai.com/v1"
	DefaultLLMAPIKeyEnv        = "TMA_LLM_API_KEY"
	DefaultContextWindowTokens = 128000
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	Turn        TurnConfig
	Context     ContextConfig
	LLM         LLMConfig
}

type TurnConfig struct {
	QueueSize     int
	Timeout       time.Duration
	TimeoutMillis int
}

type ContextConfig struct {
	DefaultWindowTokens int
}

type LLMConfig struct {
	Provider     string
	ProviderType string
	Model        string
	BaseURL      string
	APIKeyEnv    string
	APIKey       string
}

func Load(dotenvPath string) (Config, error) {
	if err := LoadDotEnv(dotenvPath); err != nil {
		return Config{}, err
	}
	return FromEnv()
}

func FromEnv() (Config, error) {
	config := Config{
		HTTPAddr:    envOrDefault("TMA_HTTP_ADDR", DefaultHTTPAddr),
		DatabaseURL: os.Getenv("TMA_DATABASE_URL"),
		Turn: TurnConfig{
			QueueSize:     envIntOrDefault("TMA_TURN_QUEUE_SIZE", DefaultTurnQueueSize),
			TimeoutMillis: envIntOrDefault("TMA_TURN_TIMEOUT_MS", DefaultTurnTimeoutMS),
		},
		Context: ContextConfig{
			DefaultWindowTokens: envIntOrDefault("TMA_DEFAULT_CONTEXT_WINDOW_TOKENS", DefaultContextWindowTokens),
		},
		LLM: LLMConfig{
			Provider:     envOrDefault("TMA_LLM_PROVIDER", DefaultLLMProvider),
			ProviderType: os.Getenv("TMA_LLM_PROVIDER_TYPE"),
			Model:        envOrDefault("TMA_LLM_MODEL", DefaultLLMModel),
			BaseURL:      envOrDefault("TMA_LLM_BASE_URL", DefaultLLMBaseURL),
			APIKeyEnv:    envOrDefault("TMA_LLM_API_KEY_ENV", DefaultLLMAPIKeyEnv),
		},
	}
	config.Turn.Timeout = time.Duration(config.Turn.TimeoutMillis) * time.Millisecond
	config.LLM.APIKey = os.Getenv(config.LLM.APIKeyEnv)

	if config.DatabaseURL == "" {
		return Config{}, errors.New("TMA_DATABASE_URL is required")
	}

	return config, nil
}

func LoadDotEnv(path string) error {
	if path == "" {
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("load dotenv %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// shell 中显式设置过的变量优先，.env 只补缺省值。
		if _, exists := os.LookupEnv(key); key == "" || exists {
			continue
		}

		value = strings.Trim(value, `"'`)
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set env from dotenv %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan dotenv %s: %w", path, err)
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func envIntOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
