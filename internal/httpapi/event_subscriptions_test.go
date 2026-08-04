package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/eventsubscription"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
)

type eventSubscriptionHTTPTestStore struct {
	*serviceIdentityHTTPTestStore
	subscriptions map[string]eventsubscription.Subscription
	nextID        int
}

func newEventSubscriptionHTTPTestServer(t *testing.T) (http.Handler, *eventSubscriptionHTTPTestStore, string) {
	t.Helper()
	workspaceID := "wksp_event_subscription"
	adminSubject := "event-admin"
	store := &eventSubscriptionHTTPTestStore{
		serviceIdentityHTTPTestStore: &serviceIdentityHTTPTestStore{
			tenantAdministrationTestStore: &tenantAdministrationTestStore{
				testStore: newTestStore(),
				memberships: map[string]managedagents.WorkspaceMembership{
					workspaceMembershipTestKey(workspaceID, adminSubject): {
						WorkspaceID: workspaceID, Subject: adminSubject,
						Role: managedagents.WorkspaceRoleAdmin, Status: "active",
					},
				},
			},
			identities:  make(map[string]managedagents.ServiceIdentity),
			credentials: make(map[string]serviceIdentityHTTPTestCredential),
		},
		subscriptions: make(map[string]eventsubscription.Subscription),
	}
	auth := AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
		DelegationSigningSecret: "test-delegation-secret-with-at-least-32-bytes", DelegationIssuer: "https://platform.example",
		DelegationAudience: "tma-platform-api", DelegationTTL: 5 * time.Minute,
		WebhookSigningKey: "event-webhook-test-key-at-least-32-bytes", WebhookAllowHTTP: true, WebhookAllowPrivate: true,
		WebhookAllowedCIDRs: []string{"127.0.0.0/8"},
	}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
		store, runner.NewMockRunner(store, time.Millisecond, nil), nil, "fake", "fake-demo",
		objectstore.NewNoopClient(objectstore.Config{}), defaultExecutionResolver(store), "worker-secret", "legacy-control-secret", auth, defaultSubagentPolicy(), nil,
	)
	return server, store, signedTestJWT(t, adminSubject, workspaceID, adminSubject, []string{RoleAdmin}, nil)
}

func (s *eventSubscriptionHTTPTestStore) ListEventSubscriptions(_ context.Context, workspaceID, appID string) ([]eventsubscription.Subscription, error) {
	items := make([]eventsubscription.Subscription, 0)
	for _, item := range s.subscriptions {
		if item.WorkspaceID == workspaceID && (appID == "" || item.AppID == appID) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *eventSubscriptionHTTPTestStore) GetEventSubscription(_ context.Context, workspaceID, id string) (eventsubscription.Subscription, error) {
	item, ok := s.subscriptions[id]
	if !ok || item.WorkspaceID != workspaceID {
		return eventsubscription.Subscription{}, managedagents.ErrNotFound
	}
	return item, nil
}

func (s *eventSubscriptionHTTPTestStore) CreateEventSubscription(_ context.Context, input eventsubscription.CreateSubscriptionInput) (eventsubscription.Subscription, error) {
	eventTypes, err := eventsubscription.NormalizeEventTypes(input.EventTypes)
	if err != nil {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	s.nextID++
	now := time.Now().UTC()
	item := eventsubscription.Subscription{
		ID: fmt.Sprintf("esub_%06d", s.nextID), WorkspaceID: input.WorkspaceID, AppID: input.AppID,
		Name: strings.TrimSpace(input.Name), EndpointURL: strings.TrimSpace(input.EndpointURL), EventTypes: eventTypes,
		Status: eventsubscription.SubscriptionStatusActive, SecretVersion: 1, CreatedBy: input.CreatedBy,
		CreatedAt: now, UpdatedAt: now,
	}
	s.subscriptions[item.ID] = item
	return item, nil
}

func (s *eventSubscriptionHTTPTestStore) UpdateEventSubscription(_ context.Context, input eventsubscription.UpdateSubscriptionInput) (eventsubscription.Subscription, error) {
	item, err := s.GetEventSubscription(context.Background(), input.WorkspaceID, input.ID)
	if err != nil || item.AppID != input.AppID {
		return eventsubscription.Subscription{}, managedagents.ErrNotFound
	}
	eventTypes, err := eventsubscription.NormalizeEventTypes(input.EventTypes)
	if err != nil {
		return eventsubscription.Subscription{}, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	item.Name, item.EndpointURL, item.EventTypes, item.Status = input.Name, input.EndpointURL, eventTypes, input.Status
	item.UpdatedAt = time.Now().UTC()
	s.subscriptions[item.ID] = item
	return item, nil
}

func (s *eventSubscriptionHTTPTestStore) RotateEventSubscriptionSecret(_ context.Context, workspaceID, id string) (eventsubscription.Subscription, error) {
	item, err := s.GetEventSubscription(context.Background(), workspaceID, id)
	if err != nil {
		return eventsubscription.Subscription{}, err
	}
	item.SecretVersion++
	item.UpdatedAt = time.Now().UTC()
	s.subscriptions[item.ID] = item
	return item, nil
}

func (*eventSubscriptionHTTPTestStore) ListEventDeliveries(context.Context, eventsubscription.ListDeliveriesInput) ([]eventsubscription.Delivery, error) {
	return []eventsubscription.Delivery{}, nil
}

func (*eventSubscriptionHTTPTestStore) ReplayEventDelivery(context.Context, string, string, string) (eventsubscription.Delivery, error) {
	return eventsubscription.Delivery{}, managedagents.ErrNotFound
}

func (*eventSubscriptionHTTPTestStore) ClaimEventDeliveries(eventsubscription.ClaimInput) ([]eventsubscription.Delivery, error) {
	return nil, nil
}

func (*eventSubscriptionHTTPTestStore) CompleteEventDelivery(eventsubscription.CompleteInput) (bool, error) {
	return false, nil
}

func (*eventSubscriptionHTTPTestStore) FailEventDelivery(eventsubscription.FailInput) (bool, error) {
	return false, nil
}

func seedEventApplicationCredential(t *testing.T, store *eventSubscriptionHTTPTestStore, workspaceID, name, locator, secret string) (managedagents.ServiceIdentity, string) {
	t.Helper()
	identity, err := store.CreateServiceIdentity(context.Background(), managedagents.CreateServiceIdentityInput{
		WorkspaceID: workspaceID, Kind: managedagents.ServiceIdentityKindApplication, Name: name,
		Role: RoleMember, Scopes: []string{managedagents.ServiceScopeEventsManage}, CreatedBy: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	secretHash := sha256.Sum256([]byte(secret))
	if _, err := store.CreateServiceCredential(context.Background(), managedagents.CreateServiceCredentialInput{
		WorkspaceID: workspaceID, ServiceIdentityID: identity.ID, Name: "test", Locator: locator,
		TokenPrefix: locator, SecretHash: secretHash[:], CreatedBy: "test",
	}); err != nil {
		t.Fatal(err)
	}
	return identity, serviceCredentialPrefix + locator + "." + secret
}

func TestEventSubscriptionApplicationBoundariesAndSecretExposure(t *testing.T) {
	server, store, adminToken := newEventSubscriptionHTTPTestServer(t)
	workspaceID := "wksp_event_subscription"
	appOne, appOneToken := seedEventApplicationCredential(t, store, workspaceID, "knowledge", "event-app-one", "secret-one")
	_, appTwoToken := seedEventApplicationCredential(t, store, workspaceID, "biography", "event-app-two", "secret-two")

	createdResponse := httptest.NewRecorder()
	server.ServeHTTP(createdResponse, authenticatedJSONRequest(t, http.MethodPost, "/v2/event-subscriptions", `{
		"name":"knowledge-events","endpoint_url":"http://127.0.0.1/hook","event_types":["run.completed","artifact.created"]
	}`, appOneToken))
	if createdResponse.Code != http.StatusCreated || createdResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create subscription returned %d: %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created eventSubscriptionSecretResponse
	decodeTestResponse(t, createdResponse, &created)
	if created.Subscription.AppID != appOne.ID || !strings.HasPrefix(created.Secret, "whsec_") {
		t.Fatalf("unexpected created subscription: %+v", created)
	}

	getResponse := httptest.NewRecorder()
	server.ServeHTTP(getResponse, authenticatedRequest(t, http.MethodGet, "/v2/event-subscriptions/"+created.Subscription.ID, appOneToken))
	if getResponse.Code != http.StatusOK || strings.Contains(getResponse.Body.String(), `"secret"`) {
		t.Fatalf("get subscription exposed secret or failed with %d: %s", getResponse.Code, getResponse.Body.String())
	}

	crossAppResponse := httptest.NewRecorder()
	server.ServeHTTP(crossAppResponse, authenticatedRequest(t, http.MethodGet, "/v2/event-subscriptions/"+created.Subscription.ID, appTwoToken))
	if crossAppResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-app read returned %d: %s", crossAppResponse.Code, crossAppResponse.Body.String())
	}
	crossAppList := httptest.NewRecorder()
	server.ServeHTTP(crossAppList, authenticatedRequest(t, http.MethodGet, "/v2/event-subscriptions?app_id="+appOne.ID, appTwoToken))
	if crossAppList.Code != http.StatusForbidden {
		t.Fatalf("cross-app list returned %d: %s", crossAppList.Code, crossAppList.Body.String())
	}

	rotateResponse := httptest.NewRecorder()
	server.ServeHTTP(rotateResponse, authenticatedJSONRequest(t, http.MethodPost, "/v2/event-subscriptions/"+created.Subscription.ID+"/rotate-secret", `{}`, appOneToken))
	if rotateResponse.Code != http.StatusOK || rotateResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate secret returned %d: %s", rotateResponse.Code, rotateResponse.Body.String())
	}
	var rotated eventSubscriptionSecretResponse
	decodeTestResponse(t, rotateResponse, &rotated)
	if rotated.Subscription.SecretVersion != 2 || rotated.Secret == created.Secret {
		t.Fatalf("secret was not rotated: before=%+v after=%+v", created, rotated)
	}

	userToken := signedTestJWT(t, "knowledge-user", workspaceID, "knowledge-user", []string{RoleMember}, nil)
	exchangeBody := fmt.Sprintf(`{
		"grant_type":"urn:ietf:params:oauth:grant-type:token-exchange",
		"subject_token":%q,
		"subject_token_type":"urn:ietf:params:oauth:token-type:access_token",
		"requested_token_type":"urn:ietf:params:oauth:token-type:access_token",
		"scope":"events:manage"
	}`, userToken)
	exchangeResponse := httptest.NewRecorder()
	server.ServeHTTP(exchangeResponse, authenticatedJSONRequest(t, http.MethodPost, "/v2/auth/token-exchange", exchangeBody, appOneToken))
	if exchangeResponse.Code != http.StatusOK {
		t.Fatalf("token exchange returned %d: %s", exchangeResponse.Code, exchangeResponse.Body.String())
	}
	var delegated tokenExchangeResponse
	decodeTestResponse(t, exchangeResponse, &delegated)
	delegatedResponse := httptest.NewRecorder()
	server.ServeHTTP(delegatedResponse, authenticatedRequest(t, http.MethodGet, "/v2/event-subscriptions/event-types", delegated.AccessToken))
	if delegatedResponse.Code != http.StatusForbidden {
		t.Fatalf("delegated subscription management returned %d: %s", delegatedResponse.Code, delegatedResponse.Body.String())
	}

	adminList := httptest.NewRecorder()
	server.ServeHTTP(adminList, authenticatedRequest(t, http.MethodGet, "/v2/event-subscriptions?app_id="+appOne.ID, adminToken))
	if adminList.Code != http.StatusOK {
		t.Fatalf("workspace admin list returned %d: %s", adminList.Code, adminList.Body.String())
	}
	var listed struct {
		Items []eventsubscription.Subscription `json:"items"`
	}
	if err := json.NewDecoder(adminList.Body).Decode(&listed); err != nil || len(listed.Items) != 1 {
		t.Fatalf("unexpected admin list: items=%+v err=%v", listed.Items, err)
	}
}

func TestEventSubscriptionUnavailableUsesNotImplemented(t *testing.T) {
	server, _, adminToken := newServiceIdentityHTTPTestServer(t)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, "/v2/event-subscriptions", adminToken))
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "event_subscriptions_unavailable") {
		t.Fatalf("unavailable event subscriptions returned %d: %s", response.Code, response.Body.String())
	}
}
