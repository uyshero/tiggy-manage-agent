import React, { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, CheckCircle2, Clock3, Database, Download, FileJson, HardDrive, RefreshCw, RotateCcw, Save, ScanSearch, ShieldCheck, X } from "lucide-react";
import * as api from "./api.js";
import "./objectCleanupSettings.css";

const statusOrder = ["pending", "processing", "blocked", "dead_letter", "completed"];
const statusLabels = {
  pending: "待处理",
  processing: "处理中",
  blocked: "待批准",
  dead_letter: "死信",
  completed: "已完成"
};
const reasonLabels = {
  object_ref_create_failed: "ObjectRef 创建失败",
  artifact_create_failed: "Artifact 创建失败",
  object_ref_rollback_failed: "ObjectRef 回滚失败",
  unsafe_custom_key: "自定义 Key 未确认",
  managed_object_orphaned: "托管对象孤儿"
};
const reconciliationLabels = {
  missing_object: "对象缺失",
  orphan_object: "孤儿对象",
  metadata_mismatch: "元数据不一致",
  provider_error: "Provider 错误"
};
const differenceLabels = {
  version: "版本",
  content_type: "内容类型",
  size_bytes: "大小",
  checksum_sha256: "SHA-256",
  etag: "ETag"
};

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const amount = bytes / (1024 ** index);
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`;
}

function formatAge(seconds) {
  const value = Math.max(0, Number(seconds || 0));
  if (value < 60) return `${Math.round(value)} 秒`;
  if (value < 3600) return `${Math.round(value / 60)} 分钟`;
  if (value < 86400) return `${(value / 3600).toFixed(value < 36000 ? 1 : 0)} 小时`;
  return `${(value / 86400).toFixed(value < 864000 ? 1 : 0)} 天`;
}

function formatDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false
  }).format(date);
}

function requestFilters(workspaceID, filters) {
  return {
    workspaceId: workspaceID,
    ...(filters.status ? { status: filters.status } : {}),
    ...(filters.reason.trim() ? { reason: filters.reason.trim() } : {}),
    ...(filters.createdFrom ? { createdFrom: new Date(filters.createdFrom) } : {}),
    ...(filters.createdTo ? { createdTo: new Date(filters.createdTo) } : {}),
    limit: 100
  };
}

function CleanupStatus({ status }) {
  return <span className={`object-cleanup-status ${status || "unknown"}`}>{statusLabels[status] || status || "未知"}</span>;
}

function ApprovalDialog({ busy, job, onApprove, onClose }) {
  const expected = `DELETE ${job.id}`;
  const [confirm, setConfirm] = useState("");
  return (
    <div className="object-cleanup-dialog-backdrop" role="presentation" onMouseDown={(event) => {
      if (!busy && event.target === event.currentTarget) onClose();
    }}>
      <form className="object-cleanup-dialog" role="alertdialog" aria-modal="true" aria-labelledby="object-cleanup-dialog-title" onSubmit={(event) => {
        event.preventDefault();
        if (confirm === expected) onApprove(confirm);
      }}>
        <header>
          <div className="object-cleanup-dialog-icon"><AlertTriangle aria-hidden="true" /></div>
          <div>
            <h2 id="object-cleanup-dialog-title">批准永久删除对象</h2>
            <p>批准后任务会进入待处理队列，Worker 将从对象存储永久删除该文件。</p>
          </div>
          <button className="secondary icon-button" type="button" title="关闭" aria-label="关闭批准窗口" disabled={busy} onClick={onClose}><X /></button>
        </header>
        <dl className="object-cleanup-dialog-facts">
          <div><dt>对象</dt><dd>{job.bucket}/{job.object_key}</dd></div>
          <div><dt>Provider</dt><dd>{job.storage_provider}</dd></div>
          <div><dt>大小</dt><dd>{formatBytes(job.size_bytes)}</dd></div>
          <div><dt>原因</dt><dd>{reasonLabels[job.reason] || job.reason}</dd></div>
        </dl>
        <label className="object-cleanup-confirm-field">
          <span>输入 <code>{expected}</code> 确认</span>
          <input autoFocus autoComplete="off" spellCheck="false" value={confirm} onChange={(event) => setConfirm(event.target.value)} />
        </label>
        <footer>
          <button className="secondary" type="button" disabled={busy} onClick={onClose}>取消</button>
          <button className="object-cleanup-danger-button" type="submit" disabled={busy || confirm !== expected}><ShieldCheck />{busy ? "批准中..." : "批准删除"}</button>
        </footer>
      </form>
    </div>
  );
}

export default function ObjectCleanupSettings({ canApprove = false, workspaceID = "" }) {
  const [stats, setStats] = useState(null);
  const [jobs, setJobs] = useState([]);
  const [filters, setFilters] = useState({ status: "", reason: "", createdFrom: "", createdTo: "" });
  const [loading, setLoading] = useState(false);
  const [busyJobID, setBusyJobID] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [approvalJob, setApprovalJob] = useState(null);
  const [reconciliationPrefix, setReconciliationPrefix] = useState(`${workspaceID}/`);
  const [reconciliationLimit, setReconciliationLimit] = useState(100);
  const [reconciliationReport, setReconciliationReport] = useState(null);
  const [reconciliationInput, setReconciliationInput] = useState(null);
  const [reconciliationLoading, setReconciliationLoading] = useState(false);
  const [reconciliationError, setReconciliationError] = useState("");
  const [artifactSessions, setArtifactSessions] = useState([]);
  const [artifactSessionID, setArtifactSessionID] = useState("");
  const [artifactSessionsLoading, setArtifactSessionsLoading] = useState(false);
  const [artifactExportLoading, setArtifactExportLoading] = useState(false);
  const [artifactExport, setArtifactExport] = useState(null);
  const requestIDRef = useRef(0);

  async function refresh(activeFilters = filters, signal) {
    if (!workspaceID) {
      setError("当前没有可用的 Workspace，无法查询对象清理任务。");
      return;
    }
    const requestID = ++requestIDRef.current;
    setLoading(true);
    setError("");
    try {
      const [statsResult, jobsResult] = await Promise.all([
        api.objectCleanupStats(workspaceID, { signal }),
        api.objectCleanupJobs(requestFilters(workspaceID, activeFilters), { signal })
      ]);
      if (requestID !== requestIDRef.current) return;
      setStats(statsResult);
      setJobs(Array.isArray(jobsResult.jobs) ? jobsResult.jobs : []);
    } catch (nextError) {
      if (nextError?.name !== "AbortError" && requestID === requestIDRef.current) setError(nextError.message);
    } finally {
      if (requestID === requestIDRef.current) setLoading(false);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    refresh({ status: "", reason: "", createdFrom: "", createdTo: "" }, controller.signal);
    return () => controller.abort();
  }, [workspaceID]);

  useEffect(() => {
    setReconciliationPrefix(workspaceID ? `${workspaceID}/` : "");
    setReconciliationReport(null);
    setReconciliationInput(null);
    setReconciliationError("");
    setArtifactExport(null);
  }, [workspaceID]);

  useEffect(() => {
    const controller = new AbortController();
    setArtifactSessions([]);
    setArtifactSessionID("");
    if (!workspaceID) {
      setArtifactSessionsLoading(false);
      return () => controller.abort();
    }
    setArtifactSessionsLoading(true);
    api.sessions({ workspace: workspaceID, limit: 100 }).then((response) => {
      if (controller.signal.aborted) return;
      const available = (response.sessions || []).filter((session) => session.workspace_id === workspaceID);
      setArtifactSessions(available);
      setArtifactSessionID((current) => available.some((session) => session.id === current) ? current : (available[0]?.id || ""));
    }).catch((nextError) => {
      if (nextError?.name !== "AbortError" && !controller.signal.aborted) setReconciliationError(nextError.message);
    }).finally(() => {
      if (!controller.signal.aborted) setArtifactSessionsLoading(false);
    });
    return () => controller.abort();
  }, [workspaceID]);

  const statusStats = useMemo(() => new Map((stats?.statuses || []).map((item) => [item.status, item])), [stats]);
  const totalJobs = useMemo(() => [...statusStats.values()].reduce((sum, item) => sum + Number(item.jobs || 0), 0), [statusStats]);
  const retryRatio = totalJobs ? Number(stats?.total_retried_jobs || 0) / totalJobs : 0;

  async function retry(job) {
    setBusyJobID(job.id);
    setError("");
    setMessage("");
    try {
      await api.retryObjectCleanup(job.id, workspaceID);
      setMessage(`清理任务 ${job.id} 已重新进入待处理队列。`);
      await refresh();
    } catch (nextError) {
      setError(nextError.message);
    } finally {
      setBusyJobID("");
    }
  }

  async function approve(confirm) {
    if (!approvalJob) return;
    const job = approvalJob;
    setBusyJobID(job.id);
    setError("");
    setMessage("");
    try {
      await api.approveObjectCleanup(job.id, workspaceID, confirm);
      setApprovalJob(null);
      setMessage(`清理任务 ${job.id} 已批准并进入待处理队列。`);
      await refresh();
    } catch (nextError) {
      setError(nextError.message);
    } finally {
      setBusyJobID("");
    }
  }

  async function runReconciliation(providerCursor = "") {
    if (!workspaceID) {
      setReconciliationError("当前没有可用的 Workspace。");
      return;
    }
    setReconciliationLoading(true);
    setReconciliationError("");
    setArtifactExport(null);
    try {
      const input = {
        workspace_id: workspaceID,
        prefix: reconciliationPrefix.trim(),
        limit: Number(reconciliationLimit),
        ...(providerCursor ? { provider_cursor: providerCursor } : {})
      };
      const report = await api.previewObjectReconciliation(input);
      setReconciliationReport(report);
      setReconciliationInput(input);
    } catch (nextError) {
      setReconciliationError(nextError.message);
    } finally {
      setReconciliationLoading(false);
    }
  }

  async function exportReconciliationArtifact() {
    if (!reconciliationInput || !artifactSessionID) return;
    setArtifactExportLoading(true);
    setReconciliationError("");
    setArtifactExport(null);
    try {
      const exported = await api.exportObjectReconciliationArtifact({
        ...reconciliationInput,
        session_id: artifactSessionID
      });
      setReconciliationReport(exported.report);
      setArtifactExport(exported);
    } catch (nextError) {
      setReconciliationError(nextError.message);
    } finally {
      setArtifactExportLoading(false);
    }
  }

  return (
    <div className="object-cleanup-console">
      <section className="object-cleanup-overview" aria-label="对象清理概览">
        <div className="object-cleanup-overview-heading">
          <div>
            <strong>清理队列</strong>
            <span>Workspace <code>{workspaceID || "-"}</code></span>
          </div>
          <button className="secondary object-cleanup-refresh" type="button" disabled={loading} onClick={() => refresh()}><RefreshCw aria-hidden="true" />{loading ? "刷新中..." : "刷新"}</button>
        </div>
        <div className="object-cleanup-metrics">
          <div><Database aria-hidden="true" /><span>队列任务</span><strong>{totalJobs}</strong><small>{formatBytes((stats?.statuses || []).reduce((sum, item) => sum + Number(item.bytes || 0), 0))}</small></div>
          <div className={Number(statusStats.get("dead_letter")?.jobs || 0) ? "critical" : ""}><AlertTriangle aria-hidden="true" /><span>死信</span><strong>{Number(statusStats.get("dead_letter")?.jobs || 0)}</strong><small>重试率 {(retryRatio * 100).toFixed(retryRatio > 0 && retryRatio < 0.1 ? 1 : 0)}%</small></div>
          <div className={Number(statusStats.get("blocked")?.jobs || 0) ? "warning" : ""}><ShieldCheck aria-hidden="true" /><span>待批准</span><strong>{Number(statusStats.get("blocked")?.jobs || 0)}</strong><small>{canApprove ? "Admin 可处理" : "需要 Admin"}</small></div>
          <div><Clock3 aria-hidden="true" /><span>最老待处理</span><strong>{formatAge(stats?.oldest_pending_age_seconds || 0)}</strong><small>{stats?.oldest_pending_at ? formatDate(stats.oldest_pending_at) : "无积压"}</small></div>
          <div><HardDrive aria-hidden="true" /><span>已删除</span><strong>{formatBytes(stats?.total_deleted_bytes || 0)}</strong><small>{Number(statusStats.get("completed")?.jobs || 0)} 个任务</small></div>
        </div>
      </section>

      <section className="object-reconciliation-panel" aria-busy={reconciliationLoading || artifactExportLoading}>
        <header>
          <div><strong>对象对账</strong><span>{reconciliationReport ? `${reconciliationReport.storage_provider} · ${reconciliationReport.bucket}` : `Workspace ${workspaceID || "-"}`}</span></div>
          <span className="object-reconciliation-mode">Dry run</span>
        </header>
        <form className="object-reconciliation-form" onSubmit={(event) => { event.preventDefault(); runReconciliation(); }}>
          <label><span>对象前缀</span><input value={reconciliationPrefix} spellCheck="false" onChange={(event) => setReconciliationPrefix(event.target.value)} placeholder={workspaceID ? `${workspaceID}/` : "workspace/"} /></label>
          <label><span>扫描上限</span><select value={reconciliationLimit} onChange={(event) => setReconciliationLimit(Number(event.target.value))}>{[50, 100, 250, 500].map((limit) => <option value={limit} key={limit}>{limit}</option>)}</select></label>
          <button type="submit" disabled={reconciliationLoading || !workspaceID}><ScanSearch aria-hidden="true" />{reconciliationLoading ? "扫描中..." : "运行对账"}</button>
        </form>
        {reconciliationError ? <div className="object-cleanup-notice error" role="alert"><AlertTriangle aria-hidden="true" /><span>{reconciliationError}</span></div> : null}
        {reconciliationReport ? (
          <div className="object-reconciliation-results">
            <div className="object-reconciliation-summary">
              <div><span>对象缺失</span><strong>{reconciliationReport.summary.missing_objects}</strong></div>
              <div><span>孤儿对象</span><strong>{reconciliationReport.summary.orphan_objects}</strong></div>
              <div><span>元数据不一致</span><strong>{reconciliationReport.summary.metadata_mismatches}</strong></div>
              <div><span>Provider 错误</span><strong>{reconciliationReport.summary.provider_errors}</strong></div>
            </div>
            <div className={`object-reconciliation-scan ${(reconciliationReport.scan.object_refs.truncated || reconciliationReport.scan.provider_objects.truncated) ? "warning" : ""}`}>
              <span>ObjectRef {reconciliationReport.scan.object_refs.scanned}{reconciliationReport.scan.object_refs.truncated ? "+" : ""}</span>
              <span>Provider {reconciliationReport.scan.provider_objects.scanned}{reconciliationReport.scan.provider_objects.truncated ? "+" : ""}</span>
              <span>{formatDate(reconciliationReport.generated_at)}</span>
              {reconciliationReport.scan.provider_objects.next_cursor ? <button className="secondary" type="button" disabled={reconciliationLoading} onClick={() => runReconciliation(reconciliationReport.scan.provider_objects.next_cursor)}>下一页</button> : null}
            </div>
            <div className="object-reconciliation-export">
              <FileJson aria-hidden="true" />
              <label><span>归属 Session</span><select value={artifactSessionID} disabled={artifactSessionsLoading || artifactExportLoading} onChange={(event) => setArtifactSessionID(event.target.value)}><option value="">{artifactSessionsLoading ? "加载中..." : "选择 Session"}</option>{artifactSessions.map((session) => <option value={session.id} key={session.id}>{session.title ? `${session.title} · ${session.id}` : session.id}</option>)}</select></label>
              <button className="secondary" type="button" disabled={!artifactSessionID || artifactSessionsLoading || artifactExportLoading} onClick={exportReconciliationArtifact}><Save aria-hidden="true" />{artifactExportLoading ? "保存中..." : "保存为 Artifact"}</button>
              {artifactExport ? <a className="object-reconciliation-download" href={`/v2/sessions/${encodeURIComponent(artifactExport.artifact.session_id)}/artifacts/${encodeURIComponent(artifactExport.artifact.id)}/download`}><Download aria-hidden="true" />{artifactExport.artifact.name}</a> : null}
            </div>
            <div className="object-reconciliation-table-scroll">
              <table>
                <thead><tr><th>发现</th><th>对象</th><th>差异</th><th>建议</th></tr></thead>
                <tbody>
                  {reconciliationReport.findings.map((finding, index) => (
                    <tr key={`${finding.kind}-${finding.object_ref_id || finding.object_key || index}`}>
                      <td><span className={`object-reconciliation-kind ${finding.kind}`}>{reconciliationLabels[finding.kind] || finding.kind}</span></td>
                      <td className="object-reconciliation-object"><code title={finding.object_key}>{finding.object_key || "-"}</code><span>{finding.object_ref_id || "无 ObjectRef"}{finding.object_version ? ` · ${finding.object_version}` : ""}</span></td>
                      <td className="object-reconciliation-differences">{finding.differences?.length ? finding.differences.map((difference) => <span key={difference.field}>{differenceLabels[difference.field] || difference.field}: <code>{difference.expected || "-"}</code> → <code>{difference.actual || "-"}</code></span>) : <span>{finding.message}</span>}</td>
                      <td className="object-reconciliation-remediation">{finding.remediation}</td>
                    </tr>
                  ))}
                  {!reconciliationReport.findings.length ? <tr><td className="object-cleanup-empty" colSpan="4"><CheckCircle2 aria-hidden="true" />当前扫描范围未发现异常。</td></tr> : null}
                </tbody>
              </table>
            </div>
          </div>
        ) : null}
      </section>

      <form className="object-cleanup-filters" onSubmit={(event) => { event.preventDefault(); refresh(); }}>
        <label><span>状态</span><select value={filters.status} onChange={(event) => setFilters((current) => ({ ...current, status: event.target.value }))}><option value="">全部状态</option>{statusOrder.map((status) => <option value={status} key={status}>{statusLabels[status]}</option>)}</select></label>
        <label><span>原因</span><select value={filters.reason} onChange={(event) => setFilters((current) => ({ ...current, reason: event.target.value }))}><option value="">全部原因</option>{Object.entries(reasonLabels).map(([reason, label]) => <option value={reason} key={reason}>{label}</option>)}</select></label>
        <label><span>创建时间从</span><input type="datetime-local" value={filters.createdFrom} onChange={(event) => setFilters((current) => ({ ...current, createdFrom: event.target.value }))} /></label>
        <label><span>创建时间至</span><input type="datetime-local" value={filters.createdTo} onChange={(event) => setFilters((current) => ({ ...current, createdTo: event.target.value }))} /></label>
        <div className="object-cleanup-filter-actions">
          <button className="secondary" type="button" disabled={loading} onClick={() => {
            const empty = { status: "", reason: "", createdFrom: "", createdTo: "" };
            setFilters(empty);
            refresh(empty);
          }}>重置</button>
          <button type="submit" disabled={loading}>应用筛选</button>
        </div>
      </form>

      {error ? <div className="object-cleanup-notice error" role="alert"><AlertTriangle aria-hidden="true" /><span>{error}</span></div> : null}
      {message ? <div className="object-cleanup-notice success" role="status"><CheckCircle2 aria-hidden="true" /><span>{message}</span></div> : null}

      <section className="object-cleanup-table-frame" aria-busy={loading}>
        <header><div><strong>清理任务</strong><span>当前显示 {jobs.length} 条，按创建时间倒序</span></div></header>
        <div className="object-cleanup-table-scroll">
          <table>
            <thead><tr><th>状态</th><th>对象</th><th>原因</th><th>大小</th><th>尝试</th><th>创建时间</th><th><span className="visually-hidden">操作</span></th></tr></thead>
            <tbody>
              {jobs.map((job) => (
                <tr key={job.id}>
                  <td><CleanupStatus status={job.status} /></td>
                  <td className="object-cleanup-object"><code title={job.object_key}>{job.object_key}</code><span>{job.storage_provider} · {job.bucket}{job.object_version ? ` · ${job.object_version}` : ""}</span></td>
                  <td><span className="object-cleanup-reason">{reasonLabels[job.reason] || job.reason}</span>{job.last_error ? <span className="object-cleanup-error-detail" title={job.last_error}>{job.last_error}</span> : null}</td>
                  <td className="object-cleanup-numeric">{formatBytes(job.size_bytes)}</td>
                  <td className="object-cleanup-numeric">{job.attempt_count}</td>
                  <td className="object-cleanup-date">{formatDate(job.created_at)}</td>
                  <td className="object-cleanup-actions">
                    {job.status === "dead_letter" && job.safe_to_delete ? <button className="secondary" type="button" disabled={Boolean(busyJobID)} title="重新加入清理队列" onClick={() => retry(job)}><RotateCcw aria-hidden="true" />{busyJobID === job.id ? "处理中" : "重试"}</button> : null}
                    {job.status === "blocked" ? <button className="secondary danger" type="button" disabled={Boolean(busyJobID) || !canApprove} title={canApprove ? "审核并批准永久删除" : "仅 Admin 可以批准"} onClick={() => setApprovalJob(job)}><ShieldCheck aria-hidden="true" />批准</button> : null}
                  </td>
                </tr>
              ))}
              {!jobs.length && !loading ? <tr><td className="object-cleanup-empty" colSpan="7">没有符合条件的清理任务。</td></tr> : null}
              {!jobs.length && loading ? <tr><td className="object-cleanup-empty" colSpan="7">正在加载清理任务...</td></tr> : null}
            </tbody>
          </table>
        </div>
      </section>
      {approvalJob ? <ApprovalDialog busy={busyJobID === approvalJob.id} job={approvalJob} onApprove={approve} onClose={() => setApprovalJob(null)} /> : null}
    </div>
  );
}
