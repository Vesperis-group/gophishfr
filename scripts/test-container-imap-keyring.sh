#!/usr/bin/env bash
set -euo pipefail

image="${1:-gophishfr:ci}"
workdir="$(mktemp -d)"
container_id=""
smtp_pid=""

cleanup() {
    if [ -n "${smtp_pid}" ] && kill -0 "${smtp_pid}" >/dev/null 2>&1; then
        kill "${smtp_pid}" >/dev/null 2>&1 || true
        wait "${smtp_pid}" 2>/dev/null || true
    fi
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
python3 - "${workdir}/wrong-keyring.json" <<'PY'
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
chmod 0400 "${workdir}/wrong-keyring.json"
chmod 0777 "${workdir}"
touch "${workdir}/gophish.db"
chmod 0666 "${workdir}/gophish.db"

# Linux bind mounts preserve numeric ownership. GitHub-hosted runners use a
# different UID from the image's non-root app user, so a runner-owned 0400
# fixture is unreadable in the container even though the mount itself is
# correct. Align only this temporary fixture with the image's app identity
# before mounting it read-only; do not broaden the keyring's permissions.
docker run --rm \
    --user 0:0 \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/keyring.json" \
    --entrypoint /usr/bin/chown \
    "${image}" \
    app:app \
    /keyring.json
docker run --rm \
    --user 0:0 \
    --mount "type=bind,src=${workdir}/wrong-keyring.json,dst=/keyring.json" \
    --entrypoint /usr/bin/chown \
    "${image}" \
    app:app \
    /keyring.json

app_uid="$(docker run --rm --entrypoint /usr/bin/id "${image}" -u app)"
app_gid="$(docker run --rm --entrypoint /usr/bin/id "${image}" -g app)"
keyring_owner="$(stat --format '%u:%g' "${workdir}/keyring.json")"
keyring_mode="$(stat --format '%a' "${workdir}/keyring.json")"
if [ "${keyring_owner}" != "${app_uid}:${app_gid}" ] ||
   [ "${keyring_mode}" != "400" ]; then
    echo "container keyring fixture is not app-owned with mode 0400" >&2
    exit 1
fi

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
sqlite3 "${workdir}/gophish.db" \
    "INSERT INTO smtp (user_id, interface_type, name, host, username, password, password_ciphertext, from_address, ignore_cert_errors) VALUES (1, 'SMTP', 'Container encrypted SMTP', 'smtp.invalid:2525', 'synthetic-container-user', 'synthetic-container-smtp-password', '', 'sender@example.test', 0), (1, 'SMTP', 'Container no-auth SMTP', 'smtp.invalid:2525', '', '', '', 'sender@example.test', 0);"

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

docker run --rm \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env DB_FILE_PATH=/data/gophish.db \
    "${image}" \
    ./docker/run.sh \
    --migrate-smtp-credentials

smtp_state="$(sqlite3 "${workdir}/gophish.db" \
    "SELECT SUM(password <> ''), SUM(password_ciphertext <> ''), SUM(instr(password_ciphertext, 'synthetic-container-smtp-password') <> 0), SUM(username = '' AND password = '' AND password_ciphertext = '') FROM smtp;")"
if [ "${smtp_state}" != "0|1|0|1" ]; then
    echo "container SMTP migration did not preserve encrypted/no-auth states" >&2
    exit 1
fi

# Re-running is a byte-preserving no-op.
smtp_ciphertext="$(sqlite3 "${workdir}/gophish.db" \
    "SELECT password_ciphertext FROM smtp WHERE name = 'Container encrypted SMTP';")"
docker run --rm \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env DB_FILE_PATH=/data/gophish.db \
    "${image}" \
    ./docker/run.sh \
    --migrate-smtp-credentials
if [ "$(sqlite3 "${workdir}/gophish.db" \
    "SELECT password_ciphertext FROM smtp WHERE name = 'Container encrypted SMTP';")" != "${smtp_ciphertext}" ]; then
    echo "idempotent container SMTP migration changed ciphertext" >&2
    exit 1
fi

# Exercise fresh writes, preservation, rotation, response secrecy, and the
# decrypt-at-use boundary through the application running in the built image.
api_key="$(sqlite3 "${workdir}/gophish.db" \
    "SELECT api_key FROM users WHERE username = 'admin' LIMIT 1;")"
if [ -z "${api_key}" ]; then
    echo "container test could not load the synthetic admin API key" >&2
    exit 1
fi

python3 - "${workdir}/smtp-auth.txt" <<'PY' &
import base64
import socket
import sys

capture_path = sys.argv[1]
server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(("127.0.0.1", 2525))
server.listen(1)
server.settimeout(30)
connection, _ = server.accept()
stream = connection.makefile("rwb", buffering=0)
stream.write(b"220 local container test SMTP\r\n")
in_data = False
for raw_line in stream:
    line = raw_line.rstrip(b"\r\n")
    if in_data:
        if line == b".":
            in_data = False
            stream.write(b"250 queued\r\n")
        continue
    command = line.decode("ascii", "replace")
    upper = command.upper()
    if upper.startswith("EHLO"):
        stream.write(b"250-localhost\r\n250 AUTH PLAIN\r\n")
    elif upper.startswith("AUTH PLAIN "):
        decoded = base64.b64decode(command.split(" ", 2)[2], validate=True)
        with open(capture_path, "wb") as capture:
            capture.write(decoded)
        stream.write(b"235 authenticated\r\n")
    elif upper.startswith("MAIL FROM") or upper.startswith("RCPT TO"):
        stream.write(b"250 ok\r\n")
    elif upper == "DATA":
        in_data = True
        stream.write(b"354 end with dot\r\n")
    elif upper == "QUIT":
        stream.write(b"221 bye\r\n")
        break
    else:
        stream.write(b"250 ok\r\n")
connection.close()
server.close()
PY
smtp_pid=$!

container_id="$(docker run --detach \
    --network host \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env GOPHISH_INITIAL_ADMIN_PASSWORD=synthetic-container-admin-password \
    --env DB_FILE_PATH=/data/gophish.db \
    --env ADMIN_LISTEN_URL=127.0.0.1:3333 \
    --env ADMIN_USE_TLS=false \
    --env PHISH_LISTEN_URL=127.0.0.1:8081 \
    "${image}" \
    ./docker/run.sh \
    --mode admin)"

api_url="http://127.0.0.1:3333"
ready=0
for _ in $(seq 1 30); do
    if curl --silent --fail \
        --header "Authorization: Bearer ${api_key}" \
        "${api_url}/api/smtp/" >/dev/null; then
        ready=1
        break
    fi
    if [ "$(docker inspect --format '{{.State.Running}}' "${container_id}")" != "true" ]; then
        break
    fi
    sleep 1
done
if [ "${ready}" -ne 1 ]; then
    docker logs "${container_id}" >&2
    echo "container API did not become ready" >&2
    exit 1
fi

api_response="$(curl --silent --show-error --fail-with-body \
    --request POST \
    --header "Authorization: Bearer ${api_key}" \
    --header "Content-Type: application/json" \
    --data '{"interface_type":"SMTP","name":"Container API SMTP","host":"127.0.0.1:2525","username":"container-api-user","password":"synthetic-container-api-password","from_address":"sender@example.test","ignore_cert_errors":false,"headers":[]}' \
    "${api_url}/api/smtp/")"
api_smtp_id="$(printf '%s' "${api_response}" | jq -er '.id')"
if printf '%s' "${api_response}" |
   grep -Eq 'password|password_ciphertext|gophishfr-cred:|container-test-key'; then
    echo "container SMTP create response exposed credential material" >&2
    exit 1
fi
api_ciphertext="$(sqlite3 "${workdir}/gophish.db" \
    "SELECT password_ciphertext FROM smtp WHERE id = ${api_smtp_id};")"
if [ -z "${api_ciphertext}" ] ||
   sqlite3 "${workdir}/gophish.db" \
       "SELECT password <> '' OR instr(password_ciphertext, 'synthetic-container-api-password') <> 0 FROM smtp WHERE id = ${api_smtp_id};" |
       grep -q '^1$'; then
    echo "container API fresh write did not store only opaque ciphertext" >&2
    exit 1
fi

curl --silent --show-error --fail-with-body \
    --request POST \
    --header "Authorization: Bearer ${api_key}" \
    --header "Content-Type: application/json" \
    --data '{"interface_type":"SMTP","name":"Container API No Auth","host":"127.0.0.1:2525","username":"","password":"","from_address":"sender@example.test","ignore_cert_errors":false,"headers":[]}' \
    "${api_url}/api/smtp/" >/dev/null
if [ "$(sqlite3 "${workdir}/gophish.db" \
    "SELECT COUNT(*) FROM smtp WHERE name = 'Container API No Auth' AND password = '' AND password_ciphertext = '';")" != "1" ]; then
    echo "container API no-auth write invented credential data" >&2
    exit 1
fi

curl --silent --show-error --fail-with-body \
    --request PUT \
    --header "Authorization: Bearer ${api_key}" \
    --header "Content-Type: application/json" \
    --data "{\"id\":${api_smtp_id},\"interface_type\":\"SMTP\",\"name\":\"Container API SMTP\",\"host\":\"127.0.0.1:2525\",\"username\":\"container-api-user\",\"password\":\"\",\"from_address\":\"sender@example.test\",\"ignore_cert_errors\":false,\"headers\":[]}" \
    "${api_url}/api/smtp/${api_smtp_id}" >/dev/null
if [ "$(sqlite3 "${workdir}/gophish.db" \
    "SELECT password_ciphertext FROM smtp WHERE id = ${api_smtp_id};")" != "${api_ciphertext}" ]; then
    echo "container API empty update did not preserve ciphertext" >&2
    exit 1
fi

api_response="$(curl --silent --show-error --fail-with-body \
    --request PUT \
    --header "Authorization: Bearer ${api_key}" \
    --header "Content-Type: application/json" \
    --data "{\"id\":${api_smtp_id},\"interface_type\":\"SMTP\",\"name\":\"Container API SMTP\",\"host\":\"127.0.0.1:2525\",\"username\":\"container-api-user\",\"password\":\"synthetic-container-rotated-password\",\"from_address\":\"sender@example.test\",\"ignore_cert_errors\":false,\"headers\":[]}" \
    "${api_url}/api/smtp/${api_smtp_id}")"
rotated_ciphertext="$(sqlite3 "${workdir}/gophish.db" \
    "SELECT password_ciphertext FROM smtp WHERE id = ${api_smtp_id};")"
if [ -z "${rotated_ciphertext}" ] || [ "${rotated_ciphertext}" = "${api_ciphertext}" ]; then
    echo "container API password rotation did not replace ciphertext" >&2
    exit 1
fi
if printf '%s' "${api_response}" |
   grep -Eq 'password|password_ciphertext|gophishfr-cred:|container-test-key'; then
    echo "container SMTP update response exposed credential material" >&2
    exit 1
fi

api_response="$(curl --silent --show-error --fail-with-body \
    --header "Authorization: Bearer ${api_key}" \
    "${api_url}/api/smtp/${api_smtp_id}")"
if printf '%s' "${api_response}" |
   grep -Eq 'password|password_ciphertext|gophishfr-cred:|container-test-key'; then
    echo "container SMTP GET response exposed credential material" >&2
    exit 1
fi

api_response="$(curl --silent --show-error --fail-with-body \
    --request POST \
    --header "Authorization: Bearer ${api_key}" \
    --header "Content-Type: application/json" \
    --data "{\"email\":\"recipient@example.test\",\"smtp\":{\"id\":${api_smtp_id},\"interface_type\":\"SMTP\",\"name\":\"Container API SMTP\",\"host\":\"127.0.0.1:2525\",\"username\":\"container-api-user\",\"password\":\"\",\"from_address\":\"sender@example.test\",\"ignore_cert_errors\":false,\"headers\":[]}}" \
    "${api_url}/api/util/send_test_email")"
wait "${smtp_pid}"
smtp_pid=""
if [ "$(python3 - "${workdir}/smtp-auth.txt" <<'PY'
import sys
value = open(sys.argv[1], "rb").read()
print(int(value == b"\0container-api-user\0synthetic-container-rotated-password"))
PY
)" != "1" ]; then
    echo "container test-email boundary did not use the decrypted rotated password" >&2
    exit 1
fi
if printf '%s' "${api_response}" |
   grep -Eq 'synthetic-container-rotated-password|password_ciphertext|gophishfr-cred:|container-test-key'; then
    echo "container test-email response exposed credential material" >&2
    exit 1
fi

# A tampered envelope must fail before the dialer opens a network connection.
sqlite3 "${workdir}/gophish.db" \
    "UPDATE smtp SET password_ciphertext = password_ciphertext || 'A' WHERE id = ${api_smtp_id};"
python3 - "${workdir}/unexpected-smtp-connection" <<'PY' &
import socket
import sys

server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(("127.0.0.1", 2525))
server.listen(1)
server.settimeout(3)
try:
    connection, _ = server.accept()
except TimeoutError:
    pass
else:
    connection.close()
    open(sys.argv[1], "w", encoding="utf-8").write("connected")
finally:
    server.close()
PY
smtp_pid=$!
http_status="$(curl --silent --output "${workdir}/tamper-response.json" \
    --write-out '%{http_code}' \
    --request POST \
    --header "Authorization: Bearer ${api_key}" \
    --header "Content-Type: application/json" \
    --data "{\"email\":\"recipient@example.test\",\"smtp\":{\"id\":${api_smtp_id},\"interface_type\":\"SMTP\",\"name\":\"Container API SMTP\",\"host\":\"127.0.0.1:2525\",\"username\":\"container-api-user\",\"password\":\"\",\"from_address\":\"sender@example.test\",\"ignore_cert_errors\":false,\"headers\":[]}}" \
    "${api_url}/api/util/send_test_email")"
wait "${smtp_pid}"
smtp_pid=""
if [ "${http_status}" != "500" ] ||
   [ -e "${workdir}/unexpected-smtp-connection" ]; then
    echo "tampered container SMTP credential did not fail before network" >&2
    exit 1
fi
if grep -Eq 'synthetic-container-rotated-password|password_ciphertext|gophishfr-cred:|container-test-key' \
    "${workdir}/tamper-response.json"; then
    echo "tampered container SMTP error exposed credential material" >&2
    exit 1
fi
sqlite3 "${workdir}/gophish.db" \
    "UPDATE smtp SET password_ciphertext = '${rotated_ciphertext}' WHERE id = ${api_smtp_id};"

docker rm --force "${container_id}" >/dev/null
container_id=""

if docker run --rm \
    --mount "type=bind,src=${workdir}/wrong-keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env DB_FILE_PATH=/data/gophish.db \
    "${image}" \
    ./docker/run.sh \
    --rollback-smtp-credentials; then
    echo "container SMTP rollback unexpectedly accepted a wrong key" >&2
    exit 1
fi
if [ "$(sqlite3 "${workdir}/gophish.db" \
    "SELECT password_ciphertext FROM smtp WHERE name = 'Container encrypted SMTP';")" != "${smtp_ciphertext}" ]; then
    echo "wrong-key container rollback changed SMTP ciphertext" >&2
    exit 1
fi

sqlite3 "${workdir}/gophish.db" \
    "UPDATE smtp SET password_ciphertext = password_ciphertext || 'A' WHERE name = 'Container encrypted SMTP';"
if docker run --rm \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env DB_FILE_PATH=/data/gophish.db \
    "${image}" \
    ./docker/run.sh \
    --rollback-smtp-credentials; then
    echo "container SMTP rollback unexpectedly accepted tampered ciphertext" >&2
    exit 1
fi
sqlite3 "${workdir}/gophish.db" \
    "UPDATE smtp SET password_ciphertext = '${smtp_ciphertext}' WHERE name = 'Container encrypted SMTP';"

docker run --rm \
    --mount "type=bind,src=${workdir}/keyring.json,dst=/run/secrets/gophishfr-credential-keyring,readonly" \
    --mount "type=bind,src=${workdir},dst=/data" \
    --env GOPHISHFR_CREDENTIAL_KEYRING_FILE=/run/secrets/gophishfr-credential-keyring \
    --env DB_FILE_PATH=/data/gophish.db \
    "${image}" \
    ./docker/run.sh \
    --rollback-smtp-credentials
smtp_rollback_state="$(sqlite3 "${workdir}/gophish.db" \
    "SELECT SUM(password <> '' AND password_ciphertext = ''), SUM(username = '' AND password = '' AND password_ciphertext = '') FROM smtp;")"
if [ "${smtp_rollback_state}" != "2|2" ]; then
    echo "container SMTP rollback did not restore the legacy/no-auth states" >&2
    exit 1
fi

if docker run --rm \
    "${image}" \
    ./docker/run.sh \
    --migrate-imap-credentials; then
    echo "offline migration unexpectedly accepted a missing keyring" >&2
    exit 1
fi
if docker run --rm \
    "${image}" \
    ./docker/run.sh \
    --migrate-smtp-credentials; then
    echo "offline SMTP migration unexpectedly accepted a missing keyring" >&2
    exit 1
fi

docker run --rm --entrypoint /bin/sh "${image}" -c \
    'test ! -e /run/secrets/gophishfr-credential-keyring'

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
