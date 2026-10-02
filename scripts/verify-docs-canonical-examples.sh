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
# every supported deprecated syntax (query/form api_key, including
# percent-encoded parameter names, a raw/wrong-case Authorization header,
# a Markdown table row or "or an api_key" sentence, and ordinary
# recommendation prose such as "use the api_key query parameter") and every
# legitimate Authorization scheme this guard must leave alone (Bearer,
# Basic, Digest, Negotiate, NTLM). This script only decides *which* files
# to scan and prints the result; no Markdown parser and no new dependency
# were added.
#
# Scope: tracked Markdown documentation only (README.md, CONTRIBUTING.md,
# docs/**/*.md), except the two files that deliberately discuss the
# deprecated transports in detail: the migration guide, which shows
# deliberate old/new examples, and its own self-review, which necessarily
# quotes and describes those same examples and this guard's own detection
# rules while recording review history. That exemption list is intentionally
# this short and lives in exactly one place.

set -euo pipefail

cd "$(dirname "$0")/.."

EXEMPT_FILES=(
  "docs/API_KEY_TRANSPORT_DEPRECATION.md"
  "docs/API_KEY_TRANSPORT_DEPRECATION_SELF_REVIEW.md"
)

mapfile -t DOC_FILES < <(git ls-files '*.md' | grep -Ev '^\.goals/' || true)

EXEMPT_ARGS=()
for exempt in "${EXEMPT_FILES[@]}"; do
  EXEMPT_ARGS+=("-exempt" "${exempt}")
done

go run ./cmd/docsguard "${EXEMPT_ARGS[@]}" "${DOC_FILES[@]}"
