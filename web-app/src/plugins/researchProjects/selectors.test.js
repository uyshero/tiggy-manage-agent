import assert from "node:assert/strict";
import test from "node:test";

import { sessionSelectionOptions } from "./selectors.js";

test("session selection uses readable unique task labels", () => {
  const options = sessionSelectionOptions([
    { id: "sesn_02", title: "材料路线调研", status: "idle" },
    { id: "sesn_01", title: "" },
    { id: "sesn_02", title: "重复记录" },
    { title: "缺少 ID" },
    null
  ]);

  assert.deepEqual(options, [
    { id: "sesn_02", label: "材料路线调研", description: "sesn_02 · idle", keywords: "材料路线调研 sesn_02 idle" },
    { id: "sesn_01", label: "sesn_01", description: "", keywords: "sesn_01" }
  ]);
  assert.equal(Object.isFrozen(options), true);
  assert.equal(Object.isFrozen(options[0]), true);
});

test("session selection returns an empty list for malformed responses", () => {
  assert.deepEqual(sessionSelectionOptions(null), []);
  assert.deepEqual(sessionSelectionOptions({ sessions: [] }), []);
});
