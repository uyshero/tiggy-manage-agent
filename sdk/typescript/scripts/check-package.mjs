import { spawnSync } from "node:child_process";

const result = spawnSync("npm", ["pack", "--dry-run", "--json", "--ignore-scripts"], {
  cwd: new URL("..", import.meta.url),
  encoding: "utf8",
});
if (result.status !== 0) {
  process.stderr.write(result.stderr);
  process.exit(result.status ?? 1);
}

const [manifest] = JSON.parse(result.stdout);
if (!manifest || !Array.isArray(manifest.files)) throw new Error("npm pack did not return a file manifest");
const files = new Set(manifest.files.map((file) => file.path));
const required = [
  "README.md",
  "package.json",
  "dist/index.js",
  "dist/index.d.ts",
  "dist/low-level.js",
  "dist/low-level.d.ts",
  "dist/internal/generated/schema.d.ts",
  "dist/services/administration.js",
  "dist/services/agents.js",
  "dist/services/artifacts.js",
  "dist/services/auth.js",
  "dist/services/environments.js",
  "dist/services/interventions.js",
  "dist/services/llm.js",
  "dist/services/marketplace.js",
  "dist/services/mcp.js",
  "dist/services/object-refs.js",
  "dist/services/orchestration.js",
  "dist/services/runs.js",
  "dist/services/sessions.js",
  "dist/services/skills.js",
  "dist/services/traces.js",
  "dist/services/workers.js",
];
for (const path of required) {
  if (!files.has(path)) throw new Error(`npm package is missing ${path}`);
}
for (const path of files) {
  if (path === "README.md" || path === "package.json" || path.startsWith("dist/")) continue;
  throw new Error(`npm package contains unexpected file ${path}`);
}
if (manifest.unpackedSize > 2_000_000) {
  throw new Error(`npm package unpacked size ${manifest.unpackedSize} exceeds the 2 MB guard`);
}

process.stdout.write(`verified ${manifest.files.length} package files (${manifest.unpackedSize} unpacked bytes)\n`);
