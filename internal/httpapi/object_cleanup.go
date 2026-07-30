package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/capability"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectcleanup"
	"tiggy-manage-agent/internal/objectreconcile"
	"tiggy-manage-agent/internal/objectstore"
)

type approveObjectCleanupRequest struct {
	Confirm string `json:"confirm"`
}

const objectReconciliationReportProtocolVersion = "tma.object_reconciliation_report.v1"

type exportObjectReconciliationArtifactRequest struct {
	objectreconcile.PreviewInput
	SessionID string `json:"session_id"`
	Name      string `json:"name,omitempty"`
}

type exportObjectReconciliationArtifactResponse struct {
	Report        objectreconcile.Report        `json:"report"`
	ObjectRef     managedagents.ObjectRef       `json:"object_ref"`
	Artifact      managedagents.SessionArtifact `json:"artifact"`
	WorkspacePath string                        `json:"workspace_path"`
}

func (s *Server) objectCleanupOperationsStore() (objectcleanup.OperationsStore, error) {
	store, ok := s.store.(objectcleanup.OperationsStore)
	if !ok {
		return nil, fmt.Errorf("%w: object cleanup operations are unavailable", managedagents.ErrConflict)
	}
	return store, nil
}

func (s *Server) previewObjectReconciliation(w http.ResponseWriter, r *http.Request) {
	var input objectreconcile.PreviewInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	report, err := s.runObjectReconciliation(r, input)
	if errors.Is(err, objectreconcile.ErrInvalid) {
		err = fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	s.recordObjectReconciliationAudit(r, input, report, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) exportObjectReconciliationArtifact(w http.ResponseWriter, r *http.Request) {
	var request exportObjectReconciliationArtifactRequest
	if err := decodeJSON(r, &request); err != nil {
		err = fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
		request.WorkspaceID = requestWorkspaceID(r, request.WorkspaceID)
		s.recordObjectReconciliationArtifactAudit(r, request, objectreconcile.Report{}, managedagents.SessionArtifact{}, err)
		writeError(w, err)
		return
	}
	request.WorkspaceID = requestWorkspaceID(r, request.WorkspaceID)
	request.SessionID = strings.TrimSpace(request.SessionID)
	if request.SessionID == "" {
		err := fmt.Errorf("%w: session_id is required", managedagents.ErrInvalid)
		s.recordObjectReconciliationArtifactAudit(r, request, objectreconcile.Report{}, managedagents.SessionArtifact{}, err)
		writeError(w, err)
		return
	}

	session, err := s.getSessionForRequest(r, request.SessionID)
	if err == nil && session.WorkspaceID != request.WorkspaceID {
		err = fmt.Errorf("%w: target session must belong to the reconciliation workspace", managedagents.ErrForbidden)
	}
	if err != nil {
		s.recordObjectReconciliationArtifactAudit(r, request, objectreconcile.Report{}, managedagents.SessionArtifact{}, err)
		writeError(w, err)
		return
	}

	report, err := s.runObjectReconciliation(r, request.PreviewInput)
	if errors.Is(err, objectreconcile.ErrInvalid) {
		err = fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	if err != nil {
		s.recordObjectReconciliationArtifactAudit(r, request, report, managedagents.SessionArtifact{}, err)
		writeError(w, err)
		return
	}

	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		err = fmt.Errorf("encode reconciliation report: %w", err)
		s.recordObjectReconciliationArtifactAudit(r, request, report, managedagents.SessionArtifact{}, err)
		writeError(w, err)
		return
	}
	content = append(content, '\n')
	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])
	name := safeArtifactFileName(strings.TrimSpace(request.Name))
	if strings.TrimSpace(request.Name) == "" {
		name = fmt.Sprintf("object-reconciliation-%s.json", report.GeneratedAt.UTC().Format("20060102T150405Z"))
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	metadata, err := json.Marshal(map[string]any{
		"protocol_version": objectReconciliationReportProtocolVersion,
		"source":           "object_reconciliation",
		"format":           "json",
		"dry_run":          true,
		"generated_at":     report.GeneratedAt,
		"size_bytes":       len(content),
		"lineage": map[string]any{
			"kind":       "system_report",
			"tool":       "object_reconciliation",
			"session_id": session.ID,
		},
		"validation": map[string]any{
			"status":          "passed",
			"content_type":    "application/json",
			"size_bytes":      len(content),
			"checksum_sha256": checksumHex,
			"checks": []map[string]string{{
				"name": "report_json", "status": "passed", "message": "Reconciliation report encoded as valid JSON.",
			}},
		},
		"filters": map[string]any{
			"storage_provider": report.StorageProvider,
			"bucket":           report.Bucket,
			"prefix":           report.Prefix,
			"limit":            request.Limit,
			"provider_cursor":  request.ProviderCursor,
		},
		"scan":    report.Scan,
		"summary": report.Summary,
	})
	if err != nil {
		err = fmt.Errorf("encode reconciliation artifact metadata: %w", err)
		s.recordObjectReconciliationArtifactAudit(r, request, report, managedagents.SessionArtifact{}, err)
		writeError(w, err)
		return
	}
	createdBy := requestActorID(r, "system")
	objectKey := fmt.Sprintf("%s/%s/reconciliation/%d-%s", session.WorkspaceID, session.ID, time.Now().UTC().UnixNano(), name)
	if err := objectstore.ValidateObjectKey(objectKey); err != nil {
		s.recordObjectReconciliationArtifactAudit(r, request, report, managedagents.SessionArtifact{}, err)
		writeError(w, err)
		return
	}
	objectRef, artifact, err := managedagents.PersistSessionArtifactObject(r.Context(), s.store, s.objectStore, managedagents.PersistSessionArtifactObjectInput{
		DeleteObjectOnFailure: true,
		PutObject: objectstore.PutObjectInput{
			Bucket:         report.Bucket,
			Key:            objectKey,
			Body:           bytes.NewReader(content),
			ContentType:    "application/json",
			SizeBytes:      int64(len(content)),
			ChecksumSHA256: checksumHex,
			Metadata:       map[string]string{"protocol-version": objectReconciliationReportProtocolVersion},
		},
		ObjectRef: managedagents.CreateObjectRefInput{
			WorkspaceID: session.WorkspaceID,
			ContentType: "application/json",
			SizeBytes:   int64(len(content)),
			Visibility:  managedagents.ObjectVisibilityWorkspace,
			Metadata:    metadata,
			CreatedBy:   createdBy,
		},
		SessionArtifact: managedagents.CreateSessionArtifactInput{
			SessionID:    session.ID,
			Name:         name,
			Description:  "Object storage reconciliation dry-run report",
			ArtifactType: managedagents.ArtifactTypeFile,
			Metadata:     metadata,
			CreatedBy:    createdBy,
		},
	})
	s.recordObjectReconciliationArtifactAudit(r, request, report, artifact, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, exportObjectReconciliationArtifactResponse{
		Report: report, ObjectRef: objectRef, Artifact: artifact,
		WorkspacePath: capability.SessionArtifactSandboxPath(artifact),
	})
}

func (s *Server) runObjectReconciliation(r *http.Request, input objectreconcile.PreviewInput) (objectreconcile.Report, error) {
	store, ok := s.store.(objectreconcile.Store)
	if !ok {
		return objectreconcile.Report{}, fmt.Errorf("%w: object reconciliation store is unavailable", managedagents.ErrConflict)
	}
	inventory, ok := s.objectStore.(objectstore.InventoryClient)
	if !ok {
		return objectreconcile.Report{}, fmt.Errorf("%w: configured object store does not support inventory", managedagents.ErrConflict)
	}
	service, err := objectreconcile.NewService(store, inventory, objectstore.ProviderForClient(s.objectStore), s.defaultObjectStoreBucket())
	if err != nil {
		return objectreconcile.Report{}, fmt.Errorf("%w: object reconciliation is unavailable", managedagents.ErrConflict)
	}
	return service.Preview(r.Context(), input)
}

func (s *Server) recordObjectReconciliationAudit(r *http.Request, input objectreconcile.PreviewInput, report objectreconcile.Report, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	principal := controlPrincipalFromRequest(r)
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	detailsJSON, _ := json.Marshal(map[string]any{
		"dry_run": true, "bucket": fallbackString(report.Bucket, input.Bucket), "prefix": fallbackString(report.Prefix, input.Prefix),
		"limit": input.Limit, "summary": report.Summary, "scan": report.Scan,
	})
	resourceID := fallbackString(report.Prefix, input.Prefix)
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, input.WorkspaceID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: "object_cleanup.reconciliation.preview",
		ResourceType: "object_store_reconciliation", ResourceID: strings.TrimSpace(resourceID),
		Outcome: outcome, ErrorMessage: errorMessage, Details: detailsJSON,
	}); err != nil && s.logger != nil {
		s.logger.Warn("object reconciliation audit write failed", "error", err)
	}
}

func (s *Server) recordObjectReconciliationArtifactAudit(r *http.Request, input exportObjectReconciliationArtifactRequest, report objectreconcile.Report, artifact managedagents.SessionArtifact, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	principal := controlPrincipalFromRequest(r)
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	detailsJSON, _ := json.Marshal(map[string]any{
		"dry_run": true, "session_id": input.SessionID, "artifact_id": artifact.ID,
		"bucket": fallbackString(report.Bucket, input.Bucket), "prefix": fallbackString(report.Prefix, input.Prefix),
		"limit": input.Limit, "summary": report.Summary, "scan": report.Scan,
	})
	resourceID := artifact.ID
	if resourceID == "" {
		resourceID = input.SessionID
	}
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, input.WorkspaceID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: "object_cleanup.reconciliation.export_artifact",
		ResourceType: "session_artifact", ResourceID: strings.TrimSpace(resourceID), Outcome: outcome,
		ErrorMessage: errorMessage, Details: detailsJSON,
	}); err != nil && s.logger != nil {
		s.logger.Warn("object reconciliation artifact audit write failed", "error", err)
	}
}

func (s *Server) listObjectCleanupJobs(w http.ResponseWriter, r *http.Request) {
	store, err := s.objectCleanupOperationsStore()
	if err != nil {
		writeError(w, err)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			writeError(w, fmt.Errorf("%w: limit must be between 1 and 200", objectcleanup.ErrInvalid))
			return
		}
	}
	createdFrom, err := parseOptionalTime(r.URL.Query().Get("created_from"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid created_from: %v", objectcleanup.ErrInvalid, err))
		return
	}
	createdTo, err := parseOptionalTime(r.URL.Query().Get("created_to"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid created_to: %v", objectcleanup.ErrInvalid, err))
		return
	}
	input := objectcleanup.ListInput{
		WorkspaceID: requestWorkspaceID(r, r.URL.Query().Get("workspace_id")),
		Status:      r.URL.Query().Get("status"),
		Reason:      r.URL.Query().Get("reason"),
		Limit:       limit,
	}
	if createdFrom != nil {
		input.CreatedFrom = *createdFrom
	}
	if createdTo != nil {
		input.CreatedTo = *createdTo
	}
	jobs, err := store.ListObjectCleanup(r.Context(), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": nonNilSlice(jobs)})
}

func (s *Server) getObjectCleanupStats(w http.ResponseWriter, r *http.Request) {
	store, err := s.objectCleanupOperationsStore()
	if err != nil {
		writeError(w, err)
		return
	}
	stats, err := store.GetObjectCleanupStats(r.Context(), requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) retryObjectCleanupJob(w http.ResponseWriter, r *http.Request) {
	store, err := s.objectCleanupOperationsStore()
	if err != nil {
		writeError(w, err)
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	jobID := strings.TrimSpace(r.PathValue("job_id"))
	job, err := store.RetryObjectCleanup(r.Context(), objectcleanup.RetryInput{
		WorkspaceID: workspaceID, JobID: jobID, Now: time.Now().UTC(),
	})
	s.recordObjectCleanupAudit(r, "object_cleanup.retry", workspaceID, jobID, map[string]any{
		"previous_status": objectcleanup.StatusDeadLetter,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) approveBlockedObjectCleanupJob(w http.ResponseWriter, r *http.Request) {
	store, err := s.objectCleanupOperationsStore()
	if err != nil {
		writeError(w, err)
		return
	}
	var request approveObjectCleanupRequest
	if err := decodeJSON(r, &request); err != nil {
		workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
		jobID := strings.TrimSpace(r.PathValue("job_id"))
		s.recordObjectCleanupAudit(r, "object_cleanup.approve_blocked", workspaceID, jobID, map[string]any{"confirmed": false}, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	workspaceID := requestWorkspaceID(r, r.URL.Query().Get("workspace_id"))
	jobID := strings.TrimSpace(r.PathValue("job_id"))
	expectedConfirmation := "DELETE " + jobID
	if request.Confirm != expectedConfirmation {
		err := fmt.Errorf("%w: confirm must equal %q", objectcleanup.ErrInvalid, expectedConfirmation)
		s.recordObjectCleanupAudit(r, "object_cleanup.approve_blocked", workspaceID, jobID, map[string]any{"confirmed": false}, err)
		writeError(w, err)
		return
	}
	job, err := store.ApproveBlockedObjectCleanup(r.Context(), objectcleanup.ApproveInput{
		WorkspaceID: workspaceID, JobID: jobID, Now: time.Now().UTC(),
	})
	s.recordObjectCleanupAudit(r, "object_cleanup.approve_blocked", workspaceID, jobID, map[string]any{
		"confirmed": true, "previous_status": objectcleanup.StatusBlocked,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) recordObjectCleanupAudit(r *http.Request, action, workspaceID, jobID string, details map[string]any, actionErr error) {
	store, ok := s.store.(managedagents.OperatorAuditStore)
	if !ok {
		return
	}
	principal := controlPrincipalFromRequest(r)
	outcome := "succeeded"
	errorMessage := ""
	if actionErr != nil {
		outcome = "failed"
		errorMessage = actionErr.Error()
	}
	detailsJSON, _ := json.Marshal(details)
	if _, err := managedagents.RecordOperatorAuditWithContext(r.Context(), store, managedagents.RecordOperatorAuditInput{
		WorkspaceID: auditWorkspaceID(r, workspaceID), PrincipalID: principal.ID,
		OperatorLabel: principal.OperatorLabel, Role: principal.Role, Action: action,
		ResourceType: "object_cleanup_job", ResourceID: strings.TrimSpace(jobID), Outcome: outcome,
		ErrorMessage: errorMessage, Details: detailsJSON,
	}); err != nil && s.logger != nil {
		s.logger.Warn("object cleanup audit write failed", "action", action, "job_id", jobID, "error", err)
	}
}
