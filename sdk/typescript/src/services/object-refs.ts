import type { CreateObjectRefRequest, ObjectRef } from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export class ObjectRefsService extends ServiceBase {
  create(request: CreateObjectRefRequest, signal?: AbortSignal): Promise<ObjectRef> {
    return this.transport.requestJSON("POST", "/v2/object-refs", request, signal ? { signal } : {});
  }

  get(objectRefId: string, signal?: AbortSignal): Promise<ObjectRef> {
    return this.transport.requestJSON("GET", objectRefPath(objectRefId), undefined, signal ? { signal } : {});
  }

  async delete(objectRefId: string, signal?: AbortSignal): Promise<void> {
    await this.transport.request("DELETE", objectRefPath(objectRefId), signal ? { signal } : {});
  }

  download(objectRefId: string, sessionId?: string, signal?: AbortSignal): Promise<Response> {
    const path = withQuery(`${objectRefPath(objectRefId)}/download`, { session_id: sessionId });
    return this.transport.request("GET", path, signal ? { signal } : {});
  }
}

function objectRefPath(objectRefId: string): string {
  return resourcePath("/v2/object-refs", objectRefId);
}
