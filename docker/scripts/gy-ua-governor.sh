#!/usr/bin/env bash
# One machine-wide fingerprint governor: UA / Client Hints, hardware
# concurrency and egress-matched time zone for every session browser.
set -u
umask 077
LIB="${GY_MERGE_LIB_DIR:-/usr/local/lib/genyee}"
LOCK_DIR="${GY_GOVERNOR_LOCK_DIR:-/var/lib/genyee/browser/auth}"
mkdir -p "$LOCK_DIR" && chmod 0700 "$LOCK_DIR" || exit 1
exec 8>"$LOCK_DIR/.ua-governor.lock"
flock -n 8 || exit 0
cd /
exec "${GY_NODE:-/usr/local/bin/node}" "$LIB/gy-ua-governor.mjs" "$@"
