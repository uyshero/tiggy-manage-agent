package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skillpackage"
	"tiggy-manage-agent/internal/skills"
)

func TestDownloadSkillPackage(t *testing.T) {
	store := newTestStore()
	skill, err := store.CreateSkill(context.Background(), skills.CreateSkillInput{
		Identifier: "downloadable", Title: "Downloadable", CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	version, err := store.CreateSkillVersion(context.Background(), skills.CreateVersionInput{
		SkillID: skill.ID, ContentFormat: "markdown", ContentText: "# Downloadable", CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	objectRef, err := store.CreateObjectRef(managedagents.CreateObjectRefInput{
		Bucket: "skills", ObjectKey: "skills/downloadable/versions/1/.tma/package.zip",
		ContentType: "application/zip", SizeBytes: 11, Visibility: managedagents.ObjectVisibilityWorkspace,
	})
	if err != nil {
		t.Fatalf("create archive object ref: %v", err)
	}
	version.PackageFormat = skillpackage.FormatV1
	version.PackageRoot = "skills/downloadable/versions/1"
	version.PackageChecksum = "package-checksum"
	version.PackageObjectRefID = objectRef.ID
	store.mu.Lock()
	store.skillVersions[skill.ID][0] = version
	store.mu.Unlock()
	objectStore := &fakeObjectStore{downloads: map[string]string{objectRef.Bucket + "/" + objectRef.ObjectKey: "zip-content"}}
	server := &Server{mux: http.NewServeMux(), store: store, objectStore: objectStore, logger: slog.Default()}
	server.routes()

	request := httptest.NewRequest(http.MethodGet, "/v1/skills/"+skill.ID+"/versions/1/package", nil)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "zip-content" {
		t.Fatalf("download package: status=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/zip" ||
		!strings.Contains(response.Header().Get("Content-Disposition"), "downloadable-v1.zip") ||
		response.Header().Get("X-TMA-Skill-Package-Checksum") != version.PackageChecksum {
		t.Fatalf("unexpected package response headers: %#v", response.Header())
	}
}

func TestDownloadLegacySkillPackageReturnsNotFound(t *testing.T) {
	store := newTestStore()
	skill, err := store.CreateSkill(context.Background(), skills.CreateSkillInput{
		Identifier: "legacy-package", Title: "Legacy Package", CreatedBy: "test",
	})
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if _, err := store.CreateSkillVersion(context.Background(), skills.CreateVersionInput{
		SkillID: skill.ID, ContentText: "legacy", CreatedBy: "test",
	}); err != nil {
		t.Fatalf("create version: %v", err)
	}
	server := &Server{mux: http.NewServeMux(), store: store, objectStore: &fakeObjectStore{}, logger: slog.Default()}
	server.routes()
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/skills/"+skill.ID+"/versions/1/package", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected legacy package 404, got %d %s", response.Code, response.Body.String())
	}
}
