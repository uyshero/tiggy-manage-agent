ALTER TABLE llm_usage_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE llm_usage_records FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS llm_usage_records_session_isolation
  ON llm_usage_records;

CREATE POLICY llm_usage_records_session_isolation
  ON llm_usage_records
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = llm_usage_records.session_id
        AND sessions.workspace_id = llm_usage_records.workspace_id
        AND sessions.agent_id = llm_usage_records.agent_id
        AND sessions.agent_config_version = llm_usage_records.agent_config_version
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = llm_usage_records.session_id
        AND sessions.workspace_id = llm_usage_records.workspace_id
        AND sessions.agent_id = llm_usage_records.agent_id
        AND sessions.agent_config_version = llm_usage_records.agent_config_version
    )
  );

ALTER TABLE trace_indexes ENABLE ROW LEVEL SECURITY;
ALTER TABLE trace_indexes FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS trace_indexes_session_isolation ON trace_indexes;

CREATE POLICY trace_indexes_session_isolation
  ON trace_indexes
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = trace_indexes.session_id
        AND sessions.workspace_id = trace_indexes.workspace_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM sessions
      WHERE sessions.id = trace_indexes.session_id
        AND sessions.workspace_id = trace_indexes.workspace_id
    )
  );

ALTER TABLE trace_span_indexes ENABLE ROW LEVEL SECURITY;
ALTER TABLE trace_span_indexes FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS trace_span_indexes_trace_isolation
  ON trace_span_indexes;

CREATE POLICY trace_span_indexes_trace_isolation
  ON trace_span_indexes
  FOR ALL
  USING (
    EXISTS (
      SELECT 1
      FROM trace_indexes
      WHERE trace_indexes.trace_id = trace_span_indexes.trace_id
        AND trace_indexes.workspace_id = trace_span_indexes.workspace_id
        AND trace_indexes.session_id = trace_span_indexes.session_id
        AND trace_indexes.turn_id = trace_span_indexes.turn_id
    )
  )
  WITH CHECK (
    EXISTS (
      SELECT 1
      FROM trace_indexes
      WHERE trace_indexes.trace_id = trace_span_indexes.trace_id
        AND trace_indexes.workspace_id = trace_span_indexes.workspace_id
        AND trace_indexes.session_id = trace_span_indexes.session_id
        AND trace_indexes.turn_id = trace_span_indexes.turn_id
    )
  );
