export const sessionMessageQueueStorageKey = "tma.workbench.session-message-queue.v1";

export function normalizeSessionMessageQueue(value) {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return [];
    const id = String(item.id || "").trim();
    const sessionID = String(item.session_id || "").trim();
    const text = String(item.text || "").trim();
    if (!id || !sessionID || !text) return [];
    return [{
      id,
      session_id: sessionID,
      text,
      display_text: String(item.display_text || text).trim() || text,
      attachments: Array.isArray(item.attachments) ? item.attachments : [],
      created_at: String(item.created_at || "")
    }];
  }).sort((left, right) => String(left.created_at).localeCompare(String(right.created_at)) || left.id.localeCompare(right.id));
}

export function appendSessionMessageQueue(items, item) {
  return normalizeSessionMessageQueue([...(items || []), item]);
}

export function removeSessionMessageQueueItem(items, itemID) {
  return normalizeSessionMessageQueue(items).filter((item) => item.id !== itemID);
}

export function sessionMessageQueueItems(items, sessionID) {
  return normalizeSessionMessageQueue(items).filter((item) => item.session_id === sessionID);
}
