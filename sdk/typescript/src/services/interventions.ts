import type { Intervention, InterventionDecision } from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";
import { sessionPath } from "./sessions.js";

export class InterventionsService extends ServiceBase {
  list(sessionId: string, status?: string, signal?: AbortSignal): Promise<Intervention[]> {
    const path = withQuery(`${sessionPath(sessionId)}/interventions`, { status });
    return this.transport.requestJSON<{ interventions: Intervention[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.interventions);
  }

  decide(sessionId: string, turnId: string, callId: string, decision: "approve" | "reject", reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    const path = resourcePath(`${sessionPath(sessionId)}/interventions`, turnId, callId) + `/${decision}`;
    return this.transport.requestJSON("POST", path, { reason }, signal ? { signal } : {});
  }

  approve(sessionId: string, turnId: string, callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.decide(sessionId, turnId, callId, "approve", reason, signal);
  }

  reject(sessionId: string, turnId: string, callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.decide(sessionId, turnId, callId, "reject", reason, signal);
  }
}
