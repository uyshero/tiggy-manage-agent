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
	"TMA_OBJECT_STORAGE_PROVIDER",
	"TMA_OBJECT_STORAGE_ENDPOINT",
	"TMA_OBJECT_STORAGE_REGION",
	"TMA_OBJECT_STORAGE_BUCKET",
	"TMA_OBJECT_STORAGE_ROOT_DIR",
	"TMA_OBJECT_STORAGE_ACCESS_KEY_ENV",
	"TMA_OBJECT_STORAGE_SECRET_KEY_ENV",
	"TMA_OBJECT_STORAGE_ACCESS_KEY",
	"TMA_OBJECT_STORAGE_SECRET_KEY",
	"TMA_OBJECT_STORAGE_ACCESS_KEY_CUSTOM",
	"TMA_OBJECT_STORAGE_SECRET_KEY_CUSTOM",
	"TMA_OBJECT_STORAGE_USE_PATH_STYLE",
	"TMA_TOOL_RUNTIME",
	"TMA_CLOUD_SANDBOX_ROOT",
	"TMA_CLOUD_SANDBOX_IMAGE",
	"TMA_CLOUD_SANDBOX_DATA_ROOT",
	"TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS",
	"TMA_CLOUD_SANDBOX_ALLOW_NETWORK",
	"TMA_ALLOW_SERVER_LOCAL_SYSTEM",
	"TMA_WORKER_AUTH_TOKEN",
	"TMA_WORKER_CONTROL_AUTH_TOKEN",
	"TMA_WORKER_REAPER_ENABLED",
	"TMA_WORKER_REAPER_INTERVAL_MS",
	"TMA_WORKER_REAPER_LIMIT",
	"TMA_WORKER_WORK_REAPER_ENABLED",
	"TMA_WORKER_WORK_REAPER_INTERVAL_MS",
	"TMA_WORKER_WORK_REAPER_LIMIT",
	"TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_ENABLED",
	"TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_INTERVAL_MS",
	"TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_LIMIT",
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
	if config.ObjectStore.Provider != DefaultObjectStorageProvider {
		t.Fatalf("expected default object storage provider %q, got %q", DefaultObjectStorageProvider, config.ObjectStore.Provider)
	}
	if config.ObjectStore.Endpoint != DefaultObjectStorageEndpoint {
		t.Fatalf("expected default object storage endpoint %q, got %q", DefaultObjectStorageEndpoint, config.ObjectStore.Endpoint)
	}
	if config.ObjectStore.Bucket != DefaultObjectStorageBucket {
		t.Fatalf("expected default object storage bucket %q, got %q", DefaultObjectStorageBucket, config.ObjectStore.Bucket)
	}
	if config.ObjectStore.RootDir != DefaultObjectStorageRootDir {
		t.Fatalf("expected default object storage root dir %q, got %q", DefaultObjectStorageRootDir, config.ObjectStore.RootDir)
	}
	if !config.ObjectStore.UsePathStyle {
		t.Fatal("expected object storage path-style URLs by default")
	}
	if config.ToolRuntime.Runtime != DefaultToolRuntime {
		t.Fatalf("expected default tool runtime %q, got %q", DefaultToolRuntime, config.ToolRuntime.Runtime)
	}
	if config.ToolRuntime.Root != "" || config.ToolRuntime.Image != "" {
		t.Fatalf("expected empty default cloud sandbox root/image, got %+v", config.ToolRuntime)
	}
	if config.ToolRuntime.DataRoot != DefaultCloudSandboxDataRoot {
		t.Fatalf("expected default cloud sandbox data root %q, got %q", DefaultCloudSandboxDataRoot, config.ToolRuntime.DataRoot)
	}
	if config.ToolRuntime.DataTTLSeconds != DefaultCloudSandboxDataTTLSec {
		t.Fatalf("expected default cloud sandbox data ttl seconds %d, got %d", DefaultCloudSandboxDataTTLSec, config.ToolRuntime.DataTTLSeconds)
	}
	if config.ToolRuntime.DataTTL != time.Duration(DefaultCloudSandboxDataTTLSec)*time.Second {
		t.Fatalf("expected default cloud sandbox data ttl %s, got %s", time.Duration(DefaultCloudSandboxDataTTLSec)*time.Second, config.ToolRuntime.DataTTL)
	}
	if config.ToolRuntime.AllowNetwork != DefaultCloudSandboxAllowNetwork {
		t.Fatalf("expected default cloud sandbox allow network %t, got %t", DefaultCloudSandboxAllowNetwork, config.ToolRuntime.AllowNetwork)
	}
	if config.ToolRuntime.AllowLocalSystem {
		t.Fatal("expected server local_system fallback to be disabled by default")
	}
	if config.Worker.AuthToken != "" {
		t.Fatalf("expected empty default worker auth token, got %q", config.Worker.AuthToken)
	}
	if config.Worker.ControlAuthToken != "" {
		t.Fatalf("expected empty default worker control auth token, got %q", config.Worker.ControlAuthToken)
	}
	if config.Worker.Reaper.Enabled != DefaultWorkerReaperEnabled {
		t.Fatalf("expected default worker reaper enabled %t, got %t", DefaultWorkerReaperEnabled, config.Worker.Reaper.Enabled)
	}
	if config.Worker.Reaper.IntervalMillis != DefaultWorkerReaperIntervalMS {
		t.Fatalf("expected default worker reaper interval ms %d, got %d", DefaultWorkerReaperIntervalMS, config.Worker.Reaper.IntervalMillis)
	}
	if config.Worker.Reaper.Interval != time.Duration(DefaultWorkerReaperIntervalMS)*time.Millisecond {
		t.Fatalf("expected default worker reaper interval %s, got %s", time.Duration(DefaultWorkerReaperIntervalMS)*time.Millisecond, config.Worker.Reaper.Interval)
	}
	if config.Worker.Reaper.Limit != DefaultWorkerReaperLimit {
		t.Fatalf("expected default worker reaper limit %d, got %d", DefaultWorkerReaperLimit, config.Worker.Reaper.Limit)
	}
	if config.Worker.WorkReaper.Enabled != DefaultWorkerWorkReaperEnabled {
		t.Fatalf("expected default worker work reaper enabled %t, got %t", DefaultWorkerWorkReaperEnabled, config.Worker.WorkReaper.Enabled)
	}
	if config.Worker.WorkReaper.IntervalMillis != DefaultWorkerWorkReaperIntervalMS {
		t.Fatalf("expected default worker work reaper interval ms %d, got %d", DefaultWorkerWorkReaperIntervalMS, config.Worker.WorkReaper.IntervalMillis)
	}
	if config.Worker.WorkReaper.Interval != time.Duration(DefaultWorkerWorkReaperIntervalMS)*time.Millisecond {
		t.Fatalf("expected default worker work reaper interval %s, got %s", time.Duration(DefaultWorkerWorkReaperIntervalMS)*time.Millisecond, config.Worker.WorkReaper.Interval)
	}
	if config.Worker.WorkReaper.Limit != DefaultWorkerWorkReaperLimit {
		t.Fatalf("expected default worker work reaper limit %d, got %d", DefaultWorkerWorkReaperLimit, config.Worker.WorkReaper.Limit)
	}
	if !config.Observability.ExporterRetry.Enabled {
		t.Fatal("expected observability exporter retry worker to be enabled by default")
	}
	if config.Observability.ExporterRetry.IntervalMillis != DefaultObservabilityRetryIntervalMS {
		t.Fatalf("expected default observability retry interval %d, got %d", DefaultObservabilityRetryIntervalMS, config.Observability.ExporterRetry.IntervalMillis)
	}
	if config.Observability.ExporterRetry.Interval != time.Duration(DefaultObservabilityRetryIntervalMS)*time.Millisecond {
		t.Fatalf("expected default observability retry interval duration, got %s", config.Observability.ExporterRetry.Interval)
	}
	if config.Observability.ExporterRetry.Limit != DefaultObservabilityRetryLimit {
		t.Fatalf("expected default observability retry limit %d, got %d", DefaultObservabilityRetryLimit, config.Observability.ExporterRetry.Limit)
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
TMA_OBJECT_STORAGE_ENDPOINT=http://object-store.local:9000
TMA_OBJECT_STORAGE_REGION=cn-local-1
TMA_OBJECT_STORAGE_BUCKET=tma-dotenv
TMA_OBJECT_STORAGE_ROOT_DIR=/data/object-store
TMA_OBJECT_STORAGE_ACCESS_KEY_ENV=TMA_OBJECT_STORAGE_ACCESS_KEY_CUSTOM
TMA_OBJECT_STORAGE_SECRET_KEY_ENV=TMA_OBJECT_STORAGE_SECRET_KEY_CUSTOM
TMA_OBJECT_STORAGE_ACCESS_KEY_CUSTOM=object-access
TMA_OBJECT_STORAGE_SECRET_KEY_CUSTOM=object-secret
TMA_OBJECT_STORAGE_USE_PATH_STYLE=false
TMA_TOOL_RUNTIME=cloud_sandbox
TMA_CLOUD_SANDBOX_ROOT=/workspace/tma
TMA_CLOUD_SANDBOX_IMAGE=onlyboxes/tma-tool-sandbox:test
TMA_CLOUD_SANDBOX_DATA_ROOT=/tmp/tma-sandbox-data
TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS=1800
TMA_CLOUD_SANDBOX_ALLOW_NETWORK=true
TMA_ALLOW_SERVER_LOCAL_SYSTEM=true
TMA_WORKER_AUTH_TOKEN=worker-dotenv-token
TMA_WORKER_CONTROL_AUTH_TOKEN=worker-control-dotenv-token
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
	if config.ObjectStore.Endpoint != "http://object-store.local:9000" || config.ObjectStore.Bucket != "tma-dotenv" {
		t.Fatalf("expected dotenv object store endpoint/bucket, got endpoint=%q bucket=%q", config.ObjectStore.Endpoint, config.ObjectStore.Bucket)
	}
	if config.ObjectStore.Region != "cn-local-1" {
		t.Fatalf("expected dotenv object store region, got %q", config.ObjectStore.Region)
	}
	if config.ObjectStore.RootDir != "/data/object-store" {
		t.Fatalf("expected dotenv object store root dir, got %q", config.ObjectStore.RootDir)
	}
	if config.ObjectStore.AccessKey != "object-access" || config.ObjectStore.SecretKey != "object-secret" {
		t.Fatalf("expected dotenv object store credentials, got access=%q secret=%q", config.ObjectStore.AccessKey, config.ObjectStore.SecretKey)
	}
	if config.ObjectStore.UsePathStyle {
		t.Fatal("expected dotenv object store path-style override false")
	}
	if config.ToolRuntime.Runtime != "cloud_sandbox" || config.ToolRuntime.Root != "/workspace/tma" || config.ToolRuntime.Image != "onlyboxes/tma-tool-sandbox:test" {
		t.Fatalf("expected dotenv tool runtime config, got %+v", config.ToolRuntime)
	}
	if config.ToolRuntime.DataRoot != "/tmp/tma-sandbox-data" {
		t.Fatalf("expected dotenv cloud sandbox data root, got %q", config.ToolRuntime.DataRoot)
	}
	if config.ToolRuntime.DataTTLSeconds != 1800 || config.ToolRuntime.DataTTL != 30*time.Minute {
		t.Fatalf("expected dotenv cloud sandbox data ttl 1800s, got seconds=%d ttl=%s", config.ToolRuntime.DataTTLSeconds, config.ToolRuntime.DataTTL)
	}
	if !config.ToolRuntime.AllowNetwork {
		t.Fatal("expected dotenv cloud sandbox allow network override true")
	}
	if !config.ToolRuntime.AllowLocalSystem {
		t.Fatal("expected dotenv server local_system fallback override true")
	}
	if config.Worker.AuthToken != "worker-dotenv-token" {
		t.Fatalf("expected dotenv worker auth token, got %q", config.Worker.AuthToken)
	}
	if config.Worker.ControlAuthToken != "worker-control-dotenv-token" {
		t.Fatalf("expected dotenv worker control auth token, got %q", config.Worker.ControlAuthToken)
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
	t.Setenv("TMA_OBJECT_STORAGE_BUCKET", "shell-bucket")
	t.Setenv("TMA_OBJECT_STORAGE_ROOT_DIR", "/var/lib/shell-object-store")
	path := writeDotEnv(t, `
TMA_DATABASE_URL=postgres://dotenv
TMA_TURN_TIMEOUT_MS=1234
TMA_LLM_MODEL=fake-dotenv
TMA_LLM_PROVIDER_TYPE=dotenv-type
TMA_LLM_API_KEY=dotenv-key
TMA_LLM_API_KEY_ENV=TMA_LLM_API_KEY
TMA_OBJECT_STORAGE_BUCKET=dotenv-bucket
TMA_OBJECT_STORAGE_ROOT_DIR=/var/lib/dotenv-object-store
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
	if config.ObjectStore.Bucket != "shell-bucket" {
		t.Fatalf("expected shell object storage bucket to win, got %q", config.ObjectStore.Bucket)
	}
	if config.ObjectStore.RootDir != "/var/lib/shell-object-store" {
		t.Fatalf("expected shell object storage root dir to win, got %q", config.ObjectStore.RootDir)
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

func TestFromEnvParsesBackgroundWorkerConfig(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_WORKER_REAPER_ENABLED", "false")
	t.Setenv("TMA_WORKER_REAPER_INTERVAL_MS", "1250")
	t.Setenv("TMA_WORKER_REAPER_LIMIT", "15")
	t.Setenv("TMA_WORKER_WORK_REAPER_ENABLED", "false")
	t.Setenv("TMA_WORKER_WORK_REAPER_INTERVAL_MS", "1500")
	t.Setenv("TMA_WORKER_WORK_REAPER_LIMIT", "25")
	t.Setenv("TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_ENABLED", "false")
	t.Setenv("TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_INTERVAL_MS", "1750")
	t.Setenv("TMA_OBSERVABILITY_EXPORTER_RETRY_WORKER_LIMIT", "35")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}

	if config.Worker.Reaper.Enabled {
		t.Fatal("expected worker reaper disabled")
	}
	if config.Worker.Reaper.IntervalMillis != 1250 {
		t.Fatalf("expected worker reaper interval ms 1250, got %d", config.Worker.Reaper.IntervalMillis)
	}
	if config.Worker.Reaper.Interval != 1250*time.Millisecond {
		t.Fatalf("expected worker reaper interval 1250ms, got %s", config.Worker.Reaper.Interval)
	}
	if config.Worker.Reaper.Limit != 15 {
		t.Fatalf("expected worker reaper limit 15, got %d", config.Worker.Reaper.Limit)
	}
	if config.Worker.WorkReaper.Enabled {
		t.Fatal("expected worker work reaper disabled")
	}
	if config.Worker.WorkReaper.IntervalMillis != 1500 {
		t.Fatalf("expected worker work reaper interval ms 1500, got %d", config.Worker.WorkReaper.IntervalMillis)
	}
	if config.Worker.WorkReaper.Interval != 1500*time.Millisecond {
		t.Fatalf("expected worker work reaper interval 1500ms, got %s", config.Worker.WorkReaper.Interval)
	}
	if config.Worker.WorkReaper.Limit != 25 {
		t.Fatalf("expected worker work reaper limit 25, got %d", config.Worker.WorkReaper.Limit)
	}
	if config.Observability.ExporterRetry.Enabled {
		t.Fatal("expected observability exporter retry worker disabled")
	}
	if config.Observability.ExporterRetry.IntervalMillis != 1750 {
		t.Fatalf("expected observability retry interval ms 1750, got %d", config.Observability.ExporterRetry.IntervalMillis)
	}
	if config.Observability.ExporterRetry.Interval != 1750*time.Millisecond {
		t.Fatalf("expected observability retry interval 1750ms, got %s", config.Observability.ExporterRetry.Interval)
	}
	if config.Observability.ExporterRetry.Limit != 35 {
		t.Fatalf("expected observability retry limit 35, got %d", config.Observability.ExporterRetry.Limit)
	}
}

func TestFromEnvParsesObjectStorageConfig(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_OBJECT_STORAGE_PROVIDER", "localfs")
	t.Setenv("TMA_OBJECT_STORAGE_ENDPOINT", "http://rustfs.local:9000")
	t.Setenv("TMA_OBJECT_STORAGE_REGION", "local")
	t.Setenv("TMA_OBJECT_STORAGE_BUCKET", "tma-local")
	t.Setenv("TMA_OBJECT_STORAGE_ROOT_DIR", "/data/object-store")
	t.Setenv("TMA_OBJECT_STORAGE_ACCESS_KEY_ENV", "TMA_OBJECT_STORAGE_ACCESS_KEY_CUSTOM")
	t.Setenv("TMA_OBJECT_STORAGE_SECRET_KEY_ENV", "TMA_OBJECT_STORAGE_SECRET_KEY_CUSTOM")
	t.Setenv("TMA_OBJECT_STORAGE_ACCESS_KEY_CUSTOM", "access")
	t.Setenv("TMA_OBJECT_STORAGE_SECRET_KEY_CUSTOM", "secret")
	t.Setenv("TMA_OBJECT_STORAGE_USE_PATH_STYLE", "false")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}

	if config.ObjectStore.Provider != "localfs" {
		t.Fatalf("expected object storage provider localfs, got %q", config.ObjectStore.Provider)
	}
	if config.ObjectStore.Endpoint != "http://rustfs.local:9000" {
		t.Fatalf("expected object storage endpoint, got %q", config.ObjectStore.Endpoint)
	}
	if config.ObjectStore.Region != "local" || config.ObjectStore.Bucket != "tma-local" {
		t.Fatalf("unexpected object storage region/bucket: %+v", config.ObjectStore)
	}
	if config.ObjectStore.RootDir != "/data/object-store" {
		t.Fatalf("expected object storage root dir, got %q", config.ObjectStore.RootDir)
	}
	if config.ObjectStore.AccessKey != "access" || config.ObjectStore.SecretKey != "secret" {
		t.Fatalf("unexpected object storage credentials: %+v", config.ObjectStore)
	}
	if config.ObjectStore.UsePathStyle {
		t.Fatal("expected object storage use path style false")
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
