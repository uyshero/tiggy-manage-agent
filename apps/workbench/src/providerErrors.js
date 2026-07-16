const providerErrorDescriptions = Object.freeze({
  auth: "模型服务认证失败，请检查 Provider 凭据或联系管理员。",
  rate_limit: "模型服务请求过多或额度受限，请稍后重试。",
  context_length: "当前对话内容超过模型上下文上限，请压缩上下文或新建任务。",
  timeout: "模型服务响应超时，请稍后重试。",
  server: "模型服务暂时不可用，请稍后重试。",
  invalid_request: "模型服务无法处理当前请求，请检查模型与请求配置。",
  unknown: "模型请求失败，请根据原始错误排查。"
});

function positiveNumber(value) {
  const number = Number(value || 0);
  return Number.isFinite(number) && number > 0 ? number : 0;
}

export function providerErrorPresentation(error, fallbackMessage = "") {
  const source = error && typeof error === "object" && !Array.isArray(error) ? error : {};
  const errorClass = String(source.class || "unknown").trim().toLowerCase() || "unknown";
  const description = providerErrorDescriptions[errorClass] || providerErrorDescriptions.unknown;
  const original = String(source.message || fallbackMessage || "Provider 未返回错误详情。")
    .replace(/\s+/g, " ")
    .trim();
  const metadata = [];
  const statusCode = positiveNumber(source.status_code);
  const attempts = positiveNumber(source.attempts);
  const retryAfterMS = positiveNumber(source.retry_after_ms);
  if (statusCode) metadata.push(`HTTP ${statusCode}`);
  if (attempts) metadata.push(`已尝试 ${attempts} 次`);
  if (retryAfterMS) metadata.push(`建议等待 ${Math.ceil(retryAfterMS / 1000)} 秒`);
  const suffix = metadata.length ? `（${metadata.join("，")}）` : "";
  return {
    description,
    original,
    detail: `${description} 原始错误：${original}${suffix}`
  };
}
