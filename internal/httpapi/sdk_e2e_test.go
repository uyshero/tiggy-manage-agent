package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/envvars"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/skillmarketplace"
	internalSkills "tiggy-manage-agent/internal/skills"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/sdk/tma"
)

func TestTypedMarketplaceSDKRealServerE2E(t *testing.T) {
	store := newMarketplaceCatalogHTTPTestStore()
	marketplace := &stubSkillMarketplace{packageResult: skillmarketplace.Package{
		Source: skillmarketplace.Source{Provider: "github", Repository: "acme/sdk-review", Ref: "main", Path: "SKILL.md"},
		Name:   "SDK Marketplace Review", Description: "Review through the typed Marketplace SDK.", License: "MIT",
		Manifest: json.RawMessage(`{"inputs_schema":{"type":"object","additionalProperties":false}}`),
		Content:  "# SDK Marketplace Review\n\nReview carefully.", Revision: "marketplace-sdk-revision",
		HTMLURL: "https://github.com/acme/sdk-review/blob/main/SKILL.md",
	}}
	api := &Server{
		mux: http.NewServeMux(), store: store, runner: runner.NewMockRunner(store, 0, nil),
		skillsToolService: newSkillsToolServiceWithMarketplace(store, marketplace),
	}
	api.routes()
	server := httptest.NewServer(api.v2EnvelopeMiddleware(api.mux))
	defer server.Close()
	client, err := tma.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	agent, err := client.Agents.Create(ctx, tma.CreateAgentRequest{Name: "Marketplace SDK Agent", LLMProvider: "fake", LLMModel: "fake-demo"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := client.Environments.Create(ctx, tma.CreateEnvironmentRequest{Name: "Marketplace SDK Environment", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{AgentID: agent.ID, EnvironmentID: environment.ID})
	if err != nil {
		t.Fatal(err)
	}

	discovered, err := client.Marketplace.Discover(ctx, tma.MarketplaceDiscoverQuery{SessionID: session.ID, Repository: "acme/sdk-review", Limit: 10})
	if err != nil || discovered.Count != 1 || len(discovered.Items) != 1 || discovered.Items[0].Repository != "acme/review-skill" {
		t.Fatalf("discover Marketplace through SDK: %+v err=%v", discovered, err)
	}
	source := tma.MarketplaceSource{Provider: "github", Repository: "acme/sdk-review", Ref: "main", Path: "SKILL.md"}
	preview, err := client.Marketplace.Preview(ctx, tma.MarketplacePreviewRequest{SessionID: session.ID, Identifier: "sdk-marketplace-review", Source: source})
	if err != nil || preview.InstallState != "new_install" || !preview.Policy.Allowed || preview.Policy.PolicyRevision == "" || preview.Assets.Files == nil {
		t.Fatalf("preview Marketplace package through SDK: %+v err=%v", preview, err)
	}
	installed, err := client.Marketplace.Install(ctx, tma.MarketplaceInstallRequest{
		SessionID: session.ID, Identifier: preview.Identifier, Source: source, PolicyRevision: preview.Policy.PolicyRevision,
	})
	if err != nil || installed.Skill.Identifier != preview.Identifier || installed.Version.Version != 1 {
		t.Fatalf("install Marketplace package through SDK: %+v err=%v", installed, err)
	}
	enabled, err := client.Marketplace.EnableInstalled(ctx, installed.Skill.ID, tma.MarketplaceEnableRequest{SessionID: session.ID, Version: 1, Mode: "full"})
	if err != nil || !enabled.Changed || enabled.Binding.Skill != installed.Skill.Identifier {
		t.Fatalf("enable installed Skill through SDK: %+v err=%v", enabled, err)
	}
	disabled, err := client.Marketplace.DisableInstalled(ctx, installed.Skill.ID, tma.MarketplaceDisableRequest{SessionID: session.ID})
	if err != nil || !disabled.Removed {
		t.Fatalf("disable installed Skill through SDK: %+v err=%v", disabled, err)
	}

	entry, err := client.Marketplace.CreateEntry(ctx, tma.CreateMarketplaceEntryRequest{
		WorkspaceID: installed.Skill.WorkspaceID, SkillID: installed.Skill.ID, SkillVersion: installed.Version.Version,
		Summary: "Typed SDK release", Category: "quality", Tags: []string{"review"},
	})
	if err != nil || entry.Status != "draft" {
		t.Fatalf("create Marketplace entry through SDK: %+v err=%v", entry, err)
	}
	entry, err = client.Marketplace.SubmitEntry(ctx, entry.ID, tma.MarketplaceTransitionRequest{WorkspaceID: entry.WorkspaceID})
	if err != nil || entry.Status != "pending_review" {
		t.Fatalf("submit Marketplace entry through SDK: %+v err=%v", entry, err)
	}
	entry, err = client.Marketplace.PublishEntry(ctx, entry.ID, tma.MarketplaceTransitionRequest{WorkspaceID: entry.WorkspaceID, Note: "approved"})
	if err != nil || entry.Status != "published" {
		t.Fatalf("publish Marketplace entry through SDK: %+v err=%v", entry, err)
	}
	internal, err := client.Marketplace.BrowseInternal(ctx, tma.MarketplaceInternalQuery{SessionID: session.ID, Query: "typed", Tags: []string{"review"}})
	if err != nil || internal.Count != 1 || len(internal.Items) != 1 || internal.Items[0].ID != entry.ID {
		t.Fatalf("browse internal Marketplace through SDK: %+v err=%v", internal, err)
	}

	policy, err := client.Marketplace.CreatePolicy(ctx, tma.CreateMarketplacePolicyRequest{
		ScopeType: "workspace", WorkspaceID: entry.WorkspaceID,
		Config: tma.MarketplacePolicyConfig{AllowedOwners: []string{"acme"}},
	})
	if err != nil || policy.Version.Version != 1 {
		t.Fatalf("create Marketplace policy through SDK: %+v err=%v", policy, err)
	}
	version, err := client.Marketplace.PublishPolicyVersion(ctx, policy.Policy.ID, tma.PublishMarketplacePolicyRequest{
		Config: tma.MarketplacePolicyConfig{AllowedRepositories: []string{"acme/sdk-review"}, RequireCommitSHA: true},
	})
	if err != nil || version.Version != 2 {
		t.Fatalf("publish Marketplace policy through SDK: %+v err=%v", version, err)
	}
	policies, err := client.Marketplace.ListPolicies(ctx, tma.MarketplacePolicyQuery{WorkspaceID: entry.WorkspaceID})
	if err != nil || len(policies) != 1 || policies[0].CurrentVersion != 2 {
		t.Fatalf("list Marketplace policies through SDK: %+v err=%v", policies, err)
	}
	archived, err := client.Marketplace.ArchivePolicy(ctx, policy.Policy.ID)
	if err != nil || archived.Status != "archived" {
		t.Fatalf("archive Marketplace policy through SDK: %+v err=%v", archived, err)
	}
}

func TestTypedSkillsSDKRealServerE2E(t *testing.T) {
	store := newRetentionHTTPTestStore()
	server := httptest.NewServer(NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil))
	defer server.Close()
	client, err := tma.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	skill, err := client.Skills.Create(ctx, tma.CreateSkillRequest{Identifier: "sdk-review", Title: "SDK Review"})
	if err != nil {
		t.Fatalf("create Skill through SDK: %v", err)
	}
	version, err := client.Skills.CreateVersion(ctx, skill.ID, tma.CreateSkillVersionRequest{
		ContentFormat: "hybrid",
		Manifest:      tma.SkillManifest{SystemRole: "Review behavior first.", Blocks: []tma.SkillManifestBlock{{Type: "checklist", Items: []string{"regressions"}}}},
		ContentText:   "Inspect the change.",
	})
	if err != nil || version.Version != 1 || version.Assets == nil || len(version.Assets.Files) != 0 {
		t.Fatalf("create Skill version through SDK: %+v err=%v", version, err)
	}
	preview, err := client.Skills.ResolvePreview(ctx, tma.ResolveSkillsPreviewRequest{
		Skills:    tma.SkillConfig{Enabled: []tma.EnabledSkill{{Skill: skill.Identifier, Version: version.Version, Mode: "full"}}},
		MaxTokens: 1000,
	})
	if err != nil || len(preview.Skills) != 1 || preview.Rendered == nil || !strings.Contains(preview.Rendered.Content, "Review behavior first") {
		t.Fatalf("resolve Skill preview through SDK: %+v err=%v", preview, err)
	}

	environment, err := client.Environments.Create(ctx, tma.CreateEnvironmentRequest{Name: "Skill E2E", Config: json.RawMessage(`{"type":"cloud"}`)})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{EnvironmentID: environment.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSkillUsages(ctx, []internalSkills.Usage{{
		WorkspaceID: skill.WorkspaceID, SessionID: session.ID, TurnID: "turn_skill_e2e",
		SkillID: skill.ID, SkillIdentifier: skill.Identifier, SkillVersion: int(version.Version),
		RequestedMode: "full", RenderedMode: "full", Status: internalSkills.UsageResolved,
	}}); err != nil {
		t.Fatal(err)
	}
	usages, err := client.Skills.ListUsages(ctx, session.ID, "turn_skill_e2e")
	if err != nil || len(usages) != 1 || usages[0].SkillID != skill.ID {
		t.Fatalf("list Skill usage through SDK: %+v err=%v", usages, err)
	}

	retention, err := client.Skills.CreateRetentionPolicy(ctx, tma.CreateSkillRetentionPolicyRequest{
		ScopeType: "workspace", WorkspaceID: skill.WorkspaceID,
		Config: tma.SkillRetentionPolicyConfig{Enabled: true, RetentionDays: 30, DeleteLimit: 25},
	})
	if err != nil || retention.Version.Version != 1 {
		t.Fatalf("create Skill retention policy through SDK: %+v err=%v", retention, err)
	}
	effective, err := client.Skills.EffectiveRetentionPolicy(ctx, skill.WorkspaceID)
	if err != nil || effective.Policy == nil || effective.Policy.ID != retention.Policy.ID {
		t.Fatalf("get effective Skill retention policy through SDK: %+v err=%v", effective, err)
	}
	gcPreview, err := client.Skills.PreviewAssetGC(ctx, tma.SkillAssetGCRequest{WorkspaceID: skill.WorkspaceID})
	if err != nil || gcPreview.CandidateCount != 0 || gcPreview.Candidates == nil {
		t.Fatalf("preview Skill asset GC through SDK: %+v err=%v", gcPreview, err)
	}
	gcResult, err := client.Skills.RunAssetGC(ctx, tma.SkillAssetGCRequest{WorkspaceID: skill.WorkspaceID, Confirm: "DELETE"})
	if err != nil || gcResult.Run.Status != "succeeded" || gcResult.Items == nil {
		t.Fatalf("run Skill asset GC through SDK: %+v err=%v", gcResult, err)
	}
}

func TestTypedAdministrationSDKRealServerE2E(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envvars.MasterKeyEnvironmentVariable, base64.StdEncoding.EncodeToString(key))
	store := newTestStore()
	server := httptest.NewServer(NewServerWithStoreAndRunner(store, runner.NewMockRunner(store, 0, nil), nil))
	defer server.Close()
	client, err := tma.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	auth, err := client.Auth.Me(ctx)
	if err != nil || auth.Authenticated || auth.Principal != nil {
		t.Fatalf("unexpected disabled-auth state: %+v err=%v", auth, err)
	}
	created, err := client.MCP.Create(ctx, tma.CreateMCPServerRequest{
		Identifier: "sdk-admin", Name: "SDK Administration", Config: tma.MCPServerConfig{Identifier: "sdk-admin", Command: "sdk-admin-mcp"},
	})
	if err != nil {
		t.Fatalf("create MCP registry server through SDK: %v", err)
	}
	servers, err := client.MCP.List(ctx, tma.MCPServerQuery{})
	if err != nil || len(servers) != 1 || servers[0].ID != created.ID {
		t.Fatalf("unexpected MCP registry list: %+v err=%v", servers, err)
	}
	versions, err := client.MCP.Versions(ctx, created.ID)
	if err != nil || len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("unexpected MCP versions: %+v err=%v", versions, err)
	}

	variable, err := client.EnvironmentVariables.Put(ctx, "SDK_ADMIN_SECRET", tma.EnvironmentVariableQuery{}, tma.PutEnvironmentVariableRequest{Value: "not-returned"})
	if err != nil || !variable.Configured {
		t.Fatalf("put environment variable through SDK: %+v err=%v", variable, err)
	}
	variables, err := client.EnvironmentVariables.List(ctx, tma.EnvironmentVariableQuery{})
	if err != nil || len(variables) != 1 || variables[0].Name != "SDK_ADMIN_SECRET" {
		t.Fatalf("unexpected environment variables: %+v err=%v", variables, err)
	}
	encodedVariables, _ := json.Marshal(variables)
	if bytes.Contains(encodedVariables, []byte("not-returned")) {
		t.Fatalf("environment variable response exposed secret: %s", encodedVariables)
	}

	auditRecords, err := client.Audit.List(ctx, tma.OperatorAuditQuery{Action: "mcp_registry.create", Limit: 10})
	if err != nil || len(auditRecords) != 1 || auditRecords[0].ResourceID != created.ID {
		t.Fatalf("unexpected operator audit records: %+v err=%v", auditRecords, err)
	}
	if err := client.EnvironmentVariables.Delete(ctx, "SDK_ADMIN_SECRET", tma.EnvironmentVariableQuery{}); err != nil {
		t.Fatalf("delete environment variable through SDK: %v", err)
	}
	variables, err = client.EnvironmentVariables.List(ctx, tma.EnvironmentVariableQuery{})
	if err != nil || len(variables) != 0 {
		t.Fatalf("expected empty environment variable list: %+v err=%v", variables, err)
	}
}

func TestGoCoreSDKRealServerE2E(t *testing.T) {
	store := newTestStore()
	turnRunner := &sdkApprovalRunner{store: store}
	objects, err := objectstore.NewLocalFSClient(objectstore.Config{
		Provider: objectstore.ProviderLocalFS,
		Bucket:   "tma-artifacts",
		RootDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("create local object store: %v", err)
	}
	server := httptest.NewServer(NewServerWithStoreRunnerLLMDefaultsAndObjectStore(
		store, turnRunner, nil, "fake", "fake-demo", objects,
	))
	defer server.Close()

	client, err := tma.NewClient(server.URL)
	if err != nil {
		t.Fatalf("create SDK client: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	agent, err := client.Agents.Create(ctx, tma.CreateAgentRequest{
		Name: "SDK E2E Agent", LLMProvider: "fake", LLMModel: "fake-demo", System: "Complete approved work.",
	})
	if err != nil {
		t.Fatalf("create agent through SDK: %v", err)
	}
	environment, err := client.Environments.Create(ctx, tma.CreateEnvironmentRequest{
		Name: "SDK E2E Environment", Config: json.RawMessage(`{"type":"cloud"}`),
	})
	if err != nil {
		t.Fatalf("create environment through SDK: %v", err)
	}
	session, err := client.Sessions.Create(ctx, tma.CreateSessionRequest{
		AgentID: agent.ID, EnvironmentID: environment.ID, Title: "SDK acceptance",
	})
	if err != nil {
		t.Fatalf("create session through SDK: %v", err)
	}

	handle, err := client.Runs.Start(ctx, session.ID, tma.StartRunRequest{
		Input: tma.TextInput("prepare the acceptance artifact"), IdempotencyKey: "sdk-e2e-run-1",
	})
	if err != nil {
		t.Fatalf("start run through SDK: %v", err)
	}
	current, err := client.Runs.Get(ctx, session.ID, handle.Run.ID)
	if err != nil {
		t.Fatalf("get waiting run through SDK: %v", err)
	}
	if current.Status != tma.RunStatusWaitingApproval {
		t.Fatalf("expected waiting_approval, got %+v", current)
	}
	pending, err := client.Interventions.List(ctx, session.ID, managedagents.InterventionStatusPending)
	if err != nil {
		t.Fatalf("list pending interventions through SDK: %v", err)
	}
	if len(pending) != 1 || pending[0].TurnID != handle.Run.ID || pending[0].CallID != sdkE2ECallID {
		t.Fatalf("unexpected pending interventions: %+v", pending)
	}
	if err := handle.Approve(ctx, sdkE2ECallID, "approved by SDK E2E"); err != nil {
		t.Fatalf("approve run through SDK: %v", err)
	}

	result, err := handle.Wait(ctx)
	if err != nil {
		t.Fatalf("wait for run through SDK SSE: %v", err)
	}
	if result.Run.Status != tma.RunStatusCompleted || !bytes.Contains(result.Output, []byte("approved SDK result")) {
		t.Fatalf("unexpected completed run result: %+v", result)
	}
	if len(turnRunner.requests) != 2 || turnRunner.requests[1].ResumeIntervention == nil ||
		turnRunner.requests[1].ResumeIntervention.DecisionReason != "approved by SDK E2E" {
		t.Fatalf("approval context did not reach resumed runner: %+v", turnRunner.requests)
	}
	events, err := client.Runs.ListEvents(ctx, session.ID, handle.Run.ID, 0)
	if err != nil {
		t.Fatalf("list run events through SDK: %v", err)
	}
	if len(events) < 4 {
		t.Fatalf("expected run lifecycle events, got %+v", events)
	}
	for _, event := range events {
		if event.EffectiveTurnID() != handle.Run.ID {
			t.Fatalf("event is not attributed to run %s: %+v", handle.Run.ID, event)
		}
	}

	upload, err := client.Artifacts.Upload(ctx, session.ID, map[string]string{
		"turn_id":       handle.Run.ID,
		"tool_call_id":  sdkE2ECallID,
		"artifact_type": managedagents.ArtifactTypeFile,
		"description":   "SDK acceptance output",
		"metadata":      `{"source":"sdk-e2e"}`,
	}, tma.UploadFile{
		FileName: "acceptance.txt", ContentType: "text/plain", Body: strings.NewReader("sdk artifact content"),
	})
	if err != nil {
		t.Fatalf("upload artifact through SDK: %v", err)
	}
	if upload.Artifact.TurnID != handle.Run.ID || upload.Artifact.ObjectRefID != upload.ObjectRef.ID || upload.ObjectRef.SizeBytes != 20 {
		t.Fatalf("unexpected uploaded artifact: %+v", upload)
	}
	artifacts, err := client.Artifacts.List(ctx, session.ID)
	if err != nil {
		t.Fatalf("list artifacts through SDK: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ID != upload.Artifact.ID {
		t.Fatalf("unexpected artifact list: %+v", artifacts)
	}
	var downloaded bytes.Buffer
	if err := client.Artifacts.Download(ctx, session.ID, upload.Artifact.ID, &downloaded); err != nil {
		t.Fatalf("download artifact through SDK: %v", err)
	}
	if downloaded.String() != "sdk artifact content" {
		t.Fatalf("unexpected artifact content: %q", downloaded.String())
	}

	rerun, err := client.Sessions.Rerun(ctx, session.ID, tma.RerunSessionRequest{})
	if err != nil || rerun.Session.ID == session.ID || len(rerun.Events) == 0 {
		t.Fatalf("rerun session through SDK: %+v err=%v", rerun, err)
	}
	rerunTurnID := rerun.Events[0].EffectiveTurnID()
	if rerunTurnID == "" {
		t.Fatalf("rerun event does not identify its turn: %+v", rerun.Events)
	}
	rerunPending, err := client.Interventions.List(ctx, rerun.Session.ID, managedagents.InterventionStatusPending)
	if err != nil || len(rerunPending) != 1 {
		t.Fatalf("list rerun intervention through SDK: %+v err=%v", rerunPending, err)
	}
	if _, err := client.Interventions.Decide(ctx, rerun.Session.ID, rerunTurnID, rerunPending[0].CallID, "approve", "approve rerun"); err != nil {
		t.Fatalf("approve rerun through SDK: %v", err)
	}
	comparison, err := client.Sessions.Compare(ctx, session.ID, rerun.Session.ID)
	if err != nil || comparison.Left.Prompt == "" || comparison.Right.Prompt == "" || comparison.Right.Result == "" {
		t.Fatalf("compare original and rerun through SDK: %+v err=%v", comparison, err)
	}

	childAgent, err := client.Agents.Create(ctx, tma.CreateAgentRequest{
		Name: "SDK E2E Child", LLMProvider: "fake", LLMModel: "fake-demo", System: "Review one item.",
	})
	if err != nil {
		t.Fatalf("create child agent through SDK: %v", err)
	}
	groupService := newAgentToolService(store, turnRunner, nil, defaultSubagentPolicy())
	createdGroup, err := groupService.CreateTaskGroup(ctx, tools.AgentTaskGroupCreateRequest{
		ParentSessionID: session.ID,
		ParentTurnID:    handle.Run.ID,
		TemplateID:      "module_risk_audit",
		Items: []tools.AgentTaskGroupItemRequest{{
			AgentID: childAgent.ID, EnvironmentID: environment.ID, Message: "review the SDK E2E result",
		}},
	})
	if err != nil {
		t.Fatalf("create task group for SDK E2E: %v", err)
	}
	groups, err := client.Orchestration.ListTaskGroups(ctx, session.ID)
	if err != nil || len(groups) != 1 || groups[0].State.Group.ID != createdGroup.Group.ID {
		t.Fatalf("list task groups through SDK: %+v err=%v", groups, err)
	}
	group, err := client.Orchestration.GetTaskGroup(ctx, session.ID, createdGroup.Group.ID)
	if err != nil || group.State.Group.ID != createdGroup.Group.ID || len(group.State.Items) != 1 {
		t.Fatalf("get task group through SDK: %+v err=%v", group, err)
	}

	originalTrace, err := client.Traces.GetSession(ctx, session.ID, handle.Run.ID)
	if err != nil || originalTrace.TraceID == "" || len(originalTrace.Spans) == 0 {
		t.Fatalf("project original trace through SDK: %+v err=%v", originalTrace, err)
	}
	rerunTrace, err := client.Traces.GetSession(ctx, rerun.Session.ID, rerunTurnID)
	if err != nil || rerunTrace.TraceID == "" {
		t.Fatalf("project rerun trace through SDK: %+v err=%v", rerunTrace, err)
	}
	tracePage, err := client.Traces.List(ctx, tma.TraceListQuery{Limit: 1})
	if err != nil || len(tracePage.Items) != 1 || !tracePage.HasMore || tracePage.NextCursor == "" {
		t.Fatalf("list first trace cursor page through SDK: %+v err=%v", tracePage, err)
	}
	nextTracePage, err := client.Traces.List(ctx, tma.TraceListQuery{Limit: 1, Cursor: tracePage.NextCursor})
	if err != nil || len(nextTracePage.Items) != 1 || nextTracePage.Items[0].TraceID == tracePage.Items[0].TraceID {
		t.Fatalf("list second trace cursor page through SDK: %+v err=%v", nextTracePage, err)
	}
	loadedTrace, err := client.Traces.Get(ctx, originalTrace.TraceID)
	if err != nil || loadedTrace.TraceID != originalTrace.TraceID {
		t.Fatalf("get trace by ID through SDK: %+v err=%v", loadedTrace, err)
	}
	spanPage, err := client.Traces.ListSpans(ctx, tma.TraceSpanListQuery{TraceID: originalTrace.TraceID, Limit: 10})
	if err != nil || len(spanPage.Items) == 0 {
		t.Fatalf("list spans through SDK: %+v err=%v", spanPage, err)
	}
	span, err := client.Traces.GetSpan(ctx, originalTrace.TraceID, spanPage.Items[0].SpanID)
	if err != nil || span.TraceID != originalTrace.TraceID || span.Span.SpanID != spanPage.Items[0].SpanID {
		t.Fatalf("get span through SDK: %+v err=%v", span, err)
	}
}

const sdkE2ECallID = "call_sdk_e2e"

type sdkApprovalRunner struct {
	store    *testStore
	requests []runner.TurnRequest
}

func (r *sdkApprovalRunner) StartTurn(_ context.Context, request runner.TurnRequest) error {
	r.requests = append(r.requests, request)
	if request.ResumeIntervention == nil {
		_, err := r.store.SaveSessionIntervention(request.SessionID, managedagents.SaveSessionInterventionInput{
			TurnID:            request.TurnID,
			CallID:            sdkE2ECallID,
			ToolIdentifier:    "default",
			APIName:           "write_file",
			Arguments:         json.RawMessage(`{"path":"acceptance.txt"}`),
			InterventionMode:  "request_approval",
			Reason:            "artifact write requires approval",
			Continuation:      json.RawMessage(`[{"role":"assistant","content":[]}]`),
			ContinuationRound: 1,
		})
		return err
	}
	_, err := r.store.CompleteSessionTurn(request.SessionID, request.TurnID, json.RawMessage(
		`{"content":[{"type":"text","text":"approved SDK result"}]}`,
	))
	return err
}

func (r *sdkApprovalRunner) InterruptTurn(context.Context, runner.InterruptRequest) error {
	return nil
}
