import { readFile, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const packageRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const output = resolve(packageRoot, `.schema.check.${process.pid}.ts`);
const expected = resolve(packageRoot, "src/internal/generated/schema.ts");

try {
  await run(resolve(packageRoot, "node_modules/.bin/openapi-typescript"), [
    resolve(packageRoot, "../../api/v2/openapi.yaml"),
    "-o",
    output,
  ]);
  const [actualContent, expectedContent] = await Promise.all([
    readFile(output, "utf8"),
    readFile(expected, "utf8"),
  ]);
  if (actualContent !== expectedContent) {
    throw new Error("generated TypeScript OpenAPI types are stale; run `make generate-typescript-sdk`");
  }
} finally {
  await rm(output, { force: true });
}

function run(command, args) {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(command, args, { cwd: packageRoot, stdio: "inherit" });
    child.once("error", reject);
    child.once("exit", (code) => {
      if (code === 0) resolvePromise();
      else reject(new Error(`openapi-typescript exited with status ${code}`));
    });
  });
}
