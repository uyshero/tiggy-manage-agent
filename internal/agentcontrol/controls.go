package agentcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
)

type SessionControls struct {
	Reader managedagents.SessionControlReader
}

const steeringContextPreamble = `[Steering update for the active turn]
This instruction adjusts how to continue the task already in progress. Preserve the original objective, accepted plan, completed work, and unrelated remaining steps unless the user explicitly cancels or replaces them. Apply the steering instruction at the next safe point. Do not apologize merely because the execution direction changed.

User steering instruction:`

var _ agentcore.ControlPort = SessionControls{}

func (c SessionControls) Drain(ctx context.Context, state agentcore.State, _ agentcore.ControlPoint) ([]agentcore.ControlCommand, error) {
	if c.Reader == nil {
		return nil, nil
	}
	events, err := c.Reader.ListSessionTurnControlEventsContext(ctx, state.SessionID, state.TurnID, state.ControlCursor)
	if err != nil {
		return nil, err
	}
	commands := make([]agentcore.ControlCommand, 0, len(events))
	for _, event := range events {
		command := agentcore.ControlCommand{Seq: event.Seq}
		switch event.Type {
		case managedagents.EventUserSteer:
			command.Mode = agentcore.ControlSteer
		case managedagents.EventUserFollowUp:
			command.Mode = agentcore.ControlFollowUp
		case managedagents.EventUserInterrupt:
			command.Mode = agentcore.ControlCancel
			command.Reason = "agent execution was interrupted by the user"
		default:
			continue
		}
		if command.Mode != agentcore.ControlCancel {
			message, err := controlMessage(event)
			if err != nil {
				return nil, err
			}
			command.Message = &message
		}
		commands = append(commands, command)
	}
	return commands, nil
}

func controlMessage(event managedagents.Event) (coremodel.Message, error) {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return coremodel.Message{}, fmt.Errorf("decode %s control payload: %w", event.Type, err)
	}
	content := make([]coremodel.Content, 0, len(payload.Content)+1)
	for _, part := range payload.Content {
		if part.Type != "" && part.Type != "text" {
			return coremodel.Message{}, fmt.Errorf("%s control payload contains unsupported content type %q", event.Type, part.Type)
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			content = append(content, coremodel.Content{Type: coremodel.ContentText, Text: text})
		}
	}
	if len(content) == 0 && strings.TrimSpace(payload.Text) != "" {
		content = append(content, coremodel.Content{Type: coremodel.ContentText, Text: strings.TrimSpace(payload.Text)})
	}
	metadata := json.RawMessage(nil)
	if event.Type == managedagents.EventUserSteer {
		content = append([]coremodel.Content{{Type: coremodel.ContentText, Text: steeringContextPreamble}}, content...)
		metadata = json.RawMessage(`{"control_mode":"steer"}`)
	}
	message := coremodel.Message{
		ID: "control_" + event.ID, Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic, Content: content, Metadata: metadata,
	}
	if err := message.Validate(); err != nil {
		return coremodel.Message{}, fmt.Errorf("invalid %s control message: %w", event.Type, err)
	}
	return message, nil
}
