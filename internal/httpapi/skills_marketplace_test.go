package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/skillmarketplace"
	skillspkg "tiggy-manage-agent/internal/skills"
)

func TestSkillsMarketplaceHTTPDiscoverPreviewInstallEnableAndDisable(t *testing.T) {
	store := newTestStore()
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		Name: "Marketplace UI Agent", LLMProvider: "fake", LLMModel: "fake-demo", System: "test",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{Name: "Marketplace UI Env", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "ui-test"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	marketplace := &stubSkillMarketplace{packageResult: skillmarketplace.Package{
		Source: skillmarketplace.Source{Provider: "github", Repository: "acme/review-skill", Ref: "main", Path: "SKILL.md"},
		Name:   "Review Skill", Description: "Review changes safely.", License: "MIT",
		Manifest: json.RawMessage(`{"inputs_schema":{"type":"object","additionalProperties":false,"properties":{"style":{"type":"string","enum":["strict","balanced"]}},"required":["style"]}}`),
		Content:  "# Review Skill\n\nReview changes before release.", Revision: "abc123", HTMLURL: "https://github.com/acme/review-skill/blob/main/SKILL.md",
	}}
	server := &Server{
		mux: http.NewServeMux(), store: store, logger: slog.Default(), controlAuthToken: "control-secret",
		skillsToolService: newSkillsToolServiceWithMarketplace(store, marketplace),
	}
	server.routes()

	unauthorized := httptest.NewRequest(http.MethodGet, "/v1/skills/marketplace/discover?session_id="+session.ID+"&repository=acme%2Freview-skill", nil)
	unauthorizedResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected marketplace management auth, got %d", unauthorizedResponse.Code)
	}

	discover := marketplaceHTTPRequest(t, server.mux, http.MethodGet,
		"/v1/skills/marketplace/discover?session_id="+session.ID+"&repository=acme%2Freview-skill", nil)
	if discover.Code != http.StatusOK {
		t.Fatalf("discover marketplace: %d %s", discover.Code, discover.Body.String())
	}
	var discovered struct {
		Count int `json:"count"`
	}
	decodeMarketplaceHTTPResponse(t, discover, &discovered)
	if discovered.Count != 1 || marketplace.discoverInput.Repository != "acme/review-skill" {
		t.Fatalf("unexpected discovery: %#v input=%#v", discovered, marketplace.discoverInput)
	}

	preview := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/preview", map[string]any{
		"session_id": session.ID, "identifier": "review-skill",
		"source": map[string]any{"provider": "github", "repository": "acme/review-skill", "ref": "main", "path": "SKILL.md"},
	})
	if preview.Code != http.StatusOK {
		t.Fatalf("preview marketplace package: %d %s", preview.Code, preview.Body.String())
	}
	var previewed struct {
		InstallState string `json:"install_state"`
		Policy       struct {
			Allowed        bool   `json:"allowed"`
			PolicyRevision string `json:"policy_revision"`
		} `json:"policy"`
		Security struct {
			Digest      string `json:"digest_sha256"`
			Attestation struct {
				Status string `json:"status"`
			} `json:"attestation"`
		} `json:"security"`
	}
	decodeMarketplaceHTTPResponse(t, preview, &previewed)
	if previewed.InstallState != "new_install" || !previewed.Policy.Allowed || previewed.Security.Digest == "" || previewed.Security.Attestation.Status != skillmarketplace.AttestationMissing {
		t.Fatalf("unexpected marketplace preview: %#v", previewed)
	}

	install := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/install", map[string]any{
		"session_id": session.ID, "identifier": "review-skill",
		"source":          map[string]any{"provider": "github", "repository": "acme/review-skill", "ref": "main", "path": "SKILL.md"},
		"policy_revision": previewed.Policy.PolicyRevision,
	})
	if install.Code != http.StatusCreated {
		t.Fatalf("install marketplace package: %d %s", install.Code, install.Body.String())
	}
	var installed struct {
		Skill struct {
			ID         string `json:"id"`
			Identifier string `json:"identifier"`
		} `json:"skill"`
		Version struct {
			Version int `json:"version"`
		} `json:"version"`
	}
	decodeMarketplaceHTTPResponse(t, install, &installed)
	if installed.Skill.Identifier != "review-skill" || installed.Version.Version != 1 {
		t.Fatalf("unexpected install response: %#v", installed)
	}

	invalidEnable := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/"+installed.Skill.ID+"/enable", map[string]any{
		"session_id": session.ID, "version": 1, "mode": "full", "priority": 100, "inputs": map[string]any{"style": "invalid-secret-value"},
	})
	if invalidEnable.Code != http.StatusBadRequest || strings.Contains(invalidEnable.Body.String(), "invalid-secret-value") {
		t.Fatalf("expected sanitized invalid inputs response, got %d %s", invalidEnable.Code, invalidEnable.Body.String())
	}
	unchangedAgent, err := store.GetAgent(agent.ID)
	if err != nil || unchangedAgent.CurrentConfigVersion != 1 {
		t.Fatalf("invalid HTTP inputs changed Agent config: agent=%#v err=%v", unchangedAgent, err)
	}

	enable := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/"+installed.Skill.ID+"/enable", map[string]any{
		"session_id": session.ID, "version": 1, "mode": "full", "priority": 100, "inputs": map[string]any{"style": "strict"},
	})
	if enable.Code != http.StatusCreated {
		t.Fatalf("enable marketplace skill: %d %s", enable.Code, enable.Body.String())
	}
	var enabled struct {
		AgentID string `json:"agent_id"`
		Changed bool   `json:"changed"`
		Binding struct {
			Skill   string         `json:"skill"`
			Version int            `json:"version"`
			Inputs  map[string]any `json:"inputs"`
		} `json:"binding"`
	}
	decodeMarketplaceHTTPResponse(t, enable, &enabled)
	if !enabled.Changed || enabled.AgentID != agent.ID || enabled.Binding.Skill != "review-skill" || enabled.Binding.Version != 1 || enabled.Binding.Inputs["style"] != "strict" {
		t.Fatalf("unexpected enable response: %#v", enabled)
	}
	unchangedEnable := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/"+installed.Skill.ID+"/enable", map[string]any{
		"session_id": session.ID, "version": 1, "mode": "full", "priority": 100, "inputs": map[string]any{"style": "strict"},
	})
	if unchangedEnable.Code != http.StatusOK {
		t.Fatalf("repeat identical enable: %d %s", unchangedEnable.Code, unchangedEnable.Body.String())
	}
	var unchangedEnabled struct {
		Changed          bool `json:"changed"`
		NewConfigVersion int  `json:"new_config_version"`
	}
	decodeMarketplaceHTTPResponse(t, unchangedEnable, &unchangedEnabled)
	if unchangedEnabled.Changed || unchangedEnabled.NewConfigVersion != 2 {
		t.Fatalf("expected unchanged enable at config v2, got %#v", unchangedEnabled)
	}

	disable := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/"+installed.Skill.ID+"/disable", map[string]any{
		"session_id": session.ID,
	})
	if disable.Code != http.StatusCreated {
		t.Fatalf("disable marketplace skill: %d %s", disable.Code, disable.Body.String())
	}
	var disabled struct {
		AgentID          string `json:"agent_id"`
		Removed          bool   `json:"removed"`
		NewConfigVersion int    `json:"new_config_version"`
	}
	decodeMarketplaceHTTPResponse(t, disable, &disabled)
	if disabled.AgentID != agent.ID || !disabled.Removed || disabled.NewConfigVersion != 3 {
		t.Fatalf("unexpected disable response: %#v", disabled)
	}

	audits, err := store.ListOperatorAudit(managedagents.ListOperatorAuditInput{Limit: 20})
	if err != nil {
		t.Fatalf("list operator audit: %v", err)
	}
	actions := map[string]bool{}
	for _, audit := range audits {
		actions[audit.Action] = true
	}
	if !actions["skills.marketplace.install"] || !actions["skills.enable"] || !actions["skills.disable"] {
		t.Fatalf("expected install, enable, and disable control audits, got %#v", actions)
	}
}

func TestSkillsMarketplaceHTTPOfflineArtifactUploadPreviewAndInstall(t *testing.T) {
	store, session := newBinarySkillsTestSession(t)
	objectStore := &binarySkillObjectStore{}
	server := &Server{
		mux: http.NewServeMux(), store: store, objectStore: objectStore, logger: slog.Default(), controlAuthToken: "control-secret",
		skillsToolService: newSkillsToolServiceWithDependencies(store, nil, skillmarketplace.Policy{}, objectStore, "artifacts"),
	}
	server.routes()

	archive := offlineSkillZIP(t, map[string]string{
		"offline-http/SKILL.md":                "---\nname: Offline HTTP\ndescription: Imported entirely offline.\nlicense: MIT\n---\nReview locally.",
		"offline-http/references/checklist.md": "# Local checklist\n",
	})
	body, contentType := multipartArtifactUpload(t, nil, "file", "offline-http.zip", string(archive))
	uploadRequest := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+session.ID+"/artifacts/upload", body)
	uploadRequest.Header.Set("Content-Type", contentType)
	uploadResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(uploadResponse, uploadRequest)
	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf("upload offline package: %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	var uploaded struct {
		Artifact managedagents.SessionArtifact `json:"artifact"`
	}
	decodeMarketplaceHTTPResponse(t, uploadResponse, &uploaded)
	if uploaded.Artifact.ID == "" || len(objectStore.puts) != 1 {
		t.Fatalf("unexpected offline upload: response=%#v puts=%#v", uploaded, objectStore.puts)
	}

	source := map[string]any{"provider": "artifact", "artifact_id": uploaded.Artifact.ID}
	preview := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/preview", map[string]any{
		"session_id": session.ID, "source": source,
	})
	if preview.Code != http.StatusOK {
		t.Fatalf("preview offline package: %d %s", preview.Code, preview.Body.String())
	}
	var previewed struct {
		Identifier   string `json:"identifier"`
		InstallState string `json:"install_state"`
		Policy       struct {
			Allowed        bool   `json:"allowed"`
			PolicyRevision string `json:"policy_revision"`
		} `json:"policy"`
		Source skillmarketplace.Source `json:"source"`
	}
	decodeMarketplaceHTTPResponse(t, preview, &previewed)
	if previewed.Identifier != "offline-http" || previewed.InstallState != "new_install" || !previewed.Policy.Allowed || previewed.Source.ArtifactID != uploaded.Artifact.ID {
		t.Fatalf("unexpected offline preview: %#v", previewed)
	}

	install := marketplaceHTTPRequest(t, server.mux, http.MethodPost, "/v1/skills/marketplace/install", map[string]any{
		"session_id": session.ID, "source": source, "policy_revision": previewed.Policy.PolicyRevision,
	})
	if install.Code != http.StatusCreated {
		t.Fatalf("install offline package: %d %s", install.Code, install.Body.String())
	}
	var installed struct {
		Skill   skillspkg.Skill   `json:"skill"`
		Version skillspkg.Version `json:"version"`
	}
	decodeMarketplaceHTTPResponse(t, install, &installed)
	if installed.Skill.Identifier != "offline-http" || installed.Skill.SourceType != skillspkg.SourceTypeArtifact || installed.Version.SourceRef != uploaded.Artifact.ID {
		t.Fatalf("unexpected offline install: %#v", installed)
	}
	if len(objectStore.puts) != 1 {
		t.Fatalf("text-only package should only upload the source ZIP, got %#v", objectStore.puts)
	}
}

func marketplaceHTTPRequest(t *testing.T, handler http.Handler, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody *bytes.Reader
	if body == nil {
		requestBody = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, requestBody)
	request.Header.Set("Authorization", "Bearer control-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-TMA-Operator", "skills-ui-test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeMarketplaceHTTPResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
