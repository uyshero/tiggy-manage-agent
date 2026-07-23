package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

const permissionRuleApplyMaxAttempts = 3

type permissionRuleSelection struct {
	SuggestionID string `json:"permission_rule_suggestion_id"`
}

func (s *Server) applySelectedPermissionRule(ctx context.Context, sessionID, turnID, callID string, response json.RawMessage) error {
	selection, ok, err := parsePermissionRuleSelection(response)
	if err != nil || !ok {
		return err
	}
	intervention, err := loadPermissionRuleIntervention(ctx, s.store, sessionID, turnID, callID)
	if err != nil {
		return err
	}
	suggestion, err := validateSelectedPermissionRule(intervention, selection.SuggestionID)
	if err != nil {
		return err
	}
	switch suggestion.Scope {
	case tools.PermissionRuleSourceSession:
		return applySessionPermissionRule(ctx, s.store, intervention.SessionID, suggestion.Rule)
	case tools.PermissionRuleSourceAgent:
		if err := applyAgentPermissionRule(ctx, s.store, intervention.SessionID, suggestion.Rule); err != nil {
			return err
		}
		// Sessions are pinned to an immutable Agent config version. Mirror the
		// selected rule into the current Session so "allow this Agent" takes
		// effect immediately without upgrading unrelated Agent configuration
		// while a turn is paused for approval.
		return applySessionPermissionRule(ctx, s.store, intervention.SessionID, suggestion.Rule)
	default:
		return fmt.Errorf("%w: unsupported suggested permission rule scope %q", managedagents.ErrInvalid, suggestion.Scope)
	}
}

func parsePermissionRuleSelection(response json.RawMessage) (permissionRuleSelection, bool, error) {
	if len(response) == 0 || bytes.Equal(bytes.TrimSpace(response), []byte("null")) {
		return permissionRuleSelection{}, false, nil
	}
	var selection permissionRuleSelection
	if err := json.Unmarshal(response, &selection); err != nil {
		return permissionRuleSelection{}, false, fmt.Errorf("%w: invalid approval response", managedagents.ErrInvalid)
	}
	selection.SuggestionID = strings.TrimSpace(selection.SuggestionID)
	if selection.SuggestionID == "" {
		return permissionRuleSelection{}, false, nil
	}
	return selection, true, nil
}

func loadPermissionRuleIntervention(ctx context.Context, store managedagents.Store, sessionID, turnID, callID string) (managedagents.SessionIntervention, error) {
	interventions, err := managedagents.ListSessionInterventionsWithContext(ctx, store, sessionID, "")
	if err != nil {
		return managedagents.SessionIntervention{}, err
	}
	for _, intervention := range interventions {
		if intervention.TurnID == turnID && intervention.CallID == callID {
			if intervention.Kind != managedagents.InterventionKindToolApproval {
				return managedagents.SessionIntervention{}, fmt.Errorf("%w: permission rules can only be saved from tool approval", managedagents.ErrInvalid)
			}
			return intervention, nil
		}
	}
	return managedagents.SessionIntervention{}, managedagents.ErrNotFound
}

func validateSelectedPermissionRule(intervention managedagents.SessionIntervention, suggestionID string) (tools.PermissionRuleSuggestion, error) {
	var request struct {
		Suggestions []tools.PermissionRuleSuggestion `json:"suggested_permission_rules"`
	}
	if json.Unmarshal(intervention.Request, &request) != nil {
		return tools.PermissionRuleSuggestion{}, fmt.Errorf("%w: approval request has no valid permission rule suggestions", managedagents.ErrInvalid)
	}
	generated := tools.SuggestedPermissionRules(tools.Call{
		Identifier: intervention.ToolIdentifier, APIName: intervention.APIName, Arguments: intervention.Arguments,
	})
	for _, stored := range request.Suggestions {
		if stored.Rule.ID != suggestionID {
			continue
		}
		for _, expected := range generated {
			if samePermissionRuleSuggestion(stored, expected) {
				if err := tools.ValidatePermissionRules([]tools.PermissionRule{stored.Rule}); err != nil {
					return tools.PermissionRuleSuggestion{}, fmt.Errorf("%w: invalid suggested permission rule: %v", managedagents.ErrInvalid, err)
				}
				return stored, nil
			}
		}
		return tools.PermissionRuleSuggestion{}, fmt.Errorf("%w: suggested permission rule does not match the approved tool call", managedagents.ErrInvalid)
	}
	return tools.PermissionRuleSuggestion{}, fmt.Errorf("%w: unknown permission rule suggestion %q", managedagents.ErrInvalid, suggestionID)
}

func samePermissionRuleSuggestion(left, right tools.PermissionRuleSuggestion) bool {
	return left.Scope == right.Scope && left.Label == right.Label && samePermissionRule(left.Rule, right.Rule)
}

func samePermissionRule(left, right tools.PermissionRule) bool {
	return left.ID == right.ID && left.Tool == right.Tool && left.Argument == right.Argument &&
		left.Pattern == right.Pattern && left.Behavior == right.Behavior && left.Reason == right.Reason
}

func applySessionPermissionRule(ctx context.Context, store managedagents.Store, sessionID string, rule tools.PermissionRule) error {
	for attempt := 0; attempt < permissionRuleApplyMaxAttempts; attempt++ {
		session, err := managedagents.GetSessionWithContext(ctx, store, sessionID)
		if err != nil {
			return err
		}
		settings, changed, err := appendPermissionRule(session.RuntimeSettings, tools.PermissionRuleSourceSession, rule)
		if err != nil || !changed {
			return err
		}
		_, err = managedagents.UpdateSessionRuntimeSettingsWithContext(ctx, store, sessionID, managedagents.UpdateSessionRuntimeSettingsInput{
			RuntimeSettings: settings, ExpectedRevision: session.RuntimeSettingsRevision,
		})
		if !errors.Is(err, managedagents.ErrRevisionConflict) {
			return err
		}
	}
	return fmt.Errorf("%w: session permission settings changed repeatedly", managedagents.ErrRevisionConflict)
}

func applyAgentPermissionRule(ctx context.Context, store managedagents.Store, sessionID string, rule tools.PermissionRule) error {
	session, err := managedagents.GetSessionWithContext(ctx, store, sessionID)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < permissionRuleApplyMaxAttempts; attempt++ {
		agent, err := managedagents.GetAgentWithContext(ctx, store, session.AgentID)
		if err != nil {
			return err
		}
		agentTools, changed, err := appendPermissionRule(agent.ConfigVersion.Tools, tools.PermissionRuleSourceAgent, rule)
		if err != nil || !changed {
			return err
		}
		_, err = managedagents.CreateAgentConfigVersionWithContext(ctx, store, managedagents.CreateAgentConfigVersionInput{
			AgentID: agent.ID, ExpectedCurrentVersion: agent.CurrentConfigVersion,
			LLMProvider: agent.ConfigVersion.LLMProvider, LLMModel: agent.ConfigVersion.LLMModel,
			System: agent.ConfigVersion.System, Tools: agentTools, MCP: agent.ConfigVersion.MCP, Skills: agent.ConfigVersion.Skills,
		})
		if !errors.Is(err, managedagents.ErrRevisionConflict) {
			return err
		}
	}
	return fmt.Errorf("%w: Agent permission config changed repeatedly", managedagents.ErrRevisionConflict)
}

func appendPermissionRule(raw json.RawMessage, source string, rule tools.PermissionRule) (json.RawMessage, bool, error) {
	settings := map[string]json.RawMessage{}
	if len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, false, fmt.Errorf("%w: permission settings must be a JSON object", managedagents.ErrInvalid)
		}
	}
	rules, err := tools.ParsePermissionRulesForSource(raw, source)
	if err != nil {
		return nil, false, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	for _, existing := range rules {
		if existing.ID != rule.ID {
			continue
		}
		existing.Source = ""
		if samePermissionRule(existing, rule) {
			return raw, false, nil
		}
		return nil, false, fmt.Errorf("%w: permission rule id %q already exists with different content", managedagents.ErrConflict, rule.ID)
	}
	rules = append(rules, rule)
	if err := tools.ValidatePermissionRules(rules); err != nil {
		return nil, false, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	encodedRules, err := json.Marshal(rules)
	if err != nil {
		return nil, false, err
	}
	settings["permission_rules"] = encodedRules
	encoded, err := json.Marshal(settings)
	return encoded, true, err
}
