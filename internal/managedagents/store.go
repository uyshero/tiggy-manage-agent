package managedagents

import (
	"encoding/json"
	"errors"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrInvalid    = errors.New("invalid input")
	ErrTerminated = errors.New("session terminated")
)

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func agentLLMProvider(input CreateAgentInput) string {
	return defaultString(input.LLMProvider, "fake")
}

func agentLLMModel(input CreateAgentInput) string {
	return defaultString(input.LLMModel, input.Model)
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}

func mustRaw(value string) json.RawMessage {
	return json.RawMessage(value)
}

func statusPayload(status string, turnID string) json.RawMessage {
	payload := map[string]string{"status": status}
	if turnID != "" {
		payload["turn_id"] = turnID
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return mustRaw(`{"status":"` + status + `"}`)
	}
	return encoded
}

func failedTurnIdlePayload(turnID string, reason string) json.RawMessage {
	payload := map[string]string{
		"status":           "idle",
		"last_turn_status": "failed",
	}
	if turnID != "" {
		payload["turn_id"] = turnID
	}
	if reason != "" {
		payload["reason"] = reason
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return mustRaw(`{"status":"idle","last_turn_status":"failed"}`)
	}
	return encoded
}

func payloadWithTurnID(payload json.RawMessage, turnID string) json.RawMessage {
	var object map[string]any
	if len(payload) == 0 || string(payload) == "null" {
		object = make(map[string]any)
	} else if err := json.Unmarshal(payload, &object); err != nil {
		object = make(map[string]any)
	} else if object == nil {
		object = make(map[string]any)
	}

	object["turn_id"] = turnID
	encoded, err := json.Marshal(object)
	if err != nil {
		return payload
	}
	return encoded
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}

	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return value
}
