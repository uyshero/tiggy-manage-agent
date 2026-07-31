CREATE TABLE IF NOT EXISTS workspace_memberships (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  subject TEXT NOT NULL,
  display_name TEXT NOT NULL DEFAULT '',
  email TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, subject),
  CONSTRAINT workspace_memberships_subject_check CHECK (btrim(subject) <> ''),
  CONSTRAINT workspace_memberships_role_check CHECK (role IN ('viewer', 'member', 'operator', 'admin')),
  CONSTRAINT workspace_memberships_status_check CHECK (status IN ('invited', 'active', 'suspended'))
);

CREATE INDEX IF NOT EXISTS workspace_memberships_subject_idx
  ON workspace_memberships(subject, workspace_id);

ALTER TABLE workspace_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_memberships FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS workspace_memberships_current_workspace ON workspace_memberships;
CREATE POLICY workspace_memberships_current_workspace
  ON workspace_memberships
  FOR ALL
  USING (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''))
  WITH CHECK (workspace_id = NULLIF(current_setting('tma.workspace_id', true), ''));

CREATE TABLE IF NOT EXISTS platform_role_assignments (
  subject TEXT PRIMARY KEY,
  display_name TEXT NOT NULL DEFAULT '',
  email TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'platform_admin',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT platform_role_assignments_subject_check CHECK (btrim(subject) <> ''),
  CONSTRAINT platform_role_assignments_role_check CHECK (role = 'platform_admin')
);

ALTER TABLE platform_role_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_role_assignments FORCE ROW LEVEL SECURITY;

CREATE SEQUENCE IF NOT EXISTS tma_workspace_id_seq;

CREATE OR REPLACE FUNCTION tma_is_platform_admin(requested_subject TEXT)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
  SELECT btrim(COALESCE(requested_subject, '')) IN ('legacy-control', 'local-development')
    OR EXISTS (
      SELECT 1
      FROM public.platform_role_assignments
      WHERE subject = btrim(requested_subject)
        AND role = 'platform_admin'
    );
$$;

CREATE OR REPLACE FUNCTION tma_list_platform_admins(caller_subject TEXT)
RETURNS TABLE(subject TEXT, display_name TEXT, email TEXT, role TEXT, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  IF NOT public.tma_is_platform_admin(caller_subject) THEN
    RAISE EXCEPTION 'platform administrator required' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
    SELECT assignment.subject, assignment.display_name, assignment.email, assignment.role,
      assignment.created_at, assignment.updated_at
    FROM public.platform_role_assignments assignment
    ORDER BY assignment.created_at, assignment.subject;
END;
$$;

CREATE OR REPLACE FUNCTION tma_upsert_platform_admin(caller_subject TEXT, requested_subject TEXT, requested_display_name TEXT, requested_email TEXT)
RETURNS TABLE(subject TEXT, display_name TEXT, email TEXT, role TEXT, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  IF NOT public.tma_is_platform_admin(caller_subject) THEN
    RAISE EXCEPTION 'platform administrator required' USING ERRCODE = '42501';
  END IF;
  IF btrim(COALESCE(requested_subject, '')) = '' THEN
    RAISE EXCEPTION 'platform administrator subject is required' USING ERRCODE = '22023';
  END IF;
  RETURN QUERY
    INSERT INTO public.platform_role_assignments (subject, display_name, email, role)
    VALUES (btrim(requested_subject), btrim(COALESCE(requested_display_name, '')), btrim(COALESCE(requested_email, '')), 'platform_admin')
    ON CONFLICT ON CONSTRAINT platform_role_assignments_pkey DO UPDATE SET
      display_name = EXCLUDED.display_name,
      email = EXCLUDED.email,
      updated_at = now()
    RETURNING platform_role_assignments.subject, platform_role_assignments.display_name,
      platform_role_assignments.email, platform_role_assignments.role,
      platform_role_assignments.created_at, platform_role_assignments.updated_at;
END;
$$;

CREATE OR REPLACE FUNCTION tma_delete_platform_admin(caller_subject TEXT, requested_subject TEXT)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  IF NOT public.tma_is_platform_admin(caller_subject) THEN
    RAISE EXCEPTION 'platform administrator required' USING ERRCODE = '42501';
  END IF;
  IF btrim(COALESCE(caller_subject, '')) = btrim(COALESCE(requested_subject, '')) THEN
    RAISE EXCEPTION 'platform administrator cannot remove itself' USING ERRCODE = '22023';
  END IF;
  DELETE FROM public.platform_role_assignments WHERE subject = btrim(requested_subject);
END;
$$;

CREATE OR REPLACE FUNCTION tma_list_platform_workspaces(caller_subject TEXT)
RETURNS TABLE(id TEXT, name TEXT, created_at TIMESTAMPTZ, member_count BIGINT)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
BEGIN
  IF NOT public.tma_is_platform_admin(caller_subject) THEN
    RAISE EXCEPTION 'platform administrator required' USING ERRCODE = '42501';
  END IF;
  RETURN QUERY
    SELECT workspace.id, workspace.name, workspace.created_at, COUNT(membership.subject)
    FROM public.workspaces workspace
    LEFT JOIN public.workspace_memberships membership ON membership.workspace_id = workspace.id
    GROUP BY workspace.id, workspace.name, workspace.created_at
    ORDER BY workspace.created_at, workspace.id;
END;
$$;

CREATE OR REPLACE FUNCTION tma_create_platform_workspace(caller_subject TEXT, requested_name TEXT)
RETURNS TABLE(id TEXT, name TEXT, created_at TIMESTAMPTZ, member_count BIGINT)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
SET row_security = off
AS $$
DECLARE
  created_id TEXT;
BEGIN
  IF NOT public.tma_is_platform_admin(caller_subject) THEN
    RAISE EXCEPTION 'platform administrator required' USING ERRCODE = '42501';
  END IF;
  IF length(btrim(COALESCE(requested_name, ''))) NOT BETWEEN 1 AND 200 THEN
    RAISE EXCEPTION 'workspace name is required and must not exceed 200 characters' USING ERRCODE = '22023';
  END IF;
  created_id := 'wksp_' || lpad(nextval('public.tma_workspace_id_seq')::TEXT, 6, '0');
  INSERT INTO public.workspaces (id, org_id, name)
  VALUES (created_id, 'org_default', btrim(requested_name));
  RETURN QUERY
    SELECT workspace.id, workspace.name, workspace.created_at, 0::BIGINT
    FROM public.workspaces workspace
    WHERE workspace.id = created_id;
END;
$$;
