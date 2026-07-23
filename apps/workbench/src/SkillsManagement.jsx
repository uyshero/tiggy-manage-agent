import React, { useEffect, useMemo, useState } from "react";
import * as api from "./api.js";
import {
  SkillInputsValidationError,
  buildSkillInputs,
  initialSkillInputValues,
  inputSchemaFromVersion,
  schemaFields,
  skillBindingState,
  skillBindingsFromConfig,
  skillConfigSyncState
} from "./skillInputs.js";
import {
  marketplaceEntryNextAction,
  marketplaceEntryStageState,
  marketplaceEntryStages,
  marketplaceEntryStatusLabel,
  marketplaceEntryStatusTone,
  marketplaceInstallStateMeta,
  marketplaceUpgradeVersions
} from "./marketplaceCatalog.js";

const emptyPolicyForm = {
  scopeType: "workspace",
  organizationID: "org_default",
  allowedOwners: "",
  allowedRepositories: "",
  allowedLicenses: "",
  deniedLicenses: "",
  requireCommitSHA: false,
  requireLicense: false,
  requireAttestation: false,
  staticScanBlockSeverity: "",
  trustedAttestationKeys: "{}"
};

const emptyMarketplaceEntryForm = {
  skillID: "",
  skillVersion: "",
  summary: "",
  category: "",
  tags: ""
};

const policyCheckLabels = {
  repository_allowlist: "仓库来源",
  commit_ref_pin: "Commit 固定",
  license: "许可证",
  attestation: "包签名",
  static_scan: "静态扫描",
  binary_scan: "二进制扫描"
};

function splitValues(value) {
  return String(value || "")
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean);
}

function joinValues(values) {
  return Array.isArray(values) ? values.join("\n") : "";
}

function shortHash(value, size = 12) {
  const text = String(value || "");
  return text.length > size ? `${text.slice(0, size)}...` : text || "-";
}

function formatDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "-" : date.toLocaleString("zh-CN", { hour12: false });
}

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`;
}

function assetBundleFromVersion(version) {
  const value = version?.assets;
  if (!value) return { files: [], sbom: {} };
  if (Array.isArray(value)) return { files: value, sbom: {} };
  if (typeof value === "object") return { files: value.files || [], sbom: value.sbom || {} };
  try {
    const decoded = JSON.parse(value);
    return Array.isArray(decoded) ? { files: decoded, sbom: {} } : { files: decoded.files || [], sbom: decoded.sbom || {} };
  } catch {
    return { files: [], sbom: {} };
  }
}

function StateTag({ children, tone = "neutral" }) {
  return <span className={`skills-state ${tone}`}>{children}</span>;
}

function toneForState(value) {
  switch (String(value || "").toLowerCase()) {
    case "active":
    case "verified":
    case "new_install":
    case "upgrade":
    case "passed":
    case "succeeded":
    case "deleted":
      return "ok";
    case "blocked":
    case "invalid":
    case "critical":
    case "high":
    case "archived":
    case "failed":
      return "danger";
    case "missing":
    case "untrusted":
    case "medium":
    case "unchanged":
    case "partial":
    case "skipped":
      return "warn";
    default:
      return "neutral";
  }
}

function policyFormFromDetail(detail) {
  const config = detail?.version?.config || {};
  const policy = detail?.policy || {};
  return {
    scopeType: policy.scope_type || "workspace",
    organizationID: policy.organization_id || "org_default",
    allowedOwners: joinValues(config.allowed_owners),
    allowedRepositories: joinValues(config.allowed_repositories),
    allowedLicenses: joinValues(config.allowed_licenses),
    deniedLicenses: joinValues(config.denied_licenses),
    requireCommitSHA: Boolean(config.require_commit_sha),
    requireLicense: Boolean(config.require_license),
    requireAttestation: Boolean(config.require_attestation),
    staticScanBlockSeverity: config.static_scan_block_severity || "",
    trustedAttestationKeys: JSON.stringify(config.trusted_attestation_keys || {}, null, 2)
  };
}

function policyConfigFromForm(form) {
  let trustedKeys = {};
  const keySource = form.trustedAttestationKeys.trim();
  if (keySource) {
    trustedKeys = JSON.parse(keySource);
    if (!trustedKeys || Array.isArray(trustedKeys) || typeof trustedKeys !== "object") {
      throw new Error("Attestation 公钥必须是 key ID 到 Base64 公钥的 JSON 对象。");
    }
  }
  return {
    allowed_owners: splitValues(form.allowedOwners),
    allowed_repositories: splitValues(form.allowedRepositories),
    require_commit_sha: form.requireCommitSHA,
    allowed_licenses: splitValues(form.allowedLicenses),
    denied_licenses: splitValues(form.deniedLicenses),
    require_license: form.requireLicense,
    require_attestation: form.requireAttestation,
    trusted_attestation_keys: trustedKeys,
    static_scan_block_severity: form.staticScanBlockSeverity
  };
}

function SkillInputField({ error, field, index, onChange, onClear, value }) {
  const id = `skill-input-${index}`;
  const label = <>{field.title}{field.required ? <span className="skill-input-required" aria-label="必填">*</span> : null}</>;
  if (field.control === "boolean") {
    return (
      <div className="skill-input-field">
        <label className="skill-input-toggle" htmlFor={id}>
          <input id={id} type="checkbox" checked={value === true} onChange={(event) => onChange(event.target.checked)} />
          <span><strong>{label}</strong>{field.description ? <small>{field.description}</small> : null}</span>
        </label>
        {!field.required && value !== undefined ? <button className="skill-input-clear" type="button" onClick={onClear}>清除</button> : null}
        {error ? <small className="skill-input-error" role="alert">{error}</small> : null}
      </div>
    );
  }
  let control;
  if (field.control === "enum") {
    control = (
      <select id={id} value={value ?? ""} onChange={(event) => onChange(event.target.value)} aria-invalid={Boolean(error)}>
        <option value="">请选择</option>
        {field.options.map((option) => <option value={option.token} key={option.token}>{option.label}</option>)}
      </select>
    );
  } else if (field.control === "number") {
    control = <input id={id} type="number" value={value ?? ""} min={field.minimum} max={field.maximum} step={field.type === "integer" ? 1 : "any"} onChange={(event) => onChange(event.target.value)} aria-invalid={Boolean(error)} />;
  } else if (field.control === "textarea" || field.control === "json") {
    control = <textarea id={id} className={field.control === "json" ? "code-input" : ""} rows={field.control === "json" ? 4 : 3} value={value ?? ""} minLength={field.minLength} maxLength={field.maxLength} spellCheck={field.control !== "json"} onChange={(event) => onChange(event.target.value)} aria-invalid={Boolean(error)} />;
  } else {
    control = <input id={id} type="text" value={value ?? ""} minLength={field.minLength} maxLength={field.maxLength} onChange={(event) => onChange(event.target.value)} aria-invalid={Boolean(error)} />;
  }
  return (
    <div className="skill-input-field">
      <span className="skill-input-label"><label htmlFor={id}><strong>{label}</strong></label>{!field.required && value !== undefined ? <button className="skill-input-clear" type="button" onClick={onClear}>清除</button> : null}</span>
      {control}
      {field.description ? <small>{field.description}</small> : null}
      {error ? <small className="skill-input-error" role="alert">{error}</small> : null}
    </div>
  );
}

function SkillVersionEnableForm({ binding, busy, onEnable, version }) {
  const schema = inputSchemaFromVersion(version);
  const fields = schemaFields(schema);
  const bindingKey = JSON.stringify(binding || null);
  const [values, setValues] = useState(() => initialSkillInputValues(schema, binding?.inputs));
  const [mode, setMode] = useState(binding?.mode || "summary");
  const [priority, setPriority] = useState(String(binding?.priority ?? 100));
  const [errors, setErrors] = useState({});

  useEffect(() => {
    setValues(initialSkillInputValues(schema, binding?.inputs));
    setMode(binding?.mode || "summary");
    setPriority(String(binding?.priority ?? 100));
    setErrors({});
  }, [bindingKey, version.id]);

  function updateValue(name, value) {
    setValues((current) => ({ ...current, [name]: value }));
    setErrors((current) => ({ ...current, [name]: undefined }));
  }

  function clearValue(name) {
    setValues((current) => Object.fromEntries(Object.entries(current).filter(([key]) => key !== name)));
    setErrors((current) => ({ ...current, [name]: undefined }));
  }

  function submit(event) {
    event.preventDefault();
    try {
      const parsedPriority = Number(priority);
      if (!Number.isInteger(parsedPriority) || parsedPriority < -1000 || parsedPriority > 1000) {
        setErrors((current) => ({ ...current, binding: "优先级必须是 -1000 到 1000 之间的整数" }));
        return;
      }
      const inputs = schema ? buildSkillInputs(schema, values) : (binding?.version === version.version ? binding.inputs || {} : {});
      setErrors({});
      onEnable(version, inputs, mode, parsedPriority === 0 ? 100 : parsedPriority);
    } catch (error) {
      if (error instanceof SkillInputsValidationError) setErrors(error.fields);
      else throw error;
    }
  }

  return (
    <form className="skill-version-enable-form" onSubmit={submit}>
      <div className="skill-binding-controls">
        <label>渲染模式<select value={mode} onChange={(event) => setMode(event.target.value)}><option value="summary">Summary（按需读取全文）</option><option value="full">Full</option><option value="examples_only">Examples only</option></select></label>
        <label>优先级<input type="number" min="-1000" max="1000" step="1" value={priority} onChange={(event) => { setPriority(event.target.value); setErrors((current) => ({ ...current, binding: undefined })); }} /></label>
      </div>
      {fields.length ? <div className="skill-input-grid">{fields.map((field, index) => (
        <SkillInputField
          error={errors[field.name]}
          field={field}
          index={`${version.id}-${index}`}
          key={field.name}
          value={values[field.name]}
          onChange={(value) => updateValue(field.name, value)}
          onClear={() => clearValue(field.name)}
        />
      ))}</div> : null}
      {errors.binding ? <small className="skill-input-error" role="alert">{errors.binding}</small> : null}
      <button type="submit" disabled={busy}>{busy ? "保存中" : (binding?.version === version.version ? `保存 v${version.version} 配置` : `启用 v${version.version}`)}</button>
    </form>
  );
}

function BindingDetail({ binding, label }) {
  return (
    <div className="skill-binding-column">
      <span>{label}</span>
      {binding ? (
        <>
          <strong>v{binding.version} · {binding.mode} · P{binding.priority}</strong>
          <pre>{JSON.stringify(binding.inputs || {}, null, 2)}</pre>
        </>
      ) : <strong>未启用</strong>}
    </div>
  );
}

function InstalledSkillsView({
  agentBindings,
  agentConfigVersion,
  onApplyAgentConfig,
  onSkillsChanged,
  session,
  sessionBindings,
  sessionConfigVersion,
  sessionID,
  skills
}) {
  const [query, setQuery] = useState("");
  const [selectedSkillID, setSelectedSkillID] = useState("");
  const [versions, setVersions] = useState([]);
  const [busyAction, setBusyAction] = useState("");
  const [confirmArchiveID, setConfirmArchiveID] = useState("");
  const [confirmDisableID, setConfirmDisableID] = useState("");
  const [message, setMessage] = useState(null);
  const agentBindingMap = useMemo(() => new Map(agentBindings.map((binding) => [binding.skill, binding])), [agentBindings]);
  const sessionBindingMap = useMemo(() => new Map(sessionBindings.map((binding) => [binding.skill, binding])), [sessionBindings]);
  const configSync = skillConfigSyncState(agentConfigVersion, sessionConfigVersion, Boolean(sessionID));
  const configNeedsApply = configSync.needsApply;
  const filteredSkills = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return skills;
    return skills.filter((skill) => [skill.identifier, skill.title, skill.description, skill.source_locator]
      .some((value) => String(value || "").toLowerCase().includes(normalized)));
  }, [query, skills]);
  const selectedSkill = skills.find((skill) => skill.id === selectedSkillID) || null;
  const selectedAgentBinding = selectedSkill ? agentBindingMap.get(selectedSkill.identifier) : null;
  const selectedSessionBinding = selectedSkill ? sessionBindingMap.get(selectedSkill.identifier) : null;

  async function inspectSkill(skill) {
    setSelectedSkillID(skill.id);
    setBusyAction(`inspect:${skill.id}`);
    setMessage(null);
    try {
      const response = await api.skillVersions(skill.id);
      setVersions(response.versions || []);
    } catch (error) {
      setVersions([]);
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusyAction("");
    }
  }

  async function enableSkill(skill) {
    if (!sessionID) {
      setMessage({ tone: "warn", text: "需要先打开一个任务会话，才能把 Skill 固定到当前 Agent 配置。" });
      return;
    }
    setBusyAction(`enable:${skill.id}`);
    setMessage(null);
    try {
      const versionsResponse = await api.skillVersions(skill.id);
      const availableVersions = versionsResponse.versions || [];
      setSelectedSkillID(skill.id);
      setVersions(availableVersions);
      const latest = availableVersions[0];
      if (!latest) {
        setMessage({ tone: "warn", text: `${skill.identifier} 还没有可启用的版本。` });
        return;
      }
      if (schemaFields(inputSchemaFromVersion(latest)).length) {
        setMessage({ tone: "warn", text: `请先配置 ${skill.identifier} v${latest.version} 的参数。` });
        return;
      }
      const response = await api.enableSkill(skill.id, { session_id: sessionID, version: latest.version, mode: "summary", priority: 100, inputs: {} });
      await onSkillsChanged();
      setMessage({
        tone: "ok",
        text: response.changed === false
          ? `Agent 已经使用 ${skill.identifier} v${latest.version} 的相同配置。`
          : response.requires_session_upgrade
          ? `已写入 Agent 配置版本 #${response.new_config_version}；当前 Session 已固定配置，需要手动升级。`
          : `已为当前 Agent 启用 ${skill.identifier}；下一条消息自动生效。`
      });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusyAction("");
    }
  }

  async function enableSkillVersion(skill, version, inputs, mode, priority) {
    if (!sessionID) {
      setMessage({ tone: "warn", text: "需要先打开一个任务会话，才能把 Skill 固定到当前 Agent 配置。" });
      return;
    }
    const action = `enable:${skill.id}:${version.version}`;
    setBusyAction(action);
    setMessage(null);
    try {
      const response = await api.enableSkill(skill.id, {
        session_id: sessionID,
        version: version.version,
        mode,
        priority,
        inputs
      });
      await onSkillsChanged();
      setMessage({
        tone: "ok",
        text: response.changed === false
          ? `${skill.identifier} v${version.version} 的 binding 没有变化，未创建新配置版本。`
          : response.requires_session_upgrade
          ? `已写入 Agent 配置版本 #${response.new_config_version}；当前 Session 已固定配置，需要手动升级。`
          : `已为当前 Agent 启用 ${skill.identifier} v${version.version}；下一条消息自动生效。`
      });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusyAction("");
    }
  }

  async function disableSkill(skill) {
    if (!sessionID) {
      setMessage({ tone: "warn", text: "需要先打开一个任务会话，才能修改当前 Agent 配置。" });
      return;
    }
    const action = `disable:${skill.id}`;
    setBusyAction(action);
    setMessage(null);
    try {
      const response = await api.disableSkill(skill.id, { session_id: sessionID });
      setConfirmDisableID("");
      await onSkillsChanged();
      setMessage({
        tone: "ok",
        text: response.removed
          ? `已从 Agent 最新配置停用 ${skill.identifier}；${response.requires_session_upgrade ? "当前 Session 已固定配置，需要手动升级。" : "下一条消息自动生效。"}`
          : `${skill.identifier} 已经处于停用状态。`
      });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusyAction("");
    }
  }

  async function applyAgentConfig() {
    if (!configNeedsApply || !onApplyAgentConfig) return;
    setBusyAction(`apply:${agentConfigVersion}`);
    setMessage(null);
    try {
      await onApplyAgentConfig(agentConfigVersion);
      setMessage({ tone: "ok", text: `当前任务已精确应用 Agent 配置 #${agentConfigVersion}。` });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusyAction("");
    }
  }

  async function archiveSkill(skill) {
    setBusyAction(`archive:${skill.id}`);
    setMessage(null);
    try {
      await api.archiveSkill(skill.id);
      setConfirmArchiveID("");
      setSelectedSkillID((current) => current === skill.id ? "" : current);
      setMessage({ tone: "ok", text: `${skill.identifier} 已归档，历史版本和既有会话仍可回放。` });
      await onSkillsChanged();
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusyAction("");
    }
  }

  return (
    <div className="skills-view-stack">
      {message ? <div className={`skills-notice ${message.tone}`}>{message.text}</div> : null}
      {sessionID ? (
        <div className={`skills-config-sync ${configNeedsApply ? "pending" : "synced"}`}>
          <div className="skills-config-versions">
            <span><small>Agent 最新</small><strong>#{agentConfigVersion || "-"}</strong></span>
            <span><small>当前任务</small><strong>#{sessionConfigVersion || "-"}</strong></span>
            <StateTag tone={configNeedsApply ? "warn" : "ok"}>{configNeedsApply ? "待应用" : "已同步"}</StateTag>
          </div>
          {configNeedsApply ? <button type="button" disabled={busyAction === `apply:${agentConfigVersion}` || session?.status !== "idle"} title={session?.status === "idle" ? "精确应用该 Agent 配置版本" : "当前任务结束后才能应用配置"} onClick={applyAgentConfig}>{busyAction === `apply:${agentConfigVersion}` ? "应用中" : `应用 Agent #${agentConfigVersion}`}</button> : null}
        </div>
      ) : null}
      <div className="skills-toolbar">
        <label>
          搜索已安装 Skill
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="名称、标识或来源仓库" />
        </label>
        <div className="skills-count"><strong>{filteredSkills.length}</strong><span>个结果</span></div>
      </div>
      <div className={`skills-master-detail ${selectedSkill ? "has-detail" : ""}`}>
        <div className="skills-list" aria-label="已安装 Skills">
          {filteredSkills.length ? filteredSkills.map((skill) => {
            const agentBinding = agentBindingMap.get(skill.identifier);
            const sessionBinding = sessionBindingMap.get(skill.identifier);
            const bindingState = skillBindingState(agentBinding, sessionBinding);
            const active = skill.status === "active";
            return (
              <article className={`skill-list-row ${selectedSkillID === skill.id ? "selected" : ""}`} key={skill.id}>
                <button className="skill-row-main" type="button" onClick={() => inspectSkill(skill)}>
                  <span className="skill-monogram" aria-hidden="true">{String(skill.title || skill.identifier).slice(0, 1).toUpperCase()}</span>
                  <span className="skill-row-copy">
                    <span className="skill-row-title"><strong>{skill.title || skill.identifier}</strong><code>{skill.identifier}</code></span>
                    <span>{skill.description || "未提供描述"}</span>
                    <span className="skill-row-meta">{skill.source_type || "inline"}{skill.source_locator ? ` · ${skill.source_locator}` : ""}</span>
                  </span>
                </button>
                <div className="skill-row-actions">
                  <StateTag tone={toneForState(skill.status)}>{skill.status}</StateTag>
                  {agentBinding ? <StateTag tone="ok">Agent v{agentBinding.version}</StateTag> : null}
                  {sessionBinding ? <StateTag tone={bindingState.synced ? "ok" : "warn"}>任务 v{sessionBinding.version}</StateTag> : null}
                  {bindingState.pendingApply ? <StateTag tone="warn">待应用</StateTag> : null}
                  {bindingState.sessionStillEnabled ? <StateTag tone="warn">任务仍启用</StateTag> : null}
                  {active && !agentBinding ? <button className="secondary" type="button" disabled={busyAction === `enable:${skill.id}`} onClick={() => enableSkill(skill)}>{busyAction === `enable:${skill.id}` ? "启用中" : "启用"}</button> : null}
                  {agentBinding && confirmDisableID !== skill.id ? <button className="secondary danger" type="button" onClick={() => setConfirmDisableID(skill.id)}>停用</button> : null}
                  {confirmDisableID === skill.id ? (
                    <span className="skills-confirm-actions">
                      <button className="secondary" type="button" onClick={() => setConfirmDisableID("")}>取消</button>
                      <button type="button" disabled={busyAction === `disable:${skill.id}`} onClick={() => disableSkill(skill)}>{busyAction === `disable:${skill.id}` ? "停用中" : "确认停用"}</button>
                    </span>
                  ) : null}
                  {active && confirmArchiveID !== skill.id ? <button className="secondary danger" type="button" disabled={Boolean(agentBinding)} title={agentBinding ? "先从 Agent 最新配置停用此 Skill" : "归档 Skill"} onClick={() => setConfirmArchiveID(skill.id)}>归档</button> : null}
                  {confirmArchiveID === skill.id ? (
                    <span className="skills-confirm-actions">
                      <button className="secondary" type="button" onClick={() => setConfirmArchiveID("")}>取消</button>
                      <button type="button" disabled={busyAction === `archive:${skill.id}`} onClick={() => archiveSkill(skill)}>确认归档</button>
                    </span>
                  ) : null}
                </div>
              </article>
            );
          }) : <div className="skills-empty">没有匹配的已安装 Skill。</div>}
        </div>
        {selectedSkill ? (
          <aside className="skill-detail-pane">
            <div className="skill-detail-header">
              <div><span>版本记录</span><strong>{selectedSkill.title || selectedSkill.identifier}</strong></div>
              <button className="secondary" type="button" onClick={() => setSelectedSkillID("")}>关闭</button>
            </div>
            <div className="skill-binding-summary">
              <BindingDetail binding={selectedAgentBinding} label={`Agent #${agentConfigVersion || "-"}`} />
              <BindingDetail binding={selectedSessionBinding} label={`当前任务 #${sessionConfigVersion || "-"}`} />
            </div>
            {busyAction === `inspect:${selectedSkill.id}` ? <div className="skills-empty">正在读取版本...</div> : null}
            {versions.map((version) => (
              <div className="skill-version-row" key={version.id}>
                <div><strong>v{version.version}</strong><StateTag tone="neutral">{version.content_format}</StateTag></div>
                <dl>
                  <dt>Checksum</dt><dd title={version.checksum_sha256}>{shortHash(version.checksum_sha256, 16)}</dd>
                  <dt>Revision</dt><dd title={version.source_revision}>{shortHash(version.source_revision, 16)}</dd>
				  <dt>Package</dt><dd>{version.package_format || "legacy_db"}</dd>
				  <dt>对象路径</dt><dd title={version.package_root}>{version.package_root || "数据库兼容存储"}</dd>
                  <dt>发布时间</dt><dd>{formatDate(version.created_at)}</dd>
                </dl>
                {version.source_url ? <a href={version.source_url} target="_blank" rel="noreferrer">查看来源</a> : null}
				{version.package_object_ref_id ? <a href={api.skillPackageDownloadPath(selectedSkill.id, version.version)} target="_blank" rel="noreferrer">下载文件包</a> : null}
                {assetBundleFromVersion(version).files.length ? (
                  <div className="installed-asset-list">
                    {assetBundleFromVersion(version).files.map((file) => (
                      <div className="installed-asset-row" key={file.path}>
                        <div>
                          <strong>{file.path}</strong>
                          <span>{file.binary ? file.content_type || "binary" : "text"} · {file.size || 0} bytes</span>
                          {file.binary && file.scan_provider ? <span>{file.scan_provider}{file.scan_version ? ` · ${file.scan_version}` : ""}</span> : null}
                        </div>
                        {file.binary ? <StateTag tone={toneForState(file.scan_status)}>{file.scan_status || "unknown"}</StateTag> : <StateTag>text</StateTag>}
                        {file.binary && file.object_ref_id && sessionID ? <a href={api.objectRefDownloadPath(file.object_ref_id, sessionID)} target="_blank" rel="noreferrer">受控下载</a> : null}
                      </div>
                    ))}
                  </div>
                ) : null}
                <SkillVersionEnableForm
                  binding={selectedAgentBinding}
                  busy={busyAction === `enable:${selectedSkill.id}:${version.version}`}
                  onEnable={(targetVersion, inputs, mode, priority) => enableSkillVersion(selectedSkill, targetVersion, inputs, mode, priority)}
                  version={version}
                />
              </div>
            ))}
            {!versions.length && busyAction !== `inspect:${selectedSkill.id}` ? <div className="skills-empty">还没有已发布版本。</div> : null}
          </aside>
        ) : null}
      </div>
    </div>
  );
}

function MarketplaceView({ onSkillsChanged, sessionID }) {
  const [searchMode, setSearchMode] = useState("internal");
  const [searchValue, setSearchValue] = useState("");
  const [sourceRef, setSourceRef] = useState("main");
  const [sourcePath, setSourcePath] = useState("SKILL.md");
  const [offlineFile, setOfflineFile] = useState(null);
  const [discovering, setDiscovering] = useState(false);
  const [candidates, setCandidates] = useState([]);
  const [previewingKey, setPreviewingKey] = useState("");
  const [preview, setPreview] = useState(null);
  const [identifier, setIdentifier] = useState("");
  const [installing, setInstalling] = useState(false);
  const [confirmingUpgrade, setConfirmingUpgrade] = useState(false);
  const [message, setMessage] = useState(null);

  useEffect(() => {
    if (!sessionID) return undefined;
    let active = true;
    setDiscovering(true);
    api.discoverInternalSkillsMarketplace({ sessionId: sessionID, limit: 30 }).then((response) => {
      if (active) setCandidates(response.items || []);
    }).catch((error) => {
      if (active) setMessage({ tone: "danger", text: error.message });
    }).finally(() => {
      if (active) setDiscovering(false);
    });
    return () => { active = false; };
  }, [sessionID]);

  async function discover(event) {
    event.preventDefault();
    if (!sessionID) {
      setMessage({ tone: "warn", text: "请先打开一个任务会话，再访问当前 workspace 的 Marketplace。" });
      return;
    }
    const value = searchValue.trim();
    if (!["offline", "internal"].includes(searchMode) && !value) return;
    if (searchMode === "offline" && !offlineFile) return;
    setDiscovering(true);
    setMessage(null);
    setPreview(null);
    setConfirmingUpgrade(false);
    try {
      if (searchMode === "internal") {
        const response = await api.discoverInternalSkillsMarketplace({
          sessionId: sessionID,
          query: value,
          limit: 30
        });
        setCandidates(response.items || []);
        if (!(response.items || []).length) setMessage({ tone: "neutral", text: "内部市场暂无匹配的已发布 Skill。" });
        return;
      }
      if (searchMode === "offline") {
        if (offlineFile.size <= 0 || offlineFile.size > 8 * 1024 * 1024) {
          throw new Error("ZIP 文件大小必须在 1 byte 到 8 MiB 之间。");
        }
        const upload = await api.uploadSessionArtifact(sessionID, offlineFile, { description: "Offline Skill package" });
        const candidate = {
          provider: "artifact", artifact_id: upload.artifact?.id || "", name: offlineFile.name,
          path: "SKILL.md", size: offlineFile.size, verified: true,
          description: `${formatBytes(offlineFile.size)} · ${upload.artifact?.id || "artifact"}`
        };
        if (!candidate.artifact_id) throw new Error("ZIP 上传后未返回 artifact ID。");
        setCandidates([candidate]);
        await previewCandidate(candidate);
        return;
      }
      if (searchMode === "repository") {
        const candidate = {
          provider: "github", repository: value, ref: sourceRef.trim(),
          path: sourcePath.trim() || "SKILL.md", verified: true
        };
        setCandidates([candidate]);
        await previewCandidate(candidate);
        return;
      }
      const response = await api.discoverSkillsMarketplace({
        sessionId: sessionID,
        query: value,
        limit: 10
      });
      setCandidates(response.items || []);
      if (!(response.items || []).length) setMessage({ tone: "neutral", text: "没有找到可安装的 Skill。" });
    } catch (error) {
      setCandidates([]);
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setDiscovering(false);
    }
  }

  async function previewCandidate(candidate) {
    const provider = candidate.provider || "github";
    const source = provider === "artifact"
      ? { provider, artifact_id: candidate.artifact_id }
      : provider === "catalog"
        ? { provider, catalog_entry_id: candidate.id }
        : { provider, repository: candidate.repository, ref: candidate.ref || "", path: candidate.path || "SKILL.md" };
    const key = provider === "artifact" ? `artifact:${source.artifact_id}` : provider === "catalog" ? `catalog:${source.catalog_entry_id}` : `${source.repository}:${source.ref}:${source.path}`;
    setPreviewingKey(key);
    setMessage(null);
    setConfirmingUpgrade(false);
    try {
      const previewRequest = {
        session_id: sessionID,
        identifier: candidate.suggested_identifier || candidate.skill_identifier || "",
        source
      };
      const response = provider === "catalog"
        ? await api.previewInternalSkillsMarketplace(previewRequest)
        : await api.previewSkillsMarketplace(previewRequest);
      setPreview(response);
      setIdentifier(response.identifier || candidate.suggested_identifier || "");
    } catch (error) {
      setPreview(null);
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setPreviewingKey("");
    }
  }

  async function installPreview() {
    if (!preview?.policy?.allowed || ["blocked", "unchanged"].includes(preview.install_state)) return;
    const previousVersion = Number(preview.existing?.version || 0);
    setInstalling(true);
    setMessage(null);
    try {
      const installRequest = {
        session_id: sessionID,
        identifier: identifier.trim() || preview.identifier,
        source: preview.source,
        policy_id: preview.policy.policy_id || "",
        policy_version: preview.policy.policy_version || 0,
        policy_revision: preview.policy.policy_revision || "",
        upgrade_existing: preview.install_state === "upgrade"
      };
      const response = preview.source?.provider === "catalog"
        ? await api.installInternalSkillsMarketplace(installRequest)
        : await api.installSkillsMarketplace(installRequest);
      const retainedVersion = response.upgraded && previousVersion > 0 ? `；本地 v${previousVersion} 继续保留，可在已安装详情中重新启用` : "";
      setMessage({ tone: "ok", text: `${response.skill.title || response.skill.identifier} v${response.version.version} 已${response.upgraded ? "升级" : "安装"}${retainedVersion}。` });
      await onSkillsChanged();
      setCandidates((current) => current.map((candidate) => (
        preview.source?.provider === "catalog" && candidate.id === preview.source.catalog_entry_id
          ? { ...candidate, install_state: "unchanged", existing: { ...(candidate.existing || {}), skill_id: response.skill.id, version: response.version.version, source_ref: preview.source.catalog_entry_id, source_revision: response.version.source_revision } }
          : candidate
      )));
      setPreview((current) => current ? { ...current, install_state: "unchanged", existing: { ...(current.existing || {}), skill_id: response.skill.id, version: response.version.version, source_ref: current.source?.catalog_entry_id || response.version.source_ref, source_revision: response.version.source_revision } } : current);
      setConfirmingUpgrade(false);
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setInstalling(false);
    }
  }

  const security = preview?.security || {};
  const policy = preview?.policy || {};
  const changes = preview?.changes || {};
  const assetFiles = preview?.assets?.files || [];
  const binaryAssetCount = assetFiles.filter((file) => file.binary).length;
  const sbom = preview?.assets?.sbom || security.sbom || {};
  const canInstall = Boolean(policy.allowed) && !["blocked", "unchanged"].includes(preview?.install_state);
  const previewState = marketplaceInstallStateMeta(preview?.install_state);
  const upgradeVersions = marketplaceUpgradeVersions(preview);

  return (
    <div className="skills-view-stack">
      {message ? <div className={`skills-notice ${message.tone}`}>{message.text}</div> : null}
      <form className={`marketplace-search ${searchMode === "repository" ? "exact" : searchMode === "offline" ? "offline" : searchMode === "internal" ? "internal" : ""}`} onSubmit={discover}>
        <div className="skills-segmented" aria-label="发现模式">
          <button className={searchMode === "internal" ? "active" : ""} type="button" onClick={() => setSearchMode("internal")}>内部市场</button>
          <button className={searchMode === "repository" ? "active" : ""} type="button" onClick={() => setSearchMode("repository")}>精确仓库</button>
          <button className={searchMode === "query" ? "active" : ""} type="button" onClick={() => setSearchMode("query")}>关键词</button>
          <button className={searchMode === "offline" ? "active" : ""} type="button" onClick={() => setSearchMode("offline")}>离线 ZIP</button>
        </div>
        {searchMode !== "offline" ? <label>
          {searchMode === "repository" ? "GitHub 仓库" : searchMode === "internal" ? "内部市场搜索" : "搜索关键词"}
          <input value={searchValue} onChange={(event) => setSearchValue(event.target.value)} placeholder={searchMode === "repository" ? "owner/repository" : searchMode === "internal" ? "名称、分类或标签，可留空" : "例如 code review"} />
        </label> : null}
        {searchMode === "offline" ? <label>Skill 文件包<input type="file" accept=".zip,application/zip" onChange={(event) => setOfflineFile(event.target.files?.[0] || null)} /></label> : null}
        {searchMode === "repository" ? <label>Ref<input value={sourceRef} onChange={(event) => setSourceRef(event.target.value)} placeholder="main 或 commit SHA" /></label> : null}
        {searchMode === "repository" ? <label>SKILL.md 路径<input value={sourcePath} onChange={(event) => setSourcePath(event.target.value)} placeholder="skills/example/SKILL.md" /></label> : null}
        <button type="submit" disabled={discovering || (searchMode === "offline" ? !offlineFile : !["internal"].includes(searchMode) && !searchValue.trim())}>{discovering ? "检查中" : searchMode === "repository" ? "预览来源" : searchMode === "offline" ? "安全预览" : "查找"}</button>
      </form>

      <div className={`marketplace-layout ${preview ? "has-preview" : ""}`}>
        <div className="marketplace-results">
          <div className="skills-section-heading"><div><span>发现结果</span><strong>{candidates.length} 个候选</strong></div></div>
          {candidates.map((candidate) => {
            const offline = candidate.provider === "artifact";
            const internal = candidate.provider === "catalog";
            const candidateState = marketplaceInstallStateMeta(candidate.install_state);
            const key = offline ? `artifact:${candidate.artifact_id}` : internal ? `catalog:${candidate.id}` : `${candidate.repository}:${candidate.ref || ""}:${candidate.path || "SKILL.md"}`;
            return (
              <article className="marketplace-candidate" key={key}>
                <div className="marketplace-candidate-head">
                  <div><strong>{internal ? candidate.skill_title : offline ? candidate.name : candidate.repository}</strong><code>{internal ? `${candidate.skill_identifier} · v${candidate.skill_version}` : candidate.path || "SKILL.md"}</code></div>
                  <StateTag tone={internal ? candidateState.tone : candidate.verified ? "ok" : "warn"}>{internal ? candidateState.label : offline ? "Session Artifact" : candidate.verified ? "已验证路径" : "待 Preview"}</StateTag>
                </div>
                <p>{internal ? candidate.summary || candidate.skill_description || "内部发布的不可变 Skill package。" : candidate.description || "仓库候选，需要 Preview 后才能确认包内容和安全状态。"}</p>
                <div className="marketplace-candidate-meta"><span>{internal ? candidate.category || "未分类" : offline ? "Offline ZIP" : candidate.stars ? `${candidate.stars} stars` : "GitHub"}</span><span>{internal ? (candidate.tags || []).join(" · ") || candidate.workspace_id : offline ? candidate.artifact_id : candidate.ref || "default ref"}</span>{internal && candidate.existing?.version ? <span>本地 v{candidate.existing.version}</span> : null}</div>
                <button type="button" disabled={Boolean(previewingKey)} onClick={() => previewCandidate(candidate)}>{previewingKey === key ? "检查中" : internal ? candidateState.actionLabel : "安全预览"}</button>
              </article>
            );
          })}
          {!candidates.length ? <div className="skills-empty">{searchMode === "offline" ? "选择一个标准 Skill ZIP 文件包。" : searchMode === "internal" ? "查询 Organization 内已发布的 Skill。" : "使用精确仓库或关键词查找 Marketplace Skill。"}</div> : null}
        </div>

        {preview ? (
          <aside className="marketplace-preview">
            <header className="marketplace-preview-header">
              <div><span>安装前检查</span><strong>{preview.title || preview.identifier}</strong><small>{preview.source?.provider === "artifact" ? `${preview.source.artifact_id} · SKILL.md` : preview.source?.provider === "catalog" ? `${preview.source.catalog_entry_id} · 内部市场` : `${preview.source?.repository} · ${preview.source?.path}`}</small></div>
              <StateTag tone={previewState.tone}>{previewState.label}</StateTag>
            </header>
            <div className="marketplace-preview-body">
              <div className="preview-summary-grid">
                <div><span>License</span><strong>{preview.license || "未声明"}</strong></div>
                <div><span>Revision</span><strong title={preview.revision}>{shortHash(preview.revision)}</strong></div>
                <div><span>文件</span><strong>{assetFiles.length}</strong></div>
                <div><span>二进制</span><strong>{binaryAssetCount}</strong></div>
                <div><span>扫描</span><strong>{security.scanned_files || 0}</strong></div>
                <div><span>SBOM</span><strong>{sbom.format || "未生成"}</strong></div>
                {preview.existing?.version ? <div><span>本地版本</span><strong>v{preview.existing.version}</strong></div> : null}
                {upgradeVersions ? <div><span>升级到</span><strong>v{upgradeVersions.target}</strong></div> : null}
              </div>

              <section className="preview-section">
                <div className="skills-section-heading"><div><span>Policy</span><strong>{policy.policy_source || "server"}</strong></div><StateTag tone={policy.allowed ? "ok" : "danger"}>{policy.allowed ? "允许" : "阻止"}</StateTag></div>
                <div className="policy-check-list">
                  {(policy.checks || []).map((check) => (
                    <div className="policy-check-row" key={check.name}>
                      <span className={`check-dot ${check.passed ? "passed" : "failed"}`} aria-hidden="true" />
                      <div><strong>{policyCheckLabels[check.name] || check.name}</strong><span>{check.message}</span></div>
                      <small>{check.enforced ? "强制" : "报告"}</small>
                    </div>
                  ))}
                </div>
                {(policy.violations || []).map((violation) => <div className="policy-violation" key={violation}>{violation}</div>)}
              </section>

              <section className="preview-section">
                <div className="skills-section-heading"><div><span>Package Security</span><strong>Attestation 与静态扫描</strong></div><StateTag tone={toneForState(security.highest_severity || security.attestation?.status)}>{security.highest_severity || security.attestation?.status || "unknown"}</StateTag></div>
                <dl className="security-facts">
                  <dt>Attestation</dt><dd>{security.attestation?.status || "unknown"} · {security.attestation?.message || "-"}</dd>
                  <dt>Digest</dt><dd title={security.digest_sha256}>{shortHash(security.digest_sha256, 20)}</dd>
                  <dt>Findings</dt><dd>{security.findings?.length || 0}{security.findings_limited ? "+" : ""}</dd>
                </dl>
                {(security.findings || []).map((finding) => (
                  <div className={`security-finding ${finding.severity}`} key={`${finding.rule_id}:${finding.path}:${finding.line}`}>
                    <StateTag tone={toneForState(finding.severity)}>{finding.severity}</StateTag>
                    <div><strong>{finding.rule_id}</strong><span>{finding.message}</span><code>{finding.path}:{finding.line}</code></div>
                  </div>
                ))}
              </section>

              <section className="preview-section">
                <div className="skills-section-heading"><div><span>Asset Closure</span><strong>{assetFiles.length} 个受控引用</strong></div><StateTag tone={binaryAssetCount ? "warn" : "neutral"}>{binaryAssetCount} binary</StateTag></div>
                <dl className="security-facts">
                  <dt>SBOM format</dt><dd>{sbom.format || "-"}</dd>
                  <dt>Components</dt><dd>{sbom.components?.length || 0}</dd>
                  <dt>Package digest</dt><dd title={sbom.package_digest_sha256}>{shortHash(sbom.package_digest_sha256, 20)}</dd>
                </dl>
                <div className="preview-asset-list">
                  {assetFiles.map((file) => (
                    <div className="preview-asset-row" key={file.path}>
                      <div className="preview-asset-main">
                        <strong>{file.path}</strong>
                        <span>{file.binary ? file.content_type || "binary" : "text"} · {file.size || 0} bytes</span>
                        {file.binary && file.scan_provider ? <span>{file.scan_provider}{file.scan_version ? ` · ${file.scan_version}` : ""}</span> : null}
                      </div>
                      <StateTag tone={file.binary ? toneForState(file.scan_status) : "neutral"}>{file.binary ? file.scan_status || "unknown" : file.executable ? "script" : "text"}</StateTag>
                      <code title={file.checksum_sha256}>{shortHash(file.checksum_sha256, 16)}</code>
                    </div>
                  ))}
                  {!assetFiles.length ? <div className="skills-empty compact">包内没有引用资产。</div> : null}
                </div>
              </section>

              <section className="preview-section">
                <div className="skills-section-heading"><div><span>Package Diff</span><strong>{changes.content_changed ? "主文件已变化" : "文件变更"}</strong></div></div>
                <div className="diff-groups">
                  {[['added_files', '新增'], ['changed_files', '修改'], ['removed_files', '删除']].map(([key, label]) => (
                    <div key={key}><span>{label}</span><strong>{changes[key]?.length || 0}</strong><small>{(changes[key] || []).join(" · ") || "无"}</small></div>
                  ))}
                </div>
                {(preview.assets?.warnings || []).map((warning) => <div className="skills-notice warn" key={warning}>{warning}</div>)}
              </section>
            </div>
            <footer className="marketplace-install-bar">
              <label>安装标识<input value={identifier} readOnly={preview.install_state === "upgrade"} onChange={(event) => setIdentifier(event.target.value)} /></label>
              {confirmingUpgrade && upgradeVersions ? (
                <div className="marketplace-upgrade-confirm">
                  <div><strong>确认升级到 v{upgradeVersions.target}</strong><span>当前 v{upgradeVersions.current} 将继续保留。</span></div>
                  <div className="skills-confirm-actions">
                    <button className="secondary" type="button" disabled={installing} onClick={() => setConfirmingUpgrade(false)}>取消</button>
                    <button type="button" disabled={installing || !identifier.trim()} onClick={installPreview}>{installing ? "写入中" : "确认升级"}</button>
                  </div>
                </div>
              ) : (
                <button type="button" disabled={!canInstall || installing || !identifier.trim()} onClick={() => preview.install_state === "upgrade" ? setConfirmingUpgrade(true) : installPreview()}>{installing ? "写入中" : preview.install_state === "upgrade" ? "升级版本" : preview.install_state === "unchanged" ? "已是最新" : "安装 Skill"}</button>
              )}
            </footer>
          </aside>
        ) : null}
      </div>
    </div>
  );
}

function MarketplaceManagementView({ skills, workspaceID }) {
  const [entries, setEntries] = useState([]);
  const [selectedID, setSelectedID] = useState("");
  const [creating, setCreating] = useState(false);
  const [versions, setVersions] = useState([]);
  const [statusFilter, setStatusFilter] = useState("");
  const [form, setForm] = useState({ ...emptyMarketplaceEntryForm });
  const [transitionNote, setTransitionNote] = useState("");
  const [confirmWithdraw, setConfirmWithdraw] = useState(false);
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState(null);

  const selected = entries.find((entry) => entry.id === selectedID) || null;
  const activeSkills = skills.filter((skill) => skill.status === "active");
  const visibleEntries = statusFilter ? entries.filter((entry) => entry.status === statusFilter) : entries;
  const counts = marketplaceEntryStages.reduce((result, stage) => {
    result[stage.status] = entries.filter((entry) => entry.status === stage.status).length;
    return result;
  }, {});

  async function loadEntries(preferredID = "") {
    const response = await api.marketplaceEntries({ workspaceId: workspaceID, includeWithdrawn: true });
    const nextEntries = response.entries || [];
    setEntries(nextEntries);
    const nextID = preferredID || selectedID;
    if (nextID && nextEntries.some((entry) => entry.id === nextID)) {
      setSelectedID(nextID);
    } else if (!creating) {
      setSelectedID(nextEntries[0]?.id || "");
    }
    return nextEntries;
  }

  useEffect(() => {
    let active = true;
    setMessage(null);
    api.marketplaceEntries({ workspaceId: workspaceID, includeWithdrawn: true }).then((response) => {
      if (!active) return;
      const nextEntries = response.entries || [];
      setEntries(nextEntries);
      setSelectedID((current) => current && nextEntries.some((entry) => entry.id === current) ? current : nextEntries[0]?.id || "");
    }).catch((error) => {
      if (active) setMessage({ tone: "danger", text: error.message });
    });
    return () => { active = false; };
  }, [workspaceID]);

  useEffect(() => {
    if (!selected || creating) return;
    setForm({
      skillID: selected.skill_id,
      skillVersion: String(selected.skill_version),
      summary: selected.summary || "",
      category: selected.category || "",
      tags: Array.isArray(selected.tags) ? selected.tags.join(", ") : ""
    });
    setTransitionNote(selected.status === "published" ? selected.withdrawal_reason || "" : selected.review_note || "");
    setConfirmWithdraw(false);
  }, [selectedID, selected?.updated_at, creating]);

  async function loadVersions(skillID, preferredVersion = "") {
    if (!skillID) {
      setVersions([]);
      return;
    }
    const response = await api.skillVersions(skillID);
    const nextVersions = response.versions || [];
    setVersions(nextVersions);
    setForm((current) => current.skillID === skillID ? {
      ...current,
      skillVersion: preferredVersion || current.skillVersion || String(nextVersions[0]?.version || "")
    } : current);
  }

  function selectEntry(entry) {
    setCreating(false);
    setSelectedID(entry.id);
    setVersions([]);
    setMessage(null);
  }

  async function startCreate() {
    const skillID = activeSkills[0]?.id || "";
    setCreating(true);
    setSelectedID("");
    setTransitionNote("");
    setConfirmWithdraw(false);
    setMessage(null);
    setForm({ ...emptyMarketplaceEntryForm, skillID });
    try {
      await loadVersions(skillID);
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    }
  }

  async function changeSkill(skillID) {
    setForm((current) => ({ ...current, skillID, skillVersion: "" }));
    try {
      await loadVersions(skillID);
    } catch (error) {
      setVersions([]);
      setMessage({ tone: "danger", text: error.message });
    }
  }

  async function saveEntry(event) {
    event.preventDefault();
    setBusy("save");
    setMessage(null);
    try {
      const body = {
        workspace_id: workspaceID,
        summary: form.summary,
        category: form.category,
        tags: splitValues(form.tags)
      };
      const entry = creating
        ? await api.createMarketplaceEntry({ ...body, skill_id: form.skillID, skill_version: Number(form.skillVersion) })
        : await api.updateMarketplaceEntry(selected.id, body);
      setCreating(false);
      setSelectedID(entry.id);
      await loadEntries(entry.id);
      setMessage({ tone: "ok", text: creating ? "市场草稿已创建。" : "市场草稿已保存。" });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusy("");
    }
  }

  async function runTransition(action) {
    if (!selected) return;
    setBusy(action.action);
    setMessage(null);
    try {
      const entry = await api.transitionMarketplaceEntry(selected.id, action.action, {
        workspace_id: workspaceID,
        note: transitionNote.trim()
      });
      await loadEntries(entry.id);
      setConfirmWithdraw(false);
      setTransitionNote("");
      setMessage({ tone: "ok", text: `状态已更新为${marketplaceEntryStatusLabel(entry.status)}。` });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusy("");
    }
  }

  const nextAction = marketplaceEntryNextAction(selected?.status);
  const editable = creating || selected?.status === "draft";

  return (
    <div className="skills-view-stack">
      {message ? <div className={`skills-notice ${message.tone}`}>{message.text}</div> : null}
      <div className="market-admin-summary">
        {marketplaceEntryStages.map((stage) => (
          <button className={statusFilter === stage.status ? "active" : ""} type="button" key={stage.status} onClick={() => setStatusFilter((current) => current === stage.status ? "" : stage.status)}>
            <span>{stage.label}</span><strong>{counts[stage.status] || 0}</strong>
          </button>
        ))}
      </div>
      <div className="market-admin-layout">
        <aside className="market-admin-list">
          <div className="skills-section-heading">
            <div><span>市场条目</span><strong>{visibleEntries.length} 条记录</strong></div>
            <button type="button" onClick={startCreate}>新建草稿</button>
          </div>
          {visibleEntries.map((entry) => (
            <button className={`market-admin-list-item ${selectedID === entry.id ? "selected" : ""}`} type="button" key={entry.id} onClick={() => selectEntry(entry)}>
              <span><strong>{entry.skill_title}</strong><StateTag tone={marketplaceEntryStatusTone(entry.status)}>{marketplaceEntryStatusLabel(entry.status)}</StateTag></span>
              <code>{entry.skill_identifier} · v{entry.skill_version}</code>
              <small>{entry.category || "未分类"} · {formatDate(entry.updated_at)}</small>
            </button>
          ))}
          {!visibleEntries.length ? <div className="skills-empty compact">当前筛选下没有市场条目。</div> : null}
        </aside>

        {creating || selected ? (
          <form className="market-admin-editor" onSubmit={saveEntry}>
            <header>
              <div><span>{creating ? "创建市场条目" : selected.id}</span><strong>{creating ? "新建草稿" : selected.skill_title}</strong></div>
              {!creating ? <StateTag tone={marketplaceEntryStatusTone(selected.status)}>{marketplaceEntryStatusLabel(selected.status)}</StateTag> : <StateTag>草稿</StateTag>}
            </header>

            <div className="market-lifecycle" aria-label="市场条目生命周期">
              {marketplaceEntryStages.map((stage) => {
                const state = marketplaceEntryStageState(creating ? "draft" : selected.status, stage.status);
                return <div className={state} key={stage.status}><span aria-hidden="true" /><strong>{stage.label}</strong></div>;
              })}
            </div>

            <div className="market-admin-form-grid">
              <label>Skill
                <select value={form.skillID} disabled={!creating} required onChange={(event) => changeSkill(event.target.value)}>
                  <option value="">请选择已安装 Skill</option>
                  {activeSkills.map((skill) => <option value={skill.id} key={skill.id}>{skill.title} · {skill.identifier}</option>)}
                </select>
              </label>
              <label>精确版本
                <select value={form.skillVersion} disabled={!creating} required onChange={(event) => setForm((current) => ({ ...current, skillVersion: event.target.value }))}>
                  <option value="">请选择版本</option>
                  {versions.map((version) => <option value={version.version} key={version.id}>v{version.version} · {shortHash(version.checksum_sha256)}</option>)}
                  {!creating && selected && !versions.some((version) => version.version === selected.skill_version) ? <option value={selected.skill_version}>v{selected.skill_version} · {shortHash(selected.version_checksum_sha256)}</option> : null}
                </select>
              </label>
              <label>分类<input value={form.category} disabled={!editable} maxLength={80} onChange={(event) => setForm((current) => ({ ...current, category: event.target.value }))} placeholder="例如 Engineering" /></label>
              <label>标签<input value={form.tags} disabled={!editable} onChange={(event) => setForm((current) => ({ ...current, tags: event.target.value }))} placeholder="review, quality" /></label>
              <label className="market-admin-summary-field">市场摘要<textarea value={form.summary} disabled={!editable} maxLength={2000} rows={5} onChange={(event) => setForm((current) => ({ ...current, summary: event.target.value }))} placeholder="用于市场列表和审核的说明" /></label>
            </div>

            {!creating && selected ? (
              <dl className="market-admin-facts">
                <dt>Package</dt><dd>{selected.package_format || "-"}</dd>
                <dt>Version digest</dt><dd title={selected.version_checksum_sha256}>{shortHash(selected.version_checksum_sha256, 24)}</dd>
                <dt>创建人</dt><dd>{selected.created_by}</dd>
                <dt>最后更新</dt><dd>{formatDate(selected.updated_at)}</dd>
              </dl>
            ) : null}

            {!creating && ["pending_review", "published"].includes(selected?.status) ? (
              <label className="market-transition-note">{selected.status === "pending_review" ? "审核意见" : "下架原因"}
                <textarea value={transitionNote} maxLength={2000} rows={3} onChange={(event) => setTransitionNote(event.target.value)} placeholder={selected.status === "pending_review" ? "可选，记录本次审核结论" : "记录下架原因"} />
              </label>
            ) : null}

            <footer className="market-admin-actions">
              {creating ? <button className="secondary" type="button" onClick={() => { setCreating(false); setSelectedID(entries[0]?.id || ""); }}>取消</button> : null}
              {editable ? <button type="submit" disabled={busy === "save" || !form.skillID || !form.skillVersion}>{busy === "save" ? "保存中" : creating ? "创建草稿" : "保存草稿"}</button> : null}
              {!creating && nextAction && nextAction.action !== "withdraw" ? <button type="button" disabled={Boolean(busy)} onClick={() => runTransition(nextAction)}>{busy === nextAction.action ? "处理中" : nextAction.label}</button> : null}
              {!creating && nextAction?.action === "withdraw" && !confirmWithdraw ? <button className="secondary danger" type="button" disabled={Boolean(busy)} onClick={() => setConfirmWithdraw(true)}>下架</button> : null}
              {!creating && nextAction?.action === "withdraw" && confirmWithdraw ? <span className="skills-confirm-actions"><button className="secondary" type="button" onClick={() => setConfirmWithdraw(false)}>取消</button><button type="button" disabled={Boolean(busy)} onClick={() => runTransition(nextAction)}>{busy === "withdraw" ? "下架中" : "确认下架"}</button></span> : null}
              {!creating && selected?.status === "withdrawn" ? <span className="market-admin-terminal">该版本已下架，只保留历史审计与回放。</span> : null}
            </footer>
          </form>
        ) : (
          <div className="skills-empty">选择一个市场条目，或创建新的草稿。</div>
        )}
      </div>
    </div>
  );
}

function PolicyView({ workspaceID }) {
  const [policies, setPolicies] = useState([]);
  const [selectedPolicyID, setSelectedPolicyID] = useState("");
  const [selectedPolicy, setSelectedPolicy] = useState(null);
  const [form, setForm] = useState({ ...emptyPolicyForm });
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [confirmArchive, setConfirmArchive] = useState(false);
  const [message, setMessage] = useState(null);

  async function loadPolicies() {
    setLoading(true);
    try {
      const requests = [api.marketplacePolicies({ workspaceId: workspaceID, includeArchived: true })];
      requests.push(api.marketplacePolicies({ organizationId: form.organizationID || "org_default", includeArchived: true }));
      const responses = await Promise.all(requests);
      const deduplicated = new Map();
      responses.flatMap((response) => response.policies || []).forEach((policy) => deduplicated.set(policy.id, policy));
      setPolicies([...deduplicated.values()].sort((left, right) => new Date(right.created_at) - new Date(left.created_at)));
    } catch (error) {
      setPolicies([]);
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    loadPolicies();
  }, [workspaceID]);

  async function selectPolicy(policy) {
    setSelectedPolicyID(policy.id);
    setMessage(null);
    setConfirmArchive(false);
    try {
      const detail = await api.marketplacePolicy(policy.id);
      setSelectedPolicy(detail);
      setForm(policyFormFromDetail(detail));
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    }
  }

  function startNewPolicy() {
    setSelectedPolicyID("");
    setSelectedPolicy(null);
    setConfirmArchive(false);
    setForm({ ...emptyPolicyForm });
    setMessage(null);
  }

  function updateForm(key, value) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  async function savePolicy(event) {
    event.preventDefault();
    setSaving(true);
    setMessage(null);
    try {
      const config = policyConfigFromForm(form);
      if (selectedPolicy?.policy?.status === "active") {
        const version = await api.publishMarketplacePolicyVersion(selectedPolicy.policy.id, config);
        const detail = await api.marketplacePolicy(selectedPolicy.policy.id);
        setSelectedPolicy(detail);
        setForm(policyFormFromDetail(detail));
        setMessage({ tone: "ok", text: `已发布不可变策略版本 v${version.version}。` });
      } else {
        const body = {
          scope_type: form.scopeType,
          workspace_id: form.scopeType === "workspace" ? workspaceID : "",
          organization_id: form.scopeType === "organization" ? form.organizationID.trim() : "",
          config
        };
        const created = await api.createMarketplacePolicy(body);
        setSelectedPolicyID(created.policy.id);
        setSelectedPolicy(created);
        setForm(policyFormFromDetail(created));
        setMessage({ tone: "ok", text: `已创建 ${form.scopeType} 策略 v1。` });
      }
      await loadPolicies();
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setSaving(false);
    }
  }

  async function archivePolicy() {
    if (!selectedPolicy?.policy?.id) return;
    setSaving(true);
    setMessage(null);
    try {
      await api.archiveMarketplacePolicy(selectedPolicy.policy.id);
      setMessage({ tone: "ok", text: "策略已归档，有效策略将自动回退到下一优先级。" });
      setSelectedPolicy(null);
      setSelectedPolicyID("");
      setConfirmArchive(false);
      setForm({ ...emptyPolicyForm });
      await loadPolicies();
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setSaving(false);
    }
  }

  const editingActive = selectedPolicy?.policy?.status === "active";
  return (
    <div className="skills-view-stack">
      {message ? <div className={`skills-notice ${message.tone}`}>{message.text}</div> : null}
      <div className="policy-layout">
        <aside className="policy-list-pane">
          <div className="skills-section-heading"><div><span>策略版本</span><strong>{policies.length} 条记录</strong></div><button type="button" onClick={startNewPolicy}>新建策略</button></div>
          {loading ? <div className="skills-empty">正在读取策略...</div> : null}
          {policies.map((policy) => (
            <button className={`policy-list-item ${selectedPolicyID === policy.id ? "selected" : ""}`} type="button" key={policy.id} onClick={() => selectPolicy(policy)}>
              <span><strong>{policy.scope_type}</strong><StateTag tone={toneForState(policy.status)}>{policy.status}</StateTag></span>
              <code>{policy.workspace_id || policy.organization_id}</code>
              <small>v{policy.current_version} · {formatDate(policy.created_at)}</small>
            </button>
          ))}
          {!loading && !policies.length ? <div className="skills-empty">当前使用 Server 默认策略。</div> : null}
        </aside>

        <form className="policy-editor" onSubmit={savePolicy}>
          <header>
            <div><span>{editingActive ? "发布新版本" : "创建策略"}</span><strong>{editingActive ? selectedPolicy.policy.id : "Marketplace Policy"}</strong></div>
            {selectedPolicy?.version ? <StateTag tone="neutral">v{selectedPolicy.version.version} · {shortHash(selectedPolicy.version.checksum_sha256)}</StateTag> : null}
          </header>
          <div className="policy-form-grid two">
            <label>作用域<select value={form.scopeType} disabled={editingActive} onChange={(event) => updateForm("scopeType", event.target.value)}><option value="workspace">Workspace</option><option value="organization">Organization</option></select></label>
            {form.scopeType === "workspace" ? <label>Workspace ID<input value={workspaceID} disabled /></label> : <label>Organization ID<input value={form.organizationID} disabled={editingActive} onChange={(event) => updateForm("organizationID", event.target.value)} /></label>}
          </div>
          <div className="policy-form-grid two">
            <label>允许的 Owners<textarea value={form.allowedOwners} onChange={(event) => updateForm("allowedOwners", event.target.value)} placeholder="每行一个 GitHub owner" /></label>
            <label>允许的 Repositories<textarea value={form.allowedRepositories} onChange={(event) => updateForm("allowedRepositories", event.target.value)} placeholder="owner/repository" /></label>
            <label>允许的 Licenses<textarea value={form.allowedLicenses} onChange={(event) => updateForm("allowedLicenses", event.target.value)} placeholder="MIT\nApache-2.0" /></label>
            <label>拒绝的 Licenses<textarea value={form.deniedLicenses} onChange={(event) => updateForm("deniedLicenses", event.target.value)} placeholder="GPL-3.0" /></label>
          </div>
          <div className="policy-toggle-grid">
            <label className="policy-toggle"><input type="checkbox" checked={form.requireCommitSHA} onChange={(event) => updateForm("requireCommitSHA", event.target.checked)} /><span><strong>固定 Commit SHA</strong><small>禁止可移动的 branch/tag ref</small></span></label>
            <label className="policy-toggle"><input type="checkbox" checked={form.requireLicense} onChange={(event) => updateForm("requireLicense", event.target.checked)} /><span><strong>必须声明许可证</strong><small>缺失 license 时阻止安装</small></span></label>
            <label className="policy-toggle"><input type="checkbox" checked={form.requireAttestation} onChange={(event) => updateForm("requireAttestation", event.target.checked)} /><span><strong>必须验证 Attestation</strong><small>仅接受受信 Ed25519 签名</small></span></label>
          </div>
          <div className="policy-form-grid two">
            <label>静态扫描阻断等级<select value={form.staticScanBlockSeverity} onChange={(event) => updateForm("staticScanBlockSeverity", event.target.value)}><option value="">仅报告</option><option value="medium">Medium 及以上</option><option value="high">High 及以上</option><option value="critical">Critical</option></select></label>
            <label>Trusted Attestation Keys<textarea className="code-input" value={form.trustedAttestationKeys} onChange={(event) => updateForm("trustedAttestationKeys", event.target.value)} spellCheck="false" /></label>
          </div>
          <footer className="policy-editor-actions">
            {editingActive && !confirmArchive ? <button className="secondary danger" type="button" onClick={() => setConfirmArchive(true)}>归档策略</button> : null}
            {confirmArchive ? <span className="skills-confirm-actions"><button className="secondary" type="button" onClick={() => setConfirmArchive(false)}>取消</button><button type="button" disabled={saving} onClick={archivePolicy}>确认归档</button></span> : null}
            <button type="submit" disabled={saving || (form.scopeType === "organization" && !form.organizationID.trim())}>{saving ? "保存中" : editingActive ? "发布新版本" : "创建策略"}</button>
          </footer>
        </form>
      </div>
    </div>
  );
}

function StorageView({ workspaceID }) {
  const resolvedWorkspaceID = workspaceID || "wksp_default";
  const [effective, setEffective] = useState(null);
  const [policies, setPolicies] = useState([]);
  const [preview, setPreview] = useState(null);
  const [runs, setRuns] = useState([]);
  const [tombstones, setTombstones] = useState([]);
  const [form, setForm] = useState({ enabled: false, retentionDays: 30, deleteLimit: 100 });
  const [busy, setBusy] = useState("");
  const [confirmRun, setConfirmRun] = useState(false);
  const [message, setMessage] = useState(null);

  async function loadStorage() {
    setBusy((current) => current || "load");
    try {
      const [effectivePolicy, policyResponse, runResponse, tombstoneResponse] = await Promise.all([
        api.skillAssetRetentionEffective(resolvedWorkspaceID),
        api.skillAssetRetentionPolicies({ workspaceId: resolvedWorkspaceID, includeArchived: true }),
        api.skillAssetGCRuns(resolvedWorkspaceID),
        api.skillAssetGCTombstones(resolvedWorkspaceID)
      ]);
      setEffective(effectivePolicy);
      setPolicies(policyResponse.policies || []);
      setRuns(runResponse.runs || []);
      setTombstones(tombstoneResponse.tombstones || []);
      setForm({
        enabled: Boolean(effectivePolicy?.config?.enabled),
        retentionDays: Number(effectivePolicy?.config?.retention_days || 30),
        deleteLimit: Number(effectivePolicy?.config?.delete_limit || 100)
      });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusy("");
    }
  }

  useEffect(() => {
    loadStorage();
  }, [resolvedWorkspaceID]);

  async function savePolicy(event) {
    event.preventDefault();
    setBusy("policy");
    setMessage(null);
    const config = {
      enabled: form.enabled,
      retention_days: Number(form.retentionDays),
      delete_limit: Number(form.deleteLimit)
    };
    try {
      if (effective?.source === "workspace" && effective?.policy?.status === "active") {
        const version = await api.publishSkillAssetRetentionPolicyVersion(effective.policy.id, config);
        setMessage({ tone: "ok", text: `已发布保留策略 v${version.version}。` });
      } else {
        const created = await api.createSkillAssetRetentionPolicy({
          scope_type: "workspace", workspace_id: resolvedWorkspaceID, config
        });
        setMessage({ tone: "ok", text: `已创建 Workspace 保留策略 v${created.version.version}。` });
      }
      setPreview(null);
      await loadStorage();
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusy("");
    }
  }

  async function archiveWorkspacePolicy() {
    if (effective?.source !== "workspace" || !effective?.policy?.id) return;
    setBusy("archive");
    setMessage(null);
    try {
      await api.archiveSkillAssetRetentionPolicy(effective.policy.id);
      setPreview(null);
      setMessage({ tone: "ok", text: "Workspace 策略已归档。" });
      await loadStorage();
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusy("");
    }
  }

  async function previewGC() {
    setBusy("preview");
    setMessage(null);
    setConfirmRun(false);
    try {
      const response = await api.previewSkillAssetGC({ workspace_id: resolvedWorkspaceID });
      setPreview(response);
      setMessage({
        tone: response.candidate_count ? "warn" : "ok",
        text: `预览完成：${response.candidate_count} 个候选，${formatBytes(response.candidate_bytes)}。`
      });
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusy("");
    }
  }

  async function runGC() {
    setBusy("run");
    setMessage(null);
    try {
      const result = await api.runSkillAssetGC({ workspace_id: resolvedWorkspaceID, confirm: "DELETE" });
      setConfirmRun(false);
      setPreview(null);
      setMessage({
        tone: result.run.failed_count ? "warn" : "ok",
        text: `运行 ${result.run.id}：删除 ${result.run.deleted_count}，跳过 ${result.run.skipped_count}，失败 ${result.run.failed_count}。`
      });
      await loadStorage();
    } catch (error) {
      setMessage({ tone: "danger", text: error.message });
    } finally {
      setBusy("");
    }
  }

  const activeWorkspacePolicy = policies.find((policy) => policy.scope_type === "workspace" && policy.status === "active");
  return (
    <div className="skills-view-stack storage-view">
      {message ? <div className={`skills-notice ${message.tone}`}>{message.text}</div> : null}
      <section className="storage-summary-band">
        <div><span>Policy source</span><strong>{effective?.source || "-"}</strong><small>{effective?.policy?.id || "Server fallback"}</small></div>
        <div><span>Retention</span><strong>{effective?.config?.retention_days || "-"} 天</strong><small>{effective?.config?.enabled ? "Enabled" : "Disabled"}</small></div>
        <div><span>Delete limit</span><strong>{effective?.config?.delete_limit || "-"}</strong><small>Objects / run</small></div>
        <div><span>Reclaimed</span><strong>{formatBytes(runs.reduce((sum, run) => sum + Number(run.bytes_deleted || 0), 0))}</strong><small>{tombstones.length} tombstones</small></div>
      </section>

      <section className="storage-policy-band">
        <header className="skills-section-heading">
          <div><span>Retention policy</span><strong>{effective?.version?.version > 0 ? `v${effective.version.version} · ${shortHash(effective.revision)}` : "Server fallback"}</strong></div>
          <StateTag tone={effective?.config?.enabled ? "ok" : "warn"}>{effective?.config?.enabled ? "enabled" : "disabled"}</StateTag>
        </header>
        <form className="storage-policy-form" onSubmit={savePolicy}>
          <label className="policy-toggle"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm((value) => ({ ...value, enabled: event.target.checked }))} /><span><strong>自动清理许可</strong><small>允许手动和后台 GC 执行删除</small></span></label>
          <label>保留天数<input type="number" min="1" max="3650" value={form.retentionDays} onChange={(event) => setForm((value) => ({ ...value, retentionDays: event.target.value }))} /></label>
          <label>单次上限<input type="number" min="1" max="1000" value={form.deleteLimit} onChange={(event) => setForm((value) => ({ ...value, deleteLimit: event.target.value }))} /></label>
          <button type="submit" disabled={Boolean(busy)}>{busy === "policy" ? "发布中" : activeWorkspacePolicy ? "发布新版本" : "创建策略"}</button>
          {effective?.source === "workspace" ? <button className="secondary danger" type="button" disabled={Boolean(busy)} onClick={archiveWorkspacePolicy}>归档</button> : null}
        </form>
      </section>

      <section className="storage-gc-band">
        <header className="skills-section-heading">
          <div><span>Dry-run inventory</span><strong>{preview ? `${preview.candidate_count} 个候选 · ${formatBytes(preview.candidate_bytes)}` : "尚未预览"}</strong></div>
          <div className="storage-actions">
            <button className="secondary" type="button" disabled={Boolean(busy)} onClick={previewGC}>{busy === "preview" ? "扫描中" : "预览"}</button>
            {!confirmRun ? <button type="button" disabled={Boolean(busy) || !preview?.candidate_count || !effective?.config?.enabled} onClick={() => setConfirmRun(true)}>执行清理</button> : null}
            {confirmRun ? <span className="skills-confirm-actions"><button className="secondary" type="button" onClick={() => setConfirmRun(false)}>取消</button><button type="button" disabled={Boolean(busy)} onClick={runGC}>{busy === "run" ? "执行中" : "确认删除"}</button></span> : null}
          </div>
        </header>
        {preview?.candidates?.length ? <div className="storage-object-list">
          {preview.candidates.map((candidate) => <div className="storage-object-row" key={candidate.object_ref_id}>
            <div><strong>{candidate.asset_path || candidate.object_key}</strong><code>{candidate.object_ref_id} · {shortHash(candidate.checksum_sha256)}</code></div>
            <span>{candidate.reason === "orphaned_skill_asset" ? "Orphan" : "Archived"}</span>
            <span>{candidate.scan_provider || "builtin"} {candidate.scan_version || ""}</span>
            <strong>{formatBytes(candidate.size_bytes)}</strong>
          </div>)}
        </div> : <div className="skills-empty compact">{preview ? "没有达到保留期限且可安全删除的对象。" : "运行预览后显示候选对象。"}</div>}
      </section>

      <section className="storage-history-grid">
        <div>
          <header className="skills-section-heading"><div><span>Recent runs</span><strong>{runs.length} 次</strong></div></header>
          <div className="storage-history-list">
            {runs.map((run) => <div className="storage-history-row" key={run.id}>
              <div><strong>{run.id}</strong><small>{formatDate(run.started_at)} · {run.policy_source}</small></div>
              <StateTag tone={toneForState(run.status)}>{run.status}</StateTag>
              <span>{run.deleted_count} deleted</span><span>{formatBytes(run.bytes_deleted)}</span>
            </div>)}
            {!runs.length ? <div className="skills-empty compact">暂无 GC 运行记录。</div> : null}
          </div>
        </div>
        <div>
          <header className="skills-section-heading"><div><span>Tombstones</span><strong>{tombstones.length} 条</strong></div></header>
          <div className="storage-history-list">
            {tombstones.map((item) => <div className="storage-tombstone-row" key={item.id}>
              <div><strong>{item.asset_path || item.object_key}</strong><code>{item.object_ref_id} · {shortHash(item.checksum_sha256)}</code></div>
              <span>{formatBytes(item.size_bytes)}</span><small>{formatDate(item.deleted_at)}</small>
            </div>)}
            {!tombstones.length ? <div className="skills-empty compact">暂无 tombstone。</div> : null}
          </div>
        </div>
      </section>
    </div>
  );
}

export default function SkillsManagement({
  agent,
  onApplyAgentConfig,
  onSkillsChanged,
  runtimeConfig,
  session,
  sessionID = "",
  skills = [],
  workspaceID = ""
}) {
  const [activeView, setActiveView] = useState("installed");
  const agentBindings = skillBindingsFromConfig(agent?.config_version?.skills);
  const sessionBindings = sessionID ? skillBindingsFromConfig(runtimeConfig?.skills) : [];
  const agentConfigVersion = Number(agent?.current_config_version || agent?.config_version?.version || 0);
  const sessionConfigVersion = sessionID ? Number(runtimeConfig?.agent_config_version || session?.agent_config_version || 0) : 0;
  const activeCount = skills.filter((skill) => skill.status === "active").length;
  return (
    <div className="skills-console">
      <section className="skills-console-header">
        <div className="skills-console-title">
          <span>Workspace capability registry</span>
          <strong>Skills 管理</strong>
          <p>安装、验证并固定可复用能力版本。</p>
        </div>
        <div className="skills-console-stats">
          <div><strong>{activeCount}</strong><span>Active</span></div>
          <div><strong>{agentBindings.length}</strong><span>Enabled</span></div>
          <div><strong>{sessionID ? "Ready" : "No session"}</strong><span>Context</span></div>
        </div>
      </section>
      <nav className="skills-view-tabs" aria-label="Skills 管理视图">
        <button className={activeView === "installed" ? "active" : ""} type="button" onClick={() => setActiveView("installed")}>已安装</button>
        <button className={activeView === "marketplace" ? "active" : ""} type="button" onClick={() => setActiveView("marketplace")}>Marketplace</button>
        <button className={activeView === "market-admin" ? "active" : ""} type="button" onClick={() => setActiveView("market-admin")}>市场管理</button>
        <button className={activeView === "policy" ? "active" : ""} type="button" onClick={() => setActiveView("policy")}>Policy</button>
        <button className={activeView === "storage" ? "active" : ""} type="button" onClick={() => setActiveView("storage")}>Storage</button>
      </nav>
      <section className="skills-console-surface">
        {activeView === "installed" ? (
          <InstalledSkillsView
            agentBindings={agentBindings}
            agentConfigVersion={agentConfigVersion}
            onApplyAgentConfig={onApplyAgentConfig}
            onSkillsChanged={onSkillsChanged}
            session={session}
            sessionBindings={sessionBindings}
            sessionConfigVersion={sessionConfigVersion}
            sessionID={sessionID}
            skills={skills}
          />
        ) : null}
        {activeView === "marketplace" ? <MarketplaceView onSkillsChanged={onSkillsChanged} sessionID={sessionID} /> : null}
        {activeView === "market-admin" ? <MarketplaceManagementView skills={skills} workspaceID={workspaceID} /> : null}
        {activeView === "policy" ? <PolicyView workspaceID={workspaceID} /> : null}
        {activeView === "storage" ? <StorageView workspaceID={workspaceID} /> : null}
      </section>
    </div>
  );
}
