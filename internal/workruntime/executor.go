package workruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

type Executor struct {
	WorkerName           string
	WorkspaceRoot        string
	Registry             tools.Registry
	Provider             capability.Provider
	ArtifactUploader     ArtifactUploader
	Handlers             map[string]WorkHandler
	DeclaredCapabilities *tools.WorkerCapabilities
}

type ArtifactUpload struct {
	SessionID     string
	EnvironmentID string
	TurnID        string
	ToolCallID    string
	Path          string
	Name          string
	Description   string
	ArtifactType  string
	ContentType   string
	Content       []byte
}

type ArtifactUploader interface {
	UploadArtifact(context.Context, ArtifactUpload) (tools.ArtifactRef, error)
}

type WorkHandler interface {
	ExecuteWork(context.Context, Executor, managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput
}

type WorkHandlerFunc func(context.Context, Executor, managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput

func (fn WorkHandlerFunc) ExecuteWork(ctx context.Context, executor Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
	return fn(ctx, executor, work)
}

func DefaultExecutor(workerName string) Executor {
	return Executor{
		WorkerName: workerName,
		Registry:   tools.DefaultRegistry(),
		Provider:   capability.LocalSystemProvider{},
		Handlers:   DefaultHandlers(),
	}
}

func DefaultHandlers() map[string]WorkHandler {
	return map[string]WorkHandler{
		managedagents.WorkerWorkTypeToolExecution: WorkHandlerFunc(func(ctx context.Context, executor Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
			return executor.executeToolExecution(ctx, work)
		}),
		managedagents.WorkerWorkTypeSandboxCommand: WorkHandlerFunc(func(ctx context.Context, executor Executor, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
			return executor.executeSandboxCommand(ctx, work)
		}),
	}
}

func (e Executor) WorkerCapabilities() tools.WorkerCapabilities {
	if e.DeclaredCapabilities != nil {
		return cloneWorkerCapabilities(*e.DeclaredCapabilities)
	}
	capabilities := LocalSystemCapabilities(e.registry())
	if strings.TrimSpace(e.WorkspaceRoot) != "" {
		if capabilities.Constraints == nil {
			capabilities.Constraints = map[string]any{}
		}
		capabilities.Constraints["filesystem_scope"] = "workspace_root"
		capabilities.Constraints["workspace_root_configured"] = true
	}
	return capabilities
}

func (e Executor) WorkerCapabilitiesJSON() json.RawMessage {
	encoded, err := json.Marshal(e.WorkerCapabilities())
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func LocalSystemCapabilities(registry tools.Registry) tools.WorkerCapabilities {
	capabilities := tools.WorkerCapabilities{
		Runtimes: []string{tools.ToolRuntimeLocalSystem},
	}
	seenNamespaces := map[string]bool{}
	seenAPIs := map[string]bool{}
	seenCapabilities := map[string]bool{}
	for _, manifest := range registry.Manifests() {
		filteredManifest := manifest
		filteredManifest.API = nil
		for _, api := range manifest.API {
			if !workerExecutable(api, tools.ToolRuntimeLocalSystem) {
				continue
			}
			namespace := defaultString(api.Namespace, manifest.Identifier)
			apiName := defaultString(api.APIName, api.Name)
			appendUnique(&capabilities.Namespaces, seenNamespaces, []string{namespace})
			appendUnique(&capabilities.APIs, seenAPIs, []string{namespace + "." + apiName})
			appendUnique(&capabilities.Capabilities, seenCapabilities, api.Capabilities)
			filteredManifest.API = append(filteredManifest.API, api)
		}
		if len(filteredManifest.API) > 0 {
			capabilities.Manifests = append(capabilities.Manifests, filteredManifest)
		}
	}
	return capabilities
}

func (e Executor) Execute(ctx context.Context, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
	workType := strings.TrimSpace(work.WorkType)
	if workType == "" {
		workType = managedagents.WorkerWorkTypeToolExecution
	}
	if handler := e.handlers()[workType]; handler != nil {
		return handler.ExecuteWork(ctx, e, work)
	}
	return e.echoWork(work)
}

func (e Executor) executeToolExecution(ctx context.Context, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(work.Payload, &invocation); err != nil {
		return workFailure("invalid tool execution payload: " + err.Error())
	}
	if err := tools.ValidateWorkInvocation(invocation); err != nil {
		return workFailure("invalid tool execution payload: " + err.Error())
	}
	var environment map[string]string
	if invocation.EnvironmentEnvelope != "" {
		cipher, err := envvars.CipherFromEnvironment()
		if err != nil {
			return workFailure("managed environment is unavailable on worker: " + err.Error())
		}
		environment, err = cipher.OpenMap(invocation.EnvironmentEnvelope, envvars.EnvelopeAssociatedData(work.WorkspaceID, work.SessionID, work.TurnID))
		if err != nil {
			return workFailure("decrypt managed environment on worker: " + err.Error())
		}
	}
	registry := e.registry()
	if _, ok := registry.Get(invocation.Namespace); !ok {
		return workFailure("unsupported tool namespace: " + invocation.Namespace)
	}
	if _, _, ok := registry.GetAPI(invocation.Namespace, invocation.API); !ok {
		return workFailure("unsupported tool api: " + invocation.Namespace + "." + invocation.API)
	}
	arguments, editRevision, editContentSHA256, err := workerCapabilityArguments(invocation)
	if err != nil {
		return workFailure("invalid tool execution payload: " + err.Error())
	}

	result, err := (tools.RegistryExecutor{Registry: registry}).Execute(ctx, tools.Call{
		ID:         work.ID,
		Identifier: invocation.Namespace,
		APIName:    invocation.API,
		Arguments:  arguments,
	}, tools.ExecutionContext{
		WorkspaceID:               work.WorkspaceID,
		SessionID:                 work.SessionID,
		EnvironmentID:             work.EnvironmentID,
		TurnID:                    work.TurnID,
		Environment:               environment,
		Provider:                  e.provider(),
		CapabilityTransport:       true,
		ExpectedFileRevision:      editRevision,
		ExpectedFileContentSHA256: editContentSHA256,
	})
	exportedFiles, artifactRefs, exportErr := e.collectExportedFiles(ctx, work, tools.Call{
		ID:         work.ID,
		Identifier: invocation.Namespace,
		APIName:    invocation.API,
	}, result)
	if len(result.ExportedFiles) > 0 {
		result.ExportedFiles = exportedFiles
	}
	if len(artifactRefs) > 0 {
		result.Artifacts = append(result.Artifacts, artifactRefs...)
	}
	if exportErr != nil {
		if result.ArtifactError == "" {
			result.ArtifactError = exportErr.Error()
		} else {
			result.ArtifactError = result.ArtifactError + "; " + exportErr.Error()
		}
	}
	response := map[string]any{
		"status":       "executed",
		"work_id":      work.ID,
		"work_type":    work.WorkType,
		"worker_name":  e.WorkerName,
		"invocation":   invocation,
		"tool_result":  result,
		"tool_runtime": tools.ToolRuntimeLocalSystem,
	}
	resultJSON, _ := json.Marshal(response)
	if err != nil {
		return managedagents.CompleteWorkerWorkInput{
			Success:      false,
			Result:       resultJSON,
			ErrorMessage: err.Error(),
		}
	}
	if result.Error != nil {
		return managedagents.CompleteWorkerWorkInput{
			Success:      false,
			Result:       resultJSON,
			ErrorMessage: result.Error.Message,
		}
	}
	return managedagents.CompleteWorkerWorkInput{
		Success: true,
		Result:  resultJSON,
	}
}

func workerCapabilityArguments(invocation tools.WorkInvocation) (json.RawMessage, string, string, error) {
	arguments := append(json.RawMessage(nil), invocation.Input...)
	if invocation.Namespace != tools.NamespaceDefault || invocation.API != "edit_file" {
		return arguments, "", "", nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &object); err != nil {
		return nil, "", "", err
	}
	var revision string
	if raw := object["expected_revision"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &revision); err != nil {
			return nil, "", "", fmt.Errorf("decode edit expected_revision: %w", err)
		}
		delete(object, "expected_revision")
	}
	var contentSHA256 string
	if raw := object["expected_content_sha256"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &contentSHA256); err != nil {
			return nil, "", "", fmt.Errorf("decode edit expected_content_sha256: %w", err)
		}
		delete(object, "expected_content_sha256")
	}
	arguments, err := json.Marshal(object)
	return arguments, strings.TrimSpace(revision), strings.ToLower(strings.TrimSpace(contentSHA256)), err
}

func (e Executor) collectExportedFiles(ctx context.Context, work managedagents.WorkerWork, call tools.Call, result tools.ExecutionResult) ([]tools.ArtifactExport, []tools.ArtifactRef, error) {
	if len(result.ExportedFiles) == 0 {
		return nil, nil, nil
	}
	exporter, ok := e.provider().(capability.ArtifactExportProvider)
	if !ok || exporter == nil {
		return nil, nil, fmt.Errorf("worker provider %T does not support artifact export", e.provider())
	}
	exported := make([]tools.ArtifactExport, 0, len(result.ExportedFiles))
	artifactRefs := make([]tools.ArtifactRef, 0, len(result.ExportedFiles))
	var exportErrors []string
	for _, file := range result.ExportedFiles {
		exportedFile, err := exporter.ExportArtifactFile(ctx, capability.ExportArtifactFileRequest{
			Path:    file.Path,
			WorkDir: file.WorkDir,
		})
		if err != nil {
			exportErrors = append(exportErrors, fmt.Sprintf("export worker artifact %q: %v", file.Path, err))
			continue
		}
		contentType := firstNonEmpty(file.ContentType, exportedFile.ContentType)
		if shouldUploadExportedFile(contentType, len(exportedFile.Content)) {
			uploader := e.ArtifactUploader
			if uploader == nil || strings.TrimSpace(work.SessionID) == "" {
				if len(exportedFile.Content) > tools.MaxTransportedArtifactBytes {
					exportErrors = append(exportErrors, fmt.Sprintf("export worker artifact %q: file size %d exceeds transported artifact limit %d", file.Path, len(exportedFile.Content), tools.MaxTransportedArtifactBytes))
				} else {
					exportErrors = append(exportErrors, fmt.Sprintf("export worker artifact %q: image exports require session artifact upload", file.Path))
				}
				continue
			}
			ref, err := uploader.UploadArtifact(ctx, ArtifactUpload{
				SessionID:     work.SessionID,
				EnvironmentID: work.EnvironmentID,
				TurnID:        work.TurnID,
				ToolCallID:    call.ID,
				Path:          firstNonEmpty(file.Path, exportedFile.Path),
				Name:          firstNonEmpty(file.Name, exportedFile.Name),
				Description:   file.Description,
				ArtifactType:  file.ArtifactType,
				ContentType:   contentType,
				Content:       exportedFile.Content,
			})
			if err != nil {
				exportErrors = append(exportErrors, fmt.Sprintf("upload worker artifact %q: %v", file.Path, err))
				continue
			}
			artifactRefs = append(artifactRefs, ref)
			continue
		}
		exported = append(exported, tools.ArtifactExport{
			Path:          firstNonEmpty(file.Path, exportedFile.Path),
			WorkDir:       file.WorkDir,
			Name:          firstNonEmpty(file.Name, exportedFile.Name),
			Description:   file.Description,
			ArtifactType:  file.ArtifactType,
			ContentType:   contentType,
			ContentBase64: base64.StdEncoding.EncodeToString(exportedFile.Content),
		})
	}
	if len(exportErrors) > 0 {
		return exported, artifactRefs, fmt.Errorf("%s", strings.Join(exportErrors, "; "))
	}
	return exported, artifactRefs, nil
}

func shouldUploadExportedFile(contentType string, size int) bool {
	if size > tools.MaxTransportedArtifactBytes {
		return true
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "image/")
}

func (e Executor) executeSandboxCommand(ctx context.Context, work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
	var request capability.RunCommandRequest
	if err := json.Unmarshal(work.Payload, &request); err != nil {
		return workFailure("invalid sandbox command payload: " + err.Error())
	}
	if request.Meta.ProtocolVersion == "" {
		request.Meta = capability.NewRequestMeta(work.SessionID, work.TurnID, nil)
	}

	commandResult, err := e.provider().RunCommand(ctx, request)
	result := map[string]any{
		"status":         "executed",
		"work_id":        work.ID,
		"work_type":      work.WorkType,
		"worker_name":    e.WorkerName,
		"command_result": commandResult,
	}
	resultJSON, _ := json.Marshal(result)
	if err != nil {
		return managedagents.CompleteWorkerWorkInput{
			Success:      false,
			Result:       resultJSON,
			ErrorMessage: err.Error(),
		}
	}
	if commandResult.ExitCode != 0 {
		return managedagents.CompleteWorkerWorkInput{
			Success:      false,
			Result:       resultJSON,
			ErrorMessage: fmt.Sprintf("command exited with code %d", commandResult.ExitCode),
		}
	}
	return managedagents.CompleteWorkerWorkInput{
		Success: true,
		Result:  resultJSON,
	}
}

func (e Executor) echoWork(work managedagents.WorkerWork) managedagents.CompleteWorkerWorkInput {
	result := map[string]any{
		"status":      "echoed",
		"work_id":     work.ID,
		"work_type":   work.WorkType,
		"payload":     rawJSONObject(work.Payload),
		"worker_name": e.WorkerName,
	}
	resultJSON, _ := json.Marshal(result)
	return managedagents.CompleteWorkerWorkInput{
		Success: true,
		Result:  resultJSON,
	}
}

func (e Executor) registry() tools.Registry {
	if len(e.Registry.Manifests()) == 0 {
		return tools.DefaultRegistry()
	}
	return e.Registry
}

func (e Executor) provider() capability.Provider {
	if e.Provider == nil {
		return capability.LocalSystemProvider{}
	}
	return e.Provider
}

func (e Executor) handlers() map[string]WorkHandler {
	if len(e.Handlers) == 0 {
		return DefaultHandlers()
	}
	return e.Handlers
}

func workerExecutable(api tools.API, runtime string) bool {
	if implementation, ok := tools.NormalizeToolImplementation(api.Implementation); ok {
		if implementation != "" && implementation != tools.ToolImplementationWorkerCapability {
			return false
		}
	}
	policy := tools.NormalizeRuntimePolicy(api.Runtime)
	for _, allowed := range policy.Allowed {
		if allowed == tools.ToolRuntimeAuto || allowed == runtime {
			return true
		}
	}
	return false
}

func workFailure(message string) managedagents.CompleteWorkerWorkInput {
	resultJSON, _ := json.Marshal(map[string]any{
		"status": "failed",
		"error":  message,
	})
	return managedagents.CompleteWorkerWorkInput{
		Success:      false,
		Result:       resultJSON,
		ErrorMessage: message,
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func appendUnique(target *[]string, seen map[string]bool, values []string) {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		*target = append(*target, value)
	}
}

func cloneWorkerCapabilities(capabilities tools.WorkerCapabilities) tools.WorkerCapabilities {
	cloned := tools.WorkerCapabilities{
		Namespaces:   append([]string(nil), capabilities.Namespaces...),
		APIs:         append([]string(nil), capabilities.APIs...),
		Runtimes:     append([]string(nil), capabilities.Runtimes...),
		Capabilities: append([]string(nil), capabilities.Capabilities...),
		Manifests:    cloneManifests(capabilities.Manifests),
	}
	if capabilities.Constraints != nil {
		cloned.Constraints = make(map[string]any, len(capabilities.Constraints))
		for key, value := range capabilities.Constraints {
			cloned.Constraints[key] = value
		}
	}
	return cloned
}

func cloneManifests(manifests []tools.Manifest) []tools.Manifest {
	if len(manifests) == 0 {
		return nil
	}
	cloned := make([]tools.Manifest, len(manifests))
	for index, manifest := range manifests {
		cloned[index] = manifest
		cloned[index].API = append([]tools.API(nil), manifest.API...)
		cloned[index].Executors = append([]string(nil), manifest.Executors...)
	}
	return cloned
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
