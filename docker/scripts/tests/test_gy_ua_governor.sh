#!/usr/bin/env bash
# docker/scripts/tests/test_gy_ua_governor.sh
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
bash -n "$HERE/../gy-ua-governor.sh"
for module in cdp-cookies gy-fingerprint-profiles gy-egress-timezone gy-ua-governor; do
  node --check "$HERE/../$module.mjs"
done
node --test "$HERE/test_ua_governor.mjs"

# The daemon form holds the machine lock in a 0700 directory; a second daemon exits at once.
export GY_GOVERNOR_LOCK_DIR="$TMP/auth" GY_MERGE_LIB_DIR="$TMP/lib" GY_NODE="$TMP/node"
mkdir -p "$TMP/lib"
mkdir -m 0755 "$TMP/auth"
printf '#!/bin/sh\nsleep 5\n' > "$TMP/node"
chmod +x "$TMP/node"
bash "$HERE/../gy-ua-governor.sh" & first=$!
sleep 0.3
timeout 2 bash "$HERE/../gy-ua-governor.sh"
kill "$first" 2>/dev/null || true
test "$(stat -c %a "$TMP/auth")" = 700
printf '%s\n' 'ok gy-ua-governor runs one daemon per machine with 0700 lock dir'
