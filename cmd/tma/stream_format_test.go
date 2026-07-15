package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/sdk/tma"
)

func TestStreamSDKEventsFormatsStructuredEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/sessions/sesn_1/events/stream" || r.URL.Query().Get("after_seq") != "0" {
			t.Fatalf("unexpected stream request %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"evt_1\",\"session_id\":\"sesn_1\",\"turn_id\":\"turn_1\",\"seq\":1,\"type\":\"user.message\",\"payload\":{\"content\":[{\"type\":\"text\",\"text\":\"hello\"}],\"turn_id\":\"turn_1\"},\"created_at\":\"2026-07-14T00:00:00Z\"}\n\n")
	}))
	defer server.Close()

	client, err := tma.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	stream, err := client.Sessions.Events(ctx, "sesn_1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	output := &cancelAfterWriteBuffer{cancel: cancel}
	err = streamSDKEvents(ctx, stream, output, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected local cancellation after first event, got %v", err)
	}
	if output.String() != "user> hello\n\n" {
		t.Fatalf("unexpected structured event output %q", output.String())
	}
}

type cancelAfterWriteBuffer struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelAfterWriteBuffer) Write(payload []byte) (int, error) {
	written, err := w.Buffer.Write(payload)
	w.cancel()
	return written, err
}

func (w *cancelAfterWriteBuffer) WriteString(value string) (int, error) {
	written, err := w.Buffer.WriteString(value)
	w.cancel()
	return written, err
}

func TestStreamSSEPassesThroughOrdinaryEvents(t *testing.T) {
	input := "id: evt_1\nevent: runtime.unknown\ndata: {\"seq\":2,\"type\":\"runtime.unknown\"}\n\n"
	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}
	if output.String() != input {
		t.Fatalf("expected ordinary event passthrough, got %q", output.String())
	}
}

func TestStreamSSEFormatsChatMessages(t *testing.T) {
	input := "id: evt_1\n" +
		"event: user.message\n" +
		"data: {\"seq\":4,\"type\":\"user.message\",\"payload\":{\"turn_id\":\"turn_123\",\"content\":[{\"type\":\"text\",\"text\":\"hello\"}]}}\n\n" +
		"id: evt_2\n" +
		"event: agent.message\n" +
		"data: {\"seq\":5,\"type\":\"agent.message\",\"payload\":{\"turn_id\":\"turn_123\",\"content\":[{\"type\":\"text\",\"text\":\"hi\\nthere\"}]}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "user> hello") {
		t.Fatalf("expected readable user message, got %q", text)
	}
	if !strings.Contains(text, "agent>\n  hi\n  there") {
		t.Fatalf("expected readable multiline agent message, got %q", text)
	}
	if strings.Contains(text, "event: user.message") || strings.Contains(text, "event: agent.message") {
		t.Fatalf("expected formatted output instead of raw SSE chunks, got %q", text)
	}
}

func TestStreamSSEFormatsSessionStatus(t *testing.T) {
	input := "id: evt_1\n" +
		"event: session.status_idle\n" +
		"data: {\"seq\":2,\"type\":\"session.status_idle\",\"payload\":{\"status\":\"idle\",\"turn_id\":\"turn_123\",\"last_turn_status\":\"failed\",\"reason\":\"command turn failed\"}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "status: idle (turn turn_123)") {
		t.Fatalf("expected readable status, got %q", text)
	}
	if !strings.Contains(text, "last_turn_status: failed") || !strings.Contains(text, "reason: command turn failed") {
		t.Fatalf("expected status details, got %q", text)
	}
}

func TestStreamSSEFormatsSessionConfigUpdated(t *testing.T) {
	input := "id: evt_1\n" +
		"event: session.config_updated\n" +
		"data: {\"seq\":12,\"type\":\"session.config_updated\",\"payload\":{\"old_agent_config_version\":1,\"new_agent_config_version\":2,\"updated_by\":\"tester\"}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "session config updated") {
		t.Fatalf("expected readable config update header, got %q", text)
	}
	if !strings.Contains(text, "agent_config_version: v1 -> v2") || !strings.Contains(text, "updated_by: tester") {
		t.Fatalf("expected config update details, got %q", text)
	}
	if strings.Contains(text, "event: session.config_updated") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}

func TestStreamSSEFormatsLLMDelta(t *testing.T) {
	input := "id: evt_1\n" +
		"event: runtime.llm_delta\n" +
		"data: {\"seq\":6,\"type\":\"runtime.llm_delta\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Received streamed LLM text.\",\"data\":{\"index\":1,\"text\":\"hello\"}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	if !strings.Contains(output.String(), "delta> hello") {
		t.Fatalf("expected readable delta, got %q", output.String())
	}
}

func TestStreamSSEFormatsReasoningChunkWithoutDuplicatingTextChunk(t *testing.T) {
	input := "id: evt_1\n" +
		"event: runtime.llm_chunk\n" +
		"data: {\"seq\":6,\"type\":\"runtime.llm_chunk\",\"payload\":{\"turn_id\":\"turn_123\",\"data\":{\"index\":1,\"type\":\"reasoning\",\"text\":\"check evidence\"}}}\n\n" +
		"id: evt_2\n" +
		"event: runtime.llm_chunk\n" +
		"data: {\"seq\":7,\"type\":\"runtime.llm_chunk\",\"payload\":{\"turn_id\":\"turn_123\",\"data\":{\"index\":2,\"type\":\"text\",\"text\":\"hello\"}}}\n\n" +
		"id: evt_3\n" +
		"event: runtime.llm_delta\n" +
		"data: {\"seq\":8,\"type\":\"runtime.llm_delta\",\"payload\":{\"turn_id\":\"turn_123\",\"data\":{\"index\":2,\"text\":\"hello\"}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "reasoning> check evidence") || strings.Count(text, "hello") != 1 {
		t.Fatalf("expected reasoning and one compatible text delta, got %q", text)
	}
	if strings.Contains(text, "event: runtime.llm_chunk") {
		t.Fatalf("expected formatted chunk output, got %q", text)
	}
}

func TestStreamSSEHandlesStructuredLLMChunks(t *testing.T) {
	input := "id: evt_1\n" +
		"event: runtime.llm_chunk\n" +
		"data: {\"seq\":6,\"type\":\"runtime.llm_chunk\",\"payload\":{\"data\":{\"index\":1,\"type\":\"tool_call\",\"tool_call\":{\"id\":\"call_1\",\"name\":\"default.read_file\",\"arguments\":\"{\"}}}}\n\n" +
		"id: evt_2\n" +
		"event: runtime.llm_chunk\n" +
		"data: {\"seq\":7,\"type\":\"runtime.llm_chunk\",\"payload\":{\"data\":{\"index\":2,\"type\":\"usage\",\"usage\":{\"total_tokens\":9}}}}\n\n" +
		"id: evt_3\n" +
		"event: runtime.llm_chunk\n" +
		"data: {\"seq\":8,\"type\":\"runtime.llm_chunk\",\"payload\":{\"data\":{\"index\":3,\"type\":\"stop\",\"finish_reason\":\"tool_calls\"}}}\n\n" +
		"id: evt_4\n" +
		"event: runtime.llm_chunk\n" +
		"data: {\"seq\":9,\"type\":\"runtime.llm_chunk\",\"payload\":{\"data\":{\"index\":4,\"type\":\"error\",\"error\":{\"class\":\"server\",\"message\":\"stream unavailable\"}}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "llm-error> stream unavailable") {
		t.Fatalf("expected readable stream error, got %q", text)
	}
	if strings.Contains(text, "tool_call") || strings.Contains(text, "total_tokens") || strings.Contains(text, "finish_reason") || strings.Contains(text, "event: runtime.llm_chunk") {
		t.Fatalf("expected non-text structured chunks to avoid raw SSE output, got %q", text)
	}
}

func TestStreamSSEFormatsToolInterventionRequired(t *testing.T) {
	input := "id: evt_2\n" +
		"event: runtime.tool_intervention_required\n" +
		"data: {\"seq\":7,\"type\":\"runtime.tool_intervention_required\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Tool call requires approval before execution.\",\"data\":{\"id\":\"call_edit\",\"identifier\":\"default\",\"api_name\":\"edit_file\",\"intervention_mode\":\"request_approval\",\"reason\":\"optional\"}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "approval required") {
		t.Fatalf("expected readable approval header, got %q", text)
	}
	if !strings.Contains(text, "tool: default.edit_file") {
		t.Fatalf("expected tool summary, got %q", text)
	}
	if !strings.Contains(text, "mode: request_approval") {
		t.Fatalf("expected intervention mode, got %q", text)
	}
	if strings.Contains(text, "event: runtime.tool_intervention_required") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}

func TestStreamSSEWithInterventionsHandlesRequiredEvent(t *testing.T) {
	input := "id: evt_2\n" +
		"event: runtime.tool_intervention_required\n" +
		"data: {\"seq\":7,\"type\":\"runtime.tool_intervention_required\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Tool call requires approval before execution.\",\"data\":{\"id\":\"call_edit\",\"identifier\":\"default\",\"api_name\":\"edit_file\",\"intervention_mode\":\"request_approval\",\"reason\":\"optional\"}}}\n\n"

	var output bytes.Buffer
	var handled []toolInterventionEvent
	err := streamSSEWithInterventions(strings.NewReader(input), &output, func(event toolInterventionEvent) error {
		handled = append(handled, event)
		return nil
	})
	if err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	if len(handled) != 1 {
		t.Fatalf("expected 1 handled intervention, got %#v", handled)
	}
	if handled[0].TurnID != "turn_123" || handled[0].CallID != "call_edit" {
		t.Fatalf("unexpected handled intervention: %#v", handled[0])
	}
}

func TestStreamSSEFormatsToolInterventionApproved(t *testing.T) {
	input := "id: evt_3\n" +
		"event: runtime.tool_intervention_approved\n" +
		"data: {\"seq\":8,\"type\":\"runtime.tool_intervention_approved\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Tool call approved by user.\",\"data\":{\"id\":\"call_edit\",\"identifier\":\"default\",\"api_name\":\"edit_file\",\"intervention_mode\":\"request_approval\",\"approval_source\":\"user\"}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "approval approved") {
		t.Fatalf("expected readable approved header, got %q", text)
	}
	if !strings.Contains(text, "tool: default.edit_file") {
		t.Fatalf("expected tool summary, got %q", text)
	}
	if strings.Contains(text, "event: runtime.tool_intervention_approved") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}

func TestStreamSSEFormatsToolInterventionRejected(t *testing.T) {
	input := "id: evt_4\n" +
		"event: runtime.tool_intervention_rejected\n" +
		"data: {\"seq\":9,\"type\":\"runtime.tool_intervention_rejected\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Tool call rejected by user.\",\"data\":{\"id\":\"call_edit\",\"identifier\":\"default\",\"api_name\":\"edit_file\",\"intervention_mode\":\"request_approval\",\"decision_reason\":\"not now\"}}}\n\n"

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
		"data: {\"seq\":10,\"type\":\"runtime.tool_result\",\"payload\":{\"turn_id\":\"turn_123\",\"message\":\"Received approved tool result.\",\"data\":{\"id\":\"call_read\",\"identifier\":\"default\",\"api_name\":\"read_file\",\"content\":\"hello\\nworld\",\"artifacts\":[{\"artifact_id\":\"art_000001\",\"object_ref_id\":\"obj_000001\",\"name\":\"read_file.json\",\"artifact_type\":\"asset\",\"download_path\":\"/v1/sessions/sesn_000001/artifacts/art_000001/download\"}],\"success\":true}}}\n\n"

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
	if !strings.Contains(text, "artifacts:") || !strings.Contains(text, "art_000001 read_file.json [asset] download: /v1/sessions/sesn_000001/artifacts/art_000001/download") {
		t.Fatalf("expected artifact summary, got %q", text)
	}
	if !strings.Contains(text, "cli: bin/tma session artifact download --session sesn_000001 --artifact art_000001") {
		t.Fatalf("expected artifact download command hint, got %q", text)
	}
	if strings.Contains(text, "event: runtime.tool_result") {
		t.Fatalf("expected formatted output instead of raw SSE chunk, got %q", text)
	}
}

func TestStreamSSEFormatsToolResultArtifactError(t *testing.T) {
	input := "id: evt_6\n" +
		"event: runtime.tool_result\n" +
		"data: {\"seq\":11,\"type\":\"runtime.tool_result\",\"payload\":{\"turn_id\":\"turn_123\",\"data\":{\"id\":\"call_read\",\"identifier\":\"default\",\"api_name\":\"read_file\",\"artifact_error\":\"object store client not configured\",\"success\":true}}}\n\n"

	var output bytes.Buffer
	if err := streamSSE(strings.NewReader(input), &output); err != nil {
		t.Fatalf("stream sse: %v", err)
	}

	text := output.String()
	if !strings.Contains(text, "artifact error: object store client not configured") {
		t.Fatalf("expected artifact error summary, got %q", text)
	}
}

func TestPromptForToolInterventionApprove(t *testing.T) {
	input := bufio.NewScanner(strings.NewReader("a\n"))
	var output bytes.Buffer
	var action string
	var reason string

	err := promptForToolIntervention(input, &output, toolInterventionEvent{CallID: "call_edit"}, func(gotAction string, gotReason string) error {
		action = gotAction
		reason = gotReason
		return nil
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if action != "approve" || reason != "" {
		t.Fatalf("unexpected decision action=%q reason=%q", action, reason)
	}
	if !strings.Contains(output.String(), "approval submitted: approve") {
		t.Fatalf("expected submitted output, got %q", output.String())
	}
}

func TestPromptForToolInterventionRejectWithReason(t *testing.T) {
	input := bufio.NewScanner(strings.NewReader("r\nnot safe\n"))
	var output bytes.Buffer
	var action string
	var reason string

	err := promptForToolIntervention(input, &output, toolInterventionEvent{CallID: "call_edit"}, func(gotAction string, gotReason string) error {
		action = gotAction
		reason = gotReason
		return nil
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if action != "reject" || reason != "not safe" {
		t.Fatalf("unexpected decision action=%q reason=%q", action, reason)
	}
}

func TestPromptForToolInterventionSkip(t *testing.T) {
	input := bufio.NewScanner(strings.NewReader("s\n"))
	var output bytes.Buffer
	called := false

	err := promptForToolIntervention(input, &output, toolInterventionEvent{CallID: "call_edit"}, func(string, string) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}

	if called {
		t.Fatal("expected skip not to call decider")
	}
	if !strings.Contains(output.String(), "approval skipped") {
		t.Fatalf("expected skipped output, got %q", output.String())
	}
}
