import type {
  Page,
  Trace,
  TraceCatalogEntry,
  TraceListQuery,
  TraceSpanCatalogEntry,
  TraceSpanDetail,
  TraceSpanListQuery,
} from "../types.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";
import { sessionPath } from "./sessions.js";

export class TracesService extends ServiceBase {
  list(query: TraceListQuery = {}, signal?: AbortSignal): Promise<Page<TraceCatalogEntry>> {
    const path = withQuery("/v2/traces", {
      workspace_id: query.workspaceId,
      session_id: query.sessionId,
      turn_id: query.turnId,
      session_status: query.sessionStatus,
      include_archived: query.includeArchived || undefined,
      limit: query.limit,
      cursor: query.cursor,
    });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  get(traceId: string, signal?: AbortSignal): Promise<Trace> {
    return this.transport.requestJSON("GET", tracePath(traceId), undefined, signal ? { signal } : {});
  }

  listSpans(query: TraceSpanListQuery = {}, signal?: AbortSignal): Promise<Page<TraceSpanCatalogEntry>> {
    const path = withQuery("/v2/spans", {
      workspace_id: query.workspaceId,
      trace_id: query.traceId,
      session_id: query.sessionId,
      turn_id: query.turnId,
      kind: query.kind,
      status: query.status,
      q: query.search,
      critical: query.critical,
      min_duration_ms: query.minDurationMs,
      max_duration_ms: query.maxDurationMs,
      min_self_duration_ms: query.minSelfDurationMs,
      include_archived: query.includeArchived || undefined,
      limit: query.limit,
      cursor: query.cursor,
    });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  getSpan(traceId: string, spanId: string, signal?: AbortSignal): Promise<TraceSpanDetail> {
    return this.transport.requestJSON("GET", resourcePath(`${tracePath(traceId)}/spans`, spanId), undefined, signal ? { signal } : {});
  }

  getSession(sessionId: string, turnId?: string, signal?: AbortSignal): Promise<Trace> {
    const path = withQuery(`${sessionPath(sessionId)}/trace`, { turn_id: turnId });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }

  exportSession(sessionId: string, format: "perfetto" | "otel" | "json", turnId?: string, signal?: AbortSignal): Promise<unknown> {
    const path = withQuery(`${sessionPath(sessionId)}/trace`, { turn_id: turnId, format });
    return this.transport.requestJSON("GET", path, undefined, signal ? { signal } : {});
  }
}

function tracePath(traceId: string): string {
  return resourcePath("/v2/traces", traceId);
}
