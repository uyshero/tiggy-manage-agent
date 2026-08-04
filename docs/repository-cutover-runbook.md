# Repository cutover runbook

This runbook moves Knowledge and R Survival traffic and data from Platform
compatibility implementations to their independent services. Biography is
already an independent codebase; its section validates Platform Speech and
deployment boundaries. Execute the runbook once per environment and retain all
command output with the release record.

No step in this document implies that production traffic has already moved.
Production completion requires an approved change window, real database
credentials, Gateway access, monitoring access, and named operators.

## Release inputs

Record these immutable values before the window:

| Input | Required value |
| --- | --- |
| Platform image and migration release | image digest and Git commit |
| Core SDK | value from `sdk/VERSION` |
| Knowledge API/Web | image digests and Git commit |
| Biography gateway/mobile | image digest or artifact checksum and Git commit |
| R Survival API/Web/Runtime | image digests and Git commit |
| Database backups | encrypted backup locations and restore test result |
| Gateway configuration | current export, candidate export, owner and rollback command |
| Observation window | start/end time and on-call owners |

The application release must declare a supported Core SDK/API version. The Go
applications verify `platform-sdk.version` and `platform-sdk.sha256` in CI. R
Survival currently declares the Platform API version while its Web client is
migrated from direct versioned HTTP calls to the published TypeScript SDK.

## 1. Preflight

1. Run repository verification from each clean checkout:

   ```bash
   make -C tma-platform ci
   make -C tma-knowledge ci
   make -C tma-biography ci
   make -C tma-r-survival-workbench ci
   make -C tma-r-survival-workbench runtime-smoke
   ```

2. Confirm Platform Core health, Retrieval readiness, Speech model capability,
   application `/health` and `/ready` endpoints, database capacity, object
   storage, GitLab, and R Runtime capacity.
3. Apply Platform migrations through `000117` and each application's own
   migrations. Never let service startup apply another service's schema.
4. Take consistent backups of the Platform, Knowledge, Biography, and R
   Survival databases. Perform a restore rehearsal in an isolated database.
5. Export the active Gateway configuration and verify the rollback command
   targets that exact revision.
6. Disable Knowledge and legacy Workbench writes at the Gateway or place the
   affected products in maintenance mode. Do not use dual writes. Confirm the
   write freeze with access logs and a rejected synthetic write.
7. Ensure no user traffic reaches the new Knowledge or R Survival service until
   copy and verification complete.

Abort before copying if any backup, restore rehearsal, health check, version
check, write freeze, or operator assignment is missing.

## 2. Data copy

Use a temporary audited source role with `SELECT` plus `BYPASSRLS`, because the
legacy Platform application tables force row-level security. The tools reject a
source role that cannot bypass RLS rather than risk reporting a false zero-row
success. Use separate least-privilege write credentials for each target. Save
each JSON report as a release artifact and revoke the temporary source role
after the window.

Knowledge:

```bash
cd tma-knowledge
PLATFORM_DATABASE_URL='<source>' KNOWLEDGE_DATABASE_URL='<target>' \
  make migrate-platform-data-dry-run
PLATFORM_DATABASE_URL='<source>' KNOWLEDGE_DATABASE_URL='<target>' \
  make migrate-platform-data
PLATFORM_DATABASE_URL='<source>' KNOWLEDGE_DATABASE_URL='<target>' \
  make migrate-platform-data-verify
```

The tool copies only `knowledge_services`, `knowledge_service_shares`, and
`knowledge_service_questions`. Retrieval resources remain in Platform and are
referenced by opaque IDs.

R Survival:

```bash
cd tma-r-survival-workbench
PLATFORM_DATABASE_URL='<source>' R_SURVIVAL_DATABASE_URL='<target>' \
  make migrate-platform-data-dry-run
PLATFORM_DATABASE_URL='<source>' R_SURVIVAL_DATABASE_URL='<target>' \
  make migrate-platform-data
PLATFORM_DATABASE_URL='<source>' R_SURVIVAL_DATABASE_URL='<target>' \
  make migrate-platform-data-verify
```

The R tool selects only rows whose plugin ID is
`com.tma.r-survival-workbench`. It preserves project, GitLab, Notebook, Runtime,
file metadata, owner, and timestamps. It never migrates unrelated Workbench
projects.

Both copy commands are idempotent UPSERTs protected by a target advisory lock.
They automatically verify row counts and canonical SHA-256 content digests.
Any mismatch exits nonzero. Do not continue on a mismatch.

## 3. Service validation

While writes remain frozen, validate against the new services through an
operator-only Gateway route:

- authenticate as users from at least two workspaces and prove cross-workspace
  access is denied;
- list, read, and update one copied Knowledge service, then discard the test
  transaction or restore the original value before final digest verification;
- execute a Knowledge question that uses Platform Retrieval and confirm
  citations, refusal policy, audit row, quota, and trace correlation;
- list and open copied R projects, verify GitLab links and file metadata, start
  an isolated R Runtime, run a minimal `survival` analysis, stop the Runtime,
  and verify cleanup;
- run Biography ASR and TTS through Platform Speech and confirm Biography has
  no provider credential or direct provider connection;
- inspect Platform and application logs for a shared request/trace ID and
  verify tokens are not logged.

Run the two `make migrate-platform-data-verify` commands again after validation.

## 4. Gateway cutover

Route application APIs to their owners; Platform Core routes remain on
Platform:

| Route family | Owner |
| --- | --- |
| Knowledge Service, Share, Question and Knowledge Web | `tma-knowledge` |
| `/v2/retrieval/*`, Agent, Session, Run, Model, Speech and Artifact | `tma-platform` |
| Biography voice/application routes | `tma-biography` |
| `/v2/r-survival-projects/*` and R Survival Web | `tma-r-survival-workbench` |

Apply the candidate Gateway revision, validate one read per route, then enable
writes only on the new application services. Keep legacy Platform application
routes read-only or unreachable from normal users.

Observe at minimum request rate, p50/p95/p99 latency, HTTP/WebSocket errors,
authentication failures, database saturation, Retrieval/Speech provider
errors, Worker queue depth, R Runtime start failures, and application business
success counts. Compare with the pre-window baseline.

## 5. Rollback

Rollback immediately for data mismatch, authorization leakage, sustained error
or latency regression beyond the approved threshold, unavailable critical
workflow, or missing audit/usage records.

1. Freeze application writes again.
2. Export and retain all target-side rows written after cutover. Never discard
   them or overwrite the source blindly.
3. Restore the previous Gateway revision and validate legacy read paths.
4. Re-enable legacy writes only after the data owner decides how target-side
   deltas will be reconciled.
5. Keep the new databases and logs intact for diagnosis. Database restore is a
   separate approved action, not an automatic rollback step.
6. Record the trigger, affected requests, data delta, and next attempt criteria.

If no writes reached the new services, Gateway rollback is sufficient. Once a
new service accepted writes, rollback is a data reconciliation event and
requires the application data owner.

## 6. Compatibility removal gate

Do not remove Platform compatibility routes, handlers, static assets, old
tables, or migration history during the cutover release. Removal requires all
of the following:

- at least two successful application releases and 30 days after production
  cutover;
- 14 consecutive days with zero normal-user traffic to legacy routes;
- verified backups retained through the rollback policy;
- no unresolved target/source data delta;
- dashboards, alerts, deployment manifests, and documentation point only to
  the owning service;
- a dedicated reviewed change removes code first and tables in a later change.

Historical Platform migrations remain immutable. A future Platform migration
may drop application tables only after this gate is signed off by Platform,
application, database, security, and operations owners.
