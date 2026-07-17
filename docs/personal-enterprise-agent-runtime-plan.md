# Personal and Workspace Agent Runtime Plan

Status: implementation plan, updated 2026-07-16

## 1. Decisions

TMA has one Agent entity and one runtime implementation. Personal and
Workspace-shared Agents differ only in ownership, visibility, and authorization.

```text
agents
  workspace_id
  owner_type       user | workspace
  owner_id
  visibility       private | workspace
  agent_kind       general | custom
```

Valid combinations are:

```text
Personal Agent
  owner_type = user
  owner_id = current user
  visibility = private

Workspace-shared Agent
  owner_type = workspace
  owner_id = workspace_id
  visibility = workspace
```

Both kinds have immutable `AgentConfigVersion` history, bind exact Skill and MCP
versions, create Sessions, and use the same Turn Runner, execution nodes, and
filesystem materialization.

There are no separate team, organization, builtin, or template-backed Agent
runtime types. Templates or system defaults may create a new independent Agent,
but they are not inherited and never upgrade an existing Agent.

## 2. Product Behavior

### 2.1 Personal general Agent

Every authenticated user lazily receives one personal general Agent per
Workspace. Creation is idempotent and enforced by:

```text
UNIQUE (workspace_id, owner_id, agent_kind)
WHERE owner_type = 'user' AND agent_kind = 'general'
```

All users start from the same default configuration, but receive different
Agent IDs and independent configuration histories. Later changes to system
defaults affect newly created Agents only.

Users may create additional personal custom Agents. Operator or Admin users may
also create Workspace-shared custom Agents. Authentication-disabled legacy mode
retains one Workspace general Agent for compatibility.

### 2.2 Authorization

| Operation | Personal Agent | Workspace-shared Agent |
|---|---|---|
| View and invoke | owner | Workspace member |
| Change config | owner | Operator or Admin |
| Bind Skills and MCP | owner | Operator or Admin |
| Session files | current Session user | current Session user |
| User Memory and credentials | owner | current Session user |

Workspace operators do not gain visibility into another user's private Agent.
A shared Agent never implies shared Session state:

```text
Shared Agent
  User A Session -> A files, Memory, credentials
  User B Session -> B files, Memory, credentials
```

### 2.3 Session configuration

Session runtime settings support:

```text
agent_config_update_policy = follow_latest | pinned
```

`follow_latest` is the default. Before creating a new Turn, the server locks the
Session and moves it to the Agent's latest config version. The Turn records the
selected `agent_id` and `agent_config_version`. Continuation of an already
waiting Turn never changes its config mid-Turn. `pinned` remains available for
replay, audit, or controlled rollout.

## 3. Storage Contract

Object storage is authoritative for immutable and portable bytes. PostgreSQL is
authoritative for metadata and transactional state. Local execution-node disk
is a cache and active working copy, never the only durable copy.

```text
PostgreSQL
  Agents and config versions
  Skill metadata and immutable versions
  Sessions, Turns, Memory, leases, snapshot manifests

Object storage
  immutable Skill packages
  artifacts
  Session workspace blobs and snapshots

Execution-node local disk
  content-addressed Skill cache
  active Session workspace
  Turn temporary files
```

NFS and a custom distributed POSIX filesystem are not target backends.

## 4. Runtime Filesystem

Each running Session receives an isolated writable workspace. Agent and Skill
configuration is projected read-only outside that workspace.

```text
/workspace                              Session writable files
/tma/agent                              read-only Agent projection
/tma/skills/<skill-id>/<version>        read-only Skill package
/tma/context                            read-only Memory/context projection
/run/secrets                            tmpfs or brokered secrets
/tmp                                    ephemeral Turn files
```

There is no persistent per-Agent writable home directory. Shared Agent users
must never share writable files, Memory, or credentials.

## 5. Skill Package Model

A Skill release is an immutable package containing `SKILL.md`, scripts, and
resource files. Metadata identifies the logical Skill and release; object
storage contains package bytes.

```text
skills
skill_versions
  skill_id
  version
  manifest
  package_object_ref
  package_checksum_sha256
skill_version_package_files
  path
  size
  checksum_sha256
  executable
```

Mutable editing happens in a draft or a new personal Skill. Publishing always
creates a new immutable version. An Agent config binds an exact `skill_id` and
version. One Agent config cannot bind the same logical Skill more than once;
different Agents may bind different versions concurrently.

Object keys use stable IDs, not display names:

```text
skills/<workspace>/<skill-id>/versions/<version>/<package-checksum>.zip
```

## 6. Conflict-free Materialization

Execution nodes cache packages by tenant and checksum:

```text
<cache-root>/<workspace>/sha256/<package-checksum>/
```

Cold materialization must:

1. Acquire a per-cache-key lock.
2. Recheck the ready marker after acquiring the lock.
3. Download into a random staging directory.
4. Reject path traversal, unsafe links, unexpected files, and checksum mismatch.
5. Verify every indexed file and its executable bit.
6. Change the tree to read-only.
7. Atomically rename staging to the checksum directory.
8. Write the ready marker last.

Warm materialization reads no package bytes from object storage. Active mounts
hold cache leases so garbage collection cannot remove in-use entries. A package
checksum mismatch is an integrity error, never an overwrite.

Runtime paths use Skill ID and version. Display identifiers are aliases only and
must not determine cache identity. This allows same-named personal and Workspace
Skills, and different versions used by different Sessions, to coexist safely.

## 7. Workspace Portability

Only `/workspace` is mutable across Turns. An execution node obtains a
single-writer Session lease, restores the latest committed snapshot, executes a
Turn, then checkpoints a deterministic manifest and changed blobs to object
storage. The database snapshot record is committed only after all referenced
objects exist.

A failed upload cannot replace the previous committed snapshot. Moving a Session
to another node restores the same workspace bytes using PostgreSQL and object
storage only. Artifact export is independent from automatic workspace snapshots.

## 8. Runtime Roles

- **Turn Runner** coordinates Agent config, context, tools, and Turn lifecycle.
- **tma-worker** is the externally registered local/desktop execution process.
- **Sandbox Execution Node** runs isolated cloud workspaces and materializes
  immutable packages.
- **Execution Node** means either worker type when cache and filesystem rules
  apply to both.

Workers are replaceable compute. They do not own Agent identity, durable Skill
packages, Session Memory, or the authoritative workspace.

## 9. Delivery Order

### A. Agent ownership and config following

- Add minimal Agent ownership columns and RLS.
- Default authenticated creation to personal/private.
- Lazily provision one personal general Agent per user.
- Restrict shared Agent changes to Operator/Admin.
- Default new Turns to the latest Agent config and attribute config per Turn.

Exit gate: two users get different personal Agents; both can invoke one shared
Agent without sharing Session state; operators cannot see another private Agent.

### B. Immutable Skill packages and node cache

- Persist exact package object and file manifests.
- Add the content-addressed cache manager, locking, verification, atomic publish,
  read-only projection, leases, metrics, and garbage collection.
- Remove identifier-only materialization maps and writable Skill paths.

Exit gate: concurrent cold starts produce one valid cache entry; warm starts do
no object GET; corruption fails closed; different versions coexist.

### C. Personal Skill lifecycle

- Allow a user-owned private Skill and mutable draft.
- Publish immutable versions and retain exact Agent bindings.
- Support explicit fork into a new personal Skill identity.
- Keep Workspace publication governed by Operator/Admin approval.

Exit gate: a user can edit and publish a private Skill without mutating an old
Agent config or colliding with a same-named Workspace Skill.

### D. Session workspace snapshots

- Add snapshot manifests, object references, single-writer fencing, restore, and
  retention.
- Checkpoint successful Turns and explicit saves.
- Verify cross-node continuation and interrupted upload behavior.

Exit gate: a Session continues on another node with byte-identical workspace
state and no NFS dependency.

### E. Enterprise catalog

Build on the existing marketplace after ownership and package contracts are
stable. Catalog entries may expose Skill, MCP, Agent configuration starter, or a
Workspace-shared Agent. Catalog visibility does not change runtime ownership;
credentials remain per user and a starter always creates a new Agent.

## 10. Completion Evidence

- Legacy migration and PostgreSQL RLS tests.
- API v2, Go SDK, TypeScript SDK, and Workbench contract tests.
- Multi-user personal/shared Agent authorization tests.
- Session config follow/pin/continuation tests.
- Package traversal, checksum, concurrent writer, read-only, lease, and GC tests.
- Real localfs and S3-compatible object-store tests.
- Cross-node workspace snapshot and restore tests.
- Audit records for actor, Agent config, Skill versions, runtime paths, and
  committed workspace snapshot.

## 11. Non-goals

- No second Agent implementation for personal or shared use.
- No team or organization Agent product type.
- No template inheritance or template-driven upgrade of existing Agents.
- No mutable published Skill version.
- No shared writable Agent directory.
- No object-storage filesystem exposed directly to the model.
- No NFS backend.
