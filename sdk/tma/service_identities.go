package tma

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type ServiceIdentity struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Role        string    `json:"role"`
	Scopes      []string  `json:"scopes"`
	Status      string    `json:"status"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateServiceIdentityRequest struct {
	Kind        string   `json:"kind,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Role        string   `json:"role,omitempty"`
	Scopes      []string `json:"scopes"`
}

type UpdateServiceIdentityRequest struct {
	Name        *string   `json:"name,omitempty"`
	Description *string   `json:"description,omitempty"`
	Role        *string   `json:"role,omitempty"`
	Scopes      *[]string `json:"scopes,omitempty"`
	Status      *string   `json:"status,omitempty"`
}

type ServiceCredential struct {
	ID                string     `json:"id"`
	WorkspaceID       string     `json:"workspace_id"`
	ServiceIdentityID string     `json:"service_identity_id"`
	Name              string     `json:"name"`
	TokenPrefix       string     `json:"token_prefix"`
	Status            string     `json:"status"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

type CreateServiceCredentialRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type CreatedServiceCredential struct {
	Credential ServiceCredential `json:"credential"`
	Token      string            `json:"token"`
}

type ServiceIdentityService struct{ client *Client }

func (s *ServiceIdentityService) Scopes(ctx context.Context) ([]string, error) {
	var response struct {
		Scopes []string `json:"scopes"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/service-identities/scopes", nil, &response)
	return response.Scopes, err
}

func (s *ServiceIdentityService) List(ctx context.Context) ([]ServiceIdentity, error) {
	var response struct {
		ServiceIdentities []ServiceIdentity `json:"service_identities"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, "/v2/service-identities", nil, &response)
	return response.ServiceIdentities, err
}

func (s *ServiceIdentityService) Create(ctx context.Context, request CreateServiceIdentityRequest) (ServiceIdentity, error) {
	var identity ServiceIdentity
	err := s.client.DoJSON(ctx, http.MethodPost, "/v2/service-identities", request, &identity)
	return identity, err
}

func (s *ServiceIdentityService) Get(ctx context.Context, identityID string) (ServiceIdentity, error) {
	var identity ServiceIdentity
	err := s.client.DoJSON(ctx, http.MethodGet, serviceIdentityPath(identityID), nil, &identity)
	return identity, err
}

func (s *ServiceIdentityService) Update(ctx context.Context, identityID string, request UpdateServiceIdentityRequest) (ServiceIdentity, error) {
	var identity ServiceIdentity
	err := s.client.DoJSON(ctx, http.MethodPatch, serviceIdentityPath(identityID), request, &identity)
	return identity, err
}

func (s *ServiceIdentityService) Credentials(ctx context.Context, identityID string) ([]ServiceCredential, error) {
	var response struct {
		Credentials []ServiceCredential `json:"credentials"`
	}
	err := s.client.DoJSON(ctx, http.MethodGet, serviceIdentityPath(identityID)+"/credentials", nil, &response)
	return response.Credentials, err
}

func (s *ServiceIdentityService) CreateCredential(ctx context.Context, identityID string, request CreateServiceCredentialRequest) (CreatedServiceCredential, error) {
	var credential CreatedServiceCredential
	err := s.client.DoJSON(ctx, http.MethodPost, serviceIdentityPath(identityID)+"/credentials", request, &credential)
	return credential, err
}

func (s *ServiceIdentityService) RevokeCredential(ctx context.Context, identityID, credentialID string) error {
	return s.client.DoJSON(ctx, http.MethodDelete, serviceIdentityPath(identityID)+"/credentials/"+url.PathEscape(credentialID), nil, nil)
}

func serviceIdentityPath(identityID string) string {
	return "/v2/service-identities/" + url.PathEscape(identityID)
}
