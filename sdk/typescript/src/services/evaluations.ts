import type {
  AutoRunEvaluationRequest,
  CreateEvaluationDatasetRequest,
  CreateEvaluationExperimentRequest,
  CreateEvaluationRubricRequest,
  CreateRunEvaluationRequest,
  EvaluationDataset,
  EvaluationExperiment,
  EvaluationRubric,
  RunEvaluation,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export interface RunEvaluationListQuery {
  leftSessionId: string;
  leftTurnId: string;
  rightSessionId: string;
  rightTurnId: string;
  limit?: number;
}

export class EvaluationsService extends ServiceBase {
  createRubric(request: CreateEvaluationRubricRequest, signal?: AbortSignal): Promise<EvaluationRubric> {
    return this.transport.requestJSON("POST", "/v2/evaluation-rubrics", request, signal ? { signal } : {});
  }

  listRubrics(workspaceId?: string, signal?: AbortSignal): Promise<EvaluationRubric[]> {
    const path = withQuery("/v2/evaluation-rubrics", { workspace_id: workspaceId });
    return this.transport.requestJSON<{ rubrics: EvaluationRubric[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.rubrics);
  }

  getRubric(rubricId: string, signal?: AbortSignal): Promise<EvaluationRubric> {
    return this.transport.requestJSON("GET", resourcePath("/v2/evaluation-rubrics", rubricId), undefined, signal ? { signal } : {});
  }

  createRunEvaluation(request: CreateRunEvaluationRequest, signal?: AbortSignal): Promise<RunEvaluation> {
    return this.transport.requestJSON("POST", "/v2/run-evaluations", request, signal ? { signal } : {});
  }

  autoEvaluate(request: AutoRunEvaluationRequest, signal?: AbortSignal): Promise<RunEvaluation> {
    return this.transport.requestJSON("POST", "/v2/run-evaluations/auto", request, signal ? { signal } : {});
  }

  createDataset(request: CreateEvaluationDatasetRequest, signal?: AbortSignal): Promise<EvaluationDataset> {
    return this.transport.requestJSON("POST", "/v2/evaluation-datasets", request, signal ? { signal } : {});
  }

  listDatasets(workspaceId?: string, signal?: AbortSignal): Promise<EvaluationDataset[]> {
    const path = withQuery("/v2/evaluation-datasets", { workspace_id: workspaceId });
    return this.transport.requestJSON<{ datasets: EvaluationDataset[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.datasets);
  }

  getDataset(datasetId: string, signal?: AbortSignal): Promise<EvaluationDataset> {
    return this.transport.requestJSON("GET", resourcePath("/v2/evaluation-datasets", datasetId), undefined, signal ? { signal } : {});
  }

  createExperiment(request: CreateEvaluationExperimentRequest, signal?: AbortSignal): Promise<EvaluationExperiment> {
    return this.transport.requestJSON("POST", "/v2/evaluation-experiments", request, signal ? { signal } : {});
  }

  listExperiments(workspaceId?: string, limit?: number, signal?: AbortSignal): Promise<EvaluationExperiment[]> {
    const path = withQuery("/v2/evaluation-experiments", { workspace_id: workspaceId, limit });
    return this.transport.requestJSON<{ experiments: EvaluationExperiment[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.experiments);
  }

  getExperiment(experimentId: string, signal?: AbortSignal): Promise<EvaluationExperiment> {
    return this.transport.requestJSON("GET", resourcePath("/v2/evaluation-experiments", experimentId), undefined, signal ? { signal } : {});
  }

  reconcileExperiment(experimentId: string, signal?: AbortSignal): Promise<EvaluationExperiment> {
    return this.transport.requestJSON("POST", resourcePath("/v2/evaluation-experiments", experimentId) + "/reconcile", undefined, signal ? { signal } : {});
  }

  listRunEvaluations(query: RunEvaluationListQuery, signal?: AbortSignal): Promise<RunEvaluation[]> {
    const path = withQuery("/v2/run-evaluations", {
      left_session_id: query.leftSessionId,
      left_turn_id: query.leftTurnId,
      right_session_id: query.rightSessionId,
      right_turn_id: query.rightTurnId,
      limit: query.limit,
    });
    return this.transport.requestJSON<{ evaluations: RunEvaluation[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.evaluations);
  }
}
