package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/tools"
)

func (s agentToolService) CreateDeliberation(ctx context.Context, request tools.AgentDeliberationCreateRequest) (tools.AgentDeliberationResponse, error) {
	store, err := s.deliberationStore()
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	parent, err := s.parentSession(ctx, request.ParentSessionID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	request, err = tools.NormalizeAgentDeliberationRequest(request, parent)
	if err != nil {
		return tools.AgentDeliberationResponse{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	if request.IdempotencyKey != "" {
		existing, lookupErr := managedagents.GetAgentDeliberationByIdempotencyWithContext(ctx, store, parent.ID, request.IdempotencyKey)
		if lookupErr == nil {
			return s.reconcileDeliberation(ctx, parent.ID, existing.ID)
		}
		if !errors.Is(lookupErr, managedagents.ErrNotFound) {
			return tools.AgentDeliberationResponse{}, lookupErr
		}
	}
	plan, err := json.Marshal(request)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	participants := make([]managedagents.AgentDeliberationParticipant, 0, len(request.Participants))
	for index, participant := range request.Participants {
		participants = append(participants, managedagents.AgentDeliberationParticipant{
			ParticipantIndex: index,
			RoleID:           participant.RoleID,
			RoleTitle:        participant.RoleTitle,
			Goal:             participant.Goal,
			AgentID:          participant.AgentID,
			EnvironmentID:    participant.EnvironmentID,
		})
	}
	deliberation, err := managedagents.CreateAgentDeliberationWithContext(ctx, store, managedagents.CreateAgentDeliberationInput{
		Deliberation: managedagents.AgentDeliberation{
			WorkspaceID:            parent.WorkspaceID,
			OwnerID:                parent.OwnerID,
			ParentSessionID:        parent.ID,
			ParentTurnID:           request.ParentTurnID,
			IdempotencyKey:         request.IdempotencyKey,
			Objective:              request.Objective,
			Strategy:               request.Strategy,
			MaxTokens:              request.Budget.MaxTokens,
			MaxSeconds:             request.Budget.MaxSeconds,
			ModeratorAgentID:       request.ModeratorAgentID,
			ModeratorEnvironmentID: request.ModeratorEnvironmentID,
			Plan:                   plan,
		},
		Participants: participants,
	})
	if err != nil {
		if request.IdempotencyKey != "" {
			if existing, lookupErr := managedagents.GetAgentDeliberationByIdempotencyWithContext(ctx, store, parent.ID, request.IdempotencyKey); lookupErr == nil {
				return s.reconcileDeliberation(ctx, parent.ID, existing.ID)
			}
		}
		return tools.AgentDeliberationResponse{}, err
	}
	s.recordDeliberationEvent(ctx, deliberation, "runtime.agent_deliberation_created", map[string]any{
		"deliberation_id": deliberation.ID,
		"strategy":        deliberation.Strategy,
		"participants":    len(participants),
		"max_tokens":      deliberation.MaxTokens,
		"max_seconds":     deliberation.MaxSeconds,
	})
	return s.reconcileDeliberation(ctx, parent.ID, deliberation.ID)
}

func (s agentToolService) GetDeliberation(ctx context.Context, request tools.AgentDeliberationRequest) (tools.AgentDeliberationResponse, error) {
	return s.reconcileDeliberation(ctx, request.ParentSessionID, request.DeliberationID)
}

func (s agentToolService) CollectDeliberation(ctx context.Context, request tools.AgentDeliberationRequest) (tools.AgentDeliberationResponse, error) {
	return s.reconcileDeliberation(ctx, request.ParentSessionID, request.DeliberationID)
}

func (s agentToolService) WaitDeliberation(ctx context.Context, request tools.AgentDeliberationWaitRequest) (tools.AgentDeliberationWaitResponse, error) {
	timeout := time.NewTimer(tools.WaitTimeout(request.TimeoutSeconds))
	defer timeout.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := s.reconcileDeliberation(ctx, request.ParentSessionID, request.DeliberationID)
		if err != nil {
			return tools.AgentDeliberationWaitResponse{}, err
		}
		result := tools.AgentDeliberationWaitResponse{AgentDeliberationResponse: response}
		if response.Completed || response.Deliberation.Status == managedagents.AgentDeliberationStatusFailed || response.Deliberation.Status == managedagents.AgentDeliberationStatusCanceled {
			return result, nil
		}
		select {
		case <-ctx.Done():
			result.TimedOut = true
			return result, nil
		case <-timeout.C:
			result.TimedOut = true
			return result, nil
		case <-ticker.C:
		}
	}
}

func (s agentToolService) CancelDeliberation(ctx context.Context, request tools.AgentDeliberationCancelRequest) (tools.AgentDeliberationResponse, error) {
	store, err := s.deliberationStore()
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	deliberation, err := s.loadOwnedDeliberation(ctx, request.ParentSessionID, request.DeliberationID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	if deliberation.Status == managedagents.AgentDeliberationStatusCanceled || deliberation.Status == managedagents.AgentDeliberationStatusCompleted {
		return s.deliberationResponse(ctx, deliberation)
	}
	reason := strings.TrimSpace(request.Reason)
	if reason == "" {
		reason = "canceled by parent agent"
	}
	rounds, err := managedagents.ListAgentDeliberationRoundsWithContext(ctx, store, deliberation.ID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	for _, round := range rounds {
		_, _ = s.CancelTaskGroup(ctx, tools.AgentTaskGroupCancelRequest{ParentSessionID: deliberation.ParentSessionID, GroupID: round.TaskGroupID, Reason: reason})
		if round.ModeratorGroupID != "" {
			_, _ = s.CancelTaskGroup(ctx, tools.AgentTaskGroupCancelRequest{ParentSessionID: deliberation.ParentSessionID, GroupID: round.ModeratorGroupID, Reason: reason})
		}
		_, _ = managedagents.UpdateAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, round.RoundNumber, managedagents.UpdateAgentDeliberationRoundInput{
			Status: roundStatusCanceled, ModeratorGroupID: round.ModeratorGroupID, Summary: round.Summary, Questions: round.Questions, Complete: true,
		})
	}
	if deliberation.FinalGroupID != "" {
		_, _ = s.CancelTaskGroup(ctx, tools.AgentTaskGroupCancelRequest{ParentSessionID: deliberation.ParentSessionID, GroupID: deliberation.FinalGroupID, Reason: reason})
	}
	deliberation, err = managedagents.UpdateAgentDeliberationWithContext(ctx, store, deliberation.ID, managedagents.UpdateAgentDeliberationInput{
		Status: managedagents.AgentDeliberationStatusCanceled, Phase: managedagents.AgentDeliberationPhaseCanceled,
		FinalGroupID: deliberation.FinalGroupID, FinalResult: deliberation.FinalResult, CancelReason: reason,
	})
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	s.recordDeliberationEvent(ctx, deliberation, "runtime.agent_deliberation_canceled", map[string]any{"deliberation_id": deliberation.ID, "reason": reason})
	return s.deliberationResponse(ctx, deliberation)
}

func (s agentToolService) RetryDeliberationParticipant(ctx context.Context, request tools.AgentDeliberationRetryParticipantRequest) (tools.AgentDeliberationResponse, error) {
	store, err := s.deliberationStore()
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	deliberation, err := s.loadOwnedDeliberation(ctx, request.ParentSessionID, request.DeliberationID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	expectedPhase := managedagents.AgentDeliberationPhaseRound1Running
	if request.RoundNumber == 2 {
		expectedPhase = managedagents.AgentDeliberationPhaseRound2Running
	}
	if request.RoundNumber < 1 || request.RoundNumber > 2 || deliberation.Phase != expectedPhase {
		return tools.AgentDeliberationResponse{}, fmt.Errorf("%w: participant retry is only allowed while its round is active", managedagents.ErrConflict)
	}
	participants, err := managedagents.ListAgentDeliberationParticipantsWithContext(ctx, store, deliberation.ID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	if request.ParticipantIndex < 0 || request.ParticipantIndex >= len(participants) {
		return tools.AgentDeliberationResponse{}, fmt.Errorf("%w: participant_index is out of range", managedagents.ErrInvalid)
	}
	round, err := managedagents.GetAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, request.RoundNumber)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	if _, err := s.RetryTaskGroupItem(ctx, tools.AgentTaskGroupRetryItemRequest{
		ParentSessionID: deliberation.ParentSessionID, GroupID: round.TaskGroupID, ItemIndex: request.ParticipantIndex,
	}); err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	if _, err := managedagents.UpdateAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, request.RoundNumber, managedagents.UpdateAgentDeliberationRoundInput{
		Status: roundStatusRunning, ModeratorGroupID: round.ModeratorGroupID, Summary: round.Summary, Questions: round.Questions,
	}); err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	s.recordDeliberationEvent(ctx, deliberation, "runtime.agent_deliberation_participant_retried", map[string]any{
		"deliberation_id": deliberation.ID, "round_number": request.RoundNumber, "participant_index": request.ParticipantIndex,
	})
	return s.reconcileDeliberation(ctx, request.ParentSessionID, deliberation.ID)
}

const (
	roundStatusRunning    = "running"
	roundStatusModerating = "moderating"
	roundStatusCompleted  = "completed"
	roundStatusFailed     = "failed"
	roundStatusCanceled   = "canceled"
)

func (s agentToolService) reconcileDeliberation(ctx context.Context, parentSessionID string, deliberationID string) (tools.AgentDeliberationResponse, error) {
	store, err := s.deliberationStore()
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	for transitions := 0; transitions < 8; transitions++ {
		deliberation, err := s.loadOwnedDeliberation(ctx, parentSessionID, deliberationID)
		if err != nil {
			return tools.AgentDeliberationResponse{}, err
		}
		if deliberation.Status != managedagents.AgentDeliberationStatusRunning {
			return s.deliberationResponse(ctx, deliberation)
		}
		if deliberation.MaxSeconds > 0 && time.Since(deliberation.CreatedAt) > time.Duration(deliberation.MaxSeconds)*time.Second {
			deliberation, err = managedagents.UpdateAgentDeliberationWithContext(ctx, store, deliberation.ID, managedagents.UpdateAgentDeliberationInput{
				Status: managedagents.AgentDeliberationStatusFailed, Phase: deliberation.Phase, FinalGroupID: deliberation.FinalGroupID,
				FinalResult: deliberation.FinalResult, CancelReason: "deliberation time budget exceeded",
			})
			if err != nil {
				return tools.AgentDeliberationResponse{}, err
			}
			return s.deliberationResponse(ctx, deliberation)
		}
		switch deliberation.Phase {
		case managedagents.AgentDeliberationPhaseRound1Running:
			advanced, err := s.reconcileParticipantRound(ctx, deliberation, 1)
			if err != nil || !advanced {
				if err != nil {
					return tools.AgentDeliberationResponse{}, err
				}
				return s.deliberationResponse(ctx, deliberation)
			}
		case managedagents.AgentDeliberationPhaseRound1Moderating:
			advanced, err := s.reconcileRoundOneModeration(ctx, deliberation)
			if err != nil || !advanced {
				if err != nil {
					return tools.AgentDeliberationResponse{}, err
				}
				return s.deliberationResponse(ctx, deliberation)
			}
		case managedagents.AgentDeliberationPhaseRound2Running:
			advanced, err := s.reconcileParticipantRound(ctx, deliberation, 2)
			if err != nil || !advanced {
				if err != nil {
					return tools.AgentDeliberationResponse{}, err
				}
				return s.deliberationResponse(ctx, deliberation)
			}
		case managedagents.AgentDeliberationPhaseFinalizing:
			advanced, err := s.reconcileFinalModeration(ctx, deliberation)
			if err != nil || !advanced {
				if err != nil {
					return tools.AgentDeliberationResponse{}, err
				}
				return s.deliberationResponse(ctx, deliberation)
			}
		default:
			return tools.AgentDeliberationResponse{}, fmt.Errorf("%w: unsupported deliberation phase %q", managedagents.ErrInvalid, deliberation.Phase)
		}
	}
	deliberation, err := managedagents.GetAgentDeliberationWithContext(ctx, store, deliberationID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	return s.deliberationResponse(ctx, deliberation)
}

func (s agentToolService) reconcileParticipantRound(ctx context.Context, deliberation managedagents.AgentDeliberation, roundNumber int) (bool, error) {
	store, _ := s.deliberationStore()
	round, err := managedagents.GetAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, roundNumber)
	if errors.Is(err, managedagents.ErrNotFound) {
		created, createErr := s.createParticipantRound(ctx, deliberation, roundNumber)
		return created, createErr
	}
	if err != nil {
		return false, err
	}
	state, err := s.GetTaskGroup(ctx, tools.AgentTaskGroupRequest{ParentSessionID: deliberation.ParentSessionID, GroupID: round.TaskGroupID})
	if err != nil {
		return false, err
	}
	if err := s.syncDeliberationContributions(ctx, deliberation.ID, roundNumber, state); err != nil {
		return false, err
	}
	if !state.Completed {
		return false, nil
	}
	if state.Status != "completed" {
		if round.Status != roundStatusFailed {
			if _, err := managedagents.UpdateAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, roundNumber, managedagents.UpdateAgentDeliberationRoundInput{
				Status: roundStatusFailed, ModeratorGroupID: round.ModeratorGroupID, Summary: round.Summary, Questions: round.Questions,
			}); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	if roundNumber == 1 {
		if round.ModeratorGroupID == "" {
			contributions, err := managedagents.ListAgentDeliberationContributionsWithContext(ctx, store, deliberation.ID, 1)
			if err != nil {
				return false, err
			}
			moderator, err := s.CreateTaskGroup(ctx, tools.AgentTaskGroupCreateRequest{
				ParentSessionID: deliberation.ParentSessionID, ParentTurnID: deliberationStageTurnID(deliberation, "round1_moderator"),
				Strategy: managedagents.SubagentTaskGroupStrategyAllCompleted, ResultReducer: managedagents.SubagentTaskGroupReducerFirstValue,
				Items: []tools.AgentTaskGroupItemRequest{{AgentID: deliberation.ModeratorAgentID, EnvironmentID: deliberation.ModeratorEnvironmentID,
					Title: "Discussion moderator: disagreements and questions", Message: moderationPrompt(deliberation, contributions), ExpectedResultSchema: tools.AgentDeliberationModerationSchema}},
			})
			if err != nil {
				return false, err
			}
			round, err = managedagents.UpdateAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, 1, managedagents.UpdateAgentDeliberationRoundInput{
				Status: roundStatusModerating, ModeratorGroupID: moderator.Group.ID, Summary: round.Summary, Questions: round.Questions,
			})
			if err != nil {
				return false, err
			}
		}
		_, err = managedagents.UpdateAgentDeliberationWithContext(ctx, store, deliberation.ID, managedagents.UpdateAgentDeliberationInput{
			Status: deliberation.Status, Phase: managedagents.AgentDeliberationPhaseRound1Moderating,
			FinalGroupID: deliberation.FinalGroupID, FinalResult: deliberation.FinalResult,
		})
		return err == nil, err
	}
	if deliberation.FinalGroupID == "" {
		allContributions, err := managedagents.ListAgentDeliberationContributionsWithContext(ctx, store, deliberation.ID, 0)
		if err != nil {
			return false, err
		}
		roundOne, err := managedagents.GetAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, 1)
		if err != nil {
			return false, err
		}
		finalGroup, err := s.CreateTaskGroup(ctx, tools.AgentTaskGroupCreateRequest{
			ParentSessionID: deliberation.ParentSessionID, ParentTurnID: deliberationStageTurnID(deliberation, "final_moderator"),
			Strategy: managedagents.SubagentTaskGroupStrategyAllCompleted, ResultReducer: managedagents.SubagentTaskGroupReducerFirstValue,
			Items: []tools.AgentTaskGroupItemRequest{{AgentID: deliberation.ModeratorAgentID, EnvironmentID: deliberation.ModeratorEnvironmentID,
				Title: "Discussion moderator: final consensus", Message: finalModerationPrompt(deliberation, roundOne, allContributions), ExpectedResultSchema: tools.AgentDeliberationFinalSchema}},
		})
		if err != nil {
			return false, err
		}
		deliberation.FinalGroupID = finalGroup.Group.ID
	}
	_, err = managedagents.UpdateAgentDeliberationWithContext(ctx, store, deliberation.ID, managedagents.UpdateAgentDeliberationInput{
		Status: deliberation.Status, Phase: managedagents.AgentDeliberationPhaseFinalizing,
		FinalGroupID: deliberation.FinalGroupID, FinalResult: deliberation.FinalResult,
	})
	return err == nil, err
}

func (s agentToolService) reconcileRoundOneModeration(ctx context.Context, deliberation managedagents.AgentDeliberation) (bool, error) {
	store, _ := s.deliberationStore()
	round, err := managedagents.GetAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, 1)
	if err != nil {
		return false, err
	}
	state, err := s.GetTaskGroup(ctx, tools.AgentTaskGroupRequest{ParentSessionID: deliberation.ParentSessionID, GroupID: round.ModeratorGroupID})
	if err != nil || !state.Completed {
		return false, err
	}
	summary := deliberationGroupResult(state, "agreements", "disagreements", "missing_evidence", "questions_by_role")
	questions := extractJSONField(summary, "questions_by_role", json.RawMessage(`{}`))
	if _, err := managedagents.UpdateAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, 1, managedagents.UpdateAgentDeliberationRoundInput{
		Status: roundStatusCompleted, ModeratorGroupID: round.ModeratorGroupID, Summary: summary, Questions: questions, Complete: true,
	}); err != nil {
		return false, err
	}
	if _, err := managedagents.GetAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, 2); errors.Is(err, managedagents.ErrNotFound) {
		if _, createErr := s.createParticipantRound(ctx, deliberation, 2); createErr != nil {
			return false, createErr
		}
	} else if err != nil {
		return false, err
	}
	_, err = managedagents.UpdateAgentDeliberationWithContext(ctx, store, deliberation.ID, managedagents.UpdateAgentDeliberationInput{
		Status: deliberation.Status, Phase: managedagents.AgentDeliberationPhaseRound2Running,
		FinalGroupID: deliberation.FinalGroupID, FinalResult: deliberation.FinalResult,
	})
	return err == nil, err
}

func (s agentToolService) reconcileFinalModeration(ctx context.Context, deliberation managedagents.AgentDeliberation) (bool, error) {
	store, _ := s.deliberationStore()
	state, err := s.GetTaskGroup(ctx, tools.AgentTaskGroupRequest{ParentSessionID: deliberation.ParentSessionID, GroupID: deliberation.FinalGroupID})
	if err != nil || !state.Completed {
		return false, err
	}
	result := deliberationGroupResult(state, "recommendation", "consensus", "dissenting_opinions", "risks", "followups", "confidence")
	deliberation, err = managedagents.UpdateAgentDeliberationWithContext(ctx, store, deliberation.ID, managedagents.UpdateAgentDeliberationInput{
		Status: managedagents.AgentDeliberationStatusCompleted, Phase: managedagents.AgentDeliberationPhaseCompleted,
		FinalGroupID: deliberation.FinalGroupID, FinalResult: result,
	})
	if err != nil {
		return false, err
	}
	s.recordDeliberationEvent(ctx, deliberation, "runtime.agent_deliberation_completed", map[string]any{"deliberation_id": deliberation.ID, "strategy": deliberation.Strategy})
	return true, nil
}

func (s agentToolService) createParticipantRound(ctx context.Context, deliberation managedagents.AgentDeliberation, roundNumber int) (bool, error) {
	store, _ := s.deliberationStore()
	participants, err := managedagents.ListAgentDeliberationParticipantsWithContext(ctx, store, deliberation.ID)
	if err != nil {
		return false, err
	}
	var roundOne managedagents.AgentDeliberationRound
	if roundNumber == 2 {
		roundOne, err = managedagents.GetAgentDeliberationRoundWithContext(ctx, store, deliberation.ID, 1)
		if err != nil {
			return false, err
		}
	}
	items := make([]tools.AgentTaskGroupItemRequest, 0, len(participants))
	for _, participant := range participants {
		message := roundOnePrompt(deliberation, participant)
		if roundNumber == 2 {
			message = roundTwoPrompt(deliberation, participant, roundOne)
		}
		items = append(items, tools.AgentTaskGroupItemRequest{
			AgentID: participant.AgentID, EnvironmentID: participant.EnvironmentID,
			Title:   fmt.Sprintf("Discussion round %d: %s", roundNumber, participant.RoleTitle),
			Message: message, ExpectedResultSchema: tools.AgentDeliberationContributionSchema,
		})
	}
	group, err := s.CreateTaskGroup(ctx, tools.AgentTaskGroupCreateRequest{
		ParentSessionID: deliberation.ParentSessionID, ParentTurnID: deliberationStageTurnID(deliberation, fmt.Sprintf("round%d", roundNumber)),
		Strategy: managedagents.SubagentTaskGroupStrategyAllCompleted, ResultReducer: managedagents.SubagentTaskGroupReducerJSONValues,
		Items: items,
	})
	if err != nil {
		return false, err
	}
	roundType := "independent_brainstorm"
	if roundNumber == 2 {
		roundType = "cross_critique"
	}
	if _, err := managedagents.CreateAgentDeliberationRoundWithContext(ctx, store, managedagents.AgentDeliberationRound{
		DeliberationID: deliberation.ID, RoundNumber: roundNumber, RoundType: roundType,
		Status: roundStatusRunning, TaskGroupID: group.Group.ID,
	}); err != nil {
		return false, err
	}
	if err := s.syncDeliberationContributions(ctx, deliberation.ID, roundNumber, tools.AgentTaskGroupResponse{
		Group: group.Group, Status: group.Status, Completed: group.Completed, Summary: group.Summary, Aggregate: group.Aggregate, Items: group.Items,
	}); err != nil {
		return false, err
	}
	s.recordDeliberationEvent(ctx, deliberation, "runtime.agent_deliberation_round_started", map[string]any{
		"deliberation_id": deliberation.ID, "round_number": roundNumber, "task_group_id": group.Group.ID,
	})
	return true, nil
}

func (s agentToolService) syncDeliberationContributions(ctx context.Context, deliberationID string, roundNumber int, state tools.AgentTaskGroupResponse) error {
	store, _ := s.deliberationStore()
	for _, item := range state.Items {
		contribution := managedagents.AgentDeliberationContribution{
			DeliberationID: deliberationID, RoundNumber: roundNumber, ParticipantIndex: item.Item.ItemIndex,
			TaskGroupID: state.Group.ID, ItemIndex: item.Item.ItemIndex, Status: item.Status,
			ContributionText: strings.TrimSpace(item.AgentText), ContributionJSON: item.ResultJSON, RetryCount: item.Item.RetryCount,
		}
		if item.Session != nil {
			contribution.SessionID = item.Session.ID
		}
		if _, err := managedagents.UpsertAgentDeliberationContributionWithContext(ctx, store, contribution); err != nil {
			return err
		}
	}
	return nil
}

func (s agentToolService) deliberationResponse(ctx context.Context, deliberation managedagents.AgentDeliberation) (tools.AgentDeliberationResponse, error) {
	store, _ := s.deliberationStore()
	participants, err := managedagents.ListAgentDeliberationParticipantsWithContext(ctx, store, deliberation.ID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	rounds, err := managedagents.ListAgentDeliberationRoundsWithContext(ctx, store, deliberation.ID)
	if err != nil {
		return tools.AgentDeliberationResponse{}, err
	}
	states := make([]tools.AgentDeliberationRoundState, 0, len(rounds))
	for _, round := range rounds {
		contributions, err := managedagents.ListAgentDeliberationContributionsWithContext(ctx, store, deliberation.ID, round.RoundNumber)
		if err != nil {
			return tools.AgentDeliberationResponse{}, err
		}
		states = append(states, tools.AgentDeliberationRoundState{Round: round, Contributions: contributions})
	}
	return tools.AgentDeliberationResponse{
		Deliberation: deliberation, Participants: participants, Rounds: states,
		Completed: deliberation.Status == managedagents.AgentDeliberationStatusCompleted,
	}, nil
}

func (s agentToolService) loadOwnedDeliberation(ctx context.Context, parentSessionID string, deliberationID string) (managedagents.AgentDeliberation, error) {
	store, err := s.deliberationStore()
	if err != nil {
		return managedagents.AgentDeliberation{}, err
	}
	parent, err := s.parentSession(ctx, parentSessionID)
	if err != nil {
		return managedagents.AgentDeliberation{}, err
	}
	deliberation, err := managedagents.GetAgentDeliberationWithContext(ctx, store, strings.TrimSpace(deliberationID))
	if err != nil {
		return managedagents.AgentDeliberation{}, err
	}
	if deliberation.ParentSessionID != parent.ID || deliberation.WorkspaceID != parent.WorkspaceID {
		return managedagents.AgentDeliberation{}, managedagents.ErrNotFound
	}
	return deliberation, nil
}

func (s agentToolService) deliberationStore() (managedagents.AgentDeliberationStore, error) {
	store, ok := s.store.(managedagents.AgentDeliberationStore)
	if !ok {
		return nil, errors.New("agent deliberation persistence is not supported by this store")
	}
	return store, nil
}

func (s agentToolService) recordDeliberationEvent(ctx context.Context, deliberation managedagents.AgentDeliberation, eventType string, payload map[string]any) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = managedagents.AppendRuntimeEventWithContext(ctx, s.store, deliberation.ParentSessionID, deliberation.ParentTurnID, managedagents.AppendEventInput{Type: eventType, Payload: encoded})
}

func roundOnePrompt(deliberation managedagents.AgentDeliberation, participant managedagents.AgentDeliberationParticipant) string {
	return fmt.Sprintf("You are participating in a structured multi-agent deliberation.\nObjective: %s\nStrategy: %s\nYour role: %s (%s)\nYour goal: %s\nRound 1 is independent: do not assume other participants' views. Produce a concrete position, key points, risks, questions, and confidence as JSON matching the required schema.", deliberation.Objective, deliberation.Strategy, participant.RoleTitle, participant.RoleID, participant.Goal)
}

func roundTwoPrompt(deliberation managedagents.AgentDeliberation, participant managedagents.AgentDeliberationParticipant, roundOne managedagents.AgentDeliberationRound) string {
	questions := extractQuestionsForRole(roundOne.Questions, participant.RoleID)
	return fmt.Sprintf("You are participating in round 2 of a structured multi-agent deliberation.\nObjective: %s\nStrategy: %s\nYour role: %s (%s)\nYour goal: %s\nModerator summary: %s\nQuestions assigned to you: %s\nCritique the first-round positions, answer your assigned questions, identify resolved and remaining disagreements, and return JSON matching the contribution schema.", deliberation.Objective, deliberation.Strategy, participant.RoleTitle, participant.RoleID, participant.Goal, string(roundOne.Summary), string(questions))
}

func moderationPrompt(deliberation managedagents.AgentDeliberation, contributions []managedagents.AgentDeliberationContribution) string {
	payload, _ := json.Marshal(contributions)
	return fmt.Sprintf("Act as the moderator for a multi-agent deliberation.\nObjective: %s\nStrategy: %s\nRound 1 contributions: %s\nIdentify agreements, concrete disagreements, missing evidence, and assign targeted questions keyed by role_id. Return only JSON matching the required schema.", deliberation.Objective, deliberation.Strategy, payload)
}

func finalModerationPrompt(deliberation managedagents.AgentDeliberation, roundOne managedagents.AgentDeliberationRound, contributions []managedagents.AgentDeliberationContribution) string {
	payload, _ := json.Marshal(contributions)
	return fmt.Sprintf("Act as the final moderator for a two-round multi-agent deliberation.\nObjective: %s\nStrategy: %s\nRound 1 moderation: %s\nAll contributions: %s\nProduce a decision-quality recommendation while preserving consensus, dissenting opinions, risks, followups, and confidence. Return only JSON matching the required schema.", deliberation.Objective, deliberation.Strategy, roundOne.Summary, payload)
}

func deliberationGroupResult(state tools.AgentTaskGroupResponse, requiredKeys ...string) json.RawMessage {
	if len(state.Aggregate.JSON) > 0 && json.Valid(state.Aggregate.JSON) {
		return append(json.RawMessage(nil), state.Aggregate.JSON...)
	}
	for _, item := range state.Items {
		if len(item.ResultJSON) > 0 && json.Valid(item.ResultJSON) {
			return append(json.RawMessage(nil), item.ResultJSON...)
		}
	}
	fallback := map[string]any{}
	for _, key := range requiredKeys {
		switch key {
		case "recommendation":
			fallback[key] = firstDeliberationText(state.Items)
		case "confidence":
			fallback[key] = 0
		case "questions_by_role":
			fallback[key] = map[string][]string{}
		default:
			fallback[key] = []string{}
		}
	}
	encoded, _ := json.Marshal(fallback)
	return encoded
}

func firstDeliberationText(items []tools.AgentTaskGroupItemState) string {
	for _, item := range items {
		if strings.TrimSpace(item.AgentText) != "" {
			return strings.TrimSpace(item.AgentText)
		}
	}
	return "No valid moderator result was produced."
}

func extractJSONField(raw json.RawMessage, field string, fallback json.RawMessage) json.RawMessage {
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) == nil && len(payload[field]) > 0 {
		return append(json.RawMessage(nil), payload[field]...)
	}
	return append(json.RawMessage(nil), fallback...)
}

func extractQuestionsForRole(raw json.RawMessage, roleID string) json.RawMessage {
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) == nil && len(payload[roleID]) > 0 {
		return payload[roleID]
	}
	return json.RawMessage(`[]`)
}

func deliberationStageTurnID(deliberation managedagents.AgentDeliberation, stage string) string {
	base := strings.TrimSpace(deliberation.ParentTurnID)
	if base == "" {
		base = "deliberation"
	}
	return base + ":" + deliberation.ID + ":" + stage
}
