package managedagents

import (
	"reflect"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
)

func TestAgentLoopPublicRuntimeEventTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event agentcore.RuntimeEvent
		want  []string
	}{
		{
			name:  "compaction started",
			event: agentcore.RuntimeEvent{Type: agentcore.EventContextCompacting},
			want:  []string{EventRuntimeContextCompacting},
		},
		{
			name:  "compaction completed",
			event: agentcore.RuntimeEvent{Type: agentcore.EventContextCompacted},
			want:  []string{EventRuntimeContextCompacted},
		},
		{
			name: "compaction failed",
			event: agentcore.RuntimeEvent{
				Type:    agentcore.EventRuntimeFailed,
				Payload: agentcore.Failure{Code: "context_compaction_failed"},
			},
			want: []string{EventRuntimeFailed, EventRuntimeContextCompactionFailed},
		},
		{
			name: "invalid compaction result",
			event: agentcore.RuntimeEvent{
				Type:    agentcore.EventRuntimeFailed,
				Payload: map[string]any{"code": "invalid_compaction_result"},
			},
			want: []string{EventRuntimeFailed, EventRuntimeContextCompactionFailed},
		},
		{
			name: "unrelated failure",
			event: agentcore.RuntimeEvent{
				Type:    agentcore.EventRuntimeFailed,
				Payload: agentcore.Failure{Code: "model_request_failed"},
			},
			want: []string{EventRuntimeFailed},
		},
		{
			name:  "other core event remains unchanged",
			event: agentcore.RuntimeEvent{Type: agentcore.EventModelRequested},
			want:  []string{string(agentcore.EventModelRequested)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := agentLoopPublicRuntimeEventTypes(test.event); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("agentLoopPublicRuntimeEventTypes() = %v, want %v", got, test.want)
			}
		})
	}
}
