package httpapi

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"

	"tiggy-manage-agent/internal/identity"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/runner"
)

const testJWTSecret = "test-jwt-secret-with-at-least-32-bytes"

func TestDisabledAuthBindsDefaultDatabaseWorkspaceScope(t *testing.T) {
	authenticator, err := newIdentityAuthenticator(AuthConfig{Mode: AuthModeDisabled})
	if err != nil {
		t.Fatalf("build disabled identity authenticator: %v", err)
	}
	server := &Server{authenticator: authenticator}
	var captured managedagents.AccessScope
	handler := server.identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		captured, ok = managedagents.DatabaseAccessScopeFromContext(r.Context())
		if !ok {
			t.Error("disabled auth request did not carry a database access scope")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agents", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("disabled auth middleware returned %d: %s", response.Code, response.Body.String())
	}
	if captured.WorkspaceID != managedagents.DefaultWorkspaceID || captured.OwnerID != "" {
		t.Fatalf("unexpected disabled auth database scope: %+v", captured)
	}
}

func TestWorkerCredentialBindsConfiguredDatabaseWorkspaceScope(t *testing.T) {
	authenticator, err := newIdentityAuthenticator(AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
		WorkerWorkspaceID: "wksp_worker_alpha",
	})
	if err != nil {
		t.Fatalf("build identity authenticator: %v", err)
	}
	server := &Server{authenticator: authenticator, workerAuthToken: "worker-secret"}
	var captured managedagents.AccessScope
	handler := server.identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		captured, ok = managedagents.DatabaseAccessScopeFromContext(r.Context())
		if !ok {
			t.Error("worker credential request did not carry a database access scope")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/v1/workers", strings.NewReader(`{"name":"worker"}`))
	request.Header.Set("Authorization", "Bearer worker-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("worker credential middleware returned %d: %s", response.Code, response.Body.String())
	}
	if captured.WorkspaceID != "wksp_worker_alpha" || captured.OwnerID != "" {
		t.Fatalf("unexpected worker database scope: %+v", captured)
	}
}

func TestUnifiedJWTAuthRequiresIdentityAndEnforcesRoles(t *testing.T) {
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
	})

	health := httptest.NewRecorder()
	server.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("expected public health endpoint, got %d", health.Code)
	}

	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/agents", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated API request to return 401, got %d: %s", unauthorized.Code, unauthorized.Body.String())
	}

	viewerToken := signedTestJWT(t, "viewer-1", "wksp_alpha", "owner-viewer", []string{RoleViewer}, nil)
	meRequest := authenticatedRequest(t, http.MethodGet, "/v1/auth/me", viewerToken)
	meResponse := httptest.NewRecorder()
	server.ServeHTTP(meResponse, meRequest)
	if meResponse.Code != http.StatusOK || !bytes.Contains(meResponse.Body.Bytes(), []byte(`"workspace_id":"wksp_alpha"`)) {
		t.Fatalf("expected effective principal response, got %d: %s", meResponse.Code, meResponse.Body.String())
	}
	if bytes.Contains(meResponse.Body.Bytes(), []byte("authorization_sources")) {
		t.Fatalf("internal authorization sources must not be exposed: %s", meResponse.Body.String())
	}

	cookieRequest := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	cookieRequest.AddCookie(&http.Cookie{Name: accessTokenCookie, Value: viewerToken})
	cookieResponse := httptest.NewRecorder()
	server.ServeHTTP(cookieResponse, cookieRequest)
	if cookieResponse.Code != http.StatusOK {
		t.Fatalf("expected JWT cookie auth to succeed, got %d: %s", cookieResponse.Code, cookieResponse.Body.String())
	}

	viewerWrite := authenticatedJSONRequest(t, http.MethodPost, "/v1/agents", `{"name":"blocked","model":"fake-demo"}`, viewerToken)
	viewerResponse := httptest.NewRecorder()
	server.ServeHTTP(viewerResponse, viewerWrite)
	if viewerResponse.Code != http.StatusForbidden {
		t.Fatalf("expected viewer write to return 403, got %d: %s", viewerResponse.Code, viewerResponse.Body.String())
	}
}

func TestAuthClientConfigurationIsPublic(t *testing.T) {
	provider := newOIDCTestProvider(t)
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: provider.server.URL, OIDCAudience: "tma-api",
		OIDCCLIClientID: "tma-cli",
	})
	for _, path := range []string{"/v1/auth/config", "/v2/auth/config"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d: %s", path, response.Code, response.Body.String())
		}
		var configuration authClientConfiguration
		decodeTestResponse(t, response, &configuration)
		if configuration.Mode != AuthModeOIDC || configuration.OIDC == nil ||
			configuration.OIDC.Issuer != provider.server.URL || configuration.OIDC.Audience != "tma-api" ||
			configuration.OIDC.ClientID != "tma-cli" || !configuration.OIDC.DeviceAuthorization {
			t.Fatalf("unexpected auth client configuration from %s: %+v", path, configuration)
		}
	}
}

func TestUnifiedCookieAuthRequiresTrustedOriginForWrites(t *testing.T) {
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
	})
	token := signedTestJWT(t, "cookie-member", "wksp_cookie", "owner-cookie", []string{RoleMember}, nil)

	request := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/agents", bytes.NewBufferString(`{"name":"Cookie Agent","model":"fake-demo"}`))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.AddCookie(&http.Cookie{Name: accessTokenCookie, Value: token})
		return r
	}

	missingOrigin := httptest.NewRecorder()
	server.ServeHTTP(missingOrigin, request(""))
	if missingOrigin.Code != http.StatusForbidden {
		t.Fatalf("expected missing cookie write Origin to return 403, got %d: %s", missingOrigin.Code, missingOrigin.Body.String())
	}

	foreignOrigin := httptest.NewRecorder()
	server.ServeHTTP(foreignOrigin, request("https://attacker.example"))
	if foreignOrigin.Code != http.StatusForbidden {
		t.Fatalf("expected foreign cookie write Origin to return 403, got %d: %s", foreignOrigin.Code, foreignOrigin.Body.String())
	}

	sameOrigin := httptest.NewRecorder()
	server.ServeHTTP(sameOrigin, request("http://example.com"))
	if sameOrigin.Code != http.StatusCreated {
		t.Fatalf("expected same-origin cookie write to succeed, got %d: %s", sameOrigin.Code, sameOrigin.Body.String())
	}

	trustedServer, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
		CookieTrustedOrigins: []string{"https://app.example.com"},
	})
	trustedOrigin := httptest.NewRecorder()
	trustedServer.ServeHTTP(trustedOrigin, request("https://app.example.com"))
	if trustedOrigin.Code != http.StatusCreated {
		t.Fatalf("expected configured cookie Origin to succeed, got %d: %s", trustedOrigin.Code, trustedOrigin.Body.String())
	}
}

func TestUnifiedAuthEmitsStructuredDecisionAuditAndMetrics(t *testing.T) {
	store := newTestStore()
	turnRunner := runner.NewMockRunner(store, time.Millisecond, nil)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	auditSink := &recordingAuthorizationDecisionSink{metrics: observability.SecurityAuditExporterMetrics{
		Enabled: true, QueueCapacity: 128, Sent: 3,
	}, integrityKeyStatus: observability.SecurityAuditIntegrityKeyStatus{
		ActiveKeyID: "2026-07",
		Keys: []observability.SecurityAuditIntegrityKeyState{
			{KeyID: "2026-01", Configured: true, Delivered: 10, SafeToRemove: true},
			{KeyID: "2026-07", Configured: true, Active: true, Pending: 1, Blocking: 1},
		},
	}}
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
		store, turnRunner, logger, "fake", "fake-demo", objectstore.NewNoopClient(objectstore.Config{}), defaultExecutionResolver(store),
		"worker-secret", "legacy-control-secret", AuthConfig{
			Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
			AuthorizationSink: auditSink,
		}, defaultSubagentPolicy(), nil,
	)

	invalidToken := "audit-secret-token-marker"
	unauthorized := httptest.NewRecorder()
	unauthorizedRequest := httptest.NewRequest(http.MethodGet, "/v1/agents?token=must-not-be-logged", nil)
	unauthorizedRequest.Header.Set("Authorization", "Bearer "+invalidToken)
	server.ServeHTTP(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected authentication rejection, got %d: %s", unauthorized.Code, unauthorized.Body.String())
	}

	viewerToken := signedTestJWT(t, "audit-viewer", "wksp_audit", "owner-audit", []string{RoleViewer}, nil)
	viewerWrite := httptest.NewRecorder()
	server.ServeHTTP(viewerWrite, authenticatedJSONRequest(t, http.MethodPost, "/v1/agents", `{"name":"blocked","model":"fake-demo"}`, viewerToken))
	if viewerWrite.Code != http.StatusForbidden {
		t.Fatalf("expected role rejection, got %d: %s", viewerWrite.Code, viewerWrite.Body.String())
	}

	operatorToken := signedTestJWT(t, "audit-operator", "wksp_audit", "owner-operator", []string{RoleOperator}, nil)
	allowed := httptest.NewRecorder()
	server.ServeHTTP(allowed, authenticatedRequest(t, http.MethodGet, "/v1/agents", operatorToken))
	if allowed.Code != http.StatusOK {
		t.Fatalf("expected authorized request, got %d: %s", allowed.Code, allowed.Body.String())
	}

	metricsResponse := httptest.NewRecorder()
	server.ServeHTTP(metricsResponse, authenticatedRequest(t, http.MethodGet, "/metrics", operatorToken))
	if metricsResponse.Code != http.StatusOK {
		t.Fatalf("expected metrics response, got %d: %s", metricsResponse.Code, metricsResponse.Body.String())
	}
	metrics := metricsResponse.Body.String()
	for _, expected := range []string{
		`tma_authorization_decisions_total{auth_type="jwt",outcome="denied",reason="authentication_failed"} 1`,
		`tma_authorization_decisions_total{auth_type="jwt",outcome="denied",reason="role_required"} 1`,
		`tma_authorization_decisions_total{auth_type="jwt",outcome="allowed",reason="control_role"} 1`,
		`tma_security_audit_exporter_enabled 1`,
		`tma_security_audit_export_events_total{outcome="sent"} 3`,
	} {
		if !strings.Contains(metrics, expected) {
			t.Fatalf("expected authorization metric %q, got:\n%s", expected, metrics)
		}
	}

	logText := logs.String()
	for _, expected := range []string{
		`"event":"authorization_decision"`,
		`"reason":"authentication_failed"`,
		`"reason":"role_required"`,
		`"reason":"identity_boundary"`,
		`"subject":"audit-viewer"`,
		`"workspace_id":"wksp_audit"`,
		`"authorization_sources":["jwt_claim:owner_id","jwt_claim:roles","jwt_claim:sub","jwt_claim:workspace_id"]`,
	} {
		if !strings.Contains(logText, expected) {
			t.Fatalf("expected structured audit field %q, got:\n%s", expected, logText)
		}
	}
	for _, sensitive := range []string{invalidToken, "must-not-be-logged", viewerToken, operatorToken} {
		if strings.Contains(logText, sensitive) {
			t.Fatalf("authorization audit leaked sensitive request data %q", sensitive)
		}
	}
	replayResponse := httptest.NewRecorder()
	server.ServeHTTP(replayResponse, authenticatedRequest(t, http.MethodPost, "/v1/observability/security-audit/replay?limit=25", operatorToken))
	if replayResponse.Code != http.StatusOK || !strings.Contains(replayResponse.Body.String(), `"replayed":2`) {
		t.Fatalf("expected security audit dead-letter replay, got %d: %s", replayResponse.Code, replayResponse.Body.String())
	}
	if auditSink.replayLimit != 25 {
		t.Fatalf("expected replay limit 25, got %d", auditSink.replayLimit)
	}
	events := auditSink.snapshot()
	if len(events) != 7 {
		t.Fatalf("expected seven authorization decisions to be forwarded, got %+v", events)
	}
	if events[0].Path != "/v1/agents" || strings.Contains(events[0].Path, "must-not-be-logged") {
		t.Fatalf("authorization sink received query data: %+v", events[0])
	}
	foundViewer := false
	for _, event := range events {
		if event.Subject == "audit-viewer" && event.Reason == "role_required" {
			foundViewer = true
			if event.WorkspaceID != "wksp_audit" || !containsTestString(event.AuthorizationSources, "jwt_claim:roles") {
				t.Fatalf("authorization sink lost principal provenance: %+v", event)
			}
		}
	}
	if !foundViewer {
		t.Fatalf("authorization sink did not receive viewer rejection: %+v", events)
	}
	viewerKeyStatus := httptest.NewRecorder()
	server.ServeHTTP(viewerKeyStatus, authenticatedRequest(t, http.MethodGet, "/v1/observability/security-audit/integrity-keys", viewerToken))
	if viewerKeyStatus.Code != http.StatusForbidden {
		t.Fatalf("expected viewer integrity key status rejection, got %d: %s", viewerKeyStatus.Code, viewerKeyStatus.Body.String())
	}
	operatorKeyStatus := httptest.NewRecorder()
	server.ServeHTTP(operatorKeyStatus, authenticatedRequest(t, http.MethodGet, "/v1/observability/security-audit/integrity-keys", operatorToken))
	if operatorKeyStatus.Code != http.StatusOK || !strings.Contains(operatorKeyStatus.Body.String(), `"active_key_id":"2026-07"`) ||
		!strings.Contains(operatorKeyStatus.Body.String(), `"safe_to_remove":true`) {
		t.Fatalf("unexpected operator integrity key status: %d: %s", operatorKeyStatus.Code, operatorKeyStatus.Body.String())
	}
	if strings.Contains(operatorKeyStatus.Body.String(), "security-audit-key-material") {
		t.Fatal("integrity key status leaked key material")
	}
	observabilityStatus := httptest.NewRecorder()
	server.ServeHTTP(observabilityStatus, authenticatedRequest(t, http.MethodGet, "/v1/observability/status", operatorToken))
	if observabilityStatus.Code != http.StatusOK || !strings.Contains(observabilityStatus.Body.String(), `"security_audit_integrity_keys"`) {
		t.Fatalf("observability status omitted integrity key readiness: %d: %s", observabilityStatus.Code, observabilityStatus.Body.String())
	}
}

type recordingAuthorizationDecisionSink struct {
	mu                 sync.Mutex
	events             []observability.AuthorizationDecisionEvent
	metrics            observability.SecurityAuditExporterMetrics
	integrityKeyStatus observability.SecurityAuditIntegrityKeyStatus
	replayLimit        int
}

func (s *recordingAuthorizationDecisionSink) EnqueueAuthorizationDecision(event observability.AuthorizationDecisionEvent) bool {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return true
}

func (s *recordingAuthorizationDecisionSink) SecurityAuditMetrics() observability.SecurityAuditExporterMetrics {
	return s.metrics
}

func (s *recordingAuthorizationDecisionSink) ReplayDeadLetters(_ time.Time, limit int) (int, error) {
	s.mu.Lock()
	s.replayLimit = limit
	s.mu.Unlock()
	return 2, nil
}

func (s *recordingAuthorizationDecisionSink) SecurityAuditIntegrityKeyStatus() (observability.SecurityAuditIntegrityKeyStatus, error) {
	return s.integrityKeyStatus, nil
}

func (s *recordingAuthorizationDecisionSink) snapshot() []observability.AuthorizationDecisionEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]observability.AuthorizationDecisionEvent(nil), s.events...)
}

func containsTestString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestUnifiedJWTAuthDerivesTenantAndOwner(t *testing.T) {
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
	})
	aliceToken := signedTestJWT(t, "alice", "wksp_alpha", "owner-alice", []string{RoleMember}, nil)

	agentResponse := httptest.NewRecorder()
	server.ServeHTTP(agentResponse, authenticatedJSONRequest(t, http.MethodPost, "/v1/agents", `{
		"workspace_id":"wksp_evil","name":"Scoped Agent","model":"fake-demo"
	}`, aliceToken))
	if agentResponse.Code != http.StatusCreated {
		t.Fatalf("create agent: %d: %s", agentResponse.Code, agentResponse.Body.String())
	}
	var agent managedagents.Agent
	decodeTestResponse(t, agentResponse, &agent)
	if agent.WorkspaceID != "wksp_alpha" {
		t.Fatalf("expected server-derived workspace, got %#v", agent)
	}

	environmentResponse := httptest.NewRecorder()
	server.ServeHTTP(environmentResponse, authenticatedJSONRequest(t, http.MethodPost, "/v1/environments", `{
		"workspace_id":"wksp_evil","name":"Scoped Env","config":{}
	}`, aliceToken))
	if environmentResponse.Code != http.StatusCreated {
		t.Fatalf("create environment: %d: %s", environmentResponse.Code, environmentResponse.Body.String())
	}
	var environment managedagents.Environment
	decodeTestResponse(t, environmentResponse, &environment)
	if environment.WorkspaceID != "wksp_alpha" {
		t.Fatalf("expected server-derived environment workspace, got %#v", environment)
	}

	sessionResponse := httptest.NewRecorder()
	sessionBody := `{"workspace_id":"wksp_evil","owner_id":"owner-evil","created_by":"evil","agent_id":"` + agent.ID + `","environment_id":"` + environment.ID + `"}`
	server.ServeHTTP(sessionResponse, authenticatedJSONRequest(t, http.MethodPost, "/v1/sessions", sessionBody, aliceToken))
	if sessionResponse.Code != http.StatusCreated {
		t.Fatalf("create session: %d: %s", sessionResponse.Code, sessionResponse.Body.String())
	}
	var session managedagents.Session
	decodeTestResponse(t, sessionResponse, &session)
	if session.WorkspaceID != "wksp_alpha" || session.OwnerID != "owner-alice" || session.CreatedBy != "alice" {
		t.Fatalf("expected server-derived session scope, got %#v", session)
	}

	bobToken := signedTestJWT(t, "bob", "wksp_alpha", "owner-bob", []string{RoleMember}, nil)
	bobRead := httptest.NewRecorder()
	server.ServeHTTP(bobRead, authenticatedRequest(t, http.MethodGet, "/v1/sessions/"+session.ID, bobToken))
	if bobRead.Code != http.StatusForbidden {
		t.Fatalf("expected owner isolation to return 403, got %d: %s", bobRead.Code, bobRead.Body.String())
	}
	bobList := httptest.NewRecorder()
	server.ServeHTTP(bobList, authenticatedRequest(t, http.MethodGet, "/v1/sessions?workspace_id=wksp_evil", bobToken))
	if bobList.Code != http.StatusOK {
		t.Fatalf("list bob sessions: %d: %s", bobList.Code, bobList.Body.String())
	}
	var bobSessions struct {
		Sessions []managedagents.Session `json:"sessions"`
	}
	decodeTestResponse(t, bobList, &bobSessions)
	if len(bobSessions.Sessions) != 0 {
		t.Fatalf("expected owner-scoped session list, got %#v", bobSessions.Sessions)
	}

	operatorToken := signedTestJWT(t, "ops", "wksp_alpha", "owner-ops", []string{RoleOperator}, nil)
	operatorRead := httptest.NewRecorder()
	server.ServeHTTP(operatorRead, authenticatedRequest(t, http.MethodGet, "/v1/sessions/"+session.ID, operatorToken))
	if operatorRead.Code != http.StatusOK {
		t.Fatalf("expected operator workspace access, got %d: %s", operatorRead.Code, operatorRead.Body.String())
	}

	otherWorkspaceToken := signedTestJWT(t, "other-ops", "wksp_other", "owner-other", []string{RoleAdmin}, nil)
	crossWorkspace := httptest.NewRecorder()
	server.ServeHTTP(crossWorkspace, authenticatedRequest(t, http.MethodGet, "/v1/agents/"+agent.ID, otherWorkspaceToken))
	if crossWorkspace.Code != http.StatusForbidden {
		t.Fatalf("expected workspace isolation to return 403, got %d: %s", crossWorkspace.Code, crossWorkspace.Body.String())
	}
}

func TestUnifiedJWTAuthValidatesIssuerAudienceAndOperatorRole(t *testing.T) {
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
	})
	badAudience := signedTestJWT(t, "alice", "wksp_alpha", "alice", []string{RoleAdmin}, map[string]any{"aud": "other-api"})
	invalid := httptest.NewRecorder()
	server.ServeHTTP(invalid, authenticatedRequest(t, http.MethodGet, "/v1/agents", badAudience))
	if invalid.Code != http.StatusUnauthorized {
		t.Fatalf("expected audience mismatch to return 401, got %d", invalid.Code)
	}

	memberToken := signedTestJWT(t, "alice", "wksp_alpha", "alice", []string{RoleMember}, nil)
	memberControl := httptest.NewRecorder()
	server.ServeHTTP(memberControl, authenticatedJSONRequest(t, http.MethodPost, "/v1/llm-models", `{"provider_id":"fake","model":"member-model","context_window_tokens":1000}`, memberToken))
	if memberControl.Code != http.StatusForbidden {
		t.Fatalf("expected member control request to return 403, got %d: %s", memberControl.Code, memberControl.Body.String())
	}

	operatorToken := signedTestJWT(t, "ops", "wksp_alpha", "ops", []string{RoleOperator}, nil)
	operatorControl := httptest.NewRecorder()
	operatorControlRequest := authenticatedJSONRequest(t, http.MethodPost, "/v1/llm-models", `{"provider_id":"fake","model":"ops-model","context_window_tokens":1000}`, operatorToken)
	operatorControlRequest.Header.Set("If-None-Match", "*")
	server.ServeHTTP(operatorControl, operatorControlRequest)
	if operatorControl.Code != http.StatusCreated {
		t.Fatalf("expected operator control request to succeed, got %d: %s", operatorControl.Code, operatorControl.Body.String())
	}

	memberDelete := httptest.NewRecorder()
	server.ServeHTTP(memberDelete, authenticatedRequest(t, http.MethodDelete, "/v1/llm-models/fake/ops-model", memberToken))
	if memberDelete.Code != http.StatusForbidden {
		t.Fatalf("expected member model deletion to return 403, got %d: %s", memberDelete.Code, memberDelete.Body.String())
	}

	operatorDelete := httptest.NewRecorder()
	server.ServeHTTP(operatorDelete, authenticatedRequest(t, http.MethodDelete, "/v1/llm-models/fake/ops-model", operatorToken))
	if operatorDelete.Code != http.StatusForbidden {
		t.Fatalf("expected operator model deletion to require admin, got %d: %s", operatorDelete.Code, operatorDelete.Body.String())
	}

	adminToken := signedTestJWT(t, "admin", "wksp_alpha", "admin", []string{RoleAdmin}, nil)
	adminDelete := httptest.NewRecorder()
	adminDeleteRequest := authenticatedRequest(t, http.MethodDelete, "/v1/llm-models/fake/ops-model", adminToken)
	adminDeleteRequest.Header.Set("If-Match", `"1"`)
	server.ServeHTTP(adminDelete, adminDeleteRequest)
	if adminDelete.Code != http.StatusNoContent {
		t.Fatalf("expected admin model deletion to succeed, got %d: %s", adminDelete.Code, adminDelete.Body.String())
	}
}

func TestUnifiedGatewayAuthRequiresTrustedProxyAndSharedToken(t *testing.T) {
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeGateway, GatewayToken: "gateway-shared-secret", GatewayTrustedCIDRs: []string{"127.0.0.0/8"},
	})

	trusted := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	trusted.RemoteAddr = "127.0.0.1:43123"
	trusted.Header.Set(gatewayTokenHeader, "gateway-shared-secret")
	trusted.Header.Set(gatewaySubjectHeader, "gateway-user")
	trusted.Header.Set(gatewayWorkspaceHeader, "wksp_alpha")
	trusted.Header.Set(gatewayOwnerHeader, "owner-gateway")
	trusted.Header.Set(gatewayRolesHeader, RoleMember)
	trustedResponse := httptest.NewRecorder()
	server.ServeHTTP(trustedResponse, trusted)
	if trustedResponse.Code != http.StatusOK {
		t.Fatalf("expected trusted gateway request, got %d: %s", trustedResponse.Code, trustedResponse.Body.String())
	}

	badToken := trusted.Clone(trusted.Context())
	badToken.Header.Set(gatewayTokenHeader, "wrong-token")
	badTokenResponse := httptest.NewRecorder()
	server.ServeHTTP(badTokenResponse, badToken)
	if badTokenResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected invalid gateway token to return 401, got %d", badTokenResponse.Code)
	}

	untrusted := trusted.Clone(trusted.Context())
	untrusted.RemoteAddr = "203.0.113.10:43123"
	untrustedResponse := httptest.NewRecorder()
	server.ServeHTTP(untrustedResponse, untrusted)
	if untrustedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected untrusted proxy to return 401, got %d", untrustedResponse.Code)
	}
}

func TestUnifiedOIDCAuthDiscoveryAlgorithmsRotationAndCachedKeys(t *testing.T) {
	rsaKey1, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key 1: %v", err)
	}
	rsaKey2, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key 2: %v", err)
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa key: %v", err)
	}
	provider := newOIDCTestProvider(t)
	provider.setKeys(oidcTestPublicJWK("rsa-1", "RS256", &rsaKey1.PublicKey))

	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: provider.server.URL, OIDCAudience: "tma-api",
		OIDCSigningAlgs: []string{"RS256", "ES256"}, OIDCHTTPTimeout: 2 * time.Second,
	})
	claims := map[string]any{
		"sub": "oidc-user", "iss": provider.server.URL, "aud": "tma-api", "exp": time.Now().Add(time.Hour).Unix(),
		"workspace_id": "wksp_oidc", "owner_id": "owner-oidc", "roles": []string{RoleMember},
	}

	rsaToken := signedOIDCTestToken(t, rsaKey1, "rsa-1", "RS256", claims)
	assertOIDCPrincipal(t, server, rsaToken, http.StatusOK)
	if provider.jwksRequestCount() != 1 {
		t.Fatalf("expected initial JWKS fetch, got %d", provider.jwksRequestCount())
	}

	badAudienceClaims := cloneOIDCTestClaims(claims)
	badAudienceClaims["aud"] = "other-api"
	assertOIDCPrincipal(t, server, signedOIDCTestToken(t, rsaKey1, "rsa-1", "RS256", badAudienceClaims), http.StatusUnauthorized)

	futureClaims := cloneOIDCTestClaims(claims)
	futureClaims["nbf"] = time.Now().Add(10 * time.Minute).Unix()
	assertOIDCPrincipal(t, server, signedOIDCTestToken(t, rsaKey1, "rsa-1", "RS256", futureClaims), http.StatusUnauthorized)

	provider.setKeys(
		oidcTestPublicJWK("rsa-1", "RS256", &rsaKey1.PublicKey),
		oidcTestPublicJWK("ec-1", "ES256", &ecdsaKey.PublicKey),
	)
	assertOIDCPrincipal(t, server, signedOIDCTestToken(t, ecdsaKey, "ec-1", "ES256", claims), http.StatusOK)
	if provider.jwksRequestCount() != 2 {
		t.Fatalf("expected unknown EC kid to refresh JWKS, got %d fetches", provider.jwksRequestCount())
	}

	provider.setKeys(oidcTestPublicJWK("rsa-2", "RS256", &rsaKey2.PublicKey))
	rsaToken2 := signedOIDCTestToken(t, rsaKey2, "rsa-2", "RS256", claims)
	assertOIDCPrincipal(t, server, rsaToken2, http.StatusOK)
	if provider.jwksRequestCount() != 3 {
		t.Fatalf("expected rotated RSA kid to refresh JWKS, got %d fetches", provider.jwksRequestCount())
	}

	provider.setJWKSFailure(true)
	assertOIDCPrincipal(t, server, rsaToken2, http.StatusOK)
	if provider.jwksRequestCount() != 3 {
		t.Fatalf("cached key should not fetch JWKS, got %d fetches", provider.jwksRequestCount())
	}
	unknownToken := signedOIDCTestToken(t, rsaKey1, "rsa-unknown", "RS256", claims)
	assertOIDCPrincipal(t, server, unknownToken, http.StatusUnauthorized)
	if provider.jwksRequestCount() != 4 {
		t.Fatalf("unknown kid should attempt JWKS refresh, got %d fetches", provider.jwksRequestCount())
	}

	hs256Claims := cloneOIDCTestClaims(claims)
	hs256Claims["iss"] = "https://issuer.example"
	hs256Token := signedTestJWT(t, "oidc-user", "wksp_oidc", "owner-oidc", []string{RoleMember}, hs256Claims)
	assertOIDCPrincipal(t, server, hs256Token, http.StatusUnauthorized)
}

func TestUnifiedOIDCAuthSupportsExplicitJWKSURL(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	provider := newOIDCTestProvider(t)
	provider.setKeys(oidcTestPublicJWK("direct-rsa", "RS256", &key.PublicKey))
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: provider.server.URL, OIDCAudience: "tma-api",
		OIDCJWKSURL: provider.server.URL + "/jwks", OIDCSigningAlgs: []string{"RS256"}, OIDCHTTPTimeout: 2 * time.Second,
	})
	claims := map[string]any{
		"sub": "direct-user", "iss": provider.server.URL, "aud": "tma-api", "exp": time.Now().Add(time.Hour).Unix(),
		"workspace_id": "wksp_direct", "roles": []string{RoleViewer},
	}
	assertOIDCPrincipal(t, server, signedOIDCTestToken(t, key, "direct-rsa", "RS256", claims), http.StatusOK)
	if provider.discoveryRequestCount() != 0 {
		t.Fatalf("explicit JWKS mode should bypass discovery, got %d discovery requests", provider.discoveryRequestCount())
	}
}

func TestUnifiedOIDCRejectsJWKSProtocolDowngrade(t *testing.T) {
	_, err := newIdentityAuthenticator(AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: "https://issuer.example", OIDCAudience: "tma-api",
		OIDCJWKSURL: "http://issuer.example/jwks", OIDCSigningAlgs: []string{"RS256"},
	})
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("expected OIDC JWKS downgrade rejection, got %v", err)
	}
}

func TestUnifiedOIDCJWKSStaleWindowFallbackAndExpiry(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	provider := newOIDCTestProvider(t)
	provider.setKeys(oidcTestPublicJWK("stale-rsa", "RS256", &key.PublicKey))
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: provider.server.URL, OIDCAudience: "tma-api",
		OIDCJWKSURL: provider.server.URL + "/jwks", OIDCSigningAlgs: []string{"RS256"}, OIDCHTTPTimeout: time.Second,
		OIDCRefreshInterval: 20 * time.Millisecond, OIDCMaxStale: 100 * time.Millisecond,
	})
	claims := map[string]any{
		"sub": "stale-user", "iss": provider.server.URL, "aud": "tma-api", "exp": time.Now().Add(time.Hour).Unix(),
		"workspace_id": "wksp_stale", "roles": []string{RoleViewer},
	}
	token := signedOIDCTestToken(t, key, "stale-rsa", "RS256", claims)
	assertOIDCPrincipal(t, server, token, http.StatusOK)
	provider.setJWKSFailure(true)

	time.Sleep(30 * time.Millisecond)
	assertOIDCPrincipal(t, server, token, http.StatusOK)
	if provider.jwksRequestCount() != 2 {
		t.Fatalf("expected failed scheduled refresh plus cached fallback, got %d JWKS requests", provider.jwksRequestCount())
	}

	time.Sleep(80 * time.Millisecond)
	assertOIDCPrincipal(t, server, token, http.StatusUnauthorized)
	if provider.jwksRequestCount() != 3 {
		t.Fatalf("expected expired cache refresh attempt, got %d JWKS requests", provider.jwksRequestCount())
	}
}

func TestUnifiedOIDCClaimMappingDerivesEnterprisePrincipalAndRejectsConflicts(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	provider := newOIDCTestProvider(t)
	provider.setKeys(oidcTestPublicJWK("mapping-rsa", "RS256", &key.PublicKey))
	mapping, err := identity.ParseOIDCClaimMapping(`{
		"subject_claim":"oid",
		"organization_claim":"tid",
		"workspace_claim":"workspace_id",
		"owner_claim":"oid",
		"roles_claim":"realm_access.roles",
		"groups_claim":"groups",
		"role_mappings":{"tma-reader":"viewer"},
		"group_mappings":{"finance-operators":{"organization_id":"org-finance","workspace_id":"wksp-finance","roles":["operator"]}},
		"allowed_workspace_ids":["wksp-finance"],
		"require_group_mapping":true
	}`)
	if err != nil {
		t.Fatalf("parse OIDC claim mapping: %v", err)
	}
	server, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: provider.server.URL, OIDCAudience: "tma-api",
		OIDCJWKSURL: provider.server.URL + "/jwks", OIDCSigningAlgs: []string{"RS256"}, OIDCClaimMapping: mapping,
	})
	baseClaims := map[string]any{
		"sub": "standard-subject", "oid": "entra-user", "tid": "org-finance", "iss": provider.server.URL,
		"aud": "tma-api", "exp": time.Now().Add(time.Hour).Unix(), "groups": []any{"finance-operators"},
		"realm_access": map[string]any{"roles": []any{"tma-reader"}},
	}
	validResponse := httptest.NewRecorder()
	server.ServeHTTP(validResponse, authenticatedRequest(t, http.MethodGet, "/v1/auth/me",
		signedOIDCTestToken(t, key, "mapping-rsa", "RS256", baseClaims)))
	if validResponse.Code != http.StatusOK {
		t.Fatalf("mapped OIDC identity: %d: %s", validResponse.Code, validResponse.Body.String())
	}
	var me struct {
		Principal Principal `json:"principal"`
	}
	decodeTestResponse(t, validResponse, &me)
	if me.Principal.Subject != "entra-user" || me.Principal.OrganizationID != "org-finance" || me.Principal.WorkspaceID != "wksp-finance" || !me.Principal.HasRole(RoleOperator) {
		t.Fatalf("unexpected mapped OIDC principal: %+v", me.Principal)
	}

	for name, overrides := range map[string]map[string]any{
		"conflicting workspace": {"workspace_id": "wksp-other"},
		"unmapped group":        {"groups": []any{"unknown"}},
		"missing subject":       {"oid": ""},
	} {
		t.Run(name, func(t *testing.T) {
			claims := cloneOIDCTestClaims(baseClaims)
			for claimName, value := range overrides {
				claims[claimName] = value
			}
			assertOIDCPrincipal(t, server, signedOIDCTestToken(t, key, "mapping-rsa", "RS256", claims), http.StatusUnauthorized)
		})
	}

	directServer, _ := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeOIDC, OIDCIssuer: provider.server.URL, OIDCAudience: "tma-api",
		OIDCJWKSURL: provider.server.URL + "/jwks", OIDCSigningAlgs: []string{"RS256"},
	})
	directClaims := map[string]any{
		"sub": "direct-user", "iss": provider.server.URL, "aud": "tma-api", "exp": time.Now().Add(time.Hour).Unix(),
		"workspace_id": "wksp-direct", "roles": []any{"external-unknown"},
	}
	assertOIDCPrincipal(t, directServer, signedOIDCTestToken(t, key, "mapping-rsa", "RS256", directClaims), http.StatusUnauthorized)
}

func TestUnifiedScopedStoreRejectsCrossTenantResourcesWritesAndLists(t *testing.T) {
	server, store := newUnifiedAuthTestServer(t, AuthConfig{
		Mode: AuthModeJWT, JWTSecret: testJWTSecret, JWTIssuer: "https://issuer.example", JWTAudience: "tma-api",
	})
	alphaToken := signedTestJWT(t, "alpha-ops", "wksp_alpha", "owner-alpha", []string{RoleOperator}, nil)
	betaToken := signedTestJWT(t, "beta-ops", "wksp_beta", "owner-beta", []string{RoleOperator}, nil)

	alphaAgent, err := store.CreateAgent(managedagents.CreateAgentInput{
		WorkspaceID: "wksp_alpha", Name: "Alpha Agent", LLMProvider: "fake", LLMModel: "fake-demo",
	})
	if err != nil {
		t.Fatalf("create alpha agent: %v", err)
	}
	betaAgent, err := store.CreateAgent(managedagents.CreateAgentInput{
		WorkspaceID: "wksp_beta", Name: "Beta Agent", LLMProvider: "fake", LLMModel: "fake-demo",
	})
	if err != nil {
		t.Fatalf("create beta agent: %v", err)
	}
	alphaObject, err := store.CreateObjectRef(managedagents.CreateObjectRefInput{
		WorkspaceID: "wksp_alpha", Bucket: "artifacts", ObjectKey: "alpha.txt",
	})
	if err != nil {
		t.Fatalf("create alpha object: %v", err)
	}
	alphaWorker, err := store.RegisterWorker(managedagents.RegisterWorkerInput{
		WorkspaceID: "wksp_alpha", Name: "alpha-worker", WorkerType: managedagents.WorkerTypeLocal,
	})
	if err != nil {
		t.Fatalf("create alpha worker: %v", err)
	}
	betaWorker, err := store.RegisterWorker(managedagents.RegisterWorkerInput{
		WorkspaceID: "wksp_beta", Name: "beta-worker", WorkerType: managedagents.WorkerTypeLocal,
	})
	if err != nil {
		t.Fatalf("create beta worker: %v", err)
	}
	alphaWork, err := store.EnqueueWorkerWork(managedagents.EnqueueWorkerWorkInput{
		WorkspaceID: "wksp_alpha", WorkerID: alphaWorker.ID, WorkType: managedagents.WorkerWorkTypeToolExecution,
	})
	if err != nil {
		t.Fatalf("create alpha work: %v", err)
	}
	for _, workspaceID := range []string{"wksp_alpha", "wksp_beta"} {
		if _, err := store.RecordOperatorAudit(managedagents.RecordOperatorAuditInput{
			WorkspaceID: workspaceID, PrincipalID: workspaceID + "-ops", Role: RoleOperator,
			Action: "scope.test", ResourceType: "test", Outcome: "succeeded",
		}); err != nil {
			t.Fatalf("record %s audit: %v", workspaceID, err)
		}
	}

	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "agent read", method: http.MethodGet, path: "/v1/agents/" + alphaAgent.ID},
		{name: "agent write", method: http.MethodPatch, path: "/v1/agents/" + alphaAgent.ID, body: `{"name":"cross-tenant"}`},
		{name: "object read", method: http.MethodGet, path: "/v1/object-refs/" + alphaObject.ID},
		{name: "object delete", method: http.MethodDelete, path: "/v1/object-refs/" + alphaObject.ID},
		{name: "worker read", method: http.MethodGet, path: "/v1/workers/" + alphaWorker.ID},
		{name: "work read", method: http.MethodGet, path: "/v1/worker-work/" + alphaWork.ID},
		{name: "work cancel", method: http.MethodPost, path: "/v1/worker-work/" + alphaWork.ID + "/cancel", body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var request *http.Request
			if test.body == "" {
				request = authenticatedRequest(t, test.method, test.path, betaToken)
			} else {
				request = authenticatedJSONRequest(t, test.method, test.path, test.body, betaToken)
			}
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", response.Code, response.Body.String())
			}
		})
	}

	unchangedAgent, err := store.GetAgent(alphaAgent.ID)
	if err != nil || unchangedAgent.Name != alphaAgent.Name {
		t.Fatalf("cross-tenant agent write changed data: agent=%+v err=%v", unchangedAgent, err)
	}
	unchangedWork, err := store.GetWorkerWork(alphaWork.ID)
	if err != nil || unchangedWork.Status != managedagents.WorkerWorkStatusPending {
		t.Fatalf("cross-tenant work cancel changed data: work=%+v err=%v", unchangedWork, err)
	}
	if _, err := store.GetObjectRef(alphaObject.ID); err != nil {
		t.Fatalf("cross-tenant object delete changed data: %v", err)
	}

	createdObjectResponse := httptest.NewRecorder()
	server.ServeHTTP(createdObjectResponse, authenticatedJSONRequest(t, http.MethodPost, "/v1/object-refs",
		`{"workspace_id":"wksp_beta","bucket":"artifacts","object_key":"derived.txt"}`, alphaToken))
	if createdObjectResponse.Code != http.StatusCreated {
		t.Fatalf("create scoped object: %d: %s", createdObjectResponse.Code, createdObjectResponse.Body.String())
	}
	var createdObject managedagents.ObjectRef
	decodeTestResponse(t, createdObjectResponse, &createdObject)
	if createdObject.WorkspaceID != "wksp_alpha" {
		t.Fatalf("expected server-derived object workspace, got %+v", createdObject)
	}

	agentsResponse := httptest.NewRecorder()
	server.ServeHTTP(agentsResponse, authenticatedRequest(t, http.MethodGet, "/v1/agents", alphaToken))
	var agentsPayload struct {
		Agents []managedagents.Agent `json:"agents"`
	}
	decodeTestResponse(t, agentsResponse, &agentsPayload)
	for _, agent := range agentsPayload.Agents {
		if agent.WorkspaceID != "wksp_alpha" || agent.ID == betaAgent.ID {
			t.Fatalf("agent list leaked another workspace: %+v", agentsPayload.Agents)
		}
	}

	workersResponse := httptest.NewRecorder()
	server.ServeHTTP(workersResponse, authenticatedRequest(t, http.MethodGet, "/v1/workers?workspace_id=wksp_beta", alphaToken))
	var workersPayload struct {
		Workers []managedagents.Worker `json:"workers"`
	}
	decodeTestResponse(t, workersResponse, &workersPayload)
	if len(workersPayload.Workers) != 1 || workersPayload.Workers[0].ID != alphaWorker.ID || workersPayload.Workers[0].ID == betaWorker.ID {
		t.Fatalf("worker list was not workspace scoped: %+v", workersPayload.Workers)
	}

	auditResponse := httptest.NewRecorder()
	server.ServeHTTP(auditResponse, authenticatedRequest(t, http.MethodGet, "/v1/operator-audit?workspace_id=wksp_beta&action=scope.test", alphaToken))
	var auditPayload struct {
		Records []managedagents.OperatorAuditRecord `json:"audit_records"`
	}
	decodeTestResponse(t, auditResponse, &auditPayload)
	if len(auditPayload.Records) != 1 || auditPayload.Records[0].WorkspaceID != "wksp_alpha" {
		t.Fatalf("audit list leaked another workspace: %+v", auditPayload.Records)
	}
}

func newUnifiedAuthTestServer(t *testing.T, auth AuthConfig) (http.Handler, *testStore) {
	t.Helper()
	store := newTestStore()
	turnRunner := runner.NewMockRunner(store, time.Millisecond, nil)
	server := NewServerWithStoreRunnerLLMDefaultsAndObjectStoreExecutionResolverUnifiedAuthSubagentPolicyAndBinaryScanner(
		store, turnRunner, nil, "fake", "fake-demo", objectstore.NewNoopClient(objectstore.Config{}), defaultExecutionResolver(store),
		"worker-secret", "legacy-control-secret", auth, defaultSubagentPolicy(), nil,
	)
	return server, store
}

func signedTestJWT(t *testing.T, subject string, workspaceID string, ownerID string, roles []string, overrides map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	claims := map[string]any{
		"sub": subject, "iss": "https://issuer.example", "aud": "tma-api",
		"exp": time.Now().Add(time.Hour).Unix(), "workspace_id": workspaceID, "owner_id": ownerID, "roles": roles,
	}
	for key, value := range overrides {
		claims[key] = value
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(mustMarshalTestJSON(t, header))
	encodedClaims := base64.RawURLEncoding.EncodeToString(mustMarshalTestJSON(t, claims))
	unsigned := encodedHeader + "." + encodedClaims
	mac := hmac.New(sha256.New, []byte(testJWTSecret))
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mustMarshalTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal test JSON: %v", err)
	}
	return encoded
}

func authenticatedJSONRequest(t *testing.T, method string, path string, body string, token string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func authenticatedRequest(t *testing.T, method string, path string, token string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

func decodeTestResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

type oidcTestProvider struct {
	server *httptest.Server

	mu                sync.RWMutex
	keys              []jose.JSONWebKey
	failJWKS          bool
	jwksRequests      int
	discoveryRequests int
}

func newOIDCTestProvider(t *testing.T) *oidcTestProvider {
	t.Helper()
	provider := &oidcTestProvider{}
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			provider.mu.Lock()
			provider.discoveryRequests++
			provider.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"issuer":                                provider.server.URL,
				"jwks_uri":                              provider.server.URL + "/jwks",
				"authorization_endpoint":                provider.server.URL + "/authorize",
				"token_endpoint":                        provider.server.URL + "/token",
				"id_token_signing_alg_values_supported": []string{"RS256", "ES256"},
			})
		case "/jwks":
			provider.mu.Lock()
			provider.jwksRequests++
			fail := provider.failJWKS
			keys := append([]jose.JSONWebKey(nil), provider.keys...)
			provider.mu.Unlock()
			if fail {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "jwks unavailable"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *oidcTestProvider) setKeys(keys ...jose.JSONWebKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append([]jose.JSONWebKey(nil), keys...)
}

func (p *oidcTestProvider) setJWKSFailure(fail bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failJWKS = fail
}

func (p *oidcTestProvider) jwksRequestCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.jwksRequests
}

func (p *oidcTestProvider) discoveryRequestCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.discoveryRequests
}

func oidcTestPublicJWK(kid string, algorithm string, key any) jose.JSONWebKey {
	return jose.JSONWebKey{Key: key, KeyID: kid, Algorithm: algorithm, Use: "sig"}
}

func signedOIDCTestToken(t *testing.T, key any, kid string, algorithm string, claims map[string]any) string {
	t.Helper()
	options := (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.SignatureAlgorithm(algorithm), Key: key}, options)
	if err != nil {
		t.Fatalf("create %s signer: %v", algorithm, err)
	}
	token, err := josejwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign %s token: %v", algorithm, err)
	}
	return token
}

func cloneOIDCTestClaims(claims map[string]any) map[string]any {
	clone := make(map[string]any, len(claims))
	for key, value := range claims {
		clone[key] = value
	}
	return clone
}

func assertOIDCPrincipal(t *testing.T, handler http.Handler, token string, expectedStatus int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(t, http.MethodGet, "/v1/auth/me", token))
	if response.Code != expectedStatus {
		t.Fatalf("expected OIDC status %d, got %d: %s", expectedStatus, response.Code, response.Body.String())
	}
	if expectedStatus == http.StatusOK && !bytes.Contains(response.Body.Bytes(), []byte(`"auth_type":"oidc"`)) {
		t.Fatalf("expected OIDC principal response, got %s", response.Body.String())
	}
}
