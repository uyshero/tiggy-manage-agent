import assert from "node:assert/strict";
import test from "node:test";
import { buildHumanInputResponse, canSubmitHumanInput, fieldHasValue } from "./interactionForms.js";

const choices = [
  { id: "prod", label: "Production" },
  { id: "staging", label: "Staging" }
];

test("builds multiselect answers in declared choice order", () => {
  assert.deepEqual(
    buildHumanInputResponse("multiselect", choices, [], { staging: true, prod: true, ignored: true }),
    { mode: "multiselect", answers: ["prod", "staging"] }
  );
});

test("normalizes form multiselect fields into arrays", () => {
  const fields = [
    { id: "owner", type: "text", required: true },
    { id: "env", type: "select", required: true, choices },
    { id: "checks", type: "multiselect", choices: [{ id: "logs" }, { id: "metrics" }] }
  ];
  const response = buildHumanInputResponse("form", [], fields, {
    owner: "Ada",
    env: "prod",
    checks: { metrics: true, logs: true }
  });
  assert.deepEqual(response, {
    mode: "form",
    fields: {
      owner: "Ada",
      env: "prod",
      checks: ["logs", "metrics"]
    }
  });
});

test("requires at least one selected option for required multiselect fields", () => {
  const field = { id: "checks", type: "multiselect", required: true, choices: [{ id: "logs" }] };
  assert.equal(fieldHasValue(field, { checks: { logs: false } }), false);
  assert.equal(canSubmitHumanInput("form", [], [field], { checks: { logs: false } }), false);
  assert.equal(canSubmitHumanInput("form", [], [field], { checks: { logs: true } }), true);
});
