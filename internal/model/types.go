package model

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Visibility string

const (
	VisibilityPublic   Visibility = "public"
	VisibilityInternal Visibility = "internal"
)

type ContentType string

const (
	ContentText       ContentType = "text"
	ContentImage      ContentType = "image"
	ContentThinking   ContentType = "thinking"
	ContentToolCall   ContentType = "tool_call"
	ContentToolResult ContentType = "tool_result"
)

type Content struct {
	Type       ContentType     `json:"type"`
	Text       string          `json:"text,omitempty"`
	Image      *ImageReference `json:"image,omitempty"`
	Thinking   *ThinkingBlock  `json:"thinking,omitempty"`
	ToolCall   *ToolCall       `json:"tool_call,omitempty"`
	ToolResult *ToolResult     `json:"tool_result,omitempty"`
}

type ImageReference struct {
	ObjectRefID string `json:"object_ref_id,omitempty"`
	URL         string `json:"url,omitempty"`
	MediaType   string `json:"media_type,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

type ThinkingBlock struct {
	Text      string `json:"text,omitempty"`
	Signature string `json:"signature,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
}

type Message struct {
	ID         string          `json:"id"`
	Role       Role            `json:"role"`
	Visibility Visibility      `json:"visibility"`
	Content    []Content       `json:"content"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type ToolCall struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Arguments      json.RawMessage `json:"arguments"`
	ArgumentsError string          `json:"arguments_error,omitempty"`
}

type ToolResult struct {
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Content   []Content       `json:"content"`
	State     json.RawMessage `json:"state,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Retryable bool            `json:"retryable,omitempty"`
}

type ToolDefinition struct {
	Name             string          `json:"name"`
	Description      string          `json:"description,omitempty"`
	InputSchema      json.RawMessage `json:"input_schema"`
	OutputSchema     json.RawMessage `json:"output_schema,omitempty"`
	SideEffect       string          `json:"side_effect,omitempty"`
	Idempotency      string          `json:"idempotency,omitempty"`
	ConcurrencyClass string          `json:"concurrency_class,omitempty"`
	LockKeyTemplate  string          `json:"lock_key_template,omitempty"`
}

type RequestPurpose string

const (
	PurposeAgent           RequestPurpose = "agent"
	PurposeCompaction      RequestPurpose = "compaction"
	PurposeVision          RequestPurpose = "vision"
	PurposeCompletionJudge RequestPurpose = "completion_judge"
	PurposeBranchSummary   RequestPurpose = "branch_summary"
	PurposeEvaluation      RequestPurpose = "evaluation"
)

type Route struct {
	ProviderInstanceID    string          `json:"provider_instance_id"`
	ProviderConfigVersion int             `json:"provider_config_version"`
	ModelID               string          `json:"model_id"`
	CatalogRevision       string          `json:"catalog_revision"`
	PricingRevision       string          `json:"pricing_revision,omitempty"`
	CredentialRef         string          `json:"credential_ref,omitempty"`
	Parameters            json.RawMessage `json:"parameters,omitempty"`
}

type Request struct {
	Purpose         RequestPurpose   `json:"purpose"`
	Route           Route            `json:"route"`
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	MaxOutputTokens int              `json:"max_output_tokens"`
	SessionID       string           `json:"session_id"`
	TurnID          string           `json:"turn_id"`
	AttemptID       string           `json:"attempt_id"`
}

type StopReason string

const (
	StopReasonComplete StopReason = "complete"
	StopReasonToolCall StopReason = "tool_call"
	StopReasonLength   StopReason = "length"
	StopReasonCanceled StopReason = "canceled"
	StopReasonError    StopReason = "error"
)

type Response struct {
	Message           Message    `json:"message"`
	StopReason        StopReason `json:"stop_reason"`
	ProviderRequestID string     `json:"provider_request_id,omitempty"`
	Usage             Usage      `json:"usage"`
}

type DeltaType string

const (
	DeltaStarted  DeltaType = "started"
	DeltaText     DeltaType = "text"
	DeltaThinking DeltaType = "thinking"
	DeltaToolCall DeltaType = "tool_call"
	DeltaUsage    DeltaType = "usage"
	DeltaStopped  DeltaType = "stopped"
	DeltaError    DeltaType = "error"
)

type Delta struct {
	Type          DeltaType      `json:"type"`
	Index         int            `json:"index"`
	Text          string         `json:"text,omitempty"`
	ToolCall      *ToolCallDelta `json:"tool_call,omitempty"`
	Usage         *Usage         `json:"usage,omitempty"`
	StopReason    StopReason     `json:"stop_reason,omitempty"`
	ProviderError *ProviderError `json:"provider_error,omitempty"`
}

type ToolCallDelta struct {
	Index             int    `json:"index"`
	ID                string `json:"id,omitempty"`
	Name              string `json:"name,omitempty"`
	ArgumentsFragment string `json:"arguments_fragment,omitempty"`
}

type UsageSource string

const (
	UsageSourceProvider    UsageSource = "provider"
	UsageSourceEstimated   UsageSource = "estimated"
	UsageSourceUnavailable UsageSource = "unavailable"
)

type Usage struct {
	InputTokens       int64       `json:"input_tokens,omitempty"`
	OutputTokens      int64       `json:"output_tokens,omitempty"`
	TotalTokens       int64       `json:"total_tokens,omitempty"`
	CachedInputTokens int64       `json:"cached_input_tokens,omitempty"`
	ReasoningTokens   int64       `json:"reasoning_tokens,omitempty"`
	CostMicros        int64       `json:"cost_micros,omitempty"`
	Source            UsageSource `json:"source,omitempty"`
}

type ModelCapabilities struct {
	Streaming           bool `json:"streaming"`
	Vision              bool `json:"vision"`
	NativeTools         bool `json:"native_tools"`
	ParallelToolCalls   bool `json:"parallel_tool_calls"`
	StrictToolSchema    bool `json:"strict_tool_schema"`
	StructuredOutput    bool `json:"structured_output"`
	Reasoning           bool `json:"reasoning"`
	ReasoningReplay     bool `json:"reasoning_replay"`
	PromptCache         bool `json:"prompt_cache"`
	ContextWindowTokens int  `json:"context_window_tokens"`
	MaxOutputTokens     int  `json:"max_output_tokens"`
}

type LiveDelta struct {
	StreamID  string    `json:"stream_id"`
	Attempt   int       `json:"attempt"`
	Index     int       `json:"index"`
	Operation string    `json:"operation"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
