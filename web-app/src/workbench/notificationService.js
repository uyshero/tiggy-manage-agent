export const NOTIFICATION_LEVELS = Object.freeze(["info", "success", "warning", "error"]);

const defaultDurations = Object.freeze({
  info: 5000,
  success: 4000,
  warning: 8000,
  error: 0
});

export class NotificationServiceError extends TypeError {
  constructor(field, message) {
    super(`${field}: ${message}`);
    this.name = "NotificationServiceError";
    this.field = field;
  }
}

function plainObject(value, field) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new NotificationServiceError(field, "must be an object");
  }
  return value;
}

function requiredText(value, field) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (!normalized) throw new NotificationServiceError(field, "must be a non-empty string");
  return normalized;
}

function optionalText(value, field) {
  if (value === undefined || value === null || value === "") return "";
  return requiredText(value, field);
}

function normalizeNotification(input, id, now) {
  const value = plainObject(input, "notification");
  const level = optionalText(value.level, "notification.level") || "info";
  if (!NOTIFICATION_LEVELS.includes(level)) {
    throw new NotificationServiceError("notification.level", `must be one of ${NOTIFICATION_LEVELS.join(", ")}`);
  }
  const durationMs = value.durationMs === undefined ? defaultDurations[level] : Number(value.durationMs);
  if (!Number.isFinite(durationMs) || durationMs < 0) {
    throw new NotificationServiceError("notification.durationMs", "must be a non-negative finite number");
  }
  return Object.freeze({
    id,
    level,
    title: requiredText(value.title, "notification.title"),
    message: optionalText(value.message, "notification.message"),
    dedupeKey: optionalText(value.dedupeKey, "notification.dedupeKey"),
    durationMs,
    createdAt: now
  });
}

export class NotificationService {
  constructor(options = {}) {
    this.counter = 0;
    this.items = Object.freeze([]);
    this.listeners = new Set();
    this.now = typeof options.now === "function" ? options.now : () => Date.now();
  }

  subscribe(listener) {
    if (typeof listener !== "function") {
      throw new NotificationServiceError("listener", "must be a function");
    }
    this.listeners.add(listener);
    listener(this.items);
    return () => this.listeners.delete(listener);
  }

  getSnapshot() {
    return this.items;
  }

  show(input) {
    const dedupeKey = typeof input?.dedupeKey === "string" ? input.dedupeKey.trim() : "";
    const existing = dedupeKey ? this.items.find((item) => item.dedupeKey === dedupeKey) : null;
    if (!existing) this.counter += 1;
    const id = existing?.id || `notification_${this.counter}`;
    const notification = normalizeNotification(input, id, this.now());
    this.items = Object.freeze(existing
      ? this.items.map((item) => item.id === existing.id ? notification : item)
      : [...this.items, notification]);
    this.notify();
    return id;
  }

  dismiss(notificationID) {
    const id = String(notificationID || "").trim();
    if (!id || !this.items.some((item) => item.id === id)) return false;
    this.items = Object.freeze(this.items.filter((item) => item.id !== id));
    this.notify();
    return true;
  }

  clear() {
    if (!this.items.length) return false;
    this.items = Object.freeze([]);
    this.notify();
    return true;
  }

  notify() {
    this.listeners.forEach((listener) => listener(this.items));
  }
}

export function createNotificationService(options) {
  return new NotificationService(options);
}
