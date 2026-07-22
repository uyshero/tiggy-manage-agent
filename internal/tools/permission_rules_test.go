package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePermissionRulesValidatesFileRules(t *testing.T) {
	rules, err := ParsePermissionRules(json.RawMessage(`{
		"permission_rules":[
			{"id":"source-edit","tool":"default.edit_file","argument":"path","pattern":"/workspace/src/**","behavior":"allow"},
			{"id":"config-edit","tool":"default.edit_file","argument":"path","pattern":"/workspace/config/**","behavior":"ask"}
		]
	}`))
	if err != nil {
		t.Fatalf("parse rules: %v", err)
	}
	if len(rules) != 2 || rules[0].Source != "session" {
		t.Fatalf("unexpected rules: %#v", rules)
	}
}

func TestPermissionRulePrecedenceAndRecursivePathMatch(t *testing.T) {
	policy := InterventionPolicy{Mode: InterventionModeRequestApproval, Rules: []PermissionRule{
		{ID: "allow-source", Tool: "default.edit_file", Argument: "path", Pattern: "/workspace/src/**", Behavior: PermissionRuleAllow, Source: "session"},
		{ID: "deny-secrets", Tool: "default.edit_file", Argument: "path", Pattern: "/workspace/src/secrets/**", Behavior: PermissionRuleDeny, Source: "workspace"},
	}}
	manifest := Manifest{ApprovalPolicy: ApprovalPolicyAlways, ApprovalReason: InterventionReasonFilesystemWrite}
	api := API{Name: "edit_file", Risk: ToolRiskWrite}

	allowed := policy.EvaluateCall(manifest, api, permissionRuleTestCall("/workspace/src/app/main.go"), ExecutionContext{})
	if !allowed.Allowed || allowed.Required || allowed.MatchedRuleID != "allow-source" || allowed.RuleSource != "session" {
		t.Fatalf("unexpected allow decision: %#v", allowed)
	}

	denied := policy.EvaluateCall(manifest, api, permissionRuleTestCall("/workspace/src/secrets/key.txt"), ExecutionContext{})
	if denied.Allowed || denied.Required || denied.MatchedRuleID != "deny-secrets" || denied.Reason != "permission_rule_deny" {
		t.Fatalf("deny rule must override allow rule: %#v", denied)
	}
}

func TestResolvePermissionRulesAppliesScopePrecedence(t *testing.T) {
	rules, err := ResolvePermissionRules(
		json.RawMessage(`{"permission_rules":[{"id":"session-allow","tool":"default.edit_file","argument":"path","pattern":"/workspace/src/**","behavior":"allow"}]}`),
		json.RawMessage(`{"permission_rules":[{"id":"agent-deny","tool":"default.edit_file","argument":"path","pattern":"/workspace/**","behavior":"deny"}]}`),
		json.RawMessage(`{"permission_rules":[{"id":"workspace-deny","tool":"default.edit_file","argument":"path","pattern":"/workspace/src/secrets/**","behavior":"deny"}]}`),
	)
	if err != nil {
		t.Fatalf("resolve rules: %v", err)
	}
	policy := InterventionPolicy{Mode: InterventionModeFullAccess, Rules: rules}
	manifest := Manifest{ApprovalPolicy: ApprovalPolicyAlways, ApprovalReason: InterventionReasonFilesystemWrite}
	api := API{Name: "edit_file", Risk: ToolRiskWrite}

	allowed := policy.EvaluateCall(manifest, api, permissionRuleTestCall("/workspace/src/main.go"), ExecutionContext{})
	if !allowed.Allowed || allowed.MatchedRuleID != "session-allow" || allowed.RuleSource != PermissionRuleSourceSession {
		t.Fatalf("session rule must override agent default: %#v", allowed)
	}
	denied := policy.EvaluateCall(manifest, api, permissionRuleTestCall("/workspace/src/secrets/token"), ExecutionContext{})
	if denied.Allowed || denied.MatchedRuleID != "workspace-deny" || denied.RuleSource != PermissionRuleSourceWorkspace {
		t.Fatalf("workspace deny must remain a hard boundary: %#v", denied)
	}
}

func TestWorkspacePermissionRulesRejectNonDenyBehavior(t *testing.T) {
	_, err := ResolvePermissionRules(nil, nil, json.RawMessage(`{"permission_rules":[{
		"id":"workspace-allow","tool":"default.read_file","argument":"path","pattern":"/workspace/**","behavior":"allow"
	}]}`))
	if err == nil || !strings.Contains(err.Error(), "behavior must be deny") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPermissionAskRuleHonorsInterventionMode(t *testing.T) {
	rule := PermissionRule{
		ID: "ask-config", Tool: "default.write_file", Argument: "path",
		Pattern: "/workspace/config/**", Behavior: PermissionRuleAsk, Source: "session",
	}
	manifest := Manifest{ApprovalPolicy: ApprovalPolicyNever}
	api := API{Name: "write_file", Risk: ToolRiskWrite}
	call := Call{Identifier: NamespaceDefault, APIName: "write_file", Arguments: json.RawMessage(`{"path":"/workspace/config/app.json"}`)}

	manual := (InterventionPolicy{Mode: InterventionModeRequestApproval, Rules: []PermissionRule{rule}}).EvaluateCall(manifest, api, call, ExecutionContext{})
	if manual.Allowed || !manual.Required {
		t.Fatalf("manual ask decision: %#v", manual)
	}
	auto := (InterventionPolicy{Mode: InterventionModeApproveForMe, Rules: []PermissionRule{rule}}).EvaluateCall(manifest, api, call, ExecutionContext{})
	if !auto.Allowed || !auto.Required {
		t.Fatalf("auto ask decision: %#v", auto)
	}
	bypass := (InterventionPolicy{Mode: InterventionModeFullAccess, Rules: []PermissionRule{rule}}).EvaluateCall(manifest, api, call, ExecutionContext{})
	if !bypass.Allowed || bypass.Required {
		t.Fatalf("full access ask decision: %#v", bypass)
	}
}

func TestPermissionRulesRejectUnsupportedTargets(t *testing.T) {
	_, err := ParsePermissionRules(json.RawMessage(`{"permission_rules":[{
		"id":"bash","tool":"default.run_command","argument":"command","pattern":"rm *","behavior":"deny"
	}]}`))
	if err == nil || !strings.Contains(err.Error(), "not a supported file tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPermissionRulesRejectUnknownFields(t *testing.T) {
	_, err := ParsePermissionRules(json.RawMessage(`{"permission_rules":[{
		"id":"source","tool":"default.edit_file","argument":"path",
		"pattern":"src/**","behaviour":"allow"
	}]}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSuggestedPermissionRulesUseParentDirectoryAndExplicitScopes(t *testing.T) {
	t.Parallel()

	suggestions := SuggestedPermissionRules(Call{
		Name:      "default.edit_file",
		Arguments: json.RawMessage(`{"path":"/workspace/src/config/app.go","old_string":"old","new_string":"new"}`),
	})
	if len(suggestions) != 2 {
		t.Fatalf("suggestions = %+v", suggestions)
	}
	if suggestions[0].Scope != PermissionRuleSourceSession || suggestions[1].Scope != PermissionRuleSourceAgent {
		t.Fatalf("suggestion scopes = %+v", suggestions)
	}
	for _, suggestion := range suggestions {
		if suggestion.Rule.Tool != "default.edit_file" || suggestion.Rule.Argument != "path" ||
			suggestion.Rule.Pattern != "/workspace/src/config/**" || suggestion.Rule.Behavior != PermissionRuleAllow || suggestion.Rule.ID == "" {
			t.Fatalf("suggestion = %+v", suggestion)
		}
		if err := ValidatePermissionRules([]PermissionRule{suggestion.Rule}); err != nil {
			t.Fatalf("suggested rule is invalid: %v", err)
		}
	}
}

func permissionRuleTestCall(filePath string) Call {
	arguments, _ := json.Marshal(map[string]string{"path": filePath})
	return Call{Identifier: NamespaceDefault, APIName: "edit_file", Arguments: arguments}
}
