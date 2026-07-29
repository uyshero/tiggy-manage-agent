export function clipboardHasPlainText(clipboard) {
  if (!clipboard) return false;
  const types = Array.from(clipboard.types || []).map((type) => String(type).toLowerCase());
  if (types.includes("text/plain")) return true;
  if (typeof clipboard.getData !== "function") return false;
  try {
    return clipboard.getData("text/plain") !== "";
  } catch {
    return false;
  }
}

export function clipboardImageFiles(clipboard) {
  if (!clipboard) return [];
  const itemImages = Array.from(clipboard.items || [])
    .filter((item) => item.kind === "file" && String(item.type || "").toLowerCase().startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter(Boolean);
  if (itemImages.length) return itemImages;
  return Array.from(clipboard.files || [])
    .filter((file) => String(file.type || "").toLowerCase().startsWith("image/"));
}
