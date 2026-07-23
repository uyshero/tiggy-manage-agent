#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
BASE_REF="${TMA_BENCH_BASE_REF:-HEAD}"
BENCHTIME="${TMA_BENCHTIME:-1s}"
COUNT="${TMA_BENCH_COUNT:-5}"
GOCACHE="${GOCACHE:-$ROOT_DIR/.gocache}"
BASE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/tma-agent-core-benchmark.XXXXXX")"

cleanup() {
	case "$BASE_DIR" in
		"${TMPDIR:-/tmp}"/tma-agent-core-benchmark.*) rm -r "$BASE_DIR" ;;
	esac
}
trap cleanup EXIT INT TERM

mkdir -p "$BASE_DIR/base"
git -C "$ROOT_DIR" archive "$BASE_REF" | tar -x -C "$BASE_DIR/base"
cp "$ROOT_DIR/internal/agentcore/engine_benchmark_test.go" "$BASE_DIR/base/internal/agentcore/engine_benchmark_test.go"

printf 'Current working tree\n'
cd "$ROOT_DIR"
GOCACHE="$GOCACHE" go test ./internal/agentcore -run '^$' -bench '^BenchmarkAgentLoop$' -benchmem -benchtime="$BENCHTIME" -count="$COUNT"

printf '\nBase ref: %s\n' "$BASE_REF"
cd "$BASE_DIR/base"
GOCACHE="$GOCACHE" go test ./internal/agentcore -run '^$' -bench '^BenchmarkAgentLoop$' -benchmem -benchtime="$BENCHTIME" -count="$COUNT"
