export function pretty(value) {
  return JSON.stringify(value, null, 2);
}

export function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

export function formatTaskTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const hours = String(date.getHours()).padStart(2, "0");
  const minutes = String(date.getMinutes()).padStart(2, "0");
  const seconds = String(date.getSeconds()).padStart(2, "0");
  return `${month}-${day} ${hours}:${minutes}:${seconds}`;
}

export function formatDuration(ms) {
  const value = Number(ms || 0);
  if (value < 1000) return `${value} ms`;
  return `${(value / 1000).toFixed(value < 10000 ? 2 : 1)} s`;
}

export function pillClass(statusValue) {
  if (["completed", "ok", "success", "approved", "idle"].includes(statusValue)) return "pill ok";
  if (["waiting approval", "waiting_approval", "pending", "blocked", "running", "interrupting", "provisioning", "compacting"].includes(statusValue)) return "pill warn";
  if (["failed", "error", "rejected"].includes(statusValue)) return "pill err";
  return "pill";
}

export function stepClass(step) {
  if (step.outcome === "error" || step.type === "runtime.failed") return "step error";
  if (step.type && step.type.includes("intervention")) return "step approval";
  if (step.type && step.type.includes("tool")) return "step tool";
  return "step";
}

export function sessionArtifactCLI(downloadPath) {
  let path = String(downloadPath || "").trim();
  if (!path) return "";
  path = path.split("?")[0].split("#")[0];
  const prefix = "/v2/sessions/";
  if (!path.startsWith(prefix)) return "";
  const parts = path.slice(prefix.length).split("/");
  if (parts.length !== 4 || parts[1] !== "artifacts" || parts[3] !== "download") return "";
  if (!parts[0] || !parts[2]) return "";
  return `bin/tma session artifact download --session ${parts[0]} --artifact ${parts[2]}`;
}

export function sessionArtifactCommand(sessionId, artifactId) {
  const session = String(sessionId || "").trim();
  const artifact = String(artifactId || "").trim();
  if (!session || !artifact) return "";
  return `bin/tma session artifact download --session ${session} --artifact ${artifact}`;
}
