const STORAGE_PREFIX = "tma.plugin.com.tma.research-projects.projects.v1";
const PROJECT_STATUSES = new Set(["active", "archived"]);
const PROJECT_STAGES = new Set(["planning", "collecting", "analyzing", "drafting", "completed"]);
const FINDING_TYPES = new Set(["finding", "hypothesis", "question"]);

export const RESEARCH_STAGE_OPTIONS = Object.freeze([
  Object.freeze({ id: "planning", label: "研究规划" }),
  Object.freeze({ id: "collecting", label: "资料收集" }),
  Object.freeze({ id: "analyzing", label: "分析归纳" }),
  Object.freeze({ id: "drafting", label: "报告撰写" }),
  Object.freeze({ id: "completed", label: "已完成" })
]);

export const RESEARCH_FINDING_TYPE_OPTIONS = Object.freeze([
  Object.freeze({ id: "finding", label: "研究结论" }),
  Object.freeze({ id: "hypothesis", label: "研究假设" }),
  Object.freeze({ id: "question", label: "待验证问题" })
]);

export class ResearchProjectRepositoryError extends Error {
  constructor(code, message) {
    super(message);
    this.name = "ResearchProjectRepositoryError";
    this.code = code;
  }
}

function requiredText(value, field, maxLength) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (!normalized) throw new ResearchProjectRepositoryError("invalid_project", `${field} is required`);
  if (normalized.length > maxLength) {
    throw new ResearchProjectRepositoryError("invalid_project", `${field} exceeds ${maxLength} characters`);
  }
  return normalized;
}

function optionalText(value, field, maxLength) {
  const normalized = typeof value === "string" ? value.trim() : "";
  if (normalized.length > maxLength) {
    throw new ResearchProjectRepositoryError("invalid_project", `${field} exceeds ${maxLength} characters`);
  }
  return normalized;
}

function requiredStage(value) {
  if (!PROJECT_STAGES.has(value)) {
    throw new ResearchProjectRepositoryError("invalid_project", "project.stage is invalid");
  }
  return value;
}

function cloneJSON(value) {
  return JSON.parse(JSON.stringify(value));
}

function normalizeResource(input, defaultLinkedAt = "") {
  if (!input || typeof input !== "object" || Array.isArray(input)) {
    throw new ResearchProjectRepositoryError("invalid_resource", "resource must be an object");
  }
  return {
    id: requiredText(input.id, "resource.id", 240),
    type: requiredText(input.type, "resource.type", 64),
    title: requiredText(input.title, "resource.title", 500),
    source: requiredText(input.source, "resource.source", 500),
    linkedAt: optionalText(input.linkedAt || defaultLinkedAt, "resource.linkedAt", 80),
    ...(typeof input.mimeType === "string" && input.mimeType.trim() ? { mimeType: input.mimeType.trim() } : {}),
    ...(typeof input.previewable === "boolean" ? { previewable: input.previewable } : {}),
    metadata: input.metadata && typeof input.metadata === "object" && !Array.isArray(input.metadata)
      ? cloneJSON(input.metadata)
      : {}
  };
}

function normalizeFinding(input) {
  if (!input || typeof input !== "object" || Array.isArray(input)) {
    throw new ResearchProjectRepositoryError("invalid_finding", "finding must be an object");
  }
  const type = FINDING_TYPES.has(input.type) ? input.type : "finding";
  return {
    id: requiredText(input.id, "finding.id", 240),
    type,
    title: requiredText(input.title, "finding.title", 180),
    content: optionalText(input.content, "finding.content", 4000),
    createdAt: requiredText(input.createdAt, "finding.createdAt", 80),
    updatedAt: requiredText(input.updatedAt, "finding.updatedAt", 80)
  };
}

function normalizedScope(scope) {
  const workspaceId = requiredText(scope?.workspaceId, "scope.workspaceId", 240);
  const userId = requiredText(scope?.userId, "scope.userId", 240);
  return { workspaceId, userId };
}

function projectID(now, randomID) {
  const generated = typeof randomID === "function" ? String(randomID()).trim() : "";
  if (generated) return `research_${generated}`;
  return `research_${now()}_${Math.random().toString(36).slice(2, 10)}`;
}

function findingID(now, randomID) {
  const generated = typeof randomID === "function" ? String(randomID()).trim() : "";
  if (generated) return `finding_${generated}`;
  return `finding_${now()}_${Math.random().toString(36).slice(2, 10)}`;
}

function normalizeStoredProject(project) {
  if (!project || typeof project !== "object" || Array.isArray(project)) return null;
  try {
    const status = PROJECT_STATUSES.has(project.status) ? project.status : "active";
    const stage = PROJECT_STAGES.has(project.stage) ? project.stage : "planning";
    const updatedAt = requiredText(project.updatedAt, "project.updatedAt", 80);
    return {
      id: requiredText(project.id, "project.id", 240),
      name: requiredText(project.name, "project.name", 120),
      objective: optionalText(project.objective, "project.objective", 1200),
      description: optionalText(project.description, "project.description", 4000),
      nextStep: optionalText(project.nextStep, "project.nextStep", 1200),
      status,
      stage,
      resources: Array.isArray(project.resources) ? project.resources.map((resource) => normalizeResource(resource, updatedAt)) : [],
      findings: Array.isArray(project.findings) ? project.findings.map(normalizeFinding) : [],
      createdAt: requiredText(project.createdAt, "project.createdAt", 80),
      updatedAt
    };
  } catch {
    return null;
  }
}

export function storageKeyForScope(scope) {
  const normalized = normalizedScope(scope);
  return `${STORAGE_PREFIX}:${encodeURIComponent(normalized.workspaceId)}:${encodeURIComponent(normalized.userId)}`;
}

export function createResearchProjectRepository(options = {}) {
  const storage = options.storage;
  if (!storage || typeof storage.getItem !== "function" || typeof storage.setItem !== "function") {
    throw new ResearchProjectRepositoryError("storage_unavailable", "a storage adapter is required");
  }
  const key = storageKeyForScope(options.scope);
  const now = typeof options.now === "function" ? options.now : () => Date.now();
  const randomID = options.randomID || (() => globalThis.crypto?.randomUUID?.() || "");

  function read() {
    const raw = storage.getItem(key);
    if (!raw) return [];
    try {
      const parsed = JSON.parse(raw);
      if (!Array.isArray(parsed)) return [];
      return parsed.map(normalizeStoredProject).filter(Boolean);
    } catch {
      return [];
    }
  }

  function write(projects) {
    storage.setItem(key, JSON.stringify(projects));
  }

  function list(options = {}) {
    const includeArchived = Boolean(options.includeArchived);
    return read()
      .filter((project) => includeArchived || project.status !== "archived")
      .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))
      .map(cloneJSON);
  }

  function get(id) {
    const project = read().find((item) => item.id === id);
    if (!project) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    return cloneJSON(project);
  }

  function create(input) {
    const timestamp = new Date(now()).toISOString();
    const project = {
      id: projectID(now, randomID),
      name: requiredText(input?.name, "project.name", 120),
      objective: optionalText(input?.objective, "project.objective", 1200),
      description: optionalText(input?.description, "project.description", 4000),
      nextStep: optionalText(input?.nextStep, "project.nextStep", 1200),
      status: "active",
      stage: PROJECT_STAGES.has(input?.stage) ? input.stage : "planning",
      resources: [],
      findings: [],
      createdAt: timestamp,
      updatedAt: timestamp
    };
    const projects = read();
    projects.push(project);
    write(projects);
    return cloneJSON(project);
  }

  function update(id, input) {
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    const current = projects[index];
    projects[index] = {
      ...current,
      name: input?.name === undefined ? current.name : requiredText(input.name, "project.name", 120),
      objective: input?.objective === undefined ? current.objective : optionalText(input.objective, "project.objective", 1200),
      description: input?.description === undefined ? current.description : optionalText(input.description, "project.description", 4000),
      nextStep: input?.nextStep === undefined ? current.nextStep : optionalText(input.nextStep, "project.nextStep", 1200),
      stage: input?.stage === undefined ? current.stage : requiredStage(input.stage),
      updatedAt: new Date(now()).toISOString()
    };
    write(projects);
    return cloneJSON(projects[index]);
  }

  function archive(id) {
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    projects[index] = { ...projects[index], status: "archived", updatedAt: new Date(now()).toISOString() };
    write(projects);
    return cloneJSON(projects[index]);
  }

  function restore(id) {
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    projects[index] = { ...projects[index], status: "active", updatedAt: new Date(now()).toISOString() };
    write(projects);
    return cloneJSON(projects[index]);
  }

  function attachResource(id, input) {
    const timestamp = new Date(now()).toISOString();
    const resource = normalizeResource({ ...input, linkedAt: input?.linkedAt || timestamp }, timestamp);
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    const resources = projects[index].resources.filter((item) => !(item.id === resource.id && item.source === resource.source));
    projects[index] = {
      ...projects[index],
      resources: [...resources, resource],
      updatedAt: timestamp
    };
    write(projects);
    return cloneJSON(projects[index]);
  }

  function detachResource(id, resourceID, source) {
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    projects[index] = {
      ...projects[index],
      resources: projects[index].resources.filter((item) => !(item.id === resourceID && item.source === source)),
      updatedAt: new Date(now()).toISOString()
    };
    write(projects);
    return cloneJSON(projects[index]);
  }

  function addFinding(id, input) {
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    const timestamp = new Date(now()).toISOString();
    const finding = normalizeFinding({
      id: findingID(now, randomID),
      type: input?.type,
      title: input?.title,
      content: input?.content,
      createdAt: timestamp,
      updatedAt: timestamp
    });
    projects[index] = {
      ...projects[index],
      findings: [...projects[index].findings, finding],
      updatedAt: timestamp
    };
    write(projects);
    return cloneJSON(finding);
  }

  function updateFinding(id, findingIDValue, input) {
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    const findingIndex = projects[index].findings.findIndex((item) => item.id === findingIDValue);
    if (findingIndex < 0) throw new ResearchProjectRepositoryError("finding_not_found", `finding ${findingIDValue} was not found`);
    const timestamp = new Date(now()).toISOString();
    const current = projects[index].findings[findingIndex];
    const finding = normalizeFinding({
      ...current,
      type: input?.type === undefined ? current.type : input.type,
      title: input?.title === undefined ? current.title : input.title,
      content: input?.content === undefined ? current.content : input.content,
      updatedAt: timestamp
    });
    const findings = [...projects[index].findings];
    findings[findingIndex] = finding;
    projects[index] = { ...projects[index], findings, updatedAt: timestamp };
    write(projects);
    return cloneJSON(finding);
  }

  function removeFinding(id, findingIDValue) {
    const projects = read();
    const index = projects.findIndex((item) => item.id === id);
    if (index < 0) throw new ResearchProjectRepositoryError("project_not_found", `project ${id} was not found`);
    if (!projects[index].findings.some((item) => item.id === findingIDValue)) {
      throw new ResearchProjectRepositoryError("finding_not_found", `finding ${findingIDValue} was not found`);
    }
    projects[index] = {
      ...projects[index],
      findings: projects[index].findings.filter((item) => item.id !== findingIDValue),
      updatedAt: new Date(now()).toISOString()
    };
    write(projects);
    return true;
  }

  return Object.freeze({
    key,
    list,
    get,
    create,
    update,
    archive,
    restore,
    attachResource,
    detachResource,
    addFinding,
    updateFinding,
    removeFinding
  });
}

function stageLabel(stage) {
  return RESEARCH_STAGE_OPTIONS.find((option) => option.id === stage)?.label || stage || "研究规划";
}

function findingTypeLabel(type) {
  return RESEARCH_FINDING_TYPE_OPTIONS.find((option) => option.id === type)?.label || type || "研究结论";
}

export function projectBriefMarkdown(project) {
  const lines = [
    `# ${project.name}`,
    "",
    `阶段：${stageLabel(project.stage)}`,
    "",
    "## 研究目标",
    "",
    project.objective || "未填写",
    "",
    "## 项目说明",
    "",
    project.description || "未填写",
    "",
    "## 下一步",
    "",
    project.nextStep || "未填写",
    "",
    "## 研究发现",
    ""
  ];
  if (!project.findings?.length) lines.push("暂无研究发现");
  else project.findings.forEach((finding) => {
    lines.push(`### ${finding.title}`, "", `类型：${findingTypeLabel(finding.type)}`, "", finding.content || "未填写", "");
  });
  lines.push(
    "## 关联成果",
    ""
  );
  if (!project.resources?.length) lines.push("暂无关联成果");
  else project.resources.forEach((resource) => lines.push(`- ${resource.title} (${resource.type})`));
  lines.push("", `更新时间：${project.updatedAt}`, "");
  return lines.join("\n");
}
