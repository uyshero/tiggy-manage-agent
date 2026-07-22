package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/llm"
)

const (
	ExecutorServer               = "server"
	ManifestProtocolVersion      = "tma.tools.manifest.v1"
	ToolCallProtocolVersion      = "tma.tool_call.v1"
	ToolResultProtocolVersion    = "tma.tool_result.v1"
	MaxTransportedArtifactBytes  = 8 << 20
	DefaultResultContextMaxChars = 12000
	DefaultResultStateMaxBytes   = 12000

	CapabilityFilesystemRead  = "filesystem.read"
	CapabilityFilesystemWrite = "filesystem.write"
	CapabilityProcessExec     = CapabilityExec
	CapabilityCodeExec        = CapabilityCodeExecute
)

type Manifest struct {
	Identifier     string         `json:"identifier"`
	Type           string         `json:"type,omitempty"`
	Meta           Meta           `json:"meta"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	SystemRole     string         `json:"system_role"`
	API            []API          `json:"api"`
	Executors      []string       `json:"executors,omitempty"`
	ApprovalPolicy string         `json:"approval_policy,omitempty"`
	ApprovalReason string         `json:"approval_reason,omitempty"`
}

type Meta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type API struct {
	Name             string          `json:"name"`
	Namespace        string          `json:"namespace,omitempty"`
	APIName          string          `json:"api,omitempty"`
	Description      string          `json:"description"`
	Parameters       json.RawMessage `json:"parameters,omitempty"`
	ApprovalPolicy   string          `json:"approval_policy,omitempty"`
	ApprovalReason   string          `json:"approval_reason,omitempty"`
	Capabilities     []string        `json:"capabilities,omitempty"`
	Risk             string          `json:"risk,omitempty"`
	Idempotency      string          `json:"idempotency,omitempty"`
	ConcurrencyClass string          `json:"concurrency_class,omitempty"`
	LockKey          string          `json:"lock_key,omitempty"`
	Runtime          *RuntimePolicy  `json:"runtime,omitempty"`
	Implementation   string          `json:"implementation,omitempty"`
	HiddenFromModel  bool            `json:"hidden_from_model,omitempty"`
}

type Call struct {
	ID         string          `json:"id,omitempty"`
	Identifier string          `json:"identifier,omitempty"`
	APIName    string          `json:"api_name,omitempty"`
	Name       string          `json:"name,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
}

type ExecutionContext struct {
	WorkspaceID               string
	SessionID                 string
	EnvironmentID             string
	TurnID                    string
	IdempotencyKey            string
	Environment               map[string]string
	Deadline                  *time.Time
	Provider                  capability.Provider
	ArtifactRecorder          ArtifactRecorder
	DeferArtifacts            bool
	ExpectedFileRevision      string
	ExpectedFileContentSHA256 string
	TaskService               TaskToolService
	CapabilityTransport       bool
}

func mergeManagedEnvironment(request map[string]string, managed map[string]string) map[string]string {
	if len(managed) == 0 {
		return request
	}
	merged := make(map[string]string, len(request)+len(managed))
	for key, value := range request {
		merged[key] = value
	}
	// Managed values win so model-generated arguments cannot replace credentials.
	for key, value := range managed {
		merged[key] = value
	}
	return merged
}

type ArtifactRecorder interface {
	RecordToolArtifact(ctx context.Context, call Call, executionContext ExecutionContext, result ExecutionResult) ([]ArtifactRef, error)
}

type ExecutionResult struct {
	ID                  string           `json:"id,omitempty"`
	Identifier          string           `json:"identifier"`
	APIName             string           `json:"api_name"`
	Content             string           `json:"content"`
	State               json.RawMessage  `json:"state,omitempty"`
	ExportedFiles       []ArtifactExport `json:"exported_files,omitempty"`
	Artifacts           []ArtifactRef    `json:"artifacts,omitempty"`
	ArtifactError       string           `json:"artifact_error,omitempty"`
	PendingIntervention bool             `json:"pending_intervention,omitempty"`
	Error               *ExecutionError  `json:"error,omitempty"`
}

type ArtifactExport struct {
	Path          string `json:"path"`
	WorkDir       string `json:"work_dir,omitempty"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	ArtifactType  string `json:"artifact_type,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Content       []byte `json:"-"`
}

type ArtifactRef struct {
	ArtifactID   string `json:"artifact_id"`
	ObjectRefID  string `json:"object_ref_id"`
	Name         string `json:"name"`
	ArtifactType string `json:"artifact_type"`
	DownloadPath string `json:"download_path"`
}

type ExecutionError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type ResultContextOptions struct {
	MaxContentChars int
	MaxStateBytes   int
}

type Runtime interface {
	Manifest() Manifest
	Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error)
}

type snapshotRuntime struct {
	manifest Manifest
	inner    Runtime
}

func (r snapshotRuntime) Manifest() Manifest {
	manifest, err := cloneManifest(r.manifest)
	if err != nil {
		panic(fmt.Sprintf("clone validated tool manifest: %v", err))
	}
	return manifest
}

func (r snapshotRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	return r.inner.Execute(ctx, call, executionContext)
}

func cloneManifest(manifest Manifest) (Manifest, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return Manifest{}, err
	}
	var cloned Manifest
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return Manifest{}, err
	}
	return cloned, nil
}

type Executor interface {
	Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error)
}

type filteredRuntime struct {
	inner        Runtime
	allowedAPIs  map[string]bool
	exposeHidden bool
}

func (r filteredRuntime) Manifest() Manifest {
	manifest := r.inner.Manifest()
	if len(r.allowedAPIs) == 0 {
		manifest.API = nil
		return manifest
	}
	apis := make([]API, 0, len(manifest.API))
	for _, api := range manifest.API {
		if r.allowedAPIs[api.Name] {
			if r.exposeHidden {
				api.HiddenFromModel = false
			}
			apis = append(apis, api)
		}
	}
	manifest.API = apis
	return manifest
}

func (r filteredRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	if len(r.allowedAPIs) > 0 && !r.allowedAPIs[call.APIName] {
		return failedResult(call, "disabled_tool_api", fmt.Sprintf("tool api %q is disabled", call.APIName)), nil
	}
	return r.inner.Execute(ctx, call, executionContext)
}

type Registry struct {
	runtimes map[string]Runtime
}

type ConfigPolicy struct {
	Explicit            bool
	Runtime             string
	EnabledToolPatterns []string
}

type AvailableCapabilities struct {
	Runtime      string
	Namespaces   []string
	APIs         []string
	Capabilities []string
}

type WorkerCapabilities struct {
	Namespaces   []string       `json:"namespaces"`
	APIs         []string       `json:"apis"`
	Runtimes     []string       `json:"runtimes"`
	Capabilities []string       `json:"capabilities"`
	Manifests    []Manifest     `json:"manifests,omitempty"`
	Constraints  map[string]any `json:"constraints,omitempty"`
}

func DecodeWorkerCapabilities(raw json.RawMessage) (WorkerCapabilities, error) {
	var capabilities WorkerCapabilities
	if len(raw) == 0 {
		return capabilities, fmt.Errorf("worker capabilities are empty")
	}
	if err := json.Unmarshal(raw, &capabilities); err != nil {
		return capabilities, err
	}
	return capabilities, nil
}

func NewRegistry(runtimes ...Runtime) Registry {
	registry := Registry{runtimes: make(map[string]Runtime)}
	for _, runtime := range runtimes {
		registry.Register(runtime)
	}
	return registry
}

func DefaultRegistry() Registry {
	return NewRegistry(DefaultRuntime{}, WebRuntime{}, AgentRuntime{}, InteractionRuntime{}, TaskRuntime{}, SkillsRuntime{})
}

func (r Registry) Register(runtime Runtime) {
	if err := r.RegisterChecked(runtime); err != nil {
		panic(err)
	}
}

func (r Registry) RegisterChecked(runtime Runtime) error {
	if runtime == nil {
		return nil
	}
	manifest := runtime.Manifest()
	if manifest.Identifier == "" {
		return nil
	}
	if err := ValidateManifestPermissions(manifest); err != nil {
		return NewToolContractError(
			"invalid_tool_registry", fmt.Errorf("invalid_tool_registry: invalid tool manifest %q: %w", manifest.Identifier, err),
		)
	}
	r.runtimes[manifest.Identifier] = runtime
	return nil
}

func (r Registry) Configured(raw json.RawMessage) (Registry, ConfigPolicy) {
	policy := ParseConfigPolicy(raw)
	if !policy.Explicit {
		return r, policy
	}
	configured := Registry{runtimes: make(map[string]Runtime)}
	if len(policy.EnabledToolPatterns) == 0 {
		return configured, policy
	}
	enabledTools := map[string]bool{}
	enabledAPIs := map[string]map[string]bool{}
	for _, pattern := range policy.EnabledToolPatterns {
		if _, ok := r.Get(pattern); ok {
			enabledTools[pattern] = true
			continue
		}
		identifier, apiName := splitFunctionName(pattern)
		if identifier == "" || apiName == "" {
			continue
		}
		if _, ok := r.Get(identifier); !ok {
			continue
		}
		if enabledAPIs[identifier] == nil {
			enabledAPIs[identifier] = map[string]bool{}
		}
		enabledAPIs[identifier][apiName] = true
	}
	for identifier := range enabledTools {
		runtime, ok := r.Get(identifier)
		if ok {
			configured.Register(runtime)
		}
	}
	for identifier, allowedAPIs := range enabledAPIs {
		if enabledTools[identifier] {
			continue
		}
		runtime, ok := r.Get(identifier)
		if !ok {
			continue
		}
		configured.Register(filteredRuntime{
			inner:        runtime,
			allowedAPIs:  allowedAPIs,
			exposeHidden: true,
		})
	}
	return configured, policy
}

func (r Registry) Available(available AvailableCapabilities) Registry {
	available.Runtime, _ = NormalizeToolRuntime(available.Runtime)
	availableNamespaces := stringSet(available.Namespaces)
	availableAPIs := stringSet(available.APIs)
	availableSet := stringSet(available.Capabilities)
	return r.FilterAPIs(func(manifest Manifest, api API) bool {
		namespace := fallbackString(api.Namespace, manifest.Identifier)
		apiName := fallbackString(api.APIName, api.Name)
		if len(availableNamespaces) > 0 && !availableNamespaces[namespace] {
			return false
		}
		if len(availableAPIs) > 0 && !availableAPIs[namespace+"."+apiName] {
			return false
		}
		if !apiRuntimeAvailable(api, available.Runtime) {
			return false
		}
		return capabilitiesAvailable(api.Capabilities, availableSet)
	})
}

func (r Registry) FilterAPIs(allow func(Manifest, API) bool) Registry {
	filtered := Registry{runtimes: make(map[string]Runtime)}
	for identifier, runtime := range r.runtimes {
		manifest := runtime.Manifest()
		allowedAPIs := map[string]bool{}
		for _, api := range manifest.API {
			if allow != nil && !allow(manifest, api) {
				continue
			}
			allowedAPIs[api.Name] = true
		}
		if len(allowedAPIs) > 0 {
			filtered.runtimes[identifier] = filteredRuntime{inner: runtime, allowedAPIs: allowedAPIs}
		}
	}
	return filtered
}

func WorkInvocationFromAPI(manifest Manifest, api API, runtime string, input json.RawMessage) WorkInvocation {
	return WorkInvocation{
		ProtocolVersion: WorkProtocolVersion,
		Namespace:       fallbackString(api.Namespace, manifest.Identifier),
		API:             fallbackString(api.APIName, api.Name),
		Capabilities:    append([]string(nil), api.Capabilities...),
		Risk:            api.Risk,
		Runtime:         runtime,
		Input:           append(json.RawMessage(nil), input...),
	}
}

func (r Registry) WorkInvocation(identifier string, apiName string, runtime string, input json.RawMessage) (WorkInvocation, bool) {
	manifest, api, ok := r.GetAPI(identifier, apiName)
	if !ok {
		return WorkInvocation{}, false
	}
	return WorkInvocationFromAPI(manifest, api, runtime, input), true
}

func ParseConfigPolicy(raw json.RawMessage) ConfigPolicy {
	if len(raw) == 0 || string(raw) == "null" {
		return ConfigPolicy{}
	}
	policy := ConfigPolicy{Explicit: true}
	var enabled []string
	if err := json.Unmarshal(raw, &enabled); err == nil {
		policy.EnabledToolPatterns = cleanStringList(enabled)
		return policy
	}
	var object struct {
		EnabledTools []string `json:"enabled_tools"`
		Tools        []string `json:"tools"`
		Runtime      string   `json:"runtime"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return ConfigPolicy{}
	}
	policy.EnabledToolPatterns = cleanStringList(object.EnabledTools)
	if len(policy.EnabledToolPatterns) == 0 {
		policy.EnabledToolPatterns = cleanStringList(object.Tools)
	}
	if runtime, ok := NormalizeToolRuntime(object.Runtime); ok && strings.TrimSpace(object.Runtime) != "" {
		policy.Runtime = runtime
	}
	return policy
}

func cleanStringList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func apiRuntimeAvailable(api API, runtime string) bool {
	if runtime == "" || runtime == ToolRuntimeAuto {
		return true
	}
	policy := NormalizeRuntimePolicy(api.Runtime)
	for _, allowed := range policy.Allowed {
		if allowed == ToolRuntimeAuto || allowed == runtime {
			return true
		}
	}
	return false
}

func capabilitiesAvailable(required []string, available map[string]bool) bool {
	for _, capability := range required {
		if strings.TrimSpace(capability) == "" {
			continue
		}
		if !available[capability] {
			return false
		}
	}
	return true
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (r Registry) Get(identifier string) (Runtime, bool) {
	runtime, ok := r.runtimes[identifier]
	return runtime, ok
}

func (r Registry) Without(identifiers ...string) Registry {
	excluded := stringSet(identifiers)
	filtered := Registry{runtimes: make(map[string]Runtime, len(r.runtimes))}
	for identifier, runtime := range r.runtimes {
		if !excluded[identifier] {
			filtered.runtimes[identifier] = runtime
		}
	}
	return filtered
}

func (r Registry) GetAPI(identifier string, apiName string) (Manifest, API, bool) {
	runtime, ok := r.runtimes[identifier]
	if !ok {
		return Manifest{}, API{}, false
	}
	manifest := runtime.Manifest()
	for _, api := range manifest.API {
		if api.Name == apiName {
			return manifest, api, true
		}
	}
	return manifest, API{}, false
}

func (r Registry) Manifests() []Manifest {
	identifiers := make([]string, 0, len(r.runtimes))
	for identifier := range r.runtimes {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)

	manifests := make([]Manifest, 0, len(identifiers))
	for _, identifier := range identifiers {
		manifests = append(manifests, r.runtimes[identifier].Manifest())
	}
	return manifests
}

func (r Registry) ModelTools() []llm.Tool {
	manifests := r.modelManifests()
	if len(manifests) == 0 {
		return nil
	}
	modelTools := make([]llm.Tool, 0)
	for _, manifest := range manifests {
		for _, api := range manifest.API {
			parameters := api.Parameters
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			modelTools = append(modelTools, llm.Tool{
				Type: "function",
				Function: llm.ToolFunction{
					Name:        manifest.Identifier + "." + api.Name,
					Description: api.Description,
					Parameters:  parameters,
				},
			})
		}
	}
	return modelTools
}

func (r Registry) ModelContext() json.RawMessage {
	manifests := r.modelManifests()
	if len(manifests) == 0 {
		return nil
	}
	payload := map[string]any{
		"protocol_version": ManifestProtocolVersion,
		"tool_call_format": map[string]any{
			"protocol_version": ToolCallProtocolVersion,
			"shape": map[string]any{
				"tool_calls": []map[string]any{{
					"id":   "optional stable call id",
					"type": "function",
					"function": map[string]any{
						"name":      "tool namespace plus api name, for example default.run_command",
						"arguments": "JSON object matching the API parameters",
					},
				}},
			},
		},
		"tools": manifests,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return json.RawMessage(encoded)
}

func (r Registry) modelManifests() []Manifest {
	manifests := r.Manifests()
	for manifestIndex := range manifests {
		visible := make([]API, 0, len(manifests[manifestIndex].API))
		for _, api := range manifests[manifestIndex].API {
			if !api.HiddenFromModel {
				visible = append(visible, api)
			}
		}
		manifests[manifestIndex].API = visible
	}
	return manifests
}

func PrettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if !json.Valid(raw) {
		return string(raw)
	}

	var builder bytes.Buffer
	if err := json.Indent(&builder, raw, "", "  "); err != nil {
		return string(raw)
	}
	return builder.String()
}

type RegistryExecutor struct {
	Registry Registry
}

func NewDefaultExecutor() RegistryExecutor {
	return RegistryExecutor{Registry: DefaultRegistry()}
}

func (e RegistryExecutor) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	call = NormalizeCall(call)
	registry := e.Registry
	if err := registry.ValidateIntegrity(); err != nil {
		return ExecutionResult{}, err
	}
	runtime, ok := registry.Get(call.Identifier)
	if !ok {
		return failedResult(call, "unsupported_tool", fmt.Sprintf("unsupported tool %q", call.Identifier)), nil
	}
	if validationError := registry.ValidateCallArguments(call); validationError != nil {
		if validationError.Type == "invalid_tool_schema" {
			return ExecutionResult{}, fmt.Errorf("%s: %s", validationError.Type, validationError.Message)
		}
		return failedResult(call, validationError.Type, validationError.Message), nil
	}

	result, err := runtime.Execute(ctx, call, executionContext)
	if err != nil {
		return failedResult(call, "tool_execution_failed", redactEnvironmentText(err.Error(), executionContext.Environment)), nil
	}
	result = redactExecutionResultEnvironment(result, executionContext.Environment)
	if result.Identifier == "" {
		result.Identifier = call.Identifier
	}
	if result.APIName == "" {
		result.APIName = call.APIName
	}
	if result.ID == "" {
		result.ID = call.ID
	}
	if executionContext.ArtifactRecorder != nil && !executionContext.DeferArtifacts && !result.PendingIntervention && result.Error == nil {
		artifactRefs, artifactErr := executionContext.ArtifactRecorder.RecordToolArtifact(ctx, call, executionContext, result)
		if len(artifactRefs) > 0 {
			result.Artifacts = append(result.Artifacts, artifactRefs...)
		}
		if artifactErr != nil {
			result.ArtifactError = artifactErr.Error()
		}
	}
	return result, nil
}

func redactExecutionResultEnvironment(result ExecutionResult, environment map[string]string) ExecutionResult {
	if len(environment) == 0 {
		return result
	}
	result.Content = redactEnvironmentText(result.Content, environment)
	result.ArtifactError = redactEnvironmentText(result.ArtifactError, environment)
	if result.Error != nil {
		cloned := *result.Error
		cloned.Message = redactEnvironmentText(cloned.Message, environment)
		result.Error = &cloned
	}
	if len(result.State) > 0 {
		var state any
		if json.Unmarshal(result.State, &state) == nil {
			state = redactEnvironmentValue(state, environment)
			if encoded, err := json.Marshal(state); err == nil {
				result.State = encoded
			}
		} else {
			result.State = json.RawMessage(redactEnvironmentText(string(result.State), environment))
		}
	}
	return result
}

func redactEnvironmentValue(value any, environment map[string]string) any {
	switch typed := value.(type) {
	case string:
		return redactEnvironmentText(typed, environment)
	case []any:
		for index := range typed {
			typed[index] = redactEnvironmentValue(typed[index], environment)
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = redactEnvironmentValue(item, environment)
		}
	}
	return value
}

func redactEnvironmentText(value string, environment map[string]string) string {
	return RedactEnvironmentText(value, environment)
}

// RedactEnvironmentText removes every managed secret value without relying on
// variable naming conventions. Public runtime path variables are excluded.
func RedactEnvironmentText(value string, environment map[string]string) string {
	type secret struct{ name, value string }
	secrets := make([]secret, 0, len(environment))
	for name, candidate := range environment {
		if candidate != "" && !isPublicRuntimeEnvironment(name) {
			secrets = append(secrets, secret{name: name, value: candidate})
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i].value) > len(secrets[j].value) })
	for _, item := range secrets {
		value = strings.ReplaceAll(value, item.value, "[REDACTED_ENV:"+item.name+"]")
	}
	return value
}

func HasSensitiveEnvironment(environment map[string]string) bool {
	for name, value := range environment {
		if value != "" && !isPublicRuntimeEnvironment(name) {
			return true
		}
	}
	return false
}

func RedactEnvironmentJSON(raw json.RawMessage, environment map[string]string) json.RawMessage {
	if len(raw) == 0 || !HasSensitiveEnvironment(environment) {
		return append(json.RawMessage(nil), raw...)
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return json.RawMessage(RedactEnvironmentText(string(raw), environment))
	}
	value = redactEnvironmentValue(value, environment)
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(RedactEnvironmentText(string(raw), environment))
	}
	return encoded
}

func isPublicRuntimeEnvironment(name string) bool {
	return name == "CLAUDE_SKILL_DIR" || name == "TMA_SKILLS_DIR" || name == "TMA_SKILL_DIR" ||
		name == "TMA_SKILL_DIRS_JSON" || strings.HasPrefix(name, "TMA_SKILL_DIR_")
}

func NormalizeCall(call Call) Call {
	if call.APIName == "" {
		call.APIName = call.Name
	}
	if call.Identifier == "" && strings.Contains(call.APIName, ".") {
		call.Identifier, call.APIName = splitFunctionName(call.APIName)
	}
	if call.Identifier == "" {
		call.Identifier = DefaultIdentifier
	}
	return call
}

func splitFunctionName(name string) (string, string) {
	parts := strings.Split(name, ".")
	if len(parts) == 1 {
		return "", name
	}
	return strings.Join(parts[:len(parts)-1], "."), parts[len(parts)-1]
}

func ResultMessage(result ExecutionResult) string {
	encoded, err := json.Marshal(ResultData(result))
	if err != nil {
		return `{"success":false,"error":{"type":"encode_failed","message":"encode tool result failed"}}`
	}
	return string(encoded)
}

func ResultData(result ExecutionResult) map[string]any {
	return map[string]any{
		"protocol_version":     ToolResultProtocolVersion,
		"id":                   result.ID,
		"identifier":           result.Identifier,
		"api_name":             result.APIName,
		"content":              result.Content,
		"state":                rawJSONObject(result.State),
		"artifacts":            result.Artifacts,
		"artifact_error":       result.ArtifactError,
		"pending_intervention": result.PendingIntervention,
		"error":                result.Error,
		"success":              result.Error == nil,
	}
}

func ContextResultMessage(result ExecutionResult, options ResultContextOptions) string {
	encoded, err := json.Marshal(ObservableResultData(result, options))
	if err != nil {
		return `{"success":false,"error":{"type":"encode_failed","message":"encode tool result failed"}}`
	}
	return string(encoded)
}

func ObservableResultData(result ExecutionResult, options ResultContextOptions) map[string]any {
	maxContentChars := options.MaxContentChars
	if maxContentChars <= 0 {
		maxContentChars = DefaultResultContextMaxChars
	}
	maxStateBytes := options.MaxStateBytes
	if maxStateBytes <= 0 {
		maxStateBytes = DefaultResultStateMaxBytes
	}
	content, contentTruncated := truncateResultTextForContext(result.Content, maxContentChars)
	state := rawJSONObject(result.State)
	stateTruncated := false
	if len(result.State) > maxStateBytes {
		stateTruncated = true
		state = map[string]any{
			"truncated":      true,
			"original_bytes": len(result.State),
			"message":        "Tool state omitted from model context; inspect the persisted tool artifact for full state.",
		}
	}
	if !stateTruncated && result.Identifier == NamespaceDefault && result.APIName == "read_file" {
		if metadata, ok := state.(map[string]any); ok {
			metadata["model_context_truncated"] = contentTruncated
			metadata["model_context_original_chars"] = textRuneCount(result.Content)
			metadata["model_context_visible_chars"] = textRuneCount(content)
		}
	}
	return map[string]any{
		"protocol_version":     ToolResultProtocolVersion,
		"id":                   result.ID,
		"identifier":           result.Identifier,
		"api_name":             result.APIName,
		"content":              content,
		"state":                state,
		"artifacts":            result.Artifacts,
		"artifact_error":       result.ArtifactError,
		"pending_intervention": result.PendingIntervention,
		"error":                result.Error,
		"success":              result.Error == nil,
		"context": map[string]any{
			"content_truncated":        contentTruncated,
			"original_content_chars":   textRuneCount(result.Content),
			"visible_content_chars":    textRuneCount(content),
			"state_truncated":          stateTruncated,
			"original_state_bytes":     len(result.State),
			"full_result_in_artifacts": len(result.Artifacts) > 0,
		},
	}
}

func truncateResultTextForContext(text string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = DefaultResultContextMaxChars
	}
	if textRuneCount(text) <= maxChars {
		return text, false
	}
	if maxChars < 120 {
		return firstRunes(text, maxChars) + "\n\n[Tool result truncated for model context. Full result is available in session artifacts when present.]", true
	}
	headChars := maxChars * 2 / 3
	tailChars := maxChars - headChars
	omitted := textRuneCount(text) - headChars - tailChars
	return firstRunes(text, headChars) +
		"\n\n[Tool result truncated for model context: " + strconv.Itoa(omitted) + " characters omitted. Full result is available in session artifacts when present.]\n\n" +
		lastRunes(text, tailChars), true
}

func firstRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	for index := range text {
		if limit == 0 {
			return text[:index]
		}
		limit--
	}
	return text
}

func lastRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[len(runes)-limit:])
}

func textRuneCount(text string) int {
	return len([]rune(text))
}

func failedResult(call Call, errorType string, message string) ExecutionResult {
	call = NormalizeCall(call)
	return ExecutionResult{
		ID:         call.ID,
		Identifier: call.Identifier,
		APIName:    call.APIName,
		Error: &ExecutionError{
			Type:    errorType,
			Message: message,
		},
	}
}

func PendingInterventionResult(call Call, message string) ExecutionResult {
	call = NormalizeCall(call)
	return ExecutionResult{
		ID:                  call.ID,
		Identifier:          call.Identifier,
		APIName:             call.APIName,
		Content:             message,
		PendingIntervention: true,
		Error: &ExecutionError{
			Type:    "human_intervention_required",
			Message: message,
		},
	}
}

func rawJSONObject(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}
