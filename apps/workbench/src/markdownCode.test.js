import assert from "node:assert/strict";
import test from "node:test";

import { copyTextToClipboard, markdownCodeLanguage, markdownCodeText } from "./markdownCode.js";

test("normalizes rendered code while preserving indentation and internal newlines", () => {
  assert.equal(markdownCodeText(["function run() {\n", "  return true;\n", "}\n"]), "function run() {\n  return true;\n}");
  assert.equal(markdownCodeText("value"), "value");
});

test("reads fenced code language classes without changing their label", () => {
  assert.equal(markdownCodeLanguage("language-typescript extra"), "typescript");
  assert.equal(markdownCodeLanguage("plain"), "");
});

test("copies through the modern clipboard API", async () => {
  const copied = [];
  await copyTextToClipboard("const ready = true;", {
    navigator: { clipboard: { writeText: async (value) => copied.push(value) } }
  });
  assert.deepEqual(copied, ["const ready = true;"]);
});

test("falls back to the document copy command when clipboard permission is denied", async () => {
  let textarea;
  let removed = false;
  const document = {
    body: { appendChild: (element) => { textarea = element; } },
    createElement: () => ({
      style: {},
      setAttribute() {},
      select() {},
      remove: () => { removed = true; }
    }),
    execCommand: (command) => command === "copy"
  };

  await copyTextToClipboard("fallback", {
    navigator: { clipboard: { writeText: async () => { throw new Error("denied"); } } },
    document
  });
  assert.equal(textarea.value, "fallback");
  assert.equal(removed, true);
});
