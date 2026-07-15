import createClient, { type Client } from "openapi-fetch";
import type { paths, components, operations } from "./internal/generated/schema.js";

export type { paths, components, operations } from "./internal/generated/schema.js";

export function createLowLevelClient(baseUrl: string, fetchImpl?: typeof globalThis.fetch): Client<paths> {
  return createClient<paths>({
    baseUrl: baseUrl.replace(/\/+$/, ""),
    ...(fetchImpl === undefined ? {} : { fetch: fetchImpl }),
  });
}
