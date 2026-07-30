#!/bin/sh
set -eu

ACTION="${1:-verify}"
REALM="${TMA_KEYCLOAK_REALM:-tma}"
REALM_FILE="${TMA_KEYCLOAK_REALM_FILE:-deploy/keycloak/tma-realm.json}"
CLIENT_ID="${TMA_KEYCLOAK_BIOGRAPHY_CLIENT_ID:-tma-biography-mobile}"
EXPECTED_REDIRECT_URIS='["https://story.tiggy.cloud/*","http://127.0.0.1:5175/*","http://localhost:5175/*"]'
EXPECTED_WEB_ORIGINS='["https://story.tiggy.cloud","http://127.0.0.1:5175","http://localhost:5175"]'

case "$ACTION" in
  apply|verify) ;;
  *)
    echo "usage: $0 [apply|verify]" >&2
    exit 2
    ;;
esac

partial_import_file="$(mktemp "${TMPDIR:-/tmp}/tma-keycloak-biography-import.XXXXXX")"
live_client_file="$(mktemp "${TMPDIR:-/tmp}/tma-keycloak-biography-live.XXXXXX")"
container_import_file="/tmp/tma-keycloak-biography-import-$$.json"

cleanup() {
  rm -f "$partial_import_file" "$live_client_file"
  if docker compose --profile oidc ps --status running --services 2>/dev/null | grep -qx keycloak; then
    docker compose --profile oidc exec -T -u root keycloak rm -f "$container_import_file" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

python3 - "$REALM_FILE" "$REALM" "$CLIENT_ID" "$EXPECTED_REDIRECT_URIS" "$EXPECTED_WEB_ORIGINS" "$partial_import_file" <<'PY'
import json
import sys

realm_path, expected_realm, client_id, expected_redirects_raw, expected_origins_raw, output_path = sys.argv[1:]
expected_redirects = set(json.loads(expected_redirects_raw))
expected_origins = set(json.loads(expected_origins_raw))
with open(realm_path, encoding="utf-8") as stream:
    realm = json.load(stream)

if realm.get("realm") != expected_realm:
    raise SystemExit(f"{realm_path}: unexpected realm")
clients = [client for client in realm.get("clients", []) if client.get("clientId") == client_id]
if len(clients) != 1:
    raise SystemExit(f"{realm_path}: expected exactly one {client_id!r} client")
client = clients[0]

expected = {
    "enabled": True,
    "protocol": "openid-connect",
    "publicClient": True,
    "standardFlowEnabled": True,
    "directAccessGrantsEnabled": False,
    "serviceAccountsEnabled": False,
}
for key, value in expected.items():
    if client.get(key) != value:
        raise SystemExit(f"{realm_path}: {client_id}.{key} has an unsafe value")

redirects = set(client.get("redirectUris") or [])
origins = set(client.get("webOrigins") or [])
if redirects != expected_redirects:
    raise SystemExit(f"{realm_path}: {client_id} redirect URI allowlist is invalid")
if origins != expected_origins:
    raise SystemExit(f"{realm_path}: {client_id} web origin allowlist is invalid")
if (client.get("attributes") or {}).get("pkce.code.challenge.method") != "S256":
    raise SystemExit(f"{realm_path}: {client_id} must require PKCE S256")

mappers = {item.get("name"): item for item in client.get("protocolMappers") or []}
groups = mappers.get("groups") or {}
audience = mappers.get("tma-api-audience") or {}
if groups.get("protocolMapper") != "oidc-group-membership-mapper":
    raise SystemExit(f"{realm_path}: {client_id} is missing the groups mapper")
if (groups.get("config") or {}).get("claim.name") != "groups":
    raise SystemExit(f"{realm_path}: {client_id} groups mapper has the wrong claim")
if audience.get("protocolMapper") != "oidc-audience-mapper":
    raise SystemExit(f"{realm_path}: {client_id} is missing the audience mapper")
if (audience.get("config") or {}).get("included.client.audience") != "tma-api":
    raise SystemExit(f"{realm_path}: {client_id} audience mapper must target tma-api")

with open(output_path, "w", encoding="utf-8") as stream:
    json.dump({"ifResourceExists": "OVERWRITE", "clients": [client]}, stream)
PY

chmod 0644 "$partial_import_file"

if ! docker compose --profile oidc ps --status running --services | grep -qx keycloak; then
  echo "Keycloak is not running; source biography client configuration verified" >&2
  echo "start it with: docker compose --profile oidc up -d keycloak" >&2
  exit 1
fi

if [ "$ACTION" = "apply" ]; then
  docker compose --profile oidc cp "$partial_import_file" "keycloak:$container_import_file" >/dev/null
fi

docker compose --profile oidc exec -T keycloak sh -s -- \
  "$ACTION" "$REALM" "$CLIENT_ID" "$container_import_file" >"$live_client_file" <<'SH'
set -eu

action="$1"
realm="$2"
client_id="$3"
import_file="$4"
config_file="/tmp/tma-biography-kcadm-$$.config"
trap 'rm -f "$config_file"' EXIT INT TERM

: "${KC_BOOTSTRAP_ADMIN_USERNAME:?missing Keycloak bootstrap administrator username}"
: "${KC_BOOTSTRAP_ADMIN_PASSWORD:?missing Keycloak bootstrap administrator password}"
export KC_CLI_PASSWORD="$KC_BOOTSTRAP_ADMIN_PASSWORD"

/opt/keycloak/bin/kcadm.sh config credentials \
  --config "$config_file" \
  --server http://localhost:8080 \
  --realm master \
  --user "$KC_BOOTSTRAP_ADMIN_USERNAME" >/dev/null

if [ "$action" = "apply" ]; then
  /opt/keycloak/bin/kcadm.sh create partialImport \
    --config "$config_file" \
    -r "$realm" \
    -f "$import_file" >/dev/null
fi

/opt/keycloak/bin/kcadm.sh get clients \
  --config "$config_file" \
  -r "$realm" \
  -q "clientId=$client_id"
SH

python3 - "$live_client_file" "$CLIENT_ID" "$EXPECTED_REDIRECT_URIS" "$EXPECTED_WEB_ORIGINS" <<'PY'
import json
import sys

path, client_id, expected_redirects_raw, expected_origins_raw = sys.argv[1:]
expected_redirects = set(json.loads(expected_redirects_raw))
expected_origins = set(json.loads(expected_origins_raw))
with open(path, encoding="utf-8") as stream:
    clients = json.load(stream)
if not isinstance(clients, list) or len(clients) != 1:
    raise SystemExit(f"live Keycloak realm: expected exactly one {client_id!r} client")
client = clients[0]
if not (client.get("publicClient") and client.get("standardFlowEnabled")):
    raise SystemExit(f"live Keycloak realm: {client_id} must be a public authorization-code client")
if client.get("directAccessGrantsEnabled") or client.get("serviceAccountsEnabled"):
    raise SystemExit(f"live Keycloak realm: {client_id} has an unsafe grant enabled")
if (client.get("attributes") or {}).get("pkce.code.challenge.method") != "S256":
    raise SystemExit(f"live Keycloak realm: {client_id} must require PKCE S256")
if set(client.get("redirectUris") or []) != expected_redirects:
    raise SystemExit(f"live Keycloak realm: {client_id} redirect URI allowlist is invalid")
if set(client.get("webOrigins") or []) != expected_origins:
    raise SystemExit(f"live Keycloak realm: {client_id} web origin allowlist is invalid")
mappers = {item.get("name"): item for item in client.get("protocolMappers") or []}
if (mappers.get("groups") or {}).get("protocolMapper") != "oidc-group-membership-mapper":
    raise SystemExit(f"live Keycloak realm: {client_id} groups mapper is missing")
audience = mappers.get("tma-api-audience") or {}
if audience.get("protocolMapper") != "oidc-audience-mapper" or (audience.get("config") or {}).get("included.client.audience") != "tma-api":
    raise SystemExit(f"live Keycloak realm: {client_id} audience mapper is missing")
print(f"Keycloak biography client verified: {client_id}")
PY
