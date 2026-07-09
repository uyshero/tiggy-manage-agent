package tools

import (
	"encoding/json"
	"strings"
)

const (
	InterventionModeRequestApproval = "request_approval"
	InterventionModeApproveForMe    = "approve_for_me"
	InterventionModeFullAccess      = "full_access"
	InterventionReasonNetworkAccess = "network_access"
)

type InterventionPolicy struct {
	Mode string
}

type InterventionDecision struct {
	Allowed  bool   `json:"allowed"`
	Required bool   `json:"required"`
	Mode     string `json:"mode"`
	Reason   string `json:"reason,omitempty"`
}

func ParseInterventionMode(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return InterventionModeRequestApproval
	}
	var decoded struct {
		InterventionMode string `json:"intervention_mode"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return InterventionModeRequestApproval
	}
	mode, ok := NormalizeInterventionMode(decoded.InterventionMode)
	if !ok {
		return InterventionModeRequestApproval
	}
	return mode
}

func NormalizeInterventionMode(value string) (string, bool) {
	mode := strings.TrimSpace(strings.ToLower(value))
	switch mode {
	case InterventionModeApproveForMe, InterventionModeFullAccess, InterventionModeRequestApproval:
		return mode, true
	default:
		return "", false
	}
}

func (p InterventionPolicy) Evaluate(manifest Manifest, api API) InterventionDecision {
	mode := strings.TrimSpace(strings.ToLower(p.Mode))
	if mode == "" {
		mode = InterventionModeRequestApproval
	}
	if mode == InterventionModeFullAccess {
		return InterventionDecision{Allowed: true, Mode: mode}
	}
	if api.HumanIntervention == "" {
		return InterventionDecision{Allowed: true, Mode: mode}
	}
	if mode == InterventionModeApproveForMe {
		return InterventionDecision{Allowed: true, Required: true, Mode: mode, Reason: api.HumanIntervention}
	}
	return InterventionDecision{Allowed: false, Required: true, Mode: mode, Reason: api.HumanIntervention}
}

func (p InterventionPolicy) EvaluateCall(manifest Manifest, api API, call Call, executionContext ExecutionContext) InterventionDecision {
	if callRequiresNetworkApproval(call, executionContext) {
		api.HumanIntervention = InterventionReasonNetworkAccess
	}
	return p.Evaluate(manifest, api)
}

type networkApprovalProvider interface {
	RequiresNetworkApproval() bool
}

func callRequiresNetworkApproval(call Call, executionContext ExecutionContext) bool {
	provider, ok := executionContext.Provider.(networkApprovalProvider)
	if !ok || !provider.RequiresNetworkApproval() {
		return false
	}
	normalized := NormalizeCall(call)
	if normalized.Identifier != NamespaceDefault {
		return false
	}
	switch normalized.APIName {
	case "run_command", "execute_code":
		return true
	default:
		return false
	}
}
