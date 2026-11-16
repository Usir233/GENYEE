#!/usr/bin/env bash
# check-secret-tokens.sh — fail if a real-looking credential was committed to
# the CLI's human-facing docs. An agent-first CLI's docs are where a live
# API key or JWT most easily gets pasted by accident (copying a working
# session into a how-to). Same intent as a gitleaks/doc-token pre-commit scan.
#
# Heuristic, low false-positive: a real GENYEE API key is `sk-` or `gy_`
# followed by a long high-entropy body that CONTAINS A DIGIT; placeholder words
# like `sk-specific` / `gy_example` have no digit or match placeholder markers,
# so they pass. JWTs are three base64url segments.
set -euo pipefail
cd "$(dirname "$0")/.."

# sk- or gy_ key: prefix + >=24 body chars including at least one digit.
KEY_RE='(sk-|gy_)[A-Za-z0-9_-]*[0-9][A-Za-z0-9_-]*'
JWT_RE='eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}'
TOKEN_PLACEHOLDER_RE='EXAMPLE|REDACTED|PLACEHOLDER|YOUR_|xxxx'

mapfile -d '' FILES < <(git ls-files -z 'skills/**/*.md' 'README.md' 'AGENTS.md' 'CHANGELOG.md' 'ROADMAP.md' 2>/dev/null || true)
if [ "${#FILES[@]}" -eq 0 ]; then
  echo "no doc files to scan"
  exit 0
fi

redact_line() {
  sed -E \
    -e 's/((sk-|gy_)[A-Za-z0-9_-]{4})[A-Za-z0-9_-]+/\1…REDACTED/g' \
    -e 's/(eyJ[A-Za-z0-9_-]{6})[A-Za-z0-9_.-]+/\1…REDACTED/g'
}

fail=0
for f in "${FILES[@]}"; do
  [ -f "$f" ] || continue
  while IFS=: read -r line content; do
    [ -z "$content" ] && continue
    while IFS= read -r tok; do
      [ -z "$tok" ] && continue
      [ "${#tok}" -lt 27 ] && continue
      echo "$tok" | grep -qiE "$TOKEN_PLACEHOLDER_RE" && continue
      echo "POSSIBLE API KEY in $f:$line"
      printf '  %s\n' "$content" | redact_line
      fail=1
      break
    done < <(printf '%s\n' "$content" | grep -oE "$KEY_RE" || true)

    while IFS= read -r jwt; do
      [ -z "$jwt" ] && continue
      echo "$jwt" | grep -qiE "$TOKEN_PLACEHOLDER_RE" && continue
      echo "POSSIBLE JWT in $f:$line"
      printf '  %s\n' "$content" | redact_line
      fail=1
      break
    done < <(printf '%s\n' "$content" | grep -oE "$JWT_RE" || true)
  done < <(grep -nE "(sk-|gy_)[A-Za-z0-9_-]|eyJ[A-Za-z0-9_-]" -- "$f" || true)
done

if [ "$fail" -ne 0 ]; then
  echo
  echo "A real-looking credential is committed in docs. Replace it with a"
  echo "placeholder (e.g. gy_EXAMPLE / sk-EXAMPLE / <api-key> / \$(genyee auth token))."
  exit 1
fi
echo "no committed credentials found in docs"
