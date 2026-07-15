package managedagents

import (
	"encoding/json"
	"testing"
)

func TestInterruptedToolResultPayloadClosesToolChain(t *testing.T) {
	payload, err := interruptedToolResultPayload(SessionIntervention{
		TurnID:         "turn_1",
		CallID:         "call_1",
		ToolIdentifier: "default",
		APIName:        "edit_file",
	})
	if err != nil {
		t.Fatalf("build interrupted tool result: %v", err)
	}

	var decoded struct {
		TurnID string `json:"turn_id"`
		Data   struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
			APIName    string `json:"api_name"`
			Status     string `json:"status"`
			Success    bool   `json:"success"`
			Reason     string `json:"reason"`
			Retryable  bool   `json:"retryable"`
			Error      struct {
				Type string `json:"type"`
			} `json:"error"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode interrupted tool result: %v", err)
	}
	if decoded.TurnID != "turn_1" || decoded.Data.ID != "call_1" || decoded.Data.Identifier != "default" || decoded.Data.APIName != "edit_file" {
		t.Fatalf("unexpected interrupted tool result identity: %+v", decoded)
	}
	if decoded.Data.Status != "canceled" || decoded.Data.Success || decoded.Data.Reason != "user_interrupted" || decoded.Data.Retryable || decoded.Data.Error.Type != "tool_canceled" {
		t.Fatalf("unexpected interrupted tool result outcome: %+v", decoded.Data)
	}
}
