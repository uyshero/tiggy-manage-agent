import type { CreateEnvironmentRequest, Environment } from "../types.js";
import { ServiceBase, resourcePath } from "./base.js";

export class EnvironmentsService extends ServiceBase {
  list(signal?: AbortSignal): Promise<Environment[]> {
    return this.transport.requestJSON<{ environments: Environment[] }>("GET", "/v2/environments", undefined, signal ? { signal } : {}).then((value) => value.environments);
  }

  get(environmentId: string, signal?: AbortSignal): Promise<Environment> {
    return this.transport.requestJSON("GET", resourcePath("/v2/environments", environmentId), undefined, signal ? { signal } : {});
  }

  create(request: CreateEnvironmentRequest, signal?: AbortSignal): Promise<Environment> {
    return this.transport.requestJSON("POST", "/v2/environments", request, signal ? { signal } : {});
  }
}
