#!/bin/sh
set -eu

BASE_URL="${TMA_VERIFY_OIDC_BASE_URL:-http://localhost:18088}"
HTTP_ADDR="${TMA_VERIFY_OIDC_HTTP_ADDR:-:18088}"
KEYCLOAK_URL="${TMA_VERIFY_KEYCLOAK_URL:-http://localhost:18080}"
ISSUER="$KEYCLOAK_URL/realms/tma-test"
POSTGRES_USER="${TMA_POSTGRES_TEST_USER:-tma}"
POSTGRES_PASSWORD="${TMA_POSTGRES_TEST_PASSWORD:-tma}"
POSTGRES_HOST="${TMA_POSTGRES_TEST_HOST:-localhost}"
POSTGRES_PORT="${TMA_POSTGRES_TEST_PORT:-5432}"
VERIFY_DATABASE="tma_verify_oidc_$(date +%Y%m%d%H%M%S)_$$"
DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$VERIFY_DATABASE?sslmode=disable"
SERVER_BIN="${TMA_SERVER_BIN:-bin/tma-server}"
LOG_FILE="${TMA_VERIFY_OIDC_SERVER_LOG:-.verify-oidc-keycloak-server.log}"
WAIT_SECONDS="${TMA_VERIFY_OIDC_WAIT_SECONDS:-60}"
AUDIT_COLLECTOR_PORT="${TMA_VERIFY_SECURITY_AUDIT_PORT:-18081}"
AUDIT_COLLECTOR_URL="http://127.0.0.1:$AUDIT_COLLECTOR_PORT"
AUDIT_COLLECTOR_TOKEN="security-audit-test-token"
AUDIT_CAPTURE_FILE=""
TEST_ORGANIZATION_ID="org_oidc_keycloak_test"
TEST_WORKSPACE_ID="wksp_oidc_keycloak_test"
CLAIM_MAPPING='{"workspace_claim":"","roles_claim":"","groups_claim":"groups","group_mappings":{"finance-operators":{"organization_id":"org_oidc_keycloak_test","workspace_id":"wksp_oidc_keycloak_test","roles":["operator"]}},"allowed_workspace_ids":["wksp_oidc_keycloak_test"],"require_group_mapping":true}'

if [ ! -x "$SERVER_BIN" ]; then
  echo "missing server binary: $SERVER_BIN" >&2
  echo "run: make build" >&2
  exit 1
fi

server_pid=""
audit_collector_pid=""
database_created="false"
keycloak_was_running="false"
if docker compose --profile oidc ps --status running --services | grep -qx keycloak; then
  keycloak_was_running="true"
fi
AUDIT_CAPTURE_FILE="$(mktemp "${TMPDIR:-/tmp}/tma-security-audit.XXXXXX")"
cleanup() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [ -n "$audit_collector_pid" ] && kill -0 "$audit_collector_pid" 2>/dev/null; then
    kill "$audit_collector_pid" 2>/dev/null || true
    wait "$audit_collector_pid" 2>/dev/null || true
  fi
  rm -f "$AUDIT_CAPTURE_FILE"
  if [ "$database_created" = "true" ]; then
    docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres \
      -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$VERIFY_DATABASE' AND pid <> pg_backend_pid();" \
      >/dev/null 2>&1 || true
    docker compose exec -T postgres dropdb --if-exists -U "$POSTGRES_USER" "$VERIFY_DATABASE" >/dev/null 2>&1 || true
  fi
  if [ "$keycloak_was_running" != "true" ]; then
    docker compose --profile oidc rm -f -s keycloak >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

docker compose up -d postgres >/dev/null
docker compose exec -T postgres createdb -U "$POSTGRES_USER" "$VERIFY_DATABASE"
database_created="true"
docker compose exec -T postgres sh -c '
  set -eu
  for file in /migrations/*.sql; do
    psql -v ON_ERROR_STOP=1 --single-transaction -U "$1" -d "$2" -f "$file" >/dev/null
  done
' sh "$POSTGRES_USER" "$VERIFY_DATABASE"

docker compose --profile oidc up -d keycloak

python3 scripts/otlp_logs_fixture.py \
  --port "$AUDIT_COLLECTOR_PORT" \
  --output "$AUDIT_CAPTURE_FILE" \
  --token "$AUDIT_COLLECTOR_TOKEN" &
audit_collector_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if curl -fsS "$AUDIT_COLLECTOR_URL/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -fsS "$AUDIT_COLLECTOR_URL/health" >/dev/null 2>&1; then
  echo "Security audit OTLP fixture did not become ready within ${WAIT_SECONDS}s" >&2
  exit 1
fi

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if curl -fsS "$ISSUER/.well-known/openid-configuration" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! curl -fsS "$ISSUER/.well-known/openid-configuration" >/dev/null 2>&1; then
  echo "Keycloak did not become ready within ${WAIT_SECONDS}s" >&2
  docker compose --profile oidc logs keycloak >&2 || true
  exit 1
fi

token_response="$(curl -fsS -X POST "$ISSUER/protocol/openid-connect/token" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  --data-urlencode 'grant_type=password' \
  --data-urlencode 'client_id=tma-api' \
  --data-urlencode 'username=alice' \
  --data-urlencode 'password=alice-password')"
access_token="$(printf '%s' "$token_response" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"

docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$VERIFY_DATABASE" >/dev/null <<SQL
INSERT INTO organizations (id, name) VALUES ('$TEST_ORGANIZATION_ID', 'OIDC Keycloak Test') ON CONFLICT (id) DO NOTHING;
INSERT INTO workspaces (id, org_id, name) VALUES ('$TEST_WORKSPACE_ID', '$TEST_ORGANIZATION_ID', 'OIDC Keycloak Test') ON CONFLICT (id) DO NOTHING;
SQL

TMA_ENV=test \
TMA_HTTP_ADDR="$HTTP_ADDR" \
TMA_DATABASE_URL="$DATABASE_URL" \
TMA_AUTH_MODE=oidc \
TMA_AUTH_OIDC_ISSUER="$ISSUER" \
TMA_AUTH_OIDC_AUDIENCE=tma-api \
TMA_AUTH_OIDC_CLAIM_MAPPING_JSON="$CLAIM_MAPPING" \
TMA_SECURITY_AUDIT_OTLP_ENDPOINT="$AUDIT_COLLECTOR_URL" \
TMA_SECURITY_AUDIT_OTLP_TOKEN="$AUDIT_COLLECTOR_TOKEN" \
TMA_SECURITY_AUDIT_INTEGRITY_KEY=keycloak-test-security-audit-integrity-key \
TMA_SECURITY_AUDIT_QUEUE_SIZE=32 \
TMA_SECURITY_AUDIT_BATCH_SIZE=1 \
TMA_SECURITY_AUDIT_FLUSH_INTERVAL_MS=50 \
TMA_WORKER_AUTH_TOKEN=keycloak-test-worker-token \
TMA_LLM_PROVIDER=fake \
TMA_LLM_MODEL=fake-demo \
"$SERVER_BIN" >"$LOG_FILE" 2>&1 &
server_pid="$!"

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "TMA server exited before becoming healthy" >&2
    cat "$LOG_FILE" >&2 || true
    exit 1
  fi
  if curl -fsS "$BASE_URL/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

principal_response="$(curl -fsS "$BASE_URL/v1/auth/me" -H "Authorization: Bearer $access_token")"
printf '%s' "$principal_response" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
principal = payload.get("principal") or {}
assert payload.get("authenticated") is True, payload
assert principal.get("workspace_id") == "wksp_oidc_keycloak_test", principal
assert principal.get("organization_id") == "org_oidc_keycloak_test", principal
assert "operator" in principal.get("roles", []), principal
print("Keycloak OIDC principal mapping verified")
'

agents_response="$(curl -fsS "$BASE_URL/v1/agents?workspace_id=wksp_untrusted" -H "Authorization: Bearer $access_token")"
printf '%s' "$agents_response" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
agents = payload.get("agents") or []
assert agents, payload
assert all(agent.get("workspace_id") == "wksp_oidc_keycloak_test" for agent in agents), agents
print("Keycloak OIDC workspace isolation verified")
'

metrics_response="$(curl -fsS "$BASE_URL/metrics" -H "Authorization: Bearer $access_token")"
printf '%s' "$metrics_response" | grep -F 'tma_authorization_decisions_total{auth_type="oidc",outcome="allowed",reason="identity_boundary"}' >/dev/null

deadline=$(( $(date +%s) + WAIT_SECONDS ))
while [ "$(date +%s)" -le "$deadline" ]; do
  if [ -s "$AUDIT_CAPTURE_FILE" ]; then
    break
  fi
  sleep 1
done

metrics_response="$(curl -fsS "$BASE_URL/metrics" -H "Authorization: Bearer $access_token")"
printf '%s' "$metrics_response" | python3 -c '
import re, sys
text = sys.stdin.read()
values = [int(value) for value in re.findall(r"tma_security_audit_export_events_total\{outcome=\"sent\"\} ([0-9]+)", text)]
assert values and max(values) > 0, text
assert "tma_security_audit_exporter_durable 1" in text, text
assert "tma_security_audit_integrity_status_available 1" in text, text
assert "tma_security_audit_integrity_blocking_events{reason=\"unconfigured_key\"} 0" in text, text
assert "tma_security_audit_integrity_blocking_events{reason=\"historical_unidentified\"} 0" in text, text
assert "tma_security_audit_integrity_keys{state=\"removal_blocked\"} 0" in text, text
'

delivered_count="$(docker compose exec -T postgres psql -U "$POSTGRES_USER" -d "$VERIFY_DATABASE" -tAc "SELECT COUNT(*) FROM security_audit_outbox WHERE payload_json LIKE '%$TEST_WORKSPACE_ID%' AND integrity_algorithm = 'hmac-sha256' AND integrity_key_id = 'legacy' AND status = 'delivered';" | tr -d '[:space:]')"
if [ "${delivered_count:-0}" -lt 1 ]; then
  echo "missing delivered HMAC security audit outbox event" >&2
  exit 1
fi

integrity_key_status="$(curl -fsS "$BASE_URL/v1/observability/security-audit/integrity-keys" -H "Authorization: Bearer $access_token")"
printf '%s' "$integrity_key_status" | python3 -c '
import json, sys
payload = json.load(sys.stdin)
assert payload.get("active_key_id") == "legacy", payload
keys = {item.get("key_id"): item for item in payload.get("keys", [])}
legacy = keys.get("legacy") or {}
assert legacy.get("configured") is True, payload
assert legacy.get("active") is True, payload
assert legacy.get("safe_to_remove") is False, payload
serialized = json.dumps(payload)
assert "keycloak-test-security-audit-integrity-key" not in serialized, payload
print("Keycloak OIDC security audit integrity key readiness verified")
'

python3 - "$LOG_FILE" <<'PY'
import json
import sys

matched = False
with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        try:
            record = json.loads(line)
        except json.JSONDecodeError:
            continue
        sources = record.get("authorization_sources") or []
        if (
            record.get("event") == "authorization_decision"
            and record.get("outcome") == "allowed"
            and record.get("auth_type") == "oidc"
            and record.get("workspace_id") == "wksp_oidc_keycloak_test"
            and "group_mapping:finance-operators" in sources
        ):
            matched = True
            break
assert matched, "missing Keycloak group authorization audit event"
print("Keycloak OIDC authorization audit and metrics verified")
PY

python3 - "$AUDIT_CAPTURE_FILE" <<'PY'
import json
import sys

matched = False
with open(sys.argv[1], encoding="utf-8") as stream:
    for line in stream:
        envelope = json.loads(line)
        assert envelope.get("path") == "/v1/logs", envelope
        assert envelope.get("authorization_valid") is True, envelope
        encoded = json.dumps(envelope.get("payload") or {})
        assert "security-audit-test-token" not in encoded, envelope
        if (
            "tma.security.authorization" in encoded
            and "group_mapping:finance-operators" in encoded
            and "wksp_oidc_keycloak_test" in encoded
        ):
            matched = True
assert matched, "missing Keycloak authorization event in OTLP Logs capture"
print("Keycloak OIDC OTLP security audit export verified")
PY
