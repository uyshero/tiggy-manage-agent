import assert from "node:assert/strict";
import test from "node:test";

import {
  SkillInputsValidationError,
  buildSkillInputs,
  initialSkillInputValues,
  inputSchemaFromVersion,
  schemaFields,
  skillBindingState,
  skillBindingsFromConfig,
  skillBindingsMatch,
  skillConfigSyncState
} from "./skillInputs.js";

const schema = {
  type: "object",
  additionalProperties: false,
  properties: {
    style: { title: "审查风格", type: "string", enum: ["strict", "balanced"], default: "balanced" },
    findings: { type: "integer", minimum: 1, maximum: 20 },
    include_tests: { type: "boolean" },
    notes: { type: "string", minLength: 2, "x-tma-control": "textarea" },
    filters: { type: "array" }
  },
  required: ["style", "findings"]
};

test("reads inputs_schema from object and JSON manifests", () => {
  assert.equal(inputSchemaFromVersion({ manifest: { inputs_schema: schema } }), schema);
  assert.deepEqual(inputSchemaFromVersion({ manifest: JSON.stringify({ inputs_schema: schema }) }), schema);
  assert.equal(inputSchemaFromVersion({ manifest: "invalid" }), null);
});

test("maps root properties to typed controls and resolves local refs", () => {
  const fields = schemaFields({
    type: "object",
    additionalProperties: false,
    $defs: { count: { type: "integer", title: "数量", minimum: 1 } },
    properties: {
      count: { $ref: "#/$defs/count", maximum: 10 },
      enabled: { type: "boolean" },
      mode: { type: "string", enum: ["fast", "safe"] },
      config: { type: "object", additionalProperties: false },
      prompt: { type: "string", "x-tma-control": "textarea" }
    }
  });
  assert.deepEqual(fields.map((field) => field.control), ["number", "boolean", "enum", "json", "textarea"]);
  assert.equal(fields[0].title, "数量");
  assert.equal(fields[0].minimum, 1);
  assert.equal(fields[0].maximum, 10);
});

test("applies defaults and omits untouched optional fields", () => {
  assert.deepEqual(initialSkillInputValues(schema), { style: '"balanced"' });
  const inputs = buildSkillInputs(schema, { style: '"strict"', findings: "5" });
  assert.deepEqual({ ...inputs }, { style: "strict", findings: 5 });
  assert.equal(Object.hasOwn(inputs, "include_tests"), false);
});

test("parses complete bindings and compares inputs independent of key order", () => {
  const bindings = skillBindingsFromConfig(JSON.stringify({
    enabled: [{
      skill: "code-review",
      version: 3,
      mode: "summary",
      priority: 200,
      inputs: { style: "strict", limits: { files: 10, tests: true } }
    }]
  }));
  assert.deepEqual(bindings, [{
    skill: "code-review",
    version: 3,
    mode: "summary",
    priority: 200,
    inputs: { style: "strict", limits: { files: 10, tests: true } }
  }]);
  assert.equal(skillBindingsMatch(bindings[0], {
    ...bindings[0],
    inputs: { limits: { tests: true, files: 10 }, style: "strict" }
  }), true);
  assert.equal(skillBindingsMatch(bindings[0], { ...bindings[0], version: 2 }), false);
});

test("defaults omitted binding mode to summary for on-demand full instructions", () => {
  const bindings = skillBindingsFromConfig({ enabled: [{ skill: "code-review", version: 3 }] });
  assert.equal(bindings[0].mode, "summary");
});

test("initial values prefer the current binding over schema defaults", () => {
  assert.deepEqual(initialSkillInputValues(schema, {
    style: "strict",
    findings: 8,
    include_tests: false,
    filters: ["src/**"]
  }), {
    style: '"strict"',
    findings: "8",
    include_tests: false,
    filters: '[\n  "src/**"\n]'
  });
});

test("reports pending apply and session-still-enabled binding states", () => {
  const binding = { skill: "code-review", version: 2, mode: "full", priority: 100, inputs: {} };
  assert.deepEqual(skillBindingState(binding, binding), {
    synced: true,
    pendingApply: false,
    sessionStillEnabled: false
  });
  assert.deepEqual(skillBindingState(null, binding), {
    synced: false,
    pendingApply: false,
    sessionStillEnabled: true
  });
  assert.deepEqual(skillBindingState({ ...binding, version: 3 }, binding), {
    synced: false,
    pendingApply: true,
    sessionStillEnabled: false
  });
});

test("targets the exact latest Agent config only when a Session is behind", () => {
  assert.deepEqual(skillConfigSyncState(7, 5, true), {
    agentVersion: 7,
    sessionVersion: 5,
    needsApply: true,
    targetVersion: 7
  });
  assert.equal(skillConfigSyncState(7, 7, true).needsApply, false);
  assert.equal(skillConfigSyncState(7, 0, false).targetVersion, 0);
});

test("preserves explicit false and parses nested JSON", () => {
  const inputs = buildSkillInputs(schema, {
    style: '"balanced"',
    findings: "2",
    include_tests: false,
    filters: '["test", "docs"]'
  });
  assert.deepEqual({ ...inputs }, {
    style: "balanced",
    findings: 2,
    include_tests: false,
    filters: ["test", "docs"]
  });
});

test("reports required, numeric, string, and JSON errors by field", () => {
  assert.throws(
    () => buildSkillInputs(schema, { findings: "21", notes: "x", filters: "{" }),
    (error) => error instanceof SkillInputsValidationError
      && error.fields.style === "此参数为必填项"
      && error.fields.findings === "不能大于 20"
      && error.fields.notes === "至少需要 2 个字符"
      && error.fields.filters === "请输入有效 JSON"
  );
  assert.throws(
    () => buildSkillInputs(schema, { style: '"strict"', findings: "1.5" }),
    (error) => error.fields.findings === "必须是整数"
  );
});
