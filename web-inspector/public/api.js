(function(global){
function tracePath(sessionId,turnId,format){const query=[];if(turnId)query.push("turn_id="+encodeURIComponent(turnId));if(format)query.push("format="+encodeURIComponent(format));return "/v1/sessions/"+encodeURIComponent(sessionId)+"/trace"+(query.length?"?"+query.join("&"):"")}
function metricsPath(sessionId,turnId){const query=["session_id="+encodeURIComponent(sessionId)];if(turnId)query.push("turn_id="+encodeURIComponent(turnId));return "/metrics?"+query.join("&")}
async function getJSON(path,options){const response=await fetch(path,options||{});if(!response.ok)throw new Error(await response.text());return response.json()}
async function getText(path,options){const response=await fetch(path,options||{});if(!response.ok)throw new Error(await response.text());return response.text()}
async function postJSON(path,body){const response=await fetch(path,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body||{})});if(!response.ok)throw new Error(await response.text());return response.json()}
async function getBlob(path){const response=await fetch(path);if(!response.ok)throw new Error(await response.text());return response}
function trace(sessionId,turnId,format,options){return getJSON(tracePath(sessionId,turnId,format),options)}
function createAgent(body){return postJSON("/v1/agents",body)}
function createEnvironment(body){return postJSON("/v1/environments",body)}
function createSession(body){return postJSON("/v1/sessions",body)}
function sendSessionMessage(sessionId,text){return postJSON("/v1/sessions/"+encodeURIComponent(sessionId)+"/events",{events:[{type:"user.message",payload:{content:[{type:"text",text:text}]}}]})}
function traceCatalog(filters){const params=new URLSearchParams();filters=filters||{};params.set("limit",String(filters.limit||20));if(filters.offset)params.set("offset",String(filters.offset));if(filters.session)params.set("session_id",filters.session);if(filters.turn)params.set("turn_id",filters.turn);return getJSON("/v1/traces?"+params.toString())}
function traceByID(traceID,options){return getJSON("/v1/traces/"+encodeURIComponent(traceID),options)}
function spanByID(traceID,spanID,options){return getJSON("/v1/traces/"+encodeURIComponent(traceID)+"/spans/"+encodeURIComponent(spanID),options)}
function spanCatalog(filters){const params=new URLSearchParams();filters=filters||{};params.set("limit",String(filters.limit||20));if(filters.offset)params.set("offset",String(filters.offset));if(filters.session)params.set("session_id",filters.session);if(filters.turn)params.set("turn_id",filters.turn);if(filters.query)params.set("q",filters.query);if(filters.kind)params.set("kind",filters.kind);if(filters.status)params.set("status",filters.status);if(filters.critical)params.set("critical",filters.critical);if(filters.minDuration)params.set("min_duration_ms",filters.minDuration);return getJSON("/v1/spans?"+params.toString())}
function session(sessionId,options){return getJSON("/v1/sessions/"+encodeURIComponent(sessionId),options)}
function usage(sessionId,options){return getJSON("/v1/sessions/"+encodeURIComponent(sessionId)+"/usage",options)}
function summary(sessionId,options){return getJSON("/v1/sessions/"+encodeURIComponent(sessionId)+"/summary",options)}
function artifacts(sessionId,options){return getJSON("/v1/sessions/"+encodeURIComponent(sessionId)+"/artifacts",options)}
function artifactDownloadPath(sessionId,artifactId){return "/v1/sessions/"+encodeURIComponent(sessionId)+"/artifacts/"+encodeURIComponent(artifactId)+"/download"}
function events(sessionId,afterSeq,options){const suffix=afterSeq>0?"?after_seq="+encodeURIComponent(afterSeq):"";return getJSON("/v1/sessions/"+encodeURIComponent(sessionId)+"/events"+suffix,options)}
function interventions(sessionId,status,options){const suffix=status?"?status="+encodeURIComponent(status):"";return getJSON("/v1/sessions/"+encodeURIComponent(sessionId)+"/interventions"+suffix,options)}
function metrics(sessionId,turnId,options){return getText(metricsPath(sessionId,turnId),options)}
function observabilityStatus(options){return getJSON("/v1/observability/status",options)}
function retryObservability(){return postJSON("/v1/observability/retry",{})}
function approveIntervention(sessionId,turnId,callId,body){return postJSON("/v1/sessions/"+sessionId+"/interventions/"+turnId+"/"+callId+"/approve",body)}
function rejectIntervention(sessionId,turnId,callId,body){return postJSON("/v1/sessions/"+sessionId+"/interventions/"+turnId+"/"+callId+"/reject",body)}
global.TMAInspectorAPI={tracePath:tracePath,metricsPath:metricsPath,getJSON:getJSON,getText:getText,postJSON:postJSON,getBlob:getBlob,trace:trace,createAgent:createAgent,createEnvironment:createEnvironment,createSession:createSession,sendSessionMessage:sendSessionMessage,traceCatalog:traceCatalog,traceByID:traceByID,spanByID:spanByID,spanCatalog:spanCatalog,session:session,usage:usage,summary:summary,artifacts:artifacts,artifactDownloadPath:artifactDownloadPath,events:events,interventions:interventions,metrics:metrics,observabilityStatus:observabilityStatus,retryObservability:retryObservability,approveIntervention:approveIntervention,rejectIntervention:rejectIntervention};
})(window);
