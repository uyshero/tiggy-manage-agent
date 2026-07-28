package managedagents

import (
	"context"
	"database/sql"
	"encoding/json"
)

const toolExportArtifactProtocolVersion = "tma.tool_export.v1"

func finalAgentMessagePayload(ctx context.Context, tx *sql.Tx, sessionID string, turnID string, payload json.RawMessage) (json.RawMessage, error) {
	artifactIDs, err := turnExportedArtifactIDs(ctx, tx, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	return payloadWithArtifactIDs(payloadWithTurnID(payload, turnID), artifactIDs), nil
}

func turnExportedArtifactIDs(ctx context.Context, tx *sql.Tx, sessionID string, turnID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM session_artifacts
		WHERE session_id = $1
			AND turn_id = $2
			AND metadata_json->>'protocol_version' = $3
		ORDER BY created_at ASC, id ASC
	`, sessionID, turnID, toolExportArtifactProtocolVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := []string{}
	seen := map[string]struct{}{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
