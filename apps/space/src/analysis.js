export const defaultCriteria = [
  { id: "task-completion", name: "任务完成度", description: "是否完整实现了要求的结果。" },
  { id: "correctness", name: "正确性", description: "结果是否准确且内部一致。" },
  { id: "tool-discipline", name: "工具使用", description: "工具调用是否高效、合理。" },
  { id: "evidence-quality", name: "证据质量", description: "结论是否得到运行证据支持。" }
];

const criterionTranslations = new Map(defaultCriteria.map((criterion) => [criterion.id, criterion]));

export function localizedCriterion(criterion) {
  return criterionTranslations.get(criterion?.id) || criterion;
}

export function statusLabel(status) {
  const labels = {
    completed: "已完成", success: "成功", ok: "正常", approved: "已批准",
    failed: "失败", error: "错误", rejected: "已拒绝", canceled: "已取消",
	queued: "排队中", running: "运行中", pending: "等待中", waiting_approval: "等待批准", blocked: "已阻塞",
    idle: "空闲", interrupted: "已中断", interrupting: "正在中断", unknown: "未知"
  };
  return labels[String(status || "").toLowerCase()] || status || "未知";
}

export function conclusionLabel(conclusion) {
  return ({ left: "A 更优", right: "B 更优", tie: "持平", inconclusive: "无法判断" })[conclusion] || conclusion || "无法判断";
}

export function createEvaluation() {
  return {
    version: 1,
    conclusion: "inconclusive",
    notes: "",
    criteria: defaultCriteria.map((criterion) => ({ ...criterion, leftScore: 3, rightScore: 3 })),
    savedAt: ""
  };
}

export function evaluationFromRubric(rubric, persisted) {
  const scores = new Map((persisted?.scores || []).map((score) => [score.criterion_id, score]));
  const criteria = Array.isArray(rubric?.criteria) && rubric.criteria.length ? rubric.criteria : defaultCriteria;
  return normalizeEvaluation({
    conclusion: persisted?.conclusion || "inconclusive",
    notes: persisted?.notes || "",
    savedAt: persisted?.created_at || "",
    criteria: criteria.map((criterion) => ({
      ...criterion,
      leftScore: scores.get(criterion.id)?.left_score ?? 3,
      rightScore: scores.get(criterion.id)?.right_score ?? 3
    }))
  });
}

export function normalizeEvaluation(value) {
  const fallback = createEvaluation();
  if (!value || !Array.isArray(value.criteria)) return fallback;
  return {
    version: 1,
    conclusion: ["left", "right", "tie", "inconclusive"].includes(value.conclusion) ? value.conclusion : "inconclusive",
    notes: String(value.notes || ""),
    criteria: value.criteria.slice(0, 6).map((criterion, index) => ({
      id: String(criterion.id || `criterion-${index + 1}`),
      name: String(criterion.name || `Criterion ${index + 1}`),
      description: String(criterion.description || ""),
      leftScore: boundedScore(criterion.leftScore),
      rightScore: boundedScore(criterion.rightScore)
    })),
    savedAt: String(value.savedAt || "")
  };
}

export function evaluationStorageKey(leftSessionID, rightSessionID) {
  return `tma.space.evaluation.v1:${String(leftSessionID)}:${String(rightSessionID)}`;
}

export function averageScore(criteria, side) {
  const key = side === "right" ? "rightScore" : "leftScore";
  if (!Array.isArray(criteria) || criteria.length === 0) return 0;
  return criteria.reduce((sum, criterion) => sum + boundedScore(criterion[key]), 0) / criteria.length;
}

export function summarizeEvidence(evidence = {}) {
  const comparison = evidence.comparison || {};
  const trace = evidence.trace || {};
  const stats = trace.stats || {};
  const usage = comparison.usage?.summary || {};
  const steps = Array.isArray(trace.steps) ? trace.steps : [];
  const events = Array.isArray(evidence.events) ? evidence.events : [];
  const toolSteps = steps.filter((step) => String(step.type || "").includes("tool"));
  const errorSteps = steps.filter((step) => {
    const type = String(step.type || "").toLowerCase();
    const outcome = String(step.outcome || step.span_status || "").toLowerCase();
    return type.includes("failed") || type.includes("error") || ["failed", "error", "rejected"].includes(outcome);
  });
  return {
    session: comparison.session || {},
    provider: comparison.llm_provider || "-",
    model: comparison.llm_model || "-",
    prompt: comparison.prompt || "",
    result: comparison.result || "",
    summary: evidence.summary?.summary_text || trace.summary || "",
    durationMS: numberOr(stats.duration_ms, comparison.duration_ms),
    latencyMS: numberOr(usage.latency_ms),
    inputTokens: numberOr(usage.input_tokens),
    outputTokens: numberOr(usage.output_tokens),
    totalTokens: numberOr(usage.total_tokens),
    reasoningTokens: numberOr(usage.reasoning_tokens),
    modelCalls: numberOr(stats.llm_requests, usage.record_count),
    toolCalls: numberOr(stats.tool_calls, toolSteps.filter((step) => step.type === "runtime.tool_call").length),
    errors: numberOr(stats.errors, errorSteps.length),
    stepCount: numberOr(stats.step_count, steps.length),
    spanCount: numberOr(stats.span_count, trace.spans?.length),
    artifactCount: Array.isArray(comparison.artifacts) ? comparison.artifacts.length : numberOr(stats.artifact_count),
    criticalPathMS: numberOr(trace.graph?.critical_path_duration_ms),
    traceID: trace.trace_id || "",
    turnID: trace.turn_id || "",
    status: trace.status || comparison.session?.status || "unknown",
    steps,
    events,
    toolSteps,
    errorSteps,
    artifacts: comparison.artifacts || []
  };
}

export function metricDelta(left, right) {
  return Number(right || 0) - Number(left || 0);
}

export function formatDelta(value, unit = "") {
  const numeric = Number(value || 0);
  if (numeric === 0) return `0${unit}`;
  return `${numeric > 0 ? "+" : ""}${numeric.toLocaleString()}${unit}`;
}

export function formatDuration(value) {
  const ms = Number(value || 0);
  if (ms < 1000) return `${ms.toLocaleString()} ms`;
  return `${(ms / 1000).toFixed(ms < 10000 ? 2 : 1)} s`;
}

export function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? String(value) : date.toLocaleString();
}

export function pillTone(status) {
  const value = String(status || "").toLowerCase();
  if (["completed", "ok", "success", "approved"].includes(value)) return "good";
  if (["failed", "error", "rejected", "canceled"].includes(value)) return "bad";
  if (["running", "pending", "waiting_approval", "blocked"].includes(value)) return "warn";
  return "neutral";
}

function boundedScore(value) {
  const score = Number(value);
  if (!Number.isFinite(score)) return 3;
  return Math.max(1, Math.min(5, Math.round(score)));
}

function numberOr(...values) {
  for (const value of values) {
    const numeric = Number(value);
    if (Number.isFinite(numeric) && numeric !== 0) return numeric;
  }
  return 0;
}
