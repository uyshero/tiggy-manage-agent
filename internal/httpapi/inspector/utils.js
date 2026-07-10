(function(global){
function escapeHTML(text){return String(text||"").replace(/[&<>]/g,function(c){return({"&":"&amp;","<":"&lt;",">":"&gt;"}[c])})}
function escapeAttr(text){return escapeHTML(text).replace(/"/g,"&quot;")}
function pretty(value){return JSON.stringify(value,null,2)}
function formatTime(value){if(!value)return"-";const date=new Date(value);if(Number.isNaN(date.getTime()))return String(value);return date.toLocaleString()}
function formatDuration(ms){const value=Number(ms||0);if(value<1000)return value+" ms";const seconds=(value/1000).toFixed(value<10000?2:1);return seconds+" s"}
function pillClass(statusValue){if(statusValue==="completed"||statusValue==="ok"||statusValue==="success"||statusValue==="approved")return"pill ok";if(statusValue==="waiting_approval"||statusValue==="pending"||statusValue==="blocked")return"pill warn";if(statusValue==="failed"||statusValue==="error"||statusValue==="rejected")return"pill err";return"pill"}
function stepClass(step){if(step.outcome==="error"||step.type==="runtime.failed")return"step error";if(step.type&&step.type.indexOf("intervention")!==-1)return"step approval";if(step.type&&step.type.indexOf("tool")!==-1)return"step tool";return"step"}
function sessionArtifactCLI(downloadPath){downloadPath=String(downloadPath||"").trim();if(!downloadPath)return"";downloadPath=downloadPath.split("?")[0].split("#")[0];const prefix="/v1/sessions/";if(!downloadPath.startsWith(prefix))return"";const parts=downloadPath.slice(prefix.length).split("/");if(parts.length!==4||parts[1]!=="artifacts"||parts[3]!=="download")return"";if(!parts[0]||!parts[2])return"";return "bin/tma session artifact download --session "+parts[0]+" --artifact "+parts[2]}
function sessionArtifactCommand(sessionId,artifactId){sessionId=String(sessionId||"").trim();artifactId=String(artifactId||"").trim();if(!sessionId||!artifactId)return"";return "bin/tma session artifact download --session "+sessionId+" --artifact "+artifactId}
global.TMAInspectorUtils={escapeHTML:escapeHTML,escapeAttr:escapeAttr,pretty:pretty,formatTime:formatTime,formatDuration:formatDuration,pillClass:pillClass,stepClass:stepClass,sessionArtifactCLI:sessionArtifactCLI,sessionArtifactCommand:sessionArtifactCommand};
})(window);
