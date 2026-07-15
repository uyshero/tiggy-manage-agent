package mcp

import (
	"encoding/json"
	"testing"
)

func TestValidProgressNotification(t *testing.T) {
	tests := []struct {
		name   string
		params string
		valid  bool
	}{
		{name: "string token", params: `{"progressToken":"request-1","progress":1}`, valid: true},
		{name: "number token and total", params: `{"progressToken":42,"progress":1.5,"total":3}`, valid: true},
		{name: "missing token", params: `{"progress":1}`, valid: false},
		{name: "null token", params: `{"progressToken":null,"progress":1}`, valid: false},
		{name: "object token", params: `{"progressToken":{},"progress":1}`, valid: false},
		{name: "missing progress", params: `{"progressToken":"request-1"}`, valid: false},
		{name: "null progress", params: `{"progressToken":"request-1","progress":null}`, valid: false},
		{name: "string progress", params: `{"progressToken":"request-1","progress":"1"}`, valid: false},
		{name: "null total", params: `{"progressToken":"request-1","progress":1,"total":null}`, valid: false},
		{name: "invalid JSON", params: `{`, valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validProgressNotification(json.RawMessage(test.params)); got != test.valid {
				t.Fatalf("validProgressNotification() = %t, want %t", got, test.valid)
			}
		})
	}
}
