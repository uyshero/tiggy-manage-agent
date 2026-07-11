package managedagents

import "testing"

func TestEventHubCoalescesWakeups(t *testing.T) {
	hub := newEventHub()
	wake, cancel := hub.subscribe("session-1")
	defer cancel()

	for range 64 {
		hub.publish(Event{SessionID: "session-1"})
	}

	select {
	case <-wake:
	default:
		t.Fatal("expected a wakeup after publishing events")
	}
	select {
	case <-wake:
		t.Fatal("expected burst wakeups to be coalesced")
	default:
	}

	hub.publish(Event{SessionID: "session-1"})
	select {
	case <-wake:
	default:
		t.Fatal("expected a new wakeup after draining the previous one")
	}
}

func TestEventHubCancelAfterSessionCloseIsSafe(t *testing.T) {
	hub := newEventHub()
	_, cancel := hub.subscribe("session-1")

	hub.closeSession("session-1")
	cancel()
	cancel()
}
