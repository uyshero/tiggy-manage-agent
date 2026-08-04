import type { CapabilityDiscoveryResponse } from "../types.js";
import { ServiceBase } from "./base.js";

export class CapabilitiesService extends ServiceBase {
  list(signal?: AbortSignal): Promise<CapabilityDiscoveryResponse> {
    return this.transport.requestJSON("GET", "/v2/capabilities", undefined, signal ? { signal } : {});
  }
}
