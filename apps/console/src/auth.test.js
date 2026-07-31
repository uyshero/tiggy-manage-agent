import assert from "node:assert/strict";
import test from "node:test";
import { createAuthenticatedFetch } from "./auth.js";

test("authenticated fetch refreshes and retries a rejected request", async () => {
  const calls = [];
  const responses = [new Response(null, { status: 401 }), new Response(null, { status: 204 }), new Response("ok")];
  const fetch = createAuthenticatedFetch({
    baseURL: "https://tma.example",
    fetch: async (input, init) => {
      const request = input instanceof Request ? input : new Request(input, init);
      calls.push(`${request.method} ${new URL(request.url).pathname}`);
      return responses.shift();
    },
    location: { pathname: "/console", search: "", hash: "", assign: () => assert.fail("must not redirect") }
  });

  const response = await fetch("https://tma.example/v2/administration/context");

  assert.equal(await response.text(), "ok");
  assert.deepEqual(calls, ["GET /v2/administration/context", "POST /auth/refresh", "GET /v2/administration/context"]);
});

test("authenticated fetch starts OIDC login when refresh is unavailable", async () => {
  let redirectURL = "";
  const fetch = createAuthenticatedFetch({
    baseURL: "https://tma.example",
    fetch: async () => new Response(null, { status: 401 }),
    location: {
      pathname: "/console",
      search: "?view=members",
      hash: "#selected",
      assign: (value) => { redirectURL = value; }
    }
  });

  const response = await fetch("https://tma.example/v2/administration/context");

  assert.equal(response.status, 401);
  assert.equal(redirectURL, "https://tma.example/auth/login?return_to=%2Fconsole%3Fview%3Dmembers%23selected");
});
