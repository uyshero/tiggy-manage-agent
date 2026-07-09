package managedagents

import (
	"encoding/json"
	"time"
)

const (
	DefaultWorkspaceID         = "wksp_default"
	DefaultContextWindowTokens = 128000
	ContextBudgetRatioPercent  = 60

	SessionStatusProvisioning = "provisioning"
	SessionStatusIdle         = "idle"
	SessionStatusRunning      = "running"
	SessionStatusInterrupting = "interrupting"
	SessionStatusCompacting   = "compacting"
	// failed 保留给系统级 Session 故障；普通 Runner 执行失败只标记 turn failed 并回到 idle。
	SessionStatusFailed     = "failed"
	SessionStatusTerminated = "terminated"

	EventSessionStatusProvisioning = "session.status_provisioning"
	EventSessionStatusIdle         = "session.status_idle"
	EventSessionStatusRunning      = "session.status_running"
	EventSessionStatusInterrupting = "session.status_interrupting"
	EventSessionStatusCompacting   = "session.status_compacting"
	// session.status_failed 保留给整个 Session 不可继续的故障，不用于普通 turn 失败。
	EventSessionStatusFailed     = "session.status_failed"
	EventSessionStatusTerminated = "session.status_terminated"

	EventUserMessage   = "user.message"
	EventUserInterrupt = "user.interrupt"
	EventAgentMessage  = "agent.message"

	EventRuntimeStarted                  = "runtime.started"
	EventRuntimeThinking                 = "runtime.thinking"
	EventRuntimeLLMRequest               = "runtime.llm_request"
	EventRuntimeLLMDelta                 = "runtime.llm_delta"
	EventRuntimeLLMResponse              = "runtime.llm_response"
	EventRuntimeToolCall                 = "runtime.tool_call"
	EventRuntimeToolInterventionRequired = "runtime.tool_intervention_required"
	EventRuntimeToolInterventionApproved = "runtime.tool_intervention_approved"
	EventRuntimeToolInterventionRejected = "runtime.tool_intervention_rejected"
	EventRuntimeToolResult               = "runtime.tool_result"
	EventRuntimeContextCompacting        = "runtime.context_compacting"
	EventRuntimeContextCompacted         = "runtime.context_compacted"
	EventRuntimeContextCompactionFailed  = "runtime.context_compaction_failed"
	EventRuntimeCompleted                = "runtime.completed"
	EventRuntimeFailed                   = "runtime.failed"
	EventSessionConfigUpdated            = "session.config_updated"

	InterventionStatusPending  = "pending"
	InterventionStatusApproved = "approved"
	InterventionStatusRejected = "rejected"

	TurnStatusRunning         = "running"
	TurnStatusWaitingApproval = "waiting_approval"
	TurnStatusInterrupted     = "interrupted"
	TurnStatusCompleted       = "completed"
	TurnStatusFailed          = "failed"

	ObjectStorageProviderS3   = "s3"
	ObjectVisibilitySession   = "session"
	ObjectVisibilityWorkspace = "workspace"

	ArtifactTypeFile     = "file"
	ArtifactTypeSnapshot = "snapshot"
	ArtifactTypeAsset    = "asset"

	WorkerTypeLocal  = "local"
	WorkerTypeShared = "shared"
	WorkerTypeCloud  = "cloud"

	WorkerStatusOnline   = "online"
	WorkerStatusOffline  = "offline"
	WorkerStatusDraining = "draining"
	WorkerStatusArchived = "archived"

	WorkerWorkTypeToolExecution  = "tool_execution"
	WorkerWorkTypeSandboxCommand = "sandbox_command"
	WorkerWorkTypeArtifactSync   = "artifact_sync"

	WorkerWorkStatusPending   = "pending"
	WorkerWorkStatusLeased    = "leased"
	WorkerWorkStatusRunning   = "running"
	WorkerWorkStatusCompleted = "completed"
	WorkerWorkStatusFailed    = "failed"
	WorkerWorkStatusCanceled  = "canceled"
)

type Agent struct {
	ID                   string             `json:"id"`
	WorkspaceID          string             `json:"workspace_id"`
	Name                 string             `json:"name"`
	CurrentConfigVersion int                `json:"current_config_version"`
	ConfigVersion        AgentConfigVersion `json:"config_version"`
	ArchivedAt           *time.Time         `json:"archived_at,omitempty"`
	CreatedAt            time.Time          `json:"created_at"`
}

type AgentConfigVersion struct {
	Version     int             `json:"version"`
	LLMProvider string          `json:"llm_provider"`
	LLMModel    string          `json:"llm_model"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type LLMProvider struct {
	ID           string    `json:"id"`
	ProviderType string    `json:"provider_type"`
	BaseURL      string    `json:"base_url,omitempty"`
	APIKeyEnv    string    `json:"api_key_env,omitempty"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

type LLMModel struct {
	ProviderID          string    `json:"provider_id"`
	Model               string    `json:"model"`
	ContextWindowTokens int       `json:"context_window_tokens"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type UpsertLLMModelInput struct {
	ProviderID          string `json:"provider_id"`
	Model               string `json:"model"`
	ContextWindowTokens int    `json:"context_window_tokens"`
}

type EnsureLLMProviderInput struct {
	ID           string `json:"id"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url,omitempty"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`
	Enabled      bool   `json:"enabled"`
}

type UpsertLLMProviderInput struct {
	ID           string `json:"id"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url,omitempty"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`
	Enabled      bool   `json:"enabled"`
}

type Environment struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Name        string          `json:"name"`
	Config      json.RawMessage `json:"config"`
	ArchivedAt  *time.Time      `json:"archived_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Session struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	AgentID            string          `json:"agent_id"`
	AgentConfigVersion int             `json:"agent_config_version"`
	EnvironmentID      string          `json:"environment_id"`
	Status             string          `json:"status"`
	Title              string          `json:"title,omitempty"`
	SandboxID          string          `json:"sandbox_id,omitempty"`
	RuntimeSettings    json.RawMessage `json:"runtime_settings,omitempty"`
	CreatedBy          string          `json:"created_by"`
	CreatedAt          time.Time       `json:"created_at"`
	ArchivedAt         *time.Time      `json:"archived_at,omitempty"`
}

type Event struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type SessionIntervention struct {
	SessionID         string          `json:"session_id"`
	TurnID            string          `json:"turn_id"`
	CallID            string          `json:"call_id"`
	ToolIdentifier    string          `json:"tool_identifier"`
	APIName           string          `json:"api_name"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	InterventionMode  string          `json:"intervention_mode"`
	Reason            string          `json:"reason,omitempty"`
	Status            string          `json:"status"`
	DecisionReason    string          `json:"decision_reason,omitempty"`
	RequestedAt       time.Time       `json:"requested_at"`
	DecidedAt         *time.Time      `json:"decided_at,omitempty"`
	Continuation      json.RawMessage `json:"-"`
	ContinuationRound int             `json:"-"`
}

type SaveSessionInterventionInput struct {
	TurnID            string          `json:"turn_id"`
	CallID            string          `json:"call_id"`
	ToolIdentifier    string          `json:"tool_identifier"`
	APIName           string          `json:"api_name"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	InterventionMode  string          `json:"intervention_mode"`
	Reason            string          `json:"reason,omitempty"`
	Continuation      json.RawMessage `json:"-"`
	ContinuationRound int             `json:"-"`
}

type DecideSessionInterventionInput struct {
	TurnID         string `json:"turn_id"`
	CallID         string `json:"call_id"`
	Status         string `json:"status"`
	DecisionReason string `json:"decision_reason,omitempty"`
}

type DecideSessionInterventionResult struct {
	Intervention SessionIntervention `json:"intervention"`
	Events       []Event             `json:"events"`
}

type ConversationMessage struct {
	Seq     int64           `json:"seq"`
	Role    string          `json:"role"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type SessionSummary struct {
	SessionID      string    `json:"session_id"`
	SummaryText    string    `json:"summary_text"`
	SourceUntilSeq int64     `json:"source_until_seq"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type UpsertSessionSummaryInput struct {
	SummaryText    string `json:"summary_text"`
	SourceUntilSeq int64  `json:"source_until_seq"`
}

type UpsertSessionSummaryResult struct {
	Summary SessionSummary `json:"summary"`
	Events  []Event        `json:"events"`
}

type LLMUsageRecord struct {
	ID                 string    `json:"id"`
	WorkspaceID        string    `json:"workspace_id"`
	AgentID            string    `json:"agent_id"`
	AgentConfigVersion int       `json:"agent_config_version"`
	SessionID          string    `json:"session_id"`
	TurnID             string    `json:"turn_id"`
	ProviderID         string    `json:"provider_id"`
	ProviderType       string    `json:"provider_type,omitempty"`
	Model              string    `json:"model"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	TotalTokens        int64     `json:"total_tokens"`
	CachedInputTokens  int64     `json:"cached_input_tokens"`
	ReasoningTokens    int64     `json:"reasoning_tokens"`
	LatencyMillis      int64     `json:"latency_ms"`
	Status             string    `json:"status"`
	ErrorMessage       string    `json:"error_message,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type LLMUsageSummary struct {
	RecordCount       int64 `json:"record_count"`
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
	LatencyMillis     int64 `json:"latency_ms"`
}

type LLMUsageReport struct {
	SessionID string           `json:"session_id"`
	Summary   LLMUsageSummary  `json:"summary"`
	Records   []LLMUsageRecord `json:"records"`
}

const (
	LLMUsageGroupByProvider      = "provider"
	LLMUsageGroupByModel         = "model"
	LLMUsageGroupByProviderModel = "provider_model"
)

type ListLLMUsageInput struct {
	WorkspaceID string     `json:"workspace_id,omitempty"`
	ProviderID  string     `json:"provider_id,omitempty"`
	Model       string     `json:"model,omitempty"`
	Status      string     `json:"status,omitempty"`
	GroupBy     string     `json:"group_by,omitempty"`
	From        *time.Time `json:"from,omitempty"`
	To          *time.Time `json:"to,omitempty"`
}

type LLMUsageAggregate struct {
	ProviderID string          `json:"provider_id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Summary    LLMUsageSummary `json:"summary"`
}

type LLMUsageAggregateReport struct {
	GroupBy string              `json:"group_by"`
	Filters ListLLMUsageInput   `json:"filters"`
	Summary LLMUsageSummary     `json:"summary"`
	Groups  []LLMUsageAggregate `json:"groups"`
}

type ObjectRef struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	StorageProvider string          `json:"storage_provider"`
	Bucket          string          `json:"bucket"`
	ObjectKey       string          `json:"object_key"`
	ObjectVersion   string          `json:"object_version,omitempty"`
	ContentType     string          `json:"content_type,omitempty"`
	SizeBytes       int64           `json:"size_bytes"`
	ChecksumSHA256  string          `json:"checksum_sha256,omitempty"`
	ETag            string          `json:"etag,omitempty"`
	Visibility      string          `json:"visibility"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
}

type CreateObjectRefInput struct {
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	StorageProvider string          `json:"storage_provider,omitempty"`
	Bucket          string          `json:"bucket"`
	ObjectKey       string          `json:"object_key"`
	ObjectVersion   string          `json:"object_version,omitempty"`
	ContentType     string          `json:"content_type,omitempty"`
	SizeBytes       int64           `json:"size_bytes"`
	ChecksumSHA256  string          `json:"checksum_sha256,omitempty"`
	ETag            string          `json:"etag,omitempty"`
	Visibility      string          `json:"visibility,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CreatedBy       string          `json:"created_by,omitempty"`
}

type SessionArtifact struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	SessionID     string          `json:"session_id"`
	EnvironmentID string          `json:"environment_id,omitempty"`
	ObjectRefID   string          `json:"object_ref_id"`
	TurnID        string          `json:"turn_id,omitempty"`
	ToolCallID    string          `json:"tool_call_id,omitempty"`
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	ArtifactType  string          `json:"artifact_type"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
}

type Worker struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	Name           string          `json:"name"`
	WorkerType     string          `json:"worker_type"`
	Status         string          `json:"status"`
	Capabilities   json.RawMessage `json:"capabilities,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	RegisteredBy   string          `json:"registered_by"`
	RegisteredAt   time.Time       `json:"registered_at"`
	LastSeenAt     *time.Time      `json:"last_seen_at,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	ArchivedAt     *time.Time      `json:"archived_at,omitempty"`
}

type RegisterWorkerInput struct {
	WorkspaceID  string          `json:"workspace_id,omitempty"`
	Name         string          `json:"name"`
	WorkerType   string          `json:"worker_type,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	RegisteredBy string          `json:"registered_by,omitempty"`
	LeaseSeconds int             `json:"lease_seconds,omitempty"`
}

type WorkerHeartbeatInput struct {
	Status       string          `json:"status,omitempty"`
	Capabilities json.RawMessage `json:"capabilities,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	LeaseSeconds int             `json:"lease_seconds,omitempty"`
}

type ListWorkersInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Status      string `json:"status,omitempty"`
}

type WorkerWork struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	WorkerID       string          `json:"worker_id,omitempty"`
	EnvironmentID  string          `json:"environment_id,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	WorkType       string          `json:"work_type"`
	Status         string          `json:"status"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
}

type EnqueueWorkerWorkInput struct {
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	WorkerID      string          `json:"worker_id,omitempty"`
	EnvironmentID string          `json:"environment_id,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	TurnID        string          `json:"turn_id,omitempty"`
	WorkType      string          `json:"work_type,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

type PollWorkerWorkInput struct {
	LeaseSeconds int `json:"lease_seconds,omitempty"`
}

type WorkerWorkHeartbeatInput struct {
	LeaseSeconds int `json:"lease_seconds,omitempty"`
}

type ReapExpiredWorkerWorkInput struct {
	Limit int `json:"limit,omitempty"`
}

type CompleteWorkerWorkInput struct {
	Success      bool            `json:"success"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
}

type CreateSessionArtifactInput struct {
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	SessionID     string          `json:"session_id"`
	EnvironmentID string          `json:"environment_id,omitempty"`
	ObjectRefID   string          `json:"object_ref_id"`
	TurnID        string          `json:"turn_id,omitempty"`
	ToolCallID    string          `json:"tool_call_id,omitempty"`
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	ArtifactType  string          `json:"artifact_type,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedBy     string          `json:"created_by,omitempty"`
}

type RecordLLMUsageInput struct {
	WorkspaceID        string `json:"workspace_id"`
	AgentID            string `json:"agent_id"`
	AgentConfigVersion int    `json:"agent_config_version"`
	SessionID          string `json:"session_id"`
	TurnID             string `json:"turn_id"`
	ProviderID         string `json:"provider_id"`
	ProviderType       string `json:"provider_type,omitempty"`
	Model              string `json:"model"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
	CachedInputTokens  int64  `json:"cached_input_tokens"`
	ReasoningTokens    int64  `json:"reasoning_tokens"`
	LatencyMillis      int64  `json:"latency_ms"`
	Status             string `json:"status"`
	ErrorMessage       string `json:"error_message,omitempty"`
}

type CreateAgentInput struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	LLMProvider string          `json:"llm_provider,omitempty"`
	LLMModel    string          `json:"llm_model,omitempty"`
	Model       string          `json:"model,omitempty"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
}

type CreateAgentConfigVersionInput struct {
	AgentID     string          `json:"agent_id,omitempty"`
	LLMProvider string          `json:"llm_provider"`
	LLMModel    string          `json:"llm_model"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
}

type UpdateSessionRuntimeSettingsInput struct {
	RuntimeSettings json.RawMessage `json:"runtime_settings"`
}

type UpgradeSessionAgentConfigInput struct {
	ToCurrent bool   `json:"to_current,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

type UpgradeSessionAgentConfigResult struct {
	Session                  Session `json:"session"`
	Event                    Event   `json:"event,omitempty"`
	OldAgentConfigVersion    int     `json:"old_agent_config_version"`
	NewAgentConfigVersion    int     `json:"new_agent_config_version"`
	LatestAgentConfigVersion int     `json:"latest_agent_config_version"`
	Changed                  bool    `json:"changed"`
}

type AgentRuntimeConfig struct {
	SessionID             string          `json:"session_id"`
	WorkspaceID           string          `json:"workspace_id"`
	AgentID               string          `json:"agent_id"`
	AgentConfigVersion    int             `json:"agent_config_version"`
	EnvironmentID         string          `json:"environment_id"`
	LLMProvider           string          `json:"llm_provider"`
	LLMProviderType       string          `json:"llm_provider_type,omitempty"`
	LLMModel              string          `json:"llm_model"`
	LLMBaseURL            string          `json:"llm_base_url,omitempty"`
	LLMAPIKeyEnv          string          `json:"llm_api_key_env,omitempty"`
	ContextWindowTokens   int             `json:"context_window_tokens"`
	SummaryText           string          `json:"summary_text,omitempty"`
	SummarySourceUntilSeq int64           `json:"summary_source_until_seq,omitempty"`
	System                string          `json:"system"`
	RuntimeSettings       json.RawMessage `json:"runtime_settings,omitempty"`
	Tools                 json.RawMessage `json:"tools,omitempty"`
	Skills                json.RawMessage `json:"skills,omitempty"`
}

type CreateEnvironmentInput struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	Config      json.RawMessage `json:"config"`
}

type CreateSessionInput struct {
	WorkspaceID   string `json:"workspace_id,omitempty"`
	AgentID       string `json:"agent_id,omitempty"`
	Agent         string `json:"agent,omitempty"`
	EnvironmentID string `json:"environment_id"`
	Title         string `json:"title,omitempty"`
	CreatedBy     string `json:"created_by,omitempty"`
}

type AppendEventInput struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
