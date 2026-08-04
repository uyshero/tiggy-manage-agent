import type { ApplicationManifestPublishResult, PublishApplicationManifestRequest } from "../types.js";
import { ServiceBase } from "./base.js";

export class ApplicationManifestsService extends ServiceBase {
  publish(request: PublishApplicationManifestRequest, signal?: AbortSignal): Promise<ApplicationManifestPublishResult> {
    return this.transport.requestJSON("POST", "/v2/application-manifests/publish", request, signal ? { signal } : {});
  }
}
