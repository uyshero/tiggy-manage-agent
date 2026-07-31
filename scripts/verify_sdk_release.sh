#!/bin/sh
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
version="$(tr -d '[:space:]' <"$root/sdk/VERSION")"
typescript_version="$(node -p "require('$root/sdk/typescript/package.json').version")"

case "$version" in
  v*) ;;
  *) echo "sdk/VERSION must be a Go module version beginning with v" >&2; exit 1 ;;
esac

if [ "${version#v}" != "$typescript_version" ]; then
  echo "SDK version mismatch: sdk/VERSION=$version, TypeScript=$typescript_version" >&2
  exit 1
fi

printf 'verified Core SDK release version %s\n' "$version"
