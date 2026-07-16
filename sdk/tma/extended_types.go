package tma

import (
	"encoding/json"
	"time"
)

const PortableAgentFormat = "tma.agent"
const PortableAgentSchemaVersion int32 = 1

type PortableAgentConfig struct {
	Name        string          `json:"name"`
	LLMProvider string          `json:"llm_provider"`
	LLMModel    string          `json:"llm_model"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	MCP         json.RawMessage `json:"mcp,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
}

type AgentExportDocument struct {
	Format              string              `json:"format"`
	SchemaVersion       int32               `json:"schema_version"`
	ExportedAt          time.Time           `json:"exported_at"`
	WorkspaceID         string              `json:"workspace_id,omitempty"`
	SourceAgentID       string              `json:"source_agent_id,omitempty"`
	SourceConfigVersion int32               `json:"source_config_version,omitempty"`
	Agent               PortableAgentConfig `json:"agent"`
}

type AgentImportRequest struct {
	Format        string              `json:"format"`
	SchemaVersion int32               `json:"schema_version"`
	WorkspaceID   string              `json:"workspace_id,omitempty"`
	Name          string              `json:"name,omitempty"`
	Agent         PortableAgentConfig `json:"agent"`
}

type AgentConfigRollbackResponse struct {
	Agent           Agent `json:"agent"`
	SourceVersion   int32 `json:"source_version"`
	PreviousVersion int32 `json:"previous_version"`
	NewVersion      int32 `json:"new_version"`
}

type ToolingHealthRequest struct {
	Kind       string `json:"kind,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

type ToolingHealthItem struct {
	Identifier            string   `json:"identifier"`
	Kind                  string   `json:"kind"`
	Status                string   `json:"status"`
	Detail                string   `json:"detail,omitempty"`
	LatencyMillis         int64    `json:"latency_ms,omitempty"`
	ToolCount             int32    `json:"tool_count,omitempty"`
	Version               int32    `json:"version,omitempty"`
	ServerName            string   `json:"server_name,omitempty"`
	Transport             string   `json:"transport,omitempty"`
	EstimatedTokens       int32    `json:"estimated_tokens,omitempty"`
	Capabilities          []string `json:"capabilities,omitempty"`
	ResourceCount         int32    `json:"resource_count,omitempty"`
	ResourceTemplateCount int32    `json:"resource_template_count,omitempty"`
	PromptCount           int32    `json:"prompt_count,omitempty"`
}

type MCPHostStats struct {
	Sessions             int32            `json:"sessions,omitempty"`
	InUseSessions        int32            `json:"in_use_sessions,omitempty"`
	MaxSessions          int32            `json:"max_sessions,omitempty"`
	IdleTimeoutSeconds   int64            `json:"idle_timeout_seconds,omitempty"`
	SweepIntervalSeconds int64            `json:"sweep_interval_seconds,omitempty"`
	StartsTotal          int64            `json:"starts_total,omitempty"`
	StopsTotal           int64            `json:"stops_total,omitempty"`
	ReapedTotal          int64            `json:"reaped_total,omitempty"`
	EvictionsTotal       int64            `json:"evictions_total,omitempty"`
	RejectionsTotal      int64            `json:"rejections_total,omitempty"`
	DiscardsTotal        int64            `json:"discards_total,omitempty"`
	LogMessagesByLevel   map[string]int64 `json:"log_messages_by_level,omitempty"`
}

type MCPHTTPHostStats struct {
	MCPHostStats
	DeleteErrorsTotal          int64 `json:"delete_errors_total,omitempty"`
	EgressPolicyEnabled        bool  `json:"egress_policy_enabled,omitempty"`
	EgressAllowHTTP            bool  `json:"egress_allow_http,omitempty"`
	EgressAllowPrivateNetworks bool  `json:"egress_allow_private_networks,omitempty"`
	EgressAllowedHostCount     int32 `json:"egress_allowed_host_count,omitempty"`
	EgressAllowedCIDRCount     int32 `json:"egress_allowed_cidr_count,omitempty"`
	EgressBlockedTotal         int64 `json:"egress_blocked_total,omitempty"`
}

type MCPRuntimeGuardStats struct {
	TrackedServers       int32            `json:"tracked_servers,omitempty"`
	OpenCircuits         int32            `json:"open_circuits,omitempty"`
	InFlight             int32            `json:"in_flight,omitempty"`
	CallsTotal           int64            `json:"calls_total,omitempty"`
	SuccessesTotal       int64            `json:"successes_total,omitempty"`
	FailuresTotal        int64            `json:"failures_total,omitempty"`
	CircuitRejectedTotal int64            `json:"circuit_rejected_total,omitempty"`
	WaitCanceledTotal    int64            `json:"wait_canceled_total,omitempty"`
	FailuresByClass      map[string]int64 `json:"failures_by_class,omitempty"`
}

type ToolingHealthResponse struct {
	AgentID         string                `json:"agent_id"`
	CheckedAt       time.Time             `json:"checked_at"`
	MCP             []ToolingHealthItem   `json:"mcp"`
	Skills          []ToolingHealthItem   `json:"skills"`
	MCPHost         *MCPHostStats         `json:"mcp_host,omitempty"`
	MCPHTTPHost     *MCPHTTPHostStats     `json:"mcp_http_host,omitempty"`
	MCPRuntimeGuard *MCPRuntimeGuardStats `json:"mcp_runtime_guard,omitempty"`
}

type UpdateSessionMetadataRequest struct {
	Pinned *bool     `json:"pinned,omitempty"`
	Tags   *[]string `json:"tags,omitempty"`
}

type RerunSessionRequest struct {
	MessageSeq               *int64                         `json:"message_seq,omitempty"`
	Title                    *string                        `json:"title,omitempty"`
	LLMProvider              *string                        `json:"llm_provider,omitempty"`
	LLMModel                 *string                        `json:"llm_model,omitempty"`
	InterventionMode         *string                        `json:"intervention_mode,omitempty"`
	ToolRuntime              *string                        `json:"tool_runtime,omitempty"`
	CloudSandboxRoot         *string                        `json:"cloud_sandbox_root,omitempty"`
	CloudSandboxImage        *string                        `json:"cloud_sandbox_image,omitempty"`
	CloudSandboxAllowNetwork *bool                          `json:"cloud_sandbox_allow_network,omitempty"`
	CompletionGate           *CompletionGateRuntimeSettings `json:"completion_gate,omitempty"`
}

type RerunSessionResponse struct {
	Session         Session `json:"session"`
	SourceSessionID string  `json:"source_session_id"`
	SourceEventSeq  int64   `json:"source_event_seq"`
	Events          []Event `json:"events"`
}

type SessionComparison struct {
	Left  SessionComparisonSide `json:"left"`
	Right SessionComparisonSide `json:"right"`
}

type SessionComparisonSide struct {
	Session     Session      `json:"session"`
	Prompt      string       `json:"prompt"`
	Result      string       `json:"result"`
	LLMProvider string       `json:"llm_provider"`
	LLMModel    string       `json:"llm_model"`
	DurationMS  int64        `json:"duration_ms"`
	Usage       SessionUsage `json:"usage"`
	Artifacts   []Artifact   `json:"artifacts"`
}

type AgentRuntimeConfig struct {
	SessionID             string          `json:"session_id"`
	WorkspaceID           string          `json:"workspace_id"`
	OwnerID               string          `json:"owner_id"`
	AgentID               string          `json:"agent_id"`
	AgentConfigVersion    int32           `json:"agent_config_version"`
	EnvironmentID         string          `json:"environment_id"`
	LLMProvider           string          `json:"llm_provider"`
	LLMModel              string          `json:"llm_model"`
	LLMProviderType       string          `json:"llm_provider_type,omitempty"`
	LLMBaseURL            string          `json:"llm_base_url,omitempty"`
	LLMAPIKeyEnv          string          `json:"llm_api_key_env,omitempty"`
	LLMCapabilityType     string          `json:"llm_capability_type"`
	ContextWindowTokens   int32           `json:"context_window_tokens"`
	VisionLLMProvider     string          `json:"vision_llm_provider,omitempty"`
	VisionLLMModel        string          `json:"vision_llm_model,omitempty"`
	VisionLLMProviderType string          `json:"vision_llm_provider_type,omitempty"`
	VisionLLMBaseURL      string          `json:"vision_llm_base_url,omitempty"`
	VisionLLMAPIKeyEnv    string          `json:"vision_llm_api_key_env,omitempty"`
	System                string          `json:"system"`
	Tools                 json.RawMessage `json:"tools,omitempty"`
	MCP                   json.RawMessage `json:"mcp,omitempty"`
	Skills                json.RawMessage `json:"skills,omitempty"`
	RuntimeSettings       json.RawMessage `json:"runtime_settings,omitempty"`
	SummaryText           string          `json:"summary_text,omitempty"`
	SummarySourceUntilSeq *int64          `json:"summary_source_until_seq,omitempty"`
}

type SessionRuntimeCapabilities struct {
	DefaultRuntime    string                              `json:"default_runtime"`
	AvailableRuntimes []string                            `json:"available_runtimes"`
	HumanInteraction  HumanInteractionRuntimeCapabilities `json:"human_interaction,omitempty"`
}

type HumanInteractionRuntimeCapabilities struct {
	Enabled        bool     `json:"enabled"`
	Modes          []string `json:"modes"`
	SupportsUpload bool     `json:"supports_upload"`
	Fallback       string   `json:"fallback"`
}

type CancelAgentDeliberationRequest struct {
	Reason string `json:"reason,omitempty"`
}

type RetryAgentDeliberationParticipantRequest struct {
	RoundNumber int32 `json:"round_number"`
}

type AgentDeliberation struct {
	ID                     string          `json:"id"`
	WorkspaceID            string          `json:"workspace_id"`
	OwnerID                string          `json:"owner_id"`
	ParentSessionID        string          `json:"parent_session_id"`
	ParentTurnID           string          `json:"parent_turn_id,omitempty"`
	IdempotencyKey         string          `json:"idempotency_key,omitempty"`
	Objective              string          `json:"objective"`
	Strategy               string          `json:"strategy"`
	ModeratorAgentID       string          `json:"moderator_agent_id"`
	ModeratorEnvironmentID string          `json:"moderator_environment_id"`
	MaxParticipants        int32           `json:"max_participants"`
	MaxRounds              int32           `json:"max_rounds"`
	MaxTokens              int64           `json:"max_tokens,omitempty"`
	MaxSeconds             int32           `json:"max_seconds,omitempty"`
	Phase                  string          `json:"phase"`
	Status                 string          `json:"status"`
	Plan                   json.RawMessage `json:"plan"`
	FinalResult            json.RawMessage `json:"final_result,omitempty"`
	FinalGroupID           string          `json:"final_group_id,omitempty"`
	CancelReason           string          `json:"cancel_reason,omitempty"`
	CanceledAt             *time.Time      `json:"canceled_at"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

type AgentDeliberationParticipant struct {
	DeliberationID   string    `json:"deliberation_id"`
	ParticipantIndex int32     `json:"participant_index"`
	RoleID           string    `json:"role_id"`
	RoleTitle        string    `json:"role_title"`
	Goal             string    `json:"goal"`
	AgentID          string    `json:"agent_id"`
	EnvironmentID    string    `json:"environment_id"`
	CreatedAt        time.Time `json:"created_at"`
}

type AgentDeliberationRound struct {
	DeliberationID   string          `json:"deliberation_id"`
	RoundNumber      int32           `json:"round_number"`
	RoundType        string          `json:"round_type"`
	Status           string          `json:"status"`
	TaskGroupID      string          `json:"task_group_id"`
	ModeratorGroupID string          `json:"moderator_group_id,omitempty"`
	Questions        json.RawMessage `json:"questions,omitempty"`
	Summary          json.RawMessage `json:"summary,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	CompletedAt      *time.Time      `json:"completed_at"`
}

type AgentDeliberationContribution struct {
	DeliberationID   string          `json:"deliberation_id"`
	RoundNumber      int32           `json:"round_number"`
	ParticipantIndex int32           `json:"participant_index"`
	ItemIndex        int32           `json:"item_index"`
	TaskGroupID      string          `json:"task_group_id"`
	SessionID        string          `json:"session_id,omitempty"`
	Status           string          `json:"status"`
	ContributionText string          `json:"contribution_text,omitempty"`
	ContributionJSON json.RawMessage `json:"contribution_json,omitempty"`
	RetryCount       int32           `json:"retry_count,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type AgentDeliberationRoundState struct {
	Round         AgentDeliberationRound          `json:"round"`
	Contributions []AgentDeliberationContribution `json:"contributions"`
}

type AgentDeliberationResponse struct {
	Deliberation AgentDeliberation              `json:"deliberation"`
	Participants []AgentDeliberationParticipant `json:"participants"`
	Rounds       []AgentDeliberationRoundState  `json:"rounds"`
	Completed    bool                           `json:"completed,omitempty"`
}

type CancelTaskGroupRequest struct {
	Reason string `json:"reason,omitempty"`
}

type SubagentTaskGroup struct {
	ID              string     `json:"id"`
	WorkspaceID     string     `json:"workspace_id"`
	OwnerID         string     `json:"owner_id"`
	ParentSessionID string     `json:"parent_session_id"`
	ParentTurnID    string     `json:"parent_turn_id,omitempty"`
	ParentGroupID   string     `json:"parent_group_id,omitempty"`
	ParentItemIndex *int32     `json:"parent_item_index,omitempty"`
	Strategy        string     `json:"strategy"`
	ResultReducer   string     `json:"result_reducer"`
	Quorum          int32      `json:"quorum,omitempty"`
	FailFast        bool       `json:"fail_fast,omitempty"`
	PlannedCount    int32      `json:"planned_count"`
	CancelReason    string     `json:"cancel_reason,omitempty"`
	CanceledAt      *time.Time `json:"canceled_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

type SubagentTaskGroupItem struct {
	GroupID              string          `json:"group_id"`
	ItemIndex            int32           `json:"item_index"`
	Title                string          `json:"title,omitempty"`
	Message              string          `json:"message,omitempty"`
	AgentID              string          `json:"agent_id"`
	EnvironmentID        string          `json:"environment_id"`
	InitialState         string          `json:"initial_state"`
	SessionID            string          `json:"session_id,omitempty"`
	Priority             int32           `json:"priority,omitempty"`
	ExpectedResultSchema json.RawMessage `json:"expected_result_schema,omitempty"`
	RetryCount           int32           `json:"retry_count,omitempty"`
	ErrorType            string          `json:"error_type,omitempty"`
	ErrorMessage         string          `json:"error_message,omitempty"`
	CreatedAt            time.Time       `json:"created_at"`
}

type AgentTaskGroupSummary struct {
	Status     string `json:"status"`
	Total      int32  `json:"total"`
	Queued     int32  `json:"queued"`
	Running    int32  `json:"running"`
	Waiting    int32  `json:"waiting"`
	Completed  int32  `json:"completed"`
	Failed     int32  `json:"failed"`
	Canceled   int32  `json:"canceled"`
	Rejected   int32  `json:"rejected"`
	Terminated int32  `json:"terminated"`
	Terminal   int32  `json:"terminal"`
}

type AgentTaskGroupAggregate struct {
	Reducer              string          `json:"reducer"`
	Text                 string          `json:"text,omitempty"`
	JSON                 json.RawMessage `json:"json,omitempty"`
	Schema               json.RawMessage `json:"schema,omitempty"`
	CompletedItemIndexes []int32         `json:"completed_item_indexes,omitempty"`
	FailedItemIndexes    []int32         `json:"failed_item_indexes,omitempty"`
	CanceledItemIndexes  []int32         `json:"canceled_item_indexes,omitempty"`
}

type AgentTaskGroupItemState struct {
	Item                  SubagentTaskGroupItem       `json:"item"`
	Status                string                      `json:"status"`
	Reason                string                      `json:"reason,omitempty"`
	Session               *Session                    `json:"session,omitempty"`
	AgentText             string                      `json:"agent_text,omitempty"`
	ResultJSON            json.RawMessage             `json:"result_json,omitempty"`
	ResultSchema          json.RawMessage             `json:"result_schema,omitempty"`
	ResultValid           bool                        `json:"result_valid"`
	ResultValidationError string                      `json:"result_validation_error,omitempty"`
	LastTurnStatus        string                      `json:"last_turn_status,omitempty"`
	EventCount            int32                       `json:"event_count,omitempty"`
	PendingApprovals      []Intervention              `json:"pending_approvals,omitempty"`
	NestedGroups          []AgentTaskGroupNestedState `json:"nested_groups,omitempty"`
	QueueRequest          *SubagentStartRequest       `json:"queue_request,omitempty"`
}

type AgentTaskGroupNestedState struct {
	Group     SubagentTaskGroup         `json:"group"`
	Status    string                    `json:"status"`
	Completed bool                      `json:"completed,omitempty"`
	Summary   AgentTaskGroupSummary     `json:"summary"`
	Aggregate AgentTaskGroupAggregate   `json:"aggregate"`
	Items     []AgentTaskGroupItemState `json:"items"`
}

type AgentTaskGroupResponse = AgentTaskGroupNestedState

type SubagentStartRequest struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	OwnerID         string          `json:"owner_id"`
	ParentSessionID string          `json:"parent_session_id"`
	ParentTurnID    string          `json:"parent_turn_id,omitempty"`
	SessionID       string          `json:"session_id"`
	TurnID          string          `json:"turn_id,omitempty"`
	Status          string          `json:"status"`
	Priority        int32           `json:"priority"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	QueuedAt        time.Time       `json:"queued_at"`
	StartedAt       *time.Time      `json:"started_at"`
	ExpiresAt       time.Time       `json:"expires_at"`
	WaitSeconds     int64           `json:"wait_seconds,omitempty"`
	CancelReason    string          `json:"cancel_reason,omitempty"`
	CanceledAt      *time.Time      `json:"canceled_at"`
}

type InspectorTaskGroupState struct {
	State         AgentTaskGroupResponse `json:"state"`
	TemplateID    string                 `json:"template_id,omitempty"`
	TemplateTitle string                 `json:"template_title,omitempty"`
}

type SessionTaskGroupTreeSummary struct {
	Sessions       int32 `json:"sessions"`
	Groups         int32 `json:"groups"`
	Items          int32 `json:"items"`
	Queued         int32 `json:"queued"`
	Running        int32 `json:"running"`
	Waiting        int32 `json:"waiting"`
	Rejected       int32 `json:"rejected"`
	MaxWaitSeconds int64 `json:"max_wait_seconds"`
}

type SessionTaskGroupTreeNode struct {
	Session    Session                    `json:"session"`
	TaskGroups []InspectorTaskGroupState  `json:"task_groups"`
	Children   []SessionTaskGroupTreeNode `json:"children"`
}

type SessionTaskGroupTree struct {
	Root    SessionTaskGroupTreeNode    `json:"root"`
	Summary SessionTaskGroupTreeSummary `json:"summary"`
}

type ReapOrphanSubagentsRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int32  `json:"limit,omitempty"`
}

type ReapedSubagent struct {
	Session         Session `json:"session"`
	ParentSessionID string  `json:"parent_session_id,omitempty"`
	Reason          string  `json:"reason"`
}

type ReapOrphanSubagentsResult struct {
	Count  int32            `json:"count"`
	Reaped []ReapedSubagent `json:"reaped"`
}

type TraceListQuery struct {
	WorkspaceID     string
	SessionID       string
	TurnID          string
	SessionStatus   string
	IncludeArchived bool
	Limit           int32
	Cursor          string
}

type TraceSpanListQuery struct {
	WorkspaceID           string
	TraceID               string
	SessionID             string
	TurnID                string
	Kind                  string
	Status                string
	Search                string
	Critical              *bool
	MinDurationMillis     int64
	MaxDurationMillis     int64
	MinSelfDurationMillis int64
	IncludeArchived       bool
	Limit                 int32
	Cursor                string
}

type TracePage struct {
	Items      []TraceCatalogEntry `json:"items"`
	NextCursor string              `json:"next_cursor"`
	HasMore    bool                `json:"has_more"`
}

type TraceCatalogEntry struct {
	TraceID       string     `json:"trace_id"`
	SessionID     string     `json:"session_id"`
	TurnID        string     `json:"turn_id"`
	SessionTitle  string     `json:"session_title,omitempty"`
	SessionStatus string     `json:"session_status,omitempty"`
	TurnStatus    string     `json:"turn_status,omitempty"`
	Summary       string     `json:"summary,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	DurationMS    int64      `json:"duration_ms"`
	StepCount     int32      `json:"step_count"`
	SpanCount     int32      `json:"span_count"`
	ToolCalls     int32      `json:"tool_calls"`
	Errors        int32      `json:"errors"`
}

type TraceSpanPage struct {
	Items      []TraceSpanCatalogEntry `json:"items"`
	NextCursor string                  `json:"next_cursor"`
	HasMore    bool                    `json:"has_more"`
}

type TraceSpanCatalogEntry struct {
	TraceID        string            `json:"trace_id"`
	SessionID      string            `json:"session_id"`
	TurnID         string            `json:"turn_id"`
	SessionTitle   string            `json:"session_title,omitempty"`
	SpanID         string            `json:"span_id"`
	ParentSpanID   string            `json:"parent_span_id,omitempty"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Status         string            `json:"status,omitempty"`
	Depth          int32             `json:"depth,omitempty"`
	StartTime      time.Time         `json:"start_time"`
	StartOffsetMS  int64             `json:"start_offset_ms,omitempty"`
	DurationMS     int64             `json:"duration_ms"`
	SelfDurationMS int64             `json:"self_duration_ms,omitempty"`
	Critical       bool              `json:"critical,omitempty"`
	EventCount     int32             `json:"event_count"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

type TraceSpanDetail struct {
	SessionID    string         `json:"session_id"`
	TurnID       string         `json:"turn_id"`
	TraceID      string         `json:"trace_id"`
	SessionTitle string         `json:"session_title,omitempty"`
	Span         TurnTraceSpan  `json:"span"`
	TraceStats   TurnTraceStats `json:"trace_stats,omitempty"`
}
