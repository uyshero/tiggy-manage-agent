import assert from "node:assert/strict";
import test from "node:test";
import { conversationFinalFileArtifacts, finalAgentMessageArtifacts } from "./artifactAssociations.js";

function exportedArtifact(id, turnID, path, name = path.split("/").at(-1)) {
  return {
    id,
    turn_id: turnID,
    name,
    description: "Exported file",
    artifact_type: "file",
    metadata: { protocol_version: "tma.tool_export.v1", path }
  };
}

test("structured artifact_ids select exact Artifacts without path text", () => {
  const artifacts = [
    exportedArtifact("art_report", "turn_1", "/workspace/report.docx"),
    exportedArtifact("art_notes", "turn_1", "/workspace/notes.md")
  ];

  const result = finalAgentMessageArtifacts({
    type: "agent.message",
    payload: {
      turn_id: "turn_1",
      artifact_ids: ["art_report"],
      content: [{ type: "text", text: "Done. The file is attached below." }]
    }
  }, artifacts);

  assert.deepEqual(result.map((artifact) => artifact.id), ["art_report"]);
});

test("structured artifact_ids prevent unrelated same-turn exports from appearing", () => {
  const artifacts = [
    exportedArtifact("art_docx", "turn_1", "/workspace/final.docx"),
    exportedArtifact("art_xlsx", "turn_1", "/workspace/data.xlsx")
  ];

  const result = finalAgentMessageArtifacts({
    type: "agent.message",
    payload: { turn_id: "turn_1", artifact_ids: ["art_xlsx"], content: "final.docx is not the selected deliverable" }
  }, artifacts);

  assert.deepEqual(result.map((artifact) => artifact.id), ["art_xlsx"]);
});

test("structured artifact_ids preserve order and ignore unknown or duplicate IDs", () => {
  const artifacts = [
    exportedArtifact("art_a", "turn_1", "/workspace/a.docx"),
    exportedArtifact("art_b", "turn_1", "/workspace/b.docx")
  ];

  const result = finalAgentMessageArtifacts({
    type: "agent.message",
    payload: { turn_id: "turn_1", artifact_ids: ["missing", "art_b", "art_a", "art_b"] }
  }, artifacts);

  assert.deepEqual(result.map((artifact) => artifact.id), ["art_b", "art_a"]);
});

test("legacy final messages still use path and name fallback", () => {
  const artifacts = [
    exportedArtifact("art_report", "turn_1", "/workspace/report.docx"),
    exportedArtifact("art_notes", "turn_1", "/workspace/notes.md")
  ];

  const result = finalAgentMessageArtifacts({
    type: "agent.message",
    payload: { turn_id: "turn_1", content: "已生成 /workspace/report.docx" }
  }, artifacts);

  assert.deepEqual(result.map((artifact) => artifact.id), ["art_report"]);
});

test("conversation result files respect structured and legacy associations", () => {
  const artifacts = [
    exportedArtifact("art_report", "turn_1", "/workspace/report.docx"),
    exportedArtifact("art_notes", "turn_1", "/workspace/notes.md"),
    exportedArtifact("art_chart", "turn_2", "/workspace/chart.png"),
    exportedArtifact("art_data", "turn_2", "/workspace/data.xlsx")
  ];
  const events = [
    { type: "agent.message", payload: { turn_id: "turn_1", artifact_ids: ["art_notes"], content: "Done" } },
    { type: "agent.message", payload: { turn_id: "turn_2", content: "See /workspace/chart.png" } }
  ];

  const result = conversationFinalFileArtifacts(artifacts, events);

  assert.deepEqual(result.map((artifact) => artifact.id), ["art_notes", "art_chart"]);
});
