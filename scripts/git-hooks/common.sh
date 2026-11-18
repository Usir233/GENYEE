#!/usr/bin/env bash
# Shared helpers for GENYEE git hooks (aligned with .github/workflows and ENGINEERING_STANDARDS.md).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

hook_skip() {
  [[ "${SKIP_HOOKS:-}" == "1" ]]
}

log_step() {
  printf '→ %s\n' "$*"
}

log_ok() {
  printf '✓ %s\n' "$*"
}

log_fail() {
  printf '✗ %s\n' "$*" >&2
}

# File types we expect hand-edited in commits/PRs (skip sample-data / generated blobs).
WHITESPACE_PATHSPECS=(
  '*.go' '*.py'
  '*.ts' '*.tsx' '*.vue' '*.js' '*.mjs'
  '*.yaml' '*.yml' '*.json'
  '*.sh' '*.sql'
  'Makefile'
  'frontend/package.json'
  'frontend/package-lock.json'
)

# All Go modules tracked in the repository.
GO_MODULES=(
  "."
  "cli"
  "client"
  "third_party/anydoc-go"
  "docs/poc/docker-sandbox"
)

# Best-effort fetch so merge-base matches CI (origin/main).
ensure_origin_main() {
  git -C "$ROOT" fetch origin main --quiet 2>/dev/null || true
}

merge_base() {
  ensure_origin_main
  local base
  base="$(git -C "$ROOT" merge-base HEAD origin/main 2>/dev/null || true)"
  if [[ -n "$base" ]]; then
    printf '%s\n' "$base"
    return
  fi
  base="$(git -C "$ROOT" merge-base HEAD main 2>/dev/null || true)"
  if [[ -n "$base" ]]; then
    printf '%s\n' "$base"
    return
  fi
  git -C "$ROOT" rev-parse HEAD~1
}

# Prefer the open PR's base SHA (matches GitHub CI diff) when gh is available.
pr_diff_base_ref() {
  ensure_origin_main
  if command -v gh >/dev/null 2>&1; then
    local base_sha
    base_sha="$(gh pr view --json baseRefOid --jq .baseRefOid 2>/dev/null || true)"
    if [[ -n "$base_sha" && "$base_sha" != "null" ]]; then
      printf '%s\n' "$base_sha"
      return
    fi
  fi
  if git -C "$ROOT" rev-parse --verify origin/main >/dev/null 2>&1; then
    printf '%s\n' "origin/main"
    return
  fi
  printf '%s\n' "main"
}

check_whitespace() {
  log_step "git diff --check (source files)"
  if [[ "${1:-}" == "--cached" ]]; then
    if git -C "$ROOT" diff --cached --quiet -- "${WHITESPACE_PATHSPECS[@]}"; then
      log_ok "no staged source changes to check"
      return 0
    fi
    git -C "$ROOT" diff --check --cached -- "${WHITESPACE_PATHSPECS[@]}"
  else
    if git -C "$ROOT" diff --quiet "$@" -- "${WHITESPACE_PATHSPECS[@]}"; then
      log_ok "no changed source files to check"
      return 0
    fi
    git -C "$ROOT" diff --check "$@" -- "${WHITESPACE_PATHSPECS[@]}"
  fi
  log_ok "no whitespace errors"
}

check_gofmt_files() {
  local mode="$1" # check | write
  shift
  local -a files=("$@")
  if [[ ${#files[@]} -gt 0 ]]; then
    local -a existing=()
    local f rel
    for f in "${files[@]}"; do
      if [[ -f "$f" ]]; then
        existing+=("$f")
        continue
      fi
      rel="${f#"$ROOT"/}"
      if [[ -f "$ROOT/$rel" ]]; then
        existing+=("$ROOT/$rel")
      fi
    done
    files=("${existing[@]}")
  fi
  [[ ${#files[@]} -gt 0 ]] || return 0

  local unformatted
  if [[ "$mode" == "write" ]]; then
    log_step "gofmt (auto-format staged Go files)"
    gofmt -w "${files[@]}"
    # Re-stage formatted files.
    git -C "$ROOT" add -- "${files[@]}"
    log_ok "gofmt applied"
    return 0
  fi

  log_step "gofmt (check)"
  unformatted="$(gofmt -l "${files[@]}")"
  if [[ -n "$unformatted" ]]; then
    log_fail "the following Go files are not gofmt-formatted:"
    printf '%s\n' "$unformatted" >&2
    log_fail "run: gofmt -w <files>  (or commit again after pre-commit auto-format)"
    return 1
  fi
  log_ok "gofmt"
}

# Full-module zero-exemption golangci-lint check (see ENGINEERING_STANDARDS.md §3).
run_golangci_if_available() {
  local module_dir="${1:-.}"
  if ! command -v golangci-lint >/dev/null 2>&1; then
    log_step "golangci-lint not installed; skip (install: https://golangci-lint.run)"
    return 0
  fi
  local -a cfg_args=()
  if [[ -f "$ROOT/.golangci.yml" && "$module_dir" != "." ]]; then
    cfg_args=("--config=$ROOT/.golangci.yml")
  fi
  log_step "golangci-lint run ./... ($module_dir)"
  (
    cd "$ROOT/$module_dir"
    GOMAXPROCS="${GOMAXPROCS:-2}" golangci-lint run "${cfg_args[@]}" ./...
  )
  log_ok "golangci-lint ($module_dir)"
}

run_shellcheck_if_available() {
  local -a files=("$@")
  local -a existing=()
  local f rel
  for f in "${files[@]}"; do
    if [[ -f "$f" ]]; then
      existing+=("$f")
      continue
    fi
    rel="${f#"$ROOT"/}"
    if [[ -f "$ROOT/$rel" ]]; then
      existing+=("$ROOT/$rel")
    fi
  done
  [[ ${#existing[@]} -gt 0 ]] || return 0

  if ! command -v shellcheck >/dev/null 2>&1; then
    log_step "shellcheck not installed; skip (install: https://www.shellcheck.net)"
    return 0
  fi
  log_step "shellcheck (${#existing[@]} script(s))"
  shellcheck "${existing[@]}"
  log_ok "shellcheck"
}

run_ruff_if_available() {
  local -a files=("$@")
  local -a existing=()
  local f rel base
  for f in "${files[@]}"; do
    base="$(basename "$f")"
    case "$base" in
      *_pb2.py|*_pb2.pyi|*_pb2_grpc.py) continue ;;
    esac
    if [[ -f "$f" ]]; then
      existing+=("$f")
      continue
    fi
    rel="${f#"$ROOT"/}"
    if [[ -f "$ROOT/$rel" ]]; then
      existing+=("$ROOT/$rel")
    fi
  done
  [[ ${#existing[@]} -gt 0 ]] || return 0

  if ! command -v ruff >/dev/null 2>&1; then
    log_step "ruff not installed; skip (install: https://docs.astral.sh/ruff)"
    return 0
  fi
  log_step "ruff check & format --check (${#existing[@]} Python file(s))"
  PYTHONDONTWRITEBYTECODE=1 ruff check --no-cache "${existing[@]}"
  PYTHONDONTWRITEBYTECODE=1 ruff format --check --no-cache "${existing[@]}"
  log_ok "ruff"
}

run_app_vet_test() {
  # Use the same application package set as CI, including callers of changed
  # packages. Testing only directly changed packages misses contract regressions.
  (
    cd "$ROOT"
    local package_list
    package_list="$(go list ./...)"
    local -a pkgs=()
    while IFS= read -r line; do
      [[ -z "$line" || "$line" == *"/docreader/"* ]] && continue
      pkgs+=("$line")
    done <<< "$package_list"
    if [[ ${#pkgs[@]} -eq 0 ]]; then
      log_fail "no application packages found"
      exit 1
    fi
    log_step "go vet (all app packages, like CI)"
    go vet "${pkgs[@]}"
    log_ok "go vet"
    run_go_test_packages "${pkgs[@]}"
  )
}

run_go_test_packages() {
  local -a pkgs=("$@")
  if [[ ${#pkgs[@]} -eq 0 ]]; then
    log_step "no Go packages; skip go test"
    return 0
  fi
  if [[ "${HOOK_SKIP_TEST:-}" == "1" ]]; then
    log_step "HOOK_SKIP_TEST=1; skip go test"
    return 0
  fi
  log_step "go test (${#pkgs[@]} app package(s), like CI)"
  (
    cd "$ROOT"
    # Match app.yml: a developer's custom log template must not alter assertions.
    GENYEE_LOG_FORMAT="" go test -count=1 "${pkgs[@]}"
  )
  log_ok "go test"
}

run_cli_checks() {
  log_step "cli: go vet && go test"
  (
    cd "$ROOT/cli"
    go vet ./...
    if [[ "${HOOK_SKIP_TEST:-}" == "1" ]]; then
      log_step "HOOK_SKIP_TEST=1; skip cli go test"
    else
      go test -count=1 ./...
    fi
  )
  log_ok "cli checks"
}

run_client_checks() {
  log_step "client: go vet && go test"
  (
    cd "$ROOT/client"
    go vet ./...
    if [[ "${HOOK_SKIP_TEST:-}" == "1" ]]; then
      log_step "HOOK_SKIP_TEST=1; skip client go test"
    else
      go test -count=1 ./...
    fi
  )
  log_ok "client checks"
}

run_frontend_checks() {
  log_step "frontend: verify (test, type-check, build)"
  "$ROOT/scripts/verify_frontend_pr.sh"
  log_ok "frontend checks"
}

paths_match_prefix() {
  local prefix="$1"
  shift
  local p
  for p in "$@"; do
    [[ "$p" == "$prefix"* ]] && return 0
  done
  return 1
}

# Print the Go modules (from GO_MODULES) touched by the given repo-relative paths.
detect_touched_go_modules() {
  local -a changed=("$@")
  local root_touched=0
  local cli_touched=0
  local client_touched=0
  local anydoc_touched=0
  local poc_touched=0
  local p

  for p in "${changed[@]}"; do
    if [[ "$p" == ".golangci.yml" ]]; then
      printf '%s\n' "${GO_MODULES[@]}"
      return 0
    fi
    case "$p" in
      cli/*)
        [[ "$p" == *.go || "$p" == "cli/go.mod" || "$p" == "cli/go.sum" ]] && cli_touched=1
        ;;
      client/*)
        [[ "$p" == *.go || "$p" == "client/go.mod" || "$p" == "client/go.sum" ]] && client_touched=1
        ;;
      third_party/anydoc-go/*)
        [[ "$p" == *.go || "$p" == "third_party/anydoc-go/go.mod" ]] && anydoc_touched=1
        ;;
      docs/poc/docker-sandbox/*)
        [[ "$p" == *.go || "$p" == "docs/poc/docker-sandbox/go.mod" || "$p" == "docs/poc/docker-sandbox/go.sum" ]] && poc_touched=1
        ;;
      *.go|go.mod|go.sum)
        root_touched=1
        ;;
    esac
  done

  [[ "$root_touched" -eq 1 ]] && printf '%s\n' "."
  [[ "$cli_touched" -eq 1 ]] && printf '%s\n' "cli"
  [[ "$client_touched" -eq 1 ]] && printf '%s\n' "client"
  [[ "$anydoc_touched" -eq 1 ]] && printf '%s\n' "third_party/anydoc-go"
  [[ "$poc_touched" -eq 1 ]] && printf '%s\n' "docs/poc/docker-sandbox"
  return 0
}
