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
	objectRefLinkOwnerKnowledgeDocument      = "knowledge_document"

	objectRefLinkRoleAsset           = "asset"
	objectRefLinkRolePackageArchive  = "package_archive"
	objectRefLinkRoleSkillMarkdown   = "skill_md"
	objectRefLinkRoleSnapshot        = "snapshot"
	objectRefLinkRoleAchievement     = "achievement"
	objectRefLinkRoleKnowledgeSource = "knowledge_source"
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

type objectRefLinkScanner interface{ Scan(dest ...any) error }

func scanObjectRefLink(scanner objectRefLinkScanner) (ObjectRefLink, error) {
	var link ObjectRefLink
	err := scanner.Scan(&link.ObjectRefID, &link.WorkspaceID, &link.OwnerType, &link.OwnerID, &link.Role, &link.CreatedAt)
	return link, err
}

func scanObjectRefLinks(rows *sql.Rows) ([]ObjectRefLink, error) {
	defer rows.Close()
	links := []ObjectRefLink{}
	for rows.Next() {
		link, err := scanObjectRefLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return links, nil
}

func (s *PostgresStore) listObjectRefLinksTx(ctx context.Context, tx *sql.Tx, objectRefID, workspaceID string) ([]ObjectRefLink, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT object_ref_id, workspace_id, owner_type, owner_id, role, created_at
		FROM object_ref_links
		WHERE object_ref_id = $1 AND ($2 = '' OR workspace_id = $2)
		ORDER BY created_at ASC, owner_type ASC, owner_id ASC, role ASC
	`, strings.TrimSpace(objectRefID), strings.TrimSpace(workspaceID))
	if err != nil {
		return nil, err
	}
	return scanObjectRefLinks(rows)
}

func objectRefLinkSummary(links []ObjectRefLink, limit int) string {
	if len(links) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(links) {
		limit = len(links)
	}
	parts := make([]string, 0, limit+1)
	for _, link := range links[:limit] {
		item := link.OwnerType + "/" + link.OwnerID
		if link.Role != "" {
			item += "(" + link.Role + ")"
		}
		parts = append(parts, item)
	}
	if len(links) > limit {
		parts = append(parts, fmt.Sprintf("and %d more", len(links)-limit))
	}
	return strings.Join(parts, ", ")
}
