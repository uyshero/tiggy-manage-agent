import assert from "node:assert/strict";
import test from "node:test";

import {
  marketplaceEntryNextAction,
  marketplaceEntryStageState,
  marketplaceEntryStages,
  marketplaceEntryStatusLabel,
  marketplaceEntryStatusTone,
  marketplaceInstallStateMeta,
  marketplaceUpgradeVersions
} from "./marketplaceCatalog.js";

test("uses the four-state marketplace lifecycle", () => {
  assert.deepEqual(marketplaceEntryStages.map((stage) => stage.status), [
    "draft", "pending_review", "published", "withdrawn"
  ]);
  assert.equal(marketplaceEntryStatusLabel("pending_review"), "待审核");
  assert.equal(marketplaceEntryStatusTone("published"), "ok");
  assert.equal(marketplaceEntryStatusTone("withdrawn"), "danger");
});

test("maps each state to one forward action", () => {
  assert.deepEqual(marketplaceEntryNextAction("draft"), {
    action: "submit", label: "提交审核", targetStatus: "pending_review"
  });
  assert.equal(marketplaceEntryNextAction("pending_review").action, "publish");
  assert.equal(marketplaceEntryNextAction("published").action, "withdraw");
  assert.equal(marketplaceEntryNextAction("withdrawn"), null);
});

test("marks completed, current, and upcoming lifecycle stages", () => {
  assert.equal(marketplaceEntryStageState("published", "draft"), "complete");
  assert.equal(marketplaceEntryStageState("published", "published"), "current");
  assert.equal(marketplaceEntryStageState("published", "withdrawn"), "upcoming");
});

test("maps catalog consumer states to bounded candidate actions", () => {
  assert.deepEqual(marketplaceInstallStateMeta("new_install"), {
    label: "可安装", tone: "ok", actionLabel: "安全预览"
  });
  assert.deepEqual(marketplaceInstallStateMeta("upgrade"), {
    label: "有新版本", tone: "warn", actionLabel: "查看更新"
  });
  assert.equal(marketplaceInstallStateMeta("unchanged").label, "已安装");
  assert.equal(marketplaceInstallStateMeta("blocked").tone, "danger");
});

test("derives the next immutable consumer version only for upgrades", () => {
  assert.deepEqual(marketplaceUpgradeVersions({
    install_state: "upgrade", existing: { version: 3 }
  }), { current: 3, target: 4 });
  assert.equal(marketplaceUpgradeVersions({ install_state: "new_install" }), null);
  assert.equal(marketplaceUpgradeVersions({ install_state: "upgrade", existing: {} }), null);
});
