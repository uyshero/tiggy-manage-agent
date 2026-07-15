package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

func TestDurableSecurityAuditPersistsHMACAndDeliversStableEventID(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	var received string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer collector.Close()
	pipeline := newTestDurableSecurityAuditPipeline(t, store, collector.URL, collector.Client(), 3)
	if !pipeline.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{
		At: time.Unix(1_700_000_000, 0), Outcome: "allowed", Reason: "identity_boundary", AuthType: "oidc",
		Subject: "alice", WorkspaceID: "wksp_finance", AuthorizationSources: []string{"group_mapping:finance"},
	}) {
		t.Fatal("expected durable audit insert to succeed")
	}
	record := store.first(t)
	if record.IntegrityAlgorithm != "hmac-sha256" || record.IntegrityKeyID != "legacy" || !verifySecurityAuditIntegrity(record.Payload, record.IntegrityAlgorithm, record.IntegrityDigest, []byte(testSecurityAuditIntegrityKey)) {
		t.Fatalf("unexpected outbox integrity metadata: %+v", record)
	}
	var persisted AuthorizationDecisionEvent
	if err := json.Unmarshal(record.Payload, &persisted); err != nil {
		t.Fatalf("decode persisted event: %v", err)
	}
	if persisted.ID != record.ID || record.WorkspaceID != "wksp_finance" || !strings.HasPrefix(record.ID, "saud_") {
		t.Fatalf("expected stable security audit id, record=%+v event=%+v", record, persisted)
	}
	result, err := pipeline.RunOnce(context.Background(), time.Unix(1_700_000_001, 0))
	if err != nil {
		t.Fatalf("deliver durable security audit: %v", err)
	}
	if result.Delivered != 1 || store.first(t).Status != managedagents.SecurityAuditOutboxDelivered {
		t.Fatalf("unexpected delivery result: %+v record=%+v", result, store.first(t))
	}
	if !strings.Contains(received, record.ID) || !strings.Contains(received, "group_mapping:finance") {
		t.Fatalf("OTLP delivery lost stable id or authorization source: %s", received)
	}
}

func TestDurableSecurityAuditRetriesThenDeadLetters(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer collector.Close()
	pipeline := newTestDurableSecurityAuditPipeline(t, store, collector.URL, collector.Client(), 2)
	firstAt := time.Unix(1_700_000_000, 0)
	if !pipeline.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{At: firstAt, Outcome: "denied", Reason: "authentication_failed", AuthType: "jwt"}) {
		t.Fatal("expected durable audit insert to succeed")
	}
	first, err := pipeline.RunOnce(context.Background(), firstAt)
	if err == nil || first.Retried != 1 {
		t.Fatalf("expected first failure to schedule retry, result=%+v err=%v", first, err)
	}
	record := store.first(t)
	if record.Status != managedagents.SecurityAuditOutboxPending || record.AttemptCount != 1 || !record.NextAttemptAt.Equal(firstAt.Add(time.Second)) {
		t.Fatalf("unexpected retry state: %+v", record)
	}
	second, err := pipeline.RunOnce(context.Background(), record.NextAttemptAt)
	if err == nil || second.DeadLettered != 1 {
		t.Fatalf("expected second failure to dead letter, result=%+v err=%v", second, err)
	}
	record = store.first(t)
	if record.Status != managedagents.SecurityAuditOutboxDeadLetter || record.AttemptCount != 2 {
		t.Fatalf("unexpected dead letter state: %+v", record)
	}
	replayed, err := pipeline.ReplayDeadLetters(time.Now().Add(time.Hour), 10)
	if err != nil || replayed != 1 || store.first(t).Status != managedagents.SecurityAuditOutboxPending || store.first(t).AttemptCount != 0 {
		t.Fatalf("replay dead letter: replayed=%d err=%v record=%+v", replayed, err, store.first(t))
	}
}

func TestDurableSecurityAuditRejectsTamperedPayload(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	requests := 0
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()
	pipeline := newTestDurableSecurityAuditPipeline(t, store, collector.URL, collector.Client(), 3)
	if !pipeline.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "allowed", Reason: "identity_boundary", AuthType: "gateway"}) {
		t.Fatal("expected durable audit insert to succeed")
	}
	store.mu.Lock()
	record := store.events[store.order[0]]
	record.Payload = json.RawMessage(strings.Replace(string(record.Payload), `"allowed"`, `"denied"`, 1))
	store.events[record.ID] = record
	store.mu.Unlock()
	result, err := pipeline.RunOnce(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("process tampered audit: %v", err)
	}
	if result.DeadLettered != 1 || requests != 0 || store.first(t).Status != managedagents.SecurityAuditOutboxDeadLetter {
		t.Fatalf("tampered event was not isolated: result=%+v requests=%d record=%+v", result, requests, store.first(t))
	}
}

func TestDurableSecurityAuditKeyRotationDeliversCurrentPreviousAndLegacyRows(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	requests := 0
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer collector.Close()

	oldPipeline := newTestDurableSecurityAuditPipelineWithKeys(t, store, collector.URL, collector.Client(), "2026-01", map[string]string{
		"2026-01": testSecurityAuditPreviousIntegrityKey,
	})
	if !oldPipeline.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "allowed", Reason: "old-key"}) ||
		!oldPipeline.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "allowed", Reason: "legacy-blank-key-id"}) {
		t.Fatal("expected old-key audit inserts to succeed")
	}
	store.mu.Lock()
	legacyID := store.order[1]
	legacy := store.events[legacyID]
	legacy.IntegrityKeyID = ""
	store.events[legacyID] = legacy
	store.mu.Unlock()

	rotated := newTestDurableSecurityAuditPipelineWithKeys(t, store, collector.URL, collector.Client(), "2026-07", map[string]string{
		"2026-01": testSecurityAuditPreviousIntegrityKey,
		"2026-07": testSecurityAuditCurrentIntegrityKey,
	})
	if !rotated.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "allowed", Reason: "current-key"}) {
		t.Fatal("expected current-key audit insert to succeed")
	}
	store.mu.Lock()
	if store.events[store.order[0]].IntegrityKeyID != "2026-01" || store.events[store.order[1]].IntegrityKeyID != "" || store.events[store.order[2]].IntegrityKeyID != "2026-07" {
		t.Fatalf("unexpected persisted key ids: old=%q legacy=%q current=%q", store.events[store.order[0]].IntegrityKeyID, store.events[store.order[1]].IntegrityKeyID, store.events[store.order[2]].IntegrityKeyID)
	}
	store.mu.Unlock()

	result, err := rotated.RunOnce(context.Background(), time.Now().Add(time.Second))
	if err != nil || result.Delivered != 3 || requests != 1 {
		t.Fatalf("deliver across integrity key rotation: result=%+v requests=%d err=%v", result, requests, err)
	}
	status, err := rotated.SecurityAuditIntegrityKeyStatus()
	if err != nil || status.HistoricalUnidentifiedBlocking != 0 || len(status.Keys) != 3 {
		t.Fatalf("unexpected post-rotation key status: %+v err=%v", status, err)
	}
	states := securityAuditIntegrityKeyStatesByID(status.Keys)
	if !states["2026-01"].SafeToRemove || states["2026-07"].SafeToRemove || states[""].SafeToRemove {
		t.Fatalf("unexpected key removal readiness: %+v", states)
	}
	metrics := rotated.SecurityAuditMetrics()
	if !metrics.IntegrityStatusAvailable || metrics.IntegrityKeysReadyToRemove != 1 || metrics.IntegrityKeysRemovalBlocked != 0 ||
		metrics.IntegrityUnconfiguredBlocking != 0 || metrics.IntegrityHistoricalUnidentifiedBlocking != 0 {
		t.Fatalf("unexpected post-rotation integrity metrics: %+v", metrics)
	}
}

func TestDurableSecurityAuditRejectsUnknownIntegrityKeyID(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	requests := 0
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer collector.Close()
	pipeline := newTestDurableSecurityAuditPipelineWithKeys(t, store, collector.URL, collector.Client(), "current", map[string]string{
		"current": testSecurityAuditCurrentIntegrityKey,
	})
	if !pipeline.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{Outcome: "allowed", Reason: "unknown-key-id"}) {
		t.Fatal("expected durable audit insert to succeed")
	}
	store.mu.Lock()
	id := store.order[0]
	record := store.events[id]
	record.IntegrityKeyID = "retired"
	store.events[id] = record
	store.mu.Unlock()

	result, err := pipeline.RunOnce(context.Background(), time.Now().Add(time.Second))
	if err != nil || result.DeadLettered != 1 || requests != 0 {
		t.Fatalf("unknown key id was not isolated: result=%+v requests=%d err=%v", result, requests, err)
	}
	status, err := pipeline.SecurityAuditIntegrityKeyStatus()
	if err != nil {
		t.Fatalf("get unknown key status: %v", err)
	}
	retired := securityAuditIntegrityKeyStatesByID(status.Keys)["retired"]
	if retired.Configured || retired.SafeToRemove || retired.DeadLetter != 1 {
		t.Fatalf("unexpected retired key status: %+v", retired)
	}
	metrics := pipeline.SecurityAuditMetrics()
	if metrics.IntegrityUnconfiguredBlocking != 1 {
		t.Fatalf("expected unconfigured key blocker metric, got %+v", metrics)
	}
}

func TestSecurityAuditIntegrityKeyStatusBlocksRemovalForUnidentifiedRows(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	pipeline := newTestDurableSecurityAuditPipelineWithKeys(t, store, "http://collector.test", http.DefaultClient, "current", map[string]string{
		"previous": testSecurityAuditPreviousIntegrityKey,
		"current":  testSecurityAuditCurrentIntegrityKey,
	})
	store.events["legacy"] = managedagents.SecurityAuditOutboxEvent{
		ID: "legacy", IntegrityAlgorithm: "hmac-sha256", IntegrityKeyID: "", Status: managedagents.SecurityAuditOutboxDeadLetter,
	}
	store.events["previous"] = managedagents.SecurityAuditOutboxEvent{
		ID: "previous", IntegrityAlgorithm: "hmac-sha256", IntegrityKeyID: "previous", Status: managedagents.SecurityAuditOutboxDelivered,
	}
	store.order = []string{"legacy", "previous"}

	status, err := pipeline.SecurityAuditIntegrityKeyStatus()
	if err != nil {
		t.Fatalf("get integrity key status: %v", err)
	}
	previous := securityAuditIntegrityKeyStatesByID(status.Keys)["previous"]
	if status.HistoricalUnidentifiedBlocking != 1 || previous.SafeToRemove {
		t.Fatalf("unidentified historical row should block previous key removal: status=%+v previous=%+v", status, previous)
	}
	metrics := pipeline.SecurityAuditMetrics()
	if metrics.IntegrityHistoricalUnidentifiedBlocking != 1 || metrics.IntegrityKeysRemovalBlocked != 1 {
		t.Fatalf("expected historical blocker metrics, got %+v", metrics)
	}
}

func TestSecurityAuditMetricsReportsIntegrityStatusUnavailable(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	store.listKeyStatsError = context.DeadlineExceeded
	pipeline := newTestDurableSecurityAuditPipeline(t, store, "http://collector.test", http.DefaultClient, 3)
	metrics := pipeline.SecurityAuditMetrics()
	if metrics.IntegrityStatusAvailable {
		t.Fatalf("expected unavailable integrity status metric, got %+v", metrics)
	}
}

func TestDurableSecurityAuditRedeliversStableIDAfterCompletionFailure(t *testing.T) {
	t.Parallel()
	store := newStubSecurityAuditOutboxStore()
	store.completeErrors = 1
	var mu sync.Mutex
	received := []string{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer collector.Close()
	createdAt := time.Unix(1_700_000_000, 0)
	firstPipeline := newTestDurableSecurityAuditPipeline(t, store, collector.URL, collector.Client(), 3)
	firstPipeline.workerID = "security-audit-worker-a"
	if !firstPipeline.EnqueueAuthorizationDecision(AuthorizationDecisionEvent{
		At: createdAt, Outcome: "allowed", Reason: "identity_boundary", AuthType: "oidc",
	}) {
		t.Fatal("expected durable audit insert to succeed")
	}
	eventID := store.first(t).ID
	if _, err := firstPipeline.RunOnce(context.Background(), createdAt.Add(time.Second)); err == nil {
		t.Fatal("expected simulated completion failure")
	}
	if store.first(t).Status != managedagents.SecurityAuditOutboxDelivering {
		t.Fatalf("event should remain leased after completion failure: %+v", store.first(t))
	}
	secondPipeline := newTestDurableSecurityAuditPipeline(t, store, collector.URL, collector.Client(), 3)
	secondPipeline.workerID = "security-audit-worker-b"
	result, err := secondPipeline.RunOnce(context.Background(), createdAt.Add(3*time.Second))
	if err != nil || result.Delivered != 1 {
		t.Fatalf("redeliver expired lease: result=%+v err=%v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || !strings.Contains(received[0], eventID) || !strings.Contains(received[1], eventID) {
		t.Fatalf("expected two deliveries with stable event id %q, got %+v", eventID, received)
	}
}

const testSecurityAuditIntegrityKey = "test-security-audit-integrity-key-32-bytes"
const testSecurityAuditPreviousIntegrityKey = "previous-security-audit-key-material-32-bytes"
const testSecurityAuditCurrentIntegrityKey = "current-security-audit-key-material-at-least-32-bytes"

func newTestDurableSecurityAuditPipeline(t *testing.T, store managedagents.SecurityAuditOutboxStore, endpoint string, client *http.Client, maxAttempts int) *DurableSecurityAuditPipeline {
	t.Helper()
	pipeline, err := NewDurableSecurityAuditPipeline(DurableSecurityAuditConfig{
		Store: store, Endpoint: endpoint, IntegrityKey: testSecurityAuditIntegrityKey,
		WorkerID: "security-audit-test-worker", BatchSize: 10,
		PollInterval: 100 * time.Millisecond, LeaseDuration: time.Second, MaxAttempts: maxAttempts,
		RetryInitialDelay: time.Second, RetryMaxDelay: time.Minute,
		Retention: 24 * time.Hour, PruneInterval: time.Hour, PruneLimit: 100,
		HTTPClient: client, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new durable security audit pipeline: %v", err)
	}
	return pipeline
}

func newTestDurableSecurityAuditPipelineWithKeys(t *testing.T, store managedagents.SecurityAuditOutboxStore, endpoint string, client *http.Client, activeKeyID string, keys map[string]string) *DurableSecurityAuditPipeline {
	t.Helper()
	pipeline, err := NewDurableSecurityAuditPipeline(DurableSecurityAuditConfig{
		Store: store, Endpoint: endpoint, IntegrityKeyID: activeKeyID, IntegrityKeys: keys,
		WorkerID: "security-audit-key-rotation-test-worker", BatchSize: 10,
		PollInterval: 100 * time.Millisecond, LeaseDuration: time.Second, MaxAttempts: 3,
		RetryInitialDelay: time.Second, RetryMaxDelay: time.Minute,
		Retention: 24 * time.Hour, PruneInterval: time.Hour, PruneLimit: 100,
		HTTPClient: client, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new rotating durable security audit pipeline: %v", err)
	}
	return pipeline
}

type stubSecurityAuditOutboxStore struct {
	mu                sync.Mutex
	events            map[string]managedagents.SecurityAuditOutboxEvent
	order             []string
	completeErrors    int
	listKeyStatsError error
}

func newStubSecurityAuditOutboxStore() *stubSecurityAuditOutboxStore {
	return &stubSecurityAuditOutboxStore{events: map[string]managedagents.SecurityAuditOutboxEvent{}}
}

func (s *stubSecurityAuditOutboxStore) RecordSecurityAuditOutbox(input managedagents.RecordSecurityAuditOutboxInput) (managedagents.SecurityAuditOutboxEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event := managedagents.SecurityAuditOutboxEvent{
		ID: input.ID, WorkspaceID: input.WorkspaceID, Payload: append(json.RawMessage(nil), input.Payload...),
		IntegrityAlgorithm: input.IntegrityAlgorithm, IntegrityKeyID: input.IntegrityKeyID, IntegrityDigest: input.IntegrityDigest,
		Status: managedagents.SecurityAuditOutboxPending, NextAttemptAt: input.CreatedAt,
		CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt,
	}
	s.events[event.ID] = event
	s.order = append(s.order, event.ID)
	return event, nil
}

func (s *stubSecurityAuditOutboxStore) ClaimSecurityAuditOutbox(input managedagents.ClaimSecurityAuditOutboxInput) ([]managedagents.SecurityAuditOutboxEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	claimed := []managedagents.SecurityAuditOutboxEvent{}
	for _, id := range s.order {
		event := s.events[id]
		eligible := event.Status == managedagents.SecurityAuditOutboxPending && !event.NextAttemptAt.After(input.Now)
		eligible = eligible || (event.Status == managedagents.SecurityAuditOutboxDelivering && event.LeaseExpiresAt != nil && !event.LeaseExpiresAt.After(input.Now))
		if !eligible {
			continue
		}
		if event.AttemptCount >= input.MaxAttempts {
			event.Status = managedagents.SecurityAuditOutboxDeadLetter
			s.events[id] = event
			continue
		}
		expires := input.Now.Add(input.LeaseDuration)
		event.Status = managedagents.SecurityAuditOutboxDelivering
		event.AttemptCount++
		event.LeaseOwner = input.LeaseOwner
		event.LeaseExpiresAt = &expires
		event.UpdatedAt = input.Now
		s.events[id] = event
		claimed = append(claimed, event)
		if len(claimed) >= input.Limit {
			break
		}
	}
	return claimed, nil
}

func (s *stubSecurityAuditOutboxStore) CompleteSecurityAuditOutbox(input managedagents.CompleteSecurityAuditOutboxInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completeErrors > 0 {
		s.completeErrors--
		return 0, context.DeadlineExceeded
	}
	updated := 0
	for _, id := range input.IDs {
		event := s.events[id]
		if event.Status != managedagents.SecurityAuditOutboxDelivering || event.LeaseOwner != input.LeaseOwner {
			continue
		}
		event.Status = managedagents.SecurityAuditOutboxDelivered
		event.LeaseOwner = ""
		event.LeaseExpiresAt = nil
		deliveredAt := input.At
		event.DeliveredAt = &deliveredAt
		s.events[id] = event
		updated++
	}
	return updated, nil
}

func (s *stubSecurityAuditOutboxStore) FailSecurityAuditOutbox(input managedagents.FailSecurityAuditOutboxInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := 0
	for _, id := range input.IDs {
		event := s.events[id]
		if event.Status != managedagents.SecurityAuditOutboxDelivering || event.LeaseOwner != input.LeaseOwner {
			continue
		}
		event.Status = managedagents.SecurityAuditOutboxPending
		if input.DeadLetter {
			event.Status = managedagents.SecurityAuditOutboxDeadLetter
		}
		event.NextAttemptAt = input.NextAttemptAt
		event.LeaseOwner = ""
		event.LeaseExpiresAt = nil
		event.LastError = input.ErrorMessage
		event.UpdatedAt = input.At
		s.events[id] = event
		updated++
	}
	return updated, nil
}

func (s *stubSecurityAuditOutboxStore) ReplaySecurityAuditDeadLetters(input managedagents.ReplaySecurityAuditDeadLettersInput) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updated := 0
	for _, id := range s.order {
		event := s.events[id]
		if event.Status != managedagents.SecurityAuditOutboxDeadLetter || event.UpdatedAt.After(input.Before) || updated >= input.Limit {
			continue
		}
		event.Status = managedagents.SecurityAuditOutboxPending
		event.AttemptCount = 0
		event.NextAttemptAt = time.Now()
		event.LastError = ""
		s.events[id] = event
		updated++
	}
	return updated, nil
}

func (s *stubSecurityAuditOutboxStore) GetSecurityAuditOutboxStats(now time.Time) (managedagents.SecurityAuditOutboxStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := managedagents.SecurityAuditOutboxStats{}
	for _, event := range s.events {
		switch event.Status {
		case managedagents.SecurityAuditOutboxPending:
			stats.Pending++
		case managedagents.SecurityAuditOutboxDelivering:
			stats.Delivering++
		case managedagents.SecurityAuditOutboxDelivered:
			stats.Delivered++
		case managedagents.SecurityAuditOutboxDeadLetter:
			stats.DeadLetter++
		}
	}
	return stats, nil
}

func (s *stubSecurityAuditOutboxStore) ListSecurityAuditIntegrityKeyStats() ([]managedagents.SecurityAuditIntegrityKeyStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listKeyStatsError != nil {
		return nil, s.listKeyStatsError
	}
	byID := map[string]managedagents.SecurityAuditIntegrityKeyStats{}
	for _, event := range s.events {
		if event.IntegrityAlgorithm != "hmac-sha256" {
			continue
		}
		stats := byID[event.IntegrityKeyID]
		stats.KeyID = event.IntegrityKeyID
		switch event.Status {
		case managedagents.SecurityAuditOutboxPending:
			stats.Pending++
		case managedagents.SecurityAuditOutboxDelivering:
			stats.Delivering++
		case managedagents.SecurityAuditOutboxDelivered:
			stats.Delivered++
		case managedagents.SecurityAuditOutboxDeadLetter:
			stats.DeadLetter++
		}
		byID[event.IntegrityKeyID] = stats
	}
	result := make([]managedagents.SecurityAuditIntegrityKeyStats, 0, len(byID))
	for _, stats := range byID {
		result = append(result, stats)
	}
	return result, nil
}

func (s *stubSecurityAuditOutboxStore) PruneDeliveredSecurityAuditOutbox(before time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for _, id := range s.order {
		event := s.events[id]
		if event.Status == managedagents.SecurityAuditOutboxDelivered && event.DeliveredAt != nil && event.DeliveredAt.Before(before) && deleted < limit {
			delete(s.events, id)
			deleted++
		}
	}
	return deleted, nil
}

func (s *stubSecurityAuditOutboxStore) first(t *testing.T) managedagents.SecurityAuditOutboxEvent {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		t.Fatal("security audit outbox is empty")
	}
	return s.events[s.order[0]]
}

func securityAuditIntegrityKeyStatesByID(states []SecurityAuditIntegrityKeyState) map[string]SecurityAuditIntegrityKeyState {
	result := make(map[string]SecurityAuditIntegrityKeyState, len(states))
	for _, state := range states {
		result[state.KeyID] = state
	}
	return result
}
