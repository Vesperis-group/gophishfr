#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
suffix="$$-$(date +%s)"
image="${1:-gophishfr-api-verifier:${suffix}}"
workdir="$(mktemp -d)"
built=0
containers=()

cleanup() {
    set +e
    for container in "${containers[@]}"; do
        docker rm --force "${container}" >/dev/null 2>&1
    done
    if [ "${built}" -eq 1 ]; then
        docker image rm "${image}" >/dev/null 2>&1
    fi
    rm -rf "${workdir}"
}
trap cleanup EXIT

if [ "$#" -eq 0 ]; then
    docker build --tag "${image}" .
    built=1
fi

mkdir "${workdir}/state" "${workdir}/generated-state"
chmod 0777 "${workdir}/state" "${workdir}/generated-state"
python3 - "${workdir}/keyring.json" "${workdir}/wrong-keyring.json" <<'PY'
import base64, json, os, sys
def document(active, ids):
    return {"version": 1, "active_key_id": active,
            "keys": [{"id": key_id,
                      "key": base64.b64encode(os.urandom(32)).decode("ascii")}
                     for key_id in ids]}
with open(sys.argv[1], "w", encoding="utf-8") as output:
    json.dump(document("api-active", ["api-old", "api-active"]), output)
with open(sys.argv[2], "w", encoding="utf-8") as output:
    json.dump(document("api-active", ["api-active"]), output)
PY
chmod 0444 "${workdir}/keyring.json" "${workdir}/wrong-keyring.json"

legacy_token="synthetic-container-legacy-api-token"
admin_password="synthetic-container-verifier-password"
bootstrap="gophishfr-api-bootstrap-${suffix}"
containers+=("${bootstrap}")
docker run --detach --name "${bootstrap}" \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/api-verifier,readonly" \
    --env DB_FILE_PATH=/state/gophish.db \
    --env ADMIN_USE_TLS=false \
    --env "GOPHISH_INITIAL_ADMIN_PASSWORD=${admin_password}" \
    --env "GOPHISH_INITIAL_ADMIN_API_TOKEN=${legacy_token}" \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier \
    "${image}" >/dev/null
for _ in $(seq 1 45); do
    if docker logs "${bootstrap}" 2>&1 | grep -q "Starting admin server"; then break; fi
    sleep 1
done
docker logs "${bootstrap}" 2>&1 | grep -q "Starting admin server"
docker rm --force "${bootstrap}" >/dev/null

# The bootstrap DB is owned by the image's non-root app UID. GitHub-hosted
# runners use a different UID, so make only this synthetic database
# host-writable before sqlite3 recreates a legacy row. Keep app ownership and
# verify the exact mode so both the host mutation and later non-root app writes
# remain covered without host sudo or broader keyring permissions.
app_uid="$(docker run --rm --entrypoint /usr/bin/id "${image}" -u app)"
app_gid="$(docker run --rm --entrypoint /usr/bin/id "${image}" -g app)"
docker run --rm \
    --user 0:0 \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --entrypoint /usr/bin/chmod \
    "${image}" \
    0666 \
    /state/gophish.db
database_owner="$(stat --format '%u:%g' "${workdir}/state/gophish.db")"
database_mode="$(stat --format '%a' "${workdir}/state/gophish.db")"
if [ "${database_owner}" != "${app_uid}:${app_gid}" ] ||
   [ "${database_mode}" != "666" ]; then
    echo "container verifier database fixture is not app-owned with mode 0666" >&2
    exit 1
fi

# Recreate a schema-upgraded legacy row; normal runtime must not authenticate it.
sqlite3 "${workdir}/state/gophish.db" \
    "UPDATE users SET api_key='${legacy_token}', api_key_verifier=NULL, api_key_verifier_key_id=NULL WHERE username='admin';"
legacy_state="$(sqlite3 "${workdir}/state/gophish.db" \
    "SELECT api_key='${legacy_token}', api_key_verifier IS NULL, api_key_verifier_key_id IS NULL FROM users WHERE username='admin';")"
if [ "${legacy_state}" != "1|1|1" ]; then
    echo "host sqlite3 did not recreate the synthetic legacy API-key row" >&2
    exit 1
fi

unmigrated="gophishfr-api-unmigrated-${suffix}"
containers+=("${unmigrated}")
docker run --detach --name "${unmigrated}" --publish 127.0.0.1::3333 \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/api-verifier,readonly" \
    --env DB_FILE_PATH=/state/gophish.db --env ADMIN_USE_TLS=false \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier \
    "${image}" >/dev/null
unmigrated_port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3333/tcp") 0).HostPort}}' "${unmigrated}")"
for _ in $(seq 1 45); do
    if curl --silent --fail "http://127.0.0.1:${unmigrated_port}/login" >/dev/null; then break; fi
    sleep 1
done
if [ "$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --header "Authorization: ${legacy_token}" \
    "http://127.0.0.1:${unmigrated_port}/api/users/")" != "401" ]; then
    echo "legacy plaintext authenticated before explicit migration" >&2
    exit 1
fi
docker rm --force "${unmigrated}" >/dev/null

migration_log="${workdir}/migration.log"
if ! docker run --rm \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/api-verifier,readonly" \
    --env DB_FILE_PATH=/state/gophish.db \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier \
    "${image}" ./docker/run.sh --migrate-api-keys >"${migration_log}" 2>&1; then
    cat "${migration_log}" >&2
    exit 1
fi
if grep -Fq "${legacy_token}" "${migration_log}"; then
    echo "offline migration logged an API token" >&2
    exit 1
fi
state="$(sqlite3 "${workdir}/state/gophish.db" \
    "SELECT api_key IS NULL, length(api_key_verifier), api_key_verifier_key_id FROM users WHERE username='admin';")"
if [ "${state}" != "1|32|api-active" ]; then
    echo "offline migration did not leave verifier-only storage" >&2
    exit 1
fi
docker run --rm \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/api-verifier,readonly" \
    --env DB_FILE_PATH=/state/gophish.db \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier \
    "${image}" ./docker/run.sh --migrate-api-keys 2>&1 |
    grep -q "0 rows updated, 1 rows unchanged"

runtime="gophishfr-api-runtime-${suffix}"
containers+=("${runtime}")
docker run --detach --name "${runtime}" --publish 127.0.0.1::3333 \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/api-verifier,readonly" \
    --env DB_FILE_PATH=/state/gophish.db --env ADMIN_USE_TLS=false \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier \
    "${image}" >/dev/null
port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3333/tcp") 0).HostPort}}' "${runtime}")"
base="http://127.0.0.1:${port}"
for _ in $(seq 1 45); do
    if curl --silent --fail "${base}/login" >/dev/null; then break; fi
    sleep 1
done

users_json="$(curl --silent --fail --header "Authorization: ${legacy_token}" "${base}/api/users/")"
if grep -Eq 'api_key|verifier|key_id|has_key' <<<"${users_json}"; then
    echo "GET users exposed API-key state" >&2
    exit 1
fi
create_json="$(curl --silent --fail --request POST \
    --header "Authorization: ${legacy_token}" --header 'Content-Type: application/json' \
    --data '{"username":"container-reveal-user","password":"synthetic-container-user-password","role":"user"}' \
    "${base}/api/users/")"
created_token="$(jq -r '.api_key' <<<"${create_json}")"
created_id="$(jq -r '.id' <<<"${create_json}")"
if ! grep -Eq '^[0-9a-f]{64}$' <<<"${created_token}" ||
   [ "$(grep -Fo "${created_token}" <<<"${create_json}" | wc -l)" -ne 1 ]; then
    echo "create did not reveal one generated token" >&2
    exit 1
fi
reset_json="$(curl --silent --fail --request POST \
    --header "Authorization: ${legacy_token}" "${base}/api/reset")"
reset_token="$(jq -r '.data' <<<"${reset_json}")"
if [ "$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --header "Authorization: ${legacy_token}" "${base}/api/users/")" != "401" ]; then
    echo "old token remained valid after reset" >&2
    exit 1
fi
# Query/form api_key transports were removed for 0.13.0 (see
# docs/API_KEY_TRANSPORT_DEPRECATION.md). Prove the rotated token works via
# the canonical Authorization header, and that query/form are now rejected
# rather than silently accepted.
curl --silent --fail --header "Authorization: ${reset_token}" "${base}/api/users/" >/dev/null
if [ "$(curl --silent --output /dev/null --write-out '%{http_code}' \
    "${base}/api/users/?api_key=${reset_token}")" != "401" ]; then
    echo "query api_key transport was unexpectedly accepted" >&2
    exit 1
fi
if [ "$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --request POST --data-urlencode "api_key=${reset_token}" "${base}/api/reset")" != "401" ]; then
    echo "form api_key transport was unexpectedly accepted" >&2
    exit 1
fi
docker rm --force "${runtime}" >/dev/null

# Put a verifier under the retained old pepper, then prove runtime lazy-upgrades
# it without plaintext.
python3 - "${workdir}/keyring.json" "${workdir}/state/gophish.db" "${created_id}" "${created_token}" <<'PY'
import base64, hashlib, hmac, json, sqlite3, struct, sys
doc = json.load(open(sys.argv[1], encoding="utf-8"))
key = base64.b64decode(next(item["key"] for item in doc["keys"] if item["id"] == "api-old"))
domain = b"gophishfr-api-key-verifier:v1"
token = sys.argv[4].encode()
message = struct.pack(">Q", len(domain)) + domain + struct.pack(">Q", len(token)) + token
verifier = hmac.new(key, message, hashlib.sha256).digest()
db = sqlite3.connect(sys.argv[2])
db.execute("UPDATE users SET api_key=NULL, api_key_verifier=?, api_key_verifier_key_id='api-old' WHERE id=?",
           (verifier, int(sys.argv[3])))
db.commit()
PY
lazy="gophishfr-api-lazy-${suffix}"
containers+=("${lazy}")
docker run --detach --name "${lazy}" --publish 127.0.0.1::3333 \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/api-verifier,readonly" \
    --env DB_FILE_PATH=/state/gophish.db --env ADMIN_USE_TLS=false \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier \
    "${image}" >/dev/null
lazy_port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3333/tcp") 0).HostPort}}' "${lazy}")"
for _ in $(seq 1 45); do
    if curl --silent --fail "http://127.0.0.1:${lazy_port}/login" >/dev/null; then break; fi
    sleep 1
done
curl --silent --fail --header "Authorization: ${created_token}" \
    "http://127.0.0.1:${lazy_port}/api/users/${created_id}" >/dev/null
if [ "$(sqlite3 "${workdir}/state/gophish.db" \
    "SELECT api_key_verifier_key_id FROM users WHERE id=${created_id};")" != "api-active" ]; then
    echo "old-pepper verifier was not lazily upgraded" >&2
    exit 1
fi
docker rm --force "${lazy}" >/dev/null

for keyring in missing wrong; do
    container="gophishfr-api-${keyring}-${suffix}"
    containers+=("${container}")
    args=(--detach --name "${container}" --publish 127.0.0.1::3333
          --mount "type=bind,src=${workdir}/state,dst=/state"
          --env DB_FILE_PATH=/state/gophish.db --env ADMIN_USE_TLS=false)
    if [ "${keyring}" = "wrong" ]; then
        args+=(--mount "type=bind,src=${workdir}/wrong-keyring.json,dst=/run/secrets/api-verifier,readonly"
               --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier)
    fi
    docker run "${args[@]}" "${image}" >/dev/null
    check_port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3333/tcp") 0).HostPort}}' "${container}")"
    for _ in $(seq 1 45); do
        if curl --silent --fail "http://127.0.0.1:${check_port}/login" >/dev/null; then break; fi
        sleep 1
    done
    if [ "$(curl --silent --output /dev/null --write-out '%{http_code}' \
        --header "Authorization: ${created_token}" \
        "http://127.0.0.1:${check_port}/api/users/")" != "401" ]; then
        echo "${keyring} verifier keyring did not fail API authentication closed" >&2
        exit 1
    fi
done

# Generated-token bootstrap stores only a verifier and emits no token.
generated="gophishfr-api-generated-${suffix}"
containers+=("${generated}")
docker run --detach --name "${generated}" \
    --mount "type=bind,src=${workdir}/generated-state,dst=/state" \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/api-verifier,readonly" \
    --env DB_FILE_PATH=/state/gophish.db --env ADMIN_USE_TLS=false \
    --env "GOPHISH_INITIAL_ADMIN_PASSWORD=${admin_password}" \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/api-verifier \
    "${image}" >/dev/null
for _ in $(seq 1 45); do
    if docker logs "${generated}" 2>&1 | grep -q "Starting admin server"; then break; fi
    sleep 1
done
if [ "$(sqlite3 "${workdir}/generated-state/gophish.db" \
    "SELECT api_key IS NULL AND length(api_key_verifier)=32 FROM users WHERE username='admin';")" != "1" ]; then
    echo "generated bootstrap token was not verifier-only" >&2
    exit 1
fi
if docker logs "${generated}" 2>&1 |
   grep -E "${legacy_token}|${created_token}|${reset_token}" >/dev/null; then
    echo "container logs exposed an API token" >&2
    exit 1
fi

docker run --rm --entrypoint /bin/sh "${image}" -c \
    'test ! -e /run/secrets/api-verifier'
echo "container API-key verifier lifecycle passed"
