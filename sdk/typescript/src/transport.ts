import { APIError } from "./errors.js";

export type TokenSource = (signal?: AbortSignal) => Promise<string | undefined>;

export interface TransportOptions {
  token?: string;
  tokenSource?: TokenSource;
  fetch?: typeof globalThis.fetch;
  headers?: HeadersInit;
}

export interface JSONRequestOptions {
  signal?: AbortSignal;
  headers?: HeadersInit;
}

export class Transport {
  readonly baseURL: string;
  private readonly staticToken: string;
  private readonly tokenSource: TokenSource | undefined;
  private readonly fetchImpl: typeof globalThis.fetch;
  private readonly defaultHeaders: Headers;

  constructor(baseURL: string, options: TransportOptions = {}) {
    const parsed = new URL(baseURL.trim());
    if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
      throw new TypeError("TMA baseURL must be an absolute HTTP(S) URL");
    }
    this.baseURL = baseURL.trim().replace(/\/+$/, "");
    this.staticToken = options.token?.trim() ?? "";
    this.tokenSource = options.tokenSource;
    this.fetchImpl = options.fetch ?? globalThis.fetch;
    if (!this.fetchImpl) throw new TypeError("A Fetch API implementation is required");
    this.defaultHeaders = new Headers(options.headers);
  }

  url(path: string): string {
    return `${this.baseURL}${path.startsWith("/") ? path : `/${path}`}`;
  }

  readonly fetch = async (input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> => {
    const headers = new Headers(input instanceof Request ? input.headers : undefined);
    for (const [key, value] of this.defaultHeaders) {
      if (!headers.has(key)) headers.set(key, value);
    }
    for (const [key, value] of new Headers(init.headers)) headers.set(key, value);
    const token = this.staticToken || await this.tokenSource?.(init.signal ?? undefined) || "";
    if (token) headers.set("Authorization", `Bearer ${token}`);
    return this.fetchImpl(input, { ...init, headers });
  };

  async requestJSON<T>(method: string, path: string, body?: unknown, options: JSONRequestOptions = {}): Promise<T> {
    const headers = new Headers(options.headers);
    headers.set("Accept", "application/json");
    let encoded: BodyInit | undefined;
    if (body !== undefined) {
      headers.set("Content-Type", "application/json");
      encoded = JSON.stringify(body);
    }
    const response = await this.fetch(this.url(path), {
      method,
      headers,
      ...(encoded === undefined ? {} : { body: encoded }),
      ...(options.signal === undefined ? {} : { signal: options.signal }),
    });
    if (!response.ok) throw await APIError.fromResponse(response);
    if (response.status === 204) return undefined as T;
    return await response.json() as T;
  }

  async request(method: string, path: string, init: RequestInit = {}): Promise<Response> {
    const response = await this.fetch(this.url(path), { ...init, method });
    if (!response.ok) throw await APIError.fromResponse(response);
    return response;
  }
}
