package managedagents

import (
	"encoding/json"
	"time"
)

const (
	DefaultWorkspaceID                = "wksp_default"
	DefaultContextWindowTokens        = 128000
	ContextBudgetRatioPercent         = 60
	LLMModelCapabilityText            = "text"
	LLMModelCapabilityTextImage       = "text_image"
	LLMModelCapabilityImageGeneration = "image_generation"
	LLMModelCapabilityVideoGeneration = "video_generation"
	LLMModelCapabilityEmbedding       = "embedding"
	LLMModelCapabilityReranker        = "reranker"

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

	EventRuntimeStarted                   = "runtime.started"
	EventRuntimeThinking                  = "runtime.thinking"
	EventRuntimeLLMRequest                = "runtime.llm_request"
	EventRuntimeLLMChunk                  = "runtime.llm_chunk"
	EventRuntimeLLMDelta                  = "runtime.llm_delta"
	EventRuntimeLLMResponse               = "runtime.llm_response"
	EventRuntimeProgressMessage           = "runtime.progress_message"
	EventRuntimeToolCall                  = "runtime.tool_call"
	EventRuntimeToolInterventionRequired  = "runtime.tool_intervention_required"
	EventRuntimeToolInterventionApproved  = "runtime.tool_intervention_approved"
	EventRuntimeToolInterventionRejected  = "runtime.tool_intervention_rejected"
	EventRuntimeHumanInputRequired        = "runtime.human_input_required"
	EventRuntimeHumanInputSubmitted       = "runtime.human_input_submitted"
	EventRuntimeHumanInputSkipped         = "runtime.human_input_skipped"
	EventRuntimeHumanInputCanceled        = "runtime.human_input_canceled"
	EventRuntimePlanApprovalRequired      = "runtime.plan_approval_required"
	EventRuntimePlanApprovalApproved      = "runtime.plan_approval_approved"
	EventRuntimePlanApprovalRejected      = "runtime.plan_approval_rejected"
	EventRuntimeTaskPlanCreated           = "runtime.task_plan_created"
	EventRuntimeTaskItemsUpdated          = "runtime.task_items_updated"
	EventRuntimeTaskPlanCompleted         = "runtime.task_plan_completed"
	EventRuntimeTaskPlanCanceled          = "runtime.task_plan_canceled"
	EventRuntimeTaskPlanSuperseded        = "runtime.task_plan_superseded"
	EventRuntimeToolResult                = "runtime.tool_result"
	EventRuntimeSubagentSpawnRejected     = "runtime.subagent_spawn_rejected"
	EventRuntimeSubagentStartRejected     = "runtime.subagent_start_rejected"
	EventRuntimeSubagentStartQueued       = "runtime.subagent_start_queued"
	EventRuntimeSubagentStartDequeued     = "runtime.subagent_start_dequeued"
	EventRuntimeSubagentStartCanceled     = "runtime.subagent_start_canceled"
	EventRuntimeSubagentStartExpired      = "runtime.subagent_start_expired"
	EventRuntimeSubagentGroupCreated      = "runtime.subagent_group_created"
	EventRuntimeSubagentGroupItemStarted  = "runtime.subagent_group_item_started"
	EventRuntimeSubagentGroupItemQueued   = "runtime.subagent_group_item_queued"
	EventRuntimeSubagentGroupItemRejected = "runtime.subagent_group_item_rejected"
	EventRuntimeSubagentGroupCompleted    = "runtime.subagent_group_completed"
	EventRuntimeSubagentGroupFailed       = "runtime.subagent_group_failed"
	EventRuntimeSubagentGroupCanceled     = "runtime.subagent_group_canceled"
	EventRuntimeSpanStarted               = "runtime.span_started"
	EventRuntimeSpanEvent                 = "runtime.span_event"
	EventRuntimeSpanEnded                 = "runtime.span_ended"
	EventRuntimeContextCompacting         = "runtime.context_compacting"
	EventRuntimeContextCompacted          = "runtime.context_compacted"
	EventRuntimeContextCompactionFailed   = "runtime.context_compaction_failed"
	EventRuntimeSkillsResolving           = "runtime.skills_resolving"
	EventRuntimeSkillsResolved            = "runtime.skills_resolved"
	EventRuntimeSkillsTruncated           = "runtime.skills_truncated"
	EventRuntimeSkillsFailed              = "runtime.skills_failed"
	EventRuntimeTurnCompleting            = "runtime.turn_completing"
	EventRuntimeCompletionValidated       = "runtime.completion_validated"
	EventRuntimeCompletionBlocked         = "runtime.completion_blocked"
	EventRuntimeCompletionFailed          = "runtime.completion_validation_failed"
	EventRuntimeCompleted                 = "runtime.completed"
	EventRuntimeFailed                    = "runtime.failed"
	EventSessionConfigUpdated             = "session.config_updated"

	InterventionStatusPending  = "pending"
	InterventionStatusApproved = "approved"
	InterventionStatusRejected = "rejected"
	InterventionStatusAnswered = "answered"
	InterventionStatusSkipped  = "skipped"
	InterventionStatusCanceled = "canceled"
	InterventionStatusExpired  = "expired"

	InterventionKindToolApproval  = "tool_approval"
	InterventionKindClarification = "clarification"
	InterventionKindPlanApproval  = "plan_approval"
	InterventionKindUploadRequest = "upload_request"

	TaskPlanModeTracked = "tracked"
	TaskPlanModePlanned = "planned"

	TaskPlanStatusActive     = "active"
	TaskPlanStatusCompleted  = "completed"
	TaskPlanStatusCanceled   = "canceled"
	TaskPlanStatusSuperseded = "superseded"

	TaskItemStatusPending      = "pending"
	TaskItemStatusInProgress   = "in_progress"
	TaskItemStatusCompleted    = "completed"
	TaskItemStatusBlocked      = "blocked"
	TaskEvidenceKindToolResult = "tool_result"

	TurnStatusRunning         = "running"
	TurnStatusWaitingApproval = "waiting_approval"
	TurnStatusWaitingHuman    = "waiting_human"
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

	SubagentTaskGroupStrategyAllCompleted = "all_completed"
	SubagentTaskGroupStrategyAnyCompleted = "any_completed"
	SubagentTaskGroupStrategyQuorum       = "quorum"
	SubagentTaskGroupReducerNone          = "none"
	SubagentTaskGroupReducerConcatText    = "concat_text"
	SubagentTaskGroupReducerJSONList      = "json_array"
	SubagentTaskGroupReducerJSONObject    = "json_object_by_item"
	SubagentTaskGroupReducerFirstSuccess  = "first_success"
	SubagentTaskGroupReducerMajorityText  = "majority_text"
	SubagentTaskGroupReducerJSONValues    = "json_values"
	SubagentTaskGroupReducerMergeObjects  = "merge_objects"
	SubagentTaskGroupReducerFirstValue    = "first_success_value"
	SubagentTaskGroupReducerMajorityValue = "majority_value"

	SubagentTaskGroupItemStateCreated  = "created"
	SubagentTaskGroupItemStateStarted  = "started"
	SubagentTaskGroupItemStateQueued   = "queued"
	SubagentTaskGroupItemStateRejected = "rejected"
)

// AccessScope is the tenant boundary applied by user-facing data access.
// An empty OwnerID grants workspace-wide access; WorkspaceID is always required.
type AccessScope struct {
	WorkspaceID string `json:"workspace_id"`
	OwnerID     string `json:"owner_id,omitempty"`
}

type Agent struct {
	ID                   string             `json:"id"`
	WorkspaceID          string             `json:"workspace_id"`
	OwnerType            string             `json:"owner_type"`
	OwnerID              string             `json:"owner_id"`
	Visibility           string             `json:"visibility"`
	AgentKind            string             `json:"agent_kind"`
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

type UpsertLLMModelInput struct {
	ProviderID          string                `json:"provider_id"`
	Model               string                `json:"model"`
	ContextWindowTokens int                   `json:"context_window_tokens"`
	CapabilityType      string                `json:"capability_type,omitempty"`
	Capabilities        *LLMModelCapabilities `json:"capabilities,omitempty"`
	IsDefaultVision     *bool                 `json:"is_default_vision,omitempty"`
	IsDefaultEmbedding  *bool                 `json:"is_default_embedding,omitempty"`
	IsDefaultReranker   *bool                 `json:"is_default_reranker,omitempty"`
}

type UpdateLLMModelInput struct {
	UpsertLLMModelInput
	ExpectedRevision int64 `json:"expected_revision"`
}

func NormalizeLLMModelCapability(value string) (string, bool) {
	switch value {
	case "", LLMModelCapabilityText:
		return LLMModelCapabilityText, true
	case LLMModelCapabilityTextImage, LLMModelCapabilityImageGeneration, LLMModelCapabilityVideoGeneration,
		LLMModelCapabilityEmbedding, LLMModelCapabilityReranker:
		return value, true
	default:
		return "", false
	}
}

func LLMModelSupportsVision(value string) bool {
	return value == LLMModelCapabilityTextImage
}

func LLMModelSupportsAgentRuntime(value string) bool {
	return value == LLMModelCapabilityText || value == LLMModelCapabilityTextImage
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

type UpdateLLMProviderInput struct {
	UpsertLLMProviderInput
	ExpectedRevision int64 `json:"expected_revision"`
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

type Event struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id,omitempty"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type SessionRun struct {
	ID                   string     `json:"id"`
	SessionID            string     `json:"session_id"`
	AgentID              string     `json:"agent_id"`
	AgentConfigVersion   int        `json:"agent_config_version"`
	Status               string     `json:"status"`
	UserEventID          string     `json:"user_event_id,omitempty"`
	UserEventSeq         int64      `json:"user_event_seq,omitempty"`
	Attempt              int        `json:"attempt"`
	StartedAt            time.Time  `json:"started_at"`
	EndedAt              *time.Time `json:"ended_at,omitempty"`
	InterruptRequestedAt *time.Time `json:"interrupt_requested_at,omitempty"`
	ErrorMessage         string     `json:"error_message,omitempty"`
	IdempotencyKey       string     `json:"idempotency_key,omitempty"`
	RequestHash          string     `json:"-"`
}

type StartSessionRunInput struct {
	Payload        json.RawMessage `json:"input"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	RequestHash    string          `json:"-"`
}

type StartSessionRunResult struct {
	Run     SessionRun `json:"run"`
	Events  []Event    `json:"events,omitempty"`
	Created bool       `json:"created"`
}

type SessionTurnWork struct {
	SessionID          string               `json:"session_id"`
	TurnID             string               `json:"turn_id"`
	Scope              AccessScope          `json:"scope"`
	UserEventSeq       int64                `json:"user_event_seq"`
	UserPayload        json.RawMessage      `json:"user_payload"`
	ResumeIntervention *SessionIntervention `json:"resume_intervention,omitempty"`
	Attempt            int                  `json:"attempt"`
}

type ClaimSessionTurnsInput struct {
	LeaseOwner    string
	LeaseDuration time.Duration
	Limit         int
}

type RenewSessionTurnLeaseInput struct {
	SessionID     string
	TurnID        string
	Scope         AccessScope
	LeaseOwner    string
	LeaseDuration time.Duration
}

type ReleaseSessionTurnLeaseInput struct {
	SessionID  string
	TurnID     string
	Scope      AccessScope
	LeaseOwner string
}

type SessionIntervention struct {
	SessionID         string          `json:"session_id"`
	TurnID            string          `json:"turn_id"`
	CallID            string          `json:"call_id"`
	ToolIdentifier    string          `json:"tool_identifier"`
	APIName           string          `json:"api_name"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	Kind              string          `json:"kind"`
	Request           json.RawMessage `json:"request,omitempty"`
	Response          json.RawMessage `json:"response,omitempty"`
	InterventionMode  string          `json:"intervention_mode"`
	Reason            string          `json:"reason,omitempty"`
	Status            string          `json:"status"`
	DecisionReason    string          `json:"decision_reason,omitempty"`
	RequestedAt       time.Time       `json:"requested_at"`
	DecidedAt         *time.Time      `json:"decided_at,omitempty"`
	RespondedAt       *time.Time      `json:"responded_at,omitempty"`
	ExpiresAt         *time.Time      `json:"expires_at,omitempty"`
	Continuation      json.RawMessage `json:"-"`
	ContinuationRound int             `json:"-"`
}

type SaveSessionInterventionInput struct {
	TurnID            string          `json:"turn_id"`
	CallID            string          `json:"call_id"`
	ToolIdentifier    string          `json:"tool_identifier"`
	APIName           string          `json:"api_name"`
	Arguments         json.RawMessage `json:"arguments,omitempty"`
	Kind              string          `json:"kind,omitempty"`
	Request           json.RawMessage `json:"request,omitempty"`
	ExpiresAt         *time.Time      `json:"expires_at,omitempty"`
	InterventionMode  string          `json:"intervention_mode"`
	Reason            string          `json:"reason,omitempty"`
	Continuation      json.RawMessage `json:"-"`
	ContinuationRound int             `json:"-"`
}

type DecideSessionInterventionInput struct {
	TurnID         string          `json:"turn_id"`
	CallID         string          `json:"call_id"`
	Status         string          `json:"status"`
	DecisionReason string          `json:"decision_reason,omitempty"`
	Response       json.RawMessage `json:"response,omitempty"`
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

type SessionTaskItem struct {
	ID           string            `json:"id"`
	PlanID       string            `json:"plan_id"`
	Index        int               `json:"index"`
	Description  string            `json:"description"`
	Status       string            `json:"status"`
	Evidence     string            `json:"evidence,omitempty"`
	EvidenceRefs []TaskEvidenceRef `json:"evidence_refs"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
}

type TaskEvidenceRef struct {
	Kind        string   `json:"kind"`
	TurnID      string   `json:"turn_id"`
	ToolCallID  string   `json:"tool_call_id"`
	Tool        string   `json:"tool"`
	ArtifactIDs []string `json:"artifact_ids,omitempty"`
}

type TaskEvidenceRefInput struct {
	ToolCallID string `json:"tool_call_id"`
}

type CreateSessionTaskPlanInput struct {
	TurnID       string   `json:"turn_id,omitempty"`
	Title        string   `json:"title,omitempty"`
	Goal         string   `json:"goal"`
	HandlingMode string   `json:"handling_mode,omitempty"`
	Items        []string `json:"items"`
}

type UpdateSessionTaskItemInput struct {
	ItemID       string                 `json:"item_id"`
	Status       string                 `json:"status"`
	Evidence     string                 `json:"evidence,omitempty"`
	EvidenceRefs []TaskEvidenceRefInput `json:"evidence_refs,omitempty"`
}

type UpdateSessionTaskItemsInput struct {
	TurnID string                       `json:"turn_id,omitempty"`
	PlanID string                       `json:"plan_id,omitempty"`
	Items  []UpdateSessionTaskItemInput `json:"items"`
}

type FinishSessionTaskPlanInput struct {
	TurnID string `json:"turn_id,omitempty"`
	PlanID string `json:"plan_id,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type SessionTaskPlanResult struct {
	Plan   SessionTaskPlan `json:"plan"`
	Events []Event         `json:"events,omitempty"`
}

type UpsertSessionSummaryInput struct {
	SummaryText    string `json:"summary_text"`
	SourceUntilSeq int64  `json:"source_until_seq"`
}

type UpsertSessionSummaryResult struct {
	Summary SessionSummary `json:"summary"`
	Events  []Event        `json:"events"`
}

type TraceIndexEntry struct {
	TraceID        string    `json:"trace_id"`
	WorkspaceID    string    `json:"workspace_id"`
	SessionID      string    `json:"session_id"`
	TurnID         string    `json:"turn_id"`
	SessionTitle   string    `json:"session_title,omitempty"`
	SessionStatus  string    `json:"session_status,omitempty"`
	TurnStatus     string    `json:"turn_status,omitempty"`
	Summary        string    `json:"summary,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`
	DurationMillis int64     `json:"duration_ms"`
	StepCount      int       `json:"step_count"`
	SpanCount      int       `json:"span_count"`
	ToolCalls      int       `json:"tool_calls"`
	Errors         int       `json:"errors"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type TraceSpanIndexEntry struct {
	TraceID            string            `json:"trace_id"`
	WorkspaceID        string            `json:"workspace_id"`
	SessionID          string            `json:"session_id"`
	TurnID             string            `json:"turn_id"`
	SessionTitle       string            `json:"session_title,omitempty"`
	SpanID             string            `json:"span_id"`
	ParentSpanID       string            `json:"parent_span_id,omitempty"`
	Name               string            `json:"name"`
	Kind               string            `json:"kind"`
	Status             string            `json:"status,omitempty"`
	Depth              int               `json:"depth,omitempty"`
	StartTime          time.Time         `json:"start_time"`
	StartOffsetMillis  int64             `json:"start_offset_ms,omitempty"`
	DurationMillis     int64             `json:"duration_ms"`
	SelfDurationMillis int64             `json:"self_duration_ms,omitempty"`
	Critical           bool              `json:"critical,omitempty"`
	EventCount         int               `json:"event_count"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type UpsertTraceIndexInput struct {
	Trace TraceIndexEntry       `json:"trace"`
	Spans []TraceSpanIndexEntry `json:"spans"`
}

type ListTraceIndexInput struct {
	WorkspaceID     string `json:"workspace_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	TraceID         string `json:"trace_id,omitempty"`
	SessionStatus   string `json:"session_status,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Offset          int    `json:"offset,omitempty"`
}

type ListTraceSpanIndexInput struct {
	WorkspaceID           string `json:"workspace_id,omitempty"`
	TraceID               string `json:"trace_id,omitempty"`
	SessionID             string `json:"session_id,omitempty"`
	TurnID                string `json:"turn_id,omitempty"`
	Kind                  string `json:"kind,omitempty"`
	Status                string `json:"status,omitempty"`
	Query                 string `json:"q,omitempty"`
	Critical              *bool  `json:"critical,omitempty"`
	MinDurationMillis     int64  `json:"min_duration_ms,omitempty"`
	MaxDurationMillis     int64  `json:"max_duration_ms,omitempty"`
	MinSelfDurationMillis int64  `json:"min_self_duration_ms,omitempty"`
	IncludeArchived       bool   `json:"include_archived,omitempty"`
	Limit                 int    `json:"limit,omitempty"`
	Offset                int    `json:"offset,omitempty"`
}

type PruneTraceIndexInput struct {
	Before time.Time `json:"before"`
	Limit  int       `json:"limit,omitempty"`
}

const (
	ObservabilityExporterPerfetto = "perfetto"
	ObservabilityExporterOTLP     = "otlp"

	ObservabilityExporterRunSucceeded = "succeeded"
	ObservabilityExporterRunFailed    = "failed"
	ObservabilityExporterRunSkipped   = "skipped"
)

type ObservabilityExporterRun struct {
	ID           string     `json:"id"`
	WorkspaceID  string     `json:"workspace_id"`
	Exporter     string     `json:"exporter"`
	Status       string     `json:"status"`
	SessionID    string     `json:"session_id"`
	TurnID       string     `json:"turn_id"`
	TraceID      string     `json:"trace_id,omitempty"`
	Destination  string     `json:"destination,omitempty"`
	Message      string     `json:"message,omitempty"`
	AttemptCount int        `json:"attempt_count"`
	NextRetryAt  *time.Time `json:"next_retry_at,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   time.Time  `json:"finished_at"`
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

type WorkspaceSnapshot struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspace_id"`
	SessionID      string    `json:"session_id"`
	Sequence       int64     `json:"sequence"`
	ObjectRefID    string    `json:"object_ref_id"`
	ChecksumSHA256 string    `json:"checksum_sha256"`
	SizeBytes      int64     `json:"size_bytes"`
	FileCount      int       `json:"file_count"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

type CreateWorkspaceSnapshotInput struct {
	SessionID      string
	ObjectRefID    string
	ChecksumSHA256 string
	SizeBytes      int64
	FileCount      int
	CreatedBy      string
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

type ListSessionsInput struct {
	WorkspaceID     string `json:"workspace_id,omitempty"`
	OwnerID         string `json:"owner_id,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	ParentTurnID    string `json:"parent_turn_id,omitempty"`
	ParentedOnly    bool   `json:"parented_only,omitempty"`
	Status          string `json:"status,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type ReapExpiredWorkersInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
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

type CancelWorkerWorkInput struct {
	Reason string `json:"reason,omitempty"`
}

type RequeueWorkerWorkInput struct {
	WorkerID    string `json:"worker_id,omitempty"`
	ClearWorker bool   `json:"clear_worker,omitempty"`
}

type ReapExpiredWorkerWorkInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
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

type RecordObservabilityExporterRunInput struct {
	WorkspaceID  string     `json:"workspace_id,omitempty"`
	Exporter     string     `json:"exporter"`
	Status       string     `json:"status"`
	SessionID    string     `json:"session_id"`
	TurnID       string     `json:"turn_id"`
	TraceID      string     `json:"trace_id,omitempty"`
	Destination  string     `json:"destination,omitempty"`
	Message      string     `json:"message,omitempty"`
	AttemptCount int        `json:"attempt_count,omitempty"`
	NextRetryAt  *time.Time `json:"next_retry_at,omitempty"`
	StartedAt    time.Time  `json:"started_at,omitempty"`
	FinishedAt   time.Time  `json:"finished_at,omitempty"`
}

type ListObservabilityExporterRunsInput struct {
	WorkspaceID     string    `json:"workspace_id,omitempty"`
	Exporter        string    `json:"exporter,omitempty"`
	Status          string    `json:"status,omitempty"`
	SessionID       string    `json:"session_id,omitempty"`
	TurnID          string    `json:"turn_id,omitempty"`
	RetryDueBefore  time.Time `json:"retry_due_before,omitempty"`
	MaxAttemptCount int       `json:"max_attempt_count,omitempty"`
	Limit           int       `json:"limit,omitempty"`
}

const (
	SecurityAuditOutboxPending    = "pending"
	SecurityAuditOutboxDelivering = "delivering"
	SecurityAuditOutboxDelivered  = "delivered"
	SecurityAuditOutboxDeadLetter = "dead_letter"
)

type SecurityAuditOutboxEvent struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id,omitempty"`
	Payload            json.RawMessage `json:"payload"`
	IntegrityAlgorithm string          `json:"integrity_algorithm"`
	IntegrityKeyID     string          `json:"integrity_key_id,omitempty"`
	IntegrityDigest    string          `json:"integrity_digest"`
	Status             string          `json:"status"`
	AttemptCount       int             `json:"attempt_count"`
	NextAttemptAt      time.Time       `json:"next_attempt_at"`
	LeaseOwner         string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt     *time.Time      `json:"lease_expires_at,omitempty"`
	LastError          string          `json:"last_error,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	DeliveredAt        *time.Time      `json:"delivered_at,omitempty"`
}

type RecordSecurityAuditOutboxInput struct {
	ID                 string
	WorkspaceID        string
	Payload            json.RawMessage
	IntegrityAlgorithm string
	IntegrityKeyID     string
	IntegrityDigest    string
	CreatedAt          time.Time
}

type ClaimSecurityAuditOutboxInput struct {
	LeaseOwner    string
	Now           time.Time
	LeaseDuration time.Duration
	MaxAttempts   int
	Limit         int
}

type CompleteSecurityAuditOutboxInput struct {
	IDs        []string
	LeaseOwner string
	At         time.Time
}

type FailSecurityAuditOutboxInput struct {
	IDs           []string
	LeaseOwner    string
	ErrorMessage  string
	NextAttemptAt time.Time
	DeadLetter    bool
	At            time.Time
}

type ReplaySecurityAuditDeadLettersInput struct {
	Before time.Time
	Limit  int
}

type SecurityAuditOutboxStats struct {
	Pending              int64      `json:"pending"`
	Delivering           int64      `json:"delivering"`
	Delivered            int64      `json:"delivered"`
	DeadLetter           int64      `json:"dead_letter"`
	OldestPendingAt      *time.Time `json:"oldest_pending_at,omitempty"`
	OldestPendingSeconds int64      `json:"oldest_pending_seconds"`
}

type SecurityAuditIntegrityKeyStats struct {
	KeyID      string `json:"key_id"`
	Pending    int64  `json:"pending"`
	Delivering int64  `json:"delivering"`
	Delivered  int64  `json:"delivered"`
	DeadLetter int64  `json:"dead_letter"`
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
	OwnerType   string          `json:"owner_type,omitempty"`
	OwnerID     string          `json:"owner_id,omitempty"`
	Visibility  string          `json:"visibility,omitempty"`
	AgentKind   string          `json:"agent_kind,omitempty"`
	Name        string          `json:"name"`
	LLMProvider string          `json:"llm_provider,omitempty"`
	LLMModel    string          `json:"llm_model,omitempty"`
	Model       string          `json:"model,omitempty"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	MCP         json.RawMessage `json:"mcp,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
}

type EnsureAgentInput struct {
	ID          string
	WorkspaceID string
	OwnerType   string
	OwnerID     string
	Visibility  string
	AgentKind   string
	Name        string
	LLMProvider string
	LLMModel    string
	System      string
	Tools       json.RawMessage
	MCP         json.RawMessage
	Skills      json.RawMessage
}

type CreateAgentConfigVersionInput struct {
	AgentID                string          `json:"agent_id,omitempty"`
	ExpectedCurrentVersion int             `json:"expected_current_version,omitempty"`
	LLMProvider            string          `json:"llm_provider"`
	LLMModel               string          `json:"llm_model"`
	System                 string          `json:"system"`
	Tools                  json.RawMessage `json:"tools,omitempty"`
	MCP                    json.RawMessage `json:"mcp,omitempty"`
	Skills                 json.RawMessage `json:"skills,omitempty"`
}

type UpdateAgentInput struct {
	AgentID     string          `json:"agent_id,omitempty"`
	Name        string          `json:"name,omitempty"`
	LLMProvider string          `json:"llm_provider,omitempty"`
	LLMModel    string          `json:"llm_model,omitempty"`
	System      string          `json:"system,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	MCP         json.RawMessage `json:"mcp,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
}

type UpdateSessionRuntimeSettingsInput struct {
	RuntimeSettings json.RawMessage `json:"runtime_settings"`
}

type UpdateSessionMetadataInput struct {
	Pinned *bool     `json:"pinned,omitempty"`
	Tags   *[]string `json:"tags,omitempty"`
}

type UpgradeSessionAgentConfigInput struct {
	ToCurrent     bool   `json:"to_current,omitempty"`
	TargetVersion int    `json:"to_version,omitempty"`
	UpdatedBy     string `json:"updated_by,omitempty"`
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
	ParentSessionID       string          `json:"parent_session_id,omitempty"`
	SpawnDepth            int             `json:"spawn_depth,omitempty"`
	WorkspaceID           string          `json:"workspace_id"`
	OwnerID               string          `json:"owner_id"`
	AgentID               string          `json:"agent_id"`
	AgentConfigVersion    int             `json:"agent_config_version"`
	EnvironmentID         string          `json:"environment_id"`
	LLMProvider           string          `json:"llm_provider"`
	LLMProviderType       string          `json:"llm_provider_type,omitempty"`
	LLMModel              string          `json:"llm_model"`
	LLMBaseURL            string          `json:"llm_base_url,omitempty"`
	LLMAPIKeyEnv          string          `json:"llm_api_key_env,omitempty"`
	ContextWindowTokens   int             `json:"context_window_tokens"`
	LLMCapabilityType     string          `json:"llm_capability_type"`
	VisionLLMProvider     string          `json:"vision_llm_provider,omitempty"`
	VisionLLMProviderType string          `json:"vision_llm_provider_type,omitempty"`
	VisionLLMModel        string          `json:"vision_llm_model,omitempty"`
	VisionLLMBaseURL      string          `json:"vision_llm_base_url,omitempty"`
	VisionLLMAPIKeyEnv    string          `json:"vision_llm_api_key_env,omitempty"`
	SummaryText           string          `json:"summary_text,omitempty"`
	SummarySourceUntilSeq int64           `json:"summary_source_until_seq,omitempty"`
	System                string          `json:"system"`
	RuntimeSettings       json.RawMessage `json:"runtime_settings,omitempty"`
	Tools                 json.RawMessage `json:"tools,omitempty"`
	MCP                   json.RawMessage `json:"mcp,omitempty"`
	Skills                json.RawMessage `json:"skills,omitempty"`
}

type CreateEnvironmentInput struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	Config      json.RawMessage `json:"config"`
}

type CreateSessionInput struct {
	WorkspaceID        string `json:"workspace_id,omitempty"`
	OwnerID            string `json:"owner_id,omitempty"`
	AgentID            string `json:"agent_id,omitempty"`
	Agent              string `json:"agent,omitempty"`
	AgentConfigVersion int    `json:"agent_config_version,omitempty"`
	EnvironmentID      string `json:"environment_id"`
	ParentSessionID    string `json:"parent_session_id,omitempty"`
	ParentTurnID       string `json:"parent_turn_id,omitempty"`
	SpawnDepth         int    `json:"spawn_depth,omitempty"`
	Title              string `json:"title,omitempty"`
	CreatedBy          string `json:"created_by,omitempty"`
}

type SubagentLimits struct {
	MaxDepth              int `json:"max_depth,omitempty"`
	MaxChildrenPerTurn    int `json:"max_children_per_turn,omitempty"`
	MaxChildrenPerSession int `json:"max_children_per_session,omitempty"`
	WorkspaceActiveLimit  int `json:"workspace_active_limit,omitempty"`
	UserActiveLimit       int `json:"user_active_limit,omitempty"`
	WorkspaceQueuedLimit  int `json:"workspace_queued_limit,omitempty"`
	UserQueuedLimit       int `json:"user_queued_limit,omitempty"`
	QueueTimeoutSeconds   int `json:"queue_timeout_seconds,omitempty"`
}

type CreateSubagentSessionInput struct {
	Session CreateSessionInput `json:"session"`
	Limits  SubagentLimits     `json:"limits"`
}

type StartSubagentTurnInput struct {
	SessionID       string          `json:"session_id"`
	ParentSessionID string          `json:"parent_session_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Limits          SubagentLimits  `json:"limits"`
}

type EnqueueSubagentStartInput struct {
	SessionID       string          `json:"session_id"`
	ParentSessionID string          `json:"parent_session_id,omitempty"`
	ParentTurnID    string          `json:"parent_turn_id,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	Priority        int             `json:"priority,omitempty"`
	Limits          SubagentLimits  `json:"limits"`
}

type SubagentStartRequest struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	OwnerID         string          `json:"owner_id"`
	SessionID       string          `json:"session_id"`
	ParentSessionID string          `json:"parent_session_id"`
	ParentTurnID    string          `json:"parent_turn_id,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	Status          string          `json:"status"`
	Priority        int             `json:"priority"`
	QueuedAt        time.Time       `json:"queued_at"`
	ExpiresAt       time.Time       `json:"expires_at"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	TurnID          string          `json:"turn_id,omitempty"`
	CanceledAt      *time.Time      `json:"canceled_at,omitempty"`
	CancelReason    string          `json:"cancel_reason,omitempty"`
	WaitSeconds     int64           `json:"wait_seconds,omitempty"`
}

type CancelSubagentStartInput struct {
	SessionID       string `json:"session_id"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type GetSubagentMetricsInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type SubagentMetrics struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Queued      int64  `json:"queued"`
	Running     int64  `json:"running"`
	Rejected    int64  `json:"rejected"`
	WaitSeconds int64  `json:"wait_seconds"`
}

type SubagentTaskGroup struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	OwnerID         string     `json:"owner_id"`
	ParentSessionID string     `json:"parent_session_id"`
	ParentTurnID    string     `json:"parent_turn_id,omitempty"`
	ParentGroupID   string     `json:"parent_group_id,omitempty"`
	ParentItemIndex int        `json:"parent_item_index,omitempty"`
	Strategy        string     `json:"strategy"`
	ResultReducer   string     `json:"result_reducer"`
	Quorum          int        `json:"quorum,omitempty"`
	FailFast        bool       `json:"fail_fast,omitempty"`
	PlannedCount    int        `json:"planned_count"`
	CreatedAt       time.Time  `json:"created_at"`
	CanceledAt      *time.Time `json:"canceled_at,omitempty"`
	CancelReason    string     `json:"cancel_reason,omitempty"`
}

type CreateSubagentTaskGroupInput struct {
	WorkspaceID     string `json:"workspace_id"`
	OwnerID         string `json:"owner_id"`
	ParentSessionID string `json:"parent_session_id"`
	ParentTurnID    string `json:"parent_turn_id,omitempty"`
	ParentGroupID   string `json:"parent_group_id,omitempty"`
	ParentItemIndex int    `json:"parent_item_index,omitempty"`
	Strategy        string `json:"strategy,omitempty"`
	ResultReducer   string `json:"result_reducer,omitempty"`
	Quorum          int    `json:"quorum,omitempty"`
	FailFast        bool   `json:"fail_fast,omitempty"`
	PlannedCount    int    `json:"planned_count"`
}

type SubagentTaskGroupItem struct {
	GroupID              string          `json:"group_id"`
	ItemIndex            int             `json:"item_index"`
	AgentID              string          `json:"agent_id"`
	EnvironmentID        string          `json:"environment_id"`
	SessionID            string          `json:"session_id,omitempty"`
	Title                string          `json:"title,omitempty"`
	Message              string          `json:"message,omitempty"`
	Priority             int             `json:"priority,omitempty"`
	InitialState         string          `json:"initial_state"`
	ErrorType            string          `json:"error_type,omitempty"`
	ErrorMessage         string          `json:"error_message,omitempty"`
	ExpectedResultSchema json.RawMessage `json:"expected_result_schema,omitempty"`
	RetryCount           int             `json:"retry_count,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
}

type AppendSubagentTaskGroupItemInput struct {
	ItemIndex            int             `json:"item_index"`
	AgentID              string          `json:"agent_id"`
	EnvironmentID        string          `json:"environment_id"`
	SessionID            string          `json:"session_id,omitempty"`
	Title                string          `json:"title,omitempty"`
	Message              string          `json:"message,omitempty"`
	Priority             int             `json:"priority,omitempty"`
	InitialState         string          `json:"initial_state,omitempty"`
	ErrorType            string          `json:"error_type,omitempty"`
	ErrorMessage         string          `json:"error_message,omitempty"`
	ExpectedResultSchema json.RawMessage `json:"expected_result_schema,omitempty"`
}

type UpdateSubagentTaskGroupItemInput struct {
	SessionID            string          `json:"session_id,omitempty"`
	Title                string          `json:"title,omitempty"`
	Message              string          `json:"message,omitempty"`
	Priority             int             `json:"priority,omitempty"`
	InitialState         string          `json:"initial_state,omitempty"`
	ErrorType            string          `json:"error_type,omitempty"`
	ErrorMessage         string          `json:"error_message,omitempty"`
	ExpectedResultSchema json.RawMessage `json:"expected_result_schema,omitempty"`
	IncrementRetry       bool            `json:"increment_retry,omitempty"`
}

type CancelSubagentTaskGroupInput struct {
	GroupID         string `json:"group_id"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

type ReactivateSubagentTaskGroupInput struct {
	GroupID         string `json:"group_id"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
}

type GetSubagentTaskGroupMetricsInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type SubagentTaskGroupMetrics struct {
	WorkspaceID  string `json:"workspace_id,omitempty"`
	Pending      int64  `json:"pending"`
	Running      int64  `json:"running"`
	Completed    int64  `json:"completed"`
	Failed       int64  `json:"failed"`
	Canceled     int64  `json:"canceled"`
	ItemCreated  int64  `json:"item_created"`
	ItemStarted  int64  `json:"item_started"`
	ItemQueued   int64  `json:"item_queued"`
	ItemRejected int64  `json:"item_rejected"`
}

type ReapOrphanSubagentsInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type ReapedSubagent struct {
	Session         Session `json:"session"`
	ParentSessionID string  `json:"parent_session_id,omitempty"`
	Reason          string  `json:"reason"`
}

type OperatorAuditRecord struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	PrincipalID   string          `json:"principal_id"`
	OperatorLabel string          `json:"operator_label,omitempty"`
	Role          string          `json:"role"`
	Action        string          `json:"action"`
	ResourceType  string          `json:"resource_type"`
	ResourceID    string          `json:"resource_id,omitempty"`
	Outcome       string          `json:"outcome"`
	ErrorMessage  string          `json:"error_message,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type RecordOperatorAuditInput struct {
	WorkspaceID   string
	SessionID     string
	PrincipalID   string
	OperatorLabel string
	Role          string
	Action        string
	ResourceType  string
	ResourceID    string
	Outcome       string
	ErrorMessage  string
	Details       json.RawMessage
}

type ListOperatorAuditInput struct {
	WorkspaceID string
	SessionID   string
	PrincipalID string
	Action      string
	Limit       int
}

const (
	AgentDeliberationStatusRunning   = "running"
	AgentDeliberationStatusCompleted = "completed"
	AgentDeliberationStatusFailed    = "failed"
	AgentDeliberationStatusCanceled  = "canceled"

	AgentDeliberationPhaseRound1Running    = "round1_running"
	AgentDeliberationPhaseRound1Moderating = "round1_moderating"
	AgentDeliberationPhaseRound2Running    = "round2_running"
	AgentDeliberationPhaseFinalizing       = "finalizing"
	AgentDeliberationPhaseCompleted        = "completed"
	AgentDeliberationPhaseCanceled         = "canceled"
)

type AgentDeliberation struct {
	ID                     string          `json:"id"`
	WorkspaceID            string          `json:"workspace_id"`
	OwnerID                string          `json:"owner_id"`
	ParentSessionID        string          `json:"parent_session_id"`
	ParentTurnID           string          `json:"parent_turn_id,omitempty"`
	IdempotencyKey         string          `json:"idempotency_key,omitempty"`
	Objective              string          `json:"objective"`
	Strategy               string          `json:"strategy"`
	Status                 string          `json:"status"`
	Phase                  string          `json:"phase"`
	MaxParticipants        int             `json:"max_participants"`
	MaxRounds              int             `json:"max_rounds"`
	MaxTokens              int64           `json:"max_tokens,omitempty"`
	MaxSeconds             int             `json:"max_seconds,omitempty"`
	ModeratorAgentID       string          `json:"moderator_agent_id"`
	ModeratorEnvironmentID string          `json:"moderator_environment_id"`
	Plan                   json.RawMessage `json:"plan"`
	FinalGroupID           string          `json:"final_group_id,omitempty"`
	FinalResult            json.RawMessage `json:"final_result,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	CanceledAt             *time.Time      `json:"canceled_at,omitempty"`
	CancelReason           string          `json:"cancel_reason,omitempty"`
}

type AgentDeliberationParticipant struct {
	DeliberationID   string    `json:"deliberation_id"`
	ParticipantIndex int       `json:"participant_index"`
	RoleID           string    `json:"role_id"`
	RoleTitle        string    `json:"role_title"`
	Goal             string    `json:"goal"`
	AgentID          string    `json:"agent_id"`
	EnvironmentID    string    `json:"environment_id"`
	CreatedAt        time.Time `json:"created_at"`
}

type AgentDeliberationRound struct {
	DeliberationID   string          `json:"deliberation_id"`
	RoundNumber      int             `json:"round_number"`
	RoundType        string          `json:"round_type"`
	Status           string          `json:"status"`
	TaskGroupID      string          `json:"task_group_id"`
	ModeratorGroupID string          `json:"moderator_group_id,omitempty"`
	Summary          json.RawMessage `json:"summary,omitempty"`
	Questions        json.RawMessage `json:"questions,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
}

type AgentDeliberationContribution struct {
	DeliberationID   string          `json:"deliberation_id"`
	RoundNumber      int             `json:"round_number"`
	ParticipantIndex int             `json:"participant_index"`
	TaskGroupID      string          `json:"task_group_id"`
	ItemIndex        int             `json:"item_index"`
	SessionID        string          `json:"session_id,omitempty"`
	Status           string          `json:"status"`
	ContributionText string          `json:"contribution_text,omitempty"`
	ContributionJSON json.RawMessage `json:"contribution_json,omitempty"`
	RetryCount       int             `json:"retry_count,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type CreateAgentDeliberationInput struct {
	Deliberation AgentDeliberation
	Participants []AgentDeliberationParticipant
}

type UpdateAgentDeliberationInput struct {
	Status       string
	Phase        string
	FinalGroupID string
	FinalResult  json.RawMessage
	CancelReason string
}

type UpdateAgentDeliberationRoundInput struct {
	Status           string
	ModeratorGroupID string
	Summary          json.RawMessage
	Questions        json.RawMessage
	Complete         bool
}

type PromoteSubagentStartsInput struct {
	Limit int `json:"limit"`
}

type SubagentStartPromotion struct {
	Request SubagentStartRequest `json:"request"`
	Events  []Event              `json:"events"`
}

type SubagentQuotaViolation struct {
	Type    string
	Message string
	State   map[string]any
}

func (e SubagentQuotaViolation) Error() string {
	return e.Message
}

func (e SubagentQuotaViolation) Unwrap() error {
	return ErrConflict
}

type AppendEventInput struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}
