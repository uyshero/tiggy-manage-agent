package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func (m Message) Validate() error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("message id is required")
	}
	switch m.Role {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
	default:
		return fmt.Errorf("unsupported message role %q", m.Role)
	}
	if m.Visibility != VisibilityPublic && m.Visibility != VisibilityInternal {
		return fmt.Errorf("unsupported message visibility %q", m.Visibility)
	}
	if len(m.Content) == 0 {
		return fmt.Errorf("message content is required")
	}
	for index, content := range m.Content {
		if err := validateContent(content, 0); err != nil {
			return fmt.Errorf("content %d: %w", index, err)
		}
	}
	if len(m.Metadata) > 0 && !json.Valid(m.Metadata) {
		return fmt.Errorf("message metadata must be valid JSON")
	}
	return nil
}

func validateContent(content Content, depth int) error {
	if depth > 4 {
		return fmt.Errorf("content nesting exceeds limit")
	}
	switch content.Type {
	case ContentText:
		if content.Text == "" {
			return fmt.Errorf("text content is empty")
		}
	case ContentImage:
		if content.Image == nil || (strings.TrimSpace(content.Image.ObjectRefID) == "" && strings.TrimSpace(content.Image.URL) == "") {
			return fmt.Errorf("image reference is required")
		}
	case ContentThinking:
		if content.Thinking == nil {
			return fmt.Errorf("thinking block is required")
		}
	case ContentToolCall:
		if content.ToolCall == nil {
			return fmt.Errorf("tool call is required")
		}
		if err := content.ToolCall.Validate(); err != nil {
			return err
		}
	case ContentToolResult:
		if content.ToolResult == nil {
			return fmt.Errorf("tool result is required")
		}
		if err := content.ToolResult.validate(depth + 1); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported content type %q", content.Type)
	}
	return nil
}

func (c ToolCall) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("tool call id is required")
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("tool call name is required")
	}
	if len(c.Arguments) == 0 || !json.Valid(c.Arguments) {
		return fmt.Errorf("tool call arguments must be valid JSON")
	}
	trimmed := bytes.TrimSpace(c.Arguments)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return fmt.Errorf("tool call arguments must be a JSON object")
	}
	return nil
}

func (r ToolResult) validate(depth int) error {
	if strings.TrimSpace(r.CallID) == "" {
		return fmt.Errorf("tool result call id is required")
	}
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("tool result name is required")
	}
	if len(r.Content) == 0 {
		return fmt.Errorf("tool result content is required")
	}
	for index, content := range r.Content {
		if err := validateContent(content, depth); err != nil {
			return fmt.Errorf("tool result content %d: %w", index, err)
		}
	}
	if len(r.State) > 0 && !json.Valid(r.State) {
		return fmt.Errorf("tool result state must be valid JSON")
	}
	return nil
}

func (r Request) Validate() error {
	switch r.Purpose {
	case PurposeAgent, PurposeCompaction, PurposeVision, PurposeCompletionJudge, PurposeBranchSummary, PurposeEvaluation:
	default:
		return fmt.Errorf("unsupported request purpose %q", r.Purpose)
	}
	if err := r.Route.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.SessionID) == "" || strings.TrimSpace(r.TurnID) == "" || strings.TrimSpace(r.AttemptID) == "" {
		return fmt.Errorf("session id, turn id, and attempt id are required")
	}
	if r.MaxOutputTokens <= 0 {
		return fmt.Errorf("max output tokens must be positive")
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("request messages are required")
	}
	for index, message := range r.Messages {
		if err := message.Validate(); err != nil {
			return fmt.Errorf("message %d: %w", index, err)
		}
	}
	for index, tool := range r.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("tool %d name is required", index)
		}
		if len(tool.InputSchema) == 0 || !json.Valid(tool.InputSchema) {
			return fmt.Errorf("tool %q input schema must be valid JSON", tool.Name)
		}
	}
	return nil
}

func (r Route) Validate() error {
	if strings.TrimSpace(r.ProviderInstanceID) == "" || strings.TrimSpace(r.ModelID) == "" || strings.TrimSpace(r.CatalogRevision) == "" {
		return fmt.Errorf("provider instance, model, and catalog revision are required")
	}
	if r.ProviderConfigVersion <= 0 {
		return fmt.Errorf("provider config version must be positive")
	}
	if len(r.Parameters) > 0 && !json.Valid(r.Parameters) {
		return fmt.Errorf("route parameters must be valid JSON")
	}
	return nil
}

func (u Usage) Validate() error {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 || u.CachedInputTokens < 0 || u.ReasoningTokens < 0 || u.CostMicros < 0 {
		return fmt.Errorf("usage values cannot be negative")
	}
	if u.TotalTokens > 0 && u.TotalTokens < u.InputTokens+u.OutputTokens {
		return fmt.Errorf("total tokens cannot be less than input plus output tokens")
	}
	switch u.Source {
	case "", UsageSourceProvider, UsageSourceEstimated, UsageSourceUnavailable:
	default:
		return fmt.Errorf("unsupported usage source %q", u.Source)
	}
	return nil
}

func (u Usage) Add(other Usage) Usage {
	source := u.Source
	if source == "" {
		source = other.Source
	} else if other.Source != "" && source != other.Source {
		source = UsageSourceEstimated
	}
	return Usage{
		InputTokens:       u.InputTokens + other.InputTokens,
		OutputTokens:      u.OutputTokens + other.OutputTokens,
		TotalTokens:       u.TotalTokens + other.TotalTokens,
		CachedInputTokens: u.CachedInputTokens + other.CachedInputTokens,
		ReasoningTokens:   u.ReasoningTokens + other.ReasoningTokens,
		CostMicros:        u.CostMicros + other.CostMicros,
		Source:            source,
	}
}
