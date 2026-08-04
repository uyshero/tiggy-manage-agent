package managedagents

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	ModelRuntimeQuotaScopeWorkspace   = "workspace"
	ModelRuntimeQuotaScopeApplication = "application"

	ModelRuntimeQuotaPolicyStatusActive   = "active"
	ModelRuntimeQuotaPolicyStatusArchived = "archived"

	ModelRuntimeQuotaAlertWarning  = "warning"
	ModelRuntimeQuotaAlertExceeded = "exceeded"
)

type ModelRuntimeQuotaPolicyConfig struct {
	ModelWorkspaceRequestsPerMinute  *int   `json:"model_workspace_requests_per_minute,omitempty"`
	ModelIdentityRequestsPerMinute   *int   `json:"model_identity_requests_per_minute,omitempty"`
	SpeechWorkspaceSessionsPerMinute *int   `json:"speech_workspace_sessions_per_minute,omitempty"`
	SpeechIdentitySessionsPerMinute  *int   `json:"speech_identity_sessions_per_minute,omitempty"`
	MonthlyModelRequestBudget        *int64 `json:"monthly_model_request_budget,omitempty"`
	MonthlySpeechSessionBudget       *int64 `json:"monthly_speech_session_budget,omitempty"`
	AlertThresholdPercent            *int   `json:"alert_threshold_percent,omitempty"`
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

type UpsertModelRuntimeQuotaPolicyInput struct {
	WorkspaceID      string
	Scope            string
	AppID            string
	Plan             string
	Config           ModelRuntimeQuotaPolicyConfig
	ExpectedRevision int64
	UpdatedBy        string
}

type ArchiveModelRuntimeQuotaPolicyInput struct {
	WorkspaceID      string
	Scope            string
	AppID            string
	ExpectedRevision int64
	ArchivedBy       string
}

type ModelRuntimeQuotaLimits struct {
	ModelWorkspaceRequestsPerMinute  int `json:"model_workspace_requests_per_minute"`
	ModelIdentityRequestsPerMinute   int `json:"model_identity_requests_per_minute"`
	SpeechWorkspaceSessionsPerMinute int `json:"speech_workspace_sessions_per_minute"`
	SpeechIdentitySessionsPerMinute  int `json:"speech_identity_sessions_per_minute"`
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
	AlertThresholdPercent      int                     `json:"alert_threshold_percent"`
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
	ThresholdPercent int    `json:"threshold_percent"`
}

type ModelRuntimeQuotaStatus struct {
	Policy EffectiveModelRuntimeQuotaPolicy `json:"policy"`
	Usage  ModelRuntimeQuotaUsage           `json:"usage"`
	Alerts []ModelRuntimeQuotaAlert         `json:"alerts"`
}

type ModelRuntimeQuotaPolicyStore interface {
	ListModelRuntimeQuotaPoliciesContext(context.Context, string, bool) ([]ModelRuntimeQuotaPolicy, error)
	GetModelRuntimeQuotaPolicyContext(context.Context, string, string, string) (ModelRuntimeQuotaPolicy, error)
	UpsertModelRuntimeQuotaPolicyContext(context.Context, UpsertModelRuntimeQuotaPolicyInput) (ModelRuntimeQuotaPolicy, error)
	ArchiveModelRuntimeQuotaPolicyContext(context.Context, ArchiveModelRuntimeQuotaPolicyInput) (ModelRuntimeQuotaPolicy, error)
	GetModelRuntimeQuotaUsageContext(context.Context, string, string, time.Time, time.Time) (ModelRuntimeQuotaUsage, error)
}

func NormalizeUpsertModelRuntimeQuotaPolicyInput(input UpsertModelRuntimeQuotaPolicyInput) (UpsertModelRuntimeQuotaPolicyInput, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Scope = strings.ToLower(strings.TrimSpace(input.Scope))
	input.AppID = strings.TrimSpace(input.AppID)
	input.Plan = strings.ToLower(strings.TrimSpace(input.Plan))
	input.UpdatedBy = strings.TrimSpace(input.UpdatedBy)
	if input.WorkspaceID == "" || input.UpdatedBy == "" || input.Plan == "" || len(input.Plan) > 64 {
		return UpsertModelRuntimeQuotaPolicyInput{}, fmt.Errorf("%w: quota policy workspace, plan, and updater are required", ErrInvalid)
	}
	if input.ExpectedRevision < 0 {
		return UpsertModelRuntimeQuotaPolicyInput{}, fmt.Errorf("%w: expected_revision cannot be negative", ErrInvalid)
	}
	switch input.Scope {
	case ModelRuntimeQuotaScopeWorkspace:
		if input.AppID != "" {
			return UpsertModelRuntimeQuotaPolicyInput{}, fmt.Errorf("%w: workspace quota policy cannot specify app_id", ErrInvalid)
		}
	case ModelRuntimeQuotaScopeApplication:
		if input.AppID == "" {
			return UpsertModelRuntimeQuotaPolicyInput{}, fmt.Errorf("%w: application quota policy requires app_id", ErrInvalid)
		}
		if input.Config.ModelWorkspaceRequestsPerMinute != nil || input.Config.SpeechWorkspaceSessionsPerMinute != nil {
			return UpsertModelRuntimeQuotaPolicyInput{}, fmt.Errorf("%w: application quota policy cannot override workspace limits", ErrInvalid)
		}
	default:
		return UpsertModelRuntimeQuotaPolicyInput{}, fmt.Errorf("%w: unsupported quota policy scope", ErrInvalid)
	}
	if err := validateModelRuntimeQuotaPolicyConfig(input.Config); err != nil {
		return UpsertModelRuntimeQuotaPolicyInput{}, err
	}
	return input, nil
}

func validateModelRuntimeQuotaPolicyConfig(config ModelRuntimeQuotaPolicyConfig) error {
	configured := false
	for name, value := range map[string]*int{
		"model_workspace_requests_per_minute":  config.ModelWorkspaceRequestsPerMinute,
		"model_identity_requests_per_minute":   config.ModelIdentityRequestsPerMinute,
		"speech_workspace_sessions_per_minute": config.SpeechWorkspaceSessionsPerMinute,
		"speech_identity_sessions_per_minute":  config.SpeechIdentitySessionsPerMinute,
	} {
		if value == nil {
			continue
		}
		configured = true
		if *value < 0 || *value > 1_000_000 {
			return fmt.Errorf("%w: %s must be between 0 and 1000000", ErrInvalid, name)
		}
	}
	for name, value := range map[string]*int64{
		"monthly_model_request_budget":  config.MonthlyModelRequestBudget,
		"monthly_speech_session_budget": config.MonthlySpeechSessionBudget,
	} {
		if value == nil {
			continue
		}
		configured = true
		if *value < 0 || *value > 1_000_000_000 {
			return fmt.Errorf("%w: %s must be between 0 and 1000000000", ErrInvalid, name)
		}
	}
	if config.AlertThresholdPercent != nil {
		configured = true
		if *config.AlertThresholdPercent < 1 || *config.AlertThresholdPercent > 100 {
			return fmt.Errorf("%w: alert_threshold_percent must be between 1 and 100", ErrInvalid)
		}
	}
	if !configured {
		return fmt.Errorf("%w: quota policy config must contain at least one override", ErrInvalid)
	}
	return nil
}

func EffectiveModelRuntimeQuota(defaults ModelRuntimeQuotaLimits, workspaceID, appID string, workspacePolicy, applicationPolicy *ModelRuntimeQuotaPolicy) EffectiveModelRuntimeQuotaPolicy {
	effective := EffectiveModelRuntimeQuotaPolicy{
		WorkspaceID: strings.TrimSpace(workspaceID), AppID: strings.TrimSpace(appID), Plan: "deployment-default",
		Limits: defaults, AlertThresholdPercent: 80,
	}
	if workspacePolicy != nil {
		effective.WorkspacePolicyID = workspacePolicy.ID
		effective.WorkspacePolicyRevision = workspacePolicy.Revision
		effective.Plan = workspacePolicy.Plan
		applyModelRuntimeQuotaConfig(&effective, workspacePolicy.Config)
	}
	if applicationPolicy != nil {
		effective.ApplicationPolicyID = applicationPolicy.ID
		effective.ApplicationPolicyRevision = applicationPolicy.Revision
		effective.Plan = applicationPolicy.Plan
		applyApplicationModelRuntimeQuotaConfig(&effective, applicationPolicy.Config)
	}
	return effective
}

func applyApplicationModelRuntimeQuotaConfig(effective *EffectiveModelRuntimeQuotaPolicy, config ModelRuntimeQuotaPolicyConfig) {
	if config.ModelIdentityRequestsPerMinute != nil {
		effective.Limits.ModelIdentityRequestsPerMinute = *config.ModelIdentityRequestsPerMinute
	}
	if config.SpeechIdentitySessionsPerMinute != nil {
		effective.Limits.SpeechIdentitySessionsPerMinute = *config.SpeechIdentitySessionsPerMinute
	}
	if config.MonthlyModelRequestBudget != nil {
		effective.MonthlyModelRequestBudget = *config.MonthlyModelRequestBudget
	}
	if config.MonthlySpeechSessionBudget != nil {
		effective.MonthlySpeechSessionBudget = *config.MonthlySpeechSessionBudget
	}
	if config.AlertThresholdPercent != nil {
		effective.AlertThresholdPercent = *config.AlertThresholdPercent
	}
}

func applyModelRuntimeQuotaConfig(effective *EffectiveModelRuntimeQuotaPolicy, config ModelRuntimeQuotaPolicyConfig) {
	if config.ModelWorkspaceRequestsPerMinute != nil {
		effective.Limits.ModelWorkspaceRequestsPerMinute = *config.ModelWorkspaceRequestsPerMinute
	}
	if config.ModelIdentityRequestsPerMinute != nil {
		effective.Limits.ModelIdentityRequestsPerMinute = *config.ModelIdentityRequestsPerMinute
	}
	if config.SpeechWorkspaceSessionsPerMinute != nil {
		effective.Limits.SpeechWorkspaceSessionsPerMinute = *config.SpeechWorkspaceSessionsPerMinute
	}
	if config.SpeechIdentitySessionsPerMinute != nil {
		effective.Limits.SpeechIdentitySessionsPerMinute = *config.SpeechIdentitySessionsPerMinute
	}
	if config.MonthlyModelRequestBudget != nil {
		effective.MonthlyModelRequestBudget = *config.MonthlyModelRequestBudget
	}
	if config.MonthlySpeechSessionBudget != nil {
		effective.MonthlySpeechSessionBudget = *config.MonthlySpeechSessionBudget
	}
	if config.AlertThresholdPercent != nil {
		effective.AlertThresholdPercent = *config.AlertThresholdPercent
	}
}

func ModelRuntimeQuotaAlerts(policy EffectiveModelRuntimeQuotaPolicy, usage ModelRuntimeQuotaUsage) []ModelRuntimeQuotaAlert {
	alerts := []ModelRuntimeQuotaAlert{}
	appendAlert := func(metric string, consumed, budget int64) {
		if budget <= 0 || consumed*100 < budget*int64(policy.AlertThresholdPercent) {
			return
		}
		status := ModelRuntimeQuotaAlertWarning
		if consumed >= budget {
			status = ModelRuntimeQuotaAlertExceeded
		}
		alerts = append(alerts, ModelRuntimeQuotaAlert{
			Metric: metric, Status: status, Consumed: consumed, Budget: budget, ThresholdPercent: policy.AlertThresholdPercent,
		})
	}
	appendAlert("monthly_model_requests", usage.ModelRequests, policy.MonthlyModelRequestBudget)
	appendAlert("monthly_speech_sessions", usage.SpeechSessions, policy.MonthlySpeechSessionBudget)
	return alerts
}
