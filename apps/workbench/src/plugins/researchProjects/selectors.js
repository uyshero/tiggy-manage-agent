export function sessionSelectionOptions(input) {
  if (!Array.isArray(input)) return Object.freeze([]);
  const seen = new Set();
  const options = [];
  for (const session of input) {
    if (!session || typeof session !== "object" || Array.isArray(session)) continue;
    const id = typeof session.id === "string" ? session.id.trim() : "";
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const title = typeof session.title === "string" ? session.title.trim() : "";
    const status = typeof session.status === "string" ? session.status.trim() : "";
    options.push(Object.freeze({
      id,
      label: title || id,
      description: [title && title !== id ? id : "", status].filter(Boolean).join(" · "),
      keywords: [title, id, status].filter(Boolean).join(" ")
    }));
  }
  return Object.freeze(options);
}
