package toolruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modelruntime"
	"tiggy-manage-agent/internal/modeltest"
	"tiggy-manage-agent/internal/toolruntime"
	"tiggy-manage-agent/internal/tools"
)

func TestToolRuntimeReturnsCodedErrorForUninitializedSnapshot(t *testing.T) {
	t.Parallel()

	_, err := (toolruntime.ToolRuntime{}).Preflight(t.Context(), agentcore.State{}, []coremodel.ToolCall{{
		ID: "call_1", Name: "read_inspect", Arguments: json.RawMessage(`{}`),
	}})
	var contractError *tools.ToolContractError
	if !errors.As(err, &contractError) || contractError.ErrorCode() != "invalid_tool_runtime_snapshot" {
		t.Fatalf("Preflight() error = %T %v", err, err)
	}
}

func TestToolRuntimeParksAndResolvesAskUser(t *testing.T) {
	runtime := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, tools.DefaultRegistry()),
	}
	state := agentcore.NewState("session_1", "turn_1", agentcore.Budget{})
	call := coremodel.ToolCall{
		ID: "call_ask", Name: "interaction_ask_user",
		Arguments: json.RawMessage(`{"question":"Choose a target","mode":"select","choices":[{"id":"a","label":"A"},{"id":"b","label":"B"}]}`),
	}
	plan, err := runtime.Preflight(context.Background(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Interactions) != 1 || plan.Interactions[0].Kind != managedagents.InterventionKindClarification {
		t.Fatalf("interactions = %+v", plan.Interactions)
	}
	plan.Interactions[0].Decision = &agentcore.InteractionDecision{
		InteractionID: plan.Interactions[0].ID,
		Status:        managedagents.InterventionStatusApproved,
		Response:      json.RawMessage(`{"choice":"a"}`),
	}
	result, err := runtime.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].IsError || string(result.Results[0].State) == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestToolRuntimeRejectsParkingInteractionBatch(t *testing.T) {
	runtime := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, tools.DefaultRegistry()),
	}
	state := agentcore.NewState("session_1", "turn_1", agentcore.Budget{})
	_, err := runtime.Preflight(context.Background(), state, []coremodel.ToolCall{
		{ID: "call_ask", Name: "interaction_ask_user", Arguments: json.RawMessage(`{"question":"Continue?","mode":"freeform"}`)},
		{ID: "call_read", Name: "default_read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)},
	})
	if err == nil {
		t.Fatal("Preflight() error = nil")
	}
}

func TestToolRuntimeReturnsInvalidArgumentsToModel(t *testing.T) {
	runtime := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, tools.DefaultRegistry()),
	}
	state := agentcore.NewState("session_1", "turn_1", agentcore.Budget{})
	for _, call := range []coremodel.ToolCall{
		{ID: "call_read", Name: "default_read_file", Arguments: json.RawMessage(`{}`)},
		{ID: "call_ask", Name: "interaction_ask_user", Arguments: json.RawMessage(`{}`)},
	} {
		t.Run(call.Name, func(t *testing.T) {
			plan, err := runtime.Preflight(t.Context(), state, []coremodel.ToolCall{call})
			if err != nil {
				t.Fatalf("Preflight() error = %v", err)
			}
			if len(plan.Calls) != 1 || plan.Calls[0].ValidationState != agentcore.ToolValidationInvalidArguments || len(plan.Interactions) != 0 {
				t.Fatalf("plan = %+v", plan)
			}
			result, err := runtime.Execute(t.Context(), state, plan)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(result.Results) != 1 || !result.Results[0].IsError || !result.Results[0].Retryable {
				t.Fatalf("result = %+v", result)
			}
			if text := result.Results[0].Content[0].Text; !strings.Contains(text, `"protocol_version":"tma.tool_result.v1"`) || !strings.Contains(text, `"type":"invalid_tool_arguments"`) {
				t.Fatalf("invalid arguments did not use the structured tool result envelope: %s", text)
			}
		})
	}
}

func TestToolRuntimeReturnsUnknownToolsToModel(t *testing.T) {
	runtime := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, tools.DefaultRegistry()),
	}
	state := agentcore.NewState("session_1", "turn_1", agentcore.Budget{})
	for _, test := range []struct {
		name            string
		tool            string
		validationState agentcore.ToolValidationState
		errorType       string
	}{
		{name: "unknown canonical name", tool: "missing_inspect", validationState: agentcore.ToolValidationUnsupportedToolAPI, errorType: "unsupported_tool_api"},
		{name: "unknown api", tool: "default_missing", validationState: agentcore.ToolValidationUnsupportedToolAPI, errorType: "unsupported_tool_api"},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := coremodel.ToolCall{ID: "call_missing", Name: test.tool, Arguments: json.RawMessage(`{}`)}
			plan, err := runtime.Preflight(t.Context(), state, []coremodel.ToolCall{call})
			if err != nil {
				t.Fatalf("Preflight() error = %v", err)
			}
			if len(plan.Calls) != 1 || plan.Calls[0].ValidationState != test.validationState {
				t.Fatalf("plan = %+v", plan)
			}
			if err := plan.Validate(); err != nil {
				t.Fatalf("plan validation failed: %v", err)
			}
			result, err := runtime.Execute(t.Context(), state, plan)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if len(result.Results) != 1 || !result.Results[0].IsError || !result.Results[0].Retryable {
				t.Fatalf("result = %+v", result)
			}
			text := result.Results[0].Content[0].Text
			if !strings.Contains(text, `"protocol_version":"tma.tool_result.v1"`) || !strings.Contains(text, `"type":"`+test.errorType+`"`) {
				t.Fatalf("unknown tool did not use the structured tool result envelope: %s", text)
			}
		})
	}
}

func TestAgentCoreContinuesAfterUnknownToolResult(t *testing.T) {
	now := time.Now().UTC()
	state := agentcore.NewState("session_unknown", "turn_unknown", agentcore.Budget{
		MaxRounds: 4, MaxModelCalls: 4, MaxToolCalls: 4,
		MaxInputTokens: 10_000, MaxOutputTokens: 10_000, MaxReasoningTokens: 10_000, MaxCostMicros: 1_000_000,
		Deadline: now.Add(time.Minute),
	})
	state.Messages = []coremodel.Message{{
		ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "inspect the workspace"}},
	}}
	registry := tools.DefaultRegistry()
	snapshot := fullAccessSnapshot(t, registry)
	definitions := snapshot.Definitions()
	for _, definition := range definitions {
		state.ActiveTools = append(state.ActiveTools, definition.Name)
	}
	state.NormalizeActiveTools()
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: coremodel.Response{
			Message: coremodel.Message{ID: "assistant_1", Content: []coremodel.Content{{
				Type:     coremodel.ContentToolCall,
				ToolCall: &coremodel.ToolCall{ID: "call_missing", Name: "missing_inspect", Arguments: json.RawMessage(`{}`)},
			}}},
			StopReason: coremodel.StopReasonToolCall,
		}},
		modeltest.ModelStep{
			Assert: func(request coremodel.Request) error {
				for _, message := range request.Messages {
					for _, content := range message.Content {
						if content.ToolResult != nil && content.ToolResult.CallID == "call_missing" && content.ToolResult.IsError && strings.Contains(content.ToolResult.Content[0].Text, `"type":"unsupported_tool_api"`) {
							return nil
						}
					}
				}
				return errors.New("unknown-tool result was not returned to the model")
			},
			Response: coremodel.Response{
				Message:    coremodel.Message{ID: "assistant_2", Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "recovered"}}},
				StopReason: coremodel.StopReasonComplete,
			},
		},
	)
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelPort,
		Context: modeltest.StaticContext{
			Route: coremodel.Route{
				ProviderInstanceID: "provider_1", ProviderConfigVersion: 1,
				ModelID: "model_1", CatalogRevision: "catalog_1",
			},
			Tools: definitions, MaxOutputTokens: 256,
		},
		Tools: toolruntime.ToolRuntime{
			Snapshot: snapshot,
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
	if outcome.Status != agentcore.OutcomeCompleted || outcome.FinalMessage == nil || outcome.FinalMessage.Content[0].Text != "recovered" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if len(outcome.State.ToolJournal) != 1 || outcome.State.ToolJournal[0].Status != agentcore.ToolCallFailed {
		t.Fatalf("tool journal = %+v", outcome.State.ToolJournal)
	}
}

func TestAgentCoreContinuesAfterMalformedToolArguments(t *testing.T) {
	now := time.Now().UTC()
	state := agentcore.NewState("session_malformed", "turn_malformed", agentcore.Budget{
		MaxRounds: 4, MaxModelCalls: 4, MaxToolCalls: 4,
		MaxInputTokens: 10_000, MaxOutputTokens: 10_000, MaxReasoningTokens: 10_000, MaxCostMicros: 1_000_000,
		Deadline: now.Add(time.Minute),
	})
	state.Messages = []coremodel.Message{{
		ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "inspect the workspace"}},
	}}
	registry := tools.NewRegistry(readRuntime{})
	snapshot := fullAccessSnapshot(t, registry)
	definitions := snapshot.Definitions()
	state.ActiveTools = []string{"read_inspect"}
	executor := &countingExecutor{}
	client := &malformedThenCompleteClient{}
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelruntime.LLMModel{Client: client},
		Context: modeltest.StaticContext{
			Route: coremodel.Route{
				ProviderInstanceID: "provider_1", ProviderConfigVersion: 1,
				ModelID: "model_1", CatalogRevision: "catalog_1",
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
	if outcome.Status != agentcore.OutcomeCompleted || client.calls != 3 || executor.calls != 0 {
		t.Fatalf("outcome = %+v model calls = %d executor calls = %d", outcome, client.calls, executor.calls)
	}
	if len(outcome.State.ToolJournal) != 2 || outcome.State.ToolJournal[0].Status != agentcore.ToolCallFailed || outcome.State.ToolJournal[1].Status != agentcore.ToolCallFailed {
		t.Fatalf("tool journal = %+v", outcome.State.ToolJournal)
	}
}

func TestAgentCoreContinuesAfterTruncatedToolCall(t *testing.T) {
	now := time.Now().UTC()
	state := agentcore.NewState("session_truncated", "turn_truncated", agentcore.Budget{
		MaxRounds: 4, MaxModelCalls: 4, MaxToolCalls: 4,
		MaxInputTokens: 10_000, MaxOutputTokens: 10_000, MaxReasoningTokens: 10_000, MaxCostMicros: 1_000_000,
		Deadline: now.Add(time.Minute),
	})
	state.Messages = []coremodel.Message{{
		ID: "user_1", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "inspect the workspace"}},
	}}
	registry := tools.NewRegistry(readRuntime{})
	snapshot := fullAccessSnapshot(t, registry)
	definitions := snapshot.Definitions()
	state.ActiveTools = []string{"read_inspect"}
	executor := &countingExecutor{}
	truncated := coremodel.Response{
		Message: coremodel.Message{ID: "assistant_1", Content: []coremodel.Content{{
			Type: coremodel.ContentToolCall,
			ToolCall: &coremodel.ToolCall{
				ID: "call_truncated", Name: "read_inspect", Arguments: json.RawMessage(`{}`),
			},
		}}},
		StopReason: coremodel.StopReasonLength,
	}
	modelPort := modeltest.NewScriptedModel(
		modeltest.ModelStep{Response: truncated},
		modeltest.ModelStep{
			Assert: func(request coremodel.Request) error {
				for _, message := range request.Messages {
					for _, content := range message.Content {
						if content.ToolResult != nil && content.ToolResult.CallID == "call_truncated" && content.ToolResult.IsError &&
							strings.Contains(content.ToolResult.Content[0].Text, `"type":"invalid_tool_arguments"`) &&
							strings.Contains(content.ToolResult.Content[0].Text, "output token limit") {
							return nil
						}
					}
				}
				return errors.New("truncated tool-call result was not returned to the model")
			},
			Response: coremodel.Response{
				Message:    coremodel.Message{ID: "assistant_2", Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "recovered"}}},
				StopReason: coremodel.StopReasonComplete,
			},
		},
	)
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelPort,
		Context: modeltest.StaticContext{
			Route: coremodel.Route{
				ProviderInstanceID: "provider_1", ProviderConfigVersion: 1,
				ModelID: "model_1", CatalogRevision: "catalog_1",
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
	if outcome.Status != agentcore.OutcomeCompleted || executor.calls != 0 {
		t.Fatalf("outcome = %+v executor calls = %d", outcome, executor.calls)
	}
}

func TestToolRuntimeTreatsInvalidRegisteredSchemaAsFatal(t *testing.T) {
	_, err := toolruntime.NewSnapshot(
		tools.NewRegistry(invalidToolSchemaRuntime{}),
		tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid_tool_schema") {
		t.Fatalf("expected fatal invalid_tool_schema, got %v", err)
	}
}

func TestToolRuntimeSnapshotIgnoresSourceManifestDrift(t *testing.T) {
	registered := &mutableSchemaRuntime{}
	registry := tools.NewRegistry(registered)
	manifestCallsBeforeSnapshot := registered.manifestCalls
	runtime := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, registry),
		Executor: tools.RegistryExecutor{Registry: registry},
	}
	state := agentcore.NewState("session_1", "turn_1", agentcore.Budget{})
	call := coremodel.ToolCall{ID: "call_mutable", Name: "mutable_inspect", Arguments: json.RawMessage(`{}`)}
	plan, err := runtime.Preflight(t.Context(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if !strings.HasPrefix(plan.RegistryRevision, "sha256:") {
		t.Fatalf("registry revision = %q", plan.RegistryRevision)
	}

	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var restored agentcore.ToolBatchPlan
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	registered.version = 2
	result, err := runtime.Execute(t.Context(), state, restored)
	if err != nil || len(result.Results) != 1 || result.Results[0].IsError {
		t.Fatalf("Execute() result = %+v error = %v", result, err)
	}
	if registered.calls != 1 {
		t.Fatalf("snapshot runtime executed %d times", registered.calls)
	}
	if registered.manifestCalls != manifestCallsBeforeSnapshot+1 {
		t.Fatalf("source Manifest() calls after snapshot = %d, want %d", registered.manifestCalls, manifestCallsBeforeSnapshot+1)
	}
}

type invalidToolSchemaRuntime struct{}

type mutableSchemaRuntime struct {
	version       int
	calls         int
	manifestCalls int
}

func (r *mutableSchemaRuntime) Manifest() tools.Manifest {
	r.manifestCalls++
	properties := `{}`
	if r.version == 2 {
		properties = `{"optional":{"type":"string"}}`
	}
	return tools.Manifest{
		Identifier: "mutable", Type: "builtin", Executors: []string{tools.ExecutorServer},
		API: []tools.API{{
			Name: "inspect", Description: "Mutable schema fixture", Risk: tools.ToolRiskRead,
			Parameters: json.RawMessage(`{"type":"object","properties":` + properties + `,"additionalProperties":false}`),
		}},
	}
}

func (r *mutableSchemaRuntime) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	r.calls++
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "executed"}, nil
}

type malformedThenCompleteClient struct{ calls int }

func (c *malformedThenCompleteClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	c.calls++
	if c.calls == 1 {
		return llm.Response{Message: llm.Message{ToolCalls: []llm.ToolCall{{
			ID: "call_malformed_1", Type: "function", Function: llm.ToolCallFunction{
				Name: "read_inspect", Arguments: json.RawMessage(`{"path":"README.md"`),
			},
		}}}}, nil
	}
	wantCallID := "call_malformed_1"
	if c.calls == 3 {
		wantCallID = "call_malformed_2"
	}
	for _, message := range request.Messages {
		if message.ToolCallID != wantCallID {
			continue
		}
		for _, content := range message.Content {
			if strings.Contains(content.Text, `"protocol_version":"tma.tool_result.v1"`) && strings.Contains(content.Text, `"type":"invalid_tool_arguments"`) {
				if c.calls == 2 {
					return llm.Response{Message: llm.Message{ToolCalls: []llm.ToolCall{{
						ID: "call_malformed_2", Type: "function", Function: llm.ToolCallFunction{
							Name: "read_inspect", Arguments: json.RawMessage(`[]`),
						},
					}}}}, nil
				}
				return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: "recovered"}}}}, nil
			}
		}
	}
	return llm.Response{}, errors.New("malformed-argument Tool Result was not returned to the model")
}

func (invalidToolSchemaRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "broken",
		API: []tools.API{{
			Name: "inspect", Description: "Invalid schema fixture",
			Parameters: json.RawMessage(`{"type":"object"`),
		}},
	}
}

func (invalidToolSchemaRuntime) Execute(context.Context, tools.Call, tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{}, errors.New("invalid-schema runtime must not execute")
}
