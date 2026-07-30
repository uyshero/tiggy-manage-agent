import React, { useEffect, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";

import { copyTextToClipboard, markdownCodeLanguage, markdownCodeText } from "./markdownCode.js";

export default function MarkdownCodeBlock({ node: _node, children, ...props }) {
  const [copyState, setCopyState] = useState("idle");
  const resetTimerRef = useRef(null);
  const codeElement = React.Children.toArray(children).find((child) => React.isValidElement(child));
  const codeText = markdownCodeText(codeElement?.props?.children ?? children);
  const language = markdownCodeLanguage(codeElement?.props?.className);
  const copied = copyState === "copied";
  const failed = copyState === "failed";
  const label = copied ? "代码已复制" : failed ? "复制失败，点击重试" : "复制代码";

  useEffect(() => () => window.clearTimeout(resetTimerRef.current), []);

  async function handleCopy() {
    window.clearTimeout(resetTimerRef.current);
    try {
      await copyTextToClipboard(codeText);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
    resetTimerRef.current = window.setTimeout(() => setCopyState("idle"), 1800);
  }

  return (
    <div className="message-code-block">
      <div className="message-code-toolbar">
        <span>{language || "代码"}</span>
        <button
          className={`message-code-copy ${copyState}`}
          type="button"
          title={label}
          aria-label={label}
          onClick={handleCopy}
        >
          {copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
        </button>
        <span className="message-code-copy-status" role="status" aria-live="polite">
          {copied ? "已复制" : failed ? "复制失败" : ""}
        </span>
      </div>
      <pre {...props}>{children}</pre>
    </div>
  );
}
