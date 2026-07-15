import type { Transport } from "../transport.js";

export class ServiceBase {
  constructor(protected readonly transport: Transport) {}
}

export function resourcePath(base: string, ...ids: Array<string | number>): string {
  return [base.replace(/\/$/, ""), ...ids.map((id) => encodeURIComponent(String(id)))].join("/");
}

export function withQuery(path: string, values: Record<string, string | number | boolean | undefined>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === "") continue;
    query.set(key, String(value));
  }
  const encoded = query.toString();
  return encoded ? `${path}?${encoded}` : path;
}
