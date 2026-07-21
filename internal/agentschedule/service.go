package agentschedule

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
)

type Service struct {
	Store  managedagents.AgentScheduleStore
	State  managedagents.Store
	Runner runner.Runner
	Logger *slog.Logger
	Limit  int
}

func (s Service) RunOnce(ctx context.Context, now time.Time) (int, error) {
	if s.Store == nil || s.State == nil || s.Runner == nil {
		return 0, fmt.Errorf("agent schedule store, state store, and runner are required")
	}
	limit := s.Limit
	if limit <= 0 {
		limit = 20
	}
	invocations, err := s.Store.ClaimDueAgentSchedules(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	for _, invocation := range invocations {
		if _, err := s.Dispatch(ctx, invocation); err != nil {
			s.logger().Error("agent schedule dispatch failed", "schedule_id", invocation.Schedule.ID, "run_id", invocation.RunID, "error", err)
		}
	}
	return len(invocations), nil
}

func (s Service) Dispatch(ctx context.Context, invocation managedagents.AgentScheduleInvocation) (managedagents.Session, error) {
	schedule := invocation.Schedule
	scopedCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{
		WorkspaceID: schedule.WorkspaceID,
		OwnerID:     schedule.OwnerID,
	})
	if err != nil {
		return managedagents.Session{}, err
	}
	fail := func(cause error, sessionID string) (managedagents.Session, error) {
		_ = s.Store.CompleteAgentScheduleRun(scopedCtx, managedagents.CompleteAgentScheduleRunInput{
			RunID: invocation.RunID, ScheduleID: schedule.ID, SessionID: sessionID,
			Status: managedagents.AgentScheduleRunFailed, Error: cause.Error(),
		})
		return managedagents.Session{}, cause
	}
	title := strings.TrimSpace(schedule.Name)
	if title == "" {
		title = "Scheduled agent task"
	}
	session, err := managedagents.CreateSessionWithContext(scopedCtx, s.State, managedagents.CreateSessionInput{
		WorkspaceID: schedule.WorkspaceID, OwnerID: schedule.OwnerID, AgentID: schedule.AgentID,
		EnvironmentID: schedule.EnvironmentID, Title: title, CreatedBy: schedule.CreatedBy,
	})
	if err != nil {
		return fail(err, "")
	}
	payload, _ := json.Marshal(map[string]any{
		"content":         []map[string]string{{"type": "text", "text": schedule.Prompt}},
		"schedule_id":     schedule.ID,
		"schedule_run_id": invocation.RunID,
		"scheduled_for":   invocation.ScheduledFor.Format(time.RFC3339),
	})
	events, err := managedagents.AppendEventsWithContext(scopedCtx, s.State, session.ID, []managedagents.AppendEventInput{{
		Type: managedagents.EventUserMessage, Payload: payload,
	}})
	if err != nil {
		_ = managedagents.DeleteSessionWithContext(scopedCtx, s.State, session.ID)
		return fail(err, "")
	}
	var startEvent managedagents.Event
	for _, event := range events {
		if event.Type == managedagents.EventUserMessage {
			startEvent = event
			break
		}
	}
	turnID := payloadString(startEvent.Payload, "turn_id")
	if turnID == "" {
		return fail(fmt.Errorf("scheduled user message did not create a turn"), session.ID)
	}
	if err := s.Runner.StartTurn(scopedCtx, runner.TurnRequest{
		SessionID: session.ID, TurnID: turnID, UserEventSeq: startEvent.Seq,
		UserPayload: startEvent.Payload,
		Scope:       managedagents.AccessScope{WorkspaceID: schedule.WorkspaceID, OwnerID: schedule.OwnerID},
	}); err != nil {
		_, _ = managedagents.FailSessionTurnWithContext(scopedCtx, s.State, session.ID, turnID, err.Error())
		return fail(err, session.ID)
	}
	if err := s.Store.CompleteAgentScheduleRun(scopedCtx, managedagents.CompleteAgentScheduleRunInput{
		RunID: invocation.RunID, ScheduleID: schedule.ID, SessionID: session.ID,
		Status: managedagents.AgentScheduleRunDispatched,
	}); err != nil {
		return managedagents.Session{}, err
	}
	s.logger().Info("agent schedule dispatched", "schedule_id", schedule.ID, "run_id", invocation.RunID, "session_id", session.ID)
	return session, nil
}

func Start(ctx context.Context, service Service, interval time.Duration) func() {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		run := func() {
			count, err := service.RunOnce(workerCtx, time.Now().UTC())
			if err != nil && workerCtx.Err() == nil {
				service.logger().Error("agent schedule poll failed", "error", err)
			}
			if count > 0 {
				service.logger().Info("agent schedules claimed", "count", count)
			}
		}
		run()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (s Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]any
	if json.Unmarshal(payload, &object) != nil {
		return ""
	}
	value, _ := object[key].(string)
	return value
}
