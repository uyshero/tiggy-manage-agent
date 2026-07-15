package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

func securityAuditPayloadWorkspaceID(payload json.RawMessage) (string, error) {
	if len(payload) == 0 || !json.Valid(payload) {
		return "", fmt.Errorf("%w: security audit payload must be valid JSON", ErrInvalid)
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return "", fmt.Errorf("%w: security audit payload must be a JSON object", ErrInvalid)
	}
	workspaceID, _ := object["workspace_id"].(string)
	return strings.TrimSpace(workspaceID), nil
}

func securityAuditContextWorkspaceID(ctx context.Context, requestedWorkspaceID string) (string, error) {
	requestedWorkspaceID = strings.TrimSpace(requestedWorkspaceID)
	if scope, ok := DatabaseAccessScopeFromContext(ctx); ok {
		if requestedWorkspaceID != "" && requestedWorkspaceID != scope.WorkspaceID {
			return "", fmt.Errorf("%w: security audit workspace scope mismatch", ErrForbidden)
		}
		return scope.WorkspaceID, nil
	}
	if requestedWorkspaceID == "" {
		requestedWorkspaceID = DefaultWorkspaceID
	}
	scope, err := ValidateAccessScope(AccessScope{WorkspaceID: requestedWorkspaceID})
	if err != nil {
		return "", err
	}
	return scope.WorkspaceID, nil
}

func (s *PostgresStore) beginSecurityAuditScopeTx(ctx context.Context, workspaceID string, global bool) (*sql.Tx, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if global {
		if _, ok := DatabaseAccessScopeFromContext(ctx); ok {
			return nil, fmt.Errorf("%w: tenant context cannot access global security audit events", ErrForbidden)
		}
		workspaceID = ""
	} else {
		var err error
		workspaceID, err = securityAuditContextWorkspaceID(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	globalSetting := "off"
	if global {
		globalSetting = "on"
	}
	if _, err := tx.ExecContext(ctx, `
		SELECT
			set_config('tma.workspace_id', $1, true),
			set_config('tma.owner_id', '', true),
			set_config('tma.security_audit_global', $2, true)
	`, workspaceID, globalSetting); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *PostgresStore) securityAuditScopes(ctx context.Context) ([]string, error) {
	workspaceIDs, err := s.listTenantWorkspaceIDs(ctx, "")
	if err != nil {
		return nil, err
	}
	return append([]string{""}, workspaceIDs...), nil
}

func (s *PostgresStore) rotatedSecurityAuditScopes(ctx context.Context) ([]string, error) {
	scopes, err := s.securityAuditScopes(ctx)
	if err != nil || len(scopes) == 0 {
		return scopes, err
	}
	s.auditClaimMu.Lock()
	start := s.auditClaimCursor % len(scopes)
	s.auditClaimCursor = (start + 1) % len(scopes)
	s.auditClaimMu.Unlock()
	return append(append([]string{}, scopes[start:]...), scopes[:start]...), nil
}

func (s *PostgresStore) recordSecurityAuditOutboxInScope(ctx context.Context, workspaceID string, global bool, input RecordSecurityAuditOutboxInput) (SecurityAuditOutboxEvent, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.IntegrityAlgorithm = strings.TrimSpace(input.IntegrityAlgorithm)
	input.IntegrityKeyID = strings.TrimSpace(input.IntegrityKeyID)
	input.IntegrityDigest = strings.TrimSpace(input.IntegrityDigest)
	if input.ID == "" || input.IntegrityAlgorithm == "" || input.IntegrityDigest == "" {
		return SecurityAuditOutboxEvent{}, fmt.Errorf("%w: security audit id and integrity metadata are required", ErrInvalid)
	}
	payloadWorkspaceID, err := securityAuditPayloadWorkspaceID(input.Payload)
	if err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	if !global && payloadWorkspaceID != "" && payloadWorkspaceID != workspaceID {
		return SecurityAuditOutboxEvent{}, fmt.Errorf("%w: security audit payload workspace mismatch", ErrForbidden)
	}
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	tx, err := s.beginSecurityAuditScopeTx(ctx, workspaceID, global)
	if err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	defer tx.Rollback()
	event, err := scanSecurityAuditOutboxEvent(tx.QueryRowContext(ctx, `
		INSERT INTO security_audit_outbox (
			id, workspace_id, payload_json, integrity_algorithm, integrity_key_id, integrity_digest,
			status, attempt_count, next_attempt_at, created_at, updated_at
		) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, 'pending', 0, $7, $7, $7)
		RETURNING `+securityAuditOutboxColumns,
		input.ID, workspaceID, string(input.Payload), input.IntegrityAlgorithm,
		input.IntegrityKeyID, input.IntegrityDigest, createdAt,
	))
	if err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	return event, nil
}

func (s *PostgresStore) RecordSecurityAuditOutboxContext(ctx context.Context, input RecordSecurityAuditOutboxInput) (SecurityAuditOutboxEvent, error) {
	payloadWorkspaceID, err := securityAuditPayloadWorkspaceID(input.Payload)
	if err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	requestedWorkspaceID := strings.TrimSpace(input.WorkspaceID)
	if requestedWorkspaceID == "" {
		requestedWorkspaceID = payloadWorkspaceID
	}
	workspaceID, err := securityAuditContextWorkspaceID(ctx, requestedWorkspaceID)
	if err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	input.WorkspaceID = workspaceID
	return s.recordSecurityAuditOutboxInScope(ctx, workspaceID, false, input)
}

func (s *PostgresStore) claimSecurityAuditOutboxInScope(ctx context.Context, workspaceID string, global bool, input ClaimSecurityAuditOutboxInput) ([]SecurityAuditOutboxEvent, error) {
	input.LeaseOwner = strings.TrimSpace(input.LeaseOwner)
	if input.LeaseOwner == "" || input.LeaseDuration <= 0 || input.MaxAttempts < 1 || input.Limit < 1 {
		return nil, fmt.Errorf("%w: valid security audit lease owner, duration, max attempts, and limit are required", ErrInvalid)
	}
	if input.Limit > 1000 {
		input.Limit = 1000
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.beginSecurityAuditScopeTx(ctx, workspaceID, global)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE security_audit_outbox
		SET status = 'dead_letter', lease_owner = '', lease_expires_at = NULL,
			last_error = CASE WHEN last_error = '' THEN 'delivery lease expired after maximum attempts' ELSE last_error END,
			updated_at = $1
		WHERE status = 'delivering' AND lease_expires_at <= $1 AND attempt_count >= $2
	`, now, input.MaxAttempts); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id FROM security_audit_outbox
			WHERE attempt_count < $3
				AND ((status = 'pending' AND next_attempt_at <= $1)
					OR (status = 'delivering' AND lease_expires_at <= $1))
			ORDER BY next_attempt_at, created_at, id
			FOR UPDATE SKIP LOCKED LIMIT $4
		)
		UPDATE security_audit_outbox AS outbox
		SET status = 'delivering', attempt_count = outbox.attempt_count + 1,
			lease_owner = $2, lease_expires_at = $5, last_error = '', updated_at = $1
		FROM candidates WHERE outbox.id = candidates.id
		RETURNING `+securityAuditOutboxReturningColumns,
		now, input.LeaseOwner, input.MaxAttempts, input.Limit, now.Add(input.LeaseDuration),
	)
	if err != nil {
		return nil, err
	}
	events := make([]SecurityAuditOutboxEvent, 0, input.Limit)
	for rows.Next() {
		event, err := scanSecurityAuditOutboxEvent(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *PostgresStore) ClaimSecurityAuditOutboxContext(ctx context.Context, input ClaimSecurityAuditOutboxInput) ([]SecurityAuditOutboxEvent, error) {
	workspaceID, err := securityAuditContextWorkspaceID(ctx, "")
	if err != nil {
		return nil, err
	}
	return s.claimSecurityAuditOutboxInScope(ctx, workspaceID, false, input)
}

func (s *PostgresStore) updateSecurityAuditOutboxBatchInScope(ctx context.Context, workspaceID string, global bool, ids []string, update func(context.Context, *sql.Tx, string) (sql.Result, error)) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.beginSecurityAuditScopeTx(ctx, workspaceID, global)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	updated := 0
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		result, err := update(ctx, tx, id)
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		updated += int(count)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func (s *PostgresStore) completeSecurityAuditOutboxInScope(ctx context.Context, workspaceID string, global bool, input CompleteSecurityAuditOutboxInput) (int, error) {
	at := input.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return s.updateSecurityAuditOutboxBatchInScope(ctx, workspaceID, global, input.IDs, func(ctx context.Context, tx *sql.Tx, id string) (sql.Result, error) {
		return tx.ExecContext(ctx, `
			UPDATE security_audit_outbox
			SET status = 'delivered', lease_owner = '', lease_expires_at = NULL,
				delivered_at = $3, updated_at = $3, last_error = ''
			WHERE id = $1 AND status = 'delivering' AND lease_owner = $2
		`, id, strings.TrimSpace(input.LeaseOwner), at)
	})
}

func (s *PostgresStore) CompleteSecurityAuditOutboxContext(ctx context.Context, input CompleteSecurityAuditOutboxInput) (int, error) {
	workspaceID, err := securityAuditContextWorkspaceID(ctx, "")
	if err != nil {
		return 0, err
	}
	return s.completeSecurityAuditOutboxInScope(ctx, workspaceID, false, input)
}

func (s *PostgresStore) failSecurityAuditOutboxInScope(ctx context.Context, workspaceID string, global bool, input FailSecurityAuditOutboxInput) (int, error) {
	at := input.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	nextAttemptAt := input.NextAttemptAt.UTC()
	if nextAttemptAt.IsZero() {
		nextAttemptAt = at
	}
	status := SecurityAuditOutboxPending
	if input.DeadLetter {
		status = SecurityAuditOutboxDeadLetter
	}
	errorMessage := strings.TrimSpace(input.ErrorMessage)
	if len(errorMessage) > 4096 {
		errorMessage = errorMessage[:4096]
	}
	return s.updateSecurityAuditOutboxBatchInScope(ctx, workspaceID, global, input.IDs, func(ctx context.Context, tx *sql.Tx, id string) (sql.Result, error) {
		return tx.ExecContext(ctx, `
			UPDATE security_audit_outbox
			SET status = $3, lease_owner = '', lease_expires_at = NULL,
				next_attempt_at = $4, last_error = $5, updated_at = $6
			WHERE id = $1 AND status = 'delivering' AND lease_owner = $2
		`, id, strings.TrimSpace(input.LeaseOwner), status, nextAttemptAt, errorMessage, at)
	})
}

func (s *PostgresStore) FailSecurityAuditOutboxContext(ctx context.Context, input FailSecurityAuditOutboxInput) (int, error) {
	workspaceID, err := securityAuditContextWorkspaceID(ctx, "")
	if err != nil {
		return 0, err
	}
	return s.failSecurityAuditOutboxInScope(ctx, workspaceID, false, input)
}

func (s *PostgresStore) replaySecurityAuditDeadLettersInScope(ctx context.Context, workspaceID string, global bool, input ReplaySecurityAuditDeadLettersInput) (int, error) {
	limit := input.Limit
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	before := input.Before.UTC()
	if before.IsZero() {
		before = time.Now().UTC()
	}
	tx, err := s.beginSecurityAuditScopeTx(ctx, workspaceID, global)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		WITH candidates AS (
			SELECT id FROM security_audit_outbox
			WHERE status = 'dead_letter' AND updated_at <= $1
			ORDER BY updated_at, id LIMIT $2 FOR UPDATE SKIP LOCKED
		)
		UPDATE security_audit_outbox AS outbox
		SET status = 'pending', attempt_count = 0, next_attempt_at = now(),
			lease_owner = '', lease_expires_at = NULL, last_error = '', updated_at = now()
		FROM candidates WHERE outbox.id = candidates.id
	`, before, limit)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *PostgresStore) ReplaySecurityAuditDeadLettersContext(ctx context.Context, input ReplaySecurityAuditDeadLettersInput) (int, error) {
	workspaceID, err := securityAuditContextWorkspaceID(ctx, "")
	if err != nil {
		return 0, err
	}
	return s.replaySecurityAuditDeadLettersInScope(ctx, workspaceID, false, input)
}

func (s *PostgresStore) getSecurityAuditOutboxStatsInScope(ctx context.Context, workspaceID string, global bool, now time.Time) (SecurityAuditOutboxStats, error) {
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.beginSecurityAuditScopeTx(ctx, workspaceID, global)
	if err != nil {
		return SecurityAuditOutboxStats{}, err
	}
	defer tx.Rollback()
	var stats SecurityAuditOutboxStats
	var oldest sql.NullTime
	var schemaProbe int64
	err = tx.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'delivering'),
			COUNT(*) FILTER (WHERE status = 'delivered'),
			COUNT(*) FILTER (WHERE status = 'dead_letter'),
			MIN(created_at) FILTER (WHERE status IN ('pending', 'delivering')),
			COUNT(integrity_key_id)
		FROM security_audit_outbox
	`).Scan(&stats.Pending, &stats.Delivering, &stats.Delivered, &stats.DeadLetter, &oldest, &schemaProbe)
	if err != nil {
		return SecurityAuditOutboxStats{}, err
	}
	if oldest.Valid {
		oldestAt := oldest.Time.UTC()
		stats.OldestPendingAt = &oldestAt
		if now.After(oldestAt) {
			stats.OldestPendingSeconds = int64(now.Sub(oldestAt) / time.Second)
		}
	}
	if err := tx.Commit(); err != nil {
		return SecurityAuditOutboxStats{}, err
	}
	return stats, nil
}

func (s *PostgresStore) GetSecurityAuditOutboxStatsContext(ctx context.Context, now time.Time) (SecurityAuditOutboxStats, error) {
	workspaceID, err := securityAuditContextWorkspaceID(ctx, "")
	if err != nil {
		return SecurityAuditOutboxStats{}, err
	}
	return s.getSecurityAuditOutboxStatsInScope(ctx, workspaceID, false, now)
}

func (s *PostgresStore) listSecurityAuditIntegrityKeyStatsInScope(ctx context.Context, workspaceID string, global bool) ([]SecurityAuditIntegrityKeyStats, error) {
	tx, err := s.beginSecurityAuditScopeTx(ctx, workspaceID, global)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT integrity_key_id,
			COUNT(*) FILTER (WHERE status = 'pending'),
			COUNT(*) FILTER (WHERE status = 'delivering'),
			COUNT(*) FILTER (WHERE status = 'delivered'),
			COUNT(*) FILTER (WHERE status = 'dead_letter')
		FROM security_audit_outbox
		WHERE integrity_algorithm = 'hmac-sha256'
		GROUP BY integrity_key_id ORDER BY integrity_key_id
	`)
	if err != nil {
		return nil, err
	}
	stats := []SecurityAuditIntegrityKeyStats{}
	for rows.Next() {
		var item SecurityAuditIntegrityKeyStats
		if err := rows.Scan(&item.KeyID, &item.Pending, &item.Delivering, &item.Delivered, &item.DeadLetter); err != nil {
			rows.Close()
			return nil, err
		}
		stats = append(stats, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return stats, nil
}

func (s *PostgresStore) ListSecurityAuditIntegrityKeyStatsContext(ctx context.Context) ([]SecurityAuditIntegrityKeyStats, error) {
	workspaceID, err := securityAuditContextWorkspaceID(ctx, "")
	if err != nil {
		return nil, err
	}
	return s.listSecurityAuditIntegrityKeyStatsInScope(ctx, workspaceID, false)
}

func (s *PostgresStore) pruneDeliveredSecurityAuditOutboxInScope(ctx context.Context, workspaceID string, global bool, before time.Time, limit int) (int, error) {
	before = before.UTC()
	if before.IsZero() {
		return 0, fmt.Errorf("%w: security audit prune cutoff is required", ErrInvalid)
	}
	if limit < 1 || limit > 10000 {
		limit = 1000
	}
	tx, err := s.beginSecurityAuditScopeTx(ctx, workspaceID, global)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		WITH candidates AS (
			SELECT id FROM security_audit_outbox
			WHERE status = 'delivered' AND delivered_at < $1
			ORDER BY delivered_at, id LIMIT $2
		)
		DELETE FROM security_audit_outbox AS outbox
		USING candidates WHERE outbox.id = candidates.id
	`, before, limit)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(count), nil
}

func (s *PostgresStore) PruneDeliveredSecurityAuditOutboxContext(ctx context.Context, before time.Time, limit int) (int, error) {
	workspaceID, err := securityAuditContextWorkspaceID(ctx, "")
	if err != nil {
		return 0, err
	}
	return s.pruneDeliveredSecurityAuditOutboxInScope(ctx, workspaceID, false, before, limit)
}

func mergeSecurityAuditStats(total *SecurityAuditOutboxStats, item SecurityAuditOutboxStats, now time.Time) {
	total.Pending += item.Pending
	total.Delivering += item.Delivering
	total.Delivered += item.Delivered
	total.DeadLetter += item.DeadLetter
	if item.OldestPendingAt != nil && (total.OldestPendingAt == nil || item.OldestPendingAt.Before(*total.OldestPendingAt)) {
		oldest := *item.OldestPendingAt
		total.OldestPendingAt = &oldest
	}
	if total.OldestPendingAt != nil && now.After(*total.OldestPendingAt) {
		total.OldestPendingSeconds = int64(now.Sub(*total.OldestPendingAt) / time.Second)
	}
}

func mergeSecurityAuditKeyStats(items []SecurityAuditIntegrityKeyStats) []SecurityAuditIntegrityKeyStats {
	byKey := make(map[string]SecurityAuditIntegrityKeyStats)
	for _, item := range items {
		total := byKey[item.KeyID]
		total.KeyID = item.KeyID
		total.Pending += item.Pending
		total.Delivering += item.Delivering
		total.Delivered += item.Delivered
		total.DeadLetter += item.DeadLetter
		byKey[item.KeyID] = total
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]SecurityAuditIntegrityKeyStats, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func securityAuditScopeContext(ctx context.Context, workspaceID string) (context.Context, bool, error) {
	if workspaceID == "" {
		return context.Background(), true, nil
	}
	workspaceCtx, err := ContextWithDatabaseAccessScope(ctx, AccessScope{WorkspaceID: workspaceID})
	return workspaceCtx, false, err
}

var _ SecurityAuditOutboxContextStore = (*PostgresStore)(nil)
