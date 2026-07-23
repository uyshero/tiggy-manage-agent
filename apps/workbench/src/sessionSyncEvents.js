const sessionSyncEventTypes = new Set([
  "agent.message",
  "runtime.tool_intervention_required",
  "runtime.tool_intervention_approved",
  "runtime.tool_intervention_rejected",
  "runtime.human_input_required",
  "runtime.human_input_submitted",
  "runtime.human_input_skipped",
  "runtime.human_input_canceled",
  "runtime.plan_approval_required",
  "runtime.plan_approval_approved",
  "runtime.plan_approval_rejected",
  "runtime.turn_completing",
  "runtime.completion_validated",
  "runtime.completion_blocked",
  "runtime.completion_validation_failed",
  "runtime.failed",
  "runtime.completed",
  "session.status_idle",
  "session.status_failed",
  "session.status_terminated",
  "session.config_updated",
  // Agent Core persists these durable event names instead of the legacy
  // runtime.* intervention events.
  "intervention.required",
  "intervention.resolved"
]);

export function shouldSyncSessionForEvent(event) {
  const type = typeof event === "string" ? event : event?.type;
  return sessionSyncEventTypes.has(String(type || ""));
}
