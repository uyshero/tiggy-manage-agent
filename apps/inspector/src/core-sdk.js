import { TMAClient } from "@tma/core-sdk";

const baseURL = globalThis.location?.origin || "http://localhost";

export const coreSDK = new TMAClient(baseURL, {
  fetch: (input, init) => globalThis.fetch(input, init)
});
