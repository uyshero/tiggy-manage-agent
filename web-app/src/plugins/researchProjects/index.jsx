import React, { useEffect, useMemo, useState } from "react";

import {
  createResearchProjectRepository,
  projectBriefMarkdown,
  RESEARCH_FINDING_TYPE_OPTIONS,
  RESEARCH_STAGE_OPTIONS
} from "./repository.js";
import { sessionSelectionOptions } from "./selectors.js";
import "./styles.css";

const repositories = new Map();
const filterOptions = Object.freeze([
  Object.freeze({ id: "active", label: "进行中" }),
  Object.freeze({ id: "archived", label: "已归档" }),
  Object.freeze({ id: "all", label: "全部" })
]);
const viewOptions = Object.freeze([
  Object.freeze({ id: "overview", label: "概览" }),
  Object.freeze({ id: "resources", label: "资料" }),
  Object.freeze({ id: "findings", label: "研究发现" })
]);

function repositoryFor(scope) {
  const key = `${scope.workspaceId}:${scope.userId}`;
  if (!repositories.has(key)) {
    repositories.set(key, createResearchProjectRepository({ storage: window.localStorage, scope }));
  }
  return repositories.get(key);
}

function projectForm(project) {
  return {
    title: project ? "编辑科研项目" : "新建科研项目",
    description: "明确目标和下一步，后续资料与发现都围绕该项目沉淀。",
    schema: {
      type: "object",
      required: ["name", "objective"],
      properties: {
        name: { type: "string", title: "项目名称", description: "例如：新能源材料技术路线调研" },
        objective: { type: "string", format: "textarea", title: "研究目标", description: "说明本项目需要回答的核心问题" },
        nextStep: { type: "string", format: "textarea", title: "下一步", description: "当前最需要推进的具体动作" },
        description: { type: "string", format: "textarea", title: "范围与说明", description: "边界、约束、方法或阶段安排" }
      }
    },
    initialValues: project ? {
      name: project.name,
      objective: project.objective,
      nextStep: project.nextStep,
      description: project.description
    } : {},
    submitLabel: project ? "保存修改" : "创建项目"
  };
}

function findingForm(finding) {
  const typeLabels = RESEARCH_FINDING_TYPE_OPTIONS.map((option) => option.label);
  const initialType = RESEARCH_FINDING_TYPE_OPTIONS.find((option) => option.id === finding?.type)?.label || typeLabels[0];
  return {
    title: finding ? "编辑研究发现" : "记录研究发现",
    description: "区分已形成的结论、仍待验证的假设和开放问题。",
    schema: {
      type: "object",
      required: ["type", "title"],
      properties: {
        type: { type: "string", title: "类型", enum: typeLabels },
        title: { type: "string", title: "标题", description: "用一句话概括" },
        content: { type: "string", format: "textarea", title: "内容与依据", description: "记录判断、证据、限制或需要补充的数据" }
      }
    },
    initialValues: {
      type: initialType,
      title: finding?.title || "",
      content: finding?.content || ""
    },
    submitLabel: finding ? "保存修改" : "记录发现"
  };
}

function formatDate(value) {
  try {
    return new Intl.DateTimeFormat("zh-CN", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false
    }).format(new Date(value));
  } catch {
    return value;
  }
}

function safeFilename(value) {
  return String(value || "research-project")
    .replace(/[\\/:*?"<>|]+/g, "-")
    .replace(/\s+/g, "-")
    .slice(0, 80);
}

function downloadMarkdown(project, markdown) {
  const blob = new Blob([markdown], { type: "text/markdown;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${safeFilename(project.name)}-摘要.md`;
  anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

function stageOption(stage) {
  return RESEARCH_STAGE_OPTIONS.find((option) => option.id === stage) || RESEARCH_STAGE_OPTIONS[0];
}

function findingTypeOption(type) {
  return RESEARCH_FINDING_TYPE_OPTIONS.find((option) => option.id === type) || RESEARCH_FINDING_TYPE_OPTIONS[0];
}

function sourceSessionID(resource) {
  const prefix = "tma.session-artifact:";
  return resource.source?.startsWith(prefix) ? resource.source.slice(prefix.length) : resource.source;
}

function resourceTypeLabel(type) {
  if (type === "file") return "文件";
  if (type === "artifact") return "任务产物";
  return type || "资源";
}

function projectActivity(project) {
  return [
    ...project.resources.map((resource) => ({
      id: `resource:${resource.source}:${resource.id}`,
      label: "关联资料",
      title: resource.title,
      time: resource.linkedAt || project.updatedAt
    })),
    ...project.findings.map((finding) => ({
      id: `finding:${finding.id}`,
      label: findingTypeOption(finding.type).label,
      title: finding.title,
      time: finding.updatedAt
    }))
  ].sort((left, right) => right.time.localeCompare(left.time)).slice(0, 6);
}

export const plugin = {
  id: "com.tma.research-projects",
  activate(context) {
    const repository = repositoryFor(context.scope);
    context.commands.register("com.tma.research-projects.create-project", async (input) => repository.create(input));
    context.commands.register("com.tma.research-projects.export-brief", async ({ projectID }) => (
      projectBriefMarkdown(repository.get(projectID))
    ));
  }
};

export function ResearchProjectsPage({ context }) {
  const repository = useMemo(() => repositoryFor(context.scope), [context]);
  const [allProjects, setAllProjects] = useState(() => repository.list({ includeArchived: true }));
  const [activeID, setActiveID] = useState(() => allProjects.find((project) => project.status === "active")?.id || allProjects[0]?.id || "");
  const [filter, setFilter] = useState("active");
  const [query, setQuery] = useState("");
  const [view, setView] = useState("overview");
  const [busy, setBusy] = useState("");

  const projects = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return allProjects.filter((project) => {
      if (filter !== "all" && project.status !== filter) return false;
      if (!normalized) return true;
      return `${project.name}\n${project.objective}\n${project.nextStep}`.toLocaleLowerCase().includes(normalized);
    });
  }, [allProjects, filter, query]);
  const activeProject = projects.find((project) => project.id === activeID) || projects[0] || null;
  const counts = useMemo(() => ({
    active: allProjects.filter((project) => project.status === "active").length,
    archived: allProjects.filter((project) => project.status === "archived").length
  }), [allProjects]);

  function refresh(preferredID = activeID) {
    const next = repository.list({ includeArchived: true });
    setAllProjects(next);
    if (preferredID) setActiveID(preferredID);
  }

  useEffect(() => {
    if (!projects.some((project) => project.id === activeID)) setActiveID(projects[0]?.id || "");
  }, [activeID, projects]);

  async function createProject() {
    const values = await context.dialog.form(projectForm());
    if (!values) return;
    setBusy("create");
    try {
      const project = await context.commands.execute("com.tma.research-projects.create-project", values);
      setFilter("active");
      setQuery("");
      setView("overview");
      refresh(project.id);
      context.notifications.show({
        level: "success",
        title: "科研项目已创建",
        message: project.name,
        dedupeKey: "research.projects.created"
      });
    } finally {
      setBusy("");
    }
  }

  async function editProject() {
    if (!activeProject) return;
    const values = await context.dialog.form(projectForm(activeProject));
    if (!values) return;
    repository.update(activeProject.id, values);
    refresh(activeProject.id);
    context.notifications.show({ level: "success", title: "项目已更新", message: values.name });
  }

  async function changeStage() {
    if (!activeProject || activeProject.status === "archived") return;
    const stage = await context.dialog.choice({
      title: "推进研究阶段",
      description: activeProject.name,
      items: RESEARCH_STAGE_OPTIONS.map((option, index) => ({
        value: option.id,
        label: option.label,
        description: `第 ${index + 1} 阶段`
      })),
      initialValue: activeProject.stage,
      searchable: false,
      submitLabel: "更新阶段"
    });
    if (!stage) return;
    repository.update(activeProject.id, { stage });
    refresh(activeProject.id);
    context.notifications.show({ level: "success", title: "研究阶段已更新", message: stageOption(stage).label });
  }

  async function archiveProject() {
    if (!activeProject) return;
    const confirmed = await context.dialog.confirm({
      title: "归档科研项目",
      description: activeProject.name,
      detail: "项目、资料和研究发现都会保留，可以随时恢复。",
      confirmLabel: "归档",
      tone: "warning"
    });
    if (!confirmed) return;
    repository.archive(activeProject.id);
    refresh("");
    context.notifications.show({ level: "success", title: "项目已归档", message: activeProject.name });
  }

  function restoreProject() {
    if (!activeProject) return;
    repository.restore(activeProject.id);
    setFilter("active");
    refresh(activeProject.id);
    context.notifications.show({ level: "success", title: "项目已恢复", message: activeProject.name });
  }

  async function attachArtifact() {
    if (!activeProject) return;
    setBusy("resources");
    try {
      const sessions = await context.tasks.list({ workspaceId: context.scope.workspaceId, limit: 50 });
      const sessionOptions = sessionSelectionOptions(sessions);
      if (!sessionOptions.length) {
        context.notifications.show({ level: "info", title: "当前工作区暂无任务" });
        return;
      }
      const sessionID = await context.dialog.choice({
        title: "选择任务",
        description: "从当前工作区最近任务中选择已有成果来源。",
        items: sessionOptions.map((option) => ({
          value: option.id,
          label: option.label,
          description: option.description,
          keywords: option.keywords
        })),
        initialValue: sessionOptions[0].id,
        searchable: true,
        searchPlaceholder: "搜索任务标题或 Session ID",
        emptyMessage: "没有匹配的任务",
        submitLabel: "查看成果"
      });
      if (!sessionID) return;
      const artifacts = await context.artifacts.list(sessionID);
      const resources = await context.resources.listRelated({ sessionID, artifacts });
      if (!resources.length) {
        context.notifications.show({ level: "info", title: "该任务暂无成果", message: sessionID });
        return;
      }
      const resourceID = await context.dialog.choice({
        title: "选择成果",
        description: sessionID,
        items: resources.map((resource) => ({
          value: resource.id,
          label: resource.title,
          description: `${resourceTypeLabel(resource.type)} · ${resource.id}`,
          keywords: `${resource.title} ${resource.type} ${resource.id}`
        })),
        initialValue: resources[0].id,
        searchable: true,
        searchPlaceholder: "搜索成果名称或 Artifact ID",
        emptyMessage: "没有匹配的成果",
        submitLabel: "关联到项目"
      });
      if (!resourceID) return;
      const resource = resources.find((item) => item.id === resourceID);
      if (!resource) throw new Error("选择的任务成果不存在");
      repository.attachResource(activeProject.id, resource);
      refresh(activeProject.id);
      setView("resources");
      context.notifications.show({ level: "success", title: "成果已关联", message: resource.title });
    } catch (error) {
      context.notifications.show({
        level: "error",
        title: "读取任务或成果失败",
        message: error.message || String(error),
        dedupeKey: "research.resources.failed"
      });
    } finally {
      setBusy("");
    }
  }

  async function openResource(resource) {
    try {
      await context.resources.open(resource);
    } catch (error) {
      context.notifications.show({ level: "error", title: "无法打开成果", message: error.message || String(error) });
    }
  }

  async function removeResource(resource) {
    if (!activeProject) return;
    const confirmed = await context.dialog.confirm({
      title: "移除关联成果",
      description: resource.title,
      detail: "只会移除项目关联，不会删除任务中的原始成果。",
      confirmLabel: "移除",
      tone: "warning"
    });
    if (!confirmed) return;
    repository.detachResource(activeProject.id, resource.id, resource.source);
    refresh(activeProject.id);
  }

  async function saveFinding(finding = null) {
    if (!activeProject || activeProject.status === "archived") return;
    const values = await context.dialog.form(findingForm(finding));
    if (!values) return;
    const type = RESEARCH_FINDING_TYPE_OPTIONS.find((option) => option.label === values.type)?.id || "finding";
    if (finding) repository.updateFinding(activeProject.id, finding.id, { ...values, type });
    else repository.addFinding(activeProject.id, { ...values, type });
    refresh(activeProject.id);
    context.notifications.show({ level: "success", title: finding ? "研究发现已更新" : "研究发现已记录", message: values.title });
  }

  async function removeFinding(finding) {
    if (!activeProject) return;
    const confirmed = await context.dialog.confirm({
      title: "删除研究发现",
      description: finding.title,
      detail: "该记录会从项目摘要中移除。",
      confirmLabel: "删除",
      tone: "danger"
    });
    if (!confirmed) return;
    repository.removeFinding(activeProject.id, finding.id);
    refresh(activeProject.id);
  }

  async function exportBrief() {
    if (!activeProject) return;
    const markdown = await context.commands.execute("com.tma.research-projects.export-brief", { projectID: activeProject.id });
    downloadMarkdown(activeProject, markdown);
    context.notifications.show({ level: "success", title: "项目摘要已导出", message: `${activeProject.name}.md` });
  }

  return (
    <div className="research-projects-page">
      <header className="research-projects-heading">
        <div>
          <span>Research Workspace</span>
          <h1>科研项目</h1>
          <p>{context.scope.workspaceId} · {counts.active} 个进行中项目</p>
        </div>
        <button type="button" onClick={createProject} disabled={busy === "create"}>新建项目</button>
      </header>

      <div className="research-projects-toolbar">
        <input aria-label="搜索科研项目" onChange={(event) => setQuery(event.target.value)} placeholder="搜索项目、目标或下一步" type="search" value={query} />
        <div aria-label="项目状态" className="research-filter-control" role="group">
          {filterOptions.map((option) => (
            <button className={filter === option.id ? "active" : ""} key={option.id} onClick={() => setFilter(option.id)} type="button">
              {option.label}<span>{option.id === "active" ? counts.active : option.id === "archived" ? counts.archived : allProjects.length}</span>
            </button>
          ))}
        </div>
      </div>

      {!projects.length ? (
        <section className="research-empty-state">
          <strong>{query ? "没有匹配的科研项目" : filter === "archived" ? "没有已归档项目" : "还没有科研项目"}</strong>
          <p>{query ? "调整搜索条件后重试。" : "创建项目并明确研究目标、下一步和资料来源。"}</p>
          {!query && filter !== "archived" ? <button type="button" onClick={createProject}>新建项目</button> : null}
        </section>
      ) : (
        <div className="research-projects-layout">
          <aside className="research-project-list" aria-label="科研项目列表">
            {projects.map((project) => (
              <button
                className={project.id === activeProject?.id ? "active" : ""}
                key={project.id}
                type="button"
                onClick={() => { setActiveID(project.id); setView("overview"); }}
              >
                <span className="research-project-list-copy">
                  <span><strong>{project.name}</strong><em>{stageOption(project.stage).label}</em></span>
                  <small>{project.nextStep || project.objective || "未填写下一步"}</small>
                  <span className="research-project-list-meta">{project.resources.length} 份资料 · {project.findings.length} 条发现</span>
                </span>
              </button>
            ))}
          </aside>

          {activeProject ? (
            <section className="research-project-detail" aria-label={activeProject.name}>
              <header className="research-project-detail-header">
                <div>
                  <span className={`research-status ${activeProject.status}`}>{activeProject.status === "archived" ? "已归档" : stageOption(activeProject.stage).label}</span>
                  <h2>{activeProject.name}</h2>
                  <small>更新于 {formatDate(activeProject.updatedAt)}</small>
                </div>
                <div className="research-project-actions">
                  {activeProject.status === "archived" ? (
                    <button type="button" onClick={restoreProject}>恢复项目</button>
                  ) : (
                    <button className="secondary" type="button" onClick={editProject}>编辑</button>
                  )}
                  <button className="secondary" type="button" onClick={exportBrief}>导出摘要</button>
                  {activeProject.status !== "archived" ? <button className="secondary" type="button" onClick={archiveProject}>归档</button> : null}
                </div>
              </header>

              <div aria-label="项目视图" className="research-view-tabs" role="tablist">
                {viewOptions.map((option) => (
                  <button
                    aria-selected={view === option.id}
                    className={view === option.id ? "active" : ""}
                    key={option.id}
                    onClick={() => setView(option.id)}
                    role="tab"
                    type="button"
                  >
                    {option.label}
                    {option.id === "resources" ? <span>{activeProject.resources.length}</span> : null}
                    {option.id === "findings" ? <span>{activeProject.findings.length}</span> : null}
                  </button>
                ))}
              </div>

              {view === "overview" ? (
                <div className="research-overview-view" role="tabpanel">
                  <section className="research-stage-section">
                    <header><div><h3>研究进度</h3><span>{stageOption(activeProject.stage).label}</span></div><button className="secondary" disabled={activeProject.status === "archived"} onClick={changeStage} type="button">调整阶段</button></header>
                    <div className="research-stage-track">
                      {RESEARCH_STAGE_OPTIONS.map((option, index) => {
                        const currentIndex = RESEARCH_STAGE_OPTIONS.findIndex((item) => item.id === activeProject.stage);
                        const state = index < currentIndex ? "complete" : index === currentIndex ? "current" : "pending";
                        return <div className={state} key={option.id}><span>{index + 1}</span><strong>{option.label}</strong></div>;
                      })}
                    </div>
                  </section>

                  <section className="research-summary-grid">
                    <div><span>资料</span><strong>{activeProject.resources.length}</strong><small>已关联任务成果</small></div>
                    <div><span>研究发现</span><strong>{activeProject.findings.length}</strong><small>结论、假设与问题</small></div>
                    <div><span>待验证</span><strong>{activeProject.findings.filter((finding) => finding.type === "question").length}</strong><small>仍需回答的问题</small></div>
                  </section>

                  <section className="research-overview-copy">
                    <div><span>研究目标</span><p>{activeProject.objective || "未填写"}</p></div>
                    <div className="research-next-step"><span>下一步</span><p>{activeProject.nextStep || "尚未设置下一步"}</p></div>
                    <div><span>范围与说明</span><p>{activeProject.description || "未填写"}</p></div>
                  </section>

                  <section className="research-activity-section">
                    <header><h3>最近沉淀</h3></header>
                    {projectActivity(activeProject).length ? (
                      <div className="research-activity-list">
                        {projectActivity(activeProject).map((item) => (
                          <div key={item.id}><span>{item.label}</span><strong>{item.title}</strong><time>{formatDate(item.time)}</time></div>
                        ))}
                      </div>
                    ) : <div className="research-section-empty">还没有资料或研究发现</div>}
                  </section>
                </div>
              ) : null}

              {view === "resources" ? (
                <section className="research-resource-section" role="tabpanel">
                  <header>
                    <div><h3>项目资料</h3><span>{activeProject.resources.length} 项</span></div>
                    <button type="button" onClick={attachArtifact} disabled={busy === "resources" || activeProject.status === "archived"}>
                      {busy === "resources" ? "读取中..." : "关联任务成果"}
                    </button>
                  </header>
                  {!activeProject.resources.length ? (
                    <div className="research-section-empty">暂无资料，先从已有任务中关联成果。</div>
                  ) : (
                    <div className="research-resource-list">
                      {activeProject.resources.map((resource) => (
                        <article key={`${resource.source}:${resource.id}`}>
                          <div>
                            <span className="research-resource-kind">{resourceTypeLabel(resource.type)}</span>
                            <strong>{resource.title}</strong>
                            <small>{sourceSessionID(resource)} · 关联于 {formatDate(resource.linkedAt)}</small>
                          </div>
                          <div>
                            <button className="secondary" type="button" onClick={() => openResource(resource)}>打开</button>
                            <button className="secondary" type="button" onClick={() => removeResource(resource)} disabled={activeProject.status === "archived"}>移除</button>
                          </div>
                        </article>
                      ))}
                    </div>
                  )}
                </section>
              ) : null}

              {view === "findings" ? (
                <section className="research-findings-section" role="tabpanel">
                  <header>
                    <div><h3>研究发现</h3><span>{activeProject.findings.length} 条</span></div>
                    <button type="button" onClick={() => saveFinding()} disabled={activeProject.status === "archived"}>记录发现</button>
                  </header>
                  {!activeProject.findings.length ? (
                    <div className="research-section-empty">暂无发现，记录结论、假设或待验证问题。</div>
                  ) : (
                    <div className="research-finding-list">
                      {activeProject.findings.map((finding) => (
                        <article key={finding.id}>
                          <header><span className={`research-finding-type ${finding.type}`}>{findingTypeOption(finding.type).label}</span><time>{formatDate(finding.updatedAt)}</time></header>
                          <h4>{finding.title}</h4>
                          <p>{finding.content || "未填写内容与依据"}</p>
                          <footer>
                            <button className="secondary" type="button" onClick={() => saveFinding(finding)} disabled={activeProject.status === "archived"}>编辑</button>
                            <button className="secondary" type="button" onClick={() => removeFinding(finding)} disabled={activeProject.status === "archived"}>删除</button>
                          </footer>
                        </article>
                      ))}
                    </div>
                  )}
                </section>
              ) : null}
            </section>
          ) : null}
        </div>
      )}
    </div>
  );
}
