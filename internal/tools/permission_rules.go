package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

const (
	PermissionRuleAllow = "allow"
	PermissionRuleAsk   = "ask"
	PermissionRuleDeny  = "deny"

	PermissionRuleSourceSession   = "session"
	PermissionRuleSourceAgent     = "agent"
	PermissionRuleSourceWorkspace = "workspace"

	maxPermissionRules = 100
)

var filePermissionRuleTools = map[string]bool{
	NamespaceDefault + ".read_file":  true,
	NamespaceDefault + ".write_file": true,
	NamespaceDefault + ".edit_file":  true,
}

// PermissionRule controls one file tool by matching its path argument.
// Source is runtime-owned audit metadata and is not accepted from settings.
type PermissionRule struct {
	ID       string `json:"id"`
	Tool     string `json:"tool"`
	Argument string `json:"argument"`
	Pattern  string `json:"pattern"`
	Behavior string `json:"behavior"`
	Reason   string `json:"reason,omitempty"`
	Source   string `json:"-"`
}

func ParsePermissionRules(raw json.RawMessage) ([]PermissionRule, error) {
	return ParsePermissionRulesForSource(raw, PermissionRuleSourceSession)
}

// ParsePermissionRulesForSource parses the permission_rules member of a
// settings object and attaches trusted runtime-owned source metadata.
func ParsePermissionRulesForSource(raw json.RawMessage, source string) ([]PermissionRule, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return nil, fmt.Errorf("parse runtime settings: %w", err)
	}
	rawRules, ok := settings["permission_rules"]
	if !ok || len(rawRules) == 0 || string(rawRules) == "null" {
		return nil, nil
	}
	var rules []PermissionRule
	decoder := json.NewDecoder(bytes.NewReader(rawRules))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rules); err != nil {
		return nil, fmt.Errorf("parse permission_rules: %w", err)
	}
	for index := range rules {
		rules[index].ID = strings.TrimSpace(rules[index].ID)
		rules[index].Tool = strings.ToLower(strings.TrimSpace(rules[index].Tool))
		rules[index].Argument = strings.ToLower(strings.TrimSpace(rules[index].Argument))
		rules[index].Pattern = normalizePermissionPath(rules[index].Pattern)
		rules[index].Behavior = strings.ToLower(strings.TrimSpace(rules[index].Behavior))
		rules[index].Reason = strings.TrimSpace(rules[index].Reason)
		rules[index].Source = strings.ToLower(strings.TrimSpace(source))
	}
	if err := ValidatePermissionRules(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// ResolvePermissionRules combines the three configurable policy scopes. A
// Workspace policy is a non-bypassable guardrail and therefore accepts DENY
// rules only. Session rules take priority over Agent defaults at evaluation.
func ResolvePermissionRules(runtimeSettings json.RawMessage, agentTools json.RawMessage, workspacePolicy json.RawMessage) ([]PermissionRule, error) {
	workspaceRules, err := ParsePermissionRulesForSource(workspacePolicy, PermissionRuleSourceWorkspace)
	if err != nil {
		return nil, fmt.Errorf("workspace tool permissions: %w", err)
	}
	if err := ValidateWorkspacePermissionRules(workspaceRules); err != nil {
		return nil, err
	}
	agentRules, err := ParsePermissionRulesForSource(agentTools, PermissionRuleSourceAgent)
	if err != nil {
		return nil, fmt.Errorf("agent tool permissions: %w", err)
	}
	sessionRules, err := ParsePermissionRulesForSource(runtimeSettings, PermissionRuleSourceSession)
	if err != nil {
		return nil, fmt.Errorf("session tool permissions: %w", err)
	}
	rules := make([]PermissionRule, 0, len(workspaceRules)+len(sessionRules)+len(agentRules))
	rules = append(rules, workspaceRules...)
	rules = append(rules, sessionRules...)
	rules = append(rules, agentRules...)
	return rules, nil
}

func ValidateWorkspacePermissionRules(rules []PermissionRule) error {
	if err := ValidatePermissionRules(rules); err != nil {
		return err
	}
	for index, rule := range rules {
		if strings.ToLower(strings.TrimSpace(rule.Behavior)) != PermissionRuleDeny {
			return fmt.Errorf("workspace permission_rules[%d].behavior must be deny", index)
		}
	}
	return nil
}

func InterventionPolicyFromSettings(raw json.RawMessage, mode string) (InterventionPolicy, error) {
	rules, err := ParsePermissionRules(raw)
	if err != nil {
		return InterventionPolicy{}, err
	}
	if strings.TrimSpace(mode) == "" {
		mode = ParseInterventionMode(raw)
	}
	return InterventionPolicy{Mode: mode, Rules: rules}, nil
}

func ValidatePermissionRules(rules []PermissionRule) error {
	if len(rules) > maxPermissionRules {
		return fmt.Errorf("permission_rules supports at most %d entries", maxPermissionRules)
	}
	seen := make(map[string]bool, len(rules))
	for index, rule := range rules {
		id := strings.TrimSpace(rule.ID)
		if id == "" || len(id) > 120 {
			return fmt.Errorf("permission_rules[%d].id must contain 1-120 characters", index)
		}
		if seen[id] {
			return fmt.Errorf("permission_rules contains duplicate id %q", id)
		}
		seen[id] = true

		tool := strings.ToLower(strings.TrimSpace(rule.Tool))
		if !filePermissionRuleTools[tool] {
			return fmt.Errorf("permission_rules[%d].tool %q is not a supported file tool", index, rule.Tool)
		}
		if strings.ToLower(strings.TrimSpace(rule.Argument)) != "path" {
			return fmt.Errorf("permission_rules[%d].argument must be path", index)
		}
		if err := validatePermissionPathPattern(rule.Pattern); err != nil {
			return fmt.Errorf("permission_rules[%d].pattern: %w", index, err)
		}
		if len(rule.Pattern) > 2048 {
			return fmt.Errorf("permission_rules[%d].pattern must not exceed 2048 characters", index)
		}
		if len(rule.Reason) > 500 {
			return fmt.Errorf("permission_rules[%d].reason must not exceed 500 characters", index)
		}
		switch strings.ToLower(strings.TrimSpace(rule.Behavior)) {
		case PermissionRuleAllow, PermissionRuleAsk, PermissionRuleDeny:
		default:
			return fmt.Errorf("permission_rules[%d].behavior must be allow, ask, or deny", index)
		}
	}
	return nil
}

func validatePermissionPathPattern(pattern string) error {
	pattern = normalizePermissionPath(pattern)
	if pattern == "" || pattern == "." {
		return fmt.Errorf("path pattern is required")
	}
	if strings.Contains(pattern, "**") && !strings.HasSuffix(pattern, "/**") {
		return fmt.Errorf("** is supported only as a recursive suffix")
	}
	check := strings.TrimSuffix(pattern, "/**")
	if _, err := path.Match(check, check); err != nil {
		return fmt.Errorf("invalid glob: %w", err)
	}
	return nil
}

func matchingPermissionRule(rules []PermissionRule, call Call) (PermissionRule, bool) {
	call = NormalizeCall(call)
	tool := call.Identifier + "." + call.APIName
	var arguments map[string]any
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return PermissionRule{}, false
	}
	value, _ := arguments["path"].(string)
	if strings.TrimSpace(value) == "" {
		return PermissionRule{}, false
	}

	matches := make([]PermissionRule, 0, len(rules))
	for _, rule := range rules {
		if strings.ToLower(strings.TrimSpace(rule.Tool)) != tool || !permissionPathMatches(rule.Pattern, value) {
			continue
		}
		matches = append(matches, rule)
	}
	// Workspace DENY is an organization-owned boundary and cannot be
	// overridden by Session, Agent, intervention mode, or full_access.
	if rule, ok := bestPermissionRule(matches, PermissionRuleSourceWorkspace); ok && strings.EqualFold(rule.Behavior, PermissionRuleDeny) {
		return rule, true
	}
	if rule, ok := bestPermissionRule(matches, PermissionRuleSourceSession); ok {
		return rule, true
	}
	if rule, ok := bestPermissionRule(matches, PermissionRuleSourceAgent); ok {
		return rule, true
	}
	// Preserve direct callers that construct source-less rules in tests or
	// embedded runtimes.
	return bestPermissionRule(matches, "")
}

func bestPermissionRule(rules []PermissionRule, source string) (PermissionRule, bool) {
	bestRank := 0
	var best PermissionRule
	for _, rule := range rules {
		if strings.ToLower(strings.TrimSpace(rule.Source)) != source {
			continue
		}
		if rank := permissionBehaviorRank(rule.Behavior); rank > bestRank {
			bestRank = rank
			best = rule
		}
	}
	return best, bestRank > 0
}

func permissionBehaviorRank(behavior string) int {
	switch strings.ToLower(strings.TrimSpace(behavior)) {
	case PermissionRuleDeny:
		return 3
	case PermissionRuleAsk:
		return 2
	case PermissionRuleAllow:
		return 1
	default:
		return 0
	}
}

func permissionPathMatches(pattern string, value string) bool {
	pattern = normalizePermissionPath(pattern)
	value = normalizePermissionPath(value)
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return value == prefix || strings.HasPrefix(value, prefix+"/")
	}
	matched, err := path.Match(pattern, value)
	return err == nil && matched
}

func normalizePermissionPath(value string) string {
	value = filepath.ToSlash(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	return path.Clean(value)
}
