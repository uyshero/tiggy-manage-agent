package managedagents

import (
	"errors"
	"testing"
)

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }

func TestApplicationModelRuntimeQuotaCannotOverrideWorkspaceLimits(t *testing.T) {
	_, err := NormalizeUpsertModelRuntimeQuotaPolicyInput(UpsertModelRuntimeQuotaPolicyInput{
		WorkspaceID: "wksp", Scope: ModelRuntimeQuotaScopeApplication, AppID: "app", Plan: "pro", UpdatedBy: "admin",
		Config: ModelRuntimeQuotaPolicyConfig{ModelWorkspaceRequestsPerMinute: intPointer(999)},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected application workspace limit rejection, got %v", err)
	}

	workspace := ModelRuntimeQuotaPolicy{
		ID: "workspace-policy", Plan: "team", Revision: 2,
		Config: ModelRuntimeQuotaPolicyConfig{
			ModelWorkspaceRequestsPerMinute: intPointer(500), ModelIdentityRequestsPerMinute: intPointer(100),
		},
	}
	application := ModelRuntimeQuotaPolicy{
		ID: "application-policy", Plan: "application-pro", Revision: 3,
		Config: ModelRuntimeQuotaPolicyConfig{
			ModelWorkspaceRequestsPerMinute: intPointer(999), ModelIdentityRequestsPerMinute: intPointer(20),
		},
	}
	effective := EffectiveModelRuntimeQuota(ModelRuntimeQuotaLimits{}, "wksp", "app", &workspace, &application)
	if effective.Limits.ModelWorkspaceRequestsPerMinute != 500 || effective.Limits.ModelIdentityRequestsPerMinute != 20 {
		t.Fatalf("application override escaped its boundary: %+v", effective.Limits)
	}
}

func TestModelRuntimeQuotaAlertsWarningAndExceeded(t *testing.T) {
	policy := EffectiveModelRuntimeQuotaPolicy{
		MonthlyModelRequestBudget: 100, MonthlySpeechSessionBudget: 10, AlertThresholdPercent: 80,
	}
	alerts := ModelRuntimeQuotaAlerts(policy, ModelRuntimeQuotaUsage{ModelRequests: 80, SpeechSessions: 10})
	if len(alerts) != 2 || alerts[0].Status != ModelRuntimeQuotaAlertWarning || alerts[1].Status != ModelRuntimeQuotaAlertExceeded {
		t.Fatalf("unexpected quota alerts: %+v", alerts)
	}
}

func TestEffectiveModelRuntimeQuotaUsesDeploymentDefaultsWithoutPolicies(t *testing.T) {
	defaults := ModelRuntimeQuotaLimits{ModelWorkspaceRequestsPerMinute: 600, ModelIdentityRequestsPerMinute: 120}
	effective := EffectiveModelRuntimeQuota(defaults, "wksp", "", nil, nil)
	if effective.Plan != "deployment-default" || effective.Limits != defaults || effective.AlertThresholdPercent != 80 {
		t.Fatalf("deployment fallback changed: %+v", effective)
	}
}

func TestApplicationModelRuntimeBudgetOverride(t *testing.T) {
	application := ModelRuntimeQuotaPolicy{Config: ModelRuntimeQuotaPolicyConfig{MonthlyModelRequestBudget: int64Pointer(42)}}
	effective := EffectiveModelRuntimeQuota(ModelRuntimeQuotaLimits{}, "wksp", "app", nil, &application)
	if effective.MonthlyModelRequestBudget != 42 {
		t.Fatalf("application budget was not applied: %+v", effective)
	}
}
