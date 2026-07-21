package runner

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentcoreadapter"
	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/tools"
)

const (
	agentCoreProtocolVersion = managedagents.AgentLoopMessageProtocolVersion
	agentCoreRouteStateKey   = "agent_core.model_route"
)

var errAgentCoreIneligible = errors.New("turn is not eligible for agent core")

type AgentCoreRolloutPolicy struct {
	Enabled      bool
	Percent      int
	WorkspaceIDs []string
	AgentIDs     []string
}

func (p AgentCoreRolloutPolicy) Allows(config managedagents.AgentRuntimeConfig, sessionID string) bool {
	if !p.Enabled || p.Percent <= 0 || p.Percent > 100 {
		return false
	}
	if len(p.WorkspaceIDs) > 0 && !slices.Contains(p.WorkspaceIDs, config.WorkspaceID) {
		return false
	}
	if len(p.AgentIDs) > 0 && !slices.Contains(p.AgentIDs, config.AgentID) {
		return false
	}
	if p.Percent == 100 {
		return true
	}
	return agentCoreRolloutBucket(config.WorkspaceID, config.AgentID, sessionID) < p.Percent
}

func agentCoreRolloutBucket(workspaceID, agentID, sessionID string) int {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + agentID + "\x00" + sessionID))
	return int(binary.BigEndian.Uint64(sum[:8]) % 100)
}

type agentCoreTurnError struct {
	failure agentcore.Failure
}

func (e *agentCoreTurnError) Error() string {
	if e == nil || strings.TrimSpace(e.failure.Message) == "" {
		return "agent core turn failed"
	}
	return e.failure.Message
}

func (e AgentRuntimeTurnExecutor) hasAgentCoreState(ctx context.Context, request TurnRequest) bool {
	if strings.TrimSpace(request.LeaseOwner) == "" || request.Attempt <= 0 {
		return false
	}
	factory, ok := e.Store.(managedagents.AgentLoopRepositoryFactory)
	if !ok {
		return false
	}
	repository := factory.AgentLoopRepository(managedagents.AgentLoopFence{LeaseOwner: request.LeaseOwner, Attempt: request.Attempt})
	if repository == nil {
		return false
	}
	_, err := repository.Load(ctx, request.SessionID, request.TurnID)
	return err == nil || !errors.Is(err, managedagents.ErrNotFound)
}

func (e AgentRuntimeTurnExecutor) runAgentCoreTurn(
	ctx context.Context,
	request TurnRequest,
	runtimeRequest agentruntime.TurnRequest,
	config managedagents.AgentRuntimeConfig,
	toolExecution execution.ToolExecution,
	startedAt time.Time,
) (TurnResult, error) {
	factory, ok := e.Store.(managedagents.AgentLoopRepositoryFactory)
	if !ok {
		return TurnResult{}, errors.New("agent core requires an agent loop repository factory")
	}
	if strings.TrimSpace(request.LeaseOwner) == "" || request.Attempt <= 0 {
		return TurnResult{}, errors.New("agent core requires a persistent Worker lease fence")
	}
	if e.CoreClient == nil {
		return TurnResult{}, errors.New("agent core model client is required")
	}
	repository := factory.AgentLoopRepository(managedagents.AgentLoopFence{LeaseOwner: request.LeaseOwner, Attempt: request.Attempt})
	if repository == nil {
		return TurnResult{}, errors.New("agent core repository factory returned nil")
	}

	state, err := repository.Load(ctx, request.SessionID, request.TurnID)
	if errors.Is(err, managedagents.ErrNotFound) {
		if len(runtimeRequest.ImageParts) > 0 && !managedagents.LLMModelSupportsVision(config.LLMCapabilityType) {
			return TurnResult{}, errAgentCoreIneligible
		}
		state, err = e.newAgentCoreState(ctx, request, runtimeRequest, config, toolExecution)
	}
	if err != nil {
		return TurnResult{}, fmt.Errorf("load agent core state: %w", err)
	}

	route := agentCoreRoute(config)
	definitions := agentcoreadapter.ToolDefinitions(toolExecution.Registry)
	modelPort := agentcoreadapter.LLMModel{
		Client: e.CoreClient,
		RouteResolver: agentcoreadapter.RouteResolverFunc(func(context.Context, coremodel.Route) (agentcoreadapter.ResolvedRoute, error) {
			return agentcoreadapter.ResolvedRoute{
				Provider: config.LLMProvider, ProviderType: config.LLMProviderType, Model: config.LLMModel,
				BaseURL: config.LLMBaseURL, APIKey: runtimeRequest.Config.LLMAPIKey,
			}, nil
		}),
	}
	contextPort := agentcoreadapter.FixedContext{
		Purpose: coremodel.PurposeAgent, Route: route,
		Tools:           definitions,
		MaxOutputTokens: agentCoreMaxOutputTokens(runtimeRequest.Config.ContextWindowTokens, runtimeRequest.Config.RuntimeSettings),
	}
	compactionThreshold, compactionSummaryMaxChars := agentCoreCompactionSettings(runtimeRequest.Config.ContextWindowTokens, runtimeRequest.Config.RuntimeSettings)
	compactionPort := agentcoreadapter.LLMCompactor{
		Model: modelPort, Route: route,
		ThresholdTokens: compactionThreshold,
		MaxOutputTokens: min(agentCoreMaxOutputTokens(runtimeRequest.Config.ContextWindowTokens, runtimeRequest.Config.RuntimeSettings), 4096),
		SummaryMaxChars: compactionSummaryMaxChars,
	}
	toolPort := agentcoreadapter.ToolRuntime{
		Registry:         toolExecution.Registry,
		Executor:         runtimeRequest.Config.ToolExecutor,
		Policy:           tools.InterventionPolicy{Mode: runtimeRequest.Config.InterventionMode},
		ExecutionContext: toolExecution.Context,
	}
	var controlPort agentcore.ControlPort
	if reader, ok := e.Store.(managedagents.SessionControlReader); ok {
		controlPort = agentcoreadapter.SessionControls{Reader: reader}
	}
	engine, err := agentcore.NewEngine(agentcore.Ports{
		Model: modelPort, Context: contextPort, Compaction: compactionPort, Tools: toolPort,
		Completion: agentcoreadapter.CompletionGate{Gate: e.CoreCompletionGate},
		Controls:   controlPort,
		Durability: repository,
		Live:       agentCoreLivePort{broker: e.LiveEvents, sessionID: request.SessionID, turnID: request.TurnID},
		Clock:      agentCoreClock{},
		IDs:        &agentCoreIDs{turnID: request.TurnID, attempt: request.Attempt},
	})
	if err != nil {
		return TurnResult{}, err
	}
	if bindingErr := validateAgentCoreBindings(state, route, definitions); bindingErr != nil {
		outcome, failErr := engine.Fail(ctx, state, agentcore.Failure{
			Code:    "runtime_binding_changed",
			Message: bindingErr.Error(),
		})
		if failErr != nil {
			return TurnResult{}, fmt.Errorf("terminalize invalid agent core binding: %w", failErr)
		}
		return e.agentCoreTurnResult(request, config, outcome, time.Since(startedAt))
	}
	if state.Phase == agentcore.PhasePaused {
		decisions, err := e.agentCoreResumeDecisions(ctx, request, state)
		if err != nil {
			return TurnResult{}, err
		}
		state, err = engine.Resume(ctx, state, decisions)
		if err != nil {
			return TurnResult{}, fmt.Errorf("resume agent core: %w", err)
		}
	}
	outcome, err := engine.Run(ctx, state)
	if err != nil {
		return TurnResult{}, err
	}
	return e.agentCoreTurnResult(request, config, outcome, time.Since(startedAt))
}

func (e AgentRuntimeTurnExecutor) newAgentCoreState(
	ctx context.Context,
	request TurnRequest,
	runtimeRequest agentruntime.TurnRequest,
	config managedagents.AgentRuntimeConfig,
	toolExecution execution.ToolExecution,
) (agentcore.State, error) {
	prepared, err := agentruntime.PrepareTurnContext(runtimeRequest, time.Now().UTC(), nil)
	if err != nil {
		return agentcore.State{}, err
	}
	messages, err := agentcoreadapter.MessagesFromLLM(prepared.Result.Messages)
	if err != nil {
		return agentcore.State{}, err
	}
	state := agentcore.NewState(request.SessionID, request.TurnID, agentCoreBudget(ctx, config, runtimeRequest.Config.RuntimeSettings, e.Timeout, e.CoreMaxRounds))
	state.Messages = messages
	state.Context = agentcore.ContextState{
		SummarySourceUntilSeq: config.SummarySourceUntilSeq,
		SummaryRevision:       fmt.Sprintf("summary:%d", config.SummarySourceUntilSeq),
		EstimatedInputTokens:  int64(prepared.Result.EstimatedTokenCount),
	}
	routeJSON, err := json.Marshal(agentCoreRoute(config))
	if err != nil {
		return agentcore.State{}, err
	}
	state.FeatureState = map[string]json.RawMessage{agentCoreRouteStateKey: routeJSON}
	definitions := agentcoreadapter.ToolDefinitions(toolExecution.Registry)
	for _, definition := range definitions {
		state.ActiveTools = append(state.ActiveTools, definition.Name)
	}
	state.NormalizeActiveTools()
	if err := state.Validate(); err != nil {
		return agentcore.State{}, fmt.Errorf("build initial agent core state: %w", err)
	}
	return state, nil
}

func (e AgentRuntimeTurnExecutor) agentCoreResumeDecisions(ctx context.Context, request TurnRequest, state agentcore.State) ([]agentcore.InteractionDecision, error) {
	interventions, err := managedagents.ListSessionInterventionsWithContext(ctx, e.Store, request.SessionID, "")
	if err != nil {
		return nil, fmt.Errorf("list agent core interventions: %w", err)
	}
	byCallID := make(map[string]managedagents.SessionIntervention, len(interventions))
	for _, intervention := range interventions {
		if intervention.TurnID == request.TurnID {
			byCallID[intervention.CallID] = intervention
		}
	}
	decisions := make([]agentcore.InteractionDecision, 0, len(state.Pause.Interactions))
	for _, interaction := range state.Pause.Interactions {
		intervention, ok := byCallID[interaction.CallID]
		if !ok || (intervention.Status != managedagents.InterventionStatusApproved && intervention.Status != managedagents.InterventionStatusRejected) {
			return nil, fmt.Errorf("agent core interaction %s is not resolved", interaction.ID)
		}
		decisions = append(decisions, agentcore.InteractionDecision{
			InteractionID: interaction.ID,
			Status:        intervention.Status,
			Response:      append(json.RawMessage(nil), intervention.Response...),
			Reason:        intervention.DecisionReason,
		})
	}
	return decisions, nil
}

func (e AgentRuntimeTurnExecutor) agentCoreTurnResult(request TurnRequest, config managedagents.AgentRuntimeConfig, outcome agentcore.Outcome, latency time.Duration) (TurnResult, error) {
	usage := agentCoreUsageRecord(request, config, outcome.State.Usage, latency)
	switch outcome.Status {
	case agentcore.OutcomePaused:
		for _, interaction := range outcome.Pause.Interactions {
			if interaction.Kind == managedagents.InterventionKindClarification || interaction.Kind == managedagents.InterventionKindUploadRequest {
				return TurnResult{}, ErrTurnWaitingHuman
			}
		}
		return TurnResult{}, ErrTurnWaitingApproval
	case agentcore.OutcomeCompleted:
		payload, err := agentCoreAgentPayload(outcome.FinalMessage)
		if err != nil {
			return TurnResult{DurableFinalized: true, DurableStatus: string(agentcore.OutcomeCompleted), Usage: usage}, err
		}
		return TurnResult{AgentPayload: payload, Usage: usage, DurableFinalized: true, DurableStatus: string(agentcore.OutcomeCompleted)}, nil
	case agentcore.OutcomeFailed:
		if usage != nil {
			usage.Status = "failed"
			if outcome.Failure != nil {
				usage.ErrorMessage = outcome.Failure.Message
			}
		}
		failure := agentcore.Failure{Code: "agent_core_failed", Message: "agent core turn failed"}
		if outcome.Failure != nil {
			failure = *outcome.Failure
		}
		return TurnResult{Usage: usage, DurableFinalized: true, DurableStatus: string(agentcore.OutcomeFailed)}, &agentCoreTurnError{failure: failure}
	case agentcore.OutcomeCanceled:
		return TurnResult{Usage: usage, DurableFinalized: true, DurableStatus: string(agentcore.OutcomeCanceled)}, context.Canceled
	default:
		return TurnResult{}, fmt.Errorf("unsupported agent core outcome %q", outcome.Status)
	}
}

func agentCoreAgentPayload(message *coremodel.Message) (json.RawMessage, error) {
	if message == nil {
		return nil, errors.New("agent core completed without a final message")
	}
	return json.Marshal(map[string]any{
		"protocol_version": agentCoreProtocolVersion,
		"content_format":   "blocks",
		"content":          message.Content,
		"message_id":       message.ID,
	})
}

func agentCoreUsageRecord(request TurnRequest, config managedagents.AgentRuntimeConfig, usage coremodel.Usage, latency time.Duration) *managedagents.RecordLLMUsageInput {
	if config.WorkspaceID == "" || config.AgentID == "" || config.AgentConfigVersion <= 0 || config.LLMProvider == "" || config.LLMModel == "" {
		return nil
	}
	return &managedagents.RecordLLMUsageInput{
		WorkspaceID: config.WorkspaceID, AgentID: config.AgentID, AgentConfigVersion: config.AgentConfigVersion,
		SessionID: request.SessionID, TurnID: request.TurnID,
		ProviderID: config.LLMProvider, ProviderType: config.LLMProviderType, Model: config.LLMModel,
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		CachedInputTokens: usage.CachedInputTokens, ReasoningTokens: usage.ReasoningTokens,
		LatencyMillis: latency.Milliseconds(), Status: "completed",
	}
}

func agentCoreRoute(config managedagents.AgentRuntimeConfig) coremodel.Route {
	providerRevision := config.LLMProviderRevision
	if providerRevision <= 0 {
		providerRevision = 1
	}
	modelRevision := config.LLMModelRevision
	if modelRevision <= 0 {
		modelRevision = 1
	}
	return coremodel.Route{
		ProviderInstanceID:    config.LLMProvider,
		ProviderConfigVersion: int(providerRevision),
		ModelID:               config.LLMModel,
		CatalogRevision:       fmt.Sprintf("model:%s:%d", config.LLMModel, modelRevision),
		CredentialRef:         config.LLMAPIKeyEnv,
	}
}

func validateAgentCoreBindings(state agentcore.State, route coremodel.Route, definitions []coremodel.ToolDefinition) error {
	raw := state.FeatureState[agentCoreRouteStateKey]
	if len(raw) == 0 {
		return errors.New("agent core state is missing its model route binding")
	}
	var stored coremodel.Route
	if err := json.Unmarshal(raw, &stored); err != nil {
		return fmt.Errorf("decode stored agent core route: %w", err)
	}
	storedJSON, _ := json.Marshal(stored)
	currentJSON, _ := json.Marshal(route)
	if string(storedJSON) != string(currentJSON) {
		return errors.New("agent core model route changed during a durable turn")
	}
	activeTools := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		activeTools = append(activeTools, definition.Name)
	}
	slices.Sort(activeTools)
	activeTools = slices.Compact(activeTools)
	if !slices.Equal(activeTools, state.ActiveTools) {
		return errors.New("agent core tool availability changed during a durable turn")
	}
	return nil
}

func agentCoreBudget(ctx context.Context, config managedagents.AgentRuntimeConfig, settings json.RawMessage, timeout time.Duration, configuredMaxRounds int) agentcore.Budget {
	maxRounds := positiveInt(configuredMaxRounds, 32)
	maxModelCalls := maxRounds + 8
	maxToolCalls := maxRounds * 8
	contextTokens := int64(config.ContextWindowTokens)
	if contextTokens <= 0 {
		contextTokens = managedagents.DefaultContextWindowTokens
	}
	budget := agentcore.Budget{
		MaxRounds: maxRounds, MaxModelCalls: maxModelCalls, MaxToolCalls: maxToolCalls,
		MaxInputTokens:     contextTokens * int64(maxModelCalls),
		MaxOutputTokens:    contextTokens * int64(maxModelCalls),
		MaxReasoningTokens: contextTokens * int64(maxModelCalls),
		MaxCostMicros:      1_000_000_000_000,
	}
	var configured struct {
		AgentCoreBudget struct {
			MaxRounds          int   `json:"max_rounds"`
			MaxModelCalls      int   `json:"max_model_calls"`
			MaxToolCalls       int   `json:"max_tool_calls"`
			MaxInputTokens     int64 `json:"max_input_tokens"`
			MaxOutputTokens    int64 `json:"max_output_tokens"`
			MaxReasoningTokens int64 `json:"max_reasoning_tokens"`
			MaxCostMicros      int64 `json:"max_cost_micros"`
		} `json:"agent_core_budget"`
	}
	if len(settings) > 0 && json.Unmarshal(settings, &configured) == nil {
		value := configured.AgentCoreBudget
		budget.MaxRounds = positiveInt(value.MaxRounds, budget.MaxRounds)
		budget.MaxModelCalls = positiveInt(value.MaxModelCalls, budget.MaxModelCalls)
		budget.MaxToolCalls = positiveInt(value.MaxToolCalls, budget.MaxToolCalls)
		budget.MaxInputTokens = positiveInt64(value.MaxInputTokens, budget.MaxInputTokens)
		budget.MaxOutputTokens = positiveInt64(value.MaxOutputTokens, budget.MaxOutputTokens)
		budget.MaxReasoningTokens = positiveInt64(value.MaxReasoningTokens, budget.MaxReasoningTokens)
		budget.MaxCostMicros = positiveInt64(value.MaxCostMicros, budget.MaxCostMicros)
	}
	if deadline, ok := ctx.Deadline(); ok {
		budget.Deadline = deadline
	} else if timeout > 0 {
		budget.Deadline = time.Now().UTC().Add(timeout)
	} else {
		budget.Deadline = time.Now().UTC().Add(time.Hour)
	}
	return budget
}

func agentCoreMaxOutputTokens(contextWindow int, settings json.RawMessage) int {
	if value := agentruntime.MaxOutputTokensForContext(contextWindow, settings); value > 0 {
		return value
	}
	return 4096
}

func agentCoreCompactionSettings(contextWindow int, settings json.RawMessage) (int, int) {
	if contextWindow <= 0 {
		contextWindow = managedagents.DefaultContextWindowTokens
	}
	threshold := max(contextWindow*55/100, 1024)
	summaryMaxChars := 12000
	var configured struct {
		AgentCoreCompactionThresholdTokens *int `json:"agent_core_compaction_threshold_tokens"`
		AgentCoreCompactionSummaryMaxChars *int `json:"agent_core_compaction_summary_max_chars"`
	}
	if len(settings) > 0 && json.Unmarshal(settings, &configured) == nil {
		if configured.AgentCoreCompactionThresholdTokens != nil && *configured.AgentCoreCompactionThresholdTokens >= 0 {
			threshold = *configured.AgentCoreCompactionThresholdTokens
		}
		if configured.AgentCoreCompactionSummaryMaxChars != nil && *configured.AgentCoreCompactionSummaryMaxChars > 0 {
			summaryMaxChars = *configured.AgentCoreCompactionSummaryMaxChars
		}
	}
	return threshold, summaryMaxChars
}

func positiveInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func positiveInt64(value, fallback int64) int64 {
	if value > 0 {
		return value
	}
	return fallback
}

type agentCoreClock struct{}

func (agentCoreClock) Now() time.Time { return time.Now().UTC() }

type agentCoreIDs struct {
	turnID  string
	attempt int
	next    int
}

func (g *agentCoreIDs) NewID(prefix string) string {
	g.next++
	return fmt.Sprintf("%s_%s_a%d_%06d", prefix, g.turnID, g.attempt, g.next)
}

type agentCoreLivePort struct {
	broker            *LiveEventBroker
	sessionID, turnID string
}

func (p agentCoreLivePort) Publish(_ context.Context, delta coremodel.LiveDelta) error {
	if p.broker == nil {
		return nil
	}
	p.broker.Publish(LiveEvent{
		SessionID: p.sessionID, TurnID: p.turnID, Type: LiveEventLLMText,
		Index: delta.Index, ToolRound: delta.Attempt, Operation: delta.Operation, ContentFormat: "markdown", Text: delta.Text,
	})
	return nil
}
