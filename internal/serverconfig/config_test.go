package serverconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/capability"
)

var configEnvKeys = []string{
	"TMA_ENV",
	"TMA_HTTP_ADDR",
	"TMA_DATABASE_URL",
	"TMA_AUTH_MODE",
	"TMA_AUTH_JWT_SECRET",
	"TMA_AUTH_JWT_ISSUER",
	"TMA_AUTH_JWT_AUDIENCE",
	"TMA_AUTH_OIDC_ISSUER",
	"TMA_AUTH_OIDC_AUDIENCE",
	"TMA_AUTH_OIDC_JWKS_URL",
	"TMA_AUTH_OIDC_SIGNING_ALGS",
	"TMA_AUTH_OIDC_HTTP_TIMEOUT_SECONDS",
	"TMA_AUTH_OIDC_REFRESH_INTERVAL_SECONDS",
	"TMA_AUTH_OIDC_MAX_STALE_SECONDS",
	"TMA_AUTH_OIDC_CLAIM_MAPPING_JSON",
	"TMA_AUTH_OIDC_CLI_CLIENT_ID",
	"TMA_AUTH_COOKIE_TRUSTED_ORIGINS",
	"TMA_AUTH_GATEWAY_TOKEN",
	"TMA_AUTH_GATEWAY_TRUSTED_CIDRS",
	"TMA_TURN_QUEUE_SIZE",
	"TMA_TURN_WORKER_COUNT",
	"TMA_TURN_POLL_INTERVAL_MS",
	"TMA_TURN_LEASE_DURATION_MS",
	"TMA_TURN_HEARTBEAT_INTERVAL_MS",
	"TMA_TURN_TIMEOUT_MS",
	"TMA_MAX_TOOL_ROUNDS",
	"TMA_DEFAULT_CONTEXT_WINDOW_TOKENS",
	"TMA_LLM_PROVIDER",
	"TMA_LLM_PROVIDER_TYPE",
	"TMA_LLM_MODEL",
	"TMA_LLM_BASE_URL",
	"TMA_LLM_API_KEY_ENV",
	"TMA_LLM_API_KEY",
	"TMA_LLM_API_KEY_CUSTOM",
	"TMA_LLM_MAX_ATTEMPTS",
	"TMA_LLM_RETRY_BASE_DELAY_MS",
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
	"TMA_SKILLS_BINARY_SCANNER_PROVIDER",
	"TMA_SKILLS_BINARY_SCANNER_ENDPOINT",
	"TMA_SKILLS_BINARY_SCANNER_TOKEN_ENV",
	"TMA_SKILLS_BINARY_SCANNER_TOKEN",
	"TMA_SKILLS_BINARY_SCANNER_TOKEN_CUSTOM",
	"TMA_SKILLS_BINARY_SCANNER_TIMEOUT_SECONDS",
	"TMA_SKILLS_BINARY_SCANNER_MAX_ATTEMPTS",
	"TMA_SKILLS_BINARY_SCANNER_POLL_INTERVAL_MS",
	"TMA_SKILLS_ASSET_RETENTION_ENABLED",
	"TMA_SKILLS_ASSET_RETENTION_DAYS",
	"TMA_SKILLS_ASSET_GC_DELETE_LIMIT",
	"TMA_SKILLS_ASSET_GC_WORKER_ENABLED",
	"TMA_SKILLS_ASSET_GC_WORKER_INTERVAL_SECONDS",
	"TMA_TOOL_RUNTIME",
	"TMA_CLOUD_SANDBOX_ROOT",
	"TMA_CLOUD_SANDBOX_IMAGE",
	"TMA_CLOUD_SANDBOX_DATA_ROOT",
	"TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS",
	"TMA_CLOUD_SANDBOX_CONTAINER_IDLE_TTL_SECONDS",
	"TMA_CLOUD_SANDBOX_CONTAINER_MAX_LIFETIME_SECONDS",
	"TMA_CLOUD_SANDBOX_CONTAINER_CLEANUP_INTERVAL_SECONDS",
	"TMA_CLOUD_SANDBOX_ALLOW_NETWORK",
	"TMA_ALLOW_SERVER_LOCAL_SYSTEM",
	"TMA_READ_FILE_DEFAULT_MAX_BYTES",
	"TMA_READ_FILE_HARD_MAX_BYTES",
	"TMA_READ_FILE_SMALL_FILE_BYTES",
	"TMA_READ_FILE_MAX_LINES",
	"TMA_MCP_STDIO_HOST_IDLE_TIMEOUT_SECONDS",
	"TMA_MCP_STDIO_HOST_SWEEP_INTERVAL_SECONDS",
	"TMA_MCP_STDIO_HOST_MAX_SESSIONS",
	"TMA_MCP_HTTP_HOST_IDLE_TIMEOUT_SECONDS",
	"TMA_MCP_HTTP_HOST_SWEEP_INTERVAL_SECONDS",
	"TMA_MCP_HTTP_HOST_MAX_SESSIONS",
	"TMA_MCP_HTTP_EGRESS_ALLOW_HTTP",
	"TMA_MCP_HTTP_EGRESS_ALLOW_PRIVATE_NETWORKS",
	"TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS",
	"TMA_MCP_HTTP_EGRESS_ALLOWED_CIDRS",
	"TMA_MCP_HTTP_CA_BUNDLE",
	"TMA_SUBAGENT_MAX_DEPTH",
	"TMA_SUBAGENT_MAX_CHILDREN_PER_TURN",
	"TMA_SUBAGENT_MAX_CHILDREN_PER_SESSION",
	"TMA_SUBAGENT_WORKSPACE_ACTIVE_LIMIT",
	"TMA_SUBAGENT_USER_ACTIVE_LIMIT",
	"TMA_SUBAGENT_WORKSPACE_QUEUE_LIMIT",
	"TMA_SUBAGENT_USER_QUEUE_LIMIT",
	"TMA_SUBAGENT_QUEUE_TIMEOUT_SECONDS",
	"TMA_WORKER_AUTH_TOKEN",
	"TMA_WORKER_AUTH_WORKSPACE_ID",
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
	"TMA_SECURITY_AUDIT_OTLP_ENDPOINT",
	"TMA_SECURITY_AUDIT_OTLP_TOKEN",
	"TMA_SECURITY_AUDIT_INTEGRITY_KEY",
	"TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID",
	"TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON",
	"TMA_SECURITY_AUDIT_DURABLE",
	"TMA_SECURITY_AUDIT_QUEUE_SIZE",
	"TMA_SECURITY_AUDIT_BATCH_SIZE",
	"TMA_SECURITY_AUDIT_FLUSH_INTERVAL_MS",
	"TMA_SECURITY_AUDIT_HTTP_TIMEOUT_SECONDS",
	"TMA_SECURITY_AUDIT_WORKER_INTERVAL_MS",
	"TMA_SECURITY_AUDIT_LEASE_DURATION_MS",
	"TMA_SECURITY_AUDIT_MAX_ATTEMPTS",
	"TMA_SECURITY_AUDIT_RETRY_INITIAL_DELAY_MS",
	"TMA_SECURITY_AUDIT_RETRY_MAX_DELAY_MS",
	"TMA_SECURITY_AUDIT_RETENTION_DAYS",
	"TMA_SECURITY_AUDIT_PRUNE_INTERVAL_MS",
	"TMA_SECURITY_AUDIT_PRUNE_LIMIT",
	"TMA_TRACE_INDEX_RETENTION_ENABLED",
	"TMA_TRACE_INDEX_RETENTION_DAYS",
	"TMA_TRACE_INDEX_RETENTION_INTERVAL_MS",
	"TMA_TRACE_INDEX_RETENTION_LIMIT",
}

func TestReadFileLimitsLoadAndValidate(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://read-file-config")
	config, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.ToolRuntime.ReadFileLimits != capability.DefaultReadFileLimits() {
		t.Fatalf("unexpected read_file defaults: %#v", config.ToolRuntime.ReadFileLimits)
	}

	t.Setenv("TMA_READ_FILE_DEFAULT_MAX_BYTES", "16384")
	t.Setenv("TMA_READ_FILE_HARD_MAX_BYTES", "131072")
	t.Setenv("TMA_READ_FILE_SMALL_FILE_BYTES", "4096")
	t.Setenv("TMA_READ_FILE_MAX_LINES", "800")
	config, err = FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := (capability.ReadFileLimits{DefaultMaxBytes: 16384, HardMaxBytes: 131072, SmallFileBytes: 4096, MaxLines: 800})
	if config.ToolRuntime.ReadFileLimits != want {
		t.Fatalf("unexpected configured limits: got %#v want %#v", config.ToolRuntime.ReadFileLimits, want)
	}

	t.Setenv("TMA_READ_FILE_DEFAULT_MAX_BYTES", "200000")
	t.Setenv("TMA_READ_FILE_HARD_MAX_BYTES", "100000")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "read_file_default_max_bytes") {
		t.Fatalf("expected invalid limit relationship, got %v", err)
	}
}

func TestProductionRejectsDisabledOrIncompleteAuth(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "disabled auth",
			env:  map[string]string{"TMA_ENV": "production", "TMA_WORKER_AUTH_TOKEN": "worker-secret"},
			want: "TMA_AUTH_MODE must be oidc, jwt, or gateway",
		},
		{
			name: "missing jwt secret",
			env:  map[string]string{"TMA_ENV": "production", "TMA_AUTH_MODE": "jwt", "TMA_WORKER_AUTH_TOKEN": "worker-secret"},
			want: "TMA_AUTH_JWT_SECRET is required",
		},
		{
			name: "missing oidc issuer",
			env:  map[string]string{"TMA_ENV": "production", "TMA_AUTH_MODE": "oidc", "TMA_WORKER_AUTH_TOKEN": "worker-secret"},
			want: "TMA_AUTH_OIDC_ISSUER is required",
		},
		{
			name: "missing oidc audience",
			env: map[string]string{
				"TMA_ENV": "production", "TMA_AUTH_MODE": "oidc", "TMA_AUTH_OIDC_ISSUER": "https://issuer.example",
				"TMA_WORKER_AUTH_TOKEN": "worker-secret",
			},
			want: "TMA_AUTH_OIDC_AUDIENCE is required",
		},
		{
			name: "insecure production oidc issuer",
			env: map[string]string{
				"TMA_ENV": "production", "TMA_AUTH_MODE": "oidc", "TMA_AUTH_OIDC_ISSUER": "http://issuer.example",
				"TMA_AUTH_OIDC_AUDIENCE": "tma-api", "TMA_WORKER_AUTH_TOKEN": "worker-secret",
			},
			want: "TMA_AUTH_OIDC_ISSUER must use https",
		},
		{
			name: "unsupported oidc algorithm",
			env: map[string]string{
				"TMA_ENV": "production", "TMA_AUTH_MODE": "oidc", "TMA_AUTH_OIDC_ISSUER": "https://issuer.example",
				"TMA_AUTH_OIDC_AUDIENCE": "tma-api", "TMA_AUTH_OIDC_SIGNING_ALGS": "HS256", "TMA_WORKER_AUTH_TOKEN": "worker-secret",
			},
			want: "unsupported TMA_AUTH_OIDC_SIGNING_ALGS",
		},
		{
			name: "oidc max stale below refresh interval",
			env: map[string]string{
				"TMA_ENV": "production", "TMA_AUTH_MODE": "oidc", "TMA_AUTH_OIDC_ISSUER": "https://issuer.example",
				"TMA_AUTH_OIDC_AUDIENCE": "tma-api", "TMA_AUTH_OIDC_REFRESH_INTERVAL_SECONDS": "60",
				"TMA_AUTH_OIDC_MAX_STALE_SECONDS": "30", "TMA_WORKER_AUTH_TOKEN": "worker-secret",
			},
			want: "TMA_AUTH_OIDC_MAX_STALE_SECONDS must be greater than or equal",
		},
		{
			name: "insecure production cookie origin",
			env: map[string]string{
				"TMA_ENV": "production", "TMA_AUTH_MODE": "oidc", "TMA_AUTH_OIDC_ISSUER": "https://issuer.example",
				"TMA_AUTH_OIDC_AUDIENCE": "tma-api", "TMA_AUTH_COOKIE_TRUSTED_ORIGINS": "http://app.example",
				"TMA_AUTH_OIDC_CLAIM_MAPPING_JSON": `{"allowed_workspace_ids":["wksp_default"]}`,
				"TMA_WORKER_AUTH_TOKEN":            "worker-secret",
			},
			want: "TMA_AUTH_COOKIE_TRUSTED_ORIGINS entries must use https",
		},
		{
			name: "production oidc without tenant restriction",
			env: map[string]string{
				"TMA_ENV": "production", "TMA_AUTH_MODE": "oidc", "TMA_AUTH_OIDC_ISSUER": "https://issuer.example",
				"TMA_AUTH_OIDC_AUDIENCE": "tma-api", "TMA_WORKER_AUTH_TOKEN": "worker-secret",
			},
			want: "TMA_AUTH_OIDC_CLAIM_MAPPING_JSON must configure allowed_workspace_ids",
		},
		{
			name: "missing worker token",
			env: map[string]string{
				"TMA_ENV": "production", "TMA_AUTH_MODE": "jwt",
				"TMA_AUTH_JWT_SECRET": "production-jwt-secret-with-at-least-32-bytes",
				"TMA_AUTH_JWT_ISSUER": "https://issuer.example", "TMA_AUTH_JWT_AUDIENCE": "tma-api",
			},
			want: "TMA_WORKER_AUTH_TOKEN is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("TMA_DATABASE_URL", "postgres://example")
			for key, value := range test.env {
				t.Setenv(key, value)
			}
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error containing %q, got %v", test.want, err)
			}
		})
	}
}

func TestProductionAcceptsCompleteOIDCAuth(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_ENV", "production")
	t.Setenv("TMA_AUTH_MODE", "oidc")
	t.Setenv("TMA_AUTH_OIDC_ISSUER", "https://issuer.example")
	t.Setenv("TMA_AUTH_OIDC_AUDIENCE", "tma-api")
	t.Setenv("TMA_AUTH_OIDC_JWKS_URL", "https://issuer.example/keys")
	t.Setenv("TMA_AUTH_OIDC_SIGNING_ALGS", "RS256,ES256")
	t.Setenv("TMA_AUTH_OIDC_CLAIM_MAPPING_JSON", `{"allowed_workspace_ids":["wksp_default"]}`)
	t.Setenv("TMA_WORKER_AUTH_TOKEN", "worker-secret")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	if config.Auth.Mode != "oidc" || config.Auth.OIDCIssuer != "https://issuer.example" || config.Auth.OIDCAudience != "tma-api" || len(config.Auth.OIDCSigningAlgs) != 2 {
		t.Fatalf("unexpected production oidc config: %+v", config.Auth)
	}
}

func TestProductionAcceptsCompleteJWTAuth(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_ENV", "production")
	t.Setenv("TMA_AUTH_MODE", "jwt")
	t.Setenv("TMA_AUTH_JWT_SECRET", "production-jwt-secret-with-at-least-32-bytes")
	t.Setenv("TMA_AUTH_JWT_ISSUER", "https://issuer.example")
	t.Setenv("TMA_AUTH_JWT_AUDIENCE", "tma-api")
	t.Setenv("TMA_WORKER_AUTH_TOKEN", "worker-secret")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	if config.Environment != "production" || config.Auth.Mode != "jwt" || config.Auth.JWTIssuer != "https://issuer.example" {
		t.Fatalf("unexpected production auth config: %+v", config.Auth)
	}
}

func TestGatewayAuthRequiresTokenAndTrustedCIDRs(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_AUTH_MODE", "gateway")
	t.Setenv("TMA_AUTH_GATEWAY_TOKEN", "gateway-secret")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "TMA_AUTH_GATEWAY_TRUSTED_CIDRS") {
		t.Fatalf("expected missing trusted CIDR error, got %v", err)
	}

	t.Setenv("TMA_AUTH_GATEWAY_TRUSTED_CIDRS", "127.0.0.0/8, 10.0.0.0/8")
	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	if len(config.Auth.GatewayTrustedCIDRs) != 2 {
		t.Fatalf("unexpected trusted CIDRs: %#v", config.Auth.GatewayTrustedCIDRs)
	}
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
	if config.Turn.WorkerCount != DefaultTurnWorkerCount {
		t.Fatalf("expected default turn worker count %d, got %d", DefaultTurnWorkerCount, config.Turn.WorkerCount)
	}
	if config.Turn.MaxToolRounds != DefaultMaxToolRounds {
		t.Fatalf("expected default max tool rounds %d, got %d", DefaultMaxToolRounds, config.Turn.MaxToolRounds)
	}
	if config.Turn.PollInterval != time.Duration(DefaultTurnPollIntervalMS)*time.Millisecond || config.Turn.LeaseDuration != time.Duration(DefaultTurnLeaseDurationMS)*time.Millisecond || config.Turn.HeartbeatInterval != time.Duration(DefaultTurnHeartbeatIntervalMS)*time.Millisecond {
		t.Fatalf("unexpected default turn lease config: %+v", config.Turn)
	}
	if config.Subagent.MaxDepth != DefaultSubagentMaxDepth || config.Subagent.MaxChildrenPerTurn != DefaultSubagentMaxChildrenPerTurn || config.Subagent.MaxChildrenPerSession != DefaultSubagentMaxChildrenPerSession || config.Subagent.WorkspaceActiveLimit != DefaultSubagentWorkspaceActiveLimit || config.Subagent.UserActiveLimit != DefaultSubagentUserActiveLimit || config.Subagent.WorkspaceQueuedLimit != DefaultSubagentWorkspaceQueuedLimit || config.Subagent.UserQueuedLimit != DefaultSubagentUserQueuedLimit || config.Subagent.QueueTimeoutSeconds != DefaultSubagentQueueTimeoutSeconds {
		t.Fatalf("unexpected default subagent config: %+v", config.Subagent)
	}
	if config.Skills.BinaryScanner.Provider != DefaultSkillsBinaryScannerProvider || config.Skills.BinaryScanner.TimeoutSeconds != DefaultSkillsBinaryScannerTimeoutSec || config.Skills.BinaryScanner.MaxAttempts != DefaultSkillsBinaryScannerMaxAttempts || config.Skills.BinaryScanner.PollIntervalMillis != DefaultSkillsBinaryScannerPollIntervalMS {
		t.Fatalf("unexpected default skills binary scanner config: %+v", config.Skills.BinaryScanner)
	}
	if config.Skills.AssetRetention.Enabled != DefaultSkillsAssetRetentionEnabled || config.Skills.AssetRetention.RetentionDays != DefaultSkillsAssetRetentionDays || config.Skills.AssetRetention.DeleteLimit != DefaultSkillsAssetGCDeleteLimit || config.Skills.AssetRetention.WorkerEnabled != DefaultSkillsAssetGCWorkerEnabled || config.Skills.AssetRetention.WorkerIntervalSeconds != DefaultSkillsAssetGCWorkerIntervalSec {
		t.Fatalf("unexpected default skills asset retention config: %+v", config.Skills.AssetRetention)
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
	if config.LLM.MaxAttempts != DefaultLLMMaxAttempts || config.LLM.RetryBaseDelay != time.Duration(DefaultLLMRetryBaseDelayMS)*time.Millisecond {
		t.Fatalf("unexpected default llm retry config: %+v", config.LLM)
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
	if config.ToolRuntime.Root != DefaultCloudSandboxWorkspaceRoot || config.ToolRuntime.Image != "" {
		t.Fatalf("expected isolated default cloud sandbox root and empty image, got %+v", config.ToolRuntime)
	}
	if config.ToolRuntime.ContainerIdleTTL != time.Duration(DefaultCloudSandboxContainerIdleTTLSec)*time.Second {
		t.Fatalf("expected default cloud sandbox container idle ttl, got %s", config.ToolRuntime.ContainerIdleTTL)
	}
	if config.ToolRuntime.ContainerMaxLifetime != time.Duration(DefaultCloudSandboxContainerMaxLifetimeSec)*time.Second {
		t.Fatalf("expected default cloud sandbox container max lifetime, got %s", config.ToolRuntime.ContainerMaxLifetime)
	}
	if config.ToolRuntime.ContainerCleanupInterval != time.Duration(DefaultCloudSandboxContainerCleanupIntervalSec)*time.Second {
		t.Fatalf("expected default cloud sandbox container cleanup interval, got %s", config.ToolRuntime.ContainerCleanupInterval)
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
	if config.MCP.StdioHost.IdleTimeout != time.Duration(DefaultMCPStdioHostIdleTimeoutSec)*time.Second || config.MCP.StdioHost.SweepInterval != time.Duration(DefaultMCPStdioHostSweepIntervalSec)*time.Second || config.MCP.StdioHost.MaxSessions != DefaultMCPStdioHostMaxSessions {
		t.Fatalf("unexpected default MCP stdio host config: %+v", config.MCP.StdioHost)
	}
	if config.MCP.StreamableHTTPHost.IdleTimeout != time.Duration(DefaultMCPHTTPHostIdleTimeoutSec)*time.Second || config.MCP.StreamableHTTPHost.SweepInterval != time.Duration(DefaultMCPHTTPHostSweepIntervalSec)*time.Second || config.MCP.StreamableHTTPHost.MaxSessions != DefaultMCPHTTPHostMaxSessions {
		t.Fatalf("unexpected default MCP HTTP host config: %+v", config.MCP.StreamableHTTPHost)
	}
	if config.MCP.StreamableHTTPHost.EgressAllowHTTP || config.MCP.StreamableHTTPHost.EgressAllowPrivateNetworks || len(config.MCP.StreamableHTTPHost.EgressAllowedHosts) != 0 || len(config.MCP.StreamableHTTPHost.EgressAllowedCIDRs) != 0 || config.MCP.StreamableHTTPHost.CABundlePath != "" {
		t.Fatalf("unexpected default MCP HTTP egress config: %+v", config.MCP.StreamableHTTPHost)
	}
	if config.Worker.AuthToken != "" {
		t.Fatalf("expected empty default worker auth token, got %q", config.Worker.AuthToken)
	}
	if config.Worker.ControlAuthToken != "" {
		t.Fatalf("expected empty default worker control auth token, got %q", config.Worker.ControlAuthToken)
	}
	if config.Auth.OIDCCLIClientID != DefaultOIDCCLIClientID {
		t.Fatalf("expected default OIDC CLI client id %q, got %q", DefaultOIDCCLIClientID, config.Auth.OIDCCLIClientID)
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
	if config.Observability.TraceIndexRetention.Enabled != DefaultTraceIndexRetentionEnabled {
		t.Fatalf("expected default trace index retention enabled %t, got %t", DefaultTraceIndexRetentionEnabled, config.Observability.TraceIndexRetention.Enabled)
	}
	if config.Observability.TraceIndexRetention.RetentionDays != DefaultTraceIndexRetentionDays {
		t.Fatalf("expected default trace index retention days %d, got %d", DefaultTraceIndexRetentionDays, config.Observability.TraceIndexRetention.RetentionDays)
	}
	if config.Observability.TraceIndexRetention.Retention != time.Duration(DefaultTraceIndexRetentionDays)*24*time.Hour {
		t.Fatalf("expected default trace index retention duration, got %s", config.Observability.TraceIndexRetention.Retention)
	}
	if config.Observability.TraceIndexRetention.IntervalMillis != DefaultTraceIndexRetentionIntervalMS {
		t.Fatalf("expected default trace index retention interval %d, got %d", DefaultTraceIndexRetentionIntervalMS, config.Observability.TraceIndexRetention.IntervalMillis)
	}
	if config.Observability.TraceIndexRetention.Interval != time.Duration(DefaultTraceIndexRetentionIntervalMS)*time.Millisecond {
		t.Fatalf("expected default trace index retention interval duration, got %s", config.Observability.TraceIndexRetention.Interval)
	}
	if config.Observability.TraceIndexRetention.Limit != DefaultTraceIndexRetentionLimit {
		t.Fatalf("expected default trace index retention limit %d, got %d", DefaultTraceIndexRetentionLimit, config.Observability.TraceIndexRetention.Limit)
	}
	if config.Observability.SecurityAudit.OTLPEndpoint != "" || config.Observability.SecurityAudit.OTLPToken != "" {
		t.Fatalf("expected disabled default security audit exporter, got %+v", config.Observability.SecurityAudit)
	}
	if config.Observability.SecurityAudit.QueueSize != DefaultSecurityAuditQueueSize || config.Observability.SecurityAudit.BatchSize != DefaultSecurityAuditBatchSize {
		t.Fatalf("unexpected default security audit queue config: %+v", config.Observability.SecurityAudit)
	}
	if config.Observability.SecurityAudit.FlushInterval != time.Duration(DefaultSecurityAuditFlushIntervalMS)*time.Millisecond || config.Observability.SecurityAudit.HTTPTimeout != time.Duration(DefaultSecurityAuditHTTPTimeoutSeconds)*time.Second {
		t.Fatalf("unexpected default security audit timing config: %+v", config.Observability.SecurityAudit)
	}
	if !config.Observability.SecurityAudit.Durable || config.Observability.SecurityAudit.WorkerInterval != time.Duration(DefaultSecurityAuditWorkerIntervalMS)*time.Millisecond || config.Observability.SecurityAudit.LeaseDuration != time.Duration(DefaultSecurityAuditLeaseDurationMS)*time.Millisecond {
		t.Fatalf("unexpected default durable security audit config: %+v", config.Observability.SecurityAudit)
	}
	if config.Observability.SecurityAudit.MaxAttempts != DefaultSecurityAuditMaxAttempts || config.Observability.SecurityAudit.Retention != time.Duration(DefaultSecurityAuditRetentionDays)*24*time.Hour {
		t.Fatalf("unexpected default security audit retry/retention config: %+v", config.Observability.SecurityAudit)
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
TMA_CLOUD_SANDBOX_CONTAINER_IDLE_TTL_SECONDS=900
TMA_CLOUD_SANDBOX_CONTAINER_MAX_LIFETIME_SECONDS=7200
TMA_CLOUD_SANDBOX_CONTAINER_CLEANUP_INTERVAL_SECONDS=30
TMA_CLOUD_SANDBOX_ALLOW_NETWORK=true
TMA_ALLOW_SERVER_LOCAL_SYSTEM=true
TMA_MCP_STDIO_HOST_IDLE_TIMEOUT_SECONDS=120
TMA_MCP_STDIO_HOST_SWEEP_INTERVAL_SECONDS=15
TMA_MCP_STDIO_HOST_MAX_SESSIONS=8
TMA_MCP_HTTP_HOST_IDLE_TIMEOUT_SECONDS=180
TMA_MCP_HTTP_HOST_SWEEP_INTERVAL_SECONDS=20
TMA_MCP_HTTP_HOST_MAX_SESSIONS=12
TMA_MCP_HTTP_EGRESS_ALLOW_HTTP=true
TMA_MCP_HTTP_EGRESS_ALLOW_PRIVATE_NETWORKS=true
TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS=mcp.internal.example,*.mcp.example.com
TMA_MCP_HTTP_EGRESS_ALLOWED_CIDRS=10.20.0.0/16,fd20::/64
TMA_MCP_HTTP_CA_BUNDLE=/etc/tma/mcp-ca.pem
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
	if config.ToolRuntime.ContainerIdleTTLSeconds != 900 || config.ToolRuntime.ContainerIdleTTL != 15*time.Minute {
		t.Fatalf("expected dotenv cloud sandbox container idle ttl 900s, got seconds=%d ttl=%s", config.ToolRuntime.ContainerIdleTTLSeconds, config.ToolRuntime.ContainerIdleTTL)
	}
	if config.ToolRuntime.ContainerMaxLifetimeSeconds != 7200 || config.ToolRuntime.ContainerMaxLifetime != 2*time.Hour {
		t.Fatalf("expected dotenv cloud sandbox container max lifetime 7200s, got seconds=%d ttl=%s", config.ToolRuntime.ContainerMaxLifetimeSeconds, config.ToolRuntime.ContainerMaxLifetime)
	}
	if config.ToolRuntime.ContainerCleanupIntervalSeconds != 30 || config.ToolRuntime.ContainerCleanupInterval != 30*time.Second {
		t.Fatalf("expected dotenv cloud sandbox container cleanup interval 30s, got seconds=%d interval=%s", config.ToolRuntime.ContainerCleanupIntervalSeconds, config.ToolRuntime.ContainerCleanupInterval)
	}
	if !config.ToolRuntime.AllowNetwork {
		t.Fatal("expected dotenv cloud sandbox allow network override true")
	}
	if !config.ToolRuntime.AllowLocalSystem {
		t.Fatal("expected dotenv server local_system fallback override true")
	}
	if config.MCP.StdioHost.IdleTimeout != 2*time.Minute || config.MCP.StdioHost.SweepInterval != 15*time.Second || config.MCP.StdioHost.MaxSessions != 8 {
		t.Fatalf("unexpected dotenv MCP stdio host config: %+v", config.MCP.StdioHost)
	}
	if config.MCP.StreamableHTTPHost.IdleTimeout != 3*time.Minute || config.MCP.StreamableHTTPHost.SweepInterval != 20*time.Second || config.MCP.StreamableHTTPHost.MaxSessions != 12 {
		t.Fatalf("unexpected dotenv MCP HTTP host config: %+v", config.MCP.StreamableHTTPHost)
	}
	if !config.MCP.StreamableHTTPHost.EgressAllowHTTP || !config.MCP.StreamableHTTPHost.EgressAllowPrivateNetworks || !reflect.DeepEqual(config.MCP.StreamableHTTPHost.EgressAllowedHosts, []string{"mcp.internal.example", "*.mcp.example.com"}) || !reflect.DeepEqual(config.MCP.StreamableHTTPHost.EgressAllowedCIDRs, []string{"10.20.0.0/16", "fd20::/64"}) {
		t.Fatalf("unexpected dotenv MCP HTTP egress config: %+v", config.MCP.StreamableHTTPHost)
	}
	if config.MCP.StreamableHTTPHost.CABundlePath != "/etc/tma/mcp-ca.pem" {
		t.Fatalf("unexpected dotenv MCP HTTP CA bundle: %q", config.MCP.StreamableHTTPHost.CABundlePath)
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
	t.Setenv("TMA_LLM_MAX_ATTEMPTS", "5")
	t.Setenv("TMA_LLM_RETRY_BASE_DELAY_MS", "750")

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
	if config.LLM.MaxAttempts != 5 || config.LLM.RetryBaseDelay != 750*time.Millisecond {
		t.Fatalf("unexpected llm retry config: %+v", config.LLM)
	}
}

func TestFromEnvRejectsInvalidLLMRetryConfig(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "zero attempts", key: "TMA_LLM_MAX_ATTEMPTS", value: "0", want: "between 1 and 10"},
		{name: "too many attempts", key: "TMA_LLM_MAX_ATTEMPTS", value: "11", want: "between 1 and 10"},
		{name: "base delay", key: "TMA_LLM_RETRY_BASE_DELAY_MS", value: "60001", want: "between 1 and 60000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("TMA_DATABASE_URL", "postgres://example")
			t.Setenv(test.key, test.value)
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s validation error, got %v", test.key, err)
			}
		})
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
	t.Setenv("TMA_TRACE_INDEX_RETENTION_ENABLED", "true")
	t.Setenv("TMA_TRACE_INDEX_RETENTION_DAYS", "7")
	t.Setenv("TMA_TRACE_INDEX_RETENTION_INTERVAL_MS", "2250")
	t.Setenv("TMA_TRACE_INDEX_RETENTION_LIMIT", "45")

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
	if !config.Observability.TraceIndexRetention.Enabled {
		t.Fatal("expected trace index retention enabled")
	}
	if config.Observability.TraceIndexRetention.RetentionDays != 7 {
		t.Fatalf("expected trace index retention days 7, got %d", config.Observability.TraceIndexRetention.RetentionDays)
	}
	if config.Observability.TraceIndexRetention.Retention != 7*24*time.Hour {
		t.Fatalf("expected trace index retention 7 days, got %s", config.Observability.TraceIndexRetention.Retention)
	}
	if config.Observability.TraceIndexRetention.IntervalMillis != 2250 {
		t.Fatalf("expected trace index retention interval ms 2250, got %d", config.Observability.TraceIndexRetention.IntervalMillis)
	}
	if config.Observability.TraceIndexRetention.Interval != 2250*time.Millisecond {
		t.Fatalf("expected trace index retention interval 2250ms, got %s", config.Observability.TraceIndexRetention.Interval)
	}
	if config.Observability.TraceIndexRetention.Limit != 45 {
		t.Fatalf("expected trace index retention limit 45, got %d", config.Observability.TraceIndexRetention.Limit)
	}
}

func TestFromEnvParsesAndValidatesSecurityAuditExporter(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_SECURITY_AUDIT_OTLP_ENDPOINT", "http://collector.test:4318")
	t.Setenv("TMA_SECURITY_AUDIT_OTLP_TOKEN", "security-audit-token")
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY", "test-integrity-key-at-least-32-bytes")
	t.Setenv("TMA_SECURITY_AUDIT_QUEUE_SIZE", "500")
	t.Setenv("TMA_SECURITY_AUDIT_BATCH_SIZE", "25")
	t.Setenv("TMA_SECURITY_AUDIT_FLUSH_INTERVAL_MS", "750")
	t.Setenv("TMA_SECURITY_AUDIT_HTTP_TIMEOUT_SECONDS", "7")
	t.Setenv("TMA_SECURITY_AUDIT_WORKER_INTERVAL_MS", "250")
	t.Setenv("TMA_SECURITY_AUDIT_LEASE_DURATION_MS", "5000")
	t.Setenv("TMA_SECURITY_AUDIT_MAX_ATTEMPTS", "6")
	t.Setenv("TMA_SECURITY_AUDIT_RETRY_INITIAL_DELAY_MS", "500")
	t.Setenv("TMA_SECURITY_AUDIT_RETRY_MAX_DELAY_MS", "10000")
	t.Setenv("TMA_SECURITY_AUDIT_RETENTION_DAYS", "120")
	t.Setenv("TMA_SECURITY_AUDIT_PRUNE_INTERVAL_MS", "60000")
	t.Setenv("TMA_SECURITY_AUDIT_PRUNE_LIMIT", "250")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	audit := config.Observability.SecurityAudit
	if audit.OTLPEndpoint != "http://collector.test:4318" || audit.OTLPToken != "security-audit-token" || audit.QueueSize != 500 || audit.BatchSize != 25 {
		t.Fatalf("unexpected security audit exporter config: %+v", audit)
	}
	if audit.FlushInterval != 750*time.Millisecond || audit.HTTPTimeout != 7*time.Second {
		t.Fatalf("unexpected security audit exporter timing: %+v", audit)
	}
	if !audit.Durable || audit.WorkerInterval != 250*time.Millisecond || audit.LeaseDuration != 5*time.Second || audit.MaxAttempts != 6 {
		t.Fatalf("unexpected durable security audit worker config: %+v", audit)
	}
	if audit.RetryInitialDelay != 500*time.Millisecond || audit.RetryMaxDelay != 10*time.Second || audit.Retention != 120*24*time.Hour || audit.PruneInterval != time.Minute || audit.PruneLimit != 250 {
		t.Fatalf("unexpected durable security audit retry/retention config: %+v", audit)
	}

	t.Setenv("TMA_SECURITY_AUDIT_BATCH_SIZE", "501")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "TMA_SECURITY_AUDIT_BATCH_SIZE") {
		t.Fatalf("expected invalid security audit batch size, got %v", err)
	}
}

func TestFromEnvParsesSecurityAuditIntegrityKeyring(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_SECURITY_AUDIT_OTLP_ENDPOINT", "http://collector.test:4318")
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID", "2026-07")
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON", `{"2026-01":"previous-security-audit-key-material-32-bytes","2026-07":"current-security-audit-key-material-at-least-32-bytes"}`)

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("parse security audit keyring: %v", err)
	}
	audit := config.Observability.SecurityAudit
	if audit.IntegrityKeyID != "2026-07" || len(audit.IntegrityKeys) != 2 || audit.IntegrityKeys["2026-01"] == "" {
		t.Fatalf("unexpected security audit integrity keyring: id=%q keys=%d", audit.IntegrityKeyID, len(audit.IntegrityKeys))
	}

	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID", "missing")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "not present") {
		t.Fatalf("expected missing active integrity key rejection, got %v", err)
	}
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON", `{"bad key":"secret"}`)
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "invalid key id") {
		t.Fatalf("expected invalid integrity key id rejection, got %v", err)
	}
}

func TestProductionSecurityAuditExporterRequiresHTTPS(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_ENV", "production")
	t.Setenv("TMA_AUTH_MODE", "jwt")
	t.Setenv("TMA_AUTH_JWT_SECRET", "production-jwt-secret-at-least-32-bytes")
	t.Setenv("TMA_AUTH_JWT_ISSUER", "https://issuer.example")
	t.Setenv("TMA_AUTH_JWT_AUDIENCE", "tma-api")
	t.Setenv("TMA_WORKER_AUTH_TOKEN", "worker-secret")
	t.Setenv("TMA_SECURITY_AUDIT_OTLP_ENDPOINT", "http://collector.example:4318")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "TMA_SECURITY_AUDIT_OTLP_ENDPOINT must use https") {
		t.Fatalf("expected production security audit HTTPS rejection, got %v", err)
	}
	t.Setenv("TMA_SECURITY_AUDIT_OTLP_ENDPOINT", "https://collector.example:4318")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "TMA_SECURITY_AUDIT_INTEGRITY_KEY") {
		t.Fatalf("expected production security audit integrity key rejection, got %v", err)
	}
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY", "production-security-audit-integrity-key")
	if _, err := FromEnv(); err != nil {
		t.Fatalf("expected valid production security audit config, got %v", err)
	}
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY", "")
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID", "current")
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON", `{"previous":"short","current":"current-production-security-audit-integrity-key"}`)
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), `key "previous" must be at least 32 bytes`) {
		t.Fatalf("expected short previous integrity key rejection, got %v", err)
	}
	t.Setenv("TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON", `{"previous":"previous-production-security-audit-integrity-key","current":"current-production-security-audit-integrity-key"}`)
	if _, err := FromEnv(); err != nil {
		t.Fatalf("expected valid production rotating integrity keyring, got %v", err)
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

func TestFromEnvParsesSkillsBinaryScannerConfig(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_SKILLS_BINARY_SCANNER_PROVIDER", "clamav_http")
	t.Setenv("TMA_SKILLS_BINARY_SCANNER_ENDPOINT", "http://clamav-gateway.local:8080/v1")
	t.Setenv("TMA_SKILLS_BINARY_SCANNER_TOKEN_ENV", "TMA_SKILLS_BINARY_SCANNER_TOKEN_CUSTOM")
	t.Setenv("TMA_SKILLS_BINARY_SCANNER_TOKEN_CUSTOM", "scanner-secret")
	t.Setenv("TMA_SKILLS_BINARY_SCANNER_TIMEOUT_SECONDS", "45")
	t.Setenv("TMA_SKILLS_BINARY_SCANNER_MAX_ATTEMPTS", "5")
	t.Setenv("TMA_SKILLS_BINARY_SCANNER_POLL_INTERVAL_MS", "250")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}
	scanner := config.Skills.BinaryScanner
	if scanner.Provider != "clamav_http" || scanner.Endpoint != "http://clamav-gateway.local:8080/v1" || scanner.TokenEnv != "TMA_SKILLS_BINARY_SCANNER_TOKEN_CUSTOM" || scanner.Token != "scanner-secret" {
		t.Fatalf("unexpected skills binary scanner identity config: %+v", scanner)
	}
	if scanner.Timeout != 45*time.Second || scanner.MaxAttempts != 5 || scanner.PollInterval != 250*time.Millisecond {
		t.Fatalf("unexpected skills binary scanner runtime config: %+v", scanner)
	}
}

func TestFromEnvInvalidIntUsesDefault(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("TMA_DATABASE_URL", "postgres://example")
	t.Setenv("TMA_TURN_QUEUE_SIZE", "nope")
	t.Setenv("TMA_TURN_WORKER_COUNT", "nope")
	t.Setenv("TMA_TURN_TIMEOUT_MS", "-1")
	t.Setenv("TMA_MAX_TOOL_ROUNDS", "-1")
	t.Setenv("TMA_SUBAGENT_MAX_DEPTH", "-1")
	t.Setenv("TMA_SUBAGENT_USER_ACTIVE_LIMIT", "nope")

	config, err := FromEnv()
	if err != nil {
		t.Fatalf("from env: %v", err)
	}

	if config.Turn.QueueSize != DefaultTurnQueueSize {
		t.Fatalf("expected default queue size, got %d", config.Turn.QueueSize)
	}
	if config.Turn.WorkerCount != DefaultTurnWorkerCount {
		t.Fatalf("expected default worker count, got %d", config.Turn.WorkerCount)
	}
	if config.Turn.TimeoutMillis != DefaultTurnTimeoutMS {
		t.Fatalf("expected default turn timeout millis, got %d", config.Turn.TimeoutMillis)
	}
	if config.Turn.MaxToolRounds != DefaultMaxToolRounds {
		t.Fatalf("expected default max tool rounds, got %d", config.Turn.MaxToolRounds)
	}
	if config.Subagent.MaxDepth != DefaultSubagentMaxDepth || config.Subagent.UserActiveLimit != DefaultSubagentUserActiveLimit {
		t.Fatalf("expected default subagent config fallback, got %+v", config.Subagent)
	}
}

func TestFromEnvRejectsInvalidMCPStdioHostLimits(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "idle timeout", key: "TMA_MCP_STDIO_HOST_IDLE_TIMEOUT_SECONDS", value: "0", want: "must be positive"},
		{name: "sweep interval", key: "TMA_MCP_STDIO_HOST_SWEEP_INTERVAL_SECONDS", value: "0", want: "must be positive"},
		{name: "max sessions", key: "TMA_MCP_STDIO_HOST_MAX_SESSIONS", value: "10001", want: "between 1 and 10000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("TMA_DATABASE_URL", "postgres://example")
			t.Setenv(test.key, test.value)
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s validation error, got %v", test.key, err)
			}
		})
	}
}

func TestFromEnvRejectsInvalidMCPHTTPHostLimits(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "idle timeout", key: "TMA_MCP_HTTP_HOST_IDLE_TIMEOUT_SECONDS", value: "0", want: "must be positive"},
		{name: "sweep interval", key: "TMA_MCP_HTTP_HOST_SWEEP_INTERVAL_SECONDS", value: "0", want: "must be positive"},
		{name: "max sessions", key: "TMA_MCP_HTTP_HOST_MAX_SESSIONS", value: "10001", want: "between 1 and 10000"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("TMA_DATABASE_URL", "postgres://example")
			t.Setenv(test.key, test.value)
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s validation error, got %v", test.key, err)
			}
		})
	}
}

func TestFromEnvRejectsInvalidMCPHTTPEgressAllowlist(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		want  string
	}{
		{name: "host URL", key: "TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS", value: "https://mcp.example.com", want: "invalid host"},
		{name: "invalid CIDR", key: "TMA_MCP_HTTP_EGRESS_ALLOWED_CIDRS", value: "10.0.0.0/not-a-prefix", want: "invalid CIDR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			t.Setenv("TMA_DATABASE_URL", "postgres://example")
			t.Setenv(test.key, test.value)
			_, err := FromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s validation error, got %v", test.key, err)
			}
		})
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
