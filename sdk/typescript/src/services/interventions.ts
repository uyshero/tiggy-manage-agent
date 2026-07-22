import type { Intervention, InterventionDecision } from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";
import { sessionPath } from "./sessions.js";

export class InterventionsService extends ServiceBase {
  list(sessionId: string, status?: string, signal?: AbortSignal): Promise<Intervention[]> {
    const path = withQuery(`${sessionPath(sessionId)}/interventions`, { status });
    return this.transport.requestJSON<{ interventions: Intervention[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.interventions);
  }

  decide(sessionId: string, turnId: string, callId: string, decision: "approve" | "reject", reason = "", signal?: AbortSignal, response?: unknown): Promise<InterventionDecision> {
    const path = resourcePath(`${sessionPath(sessionId)}/interventions`, turnId, callId) + `/${decision}`;
    const body = response === undefined ? { reason } : { reason, response };
    return this.transport.requestJSON("POST", path, body, signal ? { signal } : {});
  }

  approve(sessionId: string, turnId: string, callId: string, reason = "", signal?: AbortSignal, response?: unknown): Promise<InterventionDecision> {
    return this.decide(sessionId, turnId, callId, "approve", reason, signal, response);
  }

  reject(sessionId: string, turnId: string, callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.decide(sessionId, turnId, callId, "reject", reason, signal);
  }

  respond(sessionId: string, turnId: string, callId: string, response: unknown, signal?: AbortSignal): Promise<InterventionDecision> {
    return this.resolve(sessionId, turnId, callId, "respond", { response }, signal);
  }

  skip(sessionId: string, turnId: string, callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.resolve(sessionId, turnId, callId, "skip", { reason }, signal);
  }

  cancel(sessionId: string, turnId: string, callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.resolve(sessionId, turnId, callId, "cancel", { reason }, signal);
  }

  private resolve(sessionId: string, turnId: string, callId: string, action: "respond" | "skip" | "cancel", body: unknown, signal?: AbortSignal): Promise<InterventionDecision> {
    const path = resourcePath(`${sessionPath(sessionId)}/interventions`, turnId, callId) + `/${action}`;
    return this.transport.requestJSON("POST", path, body, signal ? { signal } : {});
  }
}
