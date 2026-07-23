package agentcontrol

import (
	"encoding/json"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
)

func TestControlMessageFramesSteeringAsActiveTurnAdjustment(t *testing.T) {
	t.Parallel()

	message, err := controlMessage(managedagents.Event{
		ID:      "steer_1",
		Type:    managedagents.EventUserSteer,
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"先验证数据库，再继续部署"}]}`),
	})
	if err != nil {
		t.Fatalf("controlMessage() error = %v", err)
	}
	if message.Role != coremodel.RoleUser || message.Visibility != coremodel.VisibilityPublic {
		t.Fatalf("unexpected message role or visibility: %+v", message)
	}
	if string(message.Metadata) != `{"control_mode":"steer"}` {
		t.Fatalf("unexpected steering metadata: %s", message.Metadata)
	}
	if len(message.Content) != 2 {
		t.Fatalf("expected steering frame and user instruction, got %+v", message.Content)
	}
	frame := message.Content[0].Text
	for _, expected := range []string{
		"[Steering update for the active turn]",
		"Preserve the original objective",
		"Do not apologize",
	} {
		if !strings.Contains(frame, expected) {
			t.Fatalf("steering frame missing %q: %q", expected, frame)
		}
	}
	if message.Content[1].Text != "先验证数据库，再继续部署" {
		t.Fatalf("unexpected steering instruction: %+v", message.Content[1])
	}
}

func TestControlMessageLeavesFollowUpAsOrdinaryUserMessage(t *testing.T) {
	t.Parallel()

	message, err := controlMessage(managedagents.Event{
		ID:      "follow_up_1",
		Type:    managedagents.EventUserFollowUp,
		Payload: json.RawMessage(`{"text":"部署完成后再检查日志"}`),
	})
	if err != nil {
		t.Fatalf("controlMessage() error = %v", err)
	}
	if len(message.Metadata) != 0 {
		t.Fatalf("follow-up should not have steering metadata: %s", message.Metadata)
	}
	if len(message.Content) != 1 || message.Content[0].Text != "部署完成后再检查日志" {
		t.Fatalf("unexpected follow-up content: %+v", message.Content)
	}
}
