#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BASE="$ROOT/deploy/kubernetes/base"
CONFIG_FILE="$ROOT/deploy/kubernetes/config.production.env"
RUNTIME_SECRET_FILE="$ROOT/deploy/kubernetes/runtime-secrets.env"
MIGRATION_SECRET_FILE="$ROOT/deploy/kubernetes/migration-secrets.env"
NAMESPACE=tma
PUBLIC_HOST=tma.example.com
INGRESS_CLASS=nginx
TLS_SECRET=tma-tls
SERVER_IMAGE="${TMA_SERVER_IMAGE:-}"
WORKER_IMAGE="${TMA_WORKER_IMAGE:-}"
MIGRATE_IMAGE="${TMA_MIGRATE_IMAGE:-}"
INIT_DB=0
KEEP_MIGRATION_SECRET=0
SKIP_WORKER=0
DRY_RUN=0
ASSUME_YES=0
TIMEOUT=10m

usage() {
  cat <<'EOF'
Usage: deploy/kubernetes/deploy.sh [options]

Required:
  --server-image IMAGE
  --worker-image IMAGE              Unless --skip-worker is used.
  --migrate-image IMAGE             Required with --init-db.

Options:
  --config-file PATH                Non-secret TMA environment file.
  --runtime-secret-file PATH        Runtime secret environment file.
  --migration-secret-file PATH      Migration owner environment file.
  --release-file PATH               Images, host, Namespace and Ingress settings.
  --namespace NAME                  Default: tma.
  --host HOST                       Public HTTPS host.
  --ingress-class NAME              Default: nginx.
  --tls-secret NAME                 Default: tma-tls.
  --init-db                         Apply the 000092 baseline Job once.
  --keep-migration-secret           Keep owner Secret after successful Job.
  --skip-worker                     Deploy control plane without Worker.
  --timeout DURATION                Rollout/Job timeout (default: 10m).
  --dry-run                         Render and validate without cluster writes.
  --yes                             Confirm deployment to the current context.
  -h, --help                        Show this help.
EOF
}

load_release_file() {
  local file="$1" key value
  [[ -f "$file" ]] || { printf 'release file not found: %s\n' "$file" >&2; exit 1; }
  while IFS='=' read -r key value || [[ -n "$key" ]]; do
    [[ -z "$key" || "$key" == \#* ]] && continue
    value="${value%$'\r'}"
    case "$key" in
      TMA_SERVER_IMAGE) SERVER_IMAGE="$value" ;;
      TMA_WORKER_IMAGE) WORKER_IMAGE="$value" ;;
      TMA_MIGRATE_IMAGE) MIGRATE_IMAGE="$value" ;;
      TMA_PUBLIC_HOST) PUBLIC_HOST="$value" ;;
      TMA_NAMESPACE) NAMESPACE="$value" ;;
      TMA_INGRESS_CLASS) INGRESS_CLASS="$value" ;;
      TMA_TLS_SECRET) TLS_SECRET="$value" ;;
      *) printf 'unsupported release setting: %s\n' "$key" >&2; exit 1 ;;
    esac
  done <"$file"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --server-image) SERVER_IMAGE="${2:?missing value}"; shift 2 ;;
    --worker-image) WORKER_IMAGE="${2:?missing value}"; shift 2 ;;
    --migrate-image) MIGRATE_IMAGE="${2:?missing value}"; shift 2 ;;
    --config-file) CONFIG_FILE="${2:?missing value}"; shift 2 ;;
    --runtime-secret-file) RUNTIME_SECRET_FILE="${2:?missing value}"; shift 2 ;;
    --migration-secret-file) MIGRATION_SECRET_FILE="${2:?missing value}"; shift 2 ;;
    --release-file) load_release_file "${2:?missing value}"; shift 2 ;;
    --namespace) NAMESPACE="${2:?missing value}"; shift 2 ;;
    --host) PUBLIC_HOST="${2:?missing value}"; shift 2 ;;
    --ingress-class) INGRESS_CLASS="${2:?missing value}"; shift 2 ;;
    --tls-secret) TLS_SECRET="${2:?missing value}"; shift 2 ;;
    --init-db) INIT_DB=1; shift ;;
    --keep-migration-secret) KEEP_MIGRATION_SECRET=1; shift ;;
    --skip-worker) SKIP_WORKER=1; shift ;;
    --timeout) TIMEOUT="${2:?missing value}"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    --yes) ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'unknown option: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

command -v kubectl >/dev/null || { echo 'kubectl is required' >&2; exit 1; }
[[ -n "$SERVER_IMAGE" ]] || { echo '--server-image is required' >&2; exit 2; }
if [[ "$SKIP_WORKER" -eq 0 && -z "$WORKER_IMAGE" ]]; then
  echo '--worker-image is required unless --skip-worker is used' >&2
  exit 2
fi
if [[ "$INIT_DB" -eq 1 && -z "$MIGRATE_IMAGE" ]]; then
  echo '--migrate-image is required with --init-db' >&2
  exit 2
fi
[[ "$NAMESPACE" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ ]] || { echo 'invalid namespace' >&2; exit 2; }
[[ "$PUBLIC_HOST" != 'tma.example.com' && "$PUBLIC_HOST" != *'example.com'* ]] || { echo '--host must be a production hostname' >&2; exit 2; }

validate_file() {
  local file="$1"
  [[ -f "$file" ]] || { printf 'required deployment file not found: %s\n' "$file" >&2; exit 1; }
  if grep -En 'replace-with|replace-me|example\.com' "$file" >/dev/null; then
    printf 'deployment file still contains template values: %s\n' "$file" >&2
    grep -En 'replace-with|replace-me|example\.com' "$file" >&2
    exit 1
  fi
}

validate_file "$CONFIG_FILE"
validate_file "$RUNTIME_SECRET_FILE"
if [[ "$INIT_DB" -eq 1 ]]; then
  validate_file "$MIGRATION_SECRET_FILE"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

render_manifest() {
  local input="$1" output="$2"
  sed \
    -e "s|namespace: tma|namespace: $NAMESPACE|g" \
    -e "s|registry.example.com/tma-server:0.1.0|$SERVER_IMAGE|g" \
    -e "s|registry.example.com/tma-worker:0.1.0|$WORKER_IMAGE|g" \
    -e "s|registry.example.com/tma-migrate:0.1.0|$MIGRATE_IMAGE|g" \
    -e "s|tma.example.com|$PUBLIC_HOST|g" \
    -e "s|ingressClassName: nginx|ingressClassName: $INGRESS_CLASS|g" \
    -e "s|secretName: tma-tls|secretName: $TLS_SECRET|g" \
    "$input" >"$output"
}

render_manifest "$BASE/server.yaml" "$TMP_DIR/server.yaml"
render_manifest "$BASE/ingress.yaml" "$TMP_DIR/ingress.yaml"
if [[ "$SKIP_WORKER" -eq 0 ]]; then
  render_manifest "$BASE/worker.yaml" "$TMP_DIR/worker.yaml"
fi
if [[ "$INIT_DB" -eq 1 ]]; then
  render_manifest "$BASE/migration-job.yaml" "$TMP_DIR/migration-job.yaml"
fi

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml >"$TMP_DIR/namespace.yaml"
kubectl -n "$NAMESPACE" create configmap tma-config --from-env-file="$CONFIG_FILE" --dry-run=client -o yaml >"$TMP_DIR/configmap.yaml"
kubectl -n "$NAMESPACE" create secret generic tma-runtime-secrets --from-env-file="$RUNTIME_SECRET_FILE" --dry-run=client -o yaml >"$TMP_DIR/runtime-secret.yaml"
if [[ "$INIT_DB" -eq 1 ]]; then
  kubectl -n "$NAMESPACE" create secret generic tma-migration-secrets --from-env-file="$MIGRATION_SECRET_FILE" --dry-run=client -o yaml >"$TMP_DIR/migration-secret.yaml"
fi

if grep -ERn 'registry\.example|replace-with|replace-me|example\.com' "$TMP_DIR" >/dev/null; then
  echo 'rendered Kubernetes manifests still contain template values:' >&2
  grep -ERn 'registry\.example|replace-with|replace-me|example\.com' "$TMP_DIR" >&2
  exit 1
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  echo 'Kubernetes deployment manifests are valid.'
  exit 0
fi

context="$(kubectl config current-context)"
printf 'Kubernetes context: %s\nNamespace: %s\nHost: %s\n' "$context" "$NAMESPACE" "$PUBLIC_HOST"
if [[ "$ASSUME_YES" -ne 1 ]]; then
  if [[ ! -t 0 ]]; then
    echo '--yes is required for non-interactive deployment' >&2
    exit 1
  fi
  read -r -p 'Continue deployment? [y/N] ' answer
  [[ "$answer" == 'y' || "$answer" == 'Y' ]] || { echo 'deployment canceled'; exit 1; }
fi

kubectl apply -f "$TMP_DIR/namespace.yaml"
kubectl apply -f "$TMP_DIR/configmap.yaml"
kubectl apply -f "$TMP_DIR/runtime-secret.yaml"

if [[ "$INIT_DB" -eq 1 ]]; then
  kubectl apply -f "$TMP_DIR/migration-secret.yaml"
  if kubectl -n "$NAMESPACE" get job tma-database-baseline-000092 >/dev/null 2>&1; then
    complete="$(kubectl -n "$NAMESPACE" get job tma-database-baseline-000092 -o jsonpath='{.status.conditions[?(@.type=="Complete")].status}')"
    [[ "$complete" == 'True' ]] || { echo 'existing baseline Job is not complete; inspect it before retrying' >&2; exit 1; }
    echo 'database baseline Job already completed; skipping reinitialization'
  else
    kubectl apply -f "$TMP_DIR/migration-job.yaml"
    if ! kubectl -n "$NAMESPACE" wait --for=condition=complete job/tma-database-baseline-000092 --timeout="$TIMEOUT"; then
      kubectl -n "$NAMESPACE" logs job/tma-database-baseline-000092 --all-containers=true >&2 || true
      exit 1
    fi
    kubectl -n "$NAMESPACE" logs job/tma-database-baseline-000092 --all-containers=true
  fi
  if [[ "$KEEP_MIGRATION_SECRET" -eq 0 ]]; then
    kubectl -n "$NAMESPACE" delete secret tma-migration-secrets --ignore-not-found
  fi
fi

kubectl apply -f "$TMP_DIR/server.yaml"
if [[ "$SKIP_WORKER" -eq 0 ]]; then
  kubectl apply -f "$TMP_DIR/worker.yaml"
fi
kubectl apply -f "$TMP_DIR/ingress.yaml"

kubectl -n "$NAMESPACE" rollout status deployment/tma-server --timeout="$TIMEOUT"
if [[ "$SKIP_WORKER" -eq 0 ]]; then
  kubectl -n "$NAMESPACE" rollout status deployment/tma-worker --timeout="$TIMEOUT"
fi
printf 'TMA Kubernetes deployment is ready: https://%s/health\n' "$PUBLIC_HOST"
