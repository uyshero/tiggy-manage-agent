package runner

import (
	"testing"
	"time"
)

func TestLiveEventBrokerScopesEventsAndDoesNotReplay(t *testing.T) {
	broker := NewLiveEventBroker(2)
	alpha, cancelAlpha, err := broker.SubscribeLiveEvents("session-alpha")
	if err != nil {
		t.Fatalf("subscribe alpha: %v", err)
	}
	defer cancelAlpha()
	beta, cancelBeta, err := broker.SubscribeLiveEvents("session-beta")
	if err != nil {
		t.Fatalf("subscribe beta: %v", err)
	}
	defer cancelBeta()

	broker.Publish(LiveEvent{SessionID: "session-alpha", TurnID: "turn-1", Type: LiveEventLLMText, Text: "hello"})
	select {
	case event := <-alpha:
		if event.Text != "hello" || event.StreamSeq == 0 || event.CreatedAt.IsZero() {
			t.Fatalf("unexpected live event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for alpha event")
	}
	select {
	case event := <-beta:
		t.Fatalf("cross-session event leaked: %#v", event)
	default:
	}

	late, cancelLate, err := broker.SubscribeLiveEvents("session-alpha")
	if err != nil {
		t.Fatalf("subscribe late: %v", err)
	}
	defer cancelLate()
	select {
	case event := <-late:
		t.Fatalf("transient event was replayed: %#v", event)
	default:
	}
}

func TestLiveEventBrokerDropsForSlowSubscriberWithoutBlocking(t *testing.T) {
	broker := NewLiveEventBroker(1)
	events, cancel, err := broker.SubscribeLiveEvents("session")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer cancel()

	broker.Publish(LiveEvent{SessionID: "session", TurnID: "turn", Type: LiveEventLLMText, Text: "first"})
	broker.Publish(LiveEvent{SessionID: "session", TurnID: "turn", Type: LiveEventLLMText, Text: "dropped"})
	if event := <-events; event.Text != "first" {
		t.Fatalf("unexpected buffered event: %#v", event)
	}
}
