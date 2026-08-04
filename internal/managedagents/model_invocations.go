package managedagents

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	ModelInvocationCapabilityGenerate           = "generate"
	ModelInvocationCapabilityEmbedding          = "embedding"
	ModelInvocationCapabilityRerank             = "rerank"
	ModelInvocationCapabilitySpeechToText       = "speech_to_text"
	ModelInvocationCapabilityTextToSpeech       = "text_to_speech"
	ModelInvocationCapabilityMultimodalRealtime = "multimodal_realtime"

	ModelInvocationStatusCompleted = "completed"
	ModelInvocationStatusFailed    = "failed"
	ModelInvocationStatusCanceled  = "canceled"
)

type ModelInvocation struct {
	ID                 string    `json:"id"`
	WorkspaceID        string    `json:"workspace_id"`
	PrincipalID        string    `json:"principal_id"`
	ServiceIdentityID  string    `json:"service_identity_id,omitempty"`
	AuthType           string    `json:"auth_type,omitempty"`
	RequestID          string    `json:"request_id"`
	Capability         string    `json:"capability"`
	ProviderID         string    `json:"provider_id"`
	ProviderType       string    `json:"provider_type,omitempty"`
	Model              string    `json:"model"`
	Status             string    `json:"status"`
	ErrorCode          string    `json:"error_code,omitempty"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	TotalTokens        int64     `json:"total_tokens"`
	CachedInputTokens  int64     `json:"cached_input_tokens"`
	ReasoningTokens    int64     `json:"reasoning_tokens"`
	InputItems         int64     `json:"input_items"`
	OutputItems        int64     `json:"output_items"`
	InputBytes         int64     `json:"input_bytes"`
	OutputBytes        int64     `json:"output_bytes"`
	InputCharacters    int64     `json:"input_characters"`
	OutputCharacters   int64     `json:"output_characters"`
	InputAudioMillis   int64     `json:"input_audio_ms"`
	OutputAudioMillis  int64     `json:"output_audio_ms"`
	InputVideoFrames   int64     `json:"input_video_frames"`
	OutputVideoFrames  int64     `json:"output_video_frames"`
	InputVideoDropped  int64     `json:"input_video_dropped"`
	OutputVideoDropped int64     `json:"output_video_dropped"`
	InputVideoMillis   int64     `json:"input_video_ms"`
	OutputVideoMillis  int64     `json:"output_video_ms"`
	LatencyMillis      int64     `json:"latency_ms"`
	StartedAt          time.Time `json:"started_at"`
	CompletedAt        time.Time `json:"completed_at"`
}

type RecordModelInvocationInput struct {
	WorkspaceID        string
	PrincipalID        string
	ServiceIdentityID  string
	AuthType           string
	RequestID          string
	Capability         string
	ProviderID         string
	ProviderType       string
	Model              string
	Status             string
	ErrorCode          string
	InputTokens        int64
	OutputTokens       int64
	TotalTokens        int64
	CachedInputTokens  int64
	ReasoningTokens    int64
	InputItems         int64
	OutputItems        int64
	InputBytes         int64
	OutputBytes        int64
	InputCharacters    int64
	OutputCharacters   int64
	InputAudioMillis   int64
	OutputAudioMillis  int64
	InputVideoFrames   int64
	OutputVideoFrames  int64
	InputVideoDropped  int64
	OutputVideoDropped int64
	InputVideoMillis   int64
	OutputVideoMillis  int64
	LatencyMillis      int64
	StartedAt          time.Time
	CompletedAt        time.Time
}

type ListModelInvocationsInput struct {
	WorkspaceID       string
	PrincipalID       string
	ServiceIdentityID string
	Capability        string
	ProviderID        string
	Model             string
	Status            string
	From              *time.Time
	To                *time.Time
	Limit             int
}

type ModelInvocationSummary struct {
	RecordCount        int64 `json:"record_count"`
	CompletedCount     int64 `json:"completed_count"`
	FailedCount        int64 `json:"failed_count"`
	CanceledCount      int64 `json:"canceled_count"`
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	CachedInputTokens  int64 `json:"cached_input_tokens"`
	ReasoningTokens    int64 `json:"reasoning_tokens"`
	InputItems         int64 `json:"input_items"`
	OutputItems        int64 `json:"output_items"`
	InputBytes         int64 `json:"input_bytes"`
	OutputBytes        int64 `json:"output_bytes"`
	InputCharacters    int64 `json:"input_characters"`
	OutputCharacters   int64 `json:"output_characters"`
	InputAudioMillis   int64 `json:"input_audio_ms"`
	OutputAudioMillis  int64 `json:"output_audio_ms"`
	InputVideoFrames   int64 `json:"input_video_frames"`
	OutputVideoFrames  int64 `json:"output_video_frames"`
	InputVideoDropped  int64 `json:"input_video_dropped"`
	OutputVideoDropped int64 `json:"output_video_dropped"`
	InputVideoMillis   int64 `json:"input_video_ms"`
	OutputVideoMillis  int64 `json:"output_video_ms"`
	LatencyMillis      int64 `json:"latency_ms"`
}

type ModelInvocationReport struct {
	Summary ModelInvocationSummary `json:"summary"`
	Records []ModelInvocation      `json:"records"`
}

type ModelInvocationStore interface {
	RecordModelInvocationContext(context.Context, RecordModelInvocationInput) (ModelInvocation, error)
	ListModelInvocationsContext(context.Context, ListModelInvocationsInput) (ModelInvocationReport, error)
}

type ReserveModelInvocationQuotaInput struct {
	WorkspaceID       string
	PrincipalID       string
	ServiceIdentityID string
	Capability        string
	ProviderID        string
	Model             string
	WindowStartedAt   time.Time
	WorkspaceLimit    int
	IdentityLimit     int
}

type ModelInvocationQuotaReservation struct {
	Allowed       bool
	ExceededScope string
	Limit         int
	Current       int
}

type ModelInvocationQuotaStore interface {
	ReserveModelInvocationQuotaContext(context.Context, ReserveModelInvocationQuotaInput) (ModelInvocationQuotaReservation, error)
}

func NormalizeReserveModelInvocationQuotaInput(input ReserveModelInvocationQuotaInput) (ReserveModelInvocationQuotaInput, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	input.ServiceIdentityID = strings.TrimSpace(input.ServiceIdentityID)
	input.Capability = strings.TrimSpace(input.Capability)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.Model = strings.TrimSpace(input.Model)
	if input.WorkspaceID == "" || input.PrincipalID == "" || input.ProviderID == "" || input.Model == "" {
		return ReserveModelInvocationQuotaInput{}, fmt.Errorf("%w: model invocation quota identity and route are required", ErrInvalid)
	}
	switch input.Capability {
	case ModelInvocationCapabilityGenerate, ModelInvocationCapabilityEmbedding, ModelInvocationCapabilityRerank,
		ModelInvocationCapabilitySpeechToText, ModelInvocationCapabilityTextToSpeech, ModelInvocationCapabilityMultimodalRealtime:
	default:
		return ReserveModelInvocationQuotaInput{}, fmt.Errorf("%w: unsupported model invocation quota capability", ErrInvalid)
	}
	if input.WorkspaceLimit < 0 || input.IdentityLimit < 0 || input.WorkspaceLimit == 0 && input.IdentityLimit == 0 {
		return ReserveModelInvocationQuotaInput{}, fmt.Errorf("%w: at least one non-negative model invocation quota limit is required", ErrInvalid)
	}
	if input.WindowStartedAt.IsZero() {
		return ReserveModelInvocationQuotaInput{}, fmt.Errorf("%w: model invocation quota window is required", ErrInvalid)
	}
	input.WindowStartedAt = input.WindowStartedAt.UTC().Truncate(time.Minute)
	return input, nil
}

func NormalizeRecordModelInvocationInput(input RecordModelInvocationInput) (RecordModelInvocationInput, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	input.ServiceIdentityID = strings.TrimSpace(input.ServiceIdentityID)
	input.AuthType = strings.TrimSpace(input.AuthType)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.Capability = strings.TrimSpace(input.Capability)
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.ProviderType = strings.TrimSpace(input.ProviderType)
	input.Model = strings.TrimSpace(input.Model)
	input.Status = strings.TrimSpace(input.Status)
	input.ErrorCode = strings.TrimSpace(input.ErrorCode)
	if input.WorkspaceID == "" || input.PrincipalID == "" || input.RequestID == "" || input.ProviderID == "" || input.Model == "" {
		return RecordModelInvocationInput{}, fmt.Errorf("%w: model invocation identity, route, and request id are required", ErrInvalid)
	}
	switch input.Capability {
	case ModelInvocationCapabilityGenerate, ModelInvocationCapabilityEmbedding, ModelInvocationCapabilityRerank,
		ModelInvocationCapabilitySpeechToText, ModelInvocationCapabilityTextToSpeech, ModelInvocationCapabilityMultimodalRealtime:
	default:
		return RecordModelInvocationInput{}, fmt.Errorf("%w: unsupported model invocation capability", ErrInvalid)
	}
	switch input.Status {
	case ModelInvocationStatusCompleted, ModelInvocationStatusFailed, ModelInvocationStatusCanceled:
	default:
		return RecordModelInvocationInput{}, fmt.Errorf("%w: unsupported model invocation status", ErrInvalid)
	}
	if input.Status == ModelInvocationStatusCompleted && input.ErrorCode != "" {
		return RecordModelInvocationInput{}, fmt.Errorf("%w: completed model invocation cannot contain an error code", ErrInvalid)
	}
	if input.Status == ModelInvocationStatusFailed && input.ErrorCode == "" {
		return RecordModelInvocationInput{}, fmt.Errorf("%w: failed model invocation requires an error code", ErrInvalid)
	}
	for _, value := range []int64{
		input.InputTokens, input.OutputTokens, input.TotalTokens, input.CachedInputTokens, input.ReasoningTokens,
		input.InputItems, input.OutputItems, input.InputBytes, input.OutputBytes, input.InputCharacters,
		input.OutputCharacters, input.InputAudioMillis, input.OutputAudioMillis, input.LatencyMillis,
		input.InputVideoFrames, input.OutputVideoFrames, input.InputVideoDropped, input.OutputVideoDropped,
		input.InputVideoMillis, input.OutputVideoMillis,
	} {
		if value < 0 {
			return RecordModelInvocationInput{}, fmt.Errorf("%w: model invocation usage values cannot be negative", ErrInvalid)
		}
	}
	if input.StartedAt.IsZero() {
		input.StartedAt = time.Now().UTC()
	} else {
		input.StartedAt = input.StartedAt.UTC()
	}
	if input.CompletedAt.IsZero() {
		input.CompletedAt = time.Now().UTC()
	} else {
		input.CompletedAt = input.CompletedAt.UTC()
	}
	if input.CompletedAt.Before(input.StartedAt) {
		return RecordModelInvocationInput{}, fmt.Errorf("%w: model invocation completed_at cannot precede started_at", ErrInvalid)
	}
	return input, nil
}

func AddModelInvocationSummary(summary *ModelInvocationSummary, record ModelInvocation) {
	if summary == nil {
		return
	}
	summary.RecordCount++
	switch record.Status {
	case ModelInvocationStatusCompleted:
		summary.CompletedCount++
	case ModelInvocationStatusFailed:
		summary.FailedCount++
	case ModelInvocationStatusCanceled:
		summary.CanceledCount++
	}
	summary.InputTokens += record.InputTokens
	summary.OutputTokens += record.OutputTokens
	summary.TotalTokens += record.TotalTokens
	summary.CachedInputTokens += record.CachedInputTokens
	summary.ReasoningTokens += record.ReasoningTokens
	summary.InputItems += record.InputItems
	summary.OutputItems += record.OutputItems
	summary.InputBytes += record.InputBytes
	summary.OutputBytes += record.OutputBytes
	summary.InputCharacters += record.InputCharacters
	summary.OutputCharacters += record.OutputCharacters
	summary.InputAudioMillis += record.InputAudioMillis
	summary.OutputAudioMillis += record.OutputAudioMillis
	summary.InputVideoFrames += record.InputVideoFrames
	summary.OutputVideoFrames += record.OutputVideoFrames
	summary.InputVideoDropped += record.InputVideoDropped
	summary.OutputVideoDropped += record.OutputVideoDropped
	summary.InputVideoMillis += record.InputVideoMillis
	summary.OutputVideoMillis += record.OutputVideoMillis
	summary.LatencyMillis += record.LatencyMillis
}
