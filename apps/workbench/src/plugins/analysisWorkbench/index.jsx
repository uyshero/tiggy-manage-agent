import React, { useEffect, useMemo, useRef, useState } from "react";
import {
  Bot,
  Box,
  Check,
  ChevronDown,
  CircleAlert,
  CircleDot,
  Cloud,
  Code2,
  File,
  FileCode2,
  FileText,
  Folder,
  GitBranch,
  GitCommitHorizontal,
  LoaderCircle,
  MessageSquare,
  PanelLeft,
  Play,
  Plus,
  RefreshCw,
  Send,
  Server,
  Settings2,
  Sheet,
  Sparkles,
  TerminalSquare
} from "lucide-react";

import { createAnalysisWorkspaceRepository } from "./repository.js";
import "./styles.css";

const repositories = new Map();

function repositoryFor(scope) {
  const key = `${scope.workspaceId}:${scope.userId}`;
  if (!repositories.has(key)) {
    repositories.set(key, createAnalysisWorkspaceRepository({ storage: window.localStorage, scope }));
  }
  return repositories.get(key);
}

function projectForm() {
  return {
    title: "新建分析项目",
    description: "创建本地项目草稿；GitLab 和远程 Notebook 地址可在项目设置中补充。",
    schema: {
      type: "object",
      required: ["name"],
      properties: {
        name: { type: "string", title: "项目名称", description: "例如：肿瘤患者生存分析" },
        objective: { type: "string", format: "textarea", title: "分析目标" },
        repositoryPath: { type: "string", title: "GitLab 项目路径", description: "例如：survival-analysis" },
        gitlabURL: { type: "string", title: "GitLab 项目地址", description: "后端连接器创建项目后写入" },
        notebookURL: { type: "string", title: "JupyterLab 地址", description: "开发环境可使用 http://127.0.0.1:18888/lab" }
      }
    },
    initialValues: {},
    submitLabel: "创建项目"
  };
}

function settingsForm(project) {
  return {
    title: "项目连接设置",
    description: project.name,
    schema: {
      type: "object",
      properties: {
        gitlabURL: { type: "string", title: "GitLab 项目地址" },
        notebookURL: { type: "string", title: "JupyterLab 地址" }
      }
    },
    initialValues: {
      gitlabURL: project.gitlabURL || "",
      notebookURL: project.notebookURL || ""
    },
    submitLabel: "保存连接"
  };
}

function statusLabel(status) {
  if (status === "synced") return "GitLab 已同步";
  if (status === "syncing") return "正在同步";
  if (status === "error") return "同步失败";
  return "本地草稿";
}

function fileIcon(path, kind) {
  if (kind === "folder") return <Folder aria-hidden="true" />;
  if (path.endsWith(".ipynb")) return <FileCode2 aria-hidden="true" />;
  if (path.endsWith(".R")) return <Code2 aria-hidden="true" />;
  if (path.endsWith(".yml") || path.endsWith(".yaml")) return <Settings2 aria-hidden="true" />;
  if (path.endsWith(".md")) return <FileText aria-hidden="true" />;
  if (path.endsWith(".xlsx") || path.endsWith(".csv")) return <Sheet aria-hidden="true" />;
  return <File aria-hidden="true" />;
}

function basename(path) {
  return String(path || "").split("/").filter(Boolean).at(-1) || path;
}

function eventText(event) {
  const payload = event?.payload || {};
  if (Array.isArray(payload.content)) {
    return payload.content.map((item) => item?.text || item?.content || "").filter(Boolean).join("\n");
  }
  if (typeof payload.content === "string") return payload.content;
  return payload.message || payload.summary || payload.text || "";
}

function wait(milliseconds, signal) {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(resolve, milliseconds);
    signal?.addEventListener("abort", () => {
      window.clearTimeout(timer);
      reject(new DOMException("Aborted", "AbortError"));
    }, { once: true });
  });
}

async function waitForAgentReply(context, sessionID, runID, signal) {
  const session = encodeURIComponent(sessionID);
  const run = encodeURIComponent(runID);
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const [runState, eventList] = await Promise.all([
      context.http.request(`/v2/sessions/${session}/runs/${run}`, { signal }),
      context.http.request(`/v2/sessions/${session}/runs/${run}/events`, { signal })
    ]);
    const events = Array.isArray(eventList?.events) ? eventList.events : [];
    const reply = [...events].reverse().find((event) => event.type === "agent.message" && eventText(event).trim());
    if (reply) return eventText(reply).trim();
    if (["failed", "interrupted"].includes(runState?.status)) {
      throw new Error(runState.error_message || `Agent Run ${runState.status}`);
    }
    if (runState?.status === "completed") return "任务已完成，未返回可见文本。";
    await wait(1500, signal);
  }
  return "任务仍在运行，可在主工作台继续查看。";
}

function KMSurvivalChart() {
  return (
    <svg className="analysis-km-chart" viewBox="0 0 620 300" role="img" aria-labelledby="analysis-km-title analysis-km-desc">
      <title id="analysis-km-title">Kaplan-Meier 生存曲线示例输出</title>
      <desc id="analysis-km-desc">新治疗组的生存概率整体高于标准治疗组。</desc>
      {[50, 100, 150, 200, 250].map((y) => <line className="grid" x1="58" x2="596" y1={y} y2={y} key={y} />)}
      <line className="axis" x1="58" x2="596" y1="250" y2="250" />
      <line className="axis" x1="58" x2="58" y1="36" y2="250" />
      <text className="axis-label" x="8" y="22">生存概率</text>
      <text className="axis-label" x="314" y="290" textAnchor="middle">随访时间（月）</text>
      <text className="tick" x="48" y="254" textAnchor="end">0%</text>
      <text className="tick" x="48" y="204" textAnchor="end">25%</text>
      <text className="tick" x="48" y="154" textAnchor="end">50%</text>
      <text className="tick" x="48" y="104" textAnchor="end">75%</text>
      <text className="tick" x="48" y="54" textAnchor="end">100%</text>
      {[58, 192, 326, 460, 596].map((x, index) => <text className="tick" x={x} y="270" textAnchor="middle" key={x}>{index * 6}</text>)}
      <path className="curve primary" d="M58 50 H100 V57 H145 V66 H190 V77 H235 V91 H280 V106 H325 V120 H370 V140 H415 V158 H460 V178 H505 V194 H550 V211 H596" />
      <path className="curve secondary" d="M58 50 H95 V63 H130 V78 H170 V96 H210 V117 H250 V140 H290 V162 H330 V181 H375 V202 H420 V216 H470 V229 H520 V239 H560 V245 H596" />
      <text className="annotation" x="414" y="74">HR 0.68 · p = 0.021</text>
      <g className="legend" transform="translate(408 92)">
        <line className="curve primary" x1="0" x2="22" y1="0" y2="0" /><text x="30" y="4">新治疗组</text>
        <line className="curve secondary" x1="0" x2="22" y1="22" y2="22" /><text x="30" y="26">标准治疗组</text>
      </g>
    </svg>
  );
}

function NotebookPreview({ code, onCodeChange, onCodeSave, onOpenRuntime, runtimeAvailable }) {
  return (
    <div className="analysis-notebook" aria-label="Notebook 预览">
      <article className="analysis-notebook-cell markdown-cell">
        <div className="cell-gutter">MD</div>
        <div className="cell-content">
          <h2>治疗组总生存期比较</h2>
          <p>使用 Kaplan-Meier 方法估计生存函数，并通过 Cox 比例风险模型调整年龄和疾病分期。</p>
        </div>
      </article>

      <article className="analysis-notebook-cell code-cell">
        <div className="cell-gutter">R</div>
        <div className="cell-content">
          <div className="cell-toolbar">
            <span>In [1]</span>
            <button className="analysis-icon-button" type="button" disabled={!runtimeAvailable} onClick={onOpenRuntime} title={runtimeAvailable ? "在远程 JupyterLab 中运行" : "先配置远程 JupyterLab"} aria-label="运行代码单元">
              <Play aria-hidden="true" />
            </button>
          </div>
          <textarea className="analysis-code-editor" spellCheck="false" value={code} onChange={(event) => onCodeChange(event.target.value)} onBlur={onCodeSave} aria-label="R 代码" />
        </div>
      </article>

      <article className="analysis-notebook-cell output-cell">
        <div className="cell-gutter">Out</div>
        <div className="cell-content">
          <div className="analysis-output-heading">
            <span>已保存的示例输出</span>
            <span className="analysis-status neutral">未重新运行</span>
          </div>
          <KMSurvivalChart />
        </div>
      </article>

      <article className="analysis-notebook-cell output-cell compact-output">
        <div className="cell-gutter">Out</div>
        <div className="cell-content">
          <table className="analysis-model-table">
            <thead><tr><th>变量</th><th>HR</th><th>95% CI</th><th>p 值</th></tr></thead>
            <tbody>
              <tr><td>新治疗组</td><td>0.68</td><td>0.49–0.94</td><td>0.021</td></tr>
              <tr><td>年龄（每 10 岁）</td><td>1.14</td><td>0.98–1.32</td><td>0.087</td></tr>
              <tr><td>III 期 vs II 期</td><td>1.72</td><td>1.23–2.41</td><td>0.002</td></tr>
            </tbody>
          </table>
        </div>
      </article>
    </div>
  );
}

function RuntimeFrame({ project }) {
  if (!project.notebookURL) {
    return (
      <div className="analysis-runtime-empty">
        <Server aria-hidden="true" />
        <strong>远程 JupyterLab 未连接</strong>
        <span>项目设置中配置同源代理地址，或使用开发环境地址。</span>
        <code>http://127.0.0.1:18888/lab</code>
      </div>
    );
  }
  return (
    <iframe
      className="analysis-runtime-frame"
      src={project.notebookURL}
      title={`${project.name} JupyterLab`}
      sandbox="allow-same-origin allow-scripts allow-forms allow-downloads allow-popups"
    />
  );
}

export const plugin = {
  id: "com.tma.r-survival-workbench",
  activate(context) {
    const repository = repositoryFor(context.scope);
    context.commands.register("com.tma.r-survival-workbench.create-project", async (input) => repository.create(input));
  }
};

export function RSurvivalWorkbenchPage({ context }) {
  const repository = useMemo(() => repositoryFor(context.scope), [context]);
  const [projects, setProjects] = useState(() => {
    repository.ensureExample();
    return repository.list();
  });
  const [projectID, setProjectID] = useState(() => projects[0]?.id || "");
  const project = projects.find((item) => item.id === projectID) || projects[0] || null;
  const [selectedFile, setSelectedFile] = useState(() => project?.activeFile || "");
  const [code, setCode] = useState(() => project?.notebookCode || "");
  const [centerView, setCenterView] = useState("notebook");
  const [sessions, setSessions] = useState([]);
  const [sessionID, setSessionID] = useState("");
  const [sessionLoading, setSessionLoading] = useState(true);
  const [prompt, setPrompt] = useState("检查当前生存分析代码，并说明需要补充的统计检验");
  const [messages, setMessages] = useState([
    { id: "welcome", role: "assistant", text: "选择一个 TMA Session 后，可结合当前项目和 R 代码继续分析。" }
  ]);
  const [sending, setSending] = useState(false);
  const sendAbortRef = useRef(null);

  function refreshProjects(preferredID = projectID) {
    const next = repository.list();
    setProjects(next);
    if (preferredID) setProjectID(preferredID);
  }

  useEffect(() => {
    if (!project) return;
    setSelectedFile(project.activeFile);
    setCode(project.notebookCode);
  }, [project?.id]);

  useEffect(() => {
    let active = true;
    setSessionLoading(true);
    context.tasks.list({ workspaceId: context.scope.workspaceId, includeArchived: false, limit: 40 }).then((items) => {
      if (!active) return;
      const next = Array.isArray(items) ? items : [];
      setSessions(next);
      setSessionID((current) => current || next[0]?.id || "");
    }).catch((error) => {
      if (active) context.notifications.show({ level: "error", title: "任务加载失败", message: error.message || String(error) });
    }).finally(() => {
      if (active) setSessionLoading(false);
    });
    return () => {
      active = false;
      sendAbortRef.current?.abort();
    };
  }, [context]);

  async function createProject() {
    const values = await context.dialog.form(projectForm());
    if (!values) return;
    const created = await context.commands.execute("com.tma.r-survival-workbench.create-project", values);
    refreshProjects(created.id);
    setSelectedFile(created.activeFile);
    setCode(created.notebookCode);
    context.notifications.show({ level: "success", title: "分析项目已创建", message: created.name });
  }

  async function configureProject() {
    if (!project) return;
    const values = await context.dialog.form(settingsForm(project));
    if (!values) return;
    const updated = repository.update(project.id, values);
    refreshProjects(updated.id);
    context.notifications.show({ level: "success", title: "项目连接已保存", message: updated.name });
  }

  function selectFile(file) {
    if (file.kind === "folder" || !project) return;
    setSelectedFile(file.path);
    repository.update(project.id, { activeFile: file.path });
  }

  function saveCode() {
    if (!project || code === project.notebookCode) return;
    const updated = repository.update(project.id, { notebookCode: code });
    refreshProjects(updated.id);
  }

  async function refreshSessions() {
    setSessionLoading(true);
    try {
      const next = await context.tasks.list({ workspaceId: context.scope.workspaceId, includeArchived: false, limit: 40 });
      setSessions(Array.isArray(next) ? next : []);
      if (!sessionID && next?.[0]?.id) setSessionID(next[0].id);
    } finally {
      setSessionLoading(false);
    }
  }

  async function sendMessage(event) {
    event.preventDefault();
    const text = prompt.trim();
    if (!text || !sessionID || !project || sending) return;
    const userMessage = { id: `user-${Date.now()}`, role: "user", text };
    const pendingID = `assistant-${Date.now()}`;
    setMessages((current) => [...current, userMessage, { id: pendingID, role: "assistant", text: "正在分析…", pending: true }]);
    setPrompt("");
    setSending(true);
    sendAbortRef.current?.abort();
    const controller = new AbortController();
    sendAbortRef.current = controller;
    const contextualPrompt = [
      "[R 语言生存分析工作台上下文]",
      `项目：${project.name}`,
      `目标：${project.objective || "未填写"}`,
      `当前文件：${selectedFile || project.activeFile}`,
      "当前 R 代码：",
      code,
      "",
      `[用户请求] ${text}`
    ].join("\n");
    try {
      const started = await context.http.request(`/v2/sessions/${encodeURIComponent(sessionID)}/runs`, {
        method: "POST",
        signal: controller.signal,
        body: { input: { content: [{ type: "text", text: contextualPrompt }], attachments: [] } }
      });
      const reply = await waitForAgentReply(context, sessionID, started.run.id, controller.signal);
      setMessages((current) => current.map((message) => message.id === pendingID ? { ...message, text: reply, pending: false } : message));
    } catch (error) {
      if (error?.name === "AbortError") return;
      setMessages((current) => current.map((message) => message.id === pendingID ? { ...message, text: error.message || String(error), pending: false, error: true } : message));
    } finally {
      if (sendAbortRef.current === controller) sendAbortRef.current = null;
      setSending(false);
    }
  }

  if (!project) return <div className="analysis-workbench-empty">没有可用项目。</div>;

  return (
    <div className="analysis-workbench-page">
      <header className="analysis-workbench-toolbar">
        <div className="analysis-workbench-project-picker">
          <label htmlFor="analysis-project-select">项目</label>
          <div className="analysis-select-wrap">
            <select id="analysis-project-select" value={project.id} onChange={(event) => setProjectID(event.target.value)}>
              {projects.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}
            </select>
            <ChevronDown aria-hidden="true" />
          </div>
          <span className={`analysis-status ${project.gitStatus}`}>{statusLabel(project.gitStatus)}</span>
        </div>
        <div className="analysis-workbench-actions">
          <button className="secondary" type="button" onClick={configureProject}><Settings2 aria-hidden="true" />项目设置</button>
          <button type="button" onClick={createProject}><Plus aria-hidden="true" />新建项目</button>
        </div>
      </header>

      <div className="analysis-workbench-grid">
        <aside className="analysis-project-pane" aria-label="项目文件">
          <div className="analysis-pane-heading">
            <div><PanelLeft aria-hidden="true" /><strong>项目目录</strong></div>
            <button className="analysis-icon-button" type="button" onClick={() => context.notifications.show({ level: "info", title: "GitLab 同步", message: project.gitlabURL ? "GitLab Connector 将在后端阶段接管同步。" : "请先配置 GitLab 项目地址。" })} aria-label="刷新项目目录" title="刷新项目目录"><RefreshCw aria-hidden="true" /></button>
          </div>

          <div className="analysis-repository-meta">
            <GitBranch aria-hidden="true" />
            <span>{project.branch}</span>
            <code>{project.repositoryPath}</code>
          </div>

          <div className="analysis-file-tree" role="tree" aria-label="项目目录">
            {project.files.map((file) => {
              const depth = Math.max(0, file.path.split("/").length - 1);
              const active = file.path === selectedFile;
              return (
                <button
                  className={`analysis-file-row ${active ? "active" : ""}`}
                  style={{ "--analysis-file-depth": depth }}
                  type="button"
                  role="treeitem"
                  aria-selected={active}
                  onClick={() => selectFile(file)}
                  key={file.path}
                >
                  {fileIcon(file.path, file.kind)}
                  <span>{basename(file.path)}</span>
                  {file.status === "modified" ? <span className="analysis-file-change">M</span> : null}
                </button>
              );
            })}
          </div>

          <div className="analysis-git-summary">
            <div><GitCommitHorizontal aria-hidden="true" /><strong>待提交变更</strong><span>1</span></div>
            <p>Notebook 代码将在 GitLab Connector 接入后生成 Diff 和检查点。</p>
          </div>
        </aside>

        <main className="analysis-notebook-pane">
          <div className="analysis-pane-heading analysis-notebook-heading">
            <div className="analysis-view-tabs" role="tablist" aria-label="分析视图">
              <button className={centerView === "notebook" ? "active" : ""} type="button" role="tab" aria-selected={centerView === "notebook"} onClick={() => setCenterView("notebook")}><FileCode2 aria-hidden="true" />Notebook</button>
              <button className={centerView === "runtime" ? "active" : ""} type="button" role="tab" aria-selected={centerView === "runtime"} onClick={() => setCenterView("runtime")}><TerminalSquare aria-hidden="true" />JupyterLab</button>
            </div>
            <div className="analysis-runtime-state">
              {project.notebookURL ? <Check aria-hidden="true" /> : <CircleAlert aria-hidden="true" />}
              <span>{project.notebookURL ? "R Runtime 已配置" : "R Runtime 待连接"}</span>
            </div>
          </div>
          {centerView === "notebook" ? (
            <NotebookPreview
              code={code}
              onCodeChange={setCode}
              onCodeSave={saveCode}
              onOpenRuntime={() => setCenterView("runtime")}
              runtimeAvailable={Boolean(project.notebookURL)}
            />
          ) : <RuntimeFrame project={project} />}
        </main>

        <aside className="analysis-chat-pane" aria-label="AI 分析助手">
          <div className="analysis-pane-heading">
            <div><MessageSquare aria-hidden="true" /><strong>AI 分析助手</strong></div>
            <span className="analysis-status agent"><CircleDot aria-hidden="true" />TMA Agent</span>
          </div>

          <div className="analysis-session-picker">
            <label htmlFor="analysis-session-select">关联任务</label>
            <div>
              <select id="analysis-session-select" value={sessionID} disabled={sessionLoading} onChange={(event) => setSessionID(event.target.value)}>
                <option value="">{sessionLoading ? "正在加载…" : "选择 TMA Session"}</option>
                {sessions.map((session) => <option value={session.id} key={session.id}>{session.title || session.id}</option>)}
              </select>
              <button
                className="analysis-icon-button"
                type="button"
                disabled={sessionLoading}
                onClick={() => refreshSessions().catch((error) => context.notifications.show({
                  level: "error",
                  title: "刷新失败",
                  message: error.message || String(error)
                }))}
                aria-label="刷新任务"
                title="刷新任务"
              >
                <RefreshCw aria-hidden="true" />
              </button>
            </div>
          </div>

          <div className="analysis-chat-messages" aria-live="polite">
            {messages.map((message) => (
              <article className={`analysis-chat-message ${message.role} ${message.pending ? "pending" : ""} ${message.error ? "error" : ""}`} key={message.id}>
                <div>{message.role === "assistant" ? <Bot aria-hidden="true" /> : <Sparkles aria-hidden="true" />}<strong>{message.role === "assistant" ? "分析助手" : "你"}</strong></div>
                <p>{message.text}</p>
                {message.pending ? <LoaderCircle className="analysis-spin" aria-hidden="true" /> : null}
              </article>
            ))}
          </div>

          <form className="analysis-chat-composer" onSubmit={sendMessage}>
            <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="结合当前 Notebook 继续分析…" aria-label="发送给分析助手" />
            <div>
              <span><Cloud aria-hidden="true" />包含当前项目上下文</span>
              <button type="submit" disabled={!sessionID || !prompt.trim() || sending} aria-label="发送消息"><Send aria-hidden="true" /></button>
            </div>
          </form>
        </aside>
      </div>
    </div>
  );
}
