WITH ranked_environments AS (
  SELECT
    id,
    row_number() OVER (
      PARTITION BY workspace_id, lower(btrim(name))
      ORDER BY created_at, id
    ) AS active_name_position
  FROM environments
  WHERE archived_at IS NULL
)
UPDATE environments AS environment
SET archived_at = now()
FROM ranked_environments AS ranked
WHERE environment.id = ranked.id
  AND ranked.active_name_position > 1;

CREATE UNIQUE INDEX IF NOT EXISTS idx_environments_workspace_active_name_unique
  ON environments(workspace_id, (lower(btrim(name))))
  WHERE archived_at IS NULL;
