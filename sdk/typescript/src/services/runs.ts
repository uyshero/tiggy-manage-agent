import { streamEvents, type EventStreamOptions } from "../sse.js";
import type { Event, InterventionDecision, Run, RunResult, StartRunRequest } from "../types.js";
import type { Transport } from "../transport.js";
import { ServiceBase, resourcePath, withQuery } from "./base.js";
import type { InterventionsService } from "./interventions.js";
import { sessionPath } from "./sessions.js";

interface StartRunResponse {
  run: Run;
  events?: Event[];
  created: boolean;
}

export class RunsService extends ServiceBase {
  constructor(transport: Transport, private readonly interventions: InterventionsService) {
    super(transport);
  }

  async start(sessionId: string, request: StartRunRequest, signal?: AbortSignal): Promise<RunHandle> {
    const result = await this.transport.requestJSON<StartRunResponse>("POST", runsPath(sessionId), request, signal ? { signal } : {});
    return new RunHandle(this, this.interventions, result.run, Object.freeze([...(result.events ?? [])]), result.created);
  }

  get(sessionId: string, runId: string, signal?: AbortSignal): Promise<Run> {
    return this.transport.requestJSON("GET", runPath(sessionId, runId), undefined, signal ? { signal } : {});
  }

  list(sessionId: string, signal?: AbortSignal): Promise<Run[]> {
    return this.transport.requestJSON<{ runs: Run[] }>("GET", runsPath(sessionId), undefined, signal ? { signal } : {}).then((value) => value.runs);
  }

  cancel(sessionId: string, runId: string, signal?: AbortSignal): Promise<Run> {
    return this.transport.requestJSON("POST", `${runPath(sessionId, runId)}/cancel`, {}, signal ? { signal } : {});
  }

  listEvents(sessionId: string, runId: string, afterSeq = 0, signal?: AbortSignal): Promise<Event[]> {
    const path = withQuery(`${runPath(sessionId, runId)}/events`, { after_seq: afterSeq > 0 ? afterSeq : undefined });
    return this.transport.requestJSON<{ events: Event[] }>("GET", path, undefined, signal ? { signal } : {}).then((value) => value.events);
  }

  events(sessionId: string, runId: string, options: EventStreamOptions = {}): AsyncGenerator<Event> {
    return streamEvents(this.transport, `${runPath(sessionId, runId)}/events/stream`, options);
  }
}

export class RunHandle {
  constructor(
    private readonly service: RunsService,
    private readonly interventions: InterventionsService,
    public run: Run,
    public readonly initialEvents: readonly Event[] = [],
    public readonly created = true,
  ) {}

  events(options: EventStreamOptions = {}): AsyncGenerator<Event> {
    return this.service.events(this.run.session_id, this.run.id, options);
  }

  async cancel(signal?: AbortSignal): Promise<Run> {
    this.run = await this.service.cancel(this.run.session_id, this.run.id, signal);
    return this.run;
  }

  approve(callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.interventions.approve(this.run.session_id, this.run.id, callId, reason, signal);
  }

  reject(callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.interventions.reject(this.run.session_id, this.run.id, callId, reason, signal);
  }

  respond(callId: string, response: unknown, signal?: AbortSignal): Promise<InterventionDecision> {
    return this.interventions.respond(this.run.session_id, this.run.id, callId, response, signal);
  }

  skip(callId: string, reason = "", signal?: AbortSignal): Promise<InterventionDecision> {
    return this.interventions.skip(this.run.session_id, this.run.id, callId, reason, signal);
  }

  async wait(signal?: AbortSignal): Promise<RunResult> {
    let lastEvent: Event | undefined;
    let output: unknown;
    const afterSeq = Math.max((this.run.user_event_seq ?? 1) - 1, 0);
    for await (const event of this.events({ afterSeq, ...(signal === undefined ? {} : { signal }) })) {
      if (effectiveTurnId(event) !== this.run.id) continue;
      lastEvent = event;
      if (event.type === "agent.message") output = event.payload;
      if (event.type !== "session.status_idle") continue;
      this.run = await this.service.get(this.run.session_id, this.run.id, signal);
      if (isTerminalRunStatus(this.run.status)) {
        return {
          run: this.run,
          ...(lastEvent === undefined ? {} : { lastEvent }),
          ...(output === undefined ? {} : { output }),
        };
      }
    }
    throw new Error("Run event stream ended before the Run reached a terminal state");
  }
}

function runsPath(sessionId: string): string {
  return `${sessionPath(sessionId)}/runs`;
}

function runPath(sessionId: string, runId: string): string {
  return resourcePath(runsPath(sessionId), runId);
}

function effectiveTurnId(event: Event): string {
  if (event.turn_id) return event.turn_id;
  const payload = event.payload;
  if (payload && typeof payload === "object" && "turn_id" in payload && typeof payload.turn_id === "string") return payload.turn_id;
  return "";
}

function isTerminalRunStatus(status: string): boolean {
  return status === "completed" || status === "failed" || status === "interrupted";
}
