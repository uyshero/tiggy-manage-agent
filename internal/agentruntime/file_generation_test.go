package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func TestRuntimeRejectsMultipleFileMutationsBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.txt")
	second := filepath.Join(dir, "second.txt")
	client := &scriptedFileGenerationClient{responses: []llm.Response{
		toolCallResponse(
			llm.ToolCall{ID: "write_1", Type: "function", Function: llm.ToolCallFunction{Name: "default.write_file", Arguments: mustJSON(t, map[string]any{"path": first, "content": "one"})}},
			llm.ToolCall{ID: "write_2", Type: "function", Function: llm.ToolCallFunction{Name: "default.write_file", Arguments: mustJSON(t, map[string]any{"path": second, "content": "two"})}},
		),
		textResponse("recovered"),
	}}

	result, err := (DemoRuntime{Client: client, MaxToolRounds: 4}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_batch", TurnID: "turn_batch",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"write files"}]}`),
		Config: Config{
			InterventionMode:     tools.InterventionModeFullAccess,
			ToolExecutor:         tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{Provider: capabilityLocalProvider()},
		},
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "recovered" {
		t.Fatalf("unexpected result %q", got)
	}
	for _, path := range []string{first, second} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("multi-mutation response unexpectedly wrote %s: %v", path, statErr)
		}
	}
}

func TestProviderModelFileMutationLimitsOverrideSessionDefault(t *testing.T) {
	settings := json.RawMessage(`{
		"file_mutation_recommended_tokens":5000,
		"file_mutation_max_tokens":7000,
		"file_mutation_limits":{"volcengine/doubao":{"recommended_tokens":3000,"max_tokens":4000}}
	}`)
	limits := fileMutationLimits(settings, "volcengine", "doubao")
	if limits.RecommendedTokens != 3000 || limits.MaxTokens != 4000 {
		t.Fatalf("unexpected model limits: %#v", limits)
	}
}

func TestPersistedSegmentHashRequiresExactReplayContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hash.txt")
	placeholder := "__TMA_PLACEHOLDER_HASH_001__"
	replacement := "exact segment"
	hash := sha256.Sum256([]byte(replacement))
	state := newSegmentedFileGenerationState()
	state.Tasks[path] = &segmentedFileTaskState{
		Path: path, SegmentHashes: map[string]string{placeholder: hex.EncodeToString(hash[:])},
	}
	call := tools.Call{ID: "retry", APIName: "edit_file", Arguments: mustJSON(t, map[string]any{
		"path": path, "old_string": placeholder, "new_string": replacement,
	})}
	if result, ok := state.idempotentReplay(call); !ok || !strings.Contains(result.Content, "already applied") {
		t.Fatalf("expected exact hash replay, result=%#v ok=%v", result, ok)
	}
	call.Arguments = mustJSON(t, map[string]any{"path": path, "old_string": placeholder, "new_string": replacement + " changed"})
	if _, ok := state.idempotentReplay(call); ok {
		t.Fatal("different replacement content must not reuse persisted idempotency evidence")
	}
}

func TestSegmentedPlanApprovalPersistsAndRedactsLongArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approved.js")
	placeholder := "__TMA_PLACEHOLDER_APPROVED_001__"
	writeArguments := mustJSON(t, map[string]any{"path": path, "content": placeholder})
	client := &scriptedFileGenerationClient{responses: []llm.Response{
		toolCallResponse(llm.ToolCall{ID: "write", Type: "function", Function: llm.ToolCallFunction{Name: "default.write_file", Arguments: writeArguments}}),
		toolCallResponse(llm.ToolCall{ID: "edit", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.edit_file", Arguments: mustJSON(t, map[string]any{
				"path": path, "old_string": placeholder, "new_string": "function approved() { return true; }", "replace_all": false,
			}),
		}}),
		textResponse("too early"),
		toolCallResponse(llm.ToolCall{ID: "validate", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.run_command", Arguments: mustJSON(t, map[string]any{"command": "sh", "args": []string{"-c", "node --check \"$1\"", "sh", path}}),
		}}),
	}}
	registry := tools.DefaultRegistry()
	config := Config{
		InterventionMode:     tools.InterventionModeRequestApproval,
		ToolRegistry:         registry,
		ToolExecutor:         tools.RegistryExecutor{Registry: registry},
		ToolExecutionContext: tools.ExecutionContext{Provider: capabilityLocalProvider()},
	}
	var initialSteps []Step
	_, err := (DemoRuntime{Client: client, MaxToolRounds: 8}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_approval", TurnID: "turn_approval",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"create approved file"}]}`),
		Config:      config,
		EmitStep: func(_ context.Context, step Step) error {
			initialSteps = append(initialSteps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected skeleton approval, got %v", err)
	}
	required := firstStepType(initialSteps, managedagents.EventRuntimeToolInterventionRequired)
	arguments, ok := required.Data["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("expected observable arguments map: %#v", required.Data)
	}
	contentSummary, ok := arguments["content"].(map[string]any)
	if !ok || contentSummary["redacted"] != true || contentSummary["sha256"] == "" {
		t.Fatalf("expected redacted content metadata, got %#v", arguments["content"])
	}
	privateArguments, ok := required.Private["arguments"].(json.RawMessage)
	if !ok || !strings.Contains(string(privateArguments), placeholder) {
		t.Fatalf("private continuation lost executable arguments: %#v", required.Private)
	}
	continuation := required.Private["continuation_messages"].([]llm.Message)
	continuationState := required.Private["continuation_state"].(json.RawMessage)
	continuationRound := required.Private["continuation_round"].(int)

	var resumedSteps []Step
	_, err = (DemoRuntime{Client: client, MaxToolRounds: 8}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_approval", TurnID: "turn_approval", Config: config,
		ResumeIntervention: &InterventionResume{
			Call:   tools.Call{ID: "write", Identifier: tools.DefaultIdentifier, APIName: "write_file", Arguments: writeArguments},
			Status: managedagents.InterventionStatusApproved, Continuation: continuation,
			ContinuationRound: continuationRound, ContinuationState: continuationState,
		},
		EmitStep: func(_ context.Context, step Step) error {
			resumedSteps = append(resumedSteps, step)
			return nil
		},
	})
	if !errors.Is(err, ErrPendingIntervention) {
		t.Fatalf("expected only validation command to need another approval, got %v", err)
	}
	nextRequired := firstStepType(resumedSteps, managedagents.EventRuntimeToolInterventionRequired)
	if nextRequired.Data["api_name"] != "run_command" {
		t.Fatalf("segmented edit should reuse plan approval; pending call=%#v", nextRequired.Data)
	}
	approved := firstStepType(resumedSteps, managedagents.EventRuntimeToolInterventionApproved)
	if approved.Data["api_name"] != "edit_file" || approved.Data["approval_source"] != "segmented_plan" {
		t.Fatalf("expected edit plan auto-approval, got %#v", approved.Data)
	}
}

func TestRuntimeBlocksCompletionUntilSegmentedFileIsValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.js")
	placeholder := "__TMA_PLACEHOLDER_REPORT_001__"
	client := &scriptedFileGenerationClient{responses: []llm.Response{
		toolCallResponse(llm.ToolCall{ID: "write", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.write_file", Arguments: mustJSON(t, map[string]any{"path": path, "content": placeholder}),
		}}),
		toolCallResponse(llm.ToolCall{ID: "edit", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.edit_file", Arguments: mustJSON(t, map[string]any{
				"path": path, "old_string": placeholder, "new_string": "function report() { return 1; }", "replace_all": false,
			}),
		}}),
		textResponse("finished too early"),
		toolCallResponse(llm.ToolCall{ID: "validate", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.run_command", Arguments: mustJSON(t, map[string]any{"command": "sh", "args": []string{"-c", "node --check \"$1\"", "sh", path}}),
		}}),
		textResponse("validated and finished"),
	}}
	artifactRecorder := &recordingArtifactRecorder{}
	var steps []Step

	result, err := (DemoRuntime{Client: client, MaxToolRounds: 8}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_segment", TurnID: "turn_segment",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"create report"}]}`),
		Config: Config{
			InterventionMode:     tools.InterventionModeFullAccess,
			ToolExecutor:         tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{Provider: capabilityLocalProvider(), ArtifactRecorder: artifactRecorder},
		},
		EmitStep: collectCompletionSteps(&steps),
	})
	if err != nil {
		t.Fatalf("run turn: %v", err)
	}
	if got := payloadText(result.AgentPayload); got != "validated and finished" {
		t.Fatalf("completion gate allowed wrong response %q", got)
	}
	if len(client.requests) != 5 {
		t.Fatalf("expected premature completion to trigger another model request, got %d", len(client.requests))
	}
	if got := messagesText(client.requests[3].Messages); !containsAll(got, "Runtime completion gate blocked", "requires a successful syntax check") {
		t.Fatalf("missing hard completion feedback: %s", got)
	}
	blocked := firstStepType(steps, managedagents.EventRuntimeCompletionBlocked)
	if blocked.Data["validator"] != "builtin.segmented_file_generation" {
		t.Fatalf("expected segmented-file completion validator event, got %#v", blocked.Data)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != "function report() { return 1; }" {
		t.Fatalf("unexpected completed file %q: %v", content, readErr)
	}
	if len(artifactRecorder.calls) != 1 || artifactRecorder.calls[0].APIName != "segmented_file_generation" || len(artifactRecorder.results[0].ExportedFiles) != 1 {
		t.Fatalf("expected only one final artifact publication, calls=%#v results=%#v", artifactRecorder.calls, artifactRecorder.results)
	}
}

func TestRuntimeRequiresReadReceiptBeforeOrdinaryEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("key=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	editCall := func(id string) llm.Response {
		return toolCallResponse(llm.ToolCall{ID: id, Type: "function", Function: llm.ToolCallFunction{
			Name: "default.edit_file", Arguments: mustJSON(t, map[string]any{
				"path": path, "old_string": "key=old", "new_string": "key=new", "replace_all": false,
			}),
		}})
	}
	client := &scriptedFileGenerationClient{responses: []llm.Response{
		editCall("blind-edit"),
		toolCallResponse(llm.ToolCall{ID: "read", Type: "function", Function: llm.ToolCallFunction{
			Name: "default.read_file", Arguments: mustJSON(t, map[string]any{"path": path}),
		}}),
		editCall("receipt-edit"),
		textResponse("updated after reading"),
	}}
	var steps []Step
	result, err := (DemoRuntime{Client: client, MaxToolRounds: 6}).RunTurn(t.Context(), TurnRequest{
		SessionID: "sesn_read_receipt", TurnID: "turn_read_receipt",
		UserPayload: json.RawMessage(`{"content":[{"type":"text","text":"update note"}]}`),
		Config: Config{
			InterventionMode: tools.InterventionModeFullAccess,
			ToolRegistry:     tools.DefaultRegistry(), ToolExecutor: tools.NewDefaultExecutor(),
			ToolExecutionContext: tools.ExecutionContext{Provider: capabilityLocalProvider()},
		},
		EmitStep: collectCompletionSteps(&steps),
	})
	if err != nil {
		t.Fatalf("run read receipt flow: %v", err)
	}
	if payloadText(result.AgentPayload) != "updated after reading" {
		t.Fatalf("unexpected final response: %s", payloadText(result.AgentPayload))
	}
	firstResult := firstStepType(steps, managedagents.EventRuntimeToolResult)
	executionError, ok := firstResult.Data["error"].(*tools.ExecutionError)
	if !ok || executionError.Type != "file_read_required" {
		t.Fatalf("blind edit was not rejected with file_read_required: %#v", firstResult.Data)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != "key=new\n" {
		t.Fatalf("unexpected edited content %q: %v", content, readErr)
	}
}

func TestSegmentedStateTracksIndentedPlaceholderEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	placeholder := "__TMA_PLACEHOLDER_HEADER_001__"
	state := newSegmentedFileGenerationState()
	state.Tasks[path] = &segmentedFileTaskState{
		Path: path, Remaining: []string{placeholder}, SegmentHashes: map[string]string{}, PlanApproved: true,
	}
	call := tools.Call{APIName: "edit_file", Arguments: mustJSON(t, map[string]any{
		"path": path, "old_string": "        " + placeholder + "\n", "new_string": "        <header>complete</header>\n", "replace_all": false,
	})}
	if !state.planApproves(call) {
		t.Fatal("indented placeholder edit should reuse segmented plan approval")
	}
	resultState := mustJSON(t, capability.EditFileResult{Path: path, Replacements: 1, Success: true})
	state.observe(call, tools.ExecutionResult{State: resultState}, true)
	if len(state.Tasks[path].Remaining) != 0 || state.Tasks[path].SegmentHashes[placeholder] == "" {
		t.Fatalf("indented placeholder was not consumed: %#v", state.Tasks[path])
	}
}

func TestPublishesReferencedVerifiedWorkspaceArtifactWithoutOutputPaths(t *testing.T) {
	state := newSegmentedFileGenerationState()
	fileState := mustJSON(t, capability.FileResult{
		Path: "/workspace/generated/result.png", Binary: true, Kind: "image", ContentType: "image/png",
	})
	state.observe(tools.Call{APIName: "read_file"}, tools.ExecutionResult{State: fileState}, false)

	recorder := &recordingArtifactRecorder{}
	executionContext := tools.ExecutionContext{ArtifactRecorder: recorder}
	if err := state.publishReferencedFinalArtifacts(t.Context(), executionContext, "图片已保存到 `/workspace/generated/result.png`"); err != nil {
		t.Fatalf("publish referenced artifact: %v", err)
	}
	if len(recorder.calls) != 1 || recorder.calls[0].APIName != "referenced_file_generation" {
		t.Fatalf("expected referenced artifact publication, calls=%#v", recorder.calls)
	}
	if exports := recorder.results[0].ExportedFiles; len(exports) != 1 || exports[0].Path != "/workspace/generated/result.png" {
		t.Fatalf("unexpected referenced artifact export: %#v", exports)
	}

	if err := state.publishReferencedFinalArtifacts(t.Context(), executionContext, "再次引用 /workspace/generated/result.png"); err != nil {
		t.Fatalf("republish referenced artifact: %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("referenced artifact should only be published once, calls=%#v", recorder.calls)
	}
}

func TestReferencedArtifactPublicationIgnoresUploadsAndUnreferencedFiles(t *testing.T) {
	state := newSegmentedFileGenerationState()
	for _, result := range []capability.FileResult{
		{Path: "/workspace/uploads/art_input/input.png", Binary: true, Kind: "image"},
		{Path: "/workspace/generated/unreferenced.pdf", Binary: true, Kind: "document"},
		{Path: "/workspace/src/main.go", Kind: "text"},
	} {
		state.observe(tools.Call{APIName: "read_file"}, tools.ExecutionResult{State: mustJSON(t, result)}, false)
	}
	recorder := &recordingArtifactRecorder{}
	if err := state.publishReferencedFinalArtifacts(t.Context(), tools.ExecutionContext{ArtifactRecorder: recorder}, "分析了 `/workspace/uploads/art_input/input.png` 和 `/workspace/src/main.go`"); err != nil {
		t.Fatalf("publish ignored artifacts: %v", err)
	}
	if len(recorder.calls) != 0 {
		t.Fatalf("uploads, text files, and unreferenced files must not be published: %#v", recorder.calls)
	}
}

func TestPublishesReferencedWorkspaceImageWithoutPriorRead(t *testing.T) {
	state := newSegmentedFileGenerationState()
	provider := &referencedArtifactProvider{}
	recorder := &recordingArtifactRecorder{}
	path := "/workspace/sunwukong_c275f767522d4577b191313055b2d696_1784539299.png"
	response := "本地保存路径：`" + path + "`"
	if err := state.publishReferencedFinalArtifacts(t.Context(), tools.ExecutionContext{
		Provider: provider, ArtifactRecorder: recorder, SessionID: "sesn_image", TurnID: "turn_image",
	}, response); err != nil {
		t.Fatalf("publish referenced workspace image: %v", err)
	}
	if len(provider.reads) != 1 || provider.reads[0] != path {
		t.Fatalf("expected runtime verification read, got %#v", provider.reads)
	}
	if len(recorder.results) != 1 || len(recorder.results[0].ExportedFiles) != 1 || recorder.results[0].ExportedFiles[0].Path != path {
		t.Fatalf("expected referenced image export, results=%#v", recorder.results)
	}
}

func TestReferencedWorkspaceDeliverablePathsHandlePlainAndCodePaths(t *testing.T) {
	paths := referencedWorkspaceDeliverablePaths("结果 `/workspace/带 空格/成品.png`；备份 /workspace/report.pdf。忽略 /workspace/src/main.go 和 /workspace/uploads/art/input.png")
	want := []string{"/workspace/report.pdf", "/workspace/带 空格/成品.png"}
	if len(paths) != len(want) {
		t.Fatalf("unexpected referenced paths: %#v", paths)
	}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("unexpected referenced paths: got %#v want %#v", paths, want)
		}
	}
}

// Keep the concrete local provider behind a helper so test setup stays compact.
func capabilityLocalProvider() capability.LocalSystemProvider {
	return capability.LocalSystemProvider{}
}

type scriptedFileGenerationClient struct {
	responses []llm.Response
	requests  []llm.Request
}

type recordingArtifactRecorder struct {
	calls   []tools.Call
	results []tools.ExecutionResult
}

type referencedArtifactProvider struct {
	capability.UnavailableProvider
	reads []string
}

func (provider *referencedArtifactProvider) ReadFile(_ context.Context, request capability.ReadFileRequest) (capability.FileResult, error) {
	provider.reads = append(provider.reads, request.Path)
	return capability.FileResult{Path: request.Path, Binary: true, Kind: "image", ContentType: "image/png"}, nil
}

func (recorder *recordingArtifactRecorder) RecordToolArtifact(_ context.Context, call tools.Call, _ tools.ExecutionContext, result tools.ExecutionResult) ([]tools.ArtifactRef, error) {
	recorder.calls = append(recorder.calls, call)
	recorder.results = append(recorder.results, result)
	return nil, nil
}

func (client *scriptedFileGenerationClient) Generate(_ context.Context, request llm.Request) (llm.Response, error) {
	client.requests = append(client.requests, request)
	response := client.responses[0]
	client.responses = client.responses[1:]
	return response, nil
}

func (client *scriptedFileGenerationClient) Provider() string { return llm.ProviderFake }

func toolCallResponse(calls ...llm.ToolCall) llm.Response {
	return llm.Response{Message: llm.Message{Role: "assistant", ToolCalls: calls}}
}

func textResponse(text string) llm.Response {
	return llm.Response{Message: llm.Message{Role: "assistant", Content: []llm.ContentPart{{Type: "text", Text: text}}}}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
