package managedagents

import (
	"encoding/json"
	"time"
)

const (
	DefaultWorkspaceID = "wksp_default"

	SessionStatusProvisioning = "provisioning"
	SessionStatusIdle         = "idle"
	SessionStatusRunning      = "running"
	SessionStatusInterrupting = "interrupting"
	// failed 保留给系统级 Session 故障；普通 Runner 执行失败只标记 turn failed 并回到 idle。
	SessionStatusFailed     = "failed"
	SessionStatusTerminated = "terminated"

	EventSessionStatusProvisioning = "session.status_provisioning"
	EventSessionStatusIdle         = "session.status_idle"
	EventSessionStatusRunning      = "session.status_running"
	EventSessionStatusInterrupting = "session.status_interrupting"
	// session.status_failed 保留给整个 Session 不可继续的故障，不用于普通 turn 失败。
	EventSessionStatusFailed     = "session.status_failed"
	EventSessionStatusTerminated = "session.status_terminated"

	EventUserMessage   = "user.message"
	EventUserInterrupt = "user.interrupt"
	EventAgentMessage  = "agent.message"

	EventRuntimeStarted     = "runtime.started"
	EventRuntimeThinking    = "runtime.thinking"
	EventRuntimeLLMRequest  = "runtime.llm_request"
	EventRuntimeLLMDelta    = "runtime.llm_delta"
	EventRuntimeLLMResponse = "runtime.llm_response"
	EventRuntimeToolCall    = "runtime.tool_call"
	EventRuntimeToolResult  = "runtime.tool_result"
	EventRuntimeCompleted   = "runtime.completed"
	EventRuntimeFailed      = "runtime.failed"
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
	ID                 string     `json:"id"`
	WorkspaceID        string     `json:"workspace_id"`
	AgentID            string     `json:"agent_id"`
	AgentConfigVersion int        `json:"agent_config_version"`
	EnvironmentID      string     `json:"environment_id"`
	Status             string     `json:"status"`
	Title              string     `json:"title,omitempty"`
	SandboxID          string     `json:"sandbox_id,omitempty"`
	CreatedBy          string     `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty"`
}

type Event struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type ConversationMessage struct {
	Seq     int64           `json:"seq"`
	Role    string          `json:"role"`
	Payload json.RawMessage `json:"payload,omitempty"`
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

type AgentRuntimeConfig struct {
	SessionID          string          `json:"session_id"`
	WorkspaceID        string          `json:"workspace_id"`
	AgentID            string          `json:"agent_id"`
	AgentConfigVersion int             `json:"agent_config_version"`
	LLMProvider        string          `json:"llm_provider"`
	LLMProviderType    string          `json:"llm_provider_type,omitempty"`
	LLMModel           string          `json:"llm_model"`
	LLMBaseURL         string          `json:"llm_base_url,omitempty"`
	LLMAPIKeyEnv       string          `json:"llm_api_key_env,omitempty"`
	System             string          `json:"system"`
	Tools              json.RawMessage `json:"tools,omitempty"`
	Skills             json.RawMessage `json:"skills,omitempty"`
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
