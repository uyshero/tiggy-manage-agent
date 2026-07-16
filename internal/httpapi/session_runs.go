package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

type rerunSessionRequest struct {
	sessionRuntimeSettingsRequest
	Title      string `json:"title,omitempty"`
	MessageSeq int64  `json:"message_seq,omitempty"`
}

type rerunSessionResponse struct {
	SourceSessionID string                `json:"source_session_id"`
	SourceEventSeq  int64                 `json:"source_event_seq"`
	Session         managedagents.Session `json:"session"`
	Events          []managedagents.Event `json:"events"`
}

type sessionComparisonSide struct {
	Session     managedagents.Session           `json:"session"`
	LLMProvider string                          `json:"llm_provider"`
	LLMModel    string                          `json:"llm_model"`
	Prompt      string                          `json:"prompt"`
	Result      string                          `json:"result"`
	DurationMS  int64                           `json:"duration_ms"`
	Usage       managedagents.LLMUsageReport    `json:"usage"`
	Artifacts   []managedagents.SessionArtifact `json:"artifacts"`
}

type sessionComparisonResponse struct {
	Left  sessionComparisonSide `json:"left"`
	Right sessionComparisonSide `json:"right"`
}

func (s *Server) rerunSession(w http.ResponseWriter, r *http.Request) {
	request := rerunSessionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}

	source, err := s.getSessionForRequest(r, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	events, err := managedagents.ListEventsWithContext(r.Context(), s.store, source.ID, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	sourceEvent, err := selectRerunMessage(events, request.MessageSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = strings.TrimSpace(source.Title)
		if title == "" {
			title = "Rerun task"
		} else {
			title += " (rerun)"
		}
	}
	created, err := managedagents.CreateSessionWithContext(r.Context(), s.store, managedagents.CreateSessionInput{
		WorkspaceID:        source.WorkspaceID,
		OwnerID:            requestOwnerID(r, source.OwnerID),
		AgentID:            source.AgentID,
		AgentConfigVersion: source.AgentConfigVersion,
		EnvironmentID:      source.EnvironmentID,
		Title:              title,
		CreatedBy:          requestActorID(r, source.CreatedBy),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	keepCreated := false
	defer func() {
		if !keepCreated {
			_ = managedagents.DeleteSessionWithContext(r.Context(), s.store, created.ID)
		}
	}()

	created, err = managedagents.UpdateSessionRuntimeSettingsWithContext(r.Context(), s.store, created.ID, managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: cloneRuntimeSettings(source.RuntimeSettings),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	created, err = s.applySessionRuntimeSettingsPatch(r.Context(), created, request.sessionRuntimeSettingsRequest)
	if err != nil {
		writeError(w, err)
		return
	}
	createdEvents, err := managedagents.AppendEventsWithContext(r.Context(), s.store, created.ID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventUserMessage,
		Payload: append(json.RawMessage(nil), sourceEvent.Payload...),
	}})
	if err != nil {
		writeError(w, err)
		return
	}

	keepCreated = true
	s.dispatchRunnerEvents(r, created.ID, createdEvents)
	writeJSON(w, http.StatusCreated, rerunSessionResponse{
		SourceSessionID: source.ID,
		SourceEventSeq:  sourceEvent.Seq,
		Session:         created,
		Events:          createdEvents,
	})
}

func (s *Server) compareSessions(w http.ResponseWriter, r *http.Request) {
	leftID := strings.TrimSpace(r.URL.Query().Get("left_session_id"))
	rightID := strings.TrimSpace(r.URL.Query().Get("right_session_id"))
	if leftID == "" || rightID == "" {
		writeError(w, fmt.Errorf("%w: left_session_id and right_session_id are required", managedagents.ErrInvalid))
		return
	}
	if leftID == rightID {
		writeError(w, fmt.Errorf("%w: comparison sessions must be different", managedagents.ErrInvalid))
		return
	}

	left, err := s.sessionComparisonSnapshot(r, leftID)
	if err != nil {
		writeError(w, err)
		return
	}
	right, err := s.sessionComparisonSnapshot(r, rightID)
	if err != nil {
		writeError(w, err)
		return
	}
	if left.Session.WorkspaceID != right.Session.WorkspaceID {
		writeError(w, fmt.Errorf("%w: comparison sessions must belong to the same workspace", managedagents.ErrInvalid))
		return
	}
	writeJSON(w, http.StatusOK, sessionComparisonResponse{Left: left, Right: right})
}

func (s *Server) sessionComparisonSnapshot(r *http.Request, sessionID string) (sessionComparisonSide, error) {
	session, err := s.getSessionForRequest(r, sessionID)
	if err != nil {
		return sessionComparisonSide{}, err
	}
	config, err := managedagents.ResolveAgentRuntimeConfigWithContext(r.Context(), s.store, sessionID)
	if err != nil {
		return sessionComparisonSide{}, err
	}
	events, err := managedagents.ListEventsWithContext(r.Context(), s.store, sessionID, 0)
	if err != nil {
		return sessionComparisonSide{}, err
	}
	usage, err := managedagents.GetSessionLLMUsageWithContext(r.Context(), s.store, sessionID)
	if err != nil {
		return sessionComparisonSide{}, err
	}
	artifacts, err := managedagents.ListSessionArtifactsWithContext(r.Context(), s.store, sessionID)
	if err != nil {
		return sessionComparisonSide{}, err
	}

	var prompt string
	var result string
	var failure string
	var startedAt time.Time
	var endedAt time.Time
	for _, event := range events {
		if event.Type == managedagents.EventUserMessage && prompt == "" {
			prompt = messagePayloadText(event.Payload)
			startedAt = event.CreatedAt
		}
		if event.Type == managedagents.EventAgentMessage {
			result = messagePayloadText(event.Payload)
			endedAt = event.CreatedAt
		}
		if event.Type == managedagents.EventRuntimeFailed {
			failure = messagePayloadText(event.Payload)
			endedAt = event.CreatedAt
		}
	}
	if result == "" && failure != "" {
		result = "运行失败：" + failure
	}
	durationMS := int64(0)
	if !startedAt.IsZero() && !endedAt.IsZero() && !endedAt.Before(startedAt) {
		durationMS = endedAt.Sub(startedAt).Milliseconds()
	}
	return sessionComparisonSide{
		Session:     session,
		LLMProvider: config.LLMProvider,
		LLMModel:    config.LLMModel,
		Prompt:      prompt,
		Result:      result,
		DurationMS:  durationMS,
		Usage:       usage,
		Artifacts:   artifacts,
	}, nil
}

func (s *Server) applySessionRuntimeSettingsPatch(ctx context.Context, session managedagents.Session, request sessionRuntimeSettingsRequest) (managedagents.Session, error) {
	settings := map[string]any{}
	if len(session.RuntimeSettings) > 0 && string(session.RuntimeSettings) != "null" {
		if err := json.Unmarshal(session.RuntimeSettings, &settings); err != nil {
			return managedagents.Session{}, fmt.Errorf("%w: existing runtime_settings must be valid JSON", managedagents.ErrInvalid)
		}
	}
	if request.LLMProvider != nil || request.LLMModel != nil || request.Model != nil {
		currentConfig, err := managedagents.ResolveAgentRuntimeConfigWithContext(ctx, s.store, session.ID)
		if err != nil {
			return managedagents.Session{}, err
		}
		providerID := currentConfig.LLMProvider
		modelName := currentConfig.LLMModel
		if request.LLMProvider != nil {
			providerID = strings.TrimSpace(*request.LLMProvider)
		}
		if request.LLMModel != nil {
			modelName = strings.TrimSpace(*request.LLMModel)
		}
		if request.Model != nil {
			modelName = strings.TrimSpace(*request.Model)
		}
		if providerID == "" || modelName == "" {
			return managedagents.Session{}, fmt.Errorf("%w: llm_provider and llm_model are required together", managedagents.ErrInvalid)
		}
		provider, err := s.store.GetLLMProvider(providerID)
		if err != nil {
			return managedagents.Session{}, err
		}
		if !provider.Enabled {
			return managedagents.Session{}, fmt.Errorf("%w: llm_provider %q is disabled", managedagents.ErrInvalid, providerID)
		}
		models, err := s.store.ListLLMModels(providerID)
		if err != nil {
			return managedagents.Session{}, err
		}
		modelFound := false
		for _, model := range models {
			if model.Model == modelName {
				modelFound = true
				break
			}
		}
		if !modelFound {
			return managedagents.Session{}, fmt.Errorf("%w: llm_model %q not found for provider %q", managedagents.ErrInvalid, modelName, providerID)
		}
		settings["llm_provider"] = providerID
		settings["llm_model"] = modelName
	}
	if request.InterventionMode != nil {
		mode, ok := tools.NormalizeInterventionMode(*request.InterventionMode)
		if !ok {
			return managedagents.Session{}, fmt.Errorf("%w: unsupported intervention_mode %q", managedagents.ErrInvalid, *request.InterventionMode)
		}
		settings["intervention_mode"] = mode
	}
	if request.ToolRuntime != nil {
		runtime, ok := tools.NormalizeToolRuntime(*request.ToolRuntime)
		if !ok {
			return managedagents.Session{}, fmt.Errorf("%w: unsupported tool_runtime %q", managedagents.ErrInvalid, *request.ToolRuntime)
		}
		settings["tool_runtime"] = runtime
	}
	if request.CloudSandboxRoot != nil {
		settings["cloud_sandbox_root"] = strings.TrimSpace(*request.CloudSandboxRoot)
	}
	if request.CloudSandboxImage != nil {
		settings["cloud_sandbox_image"] = strings.TrimSpace(*request.CloudSandboxImage)
	}
	if request.AllowNetwork != nil {
		settings["cloud_sandbox_allow_network"] = *request.AllowNetwork
	}
	if request.HumanInteraction != nil {
		humanInteraction := map[string]any{}
		if existing, ok := settings["human_interaction"].(map[string]any); ok {
			for key, value := range existing {
				humanInteraction[key] = value
			}
		}
		if request.HumanInteraction.Enabled != nil {
			humanInteraction["enabled"] = *request.HumanInteraction.Enabled
		}
		if request.HumanInteraction.Modes != nil {
			humanInteraction["modes"] = normalizedHumanInteractionModes(request.HumanInteraction.Modes)
		}
		if request.HumanInteraction.SupportsUpload != nil {
			humanInteraction["supports_upload"] = *request.HumanInteraction.SupportsUpload
		}
		if request.HumanInteraction.Fallback != nil {
			humanInteraction["fallback"] = normalizeHumanInteractionFallback(*request.HumanInteraction.Fallback)
		}
		settings["human_interaction"] = humanInteraction
	}
	if request.CompletionGate != nil && request.CompletionGate.MaxRetries != nil {
		maxRetries := *request.CompletionGate.MaxRetries
		if maxRetries < 1 {
			maxRetries = 1
		}
		if maxRetries > 10 {
			maxRetries = 10
		}
		settings["completion_gate"] = map[string]any{"max_retries": maxRetries}
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return managedagents.Session{}, err
	}
	return managedagents.UpdateSessionRuntimeSettingsWithContext(ctx, s.store, session.ID, managedagents.UpdateSessionRuntimeSettingsInput{RuntimeSettings: raw})
}

func humanInteractionCapabilities(raw json.RawMessage) humanInteractionCapabilitiesResponse {
	capability := humanInteractionCapabilitiesResponse{
		Enabled:        execution.HumanInteractionEnabled(raw),
		Modes:          []string{"select", "multiselect", "form", "freeform"},
		SupportsUpload: false,
		Fallback:       "assistant_message",
	}
	if len(raw) == 0 || string(raw) == "null" {
		return capability
	}
	var decoded struct {
		HumanInteraction struct {
			Modes          []string `json:"modes"`
			SupportsUpload *bool    `json:"supports_upload"`
			Fallback       *string  `json:"fallback"`
		} `json:"human_interaction"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return capability
	}
	if decoded.HumanInteraction.Modes != nil {
		capability.Modes = normalizedHumanInteractionModes(decoded.HumanInteraction.Modes)
	}
	if decoded.HumanInteraction.SupportsUpload != nil {
		capability.SupportsUpload = *decoded.HumanInteraction.SupportsUpload
	}
	if decoded.HumanInteraction.Fallback != nil {
		capability.Fallback = normalizeHumanInteractionFallback(*decoded.HumanInteraction.Fallback)
	}
	return humanInteractionCapabilitiesResponse{
		Enabled:        capability.Enabled,
		Modes:          capability.Modes,
		SupportsUpload: capability.SupportsUpload,
		Fallback:       capability.Fallback,
	}
}

func normalizedHumanInteractionModes(values []string) []string {
	allowed := map[string]bool{"select": true, "multiselect": true, "form": true, "freeform": true}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		mode := strings.ToLower(strings.TrimSpace(value))
		if !allowed[mode] || seen[mode] {
			continue
		}
		seen[mode] = true
		result = append(result, mode)
	}
	if len(result) == 0 {
		return []string{"select", "multiselect", "form", "freeform"}
	}
	return result
}

func normalizeHumanInteractionFallback(value string) string {
	switch strings.TrimSpace(value) {
	case "assistant_message", "fail":
		return strings.TrimSpace(value)
	default:
		return "assistant_message"
	}
}

func selectRerunMessage(events []managedagents.Event, seq int64) (managedagents.Event, error) {
	for _, event := range events {
		if event.Type != managedagents.EventUserMessage {
			continue
		}
		if seq == 0 || event.Seq == seq {
			return event, nil
		}
	}
	if seq > 0 {
		return managedagents.Event{}, fmt.Errorf("%w: user message event seq %d", managedagents.ErrNotFound, seq)
	}
	return managedagents.Event{}, fmt.Errorf("%w: source session has no user message", managedagents.ErrInvalid)
}

func cloneRuntimeSettings(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), raw...)
}

func messagePayloadText(raw json.RawMessage) string {
	var payload struct {
		Content any    `json:"content"`
		Message string `json:"message"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	switch content := payload.Content.(type) {
	case string:
		return content
	case []any:
		parts := make([]string, 0, len(content))
		for _, item := range content {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, _ := entry["text"].(string)
			if text == "" {
				text, _ = entry["content"].(string)
			}
			if text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	for _, value := range []string{payload.Message, payload.Summary, payload.Text} {
		if value != "" {
			return value
		}
	}
	return ""
}
