CREATE SEQUENCE IF NOT EXISTS tma_evaluation_dataset_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_evaluation_dataset_item_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_evaluation_experiment_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_evaluation_experiment_item_id_seq;

CREATE TABLE IF NOT EXISTS evaluation_datasets (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT evaluation_datasets_name_check CHECK (btrim(name) <> '')
);

CREATE INDEX IF NOT EXISTS idx_evaluation_datasets_workspace_updated
  ON evaluation_datasets(workspace_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS evaluation_dataset_items (
  id TEXT PRIMARY KEY,
  dataset_id TEXT NOT NULL REFERENCES evaluation_datasets(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  item_index INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  expected_output TEXT NOT NULL DEFAULT '',
  tags_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT evaluation_dataset_items_prompt_check CHECK (btrim(prompt) <> ''),
  CONSTRAINT evaluation_dataset_items_tags_check CHECK (jsonb_typeof(tags_json) = 'array'),
  CONSTRAINT evaluation_dataset_items_index_unique UNIQUE (dataset_id, item_index)
);

CREATE INDEX IF NOT EXISTS idx_evaluation_dataset_items_dataset
  ON evaluation_dataset_items(dataset_id, item_index);

CREATE TABLE IF NOT EXISTS evaluation_experiments (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  dataset_id TEXT REFERENCES evaluation_datasets(id) ON DELETE SET NULL,
  rubric_id TEXT REFERENCES evaluation_rubrics(id) ON DELETE SET NULL,
  left_template_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  right_template_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'running',
  created_by TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  CONSTRAINT evaluation_experiments_name_check CHECK (btrim(name) <> ''),
  CONSTRAINT evaluation_experiments_status_check CHECK (status IN ('running', 'completed', 'failed')),
  CONSTRAINT evaluation_experiments_templates_distinct_check
    CHECK (left_template_session_id IS NULL OR right_template_session_id IS NULL OR left_template_session_id <> right_template_session_id)
);

CREATE INDEX IF NOT EXISTS idx_evaluation_experiments_workspace_updated
  ON evaluation_experiments(workspace_id, updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS evaluation_experiment_items (
  id TEXT PRIMARY KEY,
  experiment_id TEXT NOT NULL REFERENCES evaluation_experiments(id) ON DELETE CASCADE,
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  dataset_item_id TEXT REFERENCES evaluation_dataset_items(id) ON DELETE SET NULL,
  item_index INTEGER NOT NULL,
  prompt TEXT NOT NULL,
  expected_output TEXT NOT NULL DEFAULT '',
  tags_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  left_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  left_turn_id TEXT NOT NULL DEFAULT '',
  right_session_id TEXT REFERENCES sessions(id) ON DELETE SET NULL,
  right_turn_id TEXT NOT NULL DEFAULT '',
  evaluation_id TEXT REFERENCES run_evaluations(id) ON DELETE SET NULL,
  status TEXT NOT NULL DEFAULT 'queued',
  conclusion TEXT NOT NULL DEFAULT '',
  left_average DOUBLE PRECISION NOT NULL DEFAULT 0,
  right_average DOUBLE PRECISION NOT NULL DEFAULT 0,
  error_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT evaluation_experiment_items_prompt_check CHECK (btrim(prompt) <> ''),
  CONSTRAINT evaluation_experiment_items_tags_check CHECK (jsonb_typeof(tags_json) = 'array'),
  CONSTRAINT evaluation_experiment_items_status_check CHECK (status IN ('queued', 'running', 'completed', 'failed')),
  CONSTRAINT evaluation_experiment_items_conclusion_check
    CHECK (conclusion = '' OR conclusion IN ('left', 'right', 'tie', 'inconclusive')),
  CONSTRAINT evaluation_experiment_items_index_unique UNIQUE (experiment_id, item_index)
);

CREATE INDEX IF NOT EXISTS idx_evaluation_experiment_items_experiment
  ON evaluation_experiment_items(experiment_id, item_index);

CREATE INDEX IF NOT EXISTS idx_evaluation_experiment_items_status
  ON evaluation_experiment_items(experiment_id, status, item_index);

ALTER TABLE evaluation_datasets ENABLE ROW LEVEL SECURITY;
ALTER TABLE evaluation_datasets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS evaluation_datasets_workspace_isolation ON evaluation_datasets;
CREATE POLICY evaluation_datasets_workspace_isolation ON evaluation_datasets
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

ALTER TABLE evaluation_dataset_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE evaluation_dataset_items FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS evaluation_dataset_items_workspace_isolation ON evaluation_dataset_items;
CREATE POLICY evaluation_dataset_items_workspace_isolation ON evaluation_dataset_items
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

ALTER TABLE evaluation_experiments ENABLE ROW LEVEL SECURITY;
ALTER TABLE evaluation_experiments FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS evaluation_experiments_workspace_isolation ON evaluation_experiments;
CREATE POLICY evaluation_experiments_workspace_isolation ON evaluation_experiments
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

ALTER TABLE evaluation_experiment_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE evaluation_experiment_items FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS evaluation_experiment_items_workspace_isolation ON evaluation_experiment_items;
CREATE POLICY evaluation_experiment_items_workspace_isolation ON evaluation_experiment_items
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));
