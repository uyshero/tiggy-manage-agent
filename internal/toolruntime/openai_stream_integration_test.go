package toolruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/llm"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modelruntime"
	"tiggy-manage-agent/internal/modeltest"
	"tiggy-manage-agent/internal/toolruntime"
	"tiggy-manage-agent/internal/tools"
)

func TestOpenAICompatibleStreamAgentCoreReturnsRecoverableToolErrors(t *testing.T) {
	for _, test := range []struct {
		name          string
		callID        string
		toolName      string
		arguments     string
		finishReason  string
		wantErrorType string
	}{
		{name: "unknown tool", callID: "call_unknown", toolName: "missing_inspect", arguments: `{}`, finishReason: "tool_calls", wantErrorType: "unsupported_tool"},
		{name: "malformed JSON", callID: "call_malformed", toolName: "read_inspect", arguments: `{"path":`, finishReason: "tool_calls", wantErrorType: "invalid_tool_arguments"},
		{name: "schema mismatch", callID: "call_schema", toolName: "read_inspect", arguments: `{"unexpected":true}`, finishReason: "tool_calls", wantErrorType: "invalid_tool_arguments"},
		{name: "truncated response", callID: "call_truncated", toolName: "read_inspect", arguments: `{}`, finishReason: "length", wantErrorType: "invalid_tool_arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			harness := &openAIStreamHarness{fixture: openAIStreamFixture{
				callID: test.callID, toolName: test.toolName, arguments: test.arguments, finishReason: test.finishReason,
			}}
			server := httptest.NewServer(http.HandlerFunc(harness.serveHTTP))
			defer server.Close()

			executor := &countingExecutor{}
			outcome := runOpenAIStreamAgentCore(t, tools.NewRegistry(readRuntime{}), executor, server.URL)
			if outcome.Status != agentcore.OutcomeCompleted || outcome.FinalMessage == nil || outcome.FinalMessage.Content[0].Text != "recovered" {
				t.Fatalf("outcome = %+v", outcome)
			}
			if executor.calls != 0 {
				t.Fatalf("invalid tool call reached executor %d times", executor.calls)
			}
			if len(outcome.State.ToolJournal) != 1 || outcome.State.ToolJournal[0].Status != agentcore.ToolCallFailed {
				t.Fatalf("tool journal = %+v", outcome.State.ToolJournal)
			}

			records, handlerErr := harness.snapshot()
			if handlerErr != nil {
				t.Fatal(handlerErr)
			}
			if len(records) != 2 {
				t.Fatalf("provider requests = %d, want 2", len(records))
			}
			if records[0].path != "/v1/chat/completions" || records[0].authorization != "Bearer test-key" || !strings.Contains(string(records[0].body), `"stream":true`) {
				t.Fatalf("first provider request = %+v body=%s", records[0], records[0].body)
			}
			second := string(records[1].body)
			if !strings.Contains(second, test.callID) || !strings.Contains(second, "tma.tool_result.v1") || !strings.Contains(second, test.wantErrorType) {
				t.Fatalf("second provider request does not contain the recoverable Tool Result: %s", second)
			}
		})
	}
}

func TestOpenAICompatibleStreamExecutesCanonicalUnderscoreToolName(t *testing.T) {
	t.Parallel()

	harness := &openAIStreamHarness{fixture: openAIStreamFixture{
		callID: "call_read", toolName: "read_inspect", arguments: `{}`, finishReason: "tool_calls",
	}}
	server := httptest.NewServer(http.HandlerFunc(harness.serveHTTP))
	defer server.Close()

	executor := &countingExecutor{}
	outcome := runOpenAIStreamAgentCore(t, tools.NewRegistry(readRuntime{}), executor, server.URL)
	if outcome.Status != agentcore.OutcomeCompleted || executor.calls != 1 {
		t.Fatalf("outcome=%+v executor calls=%d", outcome, executor.calls)
	}
	records, err := harness.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("provider requests=%d", len(records))
	}
	if !strings.Contains(string(records[0].body), `"name":"read_inspect"`) || strings.Contains(string(records[0].body), "read.inspect") {
		t.Fatalf("first provider body=%s", records[0].body)
	}
}

func TestOpenAICompatibleStreamCanonicalizesDottedToolAliasBeforeReplay(t *testing.T) {
	t.Parallel()

	harness := &openAIStreamHarness{fixture: openAIStreamFixture{
		callID: "call_read", toolName: "read_inspect", arguments: `{}`, finishReason: "tool_calls",
	}}
	server := httptest.NewServer(http.HandlerFunc(harness.serveHTTP))
	defer server.Close()

	executor := &countingExecutor{}
	outcome := runOpenAIStreamAgentCore(t, tools.NewRegistry(readRuntime{}), executor, server.URL)
	if outcome.Status != agentcore.OutcomeCompleted || executor.calls != 1 {
		t.Fatalf("outcome=%+v executor calls=%d", outcome, executor.calls)
	}
	if len(outcome.State.ToolJournal) != 1 || outcome.State.ToolJournal[0].Name != "read_inspect" {
		t.Fatalf("tool journal=%+v", outcome.State.ToolJournal)
	}
	records, err := harness.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("provider requests=%d", len(records))
	}
	second := string(records[1].body)
	if !strings.Contains(second, `"name":"read_inspect"`) || strings.Contains(second, "read.inspect") {
		t.Fatalf("second provider body=%s", second)
	}
}

func TestOpenAICompatibleStreamAgentCoreFailsForInvalidRegisteredSchema(t *testing.T) {
	harness := &openAIStreamHarness{fixture: openAIStreamFixture{
		callID: "call_broken", toolName: "broken_inspect", arguments: `{}`, finishReason: "tool_calls",
	}}
	server := httptest.NewServer(http.HandlerFunc(harness.serveHTTP))
	defer server.Close()

	_, err := toolruntime.NewSnapshot(
		tools.NewRegistry(externalSchemaRuntime{}),
		tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid_tool_schema") {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	var contractError *tools.ToolContractError
	if !errors.As(err, &contractError) || contractError.ErrorCode() != "invalid_tool_schema" {
		t.Fatalf("NewSnapshot() error type = %T %v", err, err)
	}
	records, handlerErr := harness.snapshot()
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if len(records) != 0 {
		t.Fatalf("provider requests = %d, want 0", len(records))
	}
}

func runOpenAIStreamAgentCore(t *testing.T, registry tools.Registry, executor tools.Executor, serverURL string) agentcore.Outcome {
	t.Helper()
	now := time.Now().UTC()
	state := agentcore.NewState("session_openai_stream", "turn_openai_stream", agentcore.Budget{
		MaxRounds: 4, MaxModelCalls: 4, MaxToolCalls: 4,
		MaxInputTokens: 10_000, MaxOutputTokens: 10_000, MaxReasoningTokens: 10_000, MaxCostMicros: 1_000_000,
		Deadline: now.Add(time.Minute),
	})
	state.Messages = []coremodel.Message{{
		ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "inspect the workspace"}},
	}}
	snapshot := fullAccessSnapshot(t, registry)
	definitions := snapshot.Definitions()
	for _, definition := range definitions {
		state.ActiveTools = append(state.ActiveTools, definition.Name)
	}
	state.NormalizeActiveTools()
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelruntime.LLMModel{Client: llm.OpenAICompatibleClient{
			BaseURL: serverURL + "/v1", APIKey: "test-key", Client: http.DefaultClient, MaxAttempts: 1,
		}},
		Context: modeltest.StaticContext{
			Route: coremodel.Route{
				ProviderInstanceID: "openai-test", ProviderConfigVersion: 1,
				ModelID: "test-model", CatalogRevision: "test:1",
			},
			Tools: definitions, MaxOutputTokens: 256,
		},
		Tools: toolruntime.ToolRuntime{
			Snapshot: snapshot, Executor: executor,
		},
		Durability: durability,
		Clock:      modeltest.FixedClock{Time: now},
		IDs:        modeltest.NewSequenceIDs(),
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := engine.Run(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

type openAIStreamFixture struct {
	callID       string
	toolName     string
	arguments    string
	finishReason string
}

type openAIStreamRequest struct {
	path          string
	authorization string
	body          []byte
}

type openAIStreamHarness struct {
	mu       sync.Mutex
	fixture  openAIStreamFixture
	requests []openAIStreamRequest
	err      error
}

func (h *openAIStreamHarness) serveHTTP(response http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		h.recordError(err)
		http.Error(response, "read request", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	h.requests = append(h.requests, openAIStreamRequest{
		path: request.URL.Path, authorization: request.Header.Get("Authorization"), body: append([]byte(nil), body...),
	})
	requestNumber := len(h.requests)
	h.mu.Unlock()

	response.Header().Set("Content-Type", "text/event-stream")
	switch requestNumber {
	case 1:
		h.writeToolCall(response)
	case 2:
		h.writeCompletion(response)
	default:
		h.recordError(fmt.Errorf("unexpected provider request %d", requestNumber))
		http.Error(response, "unexpected request", http.StatusInternalServerError)
	}
}

func (h *openAIStreamHarness) writeToolCall(response io.Writer) {
	payload := map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{
			"role": "assistant",
			"tool_calls": []any{map[string]any{
				"index": 0, "id": h.fixture.callID, "type": "function",
				"function": map[string]any{"name": h.fixture.toolName, "arguments": h.fixture.arguments},
			}},
		},
		"finish_reason": h.fixture.finishReason,
	}}}
	h.writeEvent(response, payload)
	_, _ = io.WriteString(response, "data: [DONE]\n\n")
}

func (h *openAIStreamHarness) writeCompletion(response io.Writer) {
	payload := map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"role": "assistant", "content": "recovered"}, "finish_reason": "stop",
	}}}
	h.writeEvent(response, payload)
	_, _ = io.WriteString(response, "data: [DONE]\n\n")
}

func (h *openAIStreamHarness) writeEvent(response io.Writer, payload any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		h.recordError(err)
		return
	}
	_, _ = fmt.Fprintf(response, "data: %s\n\n", encoded)
}

func (h *openAIStreamHarness) recordError(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err == nil {
		h.err = err
	}
}

func (h *openAIStreamHarness) snapshot() ([]openAIStreamRequest, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	requests := append([]openAIStreamRequest(nil), h.requests...)
	return requests, h.err
}

type externalSchemaRuntime struct{}

func (externalSchemaRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "broken", Type: "builtin", Executors: []string{tools.ExecutorServer},
		API: []tools.API{{
			Name: "inspect", Description: "Invalid external schema fixture", Risk: tools.ToolRiskRead,
			Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"$ref":"https://example.com/value.json"}}}`),
		}},
	}
}

func (externalSchemaRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{}, errors.New("invalid-schema runtime must not execute")
}
