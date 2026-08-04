package eventsubscription

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	EnvelopeSchema = "tma.event.v1"

	EventRunCompleted         = "run.completed"
	EventRunFailed            = "run.failed"
	EventInterventionRequired = "intervention.required"
	EventArtifactCreated      = "artifact.created"

	SubscriptionStatusActive   = "active"
	SubscriptionStatusDisabled = "disabled"

	DeliveryStatusPending    = "pending"
	DeliveryStatusDelivering = "delivering"
	DeliveryStatusDelivered  = "delivered"
	DeliveryStatusDeadLetter = "dead_letter"
)

var SupportedEventTypes = []string{
	EventArtifactCreated,
	EventInterventionRequired,
	EventRunCompleted,
	EventRunFailed,
}

type Subscription struct {
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

type CreateSubscriptionInput struct {
	WorkspaceID string
	AppID       string
	Name        string
	EndpointURL string
	EventTypes  []string
	CreatedBy   string
}

type UpdateSubscriptionInput struct {
	WorkspaceID string
	ID          string
	AppID       string
	Name        string
	EndpointURL string
	EventTypes  []string
	Status      string
}

type Delivery struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	SubscriptionID string          `json:"subscription_id"`
	AppID          string          `json:"app_id"`
	SourceEventID  string          `json:"source_event_id"`
	EventType      string          `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	EndpointURL    string          `json:"-"`
	SecretVersion  int             `json:"secret_version"`
	Status         string          `json:"status"`
	AttemptCount   int             `json:"attempt_count"`
	NextAttemptAt  time.Time       `json:"next_attempt_at"`
	LeaseOwner     string          `json:"-"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	LastHTTPStatus int             `json:"last_http_status,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	DeliveredAt    *time.Time      `json:"delivered_at,omitempty"`
}

type ClaimInput struct {
	LeaseOwner    string
	LeaseDuration time.Duration
	MaxAttempts   int
	Limit         int
	Now           time.Time
}

type CompleteInput struct {
	DeliveryID string
	LeaseOwner string
	HTTPStatus int
	Now        time.Time
}

type FailInput struct {
	DeliveryID string
	LeaseOwner string
	HTTPStatus int
	Error      string
	RetryAt    time.Time
	DeadLetter bool
	Now        time.Time
}

type ListDeliveriesInput struct {
	WorkspaceID    string
	SubscriptionID string
	Status         string
	Limit          int
}

type Store interface {
	ListEventSubscriptions(context.Context, string, string) ([]Subscription, error)
	GetEventSubscription(context.Context, string, string) (Subscription, error)
	CreateEventSubscription(context.Context, CreateSubscriptionInput) (Subscription, error)
	UpdateEventSubscription(context.Context, UpdateSubscriptionInput) (Subscription, error)
	RotateEventSubscriptionSecret(context.Context, string, string) (Subscription, error)
	ListEventDeliveries(context.Context, ListDeliveriesInput) ([]Delivery, error)
	ReplayEventDelivery(context.Context, string, string, string) (Delivery, error)
	DeliveryStore
}

type DeliveryStore interface {
	ClaimEventDeliveries(ClaimInput) ([]Delivery, error)
	CompleteEventDelivery(CompleteInput) (bool, error)
	FailEventDelivery(FailInput) (bool, error)
}

func NormalizeEventTypes(values []string) ([]string, error) {
	allowed := make(map[string]struct{}, len(SupportedEventTypes))
	for _, value := range SupportedEventTypes {
		allowed[value] = struct{}{}
	}
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowed[value]; !ok {
			return nil, fmt.Errorf("unsupported event type %q", value)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("at least one event type is required")
	}
	sort.Strings(normalized)
	return normalized, nil
}

func DeriveSecret(masterKey []byte, subscriptionID string, version int) (string, error) {
	if len(masterKey) < 32 {
		return "", fmt.Errorf("webhook signing key must contain at least 32 bytes")
	}
	if strings.TrimSpace(subscriptionID) == "" || version < 1 {
		return "", fmt.Errorf("subscription id and positive secret version are required")
	}
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("tma.webhook.secret.v1\x00" + subscriptionID + "\x00" + strconv.Itoa(version)))
	return "whsec_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func Signature(secret string, timestamp time.Time, deliveryID string, payload []byte) string {
	message := strconv.FormatInt(timestamp.UTC().Unix(), 10) + "." + deliveryID + "." + string(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}
