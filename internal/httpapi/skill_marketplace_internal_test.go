package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skillmarketplace"
	"tiggy-manage-agent/internal/skillpackage"
	skillspkg "tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
)

func TestInternalMarketplaceHTTPBrowsePreviewAndInstall(t *testing.T) {
	store := newMarketplaceCatalogHTTPTestStore()
	archive := offlineSkillZIP(t, map[string]string{
		"shared-review/SKILL.md":                "---\nname: Shared Review\ndescription: Organization review workflow.\nlicense: MIT\n---\nReview internal changes.",
		"shared-review/references/checklist.md": "# Checklist\n",
	})
	digest := sha256.Sum256(archive)
	objectStore := &binarySkillObjectStore{downloads: map[string][]byte{"skills/catalog/shared-review.zip": archive}}
	objectRef, err := store.CreateObjectRef(managedagents.CreateObjectRefInput{
		WorkspaceID: "wksp_publisher", Bucket: "skills", ObjectKey: "catalog/shared-review.zip",
		ContentType: "application/zip", SizeBytes: int64(len(archive)), ChecksumSHA256: hex.EncodeToString(digest[:]),
		Visibility: managedagents.ObjectVisibilityWorkspace,
	})
	if err != nil {
		t.Fatalf("create publisher package object ref: %v", err)
	}
	publisherSkill, err := store.CreateSkill(t.Context(), skillspkg.CreateSkillInput{
		WorkspaceID: "wksp_publisher", Identifier: "shared-review", Title: "Shared Review",
		Description: "Organization review workflow.", CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create publisher skill: %v", err)
	}
	publisherVersion, err := store.CreateSkillVersion(t.Context(), skillspkg.CreateVersionInput{
		SkillID: publisherSkill.ID, ContentFormat: "markdown",
		ContentText: "---\nname: Shared Review\nlicense: MIT\n---\nReview internal changes.", CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create publisher version: %v", err)
	}
	store.mu.Lock()
	publisherVersion.PackageFormat = skillpackage.FormatV1
	publisherVersion.PackageObjectRefID = objectRef.ID
	store.skillVersions[publisherSkill.ID][0] = publisherVersion
	store.mu.Unlock()

	entry, err := store.CreateMarketplaceEntry(t.Context(), skillmarketplace.CreateMarketplaceEntryInput{
		WorkspaceID: "wksp_publisher", SkillID: publisherSkill.ID, SkillVersion: 1,
		Summary: "Approved internal review package.", Category: "Engineering", Tags: []string{"review", "quality"}, CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create catalog entry: %v", err)
	}
	entry, err = store.TransitionMarketplaceEntry(t.Context(), skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: entry.WorkspaceID, EntryID: entry.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPendingReview, Actor: "publisher",
	})
	if err != nil {
		t.Fatalf("submit catalog entry: %v", err)
	}
	entry, err = store.TransitionMarketplaceEntry(t.Context(), skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: entry.WorkspaceID, EntryID: entry.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished, Actor: "admin",
	})
	if err != nil {
		t.Fatalf("publish catalog entry: %v", err)
	}

	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		WorkspaceID: "wksp_consumer", Name: "Catalog Consumer", LLMProvider: "fake", LLMModel: "fake-demo",
	})
	if err != nil {
		t.Fatalf("create consumer agent: %v", err)
	}
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{
		WorkspaceID: "wksp_consumer", Name: "Catalog Consumer Env", Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("create consumer environment: %v", err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{
		WorkspaceID: "wksp_consumer", AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "member",
	})
	if err != nil {
		t.Fatalf("create consumer session: %v", err)
	}
	service := newSkillsToolServiceWithDependencies(store, nil, skillmarketplace.Policy{}, objectStore, "skills")
	server := &Server{
		mux: http.NewServeMux(), store: store, objectStore: objectStore, logger: slog.Default(),
		skillsToolService: service,
	}
	server.routes()
	discovered, err := service.Discover(t.Context(), tools.SkillsDiscoverRequest{
		SessionID: session.ID, Query: "review", Category: "Engineering", Tags: []string{"QUALITY"}, Limit: 10,
	})
	if err != nil || discovered.Provider != skillmarketplace.CatalogProvider || discovered.SearchMode != "organization_catalog" ||
		len(discovered.Items) != 1 || discovered.Items[0].CatalogEntryID != entry.ID || discovered.Items[0].CatalogSkillID != publisherSkill.ID {
		t.Fatalf("unexpected skills_discover internal catalog result: result=%#v err=%v", discovered, err)
	}

	browse := getJSON[struct {
		Provider string `json:"provider"`
		Count    int    `json:"count"`
		Items    []struct {
			skillmarketplace.MarketplaceEntry
			Provider     string                       `json:"provider"`
			InstallState string                       `json:"install_state"`
			Existing     *tools.SkillsPreviewExisting `json:"existing"`
		} `json:"items"`
	}](t, server.mux, "/v1/skills/marketplace/internal?session_id="+session.ID+"&query=review&tag=quality")
	if browse.Provider != skillmarketplace.CatalogProvider || browse.Count != 1 || browse.Items[0].ID != entry.ID ||
		browse.Items[0].Provider != skillmarketplace.CatalogProvider || browse.Items[0].InstallState != "new_install" || browse.Items[0].Existing != nil {
		t.Fatalf("unexpected internal marketplace browse: %#v", browse)
	}

	source := map[string]any{"provider": "catalog", "catalog_entry_id": entry.ID}
	preview := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/preview", map[string]any{
		"session_id": session.ID, "source": source,
	})
	if preview.Code != http.StatusOK {
		t.Fatalf("preview internal package: %d %s", preview.Code, preview.Body.String())
	}
	var previewed struct {
		Identifier   string                  `json:"identifier"`
		InstallState string                  `json:"install_state"`
		Source       skillmarketplace.Source `json:"source"`
		Policy       struct {
			Allowed        bool   `json:"allowed"`
			PolicyRevision string `json:"policy_revision"`
		} `json:"policy"`
		Assets struct {
			Files []any `json:"files"`
		} `json:"assets"`
	}
	decodeMarketplaceHTTPResponse(t, preview, &previewed)
	if previewed.Identifier != "shared-review" || previewed.InstallState != "new_install" || !previewed.Policy.Allowed ||
		previewed.Source.CatalogEntryID != entry.ID || previewed.Source.CatalogSkillID != publisherSkill.ID || len(previewed.Assets.Files) != 1 {
		t.Fatalf("unexpected internal marketplace preview: %#v", previewed)
	}

	install := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/install", map[string]any{
		"session_id": session.ID, "identifier": previewed.Identifier, "source": source,
		"policy_revision": previewed.Policy.PolicyRevision,
	})
	if install.Code != http.StatusCreated {
		t.Fatalf("install internal package: %d %s", install.Code, install.Body.String())
	}
	var installed struct {
		Skill   skillspkg.Skill   `json:"skill"`
		Version skillspkg.Version `json:"version"`
	}
	decodeMarketplaceHTTPResponse(t, install, &installed)
	if installed.Skill.WorkspaceID != session.WorkspaceID || installed.Skill.SourceType != skillspkg.SourceTypeCatalog ||
		installed.Skill.SourceLocator != publisherSkill.ID || installed.Version.SourceRef != entry.ID || installed.Version.SourceRevision != hex.EncodeToString(digest[:]) {
		t.Fatalf("unexpected internal marketplace install provenance: %#v", installed)
	}

	installedBrowse := getJSON[struct {
		Items []internalMarketplaceCandidate `json:"items"`
	}](t, server.mux, "/v1/skills/marketplace/internal?session_id="+session.ID+"&query=review")
	if len(installedBrowse.Items) != 1 || installedBrowse.Items[0].InstallState != "unchanged" ||
		installedBrowse.Items[0].Existing == nil || installedBrowse.Items[0].Existing.SkillID != installed.Skill.ID || installedBrowse.Items[0].Existing.Version != 1 {
		t.Fatalf("installed catalog candidate was not marked unchanged: %#v", installedBrowse)
	}

	archiveV2 := offlineSkillZIP(t, map[string]string{
		"shared-review/SKILL.md":                "---\nname: Shared Review\ndescription: Organization review workflow v2.\nlicense: MIT\n---\nReview internal changes with v2 rules.",
		"shared-review/references/checklist.md": "# Checklist v2\n",
		"shared-review/references/v2.md":        "# Version 2\n",
	})
	digestV2 := sha256.Sum256(archiveV2)
	objectStore.downloads["skills/catalog/shared-review-v2.zip"] = archiveV2
	objectRefV2, err := store.CreateObjectRef(managedagents.CreateObjectRefInput{
		WorkspaceID: "wksp_publisher", Bucket: "skills", ObjectKey: "catalog/shared-review-v2.zip",
		ContentType: "application/zip", SizeBytes: int64(len(archiveV2)), ChecksumSHA256: hex.EncodeToString(digestV2[:]),
		Visibility: managedagents.ObjectVisibilityWorkspace,
	})
	if err != nil {
		t.Fatalf("create publisher v2 package object ref: %v", err)
	}
	publisherVersionV2, err := store.CreateSkillVersion(t.Context(), skillspkg.CreateVersionInput{
		SkillID: publisherSkill.ID, ContentFormat: "markdown",
		ContentText: "---\nname: Shared Review\nlicense: MIT\n---\nReview internal changes with v2 rules.", CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create publisher version 2: %v", err)
	}
	store.mu.Lock()
	for index, version := range store.skillVersions[publisherSkill.ID] {
		if version.Version == publisherVersionV2.Version {
			publisherVersionV2.PackageFormat = skillpackage.FormatV1
			publisherVersionV2.PackageObjectRefID = objectRefV2.ID
			store.skillVersions[publisherSkill.ID][index] = publisherVersionV2
			break
		}
	}
	store.mu.Unlock()
	entryV2, err := store.CreateMarketplaceEntry(t.Context(), skillmarketplace.CreateMarketplaceEntryInput{
		WorkspaceID: "wksp_publisher", SkillID: publisherSkill.ID, SkillVersion: publisherVersionV2.Version,
		Summary: "Approved internal review package v2.", Category: "Engineering", Tags: []string{"review", "quality"}, CreatedBy: "publisher",
	})
	if err != nil {
		t.Fatalf("create catalog entry v2: %v", err)
	}
	entryV2, err = store.TransitionMarketplaceEntry(t.Context(), skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: entryV2.WorkspaceID, EntryID: entryV2.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPendingReview, Actor: "publisher",
	})
	if err != nil {
		t.Fatalf("submit catalog entry v2: %v", err)
	}
	if _, err := store.TransitionMarketplaceEntry(t.Context(), skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: entry.WorkspaceID, EntryID: entry.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusWithdrawn, Actor: "admin",
	}); err != nil {
		t.Fatalf("withdraw catalog entry v1: %v", err)
	}
	entryV2, err = store.TransitionMarketplaceEntry(t.Context(), skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: entryV2.WorkspaceID, EntryID: entryV2.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusPublished, Actor: "admin",
	})
	if err != nil {
		t.Fatalf("publish catalog entry v2: %v", err)
	}
	upgradeBrowse := getJSON[struct {
		Items []internalMarketplaceCandidate `json:"items"`
	}](t, server.mux, "/v1/skills/marketplace/internal?session_id="+session.ID+"&query=review")
	if len(upgradeBrowse.Items) != 1 || upgradeBrowse.Items[0].ID != entryV2.ID || upgradeBrowse.Items[0].InstallState != "upgrade" ||
		upgradeBrowse.Items[0].Existing == nil || upgradeBrowse.Items[0].Existing.Version != 1 || upgradeBrowse.Items[0].Existing.SourceRef != entry.ID {
		t.Fatalf("catalog v2 candidate was not marked as an upgrade: %#v", upgradeBrowse)
	}

	sourceV2 := map[string]any{"provider": "catalog", "catalog_entry_id": entryV2.ID}
	previewV2Response := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/preview", map[string]any{
		"session_id": session.ID, "source": sourceV2,
	})
	if previewV2Response.Code != http.StatusOK {
		t.Fatalf("preview internal package v2: %d %s", previewV2Response.Code, previewV2Response.Body.String())
	}
	var previewV2 tools.SkillsPreviewResponse
	decodeMarketplaceHTTPResponse(t, previewV2Response, &previewV2)
	if previewV2.InstallState != "upgrade" || previewV2.Existing == nil || previewV2.Existing.Version != 1 ||
		!previewV2.Changes.ContentChanged || !stringSliceContains(previewV2.Changes.ChangedFiles, "references/checklist.md") ||
		!stringSliceContains(previewV2.Changes.AddedFiles, "references/v2.md") {
		t.Fatalf("unexpected internal catalog v2 preview: %#v", previewV2)
	}
	missingConfirmation := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/install", map[string]any{
		"session_id": session.ID, "identifier": previewV2.Identifier, "source": sourceV2,
		"policy_revision": previewV2.Policy.PolicyRevision,
	})
	if missingConfirmation.Code != http.StatusConflict {
		t.Fatalf("expected explicit upgrade_existing conflict, got %d %s", missingConfirmation.Code, missingConfirmation.Body.String())
	}
	upgradeResponse := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/install", map[string]any{
		"session_id": session.ID, "identifier": previewV2.Identifier, "source": sourceV2,
		"policy_revision": previewV2.Policy.PolicyRevision, "upgrade_existing": true,
	})
	if upgradeResponse.Code != http.StatusCreated {
		t.Fatalf("upgrade internal package v2: %d %s", upgradeResponse.Code, upgradeResponse.Body.String())
	}
	var upgraded tools.SkillsInstallResponse
	decodeMarketplaceHTTPResponse(t, upgradeResponse, &upgraded)
	if !upgraded.Upgraded || upgraded.Skill.ID != installed.Skill.ID || upgraded.Version.Version != 2 ||
		upgraded.Version.SourceRef != entryV2.ID || upgraded.Version.SourceRevision != hex.EncodeToString(digestV2[:]) {
		t.Fatalf("unexpected internal catalog v2 upgrade: %#v", upgraded)
	}
	consumerVersions, err := store.ListSkillVersions(t.Context(), installed.Skill.ID)
	if err != nil || len(consumerVersions) != 2 || consumerVersions[0].Version != 2 || consumerVersions[1].Version != 1 {
		t.Fatalf("catalog upgrade did not preserve immutable v1: versions=%#v err=%v", consumerVersions, err)
	}
	upgradedBrowse := getJSON[struct {
		Items []internalMarketplaceCandidate `json:"items"`
	}](t, server.mux, "/v1/skills/marketplace/internal?session_id="+session.ID+"&query=review")
	if len(upgradedBrowse.Items) != 1 || upgradedBrowse.Items[0].InstallState != "unchanged" ||
		upgradedBrowse.Items[0].Existing == nil || upgradedBrowse.Items[0].Existing.Version != 2 || upgradedBrowse.Items[0].Existing.SourceRef != entryV2.ID {
		t.Fatalf("upgraded catalog candidate was not marked unchanged: %#v", upgradedBrowse)
	}

	if _, err := store.TransitionMarketplaceEntry(t.Context(), skillmarketplace.TransitionMarketplaceEntryInput{
		WorkspaceID: entryV2.WorkspaceID, EntryID: entryV2.ID, TargetStatus: skillmarketplace.MarketplaceEntryStatusWithdrawn, Actor: "admin",
	}); err != nil {
		t.Fatalf("withdraw catalog entry v2: %v", err)
	}
	blocked := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/internal/preview", map[string]any{
		"session_id": session.ID, "source": sourceV2,
	})
	if blocked.Code != http.StatusNotFound {
		t.Fatalf("expected withdrawn package to disappear, got %d %s", blocked.Code, blocked.Body.String())
	}
}
