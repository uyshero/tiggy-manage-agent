package managedagents

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	objectRefLinkOwnerSessionArtifact        = "session_artifact"
	objectRefLinkOwnerSkillAsset             = "skill_asset"
	objectRefLinkOwnerSkillVersion           = "skill_version"
	objectRefLinkOwnerSkillPackageFile       = "skill_package_file"
	objectRefLinkOwnerWorkspaceSnapshot      = "workspace_snapshot"
	objectRefLinkOwnerAchievementLibraryItem = "achievement_library_item"

	objectRefLinkRoleAsset          = "asset"
	objectRefLinkRolePackageArchive = "package_archive"
	objectRefLinkRoleSkillMarkdown  = "skill_md"
	objectRefLinkRoleSnapshot       = "snapshot"
	objectRefLinkRoleAchievement    = "achievement"
)

func insertObjectRefLink(ctx context.Context, tx *sql.Tx, workspaceID, objectRefID, ownerType, ownerID, role string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	objectRefID = strings.TrimSpace(objectRefID)
	ownerType = strings.TrimSpace(ownerType)
	ownerID = strings.TrimSpace(ownerID)
	role = strings.TrimSpace(role)
	if workspaceID == "" || objectRefID == "" || ownerType == "" || ownerID == "" || role == "" {
		return fmt.Errorf("%w: object ref link requires workspace, object, owner, and role", ErrInvalid)
	}
	var objectVisible bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM object_refs WHERE id = $1 AND workspace_id = $2)
	`, objectRefID, workspaceID).Scan(&objectVisible); err != nil {
		return err
	}
	if !objectVisible {
		return fmt.Errorf("%w: object ref link workspace mismatch", ErrForbidden)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO object_ref_links (object_ref_id, workspace_id, owner_type, owner_id, role)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT DO NOTHING
	`, objectRefID, workspaceID, ownerType, ownerID, role)
	return err
}

func deleteObjectRefLinksByOwner(ctx context.Context, tx *sql.Tx, workspaceID, ownerType, ownerID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	ownerType = strings.TrimSpace(ownerType)
	ownerID = strings.TrimSpace(ownerID)
	if workspaceID == "" || ownerType == "" || ownerID == "" {
		return fmt.Errorf("%w: object ref link owner is required", ErrInvalid)
	}
	_, err := tx.ExecContext(ctx, `
		DELETE FROM object_ref_links
		WHERE workspace_id = $1 AND owner_type = $2 AND owner_id = $3
	`, workspaceID, ownerType, ownerID)
	return err
}

func skillPackageFileObjectRefLinkOwnerID(skillVersionID, path string) string {
	return strings.TrimSpace(skillVersionID) + ":" + strings.TrimSpace(path)
}

func skillAssetObjectRefLinkOwnerID(skillVersionID, path string) string {
	return strings.TrimSpace(skillVersionID) + ":" + strings.TrimSpace(path)
}
