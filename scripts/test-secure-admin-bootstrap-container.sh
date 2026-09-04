#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

suffix="$$-$(date +%s)"
image="gophishfr-bootstrap-test:${suffix}"
file_container="gophishfr-bootstrap-file-${suffix}"
restart_container="gophishfr-bootstrap-restart-${suffix}"
env_container="gophishfr-bootstrap-env-${suffix}"
missing_container="gophishfr-bootstrap-missing-${suffix}"
workdir="$(mktemp -d)"
secret="synthetic-container-${suffix}-value"

cleanup() {
    set +e
    for container in "${file_container}" "${restart_container}" "${env_container}" "${missing_container}"; do
        docker rm -f "${container}" >/dev/null 2>&1
    done
    docker image rm "${image}" >/dev/null 2>&1
    rm -rf "${workdir}"
}
trap cleanup EXIT

mkdir -p "${workdir}/file-state" "${workdir}/env-state" "${workdir}/missing-state"
chmod 0777 "${workdir}/file-state" "${workdir}/env-state" "${workdir}/missing-state"
printf '%s\n' "${secret}" >"${workdir}/admin-password"
chmod 0400 "${workdir}/admin-password"
python3 - "${workdir}/api-verifier-keyring.json" <<'PY'
import base64, json, os, sys
with open(sys.argv[1], "w", encoding="utf-8") as output:
    json.dump({"version": 1, "active_key_id": "bootstrap-api",
               "keys": [{"id": "bootstrap-api",
                         "key": base64.b64encode(os.urandom(32)).decode("ascii")}]}, output)
PY
chmod 0444 "${workdir}/api-verifier-keyring.json"

docker build --tag "${image}" .

docker run -d --name "${file_container}" \
    --mount "type=bind,src=${workdir}/admin-password,dst=/run/secrets/gophishfr_admin_password,readonly" \
    --mount "type=bind,src=${workdir}/api-verifier-keyring.json,dst=/run/secrets/gophishfr-api-verifier,readonly" \
    --mount "type=bind,src=${workdir}/file-state,dst=/state" \
    --env GOPHISH_INITIAL_ADMIN_PASSWORD_FILE=/run/secrets/gophishfr_admin_password \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/gophishfr-api-verifier \
    --env DB_FILE_PATH=/state/gophish.db \
    --env ADMIN_USE_TLS=false \
    "${image}" >/dev/null

for _ in $(seq 1 30); do
    if docker logs "${file_container}" 2>&1 | grep -q "Starting admin server"; then
        break
    fi
    sleep 1
done
docker logs "${file_container}" 2>&1 | grep -q "Starting admin server"
if docker logs "${file_container}" 2>&1 | grep -Fq "${secret}"; then
    echo "bootstrap value appeared in container logs" >&2
    exit 1
fi
if docker exec "${file_container}" grep -R -Fq "${secret}" /opt/gophish; then
    echo "bootstrap value appeared in application filesystem" >&2
    exit 1
fi
docker stop "${file_container}" >/dev/null

first_hash="$(python3 -c 'import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute("select hash from users where username=?", ("admin",)).fetchone()[0])' "${workdir}/file-state/gophish.db")"
if [ -z "${first_hash}" ] || [ "${first_hash}" = "${secret}" ]; then
    echo "container did not persist a bcrypt-only administrator password" >&2
    exit 1
fi

docker run -d --name "${restart_container}" \
    --mount "type=bind,src=${workdir}/file-state,dst=/state" \
    --mount "type=bind,src=${workdir}/api-verifier-keyring.json,dst=/run/secrets/gophishfr-api-verifier,readonly" \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/gophishfr-api-verifier \
    --env DB_FILE_PATH=/state/gophish.db \
    --env ADMIN_USE_TLS=false \
    "${image}" >/dev/null
for _ in $(seq 1 30); do
    if docker logs "${restart_container}" 2>&1 | grep -q "Starting admin server"; then
        break
    fi
    sleep 1
done
docker logs "${restart_container}" 2>&1 | grep -q "Starting admin server"
docker stop "${restart_container}" >/dev/null
restart_hash="$(python3 -c 'import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute("select hash from users where username=?", ("admin",)).fetchone()[0])' "${workdir}/file-state/gophish.db")"
if [ "${restart_hash}" != "${first_hash}" ]; then
    echo "container restart changed administrator hash" >&2
    exit 1
fi

docker run -d --name "${env_container}" \
    --mount "type=bind,src=${workdir}/env-state,dst=/state" \
    --mount "type=bind,src=${workdir}/api-verifier-keyring.json,dst=/run/secrets/gophishfr-api-verifier,readonly" \
    --env "GOPHISH_INITIAL_ADMIN_PASSWORD=${secret}" \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/gophishfr-api-verifier \
    --env DB_FILE_PATH=/state/gophish.db \
    --env ADMIN_USE_TLS=false \
    "${image}" >/dev/null
for _ in $(seq 1 30); do
    if docker logs "${env_container}" 2>&1 | grep -q "Starting admin server"; then
        break
    fi
    sleep 1
done
docker logs "${env_container}" 2>&1 | grep -q "Starting admin server"
if docker logs "${env_container}" 2>&1 | grep -Fq "${secret}"; then
    echo "environment bootstrap value appeared in container logs" >&2
    exit 1
fi
docker stop "${env_container}" >/dev/null

if docker run --name "${missing_container}" \
    --mount "type=bind,src=${workdir}/missing-state,dst=/state" \
    --mount "type=bind,src=${workdir}/api-verifier-keyring.json,dst=/run/secrets/gophishfr-api-verifier,readonly" \
    --env GOPHISHFR_API_KEY_VERIFIER_KEYRING_FILE=/run/secrets/gophishfr-api-verifier \
    --env DB_FILE_PATH=/state/gophish.db \
    --env ADMIN_USE_TLS=false \
    "${image}" >/dev/null 2>&1; then
    echo "fresh container without a bootstrap source succeeded" >&2
    exit 1
fi
missing_count="$(python3 -c 'import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute("select count(*) from users").fetchone()[0])' "${workdir}/missing-state/gophish.db")"
if [ "${missing_count}" -ne 0 ]; then
    echo "failed fresh container left an administrator row" >&2
    exit 1
fi

echo "secure administrator bootstrap container checks passed"
