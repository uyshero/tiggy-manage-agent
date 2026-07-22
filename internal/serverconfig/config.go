package serverconfig

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/identity"
)

const (
	DefaultEnvironment                             = "development"
	DefaultAuthMode                                = "disabled"
	DefaultOIDCSigningAlgorithms                   = "RS256,ES256"
	DefaultOIDCHTTPTimeoutSeconds                  = 10
	DefaultOIDCRefreshIntervalSeconds              = 900
	DefaultOIDCMaxStaleSeconds                     = 86400
	DefaultOIDCCLIClientID                         = "tma-cli"
	DefaultWorkerAuthWorkspaceID                   = "wksp_default"
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
	DefaultLLMMaxAttempts                          = 3
	DefaultLLMRetryBaseDelayMS                     = 250
	DefaultContextWindowTokens                     = 128000
	DefaultObjectStorageProvider                   = "localfs"
	DefaultObjectStorageEndpoint                   = "http://localhost:9000"
	DefaultObjectStorageRegion                     = "us-east-1"
	DefaultObjectStorageBucket                     = "tma-artifacts"
	DefaultObjectStorageRootDir                    = "/private/tmp/tma-object-store"
	DefaultObjectStorageAccessKeyEnv               = "TMA_OBJECT_STORAGE_ACCESS_KEY"
	DefaultObjectStorageSecretKeyEnv               = "TMA_OBJECT_STORAGE_SECRET_KEY"
	DefaultSkillsBinaryScannerProvider             = "builtin"
	DefaultSkillsBinaryScannerTokenEnv             = "TMA_SKILLS_BINARY_SCANNER_TOKEN"
	DefaultSkillsBinaryScannerTimeoutSec           = 30
	DefaultSkillsBinaryScannerMaxAttempts          = 3
	DefaultSkillsBinaryScannerPollIntervalMS       = 500
	DefaultSkillsAssetRetentionEnabled             = false
	DefaultSkillsAssetRetentionDays                = 30
	DefaultSkillsAssetGCDeleteLimit                = 100
	DefaultSkillsAssetGCWorkerEnabled              = false
	DefaultSkillsAssetGCWorkerIntervalSec          = 86400
	DefaultToolRuntime                             = "cloud_sandbox"
	DefaultCloudSandboxWorkspaceRoot               = "/private/tmp/tma-cloud-sandbox-workspaces"
	DefaultCloudSandboxDataRoot                    = "/private/tmp/tma-cloud-sandbox-data"
	DefaultCloudSandboxDataTTLSec                  = 3600
	DefaultCloudSandboxContainerIdleTTLSec         = 1800
	DefaultCloudSandboxContainerMaxLifetimeSec     = 14400
	DefaultCloudSandboxContainerCleanupIntervalSec = 60
	DefaultCloudSandboxAllowNetwork                = true
	DefaultMCPStdioHostIdleTimeoutSec              = 600
	DefaultMCPStdioHostSweepIntervalSec            = 60
	DefaultMCPStdioHostMaxSessions                 = 64
	DefaultMCPHTTPHostIdleTimeoutSec               = 600
	DefaultMCPHTTPHostSweepIntervalSec             = 60
	DefaultMCPHTTPHostMaxSessions                  = 64
	DefaultMCPHTTPEgressAllowHTTP                  = false
	DefaultMCPHTTPEgressAllowPrivateNetworks       = false
	DefaultWorkerReaperEnabled                     = true
	DefaultWorkerReaperIntervalMS                  = 30000
	DefaultWorkerReaperLimit                       = 100
	DefaultWorkerWorkReaperEnabled                 = true
	DefaultWorkerWorkReaperIntervalMS              = 30000
	DefaultWorkerWorkReaperLimit                   = 100
	DefaultObservabilityRetryEnabled               = true
	DefaultObservabilityRetryIntervalMS            = 30000
	DefaultObservabilityRetryLimit                 = 20
	DefaultSecurityAuditQueueSize                  = 2048
	DefaultSecurityAuditBatchSize                  = 100
	DefaultSecurityAuditFlushIntervalMS            = 1000
	DefaultSecurityAuditHTTPTimeoutSeconds         = 10
	DefaultSecurityAuditDurable                    = true
	DefaultSecurityAuditWorkerIntervalMS           = 1000
	DefaultSecurityAuditLeaseDurationMS            = 30000
	DefaultSecurityAuditMaxAttempts                = 8
	DefaultSecurityAuditRetryInitialDelayMS        = 1000
	DefaultSecurityAuditRetryMaxDelayMS            = 300000
	DefaultSecurityAuditRetentionDays              = 90
	DefaultSecurityAuditPruneIntervalMS            = 3600000
	DefaultSecurityAuditPruneLimit                 = 1000
	DefaultTraceIndexRetentionEnabled              = false
	DefaultTraceIndexRetentionDays                 = 30
	DefaultTraceIndexRetentionIntervalMS           = 3600000
	DefaultTraceIndexRetentionLimit                = 1000
	DefaultSubagentMaxDepth                        = 3
	DefaultSubagentMaxChildrenPerTurn              = 5
	DefaultSubagentMaxChildrenPerSession           = 20
	DefaultSubagentWorkspaceActiveLimit            = 50
	DefaultSubagentUserActiveLimit                 = 10
	DefaultSubagentWorkspaceQueuedLimit            = 500
	DefaultSubagentUserQueuedLimit                 = 100
	DefaultSubagentQueueTimeoutSeconds             = 3600
)

type Config struct {
	Environment   string
	HTTPAddr      string
	DatabaseURL   string
	Turn          TurnConfig
	Context       ContextConfig
	LLM           LLMConfig
	ObjectStore   ObjectStorageConfig
	Skills        SkillsConfig
	ToolRuntime   ToolRuntimeConfig
	MCP           MCPConfig
	Subagent      SubagentConfig
	Worker        WorkerConfig
	Observability ObservabilityConfig
	Auth          AuthConfig
}

type AuthConfig struct {
	Mode                    string
	JWTSecret               string
	JWTIssuer               string
	JWTAudience             string
	OIDCIssuer              string
	OIDCAudience            string
	OIDCJWKSURL             string
	OIDCSigningAlgs         []string
	OIDCHTTPTimeoutSecs     int
	OIDCRefreshIntervalSecs int
	OIDCMaxStaleSecs        int
	OIDCClaimMapping        identity.OIDCClaimMapping
	OIDCWebLoginEnabled     bool
	OIDCWebClientID         string
	OIDCWebClientSecret     string
	OIDCWebRedirectURL      string
	OIDCWebPostLogoutURL    string
	OIDCWebSessionSecret    string
	OIDCCLIClientID         string
	CookieTrustedOrigins    []string
	GatewayToken            string
	GatewayTrustedCIDRs     []string
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
	Provider             string
	ProviderType         string
	Model                string
	BaseURL              string
	APIKeyEnv            string
	APIKey               string
	MaxAttempts          int
	RetryBaseDelay       time.Duration
	RetryBaseDelayMillis int
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

type SkillsConfig struct {
	BinaryScanner  SkillsBinaryScannerConfig
	AssetRetention SkillsAssetRetentionConfig
}

type SkillsBinaryScannerConfig struct {
	Provider           string
	Endpoint           string
	TokenEnv           string
	Token              string
	Timeout            time.Duration
	TimeoutSeconds     int
	MaxAttempts        int
	PollInterval       time.Duration
	PollIntervalMillis int
}

type SkillsAssetRetentionConfig struct {
	Enabled               bool
	RetentionDays         int
	DeleteLimit           int
	WorkerEnabled         bool
	WorkerInterval        time.Duration
	WorkerIntervalSeconds int
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
	ReadFileLimits                  capability.ReadFileLimits
}

type MCPConfig struct {
	StdioHost          MCPStdioHostConfig
	StreamableHTTPHost MCPStreamableHTTPHostConfig
}

type MCPStdioHostConfig struct {
	IdleTimeout          time.Duration
	IdleTimeoutSeconds   int
	SweepInterval        time.Duration
	SweepIntervalSeconds int
	MaxSessions          int
}

type MCPStreamableHTTPHostConfig struct {
	IdleTimeout                time.Duration
	IdleTimeoutSeconds         int
	SweepInterval              time.Duration
	SweepIntervalSeconds       int
	MaxSessions                int
	EgressAllowHTTP            bool
	EgressAllowPrivateNetworks bool
	EgressAllowedHosts         []string
	EgressAllowedCIDRs         []string
	CABundlePath               string
}

type SubagentConfig struct {
	MaxDepth              int
	MaxChildrenPerTurn    int
	MaxChildrenPerSession int
	WorkspaceActiveLimit  int
	UserActiveLimit       int
	WorkspaceQueuedLimit  int
	UserQueuedLimit       int
	QueueTimeoutSeconds   int
}

type WorkerConfig struct {
	AuthToken        string
	AuthWorkspaceID  string
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
	SecurityAudit       SecurityAuditExporterConfig
}

type SecurityAuditExporterConfig struct {
	OTLPEndpoint            string
	OTLPToken               string
	IntegrityKey            string
	IntegrityKeyID          string
	IntegrityKeys           map[string]string
	Durable                 bool
	QueueSize               int
	BatchSize               int
	FlushInterval           time.Duration
	FlushIntervalMillis     int
	HTTPTimeout             time.Duration
	HTTPTimeoutSeconds      int
	WorkerInterval          time.Duration
	WorkerIntervalMillis    int
	LeaseDuration           time.Duration
	LeaseDurationMillis     int
	MaxAttempts             int
	RetryInitialDelay       time.Duration
	RetryInitialDelayMillis int
	RetryMaxDelay           time.Duration
	RetryMaxDelayMillis     int
	Retention               time.Duration
	RetentionDays           int
	PruneInterval           time.Duration
	PruneIntervalMillis     int
	PruneLimit              int
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
	authMode := strings.ToLower(strings.TrimSpace(envOrDefault("TMA_AUTH_MODE", DefaultAuthMode)))
	claimMapping := identity.DefaultOIDCClaimMapping()
	claimMappingRaw := strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_CLAIM_MAPPING_JSON"))
	if authMode == "oidc" || claimMappingRaw != "" {
		var err error
		claimMapping, err = identity.ParseOIDCClaimMapping(claimMappingRaw)
		if err != nil {
			return Config{}, fmt.Errorf("TMA_AUTH_OIDC_CLAIM_MAPPING_JSON: %w", err)
		}
	}
	integrityKeys, err := parseSecurityAuditIntegrityKeys(os.Getenv("TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON"))
	if err != nil {
		return Config{}, fmt.Errorf("TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON: %w", err)
	}
	config := Config{
		Environment: envOrDefault("TMA_ENV", DefaultEnvironment),
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
			Provider:             envOrDefault("TMA_LLM_PROVIDER", DefaultLLMProvider),
			ProviderType:         os.Getenv("TMA_LLM_PROVIDER_TYPE"),
			Model:                envOrDefault("TMA_LLM_MODEL", DefaultLLMModel),
			BaseURL:              envOrDefault("TMA_LLM_BASE_URL", DefaultLLMBaseURL),
			APIKeyEnv:            envOrDefault("TMA_LLM_API_KEY_ENV", DefaultLLMAPIKeyEnv),
			MaxAttempts:          envIntegerOrDefault("TMA_LLM_MAX_ATTEMPTS", DefaultLLMMaxAttempts),
			RetryBaseDelayMillis: envIntegerOrDefault("TMA_LLM_RETRY_BASE_DELAY_MS", DefaultLLMRetryBaseDelayMS),
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
		Skills: SkillsConfig{
			BinaryScanner: SkillsBinaryScannerConfig{
				Provider:           envOrDefault("TMA_SKILLS_BINARY_SCANNER_PROVIDER", DefaultSkillsBinaryScannerProvider),
				Endpoint:           os.Getenv("TMA_SKILLS_BINARY_SCANNER_ENDPOINT"),
				TokenEnv:           envOrDefault("TMA_SKILLS_BINARY_SCANNER_TOKEN_ENV", DefaultSkillsBinaryScannerTokenEnv),
				TimeoutSeconds:     envIntOrDefault("TMA_SKILLS_BINARY_SCANNER_TIMEOUT_SECONDS", DefaultSkillsBinaryScannerTimeoutSec),
				MaxAttempts:        envIntOrDefault("TMA_SKILLS_BINARY_SCANNER_MAX_ATTEMPTS", DefaultSkillsBinaryScannerMaxAttempts),
				PollIntervalMillis: envIntOrDefault("TMA_SKILLS_BINARY_SCANNER_POLL_INTERVAL_MS", DefaultSkillsBinaryScannerPollIntervalMS),
			},
			AssetRetention: SkillsAssetRetentionConfig{
				Enabled:               envBoolOrDefault("TMA_SKILLS_ASSET_RETENTION_ENABLED", DefaultSkillsAssetRetentionEnabled),
				RetentionDays:         envIntOrDefault("TMA_SKILLS_ASSET_RETENTION_DAYS", DefaultSkillsAssetRetentionDays),
				DeleteLimit:           envIntOrDefault("TMA_SKILLS_ASSET_GC_DELETE_LIMIT", DefaultSkillsAssetGCDeleteLimit),
				WorkerEnabled:         envBoolOrDefault("TMA_SKILLS_ASSET_GC_WORKER_ENABLED", DefaultSkillsAssetGCWorkerEnabled),
				WorkerIntervalSeconds: envIntOrDefault("TMA_SKILLS_ASSET_GC_WORKER_INTERVAL_SECONDS", DefaultSkillsAssetGCWorkerIntervalSec),
			},
		},
		ToolRuntime: ToolRuntimeConfig{
			Runtime:                         envOrDefault("TMA_TOOL_RUNTIME", DefaultToolRuntime),
			Root:                            envOrDefault("TMA_CLOUD_SANDBOX_ROOT", DefaultCloudSandboxWorkspaceRoot),
			Image:                           os.Getenv("TMA_CLOUD_SANDBOX_IMAGE"),
			DataRoot:                        envOrDefault("TMA_CLOUD_SANDBOX_DATA_ROOT", DefaultCloudSandboxDataRoot),
			DataTTLSeconds:                  envIntOrDefault("TMA_CLOUD_SANDBOX_DATA_TTL_SECONDS", DefaultCloudSandboxDataTTLSec),
			ContainerIdleTTLSeconds:         envIntOrDefault("TMA_CLOUD_SANDBOX_CONTAINER_IDLE_TTL_SECONDS", DefaultCloudSandboxContainerIdleTTLSec),
			ContainerMaxLifetimeSeconds:     envIntOrDefault("TMA_CLOUD_SANDBOX_CONTAINER_MAX_LIFETIME_SECONDS", DefaultCloudSandboxContainerMaxLifetimeSec),
			ContainerCleanupIntervalSeconds: envIntOrDefault("TMA_CLOUD_SANDBOX_CONTAINER_CLEANUP_INTERVAL_SECONDS", DefaultCloudSandboxContainerCleanupIntervalSec),
			AllowNetwork:                    envBoolOrDefault("TMA_CLOUD_SANDBOX_ALLOW_NETWORK", DefaultCloudSandboxAllowNetwork),
			AllowLocalSystem:                envBoolOrDefault("TMA_ALLOW_SERVER_LOCAL_SYSTEM", false),
			ReadFileLimits: capability.ReadFileLimits{
				DefaultMaxBytes: envIntOrDefault("TMA_READ_FILE_DEFAULT_MAX_BYTES", capability.DefaultReadFileDefaultMaxBytes),
				HardMaxBytes:    envIntOrDefault("TMA_READ_FILE_HARD_MAX_BYTES", capability.DefaultReadFileHardMaxBytes),
				SmallFileBytes:  envIntOrDefault("TMA_READ_FILE_SMALL_FILE_BYTES", capability.DefaultReadFileSmallFileBytes),
				MaxLines:        envIntOrDefault("TMA_READ_FILE_MAX_LINES", capability.DefaultReadFileMaxLines),
			},
		},
		MCP: MCPConfig{
			StdioHost: MCPStdioHostConfig{
				IdleTimeoutSeconds:   envIntegerOrDefault("TMA_MCP_STDIO_HOST_IDLE_TIMEOUT_SECONDS", DefaultMCPStdioHostIdleTimeoutSec),
				SweepIntervalSeconds: envIntegerOrDefault("TMA_MCP_STDIO_HOST_SWEEP_INTERVAL_SECONDS", DefaultMCPStdioHostSweepIntervalSec),
				MaxSessions:          envIntegerOrDefault("TMA_MCP_STDIO_HOST_MAX_SESSIONS", DefaultMCPStdioHostMaxSessions),
			},
			StreamableHTTPHost: MCPStreamableHTTPHostConfig{
				IdleTimeoutSeconds:         envIntegerOrDefault("TMA_MCP_HTTP_HOST_IDLE_TIMEOUT_SECONDS", DefaultMCPHTTPHostIdleTimeoutSec),
				SweepIntervalSeconds:       envIntegerOrDefault("TMA_MCP_HTTP_HOST_SWEEP_INTERVAL_SECONDS", DefaultMCPHTTPHostSweepIntervalSec),
				MaxSessions:                envIntegerOrDefault("TMA_MCP_HTTP_HOST_MAX_SESSIONS", DefaultMCPHTTPHostMaxSessions),
				EgressAllowHTTP:            envBoolOrDefault("TMA_MCP_HTTP_EGRESS_ALLOW_HTTP", DefaultMCPHTTPEgressAllowHTTP),
				EgressAllowPrivateNetworks: envBoolOrDefault("TMA_MCP_HTTP_EGRESS_ALLOW_PRIVATE_NETWORKS", DefaultMCPHTTPEgressAllowPrivateNetworks),
				EgressAllowedHosts:         splitCommaSeparated(os.Getenv("TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS")),
				EgressAllowedCIDRs:         splitCommaSeparated(os.Getenv("TMA_MCP_HTTP_EGRESS_ALLOWED_CIDRS")),
				CABundlePath:               strings.TrimSpace(os.Getenv("TMA_MCP_HTTP_CA_BUNDLE")),
			},
		},
		Subagent: SubagentConfig{
			MaxDepth:              envNonNegativeIntOrDefault("TMA_SUBAGENT_MAX_DEPTH", DefaultSubagentMaxDepth),
			MaxChildrenPerTurn:    envNonNegativeIntOrDefault("TMA_SUBAGENT_MAX_CHILDREN_PER_TURN", DefaultSubagentMaxChildrenPerTurn),
			MaxChildrenPerSession: envNonNegativeIntOrDefault("TMA_SUBAGENT_MAX_CHILDREN_PER_SESSION", DefaultSubagentMaxChildrenPerSession),
			WorkspaceActiveLimit:  envNonNegativeIntOrDefault("TMA_SUBAGENT_WORKSPACE_ACTIVE_LIMIT", DefaultSubagentWorkspaceActiveLimit),
			UserActiveLimit:       envNonNegativeIntOrDefault("TMA_SUBAGENT_USER_ACTIVE_LIMIT", DefaultSubagentUserActiveLimit),
			WorkspaceQueuedLimit:  envNonNegativeIntOrDefault("TMA_SUBAGENT_WORKSPACE_QUEUE_LIMIT", DefaultSubagentWorkspaceQueuedLimit),
			UserQueuedLimit:       envNonNegativeIntOrDefault("TMA_SUBAGENT_USER_QUEUE_LIMIT", DefaultSubagentUserQueuedLimit),
			QueueTimeoutSeconds:   envNonNegativeIntOrDefault("TMA_SUBAGENT_QUEUE_TIMEOUT_SECONDS", DefaultSubagentQueueTimeoutSeconds),
		},
		Worker: WorkerConfig{
			AuthToken:        os.Getenv("TMA_WORKER_AUTH_TOKEN"),
			AuthWorkspaceID:  envOrDefault("TMA_WORKER_AUTH_WORKSPACE_ID", DefaultWorkerAuthWorkspaceID),
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
			SecurityAudit: SecurityAuditExporterConfig{
				OTLPEndpoint:            strings.TrimSpace(os.Getenv("TMA_SECURITY_AUDIT_OTLP_ENDPOINT")),
				OTLPToken:               strings.TrimSpace(os.Getenv("TMA_SECURITY_AUDIT_OTLP_TOKEN")),
				IntegrityKey:            strings.TrimSpace(os.Getenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY")),
				IntegrityKeyID:          strings.TrimSpace(os.Getenv("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID")),
				IntegrityKeys:           integrityKeys,
				Durable:                 envBoolOrDefault("TMA_SECURITY_AUDIT_DURABLE", DefaultSecurityAuditDurable),
				QueueSize:               envIntOrDefault("TMA_SECURITY_AUDIT_QUEUE_SIZE", DefaultSecurityAuditQueueSize),
				BatchSize:               envIntOrDefault("TMA_SECURITY_AUDIT_BATCH_SIZE", DefaultSecurityAuditBatchSize),
				FlushIntervalMillis:     envIntOrDefault("TMA_SECURITY_AUDIT_FLUSH_INTERVAL_MS", DefaultSecurityAuditFlushIntervalMS),
				HTTPTimeoutSeconds:      envIntOrDefault("TMA_SECURITY_AUDIT_HTTP_TIMEOUT_SECONDS", DefaultSecurityAuditHTTPTimeoutSeconds),
				WorkerIntervalMillis:    envIntOrDefault("TMA_SECURITY_AUDIT_WORKER_INTERVAL_MS", DefaultSecurityAuditWorkerIntervalMS),
				LeaseDurationMillis:     envIntOrDefault("TMA_SECURITY_AUDIT_LEASE_DURATION_MS", DefaultSecurityAuditLeaseDurationMS),
				MaxAttempts:             envIntOrDefault("TMA_SECURITY_AUDIT_MAX_ATTEMPTS", DefaultSecurityAuditMaxAttempts),
				RetryInitialDelayMillis: envIntOrDefault("TMA_SECURITY_AUDIT_RETRY_INITIAL_DELAY_MS", DefaultSecurityAuditRetryInitialDelayMS),
				RetryMaxDelayMillis:     envIntOrDefault("TMA_SECURITY_AUDIT_RETRY_MAX_DELAY_MS", DefaultSecurityAuditRetryMaxDelayMS),
				RetentionDays:           envIntOrDefault("TMA_SECURITY_AUDIT_RETENTION_DAYS", DefaultSecurityAuditRetentionDays),
				PruneIntervalMillis:     envIntOrDefault("TMA_SECURITY_AUDIT_PRUNE_INTERVAL_MS", DefaultSecurityAuditPruneIntervalMS),
				PruneLimit:              envIntOrDefault("TMA_SECURITY_AUDIT_PRUNE_LIMIT", DefaultSecurityAuditPruneLimit),
			},
		},
		Auth: AuthConfig{
			Mode:                    authMode,
			JWTSecret:               os.Getenv("TMA_AUTH_JWT_SECRET"),
			JWTIssuer:               os.Getenv("TMA_AUTH_JWT_ISSUER"),
			JWTAudience:             os.Getenv("TMA_AUTH_JWT_AUDIENCE"),
			OIDCIssuer:              strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_ISSUER")),
			OIDCAudience:            strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_AUDIENCE")),
			OIDCJWKSURL:             strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_JWKS_URL")),
			OIDCSigningAlgs:         splitCommaSeparated(envOrDefault("TMA_AUTH_OIDC_SIGNING_ALGS", DefaultOIDCSigningAlgorithms)),
			OIDCHTTPTimeoutSecs:     envIntOrDefault("TMA_AUTH_OIDC_HTTP_TIMEOUT_SECONDS", DefaultOIDCHTTPTimeoutSeconds),
			OIDCRefreshIntervalSecs: envIntOrDefault("TMA_AUTH_OIDC_REFRESH_INTERVAL_SECONDS", DefaultOIDCRefreshIntervalSeconds),
			OIDCMaxStaleSecs:        envIntOrDefault("TMA_AUTH_OIDC_MAX_STALE_SECONDS", DefaultOIDCMaxStaleSeconds),
			OIDCClaimMapping:        claimMapping,
			OIDCWebLoginEnabled:     envBoolOrDefault("TMA_AUTH_OIDC_WEB_LOGIN_ENABLED", false),
			OIDCWebClientID:         strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_WEB_CLIENT_ID")),
			OIDCWebClientSecret:     strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_WEB_CLIENT_SECRET")),
			OIDCWebRedirectURL:      strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_WEB_REDIRECT_URL")),
			OIDCWebPostLogoutURL:    strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_WEB_POST_LOGOUT_URL")),
			OIDCWebSessionSecret:    strings.TrimSpace(os.Getenv("TMA_AUTH_OIDC_WEB_SESSION_SECRET")),
			OIDCCLIClientID:         strings.TrimSpace(envOrDefault("TMA_AUTH_OIDC_CLI_CLIENT_ID", DefaultOIDCCLIClientID)),
			CookieTrustedOrigins:    splitCommaSeparated(os.Getenv("TMA_AUTH_COOKIE_TRUSTED_ORIGINS")),
			GatewayToken:            os.Getenv("TMA_AUTH_GATEWAY_TOKEN"),
			GatewayTrustedCIDRs:     splitCommaSeparated(os.Getenv("TMA_AUTH_GATEWAY_TRUSTED_CIDRS")),
		},
	}
	config.Turn.Timeout = time.Duration(config.Turn.TimeoutMillis) * time.Millisecond
	config.Turn.PollInterval = time.Duration(config.Turn.PollIntervalMillis) * time.Millisecond
	config.Turn.LeaseDuration = time.Duration(config.Turn.LeaseDurationMillis) * time.Millisecond
	config.Turn.HeartbeatInterval = time.Duration(config.Turn.HeartbeatIntervalMillis) * time.Millisecond
	config.ToolRuntime.DataTTL = time.Duration(config.ToolRuntime.DataTTLSeconds) * time.Second
	config.Skills.BinaryScanner.Timeout = time.Duration(config.Skills.BinaryScanner.TimeoutSeconds) * time.Second
	config.Skills.BinaryScanner.PollInterval = time.Duration(config.Skills.BinaryScanner.PollIntervalMillis) * time.Millisecond
	config.Skills.AssetRetention.WorkerInterval = time.Duration(config.Skills.AssetRetention.WorkerIntervalSeconds) * time.Second
	config.ToolRuntime.ContainerIdleTTL = time.Duration(config.ToolRuntime.ContainerIdleTTLSeconds) * time.Second
	config.ToolRuntime.ContainerMaxLifetime = time.Duration(config.ToolRuntime.ContainerMaxLifetimeSeconds) * time.Second
	config.ToolRuntime.ContainerCleanupInterval = time.Duration(config.ToolRuntime.ContainerCleanupIntervalSeconds) * time.Second
	config.MCP.StdioHost.IdleTimeout = time.Duration(config.MCP.StdioHost.IdleTimeoutSeconds) * time.Second
	config.MCP.StdioHost.SweepInterval = time.Duration(config.MCP.StdioHost.SweepIntervalSeconds) * time.Second
	config.MCP.StreamableHTTPHost.IdleTimeout = time.Duration(config.MCP.StreamableHTTPHost.IdleTimeoutSeconds) * time.Second
	config.MCP.StreamableHTTPHost.SweepInterval = time.Duration(config.MCP.StreamableHTTPHost.SweepIntervalSeconds) * time.Second
	config.Worker.Reaper.Interval = time.Duration(config.Worker.Reaper.IntervalMillis) * time.Millisecond
	config.Worker.WorkReaper.Interval = time.Duration(config.Worker.WorkReaper.IntervalMillis) * time.Millisecond
	config.Observability.ExporterRetry.Interval = time.Duration(config.Observability.ExporterRetry.IntervalMillis) * time.Millisecond
	config.Observability.TraceIndexRetention.Retention = time.Duration(config.Observability.TraceIndexRetention.RetentionDays) * 24 * time.Hour
	config.Observability.TraceIndexRetention.Interval = time.Duration(config.Observability.TraceIndexRetention.IntervalMillis) * time.Millisecond
	config.Observability.SecurityAudit.FlushInterval = time.Duration(config.Observability.SecurityAudit.FlushIntervalMillis) * time.Millisecond
	config.Observability.SecurityAudit.HTTPTimeout = time.Duration(config.Observability.SecurityAudit.HTTPTimeoutSeconds) * time.Second
	config.Observability.SecurityAudit.WorkerInterval = time.Duration(config.Observability.SecurityAudit.WorkerIntervalMillis) * time.Millisecond
	config.Observability.SecurityAudit.LeaseDuration = time.Duration(config.Observability.SecurityAudit.LeaseDurationMillis) * time.Millisecond
	config.Observability.SecurityAudit.RetryInitialDelay = time.Duration(config.Observability.SecurityAudit.RetryInitialDelayMillis) * time.Millisecond
	config.Observability.SecurityAudit.RetryMaxDelay = time.Duration(config.Observability.SecurityAudit.RetryMaxDelayMillis) * time.Millisecond
	config.Observability.SecurityAudit.Retention = time.Duration(config.Observability.SecurityAudit.RetentionDays) * 24 * time.Hour
	config.Observability.SecurityAudit.PruneInterval = time.Duration(config.Observability.SecurityAudit.PruneIntervalMillis) * time.Millisecond
	config.LLM.RetryBaseDelay = time.Duration(config.LLM.RetryBaseDelayMillis) * time.Millisecond
	config.LLM.APIKey = os.Getenv(config.LLM.APIKeyEnv)
	config.ObjectStore.AccessKey = os.Getenv(config.ObjectStore.AccessKeyEnv)
	config.ObjectStore.SecretKey = os.Getenv(config.ObjectStore.SecretKeyEnv)
	config.Skills.BinaryScanner.Token = os.Getenv(config.Skills.BinaryScanner.TokenEnv)

	if config.DatabaseURL == "" {
		return Config{}, errors.New("TMA_DATABASE_URL is required")
	}
	if config.LLM.MaxAttempts < 1 || config.LLM.MaxAttempts > 10 {
		return Config{}, errors.New("TMA_LLM_MAX_ATTEMPTS must be between 1 and 10")
	}
	if config.LLM.RetryBaseDelayMillis < 1 || config.LLM.RetryBaseDelayMillis > 60000 {
		return Config{}, errors.New("TMA_LLM_RETRY_BASE_DELAY_MS must be between 1 and 60000")
	}
	if err := config.ToolRuntime.ReadFileLimits.Validate(); err != nil {
		return Config{}, err
	}
	if err := validateAuthConfig(config); err != nil {
		return Config{}, err
	}
	if config.Skills.AssetRetention.RetentionDays < 1 || config.Skills.AssetRetention.RetentionDays > 3650 {
		return Config{}, errors.New("TMA_SKILLS_ASSET_RETENTION_DAYS must be between 1 and 3650")
	}
	if config.Skills.AssetRetention.DeleteLimit < 1 || config.Skills.AssetRetention.DeleteLimit > 1000 {
		return Config{}, errors.New("TMA_SKILLS_ASSET_GC_DELETE_LIMIT must be between 1 and 1000")
	}
	if config.Skills.AssetRetention.WorkerIntervalSeconds < 1 {
		return Config{}, errors.New("TMA_SKILLS_ASSET_GC_WORKER_INTERVAL_SECONDS must be positive")
	}
	if config.MCP.StdioHost.IdleTimeoutSeconds < 1 {
		return Config{}, errors.New("TMA_MCP_STDIO_HOST_IDLE_TIMEOUT_SECONDS must be positive")
	}
	if config.MCP.StdioHost.SweepIntervalSeconds < 1 {
		return Config{}, errors.New("TMA_MCP_STDIO_HOST_SWEEP_INTERVAL_SECONDS must be positive")
	}
	if config.MCP.StdioHost.MaxSessions < 1 || config.MCP.StdioHost.MaxSessions > 10000 {
		return Config{}, errors.New("TMA_MCP_STDIO_HOST_MAX_SESSIONS must be between 1 and 10000")
	}
	if config.MCP.StreamableHTTPHost.IdleTimeoutSeconds < 1 {
		return Config{}, errors.New("TMA_MCP_HTTP_HOST_IDLE_TIMEOUT_SECONDS must be positive")
	}
	if config.MCP.StreamableHTTPHost.SweepIntervalSeconds < 1 {
		return Config{}, errors.New("TMA_MCP_HTTP_HOST_SWEEP_INTERVAL_SECONDS must be positive")
	}
	if config.MCP.StreamableHTTPHost.MaxSessions < 1 || config.MCP.StreamableHTTPHost.MaxSessions > 10000 {
		return Config{}, errors.New("TMA_MCP_HTTP_HOST_MAX_SESSIONS must be between 1 and 10000")
	}
	if err := validateMCPHTTPEgressConfig(config.MCP.StreamableHTTPHost); err != nil {
		return Config{}, err
	}
	if config.Observability.SecurityAudit.QueueSize < 1 || config.Observability.SecurityAudit.QueueSize > 1000000 {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_QUEUE_SIZE must be between 1 and 1000000")
	}
	if config.Observability.SecurityAudit.BatchSize < 1 || config.Observability.SecurityAudit.BatchSize > config.Observability.SecurityAudit.QueueSize {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_BATCH_SIZE must be positive and no larger than TMA_SECURITY_AUDIT_QUEUE_SIZE")
	}
	if config.Observability.SecurityAudit.FlushIntervalMillis < 10 || config.Observability.SecurityAudit.FlushIntervalMillis > 60000 {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_FLUSH_INTERVAL_MS must be between 10 and 60000")
	}
	if config.Observability.SecurityAudit.HTTPTimeoutSeconds < 1 || config.Observability.SecurityAudit.HTTPTimeoutSeconds > 120 {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_HTTP_TIMEOUT_SECONDS must be between 1 and 120")
	}
	if strings.TrimSpace(config.Observability.SecurityAudit.OTLPEndpoint) != "" {
		production := strings.EqualFold(config.Environment, "production") || strings.EqualFold(config.Environment, "prod")
		if err := validateAuthURL("TMA_SECURITY_AUDIT_OTLP_ENDPOINT", config.Observability.SecurityAudit.OTLPEndpoint, production); err != nil {
			return Config{}, err
		}
		if production && !config.Observability.SecurityAudit.Durable {
			return Config{}, errors.New("TMA_SECURITY_AUDIT_DURABLE must be enabled in production")
		}
		activeIntegrityKey, err := securityAuditActiveIntegrityKey(config.Observability.SecurityAudit)
		if err != nil {
			return Config{}, err
		}
		if production && len(activeIntegrityKey) < 32 {
			return Config{}, errors.New("TMA_SECURITY_AUDIT_INTEGRITY_KEY or the active TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON key must be at least 32 bytes in production")
		}
		if production {
			for keyID, key := range config.Observability.SecurityAudit.IntegrityKeys {
				if len(key) < 32 {
					return Config{}, fmt.Errorf("TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON key %q must be at least 32 bytes in production", keyID)
				}
			}
		}
	}
	if config.Observability.SecurityAudit.WorkerIntervalMillis < 10 || config.Observability.SecurityAudit.WorkerIntervalMillis > 60000 {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_WORKER_INTERVAL_MS must be between 10 and 60000")
	}
	if config.Observability.SecurityAudit.LeaseDurationMillis <= config.Observability.SecurityAudit.WorkerIntervalMillis {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_LEASE_DURATION_MS must be greater than TMA_SECURITY_AUDIT_WORKER_INTERVAL_MS")
	}
	if config.Observability.SecurityAudit.MaxAttempts < 1 || config.Observability.SecurityAudit.MaxAttempts > 100 {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_MAX_ATTEMPTS must be between 1 and 100")
	}
	if config.Observability.SecurityAudit.RetryInitialDelayMillis < 10 || config.Observability.SecurityAudit.RetryMaxDelayMillis < config.Observability.SecurityAudit.RetryInitialDelayMillis {
		return Config{}, errors.New("TMA security audit retry delay settings are invalid")
	}
	if config.Observability.SecurityAudit.RetentionDays < 1 || config.Observability.SecurityAudit.RetentionDays > 3650 {
		return Config{}, errors.New("TMA_SECURITY_AUDIT_RETENTION_DAYS must be between 1 and 3650")
	}
	if config.Observability.SecurityAudit.PruneIntervalMillis < 1000 || config.Observability.SecurityAudit.PruneLimit < 1 || config.Observability.SecurityAudit.PruneLimit > 10000 {
		return Config{}, errors.New("TMA security audit prune settings are invalid")
	}

	return config, nil
}

func validateAuthConfig(config Config) error {
	environment := strings.ToLower(strings.TrimSpace(config.Environment))
	switch environment {
	case "development", "dev", "test", "production", "prod":
	default:
		return fmt.Errorf("unsupported TMA_ENV %q", config.Environment)
	}
	production := environment == "production" || environment == "prod"
	switch config.Auth.Mode {
	case "disabled":
		if production {
			return errors.New("TMA_AUTH_MODE must be oidc, jwt, or gateway in production")
		}
	case "jwt":
		if strings.TrimSpace(config.Auth.JWTSecret) == "" {
			return errors.New("TMA_AUTH_JWT_SECRET is required when TMA_AUTH_MODE=jwt")
		}
		if production && len(config.Auth.JWTSecret) < 32 {
			return errors.New("TMA_AUTH_JWT_SECRET must be at least 32 bytes in production")
		}
		if production && strings.TrimSpace(config.Auth.JWTIssuer) == "" {
			return errors.New("TMA_AUTH_JWT_ISSUER is required in production")
		}
		if production && strings.TrimSpace(config.Auth.JWTAudience) == "" {
			return errors.New("TMA_AUTH_JWT_AUDIENCE is required in production")
		}
	case "oidc":
		if strings.TrimSpace(config.Auth.OIDCIssuer) == "" {
			return errors.New("TMA_AUTH_OIDC_ISSUER is required when TMA_AUTH_MODE=oidc")
		}
		if strings.TrimSpace(config.Auth.OIDCAudience) == "" {
			return errors.New("TMA_AUTH_OIDC_AUDIENCE is required when TMA_AUTH_MODE=oidc")
		}
		if config.Auth.OIDCHTTPTimeoutSecs < 1 || config.Auth.OIDCHTTPTimeoutSecs > 120 {
			return errors.New("TMA_AUTH_OIDC_HTTP_TIMEOUT_SECONDS must be between 1 and 120")
		}
		if config.Auth.OIDCRefreshIntervalSecs < 1 {
			return errors.New("TMA_AUTH_OIDC_REFRESH_INTERVAL_SECONDS must be positive")
		}
		if config.Auth.OIDCMaxStaleSecs < config.Auth.OIDCRefreshIntervalSecs {
			return errors.New("TMA_AUTH_OIDC_MAX_STALE_SECONDS must be greater than or equal to TMA_AUTH_OIDC_REFRESH_INTERVAL_SECONDS")
		}
		if err := validateOIDCSigningAlgorithms(config.Auth.OIDCSigningAlgs); err != nil {
			return err
		}
		if err := validateAuthURL("TMA_AUTH_OIDC_ISSUER", config.Auth.OIDCIssuer, production); err != nil {
			return err
		}
		if strings.TrimSpace(config.Auth.OIDCJWKSURL) != "" {
			if err := validateAuthURL("TMA_AUTH_OIDC_JWKS_URL", config.Auth.OIDCJWKSURL, production); err != nil {
				return err
			}
		}
		if production && !config.Auth.OIDCClaimMapping.HasTenantRestriction() {
			return errors.New("TMA_AUTH_OIDC_CLAIM_MAPPING_JSON must configure allowed_workspace_ids or a workspace group mapping in production")
		}
		if config.Auth.OIDCWebLoginEnabled {
			if config.Auth.OIDCWebClientID == "" {
				return errors.New("TMA_AUTH_OIDC_WEB_CLIENT_ID is required when browser OIDC login is enabled")
			}
			if config.Auth.OIDCWebRedirectURL == "" {
				return errors.New("TMA_AUTH_OIDC_WEB_REDIRECT_URL is required when browser OIDC login is enabled")
			}
			if err := validateAuthURL("TMA_AUTH_OIDC_WEB_REDIRECT_URL", config.Auth.OIDCWebRedirectURL, production); err != nil {
				return err
			}
			if config.Auth.OIDCWebPostLogoutURL != "" {
				if err := validateAuthURL("TMA_AUTH_OIDC_WEB_POST_LOGOUT_URL", config.Auth.OIDCWebPostLogoutURL, production); err != nil {
					return err
				}
			}
			if len(config.Auth.OIDCWebSessionSecret) < 32 {
				return errors.New("TMA_AUTH_OIDC_WEB_SESSION_SECRET must be at least 32 bytes when browser OIDC login is enabled")
			}
		}
	case "gateway":
		if strings.TrimSpace(config.Auth.GatewayToken) == "" {
			return errors.New("TMA_AUTH_GATEWAY_TOKEN is required when TMA_AUTH_MODE=gateway")
		}
		if len(config.Auth.GatewayTrustedCIDRs) == 0 {
			return errors.New("TMA_AUTH_GATEWAY_TRUSTED_CIDRS is required when TMA_AUTH_MODE=gateway")
		}
		for _, raw := range config.Auth.GatewayTrustedCIDRs {
			if _, _, err := net.ParseCIDR(raw); err != nil {
				return fmt.Errorf("invalid TMA_AUTH_GATEWAY_TRUSTED_CIDRS entry %q: %w", raw, err)
			}
		}
		if production && len(config.Auth.GatewayToken) < 32 {
			return errors.New("TMA_AUTH_GATEWAY_TOKEN must be at least 32 bytes in production")
		}
	default:
		return fmt.Errorf("unsupported TMA_AUTH_MODE %q", config.Auth.Mode)
	}
	if production && strings.TrimSpace(config.Worker.AuthToken) == "" {
		return errors.New("TMA_WORKER_AUTH_TOKEN is required in production")
	}
	for _, origin := range config.Auth.CookieTrustedOrigins {
		if err := validateAuthOrigin("TMA_AUTH_COOKIE_TRUSTED_ORIGINS", origin, production); err != nil {
			return err
		}
	}
	return nil
}

func validateOIDCSigningAlgorithms(algorithms []string) error {
	if len(algorithms) == 0 {
		return errors.New("TMA_AUTH_OIDC_SIGNING_ALGS must contain RS256 or ES256")
	}
	seen := map[string]bool{}
	for _, algorithm := range algorithms {
		algorithm = strings.ToUpper(strings.TrimSpace(algorithm))
		if algorithm != "RS256" && algorithm != "ES256" {
			return fmt.Errorf("unsupported TMA_AUTH_OIDC_SIGNING_ALGS entry %q", algorithm)
		}
		seen[algorithm] = true
	}
	if len(seen) == 0 {
		return errors.New("TMA_AUTH_OIDC_SIGNING_ALGS must contain RS256 or ES256")
	}
	return nil
}

func validateAuthURL(name string, raw string, requireHTTPS bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must be an absolute URL without credentials, query, or fragment", name)
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use https in production", name)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%s must use http or https", name)
	}
	return nil
}

func validateAuthOrigin(name string, raw string, requireHTTPS bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s entries must be origins such as https://app.example.com", name)
	}
	if requireHTTPS && parsed.Scheme != "https" {
		return fmt.Errorf("%s entries must use https in production", name)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%s entries must use http or https", name)
	}
	return nil
}

func parseSecurityAuditIntegrityKeys(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var keys map[string]string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, fmt.Errorf("must be a JSON object of key IDs to secrets: %w", err)
	}
	if len(keys) == 0 {
		return nil, errors.New("must contain at least one integrity key")
	}
	for keyID, key := range keys {
		if strings.TrimSpace(keyID) != keyID || !validSecurityAuditIntegrityKeyID(keyID) {
			return nil, fmt.Errorf("contains invalid key id %q", keyID)
		}
		if key == "" {
			return nil, fmt.Errorf("contains an empty key for id %q", keyID)
		}
	}
	return keys, nil
}

func securityAuditActiveIntegrityKey(config SecurityAuditExporterConfig) (string, error) {
	keyID := strings.TrimSpace(config.IntegrityKeyID)
	if keyID != "" && !validSecurityAuditIntegrityKeyID(keyID) {
		return "", errors.New("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID is invalid")
	}
	if len(config.IntegrityKeys) > 0 {
		if keyID == "" {
			return "", errors.New("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID is required with TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON")
		}
		key, ok := config.IntegrityKeys[keyID]
		if !ok {
			return "", fmt.Errorf("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID %q is not present in TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON", keyID)
		}
		return key, nil
	}
	if config.IntegrityKey == "" && keyID != "" {
		return "", errors.New("TMA_SECURITY_AUDIT_INTEGRITY_KEY_ID requires TMA_SECURITY_AUDIT_INTEGRITY_KEY or TMA_SECURITY_AUDIT_INTEGRITY_KEYS_JSON")
	}
	return config.IntegrityKey, nil
}

func validSecurityAuditIntegrityKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > 128 {
		return false
	}
	for _, char := range keyID {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func validateMCPHTTPEgressConfig(config MCPStreamableHTTPHostConfig) error {
	for _, host := range config.EgressAllowedHosts {
		host = strings.TrimSpace(strings.ToLower(host))
		if strings.HasPrefix(host, "*.") {
			host = strings.TrimPrefix(host, "*.")
		}
		if host == "" || strings.ContainsAny(host, "*/?#@") || strings.Contains(host, "://") {
			return fmt.Errorf("TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS contains invalid host %q", host)
		}
		if strings.Contains(host, ":") {
			if _, err := netip.ParseAddr(host); err != nil {
				return fmt.Errorf("TMA_MCP_HTTP_EGRESS_ALLOWED_HOSTS contains invalid host %q", host)
			}
		}
	}
	for _, raw := range config.EgressAllowedCIDRs {
		if _, err := netip.ParsePrefix(strings.TrimSpace(raw)); err != nil {
			return fmt.Errorf("TMA_MCP_HTTP_EGRESS_ALLOWED_CIDRS contains invalid CIDR %q", raw)
		}
	}
	return nil
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

func envIntegerOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envIntInRange(key string, fallback, minimum, maximum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return parsed, nil
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
