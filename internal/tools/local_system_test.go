package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/capability"
)

func TestDefaultRegistryIncludesDefaultManifest(t *testing.T) {
	registry := DefaultRegistry()

	runtime, ok := registry.Get(DefaultIdentifier)
	if !ok {
		t.Fatalf("expected %s runtime", DefaultIdentifier)
	}
	manifest := runtime.Manifest()
	if manifest.Identifier != DefaultIdentifier || len(manifest.API) != 8 {
		t.Fatalf("unexpected default manifest: %#v", manifest)
	}

	webRuntime, ok := registry.Get(WebIdentifier)
	if !ok {
		t.Fatalf("expected %s runtime", WebIdentifier)
	}
	if manifest := webRuntime.Manifest(); manifest.Identifier != WebIdentifier || len(manifest.API) != 2 {
		t.Fatalf("unexpected web manifest: %#v", manifest)
	}

	agentRuntime, ok := registry.Get(AgentIdentifier)
	if !ok {
		t.Fatalf("expected %s runtime", AgentIdentifier)
	}
	if manifest := agentRuntime.Manifest(); manifest.Identifier != AgentIdentifier || len(manifest.API) != 27 {
		t.Fatalf("unexpected agent manifest: %#v", manifest)
	}

	interactionRuntime, ok := registry.Get(InteractionIdentifier)
	if !ok {
		t.Fatalf("expected %s runtime", InteractionIdentifier)
	}
	if manifest := interactionRuntime.Manifest(); manifest.Identifier != InteractionIdentifier || len(manifest.API) != 3 {
		t.Fatalf("unexpected interaction manifest: %#v", manifest)
	}

	skillsRuntime, ok := registry.Get(SkillsIdentifier)
	if !ok {
		t.Fatalf("expected %s runtime", SkillsIdentifier)
	}
	if manifest := skillsRuntime.Manifest(); manifest.Identifier != SkillsIdentifier || len(manifest.API) != 8 {
		t.Fatalf("unexpected skills manifest: %#v", manifest)
	}
}

func TestDefaultManifestRoutesFinalDeliverablesToWorkspace(t *testing.T) {
	manifest := (DefaultRuntime{}).Manifest()
	for _, expected := range []string{
		"final user deliverables",
		"under /workspace",
		"uploaded inputs are synchronized under /workspace/uploads",
		"Use /mnt/data only for caches, temporary files, and intermediate generation results",
	} {
		if !strings.Contains(manifest.SystemRole, expected) {
			t.Fatalf("default system role is missing deliverable path rule %q: %s", expected, manifest.SystemRole)
		}
	}

	_, writeAPI, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "write_file")
	if !ok {
		t.Fatal("expected write_file API")
	}
	if !strings.Contains(string(writeAPI.Parameters), "final user deliverables must be written under /workspace") {
		t.Fatalf("write_file path schema is missing deliverable routing guidance: %s", writeAPI.Parameters)
	}
}

func TestDefaultManifestDescribesBoundedReadWorkflow(t *testing.T) {
	manifest := (DefaultRuntime{}).Manifest()
	for _, expected := range []string{"next_offset_bytes", "file_revision", "search_files", "partial read"} {
		if !strings.Contains(manifest.SystemRole, expected) {
			t.Fatalf("default system role is missing %q", expected)
		}
	}
	_, readAPI, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "read_file")
	if !ok {
		t.Fatal("expected read_file API")
	}
	for _, field := range []string{"offset_bytes", "max_bytes", "start_line", "max_lines", "file_revision"} {
		if !strings.Contains(string(readAPI.Parameters), `"`+field+`"`) {
			t.Fatalf("read_file schema is missing %s: %s", field, readAPI.Parameters)
		}
	}
	_, searchAPI, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "search_file")
	if !ok || searchAPI.Risk != ToolRiskRead || searchAPI.Implementation != ToolImplementationWorkerCapability {
		t.Fatalf("unexpected search_file API: %#v", searchAPI)
	}
}

func TestDefaultManifestKeepsExecuteCodeAsHiddenCompatibilityAPI(t *testing.T) {
	_, api, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "execute_code")
	if !ok {
		t.Fatal("expected execute_code compatibility API")
	}
	if !api.HiddenFromModel || api.Implementation != ToolImplementationWorkerCapability {
		t.Fatalf("expected execute_code to remain executable but hidden from the model: %#v", api)
	}
}

func TestRunCommandSchemaDeclaresExecutionLimits(t *testing.T) {
	_, api, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "run_command")
	if !ok {
		t.Fatal("expected run_command API")
	}
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           map[string]struct {
			Minimum int `json:"minimum"`
			Maximum int `json:"maximum"`
			Default int `json:"default"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(api.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("run_command must reject unknown fields")
	}
	timeout := schema.Properties["timeout_ms"]
	output := schema.Properties["max_output_bytes"]
	if timeout.Minimum != 100 || timeout.Maximum != 600000 || timeout.Default != 120000 {
		t.Fatalf("unexpected timeout schema: %#v", timeout)
	}
	if output.Minimum != 1024 || output.Maximum != 1048576 || output.Default != 65536 {
		t.Fatalf("unexpected output limit schema: %#v", output)
	}
}

func TestCommandResultSurfacesTimeoutAndTruncationNotices(t *testing.T) {
	result, err := commandResult(Call{ID: "call_limits", Identifier: DefaultIdentifier, APIName: "run_command"}, capability.CommandResult{
		Status:              "timeout",
		ExitCode:            -1,
		Stdout:              "partial output",
		StdoutBytes:         4096,
		StdoutCapturedBytes: 1024,
		StdoutTruncated:     true,
		DurationMS:          250,
		TimedOut:            true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"partial output", "timed out after 250 ms", "stdout truncated after capturing 1024 of 4096 bytes"} {
		if !strings.Contains(result.Content, expected) {
			t.Fatalf("command result did not surface %q: %s", expected, result.Content)
		}
	}
	var state capability.CommandResult
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatal(err)
	}
	if !state.TimedOut || !state.StdoutTruncated || state.StdoutBytes != 4096 {
		t.Fatalf("structured command metadata was lost: %#v", state)
	}
}

func TestReadFileExecutionKeepsOnlyPageMetadataInState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", capability.DefaultReadFileDefaultMaxBytes*3)), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": path})
	result, err := NewDefaultExecutor().Execute(t.Context(), Call{
		ID: "call_read_page", Identifier: DefaultIdentifier, APIName: "read_file", Arguments: args,
	}, ExecutionContext{Provider: capability.LocalSystemProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != capability.DefaultReadFileDefaultMaxBytes || result.Error != nil {
		t.Fatalf("unexpected page result: %#v", result)
	}
	var state map[string]any
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatal(err)
	}
	if _, duplicated := state["content"]; duplicated {
		t.Fatalf("read page content was duplicated in state: %s", result.State)
	}
	if state["truncated"] != true || int(state["returned_bytes"].(float64)) != capability.DefaultReadFileDefaultMaxBytes {
		t.Fatalf("missing pagination metadata: %#v", state)
	}

	visible := ObservableResultData(result, ResultContextOptions{MaxContentChars: 100})
	visibleState := visible["state"].(map[string]any)
	if visibleState["truncated"] != true || visibleState["model_context_truncated"] != true {
		t.Fatalf("file truncation and context truncation were not distinguished: %#v", visibleState)
	}
	contextState := visible["context"].(map[string]any)
	if contextState["content_truncated"] != true || contextState["state_truncated"] != false {
		t.Fatalf("unexpected context metadata: %#v", contextState)
	}
}

func TestReadFileExecutionReturnsStructuredRecoverableError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "range.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": path, "offset_bytes": 0, "start_line": 1})
	result, err := NewDefaultExecutor().Execute(t.Context(), Call{
		ID: "call_bad_range", Identifier: DefaultIdentifier, APIName: "read_file", Arguments: args,
	}, ExecutionContext{Provider: capability.LocalSystemProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == nil || result.Error.Type != "invalid_read_range" {
		t.Fatalf("expected structured invalid_read_range: %#v", result)
	}
	var state struct {
		Error capability.FileReadError `json:"error"`
	}
	if err := json.Unmarshal(result.State, &state); err != nil || state.Error.Code != "invalid_read_range" {
		t.Fatalf("unexpected structured state %s: %v", result.State, err)
	}
}

func TestSearchFileExecutionReturnsFocusedLocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.txt")
	if err := os.WriteFile(path, []byte("alpha\nneedle here\nomega\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": path, "query": "needle"})
	result, err := NewDefaultExecutor().Execute(t.Context(), Call{
		ID: "call_search", Identifier: DefaultIdentifier, APIName: "search_file", Arguments: args,
	}, ExecutionContext{Provider: capability.LocalSystemProvider{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != nil || !strings.Contains(result.Content, "2 [byte 6]") {
		t.Fatalf("unexpected search result: %#v", result)
	}
	var state capability.SearchFileResult
	if err := json.Unmarshal(result.State, &state); err != nil || len(state.Matches) != 1 || state.Matches[0].OffsetBytes != 6 {
		t.Fatalf("unexpected search state %s: %v", result.State, err)
	}
}

func TestRegistryModelContextIncludesManifestAndCallFormat(t *testing.T) {
	context := DefaultRegistry().ModelContext()
	if len(context) == 0 {
		t.Fatal("expected model context")
	}

	var decoded struct {
		ProtocolVersion string `json:"protocol_version"`
		ToolCallFormat  struct {
			ProtocolVersion string `json:"protocol_version"`
		} `json:"tool_call_format"`
		Tools []Manifest `json:"tools"`
	}
	if err := json.Unmarshal(context, &decoded); err != nil {
		t.Fatalf("decode model context: %v", err)
	}
	if decoded.ProtocolVersion != ManifestProtocolVersion {
		t.Fatalf("unexpected protocol version: %#v", decoded)
	}
	if decoded.ToolCallFormat.ProtocolVersion != ToolCallProtocolVersion {
		t.Fatalf("unexpected tool call format: %#v", decoded.ToolCallFormat)
	}
	identifiers := map[string]bool{}
	for _, manifest := range decoded.Tools {
		identifiers[manifest.Identifier] = true
	}
	if len(decoded.Tools) != 6 || !identifiers[DefaultIdentifier] || !identifiers[WebIdentifier] || !identifiers[AgentIdentifier] || !identifiers[InteractionIdentifier] || !identifiers[TaskIdentifier] || !identifiers[SkillsIdentifier] || identifiers[NamespaceBrowser] {
		t.Fatalf("unexpected tools: %#v", decoded.Tools)
	}
}

func TestRegistryModelToolsUsesQualifiedFunctionNames(t *testing.T) {
	modelTools := DefaultRegistry().ModelTools()
	if len(modelTools) != 51 {
		t.Fatalf("expected default APIs as model tools, got %#v", modelTools)
	}

	names := make(map[string]bool)
	for _, modelTool := range modelTools {
		names[modelTool.Function.Name] = true
		if modelTool.Type != "function" {
			t.Fatalf("expected function tool, got %#v", modelTool)
		}
		if len(modelTool.Function.Parameters) == 0 {
			t.Fatalf("expected parameters for %s", modelTool.Function.Name)
		}
	}
	if !names[DefaultIdentifier+".run_command"] || names[DefaultIdentifier+".execute_code"] || !names[DefaultIdentifier+".find_files"] || !names[DefaultIdentifier+".search_files"] || names[DefaultIdentifier+".search_file"] || !names[DefaultIdentifier+".edit_file"] || !names[WebIdentifier+".search"] || !names[WebIdentifier+".crawl"] || names[NamespaceBrowser+".open"] || !names[AgentIdentifier+".spawn"] || !names[AgentIdentifier+".wait"] || !names[AgentIdentifier+".collect_result"] || !names[AgentIdentifier+".stream_events"] || !names[AgentIdentifier+".approve_tool"] || !names[AgentIdentifier+".reject_tool"] || !names[AgentIdentifier+".cancel_start"] || !names[AgentIdentifier+".run_group"] || !names[AgentIdentifier+".list_group_templates"] || !names[AgentIdentifier+".get_group"] || !names[AgentIdentifier+".wait_group"] || !names[AgentIdentifier+".collect_group"] || !names[AgentIdentifier+".cancel_group"] || !names[AgentIdentifier+".retry_group_item"] || !names[AgentIdentifier+".retry_group"] || !names[InteractionIdentifier+".ask_user"] || !names[InteractionIdentifier+".request_upload"] || !names[InteractionIdentifier+".request_plan_approval"] || !names[SkillsIdentifier+".search"] || !names[SkillsIdentifier+".inspect"] || !names[SkillsIdentifier+".discover"] || !names[SkillsIdentifier+".preview"] || !names[SkillsIdentifier+".read_asset"] || !names[SkillsIdentifier+".install"] || !names[SkillsIdentifier+".enable"] || !names[SkillsIdentifier+".disable"] {
		t.Fatalf("missing expected qualified names: %#v", names)
	}
}

func TestFileMutationModelToolsDeclareProactiveContentLimits(t *testing.T) {
	modelTools := DefaultRegistry().ModelTools()
	wanted := map[string]string{
		DefaultIdentifier + ".write_file": "content",
		DefaultIdentifier + ".edit_file":  "new_string",
	}
	for _, modelTool := range modelTools {
		field, ok := wanted[modelTool.Function.Name]
		if !ok {
			continue
		}
		var schema struct {
			Properties map[string]struct {
				MaxLength int `json:"maxLength"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(modelTool.Function.Parameters, &schema); err != nil {
			t.Fatalf("decode %s schema: %v", modelTool.Function.Name, err)
		}
		if schema.Properties[field].MaxLength != 8000 {
			t.Fatalf("expected %s.%s maxLength=8000, got %#v", modelTool.Function.Name, field, schema.Properties[field])
		}
		delete(wanted, modelTool.Function.Name)
	}
	if len(wanted) != 0 {
		t.Fatalf("missing file mutation tool schemas: %#v", wanted)
	}
}

func TestParseInterventionModeDefaultsAndNormalizes(t *testing.T) {
	if got := ParseInterventionMode(nil); got != InterventionModeRequestApproval {
		t.Fatalf("expected default intervention mode, got %q", got)
	}
	if got := ParseInterventionMode(json.RawMessage(`{"intervention_mode":"APPROVE_FOR_ME"}`)); got != InterventionModeApproveForMe {
		t.Fatalf("expected normalized approve_for_me, got %q", got)
	}
	if got := ParseInterventionMode(json.RawMessage(`{"intervention_mode":"wat"}`)); got != InterventionModeRequestApproval {
		t.Fatalf("expected invalid value to fall back to default, got %q", got)
	}
}

func TestInterventionPolicyAddsNetworkApprovalLayer(t *testing.T) {
	manifest, api, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "run_command")
	if !ok {
		t.Fatal("expected run_command api")
	}
	call := Call{
		APIName:   "default.run_command",
		Arguments: json.RawMessage(`{"command":"python3","args":["download.py"]}`),
	}
	context := ExecutionContext{Provider: capability.OnlyboxesProvider{}}

	manual := InterventionPolicy{Mode: InterventionModeRequestApproval}.EvaluateCall(manifest, api, call, context)
	if manual.Allowed || !manual.Required || manual.Reason != InterventionReasonNetworkAccess {
		t.Fatalf("expected network access to require approval, got %#v", manual)
	}

	auto := InterventionPolicy{Mode: InterventionModeApproveForMe}.EvaluateCall(manifest, api, call, context)
	if !auto.Allowed || !auto.Required || auto.Reason != InterventionReasonNetworkAccess {
		t.Fatalf("expected approve_for_me to auto-approve network access, got %#v", auto)
	}

	fullAccess := InterventionPolicy{Mode: InterventionModeFullAccess}.EvaluateCall(manifest, api, call, context)
	if !fullAccess.Allowed || fullAccess.Required || fullAccess.Reason != "" {
		t.Fatalf("expected full_access to skip network intervention, got %#v", fullAccess)
	}

	codeCall := Call{
		APIName:   "default.execute_code",
		Arguments: json.RawMessage(`{"language":"python3","code":"import urllib.request; urllib.request.urlopen('https://example.com')"}`),
	}
	codeManifest, codeAPI, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "execute_code")
	if !ok {
		t.Fatal("expected execute_code api")
	}
	codeDecision := InterventionPolicy{Mode: InterventionModeRequestApproval}.EvaluateCall(codeManifest, codeAPI, codeCall, context)
	if codeDecision.Allowed || !codeDecision.Required || codeDecision.Reason != InterventionReasonNetworkAccess {
		t.Fatalf("expected execute_code network-capable sandbox to require approval, got %#v", codeDecision)
	}

	offline := InterventionPolicy{Mode: InterventionModeRequestApproval}.EvaluateCall(manifest, api, call, ExecutionContext{
		Provider: capability.OnlyboxesProvider{DisableNetwork: true},
	})
	if offline.Reason == InterventionReasonNetworkAccess {
		t.Fatalf("expected disabled network sandbox not to use network reason, got %#v", offline)
	}
}

func TestRegistryGetAPIReturnsManifestMetadata(t *testing.T) {
	manifest, api, ok := DefaultRegistry().GetAPI(DefaultIdentifier, "edit_file")
	if !ok {
		t.Fatal("expected edit_file api")
	}
	if manifest.Identifier != DefaultIdentifier || api.Name != "edit_file" || api.HumanIntervention != "optional" {
		t.Fatalf("unexpected api lookup result: manifest=%#v api=%#v", manifest, api)
	}
	if api.Namespace != NamespaceDefault || api.APIName != "edit_file" || !containsString(api.Capabilities, CapabilityFilesystemWrite) {
		t.Fatalf("expected standard metadata on edit_file api, got %#v", api)
	}
	if api.Risk != ToolRiskWrite || api.Runtime == nil || api.Runtime.Preferred != ToolRuntimeCloudSandbox {
		t.Fatalf("expected risk/runtime metadata on edit_file api, got %#v", api)
	}
}

func TestRegistryConfiguredFiltersEnabledToolAPIs(t *testing.T) {
	registry, policy := DefaultRegistry().Configured(json.RawMessage(`{
		"tools": ["default.read_file", "default.edit_file"],
		"runtime": "local_system"
	}`))

	if !policy.Explicit || policy.Runtime != ToolRuntimeLocalSystem {
		t.Fatalf("unexpected policy: %#v", policy)
	}
	modelTools := registry.ModelTools()
	if len(modelTools) != 2 {
		t.Fatalf("expected 2 configured model tools, got %#v", modelTools)
	}
	names := map[string]bool{}
	for _, modelTool := range modelTools {
		names[modelTool.Function.Name] = true
	}
	if !names[DefaultIdentifier+".read_file"] || !names[DefaultIdentifier+".edit_file"] || names[DefaultIdentifier+".run_command"] {
		t.Fatalf("unexpected configured tool names: %#v", names)
	}
}

func TestRegistryCompatibilityAPIsAreHiddenUnlessExplicitlyConfigured(t *testing.T) {
	names := map[string]bool{}
	for _, modelTool := range DefaultRegistry().ModelTools() {
		names[modelTool.Function.Name] = true
	}
	if names[DefaultIdentifier+".search_file"] || names[DefaultIdentifier+".execute_code"] || !names[DefaultIdentifier+".run_command"] || !names[DefaultIdentifier+".find_files"] || !names[DefaultIdentifier+".search_files"] {
		t.Fatalf("unexpected default filesystem tools: %#v", names)
	}
	for _, apiName := range []string{"search_file", "execute_code"} {
		registry, _ := DefaultRegistry().Configured(json.RawMessage(`{"tools":["default.` + apiName + `"]}`))
		modelTools := registry.ModelTools()
		if len(modelTools) != 1 || modelTools[0].Function.Name != DefaultIdentifier+"."+apiName {
			t.Fatalf("explicit %s configuration was not preserved: %#v", apiName, modelTools)
		}
	}
}

func TestRegistryAvailableFiltersByCapabilities(t *testing.T) {
	registry := DefaultRegistry().Available(AvailableCapabilities{
		Runtime:      ToolRuntimeLocalSystem,
		Capabilities: []string{CapabilityFilesystemRead},
	})

	modelTools := registry.ModelTools()
	names := map[string]bool{}
	for _, modelTool := range modelTools {
		names[modelTool.Function.Name] = true
	}
	if len(modelTools) != 21 || !names[DefaultIdentifier+".read_file"] || !names[DefaultIdentifier+".find_files"] || !names[DefaultIdentifier+".search_files"] || names[DefaultIdentifier+".search_file"] || !names[WebIdentifier+".search"] || !names[WebIdentifier+".crawl"] || !names[InteractionIdentifier+".ask_user"] || !names[InteractionIdentifier+".request_upload"] || !names[InteractionIdentifier+".request_plan_approval"] || !names[TaskIdentifier+".create_plan"] || !names[TaskIdentifier+".update_items"] || !names[TaskIdentifier+".get_plan"] || !names[TaskIdentifier+".complete_plan"] || !names[TaskIdentifier+".cancel_plan"] || !names[SkillsIdentifier+".search"] || !names[SkillsIdentifier+".inspect"] || !names[SkillsIdentifier+".discover"] || !names[SkillsIdentifier+".preview"] || !names[SkillsIdentifier+".read_asset"] || !names[SkillsIdentifier+".install"] || !names[SkillsIdentifier+".enable"] || !names[SkillsIdentifier+".disable"] {
		t.Fatalf("expected read_file plus server builtin interaction, task, web, and skills tools, got %#v", modelTools)
	}
	if _, _, ok := registry.GetAPI(DefaultIdentifier, "run_command"); ok {
		t.Fatal("expected run_command to be unavailable without exec capability")
	}
}

func TestRegistryAvailableKeepsRuntimeAllowedTools(t *testing.T) {
	registry := DefaultRegistry().Available(AvailableCapabilities{
		Runtime: ToolRuntimeLocalSystem,
		Capabilities: []string{
			CapabilityFilesystemRead,
			CapabilityFilesystemWrite,
			CapabilityExec,
			CapabilityCodeExecute,
		},
	})

	modelTools := registry.ModelTools()
	if len(modelTools) != 24 {
		t.Fatalf("expected all default tools to be available for local_system provider, got %#v", modelTools)
	}
	names := map[string]bool{}
	for _, modelTool := range modelTools {
		names[modelTool.Function.Name] = true
	}
	if !names[DefaultIdentifier+".run_command"] || names[DefaultIdentifier+".execute_code"] {
		t.Fatalf("expected run_command visible and execute_code hidden by default, got %#v", modelTools)
	}
	for _, name := range []string{"ask_user", "request_upload", "request_plan_approval"} {
		if !names[InteractionIdentifier+"."+name] {
			t.Fatalf("expected server builtin interaction.%s to remain available, got %#v", name, modelTools)
		}
	}
	for _, name := range []string{"search", "inspect", "discover", "preview", "read_asset", "install", "enable", "disable"} {
		if !names[SkillsIdentifier+"."+name] {
			t.Fatalf("expected server builtin skills.%s to remain available, got %#v", name, modelTools)
		}
	}
}

func TestRegistryExecutorRunsDefaultCommand(t *testing.T) {
	executor := NewDefaultExecutor()

	result, err := executor.Execute(context.Background(), Call{
		ID:   "call_1",
		Name: "run_command",
		Arguments: json.RawMessage(`{
			"command": "sh",
			"args": ["-c", "printf tool-output"]
		}`),
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if result.Identifier != DefaultIdentifier || result.APIName != "run_command" || result.Content != "tool-output" {
		t.Fatalf("unexpected result: %#v", result)
	}

	var state capability.CommandResult
	if err := json.Unmarshal(result.State, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.Stdout != "tool-output" || state.ExitCode != 0 {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestRegistryExecutorInjectsManagedEnvironmentAndRedactsOutput(t *testing.T) {
	executor := NewDefaultExecutor()
	result, err := executor.Execute(context.Background(), Call{
		ID: "call_env", Name: "run_command",
		Arguments: json.RawMessage(`{"command":"sh","args":["-c","printf %s \"$SERVICE_API_KEY\""],"env":{"SERVICE_API_KEY":"model-value"}}`),
	}, ExecutionContext{
		SessionID: "sesn_000001", TurnID: "turn_000001", Provider: capability.LocalSystemProvider{},
		Environment: map[string]string{"SERVICE_API_KEY": "managed-secret-value"},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if strings.Contains(result.Content, "managed-secret-value") || result.Content != "[REDACTED_ENV:SERVICE_API_KEY]" {
		t.Fatalf("expected managed value to win and be redacted, got %#v", result)
	}
	if strings.Contains(string(result.State), "managed-secret-value") {
		t.Fatalf("state exposed managed value: %s", result.State)
	}
}

func TestRegistryExecutorKeepsRuntimeSkillPathsVisible(t *testing.T) {
	result, err := NewDefaultExecutor().Execute(context.Background(), Call{
		ID: "call_skill_env", Name: "run_command",
		Arguments: json.RawMessage(`{"command":"sh","args":["-c","printf '%s|%s' \"$CLAUDE_SKILL_DIR\" \"$SERVICE_API_KEY\""]}`),
	}, ExecutionContext{
		SessionID: "sesn_000001", TurnID: "turn_000001", Provider: capability.LocalSystemProvider{},
		Environment: map[string]string{
			"CLAUDE_SKILL_DIR": "/tma/skills/skl_web_access/2",
			"SERVICE_API_KEY":  "managed-secret-value",
		},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if result.Content != "/tma/skills/skl_web_access/2|[REDACTED_ENV:SERVICE_API_KEY]" {
		t.Fatalf("expected public skill path and redacted secret, got %q", result.Content)
	}
}

func TestRegistryExecutorCapturesRequestedOutputPaths(t *testing.T) {
	executor := NewDefaultExecutor()
	workDir := t.TempDir()
	withoutExport, err := json.Marshal(map[string]any{
		"command":  "sh",
		"args":     []string{"-c", "printf artifact-file > result.txt && printf ok"},
		"work_dir": workDir,
	})
	if err != nil {
		t.Fatalf("marshal no-export arguments: %v", err)
	}

	result, err := executor.Execute(context.Background(), Call{
		ID:        "call_1",
		Name:      "run_command",
		Arguments: withoutExport,
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if len(result.ExportedFiles) != 0 {
		t.Fatalf("expected no exported files when output_paths is omitted, got %#v", result.ExportedFiles)
	}

	arguments, err := json.Marshal(map[string]any{
		"command":      "sh",
		"args":         []string{"-c", "printf artifact-file > result.txt && printf ok"},
		"work_dir":     workDir,
		"output_paths": []string{"result.txt"},
	})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	result, err = executor.Execute(context.Background(), Call{
		ID:        "call_2",
		Name:      "run_command",
		Arguments: arguments,
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		t.Fatalf("execute tool with output_paths: %v", err)
	}
	if len(result.ExportedFiles) != 1 || result.ExportedFiles[0].Path != "result.txt" || result.ExportedFiles[0].WorkDir != workDir {
		t.Fatalf("unexpected exported files: %#v", result.ExportedFiles)
	}
}

func TestCloudSandboxRejectsTemporaryDeliverableOutputPaths(t *testing.T) {
	executor := NewDefaultExecutor()
	for name, arguments := range map[string]map[string]any{
		"absolute": {
			"command": "sh", "output_paths": []string{"/mnt/data/report.csv"},
		},
		"unsupported_root": {
			"command": "sh", "output_paths": []string{"/tmp/report.csv"},
		},
		"relative_to_data": {
			"command": "sh", "work_dir": "/mnt/data/build", "output_paths": []string{"report.csv"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(arguments)
			if err != nil {
				t.Fatalf("marshal arguments: %v", err)
			}
			result, err := executor.Execute(context.Background(), Call{
				ID: "call_temp_output", Name: "run_command", Arguments: encoded,
			}, ExecutionContext{Provider: capability.OnlyboxesProvider{}})
			if err != nil {
				t.Fatalf("execute tool: %v", err)
			}
			if result.Error == nil || result.Error.Type != "invalid_output_path" || !strings.Contains(result.Content, "/workspace") {
				t.Fatalf("expected temporary output path rejection, got %#v", result)
			}
		})
	}
}

func TestNormalizeCallSplitsDefaultFunctionName(t *testing.T) {
	call := NormalizeCall(Call{
		APIName: "default.run_command",
	})

	if call.Identifier != DefaultIdentifier || call.APIName != "run_command" {
		t.Fatalf("unexpected normalized call: %#v", call)
	}
}

func TestRegistryExecutorRunsDefaultEditFile(t *testing.T) {
	executor := NewDefaultExecutor()
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	arguments, err := json.Marshal(map[string]any{
		"file_path":  path,
		"old_string": "world",
		"new_string": "gopher",
	})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}

	result, err := executor.Execute(context.Background(), Call{
		ID:        "call_edit",
		Name:      "edit_file",
		Arguments: arguments,
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error result: %#v", result.Error)
	}
	if result.APIName != "edit_file" || !strings.Contains(result.Content, "1 replacement") {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.ExportedFiles) != 1 || result.ExportedFiles[0].Path != path || result.ExportedFiles[0].Name != "note.txt" {
		t.Fatalf("expected edited file to be exported, got %#v", result.ExportedFiles)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "hello gopher" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestRegistryExecutorRunsDefaultWriteFileWithPlainTextContent(t *testing.T) {
	executor := NewDefaultExecutor()
	path := filepath.Join(t.TempDir(), "script.sh")

	arguments, err := json.Marshal(map[string]any{
		"path":    path,
		"content": "#!/bin/sh\necho hello from tma\n",
	})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}

	result, err := executor.Execute(context.Background(), Call{
		ID:        "call_write",
		Name:      "write_file",
		Arguments: arguments,
	}, ExecutionContext{
		SessionID: "sesn_000001",
		TurnID:    "turn_000001",
		Provider:  capability.LocalSystemProvider{},
	})
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("unexpected error result: %#v", result.Error)
	}
	if result.APIName != "write_file" || !strings.Contains(result.Content, path) {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.ExportedFiles) != 1 || result.ExportedFiles[0].Path != path || result.ExportedFiles[0].Name != "script.sh" {
		t.Fatalf("expected written file to be exported, got %#v", result.ExportedFiles)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "#!/bin/sh\necho hello from tma\n" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestRegistryExecutorReturnsStableUnsupportedToolError(t *testing.T) {
	executor := NewDefaultExecutor()

	result, err := executor.Execute(context.Background(), Call{
		ID:         "call_2",
		Identifier: "tma.missing",
		APIName:    "run_command",
	}, ExecutionContext{})
	if err != nil {
		t.Fatalf("expected stable tool result, got error: %v", err)
	}
	if result.Error == nil || result.Error.Type != "unsupported_tool" {
		t.Fatalf("expected unsupported tool error result, got %#v", result)
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
