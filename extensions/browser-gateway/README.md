# TMA Browser Extension

The Browser Extension replaces the former server-built-in `browser.*` runtime. It keeps browser lifecycle and Chromium dependencies outside `tma-server`.

## Components

- `extensions/browser-gateway`: long-running Playwright/Chromium session service.
- `extensions/browser-tool-plugin/browser-plugin.py`: Worker Process Plugin that publishes `browser.*`.
- `apps/workbench/src/plugins/enterpriseBrowser`: operator browser UI.
- `deploy/kubernetes/base/browser-extension.yaml`: Gateway and Browser Worker deployment.

The Gateway starts one persistent Chromium process and one profile directory per browser session. Commands for a session are serialized; separate sessions run concurrently. The v0.1 implementation streams authenticated JPEG frames through the same-origin extension API. Its transport boundary is intentionally separate from browser lifecycle so WebRTC can replace frame polling without changing TMA Server or the Agent tool contract.

## Local development

Install and start the Gateway:

```bash
npm --prefix extensions/browser-gateway install
TMA_BROWSER_AUTH_MODE=disabled \
TMA_BROWSER_GATEWAY_SERVICE_SECRET=development-only-secret \
npm --prefix extensions/browser-gateway start
```

Start Workbench with its extension proxy pointed at the Gateway:

```bash
TMA_DEV_BROWSER_GATEWAY_URL=http://127.0.0.1:8090 \
npm --prefix apps/workbench run dev
```

Load the Agent tool plugin on a Worker:

```bash
TMA_BROWSER_GATEWAY_URL=http://127.0.0.1:8090/v2/extensions/browser \
TMA_BROWSER_GATEWAY_SERVICE_SECRET=development-only-secret \
TMA_WORKER_PLUGINS=extensions/browser-tool-plugin/browser-plugin.py \
bin/tma-worker --base-url http://127.0.0.1:8080 --workspace wksp_default
```

Configure the Agent with:

```json
{"tools":["browser"],"runtime":"local_system"}
```

## Production routing

The Workbench plugin only uses scoped `/v2` requests. Route the extension prefix before the general TMA route:

```nginx
location ^~ /v2/extensions/browser/ {
    proxy_pass http://tma-browser-gateway:8090;
}

location /v2/ {
    proxy_pass http://tma-server:8080;
}
```

The Gateway authenticates user requests by forwarding their TMA credentials to `/v2/auth/me` and validates access to a bound TMA Session through `/v2/sessions/{id}`. Browser Worker requests use an HMAC signature bound to method, path, body, workspace and TMA session. Use different high-entropy secrets per environment.

Required production settings:

```env
TMA_SERVER_BASE_URL=http://tma-server:8080
TMA_BROWSER_AUTH_MODE=tma
TMA_BROWSER_GATEWAY_SERVICE_SECRET=replace-with-openssl-rand-hex-32
TMA_BROWSER_MAX_SESSIONS_PER_WORKSPACE=4
TMA_BROWSER_IDLE_TTL_SECONDS=300
TMA_BROWSER_TRUSTED_ORIGINS=https://tma.example.com
TMA_BROWSER_ALLOWED_WORKSPACE_ID=wksp_customer_a
```

Do not expose Chromium CDP ports. Do not put the Gateway behind an unauthenticated public route. Page frames can contain sensitive tenant data and must not be logged or cached.

## Scaling boundary

Gateway session state and Chromium processes are node-local. The base Kubernetes deployment deliberately uses one Gateway replica and pins it to one Workspace. Deploy separate releases per Workspace for a strong tenant compute boundary. Production horizontal scaling inside one Workspace must shard browser sessions or add a sticky router that always sends a browser session to its assigned Gateway. A shared load balancer without affinity will return intermittent 404 responses and is unsupported.

For stronger compute isolation, run the Gateway manager as a control plane that launches one sandbox container per browser session. The API, HMAC plugin contract and Workbench plugin remain unchanged.

## Verification

```bash
make verify-browser-tools
npm --prefix extensions/browser-gateway test
python3 -m unittest extensions/browser-tool-plugin/test_browser_plugin.py
```
