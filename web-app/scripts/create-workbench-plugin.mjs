import { access, mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";

import { definePluginManifest } from "../src/workbench/pluginManifest.js";

const pluginIDPattern = /^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)+$/;

function requiredText(value, field) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (!normalized) throw new TypeError(`${field} is required`);
  return normalized;
}

function words(value) {
  return value.split(/[^a-z0-9]+/i).filter(Boolean);
}

function camelName(value) {
  const parts = words(value);
  return parts.map((part, index) => (
    index === 0
      ? part.toLowerCase()
      : `${part[0].toUpperCase()}${part.slice(1).toLowerCase()}`
  )).join("");
}

function pascalName(value) {
  return words(value).map((part) => `${part[0].toUpperCase()}${part.slice(1).toLowerCase()}`).join("");
}

async function pathExists(value) {
  try {
    await access(value);
    return true;
  } catch {
    return false;
  }
}

export async function scaffoldWorkbenchPlugin(options = {}) {
  const id = requiredText(options.id, "plugin id");
  const name = requiredText(options.name, "plugin name");
  if (!pluginIDPattern.test(id)) throw new TypeError("plugin id must be a lowercase namespaced identifier");
  const description = typeof options.description === "string" && options.description.trim()
    ? options.description.trim()
    : `${name} Workbench 扩展`;
  const localID = id.split(".").at(-1);
  const directoryName = camelName(localID);
  const componentName = `${pascalName(localID)}Page`;
  if (!directoryName || !componentName) throw new TypeError("plugin id must end with a usable package name");
  const rootDir = path.resolve(options.rootDir || process.cwd());
  const pluginDir = path.join(rootDir, "src", "plugins", directoryName);
  if (await pathExists(pluginDir)) throw new Error(`plugin directory already exists: ${pluginDir}`);

  const route = `/plugins/${id}/${localID}`;
  const manifest = {
    protocol_version: "tma.workbench_plugin.v1",
    id,
    name,
    description,
    version: "0.1.0",
    entry: "./plugin.js",
    surfaces: ["web_desktop", "desktop_shell", "web_tablet", "web_mobile"],
    engines: {
      workbench_api: ">=1.0.0 <2.0.0",
      design_system: ">=1.0.0 <2.0.0"
    },
    permissions: [],
    contributes: {
      navigation: [{ id: localID, group: "workspace", title: name, route, order: 500 }],
      routes: [{ id: localID, path: route, component: componentName, required_permissions: [] }],
      commands: []
    }
  };
  definePluginManifest(manifest);

  const cssClass = `${localID}-plugin-page`;
  const files = {
    "plugin.json": `${JSON.stringify(manifest, null, 2)}\n`,
    "plugin.js": `export const plugin = {\n  id: ${JSON.stringify(id)},\n  activate() {}\n};\n`,
    [`${componentName}.jsx`]: `import React from "react";\n\nimport "./styles.css";\n\nexport function ${componentName}({ context }) {\n  return (\n    <div className=${JSON.stringify(cssClass)}>\n      <header>\n        <span>Workbench Plugin</span>\n        <h1>{${JSON.stringify(name)}}</h1>\n        <p>{context.scope.workspaceId}</p>\n      </header>\n      <section>\n        <strong>尚无内容</strong>\n      </section>\n    </div>\n  );\n}\n`,
    "package.jsx": `import manifest from "./plugin.json" with { type: "json" };\nimport { ${componentName} } from "./${componentName}.jsx";\nimport { plugin } from "./plugin.js";\n\nexport default Object.freeze({\n  manifest,\n  plugin,\n  components: Object.freeze({ ${componentName} }),\n  enablement: Object.freeze({ defaultEnabled: true })\n});\n`,
    "styles.css": `.${cssClass}{width:min(1120px,100%);min-width:0;margin:0 auto;padding:28px;display:grid;gap:20px}\n.${cssClass}>header{min-width:0;padding-bottom:18px;border-bottom:1px solid var(--line)}\n.${cssClass}>header span{color:var(--muted);font-size:10px;font-weight:800;text-transform:uppercase}\n.${cssClass}>header h1{margin:5px 0;font-size:30px}\n.${cssClass}>header p{margin:0;color:var(--muted);font-size:12px;overflow-wrap:anywhere}\n.${cssClass}>section{min-height:280px;display:grid;place-content:center;color:var(--muted)}\n@media(max-width:640px){.${cssClass}{padding:20px 16px}.${cssClass}>header h1{font-size:26px}}\n`,
    "plugin.test.js": `import assert from "node:assert/strict";\nimport test from "node:test";\n\nimport { plugin } from "./plugin.js";\n\ntest(${JSON.stringify(`${id} exposes a stable activation entry`)}, () => {\n  assert.equal(plugin.id, ${JSON.stringify(id)});\n  assert.equal(typeof plugin.activate, "function");\n});\n`
  };

  await mkdir(pluginDir, { recursive: false });
  await Promise.all(Object.entries(files).map(([filename, content]) => (
    writeFile(path.join(pluginDir, filename), content, { encoding: "utf8", flag: "wx" })
  )));
  return Object.freeze({ id, directoryName, pluginDir, files: Object.freeze(Object.keys(files).sort()) });
}

function parseArguments(argv) {
  const options = {};
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (!["--id", "--name", "--description", "--root"].includes(argument)) {
      throw new TypeError(`unknown argument ${argument}`);
    }
    const value = argv[index + 1];
    if (!value || value.startsWith("--")) throw new TypeError(`${argument} requires a value`);
    index += 1;
    if (argument === "--root") options.rootDir = value;
    else options[argument.slice(2)] = value;
  }
  return options;
}

async function main() {
  const result = await scaffoldWorkbenchPlugin(parseArguments(process.argv.slice(2)));
  process.stdout.write(`Created ${result.id} in ${result.pluginDir}\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    process.stderr.write(`${error.message || String(error)}\n`);
    process.exitCode = 1;
  });
}
