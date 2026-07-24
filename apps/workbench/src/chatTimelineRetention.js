export function retainedProcessText(event) {
  const payload = event?.payload && typeof event.payload === "object" && !Array.isArray(event.payload)
    ? event.payload
    : {};
  const data = payload.data && typeof payload.data === "object" && !Array.isArray(payload.data)
    ? payload.data
    : {};

  if (event?.type === "runtime.progress_message") {
    return String(data.text || "").trim();
  }
  if (event?.type === "runtime.thinking") {
    return String(data.text || payload.message || payload.summary || payload.text || "").trim();
  }
  return "";
}
