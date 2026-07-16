package tma

import (
	"encoding/json"
	"time"
)

const (
	RunStatusRunning         = "running"
	RunStatusWaitingApproval = "waiting_approval"
	RunStatusCompleted       = "completed"
	RunStatusFailed          = "failed"
	RunStatusInterrupted     = "interrupted"
)

type Session struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	OwnerID            string          `json:"owner_id"`
	AgentID            string          `json:"agent_id"`
	AgentConfigVersion int             `json:"agent_config_version"`
	EnvironmentID      string          `json:"environment_id"`
	ParentSessionID    string          `json:"parent_session_id,omitempty"`
	ParentTurnID       string          `json:"parent_turn_id,omitempty"`
	SpawnDepth         int             `json:"spawn_depth,omitempty"`
	Status             string          `json:"status"`
	Title              string          `json:"title,omitempty"`
	SandboxID          string          `json:"sandbox_id,omitempty"`
	RuntimeSettings    json.RawMessage `json:"runtime_settings,omitempty"`
	PinnedAt           *time.Time      `json:"pinned_at"`
	Tags               []string        `json:"tags"`
	SummaryText        string          `json:"summary_text,omitempty"`
	CreatedBy          string          `json:"created_by"`
	CreatedAt          time.Time       `json:"created_at"`
	ArchivedAt         *time.Time      `json:"archived_at,omitempty"`
}

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
	MCP         json.RawMessage `json:"mcp,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type CreateAgentRequest struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	LLMProvider string          `json:"llm_provider,omitempty"`
	LLMModel    string          `json:"llm_model,omitempty"`
	Model       string          `json:"model,omitempty"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	MCP         json.RawMessage `json:"mcp,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
}

// UpdateAgentRequest uses pointers so an omitted field is distinct from an
// explicitly empty value.
type UpdateAgentRequest struct {
	Name        *string          `json:"name,omitempty"`
	LLMProvider *string          `json:"llm_provider,omitempty"`
	LLMModel    *string          `json:"llm_model,omitempty"`
	Model       *string          `json:"model,omitempty"`
	System      *string          `json:"system,omitempty"`
	Tools       *json.RawMessage `json:"tools,omitempty"`
	MCP         *json.RawMessage `json:"mcp,omitempty"`
	Skills      *json.RawMessage `json:"skills,omitempty"`
}

type CreateAgentConfigVersionRequest struct {
	LLMProvider *string          `json:"llm_provider,omitempty"`
	LLMModel    *string          `json:"llm_model,omitempty"`
	Model       *string          `json:"model,omitempty"`
	System      *string          `json:"system,omitempty"`
	Tools       *json.RawMessage `json:"tools,omitempty"`
	MCP         *json.RawMessage `json:"mcp,omitempty"`
	Skills      *json.RawMessage `json:"skills,omitempty"`
}

type Environment struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Name        string          `json:"name"`
	Config      json.RawMessage `json:"config"`
	ArchivedAt  *time.Time      `json:"archived_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type CreateEnvironmentRequest struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	Config      json.RawMessage `json:"config"`
}

type LLMProvider struct {
	ID           string    `json:"id"`
	ProviderType string    `json:"provider_type"`
	BaseURL      string    `json:"base_url,omitempty"`
	APIKeyEnv    string    `json:"api_key_env,omitempty"`
	Enabled      bool      `json:"enabled"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CreateLLMProviderRequest struct {
	ID           string `json:"id"`
	ProviderType string `json:"provider_type"`
	BaseURL      string `json:"base_url,omitempty"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`
	Enabled      *bool  `json:"enabled,omitempty"`
}

type UpdateLLMProviderRequest struct {
	ProviderType *string `json:"provider_type,omitempty"`
	BaseURL      *string `json:"base_url,omitempty"`
	APIKeyEnv    *string `json:"api_key_env,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
}

type LLMModel struct {
	ProviderID          string               `json:"provider_id"`
	Model               string               `json:"model"`
	ContextWindowTokens int                  `json:"context_window_tokens"`
	CapabilityType      string               `json:"capability_type"`
	Capabilities        LLMModelCapabilities `json:"capabilities"`
	IsDefaultVision     bool                 `json:"is_default_vision"`
	IsDefaultEmbedding  bool                 `json:"is_default_embedding"`
	IsDefaultReranker   bool                 `json:"is_default_reranker"`
	Revision            int64                `json:"revision"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

type LLMModelCapabilities struct {
	Dimensions     int    `json:"dimensions,omitempty"`
	DistanceMetric string `json:"distance_metric,omitempty"`
	Normalized     bool   `json:"normalized"`
	MaxBatchSize   int    `json:"max_batch_size,omitempty"`
	MaxCandidates  int    `json:"max_candidates,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
}

type PutLLMModelRequest struct {
	ProviderID          string                `json:"provider_id"`
	Model               string                `json:"model"`
	ContextWindowTokens int                   `json:"context_window_tokens,omitempty"`
	CapabilityType      string                `json:"capability_type,omitempty"`
	Capabilities        *LLMModelCapabilities `json:"capabilities,omitempty"`
	IsDefaultVision     *bool                 `json:"is_default_vision,omitempty"`
	IsDefaultEmbedding  *bool                 `json:"is_default_embedding,omitempty"`
	IsDefaultReranker   *bool                 `json:"is_default_reranker,omitempty"`
}

type LLMDiagnosticResult struct {
	Status         string    `json:"status"`
	CapabilityType string    `json:"capability_type,omitempty"`
	Protocol       string    `json:"protocol,omitempty"`
	LatencyMS      int64     `json:"latency_ms"`
	Dimensions     int       `json:"dimensions,omitempty"`
	CandidateCount int       `json:"candidate_count,omitempty"`
	Authenticated  bool      `json:"authenticated"`
	ErrorType      string    `json:"error_type,omitempty"`
	Message        string    `json:"message"`
	Retryable      bool      `json:"retryable"`
	CheckedAt      time.Time `json:"checked_at"`
}

type LLMUsageQuery struct {
	WorkspaceID string
	ProviderID  string
	Model       string
	Status      string
	GroupBy     string
	From        *time.Time
	To          *time.Time
}

type LLMUsageAggregate struct {
	ProviderID string          `json:"provider_id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Summary    LLMUsageSummary `json:"summary"`
}

type LLMUsageAggregateReport struct {
	GroupBy string              `json:"group_by"`
	Filters LLMUsageQueryResult `json:"filters"`
	Summary LLMUsageSummary     `json:"summary"`
	Groups  []LLMUsageAggregate `json:"groups"`
}

type LLMUsageQueryResult struct {
	WorkspaceID string     `json:"workspace_id,omitempty"`
	ProviderID  string     `json:"provider_id,omitempty"`
	Model       string     `json:"model,omitempty"`
	Status      string     `json:"status,omitempty"`
	GroupBy     string     `json:"group_by,omitempty"`
	From        *time.Time `json:"from,omitempty"`
	To          *time.Time `json:"to,omitempty"`
}

type CreateSessionRequest struct {
	WorkspaceID     string `json:"workspace_id,omitempty"`
	OwnerID         string `json:"owner_id,omitempty"`
	AgentID         string `json:"agent_id,omitempty"`
	EnvironmentID   string `json:"environment_id,omitempty"`
	Title           string `json:"title,omitempty"`
	CreatedBy       string `json:"created_by,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	ParentTurnID    string `json:"parent_turn_id,omitempty"`
}

type UpdateSessionRuntimeSettingsRequest struct {
	LLMProvider       *string                          `json:"llm_provider,omitempty"`
	LLMModel          *string                          `json:"llm_model,omitempty"`
	InterventionMode  *string                          `json:"intervention_mode,omitempty"`
	ToolRuntime       *string                          `json:"tool_runtime,omitempty"`
	CloudSandboxRoot  *string                          `json:"cloud_sandbox_root,omitempty"`
	CloudSandboxImage *string                          `json:"cloud_sandbox_image,omitempty"`
	AllowNetwork      *bool                            `json:"cloud_sandbox_allow_network,omitempty"`
	HumanInteraction  *HumanInteractionRuntimeSettings `json:"human_interaction,omitempty"`
	CompletionGate    *CompletionGateRuntimeSettings   `json:"completion_gate,omitempty"`
}

type HumanInteractionRuntimeSettings struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Modes          []string `json:"modes,omitempty"`
	SupportsUpload *bool    `json:"supports_upload,omitempty"`
	Fallback       *string  `json:"fallback,omitempty"`
}

type CompletionGateRuntimeSettings struct {
	MaxRetries *int `json:"max_retries,omitempty"`
}

type UpgradeSessionConfigRequest struct {
	ToCurrent *bool  `json:"to_current,omitempty"`
	ToVersion int    `json:"to_version,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

type UpgradeSessionConfigResult struct {
	Session                  Session `json:"session"`
	Event                    Event   `json:"event,omitempty"`
	OldAgentConfigVersion    int     `json:"old_agent_config_version"`
	NewAgentConfigVersion    int     `json:"new_agent_config_version"`
	LatestAgentConfigVersion int     `json:"latest_agent_config_version"`
	Changed                  bool    `json:"changed"`
}

type Event struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id,omitempty"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type AppendEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type AppendEventsRequest struct {
	Events       []AppendEvent `json:"events"`
	PreferLatest bool          `json:"prefer_latest,omitempty"`
}

type AppendEventsResult struct {
	Events       []Event         `json:"events,omitempty"`
	Queued       bool            `json:"queued,omitempty"`
	QueueRequest json.RawMessage `json:"queue_request,omitempty"`
}

type Run struct {
	ID                   string     `json:"id"`
	SessionID            string     `json:"session_id"`
	Status               string     `json:"status"`
	UserEventID          string     `json:"user_event_id,omitempty"`
	UserEventSeq         int64      `json:"user_event_seq,omitempty"`
	Attempt              int32      `json:"attempt"`
	StartedAt            time.Time  `json:"started_at"`
	EndedAt              *time.Time `json:"ended_at,omitempty"`
	InterruptRequestedAt *time.Time `json:"interrupt_requested_at,omitempty"`
	ErrorMessage         string     `json:"error_message,omitempty"`
	IdempotencyKey       string     `json:"idempotency_key,omitempty"`
}

type StartRunRequest struct {
	Input          json.RawMessage `json:"input"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
}

type StartRunResponse struct {
	Run     Run     `json:"run"`
	Events  []Event `json:"events,omitempty"`
	Created bool    `json:"created"`
}

type RunResult struct {
	Run       Run             `json:"run"`
	LastEvent *Event          `json:"last_event,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

type Intervention struct {
	SessionID        string          `json:"session_id"`
	TurnID           string          `json:"turn_id"`
	CallID           string          `json:"call_id"`
	ToolIdentifier   string          `json:"tool_identifier"`
	APIName          string          `json:"api_name"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Kind             string          `json:"kind"`
	Request          json.RawMessage `json:"request,omitempty"`
	Response         json.RawMessage `json:"response,omitempty"`
	InterventionMode string          `json:"intervention_mode"`
	Reason           string          `json:"reason,omitempty"`
	Status           string          `json:"status"`
	DecisionReason   string          `json:"decision_reason,omitempty"`
	RequestedAt      time.Time       `json:"requested_at"`
	DecidedAt        *time.Time      `json:"decided_at,omitempty"`
	RespondedAt      *time.Time      `json:"responded_at,omitempty"`
	ExpiresAt        *time.Time      `json:"expires_at,omitempty"`
}

type InterventionDecision struct {
	Intervention Intervention `json:"intervention"`
	Events       []Event      `json:"events"`
}

type SessionSummary struct {
	SessionID      string    `json:"session_id"`
	SummaryText    string    `json:"summary_text"`
	SourceUntilSeq int64     `json:"source_until_seq"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SessionTaskItem struct {
	ID          string     `json:"id"`
	PlanID      string     `json:"plan_id"`
	Index       int        `json:"index"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Evidence    string     `json:"evidence,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type SessionTaskPlan struct {
	ID            string            `json:"id"`
	WorkspaceID   string            `json:"workspace_id"`
	OwnerID       string            `json:"owner_id"`
	SessionID     string            `json:"session_id"`
	CreatedTurnID string            `json:"created_turn_id,omitempty"`
	UpdatedTurnID string            `json:"updated_turn_id,omitempty"`
	Title         string            `json:"title,omitempty"`
	Goal          string            `json:"goal"`
	HandlingMode  string            `json:"handling_mode"`
	Status        string            `json:"status"`
	Items         []SessionTaskItem `json:"items"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
	CompletedAt   *time.Time        `json:"completed_at,omitempty"`
}

type UpsertSessionSummaryRequest struct {
	SummaryText    string `json:"summary_text"`
	SourceUntilSeq int64  `json:"source_until_seq"`
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
	RecordCount       int32 `json:"record_count"`
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	TotalTokens       int64 `json:"total_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	ReasoningTokens   int64 `json:"reasoning_tokens"`
	LatencyMillis     int64 `json:"latency_ms"`
}

type SessionUsage struct {
	SessionID string           `json:"session_id"`
	Summary   LLMUsageSummary  `json:"summary"`
	Records   []LLMUsageRecord `json:"records"`
}

type Artifact struct {
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
	ContentType   string          `json:"content_type,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedBy     string          `json:"created_by"`
	CreatedAt     time.Time       `json:"created_at"`
}

type CreateArtifactRequest struct {
	EnvironmentID string          `json:"environment_id,omitempty"`
	ObjectRefID   string          `json:"object_ref_id"`
	TurnID        string          `json:"turn_id,omitempty"`
	ToolCallID    string          `json:"tool_call_id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Description   string          `json:"description,omitempty"`
	ArtifactType  string          `json:"artifact_type,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedBy     string          `json:"created_by,omitempty"`
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

type CreateObjectRefRequest struct {
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

type ArtifactUpload struct {
	ObjectRef     ObjectRef `json:"object_ref"`
	Artifact      Artifact  `json:"artifact"`
	WorkspacePath string    `json:"workspace_path,omitempty"`
}

func TextInput(text string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
	})
	return encoded
}

func (e Event) EffectiveTurnID() string {
	if e.TurnID != "" {
		return e.TurnID
	}
	var payload struct {
		TurnID string `json:"turn_id"`
	}
	_ = json.Unmarshal(e.Payload, &payload)
	return payload.TurnID
}
