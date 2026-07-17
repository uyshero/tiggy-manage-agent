package managedagents

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	AgentConfigUpdateFollowLatest = "follow_latest"
	AgentConfigUpdatePinned       = "pinned"
)

// AgentConfigUpdatePolicy returns the policy used at the boundary of a new
// Turn. Existing Sessions default to following the Agent's latest config.
func AgentConfigUpdatePolicy(settings json.RawMessage) (string, error) {
	if len(settings) == 0 || string(settings) == "null" {
		return AgentConfigUpdateFollowLatest, nil
	}
	var value struct {
		Policy string `json:"agent_config_update_policy"`
	}
	if err := json.Unmarshal(settings, &value); err != nil {
		return "", fmt.Errorf("%w: runtime_settings must be a JSON object", ErrInvalid)
	}
	switch strings.TrimSpace(strings.ToLower(value.Policy)) {
	case "", AgentConfigUpdateFollowLatest:
		return AgentConfigUpdateFollowLatest, nil
	case AgentConfigUpdatePinned:
		return AgentConfigUpdatePinned, nil
	default:
		return "", fmt.Errorf("%w: unsupported agent_config_update_policy %q", ErrInvalid, value.Policy)
	}
}
