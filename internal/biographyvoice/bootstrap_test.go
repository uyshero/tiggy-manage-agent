package biographyvoice

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"tiggy-manage-agent/sdk/tma"
)

type fakeAgentAdmin struct {
	agents      []tma.Agent
	createCalls int
	updateCalls int
	request     tma.CreateAgentRequest
	update      tma.UpdateAgentRequest
}

func (admin *fakeAgentAdmin) List(context.Context) ([]tma.Agent, error) {
	return admin.agents, nil
}

func (admin *fakeAgentAdmin) Create(_ context.Context, request tma.CreateAgentRequest) (tma.Agent, error) {
	admin.createCalls++
	admin.request = request
	return tma.Agent{ID: "agent-biography", Name: request.Name, WorkspaceID: request.WorkspaceID, EnvironmentID: request.EnvironmentID}, nil
}

func (admin *fakeAgentAdmin) Update(_ context.Context, agentID string, request tma.UpdateAgentRequest) (tma.Agent, error) {
	admin.updateCalls++
	admin.update = request
	environmentID := ""
	if request.EnvironmentID != nil {
		environmentID = *request.EnvironmentID
	}
	return tma.Agent{ID: agentID, Name: "自传采访者", EnvironmentID: environmentID}, nil
}

type fakeEnvironmentAdmin struct{ environments []tma.Environment }

func (admin fakeEnvironmentAdmin) List(context.Context) ([]tma.Environment, error) {
	return admin.environments, nil
}

type fakeSkillAdmin struct {
	skills             []tma.Skill
	versions           map[string][]tma.SkillVersion
	createCalls        int
	createVersionCalls int
}

func (admin *fakeSkillAdmin) List(context.Context, tma.SkillListQuery) ([]tma.Skill, error) {
	return admin.skills, nil
}

func (admin *fakeSkillAdmin) Create(_ context.Context, request tma.CreateSkillRequest) (tma.Skill, error) {
	admin.createCalls++
	skill := tma.Skill{
		ID: fmt.Sprintf("skill-%d", admin.createCalls), WorkspaceID: request.WorkspaceID,
		Identifier: request.Identifier, Title: request.Title, Description: request.Description,
	}
	admin.skills = append(admin.skills, skill)
	return skill, nil
}

func (admin *fakeSkillAdmin) ListVersions(_ context.Context, skillID string) ([]tma.SkillVersion, error) {
	return admin.versions[skillID], nil
}

func (admin *fakeSkillAdmin) CreateVersion(_ context.Context, skillID string, request tma.CreateSkillVersionRequest) (tma.SkillVersion, error) {
	admin.createVersionCalls++
	version := tma.SkillVersion{
		ID: fmt.Sprintf("version-%d", admin.createVersionCalls), SkillID: skillID,
		Version: int32(len(admin.versions[skillID]) + 1), ContentFormat: request.ContentFormat,
		Manifest: request.Manifest, ContentText: request.ContentText,
	}
	admin.versions[skillID] = append(admin.versions[skillID], version)
	return version, nil
}

func TestEnsureBiographySkillsPublishesAndReusesFrozenVersions(t *testing.T) {
	admin := &fakeSkillAdmin{versions: map[string][]tma.SkillVersion{}}
	config, statuses, err := EnsureBiographySkills(t.Context(), admin, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Enabled) != 3 || len(statuses) != 3 || admin.createCalls != 3 || admin.createVersionCalls != 3 {
		t.Fatalf("unexpected first bootstrap: config=%+v statuses=%+v admin=%+v", config, statuses, admin)
	}
	for index, enabled := range config.Enabled {
		if enabled.SkillID == "" || enabled.Version != 1 || enabled.Mode != "full" || enabled.Priority <= 0 {
			t.Fatalf("invalid frozen binding at %d: %+v", index, enabled)
		}
		if !statuses[index].Created || !statuses[index].PublishedVersion {
			t.Fatalf("first bootstrap did not report creation at %d: %+v", index, statuses[index])
		}
	}

	reusedConfig, reusedStatuses, err := EnsureBiographySkills(t.Context(), admin, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if admin.createCalls != 3 || admin.createVersionCalls != 3 {
		t.Fatalf("idempotent bootstrap created extra records: %+v", admin)
	}
	for _, status := range reusedStatuses {
		if status.Created || status.PublishedVersion {
			t.Fatalf("reused skill reported as created: %+v", status)
		}
	}
	if !biographySkillConfigsEqual(mustJSON(t, config), reusedConfig) {
		t.Fatalf("reused config changed: first=%+v second=%+v", config, reusedConfig)
	}
}

func TestEnsureBiographySkillsPublishesChangedContentOnly(t *testing.T) {
	admin := &fakeSkillAdmin{versions: map[string][]tma.SkillVersion{}}
	config, _, err := EnsureBiographySkills(t.Context(), admin, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	changedSkillID := config.Enabled[0].SkillID
	admin.versions[changedSkillID][0].ContentText = "stale interview instructions"

	updated, statuses, err := EnsureBiographySkills(t.Context(), admin, "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if admin.createCalls != 3 || admin.createVersionCalls != 4 || updated.Enabled[0].Version != 2 {
		t.Fatalf("changed content was not versioned once: config=%+v admin=%+v", updated, admin)
	}
	if !statuses[0].PublishedVersion || statuses[1].PublishedVersion || statuses[2].PublishedVersion {
		t.Fatalf("unexpected version publication statuses: %+v", statuses)
	}
}

func TestSelectBiographySkillsSplitsRealtimeAndBackgroundResponsibilities(t *testing.T) {
	all := tma.SkillConfig{Enabled: []tma.EnabledSkill{
		{Skill: "conduct-biography-interview", SkillID: "skill-interview", Version: 2},
		{Skill: "verify-biography-facts", SkillID: "skill-facts", Version: 1},
		{Skill: "structure-biography-chapters", SkillID: "skill-chapters", Version: 2},
	}}
	interviewer := SelectBiographySkills(all, "conduct-biography-interview", "verify-biography-facts")
	organizer := SelectBiographySkills(all, "structure-biography-chapters", "verify-biography-facts")
	if len(interviewer.Enabled) != 2 || interviewer.Enabled[0].Skill != "conduct-biography-interview" ||
		interviewer.Enabled[1].Skill != "verify-biography-facts" {
		t.Fatalf("unexpected interviewer skills: %+v", interviewer)
	}
	if len(organizer.Enabled) != 2 || organizer.Enabled[0].Skill != "verify-biography-facts" || organizer.Enabled[1].Skill != "structure-biography-chapters" {
		t.Fatalf("unexpected organizer skills: %+v", organizer)
	}
}

func TestEnsureBiographyAgentBindsFrozenSkills(t *testing.T) {
	desired := tma.SkillConfig{Enabled: []tma.EnabledSkill{{
		SkillID: "skill-interview", Skill: "conduct-biography-interview", Version: 2, Mode: "full", Priority: 100,
	}}}
	admin := &fakeAgentAdmin{agents: []tma.Agent{{
		ID: "agent-existing", Name: "自传采访者",
		ConfigVersion: tma.AgentConfigVersion{System: BiographyInterviewerSystemPrompt, LLMProvider: "doubao", LLMModel: "doubao-seed"},
	}}}
	_, created, err := EnsureBiographyAgent(t.Context(), admin, BiographyAgentBootstrapConfig{
		LLMProvider: "doubao", LLMModel: "doubao-seed", Skills: desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || admin.updateCalls != 1 || admin.update.Skills == nil || !biographySkillConfigsEqual(*admin.update.Skills, desired) {
		t.Fatalf("frozen skills were not bound: created=%v update=%+v", created, admin.update)
	}
}

func TestEnsureBiographyAgentDisablesUnneededPlatformTools(t *testing.T) {
	desired := json.RawMessage(`{"disable_platform_defaults":true}`)
	admin := &fakeAgentAdmin{agents: []tma.Agent{{
		ID: "agent-existing", Name: "自传采访者",
		ConfigVersion: tma.AgentConfigVersion{System: BiographyInterviewerSystemPrompt, LLMProvider: "doubao", LLMModel: "doubao-seed"},
	}}}
	_, created, err := EnsureBiographyAgent(t.Context(), admin, BiographyAgentBootstrapConfig{
		LLMProvider: "doubao", LLMModel: "doubao-seed", Tools: desired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || admin.update.Tools == nil || !biographyJSONEqual(*admin.update.Tools, desired) {
		t.Fatalf("tool-less configuration was not bound: created=%v update=%+v", created, admin.update)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEnsureBiographyAgentReusesExistingAgent(t *testing.T) {
	admin := &fakeAgentAdmin{agents: []tma.Agent{{
		ID: "agent-existing", Name: "自传采访者", WorkspaceID: "workspace-1",
		ConfigVersion: tma.AgentConfigVersion{System: BiographyInterviewerSystemPrompt, LLMProvider: "doubao", LLMModel: "doubao-seed"},
	}}}
	agent, created, err := EnsureBiographyAgent(t.Context(), admin, BiographyAgentBootstrapConfig{
		WorkspaceID: "workspace-1", LLMProvider: "doubao", LLMModel: "doubao-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || agent.ID != "agent-existing" || admin.createCalls != 0 || admin.updateCalls != 0 {
		t.Fatalf("existing agent was not reused: agent=%+v created=%v calls=%d", agent, created, admin.createCalls)
	}
}

func TestEnsureBiographyAgentBindsExistingAgentToConfiguredEnvironment(t *testing.T) {
	admin := &fakeAgentAdmin{agents: []tma.Agent{{
		ID: "agent-existing", Name: "自传采访者",
		ConfigVersion: tma.AgentConfigVersion{System: BiographyInterviewerSystemPrompt, LLMProvider: "doubao", LLMModel: "doubao-seed"},
	}}}
	agent, created, err := EnsureBiographyAgent(t.Context(), admin, BiographyAgentBootstrapConfig{
		EnvironmentID: "environment-1", LLMProvider: "doubao", LLMModel: "doubao-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || agent.EnvironmentID != "environment-1" || admin.updateCalls != 1 || admin.update.EnvironmentID == nil {
		t.Fatalf("existing agent environment was not bound: agent=%+v created=%v admin=%+v", agent, created, admin)
	}
}

func TestEnsureBiographyAgentUpdatesExistingInterviewRole(t *testing.T) {
	admin := &fakeAgentAdmin{agents: []tma.Agent{{
		ID: "agent-existing", Name: "自传采访者",
		ConfigVersion: tma.AgentConfigVersion{System: "旧的采访提示", LLMProvider: "doubao", LLMModel: "doubao-seed"},
	}}}
	_, created, err := EnsureBiographyAgent(t.Context(), admin, BiographyAgentBootstrapConfig{
		LLMProvider: "doubao", LLMModel: "doubao-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || admin.updateCalls != 1 || admin.update.System == nil || *admin.update.System != BiographyInterviewerSystemPrompt {
		t.Fatalf("existing biography role was not updated: created=%v update=%+v", created, admin.update)
	}
}

func TestResolveBiographyEnvironmentPrefersGeneralSandbox(t *testing.T) {
	environmentID, err := ResolveBiographyEnvironment(t.Context(), fakeEnvironmentAdmin{environments: []tma.Environment{
		{ID: "environment-special", Name: "PPT Sandbox", WorkspaceID: "workspace-1"},
		{ID: "environment-general", Name: "通用 Sandbox", WorkspaceID: "workspace-1"},
	}}, "", "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if environmentID != "environment-general" {
		t.Fatalf("unexpected environment ID %q", environmentID)
	}
}

func TestResolveBiographyEnvironmentRejectsAmbiguousWorkspace(t *testing.T) {
	_, err := ResolveBiographyEnvironment(t.Context(), fakeEnvironmentAdmin{environments: []tma.Environment{
		{ID: "environment-a", Name: "A", WorkspaceID: "workspace-1"},
		{ID: "environment-b", Name: "B", WorkspaceID: "workspace-1"},
	}}, "", "workspace-1")
	if err == nil {
		t.Fatal("expected ambiguous environments to be rejected")
	}
}

func TestEnsureBiographyAgentCreatesManagedPrivateAgent(t *testing.T) {
	admin := &fakeAgentAdmin{}
	agent, created, err := EnsureBiographyAgent(t.Context(), admin, BiographyAgentBootstrapConfig{
		Name: "人生书采访者", WorkspaceID: "workspace-1", EnvironmentID: "environment-1",
		LLMProvider: "doubao", LLMModel: "doubao-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created || agent.ID != "agent-biography" || admin.createCalls != 1 {
		t.Fatalf("agent was not created: agent=%+v created=%v calls=%d", agent, created, admin.createCalls)
	}
	if admin.request.System != BiographyInterviewerSystemPrompt || admin.request.OwnerType != "workspace" ||
		admin.request.Visibility != "workspace" || admin.request.AgentKind != "custom" || admin.request.EnvironmentID != "environment-1" {
		t.Fatalf("unexpected biography agent request: %+v", admin.request)
	}
}

func TestEnsureBiographyAgentUsesOrganizerRole(t *testing.T) {
	admin := &fakeAgentAdmin{}
	_, created, err := EnsureBiographyAgent(t.Context(), admin, BiographyAgentBootstrapConfig{
		Name: "自传章节整理者", System: BiographyOrganizerSystemPrompt,
		WorkspaceID: "workspace-1", EnvironmentID: "environment-1",
		LLMProvider: "doubao", LLMModel: "doubao-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created || admin.request.System != BiographyOrganizerSystemPrompt || admin.request.Name != "自传章节整理者" {
		t.Fatalf("unexpected organizer agent request: %+v", admin.request)
	}
}
