package tma

import (
	"encoding/json"
	"time"
)

type AgentTaskGroupItemTemplate struct {
	AgentID              string          `json:"agent_id,omitempty"`
	Agent                string          `json:"agent,omitempty"`
	EnvironmentID        string          `json:"environment_id,omitempty"`
	Title                string          `json:"title,omitempty"`
	Message              string          `json:"message"`
	Priority             int             `json:"priority,omitempty"`
	ExpectedResultSchema json.RawMessage `json:"expected_result_schema,omitempty"`
}

type AgentTaskGroupTemplate struct {
	ID                       string                       `json:"id"`
	Title                    string                       `json:"title"`
	Description              string                       `json:"description"`
	Strategy                 string                       `json:"strategy"`
	ResultReducer            string                       `json:"result_reducer"`
	Quorum                   int                          `json:"quorum,omitempty"`
	FailFast                 bool                         `json:"fail_fast,omitempty"`
	ItemsRequired            bool                         `json:"items_required,omitempty"`
	DefaultItems             []AgentTaskGroupItemTemplate `json:"default_items,omitempty"`
	ItemExpectedResultSchema json.RawMessage              `json:"item_expected_result_schema,omitempty"`
}

type AgentTaskGroupTemplateList struct {
	Templates []AgentTaskGroupTemplate `json:"templates"`
}

type AgentDiscussionStrategy struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type AgentDiscussionStrategyList struct {
	Strategies     []AgentDiscussionStrategy `json:"strategies"`
	TeamPlanSchema json.RawMessage           `json:"team_plan_schema"`
}

type TurnTrace struct {
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	TraceID   string          `json:"trace_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Summary   string          `json:"summary,omitempty"`
	Stats     TurnTraceStats  `json:"stats,omitempty"`
	Turns     []TraceTurnInfo `json:"turns,omitempty"`
	Graph     TurnTraceGraph  `json:"graph,omitempty"`
	Steps     []TurnTraceStep `json:"steps"`
	Spans     []TurnTraceSpan `json:"spans,omitempty"`
}

type TurnTraceStats struct {
	StartTime        *time.Time `json:"start_time,omitempty"`
	EndTime          *time.Time `json:"end_time,omitempty"`
	DurationMillis   int64      `json:"duration_ms"`
	StepCount        int        `json:"step_count"`
	SpanCount        int        `json:"span_count"`
	LLMRequests      int        `json:"llm_requests"`
	ToolCalls        int        `json:"tool_calls"`
	ApprovalWaits    int        `json:"approval_waits"`
	PendingApprovals int        `json:"pending_approvals"`
	Errors           int        `json:"errors"`
	ArtifactCount    int        `json:"artifact_count"`
}

type TraceTurnInfo struct {
	TurnID         string     `json:"turn_id"`
	Status         string     `json:"status,omitempty"`
	Summary        string     `json:"summary,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	EndedAt        *time.Time `json:"ended_at,omitempty"`
	DurationMillis int64      `json:"duration_ms"`
	StepCount      int        `json:"step_count"`
	SpanCount      int        `json:"span_count"`
	ToolCalls      int        `json:"tool_calls"`
	Errors         int        `json:"errors"`
}

type TurnTraceGraph struct {
	RootSpanIDs                []string            `json:"root_span_ids,omitempty"`
	Edges                      []TurnTraceSpanEdge `json:"edges,omitempty"`
	CriticalSpanIDs            []string            `json:"critical_span_ids,omitempty"`
	CriticalPathDurationMillis int64               `json:"critical_path_duration_ms,omitempty"`
	MaxDepth                   int                 `json:"max_depth,omitempty"`
}

type TurnTraceSpanEdge struct {
	ParentSpanID string `json:"parent_span_id"`
	ChildSpanID  string `json:"child_span_id"`
}

type TurnTraceSpan struct {
	TraceID            string               `json:"trace_id"`
	SpanID             string               `json:"span_id"`
	ParentSpanID       string               `json:"parent_span_id,omitempty"`
	ChildSpanIDs       []string             `json:"child_span_ids,omitempty"`
	Name               string               `json:"name"`
	Kind               string               `json:"kind"`
	Status             string               `json:"status,omitempty"`
	StartSeq           int64                `json:"start_seq,omitempty"`
	EndSeq             int64                `json:"end_seq,omitempty"`
	Depth              int                  `json:"depth,omitempty"`
	StartOffsetMillis  int64                `json:"start_offset_ms,omitempty"`
	StartTime          time.Time            `json:"start_time"`
	EndTime            time.Time            `json:"end_time"`
	DurationMillis     int64                `json:"duration_ms"`
	SelfDurationMillis int64                `json:"self_duration_ms,omitempty"`
	Critical           bool                 `json:"critical,omitempty"`
	EventCount         int                  `json:"event_count,omitempty"`
	Attributes         map[string]string    `json:"attributes,omitempty"`
	Events             []TurnTraceSpanEvent `json:"events,omitempty"`
}

type TurnTraceSpanEvent struct {
	Seq        int64             `json:"seq"`
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Time       time.Time         `json:"time"`
	Message    string            `json:"message,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type TurnTraceStep struct {
	Seq                  int64               `json:"seq"`
	Type                 string              `json:"type"`
	CreatedAt            time.Time           `json:"created_at"`
	TraceID              string              `json:"trace_id,omitempty"`
	SpanID               string              `json:"span_id,omitempty"`
	ParentSpanID         string              `json:"parent_span_id,omitempty"`
	SpanName             string              `json:"span_name,omitempty"`
	SpanKind             string              `json:"span_kind,omitempty"`
	SpanStatus           string              `json:"span_status,omitempty"`
	DurationMillis       int64               `json:"duration_ms,omitempty"`
	Message              string              `json:"message,omitempty"`
	Summary              string              `json:"summary,omitempty"`
	CallID               string              `json:"call_id,omitempty"`
	Identifier           string              `json:"identifier,omitempty"`
	APIName              string              `json:"api_name,omitempty"`
	Outcome              string              `json:"outcome,omitempty"`
	ApprovalSource       string              `json:"approval_source,omitempty"`
	DecisionReason       string              `json:"decision_reason,omitempty"`
	ArtifactError        string              `json:"artifact_error,omitempty"`
	Artifacts            []TurnTraceArtifact `json:"artifacts,omitempty"`
	ContentTruncated     bool                `json:"content_truncated,omitempty"`
	StateTruncated       bool                `json:"state_truncated,omitempty"`
	OriginalContentChars int64               `json:"original_content_chars,omitempty"`
	VisibleContentChars  int64               `json:"visible_content_chars,omitempty"`
	OriginalStateBytes   int64               `json:"original_state_bytes,omitempty"`
}

type TurnTraceArtifact struct {
	ArtifactID   string `json:"artifact_id,omitempty"`
	ObjectRefID  string `json:"object_ref_id,omitempty"`
	Name         string `json:"name,omitempty"`
	ArtifactType string `json:"artifact_type,omitempty"`
	DownloadPath string `json:"download_path,omitempty"`
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

type WorkerListQuery struct {
	WorkspaceID string
	Status      string
}

type ReapExpiredWorkersRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type ReapExpiredWorkersResult struct {
	Count   int32    `json:"count"`
	Expired []Worker `json:"expired"`
}

type WorkerDiagnoseRequest struct {
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	ProtocolVersion string          `json:"protocol_version,omitempty"`
	Namespace       string          `json:"namespace"`
	API             string          `json:"api"`
	Capabilities    []string        `json:"capabilities,omitempty"`
	Risk            string          `json:"risk,omitempty"`
	Runtime         string          `json:"runtime,omitempty"`
	Input           json.RawMessage `json:"input,omitempty"`
}

type WorkInvocation struct {
	ProtocolVersion string          `json:"protocol_version"`
	Namespace       string          `json:"namespace"`
	API             string          `json:"api"`
	Capabilities    []string        `json:"capabilities,omitempty"`
	Risk            string          `json:"risk,omitempty"`
	Runtime         string          `json:"runtime,omitempty"`
	Input           json.RawMessage `json:"input,omitempty"`
}

type WorkerDiagnoseResponse struct {
	Invocation  WorkInvocation          `json:"invocation"`
	Matches     int32                   `json:"matches"`
	Diagnostics []WorkerDiagnosisResult `json:"diagnostics"`
}

type WorkerDiagnosisResult struct {
	WorkerID       string   `json:"worker_id"`
	WorkspaceID    string   `json:"workspace_id"`
	Name           string   `json:"name"`
	WorkerType     string   `json:"worker_type"`
	Status         string   `json:"status"`
	Match          bool     `json:"match"`
	Reasons        []string `json:"reasons,omitempty"`
	Runtimes       []string `json:"runtimes,omitempty"`
	APIs           []string `json:"apis,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	LeaseExpiresAt *string  `json:"lease_expires_at,omitempty"`
	LastSeenAt     *string  `json:"last_seen_at,omitempty"`
	RegisteredBy   string   `json:"registered_by,omitempty"`
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

type EnqueueWorkerWorkRequest struct {
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	WorkerID      string          `json:"worker_id,omitempty"`
	EnvironmentID string          `json:"environment_id,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	TurnID        string          `json:"turn_id,omitempty"`
	WorkType      string          `json:"work_type,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

type CancelWorkerWorkRequest struct {
	Reason string `json:"reason,omitempty"`
}

type RequeueWorkerWorkRequest struct {
	WorkerID    string `json:"worker_id,omitempty"`
	ClearWorker bool   `json:"clear_worker,omitempty"`
}

type ReapExpiredWorkerWorkRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type ReapExpiredWorkerWorkResult struct {
	Count   int32        `json:"count"`
	Expired []WorkerWork `json:"expired"`
}

type WorkerSummary struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspace_id"`
	Name           string  `json:"name"`
	WorkerType     string  `json:"worker_type"`
	Status         string  `json:"status"`
	LeaseExpiresAt *string `json:"lease_expires_at,omitempty"`
	LastSeenAt     *string `json:"last_seen_at,omitempty"`
}

type WorkerWorkDiagnosis struct {
	Work    WorkerWork     `json:"work"`
	Worker  *WorkerSummary `json:"worker,omitempty"`
	Reasons []string       `json:"reasons,omitempty"`
	Actions []string       `json:"actions,omitempty"`
}

type WorkerWorkConflict struct {
	Error string `json:"error"`
	WorkerDiagnoseResponse
}

type ObservabilityStatus struct {
	Perfetto                   ExporterStatus                   `json:"perfetto"`
	OTLP                       ExporterStatus                   `json:"otlp"`
	SecurityAuditOutbox        *SecurityAuditOutboxStats        `json:"security_audit_outbox,omitempty"`
	SecurityAuditIntegrityKeys *SecurityAuditIntegrityKeyStatus `json:"security_audit_integrity_keys,omitempty"`
	Sampling                   ObservabilitySamplingStatus      `json:"sampling"`
	Retry                      ObservabilityRetryStatus         `json:"retry"`
	RecentRuns                 []ObservabilityExporterRun       `json:"recent_runs,omitempty"`
}

type ExporterStatus struct {
	Enabled       bool            `json:"enabled"`
	Configured    bool            `json:"configured"`
	Destination   string          `json:"destination,omitempty"`
	TokenProvided bool            `json:"token_provided,omitempty"`
	LastSuccess   *ExporterHealth `json:"last_success,omitempty"`
	LastFailure   *ExporterHealth `json:"last_failure,omitempty"`
	LastAttempt   *ExporterHealth `json:"last_attempt,omitempty"`
}

type ExporterHealth struct {
	At        time.Time `json:"at"`
	SessionID string    `json:"session_id,omitempty"`
	TurnID    string    `json:"turn_id,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type ObservabilitySamplingStatus struct {
	Enabled    bool    `json:"enabled"`
	SampleRate float64 `json:"sample_rate"`
	Configured bool    `json:"configured"`
}

type ObservabilityRetryStatus struct {
	Enabled              bool  `json:"enabled"`
	MaxAttempts          int   `json:"max_attempts"`
	InitialDelayMillis   int64 `json:"initial_delay_ms"`
	MaxDelayMillis       int64 `json:"max_delay_ms"`
	PendingRecentRetries int   `json:"pending_recent_retries"`
}

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

type ObservabilityRetryResult struct {
	Attempted int32 `json:"attempted"`
	Succeeded int32 `json:"succeeded"`
	Failed    int32 `json:"failed"`
	Skipped   int32 `json:"skipped"`
}

type SecurityAuditOutboxStats struct {
	Pending              int64      `json:"pending"`
	Delivering           int64      `json:"delivering"`
	Delivered            int64      `json:"delivered"`
	DeadLetter           int64      `json:"dead_letter"`
	OldestPendingAt      *time.Time `json:"oldest_pending_at,omitempty"`
	OldestPendingSeconds int64      `json:"oldest_pending_seconds"`
}

type SecurityAuditIntegrityKeyStatus struct {
	ActiveKeyID                    string                           `json:"active_key_id,omitempty"`
	HistoricalUnidentifiedBlocking int64                            `json:"historical_unidentified_blocking"`
	Keys                           []SecurityAuditIntegrityKeyState `json:"keys"`
}

type SecurityAuditIntegrityKeyState struct {
	KeyID        string `json:"key_id"`
	Configured   bool   `json:"configured"`
	Active       bool   `json:"active"`
	Pending      int64  `json:"pending"`
	Delivering   int64  `json:"delivering"`
	Delivered    int64  `json:"delivered"`
	DeadLetter   int64  `json:"dead_letter"`
	Blocking     int64  `json:"blocking"`
	SafeToRemove bool   `json:"safe_to_remove"`
}
