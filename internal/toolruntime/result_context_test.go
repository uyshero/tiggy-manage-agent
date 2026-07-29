package toolruntime_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/toolruntime"
	"tiggy-manage-agent/internal/tools"
)

func TestToolRuntimeSendsBoundedArtifactBackedResultsToModel(t *testing.T) {
	registry := tools.NewRegistry(largeContextResultRuntime{})
	adapter := toolruntime.ToolRuntime{Snapshot: fullAccessSnapshot(t, registry)}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	call := coremodel.ToolCall{ID: "call_large", Name: "contexttest_large", Arguments: json.RawMessage(`{}`)}
	plan, err := adapter.Preflight(t.Context(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	batch, err := adapter.Execute(t.Context(), state, plan)
	if err != nil || len(batch.Results) != 1 {
		t.Fatalf("execute: batch=%#v err=%v", batch, err)
	}
	result := batch.Results[0]
	visible := result.Content[0].Text
	if strings.Contains(visible, "MIDDLE_SHOULD_NOT_REACH_MODEL") {
		t.Fatalf("model-visible tool result was not bounded: %s", visible)
	}
	for _, expected := range []string{"content_artifact_id", "state_artifact_id", "art_large", "artifact_read"} {
		if !strings.Contains(visible, expected) {
			t.Fatalf("bounded result lost recovery reference %q: %s", expected, visible)
		}
	}
	if len(result.State) <= tools.DefaultResultStateMaxBytes || !strings.Contains(string(result.State), "DURABLE_STATE_MARKER") {
		t.Fatalf("durable ToolResult state should remain complete outside model content: bytes=%d", len(result.State))
	}
}

type largeContextResultRuntime struct{}

func (largeContextResultRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "contexttest", Type: "builtin",
		Meta:      tools.Meta{Title: "Context test", Description: "Return a large Artifact-backed result."},
		Executors: []string{tools.ExecutorServer}, ApprovalPolicy: tools.ApprovalPolicyNever,
		API: []tools.API{{
			Name: "large", Description: "Return a large result.", Risk: tools.ToolRiskRead,
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`),
			Runtime:    &tools.RuntimePolicy{Allowed: []string{tools.ToolRuntimeAuto}},
		}},
	}
}

func (largeContextResultRuntime) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	content := strings.Repeat("A", 14000) + "MIDDLE_SHOULD_NOT_REACH_MODEL" + strings.Repeat("Z", 14000)
	state := json.RawMessage(`{"stdout":"` + strings.Repeat("DURABLE_STATE_MARKER", 1000) + `"}`)
	return tools.ExecutionResult{
		ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: content, State: state,
		Artifacts: []tools.ArtifactRef{{
			ArtifactID: "art_large", ObjectRefID: "obj_large", Name: "large-result.json",
			ArtifactType: "asset", DownloadPath: "/v1/sessions/session_1/artifacts/art_large/download",
		}},
	}, nil
}
