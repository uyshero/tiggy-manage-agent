package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"tiggy-manage-agent/internal/managedagents"
)

func bindApplicationResource(r *http.Request, appID *string) error {
	if appID == nil {
		return fmt.Errorf("%w: app_id target is required", managedagents.ErrInvalid)
	}
	*appID = strings.TrimSpace(*appID)
	principal, ok := PrincipalFromRequest(r)
	if !ok || principal.ServiceIdentityID == "" {
		return nil
	}
	if *appID != "" && *appID != principal.ServiceIdentityID {
		return fmt.Errorf("%w: application credentials cannot assign resources to another app_id", managedagents.ErrForbidden)
	}
	*appID = principal.ServiceIdentityID
	return nil
}

func matchesApplicationResource(appID, externalRef, requestedAppID, requestedExternalRef string) bool {
	requestedAppID = strings.TrimSpace(requestedAppID)
	requestedExternalRef = strings.TrimSpace(requestedExternalRef)
	return (requestedAppID == "" || appID == requestedAppID) &&
		(requestedExternalRef == "" || externalRef == requestedExternalRef)
}
