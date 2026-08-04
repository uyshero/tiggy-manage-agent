package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *PostgresStore) RecordModelInvocationContext(ctx context.Context, input RecordModelInvocationInput) (ModelInvocation, error) {
	input, err := NormalizeRecordModelInvocationInput(input)
	if err != nil {
		return ModelInvocation{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ModelInvocation{}, err
	}
	defer tx.Rollback()
	id, err := nextSequenceID(ctx, tx, "minv", "tma_model_invocation_id_seq")
	if err != nil {
		return ModelInvocation{}, err
	}
	row := tx.QueryRowContext(ctx, `
		INSERT INTO model_invocations (
			id, workspace_id, principal_id, service_identity_id, auth_type, request_id, capability,
			provider_id, provider_type, model, status, error_code,
			input_tokens, output_tokens, total_tokens, cached_input_tokens, reasoning_tokens,
				input_items, output_items, input_bytes, output_bytes, input_characters, output_characters,
				input_audio_ms, output_audio_ms, input_video_frames, output_video_frames,
				input_video_dropped, output_video_dropped, input_video_ms, output_video_ms,
				latency_ms, started_at, completed_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
				$13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26,
				$27, $28, $29, $30, $31, $32, $33, $34
			)
		RETURNING id, workspace_id, principal_id, service_identity_id, auth_type, request_id, capability,
			provider_id, provider_type, model, status, error_code,
			input_tokens, output_tokens, total_tokens, cached_input_tokens, reasoning_tokens,
			input_items, output_items, input_bytes, output_bytes, input_characters, output_characters,
				input_audio_ms, output_audio_ms, input_video_frames, output_video_frames,
				input_video_dropped, output_video_dropped, input_video_ms, output_video_ms,
				latency_ms, started_at, completed_at
	`, id, input.WorkspaceID, input.PrincipalID, input.ServiceIdentityID, input.AuthType, input.RequestID, input.Capability,
		input.ProviderID, input.ProviderType, input.Model, input.Status, input.ErrorCode,
		input.InputTokens, input.OutputTokens, input.TotalTokens, input.CachedInputTokens, input.ReasoningTokens,
		input.InputItems, input.OutputItems, input.InputBytes, input.OutputBytes, input.InputCharacters, input.OutputCharacters,
		input.InputAudioMillis, input.OutputAudioMillis, input.InputVideoFrames, input.OutputVideoFrames,
		input.InputVideoDropped, input.OutputVideoDropped, input.InputVideoMillis, input.OutputVideoMillis,
		input.LatencyMillis, input.StartedAt, input.CompletedAt)
	record, err := scanModelInvocation(row)
	if err != nil {
		return ModelInvocation{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelInvocation{}, err
	}
	return record, nil
}

func (s *PostgresStore) ListModelInvocationsContext(ctx context.Context, input ListModelInvocationsInput) (ModelInvocationReport, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	if input.WorkspaceID == "" {
		return ModelInvocationReport{}, fmt.Errorf("%w: model invocation workspace_id is required", ErrInvalid)
	}
	if input.Limit == 0 {
		input.Limit = 100
	}
	if input.Limit < 1 || input.Limit > 500 {
		return ModelInvocationReport{}, fmt.Errorf("%w: model invocation limit must be between 1 and 500", ErrInvalid)
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ModelInvocationReport{}, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, workspace_id, principal_id, service_identity_id, auth_type, request_id, capability,
			provider_id, provider_type, model, status, error_code,
			input_tokens, output_tokens, total_tokens, cached_input_tokens, reasoning_tokens,
			input_items, output_items, input_bytes, output_bytes, input_characters, output_characters,
				input_audio_ms, output_audio_ms, input_video_frames, output_video_frames,
				input_video_dropped, output_video_dropped, input_video_ms, output_video_ms,
				latency_ms, started_at, completed_at
		FROM model_invocations
		WHERE workspace_id = $1
			AND ($2 = '' OR principal_id = $2)
			AND ($3 = '' OR service_identity_id = $3)
			AND ($4 = '' OR capability = $4)
			AND ($5 = '' OR provider_id = $5)
			AND ($6 = '' OR model = $6)
			AND ($7 = '' OR status = $7)
			AND ($8::timestamptz IS NULL OR started_at >= $8)
			AND ($9::timestamptz IS NULL OR started_at < $9)
		ORDER BY started_at DESC, id DESC
		LIMIT $10
	`, input.WorkspaceID, strings.TrimSpace(input.PrincipalID), strings.TrimSpace(input.ServiceIdentityID), strings.TrimSpace(input.Capability),
		strings.TrimSpace(input.ProviderID), strings.TrimSpace(input.Model), strings.TrimSpace(input.Status),
		input.From, input.To, input.Limit)
	if err != nil {
		return ModelInvocationReport{}, err
	}
	defer rows.Close()
	report := ModelInvocationReport{Records: []ModelInvocation{}}
	for rows.Next() {
		record, err := scanModelInvocation(rows)
		if err != nil {
			return ModelInvocationReport{}, err
		}
		report.Records = append(report.Records, record)
		AddModelInvocationSummary(&report.Summary, record)
	}
	if err := rows.Err(); err != nil {
		return ModelInvocationReport{}, err
	}
	if err := tx.Commit(); err != nil {
		return ModelInvocationReport{}, err
	}
	return report, nil
}

func (s *PostgresStore) ReserveModelInvocationQuotaContext(ctx context.Context, input ReserveModelInvocationQuotaInput) (ModelInvocationQuotaReservation, error) {
	input, err := NormalizeReserveModelInvocationQuotaInput(input)
	if err != nil {
		return ModelInvocationQuotaReservation{}, err
	}
	tx, _, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return ModelInvocationQuotaReservation{}, err
	}
	defer tx.Rollback()

	current, allowed, err := reserveModelInvocationQuotaBucket(ctx, tx, input, "workspace", "", input.WorkspaceLimit)
	if err != nil {
		return ModelInvocationQuotaReservation{}, err
	}
	if !allowed {
		return ModelInvocationQuotaReservation{Allowed: false, ExceededScope: "workspace", Limit: input.WorkspaceLimit, Current: current}, nil
	}
	actorID := input.ServiceIdentityID
	if actorID == "" {
		actorID = input.PrincipalID
	}
	current, allowed, err = reserveModelInvocationQuotaBucket(ctx, tx, input, "identity", actorID, input.IdentityLimit)
	if err != nil {
		return ModelInvocationQuotaReservation{}, err
	}
	if !allowed {
		return ModelInvocationQuotaReservation{Allowed: false, ExceededScope: "identity", Limit: input.IdentityLimit, Current: current}, nil
	}
	if err := tx.Commit(); err != nil {
		return ModelInvocationQuotaReservation{}, err
	}
	return ModelInvocationQuotaReservation{Allowed: true}, nil
}

func reserveModelInvocationQuotaBucket(ctx context.Context, tx *sql.Tx, input ReserveModelInvocationQuotaInput, scope, actorID string, limit int) (int, bool, error) {
	if limit == 0 {
		return 0, true, nil
	}
	var current int
	err := tx.QueryRowContext(ctx, `
		INSERT INTO model_invocation_quota_buckets (
			workspace_id, quota_scope, actor_id, capability, provider_id, model,
			window_started_at, request_count, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 1, now())
		ON CONFLICT (workspace_id, quota_scope, actor_id, capability, provider_id, model)
		DO UPDATE SET
			window_started_at = EXCLUDED.window_started_at,
			request_count = CASE
				WHEN model_invocation_quota_buckets.window_started_at < EXCLUDED.window_started_at THEN 1
				ELSE model_invocation_quota_buckets.request_count + 1
			END,
			updated_at = now()
		WHERE model_invocation_quota_buckets.window_started_at < EXCLUDED.window_started_at
			OR model_invocation_quota_buckets.request_count < $8
		RETURNING request_count
	`, input.WorkspaceID, scope, actorID, input.Capability, input.ProviderID, input.Model, input.WindowStartedAt, limit).Scan(&current)
	if err == sql.ErrNoRows {
		return limit, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return current, true, nil
}

type modelInvocationScanner interface {
	Scan(...any) error
}

func scanModelInvocation(scanner modelInvocationScanner) (ModelInvocation, error) {
	var record ModelInvocation
	if err := scanner.Scan(
		&record.ID, &record.WorkspaceID, &record.PrincipalID, &record.ServiceIdentityID, &record.AuthType, &record.RequestID, &record.Capability,
		&record.ProviderID, &record.ProviderType, &record.Model, &record.Status, &record.ErrorCode,
		&record.InputTokens, &record.OutputTokens, &record.TotalTokens, &record.CachedInputTokens, &record.ReasoningTokens,
		&record.InputItems, &record.OutputItems, &record.InputBytes, &record.OutputBytes, &record.InputCharacters, &record.OutputCharacters,
		&record.InputAudioMillis, &record.OutputAudioMillis, &record.InputVideoFrames, &record.OutputVideoFrames,
		&record.InputVideoDropped, &record.OutputVideoDropped, &record.InputVideoMillis, &record.OutputVideoMillis,
		&record.LatencyMillis, &record.StartedAt, &record.CompletedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return ModelInvocation{}, ErrNotFound
		}
		return ModelInvocation{}, err
	}
	return record, nil
}

var _ ModelInvocationStore = (*PostgresStore)(nil)
var _ ModelInvocationQuotaStore = (*PostgresStore)(nil)
