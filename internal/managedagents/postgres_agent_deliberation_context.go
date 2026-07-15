package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) beginAgentDeliberationScopeTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, _, err := setContextDatabaseAccessScope(ctx, tx); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *PostgresStore) CreateAgentDeliberationContext(ctx context.Context, input CreateAgentDeliberationInput) (AgentDeliberation, error) {
	deliberation := input.Deliberation
	if strings.TrimSpace(deliberation.ParentSessionID) == "" || strings.TrimSpace(deliberation.Objective) == "" {
		return AgentDeliberation{}, fmt.Errorf("%w: parent_session_id and objective are required", ErrInvalid)
	}
	if len(input.Participants) < 2 || len(input.Participants) > 8 {
		return AgentDeliberation{}, fmt.Errorf("%w: deliberation participants must be between 2 and 8", ErrInvalid)
	}
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberation{}, err
	}
	defer tx.Rollback()
	parent, err := getSessionTx(ctx, tx, strings.TrimSpace(deliberation.ParentSessionID))
	if err != nil {
		return AgentDeliberation{}, err
	}
	id, err := nextSequenceID(ctx, tx, "dlib", "tma_agent_deliberation_id_seq")
	if err != nil {
		return AgentDeliberation{}, err
	}
	now := time.Now().UTC()
	deliberation.ID = id
	deliberation.WorkspaceID = parent.WorkspaceID
	deliberation.OwnerID = parent.OwnerID
	deliberation.ParentSessionID = parent.ID
	deliberation.Status = AgentDeliberationStatusRunning
	deliberation.Phase = AgentDeliberationPhaseRound1Running
	deliberation.MaxParticipants = len(input.Participants)
	deliberation.MaxRounds = 2
	deliberation.CreatedAt = now
	deliberation.UpdatedAt = now
	if len(deliberation.Plan) == 0 {
		deliberation.Plan = json.RawMessage(`{}`)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_deliberations (
			id, workspace_id, owner_id, parent_session_id, parent_turn_id, idempotency_key,
			objective, strategy, status, phase, max_participants, max_rounds, max_tokens,
			max_seconds, moderator_agent_id, moderator_environment_id, plan_json,
			final_result_json, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,2,$12,$13,$14,$15,$16,'null'::jsonb,$17,$17)
	`, deliberation.ID, deliberation.WorkspaceID, deliberation.OwnerID, deliberation.ParentSessionID,
		strings.TrimSpace(deliberation.ParentTurnID), strings.TrimSpace(deliberation.IdempotencyKey),
		strings.TrimSpace(deliberation.Objective), strings.TrimSpace(deliberation.Strategy), deliberation.Status, deliberation.Phase,
		deliberation.MaxParticipants, deliberation.MaxTokens, deliberation.MaxSeconds, strings.TrimSpace(deliberation.ModeratorAgentID),
		strings.TrimSpace(deliberation.ModeratorEnvironmentID), deliberation.Plan, now); err != nil {
		return AgentDeliberation{}, err
	}
	for index, participant := range input.Participants {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO agent_deliberation_participants (
				deliberation_id, participant_index, role_id, role_title, goal, agent_id, environment_id, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, deliberation.ID, index, strings.TrimSpace(participant.RoleID), strings.TrimSpace(participant.RoleTitle),
			strings.TrimSpace(participant.Goal), strings.TrimSpace(participant.AgentID), strings.TrimSpace(participant.EnvironmentID), now); err != nil {
			return AgentDeliberation{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentDeliberation{}, err
	}
	return deliberation, nil
}

func (s *PostgresStore) GetAgentDeliberationContext(ctx context.Context, id string) (AgentDeliberation, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberation{}, err
	}
	defer tx.Rollback()
	deliberation, err := scanAgentDeliberation(tx.QueryRowContext(ctx, agentDeliberationSelect+` WHERE id = $1`, strings.TrimSpace(id)))
	if err == sql.ErrNoRows {
		return AgentDeliberation{}, ErrNotFound
	}
	return deliberation, err
}

func (s *PostgresStore) GetAgentDeliberationByIdempotencyContext(ctx context.Context, parentSessionID string, idempotencyKey string) (AgentDeliberation, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberation{}, err
	}
	defer tx.Rollback()
	deliberation, err := scanAgentDeliberation(tx.QueryRowContext(ctx, agentDeliberationSelect+` WHERE parent_session_id = $1 AND idempotency_key = $2`, strings.TrimSpace(parentSessionID), strings.TrimSpace(idempotencyKey)))
	if err == sql.ErrNoRows {
		return AgentDeliberation{}, ErrNotFound
	}
	return deliberation, err
}

func (s *PostgresStore) ListAgentDeliberationsByParentSessionContext(ctx context.Context, parentSessionID string) ([]AgentDeliberation, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, agentDeliberationSelect+` WHERE parent_session_id = $1 ORDER BY created_at DESC, id DESC`, strings.TrimSpace(parentSessionID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentDeliberation, 0)
	for rows.Next() {
		item, err := scanAgentDeliberation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpdateAgentDeliberationContext(ctx context.Context, id string, input UpdateAgentDeliberationInput) (AgentDeliberation, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberation{}, err
	}
	defer tx.Rollback()
	deliberation, err := scanAgentDeliberation(tx.QueryRowContext(ctx, `
		UPDATE agent_deliberations SET status=$2, phase=$3, final_group_id=NULLIF($4,''),
			final_result_json=$5, cancel_reason=$6,
			canceled_at=CASE WHEN $2='canceled' THEN COALESCE(canceled_at, now()) ELSE canceled_at END,
			updated_at=now()
		WHERE id=$1
		RETURNING id, workspace_id, owner_id, parent_session_id, parent_turn_id, idempotency_key,
			objective, strategy, status, phase, max_participants, max_rounds, max_tokens, max_seconds,
			moderator_agent_id, moderator_environment_id, plan_json, COALESCE(final_group_id,''),
			final_result_json, created_at, updated_at, canceled_at, cancel_reason
	`, strings.TrimSpace(id), input.Status, input.Phase, strings.TrimSpace(input.FinalGroupID), nullableRaw(input.FinalResult), strings.TrimSpace(input.CancelReason)))
	if err == sql.ErrNoRows {
		return AgentDeliberation{}, ErrNotFound
	}
	if err != nil {
		return AgentDeliberation{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentDeliberation{}, err
	}
	return deliberation, nil
}

func (s *PostgresStore) ListAgentDeliberationParticipantsContext(ctx context.Context, deliberationID string) ([]AgentDeliberationParticipant, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT deliberation_id, participant_index, role_id, role_title, goal, agent_id, environment_id, created_at
		FROM agent_deliberation_participants WHERE deliberation_id=$1 ORDER BY participant_index
	`, strings.TrimSpace(deliberationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentDeliberationParticipant, 0)
	for rows.Next() {
		var item AgentDeliberationParticipant
		if err := rows.Scan(&item.DeliberationID, &item.ParticipantIndex, &item.RoleID, &item.RoleTitle, &item.Goal, &item.AgentID, &item.EnvironmentID, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CreateAgentDeliberationRoundContext(ctx context.Context, round AgentDeliberationRound) (AgentDeliberationRound, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberationRound{}, err
	}
	defer tx.Rollback()
	created, err := scanAgentDeliberationRound(tx.QueryRowContext(ctx, `
		INSERT INTO agent_deliberation_rounds (deliberation_id, round_number, round_type, status, task_group_id, created_at)
		VALUES ($1,$2,$3,$4,$5,now())
		RETURNING deliberation_id, round_number, round_type, status, task_group_id, COALESCE(moderator_group_id,''), summary_json, questions_json, created_at, completed_at
	`, strings.TrimSpace(round.DeliberationID), round.RoundNumber, strings.TrimSpace(round.RoundType), strings.TrimSpace(round.Status), strings.TrimSpace(round.TaskGroupID)))
	if err != nil {
		return AgentDeliberationRound{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentDeliberationRound{}, err
	}
	return created, nil
}

func (s *PostgresStore) GetAgentDeliberationRoundContext(ctx context.Context, deliberationID string, roundNumber int) (AgentDeliberationRound, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberationRound{}, err
	}
	defer tx.Rollback()
	round, err := scanAgentDeliberationRound(tx.QueryRowContext(ctx, agentDeliberationRoundSelect+` WHERE deliberation_id=$1 AND round_number=$2`, strings.TrimSpace(deliberationID), roundNumber))
	if err == sql.ErrNoRows {
		return AgentDeliberationRound{}, ErrNotFound
	}
	return round, err
}

func (s *PostgresStore) ListAgentDeliberationRoundsContext(ctx context.Context, deliberationID string) ([]AgentDeliberationRound, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, agentDeliberationRoundSelect+` WHERE deliberation_id=$1 ORDER BY round_number`, strings.TrimSpace(deliberationID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentDeliberationRound, 0)
	for rows.Next() {
		item, err := scanAgentDeliberationRound(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpdateAgentDeliberationRoundContext(ctx context.Context, deliberationID string, roundNumber int, input UpdateAgentDeliberationRoundInput) (AgentDeliberationRound, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberationRound{}, err
	}
	defer tx.Rollback()
	round, err := scanAgentDeliberationRound(tx.QueryRowContext(ctx, `
		UPDATE agent_deliberation_rounds SET status=$3, moderator_group_id=NULLIF($4,''), summary_json=$5,
			questions_json=$6, completed_at=CASE WHEN $7 THEN COALESCE(completed_at,now()) ELSE completed_at END
		WHERE deliberation_id=$1 AND round_number=$2
		RETURNING deliberation_id, round_number, round_type, status, task_group_id, COALESCE(moderator_group_id,''), summary_json, questions_json, created_at, completed_at
	`, strings.TrimSpace(deliberationID), roundNumber, strings.TrimSpace(input.Status), strings.TrimSpace(input.ModeratorGroupID), nullableRaw(input.Summary), metadataJSON(input.Questions), input.Complete))
	if err == sql.ErrNoRows {
		return AgentDeliberationRound{}, ErrNotFound
	}
	if err != nil {
		return AgentDeliberationRound{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentDeliberationRound{}, err
	}
	return round, nil
}

func (s *PostgresStore) UpsertAgentDeliberationContributionContext(ctx context.Context, contribution AgentDeliberationContribution) (AgentDeliberationContribution, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return AgentDeliberationContribution{}, err
	}
	defer tx.Rollback()
	item, err := scanAgentDeliberationContribution(tx.QueryRowContext(ctx, `
		INSERT INTO agent_deliberation_contributions (
			deliberation_id, round_number, participant_index, task_group_id, item_index, session_id,
			status, contribution_text, contribution_json, retry_count, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8,$9,$10,now(),now())
		ON CONFLICT (deliberation_id, round_number, participant_index) DO UPDATE SET
			task_group_id=EXCLUDED.task_group_id, item_index=EXCLUDED.item_index, session_id=EXCLUDED.session_id,
			status=EXCLUDED.status, contribution_text=EXCLUDED.contribution_text,
			contribution_json=EXCLUDED.contribution_json, retry_count=EXCLUDED.retry_count, updated_at=now()
		RETURNING deliberation_id, round_number, participant_index, task_group_id, item_index,
			COALESCE(session_id,''), status, contribution_text, contribution_json, retry_count, created_at, updated_at
	`, strings.TrimSpace(contribution.DeliberationID), contribution.RoundNumber, contribution.ParticipantIndex,
		strings.TrimSpace(contribution.TaskGroupID), contribution.ItemIndex, strings.TrimSpace(contribution.SessionID),
		strings.TrimSpace(contribution.Status), strings.TrimSpace(contribution.ContributionText), nullableRaw(contribution.ContributionJSON), contribution.RetryCount))
	if err != nil {
		return AgentDeliberationContribution{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentDeliberationContribution{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListAgentDeliberationContributionsContext(ctx context.Context, deliberationID string, roundNumber int) ([]AgentDeliberationContribution, error) {
	tx, err := s.beginAgentDeliberationScopeTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT deliberation_id, round_number, participant_index, task_group_id, item_index,
			COALESCE(session_id,''), status, contribution_text, contribution_json, retry_count, created_at, updated_at
		FROM agent_deliberation_contributions
		WHERE deliberation_id=$1 AND ($2=0 OR round_number=$2)
		ORDER BY round_number, participant_index
	`, strings.TrimSpace(deliberationID), roundNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AgentDeliberationContribution, 0)
	for rows.Next() {
		item, err := scanAgentDeliberationContribution(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
