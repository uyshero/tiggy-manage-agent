import { coreSDK } from "./core-sdk.js";

async function optional(request, fallback) {
  try {
    return await request;
  } catch (error) {
    if (error?.name === "AbortError") throw error;
    return typeof fallback === "function" ? fallback(error) : fallback;
  }
}

export async function catalogs(signal) {
  const [agents, sessions] = await Promise.all([
    coreSDK.agents.list(signal),
    coreSDK.sessions.list({ limit: 100, includeArchived: true }, signal)
  ]);
  return { agents, sessions };
}

export function runs(sessionID, signal) {
  if (!sessionID) return Promise.resolve([]);
  return coreSDK.runs.list(sessionID, signal);
}

export async function analyze(leftSessionID, leftTurnID, rightSessionID, rightTurnID, signal) {
  const comparison = await coreSDK.sessions.compareRuns(leftSessionID, leftTurnID, rightSessionID, rightTurnID, signal);
  const [left, right] = await Promise.all([
    loadEvidence(leftSessionID, leftTurnID, comparison.left, signal),
    loadEvidence(rightSessionID, rightTurnID, comparison.right, signal)
  ]);
  return {
    comparison,
    left,
    right
  };
}

async function loadEvidence(sessionID, turnID, comparison, signal) {
  const events = await optional(coreSDK.runs.listEvents(sessionID, turnID, 0, signal), []);
  return { comparison, trace: comparison.trace || null, events };
}

export function listRubrics(workspaceID, signal) {
  return coreSDK.evaluations.listRubrics(workspaceID, signal);
}

export function createRubric(workspaceID, criteria, signal) {
  return coreSDK.evaluations.createRubric({
    workspace_id: workspaceID,
    name: "TMA Space default",
    description: "由 TMA Space 创建的可复用运行对比评分标准。",
    criteria: criteria.map(({ id, name, description }) => ({ id, name, description }))
  }, signal);
}

export function listEvaluations(leftSessionID, leftTurnID, rightSessionID, rightTurnID, signal) {
  return coreSDK.evaluations.listRunEvaluations({
    leftSessionId: leftSessionID,
    leftTurnId: leftTurnID,
    rightSessionId: rightSessionID,
    rightTurnId: rightTurnID,
    limit: 20
  }, signal);
}

export function saveEvaluation({ leftSessionID, leftTurnID, rightSessionID, rightTurnID, rubricID, evaluation }, signal) {
  return coreSDK.evaluations.createRunEvaluation({
    left_session_id: leftSessionID,
    left_turn_id: leftTurnID,
    right_session_id: rightSessionID,
    right_turn_id: rightTurnID,
    rubric_id: rubricID,
    scores: evaluation.criteria.map((criterion) => ({
      criterion_id: criterion.id,
      left_score: criterion.leftScore,
      right_score: criterion.rightScore
    })),
    conclusion: evaluation.conclusion,
    notes: evaluation.notes
  }, signal);
}

export function autoEvaluate({ leftSessionID, leftTurnID, rightSessionID, rightTurnID, rubricID }, signal) {
  return coreSDK.evaluations.autoEvaluate({
    left_session_id: leftSessionID,
    left_turn_id: leftTurnID,
    right_session_id: rightSessionID,
    right_turn_id: rightTurnID,
    rubric_id: rubricID
  }, signal);
}

export function listDatasets(workspaceID, signal) {
  return coreSDK.evaluations.listDatasets(workspaceID, signal);
}

export function createDataset(workspaceID, { name, description, items }, signal) {
  return coreSDK.evaluations.createDataset({
    workspace_id: workspaceID,
    name,
    description,
    items: items.map((item) => ({
      prompt: item.prompt,
      expected_output: item.expectedOutput,
      tags: String(item.tags || "").split(",").map((tag) => tag.trim()).filter(Boolean)
    }))
  }, signal);
}

export function listExperiments(workspaceID, signal) {
  return coreSDK.evaluations.listExperiments(workspaceID, 30, signal);
}

export function createExperiment({ name, datasetID, rubricID, leftTemplateSessionID, rightTemplateSessionID }, signal) {
  return coreSDK.evaluations.createExperiment({
    name,
    dataset_id: datasetID,
    rubric_id: rubricID,
    left_template_session_id: leftTemplateSessionID,
    right_template_session_id: rightTemplateSessionID
  }, signal);
}

export function reconcileExperiment(experimentID, signal) {
  return coreSDK.evaluations.reconcileExperiment(experimentID, signal);
}
