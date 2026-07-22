package agenteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modelruntime"
	"tiggy-manage-agent/internal/modeltest"
	"tiggy-manage-agent/internal/toolruntime"
	"tiggy-manage-agent/internal/tools"
)

type agentCoreEvaluationRequest struct {
	SessionID            string
	Prompt               string
	Client               llm.Client
	Registry             tools.Registry
	Executor             tools.Executor
	Execution            tools.ExecutionContext
	CompletionGate       agentruntime.CompletionGate
	MaxCompletionRetries int
	MaxRounds            int
}

func runAgentCoreEvaluation(ctx context.Context, request agentCoreEvaluationRequest) (agentcore.Outcome, []agentruntime.Step, error) {
	maxRounds := request.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 32
	}
	budget := agentcore.Budget{
		MaxRounds: maxRounds, MaxModelCalls: maxRounds + 8, MaxToolCalls: maxRounds * 8,
		MaxInputTokens: 1_000_000_000, MaxOutputTokens: 1_000_000_000,
		MaxReasoningTokens: 1_000_000_000, MaxCostMicros: 1_000_000_000_000,
		Deadline: time.Now().UTC().Add(time.Minute),
	}
	state := agentcore.NewState(request.SessionID, "turn_1", budget)
	state.Messages = []coremodel.Message{{
		ID: "message_user", Role: coremodel.RoleUser, Visibility: coremodel.VisibilityPublic,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: request.Prompt}},
	}}
	snapshot, err := toolruntime.NewSnapshot(request.Registry, tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess})
	if err != nil {
		return agentcore.Outcome{}, nil, err
	}
	definitions := snapshot.Definitions()
	for _, definition := range definitions {
		state.ActiveTools = append(state.ActiveTools, definition.Name)
	}
	state.NormalizeActiveTools()
	route := coremodel.Route{
		ProviderInstanceID: "eval", ProviderConfigVersion: 1,
		ModelID: "eval", CatalogRevision: "eval:1",
	}
	durability := modeltest.NewMemoryDurability(state)
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelruntime.LLMModel{Client: request.Client},
		Context: modelruntime.FixedContext{
			Purpose: coremodel.PurposeAgent, Route: route, Tools: definitions, MaxOutputTokens: 4096,
		},
		Tools: toolruntime.ToolRuntime{
			Snapshot: snapshot, Executor: request.Executor, ExecutionContext: request.Execution,
		},
		Completion: modelruntime.CompletionGate{Gate: request.CompletionGate, MaxRetries: request.MaxCompletionRetries},
		Durability: durability,
		Clock:      modeltest.FixedClock{Time: time.Now().UTC()},
		IDs:        modeltest.NewSequenceIDs(),
	})
	if err != nil {
		return agentcore.Outcome{}, nil, err
	}
	outcome, runErr := engine.Run(ctx, state)
	steps := evaluationStepsFromAgentCore(durability.Events())
	if runErr != nil {
		return outcome, steps, runErr
	}
	switch outcome.Status {
	case agentcore.OutcomeCompleted:
		return outcome, steps, nil
	case agentcore.OutcomeFailed:
		if outcome.Failure != nil {
			return outcome, steps, errors.New(outcome.Failure.Message)
		}
		return outcome, steps, errors.New("agent core evaluation failed")
	case agentcore.OutcomeCanceled:
		return outcome, steps, context.Canceled
	case agentcore.OutcomePaused:
		return outcome, steps, errors.New("agent core evaluation unexpectedly paused")
	default:
		return outcome, steps, fmt.Errorf("unsupported agent core evaluation outcome %q", outcome.Status)
	}
}

func evaluationStepsFromAgentCore(events []agentcore.RuntimeEvent) []agentruntime.Step {
	steps := make([]agentruntime.Step, 0, len(events))
	for _, event := range events {
		switch event.Type {
		case agentcore.EventCompletionStarted:
			steps = append(steps, agentruntime.Step{Type: managedagents.EventRuntimeTurnCompleting})
		case agentcore.EventCompletionRetried:
			steps = append(steps, completionEvaluationStep(managedagents.EventRuntimeCompletionBlocked, event.Payload))
		case agentcore.EventCompletionValidated:
			steps = append(steps, completionEvaluationStep(managedagents.EventRuntimeCompletionValidated, event.Payload))
		case agentcore.EventToolCallStarted:
			entry, ok := event.Payload.(agentcore.ToolCallJournalEntry)
			if !ok {
				continue
			}
			call := tools.NormalizeCall(tools.Call{Name: entry.Name})
			steps = append(steps, agentruntime.Step{Type: managedagents.EventRuntimeToolCall, Data: map[string]any{
				"id": entry.CallID, "identifier": call.Identifier, "api_name": call.APIName,
			}})
		case agentcore.EventToolCallResult:
			entry, ok := event.Payload.(agentcore.ToolCallJournalEntry)
			if !ok || entry.Result == nil {
				continue
			}
			data := map[string]any{}
			var state map[string]any
			if len(entry.Result.State) > 0 && json.Unmarshal(entry.Result.State, &state) == nil {
				data["state"] = state
				if errorType, _ := state["error_type"].(string); strings.TrimSpace(errorType) != "" {
					data["error"] = &tools.ExecutionError{Type: errorType, Message: errorType}
				}
			}
			steps = append(steps, agentruntime.Step{Type: managedagents.EventRuntimeToolResult, Data: data})
		}
	}
	return steps
}

func completionEvaluationStep(eventType string, payload any) agentruntime.Step {
	data := map[string]any{}
	if verdict, ok := payload.(agentcore.CompletionVerdict); ok {
		data["validator"] = verdict.ValidatorID
	}
	return agentruntime.Step{Type: eventType, Data: data}
}
