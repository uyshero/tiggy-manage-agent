CREATE SEQUENCE IF NOT EXISTS tma_evaluation_rubric_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_run_evaluation_id_seq;

CREATE TABLE IF NOT EXISTS evaluation_rubrics (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  criteria_json JSONB NOT NULL,
  revision BIGINT NOT NULL DEFAULT 1,
  created_by TEXT NOT NULL DEFAULT '',
  updated_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT evaluation_rubrics_name_check CHECK (btrim(name) <> ''),
  CONSTRAINT evaluation_rubrics_criteria_check CHECK (jsonb_typeof(criteria_json) = 'array'),
  CONSTRAINT evaluation_rubrics_revision_check CHECK (revision > 0)
);

CREATE INDEX IF NOT EXISTS idx_evaluation_rubrics_workspace_updated
  ON evaluation_rubrics(workspace_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS run_evaluations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  left_session_id TEXT NOT NULL,
  left_turn_id TEXT NOT NULL,
  right_session_id TEXT NOT NULL,
  right_turn_id TEXT NOT NULL,
  rubric_id TEXT REFERENCES evaluation_rubrics(id) ON DELETE SET NULL,
  rubric_snapshot_json JSONB NOT NULL,
  scores_json JSONB NOT NULL,
  conclusion TEXT NOT NULL,
  notes TEXT NOT NULL DEFAULT '',
  evaluation_type TEXT NOT NULL DEFAULT 'manual',
  judge_provider TEXT NOT NULL DEFAULT '',
  judge_model TEXT NOT NULL DEFAULT '',
  judge_reasoning TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT run_evaluations_left_run_fkey
    FOREIGN KEY (left_session_id, left_turn_id)
    REFERENCES session_turns(session_id, id) ON DELETE CASCADE,
  CONSTRAINT run_evaluations_right_run_fkey
    FOREIGN KEY (right_session_id, right_turn_id)
    REFERENCES session_turns(session_id, id) ON DELETE CASCADE,
  CONSTRAINT run_evaluations_distinct_runs_check
    CHECK (left_session_id <> right_session_id OR left_turn_id <> right_turn_id),
  CONSTRAINT run_evaluations_snapshot_check CHECK (jsonb_typeof(rubric_snapshot_json) = 'object'),
  CONSTRAINT run_evaluations_scores_check CHECK (jsonb_typeof(scores_json) = 'array'),
  CONSTRAINT run_evaluations_conclusion_check
    CHECK (conclusion IN ('left', 'right', 'tie', 'inconclusive')),
  CONSTRAINT run_evaluations_type_check
    CHECK (evaluation_type IN ('manual', 'auto'))
);

ALTER TABLE run_evaluations
  ADD COLUMN IF NOT EXISTS evaluation_type TEXT NOT NULL DEFAULT 'manual',
  ADD COLUMN IF NOT EXISTS judge_provider TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS judge_model TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS judge_reasoning TEXT NOT NULL DEFAULT '';

ALTER TABLE run_evaluations DROP CONSTRAINT IF EXISTS run_evaluations_type_check;
ALTER TABLE run_evaluations ADD CONSTRAINT run_evaluations_type_check
  CHECK (evaluation_type IN ('manual', 'auto'));

CREATE INDEX IF NOT EXISTS idx_run_evaluations_workspace_created
  ON run_evaluations(workspace_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_run_evaluations_pair_created
  ON run_evaluations(left_session_id, left_turn_id, right_session_id, right_turn_id, created_at DESC, id DESC);

ALTER TABLE evaluation_rubrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE evaluation_rubrics FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS evaluation_rubrics_workspace_isolation ON evaluation_rubrics;
CREATE POLICY evaluation_rubrics_workspace_isolation
  ON evaluation_rubrics
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

ALTER TABLE run_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE run_evaluations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS run_evaluations_workspace_isolation ON run_evaluations;
CREATE POLICY run_evaluations_workspace_isolation
  ON run_evaluations
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
