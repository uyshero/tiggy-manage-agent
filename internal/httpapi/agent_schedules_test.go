package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

func (s *testStore) EnsureAgentScheduleEnvironment(_ context.Context, workspaceID string) (managedagents.Environment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, environment := range s.environments {
		if environment.WorkspaceID == workspaceID && environment.Name == "Scheduled tasks" {
			return environment, nil
		}
	}
	s.nextEnvironmentID++
	now := time.Now().UTC()
	environment := managedagents.Environment{
		ID: fmt.Sprintf("env_%06d", s.nextEnvironmentID), WorkspaceID: workspaceID,
		Name: "Scheduled tasks", Config: json.RawMessage(`{"managed_by":"agent_scheduler"}`), CreatedAt: now,
	}
	s.environments[environment.ID] = environment
	return environment, nil
}

func (s *testStore) CreateAgentSchedule(_ context.Context, input managedagents.CreateAgentScheduleInput) (managedagents.AgentSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Name == "" || input.Prompt == "" {
		return managedagents.AgentSchedule{}, managedagents.ErrInvalid
	}
	expression, timezone, next, err := managedagents.NormalizeAgentSchedule(input.CronExpression, input.Timezone, time.Now().UTC())
	if err != nil {
		return managedagents.AgentSchedule{}, err
	}
	s.nextScheduleID++
	enabled := input.Enabled == nil || *input.Enabled
	now := time.Now().UTC()
	item := managedagents.AgentSchedule{ID: fmt.Sprintf("asch_%06d", s.nextScheduleID), WorkspaceID: input.WorkspaceID, OwnerID: input.OwnerID, AgentID: input.AgentID, EnvironmentID: input.EnvironmentID, Name: input.Name, Prompt: input.Prompt, CronExpression: expression, Timezone: timezone, Enabled: enabled, CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now}
	if enabled {
		item.NextRunAt = &next
	}
	s.agentSchedules[item.ID] = item
	return item, nil
}

func (s *testStore) GetAgentSchedule(_ context.Context, id string) (managedagents.AgentSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.agentSchedules[id]
	if !ok {
		return managedagents.AgentSchedule{}, managedagents.ErrNotFound
	}
	return item, nil
}

func (s *testStore) ListAgentSchedules(_ context.Context, agentID string) ([]managedagents.AgentSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]managedagents.AgentSchedule, 0)
	for _, item := range s.agentSchedules {
		if item.AgentID == agentID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *testStore) UpdateAgentSchedule(_ context.Context, id string, input managedagents.UpdateAgentScheduleInput) (managedagents.AgentSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.agentSchedules[id]
	if !ok {
		return managedagents.AgentSchedule{}, managedagents.ErrNotFound
	}
	if input.Name != nil {
		item.Name = *input.Name
	}
	if input.Prompt != nil {
		item.Prompt = *input.Prompt
	}
	if input.CronExpression != nil {
		item.CronExpression = *input.CronExpression
	}
	if input.Timezone != nil {
		item.Timezone = *input.Timezone
	}
	if input.Enabled != nil {
		item.Enabled = *input.Enabled
	}
	_, _, next, err := managedagents.NormalizeAgentSchedule(item.CronExpression, item.Timezone, time.Now().UTC())
	if err != nil {
		return managedagents.AgentSchedule{}, err
	}
	if item.Enabled {
		item.NextRunAt = &next
	} else {
		item.NextRunAt = nil
	}
	item.UpdatedAt = time.Now().UTC()
	s.agentSchedules[id] = item
	return item, nil
}

func (s *testStore) DeleteAgentSchedule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.agentSchedules[id]; !ok {
		return managedagents.ErrNotFound
	}
	delete(s.agentSchedules, id)
	return nil
}

func (s *testStore) ClaimDueAgentSchedules(context.Context, time.Time, int) ([]managedagents.AgentScheduleInvocation, error) {
	return nil, nil
}

func (s *testStore) StartAgentScheduleNow(_ context.Context, id string, now time.Time) (managedagents.AgentScheduleInvocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.agentSchedules[id]
	if !ok {
		return managedagents.AgentScheduleInvocation{}, managedagents.ErrNotFound
	}
	s.nextScheduleRunID++
	item.LastRunAt = &now
	item.LastRunStatus = managedagents.AgentScheduleRunPending
	s.agentSchedules[id] = item
	return managedagents.AgentScheduleInvocation{RunID: fmt.Sprintf("asrun_%06d", s.nextScheduleRunID), ScheduledFor: now, Schedule: item}, nil
}

func (s *testStore) CompleteAgentScheduleRun(_ context.Context, input managedagents.CompleteAgentScheduleRunInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.agentSchedules[input.ScheduleID]
	if !ok {
		return managedagents.ErrNotFound
	}
	item.LastSessionID = input.SessionID
	item.LastRunStatus = input.Status
	item.LastError = input.Error
	s.agentSchedules[item.ID] = item
	return nil
}

func TestAgentScheduleCRUDAndRunNow(t *testing.T) {
	store := newTestStore()
	runner := &recordingRunner{}
	server := NewServerWithStoreAndRunner(store, runner, nil)
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{"name":"Scheduled Agent","llm_provider":"fake","llm_model":"fake-demo"}`)
	created := postJSON[managedagents.AgentSchedule](t, server, "/v1/agents/"+agent.ID+"/schedules", `{
		"name":"Morning report","prompt":"Summarize yesterday","cron_expression":"0 9 * * 1-5","timezone":"Asia/Shanghai"
	}`)
	if created.AgentID != agent.ID || created.EnvironmentID == "" || created.NextRunAt == nil {
		t.Fatalf("unexpected created schedule: %#v", created)
	}
	list := getJSON[struct {
		Schedules []managedagents.AgentSchedule `json:"schedules"`
	}](t, server, "/v1/agents/"+agent.ID+"/schedules")
	if len(list.Schedules) != 1 {
		t.Fatalf("expected one schedule, got %d", len(list.Schedules))
	}
	disabled := postJSONWithStatus[managedagents.AgentSchedule](t, server, http.MethodPatch, "/v1/agents/"+agent.ID+"/schedules/"+created.ID, `{"enabled":false}`, http.StatusOK)
	if disabled.Enabled || disabled.NextRunAt != nil {
		t.Fatalf("expected disabled schedule, got %#v", disabled)
	}
	run := postJSON[struct {
		Session managedagents.Session `json:"session"`
		RunID   string                `json:"run_id"`
	}](t, server, "/v1/agents/"+agent.ID+"/schedules/"+created.ID+"/run", `{}`)
	if run.Session.ID == "" || run.RunID == "" || len(runner.starts) != 1 {
		t.Fatalf("unexpected run response=%#v requests=%d", run, len(runner.starts))
	}
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/v1/agents/"+agent.ID+"/schedules/"+created.ID, nil))
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}
