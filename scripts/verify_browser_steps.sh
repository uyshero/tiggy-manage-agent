#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT_DIR"

GOCACHE_DIR="${GOCACHE_DIR:-$ROOT_DIR/.gocache}"
TMA_BROWSER_SANDBOX_IMAGE="${TMA_BROWSER_SANDBOX_IMAGE:-tma-browser-sandbox:playwright}"
TMA_BROWSER_NODE_DIR="${TMA_BROWSER_NODE_DIR:-$ROOT_DIR/.tma-browser-node}"
PLAYWRIGHT_NPM_VERSION="${PLAYWRIGHT_NPM_VERSION:-1.54.1}"

if [ -d "$TMA_BROWSER_NODE_DIR/node_modules" ]; then
  export NODE_PATH="$TMA_BROWSER_NODE_DIR/node_modules${NODE_PATH:+:$NODE_PATH}"
fi

usage() {
  cat <<'EOF'
Usage:
  scripts/verify_browser_steps.sh list
  scripts/verify_browser_steps.sh <step>

Steps:
  install-local-playwright Install Playwright into .tma-browser-node for local takeover.
  0   preflight              Check local dependencies.
  1   unit                   Run browser-related Go tests.
  2   full-test              Run full Go test suite.
  3   build                  Build server, CLI, and worker binaries.
  4   db                     Start Postgres and apply migrations.
  5   browser-image          Build the Playwright browser sandbox image.
  6   cloud-browser          Verify browser.* in cloud_sandbox/headless mode.
  7   local-takeover         Verify local_system browser.takeover manually.
  8   cleanup-db             Stop docker compose services.

Examples:
  scripts/verify_browser_steps.sh 0
  scripts/verify_browser_steps.sh install-local-playwright
  scripts/verify_browser_steps.sh unit
  scripts/verify_browser_steps.sh local-takeover

Notes:
  - Step 7 opens a local headed Chromium window. Close the browser window to let the script finish.
  - Local takeover needs Playwright on the worker host. Use install-local-playwright if preflight reports it missing.
  - Override image with: TMA_BROWSER_SANDBOX_IMAGE=your-image scripts/verify_browser_steps.sh 6
EOF
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

preflight() {
  require_command go
  require_command docker
  require_command node
  require_command npm
  require_command python3

  if ! docker info >/dev/null 2>&1; then
    echo "docker is not running or not reachable" >&2
    exit 1
  fi

  if ! node -e "require('playwright'); process.exit(0)" >/dev/null 2>&1 &&
    ! node -e "require('playwright-core'); process.exit(0)" >/dev/null 2>&1; then
    echo "missing playwright/playwright-core for local takeover" >&2
    echo "run: npm install" >&2
    exit 1
  fi

  echo "preflight ok"
}

install_local_playwright() {
  require_command npm

  mkdir -p "$TMA_BROWSER_NODE_DIR"
  if [ ! -f "$TMA_BROWSER_NODE_DIR/package.json" ]; then
    npm --prefix "$TMA_BROWSER_NODE_DIR" init -y >/dev/null
  fi

  npm --prefix "$TMA_BROWSER_NODE_DIR" install --omit=dev "playwright@$PLAYWRIGHT_NPM_VERSION"
  npm --prefix "$TMA_BROWSER_NODE_DIR" exec -- playwright install chromium

  echo "local Playwright installed at $TMA_BROWSER_NODE_DIR"
  echo "NODE_PATH=$TMA_BROWSER_NODE_DIR/node_modules"
}

case "${1:-}" in
  list | --help | -h | "")
    usage
    ;;
  install-local-playwright)
    install_local_playwright
    ;;
  0 | preflight)
    preflight
    ;;
  1 | unit)
    GOCACHE="$GOCACHE_DIR" go test \
      ./internal/browser \
      ./internal/tools \
      ./internal/capability \
      ./internal/execution \
      ./internal/workruntime \
      ./internal/llm \
      ./internal/httpapi
    ;;
  2 | full-test)
    GOCACHE_DIR="$GOCACHE_DIR" make test
    ;;
  3 | build)
    GOCACHE_DIR="$GOCACHE_DIR" make build build-cli build-worker
    ;;
  4 | db)
    make db-up
    make migrate-up
    ;;
  5 | browser-image)
    TMA_BROWSER_SANDBOX_IMAGE="$TMA_BROWSER_SANDBOX_IMAGE" make build-browser-sandbox
    ;;
  6 | cloud-browser)
    TMA_BROWSER_SANDBOX_IMAGE="$TMA_BROWSER_SANDBOX_IMAGE" make verify-browser-tools
    ;;
  7 | local-takeover)
    make verify-browser-takeover-local
    ;;
  8 | cleanup-db)
    make db-down
    ;;
  *)
    echo "unknown step: $1" >&2
    usage >&2
    exit 2
    ;;
esac
