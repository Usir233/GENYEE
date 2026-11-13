#!/usr/bin/env bash
# Saves this session browser's current login state into the machine store now,
# instead of waiting for the next sync pass.
set -eu
[ "${1:-}" = save ] && [ $# -eq 1 ] || {
  echo 'usage: gy-auth save' >&2
  exit 2
}
[ -n "${GENYEE_BROWSER_ROOT:-}" ] || {
  echo 'gy-auth: GENYEE_BROWSER_ROOT is required' >&2
  exit 70
}
exec "${GY_SYNC_CMD:-/usr/local/bin/gy-browser-sync}" --capture "$GENYEE_BROWSER_ROOT"
