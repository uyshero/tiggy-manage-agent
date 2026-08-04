package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"tiggy-manage-agent/internal/appmanifest"
	"tiggy-manage-agent/internal/managedagents"
)

type publishApplicationManifestRequest struct {
	AppID    string               `json:"app_id,omitempty"`
	Manifest appmanifest.Manifest `json:"manifest"`
}

func (s *Server) registerApplicationManifestRoutes() {
	s.mux.HandleFunc("POST /v2/application-manifests/publish", s.withV2Request(s.publishApplicationManifest))
}

func (s *Server) publishApplicationManifest(w http.ResponseWriter, r *http.Request) {
	publisher, ok := s.store.(appmanifest.Publisher)
	if !ok {
		writeV2Error(w, requestIDFromRequest(r), http.StatusNotImplemented, "manifest_publish_unavailable", "application manifest publishing is unavailable", false, nil)
		return
	}
	var request publishApplicationManifestRequest
	if err := decodeJSON(r, &request); err != nil {
		writeV2Error(w, requestIDFromRequest(r), http.StatusBadRequest, "invalid_request", err.Error(), false, nil)
		return
	}
	principal, ok := PrincipalFromRequest(r)
	if !ok {
		writeV2ManagedError(w, r, managedagents.ErrForbidden)
		return
	}
	if principal.AuthType == AuthTypeDelegated {
		writeV2ManagedError(w, r, fmt.Errorf("%w: delegated user tokens cannot publish application manifests", managedagents.ErrForbidden))
		return
	}
	if principal.ServiceIdentityID == "" && !principal.HasRole(RoleOperator) {
		writeV2ManagedError(w, r, fmt.Errorf("%w: operator role required to publish an application manifest", managedagents.ErrForbidden))
		return
	}
	if err := bindApplicationResource(r, &request.AppID); err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	if strings.TrimSpace(request.AppID) == "" {
		writeV2ManagedError(w, r, fmt.Errorf("%w: app_id is required", managedagents.ErrInvalid))
		return
	}
	result, err := publisher.PublishApplicationManifest(r.Context(), appmanifest.PublishInput{
		WorkspaceID: principal.WorkspaceID,
		AppID:       request.AppID,
		PublishedBy: requestActorID(r, principal.Subject),
		Manifest:    request.Manifest,
	})
	if err != nil {
		writeV2ManagedError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
