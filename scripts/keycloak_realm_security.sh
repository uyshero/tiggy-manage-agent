#!/bin/sh
set -eu

ACTION="${1:-verify}"
REALM="${TMA_KEYCLOAK_REALM:-tma}"
REALM_FILE="${TMA_KEYCLOAK_REALM_FILE:-deploy/keycloak/tma-realm.json}"
PASSWORD_POLICY='length(12) and digits(1) and lowerCase(1) and upperCase(1) and specialChars(1) and notUsername(undefined) and passwordHistory(5)'

case "$ACTION" in
  apply|verify) ;;
  *)
    echo "usage: $0 [apply|verify]" >&2
    exit 2
    ;;
esac

verify_realm_file() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

path, expected_realm = sys.argv[1:]
with open(path, encoding="utf-8") as stream:
    realm = json.load(stream)

expected = {
    "realm": expected_realm,
    "sslRequired": "external",
    "registrationAllowed": True,
    "verifyEmail": True,
    "duplicateEmailsAllowed": False,
    "resetPasswordAllowed": True,
    "editUsernameAllowed": False,
    "bruteForceProtected": True,
    "permanentLockout": False,
    "failureFactor": 5,
    "waitIncrementSeconds": 60,
    "minimumQuickLoginWaitSeconds": 60,
    "quickLoginCheckMilliSeconds": 1000,
    "maxFailureWaitSeconds": 900,
    "maxDeltaTimeSeconds": 43200,
    "eventsEnabled": True,
    "eventsExpiration": 2592000,
    "adminEventsEnabled": True,
    "adminEventsDetailsEnabled": True,
}
for key, value in expected.items():
    actual = realm.get(key)
    if actual != value:
        raise SystemExit(f"{path}: {key}={actual!r}, expected {value!r}")

listeners = realm.get("eventsListeners") or []
if "jboss-logging" not in listeners:
    raise SystemExit(f"{path}: eventsListeners must contain jboss-logging")

policy = realm.get("passwordPolicy") or ""
required_terms = (
    "length(12)",
    "digits(1)",
    "lowerCase(1)",
    "upperCase(1)",
    "specialChars(1)",
    "notUsername(undefined)",
    "passwordHistory(5)",
)
missing = [term for term in required_terms if term not in policy]
if missing:
    raise SystemExit(f"{path}: passwordPolicy is missing {', '.join(missing)}")

print(f"Keycloak realm security verified: {expected_realm} ({path})")
PY
}

verify_realm_file "$REALM_FILE" "$REALM"

if ! docker compose --profile oidc ps --status running --services | grep -qx keycloak; then
  echo "Keycloak is not running; source realm configuration verification completed" >&2
  echo "start it with: docker compose --profile oidc up -d keycloak" >&2
  exit 1
fi

live_realm_file="$(mktemp "${TMPDIR:-/tmp}/tma-keycloak-realm.XXXXXX")"
trap 'rm -f "$live_realm_file"' EXIT INT TERM

docker compose --profile oidc exec -T keycloak sh -s -- "$ACTION" "$REALM" "$PASSWORD_POLICY" >"$live_realm_file" <<'SH'
set -eu

action="$1"
realm="$2"
password_policy="$3"
config_file="/tmp/tma-kcadm-$$.config"
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
  /opt/keycloak/bin/kcadm.sh update "realms/$realm" \
    --config "$config_file" \
    -s "sslRequired=external" \
    -s "passwordPolicy=$password_policy" \
    -s "bruteForceProtected=true" \
    -s "permanentLockout=false" \
    -s "failureFactor=5" \
    -s "waitIncrementSeconds=60" \
    -s "minimumQuickLoginWaitSeconds=60" \
    -s "quickLoginCheckMilliSeconds=1000" \
    -s "maxFailureWaitSeconds=900" \
    -s "maxDeltaTimeSeconds=43200" \
    -s "eventsEnabled=true" \
    -s "eventsExpiration=2592000" \
    -s 'eventsListeners=["jboss-logging"]' \
    -s "adminEventsEnabled=true" \
    -s "adminEventsDetailsEnabled=true" >/dev/null
fi

/opt/keycloak/bin/kcadm.sh get "realms/$realm" --config "$config_file"
SH

verify_realm_file "$live_realm_file" "$REALM"
