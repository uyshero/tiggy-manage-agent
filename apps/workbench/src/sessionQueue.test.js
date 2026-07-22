import assert from "node:assert/strict";
import test from "node:test";

import {
  appendSessionMessageQueue,
  normalizeSessionMessageQueue,
  removeSessionMessageQueueItem,
  sessionMessageQueueItems
} from "./sessionQueue.js";

test("Session message queue normalizes durable items in FIFO order", () => {
  const items = normalizeSessionMessageQueue([
    { id: "queue_2", session_id: "session_1", text: "Second", created_at: "2026-07-22T02:00:00Z" },
    { id: "", session_id: "session_1", text: "Invalid" },
    { id: "queue_1", session_id: "session_1", text: "First", attachments: [{ artifact_id: "artifact_1" }], created_at: "2026-07-22T01:00:00Z" }
  ]);
  assert.deepEqual(items.map((item) => item.id), ["queue_1", "queue_2"]);
  assert.equal(items[0].attachments[0].artifact_id, "artifact_1");
});

test("Session message queue appends, filters, and removes by stable id", () => {
  const queued = appendSessionMessageQueue([], { id: "queue_1", session_id: "session_1", text: "Next", created_at: "2026-07-22T01:00:00Z" });
  const withOtherSession = appendSessionMessageQueue(queued, { id: "queue_2", session_id: "session_2", text: "Other", created_at: "2026-07-22T02:00:00Z" });
  assert.deepEqual(sessionMessageQueueItems(withOtherSession, "session_1").map((item) => item.id), ["queue_1"]);
  assert.deepEqual(removeSessionMessageQueueItem(withOtherSession, "queue_1").map((item) => item.id), ["queue_2"]);
});
