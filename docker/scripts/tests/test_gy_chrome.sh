#!/usr/bin/env bash
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/lib.sh"
GYCHROME="$HERE/../gy-chrome.sh"

setup_gy_chrome() {
  TMP="$(mktemp -d)"; export TMP
  mkdir -p "$TMP/bin"
  # A recording chrome-gy: proof we fell back to the plain shim.
  cat > "$TMP/bin/chrome-gy" <<'EOF'
#!/bin/sh
echo "chromegy $*" >> "$CHROME_GY_LOG"
EOF
  chmod 0755 "$TMP/bin/chrome-gy"
  # A recording gy-display: seat 3 has a holder, everything else does not.
  cat > "$TMP/bin/gy-display" <<'EOF'
#!/bin/sh
case "$1" in
  sid-of) case "$2" in
    3) echo "sess-3"; exit 0 ;;
    *) exit 1 ;;
  esac ;;
  browser-root) [ -n "${RECORDED_ROOT:-}" ] && { echo "$RECORDED_ROOT"; exit 0; }; exit 1 ;;
  *) exit 0 ;;
esac
EOF
  chmod 0755 "$TMP/bin/gy-display"
  # A recording agent-browser: proof we reached the session browser.
  cat > "$TMP/bin/agent-browser" <<'EOF'
#!/bin/sh
echo "agentbrowser sid=$GENYEE_SESSION_ID root=$GENYEE_BROWSER_ROOT click=${GY_DESKTOP_CLICK:-} args:$*" >> "$AB_LOG"
EOF
  chmod 0755 "$TMP/bin/agent-browser"
  export GY_CHROME_SHIM="$TMP/bin/chrome-gy" GY_DISPLAY="$TMP/bin/gy-display" \
         GY_AB_CMD="$TMP/bin/agent-browser"
  export CHROME_GY_LOG="$TMP/chrome-gy.log" AB_LOG="$TMP/ab.log"
  : > "$CHROME_GY_LOG"; : > "$AB_LOG"
  export GY_SESSIONS_ROOT="$TMP/sessions"
  # The session root the launcher expects exists only for sess-3.
  mkdir -p "$TMP/sessions/sess-3.browser"
}

t_a_seat_with_a_session_opens_the_session_browser() {
  setup_gy_chrome
  DISPLAY=:3 "$GYCHROME" https://x.example
  assert_contains "reached the session browser" "sid=sess-3" "$(cat "$AB_LOG")"
  assert_contains "url forwarded" "https://x.example" "$(cat "$AB_LOG")"
  assert_contains "headed so the window lands on the seat" "--headed" "$(cat "$AB_LOG")"
  assert_contains "marked as a desktop click" "click=1" "$(cat "$AB_LOG")"
  teardown
}

t_no_seat_falls_back_to_chrome_gy() {
  setup_gy_chrome
  DISPLAY= "$GYCHROME" https://x.example
  assert_contains "fell back to the plain shim" "chromegy" "$(cat "$CHROME_GY_LOG")"
  assert_eq "session path untouched" "" "$(cat "$AB_LOG")"
  teardown
}

t_seat_zero_falls_back_to_chrome_gy() {
  setup_gy_chrome
  DISPLAY=:0 "$GYCHROME"
  assert_contains "legacy shared desktop falls back" "chromegy" "$(cat "$CHROME_GY_LOG")"
  teardown
}

t_free_seat_falls_back_to_chrome_gy() {
  setup_gy_chrome
  DISPLAY=:4 "$GYCHROME"   # the stub gy-display reports no holder for 4
  assert_contains "unheld seat falls back" "chromegy" "$(cat "$CHROME_GY_LOG")"
  teardown
}

t_a_seat_before_any_agent_browser_still_opens_the_session_browser() {
  setup_gy_chrome
  rm -rf "$GY_SESSIONS_ROOT/sess-3.browser"
  DISPLAY=:3 "$GYCHROME" https://x.example
  assert_eq "no plain Chrome" "" "$(cat "$CHROME_GY_LOG")"
  assert_contains "the shared-machine root of the session" \
    "root=$GY_SESSIONS_ROOT/sess-3.browser" "$(cat "$AB_LOG")"
  teardown
}

t_a_single_session_machine_uses_the_single_session_root() {
  setup_gy_chrome
  rm -rf "$GY_SESSIONS_ROOT"
  export GY_SINGLE_BROWSER_ROOT="$TMP/browser/session"
  DISPLAY=:3 "$GYCHROME"
  assert_eq "no plain Chrome" "" "$(cat "$CHROME_GY_LOG")"
  assert_contains "the root the agent uses on this machine" \
    "root=$TMP/browser/session" "$(cat "$AB_LOG")"
  unset GY_SINGLE_BROWSER_ROOT
  teardown
}

t_an_unsafe_session_id_falls_back_to_chrome_gy() {
  setup_gy_chrome
  cat > "$TMP/bin/gy-display" <<'EOF'
#!/bin/sh
echo "../etc"
EOF
  DISPLAY=:3 "$GYCHROME"
  assert_contains "no path outside the sessions root" "chromegy" "$(cat "$CHROME_GY_LOG")"
  assert_eq "session path untouched" "" "$(cat "$AB_LOG")"
  teardown
}

t_the_recorded_root_wins_over_the_guess() {
  setup_gy_chrome
  rm -rf "$GY_SESSIONS_ROOT"   # the guess would fall back to the single-session root
  RECORDED_ROOT="$TMP/recorded/sess-3.browser" DISPLAY=:3 "$GYCHROME" https://x.example
  assert_contains "the root the session's claim recorded" \
    "root=$TMP/recorded/sess-3.browser" "$(cat "$AB_LOG")"
  teardown
}

t_a_failed_click_tells_the_human() {
  setup_gy_chrome
  cat > "$TMP/bin/agent-browser" <<'EOF'
#!/bin/sh
exit 70
EOF
  cat > "$TMP/bin/notify" <<'EOF'
#!/bin/sh
echo "notify DISPLAY=$DISPLAY $*" >> "$NOTIFY_LOG"
EOF
  chmod 0755 "$TMP/bin/agent-browser" "$TMP/bin/notify"
  export GY_NOTIFY="$TMP/bin/notify" NOTIFY_LOG="$TMP/notify.log"
  DISPLAY=:3 "$GYCHROME" https://x.example; rc=$?
  sleep 0.3
  assert_eq "the failure code surfaces" 70 "$rc"
  assert_contains "a notice appears on the seat" "notify DISPLAY=:3" "$(cat "$NOTIFY_LOG" 2>/dev/null)"
  unset GY_NOTIFY NOTIFY_LOG
  teardown
}

t_session_log_is_written_under_browser_root_not_world_readable_tmp() {
  setup_gy_chrome
  cat > "$TMP/bin/agent-browser" <<'EOF'
#!/bin/sh
echo "agent-browser-secret-URL-diagnostic" >&2
exit 0
EOF
  chmod 0755 "$TMP/bin/agent-browser"
  rm -f /tmp/gy-chrome.log
  DISPLAY=:3 "$GYCHROME" https://x.example
  assert_eq "/tmp/gy-chrome.log is never created" 1 "$(test -e /tmp/gy-chrome.log; echo $?)"
  assert_contains "log written inside session browser root" "agent-browser-secret-URL-diagnostic" \
    "$(cat "$GY_SESSIONS_ROOT/sess-3.browser/.gy-chrome.log")"
  assert_eq "session log mode is 600" 600 "$(stat -c %a "$GY_SESSIONS_ROOT/sess-3.browser/.gy-chrome.log")"
  teardown
}

t_a_seat_with_a_session_opens_the_session_browser
t_the_recorded_root_wins_over_the_guess
t_a_failed_click_tells_the_human
t_no_seat_falls_back_to_chrome_gy
t_seat_zero_falls_back_to_chrome_gy
t_free_seat_falls_back_to_chrome_gy
t_a_seat_before_any_agent_browser_still_opens_the_session_browser
t_a_single_session_machine_uses_the_single_session_root
t_an_unsafe_session_id_falls_back_to_chrome_gy
t_session_log_is_written_under_browser_root_not_world_readable_tmp
finish
