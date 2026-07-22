package agentruntime

import (
	"context"
	"encoding/json"
	"errors"

	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

var ErrVisionModelNotConfigured = errors.New("image attachments require a text+image model or a configured default vision model")

type Step struct {
	Type    string         `json:"type"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
	Private map[string]any `json:"-"`
}

type StreamEvent struct {
	Index     int
	ToolRound int
	Text      string
}

type TurnRequest struct {
	SessionID             string
	TurnID                string
	UserPayload           json.RawMessage
	History               []managedagents.ConversationMessage
	ImageParts            []llm.ContentPart
	CurrentUserSupplement string
	Config                Config
	EmitStep              func(context.Context, Step) error
	EmitStream            func(StreamEvent)
}

type Config struct {
	WorkspaceID           string
	EnvironmentID         string
	LLMProvider           string
	LLMProviderType       string
	LLMModel              string
	LLMBaseURL            string
	LLMAPIKey             string
	LLMCapabilityType     string
	VisionLLMProvider     string
	VisionLLMProviderType string
	VisionLLMModel        string
	VisionLLMBaseURL      string
	VisionLLMAPIKey       string
	ContextWindowTokens   int
	SummaryText           string
	SummarySourceUntilSeq int64
	TaskPlanContext       string
	System                string
	RuntimeSettings       json.RawMessage
	Tools                 json.RawMessage
	ModelTools            []llm.Tool
	Skills                json.RawMessage
	SkillsResolved        bool
	InterventionMode      string
	PermissionRules       []tools.PermissionRule
	ToolRegistry          tools.Registry
	ToolExecutor          tools.Executor
	ToolExecutionContext  tools.ExecutionContext
}
