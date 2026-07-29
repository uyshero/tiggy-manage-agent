import assert from "node:assert/strict";
import test from "node:test";

import { clipboardHasPlainText, clipboardImageFiles } from "./composerClipboard.js";

function imageItem(file) {
  return { kind: "file", type: file.type, getAsFile: () => file };
}

test("prefers native text paste when Office clipboard also contains a PNG", () => {
  const image = { name: "office-preview.png", type: "image/png" };
  const clipboard = {
    types: ["text/plain", "text/html", "image/png"],
    items: [imageItem(image)]
  };

  assert.equal(clipboardHasPlainText(clipboard), true);
  assert.deepEqual(clipboardImageFiles(clipboard), [image]);
});

test("treats a pure screenshot as an image attachment", () => {
  const image = { name: "screenshot.png", type: "image/png" };
  const clipboard = {
    types: ["image/png"],
    items: [imageItem(image)],
    getData: () => ""
  };

  assert.equal(clipboardHasPlainText(clipboard), false);
  assert.deepEqual(clipboardImageFiles(clipboard), [image]);
});

test("falls back to clipboard files when item metadata is unavailable", () => {
  const image = { name: "clipboard.webp", type: "image/webp" };
  const text = { name: "notes.txt", type: "text/plain" };

  assert.deepEqual(clipboardImageFiles({ files: [text, image] }), [image]);
  assert.equal(clipboardHasPlainText({ files: [image] }), false);
});
