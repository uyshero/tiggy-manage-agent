import assert from "node:assert/strict";
import test from "node:test";

import {
  NotificationServiceError,
  createNotificationService
} from "./notificationService.js";

test("notification service applies level defaults and emits immutable snapshots", () => {
  const service = createNotificationService({ now: () => 100 });
  const snapshots = [];
  service.subscribe((items) => snapshots.push(items));

  const id = service.show({ level: "success", title: "任务已删除", message: "测试任务" });
  const [notification] = service.getSnapshot();

  assert.equal(id, "notification_1");
  assert.equal(notification.durationMs, 4000);
  assert.equal(notification.createdAt, 100);
  assert.equal(Object.isFrozen(notification), true);
  assert.equal(Object.isFrozen(service.getSnapshot()), true);
  assert.equal(snapshots.length, 2);
});

test("notification service keeps errors until explicitly dismissed", () => {
  const service = createNotificationService();
  const id = service.show({ level: "error", title: "删除失败" });
  assert.equal(service.getSnapshot()[0].durationMs, 0);
  assert.equal(service.dismiss(id), true);
  assert.equal(service.getSnapshot().length, 0);
  assert.equal(service.dismiss(id), false);
});

test("notification service replaces matching dedupe keys", () => {
  let now = 100;
  const service = createNotificationService({ now: () => now });
  const firstID = service.show({ title: "正在处理", dedupeKey: "task.delete" });
  now = 200;
  const secondID = service.show({ level: "success", title: "处理完成", dedupeKey: "task.delete" });

  assert.equal(firstID, secondID);
  assert.equal(service.getSnapshot().length, 1);
  assert.equal(service.getSnapshot()[0].title, "处理完成");
  assert.equal(service.getSnapshot()[0].createdAt, 200);
});

test("notification service validates levels, titles, and durations", () => {
  const service = createNotificationService();
  assert.throws(
    () => service.show({ title: "无效", level: "critical" }),
    (error) => error instanceof NotificationServiceError && error.field === "notification.level"
  );
  assert.throws(
    () => service.show({ title: "" }),
    (error) => error instanceof NotificationServiceError && error.field === "notification.title"
  );
  assert.throws(
    () => service.show({ title: "无效", durationMs: -1 }),
    (error) => error instanceof NotificationServiceError && error.field === "notification.durationMs"
  );
});

test("notification service clears all current items", () => {
  const service = createNotificationService();
  service.show({ title: "第一个" });
  service.show({ title: "第二个" });
  assert.equal(service.clear(), true);
  assert.deepEqual(service.getSnapshot(), []);
  assert.equal(service.clear(), false);
});
