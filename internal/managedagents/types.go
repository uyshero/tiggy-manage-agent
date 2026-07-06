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
	EventRuntimeLLMResponse = "runtime.llm_response"
	EventRuntimeToolCall    = "runtime.tool_call"
	EventRuntimeToolResult  = "runtime.tool_result"
	EventRuntimeCompleted   = "runtime.completed"
	EventRuntimeFailed      = "runtime.failed"
)

type Agent struct {
	ID             string       `json:"id"`
	WorkspaceID    string       `json:"workspace_id"`
	Name           string       `json:"name"`
	CurrentVersion int          `json:"current_version"`
	Version        AgentVersion `json:"version"`
	ArchivedAt     *time.Time   `json:"archived_at,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
}

type AgentVersion struct {
	Version   int             `json:"version"`
	Model     string          `json:"model"`
	System    string          `json:"system"`
	Tools     json.RawMessage `json:"tools,omitempty"`
	Skills    json.RawMessage `json:"skills,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
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
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	AgentID       string     `json:"agent_id"`
	AgentVersion  int        `json:"agent_version"`
	EnvironmentID string     `json:"environment_id"`
	Status        string     `json:"status"`
	Title         string     `json:"title,omitempty"`
	SandboxID     string     `json:"sandbox_id,omitempty"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	ArchivedAt    *time.Time `json:"archived_at,omitempty"`
}

type Event struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type CreateAgentInput struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Name        string          `json:"name"`
	Model       string          `json:"model"`
	System      string          `json:"system"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	Skills      json.RawMessage `json:"skills,omitempty"`
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
