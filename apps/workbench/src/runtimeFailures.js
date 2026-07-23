import { providerErrorPresentation } from "./providerErrors.js";

const failureDescriptions = Object.freeze({
  context_compaction_failed: "上下文压缩失败，模型服务未能处理当前对话。请重试；如持续失败，请新建任务或检查模型配置。",
  context_build_failed: "任务上下文准备失败。请重试；如持续失败，请新建任务。",
  budget_exhausted: "本轮任务已达到运行预算上限。请缩小任务范围后重试。",
  runtime_binding_changed: "智能体配置在任务执行期间发生变化。请新建任务后重试。",
  tool_runtime_failed: "工具运行环境执行失败。请检查 Agent 绑定的运行环境后重试。",
  model_request_failed: "模型请求失败，请稍后重试或检查模型配置。",
  invalid_model_request: "模型无法处理当前请求，请检查模型及输入配置。",
  completion_validator_failed: "任务完成校验失败，请重试。"
});

function objectValue(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function cleanText(value) {
  return String(value || "").replace(/\s+/g, " ").trim();
}

function inferredProviderError(code, message) {
  if (!String(code || "").startsWith("http_") && !message.startsWith("provider request failed")) return {};
  const classMatch = message.match(/provider request failed \(([^/,)]+)/i);
  const statusMatch = String(code || "").match(/^http_(\d+)$/i);
  return {
    class: classMatch?.[1] || "unknown",
    code,
    status_code: statusMatch ? Number(statusMatch[1]) : 0,
    message
  };
}

export function runtimeFailurePresentation(value) {
  const root = objectValue(value);
  const data = objectValue(root.data);
  const nestedError = objectValue(data.error);
  const failure = Object.keys(nestedError).length ? nestedError : data;
  const code = cleanText(failure.code || root.code);
  const original = cleanText(failure.message || root.reason || root.error_message || root.message) || "执行过程中出现未知错误。";
  const structuredProviderError = objectValue(failure.provider_error || data.provider_error || root.provider_error);
  const providerError = Object.keys(structuredProviderError).length
    ? structuredProviderError
    : inferredProviderError(code, original);
  const mappedDescription = failureDescriptions[code] || "";

  if (Object.keys(providerError).length) {
    const provider = providerErrorPresentation(providerError, original);
    const description = mappedDescription || provider.description;
    const codeSuffix = code ? `（错误代码：${code}）` : "";
    return { code, description, original: provider.original, providerError, detail: `${description} 原始错误：${provider.original}${codeSuffix}` };
  }

  const genericMessages = new Set(["agent runtime failed.", "turn failed.", "执行过程中出现失败。"]);
  const showOriginal = !genericMessages.has(original.toLowerCase()) && original !== mappedDescription;
  const description = mappedDescription || "任务执行失败。请重试；如持续失败，请查看错误代码并联系管理员。";
  const originalDetail = showOriginal ? ` 原始错误：${original}` : "";
  const codeSuffix = code ? `（错误代码：${code}）` : "";
  return { code, description, original, providerError: {}, detail: `${description}${originalDetail}${codeSuffix}` };
}
