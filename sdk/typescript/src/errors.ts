export interface APIErrorBody {
  code: string;
  message: string;
  request_id?: string;
  retryable?: boolean;
  details?: Record<string, unknown>;
}

export class APIError extends Error {
  readonly status: number;
  readonly code: string;
  readonly requestId: string;
  readonly retryable: boolean;
  readonly details?: Record<string, unknown>;

  constructor(status: number, body: APIErrorBody) {
    super(body.message || `TMA request failed with HTTP ${status}`);
    this.name = "APIError";
    this.status = status;
    this.code = body.code || defaultErrorCode(status);
    this.requestId = body.request_id ?? "";
    this.retryable = body.retryable ?? defaultRetryable(status);
    if (body.details !== undefined) this.details = body.details;
  }

  static async fromResponse(response: Response): Promise<APIError> {
    let body: APIErrorBody | undefined;
    try {
      const decoded = (await response.json()) as { error?: APIErrorBody } | APIErrorBody;
      body = "error" in decoded && decoded.error ? decoded.error : (decoded as APIErrorBody);
    } catch {
      body = undefined;
    }
    return new APIError(response.status, body ?? {
      code: defaultErrorCode(response.status),
      message: response.statusText || `TMA request failed with HTTP ${response.status}`,
      request_id: response.headers.get("x-request-id") ?? "",
      retryable: defaultRetryable(response.status),
    });
  }
}

export class SSESchemaError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SSESchemaError";
  }
}

function defaultErrorCode(status: number): string {
  if (status === 400 || status === 422) return "invalid_request";
  if (status === 401) return "unauthorized";
  if (status === 403) return "forbidden";
  if (status === 404) return "not_found";
  if (status === 409) return "conflict";
  if (status === 412) return "revision_conflict";
  if (status === 413) return "payload_too_large";
  if (status === 415) return "unsupported_media_type";
  if (status === 429) return "rate_limited";
  if (status === 502) return "upstream_error";
  if (status === 503) return "service_unavailable";
  if (status === 504) return "upstream_timeout";
  return "internal_error";
}

function defaultRetryable(status: number): boolean {
  return status === 429 || status === 502 || status === 503 || status === 504;
}
