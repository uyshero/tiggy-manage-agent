#!/bin/sh
set -eu

# Transitional guard: Biography entrypoints must use the public SDK/API rather
# than importing Platform implementation packages. The internal biographyvoice
# package is intentionally not checked yet because its storage adapter is part
# of the planned extraction work.
forbidden='internal/httpapi|internal/managedagents|internal/runner|internal/agentruntime|internal/agentschedule'
paths='cmd/tma-biography-voice-gateway cmd/tma-biography-agent-bootstrap apps/biography-mobile'

matches="$(rg -n "$forbidden" $paths -g '*.go' -g '*.js' -g '*.jsx' -g '*.ts' -g '*.tsx' -g '*.vue' 2>/dev/null || true)"
if [ -n "$matches" ]; then
  echo "Biography boundary violation: entrypoints import Platform implementation packages" >&2
  printf '%s\n' "$matches" >&2
  exit 1
fi

echo "Repository boundary check passed"
