package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

const (
	speechProtocolDoubaoASR = modelruntime.SpeechProtocolDoubaoASR
	speechProtocolDoubaoTTS = modelruntime.SpeechProtocolDoubaoTTS
	speechMaxFrameBytes     = modelruntime.SpeechMaxFrameBytes
)

type speechClientEvent struct {
	Type         string `json:"type"`
	ProviderID   string `json:"provider_id,omitempty"`
	Model        string `json:"model,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Voice        string `json:"voice,omitempty"`
	Style        string `json:"style,omitempty"`
	Text         string `json:"text,omitempty"`
	AudioFormat  string `json:"audio_format,omitempty"`
	SampleRateHz int    `json:"sample_rate_hz,omitempty"`
}

type speechServerEvent struct {
	Type              string `json:"type"`
	SessionID         string `json:"session_id,omitempty"`
	Mode              string `json:"mode,omitempty"`
	Text              string `json:"text,omitempty"`
	AudioFormat       string `json:"audio_format,omitempty"`
	SampleRateHz      int    `json:"sample_rate_hz,omitempty"`
	Code              string `json:"code,omitempty"`
	Message           string `json:"message,omitempty"`
	Retryable         bool   `json:"retryable,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	LimitScope        string `json:"limit_scope,omitempty"`
}

type speechInvocationMetrics struct {
	InputItems       int64
	OutputItems      int64
	InputBytes       int64
	OutputBytes      int64
	InputCharacters  int64
	OutputCharacters int64
	Canceled         bool
}

func (s *Server) serveSpeechRealtime(w http.ResponseWriter, r *http.Request) {
	ensureRequestID(w, r)
	startedAt := time.Now().UTC()
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(speechMaxFrameBytes)
	defer connection.CloseNow()

	messageType, payload, err := connection.Read(r.Context())
	if err != nil {
		return
	}
	var start speechClientEvent
	if messageType != websocket.MessageText || json.Unmarshal(payload, &start) != nil || start.Type != "session.start" {
		_ = writeSpeechEvent(r.Context(), connection, speechServerEvent{Type: "error", Code: "invalid_session_start", Message: "first event must be session.start"})
		return
	}
	provider, model, err := s.resolveSpeechRoute(start)
	if err != nil {
		_ = writeSpeechEvent(r.Context(), connection, speechServerEvent{Type: "error", Code: "invalid_speech_route", Message: err.Error()})
		return
	}
	capability := managedagents.ModelInvocationCapabilitySpeechToText
	if model.CapabilityType == managedagents.LLMModelCapabilityTextToSpeech {
		capability = managedagents.ModelInvocationCapabilityTextToSpeech
	}
	invocation := s.newModelInvocationInput(r, provider, model, capability, startedAt)
	metrics := speechInvocationMetrics{}
	status, errorCode := managedagents.ModelInvocationStatusFailed, "speech_provider_error"
	defer func() {
		invocation.InputItems = metrics.InputItems
		invocation.OutputItems = metrics.OutputItems
		invocation.InputBytes = metrics.InputBytes
		invocation.OutputBytes = metrics.OutputBytes
		invocation.InputCharacters = metrics.InputCharacters
		invocation.OutputCharacters = metrics.OutputCharacters
		if model.Capabilities.SampleRateHz > 0 {
			invocation.InputAudioMillis = metrics.InputBytes * 1000 / int64(model.Capabilities.SampleRateHz*2)
			invocation.OutputAudioMillis = metrics.OutputBytes * 1000 / int64(model.Capabilities.SampleRateHz*2)
		}
		completeModelInvocation(&invocation, status, errorCode)
		s.recordModelInvocation(r, invocation)
	}()
	release, err := s.admitModelRuntime(r.Context(), modelRuntimeAdmissionRequestFromInvocation(modelRuntimeFamilySpeech, invocation))
	if err != nil {
		errorCode = modelRuntimeAdmissionErrorCode(modelRuntimeFamilySpeech, err)
		_ = writeSpeechEvent(r.Context(), connection, speechAdmissionErrorEvent(err))
		return
	}
	defer release()
	sessionContext := r.Context()
	cancelSession := func() {}
	if s.modelRuntimeAdmission != nil && s.modelRuntimeAdmission.policy.SpeechMaxSessionDuration > 0 {
		sessionContext, cancelSession = context.WithTimeout(r.Context(), s.modelRuntimeAdmission.policy.SpeechMaxSessionDuration)
	}
	defer cancelSession()
	if !provider.Enabled {
		errorCode = "speech_provider_disabled"
		_ = writeSpeechEvent(r.Context(), connection, speechServerEvent{Type: "error", Code: "speech_provider_disabled", Message: "speech provider is disabled"})
		return
	}
	apiKey := s.resolveLLMAPIKey(r.Context(), requestWorkspaceID(r, ""), provider.APIKeyEnv)
	if strings.TrimSpace(apiKey) == "" {
		errorCode = "speech_provider_unconfigured"
		_ = writeSpeechEvent(r.Context(), connection, speechServerEvent{Type: "error", Code: "speech_provider_unconfigured", Message: "speech provider credential is not configured"})
		return
	}
	speechRuntime := s.speechRuntime
	if speechRuntime == nil && s.speechDialer == nil {
		errorCode = "speech_provider_unavailable"
		_ = writeSpeechEvent(r.Context(), connection, speechServerEvent{Type: "error", Code: "speech_provider_unavailable", Message: "speech provider adapter is unavailable", Retryable: true})
		return
	}

	sessionID := strings.TrimSpace(start.SessionID)
	if sessionID == "" {
		sessionID = newSpeechID("speech")
	}
	request := modelruntime.SpeechRequest{
		Type: "session.open",
		Route: modelruntime.SpeechRoute{
			ProviderID: provider.ID, ProviderType: provider.ProviderType, BaseURL: provider.BaseURL,
			APIKey: apiKey, Model: model.Model, Protocol: model.Capabilities.Protocol,
			ResourceID: model.Capabilities.ResourceID, UpstreamModel: model.Capabilities.UpstreamModel,
			DefaultVoice: model.Capabilities.DefaultVoice, SampleRateHz: model.Capabilities.SampleRateHz,
		},
		Start: modelruntime.SpeechStart{
			SessionID: sessionID, Voice: start.Voice, Style: start.Style,
			AudioFormat: start.AudioFormat, SampleRateHz: start.SampleRateHz,
		},
	}
	var runtimeMetrics modelruntime.SpeechMetrics
	if speechRuntime != nil {
		runtimeMetrics, err = speechRuntime.ProxySpeech(sessionContext, r.Context(), connection, request)
	} else {
		runtimeMetrics, err = modelruntime.ProxySpeechWithDialer(sessionContext, r.Context(), connection, request, s.speechDialer)
	}
	metrics = speechInvocationMetrics{
		InputItems: runtimeMetrics.InputItems, OutputItems: runtimeMetrics.OutputItems,
		InputBytes: runtimeMetrics.InputBytes, OutputBytes: runtimeMetrics.OutputBytes,
		InputCharacters: runtimeMetrics.InputCharacters, OutputCharacters: runtimeMetrics.OutputCharacters,
		Canceled: runtimeMetrics.Canceled,
	}
	if errors.Is(sessionContext.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
		status, errorCode = managedagents.ModelInvocationStatusFailed, "speech_session_duration_exceeded"
		_ = writeSpeechEvent(r.Context(), connection, speechServerEvent{
			Type: "error", Code: errorCode, Message: "speech session exceeded the configured maximum duration", Retryable: true,
		})
	} else if runtimeMetrics.Canceled {
		status, errorCode = managedagents.ModelInvocationStatusCanceled, ""
	} else if runtimeMetrics.ErrorCode != "" {
		errorCode = runtimeMetrics.ErrorCode
	} else if runtimeMetrics.Completed || err == nil {
		status, errorCode = managedagents.ModelInvocationStatusCompleted, ""
	} else if isExpectedSpeechClose(err) {
		status, errorCode = managedagents.ModelInvocationStatusCanceled, ""
	} else {
		errorCode = "speech_session_failed"
	}
	if err != nil && !isExpectedSpeechClose(err) {
		s.logger.Warn("speech realtime session failed", "provider_id", provider.ID, "model", model.Model, "error", err)
	}
}

func speechAdmissionErrorEvent(err error) speechServerEvent {
	event := speechServerEvent{
		Type: "error", Code: modelRuntimeAdmissionErrorCode(modelRuntimeFamilySpeech, err),
		Message: "speech runtime admission is unavailable", Retryable: true,
	}
	var admissionError *modelRuntimeAdmissionError
	if errors.As(err, &admissionError) {
		event.Message = admissionError.Error()
		event.RetryAfterSeconds = admissionError.retryAfterSeconds()
		event.LimitScope = admissionError.scope
	}
	return event
}

func (s *Server) resolveSpeechRoute(start speechClientEvent) (managedagents.LLMProvider, managedagents.LLMModel, error) {
	providerID, modelName := strings.TrimSpace(start.ProviderID), strings.TrimSpace(start.Model)
	if providerID == "" || modelName == "" {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, errors.New("provider_id and model are required")
	}
	provider, err := s.store.GetLLMProvider(providerID)
	if err != nil {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, errors.New("speech provider not found")
	}
	model, exists, err := s.findLLMModel(providerID, modelName)
	if err != nil || !exists {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, errors.New("speech model not found")
	}
	expectedProtocol := ""
	switch model.CapabilityType {
	case managedagents.LLMModelCapabilitySpeechToText:
		expectedProtocol = speechProtocolDoubaoASR
	case managedagents.LLMModelCapabilityTextToSpeech:
		expectedProtocol = speechProtocolDoubaoTTS
	default:
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, errors.New("model does not support realtime speech")
	}
	if model.Capabilities.Protocol != expectedProtocol || model.Capabilities.ResourceID == "" || !strings.HasPrefix(provider.BaseURL, "wss://") {
		return managedagents.LLMProvider{}, managedagents.LLMModel{}, errors.New("speech model adapter configuration is invalid")
	}
	return provider, model, nil
}

func writeSpeechEvent(ctx context.Context, connection *websocket.Conn, event speechServerEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func newSpeechID(prefix string) string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return prefix + "-fallback"
	}
	return prefix + "-" + hex.EncodeToString(value)
}

func isExpectedSpeechClose(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway
}
