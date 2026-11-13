#!/usr/bin/env bash
# Machine-wide login state sync for the session browsers. With no arguments it
# runs as the daemon (a second copy exits via flock); --restore-root and
# --capture are one-shot passes for the wrapper and gy-auth.
set -u
umask 077
LIB="${GY_BROWSER_LIB_DIR:-/usr/local/lib/genyee}"
AUTH="${GY_BROWSER_AUTH_DIR:-/var/lib/genyee/browser/auth}"
export GY_BROWSER_AUTH_DIR="$AUTH"
mkdir -p "$AUTH" || exit 1
chmod 0700 "$AUTH"
if [ $# -eq 0 ]; then
  exec 8>"$AUTH/.sync.lock"
  flock -n 8 || exit 0
fi
cd /
exec "${GY_NODE:-/usr/local/bin/node}" "$LIB/gy-browser-sync.mjs" "$@"
