package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/tools"
)

type appendEventsRequest struct {
	Events []managedagents.AppendEventInput `json:"events"`
}

type llmProviderRequest struct {
	ID           string  `json:"id"`
	ProviderType *string `json:"provider_type"`
	BaseURL      *string `json:"base_url"`
	APIKeyEnv    *string `json:"api_key_env"`
	Enabled      *bool   `json:"enabled"`
}

type llmModelRequest struct {
	ProviderID          string `json:"provider_id"`
	Model               string `json:"model"`
	ContextWindowTokens int    `json:"context_window_tokens"`
}

type agentConfigVersionRequest struct {
	LLMProvider *string          `json:"llm_provider"`
	LLMModel    *string          `json:"llm_model"`
	Model       *string          `json:"model"`
	System      *string          `json:"system"`
	Tools       *json.RawMessage `json:"tools"`
	Skills      *json.RawMessage `json:"skills"`
}

type sessionSummaryRequest struct {
	SummaryText    string `json:"summary_text"`
	SourceUntilSeq int64  `json:"source_until_seq"`
}

type sessionRuntimeSettingsRequest struct {
	InterventionMode *string `json:"intervention_mode"`
}

type interventionDecisionRequest struct {
	Reason string `json:"reason,omitempty"`
}

func (s *Server) listLLMProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.store.ListLLMProviders()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Server) createLLMProvider(w http.ResponseWriter, r *http.Request) {
	var request llmProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	provider, err := s.store.UpsertLLMProvider(managedagents.UpsertLLMProviderInput{
		ID:           request.ID,
		ProviderType: stringValue(request.ProviderType),
		BaseURL:      stringValue(request.BaseURL),
		APIKeyEnv:    stringValue(request.APIKeyEnv),
		Enabled:      enabled,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) getLLMProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.store.GetLLMProvider(r.PathValue("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) updateLLMProvider(w http.ResponseWriter, r *http.Request) {
	existing, err := s.store.GetLLMProvider(r.PathValue("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	var request llmProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if request.ProviderType != nil {
		existing.ProviderType = *request.ProviderType
	}
	if request.BaseURL != nil {
		existing.BaseURL = *request.BaseURL
	}
	if request.APIKeyEnv != nil {
		existing.APIKeyEnv = *request.APIKeyEnv
	}
	if request.Enabled != nil {
		existing.Enabled = *request.Enabled
	}

	provider, err := s.store.UpsertLLMProvider(managedagents.UpsertLLMProviderInput{
		ID:           existing.ID,
		ProviderType: existing.ProviderType,
		BaseURL:      existing.BaseURL,
		APIKeyEnv:    existing.APIKeyEnv,
		Enabled:      existing.Enabled,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Server) enableLLMProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.store.SetLLMProviderEnabled(r.PathValue("provider_id"), true)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) disableLLMProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.store.SetLLMProviderEnabled(r.PathValue("provider_id"), false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) listLLMModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.store.ListLLMModels(r.URL.Query().Get("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) upsertLLMModel(w http.ResponseWriter, r *http.Request) {
	var request llmModelRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	model, err := s.store.UpsertLLMModel(managedagents.UpsertLLMModelInput{
		ProviderID:          request.ProviderID,
		Model:               request.Model,
		ContextWindowTokens: request.ContextWindowTokens,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

func (s *Server) getSessionLLMUsage(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.GetSessionLLMUsage(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) getSessionSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.store.GetSessionSummary(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) upsertSessionSummary(w http.ResponseWriter, r *http.Request) {
	var request sessionSummaryRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.store.UpsertSessionSummary(r.PathValue("session_id"), managedagents.UpsertSessionSummaryInput{
		SummaryText:    request.SummaryText,
		SourceUntilSeq: request.SourceUntilSeq,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listLLMUsage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, err := parseOptionalTime(query.Get("from"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid from: %v", managedagents.ErrInvalid, err))
		return
	}
	to, err := parseOptionalTime(query.Get("to"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid to: %v", managedagents.ErrInvalid, err))
		return
	}

	report, err := s.store.ListLLMUsage(managedagents.ListLLMUsageInput{
		WorkspaceID: query.Get("workspace_id"),
		ProviderID:  query.Get("provider_id"),
		Model:       query.Get("model"),
		Status:      query.Get("status"),
		GroupBy:     query.Get("group_by"),
		From:        from,
		To:          to,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func parseOptionalTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateAgentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.LLMProvider == "" {
		input.LLMProvider = s.defaultLLMProvider
	}
	if input.LLMModel == "" && input.Model == "" {
		input.LLMModel = s.defaultLLMModel
	}

	agent, err := s.store.CreateAgent(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := s.store.GetAgent(r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) listAgentConfigVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.store.ListAgentConfigVersions(r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config_versions": versions})
}

func (s *Server) createAgentConfigVersion(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.GetAgent(r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	var request agentConfigVersionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	next := current.ConfigVersion
	if request.LLMProvider != nil {
		next.LLMProvider = *request.LLMProvider
	}
	if request.LLMModel != nil {
		next.LLMModel = *request.LLMModel
	}
	if request.Model != nil && request.LLMModel == nil {
		next.LLMModel = *request.Model
	}
	if request.System != nil {
		next.System = *request.System
	}
	if request.Tools != nil {
		next.Tools = cloneJSONRaw(*request.Tools)
	}
	if request.Skills != nil {
		next.Skills = cloneJSONRaw(*request.Skills)
	}

	agent, err := s.store.CreateAgentConfigVersion(managedagents.CreateAgentConfigVersionInput{
		AgentID:     current.ID,
		LLMProvider: next.LLMProvider,
		LLMModel:    next.LLMModel,
		System:      next.System,
		Tools:       next.Tools,
		Skills:      next.Skills,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agent)
}

func cloneJSONRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateEnvironmentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	environment, err := s.store.CreateEnvironment(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, environment)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateSessionInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	session, err := s.store.CreateSession(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) updateSessionRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	var request sessionRuntimeSettingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	settings := map[string]any{}
	if request.InterventionMode != nil {
		mode, ok := tools.NormalizeInterventionMode(*request.InterventionMode)
		if !ok {
			writeError(w, fmt.Errorf("%w: unsupported intervention_mode %q", managedagents.ErrInvalid, *request.InterventionMode))
			return
		}
		settings["intervention_mode"] = mode
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		writeError(w, err)
		return
	}
	session, err := s.store.UpdateSessionRuntimeSettings(r.PathValue("session_id"), managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: raw,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) listSessionInterventions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	interventions, err := s.store.ListSessionInterventions(r.PathValue("session_id"), status)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interventions": interventions})
}

func (s *Server) approveSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusApproved)
}

func (s *Server) rejectSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusRejected)
}

func (s *Server) decideSessionIntervention(w http.ResponseWriter, r *http.Request, status string) {
	var request interventionDecisionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.store.DecideSessionIntervention(r.PathValue("session_id"), managedagents.DecideSessionInterventionInput{
		TurnID:         r.PathValue("turn_id"),
		CallID:         r.PathValue("call_id"),
		Status:         status,
		DecisionReason: request.Reason,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	switch status {
	case managedagents.InterventionStatusApproved:
		executionResult, events, err := s.executeApprovedIntervention(r, result.Intervention)
		if err != nil {
			writeError(w, err)
			return
		}
		result.Events = append(result.Events, events...)
		if executionResult.Error == nil && len(result.Intervention.Continuation) > 0 {
			continuationEvents, err := s.continueApprovedIntervention(r, result.Intervention, executionResult)
			if err != nil {
				writeError(w, err)
				return
			}
			result.Events = append(result.Events, continuationEvents...)
		}
	case managedagents.InterventionStatusRejected:
		reason := "tool intervention rejected"
		if request.Reason != "" {
			reason = "tool intervention rejected: " + request.Reason
		}
		events, err := s.store.FailSessionTurn(result.Intervention.SessionID, result.Intervention.TurnID, reason)
		if err != nil {
			writeError(w, err)
			return
		}
		result.Events = append(result.Events, events...)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) executeApprovedIntervention(r *http.Request, intervention managedagents.SessionIntervention) (tools.ExecutionResult, []managedagents.Event, error) {
	executor := tools.NewDefaultExecutor()
	executionResult, err := executor.Execute(r.Context(), tools.Call{
		ID:         intervention.CallID,
		Identifier: intervention.ToolIdentifier,
		APIName:    intervention.APIName,
		Arguments:  intervention.Arguments,
	}, tools.ExecutionContext{
		SessionID: intervention.SessionID,
		TurnID:    intervention.TurnID,
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		return tools.ExecutionResult{}, nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"turn_id": intervention.TurnID,
		"message": "Received approved tool result.",
		"data": map[string]any{
			"id":                   intervention.CallID,
			"identifier":           intervention.ToolIdentifier,
			"api_name":             intervention.APIName,
			"content":              executionResult.Content,
			"state":                rawJSONValue(executionResult.State),
			"pending_intervention": executionResult.PendingIntervention,
			"error":                executionResult.Error,
			"success":              executionResult.Error == nil,
			"approval_source":      "user",
		},
	})
	if err != nil {
		return tools.ExecutionResult{}, nil, err
	}
	events, err := s.store.AppendEvents(intervention.SessionID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventRuntimeToolResult,
		Payload: payload,
	}})
	return executionResult, events, err
}

func (s *Server) continueApprovedIntervention(r *http.Request, intervention managedagents.SessionIntervention, executionResult tools.ExecutionResult) ([]managedagents.Event, error) {
	var messages []llm.Message
	if err := json.Unmarshal(intervention.Continuation, &messages); err != nil {
		return nil, fmt.Errorf("decode intervention continuation: %w", err)
	}
	messages = append(messages, llm.Message{
		Role:       "tool",
		ToolCallID: intervention.CallID,
		Content:    []llm.ContentPart{{Type: "text", Text: tools.ResultMessage(executionResult)}},
	})

	config, err := s.store.ResolveAgentRuntimeConfig(intervention.SessionID)
	if err != nil {
		return nil, err
	}
	var client llm.Client
	if s.continuationClient != nil {
		client = s.continuationClient
	} else {
		manager, err := llm.NewManagerWithConfig(llm.ManagerConfig{
			Provider:     config.LLMProvider,
			ProviderType: config.LLMProviderType,
			Model:        config.LLMModel,
			BaseURL:      config.LLMBaseURL,
			APIKey:       os.Getenv(config.LLMAPIKeyEnv),
		})
		if err != nil {
			return nil, err
		}
		client = manager
	}
	registry := tools.DefaultRegistry()
	policy := tools.InterventionPolicy{Mode: tools.ParseInterventionMode(config.RuntimeSettings)}
	executor := tools.NewDefaultExecutor()
	executionContext := tools.ExecutionContext{
		SessionID: intervention.SessionID,
		TurnID:    intervention.TurnID,
		Provider:  capability.LocalSystemProvider{},
	}

	var allEvents []managedagents.Event
	for round := intervention.ContinuationRound + 1; round < 4; round++ {
		requestEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeLLMRequest, intervention.TurnID, "Resuming LLM after approved tool result.", map[string]any{
			"provider":      config.LLMProvider,
			"provider_type": config.LLMProviderType,
			"model":         config.LLMModel,
			"message_count": len(messages),
			"tool_round":    round,
		})
		if err != nil {
			return allEvents, err
		}
		allEvents = append(allEvents, requestEvents...)

		llmRequest := llm.Request{
			Provider:     config.LLMProvider,
			ProviderType: config.LLMProviderType,
			Model:        config.LLMModel,
			BaseURL:      config.LLMBaseURL,
			APIKey:       os.Getenv(config.LLMAPIKeyEnv),
			Messages:     messages,
			Tools:        registry.ModelTools(),
		}
		startedAt := time.Now()
		llmResponse, err := client.Generate(r.Context(), llmRequest)
		if err != nil {
			s.recordContinuationUsage(intervention, config, llm.Usage{}, time.Since(startedAt), "failed", err.Error())
			return allEvents, err
		}
		s.recordContinuationUsage(intervention, config, llmResponse.Usage, time.Since(startedAt), "completed", "")

		responseEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeLLMResponse, intervention.TurnID, "Received resumed LLM response.", map[string]any{
			"role":          llmResponse.Message.Role,
			"content_count": len(llmResponse.Message.Content),
			"usage":         llmResponse.Usage,
			"tool_round":    round,
		})
		if err != nil {
			return allEvents, err
		}
		allEvents = append(allEvents, responseEvents...)

		toolCalls, hasToolCalls := toolCallsFromLLMResponse(llmResponse)
		if !hasToolCalls || len(toolCalls) == 0 {
			completedEvents, err := s.completeContinuation(intervention, llmResponse)
			if err != nil {
				return allEvents, err
			}
			allEvents = append(allEvents, completedEvents...)
			return allEvents, nil
		}

		assistantMessage := llm.Message{
			Role:      "assistant",
			Content:   []llm.ContentPart{{Type: "text", Text: contentPartsText(llmResponse.Message.Content)}},
			ToolCalls: append([]llm.ToolCall(nil), llmResponse.Message.ToolCalls...),
		}
		continuationMessages := append([]llm.Message(nil), messages...)
		continuationMessages = append(continuationMessages, assistantMessage)
		messages = append(messages, assistantMessage)

		for _, toolCall := range toolCalls {
			call := tools.NormalizeCall(toolCall)
			toolEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolCall, intervention.TurnID, "Received continuation tool call request.", map[string]any{
				"id":         call.ID,
				"identifier": call.Identifier,
				"api_name":   call.APIName,
				"arguments":  rawJSONValue(call.Arguments),
			})
			if err != nil {
				return allEvents, err
			}
			allEvents = append(allEvents, toolEvents...)

			if manifest, api, ok := registry.GetAPI(call.Identifier, call.APIName); ok {
				decision := policy.Evaluate(manifest, api)
				if decision.Required && !decision.Allowed {
					requiredEvents, err := s.pauseContinuationForIntervention(intervention, call, decision, continuationMessages, round)
					if err != nil {
						return allEvents, err
					}
					allEvents = append(allEvents, requiredEvents...)
					return allEvents, nil
				}
				if decision.Required && decision.Allowed && decision.Mode == tools.InterventionModeApproveForMe {
					approvedEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolInterventionApproved, intervention.TurnID, "Tool call auto-approved for execution.", map[string]any{
						"id":                call.ID,
						"identifier":        call.Identifier,
						"api_name":          call.APIName,
						"arguments":         rawJSONValue(call.Arguments),
						"intervention_mode": decision.Mode,
						"reason":            decision.Reason,
						"approval_source":   "auto",
					})
					if err != nil {
						return allEvents, err
					}
					allEvents = append(allEvents, approvedEvents...)
				}
			}

			result, err := executor.Execute(r.Context(), call, executionContext)
			if err != nil {
				return allEvents, err
			}
			resultEvents, err := s.appendToolResultEvent(intervention.SessionID, intervention.TurnID, call, result, "Received continuation tool result.")
			if err != nil {
				return allEvents, err
			}
			allEvents = append(allEvents, resultEvents...)
			if result.Error != nil {
				failedEvents, err := s.store.FailSessionTurn(intervention.SessionID, intervention.TurnID, "continuation tool failed: "+result.Error.Message)
				if err != nil {
					return allEvents, err
				}
				allEvents = append(allEvents, failedEvents...)
				return allEvents, nil
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    []llm.ContentPart{{Type: "text", Text: tools.ResultMessage(result)}},
			})
		}
	}

	failedEvents, err := s.store.FailSessionTurn(intervention.SessionID, intervention.TurnID, "continuation tool loop exceeded maximum rounds")
	if err != nil {
		return allEvents, err
	}
	s.recordContinuationUsage(intervention, config, llm.Usage{}, 0, "failed", "continuation tool loop exceeded maximum rounds")
	allEvents = append(allEvents, failedEvents...)
	return allEvents, nil
}

func (s *Server) recordContinuationUsage(intervention managedagents.SessionIntervention, config managedagents.AgentRuntimeConfig, usage llm.Usage, latency time.Duration, status string, errorMessage string) {
	if config.WorkspaceID == "" || config.AgentID == "" || config.AgentConfigVersion <= 0 {
		return
	}
	if config.LLMProvider == "" || config.LLMModel == "" {
		return
	}
	record := managedagents.RecordLLMUsageInput{
		WorkspaceID:        config.WorkspaceID,
		AgentID:            config.AgentID,
		AgentConfigVersion: config.AgentConfigVersion,
		SessionID:          intervention.SessionID,
		TurnID:             intervention.TurnID,
		ProviderID:         config.LLMProvider,
		ProviderType:       config.LLMProviderType,
		Model:              config.LLMModel,
		InputTokens:        usage.InputTokens,
		OutputTokens:       usage.OutputTokens,
		TotalTokens:        usage.TotalTokens,
		CachedInputTokens:  usage.CachedInputTokens,
		ReasoningTokens:    usage.ReasoningTokens,
		LatencyMillis:      latency.Milliseconds(),
		Status:             status,
		ErrorMessage:       errorMessage,
	}
	if _, err := s.store.RecordLLMUsage(record); err != nil {
		s.logger.Error("continuation llm usage record failed",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"status", status,
			"error", err,
		)
	}
}

func (s *Server) appendRuntimeEvent(sessionID string, eventType string, turnID string, message string, data map[string]any) ([]managedagents.Event, error) {
	payload, err := json.Marshal(map[string]any{
		"turn_id": turnID,
		"message": message,
		"data":    data,
	})
	if err != nil {
		return nil, err
	}
	return s.store.AppendEvents(sessionID, []managedagents.AppendEventInput{{
		Type:    eventType,
		Payload: payload,
	}})
}

func (s *Server) appendToolResultEvent(sessionID string, turnID string, call tools.Call, executionResult tools.ExecutionResult, message string) ([]managedagents.Event, error) {
	return s.appendRuntimeEvent(sessionID, managedagents.EventRuntimeToolResult, turnID, message, map[string]any{
		"id":                   call.ID,
		"identifier":           call.Identifier,
		"api_name":             call.APIName,
		"content":              executionResult.Content,
		"state":                rawJSONValue(executionResult.State),
		"pending_intervention": executionResult.PendingIntervention,
		"error":                executionResult.Error,
		"success":              executionResult.Error == nil,
	})
}

func (s *Server) pauseContinuationForIntervention(intervention managedagents.SessionIntervention, call tools.Call, decision tools.InterventionDecision, continuationMessages []llm.Message, round int) ([]managedagents.Event, error) {
	encodedContinuation, err := json.Marshal(continuationMessages)
	if err != nil {
		return nil, fmt.Errorf("encode continuation messages: %w", err)
	}
	if _, err := s.store.SaveSessionIntervention(intervention.SessionID, managedagents.SaveSessionInterventionInput{
		TurnID:            intervention.TurnID,
		CallID:            call.ID,
		ToolIdentifier:    call.Identifier,
		APIName:           call.APIName,
		Arguments:         call.Arguments,
		InterventionMode:  decision.Mode,
		Reason:            decision.Reason,
		Continuation:      encodedContinuation,
		ContinuationRound: round,
	}); err != nil {
		return nil, err
	}
	if err := s.store.MarkSessionTurnWaitingApproval(intervention.SessionID, intervention.TurnID); err != nil {
		return nil, err
	}
	return s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolInterventionRequired, intervention.TurnID, "Tool call requires approval before execution.", map[string]any{
		"id":                call.ID,
		"identifier":        call.Identifier,
		"api_name":          call.APIName,
		"arguments":         rawJSONValue(call.Arguments),
		"intervention_mode": decision.Mode,
		"reason":            decision.Reason,
	})
}

func (s *Server) completeContinuation(intervention managedagents.SessionIntervention, llmResponse llm.Response) ([]managedagents.Event, error) {
	agentPayload, err := json.Marshal(map[string]any{
		"protocol_version": "tma.agent_runtime.demo.v1",
		"content":          llmResponse.Message.Content,
	})
	if err != nil {
		return nil, err
	}
	completedEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeCompleted, intervention.TurnID, "Approved intervention continuation completed.", nil)
	if err != nil {
		return nil, err
	}
	turnEvents, err := s.store.CompleteSessionTurn(intervention.SessionID, intervention.TurnID, agentPayload)
	if err != nil {
		return completedEvents, err
	}
	return append(completedEvents, turnEvents...), nil
}

type toolCallEnvelope struct {
	ProtocolVersion string                 `json:"protocol_version"`
	ToolCalls       []toolCallEnvelopeCall `json:"tool_calls"`
}

type toolCallEnvelopeCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function toolCallFunction `json:"function,omitempty"`
}

type toolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func toolCallsFromLLMResponse(response llm.Response) ([]tools.Call, bool) {
	if len(response.Message.ToolCalls) > 0 {
		calls := make([]tools.Call, 0, len(response.Message.ToolCalls))
		for _, toolCall := range response.Message.ToolCalls {
			calls = append(calls, tools.NormalizeCall(tools.Call{
				ID:        toolCall.ID,
				APIName:   toolCall.Function.Name,
				Arguments: toolCall.Function.Arguments,
			}))
		}
		return calls, true
	}

	text := strings.TrimSpace(contentPartsText(response.Message.Content))
	if text == "" || !json.Valid([]byte(text)) {
		return nil, false
	}
	var envelope toolCallEnvelope
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return nil, false
	}
	if envelope.ProtocolVersion != tools.ToolCallProtocolVersion || len(envelope.ToolCalls) == 0 {
		return nil, false
	}
	calls := make([]tools.Call, 0, len(envelope.ToolCalls))
	for _, envelopeCall := range envelope.ToolCalls {
		calls = append(calls, tools.NormalizeCall(tools.Call{
			ID:        envelopeCall.ID,
			APIName:   envelopeCall.Function.Name,
			Arguments: envelopeCall.Function.Arguments,
		}))
	}
	return calls, true
}

func contentPartsText(parts []llm.ContentPart) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "\n")
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func (s *Server) archiveSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.ArchiveSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.PathValue("session_id")); err != nil {
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) appendSessionEvents(w http.ResponseWriter, r *http.Request) {
	var request appendEventsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	events, err := s.store.AppendEvents(r.PathValue("session_id"), request.Events)
	if err != nil {
		if reminderEvents, reminderErr := s.appendApprovalReminderIfWaiting(r.PathValue("session_id"), request.Events); reminderErr == nil && len(reminderEvents) > 0 {
			s.logEvents("session approval reminder appended", reminderEvents)
			writeJSON(w, http.StatusAccepted, map[string]any{"events": reminderEvents})
			return
		}
		writeError(w, err)
		return
	}

	// Store 先把事件和状态写入数据库；后台执行只基于已经落库的事件启动。
	sessionID := r.PathValue("session_id")
	s.logEvents("session events appended", events)
	s.dispatchRunnerEvents(r, sessionID, events)
	writeJSON(w, http.StatusCreated, map[string]any{"events": events})
}

func (s *Server) appendApprovalReminderIfWaiting(sessionID string, inputs []managedagents.AppendEventInput) ([]managedagents.Event, error) {
	if len(inputs) != 1 || inputs[0].Type != managedagents.EventUserMessage {
		return nil, nil
	}
	session, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	pending, err := s.store.ListSessionInterventions(sessionID, managedagents.InterventionStatusPending)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}

	events := make([]managedagents.AppendEventInput, 0, len(pending)+1)
	events = append(events, managedagents.AppendEventInput{
		Type:    managedagents.EventAgentMessage,
		Payload: approvalReminderPayload(pending),
	})
	for _, intervention := range pending {
		payload, err := json.Marshal(map[string]any{
			"turn_id": intervention.TurnID,
			"message": "Tool call is still waiting for approval.",
			"data": map[string]any{
				"id":                intervention.CallID,
				"identifier":        intervention.ToolIdentifier,
				"api_name":          intervention.APIName,
				"arguments":         rawJSONValue(intervention.Arguments),
				"intervention_mode": intervention.InterventionMode,
				"reason":            intervention.Reason,
			},
		})
		if err != nil {
			return nil, err
		}
		events = append(events, managedagents.AppendEventInput{
			Type:    managedagents.EventRuntimeToolInterventionRequired,
			Payload: payload,
		})
	}
	return s.store.AppendEvents(sessionID, events)
}

func approvalReminderPayload(pending []managedagents.SessionIntervention) json.RawMessage {
	turnID := pending[0].TurnID
	lines := []string{"A tool call is waiting for approval before this session can continue."}
	for _, intervention := range pending {
		lines = append(lines, fmt.Sprintf("- %s.%s call=%s", intervention.ToolIdentifier, intervention.APIName, intervention.CallID))
	}
	lines = append(lines, "Approve or reject the pending call, then send your next message.")
	payload, err := json.Marshal(map[string]any{
		"protocol_version": "tma.agent_runtime.demo.v1",
		"turn_id":          turnID,
		"content": []map[string]string{{
			"type": "text",
			"text": strings.Join(lines, "\n"),
		}},
	})
	if err != nil {
		return json.RawMessage(`{"content":[{"type":"text","text":"A tool call is waiting for approval."}]}`)
	}
	return payload
}

func (s *Server) dispatchRunnerEvents(r *http.Request, sessionID string, events []managedagents.Event) {
	for _, event := range events {
		switch event.Type {
		case managedagents.EventUserMessage:
			// turn_id 由 Store 生成并写入 payload，避免客户端伪造执行编号。
			turnID := payloadString(event.Payload, "turn_id")
			s.logger.Info("session turn starting",
				"session_id", sessionID,
				"turn_id", turnID,
				"event_id", event.ID,
				"event_seq", event.Seq,
			)
			if err := s.runner.StartTurn(r.Context(), runner.TurnRequest{
				SessionID:    sessionID,
				TurnID:       turnID,
				UserEventSeq: event.Seq,
				UserPayload:  event.Payload,
			}); err != nil {
				reason := err.Error()
				s.logger.Error("runner start turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"event_id", event.ID,
					"event_seq", event.Seq,
					"error", err,
				)
				failedEvents, failErr := s.store.FailSessionTurn(sessionID, turnID, reason)
				if failErr != nil {
					s.logger.Error("session turn fail transition failed",
						"session_id", sessionID,
						"turn_id", turnID,
						"error", failErr,
					)
					continue
				}
				s.logEvents("session turn failed", failedEvents)
			}
		case managedagents.EventUserInterrupt:
			turnID := payloadString(event.Payload, "turn_id")
			if err := s.runner.InterruptTurn(r.Context(), runner.InterruptRequest{
				SessionID: sessionID,
				TurnID:    turnID,
			}); err != nil {
				s.logger.Error("runner interrupt turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"event_id", event.ID,
					"event_seq", event.Seq,
					"error", err,
				)
			}
		}
	}
}

func (s *Server) logEvents(message string, events []managedagents.Event) {
	for _, event := range events {
		s.logger.Info(message,
			"event_id", event.ID,
			"session_id", event.SessionID,
			"turn_id", payloadString(event.Payload, "turn_id"),
			"event_seq", event.Seq,
			"event_type", event.Type,
		)
	}
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}

	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return value
}

func (s *Server) listSessionEvents(w http.ResponseWriter, r *http.Request) {
	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	events, err := s.store.ListEvents(r.PathValue("session_id"), afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) streamSessionEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	sessionID := r.PathValue("session_id")
	// SSE 先用 after_seq 补历史，再订阅未来事件，支持断线续传。
	history, err := s.store.ListEvents(sessionID, afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	events, cancel, err := s.store.SubscribeEvents(sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cancel()
	s.logger.Info("sse stream opened",
		"session_id", sessionID,
		"after_seq", afterSeq,
		"history_events", len(history),
	)
	defer s.logger.Info("sse stream closed",
		"session_id", sessionID,
		"after_seq", afterSeq,
	)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for _, event := range history {
		if err := writeSSE(w, event); err != nil {
			return
		}
		flusher.Flush()
	}

	fmt.Fprint(w, ": stream ready\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Seq <= afterSeq {
				continue
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func parseAfterSeq(r *http.Request) (int64, error) {
	value := r.URL.Query().Get("after_seq")
	if value == "" {
		return 0, nil
	}

	return strconv.ParseInt(value, 10, 64)
}

func writeSSE(w http.ResponseWriter, event managedagents.Event) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, encoded)
	return err
}
