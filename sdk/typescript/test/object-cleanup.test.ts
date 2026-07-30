import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("object cleanup service", () => {
  let server: TestServer | undefined;

  afterEach(async () => {
    await server?.close();
    server = undefined;
  });

  it("previews reconciliation, lists, summarizes, retries, and approves cleanup jobs", async () => {
    const requests: string[] = [];
    server = await startServer(async (request, response) => {
      requests.push(`${request.method} ${request.url}`);
      if (request.url?.includes("/approve")) {
        expect(JSON.parse((await readBody(request)).toString())).toEqual({ confirm: "DELETE job/1" });
      }
				if (request.url === "/v2/object-cleanup/reconciliation/preview") {
				expect(JSON.parse((await readBody(request)).toString())).toEqual({ workspace_id: "workspace/1", prefix: "workspace/1/reports/", limit: 50 });
				json(response, 200, {
					dry_run: true, workspace_id: "workspace/1", storage_provider: "s3", bucket: "artifacts",
					prefix: "workspace/1/reports/", generated_at: "2026-07-30T00:00:00Z",
					scan: { object_refs: { scanned: 1, truncated: false }, provider_objects: { scanned: 1, truncated: false } },
					summary: { total: 0, missing_objects: 0, orphan_objects: 0, metadata_mismatches: 0, provider_errors: 0 }, findings: [],
				});
					return;
				}
				if (request.url === "/v2/object-cleanup/reconciliation/artifacts") {
					expect(JSON.parse((await readBody(request)).toString())).toEqual({
						session_id: "session/1", workspace_id: "workspace/1", prefix: "workspace/1/reports/", limit: 50,
					});
					json(response, 201, {
						report: {
							dry_run: true, workspace_id: "workspace/1", storage_provider: "s3", bucket: "artifacts",
							prefix: "workspace/1/reports/", generated_at: "2026-07-30T00:00:00Z",
							scan: { object_refs: { scanned: 1, truncated: false }, provider_objects: { scanned: 1, truncated: false } },
							summary: { total: 0, missing_objects: 0, orphan_objects: 0, metadata_mismatches: 0, provider_errors: 0 }, findings: [],
						},
						object_ref: { id: "obj_1", workspace_id: "workspace/1", storage_provider: "s3", bucket: "artifacts", object_key: "report.json", size_bytes: 10, visibility: "workspace", created_by: "operator", created_at: "2026-07-30T00:00:00Z" },
						artifact: { id: "art_1", workspace_id: "workspace/1", session_id: "session/1", object_ref_id: "obj_1", name: "report.json", artifact_type: "file", created_by: "operator", created_at: "2026-07-30T00:00:00Z" },
						workspace_path: "/workspace/uploads/art_1/report.json",
					});
					return;
				}
      const job = {
        id: "job/1", workspace_id: "workspace/1", storage_provider: "s3", bucket: "artifacts",
        object_key: "workspace/1/report.docx", size_bytes: 1024, reason: "managed_object_orphaned",
        safe_to_delete: true, status: "pending", attempt_count: 2, next_attempt_at: "2026-07-30T00:00:00Z",
        object_was_missing: false, created_at: "2026-07-29T00:00:00Z", updated_at: "2026-07-30T00:00:00Z",
      };
      if (request.url?.startsWith("/v2/object-cleanup/stats")) {
        json(response, 200, { workspace_id: "workspace/1", statuses: [], oldest_pending_age_seconds: 0, orphans_staged: 0, total_attempts: 0, total_retried_jobs: 0, total_deleted_bytes: 0 });
        return;
      }
      json(response, 200, request.method === "GET" ? { jobs: [job] } : job);
    });

    const client = new TMAClient(server.baseURL);
			const preview = await client.objectCleanup.previewReconciliation({ workspace_id: "workspace/1", prefix: "workspace/1/reports/", limit: 50 });
			const exported = await client.objectCleanup.exportReconciliationArtifact({ session_id: "session/1", workspace_id: "workspace/1", prefix: "workspace/1/reports/", limit: 50 });
    const jobs = await client.objectCleanup.list({
      workspaceId: "workspace/1", status: "dead_letter", reason: "artifact_create_failed",
      createdFrom: new Date("2026-07-01T00:00:00Z"), createdTo: "2026-07-30T00:00:00Z", limit: 20,
    });
    await client.objectCleanup.stats("workspace/1");
    await client.objectCleanup.retry("job/1", "workspace/1");
    await client.objectCleanup.approve("job/1", "DELETE job/1", "workspace/1");

    expect(jobs[0]?.object_key).toBe("workspace/1/report.docx");
			expect(preview.dry_run).toBe(true);
			expect(exported.artifact.id).toBe("art_1");
			expect(requests).toEqual([
				"POST /v2/object-cleanup/reconciliation/preview",
				"POST /v2/object-cleanup/reconciliation/artifacts",
      "GET /v2/object-cleanup/jobs?workspace_id=workspace%2F1&status=dead_letter&reason=artifact_create_failed&created_from=2026-07-01T00%3A00%3A00.000Z&created_to=2026-07-30T00%3A00%3A00Z&limit=20",
      "GET /v2/object-cleanup/stats?workspace_id=workspace%2F1",
      "POST /v2/object-cleanup/jobs/job%2F1/retry?workspace_id=workspace%2F1",
      "POST /v2/object-cleanup/jobs/job%2F1/approve?workspace_id=workspace%2F1",
    ]);
  });
});
