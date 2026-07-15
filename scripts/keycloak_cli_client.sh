#!/bin/sh
set -eu

ACTION="${1:-verify}"
REALM="${TMA_KEYCLOAK_REALM:-tma}"
REALM_FILE="${TMA_KEYCLOAK_REALM_FILE:-deploy/keycloak/tma-realm.json}"
CLIENT_ID="${TMA_KEYCLOAK_CLI_CLIENT_ID:-tma-cli}"

case "$ACTION" in
  apply|verify) ;;
  *)
    echo "usage: $0 [apply|verify]" >&2
    exit 2
    ;;
esac

partial_import_file="$(mktemp "${TMPDIR:-/tmp}/tma-keycloak-cli-import.XXXXXX")"
live_client_file="$(mktemp "${TMPDIR:-/tmp}/tma-keycloak-cli-live.XXXXXX")"
container_import_file="/tmp/tma-keycloak-cli-import-$$.json"

cleanup() {
  rm -f "$partial_import_file" "$live_client_file"
  if docker compose --profile oidc ps --status running --services 2>/dev/null | grep -qx keycloak; then
    docker compose --profile oidc exec -T -u root keycloak rm -f "$container_import_file" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

python3 - "$REALM_FILE" "$REALM" "$CLIENT_ID" "$partial_import_file" <<'PY'
import json
import sys

realm_path, expected_realm, client_id, output_path = sys.argv[1:]
with open(realm_path, encoding="utf-8") as stream:
    realm = json.load(stream)

if realm.get("realm") != expected_realm:
    raise SystemExit(
        f"{realm_path}: realm={realm.get('realm')!r}, expected {expected_realm!r}"
    )

clients = [client for client in realm.get("clients", []) if client.get("clientId") == client_id]
if len(clients) != 1:
    raise SystemExit(f"{realm_path}: expected exactly one {client_id!r} client, found {len(clients)}")

client = clients[0]
expected_fields = {
    "enabled": True,
    "protocol": "openid-connect",
    "publicClient": True,
    "standardFlowEnabled": False,
    "directAccessGrantsEnabled": False,
    "serviceAccountsEnabled": False,
}
for key, expected in expected_fields.items():
    if client.get(key) != expected:
        raise SystemExit(f"{realm_path}: {client_id}.{key}={client.get(key)!r}, expected {expected!r}")

attributes = client.get("attributes") or {}
if attributes.get("oauth2.device.authorization.grant.enabled") != "true":
    raise SystemExit(f"{realm_path}: {client_id} must enable OAuth 2.0 Device Authorization Grant")

mappers = {mapper.get("name"): mapper for mapper in client.get("protocolMappers") or []}
required_mappers = {
    "groups": (
        "oidc-group-membership-mapper",
        {
            "full.path": "false",
            "access.token.claim": "true",
            "id.token.claim": "true",
            "userinfo.token.claim": "true",
            "claim.name": "groups",
            "jsonType.label": "String",
        },
    ),
    "tma-api-audience": (
        "oidc-audience-mapper",
        {
            "included.client.audience": "tma-api",
            "access.token.claim": "true",
            "id.token.claim": "false",
        },
    ),
}
for name, (mapper_type, config) in required_mappers.items():
    mapper = mappers.get(name)
    if mapper is None:
        raise SystemExit(f"{realm_path}: {client_id} is missing protocol mapper {name!r}")
    if mapper.get("protocol") != "openid-connect" or mapper.get("protocolMapper") != mapper_type:
        raise SystemExit(f"{realm_path}: {client_id} mapper {name!r} has the wrong type")
    actual_config = mapper.get("config") or {}
    for key, expected in config.items():
        if actual_config.get(key) != expected:
            raise SystemExit(
                f"{realm_path}: {client_id} mapper {name!r} config {key}="
                f"{actual_config.get(key)!r}, expected {expected!r}"
            )

with open(output_path, "w", encoding="utf-8") as stream:
    json.dump({"ifResourceExists": "OVERWRITE", "clients": [client]}, stream)
PY

chmod 0644 "$partial_import_file"

if ! docker compose --profile oidc ps --status running --services | grep -qx keycloak; then
  echo "Keycloak is not running; source CLI client configuration verification completed" >&2
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
config_file="/tmp/tma-cli-kcadm-$$.config"
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

python3 - "$live_client_file" "$CLIENT_ID" <<'PY'
import json
import sys

path, client_id = sys.argv[1:]
with open(path, encoding="utf-8") as stream:
    clients = json.load(stream)

if not isinstance(clients, list) or len(clients) != 1:
    raise SystemExit(f"live Keycloak realm: expected exactly one {client_id!r} client, found {len(clients) if isinstance(clients, list) else 'invalid response'}")

client = clients[0]
expected_fields = {
    "clientId": client_id,
    "enabled": True,
    "protocol": "openid-connect",
    "publicClient": True,
    "standardFlowEnabled": False,
    "directAccessGrantsEnabled": False,
    "serviceAccountsEnabled": False,
}
for key, expected in expected_fields.items():
    if client.get(key) != expected:
        raise SystemExit(f"live Keycloak realm: {client_id}.{key}={client.get(key)!r}, expected {expected!r}")

if (client.get("attributes") or {}).get("oauth2.device.authorization.grant.enabled") != "true":
    raise SystemExit(f"live Keycloak realm: {client_id} Device Authorization Grant is not enabled")

mappers = {mapper.get("name"): mapper for mapper in client.get("protocolMappers") or []}
checks = {
    "groups": ("oidc-group-membership-mapper", "claim.name", "groups"),
    "tma-api-audience": ("oidc-audience-mapper", "included.client.audience", "tma-api"),
}
for name, (mapper_type, config_key, expected_value) in checks.items():
    mapper = mappers.get(name)
    if mapper is None or mapper.get("protocolMapper") != mapper_type:
        raise SystemExit(f"live Keycloak realm: {client_id} mapper {name!r} is missing or has the wrong type")
    if (mapper.get("config") or {}).get(config_key) != expected_value:
        raise SystemExit(f"live Keycloak realm: {client_id} mapper {name!r} has the wrong {config_key}")

print(f"Keycloak CLI client verified: {client_id}")
PY
