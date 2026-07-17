#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'EOF'
Usage:
  scripts/deploy.sh docker [options]
  scripts/deploy.sh k8s [options]

Run the platform command with --help for its options.
EOF
}

platform="${1:-}"
if [[ -z "$platform" || "$platform" == "-h" || "$platform" == "--help" ]]; then
  usage
  exit 0
fi
shift

case "$platform" in
  docker)
    exec "$ROOT/deploy/docker/deploy.sh" "$@"
    ;;
  k8s|kubernetes)
    exec "$ROOT/deploy/kubernetes/deploy.sh" "$@"
    ;;
  *)
    printf 'unsupported deployment platform: %s\n' "$platform" >&2
    usage >&2
    exit 2
    ;;
esac

