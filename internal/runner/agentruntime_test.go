package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/tools"
)

func TestAgentRuntimeTurnExecutorReturnsRuntimePayload(t *testing.T) {
	store := &mockStore{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: agentruntime.DemoRuntime{},
		Store:   store,
	}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if got := payloadText(result.AgentPayload); got != "Agent runtime received: hello" {
		t.Fatalf("expected runtime payload, got %q", got)
	}
	if result.Usage == nil {
		t.Fatal("expected usage record")
	}
	if result.Usage.WorkspaceID != "wksp_default" || result.Usage.AgentID != "agt_000001" || result.Usage.AgentConfigVersion != 1 || result.Usage.ProviderID != "fake" || result.Usage.Model != "fake-demo" {
		t.Fatalf("unexpected usage record: %#v", result.Usage)
	}
	if got := store.runtimeEventTypes(); len(got) != 5 ||
		got[0] != "runtime.started" ||
		got[1] != "runtime.thinking" ||
		got[2] != "runtime.llm_request" ||
		got[3] != "runtime.llm_response" ||
		got[4] != "runtime.completed" {
		t.Fatalf("unexpected runtime events: %#v", got)
	}
}

func TestAgentRuntimeTurnExecutorRequiresRuntime(t *testing.T) {
	executor := AgentRuntimeTurnExecutor{}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected missing runtime error")
	}
}

func TestAgentRuntimeTurnExecutorReturnsFailedUsageWhenRuntimeFailsAfterLLM(t *testing.T) {
	executor := AgentRuntimeTurnExecutor{
		Runtime: failedAfterLLMRuntime{},
		Store:   &mockStore{},
	}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000001",
		UserPayload: json.RawMessage(`{"content":[]}`),
	})
	if err == nil {
		t.Fatal("expected runtime error")
	}
	if result.Usage == nil {
		t.Fatal("expected failed usage record")
	}
	if result.Usage.Status != "failed" || result.Usage.ErrorMessage != "runtime event write failed" {
		t.Fatalf("unexpected failed usage record: %#v", result.Usage)
	}
	if result.Usage.ProviderID != "fake" || result.Usage.Model != "fake-demo" || result.Usage.TotalTokens != 12 {
		t.Fatalf("unexpected usage dimensions: %#v", result.Usage)
	}
}

func TestAgentRuntimeTurnExecutorPassesConversationHistory(t *testing.T) {
	store := &mockStore{
		history: []managedagents.ConversationMessage{{
			Seq:     3,
			Role:    "user",
			Payload: json.RawMessage(`{"content":[{"type":"text","text":"my name is Alice"}]}`),
		}},
	}
	runtime := &captureRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: runtime,
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:    "sesn_000001",
		TurnID:       "turn_000002",
		UserEventSeq: 5,
		UserPayload:  json.RawMessage(`{"content":[{"type":"text","text":"what is my name?"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if len(runtime.request.History) != 1 {
		t.Fatalf("expected 1 history message, got %#v", runtime.request.History)
	}
	if runtime.request.History[0].Role != "user" || runtime.request.History[0].Seq != 3 {
		t.Fatalf("unexpected history message: %#v", runtime.request.History[0])
	}
}

func TestAgentRuntimeTurnExecutorPassesSessionInterventionMode(t *testing.T) {
	store := &mockStore{
		runtimeSettings: json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
	}
	runtime := &captureRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: runtime,
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000002",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if runtime.request.Config.InterventionMode != "approve_for_me" {
		t.Fatalf("expected session intervention mode to reach runtime, got %q", runtime.request.Config.InterventionMode)
	}
}

func TestAgentRuntimeTurnExecutorPassesExecutionScopeToResolverAndRuntime(t *testing.T) {
	resolver := &captureProviderResolver{}
	runtime := &captureRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime:          runtime,
		Store:            &mockStore{},
		ProviderResolver: resolver,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000005",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if resolver.request.WorkspaceID != "wksp_default" || resolver.request.SessionID != "sesn_000001" || resolver.request.EnvironmentID != "env_000001" {
		t.Fatalf("unexpected resolver request: %#v", resolver.request)
	}
	if runtime.request.Config.WorkspaceID != "wksp_default" || runtime.request.Config.EnvironmentID != "env_000001" {
		t.Fatalf("unexpected runtime config scope: %#v", runtime.request.Config)
	}
	context := runtime.request.Config.ToolExecutionContext
	if context.WorkspaceID != "wksp_default" || context.SessionID != "sesn_000001" || context.EnvironmentID != "env_000001" || context.TurnID != "turn_000005" {
		t.Fatalf("unexpected tool execution context: %#v", context)
	}
	if context.Provider == nil {
		t.Fatal("expected scoped provider to reach tool execution context")
	}
}

func TestAgentRuntimeTurnExecutorPassesToolPolicyToResolverAndRuntime(t *testing.T) {
	resolver := &captureProviderResolver{}
	runtime := &captureRuntime{}
	store := &mockStore{
		toolsConfig: json.RawMessage(`{
			"tools": ["default.read_file"],
			"runtime": "cloud_sandbox"
		}`),
	}
	executor := AgentRuntimeTurnExecutor{
		Runtime:          runtime,
		Store:            store,
		ProviderResolver: resolver,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000006",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	if resolver.request.ToolRuntime != "cloud_sandbox" {
		t.Fatalf("expected tool policy minimum sandbox to reach resolver, got %#v", resolver.request)
	}
	if len(runtime.request.Config.ModelTools) != 1 || runtime.request.Config.ModelTools[0].Function.Name != "default.read_file" {
		t.Fatalf("expected configured model tools to be filtered, got %#v", runtime.request.Config.ModelTools)
	}
	if _, _, ok := runtime.request.Config.ToolRegistry.GetAPI("default", "run_command"); ok {
		t.Fatal("expected run_command to be disabled in configured registry")
	}
}

func TestAgentRuntimeTurnExecutorFiltersToolsByProviderCapabilities(t *testing.T) {
	resolver := &captureProviderResolver{provider: readOnlyProvider{}}
	runtime := &captureRuntime{}
	executor := AgentRuntimeTurnExecutor{
		Runtime:          runtime,
		Store:            &mockStore{},
		ProviderResolver: resolver,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000007",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	modelTools := runtime.request.Config.ModelTools
	names := map[string]bool{}
	for _, modelTool := range modelTools {
		names[modelTool.Function.Name] = true
	}
	if len(modelTools) != 3 || !names["default.read_file"] || !names["web.search"] || !names["web.crawl"] {
		t.Fatalf("expected provider capability filter to keep read_file plus server builtin web tools, got %#v", modelTools)
	}
	if _, _, ok := runtime.request.Config.ToolRegistry.GetAPI("default", "run_command"); ok {
		t.Fatal("expected run_command to be unavailable without exec capability")
	}
	if _, _, ok := runtime.request.Config.ToolRegistry.GetAPI("default", "read_file"); !ok {
		t.Fatal("expected read_file to remain available")
	}
}

func TestAgentRuntimeTurnExecutorFiltersLocalSystemToolsByWorkerCapabilities(t *testing.T) {
	runtime := &captureRuntime{}
	store := &mockStore{
		toolsConfig: json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		workers: []managedagents.Worker{{
			ID:          "wrk_000001",
			WorkspaceID: "wksp_default",
			Status:      managedagents.WorkerStatusOnline,
			Capabilities: json.RawMessage(`{
				"namespaces": ["default"],
				"apis": ["default.read_file"],
				"runtimes": ["local_system"],
				"capabilities": ["filesystem.read"]
			}`),
		}},
	}
	executor := AgentRuntimeTurnExecutor{
		Runtime: runtime,
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000008",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	modelTools := runtime.request.Config.ModelTools
	if len(modelTools) != 1 || modelTools[0].Function.Name != "default.read_file" {
		t.Fatalf("expected worker capability filter to expose only read_file, got %#v", modelTools)
	}
	if _, _, ok := runtime.request.Config.ToolRegistry.GetAPI("default", "run_command"); ok {
		t.Fatal("expected run_command to be unavailable without matching worker")
	}
	if _, ok := runtime.request.Config.ToolExecutionContext.Provider.(execution.WorkerBackedProvider); !ok {
		t.Fatalf("expected local_system execution to use worker-backed provider, got %T", runtime.request.Config.ToolExecutionContext.Provider)
	}
}

func TestAgentRuntimeTurnExecutorHidesLocalSystemToolsWhenNoRuntimeExists(t *testing.T) {
	runtime := &captureRuntime{}
	store := &mockStore{
		toolsConfig: json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
	}
	executor := AgentRuntimeTurnExecutor{
		Runtime: runtime,
		Store:   store,
		ProviderResolver: &captureProviderResolver{
			provider: capability.UnavailableProvider{
				Runtime: tools.ToolRuntimeLocalSystem,
				Reason:  "no matching worker",
			},
		},
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000011",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if len(runtime.request.Config.ModelTools) != 0 {
		t.Fatalf("expected no local_system model tools without worker/runtime, got %#v", runtime.request.Config.ModelTools)
	}
	if contextProvider, ok := runtime.request.Config.ToolExecutionContext.Provider.(capability.UnavailableProvider); !ok || contextProvider.Runtime != tools.ToolRuntimeLocalSystem {
		t.Fatalf("expected unavailable local_system provider, got %T %#v", runtime.request.Config.ToolExecutionContext.Provider, runtime.request.Config.ToolExecutionContext.Provider)
	}
}

func TestAgentRuntimeTurnExecutorDoesNotExposeToolsFromSplitWorkerCapabilities(t *testing.T) {
	runtime := &captureRuntime{}
	store := &mockStore{
		toolsConfig: json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		workers: []managedagents.Worker{
			{
				ID:          "wrk_api_without_capability",
				WorkspaceID: "wksp_default",
				Status:      managedagents.WorkerStatusOnline,
				Capabilities: json.RawMessage(`{
					"namespaces": ["default"],
					"apis": ["default.run_command"],
					"runtimes": ["local_system"],
					"capabilities": ["filesystem.read"]
				}`),
			},
			{
				ID:          "wrk_capability_without_api",
				WorkspaceID: "wksp_default",
				Status:      managedagents.WorkerStatusOnline,
				Capabilities: json.RawMessage(`{
					"namespaces": ["default"],
					"apis": ["default.read_file"],
					"runtimes": ["local_system"],
					"capabilities": ["exec"]
				}`),
			},
			{
				ID:          "wrk_reader",
				WorkspaceID: "wksp_default",
				Status:      managedagents.WorkerStatusOnline,
				Capabilities: json.RawMessage(`{
					"namespaces": ["default"],
					"apis": ["default.read_file"],
					"runtimes": ["local_system"],
					"capabilities": ["filesystem.read"]
				}`),
			},
		},
	}
	executor := AgentRuntimeTurnExecutor{
		Runtime: runtime,
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000010",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"go"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	modelTools := runtime.request.Config.ModelTools
	if len(modelTools) != 1 || modelTools[0].Function.Name != "default.read_file" {
		t.Fatalf("expected only singly executable worker tool to reach model, got %#v", modelTools)
	}
	if _, _, ok := runtime.request.Config.ToolRegistry.GetAPI("default", "run_command"); ok {
		t.Fatal("expected run_command to stay hidden when API and exec capability are split across workers")
	}
}

func TestAgentRuntimeTurnExecutorExecutesLocalSystemToolThroughWorkerBackedProvider(t *testing.T) {
	store := &mockStore{
		runtimeSettings: json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
		toolsConfig:     json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		workers: []managedagents.Worker{{
			ID:          "wrk_000001",
			WorkspaceID: "wksp_default",
			Status:      managedagents.WorkerStatusOnline,
			Capabilities: json.RawMessage(`{
				"namespaces": ["default"],
				"apis": ["default.run_command"],
				"runtimes": ["local_system"],
				"capabilities": ["exec"]
			}`),
		}},
	}
	executor := AgentRuntimeTurnExecutor{
		Runtime: agentruntime.DemoRuntime{},
		Store:   store,
		Timeout: 2 * time.Second,
	}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000009",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please run tma.verify_tool_call"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); !strings.Contains(got, "tma-session-tool-ok") {
		t.Fatalf("expected final response to include worker tool marker, got %q", got)
	}

	enqueued := store.enqueuedWorkerWork()
	if len(enqueued) != 1 {
		t.Fatalf("expected one enqueued worker work, got %#v", enqueued)
	}
	work := enqueued[0]
	if work.WorkerID != "wrk_000001" || work.WorkspaceID != "wksp_default" || work.SessionID != "sesn_000001" || work.TurnID != "turn_000009" {
		t.Fatalf("unexpected enqueued work scope: %#v", work)
	}
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(work.Payload, &invocation); err != nil {
		t.Fatalf("decode enqueued invocation: %v", err)
	}
	if invocation.ProtocolVersion != tools.WorkProtocolVersion || invocation.Namespace != "default" || invocation.API != "run_command" || invocation.Runtime != "local_system" {
		t.Fatalf("unexpected worker invocation: %#v", invocation)
	}
	if got := store.runtimeEventTypes(); !containsString(got, managedagents.EventRuntimeToolCall) || !containsString(got, managedagents.EventRuntimeToolResult) {
		t.Fatalf("expected tool call/result runtime events, got %#v", got)
	}
}

func TestAgentRuntimeTurnExecutorPersistsWorkerExportedFilesAsSessionArtifacts(t *testing.T) {
	store := &mockStore{
		runtimeSettings: json.RawMessage(`{"intervention_mode":"approve_for_me"}`),
		toolsConfig:     json.RawMessage(`{"tools":["default"],"runtime":"local_system"}`),
		workers: []managedagents.Worker{{
			ID:          "wrk_000001",
			WorkspaceID: "wksp_default",
			Status:      managedagents.WorkerStatusOnline,
			Capabilities: json.RawMessage(`{
				"namespaces": ["default"],
				"apis": ["default.run_command"],
				"runtimes": ["local_system"],
				"capabilities": ["exec"]
			}`),
		}},
		sessions: map[string]managedagents.Session{
			"sesn_000001": {
				ID:            "sesn_000001",
				WorkspaceID:   "wksp_default",
				EnvironmentID: "env_000001",
			},
		},
	}
	objectStore, err := objectstore.NewLocalFSClient(objectstore.Config{
		RootDir: t.TempDir(),
		Bucket:  "tma-artifacts",
	})
	if err != nil {
		t.Fatalf("new object store: %v", err)
	}
	executor := AgentRuntimeTurnExecutor{
		Runtime:        agentruntime.DemoRuntime{},
		Store:          store,
		ObjectStore:    objectStore,
		ArtifactBucket: "tma-artifacts",
		Timeout:        2 * time.Second,
	}

	result, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000012",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"please run tma.verify_worker_export"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); !strings.Contains(got, "tma-worker-export-ok") {
		t.Fatalf("expected final response to include worker export marker, got %q", got)
	}
	if len(store.createdObjects) != 2 || len(store.createdArtifacts) != 2 {
		t.Fatalf("expected tool result artifact plus exported file artifact, got objects=%#v artifacts=%#v", store.createdObjects, store.createdArtifacts)
	}
	if store.createdArtifacts[1].ArtifactType != managedagents.ArtifactTypeFile || store.createdArtifacts[1].Name != "worker-export.txt" {
		t.Fatalf("unexpected exported file artifact input: %#v", store.createdArtifacts[1])
	}
	get, err := objectStore.GetObject(context.Background(), objectstore.GetObjectInput{
		Bucket: "tma-artifacts",
		Key:    store.createdObjects[1].ObjectKey,
	})
	if err != nil {
		t.Fatalf("get exported object: %v", err)
	}
	defer get.Body.Close()
	body, err := io.ReadAll(get.Body)
	if err != nil {
		t.Fatalf("read exported object: %v", err)
	}
	if string(body) != "tma-worker-export-ok" {
		t.Fatalf("unexpected exported artifact body: %q", string(body))
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestAgentRuntimeTurnExecutorSavesPendingInterventionSteps(t *testing.T) {
	store := &mockStore{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: interventionStepRuntime{},
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000003",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"edit"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	interventions := store.savedInterventions()
	if len(interventions) != 1 {
		t.Fatalf("expected 1 saved intervention, got %#v", interventions)
	}
	got := interventions[0]
	if got.TurnID != "turn_000003" || got.CallID != "call_edit" || got.ToolIdentifier != "default" || got.APIName != "edit_file" {
		t.Fatalf("unexpected saved intervention: %#v", got)
	}
	if got.InterventionMode != "request_approval" || got.Reason != "optional" {
		t.Fatalf("unexpected intervention policy fields: %#v", got)
	}
	if string(got.Arguments) != `{"path":"README.md"}` {
		t.Fatalf("unexpected intervention arguments: %s", string(got.Arguments))
	}
	if string(got.Continuation) != `[{"role":"assistant","content":[{"type":"text","text":"calling tool"}]}]` || got.ContinuationRound != 2 {
		t.Fatalf("unexpected intervention continuation: round=%d messages=%s", got.ContinuationRound, string(got.Continuation))
	}
}

func TestAgentRuntimeTurnExecutorSavesRuntimeSummary(t *testing.T) {
	store := &mockStore{}
	executor := AgentRuntimeTurnExecutor{
		Runtime: summaryRuntime{},
		Store:   store,
	}

	_, err := executor.RunTurn(t.Context(), TurnRequest{
		SessionID:   "sesn_000001",
		TurnID:      "turn_000004",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}

	summary, ok := store.summaries["sesn_000001"]
	if !ok {
		t.Fatal("expected summary to be saved")
	}
	if summary.SummaryText != "generated summary" || summary.SourceUntilSeq != 7 {
		t.Fatalf("unexpected saved summary: %#v", summary)
	}
}

type captureRuntime struct {
	request agentruntime.TurnRequest
}

func (r *captureRuntime) RunTurn(_ context.Context, request agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	r.request = request
	return agentruntime.TurnResult{
		AgentPayload: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}, nil
}

type captureProviderResolver struct {
	request  execution.ProviderRequest
	provider capability.Provider
}

func (r *captureProviderResolver) ResolveProvider(request execution.ProviderRequest) capability.Provider {
	r.request = request
	if r.provider != nil {
		return r.provider
	}
	return capability.LocalSystemProvider{}
}

type readOnlyProvider struct{}

func (readOnlyProvider) ToolRuntime() string {
	return "local_system"
}

func (readOnlyProvider) ToolCapabilities() []string {
	return []string{"filesystem.read"}
}

func (readOnlyProvider) RunCommand(context.Context, capability.RunCommandRequest) (capability.CommandResult, error) {
	return capability.CommandResult{}, nil
}

func (readOnlyProvider) ExecuteCode(context.Context, capability.ExecuteCodeRequest) (capability.CommandResult, error) {
	return capability.CommandResult{}, nil
}

func (readOnlyProvider) ReadFile(context.Context, capability.ReadFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (readOnlyProvider) WriteFile(context.Context, capability.WriteFileRequest) (capability.FileResult, error) {
	return capability.FileResult{}, nil
}

func (readOnlyProvider) EditFile(context.Context, capability.EditFileRequest) (capability.EditFileResult, error) {
	return capability.EditFileResult{}, nil
}

type failedAfterLLMRuntime struct{}

func (failedAfterLLMRuntime) RunTurn(context.Context, agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	return agentruntime.TurnResult{
		Usage: llm.Usage{
			InputTokens:  8,
			OutputTokens: 4,
			TotalTokens:  12,
		},
		Provider:     "fake",
		ProviderType: "fake",
		Model:        "fake-demo",
	}, errors.New("runtime event write failed")
}

type summaryRuntime struct{}

func (summaryRuntime) RunTurn(context.Context, agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	return agentruntime.TurnResult{
		AgentPayload:          json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
		SummaryText:           "generated summary",
		SummarySourceUntilSeq: 7,
	}, nil
}

type interventionStepRuntime struct{}

func (interventionStepRuntime) RunTurn(ctx context.Context, request agentruntime.TurnRequest) (agentruntime.TurnResult, error) {
	if err := request.EmitStep(ctx, agentruntime.Step{
		Type:    managedagents.EventRuntimeToolInterventionRequired,
		Message: "Tool call requires approval before execution.",
		Data: map[string]any{
			"id":                "call_edit",
			"identifier":        "default",
			"api_name":          "edit_file",
			"arguments":         map[string]any{"path": "README.md"},
			"intervention_mode": "request_approval",
			"reason":            "optional",
		},
		Private: map[string]any{
			"continuation_messages": []llm.Message{{
				Role:    "assistant",
				Content: []llm.ContentPart{{Type: "text", Text: "calling tool"}},
			}},
			"continuation_round": 2,
		},
	}); err != nil {
		return agentruntime.TurnResult{}, err
	}
	return agentruntime.TurnResult{
		AgentPayload: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}, nil
}
