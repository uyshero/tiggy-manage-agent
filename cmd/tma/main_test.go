package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStreamSSEPassesThroughOrdinaryEvents(t *testing.T) {
	input := "id: evt_1\nevent: session.status_idle\ndata: {\"seq\":2,\"type\":\"session.status_idle\"}\n\n"
	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}
	if output.String() != input {
		t.Fatalf("expected ordinary event passthrough, got %q", output.String())
	}
}

func TestStreamSSEFormatsToolInterventionRequired(t *testing.T) {
	input := "id: evt_2\n" +
		"event: runtime.tool_intervention_required\n" +
		"data: {\"seq\":7,\"type\":\"runtime.tool_intervention_required\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Tool call requires approval before execution.\",\"data\":{\"id\":\"call_edit\",\"identifier\":\"tma.local_system\",\"api_name\":\"edit_file\",\"intervention_mode\":\"request_approval\",\"reason\":\"optional\"}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "approval required") {
		t.Fatalf("expected readable approval header, got %q", text)
	}
	if !strings.Contains(text, "tool: tma.local_system.edit_file") {
		t.Fatalf("expected tool summary, got %q", text)
	}
	if !strings.Contains(text, "mode: request_approval") {
		t.Fatalf("expected intervention mode, got %q", text)
	}
	if strings.Contains(text, "event: runtime.tool_intervention_required") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}
