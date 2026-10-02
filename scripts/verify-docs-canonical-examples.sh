#!/usr/bin/env bash
#
# verify-docs-canonical-examples.sh
#
# Deprecation guidance only works if canonical, first-party documentation
# keeps recommending the canonical transport. This is a small grep-based
# lint, not a Markdown parser or a new dependency: it fails if a deprecated
# API-key transport pattern reappears outside the two places that are
# explicitly allowed to show it:
#
#   - docs/API_KEY_TRANSPORT_DEPRECATION.md, which documents the deprecated
#     transports and shows deliberate old/new migration examples; and
#   - any file under scripts/ or tests/, which are compatibility
#     tests/scripts, not promoted examples.
#
# Checked patterns:
#   - a query-string `api_key` parameter (`?api_key=` or `&api_key=`)
#   - a form/body `api_key` parameter (`api_key=` outside a query string,
#     or an explicit `name="api_key"` form field)
#   - a raw `Authorization` header example with no `Bearer` prefix
#
# Scope: tracked Markdown documentation only (README.md, CONTRIBUTING.md,
# docs/**/*.md). It intentionally does not scan scripts/, tests/, or Go
# source, which legitimately exercise the still-accepted legacy transports.

set -euo pipefail

cd "$(dirname "$0")/.."

EXEMPT_FILE="docs/API_KEY_TRANSPORT_DEPRECATION.md"

mapfile -t DOC_FILES < <(git ls-files '*.md' | grep -Ev '^\.goals/' | grep -v "^${EXEMPT_FILE}$" || true)

failures=0

for file in "${DOC_FILES[@]}"; do
  [ -f "${file}" ] || continue

  while IFS= read -r line; do
    echo "FORBIDDEN (query api_key): ${file}: ${line}"
    failures=$((failures + 1))
  done < <(grep -nE '[?&]api_key=' "${file}" || true)

  while IFS= read -r line; do
    echo "FORBIDDEN (form api_key): ${file}: ${line}"
    failures=$((failures + 1))
  done < <(grep -nE '(--data|-d)[[:space:]]+.?.?api_key=|name="api_key"' "${file}" || true)

  while IFS= read -r line; do
    echo "FORBIDDEN (raw Authorization, no Bearer): ${file}: ${line}"
    failures=$((failures + 1))
  done < <(grep -nE 'Authorization: ' "${file}" | grep -vE 'Authorization: Bearer' || true)
done

echo
if [ "${failures}" -ne 0 ]; then
  echo "FAILED: ${failures} deprecated API-key transport example(s) found outside" \
       "${EXEMPT_FILE}, scripts/, and tests/." >&2
  exit 1
fi
echo "PASSED: no canonical documentation reintroduces query/form api_key or raw Authorization examples."
