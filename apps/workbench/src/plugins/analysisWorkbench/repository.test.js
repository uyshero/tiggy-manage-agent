import assert from "node:assert/strict";
import test from "node:test";

import {
  createAnalysisWorkspaceClient,
  createAnalysisWorkspaceRepository,
  DEFAULT_NOTEBOOK_CODE,
  R_SURVIVAL_WORKBENCH_PLUGIN_ID,
  storageKeyForScope
} from "./repository.js";
import { R_SURVIVAL_DATA_CLEANING_SKILL_PATH } from "./survivalSkill.js";

function memoryStorage() {
  const values = new Map();
  return {
    getItem(key) { return values.get(key) ?? null; },
    setItem(key, value) { values.set(key, value); }
  };
}

test("analysis workspace drafts are isolated by workspace and user", () => {
  const storage = memoryStorage();
  const alpha = createAnalysisWorkspaceRepository({
    storage,
    scope: { workspaceId: "wksp_alpha", userId: "user_01" },
    now: () => Date.parse("2026-07-27T02:00:00Z"),
    randomID: () => "alpha"
  });
  const beta = createAnalysisWorkspaceRepository({
    storage,
    scope: { workspaceId: "wksp_beta", userId: "user_01" },
    randomID: () => "beta"
  });

  alpha.ensureExample();
  assert.equal(alpha.list().length, 1);
  assert.equal(beta.list().length, 0);
  assert.notEqual(alpha.key, beta.key);
  assert.match(storageKeyForScope({ workspaceId: "wksp alpha", userId: "user/01" }), /wksp%20alpha:user%2F01$/);
});

test("analysis workspace creates a reproducible R project draft", () => {
  const repository = createAnalysisWorkspaceRepository({
    storage: memoryStorage(),
    scope: { workspaceId: "wksp_01", userId: "user_01" },
    now: () => Date.parse("2026-07-27T02:00:00Z"),
    randomID: () => "project-01"
  });
  const project = repository.create({ name: "中文生存分析", repositoryPath: "survival-cn" });

  assert.equal(project.repositoryPath, "survival-cn");
  assert.equal(project.gitStatus, "local");
  assert.equal(project.activeFile, "notebooks/survival-analysis.ipynb");
  assert.equal(project.notebookCode, DEFAULT_NOTEBOOK_CODE);
  assert.ok(project.files.some((file) => file.path === "renv.lock"));
  assert.ok(project.files.some((file) => file.path === R_SURVIVAL_DATA_CLEANING_SKILL_PATH && file.content?.includes("不把字段名")));

  repository.update(project.id, { notebookURL: "http://127.0.0.1:18888/lab", gitStatus: "synced" });
  assert.equal(repository.list()[0].gitStatus, "synced");
  assert.equal(repository.list()[0].notebookURL, "http://127.0.0.1:18888/lab");
});

test("analysis workspace client persists projects through the scoped API", async () => {
  const requests = [];
  const project = {
    id: "wbp_000001",
    name: "中文生存分析",
    objective: "验证后端项目",
    repository_path: "survival-cn",
    repository_url: "",
    notebook_url: "",
    sync_status: "local",
    sync_error: "",
    default_branch: "main",
    active_file: "notebooks/survival-analysis.ipynb",
    notebook_code: DEFAULT_NOTEBOOK_CODE,
    files: [{ path: "README.md", kind: "file", status: "clean" }],
    created_at: "2026-07-27T02:00:00Z",
    updated_at: "2026-07-27T02:00:00Z"
  };
  const client = createAnalysisWorkspaceClient({
    scope: { workspaceId: "wksp_01", userId: "user_01" },
    http: {
      async request(path, options = {}) {
        requests.push({ path, options });
        if (path.includes("/runtime/run-cleaning")) return { project: { ...project, active_file: "reports/data-cleaning-summary.md" }, result: { exit_code: 0, stdout: "ok" } };
        if (options.method === "PATCH") return { ...project, active_file: options.body.active_file || project.active_file };
        if (options.method === "POST") return project;
        return { projects: [project], gitlab_configured: false };
      }
    }
  });

  const listed = await client.list();
  assert.equal(listed.projects[0].persistence, "backend");
  assert.equal(listed.gitLabConfigured, false);
  const created = await client.create({ name: "中文生存分析", repositoryPath: "survival-cn" });
  assert.equal(created.id, "wbp_000001");
  await client.update("wbp_000001", { activeFile: "R/clean-data.R" });
  await client.update("wbp_000001", { files: [{ path: "R/clean-data.R", kind: "file", status: "modified", content: "followup <- raw_followup" }] });
  await client.sync("wbp_000001");
  await client.startRuntime("wbp_000001");
  await client.stopRuntime("wbp_000001");
  const cleaning = await client.runCleaning("wbp_000001");
  assert.equal(cleaning.project.activeFile, "reports/data-cleaning-summary.md");
  assert.equal(cleaning.result.stdout, "ok");
  assert.equal(requests[1].options.body.plugin_id, R_SURVIVAL_WORKBENCH_PLUGIN_ID);
  assert.equal(requests[1].options.body.repository_path, "survival-cn");
  assert.match(requests[0].path, /plugin_id=com\.tma\.r-survival-workbench/);
  assert.match(requests[2].path, /workspace_id=wksp_01/);
  assert.equal(requests[3].options.body.files[0].content, "followup <- raw_followup");
  assert.match(requests[3].path, /workspace_id=wksp_01/);
  assert.match(requests[5].path, /workspace_id=wksp_01/);
  assert.match(requests[6].path, /workspace_id=wksp_01/);
  assert.match(requests[7].path, /runtime\/run-cleaning/);
});
