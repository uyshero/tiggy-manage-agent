package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tiggy-manage-agent/internal/eventsubscription"
)

const eventSubscriptionColumns = `
	id, workspace_id, app_id, name, endpoint_url, array_to_json(event_types), status,
	secret_version, created_by, created_at, updated_at
`

const eventDeliveryColumns = `
	id, workspace_id, subscription_id, app_id, source_event_id, event_type,
	payload_json, endpoint_url, secret_version, status, attempt_count,
	next_attempt_at, lease_owner, lease_expires_at, last_http_status, last_error,
	created_at, updated_at, delivered_at
`

const eventDeliveryReturningColumns = `
	delivery.id, delivery.workspace_id, delivery.subscription_id, delivery.app_id,
	delivery.source_event_id, delivery.event_type, delivery.payload_json, delivery.endpoint_url,
	delivery.secret_version, delivery.status, delivery.attempt_count, delivery.next_attempt_at,
	delivery.lease_owner, delivery.lease_expires_at, delivery.last_http_status, delivery.last_error,
	delivery.created_at, delivery.updated_at, delivery.delivered_at
`

type eventSubscriptionScanner interface {
	Scan(...any) error
}

func scanEventSubscription(scanner eventSubscriptionScanner) (eventsubscription.Subscription, error) {
	var item eventsubscription.Subscription
	var eventTypesJSON []byte
	err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.AppID, &item.Name, &item.EndpointURL,
		&eventTypesJSON, &item.Status, &item.SecretVersion, &item.CreatedBy,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err == nil {
		err = json.Unmarshal(eventTypesJSON, &item.EventTypes)
	}
	return item, err
}

func scanEventDelivery(scanner eventSubscriptionScanner) (eventsubscription.Delivery, error) {
	var item eventsubscription.Delivery
	var leaseExpiresAt, deliveredAt sql.NullTime
	err := scanner.Scan(
		&item.ID, &item.WorkspaceID, &item.SubscriptionID, &item.AppID,
		&item.SourceEventID, &item.EventType, &item.Payload, &item.EndpointURL,
		&item.SecretVersion, &item.Status, &item.AttemptCount, &item.NextAttemptAt,
		&item.LeaseOwner, &leaseExpiresAt, &item.LastHTTPStatus, &item.LastError,
		&item.CreatedAt, &item.UpdatedAt, &deliveredAt,
	)
	if leaseExpiresAt.Valid {
		item.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	if deliveredAt.Valid {
		item.DeliveredAt = &deliveredAt.Time
	}
	return item, err
}

func (s *PostgresStore) ListEventSubscriptions(ctx context.Context, workspaceID, appID string) ([]eventsubscription.Subscription, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := `SELECT ` + eventSubscriptionColumns + ` FROM event_subscriptions WHERE workspace_id = $1`
	args := []any{scope.WorkspaceID}
	if strings.TrimSpace(appID) != "" {
		query += ` AND app_id = $2`
		args = append(args, strings.TrimSpace(appID))
	}
	query += ` ORDER BY created_at, id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []eventsubscription.Subscription{}
	for rows.Next() {
		item, err := scanEventSubscription(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) GetEventSubscription(ctx context.Context, workspaceID, id string) (eventsubscription.Subscription, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	defer tx.Rollback()
	item, err := scanEventSubscription(tx.QueryRowContext(ctx,
		`SELECT `+eventSubscriptionColumns+` FROM event_subscriptions WHERE workspace_id = $1 AND id = $2`,
		scope.WorkspaceID, strings.TrimSpace(id),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return eventsubscription.Subscription{}, ErrNotFound
	}
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return eventsubscription.Subscription{}, err
	}
	return item, nil
}

func (s *PostgresStore) CreateEventSubscription(ctx context.Context, input eventsubscription.CreateSubscriptionInput) (eventsubscription.Subscription, error) {
	input.AppID = strings.TrimSpace(input.AppID)
	input.Name = strings.TrimSpace(input.Name)
	input.EndpointURL = strings.TrimSpace(input.EndpointURL)
	eventTypes, err := eventsubscription.NormalizeEventTypes(input.EventTypes)
	if err != nil {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if input.AppID == "" || input.Name == "" || len(input.Name) > 128 || input.EndpointURL == "" || len(input.EndpointURL) > 2048 {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: app_id, name, and endpoint_url are required and must fit their limits", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	defer tx.Rollback()
	var validApp bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM service_identities
			WHERE workspace_id = $1 AND id = $2 AND kind = 'application' AND status = 'active'
		)
	`, scope.WorkspaceID, input.AppID).Scan(&validApp); err != nil {
		return eventsubscription.Subscription{}, err
	}
	if !validApp {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: active application identity not found", ErrInvalid)
	}
	id, err := nextSequenceID(ctx, tx, "esub", "tma_event_subscription_id_seq")
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	item, err := scanEventSubscription(tx.QueryRowContext(ctx, `
		INSERT INTO event_subscriptions (
			id, workspace_id, app_id, name, endpoint_url, event_types, status,
			secret_version, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'active', 1, $7, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING `+eventSubscriptionColumns,
		id, scope.WorkspaceID, input.AppID, input.Name, input.EndpointURL,
		eventTypes, defaultString(strings.TrimSpace(input.CreatedBy), "system"),
	))
	if postgresUniqueViolation(err) {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: event subscription name already exists for this application", ErrConflict)
	}
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return eventsubscription.Subscription{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateEventSubscription(ctx context.Context, input eventsubscription.UpdateSubscriptionInput) (eventsubscription.Subscription, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.AppID = strings.TrimSpace(input.AppID)
	input.Name = strings.TrimSpace(input.Name)
	input.EndpointURL = strings.TrimSpace(input.EndpointURL)
	input.Status = strings.TrimSpace(input.Status)
	eventTypes, err := eventsubscription.NormalizeEventTypes(input.EventTypes)
	if err != nil {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if input.ID == "" || input.AppID == "" || input.Name == "" || input.EndpointURL == "" || (input.Status != eventsubscription.SubscriptionStatusActive && input.Status != eventsubscription.SubscriptionStatusDisabled) {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: valid id, app_id, name, endpoint_url, and status are required", ErrInvalid)
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	defer tx.Rollback()
	item, err := scanEventSubscription(tx.QueryRowContext(ctx, `
		UPDATE event_subscriptions
		SET name = $4, endpoint_url = $5, event_types = $6, status = $7, updated_at = CURRENT_TIMESTAMP
		WHERE workspace_id = $1 AND id = $2 AND app_id = $3
		RETURNING `+eventSubscriptionColumns,
		scope.WorkspaceID, input.ID, input.AppID, input.Name, input.EndpointURL, eventTypes, input.Status,
	))
	if postgresUniqueViolation(err) {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: event subscription name already exists for this application", ErrConflict)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return eventsubscription.Subscription{}, ErrNotFound
	}
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return eventsubscription.Subscription{}, err
	}
	return item, nil
}

func (s *PostgresStore) RotateEventSubscriptionSecret(ctx context.Context, workspaceID, id string) (eventsubscription.Subscription, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	defer tx.Rollback()
	item, err := scanEventSubscription(tx.QueryRowContext(ctx, `
		UPDATE event_subscriptions
		SET secret_version = secret_version + 1, updated_at = CURRENT_TIMESTAMP
		WHERE workspace_id = $1 AND id = $2
		RETURNING `+eventSubscriptionColumns,
		scope.WorkspaceID, strings.TrimSpace(id),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return eventsubscription.Subscription{}, ErrNotFound
	}
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	if err := tx.Commit(); err != nil {
		return eventsubscription.Subscription{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListEventDeliveries(ctx context.Context, input eventsubscription.ListDeliveriesInput) ([]eventsubscription.Delivery, error) {
	if input.Limit < 1 || input.Limit > 200 {
		input.Limit = 100
	}
	tx, scope, err := s.beginDatabaseAccessScope(ctx, input.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := `SELECT ` + eventDeliveryColumns + ` FROM event_deliveries WHERE workspace_id = $1 AND subscription_id = $2`
	args := []any{scope.WorkspaceID, strings.TrimSpace(input.SubscriptionID)}
	if strings.TrimSpace(input.Status) != "" {
		query += ` AND status = $3`
		args = append(args, strings.TrimSpace(input.Status))
	}
	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, len(args)+1)
	args = append(args, input.Limit)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []eventsubscription.Delivery{}
	for rows.Next() {
		item, err := scanEventDelivery(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) ReplayEventDelivery(ctx context.Context, workspaceID, subscriptionID, deliveryID string) (eventsubscription.Delivery, error) {
	tx, scope, err := s.beginDatabaseAccessScope(ctx, workspaceID)
	if err != nil {
		return eventsubscription.Delivery{}, err
	}
	defer tx.Rollback()
	item, err := scanEventDelivery(tx.QueryRowContext(ctx, `
		UPDATE event_deliveries delivery
		SET status = 'pending', attempt_count = 0, next_attempt_at = CURRENT_TIMESTAMP,
			lease_owner = '', lease_expires_at = NULL, last_http_status = 0,
			last_error = '', delivered_at = NULL, endpoint_url = subscription.endpoint_url,
			secret_version = subscription.secret_version, updated_at = CURRENT_TIMESTAMP
		FROM event_subscriptions subscription
		WHERE delivery.workspace_id = $1 AND delivery.subscription_id = $2 AND delivery.id = $3
			AND delivery.status = 'dead_letter' AND subscription.id = delivery.subscription_id
		RETURNING `+eventDeliveryReturningColumns,
		scope.WorkspaceID, strings.TrimSpace(subscriptionID), strings.TrimSpace(deliveryID),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return eventsubscription.Delivery{}, ErrNotFound
	}
	if err != nil {
		return eventsubscription.Delivery{}, err
	}
	if err := tx.Commit(); err != nil {
		return eventsubscription.Delivery{}, err
	}
	return item, nil
}

func (s *PostgresStore) ClaimEventDeliveries(input eventsubscription.ClaimInput) ([]eventsubscription.Delivery, error) {
	if strings.TrimSpace(input.LeaseOwner) == "" || input.LeaseDuration <= 0 || input.MaxAttempts < 1 || input.Limit < 1 {
		return nil, fmt.Errorf("%w: valid event delivery claim parameters are required", ErrInvalid)
	}
	if input.Limit > 1000 {
		input.Limit = 1000
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	workspaceIDs, err := s.listTenantWorkspaceIDs(context.Background(), "")
	if err != nil {
		return nil, err
	}
	workspaceIDs = s.rotateEventDeliveryClaimWorkspaces(workspaceIDs)
	items := make([]eventsubscription.Delivery, 0, input.Limit)
	for _, workspaceID := range workspaceIDs {
		if len(items) >= input.Limit {
			break
		}
		ctx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE event_deliveries SET status = 'dead_letter', lease_owner = '', lease_expires_at = NULL,
				last_error = CASE WHEN last_error = '' THEN 'delivery lease expired after maximum attempts' ELSE last_error END,
				updated_at = $1
			WHERE status = 'delivering' AND lease_expires_at <= $1 AND attempt_count >= $2
		`, now, input.MaxAttempts); err != nil {
			tx.Rollback()
			return nil, err
		}
		rows, err := tx.QueryContext(ctx, `
			WITH candidates AS (
				SELECT id FROM event_deliveries
				WHERE attempt_count < $3
					AND ((status = 'pending' AND next_attempt_at <= $1)
						OR (status = 'delivering' AND lease_expires_at <= $1))
				ORDER BY next_attempt_at, created_at, id
				FOR UPDATE SKIP LOCKED LIMIT $4
			)
			UPDATE event_deliveries delivery
			SET status = 'delivering', attempt_count = delivery.attempt_count + 1,
				lease_owner = $2, lease_expires_at = $5, last_error = '', updated_at = $1
			FROM candidates WHERE delivery.id = candidates.id
			RETURNING `+eventDeliveryReturningColumns,
			now, input.LeaseOwner, input.MaxAttempts, input.Limit-len(items), now.Add(input.LeaseDuration),
		)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		for rows.Next() {
			item, scanErr := scanEventDelivery(rows)
			if scanErr != nil {
				rows.Close()
				tx.Rollback()
				return nil, scanErr
			}
			items = append(items, item)
		}
		if err := rows.Close(); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *PostgresStore) rotateEventDeliveryClaimWorkspaces(workspaceIDs []string) []string {
	if len(workspaceIDs) < 2 {
		return workspaceIDs
	}
	s.eventClaimMu.Lock()
	start := s.eventClaimCursor % len(workspaceIDs)
	s.eventClaimCursor = (start + 1) % len(workspaceIDs)
	s.eventClaimMu.Unlock()
	rotated := make([]string, 0, len(workspaceIDs))
	rotated = append(rotated, workspaceIDs[start:]...)
	rotated = append(rotated, workspaceIDs[:start]...)
	return rotated
}

func (s *PostgresStore) CompleteEventDelivery(input eventsubscription.CompleteInput) (bool, error) {
	return s.updateClaimedEventDelivery(input.DeliveryID, input.LeaseOwner, func(ctx context.Context, tx *sql.Tx, now time.Time) (sql.Result, error) {
		return tx.ExecContext(ctx, `
			UPDATE event_deliveries SET status = 'delivered', delivered_at = $3,
				lease_owner = '', lease_expires_at = NULL, last_http_status = $4,
				last_error = '', updated_at = $3
			WHERE id = $1 AND lease_owner = $2 AND status = 'delivering'
		`, input.DeliveryID, input.LeaseOwner, now, input.HTTPStatus)
	}, input.Now)
}

func (s *PostgresStore) FailEventDelivery(input eventsubscription.FailInput) (bool, error) {
	return s.updateClaimedEventDelivery(input.DeliveryID, input.LeaseOwner, func(ctx context.Context, tx *sql.Tx, now time.Time) (sql.Result, error) {
		status := eventsubscription.DeliveryStatusPending
		if input.DeadLetter {
			status = eventsubscription.DeliveryStatusDeadLetter
		}
		retryAt := input.RetryAt.UTC()
		if retryAt.IsZero() {
			retryAt = now
		}
		return tx.ExecContext(ctx, `
			UPDATE event_deliveries SET status = $3, next_attempt_at = $4,
				lease_owner = '', lease_expires_at = NULL, last_http_status = $5,
				last_error = $6, updated_at = $7
			WHERE id = $1 AND lease_owner = $2 AND status = 'delivering'
		`, input.DeliveryID, input.LeaseOwner, status, retryAt, input.HTTPStatus, truncateText(input.Error, 2048), now)
	}, input.Now)
}

func (s *PostgresStore) updateClaimedEventDelivery(id, leaseOwner string, update func(context.Context, *sql.Tx, time.Time) (sql.Result, error), requestedNow time.Time) (bool, error) {
	workspaceIDs, err := s.listTenantWorkspaceIDs(context.Background(), "")
	if err != nil {
		return false, err
	}
	now := requestedNow.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	for _, workspaceID := range workspaceIDs {
		ctx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: workspaceID})
		if err != nil {
			return false, err
		}
		tx, _, err := s.beginDatabaseAccessScope(ctx, workspaceID)
		if err != nil {
			return false, err
		}
		result, err := update(ctx, tx, now)
		if err != nil {
			tx.Rollback()
			return false, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			tx.Rollback()
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		if rows > 0 {
			return true, nil
		}
	}
	return false, nil
}

func enqueueSessionEventDeliveriesTx(ctx context.Context, tx *sql.Tx, sessionID string, events []Event) error {
	var workspaceID, appID string
	if err := tx.QueryRowContext(ctx, `
		SELECT workspace_id, COALESCE(app_id, '') FROM sessions WHERE id = $1
	`, sessionID).Scan(&workspaceID, &appID); err != nil {
		return err
	}
	if strings.TrimSpace(appID) == "" {
		return nil
	}
	for _, event := range events {
		eventType := publicSessionEventType(event.Type)
		if eventType == "" {
			continue
		}
		data := map[string]any{
			"session_id": event.SessionID,
			"run_id":     event.TurnID,
			"turn_id":    event.TurnID,
			"seq":        event.Seq,
			"event_type": event.Type,
			"payload":    json.RawMessage(event.Payload),
		}
		if err := enqueueApplicationEventDeliveriesTx(ctx, tx, workspaceID, appID, event.ID, eventType, event.CreatedAt, data); err != nil {
			return err
		}
	}
	return nil
}

func enqueueArtifactCreatedDeliveriesTx(ctx context.Context, tx *sql.Tx, session Session, artifact SessionArtifact) error {
	if strings.TrimSpace(session.AppID) == "" {
		return nil
	}
	return enqueueApplicationEventDeliveriesTx(
		ctx, tx, session.WorkspaceID, session.AppID, artifact.ID,
		eventsubscription.EventArtifactCreated, artifact.CreatedAt,
		map[string]any{"session_id": session.ID, "run_id": artifact.TurnID, "turn_id": artifact.TurnID, "artifact": artifact},
	)
}

func enqueueApplicationEventDeliveriesTx(ctx context.Context, tx *sql.Tx, workspaceID, appID, sourceEventID, eventType string, occurredAt time.Time, data any) error {
	payload, err := json.Marshal(map[string]any{
		"schema":       eventsubscription.EnvelopeSchema,
		"event_id":     sourceEventID,
		"type":         eventType,
		"occurred_at":  occurredAt.UTC(),
		"workspace_id": workspaceID,
		"app_id":       appID,
		"data":         data,
	})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO event_deliveries (
			id, workspace_id, subscription_id, app_id, source_event_id, event_type,
			payload_json, endpoint_url, secret_version, status, next_attempt_at,
			created_at, updated_at
		)
		SELECT 'edel_' || lpad(nextval('tma_event_delivery_id_seq')::text, 6, '0'),
			$1, subscription.id, $2, $3, $4, $5::jsonb,
			subscription.endpoint_url, subscription.secret_version, 'pending', CURRENT_TIMESTAMP,
			$6, CURRENT_TIMESTAMP
		FROM event_subscriptions subscription
		WHERE subscription.workspace_id = $1 AND subscription.app_id = $2
			AND subscription.status = 'active' AND $4 = ANY(subscription.event_types)
		ON CONFLICT (subscription_id, source_event_id) DO NOTHING
	`, workspaceID, appID, sourceEventID, eventType, payload, occurredAt.UTC())
	return err
}

func publicSessionEventType(eventType string) string {
	switch eventType {
	case EventRuntimeCompleted:
		return eventsubscription.EventRunCompleted
	case EventRuntimeFailed, EventSessionStatusFailed:
		return eventsubscription.EventRunFailed
	case EventRuntimeToolInterventionRequired, EventRuntimeHumanInputRequired, EventRuntimePlanApprovalRequired:
		return eventsubscription.EventInterventionRequired
	default:
		return ""
	}
}

func truncateText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

var _ eventsubscription.Store = (*PostgresStore)(nil)
