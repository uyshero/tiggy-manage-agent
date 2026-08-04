package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"tiggy-manage-agent/internal/eventsubscription"
	"tiggy-manage-agent/internal/managedagents"
)

var errEventSubscriptionsUnavailable = errors.New("event subscriptions are unavailable")

type createEventSubscriptionRequest struct {
	AppID       string   `json:"app_id,omitempty"`
	Name        string   `json:"name"`
	EndpointURL string   `json:"endpoint_url"`
	EventTypes  []string `json:"event_types"`
}

type updateEventSubscriptionRequest struct {
	Name        string   `json:"name"`
	EndpointURL string   `json:"endpoint_url"`
	EventTypes  []string `json:"event_types"`
	Status      string   `json:"status"`
}

type eventSubscriptionSecretResponse struct {
	Subscription eventsubscription.Subscription `json:"subscription"`
	Secret       string                         `json:"secret"`
}

func (s *Server) registerEventSubscriptionRoutes() {
	s.mux.HandleFunc("GET /v2/event-subscriptions/event-types", s.withV2Request(s.listEventSubscriptionTypes))
	s.mux.HandleFunc("GET /v2/event-subscriptions", s.withV2Request(s.listEventSubscriptions))
	s.mux.HandleFunc("POST /v2/event-subscriptions", s.withV2Request(s.createEventSubscription))
	s.mux.HandleFunc("GET /v2/event-subscriptions/{subscription_id}", s.withV2Request(s.getEventSubscription))
	s.mux.HandleFunc("PATCH /v2/event-subscriptions/{subscription_id}", s.withV2Request(s.updateEventSubscription))
	s.mux.HandleFunc("DELETE /v2/event-subscriptions/{subscription_id}", s.withV2Request(s.disableEventSubscription))
	s.mux.HandleFunc("POST /v2/event-subscriptions/{subscription_id}/rotate-secret", s.withV2Request(s.rotateEventSubscriptionSecret))
	s.mux.HandleFunc("GET /v2/event-subscriptions/{subscription_id}/deliveries", s.withV2Request(s.listEventDeliveries))
	s.mux.HandleFunc("POST /v2/event-subscriptions/{subscription_id}/deliveries/{delivery_id}/replay", s.withV2Request(s.replayEventDelivery))
}

func (s *Server) eventSubscriptionStore() (eventsubscription.Store, error) {
	store, ok := s.store.(eventsubscription.Store)
	if !ok {
		return nil, errEventSubscriptionsUnavailable
	}
	return store, nil
}

func writeEventSubscriptionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errEventSubscriptionsUnavailable) {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "event_subscriptions_unavailable", err.Error(), false, nil)
		return
	}
	writeV2ManagedError(w, r, err)
}

func (s *Server) eventSubscriptionPrincipal(r *http.Request, requestedAppID string) (Principal, string, error) {
	principal, ok := s.administrationPrincipal(r)
	if !ok && principal.Subject == "" {
		return Principal{}, "", managedagents.ErrForbidden
	}
	if principal.AuthType == AuthTypeDelegated {
		return Principal{}, "", fmt.Errorf("%w: delegated tokens cannot manage event subscriptions", managedagents.ErrForbidden)
	}
	requestedAppID = strings.TrimSpace(requestedAppID)
	if principal.ServiceIdentityID != "" {
		if requestedAppID != "" && requestedAppID != principal.ServiceIdentityID {
			return Principal{}, "", fmt.Errorf("%w: application credentials cannot manage another app subscription", managedagents.ErrForbidden)
		}
		return principal, principal.ServiceIdentityID, nil
	}
	allowed, err := s.principalIsWorkspaceAdmin(r, principal)
	if err != nil {
		return Principal{}, "", err
	}
	if !allowed {
		return Principal{}, "", fmt.Errorf("%w: workspace admin role required", managedagents.ErrForbidden)
	}
	return principal, requestedAppID, nil
}

func (s *Server) validateWebhookTarget(r *http.Request, target string) error {
	if len(s.webhookSigningKey) < 32 {
		return fmt.Errorf("%w: webhook signing key is not configured", managedagents.ErrInvalid)
	}
	if s.webhookEgress == nil {
		return fmt.Errorf("%w: webhook egress policy is unavailable", managedagents.ErrInvalid)
	}
	if err := s.webhookEgress.ValidateURL(r.Context(), target); err != nil {
		return fmt.Errorf("%w: webhook endpoint rejected by egress policy: %v", managedagents.ErrInvalid, err)
	}
	return nil
}

func (s *Server) listEventSubscriptionTypes(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.eventSubscriptionPrincipal(r, r.URL.Query().Get("app_id")); err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": eventsubscription.SupportedEventTypes})
}

func (s *Server) listEventSubscriptions(w http.ResponseWriter, r *http.Request) {
	principal, appID, err := s.eventSubscriptionPrincipal(r, r.URL.Query().Get("app_id"))
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	store, err := s.eventSubscriptionStore()
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	items, err := store.ListEventSubscriptions(r.Context(), principal.WorkspaceID, appID)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createEventSubscription(w http.ResponseWriter, r *http.Request) {
	var request createEventSubscriptionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
		return
	}
	principal, appID, err := s.eventSubscriptionPrincipal(r, request.AppID)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	if appID == "" {
		writeV2ManagedError(w, r, fmt.Errorf("%w: app_id is required", managedagents.ErrInvalid))
		return
	}
	if err := s.validateWebhookTarget(r, request.EndpointURL); err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	store, err := s.eventSubscriptionStore()
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	item, err := store.CreateEventSubscription(r.Context(), eventsubscription.CreateSubscriptionInput{
		WorkspaceID: principal.WorkspaceID, AppID: appID, Name: request.Name,
		EndpointURL: request.EndpointURL, EventTypes: request.EventTypes,
		CreatedBy: requestActorID(r, principal.Subject),
	})
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	secret, err := eventsubscription.DeriveSecret(s.webhookSigningKey, item.ID, item.SecretVersion)
	if err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusCreated, eventSubscriptionSecretResponse{Subscription: item, Secret: secret})
}

func (s *Server) getEventSubscription(w http.ResponseWriter, r *http.Request) {
	item, _, err := s.authorizedEventSubscription(r)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateEventSubscription(w http.ResponseWriter, r *http.Request) {
	current, principal, err := s.authorizedEventSubscription(r)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	var request updateEventSubscriptionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
		return
	}
	if err := s.validateWebhookTarget(r, request.EndpointURL); err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	store, _ := s.eventSubscriptionStore()
	item, err := store.UpdateEventSubscription(r.Context(), eventsubscription.UpdateSubscriptionInput{
		WorkspaceID: principal.WorkspaceID, ID: current.ID, AppID: current.AppID,
		Name: request.Name, EndpointURL: request.EndpointURL, EventTypes: request.EventTypes, Status: request.Status,
	})
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) disableEventSubscription(w http.ResponseWriter, r *http.Request) {
	current, principal, err := s.authorizedEventSubscription(r)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	store, _ := s.eventSubscriptionStore()
	item, err := store.UpdateEventSubscription(r.Context(), eventsubscription.UpdateSubscriptionInput{
		WorkspaceID: principal.WorkspaceID, ID: current.ID, AppID: current.AppID,
		Name: current.Name, EndpointURL: current.EndpointURL, EventTypes: current.EventTypes,
		Status: eventsubscription.SubscriptionStatusDisabled,
	})
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) rotateEventSubscriptionSecret(w http.ResponseWriter, r *http.Request) {
	current, principal, err := s.authorizedEventSubscription(r)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	store, _ := s.eventSubscriptionStore()
	item, err := store.RotateEventSubscriptionSecret(r.Context(), principal.WorkspaceID, current.ID)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	secret, err := eventsubscription.DeriveSecret(s.webhookSigningKey, item.ID, item.SecretVersion)
	if err != nil {
		writeV2ManagedError(w, r, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, eventSubscriptionSecretResponse{Subscription: item, Secret: secret})
}

func (s *Server) listEventDeliveries(w http.ResponseWriter, r *http.Request) {
	current, principal, err := s.authorizedEventSubscription(r)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	store, _ := s.eventSubscriptionStore()
	items, err := store.ListEventDeliveries(r.Context(), eventsubscription.ListDeliveriesInput{
		WorkspaceID: principal.WorkspaceID, SubscriptionID: current.ID,
		Status: r.URL.Query().Get("status"), Limit: limit,
	})
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) replayEventDelivery(w http.ResponseWriter, r *http.Request) {
	current, principal, err := s.authorizedEventSubscription(r)
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	store, _ := s.eventSubscriptionStore()
	item, err := store.ReplayEventDelivery(r.Context(), principal.WorkspaceID, current.ID, r.PathValue("delivery_id"))
	if err != nil {
		writeEventSubscriptionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) authorizedEventSubscription(r *http.Request) (eventsubscription.Subscription, Principal, error) {
	principal, _, err := s.eventSubscriptionPrincipal(r, "")
	if err != nil {
		return eventsubscription.Subscription{}, Principal{}, err
	}
	store, err := s.eventSubscriptionStore()
	if err != nil {
		return eventsubscription.Subscription{}, Principal{}, err
	}
	item, err := store.GetEventSubscription(r.Context(), principal.WorkspaceID, r.PathValue("subscription_id"))
	if err != nil {
		return eventsubscription.Subscription{}, Principal{}, err
	}
	if principal.ServiceIdentityID != "" && item.AppID != principal.ServiceIdentityID {
		return eventsubscription.Subscription{}, Principal{}, managedagents.ErrNotFound
	}
	return item, principal, nil
}
