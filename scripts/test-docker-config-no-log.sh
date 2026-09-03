#!/usr/bin/env bash
set -euo pipefail

image="${1:-gophishfr:ci}"
suffix="$$-$(date +%s)"
workdir="$(mktemp -d)"
declare -a containers=()

db_user="db-user-${suffix}"
db_password="db-password-${suffix}"
dsn_marker="dsn-secret-${suffix}"
config_marker="config-value-${suffix}@example.invalid"
bootstrap_marker="bootstrap-value-${suffix}"
sqlite_marker="sqlite-path-${suffix}"
mysql_dsn="${db_user}:${db_password}@tcp(127.0.0.1:1)/gophish?token=${dsn_marker}"
postgres_dsn="postgres://${db_user}:${db_password}@127.0.0.1:1/gophish?application_name=${dsn_marker}"

cleanup() {
    set +e
    for container in "${containers[@]}"; do
        docker rm --force "${container}" >/dev/null 2>&1
    done
    rm -rf "${workdir}"
}
trap cleanup EXIT

capture_docker_logs() {
    local container="$1"
    local prefix="$2"

    docker logs "${container}" >"${prefix}.stdout" 2>"${prefix}.stderr"
    docker logs "${container}" >"${prefix}.docker" 2>&1
}

assert_no_config_output() {
    local label="$1"
    shift
    local output needle
    local -a forbidden=(
        "${db_user}"
        "${db_password}"
        "${dsn_marker}"
        "${config_marker}"
        "${bootstrap_marker}"
        "${sqlite_marker}"
        '"db_path"'
        '"admin_server"'
        "Runtime configuration:"
    )

    for output in "$@"; do
        for needle in "${forbidden[@]}"; do
            if grep -Fq -- "${needle}" "${output}"; then
                echo "${label} exposed configuration content" >&2
                exit 1
            fi
        done
    done
}

assert_started_diagnostic() {
    local label="$1"
    local output="$2"

    if ! grep -Fq "Starting GophishFR" "${output}"; then
        echo "${label} omitted the static startup diagnostic" >&2
        exit 1
    fi
}

cat >"${workdir}/gophishfr-stub" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if ! jq -e \
    --arg db_name "${DB_NAME}" \
    --arg db_path "${DB_FILE_PATH}" \
    --arg contact_address "${CONTACT_ADDRESS}" \
    '.db_name == $db_name and
     .db_path == $db_path and
     .contact_address == $contact_address' \
    config.json >/dev/null; then
    echo "configuration mutation did not reach the application boundary" >&2
    exit 1
fi

echo "Configuration mutation loaded successfully"
EOF
chmod 0755 "${workdir}/gophishfr-stub"

# Exercise credential-bearing MySQL and PostgreSQL connection strings through
# the real entrypoint without allowing the application to contact a database.
for backend in mysql postgres; do
    if [ "${backend}" = "mysql" ]; then
        dsn="${mysql_dsn}"
    else
        dsn="${postgres_dsn}"
    fi
    container="gophishfr-config-${backend}-${suffix}"
    containers+=("${container}")
    docker create \
        --name "${container}" \
        --mount "type=bind,src=${workdir}/gophishfr-stub,dst=/opt/gophish/gophishfr,readonly" \
        --env "DB_NAME=${backend}" \
        --env "DB_FILE_PATH=${dsn}" \
        --env "CONTACT_ADDRESS=${config_marker}" \
        "${image}" >/dev/null

    docker start --attach "${container}" \
        >"${workdir}/${backend}.attached.stdout" \
        2>"${workdir}/${backend}.attached.stderr"
    capture_docker_logs "${container}" "${workdir}/${backend}"
    assert_no_config_output \
        "${backend} entrypoint" \
        "${workdir}/${backend}.attached.stdout" \
        "${workdir}/${backend}.attached.stderr" \
        "${workdir}/${backend}.stdout" \
        "${workdir}/${backend}.stderr" \
        "${workdir}/${backend}.docker"
    assert_started_diagnostic "${backend} entrypoint" "${workdir}/${backend}.docker"
done

# Start the real application with SQLite to prove config mutation, parsing,
# database initialization, and normal application diagnostics still work.
mkdir "${workdir}/state"
chmod 0777 "${workdir}/state"
sqlite_container="gophishfr-config-sqlite-${suffix}"
containers+=("${sqlite_container}")
docker create \
    --name "${sqlite_container}" \
    --mount "type=bind,src=${workdir}/state,dst=/state" \
    --env "DB_FILE_PATH=/state/${sqlite_marker}.db" \
    --env "CONTACT_ADDRESS=${config_marker}" \
    --env "GOPHISH_INITIAL_ADMIN_PASSWORD=${bootstrap_marker}" \
    --env ADMIN_USE_TLS=false \
    "${image}" >/dev/null
docker start "${sqlite_container}" >/dev/null

ready=0
for _ in $(seq 1 30); do
    if docker logs "${sqlite_container}" 2>&1 | grep -Fq "Starting admin server"; then
        ready=1
        break
    fi
    if [ "$(docker inspect --format '{{.State.Running}}' "${sqlite_container}")" != "true" ]; then
        break
    fi
    sleep 1
done
capture_docker_logs "${sqlite_container}" "${workdir}/sqlite"
if [ "${ready}" -ne 1 ]; then
    echo "SQLite container did not reach application startup" >&2
    exit 1
fi
if [ ! -s "${workdir}/state/${sqlite_marker}.db" ]; then
    echo "SQLite database was not initialized" >&2
    exit 1
fi
assert_no_config_output \
    "SQLite startup" \
    "${workdir}/sqlite.stdout" \
    "${workdir}/sqlite.stderr" \
    "${workdir}/sqlite.docker"
assert_started_diagnostic "SQLite startup" "${workdir}/sqlite.docker"
if ! grep -Fq "Starting admin server" "${workdir}/sqlite.docker"; then
    echo "SQLite startup omitted the application diagnostic" >&2
    exit 1
fi

# A malformed file must fail without reflecting its body, keys, or values.
malformed_marker="malformed-value-${suffix}"
printf '{"db_path":"%s","config_marker":"%s",BROKEN}\n' \
    "${malformed_marker}" "${config_marker}" >"${workdir}/malformed.json"
chmod 0444 "${workdir}/malformed.json"
malformed_container="gophishfr-config-malformed-${suffix}"
containers+=("${malformed_container}")
docker create \
    --name "${malformed_container}" \
    --mount "type=bind,src=${workdir}/malformed.json,dst=/opt/gophish/config.json,readonly" \
    "${image}" >/dev/null
set +e
docker start --attach "${malformed_container}" \
    >"${workdir}/malformed.attached.stdout" \
    2>"${workdir}/malformed.attached.stderr"
malformed_status=$?
set -e
if [ "${malformed_status}" -eq 0 ]; then
    echo "malformed configuration unexpectedly started" >&2
    exit 1
fi
capture_docker_logs "${malformed_container}" "${workdir}/malformed"
assert_no_config_output \
    "malformed configuration" \
    "${workdir}/malformed.attached.stdout" \
    "${workdir}/malformed.attached.stderr" \
    "${workdir}/malformed.stdout" \
    "${workdir}/malformed.stderr" \
    "${workdir}/malformed.docker"
for output in \
    "${workdir}/malformed.attached.stdout" \
    "${workdir}/malformed.attached.stderr" \
    "${workdir}/malformed.stdout" \
    "${workdir}/malformed.stderr" \
    "${workdir}/malformed.docker"; do
    if grep -Fq -- "${malformed_marker}" "${output}" ||
       grep -Fq '"config_marker"' "${output}"; then
        echo "malformed configuration was reflected in output" >&2
        exit 1
    fi
done
assert_started_diagnostic "malformed configuration" "${workdir}/malformed.docker"

echo "Docker configuration no-log checks passed"
