package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillretention"
	"tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workerselect"
)

const maxArtifactUploadBytes = 64 << 20

type appendEventsRequest struct {
	Events       []managedagents.AppendEventInput `json:"events"`
	PreferLatest bool                             `json:"prefer_latest,omitempty"`
}

type llmProviderRequest struct {
	ID           string  `json:"id"`
	ProviderType *string `json:"provider_type"`
	BaseURL      *string `json:"base_url"`
	APIKeyEnv    *string `json:"api_key_env"`
	Enabled      *bool   `json:"enabled"`
}

type llmModelRequest struct {
	ProviderID          string                              `json:"provider_id"`
	Model               string                              `json:"model"`
	ContextWindowTokens int                                 `json:"context_window_tokens"`
	CapabilityType      string                              `json:"capability_type"`
	Capabilities        *managedagents.LLMModelCapabilities `json:"capabilities"`
	IsDefaultVision     *bool                               `json:"is_default_vision"`
	IsDefaultEmbedding  *bool                               `json:"is_default_embedding"`
	IsDefaultReranker   *bool                               `json:"is_default_reranker"`
}

type agentConfigVersionRequest struct {
	LLMProvider *string          `json:"llm_provider"`
	LLMModel    *string          `json:"llm_model"`
	Model       *string          `json:"model"`
	System      *string          `json:"system"`
	Tools       *json.RawMessage `json:"tools"`
	MCP         *json.RawMessage `json:"mcp"`
	Skills      *json.RawMessage `json:"skills"`
}

type agentUpdateRequest struct {
	Name        *string          `json:"name"`
	LLMProvider *string          `json:"llm_provider"`
	LLMModel    *string          `json:"llm_model"`
	Model       *string          `json:"model"`
	System      *string          `json:"system"`
	Tools       *json.RawMessage `json:"tools"`
	MCP         *json.RawMessage `json:"mcp"`
	Skills      *json.RawMessage `json:"skills"`
}

type agentConfigRollbackResponse struct {
	Agent           managedagents.Agent `json:"agent"`
	PreviousVersion int                 `json:"previous_version"`
	SourceVersion   int                 `json:"source_version"`
	NewVersion      int                 `json:"new_version"`
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
	ExpectedRevision                   int64                   `json:"-"`
	LLMProvider                        *string                 `json:"llm_provider"`
	LLMModel                           *string                 `json:"llm_model"`
	Model                              *string                 `json:"model"`
	InterventionMode                   *string                 `json:"intervention_mode"`
	PermissionRules                    *[]tools.PermissionRule `json:"permission_rules"`
	ToolRuntime                        *string                 `json:"tool_runtime"`
	CloudSandboxRoot                   *string                 `json:"cloud_sandbox_root"`
	CloudSandboxImage                  *string                 `json:"cloud_sandbox_image"`
	AllowNetwork                       *bool                   `json:"cloud_sandbox_allow_network"`
	AgentConfigUpdatePolicy            *string                 `json:"agent_config_update_policy"`
	AgentCoreCompactionThresholdTokens *int                    `json:"agent_core_compaction_threshold_tokens"`
	AgentCoreCompactionSummaryMaxChars *int                    `json:"agent_core_compaction_summary_max_chars"`
	AgentCoreBudget                    *struct {
		MaxRounds          *int   `json:"max_rounds"`
		MaxModelCalls      *int   `json:"max_model_calls"`
		MaxToolCalls       *int   `json:"max_tool_calls"`
		MaxInputTokens     *int64 `json:"max_input_tokens"`
		MaxOutputTokens    *int64 `json:"max_output_tokens"`
		MaxReasoningTokens *int64 `json:"max_reasoning_tokens"`
		MaxCostMicros      *int64 `json:"max_cost_micros"`
	} `json:"agent_core_budget"`
	HumanInteraction *struct {
		Enabled        *bool    `json:"enabled"`
		Modes          []string `json:"modes,omitempty"`
		SupportsUpload *bool    `json:"supports_upload,omitempty"`
		Fallback       *string  `json:"fallback,omitempty"`
	} `json:"human_interaction"`
	CompletionGate *struct {
		MaxRetries *int `json:"max_retries"`
	} `json:"completion_gate"`
}

type sessionRuntimeCapabilitiesResponse struct {
	DefaultRuntime    string                               `json:"default_runtime"`
	AvailableRuntimes []string                             `json:"available_runtimes"`
	HumanInteraction  humanInteractionCapabilitiesResponse `json:"human_interaction"`
}

type humanInteractionCapabilitiesResponse struct {
	Enabled        bool     `json:"enabled"`
	Modes          []string `json:"modes"`
	SupportsUpload bool     `json:"supports_upload"`
	Fallback       string   `json:"fallback"`
}

type sessionConfigUpgradeRequest struct {
	ToCurrent *bool  `json:"to_current"`
	ToVersion int    `json:"to_version,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

type interventionDecisionRequest struct {
	Reason   string          `json:"reason,omitempty"`
	Response json.RawMessage `json:"response,omitempty"`
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
	writeJSON(w, http.StatusOK, map[string]any{"providers": nonNilSlice(providers)})
}

func (s *Server) createLLMProvider(w http.ResponseWriter, r *http.Request) {
	var request llmProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		s.recordLLMControlAudit(r, "llm.provider.create", "llm_provider", "", nil, nil, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resourceID := strings.TrimSpace(request.ID)
	var before any
	if existing, err := s.store.GetLLMProvider(resourceID); err == nil {
		before = llmProviderAuditSnapshot(existing)
		conflictErr := fmt.Errorf("%w: llm provider %s already exists", managedagents.ErrConflict, resourceID)
		s.recordLLMControlAudit(r, "llm.provider.create", "llm_provider", resourceID, before, nil, conflictErr)
		writeError(w, conflictErr)
		return
	} else if !errors.Is(err, managedagents.ErrNotFound) {
		s.recordLLMControlAudit(r, "llm.provider.create", "llm_provider", resourceID, nil, nil, err)
		writeError(w, err)
		return
	}

	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	input := managedagents.UpsertLLMProviderInput{
		ID:           request.ID,
		ProviderType: stringValue(request.ProviderType),
		BaseURL:      stringValue(request.BaseURL),
		APIKeyEnv:    stringValue(request.APIKeyEnv),
		Enabled:      enabled,
	}
	provider, atomicAudit, err := s.createLLMProviderWithAudit(r, input, "llm.provider.create", resourceID)
	var after any
	if err == nil {
		after = llmProviderAuditSnapshot(provider)
	}
	if !atomicAudit || err != nil {
		s.recordLLMControlAudit(r, "llm.provider.create", "llm_provider", resourceID, before, after, err)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	setLLMProviderETag(w, provider)
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) getLLMProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.store.GetLLMProvider(r.PathValue("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	setLLMProviderETag(w, provider)
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) updateLLMProvider(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("provider_id"))
	existing, err := s.store.GetLLMProvider(resourceID)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.provider.update", "llm_provider", resourceID, nil, nil, err)
		writeError(w, err)
		return
	}
	before := llmProviderAuditSnapshot(existing)
	expectedRevision, err := parseLLMProviderIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.recordLLMControlAudit(r, "llm.provider.update", "llm_provider", resourceID, before, nil, err)
		status := http.StatusBadRequest
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if expectedRevision != existing.Revision {
		err := fmt.Errorf("%w: llm provider %s revision changed from %d to %d", managedagents.ErrRevisionConflict, resourceID, expectedRevision, existing.Revision)
		s.recordLLMControlAudit(r, "llm.provider.update", "llm_provider", resourceID, before, nil, err)
		writeError(w, err)
		return
	}

	var request llmProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		s.recordLLMControlAudit(r, "llm.provider.update", "llm_provider", resourceID, before, nil, err)
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

	input := managedagents.UpdateLLMProviderInput{
		UpsertLLMProviderInput: managedagents.UpsertLLMProviderInput{
			ID:           existing.ID,
			ProviderType: existing.ProviderType,
			BaseURL:      existing.BaseURL,
			APIKeyEnv:    existing.APIKeyEnv,
			Enabled:      existing.Enabled,
		},
		ExpectedRevision: expectedRevision,
	}
	provider, atomicAudit, err := s.updateLLMProviderWithAudit(r, input, "llm.provider.update", resourceID)
	var after any
	if err == nil {
		after = llmProviderAuditSnapshot(provider)
	}
	if !atomicAudit || err != nil {
		s.recordLLMControlAudit(r, "llm.provider.update", "llm_provider", resourceID, before, after, err)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	setLLMProviderETag(w, provider)
	writeJSON(w, http.StatusOK, provider)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Server) enableLLMProvider(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("provider_id"))
	existing, err := s.store.GetLLMProvider(resourceID)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.provider.enable", "llm_provider", resourceID, nil, nil, err)
		writeError(w, err)
		return
	}
	expectedRevision, ok := s.requireLLMProviderIfMatch(w, r, existing, "llm.provider.enable")
	if !ok {
		return
	}
	provider, atomicAudit, err := s.setLLMProviderEnabledWithAudit(r, resourceID, true, expectedRevision, "llm.provider.enable")
	var after any
	if err == nil {
		after = llmProviderAuditSnapshot(provider)
	}
	if !atomicAudit || err != nil {
		s.recordLLMControlAudit(r, "llm.provider.enable", "llm_provider", resourceID, llmProviderAuditSnapshot(existing), after, err)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	setLLMProviderETag(w, provider)
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) disableLLMProvider(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("provider_id"))
	existing, err := s.store.GetLLMProvider(resourceID)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.provider.disable", "llm_provider", resourceID, nil, nil, err)
		writeError(w, err)
		return
	}
	expectedRevision, ok := s.requireLLMProviderIfMatch(w, r, existing, "llm.provider.disable")
	if !ok {
		return
	}
	provider, atomicAudit, err := s.setLLMProviderEnabledWithAudit(r, resourceID, false, expectedRevision, "llm.provider.disable")
	var after any
	if err == nil {
		after = llmProviderAuditSnapshot(provider)
	}
	if !atomicAudit || err != nil {
		s.recordLLMControlAudit(r, "llm.provider.disable", "llm_provider", resourceID, llmProviderAuditSnapshot(existing), after, err)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	setLLMProviderETag(w, provider)
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) deleteLLMProvider(w http.ResponseWriter, r *http.Request) {
	resourceID := strings.TrimSpace(r.PathValue("provider_id"))
	existing, err := s.store.GetLLMProvider(resourceID)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.provider.delete", "llm_provider", resourceID, nil, nil, err)
		writeError(w, err)
		return
	}
	before := llmProviderAuditSnapshot(existing)
	expectedRevision, ok := s.requireLLMProviderIfMatch(w, r, existing, "llm.provider.delete")
	if !ok {
		return
	}
	err, atomicAudit := s.deleteLLMProviderWithAudit(r, resourceID, expectedRevision)
	if !atomicAudit || err != nil {
		s.recordLLMControlAudit(r, "llm.provider.delete", "llm_provider", resourceID, before, nil, err)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listLLMModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.store.ListLLMModels(r.URL.Query().Get("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": nonNilSlice(models)})
}

func (s *Server) testLLMProvider(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.PathValue("provider_id"))
	provider, err := s.store.GetLLMProvider(providerID)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.provider.test", "llm_provider", providerID, nil, nil, err)
		writeError(w, err)
		return
	}
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" && (provider.ProviderType == llm.ProviderOpenAICompatible || provider.ProviderType == llm.ProviderTypeOpenAI) {
		baseURL = llm.DefaultOpenAIBaseURL
	}
	result := (llm.DiagnosticService{}).TestProvider(r.Context(), llm.DiagnosticConfig{
		ProviderType:     provider.ProviderType,
		BaseURL:          baseURL,
		APIKey:           llmDiagnosticAPIKey(provider.APIKeyEnv),
		APIKeyConfigured: strings.TrimSpace(provider.APIKeyEnv) != "",
	})
	s.recordLLMDiagnosticAudit(r, "llm.provider.test", "llm_provider", providerID, result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) testLLMModel(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.PathValue("provider_id"))
	modelName := strings.TrimSpace(r.PathValue("model"))
	provider, err := s.store.GetLLMProvider(providerID)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.model.test", "llm_model", llmModelResourceID(providerID, modelName), nil, nil, err)
		writeError(w, err)
		return
	}
	model, exists, err := s.findLLMModel(providerID, modelName)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.model.test", "llm_model", llmModelResourceID(providerID, modelName), nil, nil, err)
		writeError(w, err)
		return
	}
	if !exists {
		err = fmt.Errorf("%w: llm model %s", managedagents.ErrNotFound, llmModelResourceID(providerID, modelName))
		s.recordLLMControlAudit(r, "llm.model.test", "llm_model", llmModelResourceID(providerID, modelName), nil, nil, err)
		writeError(w, err)
		return
	}
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" && (provider.ProviderType == llm.ProviderOpenAICompatible || provider.ProviderType == llm.ProviderTypeOpenAI) {
		baseURL = llm.DefaultOpenAIBaseURL
	}
	result := (llm.DiagnosticService{}).TestModel(r.Context(), llm.DiagnosticConfig{
		ProviderType:       provider.ProviderType,
		BaseURL:            baseURL,
		APIKey:             llmDiagnosticAPIKey(provider.APIKeyEnv),
		APIKeyConfigured:   strings.TrimSpace(provider.APIKeyEnv) != "",
		Model:              model.Model,
		CapabilityType:     model.CapabilityType,
		Protocol:           model.Capabilities.Protocol,
		ExpectedDimensions: model.Capabilities.Dimensions,
	})
	s.recordLLMDiagnosticAudit(r, "llm.model.test", "llm_model", llmModelResourceID(providerID, modelName), result)
	writeJSON(w, http.StatusOK, result)
}

func llmDiagnosticAPIKey(envName string) string {
	envName = strings.TrimSpace(envName)
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}

func (s *Server) recordLLMDiagnosticAudit(r *http.Request, action string, resourceType string, resourceID string, result llm.DiagnosticResult) {
	var actionErr error
	if result.Status == llm.DiagnosticStatusFailed {
		actionErr = errors.New(result.Message)
	}
	s.recordLLMControlAudit(r, action, resourceType, resourceID, nil, result, actionErr)
}

func (s *Server) getSessionRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	config, err := managedagents.ResolveAgentRuntimeConfigWithContext(r.Context(), s.store, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *Server) getSessionRuntimeCapabilities(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	session, err := managedagents.GetSessionWithContext(r.Context(), s.store, sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	available := []string{execution.ToolRuntimeCloudSandbox}
	localProvider := s.executionProviderForRequest(execution.ProviderRequest{
		WorkspaceID:   session.WorkspaceID,
		OwnerID:       session.OwnerID,
		SessionID:     sessionID,
		EnvironmentID: session.EnvironmentID,
		ToolRuntime:   execution.ToolRuntimeLocalSystem,
	})
	if _, unavailable := localProvider.(capability.UnavailableProvider); !unavailable && localProvider != nil {
		available = append(available, execution.ToolRuntimeLocalSystem)
	}
	writeJSON(w, http.StatusOK, sessionRuntimeCapabilitiesResponse{
		DefaultRuntime:    execution.ToolRuntimeCloudSandbox,
		AvailableRuntimes: available,
		HumanInteraction:  humanInteractionCapabilities(session.RuntimeSettings),
	})
}

func (s *Server) upsertLLMModel(w http.ResponseWriter, r *http.Request) {
	var request llmModelRequest
	if err := decodeJSON(r, &request); err != nil {
		s.recordLLMControlAudit(r, "llm.model.create", "llm_model", "", nil, nil, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resourceID := llmModelResourceID(request.ProviderID, request.Model)
	existing, exists, err := s.findLLMModel(request.ProviderID, request.Model)
	if err != nil {
		s.recordLLMControlAudit(r, "llm.model.create", "llm_model", resourceID, nil, nil, err)
		writeError(w, err)
		return
	}
	action := "llm.model.create"
	var before any
	expectedRevision := int64(0)
	if exists {
		before = llmModelAuditSnapshot(existing)
		if ifNoneMatch := strings.TrimSpace(r.Header.Get("If-None-Match")); ifNoneMatch != "" {
			if ifNoneMatch != "*" {
				err := fmt.Errorf("%w: If-None-Match must be * when creating an llm model", managedagents.ErrInvalid)
				s.recordLLMControlAudit(r, action, "llm_model", resourceID, before, nil, err)
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			err := fmt.Errorf("%w: llm model %s already exists", managedagents.ErrConflict, resourceID)
			s.recordLLMControlAudit(r, action, "llm_model", resourceID, before, nil, err)
			writeError(w, err)
			return
		}
		action = "llm.model.update"
		expectedRevision, err = parseLLMProviderIfMatch(r.Header.Get("If-Match"))
		if err != nil {
			s.recordLLMControlAudit(r, action, "llm_model", resourceID, before, nil, err)
			status := http.StatusBadRequest
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		if expectedRevision != existing.Revision {
			err := fmt.Errorf("%w: llm model %s revision changed from %d to %d", managedagents.ErrRevisionConflict, resourceID, expectedRevision, existing.Revision)
			s.recordLLMControlAudit(r, action, "llm_model", resourceID, before, nil, err)
			writeError(w, err)
			return
		}
	} else if strings.TrimSpace(r.Header.Get("If-None-Match")) == "" {
		err := fmt.Errorf("%w: If-None-Match header is required when creating an llm model", managedagents.ErrInvalid)
		s.recordLLMControlAudit(r, action, "llm_model", resourceID, nil, nil, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	} else if strings.TrimSpace(r.Header.Get("If-None-Match")) != "*" {
		err := fmt.Errorf("%w: If-None-Match must be * when creating an llm model", managedagents.ErrInvalid)
		s.recordLLMControlAudit(r, action, "llm_model", resourceID, nil, nil, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input := managedagents.UpsertLLMModelInput{
		ProviderID:          request.ProviderID,
		Model:               request.Model,
		ContextWindowTokens: request.ContextWindowTokens,
		CapabilityType:      request.CapabilityType,
		Capabilities:        request.Capabilities,
		IsDefaultVision:     request.IsDefaultVision,
		IsDefaultEmbedding:  request.IsDefaultEmbedding,
		IsDefaultReranker:   request.IsDefaultReranker,
	}
	var model managedagents.LLMModel
	var atomicAudit bool
	if exists {
		model, atomicAudit, err = s.updateLLMModelWithAudit(r, managedagents.UpdateLLMModelInput{
			UpsertLLMModelInput: input, ExpectedRevision: expectedRevision,
		}, action, resourceID)
	} else {
		model, atomicAudit, err = s.createLLMModelWithAudit(r, input, action, resourceID)
	}
	var after any
	if err == nil {
		after = llmModelAuditSnapshot(model)
	}
	if !atomicAudit || err != nil {
		s.recordLLMControlAudit(r, action, "llm_model", resourceID, before, after, err)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	setLLMModelETag(w, model)
	status := http.StatusOK
	if !exists {
		status = http.StatusCreated
	}
	writeJSON(w, status, model)
}

func (s *Server) deleteLLMModel(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.PathValue("provider_id"))
	modelName := strings.TrimSpace(r.PathValue("model"))
	resourceID := llmModelResourceID(providerID, modelName)
	existing, exists, lookupErr := s.findLLMModel(providerID, modelName)
	if lookupErr != nil {
		s.recordLLMControlAudit(r, "llm.model.delete", "llm_model", resourceID, nil, nil, lookupErr)
		writeError(w, lookupErr)
		return
	}
	var before any
	if exists {
		before = llmModelAuditSnapshot(existing)
	}
	if !exists {
		err := managedagents.ErrNotFound
		s.recordLLMControlAudit(r, "llm.model.delete", "llm_model", resourceID, nil, nil, err)
		writeError(w, err)
		return
	}
	expectedRevision, err := parseLLMProviderIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.recordLLMControlAudit(r, "llm.model.delete", "llm_model", resourceID, before, nil, err)
		status := http.StatusBadRequest
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	if expectedRevision != existing.Revision {
		err := fmt.Errorf("%w: llm model %s revision changed from %d to %d", managedagents.ErrRevisionConflict, resourceID, expectedRevision, existing.Revision)
		s.recordLLMControlAudit(r, "llm.model.delete", "llm_model", resourceID, before, nil, err)
		writeError(w, err)
		return
	}
	err, atomicAudit := s.deleteLLMModelWithAudit(r, providerID, modelName, resourceID, expectedRevision)
	if !atomicAudit || err != nil {
		s.recordLLMControlAudit(r, "llm.model.delete", "llm_model", resourceID, before, nil, err)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type llmProviderAuditState struct {
	ProviderType         string `json:"provider_type"`
	Enabled              bool   `json:"enabled"`
	Revision             int64  `json:"revision"`
	BaseURLConfigured    bool   `json:"base_url_configured"`
	CredentialConfigured bool   `json:"credential_configured"`
}

type llmModelAuditState struct {
	ContextWindowTokens int                                `json:"context_window_tokens"`
	CapabilityType      string                             `json:"capability_type"`
	Capabilities        managedagents.LLMModelCapabilities `json:"capabilities"`
	IsDefaultVision     bool                               `json:"is_default_vision"`
	IsDefaultEmbedding  bool                               `json:"is_default_embedding"`
	IsDefaultReranker   bool                               `json:"is_default_reranker"`
	Revision            int64                              `json:"revision"`
}

func llmProviderAuditSnapshot(provider managedagents.LLMProvider) llmProviderAuditState {
	return llmProviderAuditState{
		ProviderType:         provider.ProviderType,
		Enabled:              provider.Enabled,
		Revision:             provider.Revision,
		BaseURLConfigured:    strings.TrimSpace(provider.BaseURL) != "",
		CredentialConfigured: strings.TrimSpace(provider.APIKeyEnv) != "",
	}
}

func llmModelAuditSnapshot(model managedagents.LLMModel) llmModelAuditState {
	return llmModelAuditState{
		ContextWindowTokens: model.ContextWindowTokens,
		CapabilityType:      model.CapabilityType,
		Capabilities:        model.Capabilities,
		IsDefaultVision:     model.IsDefaultVision,
		IsDefaultEmbedding:  model.IsDefaultEmbedding,
		IsDefaultReranker:   model.IsDefaultReranker,
		Revision:            model.Revision,
	}
}

func llmModelResourceID(providerID string, model string) string {
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	if providerID == "" {
		return model
	}
	if model == "" {
		return providerID
	}
	return providerID + "/" + model
}

func (s *Server) findLLMModel(providerID string, model string) (managedagents.LLMModel, bool, error) {
	providerID = strings.TrimSpace(providerID)
	model = strings.TrimSpace(model)
	models, err := s.store.ListLLMModels(providerID)
	if err != nil {
		return managedagents.LLMModel{}, false, err
	}
	for _, candidate := range models {
		if candidate.ProviderID == providerID && candidate.Model == model {
			return candidate, true, nil
		}
	}
	return managedagents.LLMModel{}, false, nil
}

func (s *Server) llmControlAuditInput(r *http.Request, action string, resourceType string, resourceID string) managedagents.RecordOperatorAuditInput {
	principal := controlPrincipalFromRequest(r)
	return managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, ""), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: action,
		ResourceType: resourceType, ResourceID: strings.TrimSpace(resourceID), Outcome: "succeeded",
	}
}

func setLLMProviderETag(w http.ResponseWriter, provider managedagents.LLMProvider) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(provider.Revision, 10)))
}

func setLLMModelETag(w http.ResponseWriter, model managedagents.LLMModel) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(model.Revision, 10)))
}

func parseLLMProviderIfMatch(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%w: If-Match header is required", managedagents.ErrInvalid)
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return 0, fmt.Errorf("%w: If-Match must be a quoted provider revision", managedagents.ErrInvalid)
	}
	revision, err := strconv.ParseInt(unquoted, 10, 64)
	if err != nil || revision <= 0 {
		return 0, fmt.Errorf("%w: If-Match must contain a positive provider revision", managedagents.ErrInvalid)
	}
	return revision, nil
}

func (s *Server) requireLLMProviderIfMatch(w http.ResponseWriter, r *http.Request, provider managedagents.LLMProvider, action string) (int64, bool) {
	expectedRevision, err := parseLLMProviderIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		s.recordLLMControlAudit(r, action, "llm_provider", provider.ID, llmProviderAuditSnapshot(provider), nil, err)
		status := http.StatusBadRequest
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return 0, false
	}
	if expectedRevision != provider.Revision {
		err := fmt.Errorf("%w: llm provider %s revision changed from %d to %d", managedagents.ErrRevisionConflict, provider.ID, expectedRevision, provider.Revision)
		s.recordLLMControlAudit(r, action, "llm_provider", provider.ID, llmProviderAuditSnapshot(provider), nil, err)
		writeError(w, err)
		return 0, false
	}
	return expectedRevision, true
}

func (s *Server) createLLMProviderWithAudit(r *http.Request, input managedagents.UpsertLLMProviderInput, action string, resourceID string) (managedagents.LLMProvider, bool, error) {
	if store, ok := s.store.(managedagents.LLMControlAuditContextStore); ok {
		provider, err := store.CreateLLMProviderWithAuditContext(r.Context(), input, s.llmControlAuditInput(r, action, "llm_provider", resourceID))
		return provider, true, err
	}
	provider, err := s.store.CreateLLMProvider(input)
	return provider, false, err
}

func (s *Server) updateLLMProviderWithAudit(r *http.Request, input managedagents.UpdateLLMProviderInput, action string, resourceID string) (managedagents.LLMProvider, bool, error) {
	if store, ok := s.store.(managedagents.LLMControlAuditContextStore); ok {
		provider, err := store.UpdateLLMProviderWithAuditContext(r.Context(), input, s.llmControlAuditInput(r, action, "llm_provider", resourceID))
		return provider, true, err
	}
	provider, err := s.store.UpdateLLMProvider(input)
	return provider, false, err
}

func (s *Server) setLLMProviderEnabledWithAudit(r *http.Request, resourceID string, enabled bool, expectedRevision int64, action string) (managedagents.LLMProvider, bool, error) {
	if store, ok := s.store.(managedagents.LLMControlAuditContextStore); ok {
		provider, err := store.SetLLMProviderEnabledIfRevisionWithAuditContext(r.Context(), resourceID, enabled, expectedRevision, s.llmControlAuditInput(r, action, "llm_provider", resourceID))
		return provider, true, err
	}
	provider, err := s.store.SetLLMProviderEnabledIfRevision(resourceID, enabled, expectedRevision)
	return provider, false, err
}

func (s *Server) deleteLLMProviderWithAudit(r *http.Request, resourceID string, expectedRevision int64) (error, bool) {
	if store, ok := s.store.(managedagents.LLMControlAuditContextStore); ok {
		err := store.DeleteLLMProviderIfRevisionWithAuditContext(r.Context(), resourceID, expectedRevision, s.llmControlAuditInput(r, "llm.provider.delete", "llm_provider", resourceID))
		return err, true
	}
	return s.store.DeleteLLMProviderIfRevision(resourceID, expectedRevision), false
}

func (s *Server) createLLMModelWithAudit(r *http.Request, input managedagents.UpsertLLMModelInput, action string, resourceID string) (managedagents.LLMModel, bool, error) {
	if store, ok := s.store.(managedagents.LLMControlAuditContextStore); ok {
		model, err := store.CreateLLMModelWithAuditContext(r.Context(), input, s.llmControlAuditInput(r, action, "llm_model", resourceID))
		return model, true, err
	}
	model, err := s.store.CreateLLMModel(input)
	return model, false, err
}

func (s *Server) updateLLMModelWithAudit(r *http.Request, input managedagents.UpdateLLMModelInput, action string, resourceID string) (managedagents.LLMModel, bool, error) {
	if store, ok := s.store.(managedagents.LLMControlAuditContextStore); ok {
		model, err := store.UpdateLLMModelWithAuditContext(r.Context(), input, s.llmControlAuditInput(r, action, "llm_model", resourceID))
		return model, true, err
	}
	model, err := s.store.UpdateLLMModel(input)
	return model, false, err
}

func (s *Server) deleteLLMModelWithAudit(r *http.Request, providerID string, modelName string, resourceID string, expectedRevision int64) (error, bool) {
	if store, ok := s.store.(managedagents.LLMControlAuditContextStore); ok {
		err := store.DeleteLLMModelIfRevisionWithAuditContext(r.Context(), providerID, modelName, expectedRevision, s.llmControlAuditInput(r, "llm.model.delete", "llm_model", resourceID))
		return err, true
	}
	return s.store.DeleteLLMModelIfRevision(providerID, modelName, expectedRevision), false
}

func (s *Server) recordLLMControlAudit(r *http.Request, action string, resourceType string, resourceID string, before any, after any, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	details, err := json.Marshal(map[string]any{"before": before, "after": after})
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("llm control audit details encoding failed", "action", action, "resource_id", resourceID, "error", err)
		}
		return
	}
	audit := s.llmControlAuditInput(r, action, resourceType, resourceID)
	audit.Outcome = outcome
	audit.ErrorMessage = errorMessage
	audit.Details = details
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, audit); err != nil && s.logger != nil {
		s.logger.Warn("llm control audit write failed", "action", action, "resource_id", resourceID, "error", err)
	}
}

func (s *Server) getSessionLLMUsage(w http.ResponseWriter, r *http.Request) {
	report, err := managedagents.GetSessionLLMUsageWithContext(r.Context(), s.store, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) getSessionSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := managedagents.GetSessionSummaryWithContext(r.Context(), s.store, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) getSessionTrace(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	events, err := managedagents.ListEventsWithContext(r.Context(), s.store, sessionID, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	trace := observability.ProjectTurnTrace(sessionID, r.URL.Query().Get("turn_id"), events)
	if trace.TurnID == "" || len(trace.Steps) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace not found"})
		return
	}
	s.refreshTraceIndex(r.Context(), sessionID, trace)
	s.writeTraceFormat(w, r, trace)
}

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request) {
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid limit: %v", managedagents.ErrInvalid, err))
		return
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset, err := traceCatalogOffset(r, "traces")
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid trace cursor: %v", managedagents.ErrInvalid, err))
		return
	}
	queryLimit := limit + 1
	if indexed, ok := s.store.(managedagents.TraceIndexStore); ok && requestCanViewWorkspaceWide(r) {
		traces, err := managedagents.ListTraceIndexesWithContext(r.Context(), indexed, managedagents.ListTraceIndexInput{
			WorkspaceID:     workspaceID,
			SessionID:       r.URL.Query().Get("session_id"),
			TurnID:          r.URL.Query().Get("turn_id"),
			SessionStatus:   r.URL.Query().Get("session_status"),
			IncludeArchived: strings.EqualFold(r.URL.Query().Get("include_archived"), "true") || r.URL.Query().Get("include_archived") == "1",
			Limit:           queryLimit,
			Offset:          offset,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		if len(traces) >= limit {
			catalog := observability.TraceCatalogFromIndex(traces)
			writeJSON(w, http.StatusOK, pagedTraceCatalogResponse(r, catalog, limit, offset))
			return
		}
	}
	sessions, eventsBySession, err := s.recentSessionEvents(r, offset+queryLimit)
	if err != nil {
		writeError(w, err)
		return
	}
	catalog := observability.BuildTraceCatalogPage(sessions, eventsBySession, queryLimit, offset)
	s.refreshTraceIndexesForCatalog(r.Context(), sessions, eventsBySession, catalog)
	writeJSON(w, http.StatusOK, pagedTraceCatalogResponse(r, catalog, limit, offset))
}

func (s *Server) listSpans(w http.ResponseWriter, r *http.Request) {
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid limit: %v", managedagents.ErrInvalid, err))
		return
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	offset, err := traceCatalogOffset(r, "spans")
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid span cursor: %v", managedagents.ErrInvalid, err))
		return
	}
	queryLimit := limit + 1
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
	if indexed, ok := s.store.(managedagents.TraceIndexStore); ok && requestCanViewWorkspaceWide(r) {
		spans, err := managedagents.ListTraceSpanIndexesWithContext(r.Context(), indexed, managedagents.ListTraceSpanIndexInput{
			WorkspaceID:           workspaceID,
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
			IncludeArchived:       strings.EqualFold(r.URL.Query().Get("include_archived"), "true") || r.URL.Query().Get("include_archived") == "1",
			Limit:                 queryLimit,
			Offset:                offset,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		if len(spans) > 0 {
			catalog := observability.TraceSpanCatalogFromIndex(spans)
			writeJSON(w, http.StatusOK, pagedTraceSpanCatalogResponse(r, catalog, limit, offset))
			return
		}
	}
	sessions, eventsBySession, err := s.recentSessionEvents(r, offset+queryLimit)
	if err != nil {
		writeError(w, err)
		return
	}
	catalog := observability.BuildTraceSpanCatalog(sessions, eventsBySession, observability.TraceSpanCatalogFilter{
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
		Limit:                 queryLimit,
		Offset:                offset,
	})
	s.refreshTraceIndexesForSessions(r.Context(), sessions, eventsBySession)
	writeJSON(w, http.StatusOK, pagedTraceSpanCatalogResponse(r, catalog, limit, offset))
}

func pagedTraceCatalogResponse(r *http.Request, catalog []observability.TraceCatalogEntry, limit int, offset int) map[string]any {
	hasMore := len(catalog) > limit
	if hasMore {
		catalog = catalog[:limit]
	}
	if isV2Request(r) {
		return map[string]any{
			"items":       catalog,
			"next_cursor": nextTraceCatalogCursor(r, "traces", offset, len(catalog), hasMore),
			"has_more":    hasMore,
		}
	}
	return map[string]any{
		"traces":      catalog,
		"limit":       limit,
		"offset":      offset,
		"next_offset": offset + len(catalog),
		"has_more":    hasMore,
	}
}

func pagedTraceSpanCatalogResponse(r *http.Request, catalog observability.TraceSpanCatalog, limit int, offset int) any {
	hasMore := len(catalog.Spans) > limit
	if hasMore {
		catalog.Spans = catalog.Spans[:limit]
	}
	catalog.Limit = limit
	catalog.Offset = offset
	catalog.NextOffset = offset + len(catalog.Spans)
	catalog.HasMore = hasMore
	if isV2Request(r) {
		return map[string]any{
			"items":       catalog.Spans,
			"next_cursor": nextTraceCatalogCursor(r, "spans", offset, len(catalog.Spans), hasMore),
			"has_more":    hasMore,
		}
	}
	return catalog
}

type traceCatalogCursor struct {
	Version     int    `json:"v"`
	Resource    string `json:"resource"`
	Offset      int    `json:"offset"`
	Fingerprint string `json:"fingerprint"`
}

func traceCatalogOffset(r *http.Request, resource string) (int, error) {
	cursorValue := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if cursorValue == "" {
		return optionalPositiveInt(r.URL.Query().Get("offset"))
	}
	if strings.TrimSpace(r.URL.Query().Get("offset")) != "" {
		return 0, errors.New("cursor and offset cannot be combined")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursorValue)
	if err != nil {
		return 0, errors.New("cursor is not valid base64url")
	}
	var cursor traceCatalogCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return 0, errors.New("cursor payload is invalid")
	}
	if cursor.Version != 1 || cursor.Resource != resource || cursor.Offset <= 0 {
		return 0, errors.New("cursor is not valid for this resource")
	}
	if cursor.Fingerprint != traceCatalogFingerprint(r, resource) {
		return 0, errors.New("cursor does not match the current filters")
	}
	return cursor.Offset, nil
}

func nextTraceCatalogCursor(r *http.Request, resource string, offset int, itemCount int, hasMore bool) string {
	if !hasMore {
		return ""
	}
	cursor := traceCatalogCursor{
		Version:     1,
		Resource:    resource,
		Offset:      offset + itemCount,
		Fingerprint: traceCatalogFingerprint(r, resource),
	}
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func traceCatalogFingerprint(r *http.Request, resource string) string {
	values := r.URL.Query()
	values.Del("cursor")
	values.Del("offset")
	values.Del("limit")
	digest := sha256.Sum256([]byte(resource + "\n" + values.Encode()))
	return hex.EncodeToString(digest[:16])
}

func (s *Server) recentSessionEvents(r *http.Request, limit int) ([]managedagents.Session, map[string][]managedagents.Event, error) {
	if sessionID := strings.TrimSpace(r.URL.Query().Get("session_id")); sessionID != "" {
		session, err := s.getSessionForRequest(r, sessionID)
		if err != nil {
			return nil, nil, err
		}
		if principal, ok := PrincipalFromRequest(r); ok {
			if err := authorizeSessionPrincipal(principal, session); err != nil {
				return nil, nil, err
			}
		}
		events, err := managedagents.ListEventsWithContext(r.Context(), s.store, session.ID, 0)
		if err != nil {
			return nil, nil, err
		}
		return []managedagents.Session{session}, map[string][]managedagents.Event{session.ID: events}, nil
	}
	sessions, err := s.listSessionsForRequest(r, managedagents.ListSessionsInput{
		WorkspaceID:     requestWorkspaceID(r, r.URL.Query().Get("workspace_id")),
		OwnerID:         requestSessionOwnerFilter(r),
		Status:          r.URL.Query().Get("session_status"),
		IncludeArchived: strings.EqualFold(r.URL.Query().Get("include_archived"), "true") || r.URL.Query().Get("include_archived") == "1",
		Limit:           limit,
	})
	if err != nil {
		return nil, nil, err
	}
	eventsBySession := make(map[string][]managedagents.Event, len(sessions))
	for _, session := range sessions {
		events, err := managedagents.ListEventsWithContext(r.Context(), s.store, session.ID, 0)
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
	lookup, err := s.findTraceByID(r, traceID, limit)
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
	lookup, err := s.findTraceByID(r, traceID, limit)
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

func (s *Server) findTraceByID(r *http.Request, traceID string, limit int) (traceLookupResult, error) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return traceLookupResult{}, managedagents.ErrNotFound
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if indexed, ok := s.store.(managedagents.TraceIndexStore); ok {
		entries, err := managedagents.ListTraceIndexesWithContext(r.Context(), indexed, managedagents.ListTraceIndexInput{
			WorkspaceID: requestWorkspaceID(r, ""), TraceID: traceID, IncludeArchived: true, Limit: 1,
		})
		if err == nil && len(entries) == 1 {
			entry := entries[0]
			session, err := s.getSessionForRequest(r, entry.SessionID)
			if err != nil {
				return traceLookupResult{}, err
			}
			events, err := managedagents.ListEventsWithContext(r.Context(), s.store, entry.SessionID, 0)
			if err != nil {
				return traceLookupResult{}, err
			}
			trace := observability.ProjectTurnTrace(entry.SessionID, entry.TurnID, events)
			if trace.TurnID == "" || len(trace.Steps) == 0 {
				return traceLookupResult{}, managedagents.ErrNotFound
			}
			s.refreshTraceIndex(r.Context(), entry.SessionID, trace)
			return traceLookupResult{Session: session, Trace: trace}, nil
		}
		if err != nil && !errors.Is(err, managedagents.ErrNotFound) {
			return traceLookupResult{}, err
		}
	}
	sessions, err := s.listSessionsForRequest(r, managedagents.ListSessionsInput{IncludeArchived: true, Limit: limit})
	if err != nil {
		return traceLookupResult{}, err
	}
	for _, session := range sessions {
		events, err := managedagents.ListEventsWithContext(r.Context(), s.store, session.ID, 0)
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
			s.refreshTraceIndex(r.Context(), session.ID, trace)
			return traceLookupResult{
				Session: session,
				Trace:   trace,
			}, nil
		}
	}
	return traceLookupResult{}, managedagents.ErrNotFound
}

func (s *Server) refreshTraceIndex(ctx context.Context, sessionID string, trace observability.TurnTrace) {
	indexed, ok := s.store.(managedagents.TraceIndexStore)
	if !ok || trace.TraceID == "" || trace.TurnID == "" || len(trace.Steps) == 0 {
		return
	}
	session, err := managedagents.GetSessionWithContext(ctx, s.store, sessionID)
	if err != nil {
		s.logger.Warn("trace index session lookup failed", "session_id", sessionID, "trace_id", trace.TraceID, "error", err)
		return
	}
	if err := managedagents.UpsertTraceIndexWithContext(ctx, indexed, observability.TraceIndexInput(session, trace)); err != nil {
		s.logger.Warn("trace index upsert failed", "session_id", sessionID, "turn_id", trace.TurnID, "trace_id", trace.TraceID, "error", err)
	}
}

func (s *Server) refreshTraceIndexesForCatalog(ctx context.Context, sessions []managedagents.Session, eventsBySession map[string][]managedagents.Event, catalog []observability.TraceCatalogEntry) {
	if _, ok := s.store.(managedagents.TraceIndexStore); !ok {
		return
	}
	sessionsByID := make(map[string]managedagents.Session, len(sessions))
	for _, session := range sessions {
		sessionsByID[session.ID] = session
	}
	for _, entry := range catalog {
		if _, ok := sessionsByID[entry.SessionID]; !ok {
			continue
		}
		trace := observability.ProjectTurnTrace(entry.SessionID, entry.TurnID, eventsBySession[entry.SessionID])
		s.refreshTraceIndex(ctx, entry.SessionID, trace)
	}
}

func (s *Server) refreshTraceIndexesForSessions(ctx context.Context, sessions []managedagents.Session, eventsBySession map[string][]managedagents.Event) {
	if _, ok := s.store.(managedagents.TraceIndexStore); !ok {
		return
	}
	for _, session := range sessions {
		for _, turn := range observability.BuildTurnCatalog(session.ID, eventsBySession[session.ID]) {
			trace := observability.ProjectTurnTrace(session.ID, turn.TurnID, eventsBySession[session.ID])
			s.refreshTraceIndex(ctx, session.ID, trace)
		}
	}
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
	workspaceID := requestWorkspaceID(r, query.Get("workspace_id"))
	usage, err := managedagents.ListLLMUsageWithContext(r.Context(), s.store, managedagents.ListLLMUsageInput{
		WorkspaceID: workspaceID,
		GroupBy:     managedagents.LLMUsageGroupByProviderModel,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	workers, err := s.listWorkersForRequest(r, managedagents.ListWorkersInput{
		WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	subagents, err := s.store.GetSubagentMetrics(managedagents.GetSubagentMetricsInput{
		WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	taskGroups, err := s.store.GetSubagentTaskGroupMetrics(managedagents.GetSubagentTaskGroupMetricsInput{
		WorkspaceID: workspaceID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	snapshot := observability.MetricsSnapshot{
		Usage:                 usage,
		Workers:               workers,
		Subagents:             subagents,
		TaskGroups:            taskGroups,
		Observability:         s.observabilityStatus(r.Context()),
		BinaryScans:           skillmarketplace.BinaryScanMetricsSnapshot(),
		SkillAssetGC:          skillretention.SnapshotMetrics(),
		CompletionValidations: observability.CompletionValidationMetricsSnapshot(),
		FilesystemTools:       observability.FilesystemToolMetricsSnapshot(),
		AgentCore:             observability.AgentCoreMetricsSnapshot(),
		AgentCoreDurability:   observability.AgentCoreDurabilityMetricsSnapshot(),
		WorkerLeases:          observability.WorkerLeaseMetricsSnapshot(),
	}
	if s.authorizationAudit != nil {
		snapshot.AuthorizationDecisions = s.authorizationAudit.snapshot()
		snapshot.SecurityAuditExporter = s.authorizationAudit.exporterMetrics()
	}
	if stats, ok := s.mcpHostStats(); ok {
		snapshot.MCPHost = stats
	}
	if stats, ok := s.mcpHTTPHostStats(); ok {
		snapshot.MCPHTTPHost = stats
	}
	if stats, ok := s.mcpRuntimeGuardStats(); ok {
		snapshot.MCPRuntimeGuard = stats
	}
	if sessionID := strings.TrimSpace(query.Get("session_id")); sessionID != "" {
		if principal, ok := PrincipalFromRequest(r); ok {
			session, err := s.getSessionForRequest(r, sessionID)
			if err != nil {
				writeError(w, err)
				return
			}
			if err := authorizeSessionPrincipal(principal, session); err != nil {
				writeError(w, err)
				return
			}
		}
		events, err := managedagents.ListEventsWithContext(r.Context(), s.store, sessionID, 0)
		if err != nil {
			writeError(w, err)
			return
		}
		trace := observability.ProjectTurnTrace(sessionID, query.Get("turn_id"), events)
		interventions, err := managedagents.ListSessionInterventionsWithContext(r.Context(), s.store, sessionID, "")
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

func (s *Server) getUserApp(w http.ResponseWriter, r *http.Request) {
	if s.webLogin != nil {
		if _, err := s.authenticator.authenticate(r); err != nil {
			http.Redirect(w, r, "/auth/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
	}
	content, err := inspectorAssets.ReadFile("app/index.html")
	if err != nil {
		s.logger.Error("user app index read failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "app unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err != nil {
		s.logger.Warn("user app response write failed", "error", err)
	}
}

func (s *Server) getObservabilityStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.observabilityStatus(r.Context()))
}

func (s *Server) retryObservabilityExporters(w http.ResponseWriter, r *http.Request) {
	result, err := observability.RetryFailedExporterRunsFromEnvContext(r.Context(), s.store)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) replaySecurityAuditDeadLetters(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.authorizationAudit == nil {
		writeError(w, fmt.Errorf("%w: durable security audit pipeline is not enabled", managedagents.ErrConflict))
		return
	}
	replayer, legacyOK := s.authorizationAudit.sink.(observability.SecurityAuditDeadLetterReplayer)
	contextReplayer, contextOK := s.authorizationAudit.sink.(observability.SecurityAuditDeadLetterContextReplayer)
	if !legacyOK && !contextOK {
		writeError(w, fmt.Errorf("%w: durable security audit pipeline is not enabled", managedagents.ErrConflict))
		return
	}
	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeError(w, fmt.Errorf("%w: limit must be between 1 and 1000", managedagents.ErrInvalid))
			return
		}
		limit = parsed
	}
	var replayed int
	var err error
	if contextOK {
		replayed, err = contextReplayer.ReplayDeadLettersContext(r.Context(), time.Now().UTC(), limit)
	} else {
		replayed, err = replayer.ReplayDeadLetters(time.Now().UTC(), limit)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"replayed": replayed})
}

func (s *Server) getSecurityAuditIntegrityKeyStatus(w http.ResponseWriter, r *http.Request) {
	status, ok, err := s.securityAuditIntegrityKeyStatus(r.Context())
	if !ok {
		writeError(w, fmt.Errorf("%w: durable security audit keyring is not enabled", managedagents.ErrConflict))
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) observabilityStatus(ctx context.Context) observability.Status {
	runs, err := managedagents.ListObservabilityExporterRunsWithContext(ctx, s.store, managedagents.ListObservabilityExporterRunsInput{Limit: 20})
	if err != nil {
		s.logger.Warn("list observability exporter runs failed", "error", err)
		status := observability.StatusFromEnv()
		return s.withSecurityAuditOutboxStatus(ctx, status)
	}
	return s.withSecurityAuditOutboxStatus(ctx, observability.StatusFromEnvWithRuns(runs))
}

func (s *Server) withSecurityAuditOutboxStatus(ctx context.Context, status observability.Status) observability.Status {
	store, ok := s.store.(managedagents.SecurityAuditOutboxStore)
	if ok {
		stats, err := managedagents.GetSecurityAuditOutboxStatsWithContext(ctx, store, time.Now().UTC())
		if err != nil {
			s.logger.Warn("get security audit outbox status failed", "error", err)
		} else {
			status.SecurityAuditOutbox = &stats
		}
	}
	if keyStatus, ok, err := s.securityAuditIntegrityKeyStatus(ctx); ok {
		if err != nil {
			s.logger.Warn("get security audit integrity key status failed", "error", err)
		} else {
			status.SecurityAuditIntegrityKeys = &keyStatus
		}
	}
	return status
}

func (s *Server) securityAuditIntegrityKeyStatusProvider() (observability.SecurityAuditIntegrityKeyStatusProvider, bool) {
	if s == nil || s.authorizationAudit == nil || s.authorizationAudit.sink == nil {
		return nil, false
	}
	provider, ok := s.authorizationAudit.sink.(observability.SecurityAuditIntegrityKeyStatusProvider)
	return provider, ok
}

func (s *Server) securityAuditIntegrityKeyStatus(ctx context.Context) (observability.SecurityAuditIntegrityKeyStatus, bool, error) {
	provider, ok := s.securityAuditIntegrityKeyStatusProvider()
	if !ok {
		return observability.SecurityAuditIntegrityKeyStatus{}, false, nil
	}
	if scoped, ok := s.authorizationAudit.sink.(observability.SecurityAuditIntegrityKeyContextStatusProvider); ok {
		status, err := scoped.SecurityAuditIntegrityKeyStatusContext(ctx)
		return status, true, err
	}
	status, err := provider.SecurityAuditIntegrityKeyStatus()
	return status, true, err
}

func (s *Server) upsertSessionSummary(w http.ResponseWriter, r *http.Request) {
	var request sessionSummaryRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := managedagents.UpsertSessionSummaryWithContext(r.Context(), s.store, r.PathValue("session_id"), managedagents.UpsertSessionSummaryInput{
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

	report, err := managedagents.ListLLMUsageWithContext(r.Context(), s.store, managedagents.ListLLMUsageInput{
		WorkspaceID: requestWorkspaceID(r, query.Get("workspace_id")),
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
	input.WorkspaceID = s.requestWorkerWorkspaceID(r, input.WorkspaceID)
	if principal, ok := PrincipalFromRequest(r); ok {
		input.RegisteredBy = principal.Subject
	} else if s.authenticator != nil && s.authenticator.config.Mode != AuthModeDisabled {
		input.RegisteredBy = "worker-service"
	}
	worker, err := managedagents.RegisterWorkerWithContext(r.Context(), s.store, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, worker)
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := s.getWorkerForRequest(r, r.PathValue("worker_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.listWorkersForRequest(r, managedagents.ListWorkersInput{
		WorkspaceID: s.requestWorkerWorkspaceID(r, r.URL.Query().Get("workspace_id")),
		Status:      r.URL.Query().Get("status"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": nonNilSlice(workers)})
}

func (s *Server) reapExpiredWorkers(w http.ResponseWriter, r *http.Request) {
	var input managedagents.ReapExpiredWorkersInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	expired, err := managedagents.ReapExpiredWorkersWithContext(r.Context(), s.store, input)
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
	workspaceID := s.requestWorkerWorkspaceID(r, request.WorkspaceID)
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	workers, err := s.listWorkersForRequest(r, managedagents.ListWorkersInput{
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
	worker, err := managedagents.HeartbeatWorkerWithContext(r.Context(), s.store, r.PathValue("worker_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) archiveWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := managedagents.ArchiveWorkerWithContext(r.Context(), s.store, r.PathValue("worker_id"))
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
	input.WorkspaceID = s.requestWorkerWorkspaceID(r, input.WorkspaceID)
	invocation, err := validateWorkerWorkPayload(input)
	if err != nil {
		writeError(w, err)
		return
	}
	if input.WorkerID == "" && invocation != nil {
		workerID, err := workerselect.Selector{Store: s.store}.SelectWorkerIDContext(r.Context(), workerselect.Request{
			WorkspaceID: input.WorkspaceID,
			Invocation:  *invocation,
		})
		if err != nil {
			if errors.Is(err, managedagents.ErrConflict) {
				response, diagnoseErr := s.workerWorkConflictResponse(r.Context(), input.WorkspaceID, *invocation, err)
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
	work, err := managedagents.EnqueueWorkerWorkWithContext(r.Context(), s.store, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, work)
}

func (s *Server) workerWorkConflictResponse(ctx context.Context, workspaceID string, invocation tools.WorkInvocation, cause error) (workerWorkConflictResponse, error) {
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	workers, err := managedagents.ListWorkersWithContext(ctx, s.store, managedagents.ListWorkersInput{
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
	work, err := s.getWorkerWorkForRequest(r, r.PathValue("work_id"))
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
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	expired, err := managedagents.ReapExpiredWorkerWorkWithContext(r.Context(), s.store, input)
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
	work, err := s.getWorkerWorkForRequest(r, r.PathValue("work_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	response := diagnoseWorkerWorkState(r.Context(), s.store, work, time.Now().UTC())
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) cancelWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CancelWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	work, err := managedagents.CancelWorkerWorkWithContext(r.Context(), s.store, r.PathValue("work_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) requeueWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.RequeueWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	work, err := managedagents.RequeueWorkerWorkWithContext(r.Context(), s.store, r.PathValue("work_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, work)
}

func diagnoseWorkerWorkState(ctx context.Context, store managedagents.Store, work managedagents.WorkerWork, now time.Time) workerWorkDiagnoseResponse {
	response := workerWorkDiagnoseResponse{Work: work}
	if strings.TrimSpace(work.WorkerID) != "" {
		worker, err := managedagents.GetWorkerWithContext(ctx, store, work.WorkerID)
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
		response.Actions = append(response.Actions, "run: bin/tma work requeue --work "+work.ID)
	case managedagents.WorkerWorkStatusCanceled:
		response.Reasons = append(response.Reasons, "work was canceled")
		response.Actions = append(response.Actions, "no worker result is expected; run: bin/tma work requeue --work "+work.ID+" if the operation should be retried")
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
	work, err := managedagents.PollWorkerWorkWithContext(r.Context(), s.store, r.PathValue("worker_id"), managedagents.PollWorkerWorkInput{
		LeaseSeconds: leaseSeconds,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"work": work})
}

func (s *Server) ackWorkerWork(w http.ResponseWriter, r *http.Request) {
	work, err := managedagents.AckWorkerWorkWithContext(r.Context(), s.store, r.PathValue("worker_id"), r.PathValue("work_id"))
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
	work, err := managedagents.HeartbeatWorkerWorkWithContext(r.Context(), s.store, r.PathValue("worker_id"), r.PathValue("work_id"), input)
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
	work, err := managedagents.CompleteWorkerWorkWithContext(r.Context(), s.store, r.PathValue("worker_id"), r.PathValue("work_id"), input)
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
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	input.CreatedBy = requestActorID(r, input.CreatedBy)
	object, err := managedagents.CreateObjectRefWithContext(r.Context(), s.store, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, object)
}

func (s *Server) getObjectRef(w http.ResponseWriter, r *http.Request) {
	object, err := s.getObjectRefForRequest(r, r.PathValue("object_ref_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) downloadObjectRef(w http.ResponseWriter, r *http.Request) {
	objectRef, err := s.getObjectRefForRequest(r, r.PathValue("object_ref_id"))
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
	count, err := managedagents.CountSessionArtifactsByObjectRefWithContext(r.Context(), s.store, objectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	if count > 0 {
		writeError(w, fmt.Errorf("%w: object ref is still referenced by %d artifact(s)", managedagents.ErrConflict, count))
		return
	}
	if err := managedagents.DeleteObjectRefWithContext(r.Context(), s.store, objectRefID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSessionArtifact(w http.ResponseWriter, r *http.Request) {
	if err := managedagents.DeleteSessionArtifactWithContext(r.Context(), s.store, r.PathValue("session_id"), r.PathValue("artifact_id")); err != nil {
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
		session, err := s.getSessionForRequest(r, sessionID)
		if err != nil || session.WorkspaceID != objectRef.WorkspaceID {
			return false
		}
		if principal, ok := PrincipalFromRequest(r); ok {
			return authorizeSessionPrincipal(principal, session) == nil
		}
		return true
	}
	if objectRef.Visibility == managedagents.ObjectVisibilitySession {
		if sessionID == "" {
			return false
		}
		artifacts, err := managedagents.ListSessionArtifactsWithContext(r.Context(), s.store, sessionID)
		if err != nil {
			return false
		}
		if principal, ok := PrincipalFromRequest(r); ok {
			session, err := s.getSessionForRequest(r, sessionID)
			if err != nil || authorizeSessionPrincipal(principal, session) != nil {
				return false
			}
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
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	if principal, ok := PrincipalFromRequest(r); ok {
		if strings.TrimSpace(input.OwnerType) == "" {
			input.OwnerType = managedagents.AgentOwnerUser
		}
		switch strings.ToLower(strings.TrimSpace(input.OwnerType)) {
		case managedagents.AgentOwnerUser:
			input.OwnerType = managedagents.AgentOwnerUser
			if input.OwnerID == "" {
				input.OwnerID = principal.OwnerID
			}
			if input.OwnerID != principal.OwnerID {
				writeError(w, fmt.Errorf("%w: personal Agent must belong to the current user", managedagents.ErrForbidden))
				return
			}
			if input.Visibility == "" {
				input.Visibility = managedagents.AgentVisibilityPrivate
			}
		case managedagents.AgentOwnerWorkspace:
			if !principal.HasRole(RoleOperator) {
				writeError(w, fmt.Errorf("%w: operator role required to create Workspace-shared Agents", managedagents.ErrForbidden))
				return
			}
			input.OwnerID = input.WorkspaceID
			if input.Visibility == "" {
				input.Visibility = managedagents.AgentVisibilityWorkspace
			}
		}
	}
	if input.LLMProvider == "" {
		input.LLMProvider = s.defaultLLMProvider
	}
	if input.LLMModel == "" && input.Model == "" {
		input.LLMModel = s.defaultLLMModel
	}
	if err := validateAgentToolPermissionRules(input.Tools); err != nil {
		writeError(w, err)
		return
	}
	if input.Skills != nil {
		normalized, err := s.validateAgentSkills(agentSkillValidationContext(r.Context(), input.WorkspaceID, input.OwnerType, input.OwnerID), input.WorkspaceID, input.Skills)
		if err != nil {
			writeError(w, err)
			return
		}
		input.Skills = normalized
	}
	if input.MCP != nil {
		normalized, err := s.pinAgentMCPBindings(r, input.WorkspaceID, input.MCP)
		if err != nil {
			writeMCPRegistryError(w, err)
			return
		}
		input.MCP = normalized
	}

	agent, err := managedagents.CreateAgentWithContext(r.Context(), s.store, input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	workspaceID := requestWorkspaceID(r, managedagents.DefaultWorkspaceID)
	if _, err := s.ensureDefaultAgentForRequest(r, workspaceID); err != nil {
		writeError(w, err)
		return
	}
	agents, err := s.listAgentsForRequest(r)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": nonNilSlice(agents)})
}

func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	current, err := s.getAgentForRequest(r, r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	var request agentUpdateRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	input := managedagents.UpdateAgentInput{
		AgentID: r.PathValue("agent_id"),
		Name:    current.Name,
		System:  current.ConfigVersion.System,
	}
	if request.Name != nil {
		input.Name = strings.TrimSpace(*request.Name)
	}
	if request.LLMProvider != nil {
		input.LLMProvider = strings.TrimSpace(*request.LLMProvider)
	}
	if request.LLMModel != nil {
		input.LLMModel = strings.TrimSpace(*request.LLMModel)
	}
	if request.Model != nil && request.LLMModel == nil {
		input.LLMModel = strings.TrimSpace(*request.Model)
	}
	if request.System != nil {
		input.System = *request.System
	}
	if request.Tools != nil {
		if validateErr := validateAgentToolPermissionRules(*request.Tools); validateErr != nil {
			writeError(w, validateErr)
			return
		}
		input.Tools = *request.Tools
	}
	if request.MCP != nil {
		normalized, validateErr := s.pinAgentMCPBindings(r, current.WorkspaceID, *request.MCP)
		if validateErr != nil {
			writeMCPRegistryError(w, validateErr)
			return
		}
		input.MCP = normalized
	}
	if request.Skills != nil {
		normalized, validateErr := s.validateAgentSkills(agentSkillValidationContext(r.Context(), current.WorkspaceID, current.OwnerType, current.OwnerID), current.WorkspaceID, *request.Skills)
		if validateErr != nil {
			writeError(w, validateErr)
			return
		}
		input.Skills = normalized
	}

	agent, err := managedagents.UpdateAgentWithContext(r.Context(), s.store, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := s.getAgentForRequest(r, r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) getDefaultAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := s.ensureDefaultAgentForRequest(r, requestWorkspaceID(r, managedagents.DefaultWorkspaceID))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) ensureDefaultAgentForRequest(r *http.Request, workspaceID string) (managedagents.Agent, error) {
	if principal, ok := PrincipalFromRequest(r); ok {
		input := managedagents.PersonalGeneralAgentInput(workspaceID, principal.OwnerID, s.defaultLLMProvider, s.defaultLLMModel)
		agent, err := managedagents.GetAgentWithContext(r.Context(), s.store, input.ID)
		if err == nil {
			return agent, nil
		}
		if !errors.Is(err, managedagents.ErrNotFound) && !errors.Is(err, managedagents.ErrForbidden) {
			return managedagents.Agent{}, err
		}
		return managedagents.EnsureAgentWithContext(r.Context(), s.store, input)
	}
	return s.ensureDefaultAgentForWorkspace(r.Context(), workspaceID)
}

func (s *Server) ensureDefaultAgentForWorkspace(ctx context.Context, workspaceID string) (managedagents.Agent, error) {
	input := managedagents.BuiltinGeneralAgentInputForWorkspace(workspaceID, s.defaultLLMProvider, s.defaultLLMModel)
	agent, err := managedagents.GetAgentWithContext(ctx, s.store, input.ID)
	if err == nil {
		return agent, nil
	}
	if !errors.Is(err, managedagents.ErrNotFound) {
		return managedagents.Agent{}, err
	}
	return managedagents.EnsureAgentWithContext(ctx, s.store, input)
}

func (s *Server) listAgentConfigVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := managedagents.ListAgentConfigVersionsWithContext(r.Context(), s.store, r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config_versions": nonNilSlice(versions)})
}

func (s *Server) createAgentConfigVersion(w http.ResponseWriter, r *http.Request) {
	current, err := s.getAgentForRequest(r, r.PathValue("agent_id"))
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
		if validateErr := validateAgentToolPermissionRules(*request.Tools); validateErr != nil {
			writeError(w, validateErr)
			return
		}
		next.Tools = cloneJSONRaw(*request.Tools)
	}
	if request.MCP != nil {
		normalized, validateErr := s.pinAgentMCPBindings(r, current.WorkspaceID, *request.MCP)
		if validateErr != nil {
			writeMCPRegistryError(w, validateErr)
			return
		}
		next.MCP = cloneJSONRaw(normalized)
	}
	if request.Skills != nil {
		normalizedSkills, validateErr := s.validateAgentSkills(agentSkillValidationContext(r.Context(), current.WorkspaceID, current.OwnerType, current.OwnerID), current.WorkspaceID, *request.Skills)
		if validateErr != nil {
			writeError(w, validateErr)
			return
		}
		next.Skills = normalizedSkills
	}

	agent, err := managedagents.CreateAgentConfigVersionWithContext(r.Context(), s.store, managedagents.CreateAgentConfigVersionInput{
		AgentID:     current.ID,
		LLMProvider: next.LLMProvider,
		LLMModel:    next.LLMModel,
		System:      next.System,
		Tools:       next.Tools,
		MCP:         next.MCP,
		Skills:      next.Skills,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) rollbackAgentConfigVersion(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agent_id")
	sourceVersion, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || sourceVersion < 1 {
		err = fmt.Errorf("%w: version must be a positive integer", managedagents.ErrInvalid)
		s.recordOperatorAction(r, "", "agent.config.rollback", "agent", agentID, err, map[string]any{"source_version": r.PathValue("version")})
		writeError(w, err)
		return
	}

	current, err := s.getAgentForRequest(r, agentID)
	if err != nil {
		s.recordOperatorAction(r, "", "agent.config.rollback", "agent", agentID, err, map[string]any{"source_version": sourceVersion})
		writeError(w, err)
		return
	}
	if sourceVersion >= current.CurrentConfigVersion {
		err = fmt.Errorf("%w: rollback source must be older than current version %d", managedagents.ErrInvalid, current.CurrentConfigVersion)
		s.recordOperatorAction(r, "", "agent.config.rollback", "agent", agentID, err, map[string]any{"source_version": sourceVersion, "previous_version": current.CurrentConfigVersion})
		writeError(w, err)
		return
	}

	versions, err := managedagents.ListAgentConfigVersionsWithContext(r.Context(), s.store, agentID)
	if err != nil {
		s.recordOperatorAction(r, "", "agent.config.rollback", "agent", agentID, err, map[string]any{"source_version": sourceVersion, "previous_version": current.CurrentConfigVersion})
		writeError(w, err)
		return
	}
	var source *managedagents.AgentConfigVersion
	for index := range versions {
		if versions[index].Version == sourceVersion {
			source = &versions[index]
			break
		}
	}
	if source == nil {
		err = fmt.Errorf("%w: agent config version %s#%d", managedagents.ErrNotFound, agentID, sourceVersion)
		s.recordOperatorAction(r, "", "agent.config.rollback", "agent", agentID, err, map[string]any{"source_version": sourceVersion, "previous_version": current.CurrentConfigVersion})
		writeError(w, err)
		return
	}

	rolledBack, err := managedagents.CreateAgentConfigVersionWithContext(r.Context(), s.store, managedagents.CreateAgentConfigVersionInput{
		AgentID:     agentID,
		LLMProvider: source.LLMProvider,
		LLMModel:    source.LLMModel,
		System:      source.System,
		Tools:       cloneJSONRaw(source.Tools),
		MCP:         cloneJSONRaw(source.MCP),
		Skills:      cloneJSONRaw(source.Skills),
	})
	details := map[string]any{
		"source_version":   sourceVersion,
		"previous_version": current.CurrentConfigVersion,
	}
	if err == nil {
		details["new_version"] = rolledBack.CurrentConfigVersion
	}
	s.recordOperatorAction(r, "", "agent.config.rollback", "agent", agentID, err, details)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agentConfigRollbackResponse{
		Agent:           rolledBack,
		PreviousVersion: current.CurrentConfigVersion,
		SourceVersion:   sourceVersion,
		NewVersion:      rolledBack.CurrentConfigVersion,
	})
}

func cloneJSONRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}

func (s *Server) validateAgentSkills(ctx context.Context, workspaceID string, raw json.RawMessage) (json.RawMessage, error) {
	registry, ok := s.store.(skills.Registry)
	if !ok {
		return nil, fmt.Errorf("%w: skill registry is unavailable", managedagents.ErrInvalid)
	}
	result, err := skills.ResolveRegistry(ctx, registry, fallbackString(workspaceID, managedagents.DefaultWorkspaceID), raw, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	for _, resolved := range result.Skills {
		if resolved.Skill.Status != skills.StatusActive {
			return nil, fmt.Errorf("%w: skill %q is archived", managedagents.ErrInvalid, resolved.Skill.Identifier)
		}
	}
	normalized, err := json.Marshal(result.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: encode skills config: %v", managedagents.ErrInvalid, err)
	}
	return normalized, nil
}

func agentSkillValidationContext(ctx context.Context, workspaceID string, ownerType string, ownerID string) context.Context {
	if ownerType != managedagents.AgentOwnerUser {
		ownerID = "__workspace_shared_agent__"
	}
	scoped, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: workspaceID, OwnerID: ownerID})
	if err != nil {
		return ctx
	}
	return scoped
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateEnvironmentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)

	environment, err := managedagents.CreateEnvironmentWithContext(r.Context(), s.store, input)
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
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	input.OwnerID = requestOwnerID(r, input.OwnerID)
	input.CreatedBy = requestActorID(r, input.CreatedBy)
	if input.ParentSessionID != "" {
		if err := s.authorizeSessionID(r, input.ParentSessionID); err != nil {
			writeError(w, err)
			return
		}
	}
	if input.AgentID == "" && input.Agent == "" {
		agent, err := s.ensureDefaultAgentForRequest(r, input.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		input.AgentID = agent.ID
	}

	session, err := managedagents.CreateSessionWithContext(r.Context(), s.store, input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	limit, err := optionalPositiveInt(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid limit: %v", managedagents.ErrInvalid, err))
		return
	}
	includeArchived, err := optionalBool(r.URL.Query().Get("include_archived"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid include_archived: %v", managedagents.ErrInvalid, err))
		return
	}

	sessions, err := s.listSessionsForRequest(r, managedagents.ListSessionsInput{
		WorkspaceID:     requestWorkspaceID(r, r.URL.Query().Get("workspace_id")),
		OwnerID:         requestSessionOwnerFilter(r),
		Status:          r.URL.Query().Get("status"),
		IncludeArchived: includeArchived != nil && *includeArchived,
		Limit:           limit,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": nonNilSlice(sessions)})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.getSessionForRequest(r, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	setSessionRuntimeSettingsETag(w, session.RuntimeSettingsRevision)
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) updateSessionMetadata(w http.ResponseWriter, r *http.Request) {
	var input managedagents.UpdateSessionMetadataInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	sessionID := r.PathValue("session_id")
	session, err := managedagents.UpdateSessionMetadataWithContext(r.Context(), s.store, sessionID, input)
	details := map[string]any{}
	if input.Pinned != nil {
		details["pinned"] = *input.Pinned
	}
	if input.Tags != nil {
		details["tags"] = *input.Tags
	}
	s.recordOperatorAction(r, sessionID, "session.metadata.update", "session", sessionID, err, details)
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
	input.CreatedBy = requestActorID(r, input.CreatedBy)
	artifact, err := managedagents.CreateSessionArtifactWithContext(r.Context(), s.store, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, artifact)
}

func (s *Server) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	artifacts, err := managedagents.ListSessionArtifactsWithContext(r.Context(), s.store, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": nonNilSlice(artifacts)})
}

func (s *Server) downloadSessionArtifact(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	artifactID := r.PathValue("artifact_id")

	artifact, err := managedagents.GetSessionArtifactWithContext(r.Context(), s.store, sessionID, artifactID)
	if err != nil {
		writeError(w, err)
		return
	}

	objectRef, err := s.getObjectRefForRequest(r, artifact.ObjectRefID)
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
	session, err := s.getSessionForRequest(r, sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "artifact upload requires multipart/form-data"})
		return
	}
	if r.ContentLength > maxArtifactUploadBytes+1024 {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "artifact upload exceeds size limit"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArtifactUploadBytes+1024)
	if err := r.ParseMultipartForm(maxArtifactUploadBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "artifact upload exceeds size limit"})
			return
		}
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

	objectRef, err := managedagents.CreateObjectRefWithContext(r.Context(), s.store, managedagents.CreateObjectRefInput{
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
		CreatedBy:       requestActorID(r, fallbackString(r.FormValue("created_by"), "system")),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		name = safeArtifactFileName(header.Filename)
	}
	artifact, err := managedagents.CreateSessionArtifactWithContext(r.Context(), s.store, managedagents.CreateSessionArtifactInput{
		SessionID:     sessionID,
		EnvironmentID: r.FormValue("environment_id"),
		ObjectRefID:   objectRef.ID,
		TurnID:        r.FormValue("turn_id"),
		ToolCallID:    r.FormValue("tool_call_id"),
		Name:          name,
		Description:   r.FormValue("description"),
		ArtifactType:  fallbackString(r.FormValue("artifact_type"), managedagents.ArtifactTypeFile),
		Metadata:      metadata,
		CreatedBy:     requestActorID(r, fallbackString(r.FormValue("created_by"), "system")),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"object_ref":     objectRef,
		"artifact":       artifact,
		"workspace_path": capability.SessionArtifactSandboxPath(artifact),
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
	expectedRevision, err := parseSessionRuntimeSettingsIfMatch(r.Header.Get("If-Match"))
	if err != nil {
		writeError(w, err)
		return
	}
	var request sessionRuntimeSettingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	request.ExpectedRevision = expectedRevision
	session, err := s.getSessionForRequest(r, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	session, err = s.applySessionRuntimeSettingsPatch(r.Context(), session, request)
	if err != nil {
		writeError(w, err)
		return
	}
	setSessionRuntimeSettingsETag(w, session.RuntimeSettingsRevision)
	writeJSON(w, http.StatusOK, session)
}

func setSessionRuntimeSettingsETag(w http.ResponseWriter, revision int64) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(revision, 10)))
}

func parseSessionRuntimeSettingsIfMatch(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%w: If-Match header is required", managedagents.ErrInvalid)
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return 0, fmt.Errorf("%w: If-Match must be a quoted runtime settings revision", managedagents.ErrInvalid)
	}
	revision, err := strconv.ParseInt(unquoted, 10, 64)
	if err != nil || revision <= 0 {
		return 0, fmt.Errorf("%w: If-Match must contain a positive runtime settings revision", managedagents.ErrInvalid)
	}
	return revision, nil
}

func (s *Server) upgradeSessionAgentConfig(w http.ResponseWriter, r *http.Request) {
	request := sessionConfigUpgradeRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	toCurrent := request.ToVersion == 0
	if request.ToCurrent != nil {
		toCurrent = *request.ToCurrent
	}
	result, err := managedagents.UpgradeSessionAgentConfigWithContext(r.Context(), s.store, r.PathValue("session_id"), managedagents.UpgradeSessionAgentConfigInput{
		ToCurrent:     toCurrent,
		TargetVersion: request.ToVersion,
		UpdatedBy:     request.UpdatedBy,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listSessionInterventions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	interventions, err := managedagents.ListSessionInterventionsWithContext(r.Context(), s.store, r.PathValue("session_id"), status)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interventions": nonNilSlice(interventions)})
}

func (s *Server) approveSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusApproved)
}

func (s *Server) rejectSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusRejected)
}

func (s *Server) respondSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusAnswered)
}

func (s *Server) skipSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusSkipped)
}

func (s *Server) cancelSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusCanceled)
}

func (s *Server) decideSessionIntervention(w http.ResponseWriter, r *http.Request, status string) {
	var request interventionDecisionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if status == managedagents.InterventionStatusApproved {
		if err := s.applySelectedPermissionRule(r.Context(), r.PathValue("session_id"), r.PathValue("turn_id"), r.PathValue("call_id"), request.Response); err != nil {
			writeError(w, err)
			return
		}
	}
	result, err := managedagents.DecideSessionInterventionWithContext(r.Context(), s.store, r.PathValue("session_id"), managedagents.DecideSessionInterventionInput{
		TurnID:         r.PathValue("turn_id"),
		CallID:         r.PathValue("call_id"),
		Status:         status,
		DecisionReason: request.Reason,
		Response:       append(json.RawMessage(nil), request.Response...),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	resume := result.Intervention
	shouldSchedule, err := s.shouldScheduleInterventionResume(r.Context(), result)
	if err != nil {
		writeError(w, err)
		return
	}
	if !shouldSchedule {
		writeJSON(w, http.StatusOK, result)
		return
	}
	if err := s.runner.StartTurn(context.Background(), runner.TurnRequest{
		SessionID:          resume.SessionID,
		TurnID:             resume.TurnID,
		ResumeIntervention: &resume,
		Scope:              accessScopeFromRequestOrSession(r, result.Intervention.SessionID, s.store),
	}); err != nil && !errors.Is(err, runner.ErrTurnAlreadyRunning) {
		s.logger.Error("intervention resume scheduling failed",
			"session_id", resume.SessionID,
			"turn_id", resume.TurnID,
			"call_id", resume.CallID,
			"status", resume.Status,
			"error", err,
		)
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) shouldScheduleInterventionResume(ctx context.Context, result managedagents.DecideSessionInterventionResult) (bool, error) {
	session, err := managedagents.GetSessionWithContext(ctx, s.store, result.Intervention.SessionID)
	if err != nil {
		return false, err
	}
	if session.Status != managedagents.SessionStatusRunning {
		return false, nil
	}
	if len(result.Events) > 0 {
		return true, nil
	}
	pending, err := managedagents.ListSessionInterventionsWithContext(ctx, s.store, result.Intervention.SessionID, managedagents.InterventionStatusPending)
	if err != nil {
		return false, err
	}
	for _, intervention := range pending {
		if intervention.TurnID == result.Intervention.TurnID {
			return false, nil
		}
	}
	return true, nil
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
	session, err := managedagents.ArchiveSessionWithContext(r.Context(), s.store, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) restoreSession(w http.ResponseWriter, r *http.Request) {
	session, err := managedagents.RestoreSessionWithContext(r.Context(), s.store, r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := managedagents.DeleteSessionWithContext(r.Context(), s.store, r.PathValue("session_id")); err != nil {
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

	sessionID := r.PathValue("session_id")
	inputs, err := s.normalizeLatestMessageEvents(r.Context(), sessionID, request)
	if err != nil {
		writeError(w, err)
		return
	}

	var events []managedagents.Event
	session, getErr := s.getSessionForRequest(r, sessionID)
	if getErr != nil {
		writeError(w, getErr)
		return
	}
	if session.ParentSessionID != "" && containsEventType(inputs, managedagents.EventUserMessage) {
		switch {
		case len(inputs) == 1 && inputs[0].Type == managedagents.EventUserMessage:
			events, err = managedagents.StartSubagentTurnWithContext(r.Context(), s.store, managedagents.StartSubagentTurnInput{
				SessionID: sessionID,
				Payload:   inputs[0].Payload,
				Limits:    s.subagentPolicy.storeLimits(),
			})
		case len(inputs) == 2 && session.Status == managedagents.SessionStatusRunning && inputs[0].Type == managedagents.EventUserInterrupt && inputs[1].Type == managedagents.EventUserMessage:
			events, err = managedagents.AppendEventsWithContext(r.Context(), s.store, sessionID, inputs)
		default:
			err = fmt.Errorf("%w: subagent user.message must start through active admission", managedagents.ErrInvalid)
		}
	} else {
		events, err = managedagents.AppendEventsWithContext(r.Context(), s.store, sessionID, inputs)
	}
	if err != nil {
		var violation managedagents.SubagentQuotaViolation
		if errors.As(err, &violation) {
			if len(inputs) == 1 && inputs[0].Type == managedagents.EventUserMessage {
				queued, queueErr := managedagents.EnqueueSubagentStartWithContext(r.Context(), s.store, managedagents.EnqueueSubagentStartInput{
					SessionID: sessionID, ParentSessionID: session.ParentSessionID,
					Payload: inputs[0].Payload, Limits: s.subagentPolicy.storeLimits(),
				})
				if queueErr == nil {
					writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "queue_request": queued})
					return
				}
				if errors.As(queueErr, &violation) {
					err = queueErr
				} else {
					writeError(w, queueErr)
					return
				}
			}
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{"type": violation.Type, "message": violation.Message},
				"state": violation.State,
			})
			return
		}
		if reminderEvents, reminderErr := s.appendApprovalReminderIfWaiting(r.Context(), sessionID, request.Events); reminderErr == nil && len(reminderEvents) > 0 {
			s.logEvents("session approval reminder appended", reminderEvents)
			writeJSON(w, http.StatusAccepted, map[string]any{"events": reminderEvents})
			return
		}
		writeError(w, err)
		return
	}

	// Store 先把事件和状态写入数据库；后台执行只基于已经落库的事件启动。
	s.logEvents("session events appended", events)
	s.dispatchRunnerEvents(r, sessionID, events)
	writeJSON(w, http.StatusCreated, map[string]any{"events": events})
}

func containsEventType(inputs []managedagents.AppendEventInput, eventType string) bool {
	for _, input := range inputs {
		if input.Type == eventType {
			return true
		}
	}
	return false
}

func (s *Server) normalizeLatestMessageEvents(ctx context.Context, sessionID string, request appendEventsRequest) ([]managedagents.AppendEventInput, error) {
	if !request.PreferLatest || len(request.Events) != 1 || request.Events[0].Type != managedagents.EventUserMessage {
		return request.Events, nil
	}

	session, err := managedagents.GetSessionWithContext(ctx, s.store, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != managedagents.SessionStatusRunning {
		return request.Events, nil
	}

	pending, err := managedagents.ListSessionInterventionsWithContext(ctx, s.store, sessionID, managedagents.InterventionStatusPending)
	if err != nil {
		return nil, err
	}
	if len(pending) > 0 {
		return request.Events, nil
	}

	return []managedagents.AppendEventInput{
		{Type: managedagents.EventUserInterrupt},
		{
			Type:    request.Events[0].Type,
			Payload: append(json.RawMessage(nil), request.Events[0].Payload...),
		},
	}, nil
}

func (s *Server) appendApprovalReminderIfWaiting(ctx context.Context, sessionID string, inputs []managedagents.AppendEventInput) ([]managedagents.Event, error) {
	if len(inputs) != 1 || inputs[0].Type != managedagents.EventUserMessage {
		return nil, nil
	}
	session, err := managedagents.GetSessionWithContext(ctx, s.store, sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	pending, err := managedagents.ListSessionInterventionsWithContext(ctx, s.store, sessionID, managedagents.InterventionStatusPending)
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
		eventType := managedagents.EventRuntimeToolInterventionRequired
		message := "Tool call is still waiting for approval."
		if intervention.Kind == managedagents.InterventionKindClarification || intervention.Kind == managedagents.InterventionKindUploadRequest {
			eventType = managedagents.EventRuntimeHumanInputRequired
			message = "The task is still waiting for requested user input."
		}
		payload, err := json.Marshal(map[string]any{
			"turn_id": intervention.TurnID,
			"message": message,
			"data": map[string]any{
				"id":                intervention.CallID,
				"identifier":        intervention.ToolIdentifier,
				"api_name":          intervention.APIName,
				"arguments":         rawJSONValue(intervention.Arguments),
				"kind":              intervention.Kind,
				"request":           rawJSONValue(intervention.Request),
				"intervention_mode": intervention.InterventionMode,
				"reason":            intervention.Reason,
			},
		})
		if err != nil {
			return nil, err
		}
		events = append(events, managedagents.AppendEventInput{
			Type:    eventType,
			Payload: payload,
		})
	}
	return managedagents.AppendEventsWithContext(ctx, s.store, sessionID, events)
}

func approvalReminderPayload(pending []managedagents.SessionIntervention) json.RawMessage {
	turnID := pending[0].TurnID
	lines := []string{"This task is waiting for human input before it can continue."}
	for _, intervention := range pending {
		if intervention.Kind == managedagents.InterventionKindClarification || intervention.Kind == managedagents.InterventionKindUploadRequest {
			var request map[string]any
			_ = json.Unmarshal(intervention.Request, &request)
			question, _ := request["question"].(string)
			if strings.TrimSpace(question) == "" {
				question = "additional information requested"
			}
			lines = append(lines, fmt.Sprintf("- Question: %s (call=%s)", question, intervention.CallID))
		} else {
			lines = append(lines, fmt.Sprintf("- Approval: %s call=%s", tools.ModelToolName(intervention.ToolIdentifier, intervention.APIName), intervention.CallID))
		}
	}
	payload, err := json.Marshal(map[string]any{
		"protocol_version": managedagents.AgentLoopMessageProtocolVersion,
		"content_format":   "blocks",
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
	scope := accessScopeFromRequestOrSession(r, sessionID, s.store)
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
				Scope:        scope,
			}); err != nil {
				reason := err.Error()
				s.logger.Error("runner start turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"event_id", event.ID,
					"event_seq", event.Seq,
					"error", err,
				)
				failedEvents, failErr := managedagents.FailSessionTurnWithContext(r.Context(), s.store, sessionID, turnID, reason)
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

func accessScopeFromRequestOrSession(r *http.Request, sessionID string, store managedagents.Store) managedagents.AccessScope {
	if scope, ok := requestAccessScope(r); ok {
		return scope
	}
	session, err := managedagents.GetSessionWithContext(r.Context(), store, sessionID)
	if err != nil {
		return managedagents.AccessScope{}
	}
	return managedagents.AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID}
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

	events, err := managedagents.ListEventsWithContext(r.Context(), s.store, r.PathValue("session_id"), afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": nonNilSlice(events)})
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
	// Store 以持久化事件序号为游标统一补历史和追实时事件，避免查询与订阅之间出现空窗。
	events, cancel, err := managedagents.SubscribeEventsWithContext(r.Context(), s.store, sessionID, afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cancel()
	s.logger.Info("sse stream opened",
		"session_id", sessionID,
		"after_seq", afterSeq,
	)
	defer s.logger.Info("sse stream closed",
		"session_id", sessionID,
		"after_seq", afterSeq,
	)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

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
			afterSeq = event.Seq
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
