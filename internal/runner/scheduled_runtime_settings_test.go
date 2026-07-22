package runner

import (
	"encoding/json"
	"reflect"
	"testing"

	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/tools"
)

func TestRuntimeSettingsForTurnAppliesScheduledOverrideWithoutMutatingBase(t *testing.T) {
	base := json.RawMessage(`{"intervention_mode":"request_approval","human_interaction":{"enabled":true,"modes":["form"]},"completion_gate":{"max_retries":3}}`)
	original := append(json.RawMessage(nil), base...)
	payload := json.RawMessage(`{"content":[],"runtime_settings_override":{"intervention_mode":"approve_for_me","human_interaction":{"enabled":false,"fallback":"fail"}}}`)

	got := runtimeSettingsForTurn(base, payload)
	if tools.ParseInterventionMode(got) != tools.InterventionModeApproveForMe {
		t.Fatalf("expected scheduled approval mode, got %s", got)
	}
	if execution.HumanInteractionEnabled(got) {
		t.Fatalf("expected human interaction disabled, got %s", got)
	}
	if !reflect.DeepEqual(base, original) {
		t.Fatalf("base runtime settings were mutated: got %s want %s", base, original)
	}
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["completion_gate"]; !ok {
		t.Fatalf("expected unrelated settings to be preserved: %s", got)
	}
}

func TestRuntimeSettingsForTurnRejectsUnsafeScheduledOverrides(t *testing.T) {
	base := json.RawMessage(`{"intervention_mode":"request_approval","human_interaction":{"enabled":true}}`)
	tests := []json.RawMessage{
		json.RawMessage(`{"runtime_settings_override":{"intervention_mode":"request_approval","human_interaction":{"enabled":false,"fallback":"fail"}}}`),
		json.RawMessage(`{"runtime_settings_override":{"intervention_mode":"full_access","human_interaction":{"enabled":true,"fallback":"fail"}}}`),
		json.RawMessage(`{"runtime_settings_override":{"intervention_mode":"full_access","human_interaction":{"enabled":false,"fallback":"assistant_message"}}}`),
		json.RawMessage(`{"runtime_settings_override":{"intervention_mode":"full_access"}}`),
	}
	for _, payload := range tests {
		if got := runtimeSettingsForTurn(base, payload); !reflect.DeepEqual(got, base) {
			t.Errorf("unsafe override should be ignored: payload=%s got=%s", payload, got)
		}
	}
}
