import assert from "node:assert/strict";
import test from "node:test";

import {
  SESSION_ARTIFACT_SOURCE_PREFIX,
  artifactToResourceRef,
  createSessionArtifactProvider,
  htmlPreviewDocument,
  isHTMLResource,
  isMarkdownResource,
  previewKindForResource
} from "./sessionArtifactAdapter.js";

function response({ contentType = "", contentLength = "", text = "", blob = {} } = {}) {
  const headers = new Map([
    ["content-type", contentType],
    ["content-length", String(contentLength)]
  ]);
  return {
    headers: { get: (name) => headers.get(name.toLowerCase()) || null },
    text: async () => text,
    blob: async () => blob
  };
}

function providerOptions(downloadArtifact, overrides = {}) {
  return {
    downloadArtifact,
    artifactDownloadPath: (sessionID, artifactID) => `/v2/sessions/${sessionID}/artifacts/${artifactID}`,
    openURL: () => undefined,
    ...overrides
  };
}

test("artifact adapter creates an allowlisted ResourceRef", () => {
  const resource = artifactToResourceRef({
    id: "artifact_01",
    name: "研究报告.md",
    artifact_type: "file",
    turn_id: "turn_03",
    object_ref_id: "object_01",
    metadata: {
      content_type: "text/markdown",
      size_bytes: 2048,
      path: "reports/研究报告.md",
      secret: "must-not-cross-the-boundary",
      nested: { internal: true }
    }
  }, { sessionID: "session_01" });

  assert.equal(resource.type, "file");
  assert.equal(resource.source, `${SESSION_ARTIFACT_SOURCE_PREFIX}session_01`);
  assert.equal(resource.mimeType, "text/markdown");
  assert.equal(resource.previewable, true);
  assert.deepEqual(resource.metadata, {
    size_bytes: 2048,
    path: "reports/研究报告.md",
    object_ref_id: "object_01",
    turn_id: "turn_03"
  });
});

test("artifact adapter identifies image, text, markdown, and download resources", () => {
  assert.equal(previewKindForResource({ title: "chart.PNG" }), "image");
  assert.equal(previewKindForResource({ title: "data.bin", mimeType: "application/json" }), "text");
  assert.equal(previewKindForResource({ title: "archive.zip" }), "download");
  assert.equal(isMarkdownResource({ title: "README.md" }), true);
  assert.equal(isHTMLResource({ title: "report.HTML" }), true);
  assert.equal(previewKindForResource({ title: "report.htm" }), "text");
  assert.equal(isHTMLResource({ title: "report", mimeType: "text/html; charset=utf-8" }), true);
});

test("HTML preview document injects a restrictive CSP", () => {
  const document = htmlPreviewDocument("<html><head><title>Report</title></head><body><script>alert(1)</script></body></html>");
  assert.match(document, /Content-Security-Policy/);
  assert.match(document, /default-src 'none'/);
  assert.ok(document.indexOf("Content-Security-Policy") < document.indexOf("<title>"));

  const fragment = htmlPreviewDocument("<h1>Report</h1>");
  assert.match(fragment, /^<!doctype html>/);
  assert.match(fragment, /<body><h1>Report<\/h1><\/body>/);
});

test("Session Artifact provider formats JSON and truncates long text", async () => {
  const provider = createSessionArtifactProvider(providerOptions(
    async () => response({ contentType: "application/json", text: '{"answer":42}' }),
    { maxTextCharacters: 8 }
  ));
  const resource = artifactToResourceRef({ id: "artifact_01", name: "data.json" }, { sessionID: "session_01" });
  const preview = await provider.preview(resource);

  assert.equal(preview.kind, "text");
  assert.match(preview.text, /^\{\n  "ans/);
  assert.match(preview.text, /\[预览已截断\]$/);
  assert.equal(preview.downloadUrl, "/v2/sessions/session_01/artifacts/artifact_01");
});

test("Session Artifact provider downloads previews by resource identity and forwards cancellation", async () => {
  const calls = [];
  const provider = createSessionArtifactProvider(providerOptions(
    async (sessionID, artifactID, options) => {
      calls.push({ sessionID, artifactID, options });
      return response({ contentType: "text/plain", text: "preview" });
    }
  ));
  const resource = artifactToResourceRef({ id: "artifact/01", name: "notes.txt" }, { sessionID: "session/01" });
  const controller = new AbortController();
  await provider.preview(resource, { signal: controller.signal });

  assert.equal(calls[0].sessionID, "session/01");
  assert.equal(calls[0].artifactID, "artifact/01");
  assert.equal(calls[0].options.signal, controller.signal);
});

test("Session Artifact provider returns download fallback for large and unsupported content", async () => {
  const largeProvider = createSessionArtifactProvider(providerOptions(
    async () => response({ contentType: "text/plain", contentLength: 20 }),
    { maxTextBytes: 10 }
  ));
  const textResource = artifactToResourceRef({ id: "large", name: "large.txt" }, { sessionID: "session_01" });
  const largePreview = await largeProvider.preview(textResource);
  assert.equal(largePreview.kind, "download");
  assert.match(largePreview.message, /过大/);

  const binaryProvider = createSessionArtifactProvider(providerOptions(
    async () => response({ contentType: "application/zip" })
  ));
  const binaryResource = artifactToResourceRef({ id: "archive", name: "archive.zip" }, { sessionID: "session_01" });
  const binaryPreview = await binaryProvider.preview(binaryResource);
  assert.equal(binaryPreview.kind, "download");
  assert.match(binaryPreview.message, /不支持/);
});

test("Session Artifact provider owns image Object URL cleanup", async () => {
  const revoked = [];
  const provider = createSessionArtifactProvider(providerOptions(
    async () => response({ contentType: "image/png", blob: { type: "image/png" } }),
    {
      createObjectURL: () => "blob:preview-01",
      revokeObjectURL: (url) => revoked.push(url)
    }
  ));
  const resource = artifactToResourceRef({ id: "image", name: "image.png" }, { sessionID: "session_01" });
  const preview = await provider.preview(resource);
  assert.equal(preview.objectUrl, "blob:preview-01");
  preview.dispose();
  preview.dispose();
  assert.deepEqual(revoked, ["blob:preview-01"]);
});

test("Session Artifact provider lists and opens resources through stable references", async () => {
  const opened = [];
  const provider = createSessionArtifactProvider(providerOptions(
    async () => response(),
    { openURL: (url) => opened.push(url) }
  ));
  const listed = provider.listRelated({
    sessionID: "session_02",
    artifacts: [{ id: "artifact_02", name: "notes.txt", artifact_type: "file" }]
  });
  assert.equal(listed[0].source, `${SESSION_ARTIFACT_SOURCE_PREFIX}session_02`);
  const url = provider.open(listed[0]);
  assert.equal(url, "/v2/sessions/session_02/artifacts/artifact_02");
  assert.deepEqual(opened, [url]);
});
