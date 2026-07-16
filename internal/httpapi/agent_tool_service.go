package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/tools"
)

const maxSubagentSpawnDepth = 3
const maxSubagentChildrenPerTurn = 5
const maxSubagentChildrenPerSession = 20

type agentToolService struct {
	store  managedagents.Store
	runner runner.Runner
	logger *slog.Logger
	policy SubagentPolicy
}

func newAgentToolService(store managedagents.Store, turnRunner runner.Runner, logger *slog.Logger, policy SubagentPolicy) tools.AgentToolService {
	if logger == nil {
		logger = slog.Default()
	}
	if policy == (SubagentPolicy{}) {
		policy = defaultSubagentPolicy()
	}
	return agentToolService{
		store:  store,
		runner: turnRunner,
		logger: logger,
		policy: policy,
	}
}

func (s agentToolService) Spawn(ctx context.Context, request tools.AgentSpawnRequest) (tools.AgentSpawnResponse, error) {
	parentSession, err := s.parentSession(ctx, request.ParentSessionID)
	if err != nil {
		return tools.AgentSpawnResponse{}, err
	}
	agentID := strings.TrimSpace(request.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(request.Agent)
	}
	if agentID == "" {
		agentID = parentSession.AgentID
	}
	environmentID := strings.TrimSpace(request.EnvironmentID)
	if environmentID == "" {
		environmentID = parentSession.EnvironmentID
	}
	title := strings.TrimSpace(request.Title)
	if title == "" && strings.TrimSpace(request.Message) != "" {
		title = truncateString(request.Message, 80)
	}
	createdBy := fmt.Sprintf("agent.spawn:%s:%s", request.ParentSessionID, request.ParentTurnID)
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: parentSession.WorkspaceID, OwnerID: parentSession.OwnerID})
	if err != nil {
		return tools.AgentSpawnResponse{}, err
	}
	session, err := managedagents.CreateSubagentSessionWithContext(databaseCtx, s.store, managedagents.CreateSubagentSessionInput{
		Session: managedagents.CreateSessionInput{
			AgentID:         agentID,
			EnvironmentID:   environmentID,
			ParentSessionID: request.ParentSessionID,
			ParentTurnID:    request.ParentTurnID,
			Title:           title,
			CreatedBy:       createdBy,
		},
		Limits: s.policy.storeLimits(),
	})
	if err != nil {
		var violation managedagents.SubagentQuotaViolation
		if errors.As(err, &violation) {
			return tools.AgentSpawnResponse{}, s.rejectSpawn(databaseCtx, parentSession, request.ParentTurnID, subagentQuotaError(violation.Type, violation.Message, violation.State))
		}
		return tools.AgentSpawnResponse{}, err
	}
	response := tools.AgentSpawnResponse{
		Session:   session,
		CreatedBy: createdBy,
	}
	if strings.TrimSpace(request.Message) == "" {
		return response, nil
	}
	sendResponse, err := s.SendMessage(ctx, tools.AgentSendMessageRequest{
		ParentSessionID: request.ParentSessionID,
		ParentTurnID:    request.ParentTurnID,
		SessionID:       session.ID,
		Message:         request.Message,
	})
	if err != nil {
		return tools.AgentSpawnResponse{}, err
	}
	response.Started = sendResponse.Started
	response.InitialEvents = sendResponse.Events
	response.Queued = sendResponse.Queued
	response.QueueRequest = sendResponse.QueueRequest
	return response, nil
}

func (s agentToolService) SendMessage(ctx context.Context, request tools.AgentSendMessageRequest) (tools.AgentSendMessageResponse, error) {
	parentSession, err := s.parentSession(ctx, request.ParentSessionID)
	if err != nil {
		return tools.AgentSendMessageResponse{}, err
	}
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{
		WorkspaceID: parentSession.WorkspaceID,
		OwnerID:     parentSession.OwnerID,
	})
	if err != nil {
		return tools.AgentSendMessageResponse{}, err
	}
	targetSession, err := managedagents.GetSessionWithContext(databaseCtx, s.store, request.SessionID)
	if err != nil {
		return tools.AgentSendMessageResponse{}, err
	}
	if targetSession.WorkspaceID != parentSession.WorkspaceID {
		return tools.AgentSendMessageResponse{}, fmt.Errorf("%w: cross-workspace subagent message is not allowed", managedagents.ErrInvalid)
	}
	payload, err := json.Marshal(map[string]any{
		"content": []map[string]string{{
			"type": "text",
			"text": strings.TrimSpace(request.Message),
		}},
	})
	if err != nil {
		return tools.AgentSendMessageResponse{}, err
	}
	events, err := managedagents.StartSubagentTurnWithContext(databaseCtx, s.store, managedagents.StartSubagentTurnInput{
		SessionID:       request.SessionID,
		ParentSessionID: request.ParentSessionID,
		Payload:         payload,
		Limits:          s.policy.storeLimits(),
	})
	if err != nil {
		var violation managedagents.SubagentQuotaViolation
		if errors.As(err, &violation) {
			queued, queueErr := managedagents.EnqueueSubagentStartWithContext(databaseCtx, s.store, managedagents.EnqueueSubagentStartInput{
				SessionID:       request.SessionID,
				ParentSessionID: request.ParentSessionID,
				ParentTurnID:    request.ParentTurnID,
				Payload:         payload,
				Limits:          s.policy.storeLimits(),
			})
			if queueErr != nil {
				var queueViolation managedagents.SubagentQuotaViolation
				if errors.As(queueErr, &queueViolation) {
					return tools.AgentSendMessageResponse{}, s.rejectStart(databaseCtx, parentSession, request.ParentTurnID, subagentQuotaError(queueViolation.Type, queueViolation.Message, queueViolation.State))
				}
				return tools.AgentSendMessageResponse{}, queueErr
			}
			s.recordSubagentQueued(databaseCtx, parentSession, request.ParentTurnID, queued)
			return tools.AgentSendMessageResponse{SessionID: request.SessionID, Queued: true, QueueRequest: &queued}, nil
		}
		return tools.AgentSendMessageResponse{}, err
	}
	started := s.dispatchRunnerEvents(databaseCtx, request.SessionID, events)
	return tools.AgentSendMessageResponse{
		SessionID: request.SessionID,
		Started:   started,
		Events:    events,
	}, nil
}

func (s agentToolService) recordSubagentQueued(ctx context.Context, parentSession managedagents.Session, parentTurnID string, request managedagents.SubagentStartRequest) {
	payload, err := json.Marshal(map[string]any{
		"request_id": request.ID, "subagent_session_id": request.SessionID, "queued_at": request.QueuedAt,
		"expires_at": request.ExpiresAt, "parent_session_id": parentSession.ID, "parent_turn_id": strings.TrimSpace(parentTurnID),
		"wait_seconds": request.WaitSeconds,
	})
	if err != nil {
		s.logger.Warn("marshal subagent queued event", "session_id", parentSession.ID, "error", err)
		return
	}
	if _, err := managedagents.AppendRuntimeEventWithContext(ctx, s.store, parentSession.ID, strings.TrimSpace(parentTurnID), managedagents.AppendEventInput{Type: managedagents.EventRuntimeSubagentStartQueued, Payload: payload}); err != nil {
		s.logger.Warn("append subagent queued event", "session_id", parentSession.ID, "error", err)
	}
}

func (s agentToolService) GetSession(ctx context.Context, request tools.AgentSessionRequest) (tools.AgentSessionResponse, error) {
	session, pending, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID)
	if err != nil {
		return tools.AgentSessionResponse{}, err
	}
	queueRequest, err := s.pendingSubagentStart(ctx, session.ID)
	if err != nil {
		return tools.AgentSessionResponse{}, err
	}
	status := sessionDerivedStatus(session, pending)
	if queueRequest != nil {
		status = "queued"
	}
	return tools.AgentSessionResponse{
		Session:              session,
		Status:               status,
		PendingInterventions: pending,
		QueueRequest:         queueRequest,
	}, nil
}

func (s agentToolService) Wait(ctx context.Context, request tools.AgentWaitRequest) (tools.AgentWaitResponse, error) {
	timeout := time.NewTimer(tools.WaitTimeout(request.TimeoutSeconds))
	defer timeout.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	returnOnWaitingApproval := true
	if request.ReturnOnWaitingApproval != nil {
		returnOnWaitingApproval = *request.ReturnOnWaitingApproval
	}

	for {
		session, pending, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID)
		if err != nil {
			return tools.AgentWaitResponse{}, err
		}
		events, err := managedagents.ListEventsWithContext(ctx, s.store, request.SessionID, 0)
		if err != nil {
			return tools.AgentWaitResponse{}, err
		}
		lastTurnStatus, reason := latestTurnOutcome(events)
		status := sessionDerivedStatus(session, pending)
		queueRequest, err := s.pendingSubagentStart(ctx, session.ID)
		if err != nil {
			return tools.AgentWaitResponse{}, err
		}
		if queueRequest != nil {
			status = "queued"
		}
		response := tools.AgentWaitResponse{
			Session:              session,
			Status:               status,
			LastTurnStatus:       lastTurnStatus,
			Reason:               reason,
			PendingInterventions: pending,
			QueueRequest:         queueRequest,
		}
		if status == managedagents.SessionStatusIdle || status == managedagents.SessionStatusFailed || status == managedagents.SessionStatusTerminated {
			return response, nil
		}
		if returnOnWaitingApproval && (status == managedagents.TurnStatusWaitingApproval || status == managedagents.TurnStatusWaitingHuman) {
			return response, nil
		}

		select {
		case <-ctx.Done():
			response.TimedOut = true
			return response, nil
		case <-timeout.C:
			response.TimedOut = true
			return response, nil
		case <-ticker.C:
		}
	}
}

func (s agentToolService) CollectResult(ctx context.Context, request tools.AgentCollectResultRequest) (tools.AgentCollectResultResponse, error) {
	session, pending, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID)
	if err != nil {
		return tools.AgentCollectResultResponse{}, err
	}
	events, err := managedagents.ListEventsWithContext(ctx, s.store, request.SessionID, 0)
	if err != nil {
		return tools.AgentCollectResultResponse{}, err
	}
	lastTurnStatus, reason := latestTurnOutcome(events)
	queueRequest, err := s.pendingSubagentStart(ctx, session.ID)
	if err != nil {
		return tools.AgentCollectResultResponse{}, err
	}
	status := sessionDerivedStatus(session, pending)
	if queueRequest != nil {
		status = "queued"
	}
	var agentEvent *managedagents.Event
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == managedagents.EventAgentMessage {
			event := events[index]
			agentEvent = &event
			break
		}
	}
	response := tools.AgentCollectResultResponse{
		Session:              session,
		Status:               status,
		LastTurnStatus:       lastTurnStatus,
		Reason:               reason,
		AgentMessage:         agentEvent,
		PendingInterventions: pending,
		EventCount:           len(events),
		QueueRequest:         queueRequest,
	}
	if agentEvent != nil {
		response.AgentText = tools.ExtractAgentMessageText(agentEvent.Payload)
	}
	includeArtifacts := true
	if request.IncludeArtifacts != nil {
		includeArtifacts = *request.IncludeArtifacts
	}
	if includeArtifacts {
		artifacts, err := managedagents.ListSessionArtifactsWithContext(ctx, s.store, request.SessionID)
		if err != nil {
			return tools.AgentCollectResultResponse{}, err
		}
		response.Artifacts = artifacts
	}
	return response, nil
}

func (s agentToolService) pendingSubagentStart(ctx context.Context, sessionID string) (*managedagents.SubagentStartRequest, error) {
	request, err := managedagents.GetPendingSubagentStartWithContext(ctx, s.store, sessionID)
	if errors.Is(err, managedagents.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &request, nil
}

func (s agentToolService) ListEvents(ctx context.Context, request tools.AgentListEventsRequest) (tools.AgentListEventsResponse, error) {
	if _, _, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID); err != nil {
		return tools.AgentListEventsResponse{}, err
	}
	events, err := managedagents.ListEventsWithContext(ctx, s.store, request.SessionID, request.AfterSeq)
	if err != nil {
		return tools.AgentListEventsResponse{}, err
	}
	if request.Limit > 0 && len(events) > request.Limit {
		events = events[:request.Limit]
	}
	return tools.AgentListEventsResponse{
		SessionID: request.SessionID,
		Events:    events,
	}, nil
}

func (s agentToolService) StreamEvents(ctx context.Context, request tools.AgentStreamEventsRequest) (tools.AgentStreamEventsResponse, error) {
	session, _, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID)
	if err != nil {
		return tools.AgentStreamEventsResponse{}, err
	}
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		return tools.AgentStreamEventsResponse{}, err
	}
	events, err := managedagents.ListEventsWithContext(databaseCtx, s.store, request.SessionID, request.AfterSeq)
	if err != nil {
		return tools.AgentStreamEventsResponse{}, err
	}
	if request.Limit > 0 && len(events) > request.Limit {
		events = events[:request.Limit]
	}
	if len(events) > 0 {
		return tools.AgentStreamEventsResponse{SessionID: request.SessionID, Events: events}, nil
	}

	stream, cancel, err := managedagents.SubscribeEventsWithContext(databaseCtx, s.store, request.SessionID, request.AfterSeq)
	if err != nil {
		return tools.AgentStreamEventsResponse{}, err
	}
	defer cancel()

	waitSeconds := request.WaitSeconds
	if waitSeconds <= 0 {
		waitSeconds = 30
	}
	if waitSeconds > 300 {
		waitSeconds = 300
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, time.Duration(waitSeconds)*time.Second)
	defer timeoutCancel()

	capacity := request.Limit
	if capacity <= 0 {
		capacity = 1
	}
	collected := make([]managedagents.Event, 0, capacity)
	for {
		select {
		case <-timeoutCtx.Done():
			return tools.AgentStreamEventsResponse{
				SessionID: request.SessionID,
				Events:    collected,
				TimedOut:  len(collected) == 0,
			}, nil
		case event, ok := <-stream:
			if !ok {
				return tools.AgentStreamEventsResponse{
					SessionID: request.SessionID,
					Events:    collected,
					TimedOut:  len(collected) == 0,
				}, nil
			}
			collected = append(collected, event)
			if request.Limit <= 0 || len(collected) >= request.Limit {
				return tools.AgentStreamEventsResponse{SessionID: request.SessionID, Events: collected}, nil
			}
		}
	}
}

func (s agentToolService) ApproveTool(ctx context.Context, request tools.AgentInterventionDecisionRequest) (tools.AgentInterventionDecisionResponse, error) {
	return s.decideToolIntervention(ctx, request, managedagents.InterventionStatusApproved)
}

func (s agentToolService) RejectTool(ctx context.Context, request tools.AgentInterventionDecisionRequest) (tools.AgentInterventionDecisionResponse, error) {
	return s.decideToolIntervention(ctx, request, managedagents.InterventionStatusRejected)
}

func (s agentToolService) ArchiveSession(ctx context.Context, request tools.AgentArchiveSessionRequest) (tools.AgentArchiveSessionResponse, error) {
	if _, _, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID); err != nil {
		return tools.AgentArchiveSessionResponse{}, err
	}
	session, err := managedagents.ArchiveSessionWithContext(ctx, s.store, request.SessionID)
	if err != nil {
		return tools.AgentArchiveSessionResponse{}, err
	}
	return tools.AgentArchiveSessionResponse{Session: session}, nil
}

func (s agentToolService) CancelStart(ctx context.Context, request tools.AgentCancelStartRequest) (tools.AgentCancelStartResponse, error) {
	if _, _, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID); err != nil {
		return tools.AgentCancelStartResponse{}, err
	}
	queued, err := managedagents.CancelSubagentStartWithContext(ctx, s.store, managedagents.CancelSubagentStartInput{
		SessionID: request.SessionID, ParentSessionID: request.ParentSessionID, Reason: request.Reason,
	})
	if err != nil {
		return tools.AgentCancelStartResponse{}, err
	}
	return tools.AgentCancelStartResponse{QueueRequest: queued}, nil
}

func (s agentToolService) CreateTaskGroup(ctx context.Context, request tools.AgentTaskGroupCreateRequest) (tools.AgentTaskGroupCreateResponse, error) {
	parentSession, err := s.parentSession(ctx, request.ParentSessionID)
	if err != nil {
		return tools.AgentTaskGroupCreateResponse{}, err
	}
	request, _, err = tools.ExpandAgentTaskGroupTemplate(request)
	if err != nil {
		return tools.AgentTaskGroupCreateResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	if len(request.Items) == 0 {
		return tools.AgentTaskGroupCreateResponse{}, fmt.Errorf("%w: at least one group item is required", managedagents.ErrInvalid)
	}
	strategy, reducer, quorum, err := normalizeTaskGroupRequest(request.Strategy, request.ResultReducer, request.Quorum, len(request.Items))
	if err != nil {
		return tools.AgentTaskGroupCreateResponse{}, err
	}
	parentGroupID := ""
	parentItemIndex := 0
	if parentItem, err := managedagents.GetSubagentTaskGroupItemBySessionWithContext(ctx, s.store, parentSession.ID); err == nil {
		parentGroupID = parentItem.GroupID
		parentItemIndex = parentItem.ItemIndex
	} else if !errors.Is(err, managedagents.ErrNotFound) {
		return tools.AgentTaskGroupCreateResponse{}, err
	}
	group, err := managedagents.CreateSubagentTaskGroupWithContext(ctx, s.store, managedagents.CreateSubagentTaskGroupInput{
		WorkspaceID:     parentSession.WorkspaceID,
		OwnerID:         parentSession.OwnerID,
		ParentSessionID: parentSession.ID,
		ParentTurnID:    strings.TrimSpace(request.ParentTurnID),
		ParentGroupID:   parentGroupID,
		ParentItemIndex: parentItemIndex,
		Strategy:        strategy,
		ResultReducer:   reducer,
		Quorum:          quorum,
		FailFast:        request.FailFast,
		PlannedCount:    len(request.Items),
	})
	if err != nil {
		return tools.AgentTaskGroupCreateResponse{}, err
	}
	s.recordTaskGroupEvent(ctx, parentSession.ID, request.ParentTurnID, managedagents.EventRuntimeSubagentGroupCreated, map[string]any{
		"group_id":          group.ID,
		"parent_session_id": parentSession.ID,
		"parent_turn_id":    strings.TrimSpace(request.ParentTurnID),
		"strategy":          group.Strategy,
		"result_reducer":    group.ResultReducer,
		"quorum":            group.Quorum,
		"fail_fast":         group.FailFast,
		"planned_count":     group.PlannedCount,
		"template_id":       strings.TrimSpace(request.TemplateID),
	})

	for index, item := range request.Items {
		resolved := resolveTaskGroupItem(parentSession, item)
		if strings.TrimSpace(item.Message) == "" {
			if _, err := managedagents.AppendSubagentTaskGroupItemWithContext(ctx, s.store, group.ID, managedagents.AppendSubagentTaskGroupItemInput{
				ItemIndex:            index,
				AgentID:              resolved.AgentID,
				EnvironmentID:        resolved.EnvironmentID,
				Title:                resolved.Title,
				Message:              "",
				Priority:             item.Priority,
				InitialState:         managedagents.SubagentTaskGroupItemStateRejected,
				ErrorType:            "group_item_missing_message",
				ErrorMessage:         "task group item message is required",
				ExpectedResultSchema: item.ExpectedResultSchema,
			}); err != nil {
				return tools.AgentTaskGroupCreateResponse{}, err
			}
			s.recordTaskGroupEvent(ctx, parentSession.ID, request.ParentTurnID, managedagents.EventRuntimeSubagentGroupItemRejected, map[string]any{
				"group_id":   group.ID,
				"item_index": index,
				"message":    "task group item message is required",
				"error_type": "group_item_missing_message",
			})
			continue
		}

		spawnResponse, spawnErr := s.Spawn(ctx, tools.AgentSpawnRequest{
			ParentSessionID: request.ParentSessionID,
			ParentTurnID:    request.ParentTurnID,
			AgentID:         resolved.AgentID,
			EnvironmentID:   resolved.EnvironmentID,
			Title:           resolved.Title,
			Message:         "",
		})
		if spawnErr != nil {
			errorType, errorMessage := taskGroupItemError(spawnErr, "group_item_spawn_failed")
			if _, err := managedagents.AppendSubagentTaskGroupItemWithContext(ctx, s.store, group.ID, managedagents.AppendSubagentTaskGroupItemInput{
				ItemIndex:            index,
				AgentID:              resolved.AgentID,
				EnvironmentID:        resolved.EnvironmentID,
				Title:                resolved.Title,
				Message:              strings.TrimSpace(item.Message),
				Priority:             item.Priority,
				InitialState:         managedagents.SubagentTaskGroupItemStateRejected,
				ErrorType:            errorType,
				ErrorMessage:         errorMessage,
				ExpectedResultSchema: item.ExpectedResultSchema,
			}); err != nil {
				return tools.AgentTaskGroupCreateResponse{}, err
			}
			s.recordTaskGroupEvent(ctx, parentSession.ID, request.ParentTurnID, managedagents.EventRuntimeSubagentGroupItemRejected, map[string]any{
				"group_id":   group.ID,
				"item_index": index,
				"session_id": "",
				"error_type": errorType,
				"message":    errorMessage,
			})
			continue
		}

		initialState := managedagents.SubagentTaskGroupItemStateCreated
		errorType := ""
		errorMessage := ""
		sendResponse, sendErr := s.SendMessage(ctx, tools.AgentSendMessageRequest{
			ParentSessionID: request.ParentSessionID,
			ParentTurnID:    request.ParentTurnID,
			SessionID:       spawnResponse.Session.ID,
			Message:         strings.TrimSpace(item.Message),
		})
		switch {
		case sendErr != nil:
			initialState = managedagents.SubagentTaskGroupItemStateRejected
			errorType, errorMessage = taskGroupItemError(sendErr, "group_item_start_failed")
		case sendResponse.Queued:
			initialState = managedagents.SubagentTaskGroupItemStateQueued
		case sendResponse.Started:
			initialState = managedagents.SubagentTaskGroupItemStateStarted
		}

		if _, err := managedagents.AppendSubagentTaskGroupItemWithContext(ctx, s.store, group.ID, managedagents.AppendSubagentTaskGroupItemInput{
			ItemIndex:            index,
			AgentID:              spawnResponse.Session.AgentID,
			EnvironmentID:        spawnResponse.Session.EnvironmentID,
			SessionID:            spawnResponse.Session.ID,
			Title:                spawnResponse.Session.Title,
			Message:              strings.TrimSpace(item.Message),
			Priority:             item.Priority,
			InitialState:         initialState,
			ErrorType:            errorType,
			ErrorMessage:         errorMessage,
			ExpectedResultSchema: item.ExpectedResultSchema,
		}); err != nil {
			return tools.AgentTaskGroupCreateResponse{}, err
		}
		eventType := managedagents.EventRuntimeSubagentGroupItemStarted
		if initialState == managedagents.SubagentTaskGroupItemStateQueued {
			eventType = managedagents.EventRuntimeSubagentGroupItemQueued
		}
		if initialState == managedagents.SubagentTaskGroupItemStateRejected {
			eventType = managedagents.EventRuntimeSubagentGroupItemRejected
		}
		s.recordTaskGroupEvent(ctx, parentSession.ID, request.ParentTurnID, eventType, map[string]any{
			"group_id":   group.ID,
			"item_index": index,
			"session_id": spawnResponse.Session.ID,
			"status":     initialState,
			"error_type": errorType,
			"message":    errorMessage,
		})
	}

	state, err := s.taskGroupState(ctx, request.ParentSessionID, group.ID)
	if err != nil {
		return tools.AgentTaskGroupCreateResponse{}, err
	}
	state, err = s.enforceTaskGroupFailFast(ctx, request.ParentSessionID, state)
	if err != nil {
		return tools.AgentTaskGroupCreateResponse{}, err
	}
	s.recordTaskGroupTerminalEvent(ctx, state)
	return tools.AgentTaskGroupCreateResponse{
		Group:     state.Group,
		Status:    state.Status,
		Completed: state.Completed,
		Summary:   state.Summary,
		Aggregate: state.Aggregate,
		Items:     state.Items,
	}, nil
}

func (s agentToolService) GetTaskGroup(ctx context.Context, request tools.AgentTaskGroupRequest) (tools.AgentTaskGroupResponse, error) {
	state, err := s.taskGroupState(ctx, request.ParentSessionID, request.GroupID)
	if err != nil {
		return tools.AgentTaskGroupResponse{}, err
	}
	state, err = s.enforceTaskGroupFailFast(ctx, request.ParentSessionID, state)
	if err != nil {
		return tools.AgentTaskGroupResponse{}, err
	}
	s.recordTaskGroupTerminalEvent(ctx, state)
	return tools.AgentTaskGroupResponse{
		Group:     state.Group,
		Status:    state.Status,
		Completed: state.Completed,
		Summary:   state.Summary,
		Aggregate: state.Aggregate,
		Items:     state.Items,
	}, nil
}

func (s agentToolService) WaitTaskGroup(ctx context.Context, request tools.AgentTaskGroupWaitRequest) (tools.AgentTaskGroupWaitResponse, error) {
	timeout := time.NewTimer(tools.WaitTimeout(request.TimeoutSeconds))
	defer timeout.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		state, err := s.taskGroupState(ctx, request.ParentSessionID, request.GroupID)
		if err != nil {
			return tools.AgentTaskGroupWaitResponse{}, err
		}
		state, err = s.enforceTaskGroupFailFast(ctx, request.ParentSessionID, state)
		if err != nil {
			return tools.AgentTaskGroupWaitResponse{}, err
		}
		response := tools.AgentTaskGroupWaitResponse{
			Group:     state.Group,
			Status:    state.Status,
			Completed: state.Completed,
			Summary:   state.Summary,
			Aggregate: state.Aggregate,
			Items:     state.Items,
		}
		if state.Completed {
			s.recordTaskGroupTerminalEvent(ctx, state)
			return response, nil
		}
		select {
		case <-ctx.Done():
			response.TimedOut = true
			return response, nil
		case <-timeout.C:
			response.TimedOut = true
			return response, nil
		case <-ticker.C:
		}
	}
}

func (s agentToolService) CollectTaskGroup(ctx context.Context, request tools.AgentTaskGroupCollectRequest) (tools.AgentTaskGroupCollectResponse, error) {
	state, err := s.taskGroupState(ctx, request.ParentSessionID, request.GroupID)
	if err != nil {
		return tools.AgentTaskGroupCollectResponse{}, err
	}
	state, err = s.enforceTaskGroupFailFast(ctx, request.ParentSessionID, state)
	if err != nil {
		return tools.AgentTaskGroupCollectResponse{}, err
	}
	s.recordTaskGroupTerminalEvent(ctx, state)
	return tools.AgentTaskGroupCollectResponse{
		Group:     state.Group,
		Status:    state.Status,
		Completed: state.Completed,
		Summary:   state.Summary,
		Aggregate: state.Aggregate,
		Items:     state.Items,
	}, nil
}

func (s agentToolService) CancelTaskGroup(ctx context.Context, request tools.AgentTaskGroupCancelRequest) (tools.AgentTaskGroupCancelResponse, error) {
	state, err := s.taskGroupState(ctx, request.ParentSessionID, request.GroupID)
	if err != nil {
		return tools.AgentTaskGroupCancelResponse{}, err
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "canceled by parent agent"
	}
	if err := s.cancelTaskGroupRecursive(ctx, state, reason, map[string]bool{}); err != nil {
		return tools.AgentTaskGroupCancelResponse{}, err
	}
	group, err := managedagents.GetSubagentTaskGroupWithContext(ctx, s.store, strings.TrimSpace(request.GroupID))
	if err != nil {
		return tools.AgentTaskGroupCancelResponse{}, err
	}
	state, err = s.taskGroupState(ctx, request.ParentSessionID, group.ID)
	if err != nil {
		return tools.AgentTaskGroupCancelResponse{}, err
	}
	s.recordTaskGroupTerminalEvent(ctx, state)
	return tools.AgentTaskGroupCancelResponse{
		Group:     state.Group,
		Status:    state.Status,
		Completed: state.Completed,
		Summary:   state.Summary,
		Aggregate: state.Aggregate,
		Items:     state.Items,
	}, nil
}

func (s agentToolService) RetryTaskGroupItem(ctx context.Context, request tools.AgentTaskGroupRetryItemRequest) (tools.AgentTaskGroupRetryResponse, error) {
	state, err := s.taskGroupState(ctx, request.ParentSessionID, request.GroupID)
	if err != nil {
		return tools.AgentTaskGroupRetryResponse{}, err
	}
	state, err = s.retryTaskGroupItems(ctx, request.ParentSessionID, state, map[int]bool{request.ItemIndex: true})
	if err != nil {
		return tools.AgentTaskGroupRetryResponse{}, err
	}
	s.recordTaskGroupTerminalEvent(ctx, state)
	return tools.AgentTaskGroupRetryResponse{
		Group:     state.Group,
		Status:    state.Status,
		Completed: state.Completed,
		Summary:   state.Summary,
		Aggregate: state.Aggregate,
		Items:     state.Items,
	}, nil
}

func (s agentToolService) RetryTaskGroup(ctx context.Context, request tools.AgentTaskGroupRetryRequest) (tools.AgentTaskGroupRetryResponse, error) {
	state, err := s.taskGroupState(ctx, request.ParentSessionID, request.GroupID)
	if err != nil {
		return tools.AgentTaskGroupRetryResponse{}, err
	}
	state, err = s.retryTaskGroupTree(ctx, request.ParentSessionID, state, map[string]bool{})
	if err != nil {
		return tools.AgentTaskGroupRetryResponse{}, err
	}
	s.recordTaskGroupTerminalEvent(ctx, state)
	return tools.AgentTaskGroupRetryResponse{
		Group:     state.Group,
		Status:    state.Status,
		Completed: state.Completed,
		Summary:   state.Summary,
		Aggregate: state.Aggregate,
		Items:     state.Items,
	}, nil
}

func (s agentToolService) parentSession(ctx context.Context, sessionID string) (managedagents.Session, error) {
	if strings.TrimSpace(sessionID) == "" {
		return managedagents.Session{}, fmt.Errorf("%w: parent session_id is required", managedagents.ErrInvalid)
	}
	return managedagents.GetSessionWithContext(ctx, s.store, sessionID)
}

func (s agentToolService) loadSessionState(ctx context.Context, parentSessionID string, sessionID string) (managedagents.Session, []managedagents.SessionIntervention, error) {
	parentSession, err := s.parentSession(ctx, parentSessionID)
	if err != nil {
		return managedagents.Session{}, nil, err
	}
	session, err := managedagents.GetSessionWithContext(ctx, s.store, sessionID)
	if err != nil {
		return managedagents.Session{}, nil, err
	}
	if session.WorkspaceID != parentSession.WorkspaceID {
		return managedagents.Session{}, nil, fmt.Errorf("%w: cross-workspace subagent access is not allowed", managedagents.ErrInvalid)
	}
	pending, err := managedagents.ListSessionInterventionsWithContext(ctx, s.store, sessionID, managedagents.InterventionStatusPending)
	if err != nil {
		return managedagents.Session{}, nil, err
	}
	return session, pending, nil
}

func (s agentToolService) dispatchRunnerEvents(ctx context.Context, sessionID string, events []managedagents.Event) bool {
	scope, _ := managedagents.DatabaseAccessScopeFromContext(ctx)
	started := false
	for _, event := range events {
		if event.Type != managedagents.EventUserMessage {
			continue
		}
		turnID := payloadString(event.Payload, "turn_id")
		started = true
		if err := s.runner.StartTurn(ctx, runner.TurnRequest{
			SessionID:    sessionID,
			TurnID:       turnID,
			UserEventSeq: event.Seq,
			UserPayload:  event.Payload,
			Scope:        scope,
		}); err != nil {
			s.logger.Error("agent tool failed to start subagent turn",
				"session_id", sessionID,
				"turn_id", turnID,
				"error", err,
			)
			if _, failErr := managedagents.FailSessionTurnWithContext(ctx, s.store, sessionID, turnID, err.Error()); failErr != nil {
				s.logger.Error("agent tool failed to mark subagent turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"error", failErr,
				)
			}
		}
	}
	return started
}

func sessionDerivedStatus(session managedagents.Session, pending []managedagents.SessionIntervention) string {
	if len(pending) > 0 {
		return managedagents.PendingInterventionTurnStatus(pending)
	}
	return session.Status
}

func latestTurnOutcome(events []managedagents.Event) (string, string) {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != managedagents.EventSessionStatusIdle {
			continue
		}
		var payload struct {
			LastTurnStatus string `json:"last_turn_status"`
			Reason         string `json:"reason"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		return strings.TrimSpace(payload.LastTurnStatus), strings.TrimSpace(payload.Reason)
	}
	return "", ""
}

type taskGroupState struct {
	Group     managedagents.SubagentTaskGroup
	Status    string
	Completed bool
	Summary   tools.AgentTaskGroupSummary
	Aggregate tools.AgentTaskGroupAggregate
	Items     []tools.AgentTaskGroupItemState
}

func truncateString(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "..."
}

type resolvedTaskGroupItem struct {
	AgentID       string
	EnvironmentID string
	Title         string
}

func resolveTaskGroupItem(parentSession managedagents.Session, item tools.AgentTaskGroupItemRequest) resolvedTaskGroupItem {
	agentID := strings.TrimSpace(item.AgentID)
	if agentID == "" {
		agentID = strings.TrimSpace(item.Agent)
	}
	if agentID == "" {
		agentID = parentSession.AgentID
	}
	environmentID := strings.TrimSpace(item.EnvironmentID)
	if environmentID == "" {
		environmentID = parentSession.EnvironmentID
	}
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = truncateString(item.Message, 80)
	}
	return resolvedTaskGroupItem{AgentID: agentID, EnvironmentID: environmentID, Title: title}
}

func normalizeTaskGroupRequest(strategy string, reducer string, quorum int, itemCount int) (string, string, int, error) {
	normalizedReducer := managedagents.SubagentTaskGroupReducerConcatText
	switch strings.TrimSpace(strings.ToLower(reducer)) {
	case "", managedagents.SubagentTaskGroupReducerConcatText:
		normalizedReducer = managedagents.SubagentTaskGroupReducerConcatText
	case managedagents.SubagentTaskGroupReducerNone:
		normalizedReducer = managedagents.SubagentTaskGroupReducerNone
	case managedagents.SubagentTaskGroupReducerJSONList:
		normalizedReducer = managedagents.SubagentTaskGroupReducerJSONList
	case managedagents.SubagentTaskGroupReducerJSONObject:
		normalizedReducer = managedagents.SubagentTaskGroupReducerJSONObject
	case managedagents.SubagentTaskGroupReducerFirstSuccess:
		normalizedReducer = managedagents.SubagentTaskGroupReducerFirstSuccess
	case managedagents.SubagentTaskGroupReducerMajorityText:
		normalizedReducer = managedagents.SubagentTaskGroupReducerMajorityText
	case managedagents.SubagentTaskGroupReducerMergeObjects:
		normalizedReducer = managedagents.SubagentTaskGroupReducerMergeObjects
	case managedagents.SubagentTaskGroupReducerJSONValues:
		normalizedReducer = managedagents.SubagentTaskGroupReducerJSONValues
	case managedagents.SubagentTaskGroupReducerFirstValue:
		normalizedReducer = managedagents.SubagentTaskGroupReducerFirstValue
	case managedagents.SubagentTaskGroupReducerMajorityValue:
		normalizedReducer = managedagents.SubagentTaskGroupReducerMajorityValue
	default:
		return "", "", 0, fmt.Errorf("%w: unsupported task group reducer %q", managedagents.ErrInvalid, reducer)
	}
	switch strings.TrimSpace(strings.ToLower(strategy)) {
	case "", managedagents.SubagentTaskGroupStrategyAllCompleted:
		return managedagents.SubagentTaskGroupStrategyAllCompleted, normalizedReducer, 0, nil
	case managedagents.SubagentTaskGroupStrategyAnyCompleted:
		return managedagents.SubagentTaskGroupStrategyAnyCompleted, normalizedReducer, 0, nil
	case managedagents.SubagentTaskGroupStrategyQuorum:
		if quorum <= 0 || quorum > itemCount {
			return "", "", 0, fmt.Errorf("%w: quorum must be between 1 and item count", managedagents.ErrInvalid)
		}
		return managedagents.SubagentTaskGroupStrategyQuorum, normalizedReducer, quorum, nil
	default:
		return "", "", 0, fmt.Errorf("%w: unsupported task group strategy %q", managedagents.ErrInvalid, strategy)
	}
}

func taskGroupItemError(err error, fallbackType string) (string, string) {
	var toolErr tools.AgentToolError
	if errors.As(err, &toolErr) {
		errorType := strings.TrimSpace(toolErr.Type)
		if errorType == "" {
			errorType = fallbackType
		}
		return errorType, toolErr.Error()
	}
	return fallbackType, err.Error()
}

func (s agentToolService) recordTaskGroupEvent(ctx context.Context, parentSessionID string, parentTurnID string, eventType string, payload map[string]any) {
	parentSessionID = strings.TrimSpace(parentSessionID)
	if parentSessionID == "" || eventType == "" {
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.logger.Warn("marshal task group event", "event_type", eventType, "session_id", parentSessionID, "error", err)
		return
	}
	if s.taskGroupEventExists(ctx, parentSessionID, eventType, payload) {
		return
	}
	if _, err := managedagents.AppendEventsWithContext(ctx, s.store, parentSessionID, []managedagents.AppendEventInput{{
		Type:    eventType,
		Payload: encoded,
	}}); err != nil {
		s.logger.Warn("append task group event", "event_type", eventType, "session_id", parentSessionID, "turn_id", parentTurnID, "error", err)
	}
}

func (s agentToolService) taskGroupEventExists(ctx context.Context, parentSessionID string, eventType string, payload map[string]any) bool {
	events, err := managedagents.ListEventsWithContext(ctx, s.store, parentSessionID, 0)
	if err != nil {
		return false
	}
	groupID, _ := payload["group_id"].(string)
	itemIndex, hasItemIndex := payload["item_index"]
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		if groupID != "" && payloadString(event.Payload, "group_id") != groupID {
			continue
		}
		if hasItemIndex {
			var decoded map[string]any
			if err := json.Unmarshal(event.Payload, &decoded); err != nil {
				continue
			}
			if fmt.Sprint(decoded["item_index"]) != fmt.Sprint(itemIndex) {
				continue
			}
		}
		return true
	}
	return false
}

func (s agentToolService) taskGroupState(ctx context.Context, parentSessionID string, groupID string) (taskGroupState, error) {
	return s.taskGroupStateWithVisited(ctx, parentSessionID, groupID, map[string]bool{})
}

func (s agentToolService) taskGroupStateWithVisited(ctx context.Context, parentSessionID string, groupID string, visited map[string]bool) (taskGroupState, error) {
	parentSession, err := s.parentSession(ctx, parentSessionID)
	if err != nil {
		return taskGroupState{}, err
	}
	if visited[groupID] {
		return taskGroupState{}, fmt.Errorf("%w: nested task group cycle detected for %s", managedagents.ErrConflict, groupID)
	}
	visited[groupID] = true
	defer delete(visited, groupID)
	group, err := managedagents.GetSubagentTaskGroupWithContext(ctx, s.store, groupID)
	if err != nil {
		return taskGroupState{}, err
	}
	if group.ParentSessionID != parentSession.ID || group.WorkspaceID != parentSession.WorkspaceID {
		return taskGroupState{}, fmt.Errorf("%w: task group does not belong to the requested parent session", managedagents.ErrInvalid)
	}
	groupItems, err := managedagents.ListSubagentTaskGroupItemsWithContext(ctx, s.store, group.ID)
	if err != nil {
		return taskGroupState{}, err
	}
	items := make([]tools.AgentTaskGroupItemState, 0, len(groupItems))
	for _, groupItem := range groupItems {
		itemState, err := s.taskGroupItemState(ctx, parentSession, groupItem, visited)
		if err != nil {
			return taskGroupState{}, err
		}
		items = append(items, itemState)
	}
	status, completed, summary := evaluateTaskGroup(group, items)
	aggregate := buildTaskGroupAggregate(group, items)
	return taskGroupState{
		Group:     group,
		Status:    status,
		Completed: completed,
		Summary:   summary,
		Aggregate: aggregate,
		Items:     items,
	}, nil
}

func (s agentToolService) retryTaskGroupItems(ctx context.Context, parentSessionID string, state taskGroupState, targets map[int]bool) (taskGroupState, error) {
	if len(targets) == 0 {
		return state, nil
	}
	parentSession, err := s.parentSession(ctx, parentSessionID)
	if err != nil {
		return taskGroupState{}, err
	}
	if state.Group.CanceledAt != nil {
		group, err := managedagents.ReactivateSubagentTaskGroupWithContext(ctx, s.store, managedagents.ReactivateSubagentTaskGroupInput{
			GroupID:         state.Group.ID,
			ParentSessionID: parentSessionID,
		})
		if err != nil {
			return taskGroupState{}, err
		}
		state.Group = group
	}
	for _, itemState := range state.Items {
		if !targets[itemState.Item.ItemIndex] {
			continue
		}
		if !taskGroupItemRetryable(itemState) {
			return taskGroupState{}, fmt.Errorf("%w: task group item %d is not retryable from status %s", managedagents.ErrConflict, itemState.Item.ItemIndex, itemState.Status)
		}
		if _, err := s.retryTaskGroupItem(ctx, parentSession, state.Group, itemState.Item); err != nil {
			return taskGroupState{}, err
		}
	}
	next, err := s.taskGroupState(ctx, parentSessionID, state.Group.ID)
	if err != nil {
		return taskGroupState{}, err
	}
	next, err = s.enforceTaskGroupFailFast(ctx, parentSessionID, next)
	if err != nil {
		return taskGroupState{}, err
	}
	return next, nil
}

func (s agentToolService) retryTaskGroupTree(ctx context.Context, parentSessionID string, state taskGroupState, visited map[string]bool) (taskGroupState, error) {
	if visited[state.Group.ID] {
		return state, nil
	}
	visited[state.Group.ID] = true
	defer delete(visited, state.Group.ID)

	targets := make(map[int]bool)
	for _, item := range state.Items {
		if taskGroupItemRetryable(item) {
			targets[item.Item.ItemIndex] = true
		}
	}
	if len(targets) > 0 {
		var err error
		state, err = s.retryTaskGroupItems(ctx, parentSessionID, state, targets)
		if err != nil {
			return taskGroupState{}, err
		}
	}
	for _, item := range state.Items {
		if targets[item.Item.ItemIndex] {
			continue
		}
		for _, nested := range item.NestedGroups {
			childState := taskGroupState{
				Group:     nested.Group,
				Status:    nested.Status,
				Completed: nested.Completed,
				Summary:   nested.Summary,
				Aggregate: nested.Aggregate,
				Items:     nested.Items,
			}
			if _, err := s.retryTaskGroupTree(ctx, nested.Group.ParentSessionID, childState, visited); err != nil {
				return taskGroupState{}, err
			}
		}
	}
	next, err := s.taskGroupState(ctx, parentSessionID, state.Group.ID)
	if err != nil {
		return taskGroupState{}, err
	}
	next, err = s.enforceTaskGroupFailFast(ctx, parentSessionID, next)
	if err != nil {
		return taskGroupState{}, err
	}
	return next, nil
}

func (s agentToolService) retryTaskGroupItem(ctx context.Context, parentSession managedagents.Session, group managedagents.SubagentTaskGroup, item managedagents.SubagentTaskGroupItem) (managedagents.SubagentTaskGroupItem, error) {
	spawnResponse, spawnErr := s.Spawn(ctx, tools.AgentSpawnRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    group.ParentTurnID,
		AgentID:         item.AgentID,
		EnvironmentID:   item.EnvironmentID,
		Title:           item.Title,
		Message:         "",
	})
	if spawnErr != nil {
		errorType, errorMessage := taskGroupItemError(spawnErr, "group_item_spawn_failed")
		updated, err := managedagents.UpdateSubagentTaskGroupItemWithContext(ctx, s.store, group.ID, item.ItemIndex, managedagents.UpdateSubagentTaskGroupItemInput{
			SessionID:            "",
			Title:                item.Title,
			Message:              item.Message,
			Priority:             item.Priority,
			InitialState:         managedagents.SubagentTaskGroupItemStateRejected,
			ErrorType:            errorType,
			ErrorMessage:         errorMessage,
			ExpectedResultSchema: item.ExpectedResultSchema,
			IncrementRetry:       true,
		})
		if err != nil {
			return managedagents.SubagentTaskGroupItem{}, err
		}
		s.recordTaskGroupEvent(ctx, parentSession.ID, group.ParentTurnID, managedagents.EventRuntimeSubagentGroupItemRejected, map[string]any{
			"group_id":    group.ID,
			"item_index":  item.ItemIndex,
			"session_id":  "",
			"status":      managedagents.SubagentTaskGroupItemStateRejected,
			"error_type":  errorType,
			"message":     errorMessage,
			"retry_count": updated.RetryCount,
		})
		return updated, nil
	}

	initialState := managedagents.SubagentTaskGroupItemStateCreated
	errorType := ""
	errorMessage := ""
	sendResponse, sendErr := s.SendMessage(ctx, tools.AgentSendMessageRequest{
		ParentSessionID: parentSession.ID,
		ParentTurnID:    group.ParentTurnID,
		SessionID:       spawnResponse.Session.ID,
		Message:         strings.TrimSpace(item.Message),
	})
	switch {
	case sendErr != nil:
		initialState = managedagents.SubagentTaskGroupItemStateRejected
		errorType, errorMessage = taskGroupItemError(sendErr, "group_item_start_failed")
	case sendResponse.Queued:
		initialState = managedagents.SubagentTaskGroupItemStateQueued
	case sendResponse.Started:
		initialState = managedagents.SubagentTaskGroupItemStateStarted
	}

	updated, err := managedagents.UpdateSubagentTaskGroupItemWithContext(ctx, s.store, group.ID, item.ItemIndex, managedagents.UpdateSubagentTaskGroupItemInput{
		SessionID:            spawnResponse.Session.ID,
		Title:                spawnResponse.Session.Title,
		Message:              item.Message,
		Priority:             item.Priority,
		InitialState:         initialState,
		ErrorType:            errorType,
		ErrorMessage:         errorMessage,
		ExpectedResultSchema: item.ExpectedResultSchema,
		IncrementRetry:       true,
	})
	if err != nil {
		return managedagents.SubagentTaskGroupItem{}, err
	}
	eventType := managedagents.EventRuntimeSubagentGroupItemStarted
	if initialState == managedagents.SubagentTaskGroupItemStateQueued {
		eventType = managedagents.EventRuntimeSubagentGroupItemQueued
	}
	if initialState == managedagents.SubagentTaskGroupItemStateRejected {
		eventType = managedagents.EventRuntimeSubagentGroupItemRejected
	}
	s.recordTaskGroupEvent(ctx, parentSession.ID, group.ParentTurnID, eventType, map[string]any{
		"group_id":    group.ID,
		"item_index":  item.ItemIndex,
		"session_id":  spawnResponse.Session.ID,
		"status":      initialState,
		"error_type":  errorType,
		"message":     errorMessage,
		"retry_count": updated.RetryCount,
	})
	return updated, nil
}

func (s agentToolService) enforceTaskGroupFailFast(ctx context.Context, parentSessionID string, state taskGroupState) (taskGroupState, error) {
	if !state.Group.FailFast || state.Group.CanceledAt != nil || state.Status != "failed" {
		return state, nil
	}
	if !taskGroupHasOutstandingItems(state) {
		return state, nil
	}
	group, err := managedagents.CancelSubagentTaskGroupWithContext(ctx, s.store, managedagents.CancelSubagentTaskGroupInput{
		GroupID:         state.Group.ID,
		ParentSessionID: parentSessionID,
		Reason:          "fail_fast triggered by task group item failure",
	})
	if err != nil {
		return taskGroupState{}, err
	}
	next, err := s.taskGroupState(ctx, parentSessionID, group.ID)
	if err != nil {
		return taskGroupState{}, err
	}
	return next, nil
}

func taskGroupHasOutstandingItems(state taskGroupState) bool {
	for _, item := range state.Items {
		switch item.Status {
		case managedagents.SubagentTaskGroupItemStateCreated,
			managedagents.SubagentTaskGroupItemStateQueued,
			managedagents.SessionStatusRunning,
			managedagents.TurnStatusWaitingApproval,
			managedagents.TurnStatusWaitingHuman:
			return true
		}
	}
	return false
}

func (s agentToolService) cancelTaskGroupRecursive(ctx context.Context, state taskGroupState, reason string, visited map[string]bool) error {
	if visited[state.Group.ID] {
		return nil
	}
	visited[state.Group.ID] = true
	defer delete(visited, state.Group.ID)

	for _, item := range state.Items {
		for _, nested := range item.NestedGroups {
			childState := taskGroupState{
				Group:     nested.Group,
				Status:    nested.Status,
				Completed: nested.Completed,
				Summary:   nested.Summary,
				Aggregate: nested.Aggregate,
				Items:     nested.Items,
			}
			if err := s.cancelTaskGroupRecursive(ctx, childState, "canceled by ancestor task group", visited); err != nil {
				return err
			}
		}
	}
	group, err := managedagents.CancelSubagentTaskGroupWithContext(ctx, s.store, managedagents.CancelSubagentTaskGroupInput{
		GroupID:         state.Group.ID,
		ParentSessionID: state.Group.ParentSessionID,
		Reason:          reason,
	})
	if err != nil && !errors.Is(err, managedagents.ErrNotFound) {
		return err
	}
	if err == nil {
		next, nextErr := s.taskGroupState(ctx, group.ParentSessionID, group.ID)
		if nextErr == nil {
			s.recordTaskGroupTerminalEvent(ctx, next)
		}
	}
	return nil
}

func taskGroupItemRetryable(item tools.AgentTaskGroupItemState) bool {
	switch item.Status {
	case managedagents.TurnStatusFailed, managedagents.SessionStatusTerminated, managedagents.SubagentTaskGroupItemStateRejected:
		return true
	default:
		return false
	}
}

func (s agentToolService) recordTaskGroupTerminalEvent(ctx context.Context, state taskGroupState) {
	if !state.Completed {
		return
	}
	eventType := managedagents.EventRuntimeSubagentGroupCompleted
	if state.Status == "failed" {
		eventType = managedagents.EventRuntimeSubagentGroupFailed
	}
	if state.Status == "canceled" {
		eventType = managedagents.EventRuntimeSubagentGroupCanceled
	}
	payload := map[string]any{
		"group_id":          state.Group.ID,
		"parent_session_id": state.Group.ParentSessionID,
		"parent_turn_id":    state.Group.ParentTurnID,
		"status":            state.Status,
		"result_reducer":    state.Group.ResultReducer,
		"total":             state.Summary.Total,
		"completed":         state.Summary.Completed,
		"failed":            state.Summary.Failed,
		"canceled":          state.Summary.Canceled,
		"rejected":          state.Summary.Rejected,
	}
	if state.Group.CanceledAt != nil {
		payload["cancel_reason"] = state.Group.CancelReason
		payload["canceled_at"] = state.Group.CanceledAt
	}
	s.recordTaskGroupEvent(ctx, state.Group.ParentSessionID, state.Group.ParentTurnID, eventType, payload)
}

func (s agentToolService) taskGroupItemState(ctx context.Context, parentSession managedagents.Session, item managedagents.SubagentTaskGroupItem, visited map[string]bool) (tools.AgentTaskGroupItemState, error) {
	state := tools.AgentTaskGroupItemState{Item: item}
	if item.InitialState == managedagents.SubagentTaskGroupItemStateRejected || item.SessionID == "" {
		state.Status = managedagents.SubagentTaskGroupItemStateRejected
		state.Reason = strings.TrimSpace(item.ErrorMessage)
		if state.Reason == "" {
			state.Reason = strings.TrimSpace(item.ErrorType)
		}
		return state, nil
	}

	session, pending, err := s.loadSessionState(ctx, parentSession.ID, item.SessionID)
	if err != nil {
		state.Status = managedagents.SessionStatusTerminated
		state.Reason = err.Error()
		return state, nil
	}
	state.Session = &session
	state.PendingApprovals = pending
	queueRequest, queueErr := s.pendingSubagentStart(ctx, item.SessionID)
	if queueErr == nil {
		state.QueueRequest = queueRequest
	}
	events, err := managedagents.ListEventsWithContext(ctx, s.store, item.SessionID, 0)
	if err == nil {
		state.EventCount = len(events)
		state.LastTurnStatus, state.Reason = latestTurnOutcome(events)
		for index := len(events) - 1; index >= 0; index-- {
			if events[index].Type == managedagents.EventAgentMessage {
				state.AgentText = tools.ExtractAgentMessageText(events[index].Payload)
				state.ResultJSON = tools.ExtractAgentMessageResultJSON(events[index].Payload)
				break
			}
		}
	}
	state.ResultSchema = cloneJSON(item.ExpectedResultSchema)
	state.ResultValid, state.ResultValidationError = validateTaskGroupItemResult(item.ExpectedResultSchema, state.ResultJSON)
	switch {
	case state.QueueRequest != nil:
		state.Status = managedagents.SubagentTaskGroupItemStateQueued
	case len(pending) > 0:
		state.Status = managedagents.PendingInterventionTurnStatus(pending)
	case session.Status == managedagents.SessionStatusIdle && state.LastTurnStatus == managedagents.TurnStatusCompleted:
		state.Status = managedagents.TurnStatusCompleted
	case session.Status == managedagents.SessionStatusIdle && state.LastTurnStatus == managedagents.TurnStatusFailed:
		state.Status = managedagents.TurnStatusFailed
	case session.Status == managedagents.SessionStatusIdle && state.LastTurnStatus == managedagents.TurnStatusInterrupted:
		state.Status = managedagents.SessionStatusTerminated
	case session.Status == managedagents.SessionStatusIdle && state.AgentText != "":
		state.Status = managedagents.TurnStatusCompleted
	case session.Status == managedagents.SessionStatusIdle:
		state.Status = managedagents.SubagentTaskGroupItemStateCreated
	default:
		state.Status = session.Status
	}
	if state.Status == managedagents.TurnStatusCompleted && !state.ResultValid {
		state.Status = managedagents.TurnStatusFailed
		state.Reason = state.ResultValidationError
	}
	if state.Reason == "" {
		state.Reason = strings.TrimSpace(item.ErrorMessage)
	}
	childGroups, err := managedagents.ListChildSubagentTaskGroupsWithContext(ctx, s.store, item.GroupID, item.ItemIndex)
	if err != nil {
		return tools.AgentTaskGroupItemState{}, err
	}
	state.NestedGroups = make([]tools.AgentTaskGroupNestedState, 0, len(childGroups))
	for _, childGroup := range childGroups {
		childState, err := s.taskGroupStateWithVisited(ctx, childGroup.ParentSessionID, childGroup.ID, visited)
		if err != nil {
			return tools.AgentTaskGroupItemState{}, err
		}
		state.NestedGroups = append(state.NestedGroups, tools.AgentTaskGroupNestedState{
			Group:     childState.Group,
			Status:    childState.Status,
			Completed: childState.Completed,
			Summary:   childState.Summary,
			Aggregate: childState.Aggregate,
			Items:     childState.Items,
		})
	}
	deriveNestedAggregateForItem(&state)
	return state, nil
}

func deriveNestedAggregateForItem(state *tools.AgentTaskGroupItemState) {
	if state == nil || len(state.NestedGroups) == 0 {
		return
	}
	if len(state.ResultJSON) == 0 && len(state.NestedGroups) == 1 && len(state.NestedGroups[0].Aggregate.JSON) > 0 {
		state.ResultJSON = cloneJSON(state.NestedGroups[0].Aggregate.JSON)
		state.ResultSchema = cloneJSON(state.NestedGroups[0].Aggregate.Schema)
	}
	if strings.TrimSpace(state.AgentText) == "" && len(state.NestedGroups) == 1 && strings.TrimSpace(state.NestedGroups[0].Aggregate.Text) != "" {
		state.AgentText = strings.TrimSpace(state.NestedGroups[0].Aggregate.Text)
	}
	if len(state.ResultJSON) == 0 && len(state.NestedGroups) > 1 {
		payloads := make([]map[string]any, 0, len(state.NestedGroups))
		for _, nested := range state.NestedGroups {
			payload := map[string]any{
				"group_id": nested.Group.ID,
				"status":   nested.Status,
			}
			if len(nested.Aggregate.JSON) > 0 {
				var decoded any
				if err := json.Unmarshal(nested.Aggregate.JSON, &decoded); err == nil {
					payload["aggregate_json"] = decoded
				}
			}
			if strings.TrimSpace(nested.Aggregate.Text) != "" {
				payload["aggregate_text"] = strings.TrimSpace(nested.Aggregate.Text)
			}
			payloads = append(payloads, payload)
		}
		if encoded, err := json.Marshal(payloads); err == nil {
			state.ResultJSON = encoded
			state.ResultSchema = json.RawMessage(`{"type":"array"}`)
		}
	}
}

func validateTaskGroupItemResult(schema json.RawMessage, result json.RawMessage) (bool, string) {
	if len(schema) == 0 || string(schema) == "null" {
		return true, ""
	}
	if len(result) == 0 || string(result) == "null" {
		return false, "missing result_json for validated task group item"
	}
	var schemaObject map[string]any
	if err := json.Unmarshal(schema, &schemaObject); err != nil {
		return false, "invalid expected_result_schema"
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return false, "invalid result_json payload"
	}
	if err := validateSchemaNode(schemaObject, value, "$"); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func validateSchemaNode(schema map[string]any, value any, path string) error {
	expectedType, _ := schema["type"].(string)
	switch expectedType {
	case "", "any":
		return nil
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be object", path)
		}
		required, _ := schema["required"].([]any)
		for _, entry := range required {
			key, _ := entry.(string)
			if key == "" {
				continue
			}
			if _, exists := object[key]; !exists {
				return fmt.Errorf("%s.%s is required", path, key)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, propertySchema := range properties {
			childValue, exists := object[key]
			if !exists {
				continue
			}
			propertyObject, ok := propertySchema.(map[string]any)
			if !ok {
				continue
			}
			if err := validateSchemaNode(propertyObject, childValue, path+"."+key); err != nil {
				return err
			}
		}
		return nil
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be array", path)
		}
		itemSchema, _ := schema["items"].(map[string]any)
		for index, item := range items {
			if len(itemSchema) == 0 {
				continue
			}
			if err := validateSchemaNode(itemSchema, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be string", path)
		}
		return nil
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s must be number", path)
		}
		return nil
	case "integer":
		number, ok := value.(float64)
		if !ok || number != float64(int64(number)) {
			return fmt.Errorf("%s must be integer", path)
		}
		return nil
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be boolean", path)
		}
		return nil
	case "null":
		if value != nil {
			return fmt.Errorf("%s must be null", path)
		}
		return nil
	default:
		return fmt.Errorf("%s has unsupported schema type %q", path, expectedType)
	}
}

func evaluateTaskGroup(group managedagents.SubagentTaskGroup, items []tools.AgentTaskGroupItemState) (string, bool, tools.AgentTaskGroupSummary) {
	summary := tools.AgentTaskGroupSummary{Total: len(items), Status: "running"}
	for _, item := range items {
		switch item.Status {
		case managedagents.TurnStatusCompleted:
			summary.Completed++
			summary.Terminal++
		case managedagents.TurnStatusFailed:
			summary.Failed++
			summary.Terminal++
		case managedagents.SessionStatusTerminated:
			summary.Terminated++
			summary.Terminal++
		case managedagents.SubagentTaskGroupItemStateRejected:
			summary.Rejected++
			summary.Terminal++
		case managedagents.SubagentTaskGroupItemStateQueued:
			summary.Queued++
		case managedagents.TurnStatusWaitingApproval, managedagents.TurnStatusWaitingHuman:
			summary.Waiting++
		default:
			summary.Running++
		}
	}
	remaining := summary.Total - summary.Terminal
	completed := false
	status := "running"
	if group.CanceledAt != nil {
		summary.Canceled = summary.Terminated + summary.Queued + summary.Running + summary.Waiting
		summary.Status = "canceled"
		return "canceled", true, summary
	}
	switch group.Strategy {
	case managedagents.SubagentTaskGroupStrategyAnyCompleted:
		if summary.Completed > 0 {
			status, completed = "completed", true
		} else if group.FailFast && (summary.Failed > 0 || summary.Rejected > 0 || summary.Terminated > 0) {
			status, completed = "failed", true
		} else if summary.Terminal == summary.Total {
			status, completed = "failed", true
		}
	case managedagents.SubagentTaskGroupStrategyQuorum:
		if summary.Completed >= group.Quorum {
			status, completed = "completed", true
		} else if group.FailFast && (summary.Failed > 0 || summary.Rejected > 0 || summary.Terminated > 0) {
			status, completed = "failed", true
		} else if summary.Completed+remaining < group.Quorum {
			status, completed = "failed", true
		}
	default:
		if group.FailFast && (summary.Failed > 0 || summary.Rejected > 0 || summary.Terminated > 0) {
			status, completed = "failed", true
		} else if summary.Terminal == summary.Total {
			if summary.Failed > 0 || summary.Rejected > 0 || summary.Terminated > 0 {
				status = "failed"
			} else {
				status = "completed"
			}
			completed = true
		}
	}
	summary.Status = status
	return status, completed, summary
}

func buildTaskGroupAggregate(group managedagents.SubagentTaskGroup, items []tools.AgentTaskGroupItemState) tools.AgentTaskGroupAggregate {
	aggregate := tools.AgentTaskGroupAggregate{
		Reducer:              group.ResultReducer,
		CompletedItemIndexes: []int{},
		FailedItemIndexes:    []int{},
		CanceledItemIndexes:  []int{},
	}
	texts := make([]string, 0, len(items))
	type textVote struct {
		text      string
		count     int
		firstSeen int
	}
	votes := map[string]*textVote{}
	jsonEntries := make([]map[string]any, 0, len(items))
	jsonObject := make(map[string]map[string]any, len(items))
	type valueVote struct {
		key       string
		value     json.RawMessage
		count     int
		firstSeen int
	}
	valueVotes := map[string]*valueVote{}
	values := make([]json.RawMessage, 0, len(items))
	var firstValue json.RawMessage
	var firstSchema json.RawMessage
	for _, item := range items {
		switch item.Status {
		case managedagents.TurnStatusCompleted:
			aggregate.CompletedItemIndexes = append(aggregate.CompletedItemIndexes, item.Item.ItemIndex)
			trimmed := strings.TrimSpace(item.AgentText)
			if trimmed != "" {
				texts = append(texts, fmt.Sprintf("[%d] %s", item.Item.ItemIndex, trimmed))
				vote := votes[trimmed]
				if vote == nil {
					votes[trimmed] = &textVote{text: trimmed, count: 1, firstSeen: item.Item.ItemIndex}
				} else {
					vote.count++
				}
			}
			if len(item.ResultJSON) > 0 {
				value := cloneJSON(item.ResultJSON)
				values = append(values, value)
				if len(firstValue) == 0 {
					firstValue = cloneJSON(value)
				}
				key := string(value)
				vote := valueVotes[key]
				if vote == nil {
					valueVotes[key] = &valueVote{key: key, value: cloneJSON(value), count: 1, firstSeen: item.Item.ItemIndex}
				} else {
					vote.count++
				}
				schema := effectiveResultSchema(item)
				if len(schema) > 0 && len(firstSchema) == 0 {
					firstSchema = cloneJSON(schema)
				} else if len(firstSchema) == 0 {
					firstSchema = inferSchemaFromJSONValue(value)
				}
			}
		case managedagents.TurnStatusFailed, managedagents.SubagentTaskGroupItemStateRejected:
			aggregate.FailedItemIndexes = append(aggregate.FailedItemIndexes, item.Item.ItemIndex)
		case managedagents.SessionStatusTerminated:
			aggregate.CanceledItemIndexes = append(aggregate.CanceledItemIndexes, item.Item.ItemIndex)
		}
		entry := map[string]any{
			"item_index":       item.Item.ItemIndex,
			"status":           item.Status,
			"session_id":       item.Item.SessionID,
			"agent_text":       strings.TrimSpace(item.AgentText),
			"last_turn_status": item.LastTurnStatus,
			"reason":           item.Reason,
			"retry_count":      item.Item.RetryCount,
		}
		if len(item.NestedGroups) > 0 {
			nestedIDs := make([]string, 0, len(item.NestedGroups))
			for _, nested := range item.NestedGroups {
				nestedIDs = append(nestedIDs, nested.Group.ID)
			}
			entry["nested_group_ids"] = nestedIDs
		}
		jsonEntries = append(jsonEntries, entry)
		jsonObject[fmt.Sprintf("%d", item.Item.ItemIndex)] = entry
	}
	switch group.ResultReducer {
	case managedagents.SubagentTaskGroupReducerConcatText:
		aggregate.Text = strings.TrimSpace(strings.Join(texts, "\n\n"))
		aggregate.Schema = json.RawMessage(`{"type":"string"}`)
	case managedagents.SubagentTaskGroupReducerFirstSuccess:
		if len(texts) > 0 {
			aggregate.Text = strings.TrimSpace(strings.TrimPrefix(texts[0], fmt.Sprintf("[%d] ", aggregate.CompletedItemIndexes[0])))
		}
		aggregate.Schema = json.RawMessage(`{"type":"string"}`)
	case managedagents.SubagentTaskGroupReducerMajorityText:
		var best *textVote
		for _, vote := range votes {
			if best == nil || vote.count > best.count || (vote.count == best.count && vote.firstSeen < best.firstSeen) {
				best = vote
			}
		}
		if best != nil {
			aggregate.Text = best.text
		}
		aggregate.Schema = json.RawMessage(`{"type":"string"}`)
	case managedagents.SubagentTaskGroupReducerJSONList:
		if encoded, err := json.Marshal(jsonEntries); err == nil {
			aggregate.JSON = encoded
			aggregate.Schema = json.RawMessage(`{"type":"array"}`)
		}
	case managedagents.SubagentTaskGroupReducerJSONObject:
		if encoded, err := json.Marshal(jsonObject); err == nil {
			aggregate.JSON = encoded
			aggregate.Schema = json.RawMessage(`{"type":"object"}`)
		}
	case managedagents.SubagentTaskGroupReducerJSONValues:
		aggregate.JSON = marshalJSONArray(values)
		if len(aggregate.JSON) > 0 {
			if len(firstSchema) > 0 {
				aggregate.Schema = append(json.RawMessage(nil), []byte(`{"type":"array","items":`)...)
				aggregate.Schema = append(aggregate.Schema, firstSchema...)
				aggregate.Schema = append(aggregate.Schema, '}')
			} else {
				aggregate.Schema = json.RawMessage(`{"type":"array"}`)
			}
		}
	case managedagents.SubagentTaskGroupReducerMergeObjects:
		merged := mergeObjectsWithSchema(items, firstSchema)
		if encoded, err := json.Marshal(merged); err == nil && len(merged) > 0 {
			aggregate.JSON = encoded
			if len(firstSchema) > 0 {
				aggregate.Schema = cloneJSON(firstSchema)
			} else {
				aggregate.Schema = inferSchemaFromJSONValue(encoded)
			}
		}
	case managedagents.SubagentTaskGroupReducerFirstValue:
		aggregate.JSON = cloneJSON(firstValue)
		if len(firstSchema) > 0 {
			aggregate.Schema = cloneJSON(firstSchema)
		} else {
			aggregate.Schema = inferSchemaFromJSONValue(firstValue)
		}
	case managedagents.SubagentTaskGroupReducerMajorityValue:
		var best *valueVote
		for _, vote := range valueVotes {
			if best == nil || vote.count > best.count || (vote.count == best.count && vote.firstSeen < best.firstSeen) {
				best = vote
			}
		}
		if best != nil {
			aggregate.JSON = cloneJSON(best.value)
			if len(firstSchema) > 0 {
				aggregate.Schema = cloneJSON(firstSchema)
			} else {
				aggregate.Schema = inferSchemaFromJSONValue(best.value)
			}
		}
	}
	return aggregate
}

func effectiveResultSchema(item tools.AgentTaskGroupItemState) json.RawMessage {
	if len(item.ResultSchema) > 0 && string(item.ResultSchema) != "null" {
		return cloneJSON(item.ResultSchema)
	}
	return cloneJSON(item.Item.ExpectedResultSchema)
}

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	clone := make([]byte, len(raw))
	copy(clone, raw)
	return clone
}

func marshalJSONArray(values []json.RawMessage) json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	raws := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		raws = append(raws, cloneJSON(value))
	}
	encoded, err := json.Marshal(raws)
	if err != nil {
		return nil
	}
	return encoded
}

func inferSchemaFromJSONValue(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	schema := inferSchemaNode(value)
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	return encoded
}

func inferSchemaNode(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		properties := make(map[string]any, len(typed))
		for key, child := range typed {
			properties[key] = inferSchemaNode(child)
		}
		return map[string]any{"type": "object", "properties": properties}
	case []any:
		schema := map[string]any{"type": "array"}
		if len(typed) > 0 {
			schema["items"] = inferSchemaNode(typed[0])
		}
		return schema
	case string:
		return map[string]any{"type": "string"}
	case bool:
		return map[string]any{"type": "boolean"}
	case float64:
		if typed == float64(int64(typed)) {
			return map[string]any{"type": "integer"}
		}
		return map[string]any{"type": "number"}
	case nil:
		return map[string]any{"type": "null"}
	default:
		return map[string]any{"type": "any"}
	}
}

func mergeObjectsWithSchema(items []tools.AgentTaskGroupItemState, schemaRaw json.RawMessage) map[string]any {
	var schema map[string]any
	_ = json.Unmarshal(schemaRaw, &schema)
	merged := make(map[string]any)
	for _, item := range items {
		if item.Status != managedagents.TurnStatusCompleted || len(item.ResultJSON) == 0 {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal(item.ResultJSON, &object); err != nil || object == nil {
			continue
		}
		merged = mergeObjectNode(merged, object, schema)
	}
	return merged
}

func mergeObjectNode(base map[string]any, incoming map[string]any, schema map[string]any) map[string]any {
	if base == nil {
		base = make(map[string]any)
	}
	properties, _ := schema["properties"].(map[string]any)
	for key, incomingValue := range incoming {
		propertySchema, _ := properties[key].(map[string]any)
		currentValue, exists := base[key]
		if !exists {
			base[key] = cloneAny(incomingValue)
			continue
		}
		base[key] = mergeValueNode(currentValue, incomingValue, propertySchema)
	}
	return base
}

func mergeValueNode(current any, incoming any, schema map[string]any) any {
	expectedType, _ := schema["type"].(string)
	switch expectedType {
	case "object":
		currentObject, okCurrent := current.(map[string]any)
		incomingObject, okIncoming := incoming.(map[string]any)
		if okCurrent && okIncoming {
			return mergeObjectNode(currentObject, incomingObject, schema)
		}
	case "array":
		currentArray, okCurrent := current.([]any)
		incomingArray, okIncoming := incoming.([]any)
		if okCurrent && okIncoming {
			return mergeArrayNode(currentArray, incomingArray, schema)
		}
	}
	return resolveScalarConflict(current, incoming, schema)
}

func mergeArrayNode(current []any, incoming []any, schema map[string]any) []any {
	mode, _ := schema["x-array-merge"].(string)
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "replace":
		return cloneAny(incoming).([]any)
	case "dedupe":
		merged := make([]any, 0, len(current)+len(incoming))
		seen := map[string]bool{}
		for _, value := range append(append([]any(nil), current...), incoming...) {
			key := fmt.Sprintf("%#v", value)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, cloneAny(value))
		}
		return merged
	default:
		merged := append([]any{}, current...)
		for _, value := range incoming {
			merged = append(merged, cloneAny(value))
		}
		return merged
	}
}

func resolveScalarConflict(current any, incoming any, schema map[string]any) any {
	mode, _ := schema["x-conflict-mode"].(string)
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "first_wins":
		return current
	case "last_wins", "":
		return cloneAny(incoming)
	default:
		return cloneAny(incoming)
	}
}

func cloneAny(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return value
	}
	return decoded
}

func (s agentToolService) decideToolIntervention(ctx context.Context, request tools.AgentInterventionDecisionRequest, status string) (tools.AgentInterventionDecisionResponse, error) {
	session, _, err := s.loadSessionState(ctx, request.ParentSessionID, request.SessionID)
	if err != nil {
		return tools.AgentInterventionDecisionResponse{}, err
	}
	databaseCtx, err := managedagents.ContextWithDatabaseAccessScope(ctx, managedagents.AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID})
	if err != nil {
		return tools.AgentInterventionDecisionResponse{}, err
	}
	result, err := managedagents.DecideSessionInterventionWithContext(databaseCtx, s.store, request.SessionID, managedagents.DecideSessionInterventionInput{
		TurnID:         strings.TrimSpace(request.TurnID),
		CallID:         strings.TrimSpace(request.CallID),
		Status:         status,
		DecisionReason: strings.TrimSpace(request.Reason),
	})
	if err != nil {
		return tools.AgentInterventionDecisionResponse{}, err
	}
	response := tools.AgentInterventionDecisionResponse{
		Intervention: result.Intervention,
		Events:       result.Events,
	}
	shouldResume, err := s.shouldScheduleInterventionResume(databaseCtx, result)
	if err != nil {
		return tools.AgentInterventionDecisionResponse{}, err
	}
	if !shouldResume {
		return response, nil
	}
	if err := s.runner.StartTurn(context.Background(), runner.TurnRequest{
		SessionID:          result.Intervention.SessionID,
		TurnID:             result.Intervention.TurnID,
		ResumeIntervention: &result.Intervention,
		Scope:              managedagents.AccessScope{WorkspaceID: session.WorkspaceID, OwnerID: session.OwnerID},
	}); err != nil && !errors.Is(err, runner.ErrTurnAlreadyRunning) {
		return tools.AgentInterventionDecisionResponse{}, err
	}
	response.Resumed = true
	return response, nil
}

func subagentQuotaError(errorType string, message string, state map[string]any) error {
	if state == nil {
		state = map[string]any{}
	}
	state["category"] = "quota"
	state["conflict"] = true
	return tools.AgentToolError{
		Type:    errorType,
		Message: message,
		State:   state,
	}
}

func (s agentToolService) rejectSpawn(ctx context.Context, parentSession managedagents.Session, parentTurnID string, spawnErr error) error {
	return s.recordSubagentRejection(ctx, parentSession, parentTurnID, managedagents.EventRuntimeSubagentSpawnRejected, spawnErr)
}

func (s agentToolService) rejectStart(ctx context.Context, parentSession managedagents.Session, parentTurnID string, startErr error) error {
	return s.recordSubagentRejection(ctx, parentSession, parentTurnID, managedagents.EventRuntimeSubagentStartRejected, startErr)
}

func (s agentToolService) recordSubagentRejection(ctx context.Context, parentSession managedagents.Session, parentTurnID string, eventType string, actionErr error) error {
	var toolErr tools.AgentToolError
	if !errors.As(actionErr, &toolErr) {
		return actionErr
	}
	payload := map[string]any{
		"error_type":        toolErr.Type,
		"message":           toolErr.Error(),
		"parent_session_id": parentSession.ID,
		"parent_turn_id":    strings.TrimSpace(parentTurnID),
	}
	if state, ok := toolErr.State.(map[string]any); ok {
		for key, value := range state {
			payload[key] = value
		}
	}
	if eventType == managedagents.EventRuntimeSubagentStartRejected {
		if _, exists := payload["wait_seconds"]; !exists {
			payload["wait_seconds"] = 0
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		s.logger.Warn("marshal subagent rejection event", "event_type", eventType, "session_id", parentSession.ID, "turn_id", parentTurnID, "error", err)
		return actionErr
	}
	if _, err := managedagents.AppendRuntimeEventWithContext(ctx, s.store, parentSession.ID, strings.TrimSpace(parentTurnID), managedagents.AppendEventInput{
		Type:    eventType,
		Payload: encoded,
	}); err != nil {
		s.logger.Warn("append subagent rejection event", "event_type", eventType, "session_id", parentSession.ID, "turn_id", parentTurnID, "error", err)
	}
	return actionErr
}

func (s agentToolService) shouldScheduleInterventionResume(ctx context.Context, result managedagents.DecideSessionInterventionResult) (bool, error) {
	if len(result.Events) > 0 {
		return true, nil
	}
	session, err := managedagents.GetSessionWithContext(ctx, s.store, result.Intervention.SessionID)
	if err != nil {
		return false, err
	}
	if session.Status != managedagents.SessionStatusRunning {
		return false, nil
	}
	pending, err := managedagents.ListSessionInterventionsWithContext(ctx, s.store, result.Intervention.SessionID, managedagents.InterventionStatusPending)
	if err != nil {
		return false, err
	}
	for _, intervention := range pending {
		if intervention.TurnID == result.Intervention.TurnID {
			return false, nil
		}
	}
	return true, nil
}
