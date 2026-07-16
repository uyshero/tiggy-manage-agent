import assert from "node:assert/strict";
import test from "node:test";

import {
  RelatedResourceServiceError,
  createRelatedResourceService,
  isPreviewCancelledError
} from "./relatedResourceService.js";

function resource(id = "file_01", source = "test.files:session_01") {
  return { id, type: "file", title: `${id}.txt`, source, metadata: {} };
}

function deferred() {
  let resolve;
  const promise = new Promise((next) => { resolve = next; });
  return { promise, resolve };
}

test("resource service registers providers and normalizes related resources", async () => {
  const service = createRelatedResourceService();
  const unregister = service.registerProvider({
    id: "test.files",
    sourcePrefix: "test.files:",
    listRelated: () => [resource(), resource()],
    preview: async () => ({ kind: "text", text: "hello" }),
    open: () => undefined
  });

  const listed = await service.listRelated({ sessionID: "session_01" });
  assert.equal(listed.length, 1);
  assert.equal(listed[0].title, "file_01.txt");
  assert.equal(Object.isFrozen(listed[0]), true);
  assert.equal(Object.isFrozen(listed), true);
  assert.equal(unregister(), true);
  assert.equal(unregister(), false);
  await assert.rejects(
    service.preview(resource()),
    (error) => error instanceof RelatedResourceServiceError && error.code === "provider_not_found"
  );
});

test("resource service routes preview and open to a matching provider", async () => {
  const service = createRelatedResourceService();
  const opened = [];
  service.registerProvider({
    id: "test.files",
    supports: (item) => item.source.startsWith("test.files:"),
    preview: async (item) => ({ kind: "text", contentType: "text/plain", text: item.title, downloadUrl: "/file" }),
    open: (item, context) => opened.push([item.id, context.target])
  });

  const preview = await service.preview(resource(), { purpose: "inspect" });
  assert.equal(preview.kind, "text");
  assert.equal(preview.text, "file_01.txt");
  await service.open(resource(), { target: "download" });
  assert.deepEqual(opened, [["file_01", "download"]]);
});

test("a newer preview cancels stale work and disposes temporary resources", async () => {
  const service = createRelatedResourceService();
  const first = deferred();
  const second = deferred();
  const disposed = [];
  service.registerProvider({
    id: "test.files",
    sourcePrefix: "test.files:",
    preview: (item) => item.id === "first" ? first.promise : second.promise,
    open: () => undefined
  });

  const firstRequest = service.preview(resource("first"));
  const secondRequest = service.preview(resource("second"));
  second.resolve({ kind: "text", text: "new", dispose: () => disposed.push("second") });
  assert.equal((await secondRequest).text, "new");
  first.resolve({ kind: "image", objectUrl: "blob:old", dispose: () => disposed.push("first") });
  await assert.rejects(firstRequest, isPreviewCancelledError);
  assert.deepEqual(disposed, ["first"]);

  service.releasePreview();
  assert.deepEqual(disposed, ["first", "second"]);
});

test("resource service validates providers and preview descriptors", async () => {
  const service = createRelatedResourceService();
  assert.throws(
    () => service.registerProvider({ id: "missing-support" }),
    (error) => error instanceof RelatedResourceServiceError && error.code === "invalid_provider"
  );
  service.registerProvider({
    id: "test.files",
    sourcePrefix: "test.files:",
    preview: async () => ({ kind: "canvas" }),
    open: () => undefined
  });
  await assert.rejects(
    service.preview(resource()),
    (error) => error instanceof RelatedResourceServiceError && error.code === "invalid_preview"
  );
});

test("unregistering a provider releases its active preview", async () => {
  const service = createRelatedResourceService();
  let disposeCount = 0;
  const unregister = service.registerProvider({
    id: "test.files",
    sourcePrefix: "test.files:",
    preview: async () => ({ kind: "image", objectUrl: "blob:active", dispose: () => { disposeCount += 1; } }),
    open: () => undefined
  });

  await service.preview(resource());
  assert.equal(unregister(), true);
  assert.equal(disposeCount, 1);
});
