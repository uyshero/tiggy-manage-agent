DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'tma_runtime') THEN
    RAISE EXCEPTION 'database role tma_runtime must be created before applying runtime grants';
  END IF;
END
$$;

GRANT CONNECT ON DATABASE tma TO tma_runtime;
GRANT USAGE ON SCHEMA public TO tma_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO tma_runtime;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO tma_runtime;

ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO tma_runtime;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO tma_runtime;

