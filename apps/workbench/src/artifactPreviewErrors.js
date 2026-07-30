function errorMessages(error) {
  const messages = [];
  const seen = new Set();
  let current = error;
  while (current && !seen.has(current)) {
    seen.add(current);
    const message = typeof current.message === "string" ? current.message.trim() : "";
    if (message) messages.push(message);
    current = current.cause;
  }
  return messages;
}

export function artifactPreviewErrorMessage(error) {
  const messages = errorMessages(error);
  const detail = [...messages].reverse().find((message) => !message.startsWith("Resource provider ")) || "";
  const normalized = detail.toLowerCase();
  if (normalized.includes("soffice/libreoffice binary was not found")) {
    return "无法预览 DOCX：服务器未安装 LibreOffice 文档转换组件。请下载文件查看，或联系管理员安装后重试。";
  }
  if (normalized.includes("artifact is too large for document preview")) {
    return "无法在线预览：DOCX 文件超过 25 MB，请下载后查看。";
  }
  if (normalized.includes("only docx artifacts can be converted")) {
    return "无法在线预览：该文件不是有效的 DOCX 文档，请下载后检查文件格式。";
  }
  if (normalized.includes("convert docx preview") || normalized.includes("converted pdf preview")) {
    return `DOCX 转 PDF 预览失败：${detail}`;
  }
  return detail ? `文件预览失败：${detail}` : "文件预览失败，请下载文件后查看。";
}
