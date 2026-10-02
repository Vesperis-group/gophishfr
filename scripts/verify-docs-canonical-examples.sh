#!/usr/bin/env bash
#
# verify-docs-canonical-examples.sh
#
# Deprecation guidance only works if canonical, first-party documentation
# keeps recommending the canonical transport. This script is the local
# verify.sh entry point for that check; the same check also runs as a
# blocking "docs-guard" CI job (see .github/workflows/ci.yml), so a PR cannot
# reintroduce a deprecated example while every other required check stays
# green.
#
# The actual detection logic lives in internal/docsguard, with fixture-based
# positive/negative tests in internal/docsguard/docsguard_test.go covering
# every supported deprecated syntax (query api_key, curl -d/--data/
# --data-raw/--data-urlencode/-F/--form, a standalone api_key=, and a raw,
# case-insensitive Authorization header) and every legitimate Authorization
# scheme this guard must leave alone (Bearer, Basic, Digest, Negotiate,
# NTLM). This script only decides *which* files to scan and prints the
# result; no Markdown parser and no new dependency were added.
#
# Scope: tracked Markdown documentation only (README.md, CONTRIBUTING.md,
# docs/**/*.md), except the one file that deliberately documents the
# deprecated transports with deliberate old/new migration examples. That
# exemption list is intentionally this short and lives in exactly one place.

set -euo pipefail

cd "$(dirname "$0")/.."

EXEMPT_FILE="docs/API_KEY_TRANSPORT_DEPRECATION.md"

mapfile -t DOC_FILES < <(git ls-files '*.md' | grep -Ev '^\.goals/' || true)

go run ./cmd/docsguard -exempt "${EXEMPT_FILE}" "${DOC_FILES[@]}"
