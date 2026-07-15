package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/skills"
)

type createSkillRequest struct {
	WorkspaceID    string `json:"workspace_id,omitempty"`
	Identifier     string `json:"identifier"`
	Title          string `json:"title"`
	Description    string `json:"description,omitempty"`
	OwnerType      string `json:"owner_type,omitempty"`
	SourcePluginID string `json:"source_plugin_id,omitempty"`
	SourceType     string `json:"source_type,omitempty"`
	SourceLocator  string `json:"source_locator,omitempty"`
	SourcePath     string `json:"source_path,omitempty"`
}

type createSkillVersionRequest struct {
	ContentFormat  string          `json:"content_format,omitempty"`
	Manifest       json.RawMessage `json:"manifest"`
	ContentText    string          `json:"content_text"`
	Assets         json.RawMessage `json:"assets,omitempty"`
	SourceRef      string          `json:"source_ref,omitempty"`
	SourceRevision string          `json:"source_revision,omitempty"`
	SourceURL      string          `json:"source_url,omitempty"`
}

type resolveSkillsPreviewRequest struct {
	WorkspaceID string          `json:"workspace_id,omitempty"`
	Skills      json.RawMessage `json:"skills"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

func (s *Server) skillRegistry() (skills.Registry, error) {
	registry, ok := s.store.(skills.Registry)
	if !ok {
		return nil, fmt.Errorf("skill registry is unavailable")
	}
	return registry, nil
}

func (s *Server) createSkill(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createSkillRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	principal := controlPrincipalFromRequest(r)
	created, err := registry.CreateSkill(r.Context(), skills.CreateSkillInput{
		WorkspaceID: requestWorkspaceID(r, request.WorkspaceID), Identifier: request.Identifier, Title: request.Title,
		Description: request.Description, OwnerType: request.OwnerType, SourcePluginID: request.SourcePluginID,
		SourceType: request.SourceType, SourceLocator: request.SourceLocator, SourcePath: request.SourcePath,
		CreatedBy: principal.ID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	includeArchived, err := optionalBool(r.URL.Query().Get("include_archived"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid include_archived: %v", managedagents.ErrInvalid, err))
		return
	}
	items, err := registry.ListSkills(r.Context(), skills.ListSkillsInput{
		WorkspaceID: requestWorkspaceID(r, r.URL.Query().Get("workspace_id")), IncludeArchived: includeArchived != nil && *includeArchived,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": nonNilSlice(items)})
}

func (s *Server) getSkill(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := registry.GetSkill(r.Context(), r.PathValue("skill_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) archiveSkill(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := registry.ArchiveSkill(r.Context(), r.PathValue("skill_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) createSkillVersion(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	var request createSkillVersionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	version, err := registry.CreateSkillVersion(r.Context(), skills.CreateVersionInput{
		SkillID: r.PathValue("skill_id"), ContentFormat: request.ContentFormat, Manifest: request.Manifest,
		ContentText: request.ContentText, Assets: request.Assets, SourceRef: request.SourceRef,
		SourceRevision: request.SourceRevision, SourceURL: request.SourceURL, CreatedBy: controlPrincipalFromRequest(r).ID,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (s *Server) listSkillVersions(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	versions, err := registry.ListSkillVersions(r.Context(), r.PathValue("skill_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": nonNilSlice(versions)})
}

func (s *Server) getSkillVersion(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || versionNumber <= 0 {
		writeError(w, fmt.Errorf("%w: skill version must be a positive integer", managedagents.ErrInvalid))
		return
	}
	version, err := registry.GetSkillVersion(r.Context(), r.PathValue("skill_id"), versionNumber)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *Server) downloadSkillPackage(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || versionNumber <= 0 {
		writeError(w, fmt.Errorf("%w: skill version must be a positive integer", managedagents.ErrInvalid))
		return
	}
	skill, err := registry.GetSkill(r.Context(), r.PathValue("skill_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	version, err := registry.GetSkillVersion(r.Context(), skill.ID, versionNumber)
	if err != nil {
		writeError(w, err)
		return
	}
	if version.PackageObjectRefID == "" {
		writeError(w, fmt.Errorf("%w: this legacy skill version has not been migrated to package storage", managedagents.ErrNotFound))
		return
	}
	objectRef, err := s.getObjectRefForRequest(r, version.PackageObjectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	object, err := s.objectStore.GetObject(r.Context(), objectstore.GetObjectInput{
		Bucket: objectRef.Bucket, Key: objectRef.ObjectKey, Version: objectRef.ObjectVersion,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	defer object.Body.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(fmt.Sprintf("%s-v%d.zip", skill.Identifier, version.Version)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.SizeBytes > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	}
	if version.PackageChecksum != "" {
		w.Header().Set("X-TMA-Skill-Package-Checksum", version.PackageChecksum)
	}
	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Warn("skill package download copy failed", "skill_id", skill.ID, "version", version.Version, "error", err)
	}
}

func (s *Server) backfillSkillPackages(w http.ResponseWriter, r *http.Request) {
	backfiller, ok := s.store.(skills.PackageBackfiller)
	if !ok {
		writeError(w, fmt.Errorf("skill package backfill is unavailable"))
		return
	}
	var input skills.PackageBackfillInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.WorkspaceID = requestWorkspaceID(r, input.WorkspaceID)
	result, err := backfiller.BackfillSkillPackages(r.Context(), input, controlPrincipalFromRequest(r).ID)
	s.recordSkillsControlAudit(r, "", "skills.package_storage.backfill", input.WorkspaceID, map[string]any{
		"workspace_id": input.WorkspaceID, "limit": input.Limit,
		"scanned": result.Scanned, "migrated": result.Migrated,
	}, err)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) resolveSkillsPreview(w http.ResponseWriter, r *http.Request) {
	registry, err := s.skillRegistry()
	if err != nil {
		writeError(w, err)
		return
	}
	var request resolveSkillsPreviewRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if request.MaxTokens < 0 {
		writeError(w, fmt.Errorf("%w: max_tokens must be non-negative", managedagents.ErrInvalid))
		return
	}
	result, err := skills.ResolveRegistry(r.Context(), registry, requestWorkspaceID(r, request.WorkspaceID), request.Skills, request.MaxTokens)
	if err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getSessionSkillUsages(w http.ResponseWriter, r *http.Request) {
	reader, ok := s.store.(skills.UsageReader)
	if !ok {
		writeError(w, fmt.Errorf("skill usage store is unavailable"))
		return
	}
	if _, err := s.getSessionForRequest(r, r.PathValue("session_id")); err != nil {
		writeError(w, err)
		return
	}
	usages, err := reader.ListSkillUsages(r.Context(), r.PathValue("session_id"), r.URL.Query().Get("turn_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill_usages": nonNilSlice(usages)})
}
