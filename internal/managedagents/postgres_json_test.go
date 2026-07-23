package managedagents

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPostgresSafeJSONReplacesNullCharacters(t *testing.T) {
	payload := json.RawMessage(`{"text":"before\u0000after","nested":["safe",{"key\u0000":"\u0000"}]}`)

	sanitized, err := postgresSafeJSON(payload)
	if err != nil {
		t.Fatalf("sanitize JSON: %v", err)
	}
	if strings.Contains(string(sanitized), `\u0000`) {
		t.Fatalf("sanitized JSON still contains a null escape: %s", sanitized)
	}
	var decoded map[string]any
	if err := json.Unmarshal(sanitized, &decoded); err != nil {
		t.Fatalf("decode sanitized JSON: %v", err)
	}
	if decoded["text"] != "before\uFFFDafter" {
		t.Fatalf("unexpected sanitized text: %#v", decoded["text"])
	}
	nested := decoded["nested"].([]any)[1].(map[string]any)
	if nested["key\uFFFD"] != "\uFFFD" {
		t.Fatalf("unexpected sanitized nested object: %#v", nested)
	}
}

func TestPostgresSafeJSONPreservesLiteralEscapeText(t *testing.T) {
	payload := json.RawMessage(`{"text":"literal \\u0000"}`)

	sanitized, err := postgresSafeJSON(payload)
	if err != nil {
		t.Fatalf("sanitize JSON: %v", err)
	}
	if string(sanitized) != string(payload) {
		t.Fatalf("literal escape text changed: got %s want %s", sanitized, payload)
	}
}

func TestPostgresSafeJSONReplacesNullInTopLevelString(t *testing.T) {
	sanitized, err := postgresSafeJSON(json.RawMessage(`"before\u0000after"`))
	if err != nil {
		t.Fatalf("sanitize top-level string: %v", err)
	}
	var decoded string
	if err := json.Unmarshal(sanitized, &decoded); err != nil {
		t.Fatalf("decode sanitized top-level string: %v", err)
	}
	if decoded != "before\uFFFDafter" {
		t.Fatalf("unexpected sanitized top-level string: %q", decoded)
	}
}

func TestPostgresSafeJSONRejectsInvalidPayload(t *testing.T) {
	if _, err := postgresSafeJSON(json.RawMessage(`{"broken"`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func BenchmarkPostgresSafeJSONWithoutNullEscape(b *testing.B) {
	payload := json.RawMessage(`{"turn_id":"turn_1","message":"normal event","data":{"status":"ok"}}`)
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, err := postgresSafeJSON(payload); err != nil {
			b.Fatal(err)
		}
	}
}
