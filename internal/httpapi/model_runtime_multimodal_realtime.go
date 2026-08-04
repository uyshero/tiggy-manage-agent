package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"tiggy-manage-agent/internal/managedagents"
	modelruntime "tiggy-manage-agent/internal/modelruntimeprovider"
)

type resolvedMultimodalRealtimeRoute struct {
	Provider managedagents.LLMProvider
	Model    managedagents.LLMModel
	Route    modelruntime.MultimodalRoute
}

func (s *Server) serveMultimodalRealtime(w http.ResponseWriter, r *http.Request) {
	ensureRequestID(w, r)
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{modelruntime.MultimodalRealtimeSubprotocol},
	})
	if err != nil {
		return
	}
	connection.SetReadLimit(int64(modelruntime.MultimodalMediaHeaderBytes + modelruntime.MultimodalMaxTrackIDBytes + modelruntime.MultimodalMaxFrameBytes))
	defer connection.CloseNow()
	if connection.Subprotocol() != modelruntime.MultimodalRealtimeSubprotocol {
		_ = connection.Close(websocket.StatusPolicyViolation, "multimodal subprotocol is required")
		return
	}

	messageType, payload, err := connection.Read(r.Context())
	if err != nil {
		return
	}
	var start modelruntime.MultimodalSessionStart
	if messageType != websocket.MessageText || decodeStrictModelRuntimeJSON(payload, &start) != nil || start.Type != "session.start" {
		_ = writePublicMultimodalEvent(r.Context(), connection, modelruntime.MultimodalEvent{
			Type: "error", Code: "invalid_session_start", Message: "first event must be session.start",
		})
		return
	}
	resolved, err := s.resolveMultimodalRealtimeRoute(start)
	if err != nil {
		_ = writePublicMultimodalEvent(r.Context(), connection, modelruntime.MultimodalEvent{
			Type: "error", Code: "invalid_multimodal_route", Message: err.Error(),
		})
		return
	}

	recorder := s.newMultimodalInvocationRecorder(r, resolved, time.Now().UTC())
	release, err := recorder.Admit(r.Context())
	if err != nil {
		recorder.RejectAdmission(err)
		_ = writePublicMultimodalEvent(r.Context(), connection, multimodalAdmissionErrorEvent(err))
		return
	}
	defer release()

	sessionContext := r.Context()
	cancelSession := func() {}
	if s.modelRuntimeAdmission != nil && s.modelRuntimeAdmission.policy.SpeechMaxSessionDuration > 0 {
		sessionContext, cancelSession = context.WithTimeout(r.Context(), s.modelRuntimeAdmission.policy.SpeechMaxSessionDuration)
	}
	defer cancelSession()

	metrics := modelruntime.MultimodalMetrics{}
	apiKey := ""
	if strings.TrimSpace(resolved.Provider.APIKeyEnv) != "" {
		apiKey = s.resolveLLMAPIKey(r.Context(), requestWorkspaceID(r, managedagents.DefaultWorkspaceID), resolved.Provider.APIKeyEnv)
	}
	if strings.TrimSpace(resolved.Provider.APIKeyEnv) != "" && strings.TrimSpace(apiKey) == "" {
		metrics.ErrorCode = "multimodal_provider_unconfigured"
		recorder.Finish(metrics, nil)
		_ = writePublicMultimodalEvent(r.Context(), connection, modelruntime.MultimodalEvent{
			Type: "error", Code: metrics.ErrorCode, Message: "multimodal provider credential is not configured",
		})
		return
	}
	multimodalRuntime := s.multimodalRuntime
	if multimodalRuntime == nil && s.modelRuntimeExecutor != nil {
		multimodalRuntime, _ = s.modelRuntimeExecutor.(modelruntime.MultimodalProxy)
	}
	if multimodalRuntime == nil {
		metrics.ErrorCode = "multimodal_provider_unavailable"
		recorder.Finish(metrics, nil)
		_ = writePublicMultimodalEvent(r.Context(), connection, modelruntime.MultimodalEvent{
			Type: "error", Code: metrics.ErrorCode, Message: "multimodal provider adapter is unavailable", Retryable: true,
		})
		return
	}

	resolved.Route.APIKey = apiKey
	scope := managedagents.AccessScope{
		WorkspaceID: requestWorkspaceID(r, managedagents.DefaultWorkspaceID),
		OwnerID:     requestOwnerID(r, ""),
	}
	request := modelruntime.MultimodalRequest{
		Type: "session.open", Route: resolved.Route, Start: start,
		ResolveObjectRef: func(ctx context.Context, input modelruntime.MultimodalObjectRefInput) (modelruntime.MultimodalMediaFrame, error) {
			return s.resolveMultimodalObjectRef(ctx, scope, resolved.Route, start, input)
		},
	}
	metrics, proxyErr := multimodalRuntime.ProxyMultimodal(sessionContext, r.Context(), connection, request)
	if errors.Is(sessionContext.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
		metrics.ErrorCode = "multimodal_session_duration_exceeded"
		_ = writePublicMultimodalEvent(r.Context(), connection, modelruntime.MultimodalEvent{
			Type: "error", Code: metrics.ErrorCode, Message: "multimodal session exceeded the configured maximum duration", Retryable: true,
		})
	} else if isExpectedMultimodalClose(proxyErr) && !metrics.Completed && metrics.ErrorCode == "" {
		metrics.Canceled = true
		proxyErr = nil
	}
	recorder.Finish(metrics, proxyErr)
	if proxyErr != nil && !isExpectedMultimodalClose(proxyErr) && s.logger != nil {
		s.logger.Warn("multimodal realtime session failed", "provider_id", resolved.Provider.ID, "model", resolved.Model.Model, "error", proxyErr)
	}
	_ = connection.Close(websocket.StatusNormalClosure, "multimodal session complete")
}

func multimodalAdmissionErrorEvent(err error) modelruntime.MultimodalEvent {
	event := modelruntime.MultimodalEvent{
		Type: "error", Code: modelRuntimeAdmissionErrorCode(modelRuntimeFamilyMultimodal, err),
		Message: "multimodal runtime admission is unavailable", Retryable: true,
	}
	var admissionError *modelRuntimeAdmissionError
	if errors.As(err, &admissionError) {
		event.Message = admissionError.Error()
		event.RetryAfterSeconds = admissionError.retryAfterSeconds()
		event.LimitScope = admissionError.scope
	}
	return event
}

func writePublicMultimodalEvent(ctx context.Context, connection *websocket.Conn, event modelruntime.MultimodalEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return connection.Write(ctx, websocket.MessageText, payload)
}

func isExpectedMultimodalClose(err error) bool {
	return err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway
}

// resolveMultimodalRealtimeRoute performs catalog admission without opening a
// public WebSocket or resolving the Provider credential.
func (s *Server) resolveMultimodalRealtimeRoute(start modelruntime.MultimodalSessionStart) (resolvedMultimodalRealtimeRoute, error) {
	providerID, modelName := strings.TrimSpace(start.ProviderID), strings.TrimSpace(start.Model)
	if providerID == "" || modelName == "" {
		return resolvedMultimodalRealtimeRoute{}, errors.New("provider_id and model are required")
	}
	provider, err := s.store.GetLLMProvider(providerID)
	if err != nil {
		return resolvedMultimodalRealtimeRoute{}, errors.New("multimodal realtime provider not found")
	}
	model, exists, err := s.findLLMModel(providerID, modelName)
	if err != nil || !exists {
		return resolvedMultimodalRealtimeRoute{}, errors.New("multimodal realtime model not found")
	}
	if !provider.Enabled {
		return resolvedMultimodalRealtimeRoute{}, errors.New("multimodal realtime provider is disabled")
	}
	if model.CapabilityType != managedagents.LLMModelCapabilityMultimodalRealtime || model.Capabilities.Realtime == nil {
		return resolvedMultimodalRealtimeRoute{}, errors.New("model does not support multimodal realtime")
	}
	realtime := model.Capabilities.Realtime
	route := modelruntime.MultimodalRoute{
		ProviderID: provider.ID, ProviderType: provider.ProviderType, BaseURL: provider.BaseURL,
		Model: model.Model, UpstreamModel: model.Capabilities.UpstreamModel, Protocol: model.Capabilities.Protocol,
		Constraints: modelruntime.MultimodalRouteConstraints{
			InputFormats:     runtimeMultimodalFormats(realtime.InputFormats),
			OutputModalities: append([]string(nil), realtime.OutputModalities...),
			OutputFormats:    runtimeMultimodalFormats(realtime.OutputFormats),
			MaxInputTracks:   realtime.MaxInputTracks,
			MaxFrameBytes:    realtime.MaxFrameBytes,
		},
	}
	if err := modelruntime.ValidateMultimodalRequest(modelruntime.MultimodalRequest{Type: "session.open", Route: route, Start: start}); err != nil {
		return resolvedMultimodalRealtimeRoute{}, err
	}
	return resolvedMultimodalRealtimeRoute{Provider: provider, Model: model, Route: route}, nil
}

func runtimeMultimodalFormats(formats []managedagents.LLMRealtimeMediaFormat) []modelruntime.MultimodalMediaFormat {
	result := make([]modelruntime.MultimodalMediaFormat, len(formats))
	for index, format := range formats {
		result[index] = modelruntime.MultimodalMediaFormat{Kind: format.Kind, ContentType: format.ContentType, Codec: format.Codec}
	}
	return result
}
