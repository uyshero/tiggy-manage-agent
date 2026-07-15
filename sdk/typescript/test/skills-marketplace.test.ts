import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("Skills and Marketplace services", () => {
  let server: TestServer | undefined;
  afterEach(async () => { await server?.close(); server = undefined; });

  it("covers Skill lifecycle, packages, retention, and asset GC", async () => {
    const requests: string[] = [];
    server = await startServer(async (request, response) => {
      requests.push(`${request.method} ${request.url}`);
      await readBody(request);
      if (request.url?.endsWith("/package")) { response.writeHead(200, { "content-type": "application/zip" }); response.end("skill-package"); return; }
      if (request.url?.startsWith("/v2/skills?") ) { json(response, 200, { skills: [skillFixture("future_state")] }); return; }
      if (request.url?.endsWith("/versions") && request.method === "GET") { json(response, 200, { versions: [versionFixture()] }); return; }
      if (request.url?.endsWith("/versions/1")) { json(response, 200, versionFixture()); return; }
      if (request.url?.includes("skill-usages")) { json(response, 200, { skill_usages: [] }); return; }
      if (request.url === "/v2/skills/resolve-preview") { json(response, 200, { config: { enabled: [] }, rendered: { format: "tma.skills.context.v1", content: "Review" }, skills: [], estimated_tokens: 1, truncated: false, extension: { preserved: true } }); return; }
      if (request.url === "/v2/skill-packages/backfill") { json(response, 200, { workspace_id: "workspace/1", scanned: 1, migrated: 1 }); return; }
      if (request.url?.includes("skill-asset-retention/effective")) { json(response, 200, { source: "workspace", config: { enabled: true, retention_days: 30, delete_limit: 25 }, revision: "revision-1" }); return; }
      if (request.url?.startsWith("/v2/skill-asset-retention/policies?")) { json(response, 200, { policies: [retentionPolicyFixture("active")] }); return; }
      if (request.url?.endsWith("/archive") && request.url.includes("retention")) { json(response, 200, retentionPolicyFixture("archived")); return; }
      if (request.url?.includes("skill-asset-retention/policies") && request.url?.endsWith("/versions/1")) { json(response, 200, retentionVersionFixture()); return; }
      if (request.url?.includes("skill-asset-retention/policies") && request.url?.endsWith("/versions")) { json(response, 201, retentionVersionFixture()); return; }
      if (request.url?.includes("skill-asset-retention/policies")) { json(response, request.method === "POST" ? 201 : 200, { policy: retentionPolicyFixture("active"), version: retentionVersionFixture() }); return; }
      if (request.url === "/v2/skill-asset-gc/preview") { json(response, 200, { workspace_id: "workspace/1", effective_policy: { source: "workspace", config: {}, revision: "r1" }, cutoff: "2026-06-15T00:00:00Z", candidate_count: 0, candidate_bytes: 0, candidates: [] }); return; }
      if (request.url === "/v2/skill-asset-gc/run") { json(response, 200, { run: gcRunFixture("future_gc_state"), items: [] }); return; }
      if (request.url?.startsWith("/v2/skill-asset-gc/runs?")) { json(response, 200, { runs: [gcRunFixture("future_gc_state")] }); return; }
      if (request.url?.startsWith("/v2/skill-asset-gc/runs/")) { json(response, 200, { run: gcRunFixture("future_gc_state"), items: [] }); return; }
      if (request.url?.startsWith("/v2/skill-asset-gc/tombstones")) { json(response, 200, { tombstones: [] }); return; }
      if (request.url?.endsWith("/archive")) { json(response, 200, skillFixture("archived")); return; }
      if (request.url?.endsWith("/versions")) { json(response, 201, versionFixture()); return; }
      json(response, request.method === "POST" ? 201 : 200, skillFixture("active"));
    });
    const client = new TMAClient(server.baseURL);
    await client.skills.create({ identifier: "review", title: "Review", source_type: "inline" });
    const skills = await client.skills.list({ workspaceId: "workspace/1", includeArchived: true });
    await client.skills.get("skill/1");
    await client.skills.archive("skill/1");
    await client.skills.createVersion("skill/1", { manifest: { inputs_schema: { type: "object", extension: { preserved: true } } }, content_text: "Review" });
    await client.skills.listVersions("skill/1");
    const version = await client.skills.getVersion("skill/1", 1);
    const archive = await client.skills.downloadPackage("skill/1", 1);
    const resolved = await client.skills.resolvePreview({ workspace_id: "workspace/1", skills: { enabled: [{ skill: "review", inputs: { style: { strict: true } } }] } });
    await client.skills.listUsages("session/1", "turn/1");
    await client.skills.backfillPackages({ workspace_id: "workspace/1" });
    await client.skills.effectiveRetentionPolicy("workspace/1");
    await client.skills.createRetentionPolicy({ scope_type: "workspace", workspace_id: "workspace/1", config: { enabled: true, retention_days: 30, delete_limit: 25 } });
    await client.skills.listRetentionPolicies({ workspaceId: "workspace/1", includeArchived: true });
    await client.skills.getRetentionPolicy("policy/1");
    await client.skills.publishRetentionPolicyVersion("policy/1", { config: { retention_days: 60 } });
    await client.skills.getRetentionPolicyVersion("policy/1", 1);
    await client.skills.archiveRetentionPolicy("policy/1");
    await client.skills.previewAssetGC({ workspace_id: "workspace/1", limit: 20 });
    const gc = await client.skills.runAssetGC({ workspace_id: "workspace/1", confirm: "DELETE" });
    await client.skills.listAssetGCRuns({ workspaceId: "workspace/1", limit: 20 });
    await client.skills.getAssetGCRun("gc/1");
    await client.skills.listAssetGCTombstones({ workspaceId: "workspace/1", limit: 20 });

    expect(skills[0]?.status).toBe("future_state");
    expect(version.manifest.inputs_schema).toMatchObject({ extension: { preserved: true } });
    expect(await archive.text()).toBe("skill-package");
    expect(resolved).toMatchObject({ extension: { preserved: true } });
    expect(gc.run.status).toBe("future_gc_state");
    expect(requests).toContain("GET /v2/skills?workspace_id=workspace%2F1&include_archived=true");
    expect(requests).toContain("GET /v2/skills/skill%2F1/versions/1/package");
    expect(requests).toContain("GET /v2/sessions/session%2F1/skill-usages?turn_id=turn%2F1");
    expect(requests).toContain("GET /v2/skill-asset-gc/runs/gc%2F1");
  });

  it("covers external/internal Marketplace, entries, policies, and repeated tags", async () => {
    const requests: string[] = [];
    const requestBodies: unknown[] = [];
    server = await startServer(async (request, response) => {
      requests.push(`${request.method} ${request.url}`);
      const raw = await readBody(request);
      if (raw.length) requestBodies.push(JSON.parse(raw.toString()));
      if (request.url?.includes("/marketplace/discover")) { json(response, 200, { provider: "github", search_mode: "repository", items: [], count: 0 }); return; }
      if (request.url?.endsWith("/marketplace/internal?session_id=session%2F1&query=review&category=quality&tag=go&tag=security&limit=20")) { json(response, 200, { provider: "catalog", items: [], count: 0 }); return; }
      if (request.url?.endsWith("/marketplace/preview") || request.url?.endsWith("/internal/preview")) { json(response, 200, marketplacePreviewFixture()); return; }
      if (request.url?.endsWith("/marketplace/install") || request.url?.endsWith("/internal/install")) { json(response, 201, { skill: skillFixture("active"), version: versionFixture(), extension: { preserved: true } }); return; }
      if (request.url?.endsWith("/enable")) { json(response, 200, { agent_id: "agent/1", binding: { skill: "review", version: 1 }, changed: true }); return; }
      if (request.url?.endsWith("/disable")) { json(response, 200, { agent_id: "agent/1", binding: { skill: "review", version: 1 }, removed: true }); return; }
      if (request.url?.startsWith("/v2/skill-marketplace-entries?")) { json(response, 200, { entries: [entryFixture("future_entry_state")] }); return; }
      if (request.url?.startsWith("/v2/skill-marketplace-entries/")) { json(response, 200, entryFixture("draft")); return; }
      if (request.url === "/v2/skill-marketplace-entries") { json(response, 201, entryFixture("draft")); return; }
      if (request.url?.startsWith("/v2/skill-marketplace-policies?")) { json(response, 200, { policies: [marketplacePolicyFixture("active")] }); return; }
      if (request.url?.endsWith("/archive")) { json(response, 200, marketplacePolicyFixture("archived")); return; }
      if (request.url?.endsWith("/versions/1")) { json(response, 200, marketplacePolicyVersionFixture()); return; }
      if (request.url?.endsWith("/versions")) { json(response, 201, marketplacePolicyVersionFixture()); return; }
      json(response, request.method === "POST" ? 201 : 200, { policy: marketplacePolicyFixture("active"), version: marketplacePolicyVersionFixture() });
    });
    const client = new TMAClient(server.baseURL);
    const source = { provider: "github" as const, repository: "acme/review", ref: "main" };
    await client.marketplace.discover({ sessionId: "session/1", query: "review", repository: "acme/review", limit: 10 });
    const preview = await client.marketplace.preview({ session_id: "session/1", source });
    const installed = await client.marketplace.install({ session_id: "session/1", source, policy_revision: "revision-1" });
    await client.marketplace.browseInternal({ sessionId: "session/1", query: "review", category: "quality", tags: ["go", "security"], limit: 20 });
    const catalogSource = { provider: "catalog" as const, catalog_entry_id: "entry/1" };
    await client.marketplace.previewInternal({ session_id: "session/1", source: catalogSource });
    await client.marketplace.installInternal({ session_id: "session/1", source: catalogSource });
    await client.marketplace.enableInstalled("skill/1", { session_id: "session/1", version: 1, inputs: { style: { strict: true } } });
    await client.marketplace.disableInstalled("skill/1", { session_id: "session/1" });
    await client.marketplace.createEntry({ workspace_id: "workspace/1", skill_id: "skill/1", skill_version: 1, tags: ["go"] });
    const entries = await client.marketplace.listEntries({ workspaceId: "workspace/1", status: "draft", includeWithdrawn: true });
    await client.marketplace.getEntry("entry/1", "workspace/1");
    await client.marketplace.updateEntry("entry/1", { summary: "review" });
    await client.marketplace.submitEntry("entry/1", { workspace_id: "workspace/1", note: "review" });
    await client.marketplace.publishEntry("entry/1", { workspace_id: "workspace/1", note: "approved" });
    await client.marketplace.withdrawEntry("entry/1", { workspace_id: "workspace/1", note: "retired" });
    await client.marketplace.createPolicy({ scope_type: "workspace", workspace_id: "workspace/1", config: { allowed_owners: ["acme"], trusted_attestation_keys: { key: "value" } } });
    await client.marketplace.listPolicies({ workspaceId: "workspace/1", includeArchived: true });
    await client.marketplace.getPolicy("policy/1");
    await client.marketplace.publishPolicyVersion("policy/1", { config: { allowed_owners: ["acme"] } });
    await client.marketplace.getPolicyVersion("policy/1", 1);
    await client.marketplace.archivePolicy("policy/1");

    expect(preview.policy).toMatchObject({ extension: { preserved: true } });
    expect(installed).toMatchObject({ extension: { preserved: true } });
    expect(entries[0]?.status).toBe("future_entry_state");
    expect(requestBodies).toContainEqual(expect.objectContaining({ inputs: { style: { strict: true } } }));
    expect(requests).toContain("GET /v2/skills/marketplace/discover?session_id=session%2F1&query=review&repository=acme%2Freview&limit=10");
    expect(requests).toContain("GET /v2/skills/marketplace/internal?session_id=session%2F1&query=review&category=quality&tag=go&tag=security&limit=20");
    expect(requests).toContain("GET /v2/skill-marketplace-entries/entry%2F1?workspace_id=workspace%2F1");
    expect(requests).toContain("GET /v2/skill-marketplace-policies/policy%2F1/versions/1");
  });
});

function skillFixture(status: string) {
  return { id: "skill/1", workspace_id: "workspace/1", identifier: "review", title: "Review", owner_type: "workspace", source_type: "inline", status, created_by: "user/1", created_at: "2026-07-15T00:00:00Z" };
}

function versionFixture() {
  return { id: "version/1", skill_id: "skill/1", version: 1, content_format: "markdown", manifest: { inputs_schema: { type: "object", extension: { preserved: true } } }, content_text: "Review", checksum_sha256: "checksum", package_format: "tma.skill-package.v1", created_by: "user/1", created_at: "2026-07-15T00:00:00Z" };
}

function retentionPolicyFixture(status: string) {
  return { id: "policy/1", scope_type: "workspace", workspace_id: "workspace/1", status, current_version: 1, created_by: "user/1", created_at: "2026-07-15T00:00:00Z" };
}

function retentionVersionFixture() {
  return { id: "policy-version/1", policy_id: "policy/1", version: 1, config: { enabled: true, retention_days: 30, delete_limit: 25 }, checksum_sha256: "revision-1", created_by: "user/1", created_at: "2026-07-15T00:00:00Z" };
}

function gcRunFixture(status: string) {
  return { id: "gc/1", workspace_id: "workspace/1", dry_run: false, policy_source: "workspace", policy_revision: "revision-1", retention_days: 30, delete_limit: 25, status, candidate_count: 0, deleted_count: 0, skipped_count: 0, failed_count: 0, bytes_deleted: 0, requested_by: "user/1", started_at: "2026-07-15T00:00:00Z" };
}

function marketplacePreviewFixture() {
  return { identifier: "review", source: { provider: "github", repository: "acme/review" }, assets: { files: [], total_bytes: 0 }, policy: { allowed: true, checks: [], extension: { preserved: true } }, security: { digest_sha256: "digest", attestation: { status: "missing", digest_sha256: "digest", message: "missing" }, findings: [], scanned_files: 0, binary_files: [], sbom: { format: "tma.skill.sbom.v1", package_digest_sha256: "digest", components: [] } }, install_state: "new_install", changes: { content_changed: false, added_files: [], removed_files: [], changed_files: [] } };
}

function entryFixture(status: string) {
  return { id: "entry/1", workspace_id: "workspace/1", skill_id: "skill/1", skill_version: 1, skill_identifier: "review", skill_title: "Review", skill_status: "active", version_checksum_sha256: "checksum", package_format: "tma.skill-package.v1", tags: [], status, created_by: "user/1", created_at: "2026-07-15T00:00:00Z", updated_by: "user/1", updated_at: "2026-07-15T00:00:00Z" };
}

function marketplacePolicyFixture(status: string) {
  return { id: "policy/1", scope_type: "workspace", workspace_id: "workspace/1", status, current_version: 1, created_by: "user/1", created_at: "2026-07-15T00:00:00Z" };
}

function marketplacePolicyVersionFixture() {
  return { id: "policy-version/1", policy_id: "policy/1", version: 1, config: { allowed_owners: ["acme"] }, checksum_sha256: "revision-1", created_by: "user/1", created_at: "2026-07-15T00:00:00Z" };
}
