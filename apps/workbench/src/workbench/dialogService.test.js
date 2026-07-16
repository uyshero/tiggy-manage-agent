import assert from "node:assert/strict";
import test from "node:test";

import { DialogServiceError, createDialogService } from "./dialogService.js";

test("dialog service queues confirmations and resolves them in order", async () => {
  const service = createDialogService();
  const states = [];
  const unsubscribe = service.subscribe((request) => states.push(request?.options?.title || "none"));

  const first = service.confirm({ title: "删除任务", tone: "danger" });
  const second = service.confirm({ title: "中断任务", tone: "warning" });

  assert.equal(service.getActive().options.title, "删除任务");
  assert.equal(service.resolve(service.getActive().id, true), true);
  assert.equal(await first, true);
  assert.equal(service.getActive().options.title, "中断任务");
  assert.equal(service.cancel(service.getActive().id), true);
  assert.equal(await second, false);
  assert.equal(service.getActive(), null);
  assert.deepEqual(states, ["none", "删除任务", "删除任务", "中断任务", "none"]);
  unsubscribe();
});

test("dialog service validates confirmation options", () => {
  const service = createDialogService();
  assert.throws(
    () => service.confirm({ title: "删除", tone: "critical" }),
    (error) => error instanceof DialogServiceError && error.code === "invalid_options"
  );
  assert.throws(
    () => service.confirm({ title: "" }),
    (error) => error instanceof DialogServiceError && error.code === "invalid_options"
  );
});

test("dialog service exposes schema forms through the same queue", async () => {
  const service = createDialogService();
  const result = service.form({
    title: "编辑项目",
    schema: { type: "object", properties: { name: { type: "string" } } },
    initialValues: { name: "Harness" }
  });

  assert.equal(service.getActive().kind, "form");
  service.resolve(service.getActive().id, { name: "TMA" });
  assert.deepEqual(await result, { name: "TMA" });
});

test("dialog service exposes searchable normalized choices", async () => {
  const service = createDialogService();
  const result = service.choice({
    title: "选择任务",
    items: [
      { value: "sesn_01", label: "材料调研", description: "sesn_01", keywords: "新能源" },
      { value: "sesn_02", label: "归档任务", disabled: true }
    ]
  });

  assert.equal(service.getActive().kind, "choice");
  assert.equal(service.getActive().options.initialValue, "sesn_01");
  assert.equal(service.getActive().options.searchable, false);
  assert.equal(Object.isFrozen(service.getActive().options.items), true);
  service.resolve(service.getActive().id, "sesn_01");
  assert.equal(await result, "sesn_01");
});

test("dialog service rejects duplicate or entirely disabled choices", () => {
  const service = createDialogService();
  assert.throws(() => service.choice({
    title: "重复",
    items: [{ value: "same", label: "一" }, { value: "same", label: "二" }]
  }), (error) => error instanceof DialogServiceError && error.code === "invalid_options");
  assert.throws(() => service.choice({
    title: "不可选",
    items: [{ value: "disabled", label: "不可选", disabled: true }]
  }), (error) => error instanceof DialogServiceError && error.code === "invalid_options");
});

test("dialog service registers custom renderers and rejects unknown dialogs", async () => {
  const service = createDialogService();
  await assert.rejects(
    service.open("com.example.missing", {}),
    (error) => error instanceof DialogServiceError && error.code === "unknown_dialog"
  );

  const renderer = () => null;
  const unregister = service.register("com.example.project-editor", renderer);
  const result = service.open("com.example.project-editor", { projectID: "project_01" }, { title: "项目" });
  assert.equal(service.getActive().options.renderer, renderer);
  assert.equal(service.getActive().options.input.projectID, "project_01");
  service.cancel(service.getActive().id);
  assert.equal(await result, undefined);
  unregister();
  await assert.rejects(service.open("com.example.project-editor", {}));
});

test("dialog service disposal rejects active and queued requests", async () => {
  const service = createDialogService();
  const active = service.confirm({ title: "第一个" });
  const queued = service.confirm({ title: "第二个" });
  service.dispose("test complete");
  await assert.rejects(active, (error) => error instanceof DialogServiceError && error.code === "disposed");
  await assert.rejects(queued, (error) => error instanceof DialogServiceError && error.code === "disposed");
});
