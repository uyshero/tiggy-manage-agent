package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/runner"
	tmasdk "tiggy-manage-agent/sdk/tma"
)

func TestPostgresV2EventStreamRecoversAcrossServerInstances(t *testing.T) {
	if os.Getenv("TMA_RUN_POSTGRES_TESTS") != "1" {
		t.Skip("set TMA_RUN_POSTGRES_TESTS=1 to run PostgreSQL integration tests")
	}
	databaseURL := os.Getenv("TMA_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TMA_DATABASE_URL to run PostgreSQL integration tests")
	}

	storeA, err := managedagents.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("open Server A store: %v", err)
	}
	defer storeA.Close()
	storeB, err := managedagents.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("open Server B store: %v", err)
	}
	defer storeB.Close()

	serverA := httptest.NewServer(NewServerWithStoreAndRunner(storeA, runner.NewMockRunner(storeA, 10*time.Millisecond, nil), nil))
	defer serverA.Close()
	serverB := httptest.NewServer(NewServerWithStoreAndRunner(storeB, runner.NewMockRunner(storeB, 10*time.Millisecond, nil), nil))
	defer serverB.Close()
	clientA, err := tmasdk.NewClient(serverA.URL)
	if err != nil {
		t.Fatal(err)
	}
	clientB, err := tmasdk.NewClient(serverB.URL)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	if _, err := storeA.UpsertLLMModel(managedagents.UpsertLLMModelInput{
		ProviderID: "fake", Model: "v2-multi-server-model", ContextWindowTokens: managedagents.DefaultContextWindowTokens,
	}); err != nil {
		t.Fatalf("ensure integration model: %v", err)
	}
	agent, err := storeA.CreateAgent(managedagents.CreateAgentInput{
		Name: "v2-multi-server-agent-" + suffix, LLMProvider: "fake", LLMModel: "v2-multi-server-model",
	})
	if err != nil {
		t.Fatalf("create integration agent: %v", err)
	}
	environment, err := storeA.CreateEnvironment(managedagents.CreateEnvironmentInput{
		Name: "v2-multi-server-environment-" + suffix, Config: json.RawMessage(`{"type":"integration"}`),
	})
	if err != nil {
		t.Fatalf("create integration environment: %v", err)
	}
	session, err := storeA.CreateSession(managedagents.CreateSessionInput{
		AgentID: agent.ID, EnvironmentID: environment.ID, Title: "multi-server SSE", CreatedBy: "integration-test",
	})
	if err != nil {
		t.Fatalf("create integration session: %v", err)
	}
	defer storeA.DeleteSession(session.ID)

	history, err := clientA.Sessions.ListEvents(ctx, session.ID, 0)
	if err != nil || len(history) == 0 {
		t.Fatalf("read initial Session history through Server A: events=%+v err=%v", history, err)
	}
	streamA, err := clientA.Sessions.Events(ctx, session.ID, history[len(history)-1].Seq)
	if err != nil {
		t.Fatalf("open stream through Server A: %v", err)
	}
	firstResult := make(chan struct {
		event tmasdk.Event
		err   error
	}, 1)
	go func() {
		event, nextErr := streamA.Next(ctx)
		firstResult <- struct {
			event tmasdk.Event
			err   error
		}{event: event, err: nextErr}
	}()

	firstWrite, err := clientB.Sessions.AppendEvents(ctx, session.ID, tmasdk.AppendEventsRequest{Events: []tmasdk.AppendEvent{{
		Type: "runtime.thinking", Payload: json.RawMessage(`{"message":"from Server B"}`),
	}}})
	if err != nil || len(firstWrite.Events) != 1 {
		t.Fatalf("append first event through Server B: result=%+v err=%v", firstWrite, err)
	}
	first := <-firstResult
	if first.err != nil || first.event.ID != firstWrite.Events[0].ID {
		t.Fatalf("Server A did not stream Server B event: event=%+v err=%v", first.event, first.err)
	}
	if err := streamA.Close(); err != nil {
		t.Fatalf("disconnect Server A stream: %v", err)
	}

	disconnectedWrite, err := clientB.Sessions.AppendEvents(ctx, session.ID, tmasdk.AppendEventsRequest{Events: []tmasdk.AppendEvent{
		{Type: "runtime.thinking", Payload: json.RawMessage(`{"message":"disconnected 1"}`)},
		{Type: "runtime.thinking", Payload: json.RawMessage(`{"message":"disconnected 2"}`)},
	}})
	if err != nil || len(disconnectedWrite.Events) != 2 {
		t.Fatalf("append while SDK is disconnected: result=%+v err=%v", disconnectedWrite, err)
	}

	resumed, err := clientA.Sessions.Events(ctx, session.ID, first.event.Seq)
	if err != nil {
		t.Fatalf("resume stream through Server A: %v", err)
	}
	defer resumed.Close()
	for index, want := range disconnectedWrite.Events {
		got, nextErr := resumed.Next(ctx)
		if nextErr != nil {
			t.Fatalf("resume event %d: %v", index, nextErr)
		}
		if got.ID != want.ID || got.Seq != want.Seq {
			t.Fatalf("resume event %d mismatch: got=%+v want=%+v", index, got, want)
		}
	}
}
