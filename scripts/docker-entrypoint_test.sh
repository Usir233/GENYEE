#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/docker-entrypoint.sh"

TEST_DIR="$(mktemp -d)"
SOCKET_PATH="$TEST_DIR/docker.sock"
SOCKET_PID=""

cleanup() {
    if [ -n "$SOCKET_PID" ]; then
        kill "$SOCKET_PID" 2>/dev/null || true
        wait "$SOCKET_PID" 2>/dev/null || true
    fi
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT

python3 -c '
import socket
import sys
import time

sock = socket.socket(socket.AF_UNIX)
sock.bind(sys.argv[1])
time.sleep(60)
' "$SOCKET_PATH" &
SOCKET_PID=$!

for _ in $(seq 1 50); do
    [ -S "$SOCKET_PATH" ] && break
    sleep 0.02
done
[ -S "$SOCKET_PATH" ] || {
    echo "test setup failed: unix socket was not created" >&2
    exit 1
}

FAKE_GID=""
FAKE_MODE=""
FAKE_ACCESSIBLE=0
GOSU_CALLS=()
GROUPADD_CALLS=()
USERMOD_CALLS=()

gosu() {
    GOSU_CALLS+=("$*")
    [ "$FAKE_ACCESSIBLE" -eq 1 ]
}

stat() {
    case "$*" in
        *%g*) printf '%s\n' "$FAKE_GID" ;;
        *%a*) printf '%s\n' "$FAKE_MODE" ;;
        *) command stat "$@" ;;
    esac
}

getent() {
    if [ "$1" = "group" ] && [ "$2" = "0" ]; then
        printf 'root:x:0:\n'
        return 0
    fi
    if [ "$1" = "group" ] && [ "$2" = "1234" ]; then
        printf 'dockersock:x:1234:\n'
        return 0
    fi
    return 2
}

groupadd() {
    GROUPADD_CALLS+=("$*")
}

usermod() {
    USERMOD_CALLS+=("$*")
}

reset_case() {
    FAKE_GID="$1"
    FAKE_MODE="$2"
    FAKE_ACCESSIBLE="${3:-0}"
    GOSU_CALLS=()
    GROUPADD_CALLS=()
    USERMOD_CALLS=()
}

assert_equal() {
    local want="$1"
    local got="$2"
    local message="$3"
    if [ "$got" != "$want" ]; then
        printf 'FAIL: %s\n  want: %s\n  got:  %s\n' "$message" "$want" "$got" >&2
        exit 1
    fi
}

reset_case 0 660
unset GENYEE_ALLOW_ROOT_DOCKER_SOCK_GID
grant_docker_sock_to_appuser "$SOCKET_PATH" 2>"$TEST_DIR/gid0-error"
assert_equal "" "${USERMOD_CALLS[*]:-}" \
    "root:root 0660 socket must refuse adding appuser to gid 0 by default"
grep -q "privileged root group (gid 0)" "$TEST_DIR/gid0-error"
assert_equal "appuser test -r $SOCKET_PATH" "${GOSU_CALLS[0]:-}" \
    "socket checks should pass paths as arguments, not through sh -c"

reset_case 0 660
GENYEE_ALLOW_ROOT_DOCKER_SOCK_GID=true grant_docker_sock_to_appuser "$SOCKET_PATH"
assert_equal "-aG root appuser" "${USERMOD_CALLS[*]:-}" \
    "GENYEE_ALLOW_ROOT_DOCKER_SOCK_GID=true should opt-in to root:root 0660 socket group"

reset_case 0 600
grant_docker_sock_to_appuser "$SOCKET_PATH" 2>"$TEST_DIR/mode-error"
assert_equal "" "${USERMOD_CALLS[*]:-}" \
    "root:root 0600 socket must remain inaccessible"
grep -q "not group-readable and group-writable" "$TEST_DIR/mode-error"

reset_case 1234 660
grant_docker_sock_to_appuser "$SOCKET_PATH"
assert_equal "-aG dockersock appuser" "${USERMOD_CALLS[*]:-}" \
    "existing non-root socket groups should keep working"

reset_case 0 660 1
grant_docker_sock_to_appuser "$SOCKET_PATH"
assert_equal "" "${USERMOD_CALLS[*]:-}" \
    "an already-accessible socket should not change group membership"

# Symlink safety tests for fix_mount_ownership
MOUNT_ROOT="$TEST_DIR/mount_files"
OUTSIDE_TARGET="$TEST_DIR/outside_secret"
mkdir -p "$MOUNT_ROOT/sub"
printf 'secret\n' > "$OUTSIDE_TARGET"
ln -s "$OUTSIDE_TARGET" "$MOUNT_ROOT/sub/evil_link"
ln -s "$MOUNT_ROOT" "$TEST_DIR/top_level_symlink"

CUR_UID_GID="$(id -u):$(id -g)"
fix_mount_ownership "$TEST_DIR/top_level_symlink" "$CUR_UID_GID"
fix_mount_ownership "$MOUNT_ROOT" "99999:99999"
assert_equal "$(id -u)" "$(command stat -c '%u' "$OUTSIDE_TARGET")" \
    "fix_mount_ownership must never dereference symlinks inside mounted directories"

echo "docker-entrypoint socket permission tests: PASS"
