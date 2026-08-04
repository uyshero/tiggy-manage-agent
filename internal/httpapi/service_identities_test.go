package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/runner"
)

type serviceIdentityHTTPTestStore struct {
	*tenantAdministrationTestStore
	identities  map[string]managedagents.ServiceIdentity
	credentials map[string]serviceIdentityHTTPTestCredential
	nextID      int
}

type serviceIdentityHTTPTestCredential struct {
	credential managedagents.ServiceCredential
	locator    string
	secretHash []byte
}

func newServiceIdentityHTTPTestServer(t *testing.T) (http.Handler, *serviceIdentityHTTPTestStore, string) {
	t.Helper()
	workspaceID := "wksp_service_identity"
	adminSubject := "identity-admin"
	store := &serviceIdentityHTTPTestStore{
		tenantAdministrationTestStore: &tenantAdministrationTestStore{
			testStore: newTestStore(), memberships: map[string]managedagents.WorkspaceMembership{
				workspaceMembershipTestKey(workspaceID, adminSubject): {
					WorkspaceID: workspaceID, Subject: adminSubject, Role: managedagents.WorkspaceRoleAdmin, Status: "active",
				},
			},
		},
		identities: make(map[string]managedagents.ServiceIdentity), credentials: make(map[string]serviceIdentityHTTPTestCredential),
	}
	auth := AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
		DelegationSigningSecret: "test-delegation-secret-with-at-least-32-bytes", DelegationIssuer: "https://platform.example",
		DelegationAudience: "tma-platform-api", DelegationTTL: 5 * time.Minute,
	}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
		store, runner.NewMockRunner(store, time.Millisecond, nil), nil, "fake", "fake-demo",
		objectstore.NewNoopClient(objectstore.Config{}), defaultExecutionResolver(store), "worker-secret", "legacy-control-secret", auth, defaultSubagentPolicy(), nil,
	)
	return server, store, signedTestJWT(t, adminSubject, workspaceID, adminSubject, []string{RoleAdmin}, nil)
}

func (s *serviceIdentityHTTPTestStore) ListServiceIdentities(_ context.Context, workspaceID string) ([]managedagents.ServiceIdentity, error) {
	items := []managedagents.ServiceIdentity{}
	for _, identity := range s.identities {
		if identity.WorkspaceID == workspaceID {
			items = append(items, identity)
		}
	}
	return items, nil
}

func (s *serviceIdentityHTTPTestStore) GetServiceIdentity(_ context.Context, workspaceID, id string) (managedagents.ServiceIdentity, error) {
	identity, ok := s.identities[id]
	if !ok || identity.WorkspaceID != workspaceID {
		return managedagents.ServiceIdentity{}, managedagents.ErrNotFound
	}
	return identity, nil
}

func (s *serviceIdentityHTTPTestStore) CreateServiceIdentity(_ context.Context, input managedagents.CreateServiceIdentityInput) (managedagents.ServiceIdentity, error) {
	kind, name, description, role, scopes, err := managedagents.NormalizeServiceIdentityInput(input.Kind, input.Name, input.Description, input.Role, input.Scopes)
	if err != nil {
		return managedagents.ServiceIdentity{}, err
	}
	s.nextID++
	now := time.Now().UTC()
	identity := managedagents.ServiceIdentity{
		ID: fmt.Sprintf("svc_%06d", s.nextID), WorkspaceID: input.WorkspaceID, Kind: kind, Name: name,
		Description: description, Role: role, Scopes: scopes, Status: managedagents.ServiceIdentityStatusActive,
		CreatedBy: input.CreatedBy, CreatedAt: now, UpdatedAt: now,
	}
	s.identities[identity.ID] = identity
	return identity, nil
}

func (s *serviceIdentityHTTPTestStore) UpdateServiceIdentity(_ context.Context, input managedagents.UpdateServiceIdentityInput) (managedagents.ServiceIdentity, error) {
	identity, err := s.GetServiceIdentity(context.Background(), input.WorkspaceID, input.ID)
	if err != nil {
		return managedagents.ServiceIdentity{}, err
	}
	_, name, description, role, scopes, err := managedagents.NormalizeServiceIdentityInput(identity.Kind, input.Name, input.Description, input.Role, input.Scopes)
	if err != nil {
		return managedagents.ServiceIdentity{}, err
	}
	identity.Name, identity.Description, identity.Role, identity.Scopes, identity.Status = name, description, role, scopes, input.Status
	identity.UpdatedAt = time.Now().UTC()
	s.identities[identity.ID] = identity
	return identity, nil
}

func (s *serviceIdentityHTTPTestStore) ListServiceCredentials(_ context.Context, workspaceID, identityID string) ([]managedagents.ServiceCredential, error) {
	items := []managedagents.ServiceCredential{}
	for _, stored := range s.credentials {
		if stored.credential.WorkspaceID == workspaceID && stored.credential.ServiceIdentityID == identityID {
			items = append(items, stored.credential)
		}
	}
	return items, nil
}

func (s *serviceIdentityHTTPTestStore) CreateServiceCredential(_ context.Context, input managedagents.CreateServiceCredentialInput) (managedagents.ServiceCredential, error) {
	if _, err := s.GetServiceIdentity(context.Background(), input.WorkspaceID, input.ServiceIdentityID); err != nil {
		return managedagents.ServiceCredential{}, err
	}
	s.nextID++
	credential := managedagents.ServiceCredential{
		ID: fmt.Sprintf("cred_%06d", s.nextID), WorkspaceID: input.WorkspaceID, ServiceIdentityID: input.ServiceIdentityID,
		Name: input.Name, TokenPrefix: input.TokenPrefix, Status: managedagents.ServiceCredentialStatusActive,
		ExpiresAt: input.ExpiresAt, CreatedBy: input.CreatedBy, CreatedAt: time.Now().UTC(),
	}
	s.credentials[input.Locator] = serviceIdentityHTTPTestCredential{credential: credential, locator: input.Locator, secretHash: append([]byte(nil), input.SecretHash...)}
	return credential, nil
}

func (s *serviceIdentityHTTPTestStore) RevokeServiceCredential(_ context.Context, workspaceID, identityID, credentialID string) error {
	for locator, stored := range s.credentials {
		if stored.credential.WorkspaceID == workspaceID && stored.credential.ServiceIdentityID == identityID && stored.credential.ID == credentialID {
			now := time.Now().UTC()
			stored.credential.Status = managedagents.ServiceCredentialStatusRevoked
			stored.credential.RevokedAt = &now
			s.credentials[locator] = stored
			return nil
		}
	}
	return managedagents.ErrNotFound
}

func (s *serviceIdentityHTTPTestStore) AuthenticateServiceCredential(_ context.Context, locator string, secretHash []byte) (managedagents.AuthenticatedServiceIdentity, error) {
	stored, ok := s.credentials[locator]
	identity := s.identities[stored.credential.ServiceIdentityID]
	if !ok || !bytes.Equal(stored.secretHash, secretHash) || stored.credential.Status != managedagents.ServiceCredentialStatusActive ||
		identity.Status != managedagents.ServiceIdentityStatusActive || (stored.credential.ExpiresAt != nil && !stored.credential.ExpiresAt.After(time.Now())) {
		return managedagents.AuthenticatedServiceIdentity{}, managedagents.ErrNotFound
	}
	return managedagents.AuthenticatedServiceIdentity{Identity: identity, CredentialID: stored.credential.ID}, nil
}

func TestServiceIdentityCredentialLifecycleAndScopeAuthorization(t *testing.T) {
	server, store, adminToken := newServiceIdentityHTTPTestServer(t)
	createIdentity := authenticatedJSONRequest(t, http.MethodPost, "/v2/service-identities", `{
		"kind":"application","name":"knowledge","role":"member","scopes":["agents:read"]
	}`, adminToken)
	identityResponse := httptest.NewRecorder()
	server.ServeHTTP(identityResponse, createIdentity)
	if identityResponse.Code != http.StatusCreated {
		t.Fatalf("create service identity returned %d: %s", identityResponse.Code, identityResponse.Body.String())
	}
	var identity managedagents.ServiceIdentity
	if err := json.NewDecoder(identityResponse.Body).Decode(&identity); err != nil {
		t.Fatal(err)
	}
	createCredential := authenticatedJSONRequest(t, http.MethodPost, "/v2/service-identities/"+identity.ID+"/credentials", `{"name":"deployment"}`, adminToken)
	credentialResponse := httptest.NewRecorder()
	server.ServeHTTP(credentialResponse, createCredential)
	if credentialResponse.Code != http.StatusCreated || credentialResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create credential returned %d: %s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var created createdServiceCredentialResponse
	if err := json.NewDecoder(credentialResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Token, serviceCredentialPrefix) || strings.Contains(created.Credential.TokenPrefix, ".") {
		t.Fatalf("unexpected created credential: %+v", created)
	}

	me := httptest.NewRecorder()
	server.ServeHTTP(me, authenticatedRequest(t, http.MethodGet, "/v2/auth/me", created.Token))
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"service_identity_id":"`+identity.ID+`"`) {
		t.Fatalf("service identity self returned %d: %s", me.Code, me.Body.String())
	}
	readAgents := httptest.NewRecorder()
	server.ServeHTTP(readAgents, authenticatedRequest(t, http.MethodGet, "/v2/agents", created.Token))
	if readAgents.Code != http.StatusOK {
		t.Fatalf("scoped agent read returned %d: %s", readAgents.Code, readAgents.Body.String())
	}
	writeAgents := httptest.NewRecorder()
	server.ServeHTTP(writeAgents, authenticatedJSONRequest(t, http.MethodPost, "/v2/agents", `{"name":"blocked","system":"test"}`, created.Token))
	if writeAgents.Code != http.StatusForbidden || !strings.Contains(writeAgents.Body.String(), managedagents.ServiceScopeAgentsWrite) {
		t.Fatalf("unscoped agent write returned %d: %s", writeAgents.Code, writeAgents.Body.String())
	}
	capabilitiesWithoutScope := httptest.NewRecorder()
	server.ServeHTTP(capabilitiesWithoutScope, authenticatedRequest(t, http.MethodGet, "/v2/capabilities", created.Token))
	if capabilitiesWithoutScope.Code != http.StatusForbidden || !strings.Contains(capabilitiesWithoutScope.Body.String(), managedagents.ServiceScopeCapabilitiesRead) {
		t.Fatalf("unscoped capability discovery returned %d: %s", capabilitiesWithoutScope.Code, capabilitiesWithoutScope.Body.String())
	}
	identity.Scopes = append(identity.Scopes, managedagents.ServiceScopeCapabilitiesRead)
	store.identities[identity.ID] = identity
	capabilitiesWithScope := httptest.NewRecorder()
	server.ServeHTTP(capabilitiesWithScope, authenticatedRequest(t, http.MethodGet, "/v2/capabilities", created.Token))
	if capabilitiesWithScope.Code != http.StatusOK {
		t.Fatalf("scoped capability discovery returned %d: %s", capabilitiesWithScope.Code, capabilitiesWithScope.Body.String())
	}
	identity.Scopes = append(identity.Scopes, managedagents.ServiceScopeQuotaRead, managedagents.ServiceScopeQuotaWrite)
	store.identities[identity.ID] = identity
	otherQuota := httptest.NewRecorder()
	server.ServeHTTP(otherQuota, authenticatedRequest(t, http.MethodGet, "/v2/quota-policies/effective?app_id=svc_other", created.Token))
	if otherQuota.Code != http.StatusForbidden {
		t.Fatalf("application credential read another application's quota: %d %s", otherQuota.Code, otherQuota.Body.String())
	}
	writeQuota := httptest.NewRecorder()
	server.ServeHTTP(writeQuota, authenticatedJSONRequest(t, http.MethodPut, "/v2/quota-policies/applications/"+identity.ID, `{"plan":"blocked","config":{"model_identity_requests_per_minute":1}}`, created.Token))
	if writeQuota.Code != http.StatusForbidden {
		t.Fatalf("application credential wrote quota policy: %d %s", writeQuota.Code, writeQuota.Body.String())
	}
	registry := httptest.NewRecorder()
	server.ServeHTTP(registry, authenticatedRequest(t, http.MethodGet, "/v2/llm-models", created.Token))
	if registry.Code != http.StatusForbidden {
		t.Fatalf("unmapped control-plane route returned %d: %s", registry.Code, registry.Body.String())
	}

	listCredentials := httptest.NewRecorder()
	server.ServeHTTP(listCredentials, authenticatedRequest(t, http.MethodGet, "/v2/service-identities/"+identity.ID+"/credentials", adminToken))
	if listCredentials.Code != http.StatusOK || strings.Contains(listCredentials.Body.String(), created.Token) {
		t.Fatalf("credential listing exposed token or failed: %d %s", listCredentials.Code, listCredentials.Body.String())
	}
	revoke := httptest.NewRecorder()
	server.ServeHTTP(revoke, authenticatedRequest(t, http.MethodDelete, "/v2/service-identities/"+identity.ID+"/credentials/"+created.Credential.ID, adminToken))
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("revoke credential returned %d: %s", revoke.Code, revoke.Body.String())
	}
	revoked := httptest.NewRecorder()
	server.ServeHTTP(revoked, authenticatedRequest(t, http.MethodGet, "/v2/auth/me", created.Token))
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked service credential returned %d: %s", revoked.Code, revoked.Body.String())
	}
}

func TestDelegatedTokenExchangeEnforcesUserAndApplicationBoundaries(t *testing.T) {
	server, _, adminToken := newServiceIdentityHTTPTestServer(t)
	createIdentity := authenticatedJSONRequest(t, http.MethodPost, "/v2/service-identities", `{
		"kind":"application","name":"knowledge","role":"member","scopes":["agents:read"]
	}`, adminToken)
	identityResponse := httptest.NewRecorder()
	server.ServeHTTP(identityResponse, createIdentity)
	if identityResponse.Code != http.StatusCreated {
		t.Fatalf("create service identity returned %d: %s", identityResponse.Code, identityResponse.Body.String())
	}
	var identity managedagents.ServiceIdentity
	decodeTestResponse(t, identityResponse, &identity)

	createCredential := authenticatedJSONRequest(t, http.MethodPost, "/v2/service-identities/"+identity.ID+"/credentials", `{"name":"deployment"}`, adminToken)
	credentialResponse := httptest.NewRecorder()
	server.ServeHTTP(credentialResponse, createCredential)
	if credentialResponse.Code != http.StatusCreated {
		t.Fatalf("create credential returned %d: %s", credentialResponse.Code, credentialResponse.Body.String())
	}
	var credential createdServiceCredentialResponse
	decodeTestResponse(t, credentialResponse, &credential)

	userToken := signedTestJWT(t, "knowledge-user", identity.WorkspaceID, "knowledge-owner", []string{RoleMember}, nil)
	exchangeBody := fmt.Sprintf(`{
		"grant_type":"urn:ietf:params:oauth:grant-type:token-exchange",
		"subject_token":%q,
		"subject_token_type":"urn:ietf:params:oauth:token-type:access_token",
		"requested_token_type":"urn:ietf:params:oauth:token-type:access_token",
		"scope":"agents:read"
	}`, userToken)
	exchange := httptest.NewRecorder()
	server.ServeHTTP(exchange, authenticatedJSONRequest(t, http.MethodPost, "/v2/auth/token-exchange", exchangeBody, credential.Token))
	if exchange.Code != http.StatusOK || exchange.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("token exchange returned %d: %s", exchange.Code, exchange.Body.String())
	}
	var delegated tokenExchangeResponse
	decodeTestResponse(t, exchange, &delegated)
	if !strings.HasPrefix(delegated.AccessToken, delegatedTokenPrefix) || delegated.Scope != managedagents.ServiceScopeAgentsRead || delegated.ExpiresIn != 300 {
		t.Fatalf("unexpected delegated token response: %+v", delegated)
	}

	me := httptest.NewRecorder()
	server.ServeHTTP(me, authenticatedRequest(t, http.MethodGet, "/v2/auth/me", delegated.AccessToken))
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"subject":"knowledge-user"`) ||
		!strings.Contains(me.Body.String(), `"owner_id":"knowledge-owner"`) ||
		!strings.Contains(me.Body.String(), `"auth_type":"delegated"`) ||
		!strings.Contains(me.Body.String(), `"service_identity_id":"`+identity.ID+`"`) {
		t.Fatalf("delegated principal returned %d: %s", me.Code, me.Body.String())
	}
	readAgents := httptest.NewRecorder()
	server.ServeHTTP(readAgents, authenticatedRequest(t, http.MethodGet, "/v2/agents", delegated.AccessToken))
	if readAgents.Code != http.StatusOK {
		t.Fatalf("delegated agent read returned %d: %s", readAgents.Code, readAgents.Body.String())
	}
	writeAgents := httptest.NewRecorder()
	server.ServeHTTP(writeAgents, authenticatedJSONRequest(t, http.MethodPost, "/v2/agents", `{"name":"blocked","system":"test"}`, delegated.AccessToken))
	if writeAgents.Code != http.StatusForbidden || !strings.Contains(writeAgents.Body.String(), managedagents.ServiceScopeAgentsWrite) {
		t.Fatalf("delegated scope escalation returned %d: %s", writeAgents.Code, writeAgents.Body.String())
	}

	workspaceMismatchToken := signedTestJWT(t, "other-user", "wksp_other", "other-owner", []string{RoleMember}, nil)
	mismatchBody := strings.Replace(exchangeBody, userToken, workspaceMismatchToken, 1)
	mismatch := httptest.NewRecorder()
	server.ServeHTTP(mismatch, authenticatedJSONRequest(t, http.MethodPost, "/v2/auth/token-exchange", mismatchBody, credential.Token))
	if mismatch.Code != http.StatusForbidden || !strings.Contains(mismatch.Body.String(), "workspace_mismatch") {
		t.Fatalf("cross-workspace exchange returned %d: %s", mismatch.Code, mismatch.Body.String())
	}

	overScopeBody := strings.Replace(exchangeBody, `"scope":"agents:read"`, `"scope":"agents:write"`, 1)
	overScope := httptest.NewRecorder()
	server.ServeHTTP(overScope, authenticatedJSONRequest(t, http.MethodPost, "/v2/auth/token-exchange", overScopeBody, credential.Token))
	if overScope.Code != http.StatusForbidden || !strings.Contains(overScope.Body.String(), "invalid_scope") {
		t.Fatalf("over-scoped exchange returned %d: %s", overScope.Code, overScope.Body.String())
	}

	delegationChainBody := strings.Replace(exchangeBody, userToken, delegated.AccessToken, 1)
	delegationChain := httptest.NewRecorder()
	server.ServeHTTP(delegationChain, authenticatedJSONRequest(t, http.MethodPost, "/v2/auth/token-exchange", delegationChainBody, credential.Token))
	if delegationChain.Code != http.StatusUnauthorized {
		t.Fatalf("delegated subject exchange returned %d: %s", delegationChain.Code, delegationChain.Body.String())
	}
	delegatedActor := httptest.NewRecorder()
	server.ServeHTTP(delegatedActor, authenticatedJSONRequest(t, http.MethodPost, "/v2/auth/token-exchange", exchangeBody, delegated.AccessToken))
	if delegatedActor.Code != http.StatusForbidden {
		t.Fatalf("delegated actor exchange returned %d: %s", delegatedActor.Code, delegatedActor.Body.String())
	}

	revoke := httptest.NewRecorder()
	server.ServeHTTP(revoke, authenticatedRequest(t, http.MethodDelete, "/v2/service-identities/"+identity.ID+"/credentials/"+credential.Credential.ID, adminToken))
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("revoke credential returned %d: %s", revoke.Code, revoke.Body.String())
	}
	revokedDelegation := httptest.NewRecorder()
	server.ServeHTTP(revokedDelegation, authenticatedRequest(t, http.MethodGet, "/v2/auth/me", delegated.AccessToken))
	if revokedDelegation.Code != http.StatusUnauthorized {
		t.Fatalf("revoked delegated token returned %d: %s", revokedDelegation.Code, revokedDelegation.Body.String())
	}
}
