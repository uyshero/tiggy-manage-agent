import { createParser, type EventSourceMessage } from "eventsource-parser";
import { APIError, SSESchemaError } from "./errors.js";
import type { Event, LiveEvent } from "./types.js";
import type { Transport } from "./transport.js";

export interface EventStreamOptions {
  afterSeq?: number;
  signal?: AbortSignal;
  retryInitialMs?: number;
  retryMaxMs?: number;
}

export type LiveEventStreamOptions = Omit<EventStreamOptions, "afterSeq">;

export async function* streamLiveEvents(
  transport: Transport,
  path: string,
  options: LiveEventStreamOptions = {},
): AsyncGenerator<LiveEvent> {
  let retryDelay = options.retryInitialMs ?? 250;
  const retryMax = options.retryMaxMs ?? 10_000;

  while (!options.signal?.aborted) {
    try {
      const response = await transport.fetch(new URL(transport.url(path)), {
        method: "GET",
        headers: { Accept: "text/event-stream" },
        ...(options.signal === undefined ? {} : { signal: options.signal }),
      });
      if (!response.ok) {
        if (response.status < 500 || response.status > 599) throw await APIError.fromResponse(response);
        await response.body?.cancel();
        throw new RetryableSSEError(`SSE endpoint returned HTTP ${response.status}`);
      }
      if (!response.body) throw new SSESchemaError("SSE response has no body");

      retryDelay = options.retryInitialMs ?? 250;
      const messages: EventSourceMessage[] = [];
      let parserError: Error | undefined;
      const parser = createParser({
        onEvent: (message) => messages.push(message),
        onError: (error) => { parserError = new SSESchemaError(error.message); },
      });
      const decoder = new TextDecoder();
      const reader = response.body.getReader();
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          parser.feed(decoder.decode(value, { stream: true }));
          if (parserError) throw parserError;
          while (messages.length > 0) {
            const message = messages.shift();
            if (message) yield decodeLiveEvent(message.data);
          }
        }
      } finally {
        await reader.cancel().catch(() => undefined);
        reader.releaseLock();
      }
      parser.feed(decoder.decode());
      if (parserError) throw parserError;
      throw new RetryableSSEError("live SSE connection closed");
    } catch (error) {
      if (options.signal?.aborted) throw abortError();
      if (error instanceof SSESchemaError) throw error;
      if (error instanceof APIError && (error.status < 500 || error.status > 599)) throw error;
      await abortableDelay(retryDelay, options.signal);
      retryDelay = Math.min(retryDelay * 2, retryMax);
    }
  }
  throw abortError();
}

export async function* streamEvents(
  transport: Transport,
  path: string,
  options: EventStreamOptions = {},
): AsyncGenerator<Event> {
  let afterSeq = options.afterSeq ?? 0;
  let retryDelay = options.retryInitialMs ?? 250;
  const retryMax = options.retryMaxMs ?? 10_000;

  while (!options.signal?.aborted) {
    try {
      const url = new URL(transport.url(path));
      if (afterSeq > 0) url.searchParams.set("after_seq", String(afterSeq));
      const response = await transport.fetch(url, {
        method: "GET",
        headers: { Accept: "text/event-stream" },
        ...(options.signal === undefined ? {} : { signal: options.signal }),
      });
      if (!response.ok) {
        if (response.status < 500 || response.status > 599) throw await APIError.fromResponse(response);
        await response.body?.cancel();
        throw new RetryableSSEError(`SSE endpoint returned HTTP ${response.status}`);
      }
      if (!response.body) throw new SSESchemaError("SSE response has no body");

      retryDelay = options.retryInitialMs ?? 250;
      const messages: EventSourceMessage[] = [];
      let parserError: Error | undefined;
      const parser = createParser({
        onEvent: (message) => messages.push(message),
        onError: (error) => { parserError = new SSESchemaError(error.message); },
      });
      const decoder = new TextDecoder();
      const reader = response.body.getReader();
      try {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          parser.feed(decoder.decode(value, { stream: true }));
          if (parserError) throw parserError;
          while (messages.length > 0) {
            const message = messages.shift();
            if (!message) continue;
            const event = decodeEvent(message.data);
            if (event.seq <= afterSeq) continue;
            afterSeq = event.seq;
            yield event;
          }
        }
      } finally {
        await reader.cancel().catch(() => undefined);
        reader.releaseLock();
      }
      parser.feed(decoder.decode());
      if (parserError) throw parserError;
      throw new RetryableSSEError("SSE connection closed");
    } catch (error) {
      if (options.signal?.aborted) throw abortError();
      if (error instanceof SSESchemaError) throw error;
      if (error instanceof APIError && (error.status < 500 || error.status > 599)) throw error;
      await abortableDelay(retryDelay, options.signal);
      retryDelay = Math.min(retryDelay * 2, retryMax);
    }
  }
  throw abortError();
}

function decodeEvent(data: string): Event {
  let decoded: unknown;
  try {
    decoded = JSON.parse(data);
  } catch (error) {
    throw new SSESchemaError(`SSE event is not valid JSON: ${String(error)}`);
  }
  if (!decoded || typeof decoded !== "object") throw new SSESchemaError("SSE event must be an object");
  const event = decoded as Partial<Event>;
  if (!Number.isSafeInteger(event.seq) || typeof event.type !== "string" || typeof event.created_at !== "string") {
    throw new SSESchemaError("SSE event is missing seq, type, or created_at");
  }
  return event as Event;
}

function decodeLiveEvent(data: string): LiveEvent {
  let decoded: unknown;
  try {
    decoded = JSON.parse(data);
  } catch (error) {
    throw new SSESchemaError(`Live SSE event is not valid JSON: ${String(error)}`);
  }
  if (!decoded || typeof decoded !== "object") throw new SSESchemaError("Live SSE event must be an object");
  const event = decoded as Partial<LiveEvent>;
  if (!Number.isSafeInteger(event.stream_seq) || typeof event.session_id !== "string" || typeof event.turn_id !== "string" ||
      event.type !== "llm.text" || event.operation !== "append" || event.content_format !== "markdown" ||
      typeof event.text !== "string" || typeof event.created_at !== "string") {
    throw new SSESchemaError("Live SSE event is missing required transient stream fields");
  }
  return event as LiveEvent;
}

class RetryableSSEError extends Error {}

async function abortableDelay(delay: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted) throw abortError();
  await new Promise<void>((resolve, reject) => {
    const onAbort = () => {
      clearTimeout(timeout);
      reject(abortError());
    };
    const timeout = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, delay);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function abortError(): DOMException {
  return new DOMException("The operation was aborted", "AbortError");
}
