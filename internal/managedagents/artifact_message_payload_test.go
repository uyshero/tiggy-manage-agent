package managedagents

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPayloadWithArtifactIDs(t *testing.T) {
	payload := payloadWithArtifactIDs(json.RawMessage(`{"content":"done","artifact_ids":["old"]}`), []string{"art_2", "art_1", "art_2", ""})

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if decoded["content"] != "done" {
		t.Fatalf("content was not preserved: %+v", decoded)
	}
	if got, want := stringSlice(decoded["artifact_ids"]), []string{"art_2", "art_1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("artifact_ids = %+v, want %+v", got, want)
	}
}

func TestPayloadWithArtifactIDsCreatesObjectForInvalidPayload(t *testing.T) {
	payload := payloadWithArtifactIDs(json.RawMessage(`not-json`), nil)

	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got, want := stringSlice(decoded["artifact_ids"]), []string{}; !reflect.DeepEqual(got, want) {
		t.Fatalf("artifact_ids = %+v, want %+v", got, want)
	}
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.(string))
	}
	return result
}
