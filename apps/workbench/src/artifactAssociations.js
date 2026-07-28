function eventPayload(event) {
  return event?.payload && typeof event.payload === "object" && !Array.isArray(event.payload)
    ? event.payload
    : {};
}

function eventText(event) {
  const data = eventPayload(event);
  const content = data.content;
  if (Array.isArray(content)) {
    return content.map((item) => item?.text || item?.content || "").filter(Boolean).join("\n");
  }
  if (typeof content === "string") return content;
  return data.message || data.summary || data.text || "";
}

function artifactName(artifact) {
  return artifact?.name || artifact?.id || "artifact";
}

function artifactMetadata(artifact) {
  const metadata = artifact?.metadata;
  return metadata && typeof metadata === "object" && !Array.isArray(metadata) ? metadata : {};
}

function isUserFileArtifact(artifact) {
  const metadata = artifactMetadata(artifact);
  return metadata.protocol_version === "tma.tool_export.v1" || String(artifact?.description || "").startsWith("Exported file");
}

function finalFileArtifacts(artifacts) {
  const latestByPath = new Map();
  for (const artifact of artifacts || []) {
    if (!isUserFileArtifact(artifact)) continue;
    const metadata = artifactMetadata(artifact);
    const key = String(metadata.path || artifactName(artifact)).replace(/\\/g, "/");
    latestByPath.set(key, artifact);
  }
  return [...latestByPath.values()];
}

function artifactReferencedByMessage(artifact, text) {
  const normalizedText = String(text || "").replace(/\\/g, "/");
  const metadata = artifactMetadata(artifact);
  const path = String(metadata.path || metadata.file_path || "").replace(/\\/g, "/");
  const name = artifactName(artifact);
  return Boolean((path && normalizedText.includes(path)) || (name && normalizedText.includes(name)));
}

function messageArtifactIDs(event) {
  const ids = eventPayload(event).artifact_ids;
  if (!Array.isArray(ids)) return null;
  const result = [];
  const seen = new Set();
  for (const id of ids) {
    const value = String(id || "").trim();
    if (!value || seen.has(value)) continue;
    seen.add(value);
    result.push(value);
  }
  return result;
}

function artifactsByIDs(artifacts, ids) {
  const byID = new Map();
  for (const artifact of artifacts || []) {
    if (artifact?.id) byID.set(artifact.id, artifact);
  }
  return ids.map((id) => byID.get(id)).filter(Boolean);
}

export function finalAgentMessageArtifacts(event, artifacts) {
  const structuredIDs = messageArtifactIDs(event);
  if (structuredIDs) return artifactsByIDs(artifacts, structuredIDs);

  const turnID = eventPayload(event).turn_id || "";
  const candidates = finalFileArtifacts((artifacts || []).filter((artifact) => artifact.turn_id === turnID));
  const referenced = candidates.filter((artifact) => artifactReferencedByMessage(artifact, eventText(event)));
  return referenced.length ? referenced : candidates;
}

export function conversationFinalFileArtifacts(artifacts, events) {
  const candidates = finalFileArtifacts(artifacts);
  const finalMessageByTurn = new Map();
  for (const event of events || []) {
    if (event.type === "agent.message") finalMessageByTurn.set(eventPayload(event).turn_id || "", event);
  }

  const structuredTurns = new Set();
  const structuredIDs = new Set();
  for (const event of finalMessageByTurn.values()) {
    const ids = messageArtifactIDs(event);
    if (!ids) continue;
    structuredTurns.add(eventPayload(event).turn_id || "");
    for (const id of ids) structuredIDs.add(id);
  }

  const referencedTurns = new Set();
  for (const artifact of candidates) {
    const turnID = artifact.turn_id || "";
    if (structuredTurns.has(turnID)) continue;
    const event = finalMessageByTurn.get(turnID);
    if (event && artifactReferencedByMessage(artifact, eventText(event))) referencedTurns.add(turnID);
  }

  return candidates.filter((artifact) => {
    const turnID = artifact.turn_id || "";
    if (structuredTurns.has(turnID)) return structuredIDs.has(artifact.id);
    if (!referencedTurns.has(turnID)) return true;
    return artifactReferencedByMessage(artifact, eventText(finalMessageByTurn.get(turnID)));
  });
}
