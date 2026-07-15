package managedagents

import (
	"context"
	"errors"
	"testing"
)

func TestValidateAccessScopeRequiresWorkspaceAndNormalizesValues(t *testing.T) {
	if _, err := ValidateAccessScope(AccessScope{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected missing workspace to be invalid, got %v", err)
	}

	scope, err := ValidateAccessScope(AccessScope{WorkspaceID: "  wksp_alpha ", OwnerID: " owner-alpha  "})
	if err != nil {
		t.Fatalf("validate access scope: %v", err)
	}
	if scope.WorkspaceID != "wksp_alpha" || scope.OwnerID != "owner-alpha" {
		t.Fatalf("unexpected normalized scope: %+v", scope)
	}
}

func TestDatabaseAccessScopeContextRoundTrip(t *testing.T) {
	ctx, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{WorkspaceID: " wksp_alpha ", OwnerID: " owner-alpha "})
	if err != nil {
		t.Fatalf("attach database access scope: %v", err)
	}
	scope, ok := DatabaseAccessScopeFromContext(ctx)
	if !ok || scope.WorkspaceID != "wksp_alpha" || scope.OwnerID != "owner-alpha" {
		t.Fatalf("unexpected database access scope: ok=%v scope=%+v", ok, scope)
	}
	if _, err := ContextWithDatabaseAccessScope(context.Background(), AccessScope{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid database access scope rejection, got %v", err)
	}
}
