import { defineResourceRef } from "./contracts.js";

export class RelatedResourceServiceError extends Error {
  constructor(code, message, cause) {
    super(message, cause ? { cause } : undefined);
    this.name = "RelatedResourceServiceError";
    this.code = code;
  }
}

function requireProvider(provider) {
  if (!provider || typeof provider !== "object" || Array.isArray(provider)) {
    throw new RelatedResourceServiceError("invalid_provider", "Resource provider must be an object.");
  }
  const id = typeof provider.id === "string" ? provider.id.trim() : "";
  if (!id) {
    throw new RelatedResourceServiceError("invalid_provider", "Resource provider id is required.");
  }
  if (typeof provider.supports !== "function" && !(typeof provider.sourcePrefix === "string" && provider.sourcePrefix)) {
    throw new RelatedResourceServiceError("invalid_provider", `Resource provider ${id} must define supports() or sourcePrefix.`);
  }
  return { ...provider, id };
}

function normalizePreviewDescriptor(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new RelatedResourceServiceError("invalid_preview", "Preview provider returned an invalid descriptor.");
  }
  if (!["image", "text", "download"].includes(value.kind)) {
    throw new RelatedResourceServiceError("invalid_preview", "Preview kind must be image, text, or download.");
  }
  if (value.kind === "image" && !(typeof value.objectUrl === "string" && value.objectUrl)) {
    throw new RelatedResourceServiceError("invalid_preview", "Image previews require objectUrl.");
  }
  if (value.kind === "text" && typeof value.text !== "string") {
    throw new RelatedResourceServiceError("invalid_preview", "Text previews require text.");
  }
  if (value.dispose !== undefined && typeof value.dispose !== "function") {
    throw new RelatedResourceServiceError("invalid_preview", "Preview dispose must be a function.");
  }
  return Object.freeze({
    kind: value.kind,
    contentType: typeof value.contentType === "string" ? value.contentType : "",
    text: typeof value.text === "string" ? value.text : "",
    objectUrl: typeof value.objectUrl === "string" ? value.objectUrl : "",
    downloadUrl: typeof value.downloadUrl === "string" ? value.downloadUrl : "",
    message: typeof value.message === "string" ? value.message : "",
    dispose: typeof value.dispose === "function" ? value.dispose : () => {}
  });
}

function disposePreview(descriptor) {
  if (!descriptor) return;
  try {
    descriptor.dispose();
  } catch {
    // Resource cleanup is best-effort and must not break the next preview.
  }
}

function cancelledError() {
  return new RelatedResourceServiceError("preview_cancelled", "Resource preview was cancelled.");
}

export function isPreviewCancelledError(error) {
  return error instanceof RelatedResourceServiceError && error.code === "preview_cancelled";
}

export function createRelatedResourceService() {
  const providers = new Map();
  let previewSequence = 0;
  let pendingPreview = null;
  let pendingProviderID = "";
  let activePreview = null;
  let activeProviderID = "";

  function providerFor(resource) {
    for (const provider of providers.values()) {
      let supported = false;
      try {
        supported = typeof provider.supports === "function"
          ? Boolean(provider.supports(resource))
          : resource.source.startsWith(provider.sourcePrefix);
      } catch (error) {
        throw new RelatedResourceServiceError("provider_failed", `Resource provider ${provider.id} failed while checking support.`, error);
      }
      if (supported) return provider;
    }
    throw new RelatedResourceServiceError("provider_not_found", `No resource provider supports ${resource.source}.`);
  }

  function releasePreview() {
    previewSequence += 1;
    if (pendingPreview) {
      pendingPreview.abort();
      pendingPreview = null;
    }
    pendingProviderID = "";
    disposePreview(activePreview);
    activePreview = null;
    activeProviderID = "";
  }

  function registerProvider(input) {
    const provider = requireProvider(input);
    if (providers.has(provider.id)) {
      throw new RelatedResourceServiceError("provider_exists", `Resource provider ${provider.id} is already registered.`);
    }
    providers.set(provider.id, provider);
    let registered = true;
    return () => {
      if (!registered) return false;
      registered = false;
      if (pendingProviderID === provider.id || activeProviderID === provider.id) releasePreview();
      providers.delete(provider.id);
      return true;
    };
  }

  async function listRelated(context = {}) {
    const resources = [];
    const seen = new Set();
    for (const provider of providers.values()) {
      if (typeof provider.listRelated !== "function") continue;
      let listed;
      try {
        listed = await provider.listRelated(context);
      } catch (error) {
        throw new RelatedResourceServiceError("provider_failed", `Resource provider ${provider.id} failed to list resources.`, error);
      }
      if (!Array.isArray(listed)) {
        throw new RelatedResourceServiceError("invalid_resource_list", `Resource provider ${provider.id} must return an array.`);
      }
      for (const item of listed) {
        const resource = defineResourceRef(item);
        const key = `${resource.source}\n${resource.id}`;
        if (seen.has(key)) continue;
        seen.add(key);
        resources.push(resource);
      }
    }
    return Object.freeze(resources);
  }

  async function preview(input, context = {}) {
    const resource = defineResourceRef(input);
    const provider = providerFor(resource);
    if (typeof provider.preview !== "function") {
      throw new RelatedResourceServiceError("preview_unsupported", `Resource provider ${provider.id} does not support preview.`);
    }

    releasePreview();
    const requestID = previewSequence;
    const controller = new AbortController();
    pendingPreview = controller;
    pendingProviderID = provider.id;
    const externalSignal = context?.signal;
    const abortFromContext = () => controller.abort();
    if (externalSignal?.aborted) controller.abort();
    else externalSignal?.addEventListener?.("abort", abortFromContext, { once: true });

    let descriptor;
    try {
      const value = await provider.preview(resource, { ...context, signal: controller.signal });
      descriptor = normalizePreviewDescriptor(value);
      if (controller.signal.aborted || requestID !== previewSequence) {
        disposePreview(descriptor);
        descriptor = null;
        throw cancelledError();
      }
      activePreview = descriptor;
      activeProviderID = provider.id;
      return descriptor;
    } catch (error) {
      if (controller.signal.aborted || requestID !== previewSequence || error?.name === "AbortError") {
        if (descriptor && descriptor !== activePreview) disposePreview(descriptor);
        throw cancelledError();
      }
      if (error instanceof RelatedResourceServiceError) throw error;
      throw new RelatedResourceServiceError("provider_failed", `Resource provider ${provider.id} failed to preview the resource.`, error);
    } finally {
      externalSignal?.removeEventListener?.("abort", abortFromContext);
      if (pendingPreview === controller) {
        pendingPreview = null;
        pendingProviderID = "";
      }
    }
  }

  async function open(input, context = {}) {
    const resource = defineResourceRef(input);
    const provider = providerFor(resource);
    if (typeof provider.open !== "function") {
      throw new RelatedResourceServiceError("open_unsupported", `Resource provider ${provider.id} does not support open.`);
    }
    try {
      return await provider.open(resource, context);
    } catch (error) {
      if (error instanceof RelatedResourceServiceError) throw error;
      throw new RelatedResourceServiceError("provider_failed", `Resource provider ${provider.id} failed to open the resource.`, error);
    }
  }

  function dispose() {
    releasePreview();
    providers.clear();
  }

  return Object.freeze({ registerProvider, listRelated, preview, open, releasePreview, dispose });
}
