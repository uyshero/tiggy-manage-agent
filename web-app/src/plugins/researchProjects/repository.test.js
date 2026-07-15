import assert from "node:assert/strict";
import test from "node:test";

import {
  createResearchProjectRepository,
  projectBriefMarkdown,
  storageKeyForScope
} from "./repository.js";

function memoryStorage() {
  const values = new Map();
  return {
    getItem(key) { return values.get(key) ?? null; },
    setItem(key, value) { values.set(key, value); }
  };
}

test("research repositories isolate browser drafts by workspace and user", () => {
  const storage = memoryStorage();
  const alpha = createResearchProjectRepository({
    storage,
    scope: { workspaceId: "wksp_alpha", userId: "user_01" },
    now: () => Date.parse("2026-07-14T08:00:00Z"),
    randomID: () => "alpha"
  });
  const beta = createResearchProjectRepository({
    storage,
    scope: { workspaceId: "wksp_beta", userId: "user_01" },
    now: () => Date.parse("2026-07-14T08:00:00Z"),
    randomID: () => "beta"
  });

  alpha.create({ name: "材料调研", objective: "整理技术路线" });
  assert.equal(alpha.list().length, 1);
  assert.equal(beta.list().length, 0);
  assert.notEqual(alpha.key, beta.key);
  assert.match(storageKeyForScope({ workspaceId: "wksp alpha", userId: "user/01" }), /wksp%20alpha:user%2F01$/);
});

test("research repository updates lifecycle and deduplicates resource refs", () => {
  const storage = memoryStorage();
  let timestamp = Date.parse("2026-07-14T08:00:00Z");
  const repository = createResearchProjectRepository({
    storage,
    scope: { workspaceId: "wksp_01", userId: "user_01" },
    now: () => timestamp,
    randomID: () => "project-01"
  });
  const project = repository.create({ name: "材料调研", objective: "整理技术路线" });
  timestamp += 1000;
  repository.update(project.id, { name: "材料调研二期", stage: "collecting", nextStep: "补齐行业报告" });
  const resource = {
    id: "art_01",
    type: "artifact",
    title: "路线对比.md",
    source: "tma.session-artifact:sesn_01",
    previewable: true,
    metadata: { turn_id: "turn_01" }
  };
  repository.attachResource(project.id, resource);
  repository.attachResource(project.id, { ...resource, title: "路线对比（更新）.md" });

  assert.equal(repository.get(project.id).name, "材料调研二期");
  assert.equal(repository.get(project.id).stage, "collecting");
  assert.equal(repository.get(project.id).nextStep, "补齐行业报告");
  assert.equal(repository.get(project.id).resources.length, 1);
  assert.equal(repository.get(project.id).resources[0].title, "路线对比（更新）.md");
  assert.equal(repository.get(project.id).resources[0].linkedAt, "2026-07-14T08:00:01.000Z");

  timestamp += 1000;
  const finding = repository.addFinding(project.id, {
    type: "hypothesis",
    title: "路线 A 可能更适合量产",
    content: "仍需验证成本数据"
  });
  assert.equal(repository.get(project.id).findings[0].type, "hypothesis");
  repository.updateFinding(project.id, finding.id, { type: "finding", content: "成本数据已验证" });
  assert.equal(repository.get(project.id).findings[0].content, "成本数据已验证");
  repository.removeFinding(project.id, finding.id);
  assert.equal(repository.get(project.id).findings.length, 0);

  repository.detachResource(project.id, resource.id, resource.source);
  assert.equal(repository.get(project.id).resources.length, 0);
  repository.archive(project.id);
  assert.equal(repository.list().length, 0);
  assert.equal(repository.list({ includeArchived: true })[0].status, "archived");
  repository.restore(project.id);
  assert.equal(repository.list()[0].status, "active");
});

test("research repository migrates older browser drafts in memory", () => {
  const storage = memoryStorage();
  const scope = { workspaceId: "wksp_legacy", userId: "user_legacy" };
  storage.setItem(storageKeyForScope(scope), JSON.stringify([{
    id: "research_legacy",
    name: "旧项目",
    objective: "验证兼容性",
    description: "",
    status: "active",
    resources: [{ id: "art_legacy", type: "artifact", title: "旧成果.md", source: "tma.session-artifact:sesn_legacy", metadata: {} }],
    createdAt: "2026-07-01T00:00:00.000Z",
    updatedAt: "2026-07-02T00:00:00.000Z"
  }]));
  const repository = createResearchProjectRepository({ storage, scope });
  const project = repository.get("research_legacy");
  assert.equal(project.stage, "planning");
  assert.equal(project.nextStep, "");
  assert.deepEqual(project.findings, []);
  assert.equal(project.resources[0].linkedAt, "2026-07-02T00:00:00.000Z");
});

test("project brief exports stable markdown without inventing AI conclusions", () => {
  const markdown = projectBriefMarkdown({
    name: "电池材料调研",
    objective: "比较三条路线",
    description: "第一阶段资料整理",
    stage: "analyzing",
    nextStep: "补充成本数据",
    updatedAt: "2026-07-14T08:00:00Z",
    findings: [{ title: "路线 A 具备优势", type: "finding", content: "证据来自行业报告" }],
    resources: [{ title: "路线对比.md", type: "artifact" }]
  });
  assert.match(markdown, /^# 电池材料调研/);
  assert.match(markdown, /阶段：分析归纳/);
  assert.match(markdown, /路线 A 具备优势/);
  assert.match(markdown, /补充成本数据/);
  assert.match(markdown, /- 路线对比\.md \(artifact\)/);
  assert.doesNotMatch(markdown, /AI 结论/);
});
