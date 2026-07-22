import { streamEvents, streamLiveEvents, type EventStreamOptions, type LiveEventStreamOptions } from "../sse.js";
import type {
  AgentRuntimeConfig,
  AppendEventsRequest,
  AppendEventsResult,
  CreateSessionRequest,
  Event,
  LiveEvent,
  RerunSessionRequest,
  RerunSessionResponse,
  Session,
  SessionComparison,
  SessionRuntimeCapabilities,
  SessionSummary,
  SessionTaskPlan,
  SessionUsage,
  UpdateSessionMetadataRequest,
  UpdateSessionRuntimeSettingsRequest,
  UpgradeSessionConfigRequest,
  UpgradeSessionConfigResult,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";

export interface SessionListQuery {
  workspaceId?: string;
  ownerId?: string;
  status?: string;
  includeArchived?: boolean;
  limit?: number;
}

export class SessionsService extends ServiceBase {
  create(request: CreateSessionRequest, signal?: AbortSignal): Promise<Session> {
    return this.transport.requestJSON("POST", "/v2/sessions", request, signal ? { signal } : {});
  }

  list(query: SessionListQuery = {}, signal?: AbortSignal): Promise<Session[]> {
    const path = withQuery("/v2/sessions", {
      workspace_id: query.workspaceId,
      owner_id: query.ownerId,
      status: query.status,
      include_archived: query.includeArchived || undefined,
      limit: query.limit,
    });
    return this.transport.requestJSON<{ sessions: Session[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.sessions);
  }

  get(sessionId: string, signal?: AbortSignal): Promise<Session> {
    return this.transport.requestJSON("GET", sessionPath(sessionId), undefined, signal ? { signal } : {});
  }

  updateMetadata(sessionId: string, request: UpdateSessionMetadataRequest, signal?: AbortSignal): Promise<Session> {
    return this.transport.requestJSON("PATCH", sessionPath(sessionId), request, signal ? { signal } : {});
  }

  delete(sessionId: string, signal?: AbortSignal): Promise<void> {
    return this.transport.requestJSON("DELETE", sessionPath(sessionId), undefined, signal ? { signal } : {});
  }

  archive(sessionId: string, signal?: AbortSignal): Promise<Session> {
    return this.transport.requestJSON("POST", `${sessionPath(sessionId)}/archive`, {}, signal ? { signal } : {});
  }

  restore(sessionId: string, signal?: AbortSignal): Promise<Session> {
    return this.transport.requestJSON("POST", `${sessionPath(sessionId)}/restore`, {}, signal ? { signal } : {});
  }

  rerun(sessionId: string, request: RerunSessionRequest = {}, signal?: AbortSignal): Promise<RerunSessionResponse> {
    return this.transport.requestJSON("POST", `${sessionPath(sessionId)}/rerun`, request, signal ? { signal } : {});
  }

  upgradeConfig(sessionId: string, request: UpgradeSessionConfigRequest, signal?: AbortSignal): Promise<UpgradeSessionConfigResult> {
    return this.transport.requestJSON("POST", `${sessionPath(sessionId)}/config/upgrade`, request, signal ? { signal } : {});
  }

  compare(leftSessionId: string, rightSessionId: string, signal?: AbortSignal): Promise<SessionComparison> {
    const path = withQuery("/v2/session-comparisons", { left_session_id: leftSessionId, right_session_id: rightSessionId });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  updateRuntimeSettings(sessionId: string, expectedRevision: number, request: UpdateSessionRuntimeSettingsRequest, signal?: AbortSignal): Promise<Session> {
    return this.transport.requestJSON("PATCH", `${sessionPath(sessionId)}/runtime-settings`, request, {
      headers: { "If-Match": `"${expectedRevision}"` },
      ...(signal === undefined ? {} : { signal }),
    });
  }

  runtimeConfig(sessionId: string, signal?: AbortSignal): Promise<AgentRuntimeConfig> {
    return this.transport.requestJSON("GET", `${sessionPath(sessionId)}/runtime-config`, undefined, signal ? { signal } : {});
  }

  runtimeCapabilities(sessionId: string, signal?: AbortSignal): Promise<SessionRuntimeCapabilities> {
    return this.transport.requestJSON("GET", `${sessionPath(sessionId)}/runtime-capabilities`, undefined, signal ? { signal } : {});
  }

  summary(sessionId: string, signal?: AbortSignal): Promise<SessionSummary> {
    return this.transport.requestJSON("GET", `${sessionPath(sessionId)}/summary`, undefined, signal ? { signal } : {});
  }

  taskPlan(sessionId: string, signal?: AbortSignal): Promise<SessionTaskPlan> {
    return this.transport.requestJSON<{ plan: SessionTaskPlan }>("GET", `${sessionPath(sessionId)}/task-plan`, undefined, signal ? { signal } : {}).then((value) => value.plan);
  }

  taskPlans(sessionId: string, signal?: AbortSignal): Promise<SessionTaskPlan[]> {
    return this.transport.requestJSON<{ plans: SessionTaskPlan[] }>("GET", `${sessionPath(sessionId)}/task-plans`, undefined, signal ? { signal } : {}).then((value) => value.plans);
  }

  usage(sessionId: string, signal?: AbortSignal): Promise<SessionUsage> {
    return this.transport.requestJSON("GET", `${sessionPath(sessionId)}/usage`, undefined, signal ? { signal } : {});
  }

  listEvents(sessionId: string, afterSeq = 0, signal?: AbortSignal): Promise<Event[]> {
    const path = withQuery(`${sessionPath(sessionId)}/events`, { after_seq: afterSeq > 0 ? afterSeq : undefined });
    return this.transport.requestJSON<{ events: Event[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.events);
  }

  appendEvents(sessionId: string, request: AppendEventsRequest, signal?: AbortSignal): Promise<AppendEventsResult> {
    return this.transport.requestJSON("POST", `${sessionPath(sessionId)}/events`, request, signal ? { signal } : {});
  }

  events(sessionId: string, options: EventStreamOptions = {}): AsyncGenerator<Event> {
    return streamEvents(this.transport, `${sessionPath(sessionId)}/events/stream`, options);
  }

  liveEvents(sessionId: string, options: LiveEventStreamOptions = {}): AsyncGenerator<LiveEvent> {
    return streamLiveEvents(this.transport, `${sessionPath(sessionId)}/live/stream`, options);
  }
}

export function sessionPath(sessionId: string): string {
  return resourcePath("/v2/sessions", sessionId);
}
