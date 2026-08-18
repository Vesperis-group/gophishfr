#!/usr/bin/env bash
#
# Local mirror of the blocking CI gates.
#
# Running this before pushing should make a CI failure surprising. It is
# deliberately the same commands in the same order as .github/workflows/ci.yml,
# so there is one definition of "correct" rather than two that drift.
#
# Usage:
#   ./scripts/verify.sh          # everything
#   ./scripts/verify.sh --quick  # skip the race detector (the slow gate)
#
set -euo pipefail

QUICK=0
for arg in "$@"; do
    case "${arg}" in
        --quick) QUICK=1 ;;
        -h|--help)
            sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "verify.sh: unknown argument: ${arg}" >&2
            exit 2
            ;;
    esac
done

cd "$(dirname "$0")/.."

FAILED=()

# Every gate runs even if an earlier one failed: one run should surface every
# problem, not just the first. The exit code is decided at the end.
run_gate() {
    local name="$1"
    shift
    printf '\n\033[1m==> %s\033[0m\n' "${name}"
    if "$@"; then
        printf '\033[32mok\033[0m       %s\n' "${name}"
    else
        printf '\033[31mFAILED\033[0m   %s\n' "${name}"
        FAILED+=("${name}")
    fi
}

# gofmt exits 0 even when files need formatting; the offending list is the
# actual signal, so it has to be inspected rather than trusted.
gate_gofmt() {
    local unformatted
    unformatted="$(gofmt -l .)"
    if [ -n "${unformatted}" ]; then
        echo "These files are not gofmt-formatted:"
        echo "${unformatted}"
        echo
        gofmt -d .
        return 1
    fi
    echo "All files are gofmt-formatted."
}

echo "Go toolchain: $(go version)"

run_gate "gofmt"          gate_gofmt
run_gate "golangci-lint"  golangci-lint run
run_gate "go mod verify"  go mod verify
run_gate "go vet"         go vet ./...
run_gate "go build"       go build ./...
run_gate "go test"        go test ./...

if [ "${QUICK}" -eq 0 ]; then
    run_gate "go test -race" go test -race ./...
else
    printf '\n\033[33mskipped\033[0m  go test -race (--quick)\n'
fi

# Needs network and an authenticated gh; skipped rather than failed when
# unavailable, because CI runs it unconditionally and is the authority.
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    run_gate "action pins" ./scripts/verify-action-pins.sh
else
    printf '\n\033[33mskipped\033[0m  action pins (gh not available or not authenticated; CI enforces it)\n'
fi

# Pinned to the toolchain declared in go.mod, which is the one CI scans. Run
# against a different local Go and the standard-library findings would differ
# from CI in both directions, which is worse than not running it at all.
gate_govulncheck() {
    local toolchain
    toolchain="$(awk '/^toolchain /{print $2}' go.mod)"
    if [ -z "${toolchain}" ]; then
        echo "No toolchain directive in go.mod." >&2
        return 1
    fi
    echo "Scanning with ${toolchain} (may download it on first run)."
    GOTOOLCHAIN="${toolchain}" govulncheck ./...
}

if command -v govulncheck >/dev/null 2>&1; then
    run_gate "govulncheck" gate_govulncheck
else
    printf '\n\033[33mskipped\033[0m  govulncheck (install: go install golang.org/x/vuln/cmd/govulncheck@v1.7.0)\n'
fi

echo
if [ ${#FAILED[@]} -ne 0 ]; then
    printf '\033[31m%s gate(s) failed:\033[0m %s\n' "${#FAILED[@]}" "${FAILED[*]}"
    exit 1
fi

printf '\033[32mAll gates passed.\033[0m\n'
