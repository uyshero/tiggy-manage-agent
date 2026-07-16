import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { scaffoldWorkbenchPlugin } from "../../scripts/create-workbench-plugin.mjs";
import { validateWorkbenchPlugins } from "../../scripts/validate-workbench-plugins.mjs";

test("plugin scaffold creates an isolated auto-discovered package and refuses overwrite", async (t) => {
  const rootDir = await mkdtemp(path.join(os.tmpdir(), "tma-plugin-scaffold-"));
  t.after(() => rm(rootDir, { recursive: true, force: true }));
  await import("node:fs/promises").then(({ mkdir }) => mkdir(path.join(rootDir, "src", "plugins"), { recursive: true }));

  const result = await scaffoldWorkbenchPlugin({
    rootDir,
    id: "com.example.due-diligence",
    name: "企业尽调 <安全>"
  });
  assert.equal(result.directoryName, "dueDiligence");
  assert.deepEqual(result.files, [
    "DueDiligencePage.jsx",
    "package.jsx",
    "plugin.js",
    "plugin.json",
    "plugin.test.js",
    "styles.css"
  ]);
  const manifest = JSON.parse(await readFile(path.join(result.pluginDir, "plugin.json"), "utf8"));
  assert.equal(manifest.id, "com.example.due-diligence");
  assert.equal(manifest.name, "企业尽调 <安全>");
  assert.equal(manifest.contributes.routes[0].component, "DueDiligencePage");
  const pageSource = await readFile(path.join(result.pluginDir, "DueDiligencePage.jsx"), "utf8");
  assert.match(pageSource, /<h1>\{"企业尽调 <安全>"\}<\/h1>/);
  assert.deepEqual(await validateWorkbenchPlugins({ rootDir }), [{
    id: "com.example.due-diligence",
    directory: "dueDiligence",
    package: "package.jsx"
  }]);
  await assert.rejects(() => scaffoldWorkbenchPlugin({
    rootDir,
    id: "com.example.due-diligence",
    name: "重复插件"
  }), /already exists/);
});

test("plugin validator rejects source imports that escape the package", async (t) => {
  const rootDir = await mkdtemp(path.join(os.tmpdir(), "tma-plugin-validation-"));
  t.after(() => rm(rootDir, { recursive: true, force: true }));
  await import("node:fs/promises").then(({ mkdir }) => mkdir(path.join(rootDir, "src", "plugins"), { recursive: true }));
  const result = await scaffoldWorkbenchPlugin({ rootDir, id: "com.example.escape-test", name: "越界测试" });
  const pluginPath = path.join(result.pluginDir, "plugin.js");
  await import("node:fs/promises").then(({ writeFile }) => writeFile(
    pluginPath,
    'import "../../App.jsx";\nexport const plugin = { id: "com.example.escape-test", activate() {} };\n',
    "utf8"
  ));
  await assert.rejects(() => validateWorkbenchPlugins({ rootDir }), /imports outside its plugin directory/);
});
