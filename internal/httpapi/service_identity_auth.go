package httpapi

import (
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

const (
	AuthTypeServiceCredential = "service_credential"
	AuthTypeDelegated         = "delegated"
	serviceCredentialPrefix   = "tma_svc_"
)

func (p Principal) HasScope(required string) bool {
	required = strings.TrimSpace(required)
	if required == "" {
		return true
	}
	for _, scope := range p.Scopes {
		if strings.TrimSpace(scope) == required {
			return true
		}
	}
	return false
}

func (s *Server) authenticateServiceCredential(r *http.Request) (Principal, bool, error) {
	token := bearerToken(r.Header.Get("Authorization"))
	if !strings.HasPrefix(token, serviceCredentialPrefix) {
		return Principal{}, false, nil
	}
	store, ok := s.store.(managedagents.ServiceIdentityStore)
	if !ok {
		return Principal{}, true, errors.New("service credential authentication is unavailable")
	}
	payload := strings.TrimPrefix(token, serviceCredentialPrefix)
	locator, secret, found := strings.Cut(payload, ".")
	if !found || locator == "" || secret == "" || strings.Contains(secret, ".") {
		return Principal{}, true, errors.New("invalid service credential")
	}
	secretHash := sha256.Sum256([]byte(secret))
	authenticated, err := store.AuthenticateServiceCredential(r.Context(), locator, secretHash[:])
	if err != nil {
		return Principal{}, true, errors.New("invalid service credential")
	}
	subject := "service_identity:" + authenticated.Identity.ID
	principal, err := normalizePrincipal(Principal{
		Subject: subject, Username: authenticated.Identity.Name,
		WorkspaceID: authenticated.Identity.WorkspaceID, OwnerID: subject,
		Roles: []string{authenticated.Identity.Role}, ServiceIdentityID: authenticated.Identity.ID,
		ServiceCredentialID: authenticated.CredentialID,
		Scopes:              authenticated.Identity.Scopes, AuthType: AuthTypeServiceCredential,
		AuthorizationSources: []string{"service_identity", "service_credential", "service_scope"},
	})
	return principal, true, err
}

func serviceIdentityScopeForRequest(r *http.Request) (string, bool) {
	if r == nil {
		return "", false
	}
	path := strings.Trim(strings.TrimSpace(r.URL.Path), "/")
	segments := strings.Split(path, "/")
	if len(segments) < 2 || (segments[0] != "v1" && segments[0] != "v2") {
		return "", false
	}
	resource := segments[1]
	if resource == "auth" && len(segments) == 3 {
		if segments[2] == "me" && r.Method == http.MethodGet {
			return "", true
		}
		if segments[2] == "token-exchange" && r.Method == http.MethodPost {
			return "", true
		}
	}
	if resource == "model-runtime" && len(segments) == 3 && r.Method == http.MethodPost {
		switch segments[2] {
		case "generate":
			return managedagents.ServiceScopeModelGenerate, true
		case "embeddings":
			return managedagents.ServiceScopeModelEmbedding, true
		case "rerank":
			return managedagents.ServiceScopeModelRerank, true
		}
		return "", false
	}
	if resource == "speech" && len(segments) == 3 && segments[2] == "realtime" && r.Method == http.MethodGet {
		return managedagents.ServiceScopeSpeechRealtime, true
	}
	if resource == "retrieval" {
		if len(segments) == 3 && segments[2] == "search" && r.Method == http.MethodPost {
			return managedagents.ServiceScopeRetrievalRead, true
		}
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeRetrievalRead, managedagents.ServiceScopeRetrievalWrite)
	}
	switch resource {
	case "agent", "agents":
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeAgentsRead, managedagents.ServiceScopeAgentsWrite)
	case "sessions", "runs":
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeSessionsRead, managedagents.ServiceScopeSessionsWrite)
	case "skills", "skill-marketplace-entries":
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeSkillsRead, managedagents.ServiceScopeSkillsWrite)
	case "mcp-servers", "mcp-registry":
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeMCPRead, managedagents.ServiceScopeMCPWrite)
	case "environments":
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeEnvironmentsRead, managedagents.ServiceScopeEnvironmentsWrite)
	case "object-refs", "artifacts", "achievement-library":
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeArtifactsRead, managedagents.ServiceScopeArtifactsWrite)
	case "evaluations", "evaluation-rubrics", "evaluation-datasets", "evaluation-experiments":
		return readWriteServiceScope(r.Method, managedagents.ServiceScopeEvaluationsRead, managedagents.ServiceScopeEvaluationsWrite)
	default:
		return "", false
	}
}

func readWriteServiceScope(method, readScope, writeScope string) (string, bool) {
	if isSafeRequestMethod(method) {
		return readScope, true
	}
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return writeScope, true
	default:
		return "", false
	}
}
