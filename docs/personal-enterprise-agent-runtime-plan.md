# Personal and Enterprise Agent Runtime Plan

Status: active implementation plan

Date: 2026-07-16

## 1. Decisions

This plan records the product decisions that are treated as invariants for the
implementation.

1. A general-purpose Agent is personal. Every user receives a distinct Agent
   identity, Memory, Session set, credentials, and configuration history.
2. An Agent template is a factory input only. Publishing a new template version
   never updates an existing Agent. A user may create another Agent from the new
   template.
3. An enterprise Agent shares immutable configuration, not user state. Every
   invocation still uses a user-scoped Session, workspace, Memory projection,
   and credential set.
4. A Skill package belongs to a registry scope. An exact Skill version is bound
   to an immutable Agent config version. A Session follows the Agent's current
   config by default at the boundary of a new Turn.
5. Skill drafts are mutable. Published Skill versions are immutable. Customizing
   an organization Skill without publisher permission creates a personal fork.
6. PostgreSQL is authoritative for identity, metadata, permissions, versions,
   leases, and Memory records. Object storage is authoritative for immutable
   packages, artifacts, and workspace snapshots. Execution-node disks contain
   rebuildable caches and active workspace copies.
7. The model and Skill scripts never access object-storage URLs directly.
   Packages are verified and materialized as ordinary read-only files before use.
8. The enterprise catalog exposes Skills, MCP definitions, Agent templates,
   enterprise Agents, and Plugins through one governance and discovery surface.

## 2. Runtime Terms

The implementation must not use the single word `worker` for all execution
roles.

- **Turn Runner**: the server-side background runner that leases and executes a
  Turn and its LLM loop.
- **Local Worker**: the outbound `tma-worker` process that provides
  `local_system` and plugin capabilities on its host.
- **Sandbox Execution Node**: the node that hosts `cloud_sandbox` containers and
  their active workspaces. It is currently the TMA server host and may become a
  separate service later.
- **Execution Node**: either of the latter two when a rule applies to both.

## 3. Target Domain Model

### 3.1 Identity and ownership

Introduce stable internal principals. External OIDC subjects are identity links,
not business-table primary keys.

```text
users                  internal user record
principal_identities   issuer + subject -> principal
principal_groups       organization/workspace groups
principal_group_members
resource_grants        resource + principal + permission
```

Agent scopes are `personal`, `team`, `workspace`, `organization`, and `builtin`.
An Agent records `scope_type`, `scope_id`, and `owner_principal_id`. Each config
version records its real author and publisher. Invocation permission is separate
from configuration, Skill, MCP, publication, ACL, and archive permissions.

### 3.2 Templates and Agents

```text
agent_templates
agent_template_versions       immutable factory inputs
agents                        independent runtime identities
agent_config_versions          immutable effective configurations
```

`created_from_template_version_id` is provenance only. Runtime resolution never
reads a template after Agent creation.

Personal general Agents are created lazily and idempotently on first use with a
uniqueness constraint on organization, user, and Agent kind. Creating from a
newer template creates a new Agent; it does not upgrade an existing one.

### 3.3 Session config following

Session runtime settings support:

```text
agent_config_update_policy = follow_latest | pinned
```

`follow_latest` is the default. Before a new Turn is created, the server locks
the Session, validates the Agent's latest published config, moves the Session
forward when necessary, records an upgrade event, and stores the selected config
version on the Turn. A continuation of an existing waiting Turn never changes
config mid-Turn.

### 3.4 Skills

Skill scopes are `user`, `team`, `workspace`, `organization`, `builtin`, and
`plugin`.

```text
skills
skill_drafts                    mutable working state
skill_versions                  immutable releases
skill_version_package_files     immutable file index
agent_config_skill_bindings     exact release references and unique aliases
```

Within one Agent config version:

- a `skill_id` may be bound at only one version;
- a binding alias is unique;
- a personal fork replaces its upstream binding instead of loading both by
  default.

Different Agents and Sessions may use same-named Skills or different versions
concurrently on the same execution node. That is a cache and mount requirement,
not permission to create ambiguous bindings inside one Agent config.

## 4. Storage and Filesystem Contract

### 4.1 Authoritative storage

```text
PostgreSQL
  identity, ACL, Agent and Skill metadata, versions, Memory, leases

Object storage
  Skill package files and ZIP archives, artifacts, workspace snapshots

Execution-node local disk
  content-addressed Skill cache, active Session workspace, temporary files
```

NFS is not a target backend for this plan.

### 4.2 Container paths

```text
/workspace                  Session-scoped writable workspace
/tma/agent                  immutable Agent runtime projection, read-only
/tma/skills/<skill-id>/<version>
                            immutable Skill package, read-only
/tma/context                user and Memory projection, read-only
/run/secrets                tmpfs or brokered secret projection
/tmp                        ephemeral Turn files
```

Published Skill files must never live below an otherwise writable `/workspace`
mount. Scripts write output to `/workspace` or `/tmp`, not next to themselves.

### 4.3 Skill object keys and cache keys

New package object keys use stable identities rather than a display identifier:

```text
skills/<workspace>/<scope-type>/<scope-id>/<skill-id>/versions/<version>/...
```

The execution-node cache is tenant-partitioned and content-addressed:

```text
<cache-root>/<workspace>/sha256/<package-checksum>/
```

Materialization is lock-protected, downloads into a random staging directory,
validates the archive and every indexed file, changes the tree to read-only,
atomically renames it into the cache, and writes a ready marker last. A different
checksum for the same `skill_version_id` is an integrity failure, never an
overwrite.

Active mounts hold cache leases so garbage collection cannot delete an in-use
package. Cache hits do not read package bytes from object storage.

### 4.4 Workspace persistence

An active Session has one writer lease and a local workspace copy. At defined
checkpoints the execution node creates a deterministic workspace manifest and
uploads changed blobs plus a snapshot record. A new node restores the latest
committed snapshot before running the next Turn. Artifact export remains an
explicit operation independent of snapshot retention.

## 5. Enterprise Catalog

The catalog resource types are:

```text
skill | mcp | agent_template | enterprise_agent | plugin
```

Entries have organization/workspace visibility, immutable release references,
owner principals, risk and classification metadata, and lifecycle states
`draft`, `review`, `published`, `deprecated`, and `withdrawn`.

Resource actions remain distinct:

- Skill: inspect, install into an allowed registry scope, then bind to an Agent.
- MCP: create a user or enterprise connection; secrets are never catalog data.
- Agent template: create a new independent Agent.
- Enterprise Agent: start a user-scoped Session.
- Plugin: install governed tool and Skill capabilities.

## 6. Delivery Order

### Milestone A: contracts and compatibility

- Add this plan and architecture decision records.
- Add canonical validation for config update policy and Skill bindings.
- Make new Turn creation follow the latest Agent config by default.
- Preserve the explicit upgrade endpoint and add `pinned` behavior.
- Keep existing API payloads working through default values and migrations.

Exit gate: unit and PostgreSQL tests prove next-Turn auto-follow, pinned Sessions,
continuation stability, and per-Turn config attribution.

### Milestone B: identity, ownership, and templates

- Add principal identity, group, grant, Agent ownership, and template tables.
- Extend Agent API/SDK/UI with scope and provenance.
- Lazily provision a personal general Agent per user.
- Enforce personal, shared invocation, editor, publisher, and ACL permissions.

Exit gate: two users receive different personal Agents; a shared enterprise Agent
can be invoked by both without sharing Session state; template publication never
changes an existing Agent.

### Milestone C: Skill scopes, drafts, and forks

- Replace `owner_type` with compatible scope semantics.
- Add personal Skill creation, mutable drafts, immutable publication, fork
  lineage, and promotion review.
- Persist exact normalized Agent Skill bindings and reject ambiguity.
- Preserve legacy workspace Skill identifiers during migration.

Exit gate: a user can publish and bind a private Skill, fork an organization
Skill, replace the upstream binding, publish a new personal version, and retain
replayable old Agent configs.

### Milestone D: conflict-safe materialization

- Introduce the execution-node package cache manager.
- Move package lookup to manifest and archive references before byte loading.
- Replace identifier-only directory maps with binding and Skill IDs.
- Mount packages read-only outside `/workspace`.
- Add cache leases, metrics, and GC.

Exit gate: concurrent cold materialization produces one valid cache entry; warm
materialization performs no object GET; corruption fails closed; different
Agent/Session versions coexist; scripts resolve all package files through the
runtime manifest.

### Milestone E: enterprise catalog

- Generalize the current internal Skill marketplace into catalog entries and
  immutable resource releases.
- Add type-specific browse, inspect, use, review, publish, deprecate, withdraw,
  and access-request flows.
- Add resource-level grants and real publisher audit identities.

Exit gate: organization members can discover all visible resource types and only
perform actions granted for that resource and version.

### Milestone F: workspace snapshots

- Add workspace snapshot metadata, manifests, object refs, restore, retention,
  and single-writer fencing.
- Integrate checkpointing with successful Turn completion and explicit saves.
- Restore on execution-node movement and prove interrupted uploads do not replace
  the last committed snapshot.

Exit gate: a Session completes a Turn on one node and continues with identical
workspace bytes on another node using only PostgreSQL and object storage.

## 7. Required Verification

Completion requires all of the following evidence, not only green narrow unit
tests:

1. Migration tests from the current schema and representative legacy data.
2. Store parity between PostgreSQL and in-memory HTTP test stores.
3. API v2 schema, Go SDK, TypeScript SDK, and Workbench integration tests.
4. Authorization tests for user, group, workspace operator, organization admin,
   and service principal publication.
5. Package-path traversal, symlink, checksum, concurrent writer, read-only mount,
   cache lease, and GC tests.
6. Real localfs and S3-compatible package verification.
7. Multi-user personal Agent and enterprise Agent end-to-end tests.
8. Cross-node workspace snapshot and restore verification.
9. Audit evidence identifying the actor, selected Agent config, Skill versions,
   policy revision, runtime paths, and committed workspace snapshot.

## 8. Explicit Non-goals

- No template-to-Agent live inheritance or automatic template upgrade.
- No mutable published Skill version.
- No shared writable Agent home directory.
- No direct object-storage filesystem exposed to the model.
- No NFS backend in this delivery plan.
- No custom distributed POSIX filesystem.
