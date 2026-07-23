import assert from "node:assert/strict";
import test from "node:test";
import {
  averageScore,
  conclusionLabel,
  createEvaluation,
  evaluationFromRubric,
  evaluationStorageKey,
  formatDelta,
  localizedCriterion,
  normalizeEvaluation,
  statusLabel,
  summarizeEvidence
} from "./analysis.js";

test("summarizeEvidence projects comparison and trace evidence", () => {
  const summary = summarizeEvidence({
    comparison: {
      session: { id: "sesn_a", status: "idle" },
      llm_provider: "openai",
      llm_model: "gpt-test",
      prompt: "Analyze this",
      result: "Done",
      duration_ms: 30,
      usage: { summary: { total_tokens: 120, input_tokens: 80, output_tokens: 40, latency_ms: 25 } },
      artifacts: [{ id: "artifact-1" }]
    },
    trace: {
      trace_id: "trace-1",
      turn_id: "turn-1",
      status: "completed",
      stats: { duration_ms: 35, tool_calls: 1, errors: 0, step_count: 3, span_count: 2 },
      graph: { critical_path_duration_ms: 22 },
      steps: [{ type: "runtime.tool_call", api_name: "read_file" }]
    }
  });
  assert.equal(summary.totalTokens, 120);
  assert.equal(summary.durationMS, 35);
  assert.equal(summary.toolCalls, 1);
  assert.equal(summary.criticalPathMS, 22);
  assert.equal(summary.artifactCount, 1);
});

test("display labels localize stable protocol values", () => {
  assert.equal(statusLabel("failed"), "失败");
	assert.equal(statusLabel("queued"), "排队中");
  assert.equal(conclusionLabel("right"), "B 更优");
  assert.equal(localizedCriterion({ id: "correctness", name: "Correctness" }).name, "正确性");
});

test("persisted evaluations are projected through their rubric", () => {
  const evaluation = evaluationFromRubric({ criteria: [{ id: "quality", name: "Quality" }] }, {
    conclusion: "right",
    created_at: "2026-07-23T01:00:00Z",
    scores: [{ criterion_id: "quality", left_score: 2, right_score: 5 }]
  });
  assert.equal(evaluation.criteria[0].rightScore, 5);
  assert.equal(evaluation.conclusion, "right");
  assert.equal(evaluation.savedAt, "2026-07-23T01:00:00Z");
});

test("evaluation helpers normalize scores and compute averages", () => {
  const evaluation = createEvaluation();
  evaluation.criteria[0].leftScore = 5;
  evaluation.criteria[0].rightScore = 1;
  assert.equal(averageScore(evaluation.criteria, "left"), 3.5);
  assert.equal(averageScore(evaluation.criteria, "right"), 2.5);

  const normalized = normalizeEvaluation({ conclusion: "invalid", criteria: [{ name: "A", leftScore: 9, rightScore: 0 }] });
  assert.equal(normalized.conclusion, "inconclusive");
  assert.equal(normalized.criteria[0].leftScore, 5);
  assert.equal(normalized.criteria[0].rightScore, 1);
});

test("comparison identifiers and deltas are stable", () => {
  assert.equal(evaluationStorageKey("sesn/a", "sesn/b"), "tma.space.evaluation.v1:sesn/a:sesn/b");
  assert.equal(formatDelta(12, " ms"), "+12 ms");
  assert.equal(formatDelta(-5), "-5");
});
