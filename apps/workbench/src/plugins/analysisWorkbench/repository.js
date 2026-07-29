import {
  R_SURVIVAL_DATA_CLEANING_SKILL_CONTENT,
  R_SURVIVAL_DATA_CLEANING_SKILL_PATH
} from "./survivalSkill.js";

const STORAGE_PREFIX = "tma.plugin.com.tma.r-survival-workbench.projects.v1";
export const R_SURVIVAL_WORKBENCH_PLUGIN_ID = "com.tma.r-survival-workbench";

export const DEFAULT_NOTEBOOK_CODE = `library(survival)
library(ggsurvfit)

surv_obj <- Surv(followup_month, event)
km_fit <- survfit(surv_obj ~ treatment, data = followup)
cox_fit <- coxph(
  surv_obj ~ treatment + age + stage,
  data = followup
)

ggsurvfit(km_fit) + add_risktable()`;

export const DEFAULT_PROJECT_FILES = Object.freeze([
  Object.freeze({ path: "README.md", kind: "file", status: "clean" }),
  Object.freeze({ path: "notebooks", kind: "folder" }),
  Object.freeze({ path: "notebooks/survival-analysis.ipynb", kind: "file", status: "modified" }),
  Object.freeze({ path: "R", kind: "folder" }),
  Object.freeze({ path: "R/clean-data.R", kind: "file", status: "clean" }),
  Object.freeze({ path: "R/survival-model.R", kind: "file", status: "clean" }),
  Object.freeze({ path: "config", kind: "folder" }),
  Object.freeze({ path: "config/variable-mapping.yml", kind: "file", status: "clean" }),
  Object.freeze({ path: "skills", kind: "folder" }),
  Object.freeze({ path: "skills/r-survival-data-cleaning", kind: "folder" }),
  Object.freeze({
    path: R_SURVIVAL_DATA_CLEANING_SKILL_PATH,
    kind: "file",
    status: "clean",
    content: R_SURVIVAL_DATA_CLEANING_SKILL_CONTENT
  }),
  Object.freeze({ path: "reports", kind: "folder" }),
  Object.freeze({ path: "renv.lock", kind: "file", status: "clean" })
]);

function requiredText(value, field, maxLength) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (!normalized) throw new Error(`${field} is required`);
  if (normalized.length > maxLength) throw new Error(`${field} exceeds ${maxLength} characters`);
  return normalized;
}

function optionalText(value, maxLength) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (normalized.length > maxLength) throw new Error(`value exceeds ${maxLength} characters`);
  return normalized;
}

function normalizedScope(scope) {
  return {
    workspaceId: requiredText(scope?.workspaceId, "scope.workspaceId", 240),
    userId: requiredText(scope?.userId, "scope.userId", 240)
  };
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function projectID(randomID) {
  const generated = typeof randomID === "function" ? String(randomID()).trim() : "";
  return `analysis_${generated || globalThis.crypto?.randomUUID?.() || Math.random().toString(36).slice(2)}`;
}

function normalizeProject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  try {
    return {
      id: requiredText(value.id, "project.id", 240),
      name: requiredText(value.name, "project.name", 120),
      objective: optionalText(value.objective, 1200),
      repositoryPath: optionalText(value.repositoryPath, 240),
      gitlabURL: optionalText(value.gitlabURL, 1000),
      notebookURL: optionalText(value.notebookURL, 1000),
      gitStatus: ["local", "syncing", "synced", "error"].includes(value.gitStatus) ? value.gitStatus : "local",
      branch: optionalText(value.branch, 120) || "main",
      activeFile: optionalText(value.activeFile, 500) || "notebooks/survival-analysis.ipynb",
      notebookCode: typeof value.notebookCode === "string" ? value.notebookCode : DEFAULT_NOTEBOOK_CODE,
      files: Array.isArray(value.files) && value.files.length ? value.files.map((file) => ({
        ...file,
        content: typeof file.content === "string" ? file.content : ""
      })) : clone(DEFAULT_PROJECT_FILES),
      createdAt: requiredText(value.createdAt, "project.createdAt", 80),
      updatedAt: requiredText(value.updatedAt, "project.updatedAt", 80)
    };
  } catch {
    return null;
  }
}

export function storageKeyForScope(scope) {
  const normalized = normalizedScope(scope);
  return `${STORAGE_PREFIX}:${encodeURIComponent(normalized.workspaceId)}:${encodeURIComponent(normalized.userId)}`;
}

export function createAnalysisWorkspaceRepository(options = {}) {
  const storage = options.storage;
  if (!storage || typeof storage.getItem !== "function" || typeof storage.setItem !== "function") {
    throw new Error("a storage adapter is required");
  }
  const key = storageKeyForScope(options.scope);
  const now = typeof options.now === "function" ? options.now : () => Date.now();
  const randomID = options.randomID;

  function read() {
    try {
      const parsed = JSON.parse(storage.getItem(key) || "[]");
      return Array.isArray(parsed) ? parsed.map(normalizeProject).filter(Boolean) : [];
    } catch {
      return [];
    }
  }

  function write(projects) {
    storage.setItem(key, JSON.stringify(projects));
  }

  function create(input) {
    const timestamp = new Date(now()).toISOString();
    const name = requiredText(input?.name, "project.name", 120);
    const repositoryPath = optionalText(input?.repositoryPath, 240) || name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "r-analysis";
    const project = {
      id: projectID(randomID),
      name,
      objective: optionalText(input?.objective, 1200),
      repositoryPath,
      gitlabURL: optionalText(input?.gitlabURL, 1000),
      notebookURL: optionalText(input?.notebookURL, 1000),
      gitStatus: "local",
      branch: "main",
      activeFile: "notebooks/survival-analysis.ipynb",
      notebookCode: DEFAULT_NOTEBOOK_CODE,
      files: clone(DEFAULT_PROJECT_FILES),
      createdAt: timestamp,
      updatedAt: timestamp
    };
    const projects = read();
    projects.push(project);
    write(projects);
    return clone(project);
  }

  function ensureExample() {
    const projects = read();
    if (projects.length) return clone(projects[0]);
    return create({
      name: "肿瘤患者生存分析",
      objective: "完成中文随访数据清洗、Kaplan-Meier 分析和 Cox 回归",
      repositoryPath: "survival-analysis"
    });
  }

  function list() {
    return read().sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)).map(clone);
  }

  function update(id, patch) {
    const projects = read();
    const index = projects.findIndex((project) => project.id === id);
    if (index < 0) throw new Error(`project ${id} was not found`);
    const current = projects[index];
    const next = normalizeProject({
      ...current,
      ...patch,
      id: current.id,
      createdAt: current.createdAt,
      updatedAt: new Date(now()).toISOString()
    });
    if (!next) throw new Error("project update is invalid");
    projects[index] = next;
    write(projects);
    return clone(next);
  }

  return Object.freeze({ key, create, ensureExample, list, update });
}

function remoteProject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid workbench project response");
  const runtimeURL = optionalText(value.runtime_url, 1000);
  return {
    id: requiredText(value.id, "project.id", 240),
    name: requiredText(value.name, "project.name", 120),
    objective: optionalText(value.objective, 1200),
    repositoryPath: requiredText(value.repository_path, "project.repository_path", 240),
    gitlabURL: optionalText(value.repository_url, 1000),
    notebookURL: runtimeURL || optionalText(value.notebook_url, 1000),
    runtimeID: optionalText(value.runtime_id, 240),
    runtimeStatus: ["unconfigured", "starting", "running", "stopped", "error"].includes(value.runtime_status) ? value.runtime_status : "unconfigured",
    runtimeURL,
    runtimeError: optionalText(value.runtime_error, 4000),
    runtimeStartedAt: optionalText(value.runtime_started_at, 80),
    gitStatus: ["local", "syncing", "synced", "error"].includes(value.sync_status) ? value.sync_status : "local",
    gitError: optionalText(value.sync_error, 4000),
    branch: optionalText(value.default_branch, 120) || "main",
    activeFile: optionalText(value.active_file, 500) || "notebooks/survival-analysis.ipynb",
    notebookCode: typeof value.notebook_code === "string" ? value.notebook_code : DEFAULT_NOTEBOOK_CODE,
    files: Array.isArray(value.files) && value.files.length ? value.files.map((file) => ({
      ...file,
      content: typeof file.content === "string" ? file.content : ""
    })) : clone(DEFAULT_PROJECT_FILES),
    createdAt: requiredText(value.created_at, "project.created_at", 80),
    updatedAt: requiredText(value.updated_at, "project.updated_at", 80),
    persistence: "backend"
  };
}

function remotePatch(patch) {
  const result = {};
  if (Object.hasOwn(patch, "name")) result.name = patch.name;
  if (Object.hasOwn(patch, "objective")) result.objective = patch.objective;
  if (Object.hasOwn(patch, "notebookURL")) result.notebook_url = patch.notebookURL;
  if (Object.hasOwn(patch, "activeFile")) result.active_file = patch.activeFile;
  if (Object.hasOwn(patch, "notebookCode")) result.notebook_code = patch.notebookCode;
  if (Object.hasOwn(patch, "files")) result.files = patch.files;
  return result;
}

export function createAnalysisWorkspaceClient(options = {}) {
  const request = options.http?.request;
  if (typeof request !== "function") throw new Error("a scoped HTTP service is required");
  const scope = normalizedScope(options.scope);
  const query = new URLSearchParams({ workspace_id: scope.workspaceId, plugin_id: R_SURVIVAL_WORKBENCH_PLUGIN_ID });
  const projectQuery = new URLSearchParams({ workspace_id: scope.workspaceId });

  function projectPath(id, suffix = "") {
    return `/v2/workbench-projects/${encodeURIComponent(id)}${suffix}?${projectQuery}`;
  }

  async function list() {
    const response = await request(`/v2/workbench-projects?${query}`);
    return {
      projects: Array.isArray(response?.projects) ? response.projects.map(remoteProject) : [],
      gitLabConfigured: response?.gitlab_configured === true
    };
  }

  async function create(input) {
    const name = requiredText(input?.name, "project.name", 120);
    const repositoryPath = optionalText(input?.repositoryPath, 240) || name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "r-analysis";
    const created = await request("/v2/workbench-projects", {
      method: "POST",
      body: {
        workspace_id: scope.workspaceId,
        plugin_id: R_SURVIVAL_WORKBENCH_PLUGIN_ID,
        name,
        objective: optionalText(input?.objective, 1200),
        repository_path: repositoryPath,
        notebook_url: optionalText(input?.notebookURL, 1000),
        notebook_code: typeof input?.notebookCode === "string" ? input.notebookCode : DEFAULT_NOTEBOOK_CODE
      }
    });
    return remoteProject(created);
  }

  async function update(id, patch) {
    const updated = await request(projectPath(id), {
      method: "PATCH",
      body: remotePatch(patch || {})
    });
    return remoteProject(updated);
  }

  async function sync(id) {
    const updated = await request(projectPath(id, "/sync"), { method: "POST" });
    return remoteProject(updated);
  }

  async function startRuntime(id) {
    const started = await request(projectPath(id, "/runtime/start"), { method: "POST" });
    return remoteProject(started);
  }

  async function stopRuntime(id) {
    const stopped = await request(projectPath(id, "/runtime/stop"), { method: "POST" });
    return remoteProject(stopped);
  }

  async function runCleaning(id) {
    const response = await request(projectPath(id, "/runtime/run-cleaning"), { method: "POST" });
    return {
      project: remoteProject(response.project),
      result: response.result || { exit_code: 0 }
    };
  }

  return Object.freeze({ create, list, runCleaning, sync, startRuntime, stopRuntime, update });
}
