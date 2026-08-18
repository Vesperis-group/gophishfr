#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

if [ "$(uname -s)" != "Linux" ]; then
    echo "test-browser.sh: browser smoke tests currently require Linux or WSL." >&2
    exit 1
fi

corepack yarn build
go test -tags=browser ./controllers \
    -run '^TestBrowser' \
    -count=1 \
    -timeout=2m
