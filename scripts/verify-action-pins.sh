#!/usr/bin/env bash
#
# verify-action-pins.sh
#
# actionlint and zizmor check that GitHub Actions are pinned to a commit SHA.
# Neither checks that the SHA actually *is* the version claimed in the trailing
# comment. That gap matters: a wrong or malicious SHA carrying a reassuring
# "# v7.0.0" comment passes every other lint we run, and nobody verifies 40 hex
# characters by hand during review.
#
# This script resolves each claimed tag through the GitHub API and fails if the
# pinned SHA does not match it.
#
# Requires: gh, authenticated.

set -euo pipefail

WORKFLOW_DIR=${1:-.github/workflows}
failures=0
checked=0

if ! command -v gh >/dev/null 2>&1; then
  echo "error: gh is required" >&2
  exit 2
fi

# Resolve a tag to the commit SHA it points at, dereferencing annotated tags.
resolve_tag() {
  local repo=$1 tag=$2 sha type
  if ! read -r sha type < <(gh api "repos/${repo}/git/ref/tags/${tag}" \
        --jq '"\(.object.sha) \(.object.type)"' 2>/dev/null); then
    return 1
  fi
  if [ "${type}" = "tag" ]; then
    sha=$(gh api "repos/${repo}/git/tags/${sha}" --jq '.object.sha')
  fi
  printf '%s\n' "${sha}"
}

echo "Verifying action pins in ${WORKFLOW_DIR}"
echo

# Any remote `uses:` not pinned to 40 hex characters is fatal.
while IFS= read -r line; do
  echo "UNPINNED: ${line}"
  failures=$((failures + 1))
done < <(grep -rhoE 'uses:[[:space:]]*[^.@[:space:]][^@[:space:]]*@[^[:space:]]+' "${WORKFLOW_DIR}" \
         | grep -vE '@[0-9a-f]{40}$' || true)

while IFS= read -r entry; do
  repo=${entry%%@*}
  rest=${entry#*@}
  sha=${rest%% *}
  tag=${rest##* }

  checked=$((checked + 1))

  if ! actual=$(resolve_tag "${repo}" "${tag}"); then
    echo "UNKNOWN TAG: ${repo}@${tag} (pinned ${sha:0:12})"
    failures=$((failures + 1))
    continue
  fi

  if [ "${actual}" = "${sha}" ]; then
    echo "ok        ${repo}@${tag} -> ${sha:0:12}"
  else
    echo "MISMATCH: ${repo}@${tag} pinned=${sha:0:12} actual=${actual:0:12}"
    failures=$((failures + 1))
  fi
done < <(grep -rhoE 'uses:[[:space:]]*[^@[:space:]]+@[0-9a-f]{40}[[:space:]]*#[[:space:]]*\S+' "${WORKFLOW_DIR}" \
         | sed -E 's|uses:[[:space:]]*||; s|[[:space:]]*#[[:space:]]*| |' \
         | sort -u)

echo
if [ "${failures}" -ne 0 ]; then
  echo "FAILED: ${failures} problem(s) across ${checked} pinned action(s)"
  exit 1
fi
echo "PASSED: ${checked} pinned action(s) match their claimed version"