import assert from "node:assert/strict";
import test from "node:test";

import {
  groupMCPRuntimeStates,
  mcpRuntimeFailureLabel,
  mcpRuntimeStateLabel,
  summarizeMCPRuntimeStates
} from "./mcpRuntimeStatus.js";

test("groups valid runtime states without retaining unknown entries", () => {
  const grouped = groupMCPRuntimeStates([
    { server_id: "mcps_1", version: 1, state: "open" },
    { server_id: "mcps_1", version: 3, state: "closed" },
    { server_id: "mcps_2", version: 2, state: "saturated" },
    { server_id: "", version: 1, state: "open" },
    { server_id: "mcps_3", version: 1, state: "future_state" }
  ]);
  assert.deepEqual(Object.keys(grouped).sort(), ["mcps_1", "mcps_2"]);
  assert.deepEqual(grouped.mcps_1.map((state) => state.version), [1, 3]);
});

test("summarizes the most severe version state", () => {
  assert.deepEqual(summarizeMCPRuntimeStates([]), { state: "untracked", label: "未运行", count: 0 });
  assert.deepEqual(summarizeMCPRuntimeStates([
    { state: "closed" },
    { state: "saturated" },
    { state: "half_open" },
    { state: "open" }
  ]), { state: "open", label: "已熔断", count: 4 });
});

test("uses bounded Chinese labels for state and failure classes", () => {
  assert.equal(mcpRuntimeStateLabel("half_open"), "恢复探测");
  assert.equal(mcpRuntimeStateLabel("invalid"), "未运行");
  assert.equal(mcpRuntimeFailureLabel("transport"), "传输失败");
  assert.equal(mcpRuntimeFailureLabel("secret-value"), "");
});
