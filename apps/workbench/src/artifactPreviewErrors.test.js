import assert from "node:assert/strict";
import test from "node:test";

import { artifactPreviewErrorMessage } from "./artifactPreviewErrors.js";

test("explains missing DOCX conversion support from a wrapped provider error", () => {
  const apiError = new Error("document preview unsupported: soffice/libreoffice binary was not found");
  const providerError = new Error("Resource provider tma.session-artifact failed to preview the resource.", { cause: apiError });
  assert.equal(
    artifactPreviewErrorMessage(providerError),
    "无法预览 DOCX：服务器未安装 LibreOffice 文档转换组件。请下载文件查看，或联系管理员安装后重试。"
  );
});

test("maps size and conversion failures to actionable preview messages", () => {
  assert.equal(
    artifactPreviewErrorMessage(new Error("artifact is too large for document preview")),
    "无法在线预览：DOCX 文件超过 25 MB，请下载后查看。"
  );
  assert.equal(
    artifactPreviewErrorMessage(new Error("convert DOCX preview: exit status 1")),
    "DOCX 转 PDF 预览失败：convert DOCX preview: exit status 1"
  );
});

test("keeps an unexpected underlying error instead of the provider wrapper", () => {
  const error = new Error("Resource provider tma.session-artifact failed to preview the resource.", {
    cause: new Error("network connection closed")
  });
  assert.equal(artifactPreviewErrorMessage(error), "文件预览失败：network connection closed");
});
