#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

SEARXNG_URL="${TMA_WEB_SEARXNG_BASE_URL:-http://localhost:8180}"
QUERY="${1:-测试}"
TIMEOUT_SECONDS="${TMA_VERIFY_WEB_SEARCH_TIMEOUT:-30}"
RETRIES="${TMA_VERIFY_WEB_SEARCH_RETRIES:-2}"

if [ ! -x "$ROOT_DIR/bin/tma" ]; then
  echo "Building bin/tma"
  go build -o "$ROOT_DIR/bin/tma" ./cmd/tma
fi

run_search() {
  label="$1"
  shift
  output_file="$(mktemp)"
  error_file="$(mktemp)"
  trap 'rm -f "$output_file" "$error_file"' EXIT INT TERM

  echo
  echo "== $label =="
  attempt=1
  while :; do
    if "$ROOT_DIR/bin/tma" web search "$@" >"$output_file" 2>"$error_file"; then
      break
    fi
    if [ "$attempt" -ge "$RETRIES" ]; then
      cat "$error_file" >&2
      rm -f "$error_file"
      rm -f "$output_file"
      trap - EXIT INT TERM
      return 1
    fi
    echo "search attempt ${attempt}/${RETRIES} failed, retrying..." >&2
    attempt=$((attempt + 1))
    sleep 1
  done
  cat "$output_file"

  python3 - "$label" "$output_file" <<'PY'
import json
import sys

label = sys.argv[1]
path = sys.argv[2]

with open(path, "r", encoding="utf-8") as handle:
    payload = json.load(handle)

error_detail = (payload.get("error_detail") or "").strip()
results = payload.get("results") or []
provider = (payload.get("provider") or "").strip()

if error_detail:
    print(f"{label} failed: {error_detail}", file=sys.stderr)
    raise SystemExit(1)

if not provider:
    print(f"{label} failed: missing provider in response", file=sys.stderr)
    raise SystemExit(1)

if not results:
    print(f"{label} returned 0 results", file=sys.stderr)
    raise SystemExit(1)

print(f"{label} ok: provider={provider}, results={len(results)}")
PY

  rm -f "$output_file"
  rm -f "$error_file"
  trap - EXIT INT TERM
}

echo "== web doctor =="
"$ROOT_DIR/bin/tma" web doctor --searxng-url "$SEARXNG_URL" --query "$QUERY" --timeout "$TIMEOUT_SECONDS"

run_search "plain search" \
  --query "$QUERY" \
  --limit 5 \
  --timeout "$TIMEOUT_SECONDS"

run_search "time-range m1 alias" \
  --query "$QUERY" \
  --time-range m1 \
  --limit 5 \
  --timeout "$TIMEOUT_SECONDS"

run_search "native day time-range" \
  --query "$QUERY" \
  --time-range day \
  --limit 5 \
  --timeout "$TIMEOUT_SECONDS"

echo
echo "web search local verification passed"
