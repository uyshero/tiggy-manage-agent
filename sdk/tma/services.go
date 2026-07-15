package tma

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type AgentsService struct{ client *Client }

func (s *AgentsService) Create(ctx context.Context, request CreateAgentRequest) (Agent, error) {
	var agent Agent
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/agents", request, &agent)
	return agent, err
}

func (s *AgentsService) Default(ctx context.Context) (Agent, error) {
	var agent Agent
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/agents/default", nil, &agent)
	return agent, err
}

func (s *AgentsService) Import(ctx context.Context, request AgentImportRequest) (Agent, error) {
	var agent Agent
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/agents/import", request, &agent)
	return agent, err
}

func (s *AgentsService) Get(ctx context.Context, agentID string) (Agent, error) {
	var agent Agent
	err := s.client.DoJSON(ctx, http.MethodGet, agentPath(agentID), nil, &agent)
	return agent, err
}

func (s *AgentsService) List(ctx context.Context) ([]Agent, error) {
	var response struct {
		Agents []Agent `json:"agents"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/agents", nil, &response)
	return response.Agents, err
}

func (s *AgentsService) Update(ctx context.Context, agentID string, request UpdateAgentRequest) (Agent, error) {
	var agent Agent
	err := s.client.DoJSON(ctx, http.MethodPatch, agentPath(agentID), request, &agent)
	return agent, err
}

func (s *AgentsService) Export(ctx context.Context, agentID string) (AgentExportDocument, error) {
	var document AgentExportDocument
	err := s.client.DoJSON(ctx, http.MethodGet, agentPath(agentID)+"/export", nil, &document)
	return document, err
}

func (s *AgentsService) ListConfigVersions(ctx context.Context, agentID string) ([]AgentConfigVersion, error) {
	var response struct {
		ConfigVersions []AgentConfigVersion `json:"config_versions"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, agentPath(agentID)+"/config-versions", nil, &response)
	return response.ConfigVersions, err
}

func (s *AgentsService) CreateConfigVersion(ctx context.Context, agentID string, request CreateAgentConfigVersionRequest) (Agent, error) {
	var agent Agent
	err := s.client.DoJSON(ctx, http.MethodPost, agentPath(agentID)+"/config-versions", request, &agent)
	return agent, err
}

func (s *AgentsService) Rollback(ctx context.Context, agentID string, version int32) (AgentConfigRollbackResponse, error) {
	var result AgentConfigRollbackResponse
	path := agentPath(agentID) + "/config-versions/" + strconv.FormatInt(int64(version), 10) + "/rollback"
	err := s.client.DoJSON(ctx, http.MethodPost, path, struct{}{}, &result)
	return result, err
}

func (s *AgentsService) ToolingHealth(ctx context.Context, agentID string, request ToolingHealthRequest) (ToolingHealthResponse, error) {
	var result ToolingHealthResponse
	err := s.client.DoJSON(ctx, http.MethodPost, agentPath(agentID)+"/tooling-health", request, &result)
	return result, err
}

func (s *AgentsService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2/agents", path), request, response)
}

func agentPath(agentID string) string { return "/v2/agents/" + url.PathEscape(agentID) }

type EnvironmentsService struct{ client *Client }

func (s *EnvironmentsService) Create(ctx context.Context, request CreateEnvironmentRequest) (Environment, error) {
	var environment Environment
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/environments", request, &environment)
	return environment, err
}

func (s *EnvironmentsService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2/environments", path), request, response)
}

type LLMService struct{ client *Client }

func (s *LLMService) ListProviders(ctx context.Context) ([]LLMProvider, error) {
	var response struct {
		Providers []LLMProvider `json:"providers"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/llm-providers", nil, &response)
	return response.Providers, err
}

func (s *LLMService) GetProvider(ctx context.Context, providerID string) (LLMProvider, error) {
	var provider LLMProvider
	err := s.client.DoJSON(ctx, http.MethodGet, llmProviderPath(providerID), nil, &provider)
	return provider, err
}

func (s *LLMService) CreateProvider(ctx context.Context, request CreateLLMProviderRequest) (LLMProvider, error) {
	var provider LLMProvider
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/llm-providers", request, &provider)
	return provider, err
}

func (s *LLMService) UpdateProvider(ctx context.Context, providerID string, expectedRevision int64, request UpdateLLMProviderRequest) (LLMProvider, error) {
	var provider LLMProvider
	err := s.client.DoJSONWithHeaders(ctx, http.MethodPatch, llmProviderPath(providerID), revisionHeaders(expectedRevision), request, &provider)
	return provider, err
}

func (s *LLMService) SetProviderEnabled(ctx context.Context, providerID string, expectedRevision int64, enabled bool) (LLMProvider, error) {
	action := "disable"
	if enabled {
		action = "enable"
	}
	var provider LLMProvider
	err := s.client.DoJSONWithHeaders(ctx, http.MethodPost, llmProviderPath(providerID)+"/"+action, revisionHeaders(expectedRevision), struct{}{}, &provider)
	return provider, err
}

func (s *LLMService) DeleteProvider(ctx context.Context, providerID string, expectedRevision int64) error {
	return s.client.DoJSONWithHeaders(ctx, http.MethodDelete, llmProviderPath(providerID), revisionHeaders(expectedRevision), nil, nil)
}

func (s *LLMService) TestProvider(ctx context.Context, providerID string) (LLMDiagnosticResult, error) {
	var result LLMDiagnosticResult
	err := s.client.DoJSON(ctx, http.MethodPost, llmProviderPath(providerID)+"/test", struct{}{}, &result)
	return result, err
}

func (s *LLMService) ListModels(ctx context.Context, providerID string) ([]LLMModel, error) {
	path := "/v2/llm-models"
	if strings.TrimSpace(providerID) != "" {
		path += "?provider_id=" + url.QueryEscape(providerID)
	}
	var response struct {
		Models []LLMModel `json:"models"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Models, err
}

func (s *LLMService) CreateModel(ctx context.Context, request PutLLMModelRequest) (LLMModel, error) {
	var model LLMModel
	headers := make(http.Header)
	headers.Set("If-None-Match", "*")
	err := s.client.DoJSONWithHeaders(ctx, http.MethodPost, "/v2/llm-models", headers, request, &model)
	return model, err
}

func (s *LLMService) UpdateModel(ctx context.Context, expectedRevision int64, request PutLLMModelRequest) (LLMModel, error) {
	var model LLMModel
	err := s.client.DoJSONWithHeaders(ctx, http.MethodPost, "/v2/llm-models", revisionHeaders(expectedRevision), request, &model)
	return model, err
}

func (s *LLMService) DeleteModel(ctx context.Context, providerID string, model string, expectedRevision int64) error {
	path := "/v2/llm-models/" + url.PathEscape(providerID) + "/" + url.PathEscape(model)
	return s.client.DoJSONWithHeaders(ctx, http.MethodDelete, path, revisionHeaders(expectedRevision), nil, nil)
}

func (s *LLMService) TestModel(ctx context.Context, providerID string, model string) (LLMDiagnosticResult, error) {
	path := "/v2/llm-models/" + url.PathEscape(providerID) + "/" + url.PathEscape(model) + "/test"
	var result LLMDiagnosticResult
	err := s.client.DoJSON(ctx, http.MethodPost, path, struct{}{}, &result)
	return result, err
}

func (s *LLMService) Usage(ctx context.Context, query LLMUsageQuery) (LLMUsageAggregateReport, error) {
	values := url.Values{}
	values.Set("workspace_id", query.WorkspaceID)
	values.Set("provider_id", query.ProviderID)
	values.Set("model", query.Model)
	values.Set("status", query.Status)
	values.Set("group_by", query.GroupBy)
	if query.From != nil {
		values.Set("from", query.From.Format(time.RFC3339))
	}
	if query.To != nil {
		values.Set("to", query.To.Format(time.RFC3339))
	}
	for key := range values {
		if values.Get(key) == "" {
			values.Del(key)
		}
	}
	path := "/v2/llm-usage"
	if len(values) > 0 {
		path += "?" + values.Encode()
	}
	var report LLMUsageAggregateReport
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &report)
	return report, err
}

func (s *LLMService) DoJSON(ctx context.Context, method string, path string, request any, response any) error {
	return s.client.DoJSON(ctx, method, joinAPIPath("/v2", path), request, response)
}

func llmProviderPath(providerID string) string {
	return "/v2/llm-providers/" + url.PathEscape(providerID)
}

func revisionHeaders(revision int64) http.Header {
	headers := make(http.Header)
	headers.Set("If-Match", strconv.Quote(strconv.FormatInt(revision, 10)))
	return headers
}

type SessionsService struct{ client *Client }

func (s *SessionsService) Create(ctx context.Context, request CreateSessionRequest) (Session, error) {
	var session Session
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/sessions", request, &session)
	return session, err
}

func (s *SessionsService) Get(ctx context.Context, sessionID string) (Session, error) {
	var session Session
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/sessions/"+url.PathEscape(sessionID), nil, &session)
	return session, err
}

func (s *SessionsService) UpdateMetadata(ctx context.Context, sessionID string, request UpdateSessionMetadataRequest) (Session, error) {
	var session Session
	err := s.client.DoJSON(ctx, http.MethodPatch, sessionPath(sessionID), request, &session)
	return session, err
}

func (s *SessionsService) Archive(ctx context.Context, sessionID string) (Session, error) {
	var session Session
	err := s.client.DoJSON(ctx, http.MethodPost, sessionPath(sessionID)+"/archive", struct{}{}, &session)
	return session, err
}

func (s *SessionsService) Restore(ctx context.Context, sessionID string) (Session, error) {
	var session Session
	err := s.client.DoJSON(ctx, http.MethodPost, sessionPath(sessionID)+"/restore", struct{}{}, &session)
	return session, err
}

func (s *SessionsService) Delete(ctx context.Context, sessionID string) error {
	return s.client.DoJSON(ctx, http.MethodDelete, sessionPath(sessionID), nil, nil)
}

func (s *SessionsService) Rerun(ctx context.Context, sessionID string, request RerunSessionRequest) (RerunSessionResponse, error) {
	var result RerunSessionResponse
	err := s.client.DoJSON(ctx, http.MethodPost, sessionPath(sessionID)+"/rerun", request, &result)
	return result, err
}

func (s *SessionsService) Compare(ctx context.Context, leftSessionID string, rightSessionID string) (SessionComparison, error) {
	values := url.Values{}
	values.Set("left_session_id", leftSessionID)
	values.Set("right_session_id", rightSessionID)
	var result SessionComparison
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/session-comparisons?"+values.Encode(), nil, &result)
	return result, err
}

func (s *SessionsService) RuntimeConfig(ctx context.Context, sessionID string) (AgentRuntimeConfig, error) {
	var result AgentRuntimeConfig
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/runtime-config", nil, &result)
	return result, err
}

func (s *SessionsService) RuntimeCapabilities(ctx context.Context, sessionID string) (SessionRuntimeCapabilities, error) {
	var result SessionRuntimeCapabilities
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/runtime-capabilities", nil, &result)
	return result, err
}

func (s *SessionsService) UpdateRuntimeSettings(ctx context.Context, sessionID string, request UpdateSessionRuntimeSettingsRequest) (Session, error) {
	var session Session
	err := s.client.DoJSON(ctx, http.MethodPatch, sessionPath(sessionID)+"/runtime-settings", request, &session)
	return session, err
}

func (s *SessionsService) UpgradeConfig(ctx context.Context, sessionID string, request UpgradeSessionConfigRequest) (UpgradeSessionConfigResult, error) {
	var result UpgradeSessionConfigResult
	err := s.client.DoJSON(ctx, http.MethodPost, sessionPath(sessionID)+"/config/upgrade", request, &result)
	return result, err
}

func (s *SessionsService) List(ctx context.Context, query url.Values) ([]Session, error) {
	path := "/v2/sessions"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var response struct {
		Sessions []Session `json:"sessions"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Sessions, err
}

func (s *SessionsService) Events(ctx context.Context, sessionID string, afterSeq int64) (*EventStream, error) {
	return s.client.Events(ctx, sessionPath(sessionID)+"/events/stream", afterSeq)
}

func (s *SessionsService) AppendEvents(ctx context.Context, sessionID string, request AppendEventsRequest) (AppendEventsResult, error) {
	var result AppendEventsResult
	err := s.client.DoJSON(ctx, http.MethodPost, sessionPath(sessionID)+"/events", request, &result)
	return result, err
}

func (s *SessionsService) ListEvents(ctx context.Context, sessionID string, afterSeq int64) ([]Event, error) {
	path := sessionPath(sessionID) + "/events"
	if afterSeq > 0 {
		path += "?after_seq=" + strconv.FormatInt(afterSeq, 10)
	}
	var response struct {
		Events []Event `json:"events"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Events, err
}

func (s *SessionsService) GetSummary(ctx context.Context, sessionID string) (SessionSummary, error) {
	var summary SessionSummary
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/summary", nil, &summary)
	return summary, err
}

func (s *SessionsService) UpsertSummary(ctx context.Context, sessionID string, request UpsertSessionSummaryRequest) (SessionSummary, error) {
	var summary SessionSummary
	err := s.client.DoJSON(ctx, http.MethodPut, sessionPath(sessionID)+"/summary", request, &summary)
	return summary, err
}

func (s *SessionsService) Usage(ctx context.Context, sessionID string) (SessionUsage, error) {
	var usage SessionUsage
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/usage", nil, &usage)
	return usage, err
}

func sessionPath(sessionID string) string {
	return "/v2/sessions/" + url.PathEscape(sessionID)
}

type RunsService struct{ client *Client }

func (s *RunsService) Start(ctx context.Context, sessionID string, request StartRunRequest) (*RunHandle, error) {
	var response StartRunResponse
	err := s.client.DoJSON(ctx, http.MethodPost, runBasePath(sessionID), request, &response)
	if err != nil {
		return nil, err
	}
	return &RunHandle{client: s.client, Run: response.Run}, nil
}

func (s *RunsService) Get(ctx context.Context, sessionID string, runID string) (Run, error) {
	var run Run
	err := s.client.DoJSON(ctx, http.MethodGet, runPath(sessionID, runID), nil, &run)
	return run, err
}

func (s *RunsService) List(ctx context.Context, sessionID string) ([]Run, error) {
	var response struct {
		Runs []Run `json:"runs"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, runBasePath(sessionID), nil, &response)
	return response.Runs, err
}

func (s *RunsService) Cancel(ctx context.Context, sessionID string, runID string) (Run, error) {
	var run Run
	err := s.client.DoJSON(ctx, http.MethodPost, runPath(sessionID, runID)+"/cancel", struct{}{}, &run)
	return run, err
}

func (s *RunsService) Events(ctx context.Context, sessionID string, runID string, afterSeq int64) (*EventStream, error) {
	path := runPath(sessionID, runID) + "/events/stream"
	return newEventStream(ctx, s.client, path, afterSeq), nil
}

func (s *RunsService) ListEvents(ctx context.Context, sessionID string, runID string, afterSeq int64) ([]Event, error) {
	path := runPath(sessionID, runID) + "/events"
	if afterSeq > 0 {
		path += "?after_seq=" + strconv.FormatInt(afterSeq, 10)
	}
	var response struct {
		Events []Event `json:"events"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Events, err
}

func runBasePath(sessionID string) string {
	return "/v2/sessions/" + url.PathEscape(sessionID) + "/runs"
}

func runPath(sessionID string, runID string) string {
	return runBasePath(sessionID) + "/" + url.PathEscape(runID)
}

type InterventionsService struct{ client *Client }

func (s *InterventionsService) List(ctx context.Context, sessionID string, status string) ([]Intervention, error) {
	path := sessionPath(sessionID) + "/interventions"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var response struct {
		Interventions []Intervention `json:"interventions"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Interventions, err
}

func (s *InterventionsService) Decide(ctx context.Context, sessionID string, turnID string, callID string, decision string, reason string) (Intervention, error) {
	result, err := s.DecideResult(ctx, sessionID, turnID, callID, decision, reason)
	return result.Intervention, err
}

func (s *InterventionsService) DecideResult(ctx context.Context, sessionID string, turnID string, callID string, decision string, reason string) (InterventionDecision, error) {
	if decision != "approve" && decision != "reject" {
		return InterventionDecision{}, fmt.Errorf("tma: intervention decision must be approve or reject")
	}
	path := sessionPath(sessionID) + "/interventions/" + url.PathEscape(turnID) + "/" + url.PathEscape(callID) + "/" + decision
	var result InterventionDecision
	err := s.client.DoJSON(ctx, http.MethodPost, path, map[string]string{"reason": reason}, &result)
	return result, err
}

type ArtifactsService struct{ client *Client }

func (s *ArtifactsService) Create(ctx context.Context, sessionID string, request CreateArtifactRequest) (Artifact, error) {
	var artifact Artifact
	err := s.client.DoJSON(ctx, http.MethodPost, sessionPath(sessionID)+"/artifacts", request, &artifact)
	return artifact, err
}

func (s *ArtifactsService) List(ctx context.Context, sessionID string) ([]Artifact, error) {
	var response struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, sessionPath(sessionID)+"/artifacts", nil, &response)
	return response.Artifacts, err
}

func (s *ArtifactsService) Download(ctx context.Context, sessionID string, artifactID string, output io.Writer) error {
	return s.client.Download(ctx, sessionPath(sessionID)+"/artifacts/"+url.PathEscape(artifactID)+"/download", output)
}

func (s *ArtifactsService) Delete(ctx context.Context, sessionID string, artifactID string) error {
	return s.client.DoJSON(ctx, http.MethodDelete, sessionPath(sessionID)+"/artifacts/"+url.PathEscape(artifactID), nil, nil)
}

func (s *ArtifactsService) Upload(ctx context.Context, sessionID string, fields map[string]string, file UploadFile) (ArtifactUpload, error) {
	var result ArtifactUpload
	err := s.client.Upload(ctx, sessionPath(sessionID)+"/artifacts/upload", fields, file, &result)
	return result, err
}

func (s *ArtifactsService) Raw(ctx context.Context, method string, path string, request json.RawMessage, response any) error {
	return s.client.DoJSON(ctx, method, path, request, response)
}
