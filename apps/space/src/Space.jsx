import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ArrowLeftRight,
  Clock3,
  Database,
  Download,
  ExternalLink,
  FileDown,
  FlaskConical,
  Plus,
  Play,
  RefreshCw,
  RotateCcw,
  Save,
  Scale,
  Search,
  Sparkles,
  Trash2,
  Upload
} from "lucide-react";
import * as spaceAPI from "./api.js";
import { parseDatasetFile, serializeDataset, serializeDatasetTemplate, serializeExperiment } from "./datasetFiles.js";
import {
  averageScore,
  conclusionLabel,
  createEvaluation,
  evaluationFromRubric,
  formatDelta,
  formatDuration,
  formatTime,
  localizedCriterion,
  metricDelta,
  pillTone,
  statusLabel,
  summarizeEvidence
} from "./analysis.js";
import "./styles.css";

window.TMASpaceAPI = spaceAPI;

function modelToolName(identifier, apiName) {
  const normalize = (value) => String(value || "").trim().replace(/[^a-zA-Z0-9_]/g, "_");
  return [normalize(identifier), normalize(apiName)].filter(Boolean).join("_");
}

function hashParams() {
  return new URLSearchParams(String(window.location.hash || "").replace(/^#/, ""));
}

function setHash(left, leftTurn, right, rightTurn) {
  const params = new URLSearchParams();
  if (left) params.set("left", left);
  if (leftTurn) params.set("left_turn", leftTurn);
  if (right) params.set("right", right);
  if (rightTurn) params.set("right_turn", rightTurn);
  const next = params.toString();
  window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}${next ? `#${next}` : ""}`);
}

function sessionLabel(session) {
  if (!session) return "未知会话";
  return `${session.title || session.id} · ${session.agent_id || "无智能体"}`;
}

function runLabel(run) {
  if (!run) return "未知轮次";
  return `${run.id} · ${statusLabel(run.status)} · ${formatTime(run.started_at)}`;
}

function Pill({ value }) {
  return <span className={`pill ${pillTone(value)}`}>{statusLabel(value)}</span>;
}

function IconButton({ label, children, ...props }) {
  return <button className="icon-button" type="button" aria-label={label} title={label} {...props}>{children}</button>;
}

function downloadTextFile({ content, filename, mimeType }) {
  const body = String(mimeType || "").startsWith("text/csv") ? `\uFEFF${content}` : content;
  const url = URL.createObjectURL(new Blob([body], { type: mimeType }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

function Metric({ label, left, right, format = (value) => Number(value || 0).toLocaleString(), unit = "", lowerIsBetter = false }) {
  const delta = metricDelta(left, right);
  const direction = delta === 0 ? "same" : lowerIsBetter ? (delta < 0 ? "better" : "worse") : (delta > 0 ? "better" : "worse");
  return (
    <div className="metric-cell">
      <span className="metric-label">{label}</span>
      <div className="metric-values"><strong>{format(left)}</strong><span>{format(right)}</span></div>
      <small className={`delta ${direction}`}>{formatDelta(delta, unit)}，B - A</small>
    </div>
  );
}

function RunPicker({ label, sessionID, turnID, sessions, runs, onSessionChange, onTurnChange, loading }) {
  return (
    <div className="session-picker">
      <span>{label}</span>
      <select aria-label={`${label} 会话`} value={sessionID} onChange={(event) => onSessionChange(event.target.value)}>
        <option value="">选择会话</option>
        {sessions.map((session) => (
          <option key={session.id} value={session.id}>{sessionLabel(session)}</option>
        ))}
      </select>
      <select aria-label={`${label} 轮次`} value={turnID} disabled={!sessionID || loading} onChange={(event) => onTurnChange(event.target.value)}>
        <option value="">{loading ? "正在加载轮次..." : "选择轮次"}</option>
        {runs.map((run) => <option key={run.id} value={run.id}>{runLabel(run)}</option>)}
      </select>
    </div>
  );
}

function EvidenceHeader({ label, summary }) {
  const inspectorURL = `/inspector#session=${encodeURIComponent(summary.session.id || "")}${summary.turnID ? `&turn=${encodeURIComponent(summary.turnID)}` : ""}`;
  return (
    <div className="evidence-header">
      <div>
        <span className="side-label">{label}</span>
        <h2>{summary.session.title || summary.session.id || "会话"}</h2>
        <div className="meta-line">
          <Pill value={summary.status} />
          <span>{summary.provider} / {summary.model}</span>
          <span>{summary.turnID || "最新轮次"}</span>
        </div>
      </div>
      <a className="inspector-link" href={inspectorURL} target="_blank" rel="noreferrer">
        检查器 <ExternalLink size={14} aria-hidden="true" />
      </a>
    </div>
  );
}

function TextEvidence({ title, left, right, empty }) {
  return (
    <section className="analysis-section">
      <div className="section-heading"><h2>{title}</h2></div>
      <div className="evidence-columns">
        <div className="text-evidence"><pre>{left || empty}</pre></div>
        <div className="text-evidence"><pre>{right || empty}</pre></div>
      </div>
    </section>
  );
}

function StepList({ summary }) {
  const steps = summary.steps.slice(-18);
  if (!steps.length) return <div className="empty-row">暂无追踪证据。</div>;
  return (
    <div className="step-list">
      {steps.map((step, index) => {
        const error = String(step.type || "").includes("failed") || ["error", "failed", "rejected"].includes(String(step.outcome || ""));
        const tool = String(step.type || "").includes("tool");
        return (
          <div className={`step-row ${error ? "error" : tool ? "tool" : ""}`} key={`${step.seq || index}-${step.type}`}>
            <div className="step-index">{step.seq || index + 1}</div>
            <div>
              <strong>{step.type || "运行事件"}</strong>
			  <p>{statusLabel(step.message || step.summary || modelToolName(step.identifier, step.api_name) || "无详细信息")}</p>
            </div>
            <div className="step-meta">
              {step.outcome ? <Pill value={step.outcome} /> : null}
              <span>{formatDuration(step.duration_ms)}</span>
            </div>
          </div>
        );
      })}
    </div>
  );
}

function TraceEvidence({ left, right }) {
  return (
    <section className="analysis-section">
      <div className="section-heading"><h2>执行证据</h2><span>每侧最近 18 个步骤</span></div>
      <div className="evidence-columns trace-columns">
        <StepList summary={left} />
        <StepList summary={right} />
      </div>
    </section>
  );
}

function Diagnostics({ left, right }) {
  const render = (summary) => {
    const tools = summary.toolSteps.filter((step) => step.type === "runtime.tool_call");
    return (
      <div className="diagnostic-side">
        <div className="diagnostic-group">
          <h3>工具调用</h3>
          {tools.length ? tools.map((step, index) => (
            <div className="diagnostic-row" key={`${step.call_id || step.seq || index}-tool`}>
			  <span>{modelToolName(step.identifier, step.api_name) || step.message || "工具调用"}</span>
              <small>{step.call_id || `seq ${step.seq || "-"}`}</small>
            </div>
          )) : <div className="empty-row compact">暂无工具调用。</div>}
        </div>
        <div className="diagnostic-group">
          <h3>失败记录</h3>
          {summary.errorSteps.length ? summary.errorSteps.map((step, index) => (
            <div className="diagnostic-row failure" key={`${step.seq || index}-error`}>
              <span>{step.message || step.summary || step.type}</span>
              <small>{step.outcome || step.span_status || `seq ${step.seq || "-"}`}</small>
            </div>
          )) : <div className="empty-row compact">暂无失败记录。</div>}
        </div>
      </div>
    );
  };
  return (
    <section className="analysis-section">
      <div className="section-heading"><h2>诊断信息</h2></div>
      <div className="evidence-columns">{render(left)}{render(right)}</div>
    </section>
  );
}

function ScoreControl({ value, onChange, label }) {
  return (
    <div className="score-control" role="radiogroup" aria-label={label}>
      {[1, 2, 3, 4, 5].map((score) => (
        <button
          className={score === value ? "active" : ""}
          type="button"
          role="radio"
          aria-checked={score === value}
          key={score}
          onClick={() => onChange(score)}
        >{score}</button>
      ))}
    </div>
  );
}

function Rubric({ evaluation, setEvaluation, onSave, onAuto, onReset, onExport, saved, saving, judging }) {
  const leftAverage = averageScore(evaluation.criteria, "left");
  const rightAverage = averageScore(evaluation.criteria, "right");
  function updateCriterion(index, patch) {
    setEvaluation((current) => ({
      ...current,
      criteria: current.criteria.map((criterion, criterionIndex) => criterionIndex === index ? { ...criterion, ...patch } : criterion)
    }));
  }
  return (
    <section className="analysis-section rubric-section">
      <div className="section-heading rubric-heading">
        <div><h2>评分评估</h2><span>{saved ? `已保存于 ${formatTime(saved)}` : "尚未保存"}</span></div>
        <div className="section-actions">
          <IconButton label="重置评分" onClick={onReset}><RotateCcw size={16} /></IconButton>
          <IconButton label="导出评估" onClick={onExport}><Download size={16} /></IconButton>
          <button className="judge-action" type="button" disabled={saving || judging} onClick={onAuto}><Sparkles size={16} /> {judging ? "评测中" : "自动评测"}</button>
          <button className="primary-action" type="button" disabled={saving || judging} onClick={onSave}><Save size={16} /> {saving ? "保存中" : "保存"}</button>
        </div>
      </div>
      <div className="rubric-summary">
        <div><span>A 平均分</span><strong>{leftAverage.toFixed(2)}</strong></div>
        <div><span>B 平均分</span><strong>{rightAverage.toFixed(2)}</strong></div>
        <div><span>B - A</span><strong className={rightAverage > leftAverage ? "positive" : rightAverage < leftAverage ? "negative" : ""}>{formatDelta(Number((rightAverage - leftAverage).toFixed(2)))}</strong></div>
      </div>
      <div className="rubric-table">
        <div className="rubric-table-head"><span>评分项</span><span>运行 A</span><span>运行 B</span></div>
        {evaluation.criteria.map((criterion, index) => (
          <div className="rubric-row" key={criterion.id}>
            <div>
              <strong>{localizedCriterion(criterion).name}</strong>
              <small>{localizedCriterion(criterion).description}</small>
            </div>
            <ScoreControl value={criterion.leftScore} label={`${localizedCriterion(criterion).name}，运行 A 评分`} onChange={(leftScore) => updateCriterion(index, { leftScore })} />
            <ScoreControl value={criterion.rightScore} label={`${localizedCriterion(criterion).name}，运行 B 评分`} onChange={(rightScore) => updateCriterion(index, { rightScore })} />
          </div>
        ))}
      </div>
      <div className="evaluation-footer">
        <div>
          <label>评估结论</label>
          <div className="conclusion-control">
            {[{ value: "left", label: "A 更优" }, { value: "right", label: "B 更优" }, { value: "tie", label: "持平" }, { value: "inconclusive", label: "无法判断" }].map((option) => (
              <button className={evaluation.conclusion === option.value ? "active" : ""} type="button" key={option.value} onClick={() => setEvaluation((current) => ({ ...current, conclusion: option.value }))}>{option.label}</button>
            ))}
          </div>
        </div>
        <label className="notes-field">评估备注<textarea value={evaluation.notes} onChange={(event) => setEvaluation((current) => ({ ...current, notes: event.target.value }))} placeholder="填写判断依据或补充说明" /></label>
      </div>
    </section>
  );
}

function EvaluationHistory({ evaluations }) {
  return (
    <section className="analysis-section history-section">
      <div className="section-heading"><h2>评估历史</h2><span>已保存 {evaluations.length} 条</span></div>
      {evaluations.length ? (
        <div className="history-list">
          {evaluations.map((item) => {
            const left = averageScore(item.scores.map((score) => ({ leftScore: score.left_score })), "left");
            const right = averageScore(item.scores.map((score) => ({ rightScore: score.right_score })), "right");
            const source = item.evaluation_type === "auto"
              ? `自动 · ${item.judge_provider || "未知 Provider"} / ${item.judge_model || "未知模型"}`
              : "人工";
            return (
              <div className="history-row" key={item.id}>
                <Clock3 size={15} aria-hidden="true" />
                <div><strong>{conclusionLabel(item.conclusion)} · {source}</strong><span>{item.notes || "无备注"}</span></div>
                <div className="history-scores"><span>A {left.toFixed(2)}</span><span>B {right.toFixed(2)}</span></div>
                <time>{formatTime(item.created_at)}</time>
              </div>
            );
          })}
        </div>
      ) : <div className="empty-row">这组运行尚无已保存的评估。</div>}
    </section>
  );
}

function experimentStatusLabel(status) {
  return ({ queued: "排队中", running: "运行中", completed: "已完成", failed: "失败" })[status] || statusLabel(status);
}

function BatchWorkspace({ catalog, initialLeftID, initialRightID }) {
  const workspaceID = catalog.sessions.find((session) => session.id === initialLeftID)?.workspace_id
    || catalog.sessions[0]?.workspace_id || "";
  const templateSessions = useMemo(
    () => catalog.sessions.filter((session) => session.workspace_id === workspaceID && !session.archived_at),
    [catalog.sessions, workspaceID]
  );
  const [datasets, setDatasets] = useState([]);
  const [rubrics, setRubrics] = useState([]);
  const [experiments, setExperiments] = useState([]);
  const [datasetID, setDatasetID] = useState("");
  const [rubricID, setRubricID] = useState("");
  const [experimentID, setExperimentID] = useState("");
  const [leftTemplateID, setLeftTemplateID] = useState(initialLeftID || "");
  const [rightTemplateID, setRightTemplateID] = useState(initialRightID || "");
  const [datasetName, setDatasetName] = useState("");
  const [datasetDescription, setDatasetDescription] = useState("");
  const [datasetRows, setDatasetRows] = useState([{ prompt: "", expectedOutput: "", tags: "" }]);
  const [experimentName, setExperimentName] = useState("");
  const [loading, setLoading] = useState(true);
  const [creatingDataset, setCreatingDataset] = useState(false);
  const [starting, setStarting] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [importPreview, setImportPreview] = useState(null);
  const [datasetExportFormat, setDatasetExportFormat] = useState("json");
  const [experimentExportFormat, setExperimentExportFormat] = useState("json");
  const [error, setError] = useState("");
  const refreshRef = useRef(false);
  const datasetFileRef = useRef(null);

  const selectedExperiment = experiments.find((item) => item.id === experimentID) || experiments[0] || null;
  const selectedDataset = datasets.find((item) => item.id === datasetID) || null;

  const mergeExperiment = useCallback((updated) => {
    setExperiments((current) => [updated, ...current.filter((item) => item.id !== updated.id)]);
    setExperimentID(updated.id);
  }, []);

  useEffect(() => {
    if (!workspaceID) return;
    const controller = new AbortController();
    setLoading(true);
    setError("");
    Promise.all([
      spaceAPI.listDatasets(workspaceID, controller.signal),
      spaceAPI.listRubrics(workspaceID, controller.signal),
      spaceAPI.listExperiments(workspaceID, controller.signal)
    ]).then(([nextDatasets, nextRubrics, nextExperiments]) => {
      setDatasets(nextDatasets);
      setRubrics(nextRubrics);
      setExperiments(nextExperiments);
      setDatasetID((current) => current || nextDatasets[0]?.id || "");
      setRubricID((current) => current || nextRubrics[0]?.id || "");
      setExperimentID((current) => current || nextExperiments[0]?.id || "");
    }).catch((loadError) => {
      if (loadError?.name !== "AbortError") setError(String(loadError?.message || loadError));
    }).finally(() => setLoading(false));
    return () => controller.abort();
  }, [workspaceID]);

  useEffect(() => {
    if (!templateSessions.length) return;
    if (!templateSessions.some((session) => session.id === leftTemplateID)) setLeftTemplateID(templateSessions[0]?.id || "");
    if (!templateSessions.some((session) => session.id === rightTemplateID) || rightTemplateID === leftTemplateID) {
      setRightTemplateID(templateSessions.find((session) => session.id !== leftTemplateID)?.id || "");
    }
  }, [templateSessions, leftTemplateID, rightTemplateID]);

  const refreshExperiment = useCallback(async (targetID = experimentID) => {
    if (!targetID || refreshRef.current) return;
    refreshRef.current = true;
    setRefreshing(true);
    try {
      const updated = await spaceAPI.reconcileExperiment(targetID);
      mergeExperiment(updated);
    } catch (refreshError) {
      setError(String(refreshError?.message || refreshError));
    } finally {
      refreshRef.current = false;
      setRefreshing(false);
    }
  }, [experimentID, mergeExperiment]);

  useEffect(() => {
    if (!selectedExperiment || selectedExperiment.status !== "running") return undefined;
    const timer = window.setInterval(() => refreshExperiment(selectedExperiment.id), 1500);
    return () => window.clearInterval(timer);
  }, [selectedExperiment?.id, selectedExperiment?.status, refreshExperiment]);

  function updateDatasetRow(index, patch) {
    setDatasetRows((current) => current.map((row, rowIndex) => rowIndex === index ? { ...row, ...patch } : row));
  }

  async function previewDatasetFile(event) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    setError("");
    try {
      setImportPreview(parseDatasetFile(await file.text(), file.name));
    } catch (importError) {
      setImportPreview(null);
      setError(String(importError?.message || importError));
    }
  }

  function applyImportPreview() {
    if (!importPreview) return;
    setDatasetName(importPreview.name);
    setDatasetDescription(importPreview.description);
    setDatasetRows(importPreview.items.map((item) => ({
      prompt: item.prompt,
      expectedOutput: item.expectedOutput,
      tags: item.tags.join(", ")
    })));
    setImportPreview(null);
    setError("");
  }

  function exportSelectedDataset() {
    if (selectedDataset) downloadTextFile(serializeDataset(selectedDataset, datasetExportFormat));
  }

  function exportSelectedExperiment() {
    if (selectedExperiment) downloadTextFile(serializeExperiment(selectedExperiment, experimentExportFormat));
  }

  async function createDataset() {
    const items = datasetRows.filter((row) => row.prompt.trim());
    if (!workspaceID || !datasetName.trim() || !items.length || creatingDataset) return;
    setCreatingDataset(true);
    setError("");
    try {
      const created = await spaceAPI.createDataset(workspaceID, {
        name: datasetName.trim(), description: datasetDescription.trim(), items
      });
      setDatasets((current) => [created, ...current]);
      setDatasetID(created.id);
      setExperimentName(`${created.name} 对比`);
      setDatasetName("");
      setDatasetDescription("");
      setDatasetRows([{ prompt: "", expectedOutput: "", tags: "" }]);
    } catch (createError) {
      setError(String(createError?.message || createError));
    } finally {
      setCreatingDataset(false);
    }
  }

  async function startExperiment() {
    if (!datasetID || !leftTemplateID || !rightTemplateID || leftTemplateID === rightTemplateID || starting) return;
    setStarting(true);
    setError("");
    try {
      let activeRubricID = rubricID;
      if (!activeRubricID) {
        const createdRubric = await spaceAPI.createRubric(workspaceID, createEvaluation().criteria);
        setRubrics((current) => [createdRubric, ...current]);
        setRubricID(createdRubric.id);
        activeRubricID = createdRubric.id;
      }
      const activeDataset = datasets.find((item) => item.id === datasetID);
      const created = await spaceAPI.createExperiment({
        name: experimentName.trim() || `${activeDataset?.name || "批量"} 对比`,
        datasetID, rubricID: activeRubricID,
        leftTemplateSessionID: leftTemplateID, rightTemplateSessionID: rightTemplateID
      });
      mergeExperiment(created);
      setExperimentName("");
    } catch (startError) {
      setError(String(startError?.message || startError));
    } finally {
      setStarting(false);
    }
  }

	if (loading) return <main className="batch-main"><div className="empty-state"><FlaskConical size={28} /><strong>正在加载批量实验</strong></div></main>;

  const summary = selectedExperiment?.summary || {};
  return (
    <main className="batch-main">
      {error ? <div className="error-banner">{error}</div> : null}
      <div className="batch-setup-grid">
        <section className="analysis-section dataset-builder">
          <div className="section-heading"><div className="section-title"><Database size={16} /><h2>评测数据集</h2></div><span>{datasets.length} 个</span></div>
          <input className="visually-hidden" ref={datasetFileRef} type="file" aria-label="选择数据集文件" accept=".csv,.json,text/csv,application/json" onChange={previewDatasetFile} />
          <div className="dataset-file-toolbar">
            <div className="file-toolbar-group">
              <button className="secondary-action" type="button" onClick={() => datasetFileRef.current?.click()}><Upload size={15} /> 导入文件</button>
              <button className="secondary-action" type="button" onClick={() => downloadTextFile(serializeDatasetTemplate())}><FileDown size={15} /> 下载模板</button>
            </div>
            <div className="file-toolbar-group export-toolbar">
              <select className="export-format-select" aria-label="数据集导出格式" value={datasetExportFormat} onChange={(event) => setDatasetExportFormat(event.target.value)}><option value="json">JSON</option><option value="csv">CSV</option></select>
              <IconButton label={selectedDataset ? `导出数据集 ${selectedDataset.name}` : "导出选中数据集"} disabled={!selectedDataset} onClick={exportSelectedDataset}><Download size={15} /></IconButton>
            </div>
          </div>
          {importPreview ? (
            <div className="import-preview" aria-label="导入预览">
              <div className="import-preview-heading">
                <div><strong>{importPreview.name}</strong><span>{importPreview.filename} · {importPreview.items.length} 条 · {importPreview.format.toUpperCase()}</span></div>
                <div className="section-actions"><button className="secondary-action" type="button" onClick={() => setImportPreview(null)}>取消</button><button className="primary-action" type="button" onClick={applyImportPreview}>载入编辑</button></div>
              </div>
              <div className="import-preview-list">
                {importPreview.items.map((item, index) => <div className="import-preview-row" key={`${index}-${item.prompt}`}><span>{index + 1}</span><div><strong>{item.prompt}</strong><small>{item.expectedOutput || "无期望结果"}</small></div><small>{item.tags.join(" · ") || "无标签"}</small></div>)}
              </div>
            </div>
          ) : null}
          <div className="batch-form-grid">
            <label>数据集名称<input value={datasetName} onChange={(event) => setDatasetName(event.target.value)} /></label>
            <label>描述<input value={datasetDescription} onChange={(event) => setDatasetDescription(event.target.value)} /></label>
          </div>
          <div className="dataset-row-head"><span>提示词</span><span>期望结果</span><span>标签</span><span /></div>
          <div className="dataset-rows">
            {datasetRows.map((row, index) => (
              <div className="dataset-row" key={index}>
                <textarea aria-label={`样本 ${index + 1} 提示词`} value={row.prompt} onChange={(event) => updateDatasetRow(index, { prompt: event.target.value })} />
                <textarea aria-label={`样本 ${index + 1} 期望结果`} value={row.expectedOutput} onChange={(event) => updateDatasetRow(index, { expectedOutput: event.target.value })} />
                <input aria-label={`样本 ${index + 1} 标签`} value={row.tags} onChange={(event) => updateDatasetRow(index, { tags: event.target.value })} />
                <IconButton label={`删除样本 ${index + 1}`} disabled={datasetRows.length === 1} onClick={() => setDatasetRows((current) => current.filter((_, rowIndex) => rowIndex !== index))}><Trash2 size={15} /></IconButton>
              </div>
            ))}
          </div>
          <div className="builder-actions">
            <button className="secondary-action" type="button" disabled={datasetRows.length >= 20} onClick={() => setDatasetRows((current) => [...current, { prompt: "", expectedOutput: "", tags: "" }])}><Plus size={15} /> 添加样本</button>
            <button className="primary-action" type="button" disabled={!datasetName.trim() || !datasetRows.some((row) => row.prompt.trim()) || creatingDataset} onClick={createDataset}><Save size={15} /> {creatingDataset ? "创建中" : "创建数据集"}</button>
          </div>
        </section>

        <section className="analysis-section experiment-launcher">
          <div className="section-heading"><div className="section-title"><FlaskConical size={16} /><h2>实验配置</h2></div></div>
          <div className="experiment-fields">
            <label>实验名称<input value={experimentName} onChange={(event) => setExperimentName(event.target.value)} /></label>
            <label>数据集<select value={datasetID} onChange={(event) => setDatasetID(event.target.value)}><option value="">选择数据集</option>{datasets.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.items.length} 条</option>)}</select></label>
            <label>评分标准<select value={rubricID} onChange={(event) => setRubricID(event.target.value)}><option value="">默认评分标准</option>{rubrics.map((item) => <option key={item.id} value={item.id}>{item.name} · v{item.revision}</option>)}</select></label>
            <label>模板会话 A<select value={leftTemplateID} onChange={(event) => setLeftTemplateID(event.target.value)}><option value="">选择会话</option>{templateSessions.map((session) => <option key={session.id} value={session.id}>{sessionLabel(session)}</option>)}</select></label>
            <label>模板会话 B<select value={rightTemplateID} onChange={(event) => setRightTemplateID(event.target.value)}><option value="">选择会话</option>{templateSessions.map((session) => <option key={session.id} value={session.id}>{sessionLabel(session)}</option>)}</select></label>
          </div>
          <button className="primary-action launch-action" type="button" disabled={!datasetID || !leftTemplateID || !rightTemplateID || leftTemplateID === rightTemplateID || starting} onClick={startExperiment}><Play size={16} /> {starting ? "启动中" : "启动实验"}</button>
        </section>
      </div>

      <section className="analysis-section experiment-results">
        <div className="section-heading experiment-heading">
          <div className="section-title"><FlaskConical size={16} /><h2>实验结果</h2></div>
          <div className="experiment-select-actions">
            <select className="experiment-picker" aria-label="实验" value={selectedExperiment?.id || ""} onChange={(event) => setExperimentID(event.target.value)}><option value="">选择实验</option>{experiments.map((item) => <option key={item.id} value={item.id}>{item.name} · {experimentStatusLabel(item.status)}</option>)}</select>
            <select className="export-format-select" aria-label="实验结果导出格式" value={experimentExportFormat} onChange={(event) => setExperimentExportFormat(event.target.value)}><option value="json">JSON</option><option value="csv">CSV</option></select>
            <IconButton label="导出实验结果" disabled={!selectedExperiment} onClick={exportSelectedExperiment}><Download size={15} /></IconButton>
            <IconButton label="刷新实验结果" disabled={!selectedExperiment || refreshing} onClick={() => refreshExperiment(selectedExperiment?.id)}><RefreshCw size={15} className={refreshing ? "spinning" : ""} /></IconButton>
          </div>
        </div>
        {selectedExperiment ? (
          <>
            <div className="experiment-summary">
              <div><span>进度</span><strong>{Number(summary.completed || 0) + Number(summary.failed || 0)} / {summary.total || 0}</strong></div>
              <div><span>A 胜</span><strong>{summary.left_wins || 0}</strong></div>
              <div><span>B 胜</span><strong>{summary.right_wins || 0}</strong></div>
              <div><span>持平</span><strong>{summary.ties || 0}</strong></div>
              <div><span>A 平均分</span><strong>{Number(summary.left_average || 0).toFixed(2)}</strong></div>
              <div><span>B 平均分</span><strong>{Number(summary.right_average || 0).toFixed(2)}</strong></div>
            </div>
            <div className="experiment-table">
              <div className="experiment-table-head"><span>样本</span><span>状态</span><span>A / B</span><span>结论</span><span>证据</span></div>
              {selectedExperiment.items.map((item) => (
                <div className="experiment-row" key={item.id}>
                  <div><strong>#{item.item_index + 1} {item.prompt}</strong><small>{item.tags?.join(" · ") || item.expected_output || ""}</small>{item.error_message ? <small className="row-error">{item.error_message}</small> : null}</div>
                  <Pill value={item.status} />
                  <span>{Number(item.left_average || 0).toFixed(2)} / {Number(item.right_average || 0).toFixed(2)}</span>
                  <span>{item.conclusion ? conclusionLabel(item.conclusion) : "-"}</span>
                  {item.left_session_id && item.right_session_id ? <a className="inspector-link" target="_blank" rel="noreferrer" href={`/space#left=${encodeURIComponent(item.left_session_id)}&left_turn=${encodeURIComponent(item.left_turn_id)}&right=${encodeURIComponent(item.right_session_id)}&right_turn=${encodeURIComponent(item.right_turn_id)}`}>查看对比 <ExternalLink size={13} /></a> : <span>-</span>}
                </div>
              ))}
            </div>
          </>
        ) : <div className="empty-row">暂无实验。</div>}
      </section>
    </main>
  );
}

function EmptyState({ loading, error }) {
  return (
    <div className="empty-state">
      <Search size={28} aria-hidden="true" />
      <strong>{loading ? "正在加载对比" : error ? "分析失败" : "请选择两组运行"}</strong>
      <span>{error || (loading ? "正在读取运行证据..." : "")}</span>
    </div>
  );
}

function App() {
	const [view, setView] = useState("comparison");
  const [catalog, setCatalog] = useState({ agents: [], sessions: [] });
  const [agentID, setAgentID] = useState("");
  const [leftID, setLeftID] = useState("");
  const [rightID, setRightID] = useState("");
  const [leftTurnID, setLeftTurnID] = useState("");
  const [rightTurnID, setRightTurnID] = useState("");
  const [leftRuns, setLeftRuns] = useState([]);
  const [rightRuns, setRightRuns] = useState([]);
  const [runLoading, setRunLoading] = useState({ left: false, right: false });
  const [analysis, setAnalysis] = useState(null);
  const [loading, setLoading] = useState(false);
  const [catalogLoading, setCatalogLoading] = useState(true);
  const [error, setError] = useState("");
  const [evaluation, setEvaluation] = useState(createEvaluation);
  const [rubric, setRubric] = useState(null);
  const [evaluations, setEvaluations] = useState([]);
  const [saving, setSaving] = useState(false);
  const [judging, setJudging] = useState(false);
  const [savedAt, setSavedAt] = useState("");
  const requestRef = useRef(null);
  const runRequestRef = useRef({ left: null, right: null });

  const sessions = useMemo(() => {
    const items = agentID ? catalog.sessions.filter((session) => session.agent_id === agentID) : catalog.sessions;
    return [...items].sort((left, right) => String(right.updated_at || right.created_at || "").localeCompare(String(left.updated_at || left.created_at || "")));
  }, [agentID, catalog.sessions]);

  const load = useCallback(async (nextLeft = leftID, nextLeftTurn = leftTurnID, nextRight = rightID, nextRightTurn = rightTurnID) => {
    if (!nextLeft || !nextLeftTurn || !nextRight || !nextRightTurn) {
      setError("");
      setAnalysis(null);
      return;
    }
    if (nextLeft === nextRight && nextLeftTurn === nextRightTurn) {
      setError("请选择两组不同的运行。");
      setAnalysis(null);
      return;
    }
    requestRef.current?.abort();
    const controller = new AbortController();
    requestRef.current = controller;
    setLoading(true);
    setError("");
    setHash(nextLeft, nextLeftTurn, nextRight, nextRightTurn);
    try {
      const result = await spaceAPI.analyze(nextLeft, nextLeftTurn, nextRight, nextRightTurn, controller.signal);
      if (requestRef.current !== controller) return;
      const workspaceID = result.comparison.left.session.workspace_id;
      const [availableRubrics, history] = await Promise.all([
        spaceAPI.listRubrics(workspaceID, controller.signal),
        spaceAPI.listEvaluations(nextLeft, nextLeftTurn, nextRight, nextRightTurn, controller.signal)
      ]);
      if (requestRef.current !== controller) return;
      const latest = history[0];
      const activeRubric = availableRubrics.find((item) => item.id === latest?.rubric_id)
        || availableRubrics.find((item) => item.name === "TMA Space default")
        || availableRubrics[0]
        || null;
      setAnalysis(result);
      setRubric(activeRubric);
      setEvaluations(history);
      const persisted = latest?.rubric_id === activeRubric?.id ? latest : null;
      const nextEvaluation = evaluationFromRubric(activeRubric || latest?.rubric_snapshot, persisted);
      setEvaluation(nextEvaluation);
      setSavedAt(nextEvaluation.savedAt);
    } catch (loadError) {
      if (loadError?.name !== "AbortError") setError(String(loadError?.message || loadError));
    } finally {
      if (requestRef.current === controller) setLoading(false);
    }
  }, [leftID, leftTurnID, rightID, rightTurnID]);

  async function loadRuns(side, sessionID, preferredTurnID = "") {
    runRequestRef.current[side]?.abort();
    if (!sessionID) {
      if (side === "left") { setLeftRuns([]); setLeftTurnID(""); }
      else { setRightRuns([]); setRightTurnID(""); }
      return { runs: [], turnID: "" };
    }
    const controller = new AbortController();
    runRequestRef.current[side] = controller;
    setRunLoading((current) => ({ ...current, [side]: true }));
    try {
      const items = [...await spaceAPI.runs(sessionID, controller.signal)]
        .sort((left, right) => String(right.started_at || "").localeCompare(String(left.started_at || "")));
      const turnID = items.some((run) => run.id === preferredTurnID) ? preferredTurnID : items[0]?.id || "";
      if (side === "left") { setLeftRuns(items); setLeftTurnID(turnID); }
      else { setRightRuns(items); setRightTurnID(turnID); }
      return { runs: items, turnID };
    } catch (runError) {
      if (runError?.name !== "AbortError") setError(String(runError?.message || runError));
      return { runs: [], turnID: "" };
    } finally {
      if (runRequestRef.current[side] === controller) setRunLoading((current) => ({ ...current, [side]: false }));
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    const params = hashParams();
    spaceAPI.catalogs(controller.signal).then(async (result) => {
      const sorted = [...result.sessions].sort((left, right) => String(right.updated_at || right.created_at || "").localeCompare(String(left.updated_at || left.created_at || "")));
      setCatalog({ ...result, sessions: sorted });
      const hashLeft = params.get("left") || "";
      const hashRight = params.get("right") || "";
      const nextLeft = sorted.some((session) => session.id === hashLeft) ? hashLeft : sorted[0]?.id || "";
      const nextRight = sorted.some((session) => session.id === hashRight) ? hashRight : sorted.find((session) => session.id !== nextLeft && session.workspace_id === sorted.find((item) => item.id === nextLeft)?.workspace_id)?.id || "";
      setLeftID(nextLeft);
      setRightID(nextRight);
      const [leftResult, rightResult] = await Promise.all([
        loadRuns("left", nextLeft, params.get("left_turn") || ""),
        loadRuns("right", nextRight, params.get("right_turn") || "")
      ]);
      setCatalogLoading(false);
      if (nextLeft && leftResult.turnID && nextRight && rightResult.turnID) {
        load(nextLeft, leftResult.turnID, nextRight, rightResult.turnID);
      }
    }).catch((catalogError) => {
      if (catalogError?.name !== "AbortError") setError(String(catalogError?.message || catalogError));
      setCatalogLoading(false);
    });
    return () => controller.abort();
  }, []);

  function clearComparison() {
    requestRef.current?.abort();
    setAnalysis(null);
    setRubric(null);
    setEvaluations([]);
    setEvaluation(createEvaluation());
    setSavedAt("");
    setError("");
  }

  function changeSession(side, sessionID) {
    clearComparison();
    if (side === "left") setLeftID(sessionID);
    else setRightID(sessionID);
    loadRuns(side, sessionID);
  }

  async function changeAgent(nextAgentID) {
    setAgentID(nextAgentID);
    const filtered = nextAgentID ? catalog.sessions.filter((session) => session.agent_id === nextAgentID) : catalog.sessions;
    const nextLeft = filtered[0]?.id || "";
    const nextRight = filtered.find((session) => session.id !== nextLeft && session.workspace_id === filtered[0]?.workspace_id)?.id || "";
    setLeftID(nextLeft);
    setRightID(nextRight);
    clearComparison();
    await Promise.all([loadRuns("left", nextLeft), loadRuns("right", nextRight)]);
  }

  function swap() {
    setLeftID(rightID);
    setRightID(leftID);
    setLeftTurnID(rightTurnID);
    setRightTurnID(leftTurnID);
    setLeftRuns(rightRuns);
    setRightRuns(leftRuns);
    load(rightID, rightTurnID, leftID, leftTurnID);
  }

  async function saveEvaluation() {
    if (!analysis || saving) return;
    setSaving(true);
    setError("");
    try {
      const workspaceID = analysis.comparison.left.session.workspace_id;
      const activeRubric = rubric || await spaceAPI.createRubric(workspaceID, evaluation.criteria);
      if (!rubric) setRubric(activeRubric);
      const saved = await spaceAPI.saveEvaluation({
        leftSessionID: leftID, leftTurnID, rightSessionID: rightID, rightTurnID,
        rubricID: activeRubric.id, evaluation
      });
      setEvaluations((current) => [saved, ...current]);
      setSavedAt(saved.created_at);
      setEvaluation(evaluationFromRubric(activeRubric, saved));
    } catch (saveError) {
      setError(String(saveError?.message || saveError));
    } finally {
      setSaving(false);
    }
  }

  async function autoEvaluate() {
    if (!analysis || judging || saving) return;
    setJudging(true);
    setError("");
    try {
      const workspaceID = analysis.comparison.left.session.workspace_id;
      const activeRubric = rubric || await spaceAPI.createRubric(workspaceID, evaluation.criteria);
      if (!rubric) setRubric(activeRubric);
      const saved = await spaceAPI.autoEvaluate({
        leftSessionID: leftID, leftTurnID, rightSessionID: rightID, rightTurnID,
        rubricID: activeRubric.id
      });
      setEvaluations((current) => [saved, ...current]);
      setSavedAt(saved.created_at);
      setEvaluation(evaluationFromRubric(saved.rubric_snapshot || activeRubric, saved));
    } catch (judgeError) {
      setError(String(judgeError?.message || judgeError));
    } finally {
      setJudging(false);
    }
  }

  function resetEvaluation() {
    setEvaluation(evaluationFromRubric(rubric));
    setSavedAt("");
  }

  function exportEvaluation() {
    const payload = JSON.stringify({
      leftSessionID: leftID, leftTurnID, rightSessionID: rightID, rightTurnID,
      rubricID: rubric?.id || "", ...evaluation
    }, null, 2);
    const url = URL.createObjectURL(new Blob([payload], { type: "application/json" }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `tma-space-${leftTurnID}-${rightTurnID}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  const left = analysis ? summarizeEvidence(analysis.left) : null;
  const right = analysis ? summarizeEvidence(analysis.right) : null;

  return (
    <div className="app-shell">
      <header>
		<div className="header-start">
		  <div className="brand"><Scale size={20} aria-hidden="true" /><h1>TMA Space</h1><span>运行分析</span></div>
		  <div className="view-tabs" role="tablist" aria-label="Space 视图"><button type="button" role="tab" aria-selected={view === "comparison"} className={view === "comparison" ? "active" : ""} onClick={() => setView("comparison")}>单次分析</button><button type="button" role="tab" aria-selected={view === "batch"} className={view === "batch" ? "active" : ""} onClick={() => setView("batch")}>批量实验</button></div>
		</div>
        <div className="header-links"><a href="/app">工作台</a><a href="/inspector">检查器</a></div>
      </header>

	  {view === "comparison" ? <><div className="comparison-toolbar">
        <label className="agent-filter"><span>智能体</span><select value={agentID} onChange={(event) => changeAgent(event.target.value)}><option value="">全部智能体</option>{catalog.agents.map((agent) => <option value={agent.id} key={agent.id}>{agent.name || agent.id}</option>)}</select></label>
        <RunPicker label="运行 A" sessionID={leftID} turnID={leftTurnID} sessions={sessions} runs={leftRuns} loading={runLoading.left} onSessionChange={(value) => changeSession("left", value)} onTurnChange={(value) => { setLeftTurnID(value); clearComparison(); }} />
        <IconButton label="交换两侧运行" disabled={!leftTurnID || !rightTurnID} onClick={swap}><ArrowLeftRight size={17} /></IconButton>
        <RunPicker label="运行 B" sessionID={rightID} turnID={rightTurnID} sessions={sessions} runs={rightRuns} loading={runLoading.right} onSessionChange={(value) => changeSession("right", value)} onTurnChange={(value) => { setRightTurnID(value); clearComparison(); }} />
        <button className="analyze-button" type="button" disabled={loading || !leftTurnID || !rightTurnID} onClick={() => load()}><RefreshCw size={16} className={loading ? "spinning" : ""} /> 分析</button>
	  </div>

	  <main>
        {!left || !right ? <EmptyState loading={catalogLoading || loading} error={error} /> : (
          <>
            {error ? <div className="error-banner">{error}</div> : null}
            <div className="run-headings"><EvidenceHeader label="运行 A" summary={left} /><EvidenceHeader label="运行 B" summary={right} /></div>
            <section className="metric-strip" aria-label="运行指标">
              <Metric label="总耗时" left={left.durationMS} right={right.durationMS} format={formatDuration} unit=" ms" lowerIsBetter />
              <Metric label="LLM 延迟" left={left.latencyMS} right={right.latencyMS} format={formatDuration} unit=" ms" lowerIsBetter />
              <Metric label="Token 数" left={left.totalTokens} right={right.totalTokens} lowerIsBetter />
              <Metric label="模型调用" left={left.modelCalls} right={right.modelCalls} lowerIsBetter />
              <Metric label="工具调用" left={left.toolCalls} right={right.toolCalls} lowerIsBetter />
              <Metric label="错误数" left={left.errors} right={right.errors} lowerIsBetter />
              <Metric label="关键路径" left={left.criticalPathMS} right={right.criticalPathMS} format={formatDuration} unit=" ms" lowerIsBetter />
              <Metric label="产物数" left={left.artifactCount} right={right.artifactCount} />
            </section>
            <TextEvidence title="提示词" left={left.prompt} right={right.prompt} empty="未记录提示词。" />
            <TextEvidence title="运行结果" left={left.result} right={right.result} empty="未记录运行结果。" />
            <TextEvidence title="摘要" left={left.summary} right={right.summary} empty="未记录摘要。" />
            <TraceEvidence left={left} right={right} />
            <Diagnostics left={left} right={right} />
            <Rubric evaluation={evaluation} setEvaluation={setEvaluation} onSave={saveEvaluation} onAuto={autoEvaluate} onReset={resetEvaluation} onExport={exportEvaluation} saved={savedAt} saving={saving} judging={judging} />
            <EvaluationHistory evaluations={evaluations} />
          </>
        )}
	  </main></> : <BatchWorkspace catalog={catalog} initialLeftID={leftID} initialRightID={rightID} />}
    </div>
  );
}

createRoot(document.getElementById("root")).render(<App />);
