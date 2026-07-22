package runner

import (
	"encoding/json"
	"strings"

	"tiggy-manage-agent/internal/tools"
)

// runtimeSettingsForTurn applies the narrow, non-interactive override emitted by
// the scheduler without mutating the Session's persisted runtime settings.
func runtimeSettingsForTurn(base, userPayload json.RawMessage) json.RawMessage {
	var envelope struct {
		Override json.RawMessage `json:"runtime_settings_override"`
	}
	if len(userPayload) == 0 || json.Unmarshal(userPayload, &envelope) != nil || len(envelope.Override) == 0 {
		return append(json.RawMessage(nil), base...)
	}
	var override struct {
		InterventionMode string `json:"intervention_mode"`
		HumanInteraction *struct {
			Enabled  *bool  `json:"enabled"`
			Fallback string `json:"fallback"`
		} `json:"human_interaction"`
	}
	if json.Unmarshal(envelope.Override, &override) != nil {
		return append(json.RawMessage(nil), base...)
	}
	mode := strings.ToLower(strings.TrimSpace(override.InterventionMode))
	if mode != tools.InterventionModeApproveForMe && mode != tools.InterventionModeFullAccess {
		return append(json.RawMessage(nil), base...)
	}
	if override.HumanInteraction == nil || override.HumanInteraction.Enabled == nil || *override.HumanInteraction.Enabled {
		return append(json.RawMessage(nil), base...)
	}
	if strings.ToLower(strings.TrimSpace(override.HumanInteraction.Fallback)) != "fail" {
		return append(json.RawMessage(nil), base...)
	}

	settings := map[string]any{}
	if len(base) > 0 && string(base) != "null" && json.Unmarshal(base, &settings) != nil {
		return append(json.RawMessage(nil), base...)
	}
	humanInteraction := map[string]any{}
	if existing, ok := settings["human_interaction"].(map[string]any); ok {
		for key, value := range existing {
			humanInteraction[key] = value
		}
	}
	settings["intervention_mode"] = mode
	humanInteraction["enabled"] = false
	humanInteraction["fallback"] = "fail"
	settings["human_interaction"] = humanInteraction
	merged, err := json.Marshal(settings)
	if err != nil {
		return append(json.RawMessage(nil), base...)
	}
	return merged
}
