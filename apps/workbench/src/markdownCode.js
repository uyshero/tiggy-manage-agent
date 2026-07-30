function renderedCodeText(value) {
  return Array.isArray(value)
    ? value.map((item) => renderedCodeText(item)).join("")
    : value == null || typeof value === "boolean"
      ? ""
      : String(value);
}

export function markdownCodeText(value) {
  const text = renderedCodeText(value);
  return text.endsWith("\n") ? text.slice(0, -1) : text;
}

export function markdownCodeLanguage(className) {
  const match = /(?:^|\s)language-([^\s]+)/.exec(String(className || ""));
  return match?.[1] || "";
}

export async function copyTextToClipboard(text, environment = globalThis) {
  const clipboard = environment?.navigator?.clipboard;
  if (typeof clipboard?.writeText === "function") {
    try {
      await clipboard.writeText(String(text || ""));
      return;
    } catch {
      // Fall through for browsers that expose the API but deny permission.
    }
  }

  const document = environment?.document;
  if (!document?.body || typeof document.createElement !== "function" || typeof document.execCommand !== "function") {
    throw new Error("Clipboard access is unavailable.");
  }
  const textarea = document.createElement("textarea");
  textarea.value = String(text || "");
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  document.body.appendChild(textarea);
  textarea.select();
  try {
    if (!document.execCommand("copy")) throw new Error("Copy command was rejected.");
  } finally {
    textarea.remove();
  }
}
