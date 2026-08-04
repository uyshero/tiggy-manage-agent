package eventsubscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type workerTestStore struct {
	deliveries []Delivery
	completed  []CompleteInput
	failed     []FailInput
}

func (s *workerTestStore) ClaimEventDeliveries(ClaimInput) ([]Delivery, error) {
	items := s.deliveries
	s.deliveries = nil
	return items, nil
}

func (s *workerTestStore) CompleteEventDelivery(input CompleteInput) (bool, error) {
	s.completed = append(s.completed, input)
	return true, nil
}

func (s *workerTestStore) FailEventDelivery(input FailInput) (bool, error) {
	s.failed = append(s.failed, input)
	return true, nil
}

func TestWorkerDeliversStableSignedPayload(t *testing.T) {
	masterKey := []byte("event-webhook-test-signing-key-at-least-32-bytes")
	payload := []byte(`{"schema":"tma.event.v1","event_id":"evt_1","type":"run.completed"}`)
	store := &workerTestStore{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TMA-Delivery-ID") != "edel_1" || r.Header.Get("X-TMA-Event") != EventRunCompleted {
			t.Fatalf("unexpected delivery headers: %v", r.Header)
		}
		timestampValue, err := strconv.ParseInt(r.Header.Get("X-TMA-Timestamp"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		secret, err := DeriveSecret(masterKey, "esub_1", 2)
		if err != nil {
			t.Fatal(err)
		}
		expected := Signature(secret, time.Unix(timestampValue, 0), "edel_1", payload)
		if r.Header.Get("X-TMA-Signature") != expected || r.Header.Get("X-TMA-Secret-Version") != "2" {
			t.Fatalf("unexpected signature headers: %v", r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()
	store.deliveries = []Delivery{{
		ID: "edel_1", SubscriptionID: "esub_1", EventType: EventRunCompleted,
		Payload: payload, EndpointURL: endpoint.URL, SecretVersion: 2, AttemptCount: 1,
	}}
	worker, err := NewWorker(WorkerConfig{Store: store, SigningKey: masterKey, HTTPClient: endpoint.Client()})
	if err != nil {
		t.Fatal(err)
	}
	worker.deliverBatch(context.Background())
	if len(store.completed) != 1 || len(store.failed) != 0 || store.completed[0].DeliveryID != "edel_1" {
		t.Fatalf("unexpected delivery result: completed=%+v failed=%+v", store.completed, store.failed)
	}
}

func TestWorkerRetriesAndDeadLettersGone(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer endpoint.Close()
	store := &workerTestStore{deliveries: []Delivery{{
		ID: "edel_gone", SubscriptionID: "esub_1", EventType: EventRunFailed,
		Payload: []byte(`{"type":"run.failed"}`), EndpointURL: endpoint.URL, SecretVersion: 1, AttemptCount: 1,
	}}}
	worker, err := NewWorker(WorkerConfig{
		Store: store, SigningKey: []byte("event-webhook-test-signing-key-at-least-32-bytes"), HTTPClient: endpoint.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.deliverBatch(context.Background())
	if len(store.failed) != 1 || !store.failed[0].DeadLetter || store.failed[0].HTTPStatus != http.StatusGone {
		t.Fatalf("unexpected failed delivery: %+v", store.failed)
	}
}
