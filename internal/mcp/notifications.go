package mcp

import (
	"encoding/json"
	"strings"
)

func validProgressNotification(params json.RawMessage) bool {
	var payload struct {
		ProgressToken json.RawMessage `json:"progressToken"`
		Progress      json.RawMessage `json:"progress"`
		Total         json.RawMessage `json:"total,omitempty"`
	}
	if err := json.Unmarshal(params, &payload); err != nil || !validProgressToken(payload.ProgressToken) {
		return false
	}
	if !validJSONNumber(payload.Progress) {
		return false
	}
	if len(payload.Total) > 0 && !validJSONNumber(payload.Total) {
		return false
	}
	return true
}

func validProgressToken(raw json.RawMessage) bool {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

func validJSONNumber(raw json.RawMessage) bool {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	_, ok := value.(float64)
	return ok
}

func loggingNotificationLevel(params json.RawMessage) (string, bool) {
	var payload struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		return "unknown", false
	}
	level := NormalizeLoggingLevel(payload.Level)
	if level == "" {
		return "unknown", false
	}
	return level, true
}

func cloneInt64Map(value map[string]int64) map[string]int64 {
	if len(value) == 0 {
		return nil
	}
	cloned := make(map[string]int64, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}
