package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	InterventionModeRequestApproval   = "request_approval"
	InterventionModeApproveForMe      = "approve_for_me"
	InterventionModeFullAccess        = "full_access"
	InterventionReasonNetworkAccess   = "network_access"
	InterventionReasonProcessExec     = "process_exec"
	InterventionReasonFilesystemWrite = "filesystem_write"
	InterventionReasonSkillRegistry   = "skill_registry_write"
	InterventionReasonExternalWrite   = "external_tool_write"

	ApprovalPolicyNever       = "never"
	ApprovalPolicyConditional = "conditional"
	ApprovalPolicyAlways      = "always"
)

type InterventionPolicy struct {
	Mode  string
	Rules []PermissionRule
}

type InterventionDecision struct {
	Allowed        bool   `json:"allowed"`
	Required       bool   `json:"required"`
	Mode           string `json:"mode"`
	ApprovalPolicy string `json:"approval_policy,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Risk           string `json:"risk,omitempty"`
	MatchedRuleID  string `json:"matched_rule_id,omitempty"`
	RuleSource     string `json:"rule_source,omitempty"`
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

func NormalizeApprovalPolicy(value string) (string, bool) {
	policy := strings.TrimSpace(strings.ToLower(value))
	switch policy {
	case ApprovalPolicyNever, ApprovalPolicyConditional, ApprovalPolicyAlways:
		return policy, true
	default:
		return "", false
	}
}

func (p InterventionPolicy) Evaluate(manifest Manifest, api API) InterventionDecision {
	mode := strings.TrimSpace(strings.ToLower(p.Mode))
	if mode == "" {
		mode = InterventionModeRequestApproval
	}
	policy, reason := effectiveApprovalMetadata(manifest, api)
	decision := InterventionDecision{
		Mode: mode, ApprovalPolicy: policy, Reason: reason, Risk: api.Risk,
	}
	if mode == InterventionModeFullAccess {
		decision.Allowed = true
		decision.Reason = ""
		return decision
	}
	if policy == ApprovalPolicyNever {
		decision.Allowed = true
		decision.Reason = ""
		return decision
	}
	decision.Required = true
	if mode == InterventionModeApproveForMe {
		decision.Allowed = true
		return decision
	}
	return decision
}

func (p InterventionPolicy) EvaluateCall(manifest Manifest, api API, call Call, executionContext ExecutionContext) InterventionDecision {
	if callRequiresNetworkApproval(call, executionContext) {
		api.ApprovalPolicy = ApprovalPolicyAlways
		api.ApprovalReason = InterventionReasonNetworkAccess
	}
	if rule, ok := matchingPermissionRule(p.Rules, call); ok {
		return p.evaluateRule(api, rule)
	}
	return p.Evaluate(manifest, api)
}

func (p InterventionPolicy) evaluateRule(api API, rule PermissionRule) InterventionDecision {
	mode := strings.TrimSpace(strings.ToLower(p.Mode))
	if mode == "" {
		mode = InterventionModeRequestApproval
	}
	behavior := strings.ToLower(strings.TrimSpace(rule.Behavior))
	reason := strings.TrimSpace(rule.Reason)
	if reason == "" {
		reason = "permission_rule_" + behavior
	}
	decision := InterventionDecision{
		Mode: mode, ApprovalPolicy: ApprovalPolicyConditional, Reason: reason,
		Risk: api.Risk, MatchedRuleID: rule.ID, RuleSource: rule.Source,
	}
	switch behavior {
	case PermissionRuleDeny:
		return decision
	case PermissionRuleAllow:
		decision.Allowed = true
		decision.Reason = ""
		return decision
	case PermissionRuleAsk:
		if mode == InterventionModeFullAccess {
			decision.Allowed = true
			decision.Reason = ""
			return decision
		}
		decision.Required = true
		if mode == InterventionModeApproveForMe {
			decision.Allowed = true
		}
		return decision
	default:
		return decision
	}
}

func PermissionDeniedResult(call Call, decision InterventionDecision) ExecutionResult {
	call = NormalizeCall(call)
	message := "Tool call denied by permission policy."
	if decision.MatchedRuleID != "" {
		message = fmt.Sprintf("Tool call denied by permission rule %q.", decision.MatchedRuleID)
	}
	state, _ := json.Marshal(map[string]any{
		"status":          "failed",
		"error_type":      "permission_denied",
		"reason":          decision.Reason,
		"matched_rule_id": decision.MatchedRuleID,
		"rule_source":     decision.RuleSource,
		"risk":            decision.Risk,
	})
	return ExecutionResult{
		ID: call.ID, Identifier: call.Identifier, APIName: call.APIName,
		Content: message, State: state,
		Error: &ExecutionError{Type: "permission_denied", Message: message},
	}
}

func effectiveApprovalMetadata(manifest Manifest, api API) (string, string) {
	policy := strings.TrimSpace(api.ApprovalPolicy)
	reason := strings.TrimSpace(api.ApprovalReason)
	if policy == "" {
		policy = strings.TrimSpace(manifest.ApprovalPolicy)
		reason = strings.TrimSpace(manifest.ApprovalReason)
		if policy == "" {
			return ApprovalPolicyNever, ""
		}
	}
	normalized, ok := NormalizeApprovalPolicy(policy)
	if !ok {
		// Invalid policy metadata must fail closed until manifest validation
		// rejects it at registration time.
		return ApprovalPolicyAlways, "invalid_approval_policy"
	}
	return normalized, reason
}

// ValidateManifestPermissions verifies typed approval metadata and requires
// every write or exec API to declare a policy directly or inherit one from its
// manifest. Read-only APIs default to never when no policy is declared.
func ValidateManifestPermissions(manifest Manifest) error {
	if err := validateApprovalMetadata("manifest", manifest.ApprovalPolicy, manifest.ApprovalReason); err != nil {
		return err
	}
	for _, api := range manifest.API {
		name := fallbackString(api.APIName, api.Name)
		if err := validateApprovalMetadata("api "+name, api.ApprovalPolicy, api.ApprovalReason); err != nil {
			return err
		}
		if strings.TrimSpace(api.ApprovalPolicy) == "" && strings.TrimSpace(manifest.ApprovalPolicy) == "" {
			risk, _ := NormalizeToolRisk(api.Risk)
			if risk == ToolRiskWrite || risk == ToolRiskExec {
				return fmt.Errorf("api %s with risk %s requires approval_policy", name, risk)
			}
		}
	}
	return nil
}

func validateApprovalMetadata(scope string, policy string, reason string) error {
	policy = strings.TrimSpace(policy)
	reason = strings.TrimSpace(reason)
	if policy == "" {
		if reason != "" {
			return fmt.Errorf("%s approval_reason requires approval_policy", scope)
		}
		return nil
	}
	normalized, ok := NormalizeApprovalPolicy(policy)
	if !ok {
		return fmt.Errorf("%s has invalid approval_policy %q", scope, policy)
	}
	if normalized == ApprovalPolicyNever {
		if reason != "" {
			return fmt.Errorf("%s approval_policy never cannot declare an approval reason", scope)
		}
		return nil
	}
	if reason == "" {
		return fmt.Errorf("%s approval_policy %s requires approval_reason", scope, normalized)
	}
	return nil
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
