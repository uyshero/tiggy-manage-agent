package managedagents

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"tiggy-manage-agent/internal/eventsubscription"
)

func TestPostgresEventSubscriptionOutboxLifecycle(t *testing.T) {
	store := newPostgresIntegrationStore(t)
	workspaceID := DefaultWorkspaceID
	ctx, err := ContextWithDatabaseAccessScope(t.Context(), AccessScope{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	identity, err := store.CreateServiceIdentity(ctx, CreateServiceIdentityInput{
		WorkspaceID: workspaceID, Kind: ServiceIdentityKindApplication, Name: "events-" + suffix,
		Role: WorkspaceRoleOperator, Scopes: []string{ServiceScopeEventsManage}, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateEnvironmentContext(ctx, CreateEnvironmentInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "environment/events-" + suffix,
		Name: "event-environment-" + suffix, Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := store.CreateAgentContext(ctx, CreateAgentInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "agent/events-" + suffix,
		EnvironmentID: environment.ID, Name: "event-agent-" + suffix, Model: "test-model", System: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSessionContext(ctx, CreateSessionInput{
		WorkspaceID: workspaceID, AppID: identity.ID, ExternalRef: "session/events-" + suffix,
		AgentID: agent.ID, EnvironmentID: environment.ID, CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := store.CreateEventSubscription(ctx, eventsubscription.CreateSubscriptionInput{
		WorkspaceID: workspaceID, AppID: identity.ID, Name: "primary-" + suffix,
		EndpointURL: "https://events.example.test/tma", EventTypes: eventsubscription.SupportedEventTypes,
		CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM event_subscriptions WHERE id = $1`, subscription.ID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM sessions WHERE id = $1`, session.ID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM agents WHERE id = $1`, agent.ID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM environments WHERE id = $1`, environment.ID)
		_, _ = store.db.ExecContext(cleanupCtx, `DELETE FROM service_identities WHERE id = $1`, identity.ID)
	})

	events, err := store.AppendEventsContext(ctx, session.ID, []AppendEventInput{{
		Type: EventRuntimeCompleted, Payload: json.RawMessage(`{"turn_id":"turn_000001","result":"ok"}`),
	}})
	if err != nil || len(events) != 1 {
		t.Fatalf("append completion event: events=%+v err=%v", events, err)
	}
	deliveries, err := store.ListEventDeliveries(ctx, eventsubscription.ListDeliveriesInput{
		WorkspaceID: workspaceID, SubscriptionID: subscription.ID, Status: eventsubscription.DeliveryStatusPending,
	})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("pending deliveries = %+v err=%v", deliveries, err)
	}
	if deliveries[0].SourceEventID != events[0].ID || deliveries[0].EventType != eventsubscription.EventRunCompleted || deliveries[0].ID == "" {
		t.Fatalf("unexpected completion delivery: %+v", deliveries[0])
	}

	claimed, err := store.ClaimEventDeliveries(eventsubscription.ClaimInput{
		LeaseOwner: "integration-worker-" + suffix, LeaseDuration: time.Minute, MaxAttempts: 3, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimedDelivery, ok := findEventDelivery(claimed, deliveries[0].ID)
	if !ok {
		t.Fatalf("delivery %s was not claimed: %+v", deliveries[0].ID, claimed)
	}
	if updated, err := store.FailEventDelivery(eventsubscription.FailInput{
		DeliveryID: claimedDelivery.ID, LeaseOwner: "integration-worker-" + suffix,
		HTTPStatus: 503, Error: "test failure", DeadLetter: true,
	}); err != nil || !updated {
		t.Fatalf("dead-letter delivery: updated=%t err=%v", updated, err)
	}
	replayed, err := store.ReplayEventDelivery(ctx, workspaceID, subscription.ID, deliveries[0].ID)
	if err != nil || replayed.Status != eventsubscription.DeliveryStatusPending || replayed.AttemptCount != 0 {
		t.Fatalf("replayed delivery = %+v err=%v", replayed, err)
	}

	rotated, err := store.RotateEventSubscriptionSecret(ctx, workspaceID, subscription.ID)
	if err != nil || rotated.SecretVersion != 2 {
		t.Fatalf("rotated subscription = %+v err=%v", rotated, err)
	}
}

func findEventDelivery(items []eventsubscription.Delivery, id string) (eventsubscription.Delivery, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return eventsubscription.Delivery{}, false
}
