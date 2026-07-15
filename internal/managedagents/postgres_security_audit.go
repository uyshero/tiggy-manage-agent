package managedagents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const securityAuditOutboxColumns = `
	id, COALESCE(workspace_id, ''), payload_json, integrity_algorithm, integrity_key_id, integrity_digest,
	status, attempt_count, next_attempt_at, lease_owner, lease_expires_at, last_error,
	created_at, updated_at, delivered_at
`

const securityAuditOutboxReturningColumns = `
	outbox.id, COALESCE(outbox.workspace_id, ''), outbox.payload_json, outbox.integrity_algorithm,
	outbox.integrity_key_id, outbox.integrity_digest, outbox.status, outbox.attempt_count,
	outbox.next_attempt_at, outbox.lease_owner, outbox.lease_expires_at, outbox.last_error,
	outbox.created_at, outbox.updated_at, outbox.delivered_at
`

type securityAuditOutboxScanner interface {
	Scan(dest ...any) error
}

func (s *PostgresStore) RecordSecurityAuditOutbox(input RecordSecurityAuditOutboxInput) (SecurityAuditOutboxEvent, error) {
	payloadWorkspaceID, err := securityAuditPayloadWorkspaceID(input.Payload)
	if err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	workspaceID := strings.TrimSpace(input.WorkspaceID)
	if workspaceID != "" && payloadWorkspaceID != "" && workspaceID != payloadWorkspaceID {
		return SecurityAuditOutboxEvent{}, fmt.Errorf("%w: security audit payload workspace mismatch", ErrForbidden)
	}
	if workspaceID == "" {
		workspaceID = payloadWorkspaceID
	}
	if workspaceID != "" {
		var exists bool
		if err := s.db.QueryRowContext(context.Background(), `SELECT tma_workspace_exists($1)`, workspaceID).Scan(&exists); err != nil {
			return SecurityAuditOutboxEvent{}, err
		}
		if !exists {
			workspaceID = ""
		}
	}
	input.WorkspaceID = workspaceID
	return s.recordSecurityAuditOutboxInScope(context.Background(), workspaceID, workspaceID == "", input)
}

func (s *PostgresStore) ClaimSecurityAuditOutbox(input ClaimSecurityAuditOutboxInput) ([]SecurityAuditOutboxEvent, error) {
	scopes, err := s.rotatedSecurityAuditScopes(context.Background())
	if err != nil {
		return nil, err
	}
	events := make([]SecurityAuditOutboxEvent, 0, input.Limit)
	for _, workspaceID := range scopes {
		remaining := input.Limit - len(events)
		if remaining <= 0 {
			break
		}
		workspaceCtx, global, err := securityAuditScopeContext(context.Background(), workspaceID)
		if err != nil {
			return nil, err
		}
		workspaceInput := input
		workspaceInput.Limit = remaining
		items, err := s.claimSecurityAuditOutboxInScope(workspaceCtx, workspaceID, global, workspaceInput)
		if err != nil {
			return nil, err
		}
		events = append(events, items...)
	}
	return events, nil
}

func (s *PostgresStore) CompleteSecurityAuditOutbox(input CompleteSecurityAuditOutboxInput) (int, error) {
	scopes, err := s.securityAuditScopes(context.Background())
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, workspaceID := range scopes {
		workspaceCtx, global, err := securityAuditScopeContext(context.Background(), workspaceID)
		if err != nil {
			return 0, err
		}
		count, err := s.completeSecurityAuditOutboxInScope(workspaceCtx, workspaceID, global, input)
		if err != nil {
			return 0, err
		}
		updated += count
	}
	return updated, nil
}

func (s *PostgresStore) FailSecurityAuditOutbox(input FailSecurityAuditOutboxInput) (int, error) {
	scopes, err := s.securityAuditScopes(context.Background())
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, workspaceID := range scopes {
		workspaceCtx, global, err := securityAuditScopeContext(context.Background(), workspaceID)
		if err != nil {
			return 0, err
		}
		count, err := s.failSecurityAuditOutboxInScope(workspaceCtx, workspaceID, global, input)
		if err != nil {
			return 0, err
		}
		updated += count
	}
	return updated, nil
}

func (s *PostgresStore) ReplaySecurityAuditDeadLetters(input ReplaySecurityAuditDeadLettersInput) (int, error) {
	scopes, err := s.rotatedSecurityAuditScopes(context.Background())
	if err != nil {
		return 0, err
	}
	limit := input.Limit
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	updated := 0
	for _, workspaceID := range scopes {
		remaining := limit - updated
		if remaining <= 0 {
			break
		}
		workspaceCtx, global, err := securityAuditScopeContext(context.Background(), workspaceID)
		if err != nil {
			return 0, err
		}
		workspaceInput := input
		workspaceInput.Limit = remaining
		count, err := s.replaySecurityAuditDeadLettersInScope(workspaceCtx, workspaceID, global, workspaceInput)
		if err != nil {
			return 0, err
		}
		updated += count
	}
	return updated, nil
}

func (s *PostgresStore) GetSecurityAuditOutboxStats(now time.Time) (SecurityAuditOutboxStats, error) {
	scopes, err := s.securityAuditScopes(context.Background())
	if err != nil {
		return SecurityAuditOutboxStats{}, err
	}
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var total SecurityAuditOutboxStats
	for _, workspaceID := range scopes {
		workspaceCtx, global, err := securityAuditScopeContext(context.Background(), workspaceID)
		if err != nil {
			return SecurityAuditOutboxStats{}, err
		}
		item, err := s.getSecurityAuditOutboxStatsInScope(workspaceCtx, workspaceID, global, now)
		if err != nil {
			return SecurityAuditOutboxStats{}, err
		}
		mergeSecurityAuditStats(&total, item, now)
	}
	return total, nil
}

func (s *PostgresStore) ListSecurityAuditIntegrityKeyStats() ([]SecurityAuditIntegrityKeyStats, error) {
	scopes, err := s.securityAuditScopes(context.Background())
	if err != nil {
		return nil, err
	}
	all := []SecurityAuditIntegrityKeyStats{}
	for _, workspaceID := range scopes {
		workspaceCtx, global, err := securityAuditScopeContext(context.Background(), workspaceID)
		if err != nil {
			return nil, err
		}
		items, err := s.listSecurityAuditIntegrityKeyStatsInScope(workspaceCtx, workspaceID, global)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return mergeSecurityAuditKeyStats(all), nil
}

func (s *PostgresStore) PruneDeliveredSecurityAuditOutbox(before time.Time, limit int) (int, error) {
	scopes, err := s.rotatedSecurityAuditScopes(context.Background())
	if err != nil {
		return 0, err
	}
	if limit < 1 || limit > 10000 {
		limit = 1000
	}
	deleted := 0
	for _, workspaceID := range scopes {
		remaining := limit - deleted
		if remaining <= 0 {
			break
		}
		workspaceCtx, global, err := securityAuditScopeContext(context.Background(), workspaceID)
		if err != nil {
			return 0, err
		}
		count, err := s.pruneDeliveredSecurityAuditOutboxInScope(workspaceCtx, workspaceID, global, before, remaining)
		if err != nil {
			return 0, err
		}
		deleted += count
	}
	return deleted, nil
}

func scanSecurityAuditOutboxEvent(scanner securityAuditOutboxScanner) (SecurityAuditOutboxEvent, error) {
	var event SecurityAuditOutboxEvent
	var payload string
	err := scanner.Scan(
		&event.ID, &event.WorkspaceID, &payload, &event.IntegrityAlgorithm, &event.IntegrityKeyID,
		&event.IntegrityDigest, &event.Status, &event.AttemptCount, &event.NextAttemptAt,
		&event.LeaseOwner, &event.LeaseExpiresAt, &event.LastError, &event.CreatedAt,
		&event.UpdatedAt, &event.DeliveredAt,
	)
	if err != nil {
		return SecurityAuditOutboxEvent{}, err
	}
	event.Payload = json.RawMessage(payload)
	return event, nil
}
