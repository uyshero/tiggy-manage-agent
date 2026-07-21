package agentcoreadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentcoreadapter"
	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

func TestLLMModelConvertsStreamingRequestAndResponse(t *testing.T) {
	t.Parallel()

	legacy := &recordingStreamingClient{response: llm.Response{
		Message: llm.Message{
			Role:    "assistant",
			Content: []llm.ContentPart{{Type: "text", Text: "checking"}},
			ToolCalls: []llm.ToolCall{{ID: "call_1", Type: "function", Function: llm.ToolCallFunction{
				Name: "default.read_file", Arguments: json.RawMessage(`{"path":"README.md"}`),
			}}},
		},
		Usage: llm.Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14},
	}, deltas: []llm.Delta{
		{Index: 0, Kind: llm.DeltaKindText, Text: "checking"},
		{Index: 1, Kind: llm.DeltaKindStop, FinishReason: "tool_calls"},
	}}
	adapter := agentcoreadapter.LLMModel{
		Client: legacy,
		RouteResolver: agentcoreadapter.RouteResolverFunc(func(_ context.Context, route coremodel.Route) (agentcoreadapter.ResolvedRoute, error) {
			if route.CredentialRef != "credential_1" {
				t.Fatalf("credential ref = %q", route.CredentialRef)
			}
			return agentcoreadapter.ResolvedRoute{Provider: route.ProviderInstanceID, ProviderType: llm.ProviderTypeOpenAI, Model: route.ModelID, BaseURL: "https://llm.example/v1", APIKey: "secret"}, nil
		}),
	}
	request := validModelRequest()
	var deltas []coremodel.Delta
	response, err := adapter.Generate(context.Background(), request, func(delta coremodel.Delta) error {
		deltas = append(deltas, delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.StopReason != coremodel.StopReasonToolCall || response.Usage.Source != coremodel.UsageSourceProvider || response.Usage.TotalTokens != 14 {
		t.Fatalf("response = %+v", response)
	}
	if len(response.Message.Content) != 2 || response.Message.Content[1].ToolCall == nil || response.Message.Content[1].ToolCall.Name != "default.read_file" {
		t.Fatalf("response content = %+v", response.Message.Content)
	}
	if len(deltas) != 2 || deltas[1].StopReason != coremodel.StopReasonToolCall {
		t.Fatalf("stream deltas = %+v", deltas)
	}
	if legacy.request.APIKey != "secret" || legacy.request.BaseURL != "https://llm.example/v1" || legacy.request.Messages[0].Content[0].Text != "hello" {
		t.Fatalf("legacy request = %+v", legacy.request)
	}
	if got := string(request.Route.Parameters); got != `{"temperature":0}` {
		t.Fatalf("core request was mutated: route parameters = %s", got)
	}
}

func TestLLMModelNormalizesProviderError(t *testing.T) {
	t.Parallel()

	adapter := agentcoreadapter.LLMModel{Client: errorClient{err: &llm.ProviderError{
		Class: llm.ErrorClassRateLimit, StatusCode: 429, Retryable: true, Attempts: 2, Message: "slow down",
	}}}
	_, err := adapter.Generate(context.Background(), validModelRequest(), nil)
	var providerError *coremodel.ProviderError
	if !errors.As(err, &providerError) {
		t.Fatalf("Generate() error = %T %v", err, err)
	}
	if providerError.Class != coremodel.ErrorRateLimit || providerError.Code != "http_429" || !providerError.Retryable || providerError.Attempt != 2 {
		t.Fatalf("provider error = %+v", providerError)
	}
}

func TestToolRuntimeRequiresApprovalBeforeExecution(t *testing.T) {
	t.Parallel()

	runtime := &dangerousRuntime{}
	registry := tools.NewRegistry(runtime)
	adapter := agentcoreadapter.ToolRuntime{
		Registry: registry,
		Policy:   tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval},
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	call := coremodel.ToolCall{ID: "call_1", Name: "danger.write", Arguments: json.RawMessage(`{"value":"ok"}`)}
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Interactions) != 1 || plan.Interactions[0].CallID != call.ID || runtime.calls != 0 {
		t.Fatalf("preflight plan = %+v calls = %d", plan, runtime.calls)
	}
	if _, err := adapter.Execute(context.Background(), state, plan); err == nil {
		t.Fatal("Execute() without approval error = nil")
	}
	if runtime.calls != 0 {
		t.Fatalf("dangerous runtime called before approval: %d", runtime.calls)
	}
	plan.Calls[0].ApprovalStatus = "approved"
	result, err := adapter.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runtime.calls != 1 || len(result.Results) != 1 || result.Results[0].IsError {
		t.Fatalf("execution calls = %d result = %+v", runtime.calls, result)
	}
}

func TestToolRuntimeRejectsArgumentsDuringPreflight(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(&dangerousRuntime{})
	adapter := agentcoreadapter.ToolRuntime{Registry: registry, Policy: tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess}}
	_, err := adapter.Preflight(context.Background(), agentcore.State{SessionID: "session_1", TurnID: "turn_1"}, []coremodel.ToolCall{{
		ID: "call_1", Name: "danger.write", Arguments: json.RawMessage(`{"unexpected":true}`),
	}})
	if err == nil {
		t.Fatal("Preflight() invalid arguments error = nil")
	}
}

func TestToolRuntimeConvertsBusinessFailureAndPreservesPartialResults(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(batchRuntime{})
	adapter := agentcoreadapter.ToolRuntime{
		Registry: registry,
		Executor: partialFailureExecutor{},
		Policy:   tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess},
		ExecutionContext: tools.ExecutionContext{Environment: map[string]string{
			"SECRET_TOKEN": "do-not-leak",
		}},
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	calls := []coremodel.ToolCall{
		{ID: "call_fail", Name: "batch.first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_ok", Name: "batch.second", Arguments: json.RawMessage(`{}`)},
	}
	plan, err := adapter.Preflight(context.Background(), state, calls)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	result, err := adapter.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Results) != 2 || !result.Results[0].IsError || result.Results[1].IsError {
		t.Fatalf("results = %+v", result.Results)
	}
	if got := result.Results[0].Content[0].Text; !strings.Contains(got, "tool_execution_failed") || strings.Contains(got, "do-not-leak") {
		t.Fatalf("failed result content = %s", got)
	}
	if got := result.Results[1].Content[0].Text; !strings.Contains(got, `"success":true`) {
		t.Fatalf("successful result content = %s", got)
	}
}

func TestToolRuntimePassesStableIdempotencyKeyToExecutor(t *testing.T) {
	t.Parallel()

	captured := make(chan string, 1)
	adapter := agentcoreadapter.ToolRuntime{
		Registry: tools.NewRegistry(batchRuntime{}), Executor: idempotencyCaptureExecutor{captured: captured},
		Policy: tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess},
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	call := coremodel.ToolCall{ID: "call_1", Name: "batch.first", Arguments: json.RawMessage(`{}`)}
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Calls) != 1 || !strings.HasPrefix(plan.Calls[0].IdempotencyKey, "tma_tool_") {
		t.Fatalf("planned idempotency metadata = %+v", plan.Calls)
	}
	if _, err := adapter.Execute(context.Background(), state, plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := <-captured; got != plan.Calls[0].IdempotencyKey {
		t.Fatalf("executor idempotency key = %q, want %q", got, plan.Calls[0].IdempotencyKey)
	}
}

func TestToolRuntimeHonorsManifestExecutionMetadata(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(manifestMetadataRuntime{})
	definitions := agentcoreadapter.ToolDefinitions(registry)
	if len(definitions) != 1 || definitions[0].Idempotency != "keyed" || definitions[0].ConcurrencyClass != "parallel" || definitions[0].LockKeyTemplate != "customer-record" {
		t.Fatalf("tool definitions = %+v", definitions)
	}
	adapter := agentcoreadapter.ToolRuntime{Registry: registry, Policy: tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess}}
	plan, err := adapter.Preflight(context.Background(), agentcore.State{SessionID: "session_1", TurnID: "turn_1"}, []coremodel.ToolCall{{
		ID: "call_1", Name: "metadata.write", Arguments: json.RawMessage(`{}`),
	}})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Calls) != 1 || plan.Calls[0].Idempotency != "keyed" || plan.Calls[0].ExecutionMode != "parallel" || plan.Calls[0].LockKey != "session_1:customer-record" {
		t.Fatalf("planned call = %+v", plan.Calls)
	}
}

func TestFixedContextAndCompletionGateAdapters(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(&dangerousRuntime{})
	fixed := agentcoreadapter.FixedContext{
		Purpose: coremodel.PurposeAgent,
		Route:   coremodel.Route{ProviderInstanceID: "provider_1", ProviderConfigVersion: 1, ModelID: "model_1", CatalogRevision: "catalog_1"},
		Tools:   agentcoreadapter.ToolDefinitions(registry), MaxOutputTokens: 128,
	}
	state := agentcore.State{Messages: []coremodel.Message{{ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "hello"}}}}}
	request, err := fixed.Build(context.Background(), state)
	if err != nil || len(request.Tools) != 1 || request.Tools[0].Name != "danger.write" {
		t.Fatalf("Build() request = %+v err = %v", request, err)
	}
	request.Tools[0].InputSchema[0] = 'X'
	if reflect.DeepEqual(request.Tools[0].InputSchema, fixed.Tools[0].InputSchema) {
		t.Fatal("FixedContext returned aliased tool schema")
	}

	candidateMessage := coremodel.Message{ID: "answer_1", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "answer"}}}
	completion := agentcoreadapter.CompletionGate{Gate: stubCompletionGate{}}
	verdict, err := completion.Validate(context.Background(), agentcore.CompletionCandidate{
		Message: candidateMessage,
		Attempt: 2,
		State:   agentcore.State{SessionID: "session_1", TurnID: "turn_1", Round: 3, Messages: append(state.Messages, candidateMessage)},
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if verdict.Outcome != agentcore.CompletionRetry || verdict.ValidatorID != "task-plan" || verdict.Feedback != "finish the plan" || !reflect.DeepEqual(verdict.EvidenceRefs, []string{"plan_id"}) {
		t.Fatalf("completion verdict = %+v", verdict)
	}
}

func TestLLMCompactorBuildsDedicatedRequest(t *testing.T) {
	t.Parallel()

	modelPort := &recordingCompactionModel{response: coremodel.Response{
		Message:    coremodel.Message{ID: "summary", Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "Objective: continue the work.\nNext steps: run tests."}}},
		StopReason: coremodel.StopReasonComplete,
		Usage:      coremodel.Usage{InputTokens: 12, OutputTokens: 6, TotalTokens: 18, Source: coremodel.UsageSourceProvider},
	}}
	compactor := agentcoreadapter.LLMCompactor{
		Model:           modelPort,
		Route:           coremodel.Route{ProviderInstanceID: "provider_1", ProviderConfigVersion: 1, ModelID: "model_1", CatalogRevision: "catalog_1"},
		ThresholdTokens: 10, MaxOutputTokens: 256, SummaryMaxChars: 200,
	}
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{{
			ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic,
			Content: []coremodel.Content{{Type: coremodel.ContentText, Text: strings.Repeat("large context ", 20)}},
		}},
	}
	if !compactor.NeedsCompaction(state) {
		t.Fatal("NeedsCompaction() = false")
	}
	result, err := compactor.Compact(context.Background(), state, "attempt_1")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if result.Summary == "" || result.Usage.TotalTokens != 18 || result.EstimatedInputTokens <= 0 {
		t.Fatalf("compaction result = %+v", result)
	}
	request := modelPort.request
	if request.Purpose != coremodel.PurposeCompaction || len(request.Tools) != 0 || request.AttemptID != "attempt_1" || request.MaxOutputTokens != 256 || len(request.Messages) != 2 {
		t.Fatalf("compaction request = %+v", request)
	}
}

func TestLLMCompactorDoesNotRepeatForIrreducibleLargeUserMessage(t *testing.T) {
	t.Parallel()

	compactor := agentcoreadapter.LLMCompactor{Model: &recordingCompactionModel{}, ThresholdTokens: 10}
	state := agentcore.State{
		Context: agentcore.ContextState{CompactionCount: 1, EstimatedInputTokens: 1000},
		Messages: []coremodel.Message{
			{ID: "summary", Role: coremodel.RoleSystem, Visibility: coremodel.VisibilityInternal, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "short summary"}}},
			{ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: strings.Repeat("large request ", 20)}}},
		},
	}
	if compactor.NeedsCompaction(state) {
		t.Fatal("NeedsCompaction() repeated without enough new context")
	}
	state.Messages = append(state.Messages, coremodel.Message{
		ID: "new_context", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: strings.Repeat("new tool context ", 1000)}},
	})
	if !compactor.NeedsCompaction(state) {
		t.Fatal("NeedsCompaction() did not trigger after substantial new context")
	}
}

func TestSessionControlsMapsSteerFollowUpAndCancel(t *testing.T) {
	t.Parallel()

	reader := staticControlReader{events: []managedagents.Event{
		{ID: "evt_1", SessionID: "session_1", TurnID: "turn_1", Seq: 11, Type: managedagents.EventUserSteer, Payload: json.RawMessage(`{"content":[{"type":"text","text":"focus on correctness"}]}`)},
		{ID: "evt_2", SessionID: "session_1", TurnID: "turn_1", Seq: 12, Type: managedagents.EventUserFollowUp, Payload: json.RawMessage(`{"text":"also provide tests"}`)},
		{ID: "evt_3", SessionID: "session_1", TurnID: "turn_1", Seq: 13, Type: managedagents.EventUserInterrupt, Payload: json.RawMessage(`{}`)},
	}}
	controls := agentcoreadapter.SessionControls{Reader: reader}
	commands, err := controls.Drain(context.Background(), agentcore.State{SessionID: "session_1", TurnID: "turn_1", ControlCursor: 10}, agentcore.ControlBeforeModel)
	if err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if len(commands) != 3 || commands[0].Mode != agentcore.ControlSteer || commands[1].Mode != agentcore.ControlFollowUp || commands[2].Mode != agentcore.ControlCancel {
		t.Fatalf("control commands = %+v", commands)
	}
	if commands[0].Message == nil || commands[0].Message.Content[0].Text != "focus on correctness" || commands[1].Message == nil || commands[1].Message.Content[0].Text != "also provide tests" {
		t.Fatalf("control messages = %+v", commands)
	}
}

func validModelRequest() coremodel.Request {
	return coremodel.Request{
		Purpose: coremodel.PurposeAgent,
		Route: coremodel.Route{
			ProviderInstanceID: "provider_1", ProviderConfigVersion: 2, ModelID: "model_1", CatalogRevision: "catalog_1", CredentialRef: "credential_1", Parameters: json.RawMessage(`{"temperature":0}`),
		},
		Messages:        []coremodel.Message{{ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "hello"}}}},
		Tools:           []coremodel.ToolDefinition{{Name: "default.read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		MaxOutputTokens: 128, SessionID: "session_1", TurnID: "turn_1", AttemptID: "attempt_1",
	}
}

type recordingStreamingClient struct {
	request  llm.Request
	response llm.Response
	deltas   []llm.Delta
}

func (c *recordingStreamingClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, errors.New("non-streaming path used")
}

func (c *recordingStreamingClient) GenerateStream(_ context.Context, request llm.Request, sink func(llm.Delta) error) (llm.Response, error) {
	c.request = request
	for _, delta := range c.deltas {
		if err := sink(delta); err != nil {
			return llm.Response{}, err
		}
	}
	return c.response, nil
}

type errorClient struct{ err error }

func (c errorClient) Generate(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{}, c.err
}

type recordingCompactionModel struct {
	request  coremodel.Request
	response coremodel.Response
}

type staticControlReader struct {
	events []managedagents.Event
}

func (r staticControlReader) ListSessionTurnControlEventsContext(_ context.Context, sessionID, turnID string, afterSeq int64) ([]managedagents.Event, error) {
	if sessionID != "session_1" || turnID != "turn_1" || afterSeq != 10 {
		return nil, fmt.Errorf("control read scope = %s/%s after %d", sessionID, turnID, afterSeq)
	}
	return append([]managedagents.Event(nil), r.events...), nil
}

func (m *recordingCompactionModel) Generate(_ context.Context, request coremodel.Request, _ agentcore.DeltaSink) (coremodel.Response, error) {
	m.request = request
	return m.response, nil
}

type dangerousRuntime struct{ calls int }

type batchRuntime struct{}

type manifestMetadataRuntime struct{}

func (manifestMetadataRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "metadata", Meta: tools.Meta{Title: "Metadata", Description: "Metadata test tool"},
		API: []tools.API{{
			Name: "write", Description: "Write with a backend idempotency key", Risk: "write",
			Idempotency: "keyed", ConcurrencyClass: "parallel", LockKey: "customer-record",
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		}},
	}
}

func (manifestMetadataRuntime) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "ok"}, nil
}

func (batchRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "batch",
		Meta:       tools.Meta{Title: "Batch", Description: "Batch test tools"},
		API: []tools.API{
			{Name: "first", Description: "First call", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
			{Name: "second", Description: "Second call", Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`)},
		},
	}
}

func (batchRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{}, errors.New("batch runtime should be replaced by the test executor")
}

type partialFailureExecutor struct{}

func (partialFailureExecutor) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	if call.APIName == "first" {
		return tools.ExecutionResult{}, errors.New("business failure contains do-not-leak")
	}
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "ok"}, nil
}

type idempotencyCaptureExecutor struct{ captured chan<- string }

func (e idempotencyCaptureExecutor) Execute(_ context.Context, call tools.Call, executionContext tools.ExecutionContext) (tools.ExecutionResult, error) {
	e.captured <- executionContext.IdempotencyKey
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "ok"}, nil
}

func (r *dangerousRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "danger",
		Meta:       tools.Meta{Title: "Danger", Description: "Dangerous test tool"},
		API: []tools.API{{
			Name: "write", Description: "Writes a value", HumanIntervention: "write", Risk: "write",
			Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		}},
	}
}

func (r *dangerousRuntime) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	r.calls++
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "written"}, nil
}

type stubCompletionGate struct{}

func (stubCompletionGate) Validate(_ context.Context, candidate agentruntime.CompletionCandidate) (agentruntime.CompletionVerdict, error) {
	if candidate.SessionID != "session_1" || candidate.TurnID != "turn_1" || candidate.Attempt != 2 || candidate.ToolRound != 3 {
		return agentruntime.CompletionVerdict{}, errors.New("completion candidate metadata mismatch")
	}
	return agentruntime.CompletionVerdict{
		Outcome: agentruntime.CompletionOutcomeRetry, Validator: "task-plan", Feedback: "finish the plan", Evidence: map[string]any{"plan_id": "plan_1"},
	}, nil
}
