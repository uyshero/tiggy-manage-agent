import type {
  CancelWorkerWorkRequest,
  EnqueueWorkerWorkRequest,
  ReapExpiredWorkersRequest,
  ReapExpiredWorkersResult,
  ReapExpiredWorkerWorkRequest,
  ReapExpiredWorkerWorkResult,
  RequeueWorkerWorkRequest,
  Worker,
  WorkerDiagnoseRequest,
  WorkerDiagnoseResponse,
  WorkerListQuery,
  WorkerWork,
  WorkerWorkDiagnosis,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export class WorkersService extends ServiceBase {
  list(query: WorkerListQuery = {}, signal?: AbortSignal): Promise<Worker[]> {
    const path = withQuery("/v2/workers", { workspace_id: query.workspaceId, status: query.status });
    return this.transport.requestJSON<{ workers: Worker[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.workers);
  }

  get(workerId: string, signal?: AbortSignal): Promise<Worker> {
    return this.transport.requestJSON("GET", workerPath(workerId), undefined, signal ? { signal } : {});
  }

  archive(workerId: string, signal?: AbortSignal): Promise<Worker> {
    return this.transport.requestJSON("POST", `${workerPath(workerId)}/archive`, {}, signal ? { signal } : {});
  }

  reapExpired(request: ReapExpiredWorkersRequest = {}, signal?: AbortSignal): Promise<ReapExpiredWorkersResult> {
    return this.transport.requestJSON("POST", "/v2/workers/reap-expired", request, signal ? { signal } : {});
  }

  diagnose(request: WorkerDiagnoseRequest, signal?: AbortSignal): Promise<WorkerDiagnoseResponse> {
    return this.transport.requestJSON("POST", "/v2/workers/diagnose", request, signal ? { signal } : {});
  }
}

export class WorkerWorkService extends ServiceBase {
  enqueue(request: EnqueueWorkerWorkRequest, signal?: AbortSignal): Promise<WorkerWork> {
    return this.transport.requestJSON("POST", "/v2/worker-work", request, signal ? { signal } : {});
  }

  get(workId: string, signal?: AbortSignal): Promise<WorkerWork> {
    return this.transport.requestJSON("GET", workPath(workId), undefined, signal ? { signal } : {});
  }

  reapExpired(request: ReapExpiredWorkerWorkRequest = {}, signal?: AbortSignal): Promise<ReapExpiredWorkerWorkResult> {
    return this.transport.requestJSON("POST", "/v2/worker-work/reap-expired", request, signal ? { signal } : {});
  }

  cancel(workId: string, request: CancelWorkerWorkRequest = {}, signal?: AbortSignal): Promise<WorkerWork> {
    return this.transport.requestJSON("POST", `${workPath(workId)}/cancel`, request, signal ? { signal } : {});
  }

  requeue(workId: string, request: RequeueWorkerWorkRequest = {}, signal?: AbortSignal): Promise<WorkerWork> {
    return this.transport.requestJSON("POST", `${workPath(workId)}/requeue`, request, signal ? { signal } : {});
  }

  diagnose(workId: string, signal?: AbortSignal): Promise<WorkerWorkDiagnosis> {
    return this.transport.requestJSON("GET", `${workPath(workId)}/diagnose`, undefined, signal ? { signal } : {});
  }
}

function workerPath(workerId: string): string { return resourcePath("/v2/workers", workerId); }
function workPath(workId: string): string { return resourcePath("/v2/worker-work", workId); }
