package runner

import (
	"context"
	"testing"
	"time"

	"tiggy-manage-agent/internal/tools"
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

func TestAgentCoreToolProgressPublishesTransientLiveEvent(t *testing.T) {
	t.Parallel()

	broker := NewLiveEventBroker(2)
	events, cancel, err := broker.SubscribeLiveEvents("session")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	executionContext := agentCoreToolExecutionContext(tools.ExecutionContext{}, broker, "session", "turn")
	executionContext.Progress(context.Background(), tools.ToolProgress{
		CallID: "call_1", Tool: "default.run_command", Index: 2, ToolRound: 3,
		Stage: "running", Message: "Installing dependencies", Percent: 40,
	})
	select {
	case event := <-events:
		if event.Type != LiveEventToolProgress || event.Operation != "update" || event.ContentFormat != "text" ||
			event.CallID != "call_1" || event.Tool != "default.run_command" || event.Stage != "running" ||
			event.Percent != 40 || event.Index != 2 || event.ToolRound != 3 || event.Text != "Installing dependencies" {
			t.Fatalf("live progress event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tool progress")
	}
}
