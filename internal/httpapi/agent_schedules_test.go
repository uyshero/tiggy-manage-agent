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
	sessionMode, targetSessionID, approvalMode, err := managedagents.NormalizeAgentScheduleModes(input.SessionMode, input.TargetSessionID, input.ApprovalMode)
	if err != nil {
		return managedagents.AgentSchedule{}, err
	}
	if sessionMode == managedagents.AgentScheduleSessionExisting {
		target, ok := s.sessions[targetSessionID]
		if !ok || target.ArchivedAt != nil || target.Status == managedagents.SessionStatusTerminated {
			return managedagents.AgentSchedule{}, managedagents.ErrInvalid
		}
		if target.AgentID != input.AgentID {
			return managedagents.AgentSchedule{}, managedagents.ErrInvalid
		}
		input.EnvironmentID = target.EnvironmentID
	}
	s.nextScheduleID++
	enabled := input.Enabled == nil || *input.Enabled
	now := time.Now().UTC()
	item := managedagents.AgentSchedule{ID: fmt.Sprintf("asch_%06d", s.nextScheduleID), WorkspaceID: input.WorkspaceID, OwnerID: input.OwnerID, AgentID: input.AgentID, EnvironmentID: input.EnvironmentID, SessionMode: sessionMode, TargetSessionID: targetSessionID, ApprovalMode: approvalMode, Name: input.Name, Prompt: input.Prompt, CronExpression: expression, Timezone: timezone, Enabled: enabled, CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now}
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
	if input.SessionMode != nil {
		item.SessionMode = *input.SessionMode
	}
	if input.TargetSessionID != nil {
		item.TargetSessionID = *input.TargetSessionID
	}
	if input.ApprovalMode != nil {
		item.ApprovalMode = *input.ApprovalMode
	}
	var err error
	item.SessionMode, item.TargetSessionID, item.ApprovalMode, err = managedagents.NormalizeAgentScheduleModes(item.SessionMode, item.TargetSessionID, item.ApprovalMode)
	if err != nil {
		return managedagents.AgentSchedule{}, err
	}
	if item.SessionMode == managedagents.AgentScheduleSessionExisting {
		target, ok := s.sessions[item.TargetSessionID]
		if !ok || target.AgentID != item.AgentID || target.ArchivedAt != nil || target.Status == managedagents.SessionStatusTerminated {
			return managedagents.AgentSchedule{}, managedagents.ErrInvalid
		}
		item.EnvironmentID = target.EnvironmentID
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

func (s *testStore) ClaimRunnableAgentScheduleRuns(_ context.Context, _ time.Time, limit int, _ time.Duration) ([]managedagents.AgentScheduleInvocation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]managedagents.AgentScheduleInvocation, 0, limit)
	for id, invocation := range s.agentScheduleRuns {
		if len(items) >= limit {
			break
		}
		if s.agentScheduleRunStatuses[id] != managedagents.AgentScheduleRunPending && s.agentScheduleRunStatuses[id] != managedagents.AgentScheduleRunWaitingSession {
			continue
		}
		if invocation.Schedule.SessionMode == managedagents.AgentScheduleSessionExisting {
			target, ok := s.sessions[invocation.Schedule.TargetSessionID]
			if !ok || target.Status != managedagents.SessionStatusIdle || target.ArchivedAt != nil {
				continue
			}
		}
		s.agentScheduleRunStatuses[id] = managedagents.AgentScheduleRunDispatching
		items = append(items, invocation)
	}
	return items, nil
}

func (s *testStore) ClaimAgentScheduleRun(_ context.Context, runID string, _ time.Time, _ time.Duration) (managedagents.AgentScheduleInvocation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	invocation, ok := s.agentScheduleRuns[runID]
	if !ok {
		return managedagents.AgentScheduleInvocation{}, false, managedagents.ErrNotFound
	}
	status := s.agentScheduleRunStatuses[runID]
	if status != managedagents.AgentScheduleRunPending && status != managedagents.AgentScheduleRunWaitingSession {
		return managedagents.AgentScheduleInvocation{}, false, nil
	}
	if invocation.Schedule.SessionMode == managedagents.AgentScheduleSessionExisting {
		target, ok := s.sessions[invocation.Schedule.TargetSessionID]
		if !ok || target.Status != managedagents.SessionStatusIdle || target.ArchivedAt != nil {
			return managedagents.AgentScheduleInvocation{}, false, nil
		}
	}
	s.agentScheduleRunStatuses[runID] = managedagents.AgentScheduleRunDispatching
	return invocation, true, nil
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
	invocation := managedagents.AgentScheduleInvocation{RunID: fmt.Sprintf("asrun_%06d", s.nextScheduleRunID), ScheduledFor: now, Schedule: item}
	s.agentScheduleRuns[invocation.RunID] = invocation
	s.agentScheduleRunStatuses[invocation.RunID] = managedagents.AgentScheduleRunPending
	return invocation, nil
}

func (s *testStore) DeferAgentScheduleRun(_ context.Context, runID string, scheduleID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	invocation, ok := s.agentScheduleRuns[runID]
	if !ok || invocation.Schedule.ID != scheduleID {
		return managedagents.ErrNotFound
	}
	s.agentScheduleRunStatuses[runID] = managedagents.AgentScheduleRunWaitingSession
	item := s.agentSchedules[scheduleID]
	item.LastRunStatus = managedagents.AgentScheduleRunWaitingSession
	s.agentSchedules[scheduleID] = item
	return nil
}

func (s *testStore) ReconcileInvalidAgentScheduleRuns(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
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
	s.agentScheduleRunStatuses[input.RunID] = input.Status
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
		Session *managedagents.Session `json:"session"`
		RunID   string                 `json:"run_id"`
		Status  string                 `json:"status"`
	}](t, server, "/v1/agents/"+agent.ID+"/schedules/"+created.ID+"/run", `{}`)
	if run.Session == nil || run.Session.ID == "" || run.RunID == "" || run.Status != managedagents.AgentScheduleRunDispatched || len(runner.starts) != 1 {
		t.Fatalf("unexpected run response=%#v requests=%d", run, len(runner.starts))
	}
	deleteResponse := httptest.NewRecorder()
	server.ServeHTTP(deleteResponse, httptest.NewRequest(http.MethodDelete, "/v1/agents/"+agent.ID+"/schedules/"+created.ID, nil))
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestAgentScheduleExistingSessionDispatchesThenQueuesWhenBusy(t *testing.T) {
	store := newTestStore()
	runner := &recordingRunner{}
	server := NewServerWithStoreAndRunner(store, runner, nil)
	agent := postJSON[managedagents.Agent](t, server, "/v1/agents", `{"name":"Bound Agent","llm_provider":"fake","llm_model":"fake-demo"}`)
	environment, err := store.EnsureAgentScheduleEnvironment(t.Context(), managedagents.DefaultWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(managedagents.CreateSessionInput{
		WorkspaceID: managedagents.DefaultWorkspaceID,
		AgentID:     agent.ID, EnvironmentID: environment.ID, Title: "Bound session",
	})
	if err != nil {
		t.Fatal(err)
	}
	created := postJSON[managedagents.AgentSchedule](t, server, "/v1/agents/"+agent.ID+"/schedules", fmt.Sprintf(`{
		"name":"Bound report","prompt":"Continue in this session","cron_expression":"0 9 * * *",
		"session_mode":"existing_session","target_session_id":%q,"approval_mode":"full_access"
	}`, session.ID))
	if created.TargetSessionID != session.ID || created.EnvironmentID != session.EnvironmentID || created.ApprovalMode != managedagents.AgentScheduleApprovalFullAccess {
		t.Fatalf("unexpected bound schedule: %#v", created)
	}

	first := postJSON[struct {
		Session *managedagents.Session `json:"session"`
		Status  string                 `json:"status"`
	}](t, server, "/v1/agents/"+agent.ID+"/schedules/"+created.ID+"/run", `{}`)
	if first.Status != managedagents.AgentScheduleRunDispatched || first.Session == nil || first.Session.ID != session.ID {
		t.Fatalf("expected existing session dispatch, got %#v", first)
	}
	second := postJSONWithStatus[struct {
		Session *managedagents.Session `json:"session"`
		RunID   string                 `json:"run_id"`
		Status  string                 `json:"status"`
	}](t, server, http.MethodPost, "/v1/agents/"+agent.ID+"/schedules/"+created.ID+"/run", `{}`, http.StatusAccepted)
	if second.Status != managedagents.AgentScheduleRunWaitingSession || second.Session != nil || second.RunID == "" {
		t.Fatalf("expected busy session run to queue, got %#v", second)
	}
	if len(store.sessions) != 1 || len(runner.starts) != 1 {
		t.Fatalf("busy bound run must not create or start another session: sessions=%d starts=%d", len(store.sessions), len(runner.starts))
	}
}
