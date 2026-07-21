package managedagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *PostgresStore) ValidateDatabaseTenantIsolation(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("postgres store is unavailable")
	}
	var role string
	var superuser bool
	var bypassRLS bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT current_user, rolsuper, rolbypassrls
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(&role, &superuser, &bypassRLS); err != nil {
		return fmt.Errorf("inspect database runtime role: %w", err)
	}
	if superuser {
		return fmt.Errorf("database role %q is a superuser and bypasses tenant RLS", role)
	}
	if bypassRLS {
		return fmt.Errorf("database role %q has BYPASSRLS", role)
	}
	var canListWorkspaces, canListOrganizations bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			has_table_privilege(current_user, 'public.workspaces', 'SELECT'),
			has_table_privilege(current_user, 'public.organizations', 'SELECT')
	`).Scan(&canListWorkspaces, &canListOrganizations); err != nil {
		return fmt.Errorf("inspect tenant directory privileges: %w", err)
	}
	if !canListWorkspaces {
		return fmt.Errorf("database role %q requires SELECT on workspaces for tenant-scoped background jobs", role)
	}
	if !canListOrganizations {
		return fmt.Errorf("database role %q requires SELECT on organizations for organization-scoped policies", role)
	}
	var catalogHelperOwner, catalogHelperLanguage string
	var catalogHelperSecurityDefiner, catalogHelperFixedSearchPath, catalogHelperDisablesRLS, catalogHelperOwnerBypassesRLS, catalogHelperExecutable bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			owners.rolname,
			languages.lanname,
			functions.prosecdef,
			COALESCE('search_path=pg_catalog, public' = ANY(functions.proconfig), false),
			COALESCE('row_security=off' = ANY(functions.proconfig), false),
			owners.rolsuper OR owners.rolbypassrls,
			has_function_privilege(current_user, functions.oid, 'EXECUTE')
		FROM pg_proc functions
		JOIN pg_roles owners ON owners.oid = functions.proowner
		JOIN pg_language languages ON languages.oid = functions.prolang
		WHERE functions.oid = to_regprocedure('public.tma_skill_catalog_version_visible(text,integer)')
	`).Scan(
		&catalogHelperOwner,
		&catalogHelperLanguage,
		&catalogHelperSecurityDefiner,
		&catalogHelperFixedSearchPath,
		&catalogHelperDisablesRLS,
		&catalogHelperOwnerBypassesRLS,
		&catalogHelperExecutable,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("catalog RLS helper is missing; apply migration 000063")
		}
		return fmt.Errorf("inspect catalog RLS helper: %w", err)
	}
	if catalogHelperOwner == role || catalogHelperLanguage != "plpgsql" || !catalogHelperSecurityDefiner ||
		!catalogHelperFixedSearchPath || !catalogHelperDisablesRLS || !catalogHelperOwnerBypassesRLS || !catalogHelperExecutable {
		return fmt.Errorf(
			"catalog RLS helper has unsafe configuration (owner=%q language=%q security_definer=%t fixed_search_path=%t row_security_off=%t owner_bypasses_rls=%t executable=%t); apply migration 000063 with a privileged migration role",
			catalogHelperOwner,
			catalogHelperLanguage,
			catalogHelperSecurityDefiner,
			catalogHelperFixedSearchPath,
			catalogHelperDisablesRLS,
			catalogHelperOwnerBypassesRLS,
			catalogHelperExecutable,
		)
	}

	directoryHelperRows, err := s.db.QueryContext(ctx, `
		WITH required(signature) AS (
			VALUES
				('public.tma_list_workspace_ids()'),
				('public.tma_workspace_exists(text)'),
				('public.tma_organization_exists(text)'),
				('public.tma_workspace_organization_id(text)'),
				('public.tma_workspaces_share_organization(text,text)')
		)
		SELECT
			required.signature,
			owners.rolname,
			languages.lanname,
			functions.prosecdef,
			COALESCE('search_path=pg_catalog, public' = ANY(functions.proconfig), false),
			COALESCE('row_security=off' = ANY(functions.proconfig), false),
			owners.rolsuper OR owners.rolbypassrls,
			has_function_privilege(current_user, functions.oid, 'EXECUTE')
		FROM required
		JOIN pg_proc functions ON functions.oid = to_regprocedure(required.signature)
		JOIN pg_roles owners ON owners.oid = functions.proowner
		JOIN pg_language languages ON languages.oid = functions.prolang
		ORDER BY required.signature
	`)
	if err != nil {
		return fmt.Errorf("inspect tenant directory RLS helpers: %w", err)
	}
	directoryHelperCount := 0
	for directoryHelperRows.Next() {
		var signature, owner, language string
		var securityDefiner, fixedSearchPath, disablesRLS, ownerBypassesRLS, executable bool
		if err := directoryHelperRows.Scan(
			&signature, &owner, &language, &securityDefiner, &fixedSearchPath, &disablesRLS, &ownerBypassesRLS, &executable,
		); err != nil {
			directoryHelperRows.Close()
			return fmt.Errorf("inspect tenant directory RLS helper: %w", err)
		}
		directoryHelperCount++
		if owner == role || language != "plpgsql" || !securityDefiner || !fixedSearchPath || !disablesRLS || !ownerBypassesRLS || !executable {
			directoryHelperRows.Close()
			return fmt.Errorf(
				"tenant directory RLS helper %s has unsafe configuration (owner=%q language=%q security_definer=%t fixed_search_path=%t row_security_off=%t owner_bypasses_rls=%t executable=%t); apply migration 000066 with a privileged migration role",
				signature, owner, language, securityDefiner, fixedSearchPath, disablesRLS, ownerBypassesRLS, executable,
			)
		}
	}
	if err := directoryHelperRows.Close(); err != nil {
		return fmt.Errorf("close tenant directory RLS helper inspection: %w", err)
	}
	if err := directoryHelperRows.Err(); err != nil {
		return fmt.Errorf("inspect tenant directory RLS helpers: %w", err)
	}
	if directoryHelperCount != 5 {
		return errors.New("tenant directory RLS helpers are missing; apply migration 000066")
	}

	directoryRows, err := s.db.QueryContext(ctx, `
		WITH required(table_name, policy_name) AS (
			VALUES
				('organizations', 'organizations_current_workspace_read'),
				('workspaces', 'workspaces_current_workspace_read')
		)
		SELECT
			required.table_name,
			tables.relrowsecurity,
			tables.relforcerowsecurity,
			tables.relowner = roles.oid,
			EXISTS (
				SELECT 1 FROM pg_policy policies
				WHERE policies.polrelid = tables.oid AND policies.polname = required.policy_name
			),
			has_table_privilege(current_user, tables.oid, 'SELECT')
		FROM required
		JOIN pg_namespace schemas ON schemas.nspname = 'public'
		JOIN pg_class tables ON tables.relnamespace = schemas.oid AND tables.relname = required.table_name
		JOIN pg_roles roles ON roles.rolname = current_user
		ORDER BY required.table_name
	`)
	if err != nil {
		return fmt.Errorf("inspect tenant directory RLS tables: %w", err)
	}
	directoryCount := 0
	for directoryRows.Next() {
		var table string
		var rowSecurity, forceRowSecurity, tableOwner, policyPresent, selectPrivilege bool
		if err := directoryRows.Scan(&table, &rowSecurity, &forceRowSecurity, &tableOwner, &policyPresent, &selectPrivilege); err != nil {
			directoryRows.Close()
			return fmt.Errorf("inspect tenant directory RLS table: %w", err)
		}
		directoryCount++
		switch {
		case tableOwner:
			directoryRows.Close()
			return fmt.Errorf("database role %q owns %s and must not be used by the runtime", role, table)
		case !rowSecurity || !forceRowSecurity || !policyPresent:
			directoryRows.Close()
			return fmt.Errorf("%s FORCE ROW LEVEL SECURITY policy is not installed; apply migration 000066", table)
		case !selectPrivilege:
			directoryRows.Close()
			return fmt.Errorf("database role %q requires SELECT on %s", role, table)
		}
	}
	if err := directoryRows.Close(); err != nil {
		return fmt.Errorf("close tenant directory RLS table inspection: %w", err)
	}
	if err := directoryRows.Err(); err != nil {
		return fmt.Errorf("inspect tenant directory RLS tables: %w", err)
	}
	if directoryCount != 2 {
		return errors.New("tenant directory RLS tables are missing; apply migration 000066")
	}

	var providerRead, modelRead, providerWrite, modelWrite, defaultVisionUnique, defaultEmbeddingUnique, defaultRerankerUnique bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			has_table_privilege(current_user, 'public.llm_providers', 'SELECT'),
			has_table_privilege(current_user, 'public.llm_models', 'SELECT'),
			has_table_privilege(current_user, 'public.llm_providers', 'INSERT')
				OR has_table_privilege(current_user, 'public.llm_providers', 'UPDATE')
				OR has_table_privilege(current_user, 'public.llm_providers', 'DELETE'),
			has_table_privilege(current_user, 'public.llm_models', 'INSERT')
				OR has_table_privilege(current_user, 'public.llm_models', 'UPDATE')
				OR has_table_privilege(current_user, 'public.llm_models', 'DELETE'),
			to_regclass('public.llm_models_single_default_vision_idx') IS NOT NULL,
			to_regclass('public.llm_models_single_default_embedding_idx') IS NOT NULL,
			to_regclass('public.llm_models_single_default_reranker_idx') IS NOT NULL
	`).Scan(&providerRead, &modelRead, &providerWrite, &modelWrite, &defaultVisionUnique, &defaultEmbeddingUnique, &defaultRerankerUnique); err != nil {
		return fmt.Errorf("inspect LLM control-plane privileges: %w", err)
	}
	if !providerRead || !modelRead {
		return fmt.Errorf("database role %q requires SELECT on llm_providers and llm_models", role)
	}
	if providerWrite || modelWrite {
		return fmt.Errorf("database role %q must not have direct INSERT, UPDATE, or DELETE on llm_providers or llm_models; revoke control-plane table writes and apply migration 000067", role)
	}
	if !defaultVisionUnique {
		return errors.New("LLM default vision uniqueness constraint is missing; apply migration 000041")
	}
	if !defaultEmbeddingUnique || !defaultRerankerUnique {
		return errors.New("LLM embedding/reranker default uniqueness constraints are missing; apply migration 000072")
	}
	var providerRevisionColumns, providerRevisionConstraint bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(
				SELECT count(*) = 2
				FROM pg_attribute
				WHERE attrelid = 'public.llm_providers'::regclass
					AND attname IN ('revision', 'updated_at')
					AND attnotnull AND NOT attisdropped
			),
			EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'public.llm_providers'::regclass
					AND conname = 'llm_providers_revision_positive'
					AND contype = 'c' AND convalidated
			)
	`).Scan(&providerRevisionColumns, &providerRevisionConstraint); err != nil {
		return fmt.Errorf("inspect LLM provider revision schema: %w", err)
	}
	if !providerRevisionColumns || !providerRevisionConstraint {
		return fmt.Errorf("LLM provider revision schema is incomplete (columns=%t constraint=%t); apply migration 000069", providerRevisionColumns, providerRevisionConstraint)
	}
	var modelRevisionColumn, modelRevisionConstraint bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM pg_attribute
				WHERE attrelid = 'public.llm_models'::regclass
					AND attname = 'revision' AND atttypid = 'bigint'::regtype
					AND attnotnull AND NOT attisdropped
			),
			EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'public.llm_models'::regclass
					AND conname = 'llm_models_revision_positive'
					AND contype = 'c' AND convalidated
			)
	`).Scan(&modelRevisionColumn, &modelRevisionConstraint); err != nil {
		return fmt.Errorf("inspect LLM model revision schema: %w", err)
	}
	if !modelRevisionColumn || !modelRevisionConstraint {
		return fmt.Errorf("LLM model revision schema is incomplete (column=%t constraint=%t); apply migration 000070", modelRevisionColumn, modelRevisionConstraint)
	}

	controlHelperRows, err := s.db.QueryContext(ctx, `
		WITH required(signature) AS (
			VALUES
				('public.tma_control_upsert_llm_provider(text,text,text,text,boolean)'),
				('public.tma_control_create_llm_provider(text,text,text,text,boolean)'),
				('public.tma_control_update_llm_provider(text,text,text,text,boolean,bigint)'),
				('public.tma_control_set_llm_provider_enabled(text,boolean)'),
				('public.tma_control_set_llm_provider_enabled(text,boolean,bigint)'),
				('public.tma_control_delete_llm_provider(text)'),
				('public.tma_control_delete_llm_provider(text,bigint)'),
				('public.tma_control_upsert_llm_model(text,text,integer,text,boolean)'),
				('public.tma_control_create_llm_model(text,text,integer,text,boolean)'),
				('public.tma_control_update_llm_model(text,text,integer,text,boolean,bigint)'),
				('public.tma_control_upsert_llm_model(text,text,integer,text,jsonb,boolean,boolean,boolean)'),
				('public.tma_control_create_llm_model(text,text,integer,text,jsonb,boolean,boolean,boolean)'),
				('public.tma_control_update_llm_model(text,text,integer,text,jsonb,boolean,boolean,boolean,bigint)'),
				('public.tma_control_delete_llm_model(text,text)'),
				('public.tma_control_delete_llm_model(text,text,bigint)')
		)
		SELECT
			required.signature,
			owners.rolname,
			languages.lanname,
			functions.prosecdef,
			COALESCE('search_path=pg_catalog, public' = ANY(functions.proconfig), false),
			COALESCE('row_security=off' = ANY(functions.proconfig), false),
			owners.rolsuper OR owners.rolbypassrls,
			has_function_privilege(current_user, functions.oid, 'EXECUTE')
		FROM required
		JOIN pg_proc functions ON functions.oid = to_regprocedure(required.signature)
		JOIN pg_roles owners ON owners.oid = functions.proowner
		JOIN pg_language languages ON languages.oid = functions.prolang
		ORDER BY required.signature
	`)
	if err != nil {
		return fmt.Errorf("inspect LLM control-plane helpers: %w", err)
	}
	controlHelperCount := 0
	for controlHelperRows.Next() {
		var signature, owner, language string
		var securityDefiner, fixedSearchPath, disablesRLS, ownerBypassesRLS, executable bool
		if err := controlHelperRows.Scan(
			&signature, &owner, &language, &securityDefiner, &fixedSearchPath, &disablesRLS, &ownerBypassesRLS, &executable,
		); err != nil {
			controlHelperRows.Close()
			return fmt.Errorf("inspect LLM control-plane helper: %w", err)
		}
		controlHelperCount++
		if owner == role || language != "plpgsql" || !securityDefiner || !fixedSearchPath || !disablesRLS || !ownerBypassesRLS || !executable {
			controlHelperRows.Close()
			return fmt.Errorf(
				"LLM control-plane helper %s has unsafe configuration (owner=%q language=%q security_definer=%t fixed_search_path=%t row_security_off=%t owner_bypasses_rls=%t executable=%t); apply migration 000067 with a privileged migration role",
				signature, owner, language, securityDefiner, fixedSearchPath, disablesRLS, ownerBypassesRLS, executable,
			)
		}
	}
	if err := controlHelperRows.Close(); err != nil {
		return fmt.Errorf("close LLM control-plane helper inspection: %w", err)
	}
	if err := controlHelperRows.Err(); err != nil {
		return fmt.Errorf("inspect LLM control-plane helpers: %w", err)
	}
	if controlHelperCount != 15 {
		return errors.New("LLM control-plane helpers are missing; apply migrations through 000072")
	}

	var effectiveColumns, agentModelForeignKey, sessionModelForeignKey, effectiveTrigger bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			(
				SELECT count(*) = 2
				FROM pg_attribute
				WHERE attrelid = 'public.sessions'::regclass
					AND attname IN ('effective_llm_provider', 'effective_llm_model')
					AND atttypid = 'text'::regtype
					AND attnotnull
					AND NOT attisdropped
			),
			EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'public.agent_config_versions'::regclass
					AND conname = 'agent_config_versions_llm_model_fkey'
					AND contype = 'f' AND convalidated
					AND pg_get_constraintdef(oid) = 'FOREIGN KEY (llm_provider, llm_model) REFERENCES llm_models(provider_id, model)'
			),
			EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conrelid = 'public.sessions'::regclass
					AND conname = 'sessions_effective_llm_model_fkey'
					AND contype = 'f' AND convalidated
					AND pg_get_constraintdef(oid) = 'FOREIGN KEY (effective_llm_provider, effective_llm_model) REFERENCES llm_models(provider_id, model)'
			),
			EXISTS (
				SELECT 1 FROM pg_trigger
				WHERE tgrelid = 'public.sessions'::regclass
					AND tgname = 'sessions_set_effective_llm'
					AND tgfoid = to_regprocedure('public.tma_set_session_effective_llm()')
					AND NOT tgisinternal AND tgenabled <> 'D'
			)
	`).Scan(&effectiveColumns, &agentModelForeignKey, &sessionModelForeignKey, &effectiveTrigger); err != nil {
		return fmt.Errorf("inspect LLM reference integrity constraints: %w", err)
	}
	if !effectiveColumns || !agentModelForeignKey || !sessionModelForeignKey || !effectiveTrigger {
		return fmt.Errorf(
			"LLM reference integrity is incomplete (effective_columns=%t agent_model_fk=%t session_model_fk=%t session_trigger=%t); apply migration 000068",
			effectiveColumns, agentModelForeignKey, sessionModelForeignKey, effectiveTrigger,
		)
	}

	var referenceHelperOwner, referenceHelperLanguage string
	var referenceHelperSecurityDefiner, referenceHelperFixedSearchPath, referenceHelperDisablesRLS, referenceHelperOwnerBypassesRLS, referenceHelperExecutable bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			owners.rolname,
			languages.lanname,
			functions.prosecdef,
			COALESCE('search_path=pg_catalog, public' = ANY(functions.proconfig), false),
			COALESCE('row_security=off' = ANY(functions.proconfig), false),
			owners.rolsuper OR owners.rolbypassrls,
			has_function_privilege(current_user, functions.oid, 'EXECUTE')
		FROM pg_proc functions
		JOIN pg_roles owners ON owners.oid = functions.proowner
		JOIN pg_language languages ON languages.oid = functions.prolang
		WHERE functions.oid = to_regprocedure('public.tma_set_session_effective_llm()')
	`).Scan(
		&referenceHelperOwner, &referenceHelperLanguage, &referenceHelperSecurityDefiner,
		&referenceHelperFixedSearchPath, &referenceHelperDisablesRLS,
		&referenceHelperOwnerBypassesRLS, &referenceHelperExecutable,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("LLM session reference trigger helper is missing; apply migration 000068")
		}
		return fmt.Errorf("inspect LLM session reference trigger helper: %w", err)
	}
	if referenceHelperOwner == role || referenceHelperLanguage != "plpgsql" || !referenceHelperSecurityDefiner ||
		!referenceHelperFixedSearchPath || !referenceHelperDisablesRLS || !referenceHelperOwnerBypassesRLS || !referenceHelperExecutable {
		return fmt.Errorf(
			"LLM session reference trigger helper has unsafe configuration (owner=%q language=%q security_definer=%t fixed_search_path=%t row_security_off=%t owner_bypasses_rls=%t executable=%t); apply migration 000068 with a privileged migration role",
			referenceHelperOwner, referenceHelperLanguage, referenceHelperSecurityDefiner,
			referenceHelperFixedSearchPath, referenceHelperDisablesRLS,
			referenceHelperOwnerBypassesRLS, referenceHelperExecutable,
		)
	}

	rows, err := s.db.QueryContext(ctx, `
		WITH required(table_name, policy_name) AS (
			VALUES
				('agent_deliberation_contributions', 'agent_deliberation_contributions_parent_isolation'),
				('agent_deliberation_participants', 'agent_deliberation_participants_parent_isolation'),
				('agent_deliberation_rounds', 'agent_deliberation_rounds_parent_isolation'),
				('agent_deliberations', 'agent_deliberations_session_isolation'),
				('agent_config_versions', 'agent_config_versions_workspace_isolation'),
				('agent_loop_states', 'agent_loop_states_session_isolation'),
				('agent_schedule_runs', 'agent_schedule_runs_workspace_isolation'),
				('agent_schedules', 'agent_schedules_workspace_isolation'),
				('agents', 'agents_workspace_isolation'),
				('environments', 'environments_workspace_isolation'),
				('llm_usage_records', 'llm_usage_records_session_isolation'),
				('managed_environment_variables', 'managed_environment_variables_workspace_isolation'),
				('mcp_registry_server_versions', 'mcp_registry_server_versions_workspace_isolation'),
				('mcp_registry_servers', 'mcp_registry_servers_workspace_isolation'),
				('object_refs', 'object_refs_workspace_isolation'),
				('observability_exporter_runs', 'observability_exporter_runs_session_isolation'),
				('operator_audit_log', 'operator_audit_log_workspace_isolation'),
				('security_audit_outbox', 'security_audit_outbox_workspace_isolation'),
				('session_artifacts', 'session_artifacts_workspace_isolation'),
				('session_events', 'session_events_session_isolation'),
				('session_interventions', 'session_interventions_session_isolation'),
				('session_task_items', 'session_task_items_plan_isolation'),
				('session_task_plans', 'session_task_plans_session_isolation'),
				('session_summaries', 'session_summaries_session_isolation'),
				('session_turn_skill_usages', 'session_turn_skill_usages_session_isolation'),
				('session_turns', 'session_turns_session_isolation'),
				('sessions', 'sessions_workspace_owner_isolation'),
				('skill_asset_gc_items', 'skill_asset_gc_items_run_isolation'),
				('skill_asset_gc_runs', 'skill_asset_gc_runs_workspace_isolation'),
				('skill_asset_gc_tombstones', 'skill_asset_gc_tombstones_run_isolation'),
				('skill_asset_retention_policies', 'skill_asset_retention_policies_scope_isolation'),
				('skill_asset_retention_policy_versions', 'skill_asset_retention_policy_versions_policy_isolation'),
					('skill_marketplace_entries', 'skill_marketplace_entries_organization_catalog_read'),
					('skill_marketplace_entries', 'skill_marketplace_entries_workspace_isolation'),
				('skill_marketplace_policies', 'skill_marketplace_policies_scope_isolation'),
				('skill_marketplace_policy_versions', 'skill_marketplace_policy_versions_policy_isolation'),
				('skill_version_package_files', 'skill_version_package_files_version_isolation'),
					('skill_versions', 'skill_versions_published_catalog_read'),
					('skill_versions', 'skill_versions_skill_isolation'),
					('skills', 'skills_published_catalog_read'),
					('skills', 'skills_workspace_isolation'),
				('subagent_start_requests', 'subagent_start_requests_session_isolation'),
				('subagent_task_group_items', 'subagent_task_group_items_group_isolation'),
				('subagent_task_groups', 'subagent_task_groups_session_isolation'),
				('trace_indexes', 'trace_indexes_session_isolation'),
				('trace_span_indexes', 'trace_span_indexes_trace_isolation'),
				('worker_work', 'worker_work_workspace_isolation'),
				('workers', 'workers_workspace_isolation')
		)
		SELECT
			required.table_name,
			tables.relrowsecurity,
			tables.relforcerowsecurity,
			tables.relowner = roles.oid,
			EXISTS (
				SELECT 1 FROM pg_policy policies
				WHERE policies.polrelid = tables.oid AND policies.polname = required.policy_name
			),
			has_table_privilege(current_user, tables.oid, 'SELECT')
				AND has_table_privilege(current_user, tables.oid, 'INSERT')
				AND has_table_privilege(current_user, tables.oid, 'UPDATE')
				AND has_table_privilege(current_user, tables.oid, 'DELETE')
		FROM required
		JOIN pg_namespace schemas ON schemas.nspname = 'public'
		JOIN pg_class tables ON tables.relnamespace = schemas.oid AND tables.relname = required.table_name
		JOIN pg_roles roles ON roles.rolname = current_user
		ORDER BY required.table_name
	`)
	if err != nil {
		return fmt.Errorf("inspect tenant RLS tables: %w", err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var table string
		var rowSecurity bool
		var forceRowSecurity bool
		var tableOwner bool
		var policyPresent bool
		var tablePrivileges bool
		if err := rows.Scan(&table, &rowSecurity, &forceRowSecurity, &tableOwner, &policyPresent, &tablePrivileges); err != nil {
			return fmt.Errorf("inspect tenant RLS table: %w", err)
		}
		checked++
		switch {
		case tableOwner:
			return fmt.Errorf("database role %q owns %s and must not be used by the runtime", role, table)
		case !rowSecurity || !forceRowSecurity || !policyPresent:
			return fmt.Errorf("%s FORCE ROW LEVEL SECURITY policy is not installed; apply migrations 000045 through 000065", table)
		case !tablePrivileges:
			return fmt.Errorf("database role %q requires SELECT, INSERT, UPDATE, and DELETE on %s", role, table)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect tenant RLS tables: %w", err)
	}
	if checked != 48 {
		return errors.New("tenant RLS tables are missing; apply migrations through 000083")
	}

	sequenceRows, err := s.db.QueryContext(ctx, `
		WITH required(sequence_name) AS (
			VALUES
				('tma_agent_id_seq'),
				('tma_agent_deliberation_id_seq'),
				('tma_agent_schedule_id_seq'),
				('tma_agent_schedule_run_id_seq'),
				('tma_environment_id_seq'),
				('tma_event_id_seq'),
				('tma_llm_usage_id_seq'),
				('tma_mcp_registry_server_id_seq'),
				('tma_mcp_registry_version_id_seq'),
				('tma_object_ref_id_seq'),
				('tma_observability_exporter_run_id_seq'),
				('tma_operator_audit_id_seq'),
				('tma_session_artifact_id_seq'),
				('tma_session_id_seq'),
				('tma_skill_asset_gc_item_id_seq'),
				('tma_skill_asset_gc_run_id_seq'),
				('tma_skill_asset_gc_tombstone_id_seq'),
				('tma_skill_asset_retention_policy_id_seq'),
				('tma_skill_asset_retention_policy_version_id_seq'),
				('tma_skill_id_seq'),
				('tma_skill_marketplace_entry_id_seq'),
				('tma_skill_marketplace_policy_id_seq'),
				('tma_skill_marketplace_policy_version_id_seq'),
				('tma_skill_usage_id_seq'),
				('tma_skill_version_id_seq'),
				('tma_subagent_start_request_id_seq'),
				('tma_subagent_task_group_id_seq'),
				('tma_task_item_id_seq'),
				('tma_task_plan_id_seq'),
				('tma_worker_id_seq'),
				('tma_worker_work_id_seq')
		)
		SELECT required.sequence_name, has_sequence_privilege(current_user, sequences.oid, 'USAGE')
		FROM required
		JOIN pg_namespace schemas ON schemas.nspname = 'public'
		JOIN pg_class sequences
			ON sequences.relnamespace = schemas.oid
			AND sequences.relname = required.sequence_name
			AND sequences.relkind = 'S'
		ORDER BY required.sequence_name
	`)
	if err != nil {
		return fmt.Errorf("inspect tenant object sequences: %w", err)
	}
	defer sequenceRows.Close()
	checked = 0
	for sequenceRows.Next() {
		var sequence string
		var usage bool
		if err := sequenceRows.Scan(&sequence, &usage); err != nil {
			return fmt.Errorf("inspect tenant object sequence: %w", err)
		}
		checked++
		if !usage {
			return fmt.Errorf("database role %q requires USAGE on sequence %s", role, sequence)
		}
	}
	if err := sequenceRows.Err(); err != nil {
		return fmt.Errorf("inspect tenant object sequences: %w", err)
	}
	if checked != 31 {
		return errors.New("tenant resource sequences are missing; apply migrations through 000081")
	}
	return nil
}
