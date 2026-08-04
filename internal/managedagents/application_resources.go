package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"tiggy-manage-agent/internal/appresource"
)

func normalizeApplicationOwnership(appID, externalRef string, labels map[string]string) (appresource.Ownership, error) {
	ownership, err := appresource.Normalize(appID, externalRef, labels)
	if err != nil {
		return appresource.Ownership{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return ownership, nil
}

func validateApplicationIdentityTx(ctx context.Context, tx *sql.Tx, workspaceID, appID string) error {
	if strings.TrimSpace(appID) == "" {
		return nil
	}
	var kind, status string
	if err := tx.QueryRowContext(ctx, `
		SELECT kind, status
		FROM service_identities
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, appID).Scan(&kind, &status); err == sql.ErrNoRows {
		return fmt.Errorf("%w: application identity %s", ErrNotFound, appID)
	} else if err != nil {
		return err
	}
	if kind != ServiceIdentityKindApplication {
		return fmt.Errorf("%w: app_id must reference an application identity", ErrInvalid)
	}
	if status != ServiceIdentityStatusActive {
		return fmt.Errorf("%w: application identity %s is not active", ErrInvalid, appID)
	}
	return nil
}

func marshalResourceLabels(labels map[string]string) ([]byte, error) {
	if labels == nil {
		labels = map[string]string{}
	}
	return json.Marshal(labels)
}

func unmarshalResourceLabels(raw []byte) (map[string]string, error) {
	labels := map[string]string{}
	if len(raw) == 0 {
		return labels, nil
	}
	if err := json.Unmarshal(raw, &labels); err != nil {
		return nil, fmt.Errorf("decode application resource labels: %w", err)
	}
	return labels, nil
}

func normalizeApplicationResourceWriteError(err error) error {
	if postgresUniqueViolation(err) {
		return fmt.Errorf("%w: application resource identity already exists", ErrConflict)
	}
	return err
}
