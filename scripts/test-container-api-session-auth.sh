#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

suffix="$$-$(date +%s)"
image="${1:-gophishfr-api-session-auth:${suffix}}"
container="gophishfr-api-session-auth-${suffix}"
workdir="$(mktemp -d)"
built_image=0

cleanup() {
    set +e
    docker rm --force "${container}" >/dev/null 2>&1
    if [ "${built_image}" -eq 1 ]; then
        docker image rm "${image}" >/dev/null 2>&1
    fi
    rm -rf "${workdir}"
}
trap cleanup EXIT

if [ "$#" -eq 0 ]; then
    docker build --tag "${image}" .
    built_image=1
fi

mkdir -p "${workdir}/state"
chmod 0777 "${workdir}/state"
admin_password="synthetic-container-session-password"
admin_api_key="container-api-session-contract-token"
printf '%s\n' "${admin_password}" >"${workdir}/admin-password"
chmod 0400 "${workdir}/admin-password"

docker run --detach --name "${container}" \
    --publish 127.0.0.1::3333 \
    --mount "type=bind,src=${workdir}/admin-password,dst=/run/secrets/gophishfr_admin_password,readonly" \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --env GOPHISH_INITIAL_ADMIN_PASSWORD_FILE=/run/secrets/gophishfr_admin_password \
    --env "GOPHISH_INITIAL_ADMIN_API_TOKEN=${admin_api_key}" \
    --env DB_FILE_PATH=/state/gophish.db \
    --env ADMIN_USE_TLS=false \
    "${image}" >/dev/null

host_port="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "3333/tcp") 0).HostPort}}' "${container}")"
base_url="http://127.0.0.1:${host_port}"
ready=0
for _ in $(seq 1 60); do
    if curl --silent --fail "${base_url}/login" >/dev/null; then
        ready=1
        break
    fi
    if [ "$(docker inspect --format '{{.State.Running}}' "${container}")" != "true" ]; then
        break
    fi
    sleep 1
done
if [ "${ready}" -ne 1 ]; then
    docker logs "${container}" >&2
    echo "container admin server did not become ready" >&2
    exit 1
fi

if ! python3 - "${workdir}/state/gophish.db" "${admin_password}" <<'PY'
import bcrypt
import sqlite3
import sys

database, password = sys.argv[1:]
stored = sqlite3.connect(database).execute(
    "SELECT hash FROM users WHERE username = 'admin'"
).fetchone()
if stored is None or not bcrypt.checkpw(password.encode(), stored[0].encode()):
    raise SystemExit(1)
PY
then
    echo "container bootstrap password does not match the stored administrator hash" >&2
    exit 1
fi

jar="${workdir}/cookies.txt"
curl --silent --show-error --fail --cookie-jar "${jar}" "${base_url}/login" >/dev/null
login_status="$(curl --silent --show-error \
    --output "${workdir}/login.html" \
    --write-out '%{http_code}' \
    --cookie "${jar}" \
    --cookie-jar "${jar}" \
    --header "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "username=admin" \
    --data-urlencode "password=${admin_password}" \
    "${base_url}/login")"
if [ "${login_status}" != "302" ]; then
    echo "container login did not establish the expected session (status ${login_status})" >&2
    exit 1
fi

# Complete the existing forced-password-change lifecycle so /logout executes
# rather than redirecting back to the reset page.
reset_status="$(curl --silent --show-error \
    --output "${workdir}/password-reset.html" \
    --write-out '%{http_code}' \
    --cookie "${jar}" \
    --cookie-jar "${jar}" \
    --header "Sec-Fetch-Site: same-origin" \
    --header "Content-Type: application/x-www-form-urlencoded" \
    --data-urlencode "password=${admin_password}-changed" \
    --data-urlencode "confirm_password=${admin_password}-changed" \
    "${base_url}/reset_password")"
if [ "${reset_status}" != "302" ]; then
    echo "container password-change lifecycle failed" >&2
    exit 1
fi

for page in / /campaigns /groups /templates /landing_pages /sending_profiles /webhooks; do
    curl --silent --show-error --fail --cookie "${jar}" "${base_url}${page}" \
        --output "${workdir}/page.html"
    if grep -Fq "${admin_api_key}" "${workdir}/page.html"; then
        echo "API key appeared in a standard container page" >&2
        exit 1
    fi
done

curl --silent --show-error --fail --cookie "${jar}" \
    "${base_url}/api/groups/summary" >/dev/null

session_create_status="$(curl --silent --show-error \
    --output "${workdir}/session-create.json" \
    --write-out '%{http_code}' \
    --cookie "${jar}" \
    --header "Sec-Fetch-Site: same-origin" \
    --header "Content-Type: application/json" \
    --data '{"name":"Container session contract group","targets":[{"email":"container-session@example.invalid"}]}' \
    "${base_url}/api/groups/")"
if [ "${session_create_status}" != "201" ]; then
    echo "same-origin session API mutation failed" >&2
    exit 1
fi

cross_origin_status="$(curl --silent --show-error \
    --output "${workdir}/cross-origin.json" \
    --write-out '%{http_code}' \
    --cookie "${jar}" \
    --header "Sec-Fetch-Site: cross-site" \
    --request POST \
    "${base_url}/api/reset")"
if [ "${cross_origin_status}" != "403" ]; then
    echo "cross-site session API mutation was not rejected" >&2
    exit 1
fi

for bad_header in "Bearer invalid-container-session-value" ""; do
    if [ -n "${bad_header}" ]; then
        auth_option=(--header "Authorization: ${bad_header}")
    else
        auth_option=(--header "Authorization;")
    fi
    status="$(curl --silent --show-error \
        --output "${workdir}/invalid-explicit.json" \
        --write-out '%{http_code}' \
        --cookie "${jar}" \
        "${auth_option[@]}" \
        "${base_url}/api/groups/summary")"
    if [ "${status}" != "401" ] ||
       ! grep -qi '^content-type: application/json' <(
           curl --silent --show-error --head --cookie "${jar}" "${auth_option[@]}" \
               "${base_url}/api/groups/summary"
       ); then
        echo "explicit invalid API credential did not fail as JSON" >&2
        exit 1
    fi
done

for endpoint in \
    "${base_url}/api/groups/summary?api_key=" \
    "${base_url}/api/groups/summary"; do
    if [[ "${endpoint}" == *"?"* ]]; then
        request_options=()
    else
        request_options=(
            --request POST
            --header "Content-Type: application/x-www-form-urlencoded"
            --data "api_key="
        )
    fi
    status="$(curl --silent --show-error \
        --output "${workdir}/empty-explicit.json" \
        --write-out '%{http_code}' \
        --cookie "${jar}" \
        "${request_options[@]}" \
        "${endpoint}")"
    if [ "${status}" != "401" ]; then
        echo "empty explicit API credential fell back to the session" >&2
        exit 1
    fi
done

curl --silent --show-error --fail \
    --header "Authorization: Bearer ${admin_api_key}" \
    "${base_url}/api/groups/summary" >/dev/null

conflict_status="$(curl --silent --show-error \
    --output "${workdir}/conflict.json" \
    --write-out '%{http_code}' \
    --request POST \
    --header "Authorization: Bearer ${admin_api_key}" \
    --header "Content-Type: application/x-www-form-urlencoded" \
    --data "api_key=different-container-credential" \
    "${base_url}/api/groups/summary")"
if [ "${conflict_status}" != "401" ]; then
    echo "distinct explicit container credentials were not rejected" >&2
    exit 1
fi

curl --silent --show-error --fail --cookie "${jar}" "${base_url}/settings" \
    --output "${workdir}/settings.html"
if ! grep -Fq "${admin_api_key}" "${workdir}/settings.html"; then
    echo "dedicated settings key exposure changed unexpectedly" >&2
    exit 1
fi

curl --silent --show-error --cookie "${jar}" --cookie-jar "${jar}" \
    "${base_url}/logout" >/dev/null
logout_status="$(curl --silent --show-error \
    --output "${workdir}/logged-out.json" \
    --write-out '%{http_code}' \
    --cookie "${jar}" \
    "${base_url}/api/groups/summary")"
if [ "${logout_status}" != "401" ]; then
    echo "logged-out container session retained API authority" >&2
    exit 1
fi

if docker logs "${container}" 2>&1 | grep -Fq "${admin_api_key}"; then
    echo "container logs exposed the API key" >&2
    exit 1
fi

echo "container API session authentication contract passed"
