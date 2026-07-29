package toolresult

import (
	"encoding/json"
	"testing"
)

func TestNormalizeErrorDefaultsToStableExecutionFailureCode(t *testing.T) {
	err := NormalizeError("", "", true, true, true)
	if err.Type != CodeToolExecutionFailed || err.Message == "" || !err.Recoverable || !err.Retryable || !err.Redacted {
		t.Fatalf("NormalizeError() = %+v", err)
	}
}

func TestMessageProjectsRecoverableErrorFlags(t *testing.T) {
	message := Message(Envelope{
		ID:      "call_1",
		APIName: "default_run_command",
		Content: "failed",
		Error: &Error{
			Type:        CodeToolExecutionFailed,
			Message:     "failed",
			Recoverable: true,
			Retryable:   true,
			Redacted:    true,
		},
	})
	var payload struct {
		ProtocolVersion string `json:"protocol_version"`
		Success         bool   `json:"success"`
		Recoverable     bool   `json:"recoverable"`
		Retryable       bool   `json:"retryable"`
		Redacted        bool   `json:"redacted"`
		Error           Error  `json:"error"`
	}
	if err := json.Unmarshal([]byte(message), &payload); err != nil {
		t.Fatalf("decode message: %v", err)
	}
	if payload.ProtocolVersion != ProtocolVersion || payload.Success || !payload.Recoverable || !payload.Retryable || !payload.Redacted ||
		payload.Error.Type != CodeToolExecutionFailed || !payload.Error.Recoverable || !payload.Error.Retryable || !payload.Error.Redacted {
		t.Fatalf("message flags = %+v", payload)
	}
}
