package tma

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ModelRuntimeQuotaPolicyConfig struct {
	ModelWorkspaceRequestsPerMinute  *int32 `json:"model_workspace_requests_per_minute,omitempty"`
	ModelIdentityRequestsPerMinute   *int32 `json:"model_identity_requests_per_minute,omitempty"`
	SpeechWorkspaceSessionsPerMinute *int32 `json:"speech_workspace_sessions_per_minute,omitempty"`
	SpeechIdentitySessionsPerMinute  *int32 `json:"speech_identity_sessions_per_minute,omitempty"`
	MonthlyModelRequestBudget        *int64 `json:"monthly_model_request_budget,omitempty"`
	MonthlySpeechSessionBudget       *int64 `json:"monthly_speech_session_budget,omitempty"`
	AlertThresholdPercent            *int32 `json:"alert_threshold_percent,omitempty"`
}

type PutModelRuntimeQuotaPolicyRequest struct {
	Plan   string                        `json:"plan"`
	Config ModelRuntimeQuotaPolicyConfig `json:"config"`
}

type ModelRuntimeQuotaPolicy struct {
	ID          string                        `json:"id"`
	WorkspaceID string                        `json:"workspace_id"`
	Scope       string                        `json:"scope"`
	AppID       string                        `json:"app_id,omitempty"`
	Plan        string                        `json:"plan"`
	Config      ModelRuntimeQuotaPolicyConfig `json:"config"`
	Status      string                        `json:"status"`
	Revision    int64                         `json:"revision"`
	CreatedBy   string                        `json:"created_by"`
	UpdatedBy   string                        `json:"updated_by"`
	CreatedAt   time.Time                     `json:"created_at"`
	UpdatedAt   time.Time                     `json:"updated_at"`
	ArchivedAt  *time.Time                    `json:"archived_at,omitempty"`
}

type ModelRuntimeQuotaLimits struct {
	ModelWorkspaceRequestsPerMinute  int32 `json:"model_workspace_requests_per_minute"`
	ModelIdentityRequestsPerMinute   int32 `json:"model_identity_requests_per_minute"`
	SpeechWorkspaceSessionsPerMinute int32 `json:"speech_workspace_sessions_per_minute"`
	SpeechIdentitySessionsPerMinute  int32 `json:"speech_identity_sessions_per_minute"`
}

type EffectiveModelRuntimeQuotaPolicy struct {
	WorkspaceID                string                  `json:"workspace_id"`
	AppID                      string                  `json:"app_id,omitempty"`
	WorkspacePolicyID          string                  `json:"workspace_policy_id,omitempty"`
	ApplicationPolicyID        string                  `json:"application_policy_id,omitempty"`
	WorkspacePolicyRevision    int64                   `json:"workspace_policy_revision,omitempty"`
	ApplicationPolicyRevision  int64                   `json:"application_policy_revision,omitempty"`
	Plan                       string                  `json:"plan"`
	Limits                     ModelRuntimeQuotaLimits `json:"limits"`
	MonthlyModelRequestBudget  int64                   `json:"monthly_model_request_budget"`
	MonthlySpeechSessionBudget int64                   `json:"monthly_speech_session_budget"`
	AlertThresholdPercent      int32                   `json:"alert_threshold_percent"`
}

type ModelRuntimeQuotaUsage struct {
	PeriodStartedAt time.Time `json:"period_started_at"`
	PeriodEndsAt    time.Time `json:"period_ends_at"`
	ModelRequests   int64     `json:"model_requests"`
	SpeechSessions  int64     `json:"speech_sessions"`
}

type ModelRuntimeQuotaAlert struct {
	Metric           string `json:"metric"`
	Status           string `json:"status"`
	Consumed         int64  `json:"consumed"`
	Budget           int64  `json:"budget"`
	ThresholdPercent int32  `json:"threshold_percent"`
}

type ModelRuntimeQuotaStatus struct {
	Policy EffectiveModelRuntimeQuotaPolicy `json:"policy"`
	Usage  ModelRuntimeQuotaUsage           `json:"usage"`
	Alerts []ModelRuntimeQuotaAlert         `json:"alerts"`
}

type QuotaPoliciesService struct{ client *Client }

func (s *QuotaPoliciesService) List(ctx context.Context, includeArchived bool) ([]ModelRuntimeQuotaPolicy, error) {
	path := "/v2/quota-policies"
	if includeArchived {
		path += "?include_archived=true"
	}
	var response struct {
		Policies []ModelRuntimeQuotaPolicy `json:"policies"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &response)
	return response.Policies, err
}

func (s *QuotaPoliciesService) PutWorkspace(ctx context.Context, request PutModelRuntimeQuotaPolicyRequest, expectedRevision int64) (ModelRuntimeQuotaPolicy, error) {
	return s.put(ctx, "/v2/quota-policies/workspace", request, expectedRevision)
}

func (s *QuotaPoliciesService) PutApplication(ctx context.Context, appID string, request PutModelRuntimeQuotaPolicyRequest, expectedRevision int64) (ModelRuntimeQuotaPolicy, error) {
	return s.put(ctx, "/v2/quota-policies/applications/"+url.PathEscape(appID), request, expectedRevision)
}

func (s *QuotaPoliciesService) put(ctx context.Context, path string, request PutModelRuntimeQuotaPolicyRequest, expectedRevision int64) (ModelRuntimeQuotaPolicy, error) {
	var policy ModelRuntimeQuotaPolicy
	headers := make(http.Header)
	if expectedRevision > 0 {
		headers.Set("If-Match", strconv.Quote(strconv.FormatInt(expectedRevision, 10)))
	}
	err := s.client.DoJSONWithHeaders(ctx, http.MethodPut, path, headers, request, &policy)
	return policy, err
}

func (s *QuotaPoliciesService) ArchiveApplication(ctx context.Context, appID string, expectedRevision int64) (ModelRuntimeQuotaPolicy, error) {
	var policy ModelRuntimeQuotaPolicy
	err := s.client.DoJSONWithHeaders(ctx, http.MethodDelete, "/v2/quota-policies/applications/"+url.PathEscape(appID), revisionHeaders(expectedRevision), nil, &policy)
	return policy, err
}

func (s *QuotaPoliciesService) Effective(ctx context.Context, appID string) (ModelRuntimeQuotaStatus, error) {
	path := "/v2/quota-policies/effective"
	if appID != "" {
		path += "?app_id=" + url.QueryEscape(appID)
	}
	var status ModelRuntimeQuotaStatus
	err := s.client.DoJSON(ctx, http.MethodGet, path, nil, &status)
	return status, err
}
