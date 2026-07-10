package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workerselect"
)

const maxArtifactUploadBytes = 64 << 20

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

type traceLookupResult struct {
	Session managedagents.Session
	Trace   observability.TurnTrace
}

type traceSpanDetailResponse struct {
	SessionID    string                       `json:"session_id"`
	TurnID       string                       `json:"turn_id"`
	TraceID      string                       `json:"trace_id"`
	SessionTitle string                       `json:"session_title,omitempty"`
	Span         observability.TraceSpan      `json:"span"`
	TraceStats   observability.TurnTraceStats `json:"trace_stats,omitempty"`
}

type sessionRuntimeSettingsRequest struct {
	InterventionMode  *string `json:"intervention_mode"`
	ToolRuntime       *string `json:"tool_runtime"`
	CloudSandboxRoot  *string `json:"cloud_sandbox_root"`
	CloudSandboxImage *string `json:"cloud_sandbox_image"`
	AllowNetwork      *bool   `json:"cloud_sandbox_allow_network"`
}

type sessionConfigUpgradeRequest struct {
	ToCurrent *bool  `json:"to_current"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

type interventionDecisionRequest struct {
	Reason string `json:"reason,omitempty"`
}

type workerDiagnoseRequest struct {
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	ProtocolVersion string          `json:"protocol_version,omitempty"`
	Namespace       string          `json:"namespace"`
	API             string          `json:"api"`
	Capabilities    []string        `json:"capabilities,omitempty"`
	Risk            string          `json:"risk,omitempty"`
	Runtime         string          `json:"runtime,omitempty"`
	Input           json.RawMessage `json:"input,omitempty"`
}

type workerDiagnoseResponse struct {
	Invocation  tools.WorkInvocation    `json:"invocation"`
	Matches     int                     `json:"matches"`
	Diagnostics []workerDiagnosisResult `json:"diagnostics"`
}

type workerWorkConflictResponse struct {
	Error string `json:"error"`
	workerDiagnoseResponse
}

type workerWorkDiagnoseResponse struct {
	Work    managedagents.WorkerWork `json:"work"`
	Worker  *workerSummary           `json:"worker,omitempty"`
	Reasons []string                 `json:"reasons,omitempty"`
	Actions []string                 `json:"actions,omitempty"`
}

type workerSummary struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspace_id"`
	Name           string  `json:"name"`
	WorkerType     string  `json:"worker_type"`
	Status         string  `json:"status"`
	LeaseExpiresAt *string `json:"lease_expires_at,omitempty"`
	LastSeenAt     *string `json:"last_seen_at,omitempty"`
}

type workerDiagnosisResult struct {
	WorkerID     string   `json:"worker_id"`
	WorkspaceID  string   `json:"workspace_id"`
	Name         string   `json:"name"`
	WorkerType   string   `json:"worker_type"`
	Status       string   `json:"status"`
	Match        bool     `json:"match"`
	Reasons      []string `json:"reasons,omitempty"`
	Runtimes     []string `json:"runtimes,omitempty"`
	APIs         []string `json:"apis,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	LeaseExpires *string  `json:"lease_expires_at,omitempty"`
	LastSeen     *string  `json:"last_seen_at,omitempty"`
	RegisteredBy string   `json:"registered_by,omitempty"`
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

func (s *Server) getSessionTrace(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListEvents(r.PathValue("session_id"), 0)
	if err != nil {
		writeError(w, err)
		return
	}
	trace := observability.ProjectTurnTrace(r.PathValue("session_id"), r.URL.Query().Get("turn_id"), events)
	if trace.TurnID == "" || len(trace.Steps) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace not found"})
		return
	}
	s.writeTraceFormat(w, r, trace)
}

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request) {
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid limit: %v", managedagents.ErrInvalid, err))
		return
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	sessions, eventsBySession, err := s.recentSessionEvents(r, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"traces": observability.BuildTraceCatalog(sessions, eventsBySession, limit),
	})
}

func (s *Server) listSpans(w http.ResponseWriter, r *http.Request) {
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid limit: %v", managedagents.ErrInvalid, err))
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	minDuration, err := optionalPositiveInt(r.URL.Query().Get("min_duration_ms"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid min_duration_ms: %v", managedagents.ErrInvalid, err))
		return
	}
	maxDuration, err := optionalPositiveInt(r.URL.Query().Get("max_duration_ms"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid max_duration_ms: %v", managedagents.ErrInvalid, err))
		return
	}
	minSelfDuration, err := optionalPositiveInt(r.URL.Query().Get("min_self_duration_ms"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid min_self_duration_ms: %v", managedagents.ErrInvalid, err))
		return
	}
	critical, err := optionalBool(r.URL.Query().Get("critical"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid critical: %v", managedagents.ErrInvalid, err))
		return
	}
	sessions, eventsBySession, err := s.recentSessionEvents(r, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, observability.BuildTraceSpanCatalog(sessions, eventsBySession, observability.TraceSpanCatalogFilter{
		TraceID:               r.URL.Query().Get("trace_id"),
		SessionID:             r.URL.Query().Get("session_id"),
		TurnID:                r.URL.Query().Get("turn_id"),
		Kind:                  r.URL.Query().Get("kind"),
		Status:                r.URL.Query().Get("status"),
		Query:                 r.URL.Query().Get("q"),
		Critical:              critical,
		MinDurationMillis:     int64(minDuration),
		MaxDurationMillis:     int64(maxDuration),
		MinSelfDurationMillis: int64(minSelfDuration),
		Limit:                 limit,
	}))
}

func (s *Server) recentSessionEvents(r *http.Request, limit int) ([]managedagents.Session, map[string][]managedagents.Event, error) {
	sessions, err := s.store.ListSessions(managedagents.ListSessionsInput{
		WorkspaceID:     r.URL.Query().Get("workspace_id"),
		Status:          r.URL.Query().Get("session_status"),
		IncludeArchived: strings.EqualFold(r.URL.Query().Get("include_archived"), "true") || r.URL.Query().Get("include_archived") == "1",
		Limit:           limit,
	})
	if err != nil {
		return nil, nil, err
	}
	eventsBySession := make(map[string][]managedagents.Event, len(sessions))
	for _, session := range sessions {
		events, err := s.store.ListEvents(session.ID, 0)
		if err != nil {
			return nil, nil, err
		}
		eventsBySession[session.ID] = events
	}
	return sessions, eventsBySession, nil
}

func (s *Server) getTrace(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("trace_id"))
	if traceID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace not found"})
		return
	}
	limit, err := traceSearchLimit(r)
	if err != nil {
		writeError(w, err)
		return
	}
	lookup, err := s.findTraceByID(traceID, limit)
	if err != nil {
		if errors.Is(err, managedagents.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace not found"})
			return
		}
		writeError(w, err)
		return
	}
	s.writeTraceFormat(w, r, lookup.Trace)
}

func (s *Server) getTraceSpan(w http.ResponseWriter, r *http.Request) {
	traceID := strings.TrimSpace(r.PathValue("trace_id"))
	spanID := strings.TrimSpace(r.PathValue("span_id"))
	if traceID == "" || spanID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "span not found"})
		return
	}
	limit, err := traceSearchLimit(r)
	if err != nil {
		writeError(w, err)
		return
	}
	lookup, err := s.findTraceByID(traceID, limit)
	if err != nil {
		if errors.Is(err, managedagents.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "span not found"})
			return
		}
		writeError(w, err)
		return
	}
	for _, span := range lookup.Trace.Spans {
		if span.SpanID != spanID {
			continue
		}
		writeJSON(w, http.StatusOK, traceSpanDetailResponse{
			SessionID:    lookup.Trace.SessionID,
			TurnID:       lookup.Trace.TurnID,
			TraceID:      lookup.Trace.TraceID,
			SessionTitle: lookup.Session.Title,
			Span:         span,
			TraceStats:   lookup.Trace.Stats,
		})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "span not found"})
}

func traceSearchLimit(r *http.Request) (int, error) {
	limit, err := optionalPositiveInt(r.URL.Query().Get("search_limit"))
	if err != nil {
		return 0, fmt.Errorf("%w: invalid search_limit: %v", managedagents.ErrInvalid, err)
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return limit, nil
}

func (s *Server) findTraceByID(traceID string, limit int) (traceLookupResult, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return traceLookupResult{}, managedagents.ErrNotFound
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	sessions, err := s.store.ListSessions(managedagents.ListSessionsInput{IncludeArchived: true, Limit: limit})
	if err != nil {
		return traceLookupResult{}, err
	}
	for _, session := range sessions {
		events, err := s.store.ListEvents(session.ID, 0)
		if err != nil {
			return traceLookupResult{}, err
		}
		for _, turn := range observability.BuildTurnCatalog(session.ID, events) {
			if observability.TraceIDForTurn(session.ID, turn.TurnID) != traceID {
				continue
			}
			trace := observability.ProjectTurnTrace(session.ID, turn.TurnID, events)
			if trace.TurnID == "" || len(trace.Steps) == 0 {
				break
			}
			return traceLookupResult{
				Session: session,
				Trace:   trace,
			}, nil
		}
	}
	return traceLookupResult{}, managedagents.ErrNotFound
}

func (s *Server) writeTraceFormat(w http.ResponseWriter, r *http.Request, trace observability.TurnTrace) {
	switch strings.TrimSpace(strings.ToLower(r.URL.Query().Get("format"))) {
	case "", "json", "trace":
		writeJSON(w, http.StatusOK, trace)
	case "perfetto":
		writeJSON(w, http.StatusOK, observability.ExportPerfetto(trace))
	case "otel", "otlp":
		writeJSON(w, http.StatusOK, observability.ExportOTel(trace))
	default:
		writeError(w, fmt.Errorf("%w: unsupported trace format %q", managedagents.ErrInvalid, r.URL.Query().Get("format")))
	}
}

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	usage, err := s.store.ListLLMUsage(managedagents.ListLLMUsageInput{
		WorkspaceID: query.Get("workspace_id"),
		GroupBy:     managedagents.LLMUsageGroupByProviderModel,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: query.Get("workspace_id"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	snapshot := observability.MetricsSnapshot{
		Usage:         usage,
		Workers:       workers,
		Observability: s.observabilityStatus(),
	}
	if sessionID := strings.TrimSpace(query.Get("session_id")); sessionID != "" {
		events, err := s.store.ListEvents(sessionID, 0)
		if err != nil {
			writeError(w, err)
			return
		}
		trace := observability.ProjectTurnTrace(sessionID, query.Get("turn_id"), events)
		interventions, err := s.store.ListSessionInterventions(sessionID, "")
		if err != nil {
			writeError(w, err)
			return
		}
		snapshot.Trace = &trace
		snapshot.Events = events
		snapshot.Interventions = interventions
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(observability.PrometheusText(snapshot))); err != nil {
		s.logger.Warn("metrics response write failed", "error", err)
	}
}

func (s *Server) getInspector(w http.ResponseWriter, r *http.Request) {
	content, err := inspectorAssets.ReadFile("inspector/index.html")
	if err != nil {
		s.logger.Error("inspector index read failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "inspector unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err != nil {
		s.logger.Warn("inspector response write failed", "error", err)
	}
}

func (s *Server) getObservabilityStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.observabilityStatus())
}

func (s *Server) retryObservabilityExporters(w http.ResponseWriter, r *http.Request) {
	result, err := observability.RetryFailedExporterRunsFromEnv(s.store)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) observabilityStatus() observability.Status {
	runs, err := s.store.ListObservabilityExporterRuns(managedagents.ListObservabilityExporterRunsInput{Limit: 20})
	if err != nil {
		s.logger.Warn("list observability exporter runs failed", "error", err)
		return observability.StatusFromEnv()
	}
	return observability.StatusFromEnvWithRuns(runs)
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

func (s *Server) registerWorker(w http.ResponseWriter, r *http.Request) {
	var input managedagents.RegisterWorkerInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	worker, err := s.store.RegisterWorker(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, worker)
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := s.store.GetWorker(r.PathValue("worker_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: r.URL.Query().Get("workspace_id"),
		Status:      r.URL.Query().Get("status"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": workers})
}

func (s *Server) reapExpiredWorkers(w http.ResponseWriter, r *http.Request) {
	var input managedagents.ReapExpiredWorkersInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	expired, err := s.store.ReapExpiredWorkers(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(expired),
		"expired": expired,
	})
}

func (s *Server) diagnoseWorkers(w http.ResponseWriter, r *http.Request) {
	var request workerDiagnoseRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(request.Input) == 0 {
		request.Input = json.RawMessage(`{}`)
	}
	invocation := tools.WorkInvocation{
		ProtocolVersion: request.ProtocolVersion,
		Namespace:       request.Namespace,
		API:             request.API,
		Capabilities:    request.Capabilities,
		Risk:            request.Risk,
		Runtime:         request.Runtime,
		Input:           request.Input,
	}
	if strings.TrimSpace(invocation.ProtocolVersion) == "" {
		invocation.ProtocolVersion = tools.WorkProtocolVersion
	}
	if strings.TrimSpace(invocation.Runtime) == "" {
		invocation.Runtime = tools.ToolRuntimeAuto
	}
	if err := tools.ValidateWorkInvocation(invocation); err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	workspaceID := request.WorkspaceID
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: workspaceID,
		Status:      managedagents.WorkerStatusOnline,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildWorkerDiagnoseResponse(invocation, workers, time.Now().UTC()))
}

func (s *Server) heartbeatWorker(w http.ResponseWriter, r *http.Request) {
	var input managedagents.WorkerHeartbeatInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	worker, err := s.store.HeartbeatWorker(r.PathValue("worker_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) archiveWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := s.store.ArchiveWorker(r.PathValue("worker_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) enqueueWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.EnqueueWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	invocation, err := validateWorkerWorkPayload(input)
	if err != nil {
		writeError(w, err)
		return
	}
	if input.WorkerID == "" && invocation != nil {
		workerID, err := workerselect.Selector{Store: s.store}.SelectWorkerID(workerselect.Request{
			WorkspaceID: input.WorkspaceID,
			Invocation:  *invocation,
		})
		if err != nil {
			if errors.Is(err, managedagents.ErrConflict) {
				response, diagnoseErr := s.workerWorkConflictResponse(input.WorkspaceID, *invocation, err)
				if diagnoseErr != nil {
					writeError(w, diagnoseErr)
					return
				}
				writeJSON(w, http.StatusConflict, response)
				return
			}
			writeError(w, err)
			return
		}
		input.WorkerID = workerID
	}
	work, err := s.store.EnqueueWorkerWork(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, work)
}

func (s *Server) workerWorkConflictResponse(workspaceID string, invocation tools.WorkInvocation, cause error) (workerWorkConflictResponse, error) {
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: workspaceID,
		Status:      managedagents.WorkerStatusOnline,
	})
	if err != nil {
		return workerWorkConflictResponse{}, err
	}
	return workerWorkConflictResponse{
		Error:                  cause.Error(),
		workerDiagnoseResponse: buildWorkerDiagnoseResponse(invocation, workers, time.Now().UTC()),
	}, nil
}

func buildWorkerDiagnoseResponse(invocation tools.WorkInvocation, workers []managedagents.Worker, now time.Time) workerDiagnoseResponse {
	diagnostics := workerselect.DiagnoseInvocation(workers, invocation, now)
	response := workerDiagnoseResponse{Invocation: invocation}
	for _, diagnosis := range diagnostics {
		result := workerDiagnosisResult{
			WorkerID:     diagnosis.Worker.ID,
			WorkspaceID:  diagnosis.Worker.WorkspaceID,
			Name:         diagnosis.Worker.Name,
			WorkerType:   diagnosis.Worker.WorkerType,
			Status:       diagnosis.Worker.Status,
			Match:        diagnosis.Match,
			Reasons:      diagnosis.Reasons,
			Runtimes:     diagnosis.Capabilities.Runtimes,
			APIs:         diagnosis.Capabilities.APIs,
			Capabilities: diagnosis.Capabilities.Capabilities,
			RegisteredBy: diagnosis.Worker.RegisteredBy,
		}
		if diagnosis.Worker.LeaseExpiresAt != nil {
			formatted := diagnosis.Worker.LeaseExpiresAt.UTC().Format(time.RFC3339)
			result.LeaseExpires = &formatted
		}
		if diagnosis.Worker.LastSeenAt != nil {
			formatted := diagnosis.Worker.LastSeenAt.UTC().Format(time.RFC3339)
			result.LastSeen = &formatted
		}
		if diagnosis.Match {
			response.Matches++
		}
		response.Diagnostics = append(response.Diagnostics, result)
	}
	return response
}

func (s *Server) getWorkerWork(w http.ResponseWriter, r *http.Request) {
	work, err := s.store.GetWorkerWork(r.PathValue("work_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) reapExpiredWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.ReapExpiredWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	expired, err := s.store.ReapExpiredWorkerWork(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(expired),
		"expired": expired,
	})
}

func (s *Server) diagnoseWorkerWork(w http.ResponseWriter, r *http.Request) {
	work, err := s.store.GetWorkerWork(r.PathValue("work_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	response := diagnoseWorkerWorkState(s.store, work, time.Now().UTC())
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) cancelWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CancelWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	work, err := s.store.CancelWorkerWork(r.PathValue("work_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func diagnoseWorkerWorkState(store managedagents.Store, work managedagents.WorkerWork, now time.Time) workerWorkDiagnoseResponse {
	response := workerWorkDiagnoseResponse{Work: work}
	if strings.TrimSpace(work.WorkerID) != "" {
		worker, err := store.GetWorker(work.WorkerID)
		if err != nil {
			response.Reasons = append(response.Reasons, "assigned worker not found")
		} else {
			response.Worker = summarizeWorker(worker)
			if worker.Status != managedagents.WorkerStatusOnline {
				response.Reasons = append(response.Reasons, "assigned worker status is "+worker.Status)
			}
			if worker.LeaseExpiresAt != nil && worker.LeaseExpiresAt.Before(now) {
				response.Reasons = append(response.Reasons, "assigned worker lease expired at "+worker.LeaseExpiresAt.UTC().Format(time.RFC3339))
			}
		}
	}
	switch work.Status {
	case managedagents.WorkerWorkStatusPending:
		if strings.TrimSpace(work.WorkerID) == "" {
			response.Reasons = append(response.Reasons, "work is pending without an assigned worker")
			response.Actions = append(response.Actions, "wait for a matching worker to poll, or enqueue with --worker for a specific worker")
		} else {
			response.Reasons = append(response.Reasons, "work is pending for assigned worker "+work.WorkerID)
			response.Actions = append(response.Actions, "ensure the worker is online and polling")
		}
	case managedagents.WorkerWorkStatusLeased:
		response.Reasons = append(response.Reasons, "work is leased but not acknowledged")
		response.Actions = append(response.Actions, "worker should ack or complete the work")
	case managedagents.WorkerWorkStatusRunning:
		response.Reasons = append(response.Reasons, "work is running")
		response.Actions = append(response.Actions, "worker should heartbeat while running and submit result when complete")
	case managedagents.WorkerWorkStatusCompleted:
		response.Reasons = append(response.Reasons, "work completed successfully")
	case managedagents.WorkerWorkStatusFailed:
		response.Reasons = append(response.Reasons, "work failed")
	case managedagents.WorkerWorkStatusCanceled:
		response.Reasons = append(response.Reasons, "work was canceled")
		response.Actions = append(response.Actions, "no worker result is expected; enqueue a new work item if the operation should be retried")
	default:
		response.Reasons = append(response.Reasons, "work has unknown status "+work.Status)
	}
	if work.Status == managedagents.WorkerWorkStatusLeased || work.Status == managedagents.WorkerWorkStatusRunning {
		if work.LeaseExpiresAt == nil {
			response.Reasons = append(response.Reasons, "work has no lease_expires_at")
			response.Actions = append(response.Actions, "worker should heartbeat, or mark failed if it cannot continue")
		} else if work.LeaseExpiresAt.Before(now) {
			response.Reasons = append(response.Reasons, "work lease expired at "+work.LeaseExpiresAt.UTC().Format(time.RFC3339))
			response.Actions = append(response.Actions, "run: bin/tma work reap-expired")
		} else {
			response.Reasons = append(response.Reasons, "work lease valid until "+work.LeaseExpiresAt.UTC().Format(time.RFC3339))
		}
	}
	return response
}

func summarizeWorker(worker managedagents.Worker) *workerSummary {
	summary := &workerSummary{
		ID:          worker.ID,
		WorkspaceID: worker.WorkspaceID,
		Name:        worker.Name,
		WorkerType:  worker.WorkerType,
		Status:      worker.Status,
	}
	if worker.LeaseExpiresAt != nil {
		formatted := worker.LeaseExpiresAt.UTC().Format(time.RFC3339)
		summary.LeaseExpiresAt = &formatted
	}
	if worker.LastSeenAt != nil {
		formatted := worker.LastSeenAt.UTC().Format(time.RFC3339)
		summary.LastSeenAt = &formatted
	}
	return summary
}

func validateWorkerWorkPayload(input managedagents.EnqueueWorkerWorkInput) (*tools.WorkInvocation, error) {
	workType := strings.TrimSpace(strings.ToLower(input.WorkType))
	if workType == "" {
		workType = managedagents.WorkerWorkTypeToolExecution
	}
	if workType != managedagents.WorkerWorkTypeToolExecution {
		return nil, nil
	}
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(input.Payload, &invocation); err != nil {
		return nil, fmt.Errorf("%w: decode tool_execution work payload: %v", managedagents.ErrInvalid, err)
	}
	if err := tools.ValidateWorkInvocation(invocation); err != nil {
		return nil, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	return &invocation, nil
}

func (s *Server) pollWorkerWork(w http.ResponseWriter, r *http.Request) {
	leaseSeconds, err := optionalPositiveInt(r.URL.Query().Get("lease_seconds"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid lease_seconds: %v", managedagents.ErrInvalid, err))
		return
	}
	work, err := s.store.PollWorkerWork(r.PathValue("worker_id"), managedagents.PollWorkerWorkInput{
		LeaseSeconds: leaseSeconds,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"work": work})
}

func (s *Server) ackWorkerWork(w http.ResponseWriter, r *http.Request) {
	work, err := s.store.AckWorkerWork(r.PathValue("worker_id"), r.PathValue("work_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) heartbeatWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.WorkerWorkHeartbeatInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	work, err := s.store.HeartbeatWorkerWork(r.PathValue("worker_id"), r.PathValue("work_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) completeWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CompleteWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	work, err := s.store.CompleteWorkerWork(r.PathValue("worker_id"), r.PathValue("work_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) createObjectRef(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateObjectRefInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	object, err := s.store.CreateObjectRef(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, object)
}

func (s *Server) getObjectRef(w http.ResponseWriter, r *http.Request) {
	object, err := s.store.GetObjectRef(r.PathValue("object_ref_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) downloadObjectRef(w http.ResponseWriter, r *http.Request) {
	objectRef, err := s.store.GetObjectRef(r.PathValue("object_ref_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	if !s.canDownloadObjectRef(r, objectRef) {
		writeError(w, fmt.Errorf("%w: object download not allowed", managedagents.ErrForbidden))
		return
	}

	object, err := s.objectStore.GetObject(r.Context(), objectstore.GetObjectInput{
		Bucket:  objectRef.Bucket,
		Key:     objectRef.ObjectKey,
		Version: objectRef.ObjectVersion,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	defer object.Body.Close()

	contentType := object.ContentType
	if contentType == "" {
		contentType = objectRef.ContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	filename := objectRef.ObjectKey
	if filename == "" {
		filename = objectRef.ID
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.ETag != "" {
		w.Header().Set("ETag", object.ETag)
	}
	if object.ChecksumSHA256 != "" {
		w.Header().Set("Digest", "sha-256="+object.ChecksumSHA256)
	}

	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Warn("object download copy failed", "object_ref_id", objectRef.ID, "error", err)
	}
}

func (s *Server) deleteObjectRef(w http.ResponseWriter, r *http.Request) {
	objectRefID := r.PathValue("object_ref_id")
	count, err := s.store.CountSessionArtifactsByObjectRef(objectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	if count > 0 {
		writeError(w, fmt.Errorf("%w: object ref is still referenced by %d artifact(s)", managedagents.ErrConflict, count))
		return
	}
	if err := s.store.DeleteObjectRef(objectRefID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSessionArtifact(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSessionArtifact(r.PathValue("session_id"), r.PathValue("artifact_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) canDownloadObjectRef(r *http.Request, objectRef managedagents.ObjectRef) bool {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if objectRef.Visibility == managedagents.ObjectVisibilityWorkspace {
		if sessionID == "" {
			return false
		}
		session, err := s.store.GetSession(sessionID)
		return err == nil && session.WorkspaceID == objectRef.WorkspaceID
	}
	if objectRef.Visibility == managedagents.ObjectVisibilitySession {
		if sessionID == "" {
			return false
		}
		artifacts, err := s.store.ListSessionArtifacts(sessionID)
		if err != nil {
			return false
		}
		for _, artifact := range artifacts {
			if artifact.ObjectRefID == objectRef.ID {
				return true
			}
		}
		return false
	}
	return false
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

func optionalPositiveInt(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("must be non-negative")
	}
	return parsed, nil
}

func optionalBool(value string) (*bool, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "":
		return nil, nil
	case "1", "true", "yes":
		parsed := true
		return &parsed, nil
	case "0", "false", "no":
		parsed := false
		return &parsed, nil
	default:
		return nil, fmt.Errorf("must be true or false")
	}
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

func (s *Server) createSessionArtifact(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateSessionArtifactInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.SessionID = r.PathValue("session_id")
	artifact, err := s.store.CreateSessionArtifact(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, artifact)
}

func (s *Server) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	artifacts, err := s.store.ListSessionArtifacts(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": artifacts})
}

func (s *Server) downloadSessionArtifact(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	artifactID := r.PathValue("artifact_id")

	artifact, err := s.store.GetSessionArtifact(sessionID, artifactID)
	if err != nil {
		writeError(w, err)
		return
	}

	objectRef, err := s.store.GetObjectRef(artifact.ObjectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	if objectRef.WorkspaceID != artifact.WorkspaceID {
		writeError(w, fmt.Errorf("%w: artifact workspace mismatch", managedagents.ErrInvalid))
		return
	}

	object, err := s.objectStore.GetObject(r.Context(), objectstore.GetObjectInput{
		Bucket:  objectRef.Bucket,
		Key:     objectRef.ObjectKey,
		Version: objectRef.ObjectVersion,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	defer object.Body.Close()

	contentType := object.ContentType
	if contentType == "" {
		contentType = objectRef.ContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	filename := artifact.Name
	if filename == "" {
		filename = objectRef.ObjectKey
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.ETag != "" {
		w.Header().Set("ETag", object.ETag)
	}
	if object.ChecksumSHA256 != "" {
		w.Header().Set("Digest", "sha-256="+object.ChecksumSHA256)
	}

	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Warn("artifact download copy failed", "session_id", sessionID, "artifact_id", artifactID, "error", err)
	}
}

func (s *Server) uploadSessionArtifact(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, err := s.store.GetSession(sessionID)
	if err != nil {
		writeError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArtifactUploadBytes+1024)
	if err := r.ParseMultipartForm(maxArtifactUploadBytes); err != nil {
		writeError(w, fmt.Errorf("%w: parse multipart artifact upload: %v", managedagents.ErrInvalid, err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, fmt.Errorf("%w: artifact upload requires file field", managedagents.ErrInvalid))
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, err)
		return
	}
	contentType := fallbackString(r.FormValue("content_type"), header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])

	bucket, err := objectstore.ResolveBucket(r.FormValue("bucket"), s.defaultObjectStoreBucket())
	if err != nil {
		writeError(w, err)
		return
	}
	objectKey := r.FormValue("object_key")
	if objectKey == "" {
		objectKey = defaultUploadObjectKey(session, header.Filename)
	}
	if err := objectstore.ValidateObjectKey(objectKey); err != nil {
		writeError(w, err)
		return
	}

	metadata, err := metadataFromFormValue(r.FormValue("metadata"))
	if err != nil {
		writeError(w, err)
		return
	}
	putResult, err := s.objectStore.PutObject(r.Context(), objectstore.PutObjectInput{
		Bucket:         bucket,
		Key:            objectKey,
		Body:           bytes.NewReader(content),
		ContentType:    contentType,
		SizeBytes:      int64(len(content)),
		ChecksumSHA256: checksumHex,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	objectRef, err := s.store.CreateObjectRef(managedagents.CreateObjectRefInput{
		WorkspaceID:     session.WorkspaceID,
		StorageProvider: managedagents.ObjectStorageProviderS3,
		Bucket:          fallbackString(putResult.Bucket, bucket),
		ObjectKey:       fallbackString(putResult.Key, objectKey),
		ObjectVersion:   putResult.Version,
		ContentType:     contentType,
		SizeBytes:       int64(len(content)),
		ChecksumSHA256:  fallbackString(putResult.ChecksumSHA256, checksumHex),
		ETag:            putResult.ETag,
		Visibility:      fallbackString(r.FormValue("visibility"), managedagents.ObjectVisibilityWorkspace),
		Metadata:        metadata,
		CreatedBy:       fallbackString(r.FormValue("created_by"), "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		name = safeArtifactFileName(header.Filename)
	}
	artifact, err := s.store.CreateSessionArtifact(managedagents.CreateSessionArtifactInput{
		SessionID:     sessionID,
		EnvironmentID: r.FormValue("environment_id"),
		ObjectRefID:   objectRef.ID,
		TurnID:        r.FormValue("turn_id"),
		ToolCallID:    r.FormValue("tool_call_id"),
		Name:          name,
		Description:   r.FormValue("description"),
		ArtifactType:  fallbackString(r.FormValue("artifact_type"), managedagents.ArtifactTypeFile),
		Metadata:      metadata,
		CreatedBy:     fallbackString(r.FormValue("created_by"), "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"object_ref": objectRef,
		"artifact":   artifact,
	})
}

func (s *Server) defaultObjectStoreBucket() string {
	type configuredClient interface {
		Config() objectstore.Config
	}
	if client, ok := s.objectStore.(configuredClient); ok {
		return client.Config().Bucket
	}
	return ""
}

func metadataFromFormValue(value string) (json.RawMessage, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, fmt.Errorf("%w: invalid metadata JSON object: %v", managedagents.ErrInvalid, err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func defaultUploadObjectKey(session managedagents.Session, filename string) string {
	return fmt.Sprintf("%s/%s/uploads/%d-%s", session.WorkspaceID, session.ID, time.Now().UTC().UnixNano(), safeArtifactFileName(filename))
}

func safeArtifactFileName(filename string) string {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		return "artifact"
	}
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	return filename
}

func fallbackString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func contentDispositionAttachment(filename string) string {
	filename = safeArtifactFileName(filename)
	return fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(filename, `"`, "_"))
}

func (s *Server) updateSessionRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	var request sessionRuntimeSettingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	session, err := s.store.GetSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	settings := map[string]any{}
	if len(session.RuntimeSettings) > 0 && string(session.RuntimeSettings) != "null" {
		if err := json.Unmarshal(session.RuntimeSettings, &settings); err != nil {
			writeError(w, fmt.Errorf("%w: existing runtime_settings must be valid JSON", managedagents.ErrInvalid))
			return
		}
	}
	if request.InterventionMode != nil {
		mode, ok := tools.NormalizeInterventionMode(*request.InterventionMode)
		if !ok {
			writeError(w, fmt.Errorf("%w: unsupported intervention_mode %q", managedagents.ErrInvalid, *request.InterventionMode))
			return
		}
		settings["intervention_mode"] = mode
	}
	if request.ToolRuntime != nil {
		runtime, ok := tools.NormalizeToolRuntime(*request.ToolRuntime)
		if !ok {
			writeError(w, fmt.Errorf("%w: unsupported tool_runtime %q", managedagents.ErrInvalid, *request.ToolRuntime))
			return
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
	raw, err := json.Marshal(settings)
	if err != nil {
		writeError(w, err)
		return
	}
	session, err = s.store.UpdateSessionRuntimeSettings(r.PathValue("session_id"), managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: raw,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) upgradeSessionAgentConfig(w http.ResponseWriter, r *http.Request) {
	request := sessionConfigUpgradeRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	toCurrent := true
	if request.ToCurrent != nil {
		toCurrent = *request.ToCurrent
	}
	result, err := s.store.UpgradeSessionAgentConfig(r.PathValue("session_id"), managedagents.UpgradeSessionAgentConfigInput{
		ToCurrent: toCurrent,
		UpdatedBy: request.UpdatedBy,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
		executionResult, events, err := s.buildRejectedInterventionObservation(result.Intervention, request.Reason)
		if err != nil {
			writeError(w, err)
			return
		}
		result.Events = append(result.Events, events...)
		if len(result.Intervention.Continuation) > 0 {
			continuationEvents, err := s.continueIntervention(r, result.Intervention, executionResult, "rejected")
			if err != nil {
				writeError(w, err)
				return
			}
			result.Events = append(result.Events, continuationEvents...)
		} else {
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
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) executeApprovedIntervention(r *http.Request, intervention managedagents.SessionIntervention) (tools.ExecutionResult, []managedagents.Event, error) {
	config, err := s.store.ResolveAgentRuntimeConfig(intervention.SessionID)
	if err != nil {
		return tools.ExecutionResult{}, nil, err
	}
	registry, _, executionContext := s.resolveToolExecution(config, intervention.SessionID, intervention.TurnID)
	executor := tools.RegistryExecutor{Registry: registry}
	startedAt := time.Now()
	executionResult, err := executor.Execute(r.Context(), tools.Call{
		ID:         intervention.CallID,
		Identifier: intervention.ToolIdentifier,
		APIName:    intervention.APIName,
		Arguments:  intervention.Arguments,
	}, executionContext)
	if err != nil {
		return tools.ExecutionResult{}, nil, err
	}
	duration := time.Since(startedAt)
	data := map[string]any{
		"id":                   intervention.CallID,
		"identifier":           intervention.ToolIdentifier,
		"api_name":             intervention.APIName,
		"duration_ms":          duration.Milliseconds(),
		"content":              executionResult.Content,
		"state":                rawJSONValue(executionResult.State),
		"artifacts":            executionResult.Artifacts,
		"artifact_error":       executionResult.ArtifactError,
		"pending_intervention": executionResult.PendingIntervention,
		"error":                executionResult.Error,
		"success":              executionResult.Error == nil,
		"approval_source":      "user",
	}
	fields := observability.EventTraceFields(observability.EventTraceFieldsInput{
		SessionID:    intervention.SessionID,
		TurnID:       intervention.TurnID,
		EventType:    managedagents.EventRuntimeToolResult,
		CallID:       intervention.CallID,
		Identifier:   intervention.ToolIdentifier,
		APIName:      intervention.APIName,
		Status:       spanStatusForHTTPRuntimeEvent(managedagents.EventRuntimeToolResult, data),
		Duration:     duration,
		ParentSpanID: observability.InteractionSpanID(intervention.TurnID),
	})
	for key, value := range fields {
		data[key] = value
	}

	payload, err := json.Marshal(map[string]any{
		"turn_id":        intervention.TurnID,
		"trace_id":       data["trace_id"],
		"span_id":        data["span_id"],
		"parent_span_id": data["parent_span_id"],
		"span_name":      data["span_name"],
		"span_kind":      data["span_kind"],
		"span_status":    data["span_status"],
		"duration_ms":    data["duration_ms"],
		"message":        "Received approved tool result.",
		"data":           data,
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
	return s.continueIntervention(r, intervention, executionResult, "approved")
}

func (s *Server) continueIntervention(r *http.Request, intervention managedagents.SessionIntervention, executionResult tools.ExecutionResult, action string) ([]managedagents.Event, error) {
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
	registry, _, executionContext := s.resolveToolExecution(config, intervention.SessionID, intervention.TurnID)
	policy := tools.InterventionPolicy{Mode: tools.ParseInterventionMode(config.RuntimeSettings)}
	executor := tools.RegistryExecutor{Registry: registry}

	var allEvents []managedagents.Event
	for round := intervention.ContinuationRound + 1; round < 4; round++ {
		requestEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeLLMRequest, intervention.TurnID, "Resuming LLM after "+action+" tool result.", map[string]any{
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
		llmDuration := time.Since(startedAt)
		s.recordContinuationUsage(intervention, config, llmResponse.Usage, llmDuration, "completed", "")

		responseEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeLLMResponse, intervention.TurnID, "Received resumed LLM response.", map[string]any{
			"role":          llmResponse.Message.Role,
			"content_count": len(llmResponse.Message.Content),
			"usage":         llmResponse.Usage,
			"tool_round":    round,
			"duration_ms":   llmDuration.Milliseconds(),
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
				decision := policy.EvaluateCall(manifest, api, call, executionContext)
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

			toolStartedAt := time.Now()
			result, err := executor.Execute(r.Context(), call, executionContext)
			if err != nil {
				return allEvents, err
			}
			resultEvents, err := s.appendToolResultEvent(intervention.SessionID, intervention.TurnID, call, result, "Received continuation tool result.", time.Since(toolStartedAt))
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

func (s *Server) resolveToolExecution(config managedagents.AgentRuntimeConfig, sessionID string, turnID string) (tools.Registry, tools.ConfigPolicy, tools.ExecutionContext) {
	resolved := execution.ResolveToolExecution(execution.ToolExecutionRequest{
		Config:           config,
		SessionID:        sessionID,
		TurnID:           turnID,
		ProviderResolver: s.executionResolver,
		Store:            s.store,
	})
	return resolved.Registry, resolved.Policy, resolved.Context
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
	if data == nil {
		data = map[string]any{}
	}
	callID, _ := data["id"].(string)
	identifier, _ := data["identifier"].(string)
	apiName, _ := data["api_name"].(string)
	fields := observability.EventTraceFields(observability.EventTraceFieldsInput{
		SessionID:       sessionID,
		TurnID:          turnID,
		EventType:       eventType,
		CallID:          fallbackString(callID, fmt.Sprintf("%v", data["tool_round"])),
		Identifier:      identifier,
		APIName:         apiName,
		Status:          spanStatusForHTTPRuntimeEvent(eventType, data),
		Duration:        durationFromData(data),
		ParentSpanID:    parentSpanForHTTPRuntimeEvent(turnID, eventType, callID),
		InteractionRoot: eventType == managedagents.EventRuntimeStarted,
	})
	for key, value := range fields {
		data[key] = value
	}
	payload, err := json.Marshal(map[string]any{
		"turn_id":        turnID,
		"trace_id":       data["trace_id"],
		"span_id":        data["span_id"],
		"parent_span_id": data["parent_span_id"],
		"span_name":      data["span_name"],
		"span_kind":      data["span_kind"],
		"span_status":    data["span_status"],
		"duration_ms":    data["duration_ms"],
		"message":        message,
		"data":           data,
	})
	if err != nil {
		return nil, err
	}
	return s.store.AppendEvents(sessionID, []managedagents.AppendEventInput{{
		Type:    eventType,
		Payload: payload,
	}})
}

func (s *Server) appendToolResultEvent(sessionID string, turnID string, call tools.Call, executionResult tools.ExecutionResult, message string, duration time.Duration) ([]managedagents.Event, error) {
	return s.appendRuntimeEvent(sessionID, managedagents.EventRuntimeToolResult, turnID, message, map[string]any{
		"id":                   call.ID,
		"identifier":           call.Identifier,
		"api_name":             call.APIName,
		"duration_ms":          duration.Milliseconds(),
		"content":              executionResult.Content,
		"state":                rawJSONValue(executionResult.State),
		"artifacts":            executionResult.Artifacts,
		"artifact_error":       executionResult.ArtifactError,
		"pending_intervention": executionResult.PendingIntervention,
		"error":                executionResult.Error,
		"success":              executionResult.Error == nil,
	})
}

func spanStatusForHTTPRuntimeEvent(eventType string, data map[string]any) string {
	switch eventType {
	case managedagents.EventRuntimeCompleted, managedagents.EventRuntimeLLMResponse:
		return "ok"
	case managedagents.EventRuntimeFailed, managedagents.EventRuntimeContextCompactionFailed:
		return "error"
	case managedagents.EventRuntimeToolResult:
		if success, ok := data["success"].(bool); ok && success {
			return "ok"
		}
		return "error"
	case managedagents.EventRuntimeToolInterventionApproved:
		return "approved"
	case managedagents.EventRuntimeToolInterventionRejected:
		return "rejected"
	case managedagents.EventRuntimeToolInterventionRequired:
		return "waiting"
	default:
		return "point"
	}
}

func parentSpanForHTTPRuntimeEvent(turnID string, eventType string, callID string) string {
	switch eventType {
	case managedagents.EventRuntimeStarted:
		return ""
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected:
		return observability.ToolSpanID(turnID, callID, 0)
	default:
		return observability.InteractionSpanID(turnID)
	}
}

func durationFromData(data map[string]any) time.Duration {
	switch value := data["duration_ms"].(type) {
	case int64:
		return time.Duration(value) * time.Millisecond
	case int:
		return time.Duration(value) * time.Millisecond
	case float64:
		return time.Duration(int64(value)) * time.Millisecond
	default:
		return 0
	}
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
	allEvents := append(completedEvents, turnEvents...)
	if err := observability.RefreshSessionSummary(s.store, intervention.SessionID, intervention.TurnID); err != nil {
		s.logger.Warn("refresh session summary failed after continuation",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"error", err,
		)
	}
	if result, err := observability.ExportTurnTraceFromEnv(s.store, intervention.SessionID, intervention.TurnID); err != nil {
		s.logger.Warn("observability export failed after continuation",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"error", err,
		)
	} else if !result.Skipped {
		s.logger.Info("observability export completed after continuation",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"trace_id", result.TraceID,
			"perfetto_path", exporterPerfettoPath(result),
			"otlp_endpoint", exporterOTLPEndpoint(result),
		)
	}
	return allEvents, nil
}

func exporterPerfettoPath(result observability.ExporterResult) string {
	if result.Perfetto == nil {
		return ""
	}
	return result.Perfetto.Path
}

func exporterOTLPEndpoint(result observability.ExporterResult) string {
	if result.OTLPPush == nil {
		return ""
	}
	return result.OTLPPush.Endpoint
}

func (s *Server) buildRejectedInterventionObservation(intervention managedagents.SessionIntervention, decisionReason string) (tools.ExecutionResult, []managedagents.Event, error) {
	message := "Tool call rejected by user."
	if decisionReason != "" {
		message += " Reason: " + decisionReason
	}
	result := tools.ExecutionResult{
		ID:         intervention.CallID,
		Identifier: intervention.ToolIdentifier,
		APIName:    intervention.APIName,
		Content:    message,
		State: mustJSONMarshal(map[string]any{
			"rejected":        true,
			"decision_reason": decisionReason,
		}),
		Error: &tools.ExecutionError{
			Type:    "tool_rejected_by_user",
			Message: message,
		},
	}
	events, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolResult, intervention.TurnID, "Recorded rejected tool result for model continuation.", map[string]any{
		"id":                   intervention.CallID,
		"identifier":           intervention.ToolIdentifier,
		"api_name":             intervention.APIName,
		"content":              result.Content,
		"state":                rawJSONValue(result.State),
		"pending_intervention": false,
		"error":                result.Error,
		"success":              false,
		"approval_source":      "user",
		"decision_reason":      decisionReason,
	})
	return result, events, err
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

func mustJSONMarshal(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
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
