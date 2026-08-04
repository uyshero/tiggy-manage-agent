package tma

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const EventEnvelopeSchemaV1 = "tma.event.v1"

type EventSubscription struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	AppID         string    `json:"app_id"`
	Name          string    `json:"name"`
	EndpointURL   string    `json:"endpoint_url"`
	EventTypes    []string  `json:"event_types"`
	Status        string    `json:"status"`
	SecretVersion int       `json:"secret_version"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CreateEventSubscriptionRequest struct {
	AppID       string   `json:"app_id,omitempty"`
	Name        string   `json:"name"`
	EndpointURL string   `json:"endpoint_url"`
	EventTypes  []string `json:"event_types"`
}

type UpdateEventSubscriptionRequest struct {
	Name        string   `json:"name"`
	EndpointURL string   `json:"endpoint_url"`
	EventTypes  []string `json:"event_types"`
	Status      string   `json:"status"`
}

type CreatedEventSubscription struct {
	Subscription EventSubscription `json:"subscription"`
	Secret       string            `json:"secret"`
}

type EventEnvelope struct {
	Schema      string          `json:"schema"`
	EventID     string          `json:"event_id"`
	Type        string          `json:"type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	WorkspaceID string          `json:"workspace_id"`
	AppID       string          `json:"app_id"`
	Data        json.RawMessage `json:"data"`
}

type EventDelivery struct {
	ID             string        `json:"id"`
	WorkspaceID    string        `json:"workspace_id"`
	SubscriptionID string        `json:"subscription_id"`
	AppID          string        `json:"app_id"`
	SourceEventID  string        `json:"source_event_id"`
	EventType      string        `json:"event_type"`
	Payload        EventEnvelope `json:"payload"`
	SecretVersion  int           `json:"secret_version"`
	Status         string        `json:"status"`
	AttemptCount   int           `json:"attempt_count"`
	NextAttemptAt  time.Time     `json:"next_attempt_at"`
	LeaseExpiresAt *time.Time    `json:"lease_expires_at,omitempty"`
	LastHTTPStatus int           `json:"last_http_status,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	DeliveredAt    *time.Time    `json:"delivered_at,omitempty"`
}

type EventDeliveryQuery struct {
	Status string
	Limit  int
}

type EventSubscriptionsService struct{ client *Client }

func (s *EventSubscriptionsService) EventTypes(ctx context.Context) ([]string, error) {
	var response struct {
		Items []string `json:"items"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/event-subscriptions/event-types", nil, &response)
	return response.Items, err
}

func (s *EventSubscriptionsService) List(ctx context.Context, appID string) ([]EventSubscription, error) {
	values := url.Values{}
	setStringQuery(values, "app_id", appID)
	var response struct {
		Items []EventSubscription `json:"items"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, pathWithQuery("/v2/event-subscriptions", values), nil, &response)
	return response.Items, err
}

func (s *EventSubscriptionsService) Create(ctx context.Context, request CreateEventSubscriptionRequest) (CreatedEventSubscription, error) {
	var result CreatedEventSubscription
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/event-subscriptions", request, &result)
	return result, err
}

func (s *EventSubscriptionsService) Get(ctx context.Context, subscriptionID string) (EventSubscription, error) {
	var result EventSubscription
	err := s.client.DoJSON(ctx, http.MethodGet, eventSubscriptionPath(subscriptionID), nil, &result)
	return result, err
}

func (s *EventSubscriptionsService) Update(ctx context.Context, subscriptionID string, request UpdateEventSubscriptionRequest) (EventSubscription, error) {
	var result EventSubscription
	err := s.client.DoJSON(ctx, http.MethodPatch, eventSubscriptionPath(subscriptionID), request, &result)
	return result, err
}

func (s *EventSubscriptionsService) Disable(ctx context.Context, subscriptionID string) (EventSubscription, error) {
	var result EventSubscription
	err := s.client.DoJSON(ctx, http.MethodDelete, eventSubscriptionPath(subscriptionID), nil, &result)
	return result, err
}

func (s *EventSubscriptionsService) RotateSecret(ctx context.Context, subscriptionID string) (CreatedEventSubscription, error) {
	var result CreatedEventSubscription
	err := s.client.DoJSON(ctx, http.MethodPost, eventSubscriptionPath(subscriptionID)+"/rotate-secret", nil, &result)
	return result, err
}

func (s *EventSubscriptionsService) Deliveries(ctx context.Context, subscriptionID string, query EventDeliveryQuery) ([]EventDelivery, error) {
	values := url.Values{}
	setStringQuery(values, "status", query.Status)
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	var response struct {
		Items []EventDelivery `json:"items"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, pathWithQuery(eventSubscriptionPath(subscriptionID)+"/deliveries", values), nil, &response)
	return response.Items, err
}

func (s *EventSubscriptionsService) Replay(ctx context.Context, subscriptionID, deliveryID string) (EventDelivery, error) {
	var result EventDelivery
	path := eventSubscriptionPath(subscriptionID) + "/deliveries/" + url.PathEscape(deliveryID) + "/replay"
	err := s.client.DoJSON(ctx, http.MethodPost, path, nil, &result)
	return result, err
}

func eventSubscriptionPath(subscriptionID string) string {
	return "/v2/event-subscriptions/" + url.PathEscape(subscriptionID)
}
