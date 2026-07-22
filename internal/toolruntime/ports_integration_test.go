package toolruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/agentcontrol"
	"tiggy-manage-agent/internal/agentcore"
	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	coremodel "tiggy-manage-agent/internal/model"
	"tiggy-manage-agent/internal/modelruntime"
	"tiggy-manage-agent/internal/toolruntime"
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
	adapter := modelruntime.LLMModel{
		Client: legacy,
		RouteResolver: modelruntime.RouteResolverFunc(func(_ context.Context, route coremodel.Route) (modelruntime.ResolvedRoute, error) {
			if route.CredentialRef != "credential_1" {
				t.Fatalf("credential ref = %q", route.CredentialRef)
			}
			return modelruntime.ResolvedRoute{Provider: route.ProviderInstanceID, ProviderType: llm.ProviderTypeOpenAI, Model: route.ModelID, BaseURL: "https://llm.example/v1", APIKey: "secret"}, nil
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

func TestLLMModelNormalizesUnsafeToolArguments(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		arguments json.RawMessage
		wantError bool
	}{
		{name: "empty", arguments: nil},
		{name: "malformed", arguments: json.RawMessage(`{"path":"README.md"`), wantError: true},
		{name: "non-object", arguments: json.RawMessage(`[]`), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			legacy := &recordingStreamingClient{
				response: llm.Response{Message: llm.Message{ToolCalls: []llm.ToolCall{{
					ID: "call_1", Type: "function", Function: llm.ToolCallFunction{Name: "default.read_file", Arguments: test.arguments},
				}}}},
				deltas: []llm.Delta{{Kind: llm.DeltaKindStop, FinishReason: "tool_calls"}},
			}
			response, err := (modelruntime.LLMModel{Client: legacy}).Generate(t.Context(), validModelRequest(), func(coremodel.Delta) error { return nil })
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			call := response.Message.Content[0].ToolCall
			if call == nil || string(call.Arguments) != `{}` || (call.ArgumentsError != "") != test.wantError {
				t.Fatalf("normalized call = %+v", call)
			}
			if err := call.Validate(); err != nil {
				t.Fatalf("normalized call validation error = %v", err)
			}
		})
	}
}

func TestLLMModelNormalizesProviderError(t *testing.T) {
	t.Parallel()

	adapter := modelruntime.LLMModel{Client: errorClient{err: &llm.ProviderError{
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
	adapter := toolruntime.ToolRuntime{
		Snapshot: mustToolSnapshot(t, registry, tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}),
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
	unapproved, err := adapter.Execute(context.Background(), state, plan)
	if err != nil || len(unapproved.Results) != 1 || !unapproved.Results[0].IsError || !strings.Contains(unapproved.Results[0].Content[0].Text, "tool_approval_required") {
		t.Fatalf("Execute() without approval result = %+v error = %v", unapproved, err)
	}
	if runtime.calls != 0 {
		t.Fatalf("dangerous runtime called before approval: %d", runtime.calls)
	}
	plan.Calls[0].ApprovalState = agentcore.ToolApprovalApproved
	plan.Calls[0].ApprovalSource = agentcore.ToolApprovalSourceHuman
	plan.Interactions[0].Decision = &agentcore.InteractionDecision{
		InteractionID: plan.Interactions[0].ID, Status: managedagents.InterventionStatusApproved,
	}
	result, err := adapter.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if runtime.calls != 1 || len(result.Results) != 1 || result.Results[0].IsError {
		t.Fatalf("execution calls = %d result = %+v", runtime.calls, result)
	}
}

func TestToolRuntimeApprovalIncludesSuggestedFilePermissionRules(t *testing.T) {
	t.Parallel()

	adapter := toolruntime.ToolRuntime{
		Snapshot: mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}),
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	call := coremodel.ToolCall{
		ID: "write_1", Name: "default.write_file",
		Arguments: json.RawMessage(`{"path":"/workspace/src/config/app.go","content":"new"}`),
	}
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Interactions) != 1 {
		t.Fatalf("interactions = %+v", plan.Interactions)
	}
	var request struct {
		Suggestions []tools.PermissionRuleSuggestion `json:"suggested_permission_rules"`
	}
	if err := json.Unmarshal(plan.Interactions[0].Request, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Suggestions) != 2 || request.Suggestions[0].Rule.Pattern != "/workspace/src/config/**" {
		t.Fatalf("approval request suggestions = %+v", request.Suggestions)
	}
}

func TestToolRuntimeEditApprovalBindsDurableDiffAndExecutesExactPreview(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "approval.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := stateWithPersistedFileRead(t, path)
	adapter := toolruntime.ToolRuntime{
		Snapshot:         mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}),
		ExecutionContext: tools.ExecutionContext{Provider: capability.LocalSystemProvider{}, SessionID: state.SessionID, TurnID: state.TurnID},
	}
	call := coremodel.ToolCall{
		ID: "edit_approval", Name: "default.edit_file",
		Arguments: mustRawJSON(t, map[string]any{"path": path, "old_string": "beta", "new_string": "BETA"}),
	}
	plan, err := adapter.Preflight(t.Context(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Interactions) != 1 {
		t.Fatalf("interactions = %+v", plan.Interactions)
	}
	var request struct {
		Preview struct {
			BaseRevision string `json:"base_revision"`
			BaseSHA256   string `json:"base_sha256"`
			UnifiedDiff  string `json:"unified_diff"`
			PatchSHA256  string `json:"patch_sha256"`
			CallSHA256   string `json:"call_sha256"`
			LinesAdded   int    `json:"lines_added"`
			LinesDeleted int    `json:"lines_deleted"`
		} `json:"edit_preview"`
		Suggestions []tools.PermissionRuleSuggestion `json:"suggested_permission_rules"`
	}
	if err := json.Unmarshal(plan.Interactions[0].Request, &request); err != nil {
		t.Fatal(err)
	}
	if request.Preview.BaseRevision == "" || request.Preview.BaseSHA256 == "" || request.Preview.PatchSHA256 == "" ||
		request.Preview.CallSHA256 == "" || !strings.Contains(request.Preview.UnifiedDiff, "-beta") ||
		!strings.Contains(request.Preview.UnifiedDiff, "+BETA") || request.Preview.LinesAdded != 1 || request.Preview.LinesDeleted != 1 ||
		len(request.Suggestions) != 2 {
		t.Fatalf("approval request = %+v", request)
	}
	plan.Calls[0].ApprovalState = agentcore.ToolApprovalApproved
	plan.Calls[0].ApprovalSource = agentcore.ToolApprovalSourceHuman
	plan.Interactions[0].Decision = &agentcore.InteractionDecision{
		InteractionID: plan.Interactions[0].ID, Status: managedagents.InterventionStatusApproved,
	}
	result, err := adapter.Execute(t.Context(), state, plan)
	if err != nil || len(result.Results) != 1 || result.Results[0].IsError {
		t.Fatalf("Execute() result = %+v error = %v", result, err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "alpha\nBETA\n" {
		t.Fatalf("approved content = %q err=%v", content, err)
	}
}

func TestToolRuntimeEditApprovalRejectsChangedFileAndTamperedPreview(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, path string, plan *agentcore.ToolBatchPlan)
	}{
		{
			name: "file changed",
			mutate: func(t *testing.T, path string, _ *agentcore.ToolBatchPlan) {
				t.Helper()
				if err := os.WriteFile(path, []byte("external\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "preview tampered",
			mutate: func(t *testing.T, _ string, plan *agentcore.ToolBatchPlan) {
				t.Helper()
				var request map[string]any
				if err := json.Unmarshal(plan.Interactions[0].Request, &request); err != nil {
					t.Fatal(err)
				}
				request["edit_preview"].(map[string]any)["patch_sha256"] = strings.Repeat("0", 64)
				plan.Interactions[0].Request = mustRawJSON(t, request)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "stale.txt")
			original := "old\n"
			if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
				t.Fatal(err)
			}
			state := stateWithPersistedFileRead(t, path)
			adapter := toolruntime.ToolRuntime{
				Snapshot:         mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}),
				ExecutionContext: tools.ExecutionContext{Provider: capability.LocalSystemProvider{}},
			}
			call := coremodel.ToolCall{ID: "edit_stale", Name: "default.edit_file", Arguments: mustRawJSON(t, map[string]any{
				"path": path, "old_string": "old", "new_string": "new",
			})}
			plan, err := adapter.Preflight(t.Context(), state, []coremodel.ToolCall{call})
			if err != nil || len(plan.Interactions) != 1 {
				t.Fatalf("Preflight() plan=%+v err=%v", plan, err)
			}
			plan.Calls[0].ApprovalState = agentcore.ToolApprovalApproved
			plan.Calls[0].ApprovalSource = agentcore.ToolApprovalSourceHuman
			plan.Interactions[0].Decision = &agentcore.InteractionDecision{InteractionID: plan.Interactions[0].ID, Status: managedagents.InterventionStatusApproved}
			test.mutate(t, path, &plan)
			result, err := adapter.Execute(t.Context(), state, plan)
			if err != nil || len(result.Results) != 1 || !result.Results[0].IsError || !result.Results[0].Retryable {
				t.Fatalf("Execute() result=%+v err=%v", result, err)
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if test.name == "preview tampered" && string(content) != original {
				t.Fatalf("tampered approval changed file: %q", content)
			}
			if test.name == "file changed" && string(content) != "external\n" {
				t.Fatalf("stale approval overwrote external content: %q", content)
			}
		})
	}
}

func TestToolRuntimeDoesNotRequestApprovalForInvalidEditPreview(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "invalid-preview.txt")
	if err := os.WriteFile(path, []byte("present\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := stateWithPersistedFileRead(t, path)
	adapter := toolruntime.ToolRuntime{
		Snapshot:         mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}),
		ExecutionContext: tools.ExecutionContext{Provider: capability.LocalSystemProvider{}},
	}
	call := coremodel.ToolCall{ID: "edit_invalid", Name: "default.edit_file", Arguments: mustRawJSON(t, map[string]any{
		"path": path, "old_string": "missing", "new_string": "new",
	})}
	plan, err := adapter.Preflight(t.Context(), state, []coremodel.ToolCall{call})
	if err != nil || len(plan.Interactions) != 0 {
		t.Fatalf("Preflight() plan=%+v err=%v", plan, err)
	}
	result, err := adapter.Execute(t.Context(), state, plan)
	if err != nil || len(result.Results) != 1 || !result.Results[0].IsError || !strings.Contains(string(result.Results[0].State), "match_not_found") {
		t.Fatalf("Execute() result=%+v err=%v", result, err)
	}
}

func stateWithPersistedFileRead(t *testing.T, path string) agentcore.State {
	t.Helper()
	readResult, err := (capability.LocalSystemProvider{}).ReadFile(t.Context(), capability.ReadFileRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	readState, err := json.Marshal(readResult)
	if err != nil {
		t.Fatal(err)
	}
	read := coremodel.ToolCall{ID: "read_receipt", Name: "default.read_file", Arguments: mustRawJSON(t, map[string]any{"path": path})}
	result := coremodel.ToolResult{
		CallID: read.ID, Name: read.Name, State: readState,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: string(readResult.Content)}},
	}
	return agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{{
			ID: "assistant_read", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal,
			Content: []coremodel.Content{{Type: coremodel.ContentToolCall, ToolCall: &read}},
		}},
		ToolJournal: []agentcore.ToolCallJournalEntry{{
			CallID: read.ID, Name: read.Name, Status: agentcore.ToolCallSucceeded, Result: &result,
		}},
	}
}

func TestToolRuntimeCurrentDenyOverridesOldApproval(t *testing.T) {
	t.Parallel()

	registry := tools.DefaultRegistry()
	oldSnapshot := mustToolSnapshot(t, registry, tools.InterventionPolicy{Mode: tools.InterventionModeApproveForMe})
	call := coremodel.ToolCall{
		ID: "write_1", Name: "default.write_file",
		Arguments: json.RawMessage(`{"path":"/workspace/config/app.json","content":"new"}`),
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	plan, err := (toolruntime.ToolRuntime{Snapshot: oldSnapshot}).Preflight(t.Context(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if plan.Calls[0].ApprovalState != agentcore.ToolApprovalAuto || plan.Calls[0].ApprovalSource != agentcore.ToolApprovalSourcePolicy {
		t.Fatalf("old policy plan = %+v", plan.Calls[0])
	}

	denyPolicy := tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess, Rules: []tools.PermissionRule{{
		ID: "deny-config", Tool: "default.write_file", Argument: "path", Pattern: "/workspace/config/**",
		Behavior: tools.PermissionRuleDeny, Source: tools.PermissionRuleSourceSession,
	}}}
	executor := &countingExecutor{}
	current := toolruntime.ToolRuntime{Snapshot: mustToolSnapshot(t, tools.DefaultRegistry(), denyPolicy), Executor: executor}
	result, err := current.Execute(t.Context(), state, plan)
	if err != nil || len(result.Results) != 1 || !result.Results[0].IsError || !strings.Contains(result.Results[0].Content[0].Text, "permission_denied") {
		t.Fatalf("Execute() result = %+v error = %v", result, err)
	}
	if executor.calls != 0 {
		t.Fatalf("new deny policy allowed %d executions", executor.calls)
	}
}

func TestToolRuntimeNewAskRejectsOldAutoApproval(t *testing.T) {
	t.Parallel()

	registry := tools.DefaultRegistry()
	oldSnapshot := mustToolSnapshot(t, registry, tools.InterventionPolicy{Mode: tools.InterventionModeApproveForMe})
	call := coremodel.ToolCall{
		ID: "write_1", Name: "default.write_file",
		Arguments: json.RawMessage(`{"path":"/workspace/output.txt","content":"new"}`),
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	plan, err := (toolruntime.ToolRuntime{Snapshot: oldSnapshot}).Preflight(t.Context(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if plan.Calls[0].ApprovalState != agentcore.ToolApprovalAuto || plan.Calls[0].ApprovalSource != agentcore.ToolApprovalSourcePolicy || len(plan.Interactions) != 0 {
		t.Fatalf("auto-approved plan = %+v", plan)
	}

	executor := &countingExecutor{}
	current := toolruntime.ToolRuntime{
		Snapshot: mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}),
		Executor: executor,
	}
	result, err := current.Execute(t.Context(), state, plan)
	if err != nil || len(result.Results) != 1 || !result.Results[0].IsError || !strings.Contains(result.Results[0].Content[0].Text, "tool_approval_required") {
		t.Fatalf("Execute() result = %+v error = %v", result, err)
	}
	if executor.calls != 0 {
		t.Fatalf("old auto-approval allowed %d executions", executor.calls)
	}
}

func TestToolRuntimeAcceptsHumanApprovalWhenCurrentPolicyStillAsks(t *testing.T) {
	t.Parallel()

	registry := tools.DefaultRegistry()
	requestPolicy := tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}
	call := coremodel.ToolCall{
		ID: "write_1", Name: "default.write_file",
		Arguments: json.RawMessage(`{"path":"/workspace/output.txt","content":"new"}`),
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	plan, err := (toolruntime.ToolRuntime{Snapshot: mustToolSnapshot(t, registry, requestPolicy)}).Preflight(t.Context(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	plan.Calls[0].ApprovalState = agentcore.ToolApprovalApproved
	plan.Calls[0].ApprovalSource = agentcore.ToolApprovalSourceHuman
	plan.Interactions[0].Decision = &agentcore.InteractionDecision{
		InteractionID: plan.Interactions[0].ID, Status: managedagents.InterventionStatusApproved,
	}
	currentPolicy := tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval, Rules: []tools.PermissionRule{{
		ID: "ask-output", Tool: "default.write_file", Argument: "path", Pattern: "/workspace/**",
		Behavior: tools.PermissionRuleAsk, Source: tools.PermissionRuleSourceSession,
	}}}
	executor := &countingExecutor{}
	current := toolruntime.ToolRuntime{Snapshot: mustToolSnapshot(t, tools.DefaultRegistry(), currentPolicy), Executor: executor}
	result, err := current.Execute(t.Context(), state, plan)
	if err != nil || len(result.Results) != 1 || result.Results[0].IsError {
		t.Fatalf("Execute() result = %+v error = %v", result, err)
	}
	if executor.calls != 1 {
		t.Fatalf("human-approved call executed %d times", executor.calls)
	}
}

func TestToolRuntimeReturnsRecoverableResultForPermissionDeny(t *testing.T) {
	t.Parallel()

	policy := tools.InterventionPolicy{Mode: tools.InterventionModeFullAccess, Rules: []tools.PermissionRule{{
		ID: "deny-config", Tool: "default.edit_file", Argument: "path",
		Pattern: "/workspace/config/**", Behavior: tools.PermissionRuleDeny, Source: "session",
	}}}
	adapter := toolruntime.ToolRuntime{
		Snapshot: mustToolSnapshot(t, tools.DefaultRegistry(), policy),
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	call := coremodel.ToolCall{ID: "call_deny", Name: "default.edit_file", Arguments: json.RawMessage(`{
		"path":"/workspace/config/app.json","old_string":"old","new_string":"new"
	}`)}
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Interactions) != 0 || len(plan.Calls) != 1 || plan.Calls[0].Disposition != agentcore.ToolDispositionDenied {
		t.Fatalf("unexpected denied plan: %+v", plan)
	}
	permission := plan.Calls[0].Permission
	if permission == nil || permission.Decision != "deny" || permission.Allowed || permission.Required || permission.MatchedRuleID != "deny-config" || permission.RuleSource != "session" {
		t.Fatalf("permission snapshot = %+v", permission)
	}
	result, err := adapter.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].IsError || !strings.Contains(result.Results[0].Content[0].Text, "permission_denied") {
		t.Fatalf("unexpected denied result: %+v", result)
	}
}

func TestToolRuntimePersistsInvalidArgumentsDuringPreflight(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(&dangerousRuntime{})
	adapter := toolruntime.ToolRuntime{Snapshot: fullAccessSnapshot(t, registry)}
	plan, err := adapter.Preflight(context.Background(), agentcore.State{SessionID: "session_1", TurnID: "turn_1"}, []coremodel.ToolCall{{
		ID: "call_1", Name: "danger.write", Arguments: json.RawMessage(`{"unexpected":true}`),
	}})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Calls) != 1 || plan.Calls[0].Disposition != agentcore.ToolDispositionReturnError || plan.Calls[0].ValidationState != agentcore.ToolValidationInvalidArguments {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestToolRuntimeConvertsBusinessFailureAndPreservesPartialResults(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(batchRuntime{})
	adapter := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, registry),
		Executor: partialFailureExecutor{},
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

func TestToolRuntimeValidatesWholeBatchBeforeExecution(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(batchRuntime{})
	executor := &countingExecutor{}
	runtime := toolruntime.ToolRuntime{Snapshot: fullAccessSnapshot(t, registry), Executor: executor}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	plan, err := runtime.Preflight(t.Context(), state, []coremodel.ToolCall{
		{ID: "call_1", Name: "batch.first", Arguments: json.RawMessage(`{}`)},
		{ID: "call_2", Name: "batch.second", Arguments: json.RawMessage(`{}`)},
	})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	plan.Calls[1].Permission.Mode = tools.InterventionModeRequestApproval
	_, err = runtime.Execute(t.Context(), state, plan)
	var fatal *agentcore.ToolFatalError
	if !errors.As(err, &fatal) || fatal.Code != "tool_policy_changed" {
		t.Fatalf("Execute() error = %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor ran %d calls before whole-batch validation failed", executor.calls)
	}
}

func TestToolRuntimePassesStableIdempotencyKeyToExecutor(t *testing.T) {
	t.Parallel()

	captured := make(chan string, 1)
	adapter := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, tools.NewRegistry(batchRuntime{})), Executor: idempotencyCaptureExecutor{captured: captured},
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

func TestToolRuntimeReplaysPersistedSegmentEditByBusinessIdentity(t *testing.T) {
	t.Parallel()

	path := "/workspace/report.md"
	previous := coremodel.ToolCall{
		ID: "edit_1", Name: "default.edit_file",
		Arguments: json.RawMessage(`{"path":"/workspace/report.md","old_string":"__TMA_PLACEHOLDER_REPORT_001__","new_string":"complete section","replace_all":false}`),
	}
	previousResult := coremodel.ToolResult{
		CallID: previous.ID, Name: previous.Name,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "edited"}},
	}
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{{
			ID: "assistant_1", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal,
			Content: []coremodel.Content{{Type: coremodel.ContentToolCall, ToolCall: &previous}},
		}},
		ToolJournal: []agentcore.ToolCallJournalEntry{{
			CallID: previous.ID, Name: previous.Name, Status: agentcore.ToolCallSucceeded, Result: &previousResult,
		}},
	}
	executor := &countingExecutor{}
	adapter := toolruntime.ToolRuntime{
		Snapshot: mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}), Executor: executor,
	}
	retry := previous
	retry.ID = "edit_2"
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{retry})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Interactions) != 0 {
		t.Fatalf("persisted replay must not request approval: %+v", plan.Interactions)
	}
	result, err := adapter.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if executor.calls != 0 || len(result.Results) != 1 || result.Results[0].IsError {
		t.Fatalf("persisted replay executed tool: calls=%d result=%+v", executor.calls, result)
	}
	if !strings.Contains(result.Results[0].Content[0].Text, "already applied") || !strings.Contains(string(result.Results[0].State), `"already_applied":true`) {
		t.Fatalf("unexpected replay result for %s: %+v", path, result.Results[0])
	}
}

func TestToolRuntimeInvalidatesPersistedSegmentReplayAfterRewrite(t *testing.T) {
	t.Parallel()

	edit := coremodel.ToolCall{
		ID: "edit_1", Name: "default.edit_file",
		Arguments: json.RawMessage(`{"path":"/workspace/report.md","old_string":"__TMA_PLACEHOLDER_REPORT_001__","new_string":"complete section"}`),
	}
	rewrite := coremodel.ToolCall{
		ID: "write_2", Name: "default.write_file",
		Arguments: json.RawMessage(`{"path":"/workspace/report.md","content":"new skeleton"}`),
	}
	editResult := coremodel.ToolResult{CallID: edit.ID, Name: edit.Name, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "edited"}}}
	writeResult := coremodel.ToolResult{CallID: rewrite.ID, Name: rewrite.Name, Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "rewritten"}}}
	writeResult.State, _ = json.Marshal(capability.FileResult{
		Path: "/workspace/report.md", FileRevision: "stat-v1:rewrite", ContentSHA256: strings.Repeat("a", 64),
	})
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{
			{ID: "assistant_1", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal, Content: []coremodel.Content{{Type: coremodel.ContentToolCall, ToolCall: &edit}}},
			{ID: "assistant_2", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal, Content: []coremodel.Content{{Type: coremodel.ContentToolCall, ToolCall: &rewrite}}},
		},
		ToolJournal: []agentcore.ToolCallJournalEntry{
			{CallID: edit.ID, Name: edit.Name, Status: agentcore.ToolCallSucceeded, Result: &editResult},
			{CallID: rewrite.ID, Name: rewrite.Name, Status: agentcore.ToolCallSucceeded, Result: &writeResult},
		},
	}
	executor := &countingExecutor{}
	adapter := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, tools.DefaultRegistry()), Executor: executor,
	}
	retry := edit
	retry.ID = "edit_3"
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{retry})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	result, err := adapter.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if executor.calls != 1 || len(result.Results) != 1 || !strings.Contains(result.Results[0].Content[0].Text, "executed") || strings.Contains(result.Results[0].Content[0].Text, "already applied") {
		t.Fatalf("rewrite must invalidate persisted edit replay: calls=%d result=%+v", executor.calls, result)
	}
}

func TestToolRuntimeRequiresPersistedReadReceiptBeforeEdit(t *testing.T) {
	t.Parallel()

	executor := &countingExecutor{}
	adapter := toolruntime.ToolRuntime{
		Snapshot: mustToolSnapshot(t, tools.DefaultRegistry(), tools.InterventionPolicy{Mode: tools.InterventionModeRequestApproval}), Executor: executor,
	}
	call := coremodel.ToolCall{
		ID: "edit_1", Name: "default.edit_file",
		Arguments: json.RawMessage(`{"path":"/workspace/note.txt","old_string":"old","new_string":"new"}`),
	}
	state := agentcore.State{SessionID: "session_1", TurnID: "turn_1"}
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{call})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Interactions) != 0 {
		t.Fatalf("blind edit must return read guidance before requesting approval: %+v", plan.Interactions)
	}
	result, err := adapter.Execute(context.Background(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if executor.calls != 0 || len(result.Results) != 1 || !result.Results[0].IsError || !result.Results[0].Retryable {
		t.Fatalf("blind edit unexpectedly executed: calls=%d result=%+v", executor.calls, result)
	}
	if !strings.Contains(string(result.Results[0].State), "file_read_required") {
		t.Fatalf("unexpected blind-edit result: %+v", result.Results[0])
	}
}

func TestToolRuntimeInjectsPersistedReadReceiptIntoEdit(t *testing.T) {
	t.Parallel()

	contentHash := strings.Repeat("b", 64)
	read := coremodel.ToolCall{ID: "read_1", Name: "default.read_file", Arguments: json.RawMessage(`{"path":"/workspace/note.txt"}`)}
	readState, _ := json.Marshal(capability.FileResult{
		Path: "/workspace/note.txt", FileRevision: "stat-v1:read", ContentSHA256: contentHash,
	})
	readResult := coremodel.ToolResult{
		CallID: read.ID, Name: read.Name, State: readState,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: "old"}},
	}
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{{
			ID: "assistant_1", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal,
			Content: []coremodel.Content{{Type: coremodel.ContentToolCall, ToolCall: &read}},
		}},
		ToolJournal: []agentcore.ToolCallJournalEntry{{
			CallID: read.ID, Name: read.Name, Status: agentcore.ToolCallSucceeded, Result: &readResult,
		}},
	}
	executor := &receiptCaptureExecutor{}
	adapter := toolruntime.ToolRuntime{
		Snapshot: fullAccessSnapshot(t, tools.DefaultRegistry()), Executor: executor,
	}
	edit := coremodel.ToolCall{
		ID: "edit_1", Name: "default.edit_file",
		Arguments: json.RawMessage(`{"path":"/workspace/note.txt","old_string":"old","new_string":"new"}`),
	}
	plan, err := adapter.Preflight(context.Background(), state, []coremodel.ToolCall{edit})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if _, err := adapter.Execute(context.Background(), state, plan); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if executor.calls != 1 || executor.revision != "stat-v1:read" || executor.contentSHA256 != contentHash {
		t.Fatalf("receipt was not injected: %+v", executor)
	}
}

func TestToolRuntimeRejectsEditWhenFileChangesAfterPersistedRead(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("old value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readResult, err := (capability.LocalSystemProvider{}).ReadFile(t.Context(), capability.ReadFileRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	readState, _ := json.Marshal(readResult)
	read := coremodel.ToolCall{ID: "read_1", Name: "default.read_file", Arguments: mustRawJSON(t, map[string]any{"path": path})}
	persistedResult := coremodel.ToolResult{
		CallID: read.ID, Name: read.Name, State: readState,
		Content: []coremodel.Content{{Type: coremodel.ContentText, Text: string(readResult.Content)}},
	}
	state := agentcore.State{
		SessionID: "session_1", TurnID: "turn_1",
		Messages: []coremodel.Message{{
			ID: "assistant_1", Role: coremodel.RoleAssistant, Visibility: coremodel.VisibilityInternal,
			Content: []coremodel.Content{{Type: coremodel.ContentToolCall, ToolCall: &read}},
		}},
		ToolJournal: []agentcore.ToolCallJournalEntry{{
			CallID: read.ID, Name: read.Name, Status: agentcore.ToolCallSucceeded, Result: &persistedResult,
		}},
	}
	if err := os.WriteFile(path, []byte("external value with a different size\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapter := toolruntime.ToolRuntime{
		Snapshot:         fullAccessSnapshot(t, tools.DefaultRegistry()),
		Executor:         tools.NewDefaultExecutor(),
		ExecutionContext: tools.ExecutionContext{Provider: capability.LocalSystemProvider{}},
	}
	edit := coremodel.ToolCall{
		ID: "edit_1", Name: "default.edit_file",
		Arguments: mustRawJSON(t, map[string]any{"path": path, "old_string": "old", "new_string": "new"}),
	}
	plan, err := adapter.Preflight(t.Context(), state, []coremodel.ToolCall{edit})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	result, err := adapter.Execute(t.Context(), state, plan)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Results) != 1 || !result.Results[0].IsError || (!strings.Contains(result.Results[0].Content[0].Text, "stale_file_revision") && !strings.Contains(result.Results[0].Content[0].Text, "stale_file_content")) {
		t.Fatalf("external modification was not rejected: %+v", result)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "external value with a different size\n" {
		t.Fatalf("stale edit changed external content: %q err=%v", content, err)
	}
}

func mustRawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestToolRuntimeHonorsManifestExecutionMetadata(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(manifestMetadataRuntime{})
	snapshot := fullAccessSnapshot(t, registry)
	definitions := snapshot.Definitions()
	if len(definitions) != 1 || definitions[0].Idempotency != "keyed" || definitions[0].ConcurrencyClass != "parallel" || definitions[0].LockKeyTemplate != "customer-record" {
		t.Fatalf("tool definitions = %+v", definitions)
	}
	adapter := toolruntime.ToolRuntime{Snapshot: snapshot}
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

func TestToolRuntimeExpandsEditFilePathLockKey(t *testing.T) {
	t.Parallel()

	registry := tools.DefaultRegistry()
	snapshot := fullAccessSnapshot(t, registry)
	adapter := toolruntime.ToolRuntime{Snapshot: snapshot}
	plan, err := adapter.Preflight(context.Background(), agentcore.State{SessionID: "session_1", TurnID: "turn_1"}, []coremodel.ToolCall{{
		ID: "call_edit", Name: "default.edit_file",
		Arguments: json.RawMessage(`{"path":"src/../src/app.go","old_string":"old","new_string":"new"}`),
	}})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Calls) != 1 || plan.Calls[0].LockKey != "session_1:file:src/app.go" {
		t.Fatalf("planned edit call = %+v", plan.Calls)
	}
}

func TestToolRuntimeInfersReadToolsAsSafeAndParallel(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(readRuntime{})
	snapshot := fullAccessSnapshot(t, registry)
	definitions := snapshot.Definitions()
	if len(definitions) != 1 || definitions[0].Idempotency != "safe" || definitions[0].ConcurrencyClass != "parallel" {
		t.Fatalf("tool definitions = %+v", definitions)
	}
	adapter := toolruntime.ToolRuntime{Snapshot: snapshot}
	plan, err := adapter.Preflight(context.Background(), agentcore.State{SessionID: "session_1", TurnID: "turn_1"}, []coremodel.ToolCall{{
		ID: "call_1", Name: "read.inspect", Arguments: json.RawMessage(`{}`),
	}})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(plan.Calls) != 1 || plan.Calls[0].SideEffect != tools.ToolRiskRead || plan.Calls[0].Idempotency != "safe" || plan.Calls[0].ExecutionMode != "parallel" || plan.Calls[0].LockKey != "" {
		t.Fatalf("planned call = %+v", plan.Calls)
	}
}

func TestFixedContextAndCompletionGateAdapters(t *testing.T) {
	t.Parallel()

	registry := tools.NewRegistry(&dangerousRuntime{})
	snapshot := fullAccessSnapshot(t, registry)
	fixed := modelruntime.FixedContext{
		Purpose: coremodel.PurposeAgent,
		Route:   coremodel.Route{ProviderInstanceID: "provider_1", ProviderConfigVersion: 1, ModelID: "model_1", CatalogRevision: "catalog_1"},
		Tools:   snapshot.Definitions(), MaxOutputTokens: 128,
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
	completion := modelruntime.CompletionGate{Gate: stubCompletionGate{}}
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
	compactor := modelruntime.LLMCompactor{
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

	compactor := modelruntime.LLMCompactor{Model: &recordingCompactionModel{}, ThresholdTokens: 10}
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
	controls := agentcontrol.SessionControls{Reader: reader}
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

type readRuntime struct{}

func (readRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "read", Meta: tools.Meta{Title: "Read", Description: "Read-only test tool"},
		API: []tools.API{{
			Name: "inspect", Description: "Inspect state", Risk: tools.ToolRiskRead,
			Parameters: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		}},
	}
}

func (readRuntime) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "ok"}, nil
}

func (manifestMetadataRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "metadata", Meta: tools.Meta{Title: "Metadata", Description: "Metadata test tool"},
		API: []tools.API{{
			Name: "write", Description: "Write with a backend idempotency key", Risk: "write",
			ApprovalPolicy: tools.ApprovalPolicyAlways, ApprovalReason: tools.InterventionReasonExternalWrite,
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

type countingExecutor struct{ calls int }

type receiptCaptureExecutor struct {
	calls         int
	revision      string
	contentSHA256 string
}

func (e *receiptCaptureExecutor) Execute(_ context.Context, call tools.Call, executionContext tools.ExecutionContext) (tools.ExecutionResult, error) {
	e.calls++
	e.revision = executionContext.ExpectedFileRevision
	e.contentSHA256 = executionContext.ExpectedFileContentSHA256
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "executed"}, nil
}

func (e *countingExecutor) Execute(_ context.Context, call tools.Call, _ tools.ExecutionContext) (tools.ExecutionResult, error) {
	e.calls++
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "executed"}, nil
}

func (e idempotencyCaptureExecutor) Execute(_ context.Context, call tools.Call, executionContext tools.ExecutionContext) (tools.ExecutionResult, error) {
	e.captured <- executionContext.IdempotencyKey
	return tools.ExecutionResult{ID: call.ID, Identifier: call.Identifier, APIName: call.APIName, Content: "ok"}, nil
}

func (r *dangerousRuntime) Manifest() tools.Manifest {
	return tools.Manifest{
		Identifier: "danger",
		Meta:       tools.Meta{Title: "Danger", Description: "Dangerous test tool"},
		API: []tools.API{{
			Name: "write", Description: "Writes a value", ApprovalPolicy: tools.ApprovalPolicyAlways, ApprovalReason: "write", Risk: "write",
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
