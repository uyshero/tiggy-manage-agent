package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tiggy-manage-agent/internal/execution"
	"tiggy-manage-agent/internal/llm"
	"tiggy-manage-agent/internal/managedagents"
	"tiggy-manage-agent/internal/objectstore"
	"tiggy-manage-agent/internal/observability"
	"tiggy-manage-agent/internal/runner"
	"tiggy-manage-agent/internal/tools"
	"tiggy-manage-agent/internal/workerselect"
)

const maxArtifactUploadBytes = 64 << 20

type appendEventsRequest struct {
	Events []managedagents.AppendEventInput `json:"events"`
}

type llmProviderRequest struct {
	ID           string  `json:"id"`
	ProviderType *string `json:"provider_type"`
	BaseURL      *string `json:"base_url"`
	APIKeyEnv    *string `json:"api_key_env"`
	Enabled      *bool   `json:"enabled"`
}

type llmModelRequest struct {
	ProviderID          string `json:"provider_id"`
	Model               string `json:"model"`
	ContextWindowTokens int    `json:"context_window_tokens"`
}

type agentConfigVersionRequest struct {
	LLMProvider *string          `json:"llm_provider"`
	LLMModel    *string          `json:"llm_model"`
	Model       *string          `json:"model"`
	System      *string          `json:"system"`
	Tools       *json.RawMessage `json:"tools"`
	Skills      *json.RawMessage `json:"skills"`
}

type sessionSummaryRequest struct {
	SummaryText    string `json:"summary_text"`
	SourceUntilSeq int64  `json:"source_until_seq"`
}

type sessionRuntimeSettingsRequest struct {
	InterventionMode  *string `json:"intervention_mode"`
	ToolRuntime       *string `json:"tool_runtime"`
	CloudSandboxRoot  *string `json:"cloud_sandbox_root"`
	CloudSandboxImage *string `json:"cloud_sandbox_image"`
	AllowNetwork      *bool   `json:"cloud_sandbox_allow_network"`
}

type sessionConfigUpgradeRequest struct {
	ToCurrent *bool  `json:"to_current"`
	UpdatedBy string `json:"updated_by,omitempty"`
}

type interventionDecisionRequest struct {
	Reason string `json:"reason,omitempty"`
}

type workerDiagnoseRequest struct {
	WorkspaceID     string          `json:"workspace_id,omitempty"`
	ProtocolVersion string          `json:"protocol_version,omitempty"`
	Namespace       string          `json:"namespace"`
	API             string          `json:"api"`
	Capabilities    []string        `json:"capabilities,omitempty"`
	Risk            string          `json:"risk,omitempty"`
	Runtime         string          `json:"runtime,omitempty"`
	Input           json.RawMessage `json:"input,omitempty"`
}

type workerDiagnoseResponse struct {
	Invocation  tools.WorkInvocation    `json:"invocation"`
	Matches     int                     `json:"matches"`
	Diagnostics []workerDiagnosisResult `json:"diagnostics"`
}

type workerWorkConflictResponse struct {
	Error string `json:"error"`
	workerDiagnoseResponse
}

type workerWorkDiagnoseResponse struct {
	Work    managedagents.WorkerWork `json:"work"`
	Worker  *workerSummary           `json:"worker,omitempty"`
	Reasons []string                 `json:"reasons,omitempty"`
	Actions []string                 `json:"actions,omitempty"`
}

type workerSummary struct {
	ID             string  `json:"id"`
	WorkspaceID    string  `json:"workspace_id"`
	Name           string  `json:"name"`
	WorkerType     string  `json:"worker_type"`
	Status         string  `json:"status"`
	LeaseExpiresAt *string `json:"lease_expires_at,omitempty"`
	LastSeenAt     *string `json:"last_seen_at,omitempty"`
}

type workerDiagnosisResult struct {
	WorkerID     string   `json:"worker_id"`
	WorkspaceID  string   `json:"workspace_id"`
	Name         string   `json:"name"`
	WorkerType   string   `json:"worker_type"`
	Status       string   `json:"status"`
	Match        bool     `json:"match"`
	Reasons      []string `json:"reasons,omitempty"`
	Runtimes     []string `json:"runtimes,omitempty"`
	APIs         []string `json:"apis,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	LeaseExpires *string  `json:"lease_expires_at,omitempty"`
	LastSeen     *string  `json:"last_seen_at,omitempty"`
	RegisteredBy string   `json:"registered_by,omitempty"`
}

func (s *Server) listLLMProviders(w http.ResponseWriter, r *http.Request) {
	providers, err := s.store.ListLLMProviders()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Server) createLLMProvider(w http.ResponseWriter, r *http.Request) {
	var request llmProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	provider, err := s.store.UpsertLLMProvider(managedagents.UpsertLLMProviderInput{
		ID:           request.ID,
		ProviderType: stringValue(request.ProviderType),
		BaseURL:      stringValue(request.BaseURL),
		APIKeyEnv:    stringValue(request.APIKeyEnv),
		Enabled:      enabled,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) getLLMProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.store.GetLLMProvider(r.PathValue("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) updateLLMProvider(w http.ResponseWriter, r *http.Request) {
	existing, err := s.store.GetLLMProvider(r.PathValue("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	var request llmProviderRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if request.ProviderType != nil {
		existing.ProviderType = *request.ProviderType
	}
	if request.BaseURL != nil {
		existing.BaseURL = *request.BaseURL
	}
	if request.APIKeyEnv != nil {
		existing.APIKeyEnv = *request.APIKeyEnv
	}
	if request.Enabled != nil {
		existing.Enabled = *request.Enabled
	}

	provider, err := s.store.UpsertLLMProvider(managedagents.UpsertLLMProviderInput{
		ID:           existing.ID,
		ProviderType: existing.ProviderType,
		BaseURL:      existing.BaseURL,
		APIKeyEnv:    existing.APIKeyEnv,
		Enabled:      existing.Enabled,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Server) enableLLMProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.store.SetLLMProviderEnabled(r.PathValue("provider_id"), true)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) disableLLMProvider(w http.ResponseWriter, r *http.Request) {
	provider, err := s.store.SetLLMProviderEnabled(r.PathValue("provider_id"), false)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, provider)
}

func (s *Server) listLLMModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.store.ListLLMModels(r.URL.Query().Get("provider_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) upsertLLMModel(w http.ResponseWriter, r *http.Request) {
	var request llmModelRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	model, err := s.store.UpsertLLMModel(managedagents.UpsertLLMModelInput{
		ProviderID:          request.ProviderID,
		Model:               request.Model,
		ContextWindowTokens: request.ContextWindowTokens,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model)
}

func (s *Server) getSessionLLMUsage(w http.ResponseWriter, r *http.Request) {
	report, err := s.store.GetSessionLLMUsage(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) getSessionSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := s.store.GetSessionSummary(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) getSessionTrace(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.ListEvents(r.PathValue("session_id"), 0)
	if err != nil {
		writeError(w, err)
		return
	}
	trace := observability.ProjectTurnTrace(r.PathValue("session_id"), r.URL.Query().Get("turn_id"), events)
	if trace.TurnID == "" || len(trace.Steps) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "trace not found"})
		return
	}
	switch strings.TrimSpace(strings.ToLower(r.URL.Query().Get("format"))) {
	case "", "json", "trace":
		writeJSON(w, http.StatusOK, trace)
	case "perfetto":
		writeJSON(w, http.StatusOK, observability.ExportPerfetto(trace))
	case "otel", "otlp":
		writeJSON(w, http.StatusOK, observability.ExportOTel(trace))
	default:
		writeError(w, fmt.Errorf("%w: unsupported trace format %q", managedagents.ErrInvalid, r.URL.Query().Get("format")))
	}
}

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	usage, err := s.store.ListLLMUsage(managedagents.ListLLMUsageInput{
		WorkspaceID: query.Get("workspace_id"),
		GroupBy:     managedagents.LLMUsageGroupByProviderModel,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: query.Get("workspace_id"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	snapshot := observability.MetricsSnapshot{
		Usage:   usage,
		Workers: workers,
	}
	if sessionID := strings.TrimSpace(query.Get("session_id")); sessionID != "" {
		events, err := s.store.ListEvents(sessionID, 0)
		if err != nil {
			writeError(w, err)
			return
		}
		trace := observability.ProjectTurnTrace(sessionID, query.Get("turn_id"), events)
		interventions, err := s.store.ListSessionInterventions(sessionID, "")
		if err != nil {
			writeError(w, err)
			return
		}
		snapshot.Trace = &trace
		snapshot.Events = events
		snapshot.Interventions = interventions
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(observability.PrometheusText(snapshot))); err != nil {
		s.logger.Warn("metrics response write failed", "error", err)
	}
}

func (s *Server) getInspector(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(inspectorHTML)); err != nil {
		s.logger.Warn("inspector response write failed", "error", err)
	}
}

func (s *Server) getObservabilityStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, observability.StatusFromEnv())
}

const inspectorHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TMA Inspector</title>
<style>
:root{color-scheme:light;--bg:#f3f4f6;--ink:#111827;--muted:#6b7280;--line:#d1d5db;--panel:#fff;--accent:#0f766e;--accent-soft:#ecfdf5;--warn:#b45309;--warn-soft:#fffbeb;--err:#b42318;--err-soft:#fef2f2}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;font-size:14px;letter-spacing:0}
header{display:flex;align-items:center;justify-content:space-between;gap:16px;padding:14px 18px;border-bottom:1px solid var(--line);background:#fff;position:sticky;top:0;z-index:10}
h1{margin:0;font-size:18px;font-weight:650}.layout{display:grid;grid-template-columns:320px minmax(0,1fr);min-height:calc(100vh - 57px)}
aside{border-right:1px solid var(--line);background:#fff;padding:16px;display:grid;gap:16px;align-content:start}main{padding:16px;display:grid;gap:16px;align-content:start}
label{display:block;font-size:12px;font-weight:650;color:var(--muted);margin:0 0 6px}input,select,button{font:inherit}
input,select{width:100%;height:34px;border:1px solid var(--line);border-radius:6px;padding:6px 8px;background:#fff;color:var(--ink)}
button{height:34px;border:1px solid #0b5f59;border-radius:6px;background:var(--accent);color:#fff;padding:0 12px;font-weight:650;cursor:pointer}button.secondary{background:#fff;color:var(--ink);border-color:var(--line)}
.actions{display:flex;gap:8px;flex-wrap:wrap}.panel{background:var(--panel);border:1px solid var(--line);border-radius:8px;overflow:hidden}
.panel h2{margin:0;padding:10px 12px;border-bottom:1px solid var(--line);font-size:14px;background:#f9fafb}.content{padding:12px}
.stack{display:grid;gap:12px}.meta{color:var(--muted);font-size:12px;display:flex;gap:8px;flex-wrap:wrap}.pill{display:inline-flex;align-items:center;height:22px;padding:0 8px;border-radius:999px;border:1px solid var(--line);background:#fff;font-size:12px}
.pill.ok{background:var(--accent-soft);border-color:#a7f3d0;color:#065f46}.pill.warn{background:var(--warn-soft);border-color:#fcd34d;color:#92400e}.pill.err{background:var(--err-soft);border-color:#fecaca;color:#991b1b}
.overview{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:12px}.stat-card{border:1px solid var(--line);border-radius:8px;background:#fff;padding:10px 12px;min-height:82px}.stat-label{font-size:12px;color:var(--muted)}.stat-value{font-size:20px;font-weight:650;margin-top:6px}.stat-sub{font-size:12px;color:var(--muted);margin-top:6px}
.split{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}.triple{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}
.timeline,.list,.turn-list{display:grid;gap:8px}.step,.list-item,.turn-item{border:1px solid var(--line);border-radius:6px;background:#f9fafb;padding:10px}
.step{border-left:3px solid #94a3b8}.step.tool{border-left-color:var(--accent)}.step.approval{border-left-color:var(--warn)}.step.error{border-left-color:var(--err)}
.turn-item{cursor:pointer}.turn-item.active{border-color:var(--accent);background:#f0fdfa}.turn-item:hover{border-color:#9ca3af}
.summary{white-space:pre-wrap;line-height:1.45}.summary.compact{font-size:13px}.empty{color:var(--muted)}pre{margin:0;white-space:pre-wrap;word-break:break-word;font:12px ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
.code{background:#111827;color:#e5e7eb;border-radius:8px;padding:12px;max-height:420px;overflow:auto}
.table-wrap{overflow:auto}.span-table{width:100%;border-collapse:collapse;font-size:13px}.span-table th,.span-table td{padding:8px 10px;border-bottom:1px solid var(--line);text-align:left;vertical-align:top}.span-table th{font-size:12px;color:var(--muted);font-weight:650;background:#f9fafb;position:sticky;top:0}
.toolbar{display:grid;gap:12px}.field{display:grid;gap:6px}.toggle{display:flex;align-items:center;gap:8px;font-size:13px;color:var(--ink)}.toggle input{width:auto;height:auto}
a.link{color:#0f766e;text-decoration:none}a.link:hover{text-decoration:underline}.subtle{font-size:12px;color:var(--muted)}.artifact-list{display:grid;gap:6px;margin-top:8px}.artifact-line{border:1px solid var(--line);border-radius:6px;background:#fff;padding:8px}
@media(max-width:1200px){.overview{grid-template-columns:repeat(3,minmax(0,1fr))}.triple{grid-template-columns:1fr}.split{grid-template-columns:1fr}}
@media(max-width:860px){.layout{grid-template-columns:1fr}aside{border-right:0;border-bottom:1px solid var(--line)}.overview{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
</head>
<body>
<header><h1>TMA Inspector</h1><div class="meta" id="status">idle</div></header>
<div class="layout">
<aside>
<section class="panel">
<h2>Query</h2>
<div class="content toolbar">
<div class="field"><label for="session">Session</label><input id="session" autocomplete="off" placeholder="sesn_000001"></div>
<div class="field"><label for="turn">Turn</label><select id="turn"><option value="">latest</option></select></div>
<div class="field"><label for="format">Export Format</label><select id="format"><option value="json">Trace JSON</option><option value="perfetto">Perfetto JSON</option><option value="otel">OTel JSON</option></select></div>
<label class="toggle"><input type="checkbox" id="autoRefresh">Auto refresh every 5s</label>
<div class="actions"><button id="load">Load</button><button class="secondary" id="export">Preview Export</button><button class="secondary" id="download">Download</button></div>
</div>
</section>
<section class="panel">
<h2>Turns</h2>
<div class="content turn-list" id="turns"><span class="empty">No turns loaded.</span></div>
</section>
</aside>
<main>
<section class="overview" id="overviewCards">
<div class="stat-card"><div class="stat-label">Turn</div><div class="stat-value">-</div><div class="stat-sub">Load a session.</div></div>
</section>
<section class="split">
<section class="panel"><h2>Session</h2><div class="content stack" id="sessionMeta"><span class="empty">No session loaded.</span></div></section>
<section class="panel"><h2>Trace Summary</h2><div class="content stack"><div id="traceMeta"><span class="empty">No trace loaded.</span></div><div class="summary compact" id="summary">No trace summary loaded.</div></div></section>
</section>
<section class="split">
<section class="panel"><h2>Context Summary</h2><div class="content summary" id="sessionSummary">No session summary loaded.</div></section>
<section class="panel"><h2>Usage</h2><div class="content stack"><div id="usageSummary" class="meta"><span class="empty">No usage loaded.</span></div><pre id="usage">No usage loaded.</pre></div></section>
</section>
<section class="panel"><h2>Spans</h2><div class="content table-wrap" id="spans"><span class="empty">No spans loaded.</span></div></section>
<section class="panel"><h2>Timeline</h2><div class="content timeline" id="timeline"><span class="empty">No timeline loaded.</span></div></section>
<section class="triple">
<section class="panel"><h2>Pending Approvals</h2><div class="content list" id="interventions"><span class="empty">No interventions loaded.</span></div></section>
<section class="panel"><h2>Artifacts</h2><div class="content list" id="artifacts"><span class="empty">No artifacts loaded.</span></div></section>
<section class="panel"><h2>Recent Events</h2><div class="content list" id="events"><span class="empty">No events loaded.</span></div></section>
</section>
<section class="triple">
<section class="panel"><h2>Metrics</h2><div class="content"><pre id="metrics" class="code">No metrics loaded.</pre></div></section>
<section class="panel"><h2>Exporters</h2><div class="content list" id="exporters"><span class="empty">No exporter status loaded.</span></div></section>
<section class="panel"><h2>Raw Export</h2><div class="content"><pre id="raw" class="code">No raw export loaded.</pre></div></section>
</section>
</main>
</div>
<script>
const $=id=>document.getElementById(id);
let refreshHandle=0;
function status(text){$("status").textContent=text}
function activeSession(){return $("session").value.trim()}
function activeTurn(){return $("turn").value.trim()}
function tracePath(format){const session=activeSession();const query=[];if(activeTurn())query.push("turn_id="+encodeURIComponent(activeTurn()));if(format)query.push("format="+encodeURIComponent(format));return "/v1/sessions/"+encodeURIComponent(session)+"/trace"+(query.length?"?"+query.join("&"):"")}
function metricsPath(){const session=activeSession();const query=["session_id="+encodeURIComponent(session)];if(activeTurn())query.push("turn_id="+encodeURIComponent(activeTurn()));return "/metrics?"+query.join("&")}
async function getJSON(path){const response=await fetch(path);if(!response.ok)throw new Error(await response.text());return response.json()}
async function getText(path){const response=await fetch(path);if(!response.ok)throw new Error(await response.text());return response.text()}
async function postJSON(path,body){const response=await fetch(path,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body||{})});if(!response.ok)throw new Error(await response.text());return response.json()}
function escapeHTML(text){return String(text||"").replace(/[&<>]/g,function(c){return({"&":"&amp;","<":"&lt;",">":"&gt;"}[c])})}
function escapeAttr(text){return escapeHTML(text).replace(/"/g,"&quot;")}
function pretty(value){return JSON.stringify(value,null,2)}
function formatTime(value){if(!value)return"-";const date=new Date(value);if(Number.isNaN(date.getTime()))return String(value);return date.toLocaleString()}
function formatDuration(ms){const value=Number(ms||0);if(value<1000)return value+" ms";const seconds=(value/1000).toFixed(value<10000?2:1);return seconds+" s"}
function pillClass(statusValue){if(statusValue==="completed"||statusValue==="ok"||statusValue==="success"||statusValue==="approved")return"pill ok";if(statusValue==="waiting_approval"||statusValue==="pending"||statusValue==="blocked")return"pill warn";if(statusValue==="failed"||statusValue==="error"||statusValue==="rejected")return"pill err";return"pill"}
function stepClass(step){if(step.outcome==="error"||step.type==="runtime.failed")return"step error";if(step.type&&step.type.indexOf("intervention")!==-1)return"step approval";if(step.type&&step.type.indexOf("tool")!==-1)return"step tool";return"step"}
function sessionArtifactCLI(downloadPath){downloadPath=String(downloadPath||"").trim();if(!downloadPath)return"";downloadPath=downloadPath.split("?")[0].split("#")[0];const prefix="/v1/sessions/";if(!downloadPath.startsWith(prefix))return"";const parts=downloadPath.slice(prefix.length).split("/");if(parts.length!==4||parts[1]!=="artifacts"||parts[3]!=="download")return"";if(!parts[0]||!parts[2])return"";return "bin/tma session artifact download --session "+parts[0]+" --artifact "+parts[2]}
function sessionArtifactCommand(sessionId,artifactId){sessionId=String(sessionId||"").trim();artifactId=String(artifactId||"").trim();if(!sessionId||!artifactId)return"";return "bin/tma session artifact download --session "+sessionId+" --artifact "+artifactId}
function artifactActions(href,command){let html='<div class="actions" style="margin-top:6px">';if(href){html+='<a class="link" href="'+escapeAttr(href)+'" target="_blank" rel="noreferrer">Download</a>'}if(command){html+='<button class="secondary" type="button" data-copy="'+escapeAttr(command)+'">Copy CLI</button>'}return html+'</div>'}
function renderArtifactLine(artifact){const label=[artifact.artifact_id||artifact.id||"(unknown)",artifact.name||"",artifact.artifact_type?("["+artifact.artifact_type+"]"):""].filter(Boolean).join(" ");const href=artifact.download_path||"";const command=sessionArtifactCLI(href);return'<div class="artifact-line"><div>'+escapeHTML(label)+'</div>'+(href?'<div class="subtle">download: '+escapeHTML(href)+'</div>':"")+(command?'<div class="subtle">cli: '+escapeHTML(command)+'</div>':"")+artifactActions(href,command)+'</div>'}
async function copyText(text){text=String(text||"");if(!text)return;if(navigator.clipboard&&navigator.clipboard.writeText){await navigator.clipboard.writeText(text)}else{const input=document.createElement("textarea");input.value=text;document.body.appendChild(input);input.select();document.execCommand("copy");input.remove()}status("copied command")}
function renderOverview(trace){const stats=trace.stats||{};const cards=[{label:"Turn",value:trace.turn_id||"-",sub:trace.status||"running"},{label:"Duration",value:formatDuration(stats.duration_ms),sub:(stats.start_time?formatTime(stats.start_time):"-")+" -> "+(stats.end_time?formatTime(stats.end_time):"-")},{label:"Steps",value:String(stats.step_count||0),sub:"timeline events"},{label:"Spans",value:String(stats.span_count||0),sub:"projected trace spans"},{label:"Tools",value:String(stats.tool_calls||0),sub:String(stats.approval_waits||0)+" approval waits"},{label:"Errors",value:String(stats.errors||0),sub:String(stats.artifact_count||0)+" artifacts"}];$("overviewCards").innerHTML=cards.map(function(card){return'<div class="stat-card"><div class="stat-label">'+escapeHTML(card.label)+'</div><div class="stat-value">'+escapeHTML(card.value)+'</div><div class="stat-sub">'+escapeHTML(card.sub)+'</div></div>'}).join("")}
function renderTraceMeta(trace){const stats=trace.stats||{};$("traceMeta").innerHTML='<div class="meta"><span>'+escapeHTML(trace.session_id)+'</span><span class="'+pillClass(trace.status||"running")+'">'+escapeHTML(trace.status||"running")+'</span><span>'+escapeHTML(trace.trace_id||"")+'</span></div><div class="meta"><span>'+String(stats.step_count||0)+' steps</span><span>'+String(stats.span_count||0)+' spans</span><span>'+String(stats.llm_requests||0)+' llm</span><span>'+String(stats.tool_calls||0)+' tools</span><span>'+String(stats.pending_approvals||0)+' pending approvals</span></div>'}
function renderTimeline(trace){const steps=trace.steps||[];$("timeline").innerHTML=steps.length?steps.map(function(step){const subject=step.identifier?(escapeHTML(step.identifier)+(step.api_name?"."+escapeHTML(step.api_name):"")):"";const artifacts=(step.artifacts&&step.artifacts.length)?'<div class="artifact-list">'+step.artifacts.map(renderArtifactLine).join("")+'</div>':"";const artifactError=step.artifact_error?'<div class="subtle" style="margin-top:6px">artifact error: '+escapeHTML(step.artifact_error)+'</div>':"";return'<div class="'+stepClass(step)+'"><div class="meta"><span>seq '+step.seq+'</span><span>'+escapeHTML(step.type)+'</span><span>'+subject+'</span><span>'+escapeHTML(step.outcome||"")+'</span><span>'+formatTime(step.created_at)+'</span></div><div style="margin-top:6px">'+escapeHTML(step.message||step.summary||"")+'</div>'+artifacts+artifactError+'</div>'}).join(""):'<span class="empty">No timeline steps.</span>'}
function renderSpans(trace){const spans=trace.spans||[];if(!spans.length){$("spans").innerHTML='<span class="empty">No spans loaded.</span>';return}$("spans").innerHTML='<table class="span-table"><thead><tr><th>Name</th><th>Kind</th><th>Status</th><th>Duration</th><th>Range</th><th>Attributes</th></tr></thead><tbody>'+spans.map(function(span){const attrs=Object.entries(span.attributes||{}).slice(0,6).map(function(entry){return escapeHTML(entry[0])+': '+escapeHTML(entry[1])}).join('<br>');return'<tr><td><strong>'+escapeHTML(span.name)+'</strong><div class="subtle">'+escapeHTML(span.span_id)+'</div></td><td>'+escapeHTML(span.kind)+'</td><td><span class="'+pillClass(span.status||"unknown")+'">'+escapeHTML(span.status||"unknown")+'</span></td><td>'+escapeHTML(formatDuration(span.duration_ms))+'</td><td>'+escapeHTML(String(span.start_seq||0))+' -> '+escapeHTML(String(span.end_seq||0))+'</td><td>'+(attrs||'<span class="subtle">No attributes</span>')+'</td></tr>'}).join("")+'</tbody></table>'}
function setTurnOptions(turns,selectedTurn){const previous=selectedTurn||activeTurn();const options=['<option value="">latest</option>'];(turns||[]).forEach(function(turn){options.push('<option value="'+escapeHTML(turn.turn_id)+'">'+escapeHTML(turn.turn_id)+' ('+escapeHTML(turn.status||"running")+')</option>')});$("turn").innerHTML=options.join("");$("turn").value=previous||""}
function renderTurns(turns,active){$("turns").innerHTML=(turns&&turns.length)?turns.map(function(turn){return'<div class="turn-item'+(turn.turn_id===active?' active':'')+'" data-turn="'+escapeHTML(turn.turn_id)+'"><div class="meta"><span>'+escapeHTML(turn.turn_id)+'</span><span class="'+pillClass(turn.status||"running")+'">'+escapeHTML(turn.status||"running")+'</span></div><div class="subtle" style="margin-top:6px">'+escapeHTML(formatDuration(turn.duration_ms))+' | '+turn.step_count+' steps | '+turn.span_count+' spans</div><div class="summary compact" style="margin-top:8px">'+escapeHTML(turn.summary||"No summary.")+'</div></div>'}).join(""):'<span class="empty">No turns loaded.</span>';Array.from(document.querySelectorAll(".turn-item")).forEach(function(node){node.addEventListener("click",function(){const turn=node.getAttribute("data-turn")||"";$("turn").value=turn;load().catch(function(error){status(error.message)})})})}
function renderTrace(trace){renderOverview(trace);renderTraceMeta(trace);$("summary").textContent=trace.summary||"No trace summary loaded.";setTurnOptions(trace.turns||[],trace.turn_id||"");renderTurns(trace.turns||[],trace.turn_id||"");renderTimeline(trace);renderSpans(trace);$("raw").textContent=pretty(trace)}
function renderSession(session){const runtime=session.runtime_settings?pretty(session.runtime_settings):"{}";$("sessionMeta").innerHTML='<div class="meta"><span>'+escapeHTML(session.id)+'</span><span class="'+pillClass(session.status||"unknown")+'">'+escapeHTML(session.status||"unknown")+'</span><span>'+escapeHTML(session.agent_id)+'</span><span>'+escapeHTML(session.environment_id)+'</span></div><div><strong>'+escapeHTML(session.title||"Untitled session")+'</strong></div><div class="meta"><span>created '+escapeHTML(formatTime(session.created_at))+'</span><span>'+escapeHTML(session.created_by||"")+'</span></div><pre>'+escapeHTML(runtime)+'</pre>'}
function renderSessionSummary(summary){$("sessionSummary").textContent=(summary&&summary.summary_text)||"No session summary loaded."}
function renderUsage(usage){const summary=(usage&&usage.summary)||{};$("usageSummary").innerHTML='<span>'+String(summary.record_count||0)+' records</span><span>'+String(summary.total_tokens||0)+' tokens</span><span>'+formatDuration(summary.latency_ms||0)+'</span>';$("usage").textContent=pretty(usage||{})}
function renderArtifacts(sessionId,response){const artifacts=response.artifacts||[];$("artifacts").innerHTML=artifacts.length?artifacts.map(function(artifact){const href='/v1/sessions/'+encodeURIComponent(sessionId)+'/artifacts/'+encodeURIComponent(artifact.id)+'/download';const command=sessionArtifactCommand(sessionId,artifact.id);return'<div class="list-item"><div><strong>'+escapeHTML(artifact.name||artifact.id)+'</strong></div><div class="meta"><span>'+escapeHTML(artifact.artifact_type)+'</span><span>'+escapeHTML(artifact.object_ref_id||"")+'</span><span>'+escapeHTML(artifact.turn_id||"")+'</span></div><div class="subtle">cli: '+escapeHTML(command)+'</div>'+artifactActions(href,command)+'</div>'}).join(""):'<span class="empty">No artifacts.</span>'}
function renderEvents(response){const events=(response.events||[]).slice(-18).reverse();$("events").innerHTML=events.length?events.map(function(event){return'<div class="list-item"><div class="meta"><span>seq '+event.seq+'</span><span>'+escapeHTML(event.type)+'</span><span>'+escapeHTML(formatTime(event.created_at))+'</span></div><pre style="margin-top:8px">'+escapeHTML(pretty(event.payload||{}))+'</pre></div>'}).join(""):'<span class="empty">No events.</span>'}
function renderExporterStatus(response){const perfetto=(response&&response.perfetto)||{};const otlp=(response&&response.otlp)||{};function item(name,entry){const state=entry.enabled?"enabled":"disabled";const destination=entry.destination||"not configured";const token=entry.token_provided?'<span>token configured</span>':"";return'<div class="list-item"><div><strong>'+escapeHTML(name)+'</strong> <span class="'+pillClass(state==="enabled"?"ok":"unknown")+'">'+escapeHTML(state)+'</span></div><div class="meta" style="margin-top:6px"><span>'+escapeHTML(destination)+'</span>'+token+'</div></div>'}$("exporters").innerHTML=item("Perfetto",perfetto)+item("OTLP HTTP",otlp)}
function approvalButtons(sessionId,intervention){const approve="approveIntervention('"+encodeURIComponent(sessionId)+"','"+encodeURIComponent(intervention.turn_id)+"','"+encodeURIComponent(intervention.call_id)+"')";const reject="rejectIntervention('"+encodeURIComponent(sessionId)+"','"+encodeURIComponent(intervention.turn_id)+"','"+encodeURIComponent(intervention.call_id)+"')";return '<div class="actions"><button onclick="'+approve+'">Approve</button><button class="secondary" onclick="'+reject+'">Reject</button></div>'}
function renderInterventions(sessionId,response){const interventions=response.interventions||[];$("interventions").innerHTML=interventions.length?interventions.map(function(intervention){return'<div class="list-item"><div><strong>'+escapeHTML(intervention.tool_identifier)+'.'+escapeHTML(intervention.api_name)+'</strong></div><div class="meta"><span>'+escapeHTML(intervention.call_id)+'</span><span class="'+pillClass(intervention.status||"pending")+'">'+escapeHTML(intervention.status||"pending")+'</span><span>'+escapeHTML(intervention.reason||"")+'</span></div><pre style="margin-top:8px">'+escapeHTML(pretty(intervention.arguments||{}))+'</pre>'+approvalButtons(sessionId,intervention)+'</div>'}).join(""):'<span class="empty">No pending approvals.</span>'}
async function approveIntervention(sessionId,turnId,callId){status("approving");await postJSON("/v1/sessions/"+sessionId+"/interventions/"+turnId+"/"+callId+"/approve",{reason:"approved from inspector"});await load();status("approved")}
async function rejectIntervention(sessionId,turnId,callId){const reason=window.prompt("Reject reason","rejected from inspector");if(reason===null)return;status("rejecting");await postJSON("/v1/sessions/"+sessionId+"/interventions/"+turnId+"/"+callId+"/reject",{reason:reason});await load();status("rejected")}
async function load(){const session=activeSession();if(!session){status("session required");return}status("loading "+session);const trace=await getJSON(tracePath(""));renderTrace(trace);const requests=[getJSON("/v1/sessions/"+encodeURIComponent(session)),getJSON("/v1/sessions/"+encodeURIComponent(session)+"/usage").catch(function(error){return{error:String(error)}}),getJSON("/v1/sessions/"+encodeURIComponent(session)+"/summary").catch(function(){return{summary_text:""}}),getJSON("/v1/sessions/"+encodeURIComponent(session)+"/artifacts").catch(function(error){return{artifacts:[],error:String(error)}}),getJSON("/v1/sessions/"+encodeURIComponent(session)+"/events").catch(function(error){return{events:[],error:String(error)}}),getJSON("/v1/sessions/"+encodeURIComponent(session)+"/interventions?status=pending").catch(function(error){return{interventions:[],error:String(error)}}),getText(metricsPath()).catch(function(error){return String(error)}),getJSON("/v1/observability/status").catch(function(error){return{error:String(error)}})];const results=await Promise.all(requests);renderSession(results[0]);renderUsage(results[1]);renderSessionSummary(results[2]);renderArtifacts(session,results[3]);renderEvents(results[4]);renderInterventions(session,results[5]);$("metrics").textContent=results[6];renderExporterStatus(results[7]);status("loaded "+session+" / "+(trace.turn_id||"latest"))}
async function exportTrace(download){const session=activeSession();if(!session){status("session required");return}const format=$("format").value;const data=await getJSON(tracePath(format));const text=pretty(data);$("raw").textContent=text;if(download){const filename=[session,activeTurn()||"latest",format].join("-")+".json";const blob=new Blob([text],{type:"application/json"});const url=URL.createObjectURL(blob);const anchor=document.createElement("a");anchor.href=url;anchor.download=filename;anchor.click();window.setTimeout(function(){URL.revokeObjectURL(url)},1000)}status((download?"downloaded ":"previewed ")+format)}
function updateRefresh(){if(refreshHandle){window.clearInterval(refreshHandle);refreshHandle=0}if($("autoRefresh").checked){refreshHandle=window.setInterval(function(){if(activeSession())load().catch(function(error){status(error.message)})},5000)}}
$("load").addEventListener("click",function(){load().catch(function(error){status(error.message)})});
$("turn").addEventListener("change",function(){if(activeSession())load().catch(function(error){status(error.message)})});
$("export").addEventListener("click",function(){exportTrace(false).catch(function(error){status(error.message)})});
$("download").addEventListener("click",function(){exportTrace(true).catch(function(error){status(error.message)})});
$("autoRefresh").addEventListener("change",updateRefresh);
document.addEventListener("click",function(event){const target=event.target.closest("[data-copy]");if(!target)return;copyText(target.getAttribute("data-copy")||"").catch(function(error){status(error.message)})});
</script>
</body>
</html>`

func (s *Server) upsertSessionSummary(w http.ResponseWriter, r *http.Request) {
	var request sessionSummaryRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.store.UpsertSessionSummary(r.PathValue("session_id"), managedagents.UpsertSessionSummaryInput{
		SummaryText:    request.SummaryText,
		SourceUntilSeq: request.SourceUntilSeq,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listLLMUsage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, err := parseOptionalTime(query.Get("from"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid from: %v", managedagents.ErrInvalid, err))
		return
	}
	to, err := parseOptionalTime(query.Get("to"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid to: %v", managedagents.ErrInvalid, err))
		return
	}

	report, err := s.store.ListLLMUsage(managedagents.ListLLMUsageInput{
		WorkspaceID: query.Get("workspace_id"),
		ProviderID:  query.Get("provider_id"),
		Model:       query.Get("model"),
		Status:      query.Get("status"),
		GroupBy:     query.Get("group_by"),
		From:        from,
		To:          to,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) registerWorker(w http.ResponseWriter, r *http.Request) {
	var input managedagents.RegisterWorkerInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	worker, err := s.store.RegisterWorker(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, worker)
}

func (s *Server) getWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := s.store.GetWorker(r.PathValue("worker_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) listWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: r.URL.Query().Get("workspace_id"),
		Status:      r.URL.Query().Get("status"),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": workers})
}

func (s *Server) diagnoseWorkers(w http.ResponseWriter, r *http.Request) {
	var request workerDiagnoseRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(request.Input) == 0 {
		request.Input = json.RawMessage(`{}`)
	}
	invocation := tools.WorkInvocation{
		ProtocolVersion: request.ProtocolVersion,
		Namespace:       request.Namespace,
		API:             request.API,
		Capabilities:    request.Capabilities,
		Risk:            request.Risk,
		Runtime:         request.Runtime,
		Input:           request.Input,
	}
	if strings.TrimSpace(invocation.ProtocolVersion) == "" {
		invocation.ProtocolVersion = tools.WorkProtocolVersion
	}
	if strings.TrimSpace(invocation.Runtime) == "" {
		invocation.Runtime = tools.ToolRuntimeAuto
	}
	if err := tools.ValidateWorkInvocation(invocation); err != nil {
		writeError(w, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err))
		return
	}
	workspaceID := request.WorkspaceID
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: workspaceID,
		Status:      managedagents.WorkerStatusOnline,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildWorkerDiagnoseResponse(invocation, workers, time.Now().UTC()))
}

func (s *Server) heartbeatWorker(w http.ResponseWriter, r *http.Request) {
	var input managedagents.WorkerHeartbeatInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	worker, err := s.store.HeartbeatWorker(r.PathValue("worker_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) archiveWorker(w http.ResponseWriter, r *http.Request) {
	worker, err := s.store.ArchiveWorker(r.PathValue("worker_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, worker)
}

func (s *Server) enqueueWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.EnqueueWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	invocation, err := validateWorkerWorkPayload(input)
	if err != nil {
		writeError(w, err)
		return
	}
	if input.WorkerID == "" && invocation != nil {
		workerID, err := workerselect.Selector{Store: s.store}.SelectWorkerID(workerselect.Request{
			WorkspaceID: input.WorkspaceID,
			Invocation:  *invocation,
		})
		if err != nil {
			if errors.Is(err, managedagents.ErrConflict) {
				response, diagnoseErr := s.workerWorkConflictResponse(input.WorkspaceID, *invocation, err)
				if diagnoseErr != nil {
					writeError(w, diagnoseErr)
					return
				}
				writeJSON(w, http.StatusConflict, response)
				return
			}
			writeError(w, err)
			return
		}
		input.WorkerID = workerID
	}
	work, err := s.store.EnqueueWorkerWork(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, work)
}

func (s *Server) workerWorkConflictResponse(workspaceID string, invocation tools.WorkInvocation, cause error) (workerWorkConflictResponse, error) {
	if workspaceID == "" {
		workspaceID = managedagents.DefaultWorkspaceID
	}
	workers, err := s.store.ListWorkers(managedagents.ListWorkersInput{
		WorkspaceID: workspaceID,
		Status:      managedagents.WorkerStatusOnline,
	})
	if err != nil {
		return workerWorkConflictResponse{}, err
	}
	return workerWorkConflictResponse{
		Error:                  cause.Error(),
		workerDiagnoseResponse: buildWorkerDiagnoseResponse(invocation, workers, time.Now().UTC()),
	}, nil
}

func buildWorkerDiagnoseResponse(invocation tools.WorkInvocation, workers []managedagents.Worker, now time.Time) workerDiagnoseResponse {
	diagnostics := workerselect.DiagnoseInvocation(workers, invocation, now)
	response := workerDiagnoseResponse{Invocation: invocation}
	for _, diagnosis := range diagnostics {
		result := workerDiagnosisResult{
			WorkerID:     diagnosis.Worker.ID,
			WorkspaceID:  diagnosis.Worker.WorkspaceID,
			Name:         diagnosis.Worker.Name,
			WorkerType:   diagnosis.Worker.WorkerType,
			Status:       diagnosis.Worker.Status,
			Match:        diagnosis.Match,
			Reasons:      diagnosis.Reasons,
			Runtimes:     diagnosis.Capabilities.Runtimes,
			APIs:         diagnosis.Capabilities.APIs,
			Capabilities: diagnosis.Capabilities.Capabilities,
			RegisteredBy: diagnosis.Worker.RegisteredBy,
		}
		if diagnosis.Worker.LeaseExpiresAt != nil {
			formatted := diagnosis.Worker.LeaseExpiresAt.UTC().Format(time.RFC3339)
			result.LeaseExpires = &formatted
		}
		if diagnosis.Worker.LastSeenAt != nil {
			formatted := diagnosis.Worker.LastSeenAt.UTC().Format(time.RFC3339)
			result.LastSeen = &formatted
		}
		if diagnosis.Match {
			response.Matches++
		}
		response.Diagnostics = append(response.Diagnostics, result)
	}
	return response
}

func (s *Server) getWorkerWork(w http.ResponseWriter, r *http.Request) {
	work, err := s.store.GetWorkerWork(r.PathValue("work_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) reapExpiredWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.ReapExpiredWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	expired, err := s.store.ReapExpiredWorkerWork(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":   len(expired),
		"expired": expired,
	})
}

func (s *Server) diagnoseWorkerWork(w http.ResponseWriter, r *http.Request) {
	work, err := s.store.GetWorkerWork(r.PathValue("work_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	response := diagnoseWorkerWorkState(s.store, work, time.Now().UTC())
	writeJSON(w, http.StatusOK, response)
}

func diagnoseWorkerWorkState(store managedagents.Store, work managedagents.WorkerWork, now time.Time) workerWorkDiagnoseResponse {
	response := workerWorkDiagnoseResponse{Work: work}
	if strings.TrimSpace(work.WorkerID) != "" {
		worker, err := store.GetWorker(work.WorkerID)
		if err != nil {
			response.Reasons = append(response.Reasons, "assigned worker not found")
		} else {
			response.Worker = summarizeWorker(worker)
			if worker.Status != managedagents.WorkerStatusOnline {
				response.Reasons = append(response.Reasons, "assigned worker status is "+worker.Status)
			}
			if worker.LeaseExpiresAt != nil && worker.LeaseExpiresAt.Before(now) {
				response.Reasons = append(response.Reasons, "assigned worker lease expired at "+worker.LeaseExpiresAt.UTC().Format(time.RFC3339))
			}
		}
	}
	switch work.Status {
	case managedagents.WorkerWorkStatusPending:
		if strings.TrimSpace(work.WorkerID) == "" {
			response.Reasons = append(response.Reasons, "work is pending without an assigned worker")
			response.Actions = append(response.Actions, "wait for a matching worker to poll, or enqueue with --worker for a specific worker")
		} else {
			response.Reasons = append(response.Reasons, "work is pending for assigned worker "+work.WorkerID)
			response.Actions = append(response.Actions, "ensure the worker is online and polling")
		}
	case managedagents.WorkerWorkStatusLeased:
		response.Reasons = append(response.Reasons, "work is leased but not acknowledged")
		response.Actions = append(response.Actions, "worker should ack or complete the work")
	case managedagents.WorkerWorkStatusRunning:
		response.Reasons = append(response.Reasons, "work is running")
		response.Actions = append(response.Actions, "worker should heartbeat while running and submit result when complete")
	case managedagents.WorkerWorkStatusCompleted:
		response.Reasons = append(response.Reasons, "work completed successfully")
	case managedagents.WorkerWorkStatusFailed:
		response.Reasons = append(response.Reasons, "work failed")
	case managedagents.WorkerWorkStatusCanceled:
		response.Reasons = append(response.Reasons, "work was canceled")
	default:
		response.Reasons = append(response.Reasons, "work has unknown status "+work.Status)
	}
	if work.Status == managedagents.WorkerWorkStatusLeased || work.Status == managedagents.WorkerWorkStatusRunning {
		if work.LeaseExpiresAt == nil {
			response.Reasons = append(response.Reasons, "work has no lease_expires_at")
			response.Actions = append(response.Actions, "worker should heartbeat, or mark failed if it cannot continue")
		} else if work.LeaseExpiresAt.Before(now) {
			response.Reasons = append(response.Reasons, "work lease expired at "+work.LeaseExpiresAt.UTC().Format(time.RFC3339))
			response.Actions = append(response.Actions, "run: bin/tma work reap-expired")
		} else {
			response.Reasons = append(response.Reasons, "work lease valid until "+work.LeaseExpiresAt.UTC().Format(time.RFC3339))
		}
	}
	return response
}

func summarizeWorker(worker managedagents.Worker) *workerSummary {
	summary := &workerSummary{
		ID:          worker.ID,
		WorkspaceID: worker.WorkspaceID,
		Name:        worker.Name,
		WorkerType:  worker.WorkerType,
		Status:      worker.Status,
	}
	if worker.LeaseExpiresAt != nil {
		formatted := worker.LeaseExpiresAt.UTC().Format(time.RFC3339)
		summary.LeaseExpiresAt = &formatted
	}
	if worker.LastSeenAt != nil {
		formatted := worker.LastSeenAt.UTC().Format(time.RFC3339)
		summary.LastSeenAt = &formatted
	}
	return summary
}

func validateWorkerWorkPayload(input managedagents.EnqueueWorkerWorkInput) (*tools.WorkInvocation, error) {
	workType := strings.TrimSpace(strings.ToLower(input.WorkType))
	if workType == "" {
		workType = managedagents.WorkerWorkTypeToolExecution
	}
	if workType != managedagents.WorkerWorkTypeToolExecution {
		return nil, nil
	}
	var invocation tools.WorkInvocation
	if err := json.Unmarshal(input.Payload, &invocation); err != nil {
		return nil, fmt.Errorf("%w: decode tool_execution work payload: %v", managedagents.ErrInvalid, err)
	}
	if err := tools.ValidateWorkInvocation(invocation); err != nil {
		return nil, fmt.Errorf("%w: %v", managedagents.ErrInvalid, err)
	}
	return &invocation, nil
}

func (s *Server) pollWorkerWork(w http.ResponseWriter, r *http.Request) {
	leaseSeconds, err := optionalPositiveInt(r.URL.Query().Get("lease_seconds"))
	if err != nil {
		writeError(w, fmt.Errorf("%w: invalid lease_seconds: %v", managedagents.ErrInvalid, err))
		return
	}
	work, err := s.store.PollWorkerWork(r.PathValue("worker_id"), managedagents.PollWorkerWorkInput{
		LeaseSeconds: leaseSeconds,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"work": work})
}

func (s *Server) ackWorkerWork(w http.ResponseWriter, r *http.Request) {
	work, err := s.store.AckWorkerWork(r.PathValue("worker_id"), r.PathValue("work_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) heartbeatWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.WorkerWorkHeartbeatInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	work, err := s.store.HeartbeatWorkerWork(r.PathValue("worker_id"), r.PathValue("work_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) completeWorkerWork(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CompleteWorkerWorkInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	work, err := s.store.CompleteWorkerWork(r.PathValue("worker_id"), r.PathValue("work_id"), input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, work)
}

func (s *Server) createObjectRef(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateObjectRefInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	object, err := s.store.CreateObjectRef(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, object)
}

func (s *Server) getObjectRef(w http.ResponseWriter, r *http.Request) {
	object, err := s.store.GetObjectRef(r.PathValue("object_ref_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, object)
}

func (s *Server) downloadObjectRef(w http.ResponseWriter, r *http.Request) {
	objectRef, err := s.store.GetObjectRef(r.PathValue("object_ref_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	if !s.canDownloadObjectRef(r, objectRef) {
		writeError(w, fmt.Errorf("%w: object download not allowed", managedagents.ErrForbidden))
		return
	}

	object, err := s.objectStore.GetObject(r.Context(), objectstore.GetObjectInput{
		Bucket:  objectRef.Bucket,
		Key:     objectRef.ObjectKey,
		Version: objectRef.ObjectVersion,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	defer object.Body.Close()

	contentType := object.ContentType
	if contentType == "" {
		contentType = objectRef.ContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	filename := objectRef.ObjectKey
	if filename == "" {
		filename = objectRef.ID
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.ETag != "" {
		w.Header().Set("ETag", object.ETag)
	}
	if object.ChecksumSHA256 != "" {
		w.Header().Set("Digest", "sha-256="+object.ChecksumSHA256)
	}

	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Warn("object download copy failed", "object_ref_id", objectRef.ID, "error", err)
	}
}

func (s *Server) deleteObjectRef(w http.ResponseWriter, r *http.Request) {
	objectRefID := r.PathValue("object_ref_id")
	count, err := s.store.CountSessionArtifactsByObjectRef(objectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	if count > 0 {
		writeError(w, fmt.Errorf("%w: object ref is still referenced by %d artifact(s)", managedagents.ErrConflict, count))
		return
	}
	if err := s.store.DeleteObjectRef(objectRefID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSessionArtifact(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSessionArtifact(r.PathValue("session_id"), r.PathValue("artifact_id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) canDownloadObjectRef(r *http.Request, objectRef managedagents.ObjectRef) bool {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if objectRef.Visibility == managedagents.ObjectVisibilityWorkspace {
		if sessionID == "" {
			return false
		}
		session, err := s.store.GetSession(sessionID)
		return err == nil && session.WorkspaceID == objectRef.WorkspaceID
	}
	if objectRef.Visibility == managedagents.ObjectVisibilitySession {
		if sessionID == "" {
			return false
		}
		artifacts, err := s.store.ListSessionArtifacts(sessionID)
		if err != nil {
			return false
		}
		for _, artifact := range artifacts {
			if artifact.ObjectRefID == objectRef.ID {
				return true
			}
		}
		return false
	}
	return false
}

func parseOptionalTime(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func optionalPositiveInt(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("must be non-negative")
	}
	return parsed, nil
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateAgentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if input.LLMProvider == "" {
		input.LLMProvider = s.defaultLLMProvider
	}
	if input.LLMModel == "" && input.Model == "" {
		input.LLMModel = s.defaultLLMModel
	}

	agent, err := s.store.CreateAgent(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

func (s *Server) getAgent(w http.ResponseWriter, r *http.Request) {
	agent, err := s.store.GetAgent(r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (s *Server) listAgentConfigVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.store.ListAgentConfigVersions(r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config_versions": versions})
}

func (s *Server) createAgentConfigVersion(w http.ResponseWriter, r *http.Request) {
	current, err := s.store.GetAgent(r.PathValue("agent_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	var request agentConfigVersionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	next := current.ConfigVersion
	if request.LLMProvider != nil {
		next.LLMProvider = *request.LLMProvider
	}
	if request.LLMModel != nil {
		next.LLMModel = *request.LLMModel
	}
	if request.Model != nil && request.LLMModel == nil {
		next.LLMModel = *request.Model
	}
	if request.System != nil {
		next.System = *request.System
	}
	if request.Tools != nil {
		next.Tools = cloneJSONRaw(*request.Tools)
	}
	if request.Skills != nil {
		next.Skills = cloneJSONRaw(*request.Skills)
	}

	agent, err := s.store.CreateAgentConfigVersion(managedagents.CreateAgentConfigVersionInput{
		AgentID:     current.ID,
		LLMProvider: next.LLMProvider,
		LLMModel:    next.LLMModel,
		System:      next.System,
		Tools:       next.Tools,
		Skills:      next.Skills,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, agent)
}

func cloneJSONRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	clone := make([]byte, len(value))
	copy(clone, value)
	return clone
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateEnvironmentInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	environment, err := s.store.CreateEnvironment(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, environment)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateSessionInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	session, err := s.store.CreateSession(input)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, session)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.GetSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) createSessionArtifact(w http.ResponseWriter, r *http.Request) {
	var input managedagents.CreateSessionArtifactInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	input.SessionID = r.PathValue("session_id")
	artifact, err := s.store.CreateSessionArtifact(input)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, artifact)
}

func (s *Server) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	artifacts, err := s.store.ListSessionArtifacts(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": artifacts})
}

func (s *Server) downloadSessionArtifact(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	artifactID := r.PathValue("artifact_id")

	artifact, err := s.store.GetSessionArtifact(sessionID, artifactID)
	if err != nil {
		writeError(w, err)
		return
	}

	objectRef, err := s.store.GetObjectRef(artifact.ObjectRefID)
	if err != nil {
		writeError(w, err)
		return
	}
	if objectRef.WorkspaceID != artifact.WorkspaceID {
		writeError(w, fmt.Errorf("%w: artifact workspace mismatch", managedagents.ErrInvalid))
		return
	}

	object, err := s.objectStore.GetObject(r.Context(), objectstore.GetObjectInput{
		Bucket:  objectRef.Bucket,
		Key:     objectRef.ObjectKey,
		Version: objectRef.ObjectVersion,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	defer object.Body.Close()

	contentType := object.ContentType
	if contentType == "" {
		contentType = objectRef.ContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	filename := artifact.Name
	if filename == "" {
		filename = objectRef.ObjectKey
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.SizeBytes, 10))
	w.Header().Set("Content-Disposition", contentDispositionAttachment(filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if object.ETag != "" {
		w.Header().Set("ETag", object.ETag)
	}
	if object.ChecksumSHA256 != "" {
		w.Header().Set("Digest", "sha-256="+object.ChecksumSHA256)
	}

	if _, err := io.Copy(w, object.Body); err != nil {
		s.logger.Warn("artifact download copy failed", "session_id", sessionID, "artifact_id", artifactID, "error", err)
	}
}

func (s *Server) uploadSessionArtifact(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("session_id")
	session, err := s.store.GetSession(sessionID)
	if err != nil {
		writeError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArtifactUploadBytes+1024)
	if err := r.ParseMultipartForm(maxArtifactUploadBytes); err != nil {
		writeError(w, fmt.Errorf("%w: parse multipart artifact upload: %v", managedagents.ErrInvalid, err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, fmt.Errorf("%w: artifact upload requires file field", managedagents.ErrInvalid))
		return
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		writeError(w, err)
		return
	}
	contentType := fallbackString(r.FormValue("content_type"), header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	checksum := sha256.Sum256(content)
	checksumHex := hex.EncodeToString(checksum[:])

	bucket, err := objectstore.ResolveBucket(r.FormValue("bucket"), s.defaultObjectStoreBucket())
	if err != nil {
		writeError(w, err)
		return
	}
	objectKey := r.FormValue("object_key")
	if objectKey == "" {
		objectKey = defaultUploadObjectKey(session, header.Filename)
	}
	if err := objectstore.ValidateObjectKey(objectKey); err != nil {
		writeError(w, err)
		return
	}

	metadata, err := metadataFromFormValue(r.FormValue("metadata"))
	if err != nil {
		writeError(w, err)
		return
	}
	putResult, err := s.objectStore.PutObject(r.Context(), objectstore.PutObjectInput{
		Bucket:         bucket,
		Key:            objectKey,
		Body:           bytes.NewReader(content),
		ContentType:    contentType,
		SizeBytes:      int64(len(content)),
		ChecksumSHA256: checksumHex,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	objectRef, err := s.store.CreateObjectRef(managedagents.CreateObjectRefInput{
		WorkspaceID:     session.WorkspaceID,
		StorageProvider: managedagents.ObjectStorageProviderS3,
		Bucket:          fallbackString(putResult.Bucket, bucket),
		ObjectKey:       fallbackString(putResult.Key, objectKey),
		ObjectVersion:   putResult.Version,
		ContentType:     contentType,
		SizeBytes:       int64(len(content)),
		ChecksumSHA256:  fallbackString(putResult.ChecksumSHA256, checksumHex),
		ETag:            putResult.ETag,
		Visibility:      fallbackString(r.FormValue("visibility"), managedagents.ObjectVisibilityWorkspace),
		Metadata:        metadata,
		CreatedBy:       fallbackString(r.FormValue("created_by"), "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		name = safeArtifactFileName(header.Filename)
	}
	artifact, err := s.store.CreateSessionArtifact(managedagents.CreateSessionArtifactInput{
		SessionID:     sessionID,
		EnvironmentID: r.FormValue("environment_id"),
		ObjectRefID:   objectRef.ID,
		TurnID:        r.FormValue("turn_id"),
		ToolCallID:    r.FormValue("tool_call_id"),
		Name:          name,
		Description:   r.FormValue("description"),
		ArtifactType:  fallbackString(r.FormValue("artifact_type"), managedagents.ArtifactTypeFile),
		Metadata:      metadata,
		CreatedBy:     fallbackString(r.FormValue("created_by"), "system"),
	})
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"object_ref": objectRef,
		"artifact":   artifact,
	})
}

func (s *Server) defaultObjectStoreBucket() string {
	type configuredClient interface {
		Config() objectstore.Config
	}
	if client, ok := s.objectStore.(configuredClient); ok {
		return client.Config().Bucket
	}
	return ""
}

func metadataFromFormValue(value string) (json.RawMessage, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil, fmt.Errorf("%w: invalid metadata JSON object: %v", managedagents.ErrInvalid, err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func defaultUploadObjectKey(session managedagents.Session, filename string) string {
	return fmt.Sprintf("%s/%s/uploads/%d-%s", session.WorkspaceID, session.ID, time.Now().UTC().UnixNano(), safeArtifactFileName(filename))
}

func safeArtifactFileName(filename string) string {
	filename = filepath.Base(strings.TrimSpace(filename))
	if filename == "." || filename == string(filepath.Separator) || filename == "" {
		return "artifact"
	}
	filename = strings.ReplaceAll(filename, "/", "_")
	filename = strings.ReplaceAll(filename, "\\", "_")
	return filename
}

func fallbackString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func contentDispositionAttachment(filename string) string {
	filename = safeArtifactFileName(filename)
	return fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(filename, `"`, "_"))
}

func (s *Server) updateSessionRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	var request sessionRuntimeSettingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	session, err := s.store.GetSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	settings := map[string]any{}
	if len(session.RuntimeSettings) > 0 && string(session.RuntimeSettings) != "null" {
		if err := json.Unmarshal(session.RuntimeSettings, &settings); err != nil {
			writeError(w, fmt.Errorf("%w: existing runtime_settings must be valid JSON", managedagents.ErrInvalid))
			return
		}
	}
	if request.InterventionMode != nil {
		mode, ok := tools.NormalizeInterventionMode(*request.InterventionMode)
		if !ok {
			writeError(w, fmt.Errorf("%w: unsupported intervention_mode %q", managedagents.ErrInvalid, *request.InterventionMode))
			return
		}
		settings["intervention_mode"] = mode
	}
	if request.ToolRuntime != nil {
		runtime, ok := tools.NormalizeToolRuntime(*request.ToolRuntime)
		if !ok {
			writeError(w, fmt.Errorf("%w: unsupported tool_runtime %q", managedagents.ErrInvalid, *request.ToolRuntime))
			return
		}
		settings["tool_runtime"] = runtime
	}
	if request.CloudSandboxRoot != nil {
		settings["cloud_sandbox_root"] = strings.TrimSpace(*request.CloudSandboxRoot)
	}
	if request.CloudSandboxImage != nil {
		settings["cloud_sandbox_image"] = strings.TrimSpace(*request.CloudSandboxImage)
	}
	if request.AllowNetwork != nil {
		settings["cloud_sandbox_allow_network"] = *request.AllowNetwork
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		writeError(w, err)
		return
	}
	session, err = s.store.UpdateSessionRuntimeSettings(r.PathValue("session_id"), managedagents.UpdateSessionRuntimeSettingsInput{
		RuntimeSettings: raw,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) upgradeSessionAgentConfig(w http.ResponseWriter, r *http.Request) {
	request := sessionConfigUpgradeRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeJSON(r, &request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	toCurrent := true
	if request.ToCurrent != nil {
		toCurrent = *request.ToCurrent
	}
	result, err := s.store.UpgradeSessionAgentConfig(r.PathValue("session_id"), managedagents.UpgradeSessionAgentConfigInput{
		ToCurrent: toCurrent,
		UpdatedBy: request.UpdatedBy,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listSessionInterventions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	interventions, err := s.store.ListSessionInterventions(r.PathValue("session_id"), status)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interventions": interventions})
}

func (s *Server) approveSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusApproved)
}

func (s *Server) rejectSessionIntervention(w http.ResponseWriter, r *http.Request) {
	s.decideSessionIntervention(w, r, managedagents.InterventionStatusRejected)
}

func (s *Server) decideSessionIntervention(w http.ResponseWriter, r *http.Request, status string) {
	var request interventionDecisionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.store.DecideSessionIntervention(r.PathValue("session_id"), managedagents.DecideSessionInterventionInput{
		TurnID:         r.PathValue("turn_id"),
		CallID:         r.PathValue("call_id"),
		Status:         status,
		DecisionReason: request.Reason,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	switch status {
	case managedagents.InterventionStatusApproved:
		executionResult, events, err := s.executeApprovedIntervention(r, result.Intervention)
		if err != nil {
			writeError(w, err)
			return
		}
		result.Events = append(result.Events, events...)
		if executionResult.Error == nil && len(result.Intervention.Continuation) > 0 {
			continuationEvents, err := s.continueApprovedIntervention(r, result.Intervention, executionResult)
			if err != nil {
				writeError(w, err)
				return
			}
			result.Events = append(result.Events, continuationEvents...)
		}
	case managedagents.InterventionStatusRejected:
		executionResult, events, err := s.buildRejectedInterventionObservation(result.Intervention, request.Reason)
		if err != nil {
			writeError(w, err)
			return
		}
		result.Events = append(result.Events, events...)
		if len(result.Intervention.Continuation) > 0 {
			continuationEvents, err := s.continueIntervention(r, result.Intervention, executionResult, "rejected")
			if err != nil {
				writeError(w, err)
				return
			}
			result.Events = append(result.Events, continuationEvents...)
		} else {
			reason := "tool intervention rejected"
			if request.Reason != "" {
				reason = "tool intervention rejected: " + request.Reason
			}
			events, err := s.store.FailSessionTurn(result.Intervention.SessionID, result.Intervention.TurnID, reason)
			if err != nil {
				writeError(w, err)
				return
			}
			result.Events = append(result.Events, events...)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) executeApprovedIntervention(r *http.Request, intervention managedagents.SessionIntervention) (tools.ExecutionResult, []managedagents.Event, error) {
	config, err := s.store.ResolveAgentRuntimeConfig(intervention.SessionID)
	if err != nil {
		return tools.ExecutionResult{}, nil, err
	}
	registry, _, executionContext := s.resolveToolExecution(config, intervention.SessionID, intervention.TurnID)
	executor := tools.RegistryExecutor{Registry: registry}
	executionResult, err := executor.Execute(r.Context(), tools.Call{
		ID:         intervention.CallID,
		Identifier: intervention.ToolIdentifier,
		APIName:    intervention.APIName,
		Arguments:  intervention.Arguments,
	}, executionContext)
	if err != nil {
		return tools.ExecutionResult{}, nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"turn_id": intervention.TurnID,
		"message": "Received approved tool result.",
		"data": map[string]any{
			"id":                   intervention.CallID,
			"identifier":           intervention.ToolIdentifier,
			"api_name":             intervention.APIName,
			"content":              executionResult.Content,
			"state":                rawJSONValue(executionResult.State),
			"artifacts":            executionResult.Artifacts,
			"artifact_error":       executionResult.ArtifactError,
			"pending_intervention": executionResult.PendingIntervention,
			"error":                executionResult.Error,
			"success":              executionResult.Error == nil,
			"approval_source":      "user",
		},
	})
	if err != nil {
		return tools.ExecutionResult{}, nil, err
	}
	events, err := s.store.AppendEvents(intervention.SessionID, []managedagents.AppendEventInput{{
		Type:    managedagents.EventRuntimeToolResult,
		Payload: payload,
	}})
	return executionResult, events, err
}

func (s *Server) continueApprovedIntervention(r *http.Request, intervention managedagents.SessionIntervention, executionResult tools.ExecutionResult) ([]managedagents.Event, error) {
	return s.continueIntervention(r, intervention, executionResult, "approved")
}

func (s *Server) continueIntervention(r *http.Request, intervention managedagents.SessionIntervention, executionResult tools.ExecutionResult, action string) ([]managedagents.Event, error) {
	var messages []llm.Message
	if err := json.Unmarshal(intervention.Continuation, &messages); err != nil {
		return nil, fmt.Errorf("decode intervention continuation: %w", err)
	}
	messages = append(messages, llm.Message{
		Role:       "tool",
		ToolCallID: intervention.CallID,
		Content:    []llm.ContentPart{{Type: "text", Text: tools.ResultMessage(executionResult)}},
	})

	config, err := s.store.ResolveAgentRuntimeConfig(intervention.SessionID)
	if err != nil {
		return nil, err
	}
	var client llm.Client
	if s.continuationClient != nil {
		client = s.continuationClient
	} else {
		manager, err := llm.NewManagerWithConfig(llm.ManagerConfig{
			Provider:     config.LLMProvider,
			ProviderType: config.LLMProviderType,
			Model:        config.LLMModel,
			BaseURL:      config.LLMBaseURL,
			APIKey:       os.Getenv(config.LLMAPIKeyEnv),
		})
		if err != nil {
			return nil, err
		}
		client = manager
	}
	registry, _, executionContext := s.resolveToolExecution(config, intervention.SessionID, intervention.TurnID)
	policy := tools.InterventionPolicy{Mode: tools.ParseInterventionMode(config.RuntimeSettings)}
	executor := tools.RegistryExecutor{Registry: registry}

	var allEvents []managedagents.Event
	for round := intervention.ContinuationRound + 1; round < 4; round++ {
		requestEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeLLMRequest, intervention.TurnID, "Resuming LLM after "+action+" tool result.", map[string]any{
			"provider":      config.LLMProvider,
			"provider_type": config.LLMProviderType,
			"model":         config.LLMModel,
			"message_count": len(messages),
			"tool_round":    round,
		})
		if err != nil {
			return allEvents, err
		}
		allEvents = append(allEvents, requestEvents...)

		llmRequest := llm.Request{
			Provider:     config.LLMProvider,
			ProviderType: config.LLMProviderType,
			Model:        config.LLMModel,
			BaseURL:      config.LLMBaseURL,
			APIKey:       os.Getenv(config.LLMAPIKeyEnv),
			Messages:     messages,
			Tools:        registry.ModelTools(),
		}
		startedAt := time.Now()
		llmResponse, err := client.Generate(r.Context(), llmRequest)
		if err != nil {
			s.recordContinuationUsage(intervention, config, llm.Usage{}, time.Since(startedAt), "failed", err.Error())
			return allEvents, err
		}
		s.recordContinuationUsage(intervention, config, llmResponse.Usage, time.Since(startedAt), "completed", "")

		responseEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeLLMResponse, intervention.TurnID, "Received resumed LLM response.", map[string]any{
			"role":          llmResponse.Message.Role,
			"content_count": len(llmResponse.Message.Content),
			"usage":         llmResponse.Usage,
			"tool_round":    round,
		})
		if err != nil {
			return allEvents, err
		}
		allEvents = append(allEvents, responseEvents...)

		toolCalls, hasToolCalls := toolCallsFromLLMResponse(llmResponse)
		if !hasToolCalls || len(toolCalls) == 0 {
			completedEvents, err := s.completeContinuation(intervention, llmResponse)
			if err != nil {
				return allEvents, err
			}
			allEvents = append(allEvents, completedEvents...)
			return allEvents, nil
		}

		assistantMessage := llm.Message{
			Role:      "assistant",
			Content:   []llm.ContentPart{{Type: "text", Text: contentPartsText(llmResponse.Message.Content)}},
			ToolCalls: append([]llm.ToolCall(nil), llmResponse.Message.ToolCalls...),
		}
		continuationMessages := append([]llm.Message(nil), messages...)
		continuationMessages = append(continuationMessages, assistantMessage)
		messages = append(messages, assistantMessage)

		for _, toolCall := range toolCalls {
			call := tools.NormalizeCall(toolCall)
			toolEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolCall, intervention.TurnID, "Received continuation tool call request.", map[string]any{
				"id":         call.ID,
				"identifier": call.Identifier,
				"api_name":   call.APIName,
				"arguments":  rawJSONValue(call.Arguments),
			})
			if err != nil {
				return allEvents, err
			}
			allEvents = append(allEvents, toolEvents...)

			if manifest, api, ok := registry.GetAPI(call.Identifier, call.APIName); ok {
				decision := policy.EvaluateCall(manifest, api, call, executionContext)
				if decision.Required && !decision.Allowed {
					requiredEvents, err := s.pauseContinuationForIntervention(intervention, call, decision, continuationMessages, round)
					if err != nil {
						return allEvents, err
					}
					allEvents = append(allEvents, requiredEvents...)
					return allEvents, nil
				}
				if decision.Required && decision.Allowed && decision.Mode == tools.InterventionModeApproveForMe {
					approvedEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolInterventionApproved, intervention.TurnID, "Tool call auto-approved for execution.", map[string]any{
						"id":                call.ID,
						"identifier":        call.Identifier,
						"api_name":          call.APIName,
						"arguments":         rawJSONValue(call.Arguments),
						"intervention_mode": decision.Mode,
						"reason":            decision.Reason,
						"approval_source":   "auto",
					})
					if err != nil {
						return allEvents, err
					}
					allEvents = append(allEvents, approvedEvents...)
				}
			}

			result, err := executor.Execute(r.Context(), call, executionContext)
			if err != nil {
				return allEvents, err
			}
			resultEvents, err := s.appendToolResultEvent(intervention.SessionID, intervention.TurnID, call, result, "Received continuation tool result.")
			if err != nil {
				return allEvents, err
			}
			allEvents = append(allEvents, resultEvents...)
			if result.Error != nil {
				failedEvents, err := s.store.FailSessionTurn(intervention.SessionID, intervention.TurnID, "continuation tool failed: "+result.Error.Message)
				if err != nil {
					return allEvents, err
				}
				allEvents = append(allEvents, failedEvents...)
				return allEvents, nil
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    []llm.ContentPart{{Type: "text", Text: tools.ResultMessage(result)}},
			})
		}
	}

	failedEvents, err := s.store.FailSessionTurn(intervention.SessionID, intervention.TurnID, "continuation tool loop exceeded maximum rounds")
	if err != nil {
		return allEvents, err
	}
	s.recordContinuationUsage(intervention, config, llm.Usage{}, 0, "failed", "continuation tool loop exceeded maximum rounds")
	allEvents = append(allEvents, failedEvents...)
	return allEvents, nil
}

func (s *Server) resolveToolExecution(config managedagents.AgentRuntimeConfig, sessionID string, turnID string) (tools.Registry, tools.ConfigPolicy, tools.ExecutionContext) {
	resolved := execution.ResolveToolExecution(execution.ToolExecutionRequest{
		Config:           config,
		SessionID:        sessionID,
		TurnID:           turnID,
		ProviderResolver: s.executionResolver,
		Store:            s.store,
	})
	return resolved.Registry, resolved.Policy, resolved.Context
}

func (s *Server) recordContinuationUsage(intervention managedagents.SessionIntervention, config managedagents.AgentRuntimeConfig, usage llm.Usage, latency time.Duration, status string, errorMessage string) {
	if config.WorkspaceID == "" || config.AgentID == "" || config.AgentConfigVersion <= 0 {
		return
	}
	if config.LLMProvider == "" || config.LLMModel == "" {
		return
	}
	record := managedagents.RecordLLMUsageInput{
		WorkspaceID:        config.WorkspaceID,
		AgentID:            config.AgentID,
		AgentConfigVersion: config.AgentConfigVersion,
		SessionID:          intervention.SessionID,
		TurnID:             intervention.TurnID,
		ProviderID:         config.LLMProvider,
		ProviderType:       config.LLMProviderType,
		Model:              config.LLMModel,
		InputTokens:        usage.InputTokens,
		OutputTokens:       usage.OutputTokens,
		TotalTokens:        usage.TotalTokens,
		CachedInputTokens:  usage.CachedInputTokens,
		ReasoningTokens:    usage.ReasoningTokens,
		LatencyMillis:      latency.Milliseconds(),
		Status:             status,
		ErrorMessage:       errorMessage,
	}
	if _, err := s.store.RecordLLMUsage(record); err != nil {
		s.logger.Error("continuation llm usage record failed",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"status", status,
			"error", err,
		)
	}
}

func (s *Server) appendRuntimeEvent(sessionID string, eventType string, turnID string, message string, data map[string]any) ([]managedagents.Event, error) {
	payload, err := json.Marshal(map[string]any{
		"turn_id": turnID,
		"message": message,
		"data":    data,
	})
	if err != nil {
		return nil, err
	}
	return s.store.AppendEvents(sessionID, []managedagents.AppendEventInput{{
		Type:    eventType,
		Payload: payload,
	}})
}

func (s *Server) appendToolResultEvent(sessionID string, turnID string, call tools.Call, executionResult tools.ExecutionResult, message string) ([]managedagents.Event, error) {
	return s.appendRuntimeEvent(sessionID, managedagents.EventRuntimeToolResult, turnID, message, map[string]any{
		"id":                   call.ID,
		"identifier":           call.Identifier,
		"api_name":             call.APIName,
		"content":              executionResult.Content,
		"state":                rawJSONValue(executionResult.State),
		"artifacts":            executionResult.Artifacts,
		"artifact_error":       executionResult.ArtifactError,
		"pending_intervention": executionResult.PendingIntervention,
		"error":                executionResult.Error,
		"success":              executionResult.Error == nil,
	})
}

func (s *Server) pauseContinuationForIntervention(intervention managedagents.SessionIntervention, call tools.Call, decision tools.InterventionDecision, continuationMessages []llm.Message, round int) ([]managedagents.Event, error) {
	encodedContinuation, err := json.Marshal(continuationMessages)
	if err != nil {
		return nil, fmt.Errorf("encode continuation messages: %w", err)
	}
	if _, err := s.store.SaveSessionIntervention(intervention.SessionID, managedagents.SaveSessionInterventionInput{
		TurnID:            intervention.TurnID,
		CallID:            call.ID,
		ToolIdentifier:    call.Identifier,
		APIName:           call.APIName,
		Arguments:         call.Arguments,
		InterventionMode:  decision.Mode,
		Reason:            decision.Reason,
		Continuation:      encodedContinuation,
		ContinuationRound: round,
	}); err != nil {
		return nil, err
	}
	if err := s.store.MarkSessionTurnWaitingApproval(intervention.SessionID, intervention.TurnID); err != nil {
		return nil, err
	}
	return s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolInterventionRequired, intervention.TurnID, "Tool call requires approval before execution.", map[string]any{
		"id":                call.ID,
		"identifier":        call.Identifier,
		"api_name":          call.APIName,
		"arguments":         rawJSONValue(call.Arguments),
		"intervention_mode": decision.Mode,
		"reason":            decision.Reason,
	})
}

func (s *Server) completeContinuation(intervention managedagents.SessionIntervention, llmResponse llm.Response) ([]managedagents.Event, error) {
	agentPayload, err := json.Marshal(map[string]any{
		"protocol_version": "tma.agent_runtime.demo.v1",
		"content":          llmResponse.Message.Content,
	})
	if err != nil {
		return nil, err
	}
	completedEvents, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeCompleted, intervention.TurnID, "Approved intervention continuation completed.", nil)
	if err != nil {
		return nil, err
	}
	turnEvents, err := s.store.CompleteSessionTurn(intervention.SessionID, intervention.TurnID, agentPayload)
	if err != nil {
		return completedEvents, err
	}
	allEvents := append(completedEvents, turnEvents...)
	if err := observability.RefreshSessionSummary(s.store, intervention.SessionID, intervention.TurnID); err != nil {
		s.logger.Warn("refresh session summary failed after continuation",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"error", err,
		)
	}
	if result, err := observability.ExportTurnTraceFromEnv(s.store, intervention.SessionID, intervention.TurnID); err != nil {
		s.logger.Warn("observability export failed after continuation",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"error", err,
		)
	} else if !result.Skipped {
		s.logger.Info("observability export completed after continuation",
			"session_id", intervention.SessionID,
			"turn_id", intervention.TurnID,
			"trace_id", result.TraceID,
			"perfetto_path", exporterPerfettoPath(result),
			"otlp_endpoint", exporterOTLPEndpoint(result),
		)
	}
	return allEvents, nil
}

func exporterPerfettoPath(result observability.ExporterResult) string {
	if result.Perfetto == nil {
		return ""
	}
	return result.Perfetto.Path
}

func exporterOTLPEndpoint(result observability.ExporterResult) string {
	if result.OTLPPush == nil {
		return ""
	}
	return result.OTLPPush.Endpoint
}

func (s *Server) buildRejectedInterventionObservation(intervention managedagents.SessionIntervention, decisionReason string) (tools.ExecutionResult, []managedagents.Event, error) {
	message := "Tool call rejected by user."
	if decisionReason != "" {
		message += " Reason: " + decisionReason
	}
	result := tools.ExecutionResult{
		ID:         intervention.CallID,
		Identifier: intervention.ToolIdentifier,
		APIName:    intervention.APIName,
		Content:    message,
		State: mustJSONMarshal(map[string]any{
			"rejected":        true,
			"decision_reason": decisionReason,
		}),
		Error: &tools.ExecutionError{
			Type:    "tool_rejected_by_user",
			Message: message,
		},
	}
	events, err := s.appendRuntimeEvent(intervention.SessionID, managedagents.EventRuntimeToolResult, intervention.TurnID, "Recorded rejected tool result for model continuation.", map[string]any{
		"id":                   intervention.CallID,
		"identifier":           intervention.ToolIdentifier,
		"api_name":             intervention.APIName,
		"content":              result.Content,
		"state":                rawJSONValue(result.State),
		"pending_intervention": false,
		"error":                result.Error,
		"success":              false,
		"approval_source":      "user",
		"decision_reason":      decisionReason,
	})
	return result, events, err
}

type toolCallEnvelope struct {
	ProtocolVersion string                 `json:"protocol_version"`
	ToolCalls       []toolCallEnvelopeCall `json:"tool_calls"`
}

type toolCallEnvelopeCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function toolCallFunction `json:"function,omitempty"`
}

type toolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func toolCallsFromLLMResponse(response llm.Response) ([]tools.Call, bool) {
	if len(response.Message.ToolCalls) > 0 {
		calls := make([]tools.Call, 0, len(response.Message.ToolCalls))
		for _, toolCall := range response.Message.ToolCalls {
			calls = append(calls, tools.NormalizeCall(tools.Call{
				ID:        toolCall.ID,
				APIName:   toolCall.Function.Name,
				Arguments: toolCall.Function.Arguments,
			}))
		}
		return calls, true
	}

	text := strings.TrimSpace(contentPartsText(response.Message.Content))
	if text == "" || !json.Valid([]byte(text)) {
		return nil, false
	}
	var envelope toolCallEnvelope
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		return nil, false
	}
	if envelope.ProtocolVersion != tools.ToolCallProtocolVersion || len(envelope.ToolCalls) == 0 {
		return nil, false
	}
	calls := make([]tools.Call, 0, len(envelope.ToolCalls))
	for _, envelopeCall := range envelope.ToolCalls {
		calls = append(calls, tools.NormalizeCall(tools.Call{
			ID:        envelopeCall.ID,
			APIName:   envelopeCall.Function.Name,
			Arguments: envelopeCall.Function.Arguments,
		}))
	}
	return calls, true
}

func contentPartsText(parts []llm.ContentPart) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "\n")
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func mustJSONMarshal(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return encoded
}

func (s *Server) archiveSession(w http.ResponseWriter, r *http.Request) {
	session, err := s.store.ArchiveSession(r.PathValue("session_id"))
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSession(r.PathValue("session_id")); err != nil {
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) appendSessionEvents(w http.ResponseWriter, r *http.Request) {
	var request appendEventsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	events, err := s.store.AppendEvents(r.PathValue("session_id"), request.Events)
	if err != nil {
		if reminderEvents, reminderErr := s.appendApprovalReminderIfWaiting(r.PathValue("session_id"), request.Events); reminderErr == nil && len(reminderEvents) > 0 {
			s.logEvents("session approval reminder appended", reminderEvents)
			writeJSON(w, http.StatusAccepted, map[string]any{"events": reminderEvents})
			return
		}
		writeError(w, err)
		return
	}

	// Store 先把事件和状态写入数据库；后台执行只基于已经落库的事件启动。
	sessionID := r.PathValue("session_id")
	s.logEvents("session events appended", events)
	s.dispatchRunnerEvents(r, sessionID, events)
	writeJSON(w, http.StatusCreated, map[string]any{"events": events})
}

func (s *Server) appendApprovalReminderIfWaiting(sessionID string, inputs []managedagents.AppendEventInput) ([]managedagents.Event, error) {
	if len(inputs) != 1 || inputs[0].Type != managedagents.EventUserMessage {
		return nil, nil
	}
	session, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, err
	}
	if session.Status != managedagents.SessionStatusRunning {
		return nil, nil
	}
	pending, err := s.store.ListSessionInterventions(sessionID, managedagents.InterventionStatusPending)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}

	events := make([]managedagents.AppendEventInput, 0, len(pending)+1)
	events = append(events, managedagents.AppendEventInput{
		Type:    managedagents.EventAgentMessage,
		Payload: approvalReminderPayload(pending),
	})
	for _, intervention := range pending {
		payload, err := json.Marshal(map[string]any{
			"turn_id": intervention.TurnID,
			"message": "Tool call is still waiting for approval.",
			"data": map[string]any{
				"id":                intervention.CallID,
				"identifier":        intervention.ToolIdentifier,
				"api_name":          intervention.APIName,
				"arguments":         rawJSONValue(intervention.Arguments),
				"intervention_mode": intervention.InterventionMode,
				"reason":            intervention.Reason,
			},
		})
		if err != nil {
			return nil, err
		}
		events = append(events, managedagents.AppendEventInput{
			Type:    managedagents.EventRuntimeToolInterventionRequired,
			Payload: payload,
		})
	}
	return s.store.AppendEvents(sessionID, events)
}

func approvalReminderPayload(pending []managedagents.SessionIntervention) json.RawMessage {
	turnID := pending[0].TurnID
	lines := []string{"A tool call is waiting for approval before this session can continue."}
	for _, intervention := range pending {
		lines = append(lines, fmt.Sprintf("- %s.%s call=%s", intervention.ToolIdentifier, intervention.APIName, intervention.CallID))
	}
	lines = append(lines, "Approve or reject the pending call, then send your next message.")
	payload, err := json.Marshal(map[string]any{
		"protocol_version": "tma.agent_runtime.demo.v1",
		"turn_id":          turnID,
		"content": []map[string]string{{
			"type": "text",
			"text": strings.Join(lines, "\n"),
		}},
	})
	if err != nil {
		return json.RawMessage(`{"content":[{"type":"text","text":"A tool call is waiting for approval."}]}`)
	}
	return payload
}

func (s *Server) dispatchRunnerEvents(r *http.Request, sessionID string, events []managedagents.Event) {
	for _, event := range events {
		switch event.Type {
		case managedagents.EventUserMessage:
			// turn_id 由 Store 生成并写入 payload，避免客户端伪造执行编号。
			turnID := payloadString(event.Payload, "turn_id")
			s.logger.Info("session turn starting",
				"session_id", sessionID,
				"turn_id", turnID,
				"event_id", event.ID,
				"event_seq", event.Seq,
			)
			if err := s.runner.StartTurn(r.Context(), runner.TurnRequest{
				SessionID:    sessionID,
				TurnID:       turnID,
				UserEventSeq: event.Seq,
				UserPayload:  event.Payload,
			}); err != nil {
				reason := err.Error()
				s.logger.Error("runner start turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"event_id", event.ID,
					"event_seq", event.Seq,
					"error", err,
				)
				failedEvents, failErr := s.store.FailSessionTurn(sessionID, turnID, reason)
				if failErr != nil {
					s.logger.Error("session turn fail transition failed",
						"session_id", sessionID,
						"turn_id", turnID,
						"error", failErr,
					)
					continue
				}
				s.logEvents("session turn failed", failedEvents)
			}
		case managedagents.EventUserInterrupt:
			turnID := payloadString(event.Payload, "turn_id")
			if err := s.runner.InterruptTurn(r.Context(), runner.InterruptRequest{
				SessionID: sessionID,
				TurnID:    turnID,
			}); err != nil {
				s.logger.Error("runner interrupt turn failed",
					"session_id", sessionID,
					"turn_id", turnID,
					"event_id", event.ID,
					"event_seq", event.Seq,
					"error", err,
				)
			}
		}
	}
}

func (s *Server) logEvents(message string, events []managedagents.Event) {
	for _, event := range events {
		s.logger.Info(message,
			"event_id", event.ID,
			"session_id", event.SessionID,
			"turn_id", payloadString(event.Payload, "turn_id"),
			"event_seq", event.Seq,
			"event_type", event.Type,
		)
	}
}

func payloadString(payload json.RawMessage, key string) string {
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return ""
	}

	value, ok := object[key].(string)
	if !ok {
		return ""
	}
	return value
}

func (s *Server) listSessionEvents(w http.ResponseWriter, r *http.Request) {
	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	events, err := s.store.ListEvents(r.PathValue("session_id"), afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) streamSessionEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	afterSeq, err := parseAfterSeq(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	sessionID := r.PathValue("session_id")
	// SSE 先用 after_seq 补历史，再订阅未来事件，支持断线续传。
	history, err := s.store.ListEvents(sessionID, afterSeq)
	if err != nil {
		writeError(w, err)
		return
	}

	events, cancel, err := s.store.SubscribeEvents(sessionID)
	if err != nil {
		writeError(w, err)
		return
	}
	defer cancel()
	s.logger.Info("sse stream opened",
		"session_id", sessionID,
		"after_seq", afterSeq,
		"history_events", len(history),
	)
	defer s.logger.Info("sse stream closed",
		"session_id", sessionID,
		"after_seq", afterSeq,
	)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for _, event := range history {
		if err := writeSSE(w, event); err != nil {
			return
		}
		flusher.Flush()
	}

	fmt.Fprint(w, ": stream ready\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Seq <= afterSeq {
				continue
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func parseAfterSeq(r *http.Request) (int64, error) {
	value := r.URL.Query().Get("after_seq")
	if value == "" {
		return 0, nil
	}

	return strconv.ParseInt(value, 10, 64)
}

func writeSSE(w http.ResponseWriter, event managedagents.Event) error {
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, encoded)
	return err
}
