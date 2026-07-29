package toolresult

import (
	"encoding/json"
	"strings"
)

const ProtocolVersion = "tma.tool_result.v1"

const (
	CodeEncodeFailed               = "encode_failed"
	CodeInvalidToolArguments       = "invalid_tool_arguments"
	CodeInvalidToolSchema          = "invalid_tool_schema"
	CodePermissionDenied           = "permission_denied"
	CodeToolApprovalRequired       = "tool_approval_required"
	CodeToolExecutionCanceled      = "tool_execution_canceled"
	CodeToolExecutionFailed        = "tool_execution_failed"
	CodeToolExecutionIndeterminate = "tool_execution_indeterminate"
	CodeToolExecutionRejected      = "tool_execution_rejected"
	CodeUnsupportedTool            = "unsupported_tool"
	CodeUnsupportedToolAPI         = "unsupported_tool_api"
)

type Error struct {
	Type        string `json:"type"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable,omitempty"`
	Retryable   bool   `json:"retryable,omitempty"`
	Redacted    bool   `json:"redacted,omitempty"`
}

type Envelope struct {
	ID                  string `json:"id,omitempty"`
	Identifier          string `json:"identifier,omitempty"`
	APIName             string `json:"api_name,omitempty"`
	Content             string `json:"content,omitempty"`
	State               any    `json:"state,omitempty"`
	Artifacts           any    `json:"artifacts,omitempty"`
	ArtifactError       string `json:"artifact_error,omitempty"`
	PendingIntervention bool   `json:"pending_intervention,omitempty"`
	Error               *Error `json:"error,omitempty"`
	Success             bool   `json:"success"`
	Context             any    `json:"context,omitempty"`
}

func Data(envelope Envelope) map[string]any {
	data := map[string]any{
		"protocol_version":     ProtocolVersion,
		"id":                   envelope.ID,
		"identifier":           envelope.Identifier,
		"api_name":             envelope.APIName,
		"content":              envelope.Content,
		"state":                envelope.State,
		"artifacts":            envelope.Artifacts,
		"artifact_error":       envelope.ArtifactError,
		"pending_intervention": envelope.PendingIntervention,
		"error":                envelope.Error,
		"success":              envelope.Error == nil && envelope.Success,
	}
	if envelope.Context != nil {
		data["context"] = envelope.Context
	}
	if envelope.Error != nil {
		data["recoverable"] = envelope.Error.Recoverable
		if envelope.Error.Retryable {
			data["retryable"] = true
		}
		if envelope.Error.Redacted {
			data["redacted"] = true
		}
	}
	return data
}

func Message(envelope Envelope) string {
	encoded, err := json.Marshal(Data(envelope))
	if err != nil {
		return `{"protocol_version":"tma.tool_result.v1","success":false,"recoverable":true,"error":{"type":"encode_failed","message":"encode tool result failed","recoverable":true}}`
	}
	return string(encoded)
}

func FailureMessage(id, apiName, content string, state json.RawMessage, err Error) string {
	return Message(Envelope{
		ID:      id,
		APIName: apiName,
		Content: content,
		State:   RawJSON(state),
		Error:   &err,
	})
}

func RawJSON(raw json.RawMessage) any {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

func NormalizeError(errorType, message string, recoverable, retryable, redacted bool) Error {
	errorType = strings.TrimSpace(errorType)
	if errorType == "" {
		errorType = CodeToolExecutionFailed
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Tool execution failed. Retry or use another approach."
	}
	return Error{
		Type:        errorType,
		Message:     message,
		Recoverable: recoverable,
		Retryable:   retryable,
		Redacted:    redacted,
	}
}
