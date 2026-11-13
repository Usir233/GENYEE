#!/usr/bin/env bash
# docker/scripts/gy-secret-fill.sh
# Fills credential values into the session browser. The fill mode holds the
# session's command lock so agent-browser calls queue behind it; the probe
# only reads the page URL and never waits.
set -u
umask 077
LIB="${GY_BROWSER_LIB_DIR:-/usr/local/lib/genyee}"
NODE="${GY_NODE:-/usr/local/bin/node}"
if [ "${1:-}" != "--probe" ]; then
  root="${GENYEE_BROWSER_ROOT:-}"
  if [ -z "$root" ]; then
    echo '{"error":"no_browser"}'
    exit 0
  fi
  mkdir -p "$root"
  exec 7>"$root/.command.lock"
  flock -w 30 7 || { echo '{"error":"browser_busy"}'; exit 0; }
fi
cd /
exec "$NODE" "$LIB/gy-secret-fill.mjs" "$@"
