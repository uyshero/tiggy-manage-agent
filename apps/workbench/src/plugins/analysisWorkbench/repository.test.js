import assert from "node:assert/strict";
import test from "node:test";

import {
  createAnalysisWorkspaceRepository,
  DEFAULT_NOTEBOOK_CODE,
  storageKeyForScope
} from "./repository.js";

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

  repository.update(project.id, { notebookURL: "http://127.0.0.1:18888/lab", gitStatus: "synced" });
  assert.equal(repository.list()[0].gitStatus, "synced");
  assert.equal(repository.list()[0].notebookURL, "http://127.0.0.1:18888/lab");
});
