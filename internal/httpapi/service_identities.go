package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tiggy-manage-agent/internal/managedagents"
)

type createdServiceCredentialResponse struct {
	Credential managedagents.ServiceCredential `json:"credential"`
	Token      string                          `json:"token"`
}

func (s *Server) registerServiceIdentityRoutes() {
	s.mux.HandleFunc("GET /v2/service-identities/scopes", s.withV2Request(s.requireWorkspaceAdmin(s.listServiceIdentityScopes)))
	s.mux.HandleFunc("GET /v2/service-identities", s.withV2Request(s.requireWorkspaceAdmin(s.listServiceIdentities)))
	s.mux.HandleFunc("POST /v2/service-identities", s.withV2Request(s.requireWorkspaceAdmin(s.createServiceIdentity)))
	s.mux.HandleFunc("GET /v2/service-identities/{identity_id}", s.withV2Request(s.requireWorkspaceAdmin(s.getServiceIdentity)))
	s.mux.HandleFunc("PATCH /v2/service-identities/{identity_id}", s.withV2Request(s.requireWorkspaceAdmin(s.updateServiceIdentity)))
	s.mux.HandleFunc("GET /v2/service-identities/{identity_id}/credentials", s.withV2Request(s.requireWorkspaceAdmin(s.listServiceCredentials)))
	s.mux.HandleFunc("POST /v2/service-identities/{identity_id}/credentials", s.withV2Request(s.requireWorkspaceAdmin(s.createServiceCredential)))
	s.mux.HandleFunc("DELETE /v2/service-identities/{identity_id}/credentials/{credential_id}", s.withV2Request(s.requireWorkspaceAdmin(s.revokeServiceCredential)))
}

func (s *Server) serviceIdentityStore() (managedagents.ServiceIdentityStore, error) {
	store, ok := s.store.(managedagents.ServiceIdentityStore)
	if !ok {
		return nil, fmt.Errorf("%w: service identity store is unavailable", managedagents.ErrInvalid)
	}
	return store, nil
}

func (s *Server) listServiceIdentityScopes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"scopes": managedagents.SupportedServiceIdentityScopes()})
}

func (s *Server) listServiceIdentities(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.serviceIdentityStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListServiceIdentities(r.Context(), principal.WorkspaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service_identities": items})
}

func (s *Server) createServiceIdentity(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	var request struct {
		Kind        string   `json:"kind"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Role        string   `json:"role"`
		Scopes      []string `json:"scopes"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	store, err := s.serviceIdentityStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.CreateServiceIdentity(r.Context(), managedagents.CreateServiceIdentityInput{
		WorkspaceID: principal.WorkspaceID, Kind: request.Kind, Name: request.Name,
		Description: request.Description, Role: request.Role, Scopes: request.Scopes, CreatedBy: principal.Subject,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getServiceIdentity(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.serviceIdentityStore()
	if err != nil {
		writeError(w, err)
		return
	}
	item, err := store.GetServiceIdentity(r.Context(), principal.WorkspaceID, r.PathValue("identity_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateServiceIdentity(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.serviceIdentityStore()
	if err != nil {
		writeError(w, err)
		return
	}
	current, err := store.GetServiceIdentity(r.Context(), principal.WorkspaceID, r.PathValue("identity_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var request struct {
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
		Role        *string   `json:"role"`
		Scopes      *[]string `json:"scopes"`
		Status      *string   `json:"status"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if request.Name != nil {
		current.Name = *request.Name
	}
	if request.Description != nil {
		current.Description = *request.Description
	}
	if request.Role != nil {
		current.Role = *request.Role
	}
	if request.Scopes != nil {
		current.Scopes = *request.Scopes
	}
	if request.Status != nil {
		current.Status = *request.Status
	}
	item, err := store.UpdateServiceIdentity(r.Context(), managedagents.UpdateServiceIdentityInput{
		WorkspaceID: principal.WorkspaceID, ID: current.ID, Name: current.Name, Description: current.Description,
		Role: current.Role, Scopes: current.Scopes, Status: current.Status,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listServiceCredentials(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.serviceIdentityStore()
	if err != nil {
		writeError(w, err)
		return
	}
	items, err := store.ListServiceCredentials(r.Context(), principal.WorkspaceID, r.PathValue("identity_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": items})
}

func (s *Server) createServiceCredential(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	var request struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	locator, err := randomServiceCredentialPart(18)
	if err != nil {
		writeError(w, err)
		return
	}
	secret, err := randomServiceCredentialPart(32)
	if err != nil {
		writeError(w, err)
		return
	}
	token := serviceCredentialPrefix + locator + "." + secret
	secretHash := sha256.Sum256([]byte(secret))
	tokenPrefix := serviceCredentialPrefix + locator[:8]
	store, err := s.serviceIdentityStore()
	if err != nil {
		writeError(w, err)
		return
	}
	credential, err := store.CreateServiceCredential(r.Context(), managedagents.CreateServiceCredentialInput{
		WorkspaceID: principal.WorkspaceID, ServiceIdentityID: r.PathValue("identity_id"), Name: request.Name,
		Locator: locator, TokenPrefix: tokenPrefix, SecretHash: secretHash[:], ExpiresAt: request.ExpiresAt, CreatedBy: principal.Subject,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, createdServiceCredentialResponse{Credential: credential, Token: token})
}

func (s *Server) revokeServiceCredential(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.administrationPrincipal(r)
	store, err := s.serviceIdentityStore()
	if err != nil {
		writeError(w, err)
		return
	}
	if err := store.RevokeServiceCredential(r.Context(), principal.WorkspaceID, r.PathValue("identity_id"), r.PathValue("credential_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func randomServiceCredentialPart(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate service credential: %w", err)
	}
	return strings.TrimRight(base64.RawURLEncoding.EncodeToString(buffer), "="), nil
}
