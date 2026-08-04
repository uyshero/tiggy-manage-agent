package tma

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEventSubscriptionsService(t *testing.T) {
	requests := []string{
		"POST /v2/event-subscriptions",
		"GET /v2/event-subscriptions?app_id=svc%2Fapp",
		"GET /v2/event-subscriptions/esub%2F1/deliveries?limit=25&status=dead_letter",
		"POST /v2/event-subscriptions/esub%2F1/deliveries/edel%2F1/replay",
	}
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual := r.Method + " " + r.URL.RequestURI()
		if call >= len(requests) || actual != requests[call] {
			t.Fatalf("request %d = %s, want %s", call, actual, requests[call])
		}
		call++
		w.Header().Set("Content-Type", "application/json")
		switch call {
		case 1:
			fmt.Fprint(w, `{"subscription":{"id":"esub/1","workspace_id":"wksp_1","app_id":"svc/app","name":"primary","endpoint_url":"https://app.example/events","event_types":["run.completed"],"status":"active","secret_version":1,"created_by":"test","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},"secret":"whsec_once"}`)
		case 2:
			fmt.Fprint(w, `{"items":[]}`)
		case 3:
			fmt.Fprint(w, `{"items":[]}`)
		case 4:
			fmt.Fprint(w, `{}`)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.EventSubscriptions.Create(t.Context(), CreateEventSubscriptionRequest{
		AppID: "svc/app", Name: "primary", EndpointURL: "https://app.example/events", EventTypes: []string{"run.completed"},
	})
	if err != nil || created.Secret != "whsec_once" {
		t.Fatalf("create result = %+v err=%v", created, err)
	}
	if _, err := client.EventSubscriptions.List(t.Context(), "svc/app"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EventSubscriptions.Deliveries(t.Context(), "esub/1", EventDeliveryQuery{Status: "dead_letter", Limit: 25}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EventSubscriptions.Replay(t.Context(), "esub/1", "edel/1"); err != nil {
		t.Fatal(err)
	}
}
