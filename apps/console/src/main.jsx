import React, { useCallback, useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ArrowLeft, Building2, Check, ChevronRight, CircleAlert, Loader2, Plus, RefreshCw,
  ShieldCheck, Trash2, UserCog, Users, X
} from "lucide-react";
import * as api from "./api.js";
import "./styles.css";
import "./platform.css";

const roleLabels = { viewer: "只读", member: "成员", operator: "运维", admin: "管理员" };
const statusLabels = { invited: "待加入", active: "正常", suspended: "已停用" };
const emptyMember = { subject: "", display_name: "", email: "", role: "member", status: "active" };
const emptyAdmin = { subject: "", display_name: "", email: "" };

function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "-" : new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function IconButton({ label, children, ...props }) {
  return <button className="icon-button" type="button" title={label} aria-label={label} {...props}>{children}</button>;
}

function Notice({ error, onClose }) {
  if (!error) return null;
  return <div className="notice" role="alert"><CircleAlert size={17} /><span>{error}</span><IconButton label="关闭" onClick={onClose}><X size={16} /></IconButton></div>;
}

function Modal({ title, detail, children, onClose }) {
  return <div className="modal-backdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
    <section className="modal" role="dialog" aria-modal="true" aria-label={title}>
      <header><div><h2>{title}</h2>{detail ? <p>{detail}</p> : null}</div><IconButton label="关闭" onClick={onClose}><X size={17} /></IconButton></header>
      {children}
    </section>
  </div>;
}

function Field({ label, children }) {
  return <label className="field"><span>{label}</span>{children}</label>;
}

function MemberForm({ initial = emptyMember, lockedSubject = false, onCancel, onSubmit, busy }) {
  const [draft, setDraft] = useState({ ...emptyMember, ...initial });
  const change = (key) => (event) => setDraft((value) => ({ ...value, [key]: event.target.value }));
  return <form onSubmit={(event) => { event.preventDefault(); onSubmit(draft); }}>
    <div className="form-grid">
      <Field label="用户标识"><input required disabled={lockedSubject} value={draft.subject} onChange={change("subject")} placeholder="OIDC subject" /></Field>
      <Field label="姓名"><input value={draft.display_name} onChange={change("display_name")} placeholder="显示名称" /></Field>
      <Field label="邮箱"><input type="email" value={draft.email} onChange={change("email")} placeholder="name@example.com" /></Field>
      <Field label="Workspace 角色"><select value={draft.role} onChange={change("role")}>{Object.entries(roleLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></Field>
      <Field label="状态"><select value={draft.status} onChange={change("status")}>{Object.entries(statusLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></Field>
    </div>
    <div className="form-actions"><button className="secondary" type="button" onClick={onCancel}>取消</button><button className="primary" disabled={busy} type="submit">{busy ? <Loader2 className="spin" size={16} /> : <Check size={16} />}保存</button></div>
  </form>;
}

function WorkspaceMembers({ context, setError }) {
  const [members, setMembers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(null);
  const load = useCallback(async () => {
    setLoading(true);
    try { setMembers(await api.listMembers()); } catch (error) { setError(error.message); } finally { setLoading(false); }
  }, [setError]);
  useEffect(() => { load(); }, [load]);
  const save = async (draft) => {
    setBusy(true);
    try {
      await api.saveMember(draft.subject.trim(), draft);
      setEditing(null);
      await load();
    } catch (error) { setError(error.message); } finally { setBusy(false); }
  };
  const remove = async (member) => {
    if (!window.confirm(`移除 ${member.display_name || member.subject}？`)) return;
    try { await api.removeMember(member.subject); await load(); } catch (error) { setError(error.message); }
  };
  return <section className="content-section">
    <div className="section-head"><div><h2>Workspace 成员</h2><p>成员与角色仅在当前 Workspace 内生效。</p></div><div className="actions"><IconButton label="刷新" onClick={load} disabled={loading}><RefreshCw className={loading ? "spin" : ""} size={17} /></IconButton><button className="primary" onClick={() => setEditing(emptyMember)}><Plus size={16} />添加成员</button></div></div>
    <div className="summary-strip"><div><span>Workspace</span><strong>{context.principal.workspace_id}</strong></div><div><span>成员数</span><strong>{members.length}</strong></div><div><span>当前身份</span><strong>{context.principal.username || context.principal.subject}</strong></div></div>
    <div className="table-wrap"><table><thead><tr><th>成员</th><th>用户标识</th><th>角色</th><th>状态</th><th>更新时间</th><th aria-label="操作" /></tr></thead><tbody>
      {loading ? <tr><td colSpan="6"><div className="table-state"><Loader2 className="spin" size={20} />正在加载</div></td></tr> : members.length === 0 ? <tr><td colSpan="6"><div className="table-state">暂无成员记录</div></td></tr> : members.map((member) => <tr key={member.subject}>
        <td><div className="identity"><span>{(member.display_name || member.subject).slice(0, 1).toUpperCase()}</span><div><strong>{member.display_name || "未设置姓名"}</strong><small>{member.email || "未设置邮箱"}</small></div></div></td>
        <td><code>{member.subject}</code></td><td><span className={`role role-${member.role}`}>{roleLabels[member.role] || member.role}</span></td><td><span className={`status status-${member.status}`}>{statusLabels[member.status] || member.status}</span></td><td>{formatTime(member.updated_at)}</td>
        <td><div className="row-actions"><IconButton label="编辑成员" onClick={() => setEditing(member)}><UserCog size={16} /></IconButton><IconButton label="移除成员" disabled={member.subject === context.principal.subject} onClick={() => remove(member)}><Trash2 size={16} /></IconButton></div></td>
      </tr>)}</tbody></table></div>
    {editing ? <Modal title={editing.subject ? "编辑成员" : "添加成员"} detail={`Workspace: ${context.principal.workspace_id}`} onClose={() => setEditing(null)}><MemberForm initial={editing} lockedSubject={!!editing.subject} onCancel={() => setEditing(null)} onSubmit={save} busy={busy} /></Modal> : null}
  </section>;
}

function PlatformWorkspaces({ setError }) {
  const [items, setItems] = useState([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [selected, setSelected] = useState(null);
  const load = useCallback(async () => { setLoading(true); try { setItems(await api.listWorkspaces()); } catch (error) { setError(error.message); } finally { setLoading(false); } }, [setError]);
  useEffect(() => { load(); }, [load]);
  const create = async (event) => { event.preventDefault(); setCreating(true); try { const created = await api.createWorkspace(name.trim()); setName(""); await load(); setSelected(created); } catch (error) { setError(error.message); } finally { setCreating(false); } };
  if (selected) return <PlatformWorkspaceMembers workspace={selected} onBack={() => { setSelected(null); load(); }} setError={setError} />;
  return <section className="content-section"><div className="section-head"><div><h2>Workspace 租户</h2><p>创建和查看平台上的租户边界。</p></div><IconButton label="刷新" onClick={load}><RefreshCw className={loading ? "spin" : ""} size={17} /></IconButton></div>
    <form className="inline-create" onSubmit={create}><Building2 size={18} /><input required maxLength="200" value={name} onChange={(event) => setName(event.target.value)} placeholder="Workspace 名称" /><button className="primary" disabled={creating || !name.trim()}>{creating ? <Loader2 className="spin" size={16} /> : <Plus size={16} />}创建</button></form>
    <div className="workspace-list">{loading ? <div className="table-state"><Loader2 className="spin" size={20} />正在加载</div> : items.map((item) => <button type="button" className="workspace-row" key={item.id} onClick={() => setSelected(item)}><div className="workspace-icon"><Building2 size={18} /></div><div><strong>{item.name}</strong><code>{item.id}</code></div><span>{item.member_count} 位成员</span><time>{formatTime(item.created_at)}</time><ChevronRight size={17} /></button>)}</div>
  </section>;
}

function PlatformWorkspaceMembers({ workspace, onBack, setError }) {
  const [members, setMembers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState(null);
  const load = useCallback(async () => {
    setLoading(true);
    try { setMembers(await api.listWorkspaceMembersAsPlatform(workspace.id)); } catch (error) { setError(error.message); } finally { setLoading(false); }
  }, [setError, workspace.id]);
  useEffect(() => { load(); }, [load]);
  const save = async (draft) => {
    setBusy(true);
    try { await api.saveWorkspaceMemberAsPlatform(workspace.id, draft.subject.trim(), draft); setEditing(null); await load(); } catch (error) { setError(error.message); } finally { setBusy(false); }
  };
  const remove = async (member) => {
    if (!window.confirm(`从 ${workspace.name} 移除 ${member.display_name || member.subject}？`)) return;
    try { await api.removeWorkspaceMemberAsPlatform(workspace.id, member.subject); await load(); } catch (error) { setError(error.message); }
  };
  return <section className="content-section">
    <button className="back-button" type="button" onClick={onBack}><ArrowLeft size={16} />返回租户列表</button>
    <div className="section-head"><div><h2>{workspace.name}</h2><p>Platform Admin 正在管理目标 Workspace 的成员。</p></div><div className="actions"><IconButton label="刷新" onClick={load} disabled={loading}><RefreshCw className={loading ? "spin" : ""} size={17} /></IconButton><button className="primary" onClick={() => setEditing(emptyMember)}><Plus size={16} />添加成员</button></div></div>
    <div className="summary-strip"><div><span>Workspace</span><strong>{workspace.id}</strong></div><div><span>成员数</span><strong>{members.length}</strong></div><div><span>管理范围</span><strong>Platform Admin</strong></div></div>
    <div className="table-wrap"><table><thead><tr><th>成员</th><th>用户标识</th><th>角色</th><th>状态</th><th>更新时间</th><th aria-label="操作" /></tr></thead><tbody>
      {loading ? <tr><td colSpan="6"><div className="table-state"><Loader2 className="spin" size={20} />正在加载</div></td></tr> : members.length === 0 ? <tr><td colSpan="6"><div className="table-state">暂无成员，请先配置 Workspace 管理员</div></td></tr> : members.map((member) => <tr key={member.subject}>
        <td><div className="identity"><span>{(member.display_name || member.subject).slice(0, 1).toUpperCase()}</span><div><strong>{member.display_name || "未设置姓名"}</strong><small>{member.email || "未设置邮箱"}</small></div></div></td>
        <td><code>{member.subject}</code></td><td><span className={`role role-${member.role}`}>{roleLabels[member.role] || member.role}</span></td><td><span className={`status status-${member.status}`}>{statusLabels[member.status] || member.status}</span></td><td>{formatTime(member.updated_at)}</td>
        <td><div className="row-actions"><IconButton label="编辑成员" onClick={() => setEditing(member)}><UserCog size={16} /></IconButton><IconButton label="移除成员" onClick={() => remove(member)}><Trash2 size={16} /></IconButton></div></td>
      </tr>)}</tbody></table></div>
    {editing ? <Modal title={editing.subject ? "编辑租户成员" : "添加租户成员"} detail={`Workspace: ${workspace.id}`} onClose={() => setEditing(null)}><MemberForm initial={editing} lockedSubject={!!editing.subject} onCancel={() => setEditing(null)} onSubmit={save} busy={busy} /></Modal> : null}
  </section>;
}

function PlatformAdmins({ context, setError }) {
  const [items, setItems] = useState([]);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(emptyAdmin);
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => { try { setItems(await api.listPlatformAdmins()); } catch (error) { setError(error.message); } }, [setError]);
  useEffect(() => { load(); }, [load]);
  const save = async (event) => { event.preventDefault(); setBusy(true); try { await api.savePlatformAdmin(draft.subject.trim(), draft); setEditing(false); setDraft(emptyAdmin); await load(); } catch (error) { setError(error.message); } finally { setBusy(false); } };
  const remove = async (item) => { if (!window.confirm(`移除平台管理员 ${item.display_name || item.subject}？`)) return; try { await api.removePlatformAdmin(item.subject); await load(); } catch (error) { setError(error.message); } };
  return <section className="content-section"><div className="section-head"><div><h2>Platform Admin</h2><p>平台角色不属于任何单个 Workspace。</p></div><button className="primary" onClick={() => setEditing(true)}><Plus size={16} />添加管理员</button></div>
    <div className="admin-grid">{items.length ? items.map((item) => <article className="admin-item" key={item.subject}><div className="identity"><span><ShieldCheck size={17} /></span><div><strong>{item.display_name || item.subject}</strong><small>{item.email || item.subject}</small></div></div><IconButton label="移除平台管理员" disabled={item.subject === context.principal.subject} onClick={() => remove(item)}><Trash2 size={16} /></IconButton></article>) : <div className="table-state">尚无持久化的平台管理员</div>}</div>
    {editing ? <Modal title="添加 Platform Admin" detail="该角色可以管理所有 Workspace 租户。" onClose={() => setEditing(false)}><form onSubmit={save}><div className="form-grid"><Field label="用户标识"><input required value={draft.subject} onChange={(event) => setDraft({ ...draft, subject: event.target.value })} /></Field><Field label="姓名"><input value={draft.display_name} onChange={(event) => setDraft({ ...draft, display_name: event.target.value })} /></Field><Field label="邮箱"><input type="email" value={draft.email} onChange={(event) => setDraft({ ...draft, email: event.target.value })} /></Field></div><div className="form-actions"><button className="secondary" type="button" onClick={() => setEditing(false)}>取消</button><button className="primary" disabled={busy}>{busy ? <Loader2 className="spin" size={16} /> : <Check size={16} />}保存</button></div></form></Modal> : null}
  </section>;
}

function App() {
  const [context, setContext] = useState(null);
  const [active, setActive] = useState("members");
  const [error, setError] = useState("");
  useEffect(() => { api.getContext().then(setContext).catch((loadError) => setError(loadError.message)); }, []);
  const nav = useMemo(() => {
    const items = [];
    if (context?.workspace_admin) items.push({ id: "members", label: "成员与角色", detail: "当前 Workspace", icon: Users });
    if (context?.platform_admin) items.push({ id: "workspaces", label: "租户管理", detail: "Platform Admin", icon: Building2 }, { id: "admins", label: "平台管理员", detail: "Platform Admin", icon: ShieldCheck });
    return items;
  }, [context]);
  if (!context) return <main className="boot"><div className="brand-mark"><ShieldCheck size={24} /></div>{error ? <><strong>Console 无法加载</strong><p>{error}</p></> : <><Loader2 className="spin" size={20} /><span>正在加载管理上下文</span></>}</main>;
  const selected = nav.some((item) => item.id === active) ? active : nav[0]?.id || "";
  return <div className="app-shell"><header className="topbar"><div className="brand"><div className="brand-mark"><ShieldCheck size={19} /></div><div><h1>TMA Console</h1><span>管理中心</span></div></div><div className="principal"><div><strong>{context.principal.username || context.principal.subject}</strong><span>{context.principal.workspace_id}</span></div><div className="avatar">{(context.principal.username || context.principal.subject).slice(0, 1).toUpperCase()}</div></div></header>
    <div className="layout"><aside><div className="scope-label">Workspace</div>{nav.map(({ id, label, detail, icon: Icon }) => <button key={id} className={selected === id ? "nav-item active" : "nav-item"} onClick={() => setActive(id)}><Icon size={17} /><span><strong>{label}</strong><small>{detail}</small></span></button>)}</aside>
      <main><Notice error={error} onClose={() => setError("")} />{selected === "members" ? <WorkspaceMembers context={context} setError={setError} /> : selected === "workspaces" ? <PlatformWorkspaces setError={setError} /> : selected === "admins" ? <PlatformAdmins context={context} setError={setError} /> : <section className="content-section"><div className="table-state">当前身份没有 Console 管理权限</div></section>}</main></div>
  </div>;
}

createRoot(document.getElementById("root")).render(<App />);
