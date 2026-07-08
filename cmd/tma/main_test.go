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

func TestStreamSSEFormatsToolInterventionApproved(t *testing.T) {
	input := "id: evt_3\n" +
		"event: runtime.tool_intervention_approved\n" +
		"data: {\"seq\":8,\"type\":\"runtime.tool_intervention_approved\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Tool call approved by user.\",\"data\":{\"id\":\"call_edit\",\"identifier\":\"tma.local_system\",\"api_name\":\"edit_file\",\"intervention_mode\":\"request_approval\",\"approval_source\":\"user\"}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "approval approved") {
		t.Fatalf("expected readable approved header, got %q", text)
	}
	if !strings.Contains(text, "tool: tma.local_system.edit_file") {
		t.Fatalf("expected tool summary, got %q", text)
	}
	if strings.Contains(text, "event: runtime.tool_intervention_approved") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}

func TestStreamSSEFormatsToolInterventionRejected(t *testing.T) {
	input := "id: evt_4\n" +
		"event: runtime.tool_intervention_rejected\n" +
		"data: {\"seq\":9,\"type\":\"runtime.tool_intervention_rejected\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Tool call rejected by user.\",\"data\":{\"id\":\"call_edit\",\"identifier\":\"tma.local_system\",\"api_name\":\"edit_file\",\"intervention_mode\":\"request_approval\",\"decision_reason\":\"not now\"}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "approval rejected") {
		t.Fatalf("expected readable rejected header, got %q", text)
	}
	if !strings.Contains(text, "message: Tool call rejected by user.") {
		t.Fatalf("expected rejection message, got %q", text)
	}
	if strings.Contains(text, "event: runtime.tool_intervention_rejected") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}

func TestStreamSSEFormatsToolResult(t *testing.T) {
	input := "id: evt_5\n" +
		"event: runtime.tool_result\n" +
		"data: {\"seq\":10,\"type\":\"runtime.tool_result\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Received approved tool result.\",\"data\":{\"id\":\"call_read\",\"identifier\":\"tma.local_system\",\"api_name\":\"read_file\",\"content\":\"hello\\nworld\",\"success\":true}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "tool result") {
		t.Fatalf("expected readable result header, got %q", text)
	}
	if !strings.Contains(text, "success: true") {
		t.Fatalf("expected success summary, got %q", text)
	}
	if !strings.Contains(text, "content: hello\\nworld") {
		t.Fatalf("expected content summary, got %q", text)
	}
	if strings.Contains(text, "event: runtime.tool_result") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}
