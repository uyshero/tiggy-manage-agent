# TMA Console

TMA Console is the Platform administration UI for Workspace membership, Workspace roles, and global Platform administrators. It consumes Platform only through `@tma/core-sdk`.

## Development

```bash
TMA_DEV_API_BASE_URL=http://127.0.0.1:8080 npm run dev
```

The Vite development server proxies `/auth` and `/v2` to the Platform server.

## Standalone Build

```bash
npm run build
```

The deployable static site is written to `dist/`. Deploy it behind the same origin as Platform when possible. For a separate origin, set `VITE_TMA_API_BASE_URL` at build time and configure the Platform gateway for that origin.

Set `TMA_CONSOLE_BASE_PATH` when the static site is served below a path instead of `/`.

## Embedded Compatibility Build

```bash
npm run build:embedded
```

This writes the bundle to `internal/httpapi/console` for the compatibility route served by `tma-server`. It is not the standalone Console release artifact.

## Container Image

Build the standalone image from the repository root so the Console can compile against the Core SDK:

```bash
docker build -f apps/console/Dockerfile -t tma-console:local .
docker run --rm -p 8081:8080 tma-console:local
```

The image runs Nginx as a non-root user and exposes `/healthz`. In production, route Console and Platform through the same gateway origin. Use the `VITE_TMA_API_BASE_URL` and `TMA_CONSOLE_BASE_PATH` build arguments when a different topology is required.
