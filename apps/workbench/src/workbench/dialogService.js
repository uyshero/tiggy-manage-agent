const dialogIDPattern = /^[a-z][a-z0-9_-]*(?:\.[a-z][a-z0-9_-]*)+$/;
const confirmTones = new Set(["default", "warning", "danger"]);

export class DialogServiceError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "DialogServiceError";
    this.code = code;
  }
}

function plainObject(value, field) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new DialogServiceError("invalid_options", `${field} must be an object`);
  }
  return value;
}

function requiredText(value, field) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (!normalized) {
    throw new DialogServiceError("invalid_options", `${field} must be a non-empty string`);
  }
  return normalized;
}

function optionalText(value, field) {
  if (value === undefined || value === null || value === "") return "";
  return requiredText(value, field);
}

function dismissible(value) {
  return value === undefined ? true : Boolean(value);
}

function confirmOptions(input) {
  const value = plainObject(input, "confirm options");
  const tone = optionalText(value.tone, "confirm options.tone") || "default";
  if (!confirmTones.has(tone)) {
    throw new DialogServiceError("invalid_options", "confirm options.tone must be default, warning, or danger");
  }
  return Object.freeze({
    title: requiredText(value.title, "confirm options.title"),
    description: optionalText(value.description, "confirm options.description"),
    detail: optionalText(value.detail, "confirm options.detail"),
    confirmLabel: optionalText(value.confirmLabel, "confirm options.confirmLabel") || "确认",
    cancelLabel: optionalText(value.cancelLabel, "confirm options.cancelLabel") || "取消",
    tone,
    dismissible: dismissible(value.dismissible)
  });
}

function formOptions(input) {
  const value = plainObject(input, "form options");
  const schema = plainObject(value.schema, "form options.schema");
  const initialValues = value.initialValues === undefined ? {} : plainObject(value.initialValues, "form options.initialValues");
  return Object.freeze({
    title: requiredText(value.title, "form options.title"),
    description: optionalText(value.description, "form options.description"),
    schema,
    initialValues,
    submitLabel: optionalText(value.submitLabel, "form options.submitLabel") || "保存",
    cancelLabel: optionalText(value.cancelLabel, "form options.cancelLabel") || "取消",
    dismissible: dismissible(value.dismissible)
  });
}

function choiceOptions(input) {
  const value = plainObject(input, "choice options");
  if (!Array.isArray(value.items) || !value.items.length) {
    throw new DialogServiceError("invalid_options", "choice options.items must be a non-empty array");
  }
  const seen = new Set();
  const items = value.items.map((item, index) => {
    const option = plainObject(item, `choice options.items[${index}]`);
    const optionValue = requiredText(option.value, `choice options.items[${index}].value`);
    if (seen.has(optionValue)) {
      throw new DialogServiceError("invalid_options", `choice option value ${optionValue} is duplicated`);
    }
    seen.add(optionValue);
    return Object.freeze({
      value: optionValue,
      label: requiredText(option.label, `choice options.items[${index}].label`),
      description: optionalText(option.description, `choice options.items[${index}].description`),
      keywords: optionalText(option.keywords, `choice options.items[${index}].keywords`),
      disabled: Boolean(option.disabled)
    });
  });
  const enabled = items.filter((item) => !item.disabled);
  if (!enabled.length) {
    throw new DialogServiceError("invalid_options", "choice options.items must include an enabled item");
  }
  const requestedInitialValue = optionalText(value.initialValue, "choice options.initialValue");
  const initialValue = enabled.some((item) => item.value === requestedInitialValue)
    ? requestedInitialValue
    : enabled[0].value;
  return Object.freeze({
    title: requiredText(value.title, "choice options.title"),
    description: optionalText(value.description, "choice options.description"),
    items: Object.freeze(items),
    initialValue,
    searchable: value.searchable === undefined ? items.length > 8 : Boolean(value.searchable),
    searchPlaceholder: optionalText(value.searchPlaceholder, "choice options.searchPlaceholder") || "搜索...",
    emptyMessage: optionalText(value.emptyMessage, "choice options.emptyMessage") || "没有匹配项",
    submitLabel: optionalText(value.submitLabel, "choice options.submitLabel") || "选择",
    cancelLabel: optionalText(value.cancelLabel, "choice options.cancelLabel") || "取消",
    dismissible: dismissible(value.dismissible)
  });
}

function customOptions(dialogID, renderer, input, options) {
  const value = options === undefined ? {} : plainObject(options, "dialog options");
  return Object.freeze({
    dialogID,
    renderer,
    input,
    title: optionalText(value.title, "dialog options.title") || dialogID,
    description: optionalText(value.description, "dialog options.description"),
    dismissible: dismissible(value.dismissible)
  });
}

export class DialogService {
  constructor() {
    this.active = null;
    this.counter = 0;
    this.listeners = new Set();
    this.queue = [];
    this.renderers = new Map();
  }

  subscribe(listener) {
    if (typeof listener !== "function") {
      throw new DialogServiceError("invalid_listener", "dialog listener must be a function");
    }
    this.listeners.add(listener);
    listener(this.active);
    return () => this.listeners.delete(listener);
  }

  getActive() {
    return this.active;
  }

  confirm(options) {
    return this.enqueue("confirm", confirmOptions(options));
  }

  form(options) {
    return this.enqueue("form", formOptions(options));
  }

  choice(options) {
    return this.enqueue("choice", choiceOptions(options));
  }

  register(dialogID, renderer) {
    const normalizedID = requiredText(dialogID, "dialog id");
    if (!dialogIDPattern.test(normalizedID)) {
      throw new DialogServiceError("invalid_dialog_id", "dialog id must be a lowercase namespaced identifier");
    }
    if (typeof renderer !== "function") {
      throw new DialogServiceError("invalid_renderer", "dialog renderer must be a function");
    }
    if (this.renderers.has(normalizedID)) {
      throw new DialogServiceError("duplicate_dialog", `dialog ${normalizedID} is already registered`);
    }
    this.renderers.set(normalizedID, renderer);
    return () => {
      if (this.renderers.get(normalizedID) === renderer) this.renderers.delete(normalizedID);
    };
  }

  open(dialogID, input, options) {
    const normalizedID = requiredText(dialogID, "dialog id");
    const renderer = this.renderers.get(normalizedID);
    if (!renderer) {
      return Promise.reject(new DialogServiceError("unknown_dialog", `dialog ${normalizedID} is not registered`));
    }
    return this.enqueue("custom", customOptions(normalizedID, renderer, input, options));
  }

  resolve(requestID, result) {
    if (!this.active || this.active.id !== requestID) return false;
    const request = this.active;
    this.active = null;
    request.resolve(result);
    this.advance();
    return true;
  }

  cancel(requestID) {
    if (!this.active || this.active.id !== requestID) return false;
    return this.resolve(requestID, this.active.kind === "confirm" ? false : undefined);
  }

  dispose(reason = "dialog service disposed") {
    const error = new DialogServiceError("disposed", reason);
    const requests = [this.active, ...this.queue].filter(Boolean);
    this.active = null;
    this.queue = [];
    requests.forEach((request) => request.reject(error));
    this.notify();
  }

  enqueue(kind, options) {
    return new Promise((resolve, reject) => {
      this.counter += 1;
      this.queue.push(Object.freeze({
        id: `dialog_${this.counter}`,
        kind,
        options,
        resolve,
        reject
      }));
      this.advance();
    });
  }

  advance() {
    if (!this.active) this.active = this.queue.shift() || null;
    this.notify();
  }

  notify() {
    this.listeners.forEach((listener) => listener(this.active));
  }
}

export function createDialogService() {
  return new DialogService();
}
