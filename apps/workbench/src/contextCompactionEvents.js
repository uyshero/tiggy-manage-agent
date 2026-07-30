import { runtimeFailurePresentation } from "./runtimeFailures.js";

const eventTypes = new Set([
  "runtime.context_compacting",
  "runtime.context_compacted",
  "runtime.context_compaction_failed"
]);

function objectValue(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function eventData(event) {
  return objectValue(objectValue(event?.payload).data);
}

export function isContextCompactionEvent(event) {
  return eventTypes.has(event?.type);
}

export function contextCompactionFailureDedupeKey(event) {
  const root = objectValue(event?.payload);
  const data = eventData(event);
  const isExplicitFailure = event?.type === "runtime.context_compaction_failed";
  const isCompactionRuntimeFailure = event?.type === "runtime.failed" && [
    "context_compaction_failed",
    "invalid_compaction_result",
    "invalid_compaction_usage"
  ].includes(String(data.code || ""));
  if (!isExplicitFailure && !isCompactionRuntimeFailure) return "";
  const turnID = String(root.turn_id || "");
  const revision = String(root.loop_revision ?? "");
  return turnID && revision ? `${turnID}:${revision}` : "";
}

export function contextCompactionPresentation(event, { active = false } = {}) {
  const data = eventData(event);
  switch (event?.type) {
    case "runtime.context_compacting": {
      const attempt = Number(data.number || 0);
      return {
        title: "开始压缩上下文",
        detail: attempt > 0 ? `正在进行第 ${attempt} 次上下文压缩，完成后自动继续任务。` : "正在压缩历史上下文，完成后自动继续任务。",
        metaLabel: "上下文管理",
        tone: "tool",
        status: active ? "running" : "completed",
        statusLabel: active ? "压缩中" : "已开始",
        defaultExpanded: active,
        contextItems: [
          ...(attempt > 0 ? [{ label: "尝试次数", value: String(attempt) }] : []),
          ...(data.id ? [{ label: "尝试 ID", value: String(data.id) }] : [])
        ],
        detailObject: {
          attempt_number: attempt || undefined,
          attempt_id: data.id || undefined
        }
      };
    }
    case "runtime.context_compacted": {
      const estimatedInputTokens = Number(data.estimated_input_tokens);
      const hasEstimate = Number.isFinite(estimatedInputTokens) && estimatedInputTokens >= 0;
      return {
        title: "上下文压缩完成",
        detail: hasEstimate ? `压缩后预计包含 ${estimatedInputTokens.toLocaleString()} 个输入 token，任务将继续执行。` : "历史上下文已压缩，任务将继续执行。",
        metaLabel: "上下文管理",
        tone: "ok",
        status: "completed",
        statusLabel: "完成",
        defaultExpanded: false,
        contextItems: [
          ...(hasEstimate ? [{ label: "预计输入 Token", value: estimatedInputTokens.toLocaleString() }] : []),
          ...(data.attempt_id ? [{ label: "尝试 ID", value: String(data.attempt_id) }] : [])
        ],
        detailObject: {
          estimated_input_tokens: hasEstimate ? estimatedInputTokens : undefined,
          attempt_id: data.attempt_id || undefined,
          usage: Object.keys(objectValue(data.usage)).length ? data.usage : undefined
        }
      };
    }
    case "runtime.context_compaction_failed": {
      const failure = runtimeFailurePresentation(event?.payload);
      return {
        title: "上下文压缩失败",
        detail: failure.detail,
        metaLabel: "上下文管理",
        tone: "error",
        status: "error",
        statusLabel: "失败",
        defaultExpanded: true,
        contextItems: failure.code ? [{ label: "错误代码", value: failure.code }] : [],
        detailObject: {
          error_code: failure.code || undefined,
          description: failure.description,
          original_error: failure.original,
          ...(Object.keys(failure.providerError).length ? { provider_error: failure.providerError } : {})
        }
      };
    }
    default:
      return null;
  }
}
