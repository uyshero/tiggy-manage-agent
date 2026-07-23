import { afterEach, describe, expect, it } from "vitest";
import { TMAClient } from "../src/index.js";
import { json, readBody, startServer, type TestServer } from "./helpers.js";

describe("Run comparisons and evaluations", () => {
  let server: TestServer | undefined;
  afterEach(async () => { await server?.close(); server = undefined; });

  it("compares Runs and persists rubric-backed evaluations", async () => {
    const requests: Array<{ method: string; url: string; body?: unknown }> = [];
    server = await startServer(async (request, response) => {
	  const raw = request.method === "POST" ? await readBody(request) : undefined;
	  const body = raw?.length ? JSON.parse(raw.toString()) : undefined;
      requests.push({ method: request.method || "", url: request.url || "", ...(body === undefined ? {} : { body }) });
      if (request.url?.startsWith("/v2/run-comparisons")) {
        json(response, 200, { left: { session: {}, usage: { summary: {}, records: [] }, artifacts: [] }, right: { session: {}, usage: { summary: {}, records: [] }, artifacts: [] } });
      } else if (request.url === "/v2/evaluation-rubrics" && request.method === "POST") {
        json(response, 201, { id: "rubric/1", workspace_id: "workspace/1", name: body.name, criteria: body.criteria, revision: 1, created_at: "2026-07-23T00:00:00Z", updated_at: "2026-07-23T00:00:00Z" });
      } else if (request.url?.startsWith("/v2/evaluation-rubrics")) {
        json(response, 200, { rubrics: [] });
      } else if (request.url === "/v2/run-evaluations/auto" && request.method === "POST") {
        json(response, 201, { id: "evaluation/2", ...body, scores: [], conclusion: "tie", evaluation_type: "auto", workspace_id: "workspace/1", rubric_snapshot: {}, created_at: "2026-07-23T00:00:00Z" });
      } else if (request.url === "/v2/run-evaluations" && request.method === "POST") {
        json(response, 201, { id: "evaluation/1", ...body, evaluation_type: "manual", workspace_id: "workspace/1", rubric_snapshot: {}, created_at: "2026-07-23T00:00:00Z" });
	  } else if (request.url === "/v2/evaluation-datasets" && request.method === "POST") {
		json(response, 201, { id: "dataset/1", ...body, workspace_id: "workspace/1", created_at: "2026-07-23T00:00:00Z", updated_at: "2026-07-23T00:00:00Z" });
	  } else if (request.url?.startsWith("/v2/evaluation-datasets")) {
		json(response, 200, request.url === "/v2/evaluation-datasets/dataset%2F1" ? { id: "dataset/1", items: [] } : { datasets: [] });
	  } else if (request.url === "/v2/evaluation-experiments" && request.method === "POST") {
		json(response, 201, { id: "experiment/1", ...body, workspace_id: "workspace/1", status: "running", summary: {}, items: [], created_at: "2026-07-23T00:00:00Z", updated_at: "2026-07-23T00:00:00Z" });
	  } else if (request.url === "/v2/evaluation-experiments/experiment%2F1/reconcile") {
		json(response, 200, { id: "experiment/1", status: "completed", summary: {}, items: [] });
	  } else if (request.url?.startsWith("/v2/evaluation-experiments")) {
		json(response, 200, request.url === "/v2/evaluation-experiments/experiment%2F1" ? { id: "experiment/1", summary: {}, items: [] } : { experiments: [] });
      } else {
        json(response, 200, { evaluations: [] });
      }
    });

    const client = new TMAClient(server.baseURL);
    await client.sessions.compareRuns("session/a", "turn/a", "session/b", "turn/b");
    const rubric = await client.evaluations.createRubric({
      workspace_id: "workspace/1",
      name: "Default",
      criteria: [{ id: "quality", name: "Quality" }, { id: "safety", name: "Safety" }],
    });
    await client.evaluations.listRubrics("workspace/1");
    await client.evaluations.createRunEvaluation({
      left_session_id: "session/a", left_turn_id: "turn/a",
      right_session_id: "session/b", right_turn_id: "turn/b",
      rubric_id: rubric.id,
      scores: [{ criterion_id: "quality", left_score: 3, right_score: 5 }, { criterion_id: "safety", left_score: 4, right_score: 4 }],
      conclusion: "right",
    });
    await client.evaluations.autoEvaluate({
      left_session_id: "session/a", left_turn_id: "turn/a",
      right_session_id: "session/b", right_turn_id: "turn/b",
      rubric_id: rubric.id,
    });
    await client.evaluations.listRunEvaluations({
      leftSessionId: "session/a", leftTurnId: "turn/a",
      rightSessionId: "session/b", rightTurnId: "turn/b", limit: 20,
    });
	const dataset = await client.evaluations.createDataset({ name: "Regression", items: [{ prompt: "Explain RLS" }] });
	await client.evaluations.listDatasets("workspace/1");
	await client.evaluations.getDataset(dataset.id);
	const experiment = await client.evaluations.createExperiment({
	  name: "Nightly", dataset_id: dataset.id, rubric_id: rubric.id,
	  left_template_session_id: "session/a", right_template_session_id: "session/b",
	});
	await client.evaluations.listExperiments("workspace/1", 10);
	await client.evaluations.getExperiment(experiment.id);
	await client.evaluations.reconcileExperiment(experiment.id);

    expect(requests.map((item) => `${item.method} ${item.url}`)).toEqual([
      "GET /v2/run-comparisons?left_session_id=session%2Fa&left_turn_id=turn%2Fa&right_session_id=session%2Fb&right_turn_id=turn%2Fb",
      "POST /v2/evaluation-rubrics",
      "GET /v2/evaluation-rubrics?workspace_id=workspace%2F1",
      "POST /v2/run-evaluations",
      "POST /v2/run-evaluations/auto",
      "GET /v2/run-evaluations?left_session_id=session%2Fa&left_turn_id=turn%2Fa&right_session_id=session%2Fb&right_turn_id=turn%2Fb&limit=20",
	  "POST /v2/evaluation-datasets",
	  "GET /v2/evaluation-datasets?workspace_id=workspace%2F1",
	  "GET /v2/evaluation-datasets/dataset%2F1",
	  "POST /v2/evaluation-experiments",
	  "GET /v2/evaluation-experiments?workspace_id=workspace%2F1&limit=10",
	  "GET /v2/evaluation-experiments/experiment%2F1",
	  "POST /v2/evaluation-experiments/experiment%2F1/reconcile",
    ]);
    expect(requests[3]?.body).toMatchObject({ rubric_id: "rubric/1", conclusion: "right" });
    expect(requests[4]?.body).toMatchObject({ rubric_id: "rubric/1" });
	expect(requests[6]?.body).toMatchObject({ name: "Regression" });
	expect(requests[9]?.body).toMatchObject({ dataset_id: "dataset/1", rubric_id: "rubric/1" });
  });
});
