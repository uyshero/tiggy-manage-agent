const taskPlanEventTypes = new Set([
  "runtime.task_plan_created",
  "runtime.task_items_updated",
  "runtime.task_plan_completed",
  "runtime.task_plan_canceled",
  "runtime.task_plan_superseded"
]);

export function latestTaskPlan(events, snapshot = null) {
  let current = normalizePlan(snapshot);
  const snapshotUpdatedAt = dateMillis(current?.updated_at);
  const ordered = [...(events || [])].sort((left, right) => Number(left?.seq || 0) - Number(right?.seq || 0));
  for (const event of ordered) {
    if (!taskPlanEventTypes.has(event?.type)) continue;
    const eventCreatedAt = dateMillis(event?.created_at);
    if (snapshotUpdatedAt !== null && eventCreatedAt !== null && eventCreatedAt <= snapshotUpdatedAt) continue;
    const payload = objectValue(event.payload);
    const plan = normalizePlan(payload.plan);
    if (plan) {
      current = plan;
      continue;
    }
    const planID = String(payload.plan_id || "").trim();
    if (current && planID === current.id) {
      current = { ...current, status: String(payload.status || current.status) };
    }
  }
  if (!current || ["canceled", "superseded"].includes(current.status)) return null;
  return current;
}

function dateMillis(value) {
  const parsed = Date.parse(String(value || ""));
  return Number.isFinite(parsed) ? parsed : null;
}

function normalizePlan(value) {
  const plan = objectValue(value);
  const id = String(plan.id || "").trim();
  if (!id || !Array.isArray(plan.items)) return null;
  return {
    ...plan,
    id,
    goal: String(plan.goal || "").trim(),
    handling_mode: String(plan.handling_mode || "tracked").trim(),
    status: String(plan.status || "active").trim(),
    items: plan.items.map((item, index) => ({
      ...objectValue(item),
      id: String(item?.id || `item-${index + 1}`).trim(),
      description: String(item?.description || "").trim(),
      status: String(item?.status || "pending").trim(),
      evidence: String(item?.evidence || "").trim()
    }))
  };
}

function objectValue(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}
