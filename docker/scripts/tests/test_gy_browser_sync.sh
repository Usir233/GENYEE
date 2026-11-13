#!/usr/bin/env bash
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
bash -n "$HERE/../gy-browser-sync.sh"
for module in cdp-cookies gy-browser-sync gy-cookie-plan gy-page-sync; do
  node --check "$HERE/../$module.mjs"
done
node --test "$HERE/test_browser_sync.mjs"

# The daemon form holds the machine lock; a second daemon exits at once.
export GY_BROWSER_AUTH_DIR="$TMP/auth" GY_BROWSER_LIB_DIR="$TMP/lib" GY_NODE="$TMP/node"
mkdir -p "$TMP/lib"
printf '#!/bin/sh\nsleep 5\n' > "$TMP/node"
chmod +x "$TMP/node"
bash "$HERE/../gy-browser-sync.sh" & first=$!
sleep 0.3
timeout 2 bash "$HERE/../gy-browser-sync.sh"
kill "$first" 2>/dev/null || true
test "$(stat -c %a "$TMP/auth")" = 700
printf '%s\n' 'ok gy-browser-sync runs one daemon per machine'
