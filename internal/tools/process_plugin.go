package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	PluginProtocolVersion       = "tma.plugin.v1"
	PluginResultProtocolVersion = "tma.plugin_result.v1"
	defaultPluginTimeout        = 30 * time.Second
)

// ProcessPluginRuntime adapts an external executable into a TMA tool runtime.
//
// The plugin executable must support two subcommands:
//   - manifest: print a tools.Manifest JSON object to stdout.
//   - execute: read PluginRequest JSON from stdin and print PluginResult JSON to stdout.
type ProcessPluginRuntime struct {
	Command         string
	Args            []string
	manifest        Manifest
	ManifestTimeout time.Duration
	ExecuteTimeout  time.Duration
}

type PluginRequest struct {
	ProtocolVersion string            `json:"protocol_version"`
	Call            Call              `json:"call"`
	Context         PluginCallContext `json:"context"`
}

type PluginCallContext struct {
	WorkspaceID   string `json:"workspace_id,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	TurnID        string `json:"turn_id,omitempty"`
}

type PluginResult struct {
	ProtocolVersion string           `json:"protocol_version"`
	Success         *bool            `json:"success,omitempty"`
	Content         string           `json:"content,omitempty"`
	State           json.RawMessage  `json:"state,omitempty"`
	ExportedFiles   []ArtifactExport `json:"exported_files,omitempty"`
	Artifacts       []ArtifactRef    `json:"artifacts,omitempty"`
	ArtifactError   string           `json:"artifact_error,omitempty"`
	Error           *ExecutionError  `json:"error,omitempty"`
}

func LoadProcessPluginRuntime(ctx context.Context, command string, args ...string) (ProcessPluginRuntime, error) {
	runtime := ProcessPluginRuntime{
		Command: strings.TrimSpace(command),
		Args:    append([]string(nil), args...),
	}
	if runtime.Command == "" {
		return ProcessPluginRuntime{}, fmt.Errorf("plugin command is required")
	}
	manifest, err := runtime.loadManifest(ctx)
	if err != nil {
		return ProcessPluginRuntime{}, err
	}
	runtime.manifest = manifest
	return runtime, nil
}

func (r ProcessPluginRuntime) Manifest() Manifest {
	return r.manifest
}

func (r ProcessPluginRuntime) Execute(ctx context.Context, call Call, executionContext ExecutionContext) (ExecutionResult, error) {
	if r.Command == "" {
		return ExecutionResult{}, fmt.Errorf("plugin command is required")
	}
	request := PluginRequest{
		ProtocolVersion: PluginProtocolVersion,
		Call:            call,
		Context: PluginCallContext{
			WorkspaceID:   executionContext.WorkspaceID,
			SessionID:     executionContext.SessionID,
			EnvironmentID: executionContext.EnvironmentID,
			TurnID:        executionContext.TurnID,
		},
	}
	input, err := json.Marshal(request)
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("encode plugin request: %w", err)
	}
	output, stderr, err := r.run(ctx, r.ExecuteTimeout, "execute", input, executionContext.Environment)
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("execute plugin %s: %w%s", r.Command, err, formatPluginStderr(stderr))
	}
	var result PluginResult
	if err := json.Unmarshal(output, &result); err != nil {
		return ExecutionResult{}, fmt.Errorf("decode plugin result from %s: %w; stdout=%q%s", r.Command, err, string(output), formatPluginStderr(stderr))
	}
	if result.ProtocolVersion != "" && result.ProtocolVersion != PluginResultProtocolVersion {
		return ExecutionResult{}, fmt.Errorf("unsupported plugin result protocol version %q", result.ProtocolVersion)
	}
	executionResult := ExecutionResult{
		ID:            call.ID,
		Identifier:    call.Identifier,
		APIName:       call.APIName,
		Content:       result.Content,
		State:         result.State,
		ExportedFiles: result.ExportedFiles,
		Artifacts:     result.Artifacts,
		ArtifactError: result.ArtifactError,
		Error:         result.Error,
	}
	if len(executionResult.State) == 0 {
		executionResult.State = json.RawMessage(`{}`)
	}
	if result.Success != nil && !*result.Success && executionResult.Error == nil {
		executionResult.Error = &ExecutionError{Type: "plugin_failed", Message: "plugin returned success=false"}
	}
	return executionResult, nil
}

func (r ProcessPluginRuntime) loadManifest(ctx context.Context) (Manifest, error) {
	output, stderr, err := r.run(ctx, r.ManifestTimeout, "manifest", nil, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("load plugin manifest from %s: %w%s", r.Command, err, formatPluginStderr(stderr))
	}
	var manifest Manifest
	if err := json.Unmarshal(output, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode plugin manifest from %s: %w; stdout=%q%s", r.Command, err, string(output), formatPluginStderr(stderr))
	}
	if err := validatePluginManifest(manifest); err != nil {
		return Manifest{}, fmt.Errorf("invalid plugin manifest from %s: %w", r.Command, err)
	}
	return manifest, nil
}

func (r ProcessPluginRuntime) run(ctx context.Context, timeout time.Duration, mode string, stdin []byte, environment map[string]string) ([]byte, string, error) {
	if timeout <= 0 {
		timeout = defaultPluginTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append([]string(nil), r.Args...)
	args = append(args, mode)
	command := exec.CommandContext(runCtx, r.Command, args...)
	if len(environment) > 0 {
		command.Env = append([]string(nil), os.Environ()...)
		for key, value := range environment {
			prefix := key + "="
			filtered := command.Env[:0]
			for _, entry := range command.Env {
				if !strings.HasPrefix(entry, prefix) {
					filtered = append(filtered, entry)
				}
			}
			command.Env = filtered
			command.Env = append(command.Env, key+"="+value)
		}
	}
	if len(stdin) > 0 {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if runCtx.Err() != nil {
		err = runCtx.Err()
	}
	return stdout.Bytes(), stderr.String(), err
}

func validatePluginManifest(manifest Manifest) error {
	namespace, ok := NormalizeToolNamespace(manifest.Identifier)
	if !ok {
		return fmt.Errorf("invalid identifier %q", manifest.Identifier)
	}
	if manifest.Identifier != namespace {
		return fmt.Errorf("identifier %q must be normalized as %q", manifest.Identifier, namespace)
	}
	if isReservedPluginNamespace(namespace) {
		return fmt.Errorf("identifier %q is reserved for built-in tools", namespace)
	}
	if len(manifest.API) == 0 {
		return fmt.Errorf("manifest must declare at least one api")
	}
	for _, api := range manifest.API {
		apiName := defaultString(api.APIName, api.Name)
		if !isValidToolIdentifier(strings.TrimSpace(strings.ToLower(apiName))) {
			return fmt.Errorf("invalid api %q", apiName)
		}
		if api.Namespace != "" {
			if namespace, ok := NormalizeToolNamespace(api.Namespace); !ok || namespace != api.Namespace {
				return fmt.Errorf("invalid api namespace %q", api.Namespace)
			}
		}
		if api.Risk != "" {
			if _, ok := NormalizeToolRisk(api.Risk); !ok {
				return fmt.Errorf("invalid api risk %q", api.Risk)
			}
		}
		for _, runtime := range NormalizeRuntimePolicy(api.Runtime).Allowed {
			if _, ok := NormalizeToolRuntime(runtime); !ok {
				return fmt.Errorf("invalid api runtime %q", runtime)
			}
		}
		if implementation, ok := NormalizeToolImplementation(api.Implementation); !ok || implementation == ToolImplementationServerBuiltin {
			return fmt.Errorf("plugin api %q must be worker executable", apiName)
		}
	}
	return nil
}

func isReservedPluginNamespace(namespace string) bool {
	switch namespace {
	case NamespaceDefault, NamespaceArtifact, NamespaceBrowser, NamespaceAgent, NamespaceSkills, NamespaceWeb:
		return true
	default:
		return false
	}
}

func formatPluginStderr(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + stderr
}
