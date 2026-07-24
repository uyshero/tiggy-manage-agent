package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	skillspkg "tiggy-manage-agent/internal/skills"
)

type stubSkillsToolService struct {
	searchRequest   SkillsSearchRequest
	inspectRequest  SkillsInspectRequest
	discoverRequest SkillsDiscoverRequest
	previewRequest  SkillsPreviewRequest
	installRequest  SkillsInstallRequest
	enableRequest   SkillsEnableRequest
	disableRequest  SkillsDisableRequest
}

func (s *stubSkillsToolService) Search(_ context.Context, request SkillsSearchRequest) (SkillsSearchResponse, error) {
	s.searchRequest = request
	return SkillsSearchResponse{Query: request.Query, Count: 1, Items: []SkillsSearchItem{{Skill: skillspkg.Skill{Identifier: "code-review"}}}}, nil
}

func (s *stubSkillsToolService) Inspect(_ context.Context, request SkillsInspectRequest) (SkillsInspectResponse, error) {
	s.inspectRequest = request
	return SkillsInspectResponse{
		Skill: skillspkg.Skill{Identifier: "code-review"}, Version: skillspkg.Version{Version: 2, ContentText: "FULL-SKILL-PAGE"},
		ContentOffset: request.ContentOffset, ContentChars: 15, TotalContentChars: 30, NextOffset: 15, HasMore: true,
	}, nil
}

func (s *stubSkillsToolService) Discover(_ context.Context, request SkillsDiscoverRequest) (SkillsDiscoverResponse, error) {
	s.discoverRequest = request
	return SkillsDiscoverResponse{Provider: "catalog", SearchMode: "organization_catalog", Count: 1}, nil
}

func (s *stubSkillsToolService) Preview(_ context.Context, request SkillsPreviewRequest) (SkillsPreviewResponse, error) {
	s.previewRequest = request
	return SkillsPreviewResponse{Identifier: "code-review", Revision: "blob123", InstallState: "new_install"}, nil
}

func (*stubSkillsToolService) ReadAsset(_ context.Context, request SkillsReadAssetRequest) (SkillsReadAssetResponse, error) {
	if request.Path == "assets/manifest.json" {
		return SkillsReadAssetResponse{
			SkillIdentifier: "code-review", SkillVersion: 2, Found: false,
			RequestedPath: request.Path, AvailablePaths: []string{"SKILL.md", "REFERENCE.md"},
		}, nil
	}
	return SkillsReadAssetResponse{
		SkillIdentifier: "code-review", SkillVersion: 2, Found: true,
		File: skillspkg.AssetFile{Path: "REFERENCE.md", Content: "Reference text.", Size: 15},
	}, nil
}

func (s *stubSkillsToolService) Install(_ context.Context, request SkillsInstallRequest) (SkillsInstallResponse, error) {
	s.installRequest = request
	return SkillsInstallResponse{Skill: skillspkg.Skill{Identifier: request.Identifier}, Version: skillspkg.Version{Version: 1}}, nil
}

func (s *stubSkillsToolService) Enable(_ context.Context, request SkillsEnableRequest) (SkillsEnableResponse, error) {
	s.enableRequest = request
	return SkillsEnableResponse{
		AgentID: "agt_1", NewConfigVersion: 2, CurrentSessionVersion: 1,
		Binding: skillspkg.EnabledSkill{Skill: request.Identifier, Version: 1}, RequiresSessionUpgrade: false,
	}, nil
}

func (s *stubSkillsToolService) Disable(_ context.Context, request SkillsDisableRequest) (SkillsDisableResponse, error) {
	s.disableRequest = request
	return SkillsDisableResponse{
		AgentID: "agt_1", PreviousConfigVersion: 2, NewConfigVersion: 3, CurrentSessionVersion: 2,
		Binding: skillspkg.EnabledSkill{Skill: request.Identifier, Version: 1}, Removed: true, RequiresSessionUpgrade: true,
	}, nil
}

func TestSkillsRuntimeManifestAndExecution(t *testing.T) {
	manifest := (SkillsRuntime{}).Manifest()
	if manifest.Identifier != SkillsIdentifier || len(manifest.API) != 8 {
		t.Fatalf("unexpected skills manifest: %#v", manifest)
	}
	if !strings.Contains(manifest.SystemRole, "binding execution contract") || !strings.Contains(manifest.SystemRole, "has_more is false") || !strings.Contains(manifest.SystemRole, "Never substitute a mandatory workflow") {
		t.Fatalf("skills system role is missing binding compliance rules: %s", manifest.SystemRole)
	}
	if manifest.ApprovalPolicy != ApprovalPolicyNever || manifest.API[2].Risk != ToolRiskRead || manifest.API[3].Risk != ToolRiskRead || manifest.API[4].Risk != ToolRiskRead || manifest.API[5].Risk != ToolRiskWrite || manifest.API[5].ApprovalPolicy != ApprovalPolicyAlways || manifest.API[6].Risk != ToolRiskWrite || manifest.API[7].Risk != ToolRiskWrite || manifest.API[7].ApprovalPolicy != ApprovalPolicyAlways {
		t.Fatalf("expected install and enable approval metadata: %#v", manifest.API)
	}

	service := &stubSkillsToolService{}
	runtime := SkillsRuntime{Service: service}
	context := ExecutionContext{WorkspaceID: "wksp_1", SessionID: "sesn_1", TurnID: "turn_1"}
	discovered, err := runtime.Execute(t.Context(), Call{
		ID: "call_discover", Identifier: SkillsIdentifier, APIName: "discover",
		Arguments: json.RawMessage(`{"provider":"catalog","query":"review","category":"Engineering","tags":["quality"]}`),
	}, context)
	if err != nil || discovered.Content == "" || service.discoverRequest.Provider != "catalog" ||
		service.discoverRequest.WorkspaceID != "wksp_1" || service.discoverRequest.SessionID != "sesn_1" || len(service.discoverRequest.Tags) != 1 {
		t.Fatalf("execute discover: result=%#v request=%#v err=%v", discovered, service.discoverRequest, err)
	}
	previewed, err := runtime.Execute(t.Context(), Call{
		ID: "call_preview", Identifier: SkillsIdentifier, APIName: "preview",
		Arguments: json.RawMessage(`{"source":{"provider":"github","repository":"acme/review-skill","ref":"main","path":"SKILL.md"}}`),
	}, context)
	if err != nil || previewed.Content == "" || service.previewRequest.Source.Repository != "acme/review-skill" || service.previewRequest.WorkspaceID != "wksp_1" {
		t.Fatalf("execute preview: result=%#v request=%#v err=%v", previewed, service.previewRequest, err)
	}
	offlinePreview, err := runtime.Execute(t.Context(), Call{
		ID: "call_offline_preview", Identifier: SkillsIdentifier, APIName: "preview",
		Arguments: json.RawMessage(`{"source":{"provider":"artifact","artifact_id":"art_1"}}`),
	}, context)
	if err != nil || offlinePreview.Content == "" || service.previewRequest.Source.Provider != "artifact" || service.previewRequest.Source.ArtifactID != "art_1" || service.previewRequest.SessionID != "sesn_1" {
		t.Fatalf("execute offline preview: result=%#v request=%#v err=%v", offlinePreview, service.previewRequest, err)
	}
	inspected, err := runtime.Execute(t.Context(), Call{
		ID: "call_inspect", Identifier: SkillsIdentifier, APIName: "inspect",
		Arguments: json.RawMessage(`{"identifier":"code-review","version":2,"content_offset":0,"content_max_chars":8000}`),
	}, context)
	if err != nil || !strings.Contains(inspected.Content, "FULL-SKILL-PAGE") || !strings.Contains(inspected.Content, "content_offset=15") || service.inspectRequest.ContentMaxChars != 8000 {
		t.Fatalf("execute paged inspect: result=%#v request=%#v err=%v", inspected, service.inspectRequest, err)
	}
	if strings.Contains(string(inspected.State), "FULL-SKILL-PAGE") {
		t.Fatalf("inspect state must not duplicate paged instruction content: %s", inspected.State)
	}
	asset, err := runtime.Execute(t.Context(), Call{
		ID: "call_asset", Identifier: SkillsIdentifier, APIName: "read_asset",
		Arguments: json.RawMessage(`{"identifier":"code-review","version":2,"path":"REFERENCE.md"}`),
	}, context)
	if err != nil || asset.Content == "" {
		t.Fatalf("execute read asset: result=%#v err=%v", asset, err)
	}
	missingAsset, err := runtime.Execute(t.Context(), Call{
		ID: "call_missing_asset", Identifier: SkillsIdentifier, APIName: "read_asset",
		Arguments: json.RawMessage(`{"identifier":"code-review","version":2,"path":"assets/manifest.json"}`),
	}, context)
	if err != nil || missingAsset.Error != nil || !strings.Contains(missingAsset.Content, "Available paths: SKILL.md, REFERENCE.md") ||
		!strings.Contains(string(missingAsset.State), `"found":false`) {
		t.Fatalf("execute missing read asset: result=%#v err=%v", missingAsset, err)
	}
	install, err := runtime.Execute(t.Context(), Call{
		ID: "call_install", Identifier: SkillsIdentifier, APIName: "install",
		Arguments: json.RawMessage(`{"identifier":"code-review","title":"Code Review","content_text":"Review carefully."}`),
	}, context)
	if err != nil {
		t.Fatalf("execute install: %v", err)
	}
	if install.Identifier != SkillsIdentifier || service.installRequest.WorkspaceID != "wksp_1" || service.installRequest.SessionID != "sesn_1" || service.installRequest.TurnID != "turn_1" {
		t.Fatalf("unexpected install execution: result=%#v request=%#v", install, service.installRequest)
	}
	offlineInstall, err := runtime.Execute(t.Context(), Call{
		ID: "call_offline_install", Identifier: SkillsIdentifier, APIName: "install",
		Arguments: json.RawMessage(`{"identifier":"offline-review","source":{"provider":"artifact","artifact_id":"art_1"}}`),
	}, context)
	if err != nil || offlineInstall.Content == "" || service.installRequest.Source == nil || service.installRequest.Source.ArtifactID != "art_1" || service.installRequest.SessionID != "sesn_1" || service.installRequest.TurnID != "turn_1" {
		t.Fatalf("execute offline install: result=%#v request=%#v err=%v", offlineInstall, service.installRequest, err)
	}

	enable, err := runtime.Execute(t.Context(), Call{
		ID: "call_enable", Identifier: SkillsIdentifier, APIName: "enable",
		Arguments: json.RawMessage(`{"identifier":"code-review","version":1}`),
	}, context)
	if err != nil {
		t.Fatalf("execute enable: %v", err)
	}
	if !strings.Contains(enable.Content, "next user turn") || strings.Contains(enable.Content, "cannot use") || service.enableRequest.Identifier != "code-review" || service.enableRequest.SessionID != "sesn_1" {
		t.Fatalf("unexpected enable execution: result=%#v request=%#v", enable, service.enableRequest)
	}

	disable, err := runtime.Execute(t.Context(), Call{
		ID: "call_disable", Identifier: SkillsIdentifier, APIName: "disable",
		Arguments: json.RawMessage(`{"identifier":"code-review"}`),
	}, context)
	if err != nil {
		t.Fatalf("execute disable: %v", err)
	}
	if !strings.Contains(disable.Content, "requires a manual config upgrade") || service.disableRequest.Identifier != "code-review" || service.disableRequest.SessionID != "sesn_1" || service.disableRequest.TurnID != "turn_1" {
		t.Fatalf("unexpected disable execution: result=%#v request=%#v", disable, service.disableRequest)
	}
}
