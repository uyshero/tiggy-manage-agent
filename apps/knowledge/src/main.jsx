import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Bot,
  Check,
  Copy,
  Database,
  ExternalLink,
  FileText,
  Globe2,
  Link2,
  Loader2,
  Lock,
  MessageSquare,
  Plus,
  RefreshCw,
  Search,
  Send,
  ShieldAlert,
  Trash2,
  Upload
} from "lucide-react";
import * as api from "./api.js";
import "./styles.css";

const expiryOptions = [
  ["1d", "1天"],
  ["7d", "7天"],
  ["1m", "1个月"],
  ["1y", "1年"],
  ["permanent", "永久"]
];

const emptyServiceDraft = { name: "", scenario: "", system_prompt: "", knowledge_base_ids: [], knowledge_document_ids: [], allow_web_search: true, sensitive_terms: "" };

function serviceToDraft(service) {
  if (!service) return { ...emptyServiceDraft, knowledge_base_ids: [], knowledge_document_ids: [] };
  return {
    name: service.name || "",
    scenario: service.scenario || "",
    system_prompt: service.system_prompt || "",
    knowledge_base_ids: service.knowledge_base_ids || [],
    knowledge_document_ids: service.knowledge_document_ids || [],
    allow_web_search: !!service.allow_web_search,
    sensitive_terms: (service.sensitive_terms || []).join("\n")
  };
}

function draftPayload(draft) {
  return {
    ...draft,
    allow_web_search: !!draft.allow_web_search,
    knowledge_document_ids: draft.knowledge_document_ids || [],
    sensitive_terms: draft.sensitive_terms.split(/[,，\n]/).map((item) => item.trim()).filter(Boolean)
  };
}

function formatTime(value) {
  if (!value) return "永久";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "未知";
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function formatSize(value) {
  const bytes = Number(value || 0);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function IconButton({ label, children, ...props }) {
  return <button className="icon-button" type="button" title={label} aria-label={label} {...props}>{children}</button>;
}

function Empty({ icon: Icon, title, detail }) {
  return <div className="empty"><Icon size={28} aria-hidden="true" /><strong>{title}</strong><span>{detail}</span></div>;
}

function Shell({ children }) {
  return (
    <div className="knowledge-shell">
      <header className="topbar">
        <div className="brand">
          <Database size={20} aria-hidden="true" />
          <h1>知识库服务</h1>
        </div>
      </header>
      {children}
    </div>
  );
}

function ShareApp() {
  const token = decodeURIComponent(window.location.pathname.replace(/^\/share\//, ""));
  const [state, setState] = useState({ loading: true, share: null, service: null, error: "" });
  const [question, setQuestion] = useState("");
  const [messages, setMessages] = useState([]);
  const [asking, setAsking] = useState(false);

  useEffect(() => {
    let active = true;
    api.getPublicShare(token).then((result) => {
      if (active) setState({ loading: false, share: result.share, service: result.service, error: "" });
    }).catch((error) => {
      if (active) setState({ loading: false, share: null, service: null, error: error.message });
    });
    return () => { active = false; };
  }, [token]);

  const ask = useCallback(async () => {
    const text = question.trim();
    if (!text || asking) return;
    setQuestion("");
    setMessages((items) => [...items, { role: "user", text }]);
    setAsking(true);
    try {
      const response = await api.askPublicShare(token, text);
      setMessages((items) => [...items, { role: "assistant", text: response.answer, refused: response.refused, sources: response.sources || [] }]);
    } catch (error) {
      setMessages((items) => [...items, { role: "assistant", text: error.message, refused: true }]);
    } finally {
      setAsking(false);
    }
  }, [asking, question, token]);

  return (
    <Shell>
      <main className="share-layout">
        <section className="share-panel">
          {state.loading ? <Empty icon={Loader2} title="正在加载服务" detail="请稍候" /> : state.error ? <Empty icon={Lock} title="分享不可用" detail={state.error} /> : (
            <>
              <div className="share-head">
                <div className="share-title">
                  <h2>{state.service?.name || "知识库服务"}</h2>
                  {state.service?.scenario ? <p>{state.service.scenario}</p> : null}
                </div>
                <span className="share-badge">{state.service?.allow_web_search ? <Globe2 size={15} /> : <Search size={15} />}{state.service?.allow_web_search ? "知识库 + 联网" : "知识库检索"}</span>
              </div>
              <div className="chat-log">
                {messages.length ? messages.map((message, index) => (
                  <div className={`message ${message.role}`} key={index}>
                    <p>{message.text}</p>
                    {message.sources?.length ? <SourceList sources={message.sources} compact /> : null}
                  </div>
                )) : <Empty icon={MessageSquare} title="直接提问" detail="无需登录，问题会被限制在服务场景内。" />}
                {asking ? <div className="message assistant loading"><Loader2 className="spin" size={16} />正在检索和回答</div> : null}
              </div>
              <div className="composer">
                <textarea value={question} onChange={(event) => setQuestion(event.target.value)} onKeyDown={(event) => {
                  if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) ask();
                }} placeholder="输入问题" />
                <button type="button" onClick={ask} disabled={!question.trim() || asking}><Send size={16} />发送</button>
              </div>
            </>
          )}
        </section>
      </main>
    </Shell>
  );
}

function AdminApp() {
  const [bases, setBases] = useState([]);
  const [services, setServices] = useState([]);
  const [documents, setDocuments] = useState([]);
  const [documentsByBase, setDocumentsByBase] = useState({});
  const [shares, setShares] = useState([]);
  const [activeBaseID, setActiveBaseID] = useState("");
  const [activeServiceID, setActiveServiceID] = useState("");
  const [baseDraft, setBaseDraft] = useState({ name: "", description: "" });
  const [serviceDraft, setServiceDraft] = useState(emptyServiceDraft);
  const [serviceEditDraft, setServiceEditDraft] = useState(emptyServiceDraft);
  const [question, setQuestion] = useState("");
  const [answer, setAnswer] = useState(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [createdShare, setCreatedShare] = useState(null);
  const [activePanel, setActivePanel] = useState("bases");
  const [activeServiceTab, setActiveServiceTab] = useState("config");
  const [serviceMode, setServiceMode] = useState("detail");
  const fileInput = useRef(null);

  const activeBase = bases.find((item) => item.id === activeBaseID) || null;
  const activeService = services.find((item) => item.id === activeServiceID) || null;

  const load = useCallback(async () => {
    setBusy("load");
    setError("");
    try {
      const [baseResult, serviceResult] = await Promise.all([api.listBases(), api.listServices()]);
      setBases(baseResult.knowledge_bases || []);
      setServices(serviceResult.services || []);
      setActiveBaseID((current) => current || baseResult.knowledge_bases?.[0]?.id || "");
      setActiveServiceID((current) => current || serviceResult.services?.[0]?.id || "");
    } catch (loadError) {
      setError(loadError.message);
    } finally {
      setBusy("");
    }
  }, []);

  useEffect(() => { load(); }, [load]);

  useEffect(() => {
    if (!activeBaseID) {
      setDocuments([]);
      return;
    }
    let active = true;
    api.listDocuments(activeBaseID).then((result) => {
      if (!active) return;
      const nextDocuments = result.documents || [];
      setDocuments(nextDocuments);
      setDocumentsByBase((current) => ({ ...current, [activeBaseID]: nextDocuments }));
    }).catch((loadError) => setError(loadError.message));
    return () => { active = false; };
  }, [activeBaseID]);

  useEffect(() => {
    if (!bases.length) {
      setDocumentsByBase({});
      return;
    }
    let active = true;
    Promise.all(bases.map((base) => api.listDocuments(base.id).then((result) => [base.id, result.documents || []]))).then((entries) => {
      if (!active) return;
      setDocumentsByBase(Object.fromEntries(entries));
      if (activeBaseID) {
        const current = entries.find(([baseID]) => baseID === activeBaseID);
        if (current) setDocuments(current[1]);
      }
    }).catch((loadError) => setError(loadError.message));
    return () => { active = false; };
  }, [bases, activeBaseID]);

  useEffect(() => {
    if (!activeServiceID) {
      setShares([]);
      return;
    }
    let active = true;
    api.listShares(activeServiceID).then((result) => { if (active) setShares(result.shares || []); }).catch((loadError) => setError(loadError.message));
    return () => { active = false; };
  }, [activeServiceID, createdShare]);

  const baseOptions = useMemo(() => bases.map((base) => ({ id: base.id, name: base.name })), [bases]);

  useEffect(() => { setServiceEditDraft(serviceToDraft(activeService)); }, [activeService]);

  const createBase = async () => {
    if (!baseDraft.name.trim()) return;
    setBusy("base");
    setError("");
    try {
      const created = await api.createBase(baseDraft);
      setBases((items) => [created, ...items]);
      setActiveBaseID(created.id);
      setBaseDraft({ name: "", description: "" });
    } catch (createError) {
      setError(createError.message);
    } finally {
      setBusy("");
    }
  };

  const deleteBase = async () => {
    if (!activeBase) return;
    const usedBy = services.filter((service) => service.knowledge_base_ids?.includes(activeBase.id)).map((service) => service.name);
    const warning = usedBy.length ? `\n\n注意：以下服务已绑定这个知识库，删除后这些服务将无法再检索该知识库：\n${usedBy.join("、")}` : "";
    if (!window.confirm(`确定删除知识库「${activeBase.name}」吗？知识库下的文件和切片会一起删除。${warning}`)) return;
    setBusy("delete-base");
    setError("");
    try {
      await api.deleteBase(activeBase.id);
      const remaining = bases.filter((base) => base.id !== activeBase.id);
      setBases(remaining);
      setActiveBaseID(remaining[0]?.id || "");
      setDocuments([]);
      setServiceDraft((draft) => ({ ...draft, knowledge_base_ids: draft.knowledge_base_ids.filter((id) => id !== activeBase.id), knowledge_document_ids: draft.knowledge_document_ids.filter((id) => !documents.some((document) => document.id === id)) }));
      setServiceEditDraft((draft) => ({ ...draft, knowledge_base_ids: draft.knowledge_base_ids.filter((id) => id !== activeBase.id), knowledge_document_ids: draft.knowledge_document_ids.filter((id) => !documents.some((document) => document.id === id)) }));
      await load();
    } catch (deleteError) {
      setError(deleteError.message);
    } finally {
      setBusy("");
    }
  };

  const upload = async (files) => {
    const file = files?.[0];
    if (!file || !activeBaseID) return;
    setBusy("upload");
    setError("");
    try {
      await api.uploadDocument(activeBaseID, file);
      const result = await api.listDocuments(activeBaseID);
      const nextDocuments = result.documents || [];
      setDocuments(nextDocuments);
      setDocumentsByBase((current) => ({ ...current, [activeBaseID]: nextDocuments }));
      await load();
    } catch (uploadError) {
      setError(uploadError.message);
    } finally {
      setBusy("");
      if (fileInput.current) fileInput.current.value = "";
    }
  };

  const createService = async () => {
    if (!serviceDraft.name.trim() || !serviceDraft.scenario.trim()) return;
    setBusy("service");
    setError("");
    try {
      const created = await api.createService({
        ...draftPayload(serviceDraft)
      });
      setServices((items) => [created, ...items]);
      setActiveServiceID(created.id);
      setServiceMode("detail");
      setActiveServiceTab("config");
      setServiceDraft({ ...emptyServiceDraft, knowledge_base_ids: [], knowledge_document_ids: [] });
    } catch (createError) {
      setError(createError.message);
    } finally {
      setBusy("");
    }
  };

  const updateService = async () => {
    if (!activeServiceID || !serviceEditDraft.name.trim() || !serviceEditDraft.scenario.trim()) return;
    setBusy("update-service");
    setError("");
    try {
      const updated = await api.updateService(activeServiceID, draftPayload(serviceEditDraft));
      setServices((items) => items.map((item) => item.id === updated.id ? updated : item));
      setActiveServiceID(updated.id);
      setServiceEditDraft(serviceToDraft(updated));
    } catch (updateError) {
      setError(updateError.message);
    } finally {
      setBusy("");
    }
  };

  const deleteService = async () => {
    if (!activeService) return;
    if (!window.confirm(`确定删除对话服务「${activeService.name}」吗？它的分享链接也会一起失效。`)) return;
    setBusy("delete-service");
    setError("");
    try {
      await api.deleteService(activeService.id);
      const remaining = services.filter((service) => service.id !== activeService.id);
      setServices(remaining);
      setActiveServiceID(remaining[0]?.id || "");
      setShares([]);
      setAnswer(null);
      setCreatedShare(null);
    } catch (deleteError) {
      setError(deleteError.message);
    } finally {
      setBusy("");
    }
  };

  const ask = async () => {
    if (!activeServiceID || !question.trim()) return;
    setBusy("ask");
    setAnswer(null);
    setError("");
    try {
      const result = await api.askService(activeServiceID, question);
      setAnswer(result);
    } catch (askError) {
      setError(askError.message);
    } finally {
      setBusy("");
    }
  };

  const createShare = async (expiresIn) => {
    if (!activeServiceID) return;
    setBusy("share");
    setError("");
    try {
      const result = await api.createShare(activeServiceID, expiresIn);
      setCreatedShare(result);
    } catch (shareError) {
      setError(shareError.message);
    } finally {
      setBusy("");
    }
  };

  return (
    <Shell>
      <main className="knowledge-admin">
        {error ? <div className="error"><ShieldAlert size={16} />{error}</div> : null}
        <section className="knowledge-layout">
          <aside className="module-sidebar" aria-label="知识库服务模块">
            {[
              ["bases", Database, "知识库", `${bases.length} 个知识库`],
              ["services", Bot, "对话服务", `${services.length} 个服务`]
            ].map(([key, Icon, label, detail]) => (
              <button type="button" key={key} className={activePanel === key ? "module-tab active" : "module-tab"} onClick={() => setActivePanel(key)}>
                <Icon size={18} aria-hidden="true" />
                <span>{label}</span>
                <small>{detail}</small>
              </button>
            ))}
            <IconButton label="刷新" onClick={load}><RefreshCw size={16} className={busy === "load" ? "spin" : ""} /></IconButton>
          </aside>

          <section className="knowledge-content">
            {activePanel === "bases" ? (
              <section className="workspace-grid">
                <aside className="panel picker-panel">
                  <SectionTitle icon={Database} title="选择知识库" />
                  <div className="create-row">
                    <input value={baseDraft.name} onChange={(event) => setBaseDraft({ ...baseDraft, name: event.target.value })} placeholder="新知识库名称" />
                    <IconButton label="新建知识库" onClick={createBase} disabled={busy === "base"}><Plus size={16} /></IconButton>
                  </div>
                  <textarea className="small-textarea" value={baseDraft.description} onChange={(event) => setBaseDraft({ ...baseDraft, description: event.target.value })} placeholder="描述，可选" />
                  <div className="list">
                    {bases.map((base) => <button className={base.id === activeBaseID ? "list-item active" : "list-item"} key={base.id} onClick={() => setActiveBaseID(base.id)}><strong>{base.name}</strong><span>{base.document_count || 0} 个文件</span></button>)}
                  </div>
                </aside>
                <section className="panel">
                  <SectionTitle icon={FileText} title={activeBase ? activeBase.name : "文件管理"} action={(
                    <div className="section-actions">
                      <button type="button" className="secondary danger" onClick={deleteBase} disabled={!activeBaseID || busy === "delete-base"}><Trash2 size={16} />删除知识库</button>
                      <button type="button" className="secondary" onClick={() => fileInput.current?.click()} disabled={!activeBaseID || busy === "upload"}><Upload size={16} />上传文件</button>
                    </div>
                  )} />
                  <p className="panel-hint">上传文件后会自动解析、切片、向量化，并参与当前知识库检索。</p>
                  <input ref={fileInput} className="hidden" type="file" onChange={(event) => upload(event.target.files)} />
                  {documents.length ? <div className="document-table">
                    {documents.map((document) => (
                      <div className="document-row" key={document.id}>
                        <FileText size={16} />
                        <div><strong>{document.name}</strong><span>{document.content_type || "text"} · {formatSize(document.size_bytes)} · {document.chunk_count} 块</span></div>
                        <span className="status">{document.status}</span>
                        <IconButton label="删除文件" onClick={async () => {
                          await api.deleteDocument(document.id);
                          setDocuments((items) => items.filter((item) => item.id !== document.id));
                          setDocumentsByBase((current) => ({ ...current, [document.knowledge_base_id]: (current[document.knowledge_base_id] || []).filter((item) => item.id !== document.id) }));
                          setServiceDraft((draft) => ({ ...draft, knowledge_document_ids: draft.knowledge_document_ids.filter((id) => id !== document.id) }));
                          setServiceEditDraft((draft) => ({ ...draft, knowledge_document_ids: draft.knowledge_document_ids.filter((id) => id !== document.id) }));
                        }}><Trash2 size={15} /></IconButton>
                      </div>
                    ))}
                  </div> : <Empty icon={Upload} title="还没有文件" detail="上传 txt、md、csv、json、html、xml、docx 或 pdf。" />}
                </section>
              </section>
            ) : null}

            {activePanel === "services" ? (
              <section className="workspace-grid">
            <aside className="panel picker-panel">
              <SectionTitle icon={Bot} title="已有服务" />
              <div className="list">
                {services.map((service) => <button className={serviceMode !== "create" && service.id === activeServiceID ? "list-item active" : "list-item"} key={service.id} onClick={() => {
                  setActiveServiceID(service.id);
                  setServiceMode("detail");
                  setActiveServiceTab("config");
                }}><strong>{service.name}</strong><span>{service.allow_web_search ? "知识库 + 联网" : "仅知识库"}</span></button>)}
              </div>
              <button type="button" className={serviceMode === "create" ? "add-service-button active" : "add-service-button"} onClick={() => {
                setServiceMode("create");
                setActiveServiceTab("config");
                setAnswer(null);
                setCreatedShare(null);
              }}><Plus size={16} />创建新服务</button>
              {serviceMode !== "create" && !activeService ? <Empty icon={Bot} title="还没有服务" detail="点击上方 + 创建一个服务。" /> : null}
            </aside>
            <section className="service-editor-stack">
              {serviceMode === "create" ? (
                <div className="panel">
                  <SectionTitle icon={Plus} title="创建新服务" action={activeService ? (
                    <button type="button" className="secondary" onClick={() => setServiceMode("detail")}>取消</button>
                  ) : null} />
                  <p className="panel-hint">先定义主要场景，再绑定知识库；开启联网后，知识库没命中也可用网上搜索回答。</p>
                  <div className="service-form">
                    <input value={serviceDraft.name} onChange={(event) => setServiceDraft({ ...serviceDraft, name: event.target.value })} placeholder="服务名称" />
                    <textarea value={serviceDraft.scenario} onChange={(event) => setServiceDraft({ ...serviceDraft, scenario: event.target.value })} placeholder="主要场景，例如：回答售后政策、内部制度、产品交付流程" />
                    <textarea value={serviceDraft.system_prompt} onChange={(event) => setServiceDraft({ ...serviceDraft, system_prompt: event.target.value })} placeholder="服务提示词，可选" />
                    <textarea value={serviceDraft.sensitive_terms} onChange={(event) => setServiceDraft({ ...serviceDraft, sensitive_terms: event.target.value })} placeholder="额外敏感词，逗号或换行分隔" />
                    <label className="toggle"><input type="checkbox" checked={!!serviceDraft.allow_web_search} onChange={(event) => setServiceDraft({ ...serviceDraft, allow_web_search: event.target.checked })} /><Globe2 size={15} />允许联网搜索</label>
                    <KnowledgeScopeSelector bases={baseOptions} documentsByBase={documentsByBase} draft={serviceDraft} onChange={setServiceDraft} />
                    <button type="button" className="primary" onClick={createService} disabled={busy === "service" || !serviceDraft.name.trim() || !serviceDraft.scenario.trim()}><Plus size={16} />创建服务</button>
                  </div>
                </div>
              ) : (
                <div className="panel">
                <SectionTitle icon={Bot} title={activeService ? activeService.name : "服务详情"} />
                {activeService ? (
                  <>
                    <div className="service-subtabs" aria-label="对话服务子功能">
                      {[
                        ["config", Bot, "配置"],
                        ["shares", Link2, "分享链接"],
                        ["debug", MessageSquare, "调试问答"]
                      ].map(([key, Icon, label]) => (
                        <button type="button" key={key} className={activeServiceTab === key ? "service-subtab active" : "service-subtab"} onClick={() => setActiveServiceTab(key)}>
                          <Icon size={15} aria-hidden="true" />{label}
                        </button>
                      ))}
                    </div>

                    {activeServiceTab === "config" ? (
                      <div className="service-tab-panel">
                        <p className="panel-hint">在这里查看并修改服务配置；保存后分享链接和调试问答都会使用新配置。</p>
                        <div className="service-form">
                          <input value={serviceEditDraft.name} onChange={(event) => setServiceEditDraft({ ...serviceEditDraft, name: event.target.value })} placeholder="服务名称" />
                          <textarea value={serviceEditDraft.scenario} onChange={(event) => setServiceEditDraft({ ...serviceEditDraft, scenario: event.target.value })} placeholder="主要场景，例如：回答售后政策、内部制度、产品交付流程" />
                          <textarea value={serviceEditDraft.system_prompt} onChange={(event) => setServiceEditDraft({ ...serviceEditDraft, system_prompt: event.target.value })} placeholder="服务提示词，可选" />
                          <textarea value={serviceEditDraft.sensitive_terms} onChange={(event) => setServiceEditDraft({ ...serviceEditDraft, sensitive_terms: event.target.value })} placeholder="额外敏感词，逗号或换行分隔" />
                          <label className="toggle"><input type="checkbox" checked={!!serviceEditDraft.allow_web_search} onChange={(event) => setServiceEditDraft({ ...serviceEditDraft, allow_web_search: event.target.checked })} /><Globe2 size={15} />允许联网搜索</label>
                          <KnowledgeScopeSelector bases={baseOptions} documentsByBase={documentsByBase} draft={serviceEditDraft} onChange={setServiceEditDraft} />
                          <div className="service-actions">
                            <button type="button" className="secondary danger" onClick={deleteService} disabled={busy === "delete-service"}><Trash2 size={16} />删除服务</button>
                            <button type="button" className="primary" onClick={updateService} disabled={busy === "update-service" || !serviceEditDraft.name.trim() || !serviceEditDraft.scenario.trim()}><Check size={16} />保存修改</button>
                          </div>
                        </div>
                      </div>
                    ) : null}

                    {activeServiceTab === "shares" ? (
                      <div className="service-tab-panel">
                        <p className="panel-hint">生成免登录访问地址，可设置 1 天、7 天、1 个月、1 年或永久有效；分享历史也在这里管理。</p>
                        <ServiceSummary service={activeService} bases={bases} />
                        <div className="expiry-grid">
                          {expiryOptions.map(([value, label]) => <button type="button" key={value} onClick={() => createShare(value)} disabled={!activeServiceID || busy === "share"}>{label}</button>)}
                        </div>
                        {createdShare ? <ShareCreated result={createdShare} /> : null}
                        <div className="share-list">
                          {shares.map((share) => (
                            <ShareHistoryRow
                              key={share.id}
                              share={share}
                              onRevoke={async () => {
                                await api.revokeShare(share.id);
                                setShares((items) => items.map((item) => item.id === share.id ? { ...item, revoked_at: new Date().toISOString() } : item));
                              }}
                              onDelete={async () => {
                                await api.deleteShare(share.id);
                                setShares((items) => items.filter((item) => item.id !== share.id));
                              }}
                            />
                          ))}
                        </div>
                      </div>
                    ) : null}

                    {activeServiceTab === "debug" ? (
                      <div className="service-tab-panel qa-panel">
                        <p className="panel-hint">在发布分享前先测试场景限制、敏感词拦截、知识库检索和联网搜索效果。</p>
                        <ServiceSummary service={activeService} bases={bases} />
                        <textarea value={question} onChange={(event) => setQuestion(event.target.value)} placeholder="输入测试问题" />
                        <button type="button" className="primary wide" onClick={ask} disabled={!activeServiceID || !question.trim() || busy === "ask"}><Send size={16} />测试问答</button>
                        {answer ? <AnswerBlock answer={answer} /> : <Empty icon={MessageSquare} title="还没有测试结果" detail="输入问题后，可以验证服务是否只回答场景内且非敏感的问题。" />}
                      </div>
                    ) : null}
                  </>
                ) : <Empty icon={Bot} title="先选择一个服务" detail="左侧点击已有服务后，这里会显示详情和编辑表单。" />}
                </div>
              )}
            </section>
              </section>
            ) : null}
          </section>
        </section>
      </main>
    </Shell>
  );
}

function SectionTitle({ icon: Icon, title, action }) {
  return <div className="section-title"><div><Icon size={17} aria-hidden="true" /><h2>{title}</h2></div>{action}</div>;
}

function KnowledgeScopeSelector({ bases, documentsByBase, draft, onChange }) {
  const selectedBaseIDs = draft.knowledge_base_ids || [];
  const selectedDocumentIDs = draft.knowledge_document_ids || [];
  const selectedDocumentSet = new Set(selectedDocumentIDs);
  const documentCount = selectedDocumentIDs.length;
  const updateBase = (baseID, checked) => {
    const baseDocuments = documentsByBase[baseID] || [];
    const nextBaseIDs = checked ? [...selectedBaseIDs, baseID] : selectedBaseIDs.filter((id) => id !== baseID);
    const nextDocumentIDs = checked ? selectedDocumentIDs : selectedDocumentIDs.filter((id) => !baseDocuments.some((document) => document.id === id));
    onChange({ ...draft, knowledge_base_ids: Array.from(new Set(nextBaseIDs)), knowledge_document_ids: nextDocumentIDs });
  };
  const updateDocument = (baseID, documentID, checked) => {
    const nextBaseIDs = selectedBaseIDs.includes(baseID) ? selectedBaseIDs : [...selectedBaseIDs, baseID];
    const nextDocumentIDs = checked ? [...selectedDocumentIDs, documentID] : selectedDocumentIDs.filter((id) => id !== documentID);
    onChange({ ...draft, knowledge_base_ids: Array.from(new Set(nextBaseIDs)), knowledge_document_ids: Array.from(new Set(nextDocumentIDs)) });
  };
  return (
    <div className="scope-picker">
      <div className="scope-head">
        <strong>知识范围</strong>
        <span>{selectedBaseIDs.length} 个知识库 · {documentCount ? `${documentCount} 个指定文件` : "全部文件"}</span>
      </div>
      <p>先选择知识库；如需限定范围，再勾选具体文件。未勾选文件时，默认检索已选知识库下的全部文件。</p>
      <div className="scope-tree">
        {bases.map((base) => {
          const checked = selectedBaseIDs.includes(base.id);
          const docs = documentsByBase[base.id] || [];
          const selectedInBase = docs.filter((document) => selectedDocumentSet.has(document.id)).length;
          return (
            <div className={checked ? "scope-base active" : "scope-base"} key={base.id}>
              <label className="scope-base-label">
                <input type="checkbox" checked={checked} onChange={(event) => updateBase(base.id, event.target.checked)} />
                <span>{base.name}</span>
                <small>{docs.length ? `${selectedInBase || "全部"} / ${docs.length} 文件` : "暂无文件"}</small>
              </label>
              {checked ? (
                <div className="scope-docs">
                  {docs.length ? docs.map((document) => (
                    <label key={document.id}>
                      <input type="checkbox" checked={selectedDocumentSet.has(document.id)} onChange={(event) => updateDocument(base.id, document.id, event.target.checked)} />
                      <FileText size={14} aria-hidden="true" />
                      <span>{document.name}</span>
                      <small>{document.status}</small>
                    </label>
                  )) : <span className="scope-empty">这个知识库还没有文件</span>}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ServiceSummary({ service, bases }) {
  const names = service.knowledge_base_ids?.map((id) => bases.find((base) => base.id === id)?.name || id) || [];
  const documentCount = service.knowledge_document_ids?.length || 0;
  return (
    <div className="service-summary">
      <p>{service.scenario}</p>
      <div>{names.map((name) => <span key={name}>{name}</span>)}</div>
      {documentCount ? <small>限定 {documentCount} 个文件</small> : null}
      <small>{service.allow_web_search ? "知识库不足时允许联网搜索" : "仅使用知识库检索"}</small>
    </div>
  );
}

function AnswerBlock({ answer }) {
  return (
    <div className={`answer ${answer.refused ? "refused" : ""}`}>
      <p>{answer.answer}</p>
      <SourceList sources={(answer.sources || []).filter((source) => source.type !== "knowledge")} />
    </div>
  );
}

function SourceList({ sources, compact = false }) {
  const visibleSources = sources.filter((source) => source.type !== "knowledge");
  if (!visibleSources.length) return null;
  return <div className={compact ? "sources compact" : "sources"}>{visibleSources.slice(0, 6).map((source, index) => (
    <a key={`${source.type}-${index}`} href={source.url || undefined} target={source.url ? "_blank" : undefined} rel="noreferrer">
      <span>{source.type === "web" ? "Web" : "KB"}</span>
      <strong>{source.title || source.document_id || "资料"}</strong>
      {source.url ? <ExternalLink size={12} /> : null}
    </a>
  ))}</div>;
}

function ShareCreated({ result }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="created-share">
      <input readOnly value={result.share_url || ""} />
      <IconButton label="复制地址" onClick={async () => {
        await navigator.clipboard.writeText(result.share_url || "");
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1200);
      }}>{copied ? <Check size={16} /> : <Copy size={16} />}</IconButton>
      <a href={result.share_url} target="_blank" rel="noreferrer"><ExternalLink size={16} /></a>
    </div>
  );
}

function ShareHistoryRow({ share, onRevoke, onDelete }) {
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState("");
  const canOpen = !!share.share_url;
  return (
    <div className="share-row">
      <div className="share-meta">
        <strong>{share.revoked_at ? "已撤销" : "有效"}</strong>
        <span>到期：{formatTime(share.expires_at)} · 创建：{formatTime(share.created_at)}</span>
      </div>
      <div className="share-url">
        <input readOnly value={share.share_url || "旧分享记录未保存完整地址，无法恢复"} />
        <IconButton label="复制地址" onClick={async () => {
          if (!canOpen) return;
          await navigator.clipboard.writeText(share.share_url);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1200);
        }} disabled={!canOpen}>{copied ? <Check size={16} /> : <Copy size={16} />}</IconButton>
        <a className={canOpen ? "" : "disabled"} href={canOpen ? share.share_url : undefined} target="_blank" rel="noreferrer" aria-disabled={!canOpen}><ExternalLink size={16} /></a>
      </div>
      <div className="share-actions">
        {share.revoked_at ? (
          <button type="button" className="danger-text" onClick={async () => {
            if (!window.confirm("确定删除这条已撤销的分享记录吗？")) return;
            setBusy("delete");
            try {
              await onDelete();
            } finally {
              setBusy("");
            }
          }} disabled={busy === "delete"}>删除记录</button>
        ) : (
          <button type="button" onClick={async () => {
            setBusy("revoke");
            try {
              await onRevoke();
            } finally {
              setBusy("");
            }
          }} disabled={busy === "revoke"}>撤销</button>
        )}
      </div>
    </div>
  );
}

function App() {
  if (window.location.pathname.startsWith("/share/")) return <ShareApp />;
  return <AdminApp />;
}

createRoot(document.getElementById("root")).render(<App />);
