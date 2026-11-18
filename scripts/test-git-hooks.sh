#!/usr/bin/env bash
# Verify that git hooks enforce zero-exemption Go, Shell, and Python gates,
# cover unchanged Go callers, reject upstream pushes, and propagate failures.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# 1. Static guard: zero incremental lint flags in git-hooks or CI workflows.
forbidden_inc_flag="--new-from-""rev|--new-from-""merge-base"
if grep -rn -E -e "$forbidden_inc_flag" "$repo_root/scripts/git-hooks" "$repo_root/.github/workflows"; then
  echo "Incremental lint exemption flag found in git-hooks or workflows" >&2
  exit 1
fi

# Verify real shellcheck (both absolute-path multi-file and standalone single-file) when installed.
real_shellcheck="$(command -v shellcheck || true)"
if [[ -n "$real_shellcheck" ]]; then
  "$real_shellcheck" "$repo_root/scripts/git-hooks/"* "$repo_root/scripts/test-git-hooks.sh"
  "$real_shellcheck" "$repo_root/scripts/git-hooks/pre-commit"
  "$real_shellcheck" "$repo_root/scripts/git-hooks/pre-push"
fi

fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/scripts/git-hooks" "$fixture/bin" "$fixture/cli"
cp "$repo_root/scripts/git-hooks/common.sh" "$fixture/scripts/git-hooks/"
cp "$repo_root/scripts/git-hooks/pre-commit" "$fixture/scripts/git-hooks/"
cp "$repo_root/scripts/git-hooks/pre-push" "$fixture/scripts/git-hooks/"
chmod +x "$fixture/scripts/git-hooks/"*

export HOOK_TEST_LOG="$fixture/go.log"
export GOLANGCI_TEST_LOG="$fixture/golangci.log"
export SHELLCHECK_TEST_LOG="$fixture/shellcheck.log"
export RUFF_TEST_LOG="$fixture/ruff.log"
export LOG_FORMAT="legacy: %msg"
export GENYEE_LOG_FORMAT="developer: %msg"

cat > "$fixture/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$HOOK_TEST_LOG"
case "$1" in
  list)
    [[ "${HOOK_TEST_FAIL:-}" != list ]] || exit 1
    printf '%s\n' example/internal/agent/tools example/internal/application/service example/docreader/proto
    ;;
  vet|test|build)
    if [[ "$1" == test && ( -n "${GENYEE_LOG_FORMAT:-}" || "${GENYEE_LOG_FORMAT+x}" != x ) ]]; then
      echo "App tests inherited the developer log template" >&2
      exit 1
    fi
    [[ "${HOOK_TEST_FAIL:-}" != "$1" ]] || exit 1
    ;;
  *) echo "Unexpected go invocation: $*" >&2; exit 1 ;;
esac
EOF

cat > "$fixture/bin/golangci-lint" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s|%s\n' "$PWD" "$*" >> "$GOLANGCI_TEST_LOG"
[[ "${HOOK_TEST_FAIL:-}" != golangci ]] || exit 1
EOF

cat > "$fixture/bin/shellcheck" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$SHELLCHECK_TEST_LOG"
[[ "${HOOK_TEST_FAIL:-}" != shellcheck ]] || exit 1
EOF

cat > "$fixture/bin/ruff" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$RUFF_TEST_LOG"
[[ "${HOOK_TEST_FAIL:-}" != ruff ]] || exit 1
EOF

chmod +x "$fixture/bin/go" "$fixture/bin/golangci-lint" "$fixture/bin/shellcheck" "$fixture/bin/ruff"
export PATH="$fixture/bin:$PATH"
unset HOOK_SKIP_TEST HOOK_TEST_FAIL SKIP_HOOKS

run_checks() {
  bash -c 'source "$1"; run_app_vet_test' bash "$fixture/scripts/git-hooks/common.sh"
}

# 2. Verify run_app_vet_test covers all non-docreader packages and clears GENYEE_LOG_FORMAT.
run_checks
cat > "$fixture/expected.log" <<'EOF'
list ./...
vet example/internal/agent/tools example/internal/application/service
test -count=1 example/internal/agent/tools example/internal/application/service
EOF
diff -u "$fixture/expected.log" "$HOOK_TEST_LOG"

# Keep the explicit local opt-out, but it must not skip vet.
: > "$HOOK_TEST_LOG"
HOOK_SKIP_TEST=1 run_checks
head -n 2 "$fixture/expected.log" > "$fixture/skip.log"
diff -u "$fixture/skip.log" "$HOOK_TEST_LOG"

for phase in list vet test; do
  : > "$HOOK_TEST_LOG"
  if HOOK_TEST_FAIL="$phase" run_checks > "$fixture/failure.log" 2>&1; then
    echo "App checks accepted a failing go $phase" >&2
    exit 1
  fi
  case "$phase" in
    list) head -n 1 "$fixture/expected.log" ;;
    vet) head -n 2 "$fixture/expected.log" ;;
    test) cat "$fixture/expected.log" ;;
  esac > "$fixture/failure-expected.log"
  diff -u "$fixture/failure-expected.log" "$HOOK_TEST_LOG"
done

# 3. Verify run_golangci_if_available runs full-module lint (no incremental rev flags) and propagates failures.
: > "$GOLANGCI_TEST_LOG"
touch "$fixture/.golangci.yml"
bash -c 'source "$1"; run_golangci_if_available "."; run_golangci_if_available "cli"' \
  bash "$fixture/scripts/git-hooks/common.sh"
cat > "$fixture/golangci-expected.log" <<EOF
$fixture|run ./...
$fixture/cli|run --config=$fixture/.golangci.yml ./...
EOF
diff -u "$fixture/golangci-expected.log" "$GOLANGCI_TEST_LOG"

if HOOK_TEST_FAIL=golangci bash -c 'source "$1"; run_golangci_if_available "."' \
  bash "$fixture/scripts/git-hooks/common.sh" > "$fixture/failure.log" 2>&1; then
  echo "run_golangci_if_available accepted a failing golangci-lint run" >&2
  exit 1
fi

# 4. Verify run_shellcheck_if_available checks existing scripts, ignores missing files, and propagates failures.
: > "$SHELLCHECK_TEST_LOG"
printf '#!/usr/bin/env bash\necho ok\n' > "$fixture/sample.sh"
bash -c 'source "$1"; run_shellcheck_if_available "$2" "$3"' \
  bash "$fixture/scripts/git-hooks/common.sh" "$fixture/sample.sh" "$fixture/missing.sh"
printf -- '-x %s\n' "$fixture/sample.sh" > "$fixture/shellcheck-expected.log"
diff -u "$fixture/shellcheck-expected.log" "$SHELLCHECK_TEST_LOG"

if HOOK_TEST_FAIL=shellcheck bash -c 'source "$1"; run_shellcheck_if_available "$2"' \
  bash "$fixture/scripts/git-hooks/common.sh" "$fixture/sample.sh" > "$fixture/failure.log" 2>&1; then
  echo "run_shellcheck_if_available accepted a failing shellcheck run" >&2
  exit 1
fi

# 5. Verify run_ruff_if_available checks hand-authored Python files, skips *_pb2*.py, and propagates failures.
: > "$RUFF_TEST_LOG"
printf 'x = 1\n' > "$fixture/app.py"
printf 'y = 2\n' > "$fixture/service_pb2.py"
printf 'z = 3\n' > "$fixture/service_pb2_grpc.py"
bash -c 'source "$1"; run_ruff_if_available "$2" "$3" "$4"' \
  bash "$fixture/scripts/git-hooks/common.sh" \
  "$fixture/app.py" "$fixture/service_pb2.py" "$fixture/service_pb2_grpc.py"
cat > "$fixture/ruff-expected.log" <<EOF
check --no-cache $fixture/app.py
format --check --no-cache $fixture/app.py
EOF
diff -u "$fixture/ruff-expected.log" "$RUFF_TEST_LOG"

if HOOK_TEST_FAIL=ruff bash -c 'source "$1"; run_ruff_if_available "$2"' \
  bash "$fixture/scripts/git-hooks/common.sh" "$fixture/app.py" > "$fixture/failure.log" 2>&1; then
  echo "run_ruff_if_available accepted a failing ruff run" >&2
  exit 1
fi

# 6. End-to-end pre-commit and pre-push checks in an isolated git repository.
git_repo="$fixture/git-repo"
mkdir -p "$git_repo/scripts/git-hooks"
cp "$repo_root/scripts/git-hooks/common.sh" "$git_repo/scripts/git-hooks/"
cp "$repo_root/scripts/git-hooks/pre-commit" "$git_repo/scripts/git-hooks/"
cp "$repo_root/scripts/git-hooks/pre-push" "$git_repo/scripts/git-hooks/"
chmod +x "$git_repo/scripts/git-hooks/"*

git -C "$git_repo" init -q -b main
git -C "$git_repo" config user.name "Hook Tester"
git -C "$git_repo" config user.email "hooks@example.com"
printf 'package main\n\nfunc main() {}\n' > "$git_repo/main.go"
git -C "$git_repo" add main.go scripts/git-hooks
git -C "$git_repo" commit -q -m "initial"

: > "$GOLANGCI_TEST_LOG"
: > "$SHELLCHECK_TEST_LOG"
: > "$RUFF_TEST_LOG"
printf 'package main\n\nfunc helper() int { return 1 }\n' > "$git_repo/helper.go"
printf '#!/usr/bin/env bash\necho hi\n' > "$git_repo/tool.sh"
printf 'VALUE = 42\n' > "$git_repo/worker.py"
git -C "$git_repo" add helper.go tool.sh worker.py

"$git_repo/scripts/git-hooks/pre-commit" >/dev/null
grep -q "run ./..." "$GOLANGCI_TEST_LOG"
grep -q "$git_repo/tool.sh" "$SHELLCHECK_TEST_LOG"
grep -q "check --no-cache $git_repo/worker.py" "$RUFF_TEST_LOG"
grep -q "format --check --no-cache $git_repo/worker.py" "$RUFF_TEST_LOG"

git -C "$git_repo" commit -q -m "add helper, tool, and worker"

# Pushing to the read-only upstream remote must fail closed.
head_sha="$(git -C "$git_repo" rev-parse HEAD)"
prev_sha="$(git -C "$git_repo" rev-parse HEAD~1)"
if printf 'refs/heads/main %s refs/heads/main %s\n' "$head_sha" "$prev_sha" |
  "$git_repo/scripts/git-hooks/pre-push" upstream > "$fixture/upstream.log" 2>&1; then
  echo "pre-push allowed pushing to read-only upstream remote" >&2
  exit 1
fi

# Pushing to origin must run full Go/Shell/Python checks cleanly.
: > "$HOOK_TEST_LOG"
: > "$GOLANGCI_TEST_LOG"
: > "$SHELLCHECK_TEST_LOG"
: > "$RUFF_TEST_LOG"
printf 'refs/heads/main %s refs/heads/main %s\n' "$head_sha" "$prev_sha" |
  "$git_repo/scripts/git-hooks/pre-push" origin >/dev/null
grep -q "run ./..." "$GOLANGCI_TEST_LOG"
grep -q "build ./cmd/server" "$HOOK_TEST_LOG"
grep -q "$git_repo/tool.sh" "$SHELLCHECK_TEST_LOG"
grep -q "check --no-cache $git_repo/worker.py" "$RUFF_TEST_LOG"

echo 'Git hook coverage checks passed'
