package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/tools"
)

const defaultToolArtifactBucket = "tma-artifacts"

type ToolArtifactRecorder struct {
	Store       managedagents.Store
	ObjectStore objectstore.Client
	Bucket      string
}

type deliverableManifest struct {
	ProtocolVersion string                        `json:"protocol_version"`
	SessionID       string                        `json:"session_id"`
	TurnID          string                        `json:"turn_id"`
	ToolCallID      string                        `json:"tool_call_id"`
	Tool            string                        `json:"tool"`
	GeneratedAt     time.Time                     `json:"generated_at"`
	Deliverables    []deliverableManifestItem     `json:"deliverables"`
	Validation      deliverableManifestValidation `json:"validation"`
}

type deliverableManifestItem struct {
	ArtifactID     string `json:"artifact_id"`
	ObjectRefID    string `json:"object_ref_id"`
	Name           string `json:"name"`
	ArtifactType   string `json:"artifact_type"`
	Path           string `json:"path,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	Validation     string `json:"validation_status"`
}

type deliverableManifestValidation struct {
	Status string   `json:"status"`
	Checks []string `json:"checks"`
}

func (r ToolArtifactRecorder) RecordToolArtifact(ctx context.Context, call tools.Call, executionContext tools.ExecutionContext, result tools.ExecutionResult) ([]tools.ArtifactRef, error) {
	if r.Store == nil || r.ObjectStore == nil || result.PendingIntervention || result.Error != nil {
		return nil, nil
	}

	sessionID := strings.TrimSpace(executionContext.SessionID)
	turnID := strings.TrimSpace(executionContext.TurnID)
	if sessionID == "" || turnID == "" {
		return nil, nil
	}
	if strings.TrimSpace(result.Content) == "" && len(result.State) == 0 {
		if len(result.ExportedFiles) == 0 {
			return nil, nil
		}
	}

	session, err := managedagents.GetSessionWithContext(ctx, r.Store, sessionID)
	if err != nil {
		return nil, err
	}

	bucket := strings.TrimSpace(r.Bucket)
	if bucket == "" {
		if configured, ok := r.ObjectStore.(interface{ Config() objectstore.Config }); ok {
			bucket = strings.TrimSpace(configured.Config().Bucket)
		}
	}
	if bucket == "" {
		bucket = defaultToolArtifactBucket
	}

	var refs []tools.ArtifactRef
	if strings.TrimSpace(result.Content) != "" || len(result.State) > 0 {
		ref, err := r.recordStructuredToolResult(ctx, session, bucket, sessionID, turnID, call, result)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	fileRefs, err := r.recordExportedFiles(ctx, session, bucket, sessionID, turnID, call, executionContext, result.ExportedFiles)
	if err != nil {
		return nil, err
	}
	refs = append(refs, fileRefs...)
	if len(refs) == 0 {
		return nil, nil
	}
	return refs, nil
}

func artifactName(call tools.Call) string {
	name := strings.TrimSpace(call.APIName)
	if name == "" {
		name = "tool_result"
	}
	return sanitizeObjectName(name) + ".json"
}

func toolArtifactDescription(call tools.Call) string {
	return fmt.Sprintf("Tool output for %s", tools.ModelToolName(call.Identifier, call.APIName))
}

func exportedArtifactDescription(call tools.Call, export tools.ArtifactExport) string {
	if description := strings.TrimSpace(export.Description); description != "" {
		return description
	}
	return fmt.Sprintf("Exported file from %s", tools.ModelToolName(call.Identifier, call.APIName))
}

func sanitizeObjectName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "/", "_")
	value = strings.ReplaceAll(value, "\\", "_")
	value = strings.ReplaceAll(value, "..", "_")
	return value
}

func rawJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func (r ToolArtifactRecorder) recordStructuredToolResult(ctx context.Context, session managedagents.Session, bucket string, sessionID string, turnID string, call tools.Call, result tools.ExecutionResult) (tools.ArtifactRef, error) {
	payload := map[string]any{
		"protocol_version": "tma.tool_artifact.v1",
		"session_id":       sessionID,
		"turn_id":          turnID,
		"call":             call,
		"result": map[string]any{
			"id":                   result.ID,
			"identifier":           result.Identifier,
			"api_name":             result.APIName,
			"content":              result.Content,
			"state":                rawJSON(result.State),
			"exported_files":       result.ExportedFiles,
			"artifacts":            result.Artifacts,
			"artifact_error":       result.ArtifactError,
			"pending_intervention": result.PendingIntervention,
			"error":                result.Error,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return tools.ArtifactRef{}, err
	}

	toolName := sanitizeObjectName(result.Identifier + "_" + result.APIName)
	if toolName == "" {
		toolName = "tool_result"
	}
	objectKey := path.Join(session.WorkspaceID, sessionID, "tool-results", fmt.Sprintf("%s-%s.json", toolName, time.Now().UTC().Format("20060102T150405.000000000Z07")))
	objectRef, artifact, err := r.persistArtifactObject(ctx, session, bucket, objectKey, encoded, "application/json", managedagents.ArtifactTypeAsset, artifactName(call), toolArtifactDescription(call), call, sessionID, turnID, json.RawMessage(fmt.Sprintf(`{"protocol_version":"tma.tool_artifact.v1","tool":%q}`,
		tools.ModelToolName(call.Identifier, call.APIName))))
	if err != nil {
		return tools.ArtifactRef{}, err
	}
	return artifactRef(sessionID, artifact, objectRef), nil
}

func (r ToolArtifactRecorder) recordExportedFiles(ctx context.Context, session managedagents.Session, bucket string, sessionID string, turnID string, call tools.Call, executionContext tools.ExecutionContext, exports []tools.ArtifactExport) ([]tools.ArtifactRef, error) {
	if len(exports) == 0 {
		return nil, nil
	}
	var exporter capability.ArtifactExportProvider
	if provider, ok := executionContext.Provider.(capability.ArtifactExportProvider); ok {
		exporter = provider
	}
	refs := make([]tools.ArtifactRef, 0, len(exports))
	manifestItems := make([]deliverableManifestItem, 0, len(exports))
	for index, export := range exports {
		file, err := exportedArtifactFile(ctx, exporter, export)
		if err != nil {
			return nil, err
		}
		contentType := strings.TrimSpace(file.ContentType)
		if contentType == "" {
			contentType = http.DetectContentType(file.Content)
		}
		name := strings.TrimSpace(export.Name)
		if name == "" {
			name = strings.TrimSpace(file.Name)
		}
		if name == "" {
			name = filepath.Base(strings.TrimSpace(file.Path))
		}
		name = sanitizeObjectName(name)
		if name == "" {
			name = fmt.Sprintf("artifact-%d", index+1)
		}
		checksum := sha256.Sum256(file.Content)
		checksumHex := hex.EncodeToString(checksum[:])
		artifactType := strings.TrimSpace(export.ArtifactType)
		if artifactType == "" {
			artifactType = managedagents.ArtifactTypeFile
		}
		objectKey := path.Join(session.WorkspaceID, sessionID, "tool-exports", sanitizeObjectName(call.Identifier+"_"+call.APIName), fmt.Sprintf("%s-%02d-%s", time.Now().UTC().Format("20060102T150405.000000000Z07"), index+1, name))
		metadata, err := exportedArtifactMetadata(sessionID, turnID, call, export, contentType, int64(len(file.Content)), checksumHex)
		if err != nil {
			return nil, err
		}
		objectRef, artifact, err := r.persistArtifactObject(ctx, session, bucket, objectKey, file.Content, contentType, artifactType, name, exportedArtifactDescription(call, export), call, sessionID, turnID, metadata)
		if err != nil {
			return nil, err
		}
		ref := artifactRef(sessionID, artifact, objectRef)
		refs = append(refs, ref)
		manifestItems = append(manifestItems, deliverableManifestItem{
			ArtifactID:     artifact.ID,
			ObjectRefID:    objectRef.ID,
			Name:           artifact.Name,
			ArtifactType:   artifact.ArtifactType,
			Path:           export.Path,
			ContentType:    contentType,
			SizeBytes:      int64(len(file.Content)),
			ChecksumSHA256: checksumHex,
			Validation:     "passed",
		})
	}
	manifestRef, err := r.recordDeliverableManifest(ctx, session, bucket, sessionID, turnID, call, manifestItems)
	if err != nil {
		return nil, err
	}
	if manifestRef.ArtifactID != "" {
		refs = append(refs, manifestRef)
	}
	return refs, nil
}

func exportedArtifactMetadata(sessionID string, turnID string, call tools.Call, export tools.ArtifactExport, contentType string, sizeBytes int64, checksumSHA256 string) (json.RawMessage, error) {
	payload := map[string]any{
		"protocol_version": "tma.tool_export.v1",
		"tool":             tools.ModelToolName(call.Identifier, call.APIName),
		"path":             export.Path,
		"lineage": map[string]any{
			"kind":         "tool_export",
			"session_id":   sessionID,
			"turn_id":      turnID,
			"tool_call_id": call.ID,
			"tool":         tools.ModelToolName(call.Identifier, call.APIName),
			"source_path":  export.Path,
		},
		"template": map[string]any{
			"status":           "unbound",
			"template_id":      "",
			"template_version": "",
		},
		"validation": map[string]any{
			"status":          "passed",
			"content_type":    contentType,
			"size_bytes":      sizeBytes,
			"checksum_sha256": checksumSHA256,
			"checks": []map[string]string{
				{"name": "artifact_export_read", "status": "passed"},
				{"name": "object_checksum", "status": "passed"},
			},
		},
	}
	return json.Marshal(payload)
}

func (r ToolArtifactRecorder) recordDeliverableManifest(ctx context.Context, session managedagents.Session, bucket string, sessionID string, turnID string, call tools.Call, items []deliverableManifestItem) (tools.ArtifactRef, error) {
	if len(items) == 0 {
		return tools.ArtifactRef{}, nil
	}
	manifest := deliverableManifest{
		ProtocolVersion: "tma.deliverable_manifest.v1",
		SessionID:       sessionID,
		TurnID:          turnID,
		ToolCallID:      call.ID,
		Tool:            tools.ModelToolName(call.Identifier, call.APIName),
		GeneratedAt:     time.Now().UTC(),
		Deliverables:    items,
		Validation: deliverableManifestValidation{
			Status: "passed",
			Checks: []string{"artifact_export_read", "object_checksum"},
		},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return tools.ArtifactRef{}, err
	}
	metadata := json.RawMessage(fmt.Sprintf(`{"protocol_version":"tma.deliverable_manifest.v1","tool":%q,"lineage":{"kind":"deliverable_manifest","session_id":%q,"turn_id":%q,"tool_call_id":%q},"validation":{"status":"passed"}}`,
		tools.ModelToolName(call.Identifier, call.APIName), sessionID, turnID, call.ID))
	objectKey := path.Join(session.WorkspaceID, sessionID, "deliverable-manifests", fmt.Sprintf("%s-%s.json", sanitizeObjectName(call.Identifier+"_"+call.APIName), time.Now().UTC().Format("20060102T150405.000000000Z07")))
	objectRef, artifact, err := r.persistArtifactObject(ctx, session, bucket, objectKey, encoded, "application/json", managedagents.ArtifactTypeAsset, "deliverable-manifest.json", "Deliverable manifest for exported files", call, sessionID, turnID, metadata)
	if err != nil {
		return tools.ArtifactRef{}, err
	}
	return artifactRef(sessionID, artifact, objectRef), nil
}

func exportedArtifactFile(ctx context.Context, exporter capability.ArtifactExportProvider, export tools.ArtifactExport) (capability.ExportArtifactFileResult, error) {
	if len(export.Content) > 0 {
		return capability.ExportArtifactFileResult{
			Path:        export.Path,
			Name:        export.Name,
			ContentType: export.ContentType,
			Content:     append([]byte(nil), export.Content...),
		}, nil
	}
	if exporter == nil {
		return capability.ExportArtifactFileResult{}, fmt.Errorf("tool runtime does not support artifact export for %q", export.Path)
	}
	file, err := exporter.ExportArtifactFile(ctx, capability.ExportArtifactFileRequest{
		Path:    export.Path,
		WorkDir: export.WorkDir,
	})
	if err != nil {
		return capability.ExportArtifactFileResult{}, fmt.Errorf("export tool artifact %q: %w", export.Path, err)
	}
	return file, nil
}

func (r ToolArtifactRecorder) persistArtifactObject(ctx context.Context, session managedagents.Session, bucket string, objectKey string, content []byte, contentType string, artifactType string, artifactName string, description string, call tools.Call, sessionID string, turnID string, metadata json.RawMessage) (managedagents.ObjectRef, managedagents.SessionArtifact, error) {
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		return managedagents.ObjectRef{}, managedagents.SessionArtifact{}, err
	}
	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])
	return managedagents.PersistSessionArtifactObject(databaseCtx, r.Store, r.ObjectStore, managedagents.PersistSessionArtifactObjectInput{
		DeleteObjectOnFailure: true,
		PutObject: objectstore.PutObjectInput{
			Bucket:         bucket,
			Key:            objectKey,
			Body:           bytes.NewReader(content),
			ContentType:    contentType,
			SizeBytes:      int64(len(content)),
			ChecksumSHA256: checksumHex,
			Metadata: map[string]string{
				"session_id": sessionID,
				"turn_id":    turnID,
				"call_id":    call.ID,
				"tool":       tools.ModelToolName(call.Identifier, call.APIName),
			},
		},
		ObjectRef: managedagents.CreateObjectRefInput{
			WorkspaceID: session.WorkspaceID,
			ContentType: contentType,
			SizeBytes:   int64(len(content)),
			Visibility:  managedagents.ObjectVisibilitySession,
			Metadata:    metadata,
			CreatedBy:   "system",
		},
		SessionArtifact: managedagents.CreateSessionArtifactInput{
			WorkspaceID:   session.WorkspaceID,
			SessionID:     sessionID,
			EnvironmentID: session.EnvironmentID,
			TurnID:        turnID,
			ToolCallID:    call.ID,
			Name:          artifactName,
			Description:   description,
			ArtifactType:  artifactType,
			Metadata:      metadata,
			CreatedBy:     "system",
		},
	})
}

func artifactRef(sessionID string, artifact managedagents.SessionArtifact, objectRef managedagents.ObjectRef) tools.ArtifactRef {
	return tools.ArtifactRef{
		ArtifactID:   artifact.ID,
		ObjectRefID:  objectRef.ID,
		Name:         artifact.Name,
		ArtifactType: artifact.ArtifactType,
		DownloadPath: "/v1/sessions/" + sessionID + "/artifacts/" + artifact.ID + "/download",
	}
}
