#!/usr/bin/env bash
#
# verify-docs-canonical-examples.sh
#
# Deprecation guidance only works if the canonical first-party documentation
# this PR (security/deprecate-legacy-api-key-transports, #64) ships keeps
# recommending the canonical transport. This script is the local verify.sh
# entry point for that check; the same check also runs as a blocking
# "docs-guard" CI job (see .github/workflows/ci.yml), so a PR cannot
# silently drift back toward recommending a deprecated transport while
# every other required check stays green.
#
# internal/docsguard checks a fixed, small, hardcoded list of canonical
# documents this PR ships -- it does not scan every tracked Markdown file,
# and it is not a Markdown/HTML parser. See internal/docsguard's package
# doc comment for the full rationale (an explicit, iteration-17 scope
# reduction: this gate is a narrow CI assertion tool, not a runtime
# security boundary, so it checks only the documents this same PR authors
# and reviews by hand). This script therefore takes no file arguments and
# no exemption list at all: cmd/docsguard reads its own registry relative
# to the repository root.

set -euo pipefail

cd "$(dirname "$0")/.."

go run ./cmd/docsguard
