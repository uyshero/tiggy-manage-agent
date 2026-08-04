import type { ApplicationResourceQuery, CreateEnvironmentRequest, Environment } from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export class EnvironmentsService extends ServiceBase {
  list(signal?: AbortSignal): Promise<Environment[]> {
    return this.transport.requestJSON<{ environments: Environment[] }>("GET", "/v2/environments", undefined, signal ? { signal } : {}).then((value) => value.environments);
  }

  listByApplication(query: ApplicationResourceQuery = {}, signal?: AbortSignal): Promise<Environment[]> {
    const path = withQuery("/v2/environments", { app_id: query.appId, external_ref: query.externalRef });
    return this.transport.requestJSON<{ environments: Environment[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.environments);
  }

  get(environmentId: string, signal?: AbortSignal): Promise<Environment> {
    return this.transport.requestJSON("GET", resourcePath("/v2/environments", environmentId), undefined, signal ? { signal } : {});
  }

  create(request: CreateEnvironmentRequest, signal?: AbortSignal): Promise<Environment> {
    return this.transport.requestJSON("POST", "/v2/environments", request, signal ? { signal } : {});
  }
}
