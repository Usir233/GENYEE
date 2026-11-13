#!/usr/bin/env bash
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/sync" <<'STUB'
#!/bin/sh
printf '%s\n' "$*" > "$GY_AUTH_TEST_LOG"
STUB
chmod +x "$TMP/sync"
export GY_SYNC_CMD="$TMP/sync" GY_AUTH_TEST_LOG="$TMP/log" GENYEE_BROWSER_ROOT="$TMP/session.browser"
bash "$HERE/../gy-auth.sh" save
test "$(cat "$TMP/log")" = "--capture $GENYEE_BROWSER_ROOT"
if bash "$HERE/../gy-auth.sh" save https://example.com 2>/dev/null; then exit 1; fi
printf '%s\n' 'ok gy-auth delegates to the browser sync capture'
