import assert from "node:assert/strict";
import test from "node:test";
import { latestTaskPlan } from "./taskPlanEvents.js";

function plan(id, status = "active") {
  return {
    id,
    goal: "Deliver the workflow",
    handling_mode: "planned",
    status,
    items: [
      { id: `${id}-1`, description: "Inspect", status: "completed", evidence: "tests passed" },
      { id: `${id}-2`, description: "Implement", status: "in_progress" },
      { id: `${id}-3`, description: "Verify", status: "pending" }
    ]
  };
}

test("rebuilds the latest task plan from task events", () => {
  const result = latestTaskPlan([
    { seq: 2, type: "runtime.task_items_updated", payload: { plan: plan("plan-1") } },
    { seq: 1, type: "runtime.task_plan_created", payload: { plan: plan("plan-1") } }
  ]);
  assert.equal(result.id, "plan-1");
  assert.equal(result.items[1].status, "in_progress");
});

test("uses a replacement plan after the previous plan is superseded", () => {
  const result = latestTaskPlan([
    { seq: 1, type: "runtime.task_plan_created", payload: { plan: plan("plan-1") } },
    { seq: 2, type: "runtime.task_plan_superseded", payload: { plan_id: "plan-1", status: "superseded" } },
    { seq: 3, type: "runtime.task_plan_created", payload: { plan: plan("plan-2") } }
  ]);
  assert.equal(result.id, "plan-2");
});

test("keeps completed plans visible but hides canceled plans", () => {
  assert.equal(latestTaskPlan([{ seq: 1, type: "runtime.task_plan_completed", payload: { plan: plan("plan-1", "completed") } }]).status, "completed");
  assert.equal(latestTaskPlan([{ seq: 1, type: "runtime.task_plan_canceled", payload: { plan: plan("plan-1", "canceled") } }]), null);
});

test("uses a Plan snapshot when task event history is unavailable", () => {
  const snapshot = { ...plan("plan-snapshot"), updated_at: "2026-07-16T08:00:00Z" };
  assert.equal(latestTaskPlan([], snapshot).id, "plan-snapshot");
});

test("applies only task events newer than the Plan snapshot", () => {
  const snapshot = { ...plan("plan-current"), updated_at: "2026-07-16T08:00:00Z" };
  const result = latestTaskPlan([
    { seq: 10, created_at: "2026-07-16T07:59:00Z", type: "runtime.task_plan_created", payload: { plan: plan("plan-stale") } },
    { seq: 11, created_at: "2026-07-16T08:01:00Z", type: "runtime.task_plan_completed", payload: { plan: plan("plan-current", "completed") } }
  ], snapshot);
  assert.equal(result.id, "plan-current");
  assert.equal(result.status, "completed");
});
