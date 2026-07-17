package managedagents

import (
	"encoding/json"
	"testing"
)

func TestAgentConfigUpdatePolicy(t *testing.T) {
	tests := []struct {
		name     string
		settings json.RawMessage
		want     string
		wantErr  bool
	}{
		{name: "empty", want: AgentConfigUpdateFollowLatest},
		{name: "object default", settings: json.RawMessage(`{}`), want: AgentConfigUpdateFollowLatest},
		{name: "follow latest", settings: json.RawMessage(`{"agent_config_update_policy":"follow_latest"}`), want: AgentConfigUpdateFollowLatest},
		{name: "pinned", settings: json.RawMessage(`{"agent_config_update_policy":"pinned"}`), want: AgentConfigUpdatePinned},
		{name: "unknown", settings: json.RawMessage(`{"agent_config_update_policy":"manual"}`), wantErr: true},
		{name: "array", settings: json.RawMessage(`[]`), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := AgentConfigUpdatePolicy(test.settings)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve policy: %v", err)
			}
			if got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}
