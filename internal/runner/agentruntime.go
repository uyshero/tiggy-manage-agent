package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentruntime"
	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/mcp"
	"tiggy-manage-agent/internal/mcpregistry"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tokenestimate"
	"tiggy-manage-agent/internal/toolruntime"
	"tiggy-manage-agent/internal/tools"
)

const maxVisionImageBytes = 20 << 20

// AgentRuntimeTurnExecutor 把 WorkerRunner 的 TurnExecutor 接口适配到 AgentRuntime。
type AgentRuntimeTurnExecutor struct {
	CoreClient         llm.Client
	CoreCompletionGate agentruntime.CompletionGate
	CoreMaxRounds      int
	Store              managedagents.Store
	ObjectStore        objectstore.Client
	ArtifactBucket     string
	Timeout            time.Duration
	ProviderResolver   execution.ProviderResolver
	MCPHost            *mcp.StdioHost
	MCPHTTPHost        *mcp.StreamableHTTPHost
	MCPRuntimeGuard    *mcp.RuntimeGuard
	LiveEvents         *LiveEventBroker
	ToolMiddlewares    []toolruntime.ToolMiddleware
}

func (e AgentRuntimeTurnExecutor) MCPHostStats() mcp.StdioHostStats {
	if e.MCPHost == nil {
		return mcp.StdioHostStats{}
	}
	return e.MCPHost.Stats()
}

func (e AgentRuntimeTurnExecutor) MCPHTTPHostStats() mcp.StreamableHTTPHostStats {
	if e.MCPHTTPHost == nil {
		return mcp.StreamableHTTPHostStats{}
	}
	return e.MCPHTTPHost.Stats()
}

func (e AgentRuntimeTurnExecutor) MCPHTTPEgressPolicy() *mcp.EgressPolicy {
	if e.MCPHTTPHost == nil {
		return nil
	}
	return e.MCPHTTPHost.EgressPolicy()
}

func (e AgentRuntimeTurnExecutor) MCPRuntimeGuardStats() mcp.RuntimeGuardStats {
	if e.MCPRuntimeGuard == nil {
		return mcp.RuntimeGuardStats{}
	}
	return e.MCPRuntimeGuard.Stats()
}

func (e AgentRuntimeTurnExecutor) MCPRegistryRuntimeStates(workspaceID string) []mcp.RegistryRuntimeState {
	if e.MCPRuntimeGuard == nil {
		return nil
	}
	return e.MCPRuntimeGuard.RegistryStates(workspaceID)
}

func (e AgentRuntimeTurnExecutor) RunTurn(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	ctx, err := databaseContextForTurn(ctx, request)
	if err != nil {
		return TurnResult{}, err
	}
	emit := e.emitStep(request)

	config, err := e.resolveRuntimeConfig(ctx, request.SessionID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	config.RuntimeSettings = runtimeSettingsForTurn(config.RuntimeSettings, request.UserPayload)
	taskPlanContext, err := e.resolveTaskPlanContext(ctx, request.SessionID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	selectionHistory, err := e.resolveConversationHistory(ctx, request.SessionID, request.UserEventSeq)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	continuationContext, err := e.resolveContinuationContext(ctx, request)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	if continuationContext != "" {
		if taskPlanContext != "" {
			taskPlanContext += "\n\n"
		}
		taskPlanContext += continuationContext
	}
	selectionHistory = conversationHistoryAfterSeq(selectionHistory, config.SummarySourceUntilSeq)
	var history []managedagents.ConversationMessage
	if request.ResumeIntervention == nil {
		history = selectionHistory
	}
	resolvedSkills, err := e.resolveSkills(ctx, request, config, emit)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	imageParts, err := e.loadUserImageParts(ctx, request.UserPayload, config.WorkspaceID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}

	startedAt := time.Now()
	managedEnvironment, environmentCipher, err := envvars.ResolveWorkspace(ctx, e.Store, config.WorkspaceID)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, fmt.Errorf("resolve managed environment: %w", err)
	}
	history = redactConversationHistoryEnvironment(history, managedEnvironment)
	request.UserPayload = tools.RedactEnvironmentJSON(request.UserPayload, managedEnvironment)
	resolvedSkills.Rendered = tools.RedactEnvironmentJSON(resolvedSkills.Rendered, managedEnvironment)
	toolExecution := execution.ResolveToolExecution(execution.ToolExecutionRequest{
		Context:           ctx,
		Config:            config,
		SessionID:         config.SessionID,
		TurnID:            request.TurnID,
		ProviderResolver:  e.ProviderResolver,
		Store:             e.Store,
		ArtifactRecorder:  ToolArtifactRecorder{Store: e.Store, ObjectStore: e.ObjectStore, Bucket: e.ArtifactBucket},
		Environment:       managedEnvironment,
		EnvironmentCipher: environmentCipher,
		MCPHost:           e.MCPHost,
		MCPHTTPHost:       e.MCPHTTPHost,
		MCPRuntimeGuard:   e.MCPRuntimeGuard,
		ModelClient:       e.CoreClient,
	})
	if err := e.restoreWorkspaceSnapshot(ctx, toolExecution.Provider, config, request.SessionID); err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	materializedSkills, err := e.materializeResolvedSkills(ctx, toolExecution.Provider, resolvedSkills)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, err
	}
	if len(materializedSkills.directories) > 0 {
		resolvedSkills, err = skills.BindRuntimeDirectories(resolvedSkills, materializedSkills.directories)
		if err != nil {
			_ = e.recordRuntimeFailed(ctx, err, emit)
			return TurnResult{}, fmt.Errorf("bind runtime skill directories: %w", err)
		}
		managedEnvironment = mergeRuntimeSkillEnvironment(managedEnvironment, materializedSkills)
		toolExecution.Context.Environment = managedEnvironment
	}
	toolExecution.Registry = execution.SelectTurnTools(toolExecution.Registry, toolExecution.Policy, execution.TurnToolSelection{
		UserPayload:     request.UserPayload,
		History:         selectionHistory,
		SummaryText:     config.SummaryText,
		HasActiveSkills: len(resolvedSkills.Skills) > 0,
		SkillContext:    resolvedSkills.Rendered,
	})
	permissionRules, err := tools.ResolvePermissionRules(config.RuntimeSettings, config.Tools, config.WorkspaceToolPolicy)
	if err != nil {
		_ = e.recordRuntimeFailed(ctx, err, emit)
		return TurnResult{}, fmt.Errorf("resolve tool permission rules: %w", err)
	}
	runtimeRequest := agentruntime.TurnRequest{
		SessionID:   request.SessionID,
		TurnID:      request.TurnID,
		UserPayload: request.UserPayload,
		History:     history,
		ImageParts:  imageParts,
		Config: agentruntime.Config{
			WorkspaceID:           config.WorkspaceID,
			EnvironmentID:         config.EnvironmentID,
			LLMProvider:           config.LLMProvider,
			LLMProviderType:       config.LLMProviderType,
			LLMModel:              config.LLMModel,
			LLMBaseURL:            config.LLMBaseURL,
			LLMAPIKey:             llmAPIKey(config.LLMAPIKeyEnv),
			LLMCapabilityType:     config.LLMCapabilityType,
			VisionLLMProvider:     config.VisionLLMProvider,
			VisionLLMProviderType: config.VisionLLMProviderType,
			VisionLLMModel:        config.VisionLLMModel,
			VisionLLMBaseURL:      config.VisionLLMBaseURL,
			VisionLLMAPIKey:       llmAPIKey(config.VisionLLMAPIKeyEnv),
			ContextWindowTokens:   config.ContextWindowTokens,
			SummaryText:           tools.RedactEnvironmentText(config.SummaryText, managedEnvironment),
			SummarySourceUntilSeq: config.SummarySourceUntilSeq,
			TaskPlanContext:       tools.RedactEnvironmentText(taskPlanContext, managedEnvironment),
			System:                tools.RedactEnvironmentText(config.System, managedEnvironment),
			RuntimeSettings:       config.RuntimeSettings,
			Tools:                 toolExecution.Registry.ModelContext(),
			ModelTools:            toolExecution.Registry.ModelTools(),
			Skills:                resolvedSkills.Rendered,
			SkillsResolved:        true,
			InterventionMode:      tools.ParseInterventionMode(config.RuntimeSettings),
			PermissionRules:       permissionRules,
			ToolRegistry:          toolExecution.Registry,
			ToolExecutor:          tools.RegistryExecutor{Registry: toolExecution.Registry},
			ToolExecutionContext:  toolExecution.Context,
		},
		EmitStep: emit,
		EmitStream: func(event agentruntime.StreamEvent) {
			if e.LiveEvents != nil {
				e.LiveEvents.Publish(LiveEvent{
					SessionID: request.SessionID, TurnID: request.TurnID, Type: LiveEventLLMText,
					Index: event.Index, ToolRound: event.ToolRound, Operation: "append", ContentFormat: "markdown", Text: event.Text,
				})
			}
		},
	}
	result, err := e.runAgentCoreTurn(ctx, request, runtimeRequest, config, toolExecution, startedAt)
	result.AgentPayload = tools.RedactEnvironmentJSON(result.AgentPayload, managedEnvironment)
	if err == nil && result.DurableStatus == "completed" {
		if checkpointErr := e.checkpointWorkspaceSnapshot(ctx, toolExecution.Provider, config, request); checkpointErr != nil {
			slog.Default().Warn("workspace snapshot checkpoint failed", "session_id", request.SessionID, "turn_id", request.TurnID, "error", checkpointErr)
		}
	}
	return result, err
}

func (e AgentRuntimeTurnExecutor) SubscribeLiveEvents(sessionID string) (<-chan LiveEvent, func(), error) {
	return e.LiveEvents.SubscribeLiveEvents(sessionID)
}

func (e AgentRuntimeTurnExecutor) restoreWorkspaceSnapshot(ctx context.Context, provider capability.Provider, config managedagents.AgentRuntimeConfig, sessionID string) error {
	runtime, ok := provider.(capability.WorkspaceSnapshotProvider)
	store, storeOK := e.Store.(managedagents.WorkspaceSnapshotStore)
	if !ok || !storeOK || e.ObjectStore == nil {
		return nil
	}
	snapshot, err := store.GetLatestWorkspaceSnapshot(ctx, sessionID)
	if errors.Is(err, managedagents.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get latest workspace snapshot: %w", err)
	}
	objectRef, err := managedagents.GetObjectRefWithContext(ctx, e.Store, snapshot.ObjectRefID)
	if err != nil {
		return err
	}
	object, err := e.ObjectStore.GetObject(ctx, objectstore.GetObjectInput{Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion})
	if err != nil {
		return fmt.Errorf("download workspace snapshot: %w", err)
	}
	defer object.Body.Close()
	archive, err := io.ReadAll(io.LimitReader(object.Body, snapshot.SizeBytes+1))
	if err != nil {
		return err
	}
	if int64(len(archive)) != snapshot.SizeBytes {
		return fmt.Errorf("workspace snapshot size mismatch")
	}
	checksum := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(checksum[:]), snapshot.ChecksumSHA256) {
		return fmt.Errorf("workspace snapshot checksum mismatch")
	}
	if err := runtime.RestoreWorkspaceSnapshot(ctx, archive); err != nil {
		return fmt.Errorf("restore workspace snapshot: %w", err)
	}
	_ = config
	return nil
}

func (e AgentRuntimeTurnExecutor) checkpointWorkspaceSnapshot(ctx context.Context, provider capability.Provider, config managedagents.AgentRuntimeConfig, request TurnRequest) error {
	runtime, ok := provider.(capability.WorkspaceSnapshotProvider)
	store, storeOK := e.Store.(managedagents.WorkspaceSnapshotStore)
	if !ok || !storeOK || e.ObjectStore == nil {
		return nil
	}
	archive, fileCount, err := runtime.CreateWorkspaceSnapshot(ctx)
	if err != nil {
		return err
	}
	checksumBytes := sha256.Sum256(archive)
	checksum := hex.EncodeToString(checksumBytes[:])
	bucket := strings.TrimSpace(e.ArtifactBucket)
	if bucket == "" {
		bucket = "tma-artifacts"
	}
	key := fmt.Sprintf("%s/%s/workspace-snapshots/%s.tar", config.WorkspaceID, request.SessionID, checksum)
	put, err := e.ObjectStore.PutObject(ctx, objectstore.PutObjectInput{Bucket: bucket, Key: key, Body: bytes.NewReader(archive), ContentType: "application/x-tar", SizeBytes: int64(len(archive)), ChecksumSHA256: checksum})
	if err != nil {
		return err
	}
	objectRef, err := managedagents.CreateObjectRefWithContext(ctx, e.Store, managedagents.CreateObjectRefInput{
		WorkspaceID: config.WorkspaceID, StorageProvider: managedagents.ObjectStorageProviderS3,
		Bucket: defaultStringRunner(put.Bucket, bucket), ObjectKey: defaultStringRunner(put.Key, key), ObjectVersion: put.Version,
		ContentType: "application/x-tar", SizeBytes: int64(len(archive)), ChecksumSHA256: checksum, ETag: put.ETag,
		Visibility: managedagents.ObjectVisibilitySession, CreatedBy: config.OwnerID,
	})
	if err != nil {
		return err
	}
	_, err = store.CreateWorkspaceSnapshot(ctx, managedagents.CreateWorkspaceSnapshotInput{SessionID: request.SessionID, ObjectRefID: objectRef.ID, ChecksumSHA256: checksum, SizeBytes: int64(len(archive)), FileCount: fileCount, CreatedBy: config.OwnerID})
	return err
}

func defaultStringRunner(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

type runtimeSkillMaterialization struct {
	directories       map[string]string
	compatibilityDirs []string
}

func (e AgentRuntimeTurnExecutor) materializeResolvedSkills(ctx context.Context, provider capability.Provider, resolved skills.ResolveResult) (runtimeSkillMaterialization, error) {
	materializer, ok := provider.(capability.RuntimeSkillMaterializer)
	if !ok || len(resolved.Skills) == 0 {
		return runtimeSkillMaterialization{}, nil
	}
	packages := make([]capability.RuntimeSkillPackage, 0, len(resolved.Skills))
	materializedHits := make([]capability.MaterializedRuntimeSkill, 0, len(resolved.Skills))
	compatibilityIdentifiers := make(map[string]bool)
	for _, item := range resolved.Skills {
		if item.Status != skills.UsageResolved && item.Status != skills.UsageDegraded {
			continue
		}
		checksum := strings.TrimSpace(item.Version.PackageChecksum)
		if checksum == "" {
			checksum = strings.TrimSpace(item.Version.Checksum)
		}
		if cache, ok := provider.(capability.RuntimeSkillCache); ok {
			cached, hit, err := cache.LookupMaterializedRuntimeSkill(ctx, item.Skill.ID, item.Skill.Identifier, item.Version.Version, checksum)
			if err != nil {
				return runtimeSkillMaterialization{}, fmt.Errorf("lookup runtime skill %s cache: %w", item.Skill.Identifier, err)
			}
			if hit {
				materializedHits = append(materializedHits, cached)
				if strings.Contains(item.Version.ContentText, "CLAUDE_SKILL_DIR") || strings.Contains(item.Version.ContentText, "TMA_SKILL_DIR") {
					compatibilityIdentifiers[item.Skill.Identifier] = true
				}
				continue
			}
		}
		bundle, err := skills.DecodeAssetBundle(item.Version.Assets)
		if err != nil {
			return runtimeSkillMaterialization{}, fmt.Errorf("decode runtime skill %s assets: %w", item.Skill.Identifier, err)
		}
		files := make([]capability.RuntimeSkillFile, 0, len(bundle.Files)+1)
		files = append(files, capability.RuntimeSkillFile{Path: "SKILL.md", Content: []byte(item.Version.ContentText)})
		for _, asset := range bundle.Files {
			content, err := e.runtimeSkillAssetContent(ctx, item.Skill.WorkspaceID, asset)
			if err != nil {
				return runtimeSkillMaterialization{}, fmt.Errorf("load runtime skill %s asset %s: %w", item.Skill.Identifier, asset.Path, err)
			}
			files = append(files, capability.RuntimeSkillFile{Path: asset.Path, Content: content, Executable: asset.Executable})
		}
		packages = append(packages, capability.RuntimeSkillPackage{
			SkillID: item.Skill.ID, Identifier: item.Skill.Identifier, Version: item.Version.Version, Checksum: checksum, Files: files,
		})
		if strings.Contains(item.Version.ContentText, "CLAUDE_SKILL_DIR") || strings.Contains(item.Version.ContentText, "TMA_SKILL_DIR") {
			compatibilityIdentifiers[item.Skill.Identifier] = true
		}
	}
	if len(packages) == 0 && len(materializedHits) == 0 {
		return runtimeSkillMaterialization{}, nil
	}
	materialized := materializedHits
	if len(packages) > 0 {
		cold, err := materializer.MaterializeRuntimeSkills(ctx, packages)
		if err != nil {
			return runtimeSkillMaterialization{}, fmt.Errorf("materialize runtime skills: %w", err)
		}
		materialized = append(materialized, cold...)
	}
	result := runtimeSkillMaterialization{directories: make(map[string]string, len(materialized)*2)}
	for _, item := range materialized {
		result.directories[item.Identifier] = item.Directory
		if item.SkillID != "" {
			result.directories[item.SkillID] = item.Directory
		}
		if compatibilityIdentifiers[item.Identifier] {
			result.compatibilityDirs = append(result.compatibilityDirs, item.Directory)
		}
	}
	return result, nil
}

func (e AgentRuntimeTurnExecutor) runtimeSkillAssetContent(ctx context.Context, workspaceID string, asset skills.AssetFile) ([]byte, error) {
	if !asset.Binary {
		return []byte(asset.Content), nil
	}
	var content []byte
	if strings.TrimSpace(asset.ContentBase64) != "" {
		decoded, err := base64.StdEncoding.DecodeString(asset.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("decode inline binary asset: %w", err)
		}
		content = decoded
	} else {
		if e.Store == nil || e.ObjectStore == nil || strings.TrimSpace(asset.ObjectRefID) == "" {
			return nil, errors.New("binary skill asset storage is unavailable")
		}
		objectRef, err := managedagents.GetObjectRefWithContext(ctx, e.Store, asset.ObjectRefID)
		if err != nil {
			return nil, err
		}
		if objectRef.WorkspaceID != workspaceID {
			return nil, managedagents.ErrForbidden
		}
		object, err := e.ObjectStore.GetObject(ctx, objectstore.GetObjectInput{Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion})
		if err != nil {
			return nil, err
		}
		defer object.Body.Close()
		content, err = io.ReadAll(io.LimitReader(object.Body, int64(asset.Size)+1))
		if err != nil {
			return nil, err
		}
	}
	if len(content) != asset.Size {
		return nil, fmt.Errorf("binary skill asset size mismatch: expected %d, got %d", asset.Size, len(content))
	}
	checksum := sha256.Sum256(content)
	if !strings.EqualFold(asset.ChecksumSHA256, fmt.Sprintf("%x", checksum)) {
		return nil, errors.New("binary skill asset checksum mismatch")
	}
	return content, nil
}

func mergeRuntimeSkillEnvironment(environment map[string]string, materialized runtimeSkillMaterialization) map[string]string {
	result := make(map[string]string, len(environment)+len(materialized.directories)+3)
	for key, value := range environment {
		result[key] = value
	}
	result["TMA_SKILLS_DIR"] = "/tma/skills"
	encodedDirectories, _ := json.Marshal(materialized.directories)
	result["TMA_SKILL_DIRS_JSON"] = string(encodedDirectories)
	for identifier, directory := range materialized.directories {
		result[runtimeSkillEnvironmentKey(identifier)] = directory
	}
	if len(materialized.compatibilityDirs) == 1 {
		result["TMA_SKILL_DIR"] = materialized.compatibilityDirs[0]
		result["CLAUDE_SKILL_DIR"] = materialized.compatibilityDirs[0]
	}
	return result
}

func runtimeSkillEnvironmentKey(identifier string) string {
	var builder strings.Builder
	builder.WriteString("TMA_SKILL_DIR_")
	for _, char := range strings.ToUpper(identifier) {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func (e AgentRuntimeTurnExecutor) resolveTaskPlanContext(ctx context.Context, sessionID string) (string, error) {
	store, ok := e.Store.(managedagents.SessionTaskPlanStore)
	if !ok {
		return "", nil
	}
	plan, err := store.GetCurrentSessionTaskPlanContext(ctx, sessionID)
	if errors.Is(err, managedagents.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve current task plan: %w", err)
	}
	lines := []string{
		"Current task plan (protected execution state; update only through task_*):",
		fmt.Sprintf("Plan: %s [%s, %s]", plan.ID, plan.HandlingMode, plan.Status),
		"Goal: " + plan.Goal,
	}
	for index, item := range plan.Items {
		line := fmt.Sprintf("%d. [%s] %s (item_id=%s)", index+1, item.Status, item.Description, item.ID)
		if evidence := strings.TrimSpace(item.Evidence); evidence != "" {
			line += " Evidence: " + evidence
		}
		if len(item.EvidenceRefs) > 0 {
			refs := make([]string, 0, len(item.EvidenceRefs))
			for _, ref := range item.EvidenceRefs {
				refs = append(refs, fmt.Sprintf("%s@%s/%s", ref.Tool, ref.TurnID, ref.ToolCallID))
			}
			line += " Verified refs: " + strings.Join(refs, ", ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func conversationHistoryAfterSeq(history []managedagents.ConversationMessage, seq int64) []managedagents.ConversationMessage {
	filtered := make([]managedagents.ConversationMessage, 0, len(history))
	for _, message := range history {
		if message.Seq > seq {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func redactConversationHistoryEnvironment(history []managedagents.ConversationMessage, environment map[string]string) []managedagents.ConversationMessage {
	if !tools.HasSensitiveEnvironment(environment) {
		return history
	}
	result := append([]managedagents.ConversationMessage(nil), history...)
	for index := range result {
		result[index].Payload = tools.RedactEnvironmentJSON(result[index].Payload, environment)
	}
	return result
}

func (e AgentRuntimeTurnExecutor) loadUserImageParts(ctx context.Context, payload json.RawMessage, workspaceID string) ([]llm.ContentPart, error) {
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		return nil, err
	}
	var message struct {
		Attachments []struct {
			ObjectRefID string `json:"object_ref_id"`
			ContentType string `json:"content_type"`
			Name        string `json:"name"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, nil
	}
	parts := make([]llm.ContentPart, 0, len(message.Attachments))
	totalBytes := 0
	for _, attachment := range message.Attachments {
		contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
		if !strings.HasPrefix(contentType, "image/") {
			continue
		}
		if !supportedVisionImageType(contentType) {
			return nil, fmt.Errorf("unsupported vision image type %s for %s", contentType, attachment.Name)
		}
		if e.Store == nil || e.ObjectStore == nil || strings.TrimSpace(attachment.ObjectRefID) == "" {
			return nil, errors.New("image attachment storage is unavailable")
		}
		objectRef, err := managedagents.GetObjectRefWithContext(databaseCtx, e.Store, attachment.ObjectRefID)
		if err != nil {
			return nil, fmt.Errorf("load image object ref %s: %w", attachment.ObjectRefID, err)
		}
		if objectRef.WorkspaceID != "" && workspaceID != "" && objectRef.WorkspaceID != workspaceID {
			return nil, errors.New("image attachment workspace mismatch")
		}
		if supportedVisionImageType(strings.ToLower(strings.TrimSpace(objectRef.ContentType))) {
			contentType = strings.ToLower(strings.TrimSpace(objectRef.ContentType))
		}
		object, err := e.ObjectStore.GetObject(ctx, objectstore.GetObjectInput{Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion})
		if err != nil {
			return nil, fmt.Errorf("download image attachment %s: %w", attachment.Name, err)
		}
		content, readErr := io.ReadAll(io.LimitReader(object.Body, maxVisionImageBytes+1))
		_ = object.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read image attachment %s: %w", attachment.Name, readErr)
		}
		if len(content) > maxVisionImageBytes {
			return nil, fmt.Errorf("image attachment %s exceeds vision limit of 20 MB", attachment.Name)
		}
		totalBytes += len(content)
		if totalBytes > 40<<20 {
			return nil, errors.New("image attachments exceed total vision limit of 40 MB")
		}
		detectedType := strings.ToLower(strings.TrimSpace(http.DetectContentType(content)))
		if !supportedVisionImageType(detectedType) {
			return nil, fmt.Errorf("image attachment %s content does not match a supported image format", attachment.Name)
		}
		contentType = detectedType
		parts = append(parts, llm.ContentPart{Type: "image_url", ImageURL: &llm.ImageURL{
			URL: "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(content), Detail: "auto",
		}})
	}
	return parts, nil
}

func supportedVisionImageType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func (e AgentRuntimeTurnExecutor) resolveSkills(ctx context.Context, request TurnRequest, config managedagents.AgentRuntimeConfig, emit func(context.Context, agentruntime.Step) error) (skills.ResolveResult, error) {
	if len(config.Skills) == 0 || string(config.Skills) == "null" {
		return skills.ResolveResult{}, nil
	}
	if err := emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsResolving, Message: "Resolving agent skills."}); err != nil {
		return skills.ResolveResult{}, err
	}
	registry, ok := e.Store.(skills.Registry)
	if !ok {
		err := errors.New("skill registry is unavailable")
		_ = emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsFailed, Message: err.Error()})
		return skills.ResolveResult{}, err
	}
	maxTokens := skillContextBudget(config.ContextWindowTokens)
	result, err := skills.ResolveRegistry(ctx, registry, config.WorkspaceID, config.Skills, maxTokens)
	if err != nil {
		legacy, legacyErr := skills.Resolve(config.Skills)
		if legacyErr == nil && isLegacySkillsResult(legacy) {
			legacy.EstimatedTokens = estimateSkillTokens(legacy.Rendered)
			if emitErr := emit(ctx, agentruntime.Step{
				Type: managedagents.EventRuntimeSkillsResolved, Message: "Using legacy skills context.",
				Data: map[string]any{"legacy_passthrough": true, "estimated_tokens": legacy.EstimatedTokens},
			}); emitErr != nil {
				return skills.ResolveResult{}, emitErr
			}
			return legacy, nil
		}
		wrapped := fmt.Errorf("resolve runtime skills: %w", err)
		_ = emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsFailed, Message: wrapped.Error()})
		return skills.ResolveResult{}, wrapped
	}
	usages := make([]skills.Usage, 0, len(result.Skills))
	for _, resolved := range result.Skills {
		usages = append(usages, skills.Usage{
			WorkspaceID: config.WorkspaceID, SessionID: request.SessionID, TurnID: request.TurnID,
			AgentID: config.AgentID, AgentConfigVersion: config.AgentConfigVersion,
			SkillID: resolved.Skill.ID, SkillIdentifier: resolved.Skill.Identifier, SkillVersion: resolved.Version.Version,
			RequestedMode: resolved.RequestedMode, RenderedMode: resolved.RenderedMode, Priority: resolved.Priority,
			EstimatedTokens: resolved.EstimatedTokens, Status: resolved.Status, FailureReason: resolved.FailureReason,
		})
	}
	if recorder, ok := e.Store.(skills.UsageRecorder); ok {
		if err := recorder.RecordSkillUsages(ctx, usages); err != nil {
			wrapped := fmt.Errorf("record skill usages: %w", err)
			_ = emit(ctx, agentruntime.Step{Type: managedagents.EventRuntimeSkillsFailed, Message: wrapped.Error()})
			return skills.ResolveResult{}, wrapped
		}
	}
	eventType := managedagents.EventRuntimeSkillsResolved
	message := "Agent skills resolved."
	if result.Truncated {
		eventType = managedagents.EventRuntimeSkillsTruncated
		message = "Agent skills resolved with budget degradation."
	}
	if err := emit(ctx, agentruntime.Step{
		Type: eventType, Message: message,
		Data: map[string]any{"skills": skillResolutionEventItems(result.Skills), "estimated_tokens": result.EstimatedTokens, "max_tokens": maxTokens},
	}); err != nil {
		return skills.ResolveResult{}, err
	}
	return result, nil
}

func skillResolutionEventItems(resolved []skills.ResolvedSkill) []map[string]any {
	items := make([]map[string]any, 0, len(resolved))
	for _, item := range resolved {
		items = append(items, map[string]any{
			"skill_id":         item.Skill.ID,
			"identifier":       item.Skill.Identifier,
			"version_id":       item.Version.ID,
			"version":          item.Version.Version,
			"requested_mode":   item.RequestedMode,
			"rendered_mode":    item.RenderedMode,
			"priority":         item.Priority,
			"estimated_tokens": item.EstimatedTokens,
			"status":           item.Status,
			"failure_reason":   item.FailureReason,
		})
	}
	return items
}

func isLegacySkillsResult(result skills.ResolveResult) bool {
	if result.LegacyPassthrough {
		return true
	}
	for _, enabled := range result.Config.Enabled {
		if enabled.Version <= 0 {
			return true
		}
	}
	return false
}

func skillContextBudget(contextWindowTokens int) int {
	if contextWindowTokens <= 0 {
		contextWindowTokens = managedagents.DefaultContextWindowTokens
	}
	budget := contextWindowTokens / 10
	if budget > 16000 {
		return 16000
	}
	if budget < 512 {
		return 512
	}
	return budget
}

func estimateSkillTokens(raw json.RawMessage) int {
	return tokenestimate.Text(string(raw))
}

func (e AgentRuntimeTurnExecutor) resolveRuntimeConfig(ctx context.Context, sessionID string) (managedagents.AgentRuntimeConfig, error) {
	if e.Store == nil {
		return managedagents.AgentRuntimeConfig{}, nil
	}
	config, err := managedagents.ResolveAgentRuntimeConfigWithContext(ctx, e.Store, sessionID)
	if err != nil {
		return managedagents.AgentRuntimeConfig{}, err
	}
	if registry, ok := e.Store.(mcpregistry.Store); ok {
		_, resolved, resolveErr := mcpregistry.PinAndResolve(ctx, registry, config.WorkspaceID, config.MCP)
		if resolveErr != nil {
			return managedagents.AgentRuntimeConfig{}, fmt.Errorf("resolve MCP registry bindings: %w", resolveErr)
		}
		config.MCP = resolved
	}
	return config, nil
}

func (e AgentRuntimeTurnExecutor) resolveConversationHistory(ctx context.Context, sessionID string, beforeSeq int64) ([]managedagents.ConversationMessage, error) {
	if e.Store == nil || beforeSeq <= 0 {
		return nil, nil
	}
	return managedagents.ListConversationMessagesWithContext(ctx, e.Store, sessionID, beforeSeq)
}

func llmAPIKey(envName string) string {
	if envName == "" {
		return ""
	}
	return os.Getenv(envName)
}

func (e AgentRuntimeTurnExecutor) emitStep(request TurnRequest) func(context.Context, agentruntime.Step) error {
	traceState := newRuntimeTraceState(request.SessionID, request.TurnID)
	return func(ctx context.Context, step agentruntime.Step) error {
		if e.Store == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		eventType := step.Type
		if eventType == "" {
			return errors.New("runtime step type is required")
		}
		if step.Data == nil {
			step.Data = map[string]any{}
		}
		traceState.decorate(eventType, step.Data)
		payload, err := json.Marshal(map[string]any{
			"trace_id":       step.Data["trace_id"],
			"span_id":        step.Data["span_id"],
			"parent_span_id": step.Data["parent_span_id"],
			"span_name":      step.Data["span_name"],
			"span_kind":      step.Data["span_kind"],
			"span_status":    step.Data["span_status"],
			"duration_ms":    step.Data["duration_ms"],
			"message":        step.Message,
			"data":           step.Data,
		})
		if err != nil {
			return fmt.Errorf("encode runtime step: %w", err)
		}
		_, err = managedagents.AppendRuntimeEventWithContext(ctx, e.Store, request.SessionID, request.TurnID, managedagents.AppendEventInput{
			Type:    eventType,
			Payload: payload,
		})
		if err == nil {
			validator, _ := step.Data["validator"].(string)
			observability.RecordCompletionValidation(eventType, validator)
			if eventType == managedagents.EventRuntimeToolResult {
				recordFilesystemToolMetric(step.Data)
			}
		}
		return err
	}
}

func recordFilesystemToolMetric(data map[string]any) {
	api, _ := data["api_name"].(string)
	identifier, _ := data["identifier"].(string)
	if identifier != "" && identifier != tools.NamespaceDefault {
		return
	}
	outcome := "error"
	if success, _ := data["success"].(bool); success {
		outcome = "success"
	} else if pending, _ := data["pending_intervention"].(bool); pending {
		outcome = "pending_intervention"
	}
	errorCode := ""
	if executionError, ok := data["error"].(*tools.ExecutionError); ok && executionError != nil {
		errorCode = executionError.Type
	}
	state, _ := data["state"].(map[string]any)
	observability.RecordFilesystemToolMetric(observability.FilesystemToolMetricInput{
		API: api, Outcome: outcome, ErrorCode: errorCode,
		DurationMillis: runtimeMetricInt64(data["duration_ms"]), State: state,
	})
}

func runtimeMetricInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	default:
		return 0
	}
}

type runtimeTraceState struct {
	sessionID        string
	turnID           string
	interactionStart time.Time
	llmStart         map[string]time.Time
	toolStart        map[string]time.Time
	contextStart     time.Time
	approvalStart    map[string]time.Time
}

func newRuntimeTraceState(sessionID string, turnID string) *runtimeTraceState {
	return &runtimeTraceState{
		sessionID:     sessionID,
		turnID:        turnID,
		llmStart:      map[string]time.Time{},
		toolStart:     map[string]time.Time{},
		approvalStart: map[string]time.Time{},
	}
}

func (s *runtimeTraceState) decorate(eventType string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	now := time.Now()
	callID, _ := data["id"].(string)
	identifier, _ := data["identifier"].(string)
	apiName, _ := data["api_name"].(string)
	roundKey := fmt.Sprintf("%v", data["tool_round"])
	if roundKey == "<nil>" {
		roundKey = "0"
	}
	status := spanStatusForRuntimeEvent(eventType, data)
	duration := time.Duration(0)
	switch eventType {
	case managedagents.EventRuntimeStarted:
		s.interactionStart = now
	case managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		if !s.interactionStart.IsZero() {
			duration = now.Sub(s.interactionStart)
		}
	case managedagents.EventRuntimeLLMRequest:
		s.llmStart[roundKey] = now
	case managedagents.EventRuntimeLLMResponse:
		if startedAt, ok := s.llmStart[roundKey]; ok {
			duration = now.Sub(startedAt)
			delete(s.llmStart, roundKey)
		}
	case managedagents.EventRuntimeToolCall:
		if callID != "" {
			s.toolStart[callID] = now
		}
	case managedagents.EventRuntimeToolResult:
		if startedAt, ok := s.toolStart[callID]; ok {
			duration = now.Sub(startedAt)
			delete(s.toolStart, callID)
		}
	case managedagents.EventRuntimeContextCompacting:
		s.contextStart = now
	case managedagents.EventRuntimeContextCompacted, managedagents.EventRuntimeContextCompactionFailed:
		if !s.contextStart.IsZero() {
			duration = now.Sub(s.contextStart)
			s.contextStart = time.Time{}
		}
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeHumanInputRequired, managedagents.EventRuntimePlanApprovalRequired:
		if callID != "" {
			s.approvalStart[callID] = now
		}
	case managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected, managedagents.EventRuntimeHumanInputSubmitted, managedagents.EventRuntimeHumanInputSkipped, managedagents.EventRuntimeHumanInputCanceled, managedagents.EventRuntimePlanApprovalApproved, managedagents.EventRuntimePlanApprovalRejected:
		if startedAt, ok := s.approvalStart[callID]; ok {
			duration = now.Sub(startedAt)
			delete(s.approvalStart, callID)
		}
	}
	interactionEvent := eventType == managedagents.EventRuntimeStarted || eventType == managedagents.EventRuntimeCompleted || eventType == managedagents.EventRuntimeFailed
	fields := observability.EventTraceFields(observability.EventTraceFieldsInput{
		SessionID:       s.sessionID,
		TurnID:          s.turnID,
		EventType:       eventType,
		CallID:          defaultString(callID, roundKey),
		Identifier:      identifier,
		APIName:         apiName,
		Status:          status,
		Duration:        duration,
		ParentSpanID:    parentSpanForRuntimeEvent(s.turnID, eventType, callID),
		InteractionRoot: interactionEvent,
	})
	for key, value := range fields {
		data[key] = value
	}
}

func spanStatusForRuntimeEvent(eventType string, data map[string]any) string {
	switch eventType {
	case managedagents.EventRuntimeStarted:
		return "running"
	case managedagents.EventRuntimeCompleted, managedagents.EventRuntimeLLMResponse:
		return "ok"
	case managedagents.EventRuntimeFailed, managedagents.EventRuntimeContextCompactionFailed:
		return "error"
	case managedagents.EventRuntimeToolResult:
		if success, ok := data["success"].(bool); ok && success {
			return "ok"
		}
		return "error"
	case managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeHumanInputSubmitted, managedagents.EventRuntimePlanApprovalApproved:
		return "approved"
	case managedagents.EventRuntimeToolInterventionRejected, managedagents.EventRuntimeHumanInputSkipped, managedagents.EventRuntimeHumanInputCanceled, managedagents.EventRuntimePlanApprovalRejected:
		return "rejected"
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeHumanInputRequired, managedagents.EventRuntimePlanApprovalRequired:
		return "waiting"
	default:
		return "point"
	}
}

func parentSpanForRuntimeEvent(turnID string, eventType string, callID string) string {
	switch eventType {
	case managedagents.EventRuntimeStarted, managedagents.EventRuntimeCompleted, managedagents.EventRuntimeFailed:
		return ""
	case managedagents.EventRuntimeToolInterventionRequired, managedagents.EventRuntimeToolInterventionApproved, managedagents.EventRuntimeToolInterventionRejected, managedagents.EventRuntimeHumanInputRequired, managedagents.EventRuntimeHumanInputSubmitted, managedagents.EventRuntimeHumanInputSkipped, managedagents.EventRuntimeHumanInputCanceled, managedagents.EventRuntimePlanApprovalRequired, managedagents.EventRuntimePlanApprovalApproved, managedagents.EventRuntimePlanApprovalRejected:
		return observability.ToolSpanID(turnID, callID, 0)
	default:
		return observability.InteractionSpanID(turnID)
	}
}

func (e AgentRuntimeTurnExecutor) recordRuntimeFailed(ctx context.Context, err error, emit func(context.Context, agentruntime.Step) error) error {
	if e.Store == nil || err == nil || ctx.Err() != nil {
		return nil
	}
	data := map[string]any{}
	var providerError *llm.ProviderError
	if errors.As(err, &providerError) {
		data["provider_error"] = map[string]any{
			"class":          providerError.Class,
			"status_code":    providerError.StatusCode,
			"retryable":      providerError.Retryable,
			"retry_after_ms": providerError.RetryAfter.Milliseconds(),
			"attempts":       providerError.Attempts,
			"message":        providerError.Message,
		}
	}
	return emit(ctx, agentruntime.Step{
		Type:    managedagents.EventRuntimeFailed,
		Message: err.Error(),
		Data:    data,
	})
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
