package httpapi

import (
	"encoding/json"
	"testing"

	"tiggy-manage-agent/internal/managedagents"
)

func TestNewRunFollowsLatestAgentConfigByDefault(t *testing.T) {
	store := newTestStore()
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		Name: "Follow Latest", LLMProvider: "fake", LLMModel: "fake-demo", System: "v1",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{Name: "cloud"})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{AgentID: agent.ID, EnvironmentID: environment.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	updated, err := store.CreateAgentConfigVersion(managedagents.CreateAgentConfigVersionInput{
		AgentID: agent.ID, ExpectedCurrentVersion: 1, LLMProvider: "fake", LLMModel: "fake-demo", System: "v2",
	})
	if err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	started, err := store.StartSessionRunContext(t.Context(), session.ID, managedagents.StartSessionRunInput{
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"use latest"}]}`),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if started.Run.AgentID != agent.ID || started.Run.AgentConfigVersion != updated.CurrentConfigVersion {
		t.Fatalf("run did not freeze latest config: %+v", started.Run)
	}
	if len(started.Events) != 3 || started.Events[0].Type != managedagents.EventSessionConfigUpdated {
		t.Fatalf("expected config update before turn events: %+v", started.Events)
	}
	current, err := store.GetSession(session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if current.AgentConfigVersion != updated.CurrentConfigVersion {
		t.Fatalf("expected session config %d, got %d", updated.CurrentConfigVersion, current.AgentConfigVersion)
	}
}

func TestPinnedSessionKeepsConfigOnNewRun(t *testing.T) {
	store := newTestStore()
	agent, err := store.CreateAgent(managedagents.CreateAgentInput{
		Name: "Pinned", LLMProvider: "fake", LLMModel: "fake-demo", System: "v1",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	environment, err := store.CreateEnvironment(managedagents.CreateEnvironmentInput{Name: "cloud"})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{AgentID: agent.ID, EnvironmentID: environment.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := store.UpdateSessionRuntimeSettings(session.ID, managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: json.RawMessage(`{"agent_config_update_policy":"pinned"}`),
	}); err != nil {
		t.Fatalf("pin session: %v", err)
	}
	if _, err := store.CreateAgentConfigVersion(managedagents.CreateAgentConfigVersionInput{
		AgentID: agent.ID, ExpectedCurrentVersion: 1, LLMProvider: "fake", LLMModel: "fake-demo", System: "v2",
	}); err != nil {
		t.Fatalf("create agent config: %v", err)
	}

	started, err := store.StartSessionRunContext(t.Context(), session.ID, managedagents.StartSessionRunInput{
		Payload: json.RawMessage(`{"content":[{"type":"text","text":"stay pinned"}]}`),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if started.Run.AgentConfigVersion != 1 {
		t.Fatalf("expected pinned run config 1, got %d", started.Run.AgentConfigVersion)
	}
	if len(started.Events) != 2 || started.Events[0].Type != managedagents.EventSessionStatusRunning {
		t.Fatalf("unexpected pinned turn events: %+v", started.Events)
	}
}
