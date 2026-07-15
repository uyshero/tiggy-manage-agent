import assert from "node:assert/strict";
import test from "node:test";

import { buildStaticPluginCatalog, loadPluginCatalog } from "./index.js";

function packageModule(id, componentName = "Page") {
  return {
    default: {
      manifest: {
        id,
        contributes: { routes: [{ component: componentName }] }
      },
      plugin: { id, activate() {} },
      components: { [componentName]: () => null },
      enablement: { defaultEnabled: true }
    }
  };
}

test("static plugin catalog discovers, validates, and orders build-time packages", async () => {
  const calls = [];
  const catalog = buildStaticPluginCatalog({
    "./zeta/package.js": packageModule("com.example.zeta"),
    "./alpha/package.jsx": packageModule("com.example.alpha")
  });
  const result = await loadPluginCatalog({
    load(catalog, options) {
      calls.push({ catalog, options });
      return Promise.resolve([]);
    }
  }, catalog, { activate: false });
  assert.deepEqual(result, []);
  assert.equal(calls[0].catalog, catalog);
  assert.deepEqual(
    calls[0].catalog.map((item) => item.manifest.id),
    ["com.example.alpha", "com.example.zeta"]
  );
  assert.deepEqual(calls[0].options, { activate: false });
  assert.equal(Object.isFrozen(catalog), true);
});

test("static plugin catalog rejects duplicate ids and missing route exports", () => {
  assert.throws(() => buildStaticPluginCatalog({
    "./one/package.js": packageModule("com.example.same"),
    "./two/package.js": packageModule("com.example.same")
  }), /duplicated/);
  assert.throws(() => buildStaticPluginCatalog({
    "./broken/package.js": {
      default: {
        manifest: { id: "com.example.broken", contributes: { routes: [{ component: "MissingPage" }] } },
        plugin: { id: "com.example.broken", activate() {} },
        components: {}
      }
    }
  }), /MissingPage/);
});
