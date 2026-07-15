import type { CreateEnvironmentRequest, Environment } from "../types.js";
import { ServiceBase } from "./base.js";

export class EnvironmentsService extends ServiceBase {
  create(request: CreateEnvironmentRequest, signal?: AbortSignal): Promise<Environment> {
    return this.transport.requestJSON("POST", "/v2/environments", request, signal ? { signal } : {});
  }
}
