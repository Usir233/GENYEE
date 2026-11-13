#!/usr/bin/env bash
# docker/scripts/tests/test_gy_secret_fill.sh
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
bash -n "$HERE/../gy-secret-fill.sh"
node --check "$HERE/../gy-secret-fill.mjs"
node --test "$HERE/test_secret_fill.mjs"

# The fill mode holds the session's command lock; the probe never waits on it.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
printf '#!/bin/sh\necho "{\\"url\\":null}"\n' > "$TMP/node"
chmod +x "$TMP/node"
export GY_NODE="$TMP/node" GY_BROWSER_LIB_DIR="$TMP" GENYEE_BROWSER_ROOT="$TMP/br"
mkdir -p "$GENYEE_BROWSER_ROOT"
exec 9>"$GENYEE_BROWSER_ROOT/.command.lock"
flock 9
out="$(timeout 5 bash "$HERE/../gy-secret-fill.sh" --probe)"
test "$out" = '{"url":null}'
exec 9>&-
printf '%s\n' 'ok gy-secret-fill'
