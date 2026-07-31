#!/bin/sh
set -eu

# Biography is an independent project. Keep these legacy source and migration
# paths out of Platform so the two projects cannot silently diverge.
legacy_paths='apps/biography-mobile
cmd/tma-biography-agent-bootstrap
cmd/tma-biography-voice-gateway
internal/biographyvoice
skills/conduct-biography-interview
skills/structure-biography-chapters
skills/verify-biography-facts
skills/embed.go
docs/biography-voice-gateway.md
docs/biography-voice-production.md
scripts/keycloak_biography_client.sh
sql/migrations/000103_biography_voice_persistence.sql
sql/migrations/000104_biography_recording_segments.sql'

printf '%s\n' "$legacy_paths" | while IFS= read -r legacy_path; do
  if [ -e "$legacy_path" ]; then
    echo "Biography boundary violation: legacy path still exists: $legacy_path" >&2
    exit 1
  fi
done

boundary_allowlist="scripts/platform-legacy-boundary.allowlist"
if [ ! -f "$boundary_allowlist" ]; then
  echo "Platform boundary violation: compatibility debt allowlist is missing" >&2
  exit 1
fi

assert_legacy_allowlisted() {
  kind="$1"
  value="$2"
  if ! rg -Fqx "$kind $value" "$boundary_allowlist"; then
    echo "Platform boundary violation: new application compatibility $kind is not allowed: $value" >&2
    exit 1
  fi
}

legacy_dependencies="$(go list -deps ./cmd/tma-server | rg '^tiggy-manage-agent/internal/(workbenchprojects|workbenchruntime|biography[^/]*|knowledge[^/]*|r_survival[^/]*)' || true)"
printf '%s\n' "$legacy_dependencies" | while IFS= read -r dependency; do
  [ -z "$dependency" ] || assert_legacy_allowlisted dependency "$dependency"
done

model_runtime_control_plane_dependencies="$(go list -deps ./cmd/tma-model-runtime | rg '^tiggy-manage-agent/internal/(httpapi|managedagents|objectstore|runner|execution|tools|workbenchprojects|workbenchruntime)' || true)"
if [ -n "$model_runtime_control_plane_dependencies" ]; then
  echo "Model Runtime boundary violation: data plane imports control-plane packages:" >&2
  printf '%s\n' "$model_runtime_control_plane_dependencies" >&2
  exit 1
fi

if rg -n 'proxyDoubao|buildDoubao|parseDoubao|doubaoFrame' internal/httpapi --glob '*.go' --glob '!*_test.go'; then
  echo "Model Runtime boundary violation: speech provider protocol must not live in the HTTP control plane" >&2
  exit 1
fi

legacy_routes="$(rg -o --no-filename '"[A-Z]+ /v(1|2)/[^"]+"' internal/httpapi/server.go internal/httpapi/server_v2.go \
  | tr -d '"' \
  | rg '^[A-Z]+ /v(1|2)/(knowledge|public/knowledge-shares|workbench-projects|r-survival-projects|biography)' \
  | LC_ALL=C sort -u || true)"
printf '%s\n' "$legacy_routes" | while IFS= read -r route; do
  [ -z "$route" ] || assert_legacy_allowlisted route "$route"
done

legacy_tables="$(rg -o --no-filename 'CREATE TABLE IF NOT EXISTS (knowledge_[a-z0-9_]+|workbench_[a-z0-9_]+|r_survival_[a-z0-9_]+|biography_[a-z0-9_]+)' sql/migrations \
  | sed -E 's/CREATE TABLE IF NOT EXISTS //' \
  | LC_ALL=C sort -u || true)"
printf '%s\n' "$legacy_tables" | while IFS= read -r table; do
  [ -z "$table" ] || assert_legacy_allowlisted table "$table"
done

if [ -e deploy/r-notebook-runtime ]; then
  echo "R Survival boundary violation: the application-specific notebook runtime must live in tma-r-survival-workbench" >&2
  exit 1
fi

if [ -n "$(find apps/workbench/src/plugins/analysisWorkbench -type f -print -quit 2>/dev/null)" ]; then
  echo "R Survival boundary violation: the domain application must not live in apps/workbench" >&2
  exit 1
fi
if rg -n 'com\.tma\.r-survival-workbench|/v2/(workbench-projects|r-survival-projects)' apps/workbench/src; then
  echo "R Survival boundary violation: conversation workbench contains R application coupling" >&2
  exit 1
fi

if rg -n '/v2/(knowledge/|public/knowledge-shares|workbench-projects|r-survival-projects)' \
  api/v2/openapi.yaml sdk/tma sdk/typescript/src; then
  echo "Core SDK boundary violation: application business APIs must not appear in Platform OpenAPI or SDKs" >&2
  exit 1
fi
if rg -n '\b(Knowledge(Base|Document|Service|Share|Answer)|WorkbenchProject)\b' \
  sdk/tma/internal/generated/client.gen.go sdk/typescript/src/internal/generated/schema.ts; then
  echo "Core SDK boundary violation: application business schemas must not be published by Platform" >&2
  exit 1
fi

if [ -d ../tma-knowledge ]; then
  if [ ! -x ../tma-knowledge/scripts/check-boundaries.sh ]; then
    echo "Knowledge boundary violation: independent repository has no executable boundary check" >&2
    exit 1
  fi
  (cd ../tma-knowledge && bash scripts/check-boundaries.sh)
fi

if [ -d ../tma-r-survival-workbench ]; then
  if [ ! -f ../tma-r-survival-workbench/scripts/check-boundaries.sh ]; then
    echo "R Survival boundary violation: independent repository has no boundary check" >&2
    exit 1
  fi
  (cd ../tma-r-survival-workbench && bash scripts/check-boundaries.sh)
fi

if [ -d apps/console ]; then
  if ! rg -q '"@tma/core-sdk"' apps/console/package.json; then
    echo "Console boundary violation: apps/console must depend on @tma/core-sdk" >&2
    exit 1
  fi
  if ! rg -q '"build:embedded"' apps/console/package.json; then
    echo "Console boundary violation: embedded compatibility must be an explicit build mode" >&2
    exit 1
  fi
  if [ ! -f apps/console/Dockerfile ]; then
    echo "Console boundary violation: apps/console must own its release image" >&2
    exit 1
  fi
  if rg -n 'internal/httpapi|cmd/tma-server' apps/console/Dockerfile; then
    echo "Console boundary violation: release image must not copy Server implementation or embedded assets" >&2
    exit 1
  fi
  if rg -n --glob '!*.test.*' '/v(1|2)/' apps/console/src; then
    echo "Console boundary violation: production source must use @tma/core-sdk instead of raw API paths" >&2
    exit 1
  fi
fi

echo "Repository boundary check passed"
