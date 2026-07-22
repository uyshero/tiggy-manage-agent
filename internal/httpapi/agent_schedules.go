package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tiggy-manage-agent/internal/agentschedule"
	"tiggy-manage-agent/internal/managedagents"
)

func (s *Server) scheduleStore() (managedagents.AgentScheduleStore, error) {
	store, ok := s.store.(managedagents.AgentScheduleStore)
	if !ok {
		return nil, fmt.Errorf("%w: agent schedules are not supported by this store", managedagents.ErrInvalid)
	}
	return store, nil
}

func (s *Server) scheduleRequestContext(r *http.Request) (context.Context, managedagents.Agent, error) {
	agent, err := s.getAgentForRequest(r, r.PathValue("agent_id"))
	if err != nil {
		return nil, managedagents.Agent{}, err
	}
	ownerID := requestOwnerID(r, "")
	if ownerID == "" && agent.OwnerType == managedagents.AgentOwnerUser {
		ownerID = agent.OwnerID
	}
	ctx, err := managedagents.ContextWithDatabaseAccessScope(r.Context(), managedagents.AccessScope{
		WorkspaceID: agent.WorkspaceID, OwnerID: ownerID,
	})
	return ctx, agent, err
}

func (s *Server) createAgentSchedule(w http.ResponseWriter, r *http.Request) {
	store, err := s.scheduleStore()
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, agent, err := s.scheduleRequestContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	var input managedagents.CreateAgentScheduleInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.AgentID = agent.ID
	input.WorkspaceID = agent.WorkspaceID
	input.OwnerID = requestOwnerID(r, input.OwnerID)
	input.CreatedBy = requestActorID(r, input.CreatedBy)
	if input.CreatedBy == "" {
		input.CreatedBy = "agent-scheduler"
	}
	sessionMode, targetSessionID, approvalMode, err := managedagents.NormalizeAgentScheduleModes(input.SessionMode, input.TargetSessionID, input.ApprovalMode)
	if err != nil {
		writeError(w, err)
		return
	}
	input.SessionMode = sessionMode
	input.TargetSessionID = targetSessionID
	input.ApprovalMode = approvalMode
	if input.SessionMode == managedagents.AgentScheduleSessionNew && strings.TrimSpace(input.EnvironmentID) == "" {
		environment, createErr := store.EnsureAgentScheduleEnvironment(ctx, agent.WorkspaceID)
		if createErr != nil {
			writeError(w, createErr)
			return
		}
		input.EnvironmentID = environment.ID
	}
	created, err := store.CreateAgentSchedule(ctx, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listAgentSchedules(w http.ResponseWriter, r *http.Request) {
	store, err := s.scheduleStore()
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, agent, err := s.scheduleRequestContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListAgentSchedules(ctx, agent.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": nonNilSlice(items)})
}

func (s *Server) getAgentSchedule(w http.ResponseWriter, r *http.Request) {
	store, err := s.scheduleStore()
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, agent, err := s.scheduleRequestContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.GetAgentSchedule(ctx, r.PathValue("schedule_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if item.AgentID != agent.ID {
		writeError(w, managedagents.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateAgentSchedule(w http.ResponseWriter, r *http.Request) {
	store, err := s.scheduleStore()
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, agent, err := s.scheduleRequestContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("schedule_id")
	current, err := store.GetAgentSchedule(ctx, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if current.AgentID != agent.ID {
		writeError(w, managedagents.ErrNotFound)
		return
	}
	var input managedagents.UpdateAgentScheduleInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated, err := store.UpdateAgentSchedule(ctx, id, input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) deleteAgentSchedule(w http.ResponseWriter, r *http.Request) {
	store, err := s.scheduleStore()
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, agent, err := s.scheduleRequestContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("schedule_id")
	current, err := store.GetAgentSchedule(ctx, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if current.AgentID != agent.ID {
		writeError(w, managedagents.ErrNotFound)
		return
	}
	if err := store.DeleteAgentSchedule(ctx, id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runAgentScheduleNow(w http.ResponseWriter, r *http.Request) {
	store, err := s.scheduleStore()
	if err != nil {
		writeError(w, err)
		return
	}
	ctx, agent, err := s.scheduleRequestContext(r)
	if err != nil {
		writeError(w, err)
		return
	}
	id := r.PathValue("schedule_id")
	current, err := store.GetAgentSchedule(ctx, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if current.AgentID != agent.ID {
		writeError(w, managedagents.ErrNotFound)
		return
	}
	invocation, err := store.StartAgentScheduleNow(ctx, id, time.Now().UTC())
	if err != nil {
		writeError(w, err)
		return
	}
	result, err := (agentschedule.Service{Store: store, State: s.store, Runner: s.runner, Logger: s.logger}).TryDispatchRun(ctx, invocation)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusCreated
	if result.Status == managedagents.AgentScheduleRunWaitingSession {
		status = http.StatusAccepted
	}
	response := map[string]any{
		"schedule": invocation.Schedule,
		"run_id":   invocation.RunID,
		"status":   result.Status,
	}
	if result.Session != nil {
		response["session"] = result.Session
	}
	writeJSON(w, status, response)
}
