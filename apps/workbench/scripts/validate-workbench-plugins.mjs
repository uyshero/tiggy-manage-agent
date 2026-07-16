import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";

import { definePluginManifest } from "../src/workbench/pluginManifest.js";

async function sourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const resolved = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await sourceFiles(resolved));
    else if (/\.(?:js|jsx|mjs)$/.test(entry.name)) files.push(resolved);
  }
  return files;
}

function moduleSpecifiers(source) {
  const values = [];
  const patterns = [
    /\b(?:import|export)\s+(?:[^'";]*?\s+from\s*)?["']([^"']+)["']/g,
    /\bimport\s*\(\s*["']([^"']+)["']\s*\)/g
  ];
  for (const pattern of patterns) {
    let match;
    while ((match = pattern.exec(source)) !== null) values.push(match[1]);
  }
  return values;
}

export async function validateWorkbenchPlugins(options = {}) {
  const rootDir = path.resolve(options.rootDir || process.cwd());
  const pluginsDir = path.join(rootDir, "src", "plugins");
  const entries = await readdir(pluginsDir, { withFileTypes: true });
  const records = [];
  const ids = new Set();

  for (const entry of entries.filter((value) => value.isDirectory()).sort((left, right) => left.name.localeCompare(right.name))) {
    const pluginDir = path.join(pluginsDir, entry.name);
    const manifestPath = path.join(pluginDir, "plugin.json");
    let rawManifest;
    try {
      rawManifest = await readFile(manifestPath, "utf8");
    } catch (error) {
      if (error?.code === "ENOENT") continue;
      throw error;
    }
    const manifest = definePluginManifest(JSON.parse(rawManifest));
    if (ids.has(manifest.id)) throw new Error(`duplicate plugin id ${manifest.id}`);
    ids.add(manifest.id);

    const filenames = await readdir(pluginDir);
    const packages = filenames.filter((filename) => ["package.js", "package.jsx"].includes(filename));
    if (packages.length !== 1) throw new Error(`${manifest.id} must contain exactly one package.js or package.jsx`);
    const entryPath = path.resolve(pluginDir, manifest.entry);
    if (!entryPath.startsWith(`${pluginDir}${path.sep}`)) throw new Error(`${manifest.id} entry escapes its plugin directory`);
    try {
      await readFile(entryPath, "utf8");
    } catch (error) {
      if (error?.code === "ENOENT") throw new Error(`${manifest.id} entry does not exist: ${manifest.entry}`);
      throw error;
    }

    const files = await sourceFiles(pluginDir);
    for (const filename of files) {
      const source = await readFile(filename, "utf8");
      for (const specifier of moduleSpecifiers(source)) {
        if (specifier === ".." || specifier.startsWith("../") || path.isAbsolute(specifier)) {
          throw new Error(`${manifest.id} imports outside its plugin directory in ${path.relative(pluginDir, filename)}: ${specifier}`);
        }
      }
    }
    records.push(Object.freeze({ id: manifest.id, directory: entry.name, package: packages[0] }));
  }
  return Object.freeze(records);
}

async function main() {
  const records = await validateWorkbenchPlugins();
  records.forEach((record) => process.stdout.write(`validated ${record.id} (${record.directory}/${record.package})\n`));
  process.stdout.write(`Validated ${records.length} Workbench plugins.\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    process.stderr.write(`${error.message || String(error)}\n`);
    process.exitCode = 1;
  });
}
