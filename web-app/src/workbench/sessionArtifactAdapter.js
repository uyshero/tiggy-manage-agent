import { defineResourceRef } from "./contracts.js";

export const SESSION_ARTIFACT_SOURCE_PREFIX = "tma.session-artifact:";
export const MAX_ARTIFACT_PREVIEW_BYTES = 512 * 1024;
export const MAX_ARTIFACT_PREVIEW_CHARACTERS = 64000;

const imageExtensions = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg"]);
const textExtensions = new Set([
  "txt", "md", "markdown", "json", "jsonl", "csv", "tsv", "log", "xml", "html", "htm", "css",
  "js", "jsx", "ts", "tsx", "go", "py", "sh", "yaml", "yml"
]);

function plainMetadata(artifact) {
  const value = artifact?.metadata;
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function resourceExtension(resource) {
  const title = String(resource?.title || "").toLowerCase();
  const index = title.lastIndexOf(".");
  return index >= 0 ? title.slice(index + 1) : "";
}

export function previewKindForResource(resource, contentType = "") {
  const type = String(contentType || resource?.mimeType || "").toLowerCase();
  const extension = resourceExtension(resource);
  if (type.startsWith("image/") || imageExtensions.has(extension)) return "image";
  if (type.startsWith("text/") || type.includes("json") || type.includes("xml") || textExtensions.has(extension)) return "text";
  return "download";
}

export function isMarkdownResource(resource, contentType = "") {
  const type = String(contentType || resource?.mimeType || "").toLowerCase();
  return type.includes("markdown") || ["md", "markdown"].includes(resourceExtension(resource));
}

export function isHTMLResource(resource, contentType = "") {
  const type = String(contentType || resource?.mimeType || "").toLowerCase();
  return type.includes("text/html") || ["html", "htm"].includes(resourceExtension(resource));
}

export function htmlPreviewDocument(value = "") {
  const html = String(value || "");
  const policy = `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src data: blob:; media-src data: blob:; style-src 'unsafe-inline'; font-src data:;">`;
  if (/<head(?:\s[^>]*)?>/i.test(html)) {
    return html.replace(/<head(\s[^>]*)?>/i, (match) => `${match}${policy}`);
  }
  if (/<html(?:\s[^>]*)?>/i.test(html)) {
    return html.replace(/<html(\s[^>]*)?>/i, (match) => `${match}<head>${policy}</head>`);
  }
  return `<!doctype html><html><head>${policy}</head><body>${html}</body></html>`;
}

function allowlistedMetadata(artifact) {
  const input = plainMetadata(artifact);
  const output = {};
  const fields = ["size_bytes", "path", "file_path", "workspace_path"];
  for (const field of fields) {
    const value = input[field];
    if (typeof value === "string" && value) output[field] = value;
    if (field === "size_bytes" && typeof value === "number" && Number.isFinite(value) && value >= 0) output[field] = value;
  }
  const turnID = artifact?.turn_id || input.turn_id;
  if (typeof turnID === "string" && turnID) output.turn_id = turnID;
  const objectRefID = artifact?.object_ref_id || input.object_ref_id;
  if (typeof objectRefID === "string" && objectRefID) output.object_ref_id = objectRefID;
  return output;
}

export function artifactToResourceRef(artifact, { sessionID } = {}) {
  if (!artifact || typeof artifact !== "object" || Array.isArray(artifact)) {
    throw new TypeError("Artifact must be an object.");
  }
  const normalizedSessionID = typeof sessionID === "string" ? sessionID.trim() : "";
  if (!normalizedSessionID) throw new TypeError("Session id is required.");
  const metadata = plainMetadata(artifact);
  const title = artifact.name || artifact.id;
  const resource = {
    id: artifact.id,
    type: artifact.artifact_type === "file" ? "file" : "artifact",
    title,
    source: `${SESSION_ARTIFACT_SOURCE_PREFIX}${normalizedSessionID}`,
    previewable: previewKindForResource({ title, mimeType: metadata.content_type }) !== "download",
    metadata: allowlistedMetadata(artifact)
  };
  if (typeof metadata.content_type === "string" && metadata.content_type.trim()) {
    resource.mimeType = metadata.content_type.trim();
  }
  return defineResourceRef(resource);
}

function sessionIDFromResource(resource) {
  if (!resource.source.startsWith(SESSION_ARTIFACT_SOURCE_PREFIX)) {
    throw new TypeError("Resource is not a Session Artifact.");
  }
  const sessionID = resource.source.slice(SESSION_ARTIFACT_SOURCE_PREFIX.length);
  if (!sessionID) throw new TypeError("Session Artifact source is missing a session id.");
  return sessionID;
}

function throwIfAborted(signal) {
  if (!signal?.aborted) return;
  const error = new Error("Preview aborted.");
  error.name = "AbortError";
  throw error;
}

export function createSessionArtifactProvider(options = {}) {
  const downloadArtifact = options.downloadArtifact;
  const artifactDownloadPath = options.artifactDownloadPath;
  const createObjectURL = options.createObjectURL || ((blob) => URL.createObjectURL(blob));
  const revokeObjectURL = options.revokeObjectURL || ((url) => URL.revokeObjectURL(url));
  const openURL = options.openURL || ((url) => window.open(url, "_blank", "noopener,noreferrer"));
  const maxTextBytes = options.maxTextBytes || MAX_ARTIFACT_PREVIEW_BYTES;
  const maxTextCharacters = options.maxTextCharacters || MAX_ARTIFACT_PREVIEW_CHARACTERS;
  if (typeof downloadArtifact !== "function" || typeof artifactDownloadPath !== "function") {
    throw new TypeError("Session Artifact provider requires downloadArtifact and artifactDownloadPath.");
  }

  function downloadURL(resource) {
    return artifactDownloadPath(sessionIDFromResource(resource), resource.id);
  }

  return Object.freeze({
    id: "tma.session-artifact",
    sourcePrefix: SESSION_ARTIFACT_SOURCE_PREFIX,
    listRelated(context = {}) {
      const artifacts = Array.isArray(context.artifacts) ? context.artifacts : [];
      return artifacts.map((artifact) => artifactToResourceRef(artifact, { sessionID: context.sessionID }));
    },
    async preview(resource, context = {}) {
      const url = downloadURL(resource);
      const response = await downloadArtifact(sessionIDFromResource(resource), resource.id, { signal: context.signal });
      throwIfAborted(context.signal);
      const contentType = response.headers.get("Content-Type") || resource.mimeType || "";
      const contentLength = Number(response.headers.get("Content-Length") || 0);
      const kind = previewKindForResource(resource, contentType);
      if (kind === "image") {
        const blob = await response.blob();
        throwIfAborted(context.signal);
        const objectUrl = createObjectURL(blob);
        let disposed = false;
        return {
          kind,
          contentType,
          objectUrl,
          downloadUrl: url,
          dispose() {
            if (disposed) return;
            disposed = true;
            revokeObjectURL(objectUrl);
          }
        };
      }
      if (kind === "text") {
        if (contentLength > maxTextBytes) {
          return { kind: "download", contentType, downloadUrl: url, message: "预览内容过大，请下载文件后查看。" };
        }
        let text = await response.text();
        throwIfAborted(context.signal);
        if (contentType.toLowerCase().includes("json")) {
          try {
            text = JSON.stringify(JSON.parse(text), null, 2);
          } catch {}
        }
        const truncated = text.length > maxTextCharacters;
        return {
          kind,
          contentType,
          text: truncated ? `${text.slice(0, maxTextCharacters)}\n\n[预览已截断]` : text,
          downloadUrl: url
        };
      }
      return {
        kind: "download",
        contentType,
        downloadUrl: url,
        message: "暂不支持此文件类型的内联预览，请下载后查看。"
      };
    },
    open(resource) {
      const url = downloadURL(resource);
      openURL(url);
      return url;
    }
  });
}
