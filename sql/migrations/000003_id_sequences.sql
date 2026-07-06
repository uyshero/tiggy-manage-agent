CREATE SEQUENCE IF NOT EXISTS tma_agent_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_environment_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_session_id_seq;
CREATE SEQUENCE IF NOT EXISTS tma_event_id_seq;

SELECT setval(
  'tma_agent_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM agents), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM agents)
);

SELECT setval(
  'tma_environment_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM environments), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM environments)
);

SELECT setval(
  'tma_session_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM sessions), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM sessions)
);

SELECT setval(
  'tma_event_id_seq',
  GREATEST((SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) FROM session_events), 1),
  (SELECT COALESCE(MAX(substring(id FROM '[0-9]+$')::BIGINT), 0) > 0 FROM session_events)
);
