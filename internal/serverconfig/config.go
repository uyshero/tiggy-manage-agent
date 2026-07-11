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
	DefaultHTTPAddr                                = ":8080"
	DefaultTurnQueueSize                           = 16
	DefaultTurnWorkerCount                         = 10
	DefaultTurnPollIntervalMS                      = 500
	DefaultTurnLeaseDurationMS                     = 10000
	DefaultTurnHeartbeatIntervalMS                 = 1000
	DefaultTurnTimeoutMS                           = 3600000
	DefaultMaxToolRounds                           = 0
	DefaultLLMProvider                             = "fake"
	DefaultLLMModel                                = "fake-demo"
	DefaultLLMBaseURL                              = "https://api.openai.com/v1"
	DefaultLLMAPIKeyEnv                            = "TMA_LLM_API_KEY"
	DefaultContextWindowTokens                     = 128000
	DefaultObjectStorageProvider                   = "localfs"
	DefaultObjectStorageEndpoint                   = "http://localhost:9000"
	DefaultObjectStorageRegion                     = "us-east-1"
	DefaultObjectStorageBucket                     = "tma-artifacts"
	DefaultObjectStorageRootDir                    = "/private/tmp/tma-object-store"
	DefaultObjectStorageAccessKeyEnv               = "TMA_OBJECT_STORAGE_ACCESS_KEY"
	DefaultObjectStorageSecretKeyEnv               = "TMA_OBJECT_STORAGE_SECRET_KEY"
	DefaultToolRuntime                             = "cloud_sandbox"
	DefaultCloudSandboxDataRoot                    = "/private/tmp/tma-cloud-sandbox-data"
	DefaultCloudSandboxDataTTLSec                  = 3600
	DefaultCloudSandboxContainerIdleTTLSec         = 1800
	DefaultCloudSandboxContainerMaxLifetimeSec     = 14400
	DefaultCloudSandboxContainerCleanupIntervalSec = 60
	DefaultCloudSandboxAllowNetwork                = true
	DefaultWorkerReaperEnabled                     = true
	DefaultWorkerReaperIntervalMS                  = 30000
	DefaultWorkerReaperLimit                       = 100
	DefaultWorkerWorkReaperEnabled                 = true
	DefaultWorkerWorkReaperIntervalMS              = 30000
	DefaultWorkerWorkReaperLimit                   = 100
	DefaultObservabilityRetryEnabled               = true
	DefaultObservabilityRetryIntervalMS            = 30000
	DefaultObservabilityRetryLimit                 = 20
	DefaultTraceIndexRetentionEnabled              = false
	DefaultTraceIndexRetentionDays                 = 30
	DefaultTraceIndexRetentionIntervalMS           = 3600000
	DefaultTraceIndexRetentionLimit                = 1000
)

type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	Turn          TurnConfig
	Context       ContextConfig
	LLM           LLMConfig
	ObjectStore   ObjectStorageConfig
	ToolRuntime   ToolRuntimeConfig
	Worker        WorkerConfig
	Observability ObservabilityConfig
}

type TurnConfig struct {
	QueueSize               int
	WorkerCount             int
	PollInterval            time.Duration
	PollIntervalMillis      int
	LeaseDuration           time.Duration
	LeaseDurationMillis     int
	HeartbeatInterval       time.Duration
	HeartbeatIntervalMillis int
	Timeout                 time.Duration
	TimeoutMillis           int
	MaxToolRounds           int
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

type ObjectStorageConfig struct {
	Provider     string
	Endpoint     string
	Region       string
	Bucket       string
	RootDir      string
	AccessKeyEnv string
	SecretKeyEnv string
	AccessKey    string
	SecretKey    string
	UsePathStyle bool
}

type ToolRuntimeConfig struct {
	Runtime                         string
	Root                            string
	Image                           string
	DataRoot                        string
	DataTTL                         time.Duration
	DataTTLSeconds                  int
	ContainerIdleTTL                time.Duration
	ContainerIdleTTLSeconds         int
	ContainerMaxLifetime            time.Duration
	ContainerMaxLifetimeSeconds     int
	ContainerCleanupInterval        time.Duration
	ContainerCleanupIntervalSeconds int
	AllowNetwork                    bool
	AllowLocalSystem                bool
}

type WorkerConfig struct {
	AuthToken        string
	ControlAuthToken string
	Reaper           WorkerReaperConfig
	WorkReaper       WorkerWorkReaperConfig
}

type WorkerReaperConfig struct {
	Enabled        bool
	Interval       time.Duration
	IntervalMillis int
	Limit          int
}

type WorkerWorkReaperConfig struct {
	Enabled        bool
	Interval       time.Duration
	IntervalMillis int
	Limit          int
}

type ObservabilityConfig struct {
	ExporterRetry       ObservabilityExporterRetryConfig
	TraceIndexRetention TraceIndexRetentionConfig
}

type ObservabilityExporterRetryConfig struct {
	Enabled        bool
	Interval       time.Duration
	IntervalMillis int
	Limit          int
}

type TraceIndexRetentionConfig struct {
	Enabled        bool
	Retention      time.Duration
	RetentionDays  int
	Interval       time.Duration
	IntervalMillis int
	Limit          int
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
			QueueSize:               envIntOrDefault("TMA_TURN_QUEUE_SIZE", DefaultTurnQueueSize),
			WorkerCount:             envIntOrDefault("TMA_TURN_WORKER_COUNT", DefaultTurnWorkerCount),
			PollIntervalMillis:      envIntOrDefault("TMA_TURN_POLL_INTERVAL_MS", DefaultTurnPollIntervalMS),
			LeaseDurationMillis:     envIntOrDefault("TMA_TURN_LEASE_DURATION_MS", DefaultTurnLeaseDurationMS),
			HeartbeatIntervalMillis: envIntOrDefault("TMA_TURN_HEARTBEAT_INTERVAL_MS", DefaultTurnHeartbeatIntervalMS),
			TimeoutMillis:           envIntOrDefault("TMA_TURN_TIMEOUT_MS", DefaultTurnTimeoutMS),
			MaxToolRounds:           envNonNegativeIntOrDefault("TMA_MAX_TOOL_ROUNDS", DefaultMaxToolRounds),
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
		ObjectStore: ObjectStorageConfig{
			Provider:     envOrDefault("TMA_OBJECT_STORAGE_PROVIDER", DefaultObjectStorageProvider),
			Endpoint:     envOrDefault("TMA_OBJECT_STORAGE_ENDPOINT", DefaultObjectStorageEndpoint),
			Region:       envOrDefault("TMA_OBJECT_STORAGE_REGION", DefaultObjectStorageRegion),
			Bucket:       envOrDefault("TMA_OBJECT_STORAGE_BUCKET", DefaultObjectStorageBucket),
			RootDir:      envOrDefault("TMA_OBJECT_STORAGE_ROOT_DIR", DefaultObjectStorageRootDir),
			AccessKeyEnv: envOrDefault("TMA_OBJECT_STORAGE_ACCESS_KEY_ENV", DefaultObjectStorageAccessKeyEnv),
			SecretKeyEnv: envOrDefault("TMA_OBJECT_STORAGE_SECRET_KEY_ENV", DefaultObjectStorageSecretKeyEnv),
			UsePathStyle: envBoolOrDefault("TMA_OBJECT_STORAGE_USE_PATH_STYLE", true),
		},
		ToolRuntime: ToolRuntimeConfig{
			Runtime:                         envOrDefault("TMA_TOOL_RUNTIME", DefaultToolRuntime),
			Root:                            os.Getenv("TMA_CLOUD_SANDBOX_ROOT"),
			Image:                           os.Getenv("TMA_CLOUD_SANDBOX_IMAGE"),
			DataRoot:                        envOrDefault("TMA_CLOUD_SANDBOX_DATA_ROOT", DefaultCloudSandboxDataRoot),
			DataTTLSeconds:                  envIntOrDefault("TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS", DefaultCloudSandboxDataTTLSec),
			ContainerIdleTTLSeconds:         envIntOrDefault("TMA_CLOUD_SANDBOX_CONTAINER_IDLE_TTL_SECONDS", DefaultCloudSandboxContainerIdleTTLSec),
			ContainerMaxLifetimeSeconds:     envIntOrDefault("TMA_CLOUD_SANDBOX_CONTAINER_MAX_LIFETIME_SECONDS", DefaultCloudSandboxContainerMaxLifetimeSec),
			ContainerCleanupIntervalSeconds: envIntOrDefault("TMA_CLOUD_SANDBOX_CONTAINER_CLEANUP_INTERVAL_SECONDS", DefaultCloudSandboxContainerCleanupIntervalSec),
			AllowNetwork:                    envBoolOrDefault("TMA_CLOUD_SANDBOX_ALLOW_NETWORK", DefaultCloudSandboxAllowNetwork),
			AllowLocalSystem:                envBoolOrDefault("TMA_ALLOW_SERVER_LOCAL_SYSTEM", false),
		},
		Worker: WorkerConfig{
			AuthToken:        os.Getenv("TMA_WORKER_AUTH_TOKEN"),
			ControlAuthToken: os.Getenv("TMA_WORKER_CONTROL_AUTH_TOKEN"),
			Reaper: WorkerReaperConfig{
				Enabled:        envBoolOrDefault("TMA_WORKER_REAPER_ENABLED", DefaultWorkerReaperEnabled),
				IntervalMillis: envIntOrDefault("TMA_WORKER_REAPER_INTERVAL_MS", DefaultWorkerReaperIntervalMS),
				Limit:          envIntOrDefault("TMA_WORKER_REAPER_LIMIT", DefaultWorkerReaperLimit),
			},
			WorkReaper: WorkerWorkReaperConfig{
				Enabled:        envBoolOrDefault("TMA_WORKER_WORK_REAPER_ENABLED", DefaultWorkerWorkReaperEnabled),
				IntervalMillis: envIntOrDefault("TMA_WORKER_WORK_REAPER_INTERVAL_MS", DefaultWorkerWorkReaperIntervalMS),
				Limit:          envIntOrDefault("TMA_WORKER_WORK_REAPER_LIMIT", DefaultWorkerWorkReaperLimit),
			},
		},
		Observability: ObservabilityConfig{
			ExporterRetry: ObservabilityExporterRetryConfig{
				Enabled:        envBoolOrDefault("TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_ENABLED", DefaultObservabilityRetryEnabled),
				IntervalMillis: envIntOrDefault("TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_INTERVAL_MS", DefaultObservabilityRetryIntervalMS),
				Limit:          envIntOrDefault("TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_LIMIT", DefaultObservabilityRetryLimit),
			},
			TraceIndexRetention: TraceIndexRetentionConfig{
				Enabled:        envBoolOrDefault("TMA_TRACE_INDEX_RETENTION_ENABLED", DefaultTraceIndexRetentionEnabled),
				RetentionDays:  envIntOrDefault("TMA_TRACE_INDEX_RETENTION_DAYS", DefaultTraceIndexRetentionDays),
				IntervalMillis: envIntOrDefault("TMA_TRACE_INDEX_RETENTION_INTERVAL_MS", DefaultTraceIndexRetentionIntervalMS),
				Limit:          envIntOrDefault("TMA_TRACE_INDEX_RETENTION_LIMIT", DefaultTraceIndexRetentionLimit),
			},
		},
	}
	config.Turn.Timeout = time.Duration(config.Turn.TimeoutMillis) * time.Millisecond
	config.Turn.PollInterval = time.Duration(config.Turn.PollIntervalMillis) * time.Millisecond
	config.Turn.LeaseDuration = time.Duration(config.Turn.LeaseDurationMillis) * time.Millisecond
	config.Turn.HeartbeatInterval = time.Duration(config.Turn.HeartbeatIntervalMillis) * time.Millisecond
	config.ToolRuntime.DataTTL = time.Duration(config.ToolRuntime.DataTTLSeconds) * time.Second
	config.ToolRuntime.ContainerIdleTTL = time.Duration(config.ToolRuntime.ContainerIdleTTLSeconds) * time.Second
	config.ToolRuntime.ContainerMaxLifetime = time.Duration(config.ToolRuntime.ContainerMaxLifetimeSeconds) * time.Second
	config.ToolRuntime.ContainerCleanupInterval = time.Duration(config.ToolRuntime.ContainerCleanupIntervalSeconds) * time.Second
	config.Worker.Reaper.Interval = time.Duration(config.Worker.Reaper.IntervalMillis) * time.Millisecond
	config.Worker.WorkReaper.Interval = time.Duration(config.Worker.WorkReaper.IntervalMillis) * time.Millisecond
	config.Observability.ExporterRetry.Interval = time.Duration(config.Observability.ExporterRetry.IntervalMillis) * time.Millisecond
	config.Observability.TraceIndexRetention.Retention = time.Duration(config.Observability.TraceIndexRetention.RetentionDays) * 24 * time.Hour
	config.Observability.TraceIndexRetention.Interval = time.Duration(config.Observability.TraceIndexRetention.IntervalMillis) * time.Millisecond
	config.LLM.APIKey = os.Getenv(config.LLM.APIKeyEnv)
	config.ObjectStore.AccessKey = os.Getenv(config.ObjectStore.AccessKeyEnv)
	config.ObjectStore.SecretKey = os.Getenv(config.ObjectStore.SecretKeyEnv)

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

func envNonNegativeIntOrDefault(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}
