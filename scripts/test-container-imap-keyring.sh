#!/usr/bin/env bash
set -euo pipefail

image="${1:-gophishfr:ci}"
workdir="$(mktemp -d)"
container_id=""

cleanup() {
    if [ -n "${container_id}" ] &&
       docker inspect "${container_id}" >/dev/null 2>&1; then
        docker rm --force "${container_id}" >/dev/null
    fi
    rm -rf "${workdir}"
}
trap cleanup EXIT

python3 - "${workdir}/keyring.json" <<'PY'
import base64
import json
import os
import sys

document = {
    "version": 1,
    "active_key_id": "container-test-key",
    "keys": [{
        "id": "container-test-key",
        "key": base64.b64encode(os.urandom(32)).decode("ascii"),
    }],
}
with open(sys.argv[1], "w", encoding="utf-8") as keyring:
    json.dump(document, keyring)
PY
chmod 0400 "${workdir}/keyring.json"
chmod 0777 "${workdir}"
touch "${workdir}/gophish.db"
chmod 0666 "${workdir}/gophish.db"

docker run --rm \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env GOPHISH_INITIAL_ADMIN_PASSWORD=synthetic-container-admin-password \
    --env DB_FILE_PATH=/data/gophish.db \
    "${image}" \
    ./docker/run.sh \
    --migrate-imap-credentials

sqlite3 "${workdir}/gophish.db" \
    "INSERT INTO imap (user_id, host, port, username, password, password_ciphertext, tls, enabled, folder, imap_freq) VALUES (1, 'external-imap.invalid', 993, 'synthetic-container-user', 'synthetic-container-imap-password', '', 1, 1, 'INBOX', 60);"

docker run --rm \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env DB_FILE_PATH=/data/gophish.db \
    "${image}" \
    ./docker/run.sh \
    --migrate-imap-credentials

state="$(sqlite3 "${workdir}/gophish.db" \
    "SELECT SUM(password <> ''), SUM(password_ciphertext <> ''), SUM(instr(password_ciphertext, 'synthetic-container-imap-password') <> 0) FROM imap WHERE user_id = 1;")"
if [ "${state}" != "0|1|0" ]; then
    echo "container migration did not leave only opaque ciphertext" >&2
    exit 1
fi

if docker run --rm \
    "${image}" \
    ./docker/run.sh \
    --migrate-imap-credentials; then
    echo "offline migration unexpectedly accepted a missing keyring" >&2
    exit 1
fi

container_id="$(docker run --detach \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env DB_FILE_PATH=/data/gophish.db \
    --env GOPHISH_INITIAL_ADMIN_PASSWORD=synthetic-container-admin-password \
    "${image}")"
failed_closed=0
for _ in $(seq 1 30); do
    if docker logs "${container_id}" 2>&1 |
       grep -q "IMAP credential is unavailable for user 1"; then
        failed_closed=1
        break
    fi
    if [ "$(docker inspect --format '{{.State.Running}}' "${container_id}")" != "true" ]; then
        break
    fi
    sleep 1
done
if [ "${failed_closed}" -ne 1 ]; then
    docker logs "${container_id}" >&2
    echo "encrypted IMAP operation did not fail closed without a keyring" >&2
    exit 1
fi

docker rm --force "${container_id}" >/dev/null
container_id=""
