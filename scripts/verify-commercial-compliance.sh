#!/usr/bin/env bash
# ==============================================================================
# verify-commercial-compliance.sh
#
# Unified Commercial Compliance & Dependency Hygiene Verification Suite
# Checks selected license, configuration and source-delivery requirements.
# This engineering suite is not a legal determination or a full runtime audit.
#
# Covers 5 Tiers:
#   Tier 1: Red-Line Licenses & Notices (Dataset, GPL/AGPL Removal, Notices & Dockerfiles)
#   Tier 2: Functional & Clean-Room Replacements (.xls, .epub, VectorDB Tokenizer & Guards)
#   Tier 3: Default-Off Cloud/Telemetry & Third-Party Publisher Gating (Chrome/Claw, Langfuse, Auto-Update)
#   Tier 4: Brand Residue & Image Parameterization (Desktop Metadata, SVGs, READMEs, Swagger, Registry)
#   Tier 5: Offline Bundle, Source Export & Hygiene (Air-Gapped Bundle, Clean Source Archive, Path Hygiene)
# ==============================================================================

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Color codes (disabled if NO_COLOR is set or non-tty)
if [[ -t 1 ]] && [[ -z "${NO_COLOR:-}" ]]; then
    C_RESET='\033[0m'
    C_BOLD='\033[1m'
    C_GREEN='\033[32m'
    C_RED='\033[31m'
    C_YELLOW='\033[33m'
    C_BLUE='\033[34m'
    C_CYAN='\033[36m'
else
    C_RESET=''
    C_BOLD=''
    C_GREEN=''
    C_RED=''
    C_YELLOW=''
    C_BLUE=''
    C_CYAN=''
fi

# Options & Counters
TARGET_TIER=""
VERBOSE=0
BAIL_ON_FAIL=0

TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0
SKIPPED_TESTS=0
FAILED_TEST_LIST=()

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Unified Commercial Compliance & Dependency Hygiene Verification Suite

Options:
  --tier <1|2|3|4|5> Run tests only for the specified Tier (1-5)
  --all              Run all 5 tiers (default)
  -v, --verbose      Display command stdout and stderr during test execution
  -b, --bail         Stop execution immediately on first test failure
  -h, --help         Show this help message and exit

Examples:
  bash scripts/verify-commercial-compliance.sh
  bash scripts/verify-commercial-compliance.sh --tier 1
  bash scripts/verify-commercial-compliance.sh --tier 5
  bash scripts/verify-commercial-compliance.sh --verbose --bail
EOF
}

# Parse CLI Arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --tier)
            if [[ -n "${2:-}" ]] && [[ "$2" =~ ^[1-5]$ ]]; then
                TARGET_TIER="$2"
                shift 2
            else
                echo -e "${C_RED}Error: --tier requires an argument between 1 and 5${C_RESET}" >&2
                exit 2
            fi
            ;;
        --tier=*)
            tier_val="${1#*=}"
            if [[ "$tier_val" =~ ^[1-5]$ ]]; then
                TARGET_TIER="$tier_val"
                shift
            else
                echo -e "${C_RED}Error: --tier requires an argument between 1 and 5${C_RESET}" >&2
                exit 2
            fi
            ;;
        --all)
            TARGET_TIER=""
            shift
            ;;
        -v|--verbose)
            VERBOSE=1
            shift
            ;;
        -b|--bail)
            BAIL_ON_FAIL=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo -e "${C_RED}Unknown option: $1${C_RESET}" >&2
            usage
            exit 2
            ;;
    esac
done

# Test Runner Helper
run_test() {
    local test_id="$1"
    local description="$2"
    local command_fn="$3"

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    printf "  %-12s %-62s " "[${test_id}]" "${description}..."

    local output
    local exit_code=0
    output=$($command_fn 2>&1) || exit_code=$?

    if [[ $exit_code -eq 0 ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        printf "${C_GREEN}${C_BOLD}[PASS]${C_RESET}\n"
        if [[ $VERBOSE -eq 1 ]] && [[ -n "${output}" ]]; then
            echo -e "${C_CYAN}--- Command Output ---${C_RESET}"
            echo "${output}" | sed 's/^/    /'
            echo -e "${C_CYAN}----------------------${C_RESET}"
        fi
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        FAILED_TEST_LIST+=("${test_id}: ${description}")
        printf "${C_RED}${C_BOLD}[FAIL]${C_RESET}\n"
        echo -e "${C_RED}    [ERROR] Command failed with exit code ${exit_code}:${C_RESET}"
        echo "${output}" | sed 's/^/    /'
        if [[ $BAIL_ON_FAIL -eq 1 ]]; then
            echo -e "\n${C_RED}${C_BOLD}[BAIL] Stopping immediately on first test failure.${C_RESET}"
            print_summary
            exit 1
        fi
    fi
}

# Resolve Python & uv runner helper
get_pyarrow_runner() {
    if python3 -c "import pyarrow.parquet" >/dev/null 2>&1; then
        echo "python3"
    elif command -v uv >/dev/null 2>&1; then
        echo "uv run --with pyarrow python3"
    else
        echo ""
    fi
}

get_dataset_test_runner() {
    if python3 -c "import pandas, pyarrow, openai, pytest" >/dev/null 2>&1; then
        echo "python3 -m pytest"
    elif [ -x docreader/.venv/bin/python ] && docreader/.venv/bin/python -c "import pandas, pyarrow, openai, pytest" >/dev/null 2>&1; then
        echo "docreader/.venv/bin/python -m pytest"
    elif command -v uv >/dev/null 2>&1; then
        echo "uv run --with pandas --with pyarrow --with openai --with pytest python3 -m pytest"
    else
        echo ""
    fi
}

# ==============================================================================
# TIER 1: Red-Line Licenses & Notices
# ==============================================================================

# TC-T1-01: Parquet sample files existence and non-zero size
test_t1_parquet_exists() {
    local missing=0
    for f in queries.parquet corpus.parquet answers.parquet qrels.parquet qas.parquet; do
        if [[ ! -s "dataset/samples/${f}" ]]; then
            echo "Missing or zero-size parquet file: dataset/samples/${f}" >&2
            missing=1
        fi
    done
    return $missing
}

# TC-T1-02: Parquet schema conformance via PyArrow (int64 IDs, string text)
test_t1_parquet_schemas() {
    local py_runner
    py_runner="$(get_pyarrow_runner)"
    if [[ -z "${py_runner}" ]]; then
        echo "Neither python3 with pyarrow nor uv found" >&2
        return 1
    fi

    $py_runner - <<'EOF'
import pyarrow.parquet as pq
import sys

expected = {
    "queries.parquet": [("id", "int64"), ("text", "string")],
    "corpus.parquet": [("id", "int64"), ("text", "string")],
    "answers.parquet": [("id", "int64"), ("text", "string")],
    "qrels.parquet": [("qid", "int64"), ("pid", "int64")],
    "qas.parquet": [("qid", "int64"), ("aid", "int64")]
}

for fname, cols in expected.items():
    table = pq.read_table(f"dataset/samples/{fname}")
    schema = table.schema
    for col_name, exp_type in cols:
        assert col_name in schema.names, f"{fname} missing expected column: {col_name}"
        field = schema.field(col_name)
        actual_type = str(field.type)
        assert exp_type in actual_type, f"{fname}.{col_name}: expected {exp_type}, got {actual_type}"
print("All 5 Parquet schemas conform to int64 IDs and string fields.")
EOF
}

# TC-T1-03: Zero MS MARCO strings in Parquet files
test_t1_parquet_zero_msmarco() {
    local matches
    matches=$(strings dataset/samples/*.parquet | grep -E -i "freebsd|wp\.exe|ms marco|mmarco" || true)
    if [[ -n "${matches}" ]]; then
        echo "Forbidden strings found in evaluation parquet files:" >&2
        echo "${matches}" | head -n 10 >&2
        return 1
    fi
    return 0
}

# TC-T1-04: Synthetic dataset provenance documented in README & zero MS MARCO in generator
test_t1_docs_zero_msmarco() {
    local matches
    matches=$(grep -rn -E -i "ms marco|mmarco" dataset/qa_dataset.py || true)
    if [[ -n "${matches}" ]]; then
        echo "Forbidden MS MARCO references found in dataset/qa_dataset.py:" >&2
        echo "${matches}" >&2
        return 1
    fi
    if ! grep -q "synthetic evaluation samples authored by GENYEE" dataset/README; then
        echo "dataset/README missing synthetic dataset provenance documentation" >&2
        return 1
    fi
    if ! grep -q "由 GENYEE 编写的合成评估样本" dataset/README_zh.md; then
        echo "dataset/README_zh.md missing synthetic dataset provenance documentation" >&2
        return 1
    fi
    return 0
}

# TC-T1-05: Go dataset service GetDatasetByID unit test
test_t1_go_dataset_service() {
    GOFLAGS=-mod=readonly go test -v ./internal/application/service -run TestDataset
}

# TC-T1-06: Python dataset CLI model unit tests
test_t1_python_dataset_tests() {
    local runner
    runner="$(get_dataset_test_runner)"
    if [[ -z "${runner}" ]]; then
        echo "Neither python3 with test dependencies nor uv found" >&2
        return 1
    fi
    $runner dataset/tests/ -q
}

# TC-T1-07: Docreader textract removal verification
test_t1_docreader_no_textract() {
    local matches
    matches=$(grep -rn "textract" docreader/pyproject.toml docreader/uv.lock docreader/parser/ || true)
    if [[ -n "${matches}" ]]; then
        echo "Forbidden textract references found in docreader:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T1-08: No GPL-3.0 xlsReader in the statically linked Go binaries
test_t1_go_no_gpl_xlsreader() {
    local matches
    matches=$(grep -n "shakinm/xlsReader" go.mod go.sum || true)
    matches+=$(git grep -n "shakinm/xlsReader" -- '*.go' || true)
    if [[ -n "${matches}" ]]; then
        echo "GPL-3.0 github.com/shakinm/xlsReader is referenced:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T1-09: No AGPL-3.0 ebooklib in the docreader service
test_t1_docreader_no_agpl_ebooklib() {
    local matches
    matches=$(grep -n -i "ebooklib" docreader/pyproject.toml docreader/uv.lock || true)
    matches+=$(git grep -n -E "^\s*(import|from)\s+ebooklib" -- 'docreader/*.py' || true)
    if [[ -n "${matches}" ]]; then
        echo "AGPL-3.0 ebooklib is referenced by docreader:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T1-10: scripts/check-license-bundle.sh guards shakinm/xlsReader and passes
test_t1_check_license_bundle() {
    grep -q "shakinm/xlsReader" scripts/check-license-bundle.sh || {
        echo "scripts/check-license-bundle.sh missing shakinm/xlsReader guard" >&2
        return 1
    }
    bash scripts/check-license-bundle.sh
}

# TC-T1-11: scripts/test-license-bundle.sh execution
test_t1_test_license_bundle() {
    bash scripts/test-license-bundle.sh
}

# TC-T1-12: Root LICENSE is GENYEE Commercial License & THIRD_PARTY_NOTICES.md preserves upstream MIT + Written Offer
test_t1_license_and_notices() {
    if ! grep -q "GENYEE Commercial License" LICENSE; then
        echo "Root LICENSE does not contain 'GENYEE Commercial License'" >&2
        return 1
    fi
    if ! grep -q "Tencent" THIRD_PARTY_NOTICES.md || ! grep -q "MIT License" THIRD_PARTY_NOTICES.md; then
        echo "THIRD_PARTY_NOTICES.md does not preserve upstream Tencent MIT license notice" >&2
        return 1
    fi
    if ! grep -qi "Written Offer" THIRD_PARTY_NOTICES.md; then
        echo "THIRD_PARTY_NOTICES.md does not include Written Offer for source code" >&2
        return 1
    fi
    return 0
}

# TC-T1-13: All 7 Dockerfiles copy LICENSE and THIRD_PARTY_NOTICES.md
test_t1_dockerfiles_copy_notices() {
    # COPY sources must exist in each Docker build context, not just appear as
    # words in a Dockerfile. Keep distributed notices synchronized.
    for context in frontend mcp-server website-docs; do
        test -s "${context}/LICENSE" || return 1
        cmp THIRD_PARTY_NOTICES.md "${context}/THIRD_PARTY_NOTICES.md" || return 1
    done
    if ! grep -q "copy-licenses.sh /license-bundle" docker/Dockerfile.app || \
       ! grep -q "COPY --from=builder /license-bundle/ ./" docker/Dockerfile.app || \
       ! grep -q "LICENSE" scripts/copy-licenses.sh || \
       ! grep -q "THIRD_PARTY_NOTICES.md" scripts/copy-licenses.sh; then
        echo "docker/Dockerfile.app does not bundle LICENSE and THIRD_PARTY_NOTICES.md via scripts/copy-licenses.sh" >&2
        return 1
    fi
    local dockerfiles=(
        "docker/Dockerfile.docreader"
        "docker/Dockerfile.sandbox"
        "docker/Dockerfile.odl-hybrid"
        "frontend/Dockerfile"
        "mcp-server/Dockerfile"
        "website-docs/Dockerfile"
    )
    local missing=0
    for df in "${dockerfiles[@]}"; do
        if [[ ! -f "${df}" ]]; then
            echo "Missing Dockerfile: ${df}" >&2
            missing=1
            continue
        fi
        if ! grep -q "LICENSE" "${df}" || ! grep -q "THIRD_PARTY_NOTICES.md" "${df}"; then
            echo "Dockerfile ${df} does not copy both LICENSE and THIRD_PARTY_NOTICES.md" >&2
            missing=1
        fi
    done
    return $missing
}

run_tier_1() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 1] Red-Line Licenses & Notices${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T1-01" "Parquet evaluation dataset existence (5 files)" test_t1_parquet_exists
    run_test "TC-T1-02" "Parquet schema compliance (int64 IDs, string text)" test_t1_parquet_schemas
    run_test "TC-T1-03" "Zero non-commercial MS MARCO strings in Parquet files" test_t1_parquet_zero_msmarco
    run_test "TC-T1-04" "Zero MS MARCO in dataset generator script & provenance documented in README" test_t1_docs_zero_msmarco
    run_test "TC-T1-05" "Go DatasetService GetDatasetByID functional execution" test_t1_go_dataset_service
    run_test "TC-T1-06" "Python dataset CLI model unit tests" test_t1_python_dataset_tests
    run_test "TC-T1-07" "Docreader textract absence in config, lock & parser" test_t1_docreader_no_textract
    run_test "TC-T1-08" "Go modules exclude GPL-3.0 shakinm/xlsReader" test_t1_go_no_gpl_xlsreader
    run_test "TC-T1-09" "Docreader excludes AGPL-3.0 ebooklib" test_t1_docreader_no_agpl_ebooklib
    run_test "TC-T1-10" "License bundle checker guards shakinm/xlsReader & passes" test_t1_check_license_bundle
    run_test "TC-T1-11" "License packaging test execution (scripts/test-license-bundle.sh)" test_t1_test_license_bundle
    run_test "TC-T1-12" "Root LICENSE & THIRD_PARTY_NOTICES.md upstream MIT + Written Offer" test_t1_license_and_notices
    run_test "TC-T1-13" "All 7 Dockerfiles copy LICENSE and THIRD_PARTY_NOTICES.md" test_t1_dockerfiles_copy_notices
}

# ==============================================================================
# TIER 2: Functional & Clean-Room Replacements
# ==============================================================================

# TC-T2-01: Clean-room .xls reader unit tests
test_t2_cleanroom_xls_reader() {
    GOFLAGS=-mod=readonly go test -v ./internal/infrastructure/legacyxls/...
}

# TC-T2-02: Clean-room .epub parser unit tests
test_t2_cleanroom_epub_parser() {
    PYTHONPATH="${REPO_ROOT}" uv --directory docreader run --with pytest pytest tests/test_epub_parser.py
}

# TC-T2-03: Upstream Tencent VectorDB Chinese BM25 tokenizer & stub removal
test_t2_vectordb_jieba_tokenizer() {
    if [[ -e "${REPO_ROOT}/third_party/vectordatabase-sdk-go" ]]; then
        echo "Forbidden stub third_party/vectordatabase-sdk-go still exists" >&2
        return 1
    fi
    GOFLAGS=-mod=readonly go test -v ./internal/infrastructure/retriever/... -run TestTencentVectorDBJiebaTokenizerChineseBM25
}

# TC-T2-04: Docreader parser routing, docx tables & sandbox executor unit tests
test_t2_docreader_unit_tests() {
    PYTHONPATH="${REPO_ROOT}" uv --directory docreader run --with pytest pytest tests/test_sandbox_executor.py tests/test_parser_routing.py tests/test_docx_tables.py
}

# TC-T2-05: Parquet strict int64 schema validation & adversarial scan
test_t2_parquet_strict_and_adversarial() {
    local py_runner
    py_runner="$(get_pyarrow_runner)"
    if [[ -z "${py_runner}" ]]; then
        echo "PyArrow runner unavailable" >&2
        return 1
    fi

    $py_runner - <<'EOF' || return 1
import pyarrow.parquet as pq

for fname, id_col in [("queries.parquet", "id"), ("corpus.parquet", "id"), ("answers.parquet", "id"), ("qrels.parquet", "qid"), ("qas.parquet", "qid")]:
    tbl = pq.read_table(f"dataset/samples/{fname}")
    t = str(tbl.schema.field(id_col).type)
    if t != "int64":
        raise ValueError(f"Strict type violation in {fname}: field '{id_col}' is '{t}', must be strictly 'int64'")
EOF

    local forbidden_regex="(ms[-_ ]?marco|mmarco|freebsd|wp\.exe|idwp)"
    local matches
    matches=$(strings dataset/samples/*.parquet | grep -E -i "${forbidden_regex}" || true)
    if [[ -n "${matches}" ]]; then
        echo "Adversarial match detected in parquet files:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T2-06: Defense against reintroduction in scripts/check-license-bundle.sh
test_t2_license_guard_reintroduction_rejection() {
    if ! grep -Eq 'shakinm/xlsReader' scripts/check-license-bundle.sh; then
        echo "scripts/check-license-bundle.sh does not contain permanent regex guard for shakinm/xlsReader" >&2
        return 1
    fi

    local tmp_dir
    tmp_dir=$(mktemp -d)
    trap 'rm -rf "${tmp_dir}"' RETURN

    cat << 'EOF' > "${tmp_dir}/go.mod"
module testfixture

go 1.22

require github.com/shakinm/xlsReader v0.0.1
EOF
    touch "${tmp_dir}/go.sum"

    local output
    local status=0
    output=$(LICENSE_ROOT="${tmp_dir}" "${REPO_ROOT}/scripts/check-license-bundle.sh" 2>&1) || status=$?
    if [ "${status}" -eq 0 ]; then
        echo "scripts/check-license-bundle.sh falsely accepted prohibited dependency shakinm/xlsReader" >&2
        return 1
    fi
    if ! echo "${output}" | grep -q "Removed conversion dependencies have reappeared in go.mod/go.sum"; then
        echo "scripts/check-license-bundle.sh failed with unexpected error: ${output}" >&2
        return 1
    fi
    return 0
}

# TC-T2-07: Docreader SandboxExecutor process group reaping on timeout
test_t2_sandbox_timeout_reaping() {
    PYTHONPATH="${REPO_ROOT}" uv --directory docreader run --with pytest pytest tests/test_sandbox_executor.py -k "test_timeout_kills_whole_process_group"
}

run_tier_2() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 2] Functional & Clean-Room Replacements${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T2-01" "Clean-room .xls BIFF8/BIFF5 reader unit tests" test_t2_cleanroom_xls_reader
    run_test "TC-T2-02" "Clean-room .epub stdlib parser unit tests" test_t2_cleanroom_epub_parser
    run_test "TC-T2-03" "Tencent VectorDB Chinese BM25 tokenizer & no stub" test_t2_vectordb_jieba_tokenizer
    run_test "TC-T2-04" "Docreader parser & sandbox unit tests" test_t2_docreader_unit_tests
    run_test "TC-T2-05" "Parquet strict int64 schema & adversarial scan" test_t2_parquet_strict_and_adversarial
    run_test "TC-T2-06" "License bundle guard regression detection for shakinm/xlsReader" test_t2_license_guard_reintroduction_rejection
    run_test "TC-T2-07" "Docreader SandboxExecutor subprocess group timeout kill" test_t2_sandbox_timeout_reaping
}

# ==============================================================================
# TIER 3: Default-Off Cloud/Telemetry & Third-Party Publisher Gating
# ==============================================================================

# TC-T3-01: The discontinued managed cloud integration must be absent.
test_t3_removed_cloud_integration() {
    local removed_provider="we""knora""cloud"
    local removed_alias="genyee""[ _-]?cloud"
    if grep -rnEi "$removed_provider|$removed_alias" internal frontend/src .env.example .env.lite.example docker-compose.yml helm/templates; then
        echo "Discontinued cloud integration remains in a product surface" >&2
        return 1
    fi
    return 0
}

# TC-T3-02: Chrome & Claw integrations gated behind runtime URLs & zero @lyingbug/weknora
test_t3_chrome_claw_gated_and_clean() {
    if ! grep -q "GENYEE_CHROME_EXTENSION_URL" frontend/src/config/integrations.ts || \
       ! grep -q "GENYEE_CLAWHUB_SKILL_URL" frontend/src/config/integrations.ts; then
        echo "frontend/src/config/integrations.ts does not gate chrome/claw via GENYEE_CHROME_EXTENSION_URL / GENYEE_CLAWHUB_SKILL_URL" >&2
        return 1
    fi
    local matches
    matches=$(grep -rn "@lyingbug/weknora\|clawhub.ai/lyingbug/weknora" frontend/ website-docs/ || true)
    if [[ -n "${matches}" ]]; then
        echo "Found third-party publisher @lyingbug/weknora references:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T3-03: Langfuse services gated under profiles: ["langfuse"], default PROFILES="", no default cloud.langfuse.com
test_t3_langfuse_gated_and_no_cloud_default() {
    for env_template in .env.example .env.lite.example; do
        if ! grep -Eq '^LANGFUSE_ENABLED=false[[:space:]]*$' "$env_template"; then
            echo "$env_template must explicitly default Langfuse tracing to false" >&2
            return 1
        fi
    done
    bash -n scripts/dev.sh || return 1
    if ! grep -Eq '^[[:space:]]*PROFILES=""' scripts/dev.sh; then
        echo "scripts/dev.sh does not initialize PROFILES=\"\"" >&2
        return 1
    fi
    if grep -q "cloud.langfuse.com" docker-compose.yml; then
        echo "docker-compose.yml still defaults LANGFUSE_HOST to cloud.langfuse.com" >&2
        return 1
    fi
    if ! grep -q "LANGFUSE_HOST" docker-compose.yml; then
        echo "docker-compose.yml missing LANGFUSE_HOST passthrough variable" >&2
        return 1
    fi
    return 0
}

# TC-T3-04: Desktop auto-update defaults to disabled with zero Tencent/WeKnora release URLs
test_t3_desktop_autoupdate_disabled_by_default() {
    if ! grep -q "func isAutoUpdateEnabled() bool" cmd/desktop/update.go; then
        echo "cmd/desktop/update.go missing isAutoUpdateEnabled() guard" >&2
        return 1
    fi
    if grep -q "api.github.com/repos/Tencent/WeKnora" cmd/desktop/update.go; then
        echo "cmd/desktop/update.go still points to api.github.com/repos/Tencent/WeKnora" >&2
        return 1
    fi
    return 0
}

# TC-T3-05: Docker Compose configuration validation (prod & dev, with and without .env)
test_t3_compose_configs_valid() {
    local compose_cmd=(docker compose)
    if [[ -n "${COMPOSE_BIN:-}" ]]; then
        compose_cmd=("${COMPOSE_BIN}")
    elif command -v docker-compose >/dev/null 2>&1; then
        compose_cmd=(docker-compose)
    fi
    local err=0
    local tmp_dir
    tmp_dir="$(mktemp -d)"
    cp .env.example "${tmp_dir}/.env"
    for config in docker-compose.yml docker-compose.dev.yml; do
        "${compose_cmd[@]}" --project-directory "${tmp_dir}" --env-file "${tmp_dir}/.env" \
            -f "${REPO_ROOT}/${config}" config --quiet || err=1
        "${compose_cmd[@]}" --project-directory "${tmp_dir}" --env-file /dev/null \
            -f "${REPO_ROOT}/${config}" config --quiet || err=1
    done
    rm -rf "${tmp_dir}"

    return $err
}

run_tier_3() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 3] Default-Off Cloud/Telemetry & Third-Party Publisher Gating${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T3-01" "Discontinued cloud integration removed" test_t3_removed_cloud_integration
    run_test "TC-T3-02" "Chrome & Claw gated by runtime URLs & zero @lyingbug/weknora" test_t3_chrome_claw_gated_and_clean
    run_test "TC-T3-03" "Langfuse profile-gated, PROFILES=\"\", no cloud.langfuse.com default" test_t3_langfuse_gated_and_no_cloud_default
    run_test "TC-T3-04" "Desktop auto-update disabled by default & no Tencent GitHub URL" test_t3_desktop_autoupdate_disabled_by_default
    run_test "TC-T3-05" "Docker Compose config validation (prod & dev, isolated)" test_t3_compose_configs_valid
}

# ==============================================================================
# TIER 4: Brand Residue & Image Parameterization
# ==============================================================================

# TC-T4-01: Desktop wails.json & Info.plist use GENYEE Lite and com.genyee.lite
test_t4_desktop_branding() {
    if ! grep -q '"name": "GENYEE Lite"' cmd/desktop/wails.json || \
       ! grep -q '"outputfilename": "GENYEE Lite"' cmd/desktop/wails.json || \
       ! grep -q '"companyName": "GENYEE"' cmd/desktop/wails.json || \
       ! grep -q '"productName": "GENYEE Lite"' cmd/desktop/wails.json; then
        echo "cmd/desktop/wails.json does not use 'GENYEE Lite' / 'GENYEE'" >&2
        return 1
    fi
    if grep -qi "WeKnora" cmd/desktop/wails.json; then
        echo "cmd/desktop/wails.json still contains WeKnora" >&2
        return 1
    fi
    if ! grep -q "<string>com.genyee.lite</string>" cmd/desktop/build/darwin/Info.plist; then
        echo "cmd/desktop/build/darwin/Info.plist does not use com.genyee.lite" >&2
        return 1
    fi
    return 0
}

# TC-T4-02: All 8 docs/images/readme/hero-*.svg contain zero Tencent/腾讯 strings
test_t4_hero_svgs_clean() {
    local count=0
    for svg in docs/images/readme/hero-*.svg; do
        count=$((count + 1))
        if grep -Eq "Tencent|腾讯|WeKnora" "${svg}"; then
            echo "Found unbranded residue in ${svg}" >&2
            return 1
        fi
    done
    if [[ $count -lt 8 ]]; then
        echo "Expected 8 hero-*.svg files, found ${count}" >&2
        return 1
    fi
    return 0
}

# TC-T4-03: README*.md, docs/, and misc/ contain zero weknora.weixin.qq.com links
test_t4_readmes_no_weixin_links() {
    local matches
    matches=$(git grep -n "weknora\.weixin\.qq\.com" -- 'README*.md' 'docs/' 'misc/' || true)
    if [[ -n "${matches}" ]]; then
        echo "Found weknora.weixin.qq.com links in documentation and asset files:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T4-04: docs/swagger.json contains zero github_com_Tencent_GENYEE references & go test ./docs/... passes
test_t4_swagger_clean_and_valid() {
    local matches
    matches=$(grep -c "github_com_Tencent_" docs/docs.go docs/swagger.json docs/swagger.yaml | grep -v ":0$" || true)
    if [[ -n "${matches}" ]]; then
        echo "Found github_com_Tencent_ definitions in Swagger docs:" >&2
        echo "${matches}" >&2
        return 1
    fi
    GOFLAGS=-mod=readonly go test ./docs/...
}

# TC-T4-05: Self-built images parameterized as genyee/genyee-* & Helm/Homepage clean of legacy branding
test_t4_image_parameterization_and_helm() {
    if grep -Eq "wechatopenai/weknora-" docker-compose.yml Makefile scripts/build_images.sh helm/values.yaml internal/sandbox/sandbox.go; then
        echo "Found legacy wechatopenai/weknora-* image references in deployment/build files" >&2
        return 1
    fi
    if ! grep -q "genyee-app" docker-compose.yml || \
       ! grep -q "genyee-app" Makefile || \
       ! grep -q "genyee-app" scripts/build_images.sh || \
       ! grep -q "genyee/genyee-app" helm/values.yaml || \
       ! grep -q "genyee/genyee-sandbox" internal/sandbox/sandbox.go; then
        echo "Missing genyee/genyee-* image references in deployment/build files" >&2
        return 1
    fi
    if ! grep -Eq 'tag:[[:space:]]*"?7\.2-alpine"?' helm/values.yaml; then
        echo "helm/values.yaml does not pin Redis tag to 7.2-alpine" >&2
        return 1
    fi
    if grep -Eq "Copyright 2025 Tencent|dbName:[[:space:]]*weknora" helm/values.yaml || \
       grep -Eq "Copyright 2025 Tencent|part-of:[[:space:]]*weknora" helm/templates/_helpers.tpl; then
        echo "Found legacy Tencent/weknora residue in helm/values.yaml or helm/templates/_helpers.tpl" >&2
        return 1
    fi
    if grep -Eq "光子工作室群|腾讯客服|微信游戏|腾讯云 ClawPro|腾讯云轻量应用服务器" website-docs/homepage/app/adopters.ts; then
        echo "Found false Tencent adopter claims in website-docs/homepage/app/adopters.ts" >&2
        return 1
    fi
    return 0
}

run_tier_4() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 4] Brand Residue & Image Parameterization${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T4-01" "Desktop wails.json & Info.plist use GENYEE Lite & com.genyee.lite" test_t4_desktop_branding
    run_test "TC-T4-02" "All 8 docs/images/readme/hero-*.svg clean of Tencent/腾讯" test_t4_hero_svgs_clean
    run_test "TC-T4-03" "README*.md, docs/, and misc/ contain zero weknora.weixin.qq.com links" test_t4_readmes_no_weixin_links
    run_test "TC-T4-04" "Swagger docs clean of github_com_Tencent_ & pass contract tests" test_t4_swagger_clean_and_valid
    run_test "TC-T4-05" "Self-built images use genyee/genyee-* & Helm pins Redis 7.2-alpine" test_t4_image_parameterization_and_helm
}

# ==============================================================================
# TIER 5: Offline Bundle, Source Export & Hygiene
# ==============================================================================

# TC-T5-01: OFFLINE_LICENSES exist and scripts/offline-bundle.sh --manifest-only succeeds
test_t5_offline_bundle_manifest() {
    test -s OFFLINE_LICENSES/README.md || { echo "Missing OFFLINE_LICENSES/README.md" >&2; return 1; }
    test -s OFFLINE_LICENSES/WRITTEN_OFFER.md || { echo "Missing OFFLINE_LICENSES/WRITTEN_OFFER.md" >&2; return 1; }

    local tmp_out
    tmp_out="$(mktemp -d)"
    bash scripts/offline-bundle.sh --manifest-only --output "${tmp_out}/bundle" || { rm -rf "${tmp_out}"; return 1; }
    test -s "${tmp_out}/bundle/MANIFEST.json" || { rm -rf "${tmp_out}"; return 1; }
    python3 -m json.tool "${tmp_out}/bundle/MANIFEST.json" >/dev/null || { rm -rf "${tmp_out}"; return 1; }
    test -s "${tmp_out}/bundle/OFFLINE_LICENSES/WRITTEN_OFFER.md" || { rm -rf "${tmp_out}"; return 1; }
    rm -rf "${tmp_out}"
    # Exercise real export/load code with Docker command fixtures, including
    # checksums, corrupt/incomplete packages, configuration and registry paths.
    python3 -m unittest discover -s scripts/tests -p 'test_offline_delivery.py'
}

# TC-T5-02: scripts/export-source.sh succeeds and verifies excluded/included files
test_t5_export_source_archive() {
    if ! grep -q "BRANCH_GUIDE.md export-ignore" .gitattributes || \
       ! grep -q "REBRAND_RESERVED.md export-ignore" .gitattributes || \
       ! grep -q "test-reports export-ignore" .gitattributes || \
       ! grep -q "tests/adversarial export-ignore" .gitattributes; then
        echo ".gitattributes missing export-ignore rules for BRANCH_GUIDE.md, REBRAND_RESERVED.md, test-reports, or tests/adversarial" >&2
        return 1
    fi
    if ! grep -q "BRANCH_GUIDE" scripts/export-source.sh || \
       ! grep -q "REBRAND_RESERVED" scripts/export-source.sh || \
       ! grep -q "test-reports" scripts/export-source.sh || \
       ! grep -q "tests/(adversarial|e2e|stress)" scripts/export-source.sh; then
        echo "scripts/export-source.sh FORBIDDEN_PATTERNS missing BRANCH_GUIDE.md, REBRAND_RESERVED.md, test-reports, or tests/(adversarial|e2e|stress)" >&2
        return 1
    fi

    local tmp_out source_tree
    tmp_out="$(mktemp -d)"
    # Test the proposed worktree, including new files, without staging changes
    # in the user's index or creating a commit. Testing HEAD misses local fixes.
    source_tree="$(
        export GIT_INDEX_FILE="${tmp_out}/source.index"
        git read-tree HEAD && git add -A -- . && git write-tree
    )" || { rm -rf "${tmp_out}"; return 1; }
    bash scripts/export-source.sh "${tmp_out}/genyee-source-test.tar.gz" "$source_tree" || { rm -rf "${tmp_out}"; return 1; }
    test -s "${tmp_out}/genyee-source-test.tar.gz" || { rm -rf "${tmp_out}"; return 1; }
    test -s "${tmp_out}/genyee-source-test.tar.gz.sha256" || { rm -rf "${tmp_out}"; return 1; }
    rm -rf "${tmp_out}"
    return 0
}

# TC-T5-03: Zero hardcoded local user paths, leaked test credentials, or rebranding giveaway strings in tracked files
test_t5_zero_hardcoded_user_paths() {
    local p1="/Users/""cy"
    local p2="/home/""cursor"
    local p3="/root/"".cursor"
    local p4="zhanshun"".bao"
    local p5="mkRI3z"
    local p6="去腾讯""化"
    local p7="品牌""重塑"
    local p8="储能设备""采购"
    local p9="父""ASIN"
    local p10="历年""资料"
    local p11="长库""龄"
    local p12="月度经营""分析"
    local p13="reconstructed ""GENYEE"
    local p14="commercial ""rebranding"
    local p15="\.woa""\.com"
    local p16="\.oa""\.com"
    local p17="9月13日""周报"
    local pattern="${p1}|${p2}|${p3}|${p4}|${p5}|${p6}|${p7}|${p8}|${p9}|${p10}|${p11}|${p12}|${p13}|${p14}|${p15}|${p16}|${p17}"
    local matches
    matches=$(git grep -n -E "${pattern}" -- . ':!scripts/verify-commercial-compliance.sh' || true)
    if [[ -n "${matches}" ]]; then
        echo "Found hardcoded host paths, test credentials, or internal giveaway strings in tracked files:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T5-04: BrowserSkill, ProductDocs & Frontend docsUrl integrity and tests
test_t5_browserskill_and_productdocs() {
    GOFLAGS=-mod=readonly go test -count=1 ./internal/browserskill ./internal/productdocs || return 1
    (cd frontend && npx tsx --test src/utils/docsUrl.test.ts) || return 1
    return 0
}

run_tier_5() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 5] Offline Bundle, Source Export & Hygiene${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T5-01" "OFFLINE_LICENSES & scripts/offline-bundle.sh --manifest-only" test_t5_offline_bundle_manifest
    run_test "TC-T5-02" "Clean customer source archive export (scripts/export-source.sh)" test_t5_export_source_archive
    run_test "TC-T5-03" "Zero hardcoded host paths, credentials & internal strings" test_t5_zero_hardcoded_user_paths
    run_test "TC-T5-04" "BrowserSkill, ProductDocs & frontend docsUrl unit tests" test_t5_browserskill_and_productdocs
}

# ==============================================================================
# Summary & Execution Dispatcher
# ==============================================================================

print_summary() {
    echo -e "\n${C_BOLD}================================================================================${C_RESET}"
    echo -e "${C_BOLD}  Verification Summary${C_RESET}"
    echo -e "${C_BOLD}================================================================================${C_RESET}"
    printf "  Total Tests:   %d\n" "${TOTAL_TESTS}"
    printf "  Passed:        ${C_GREEN}%d${C_RESET}\n" "${PASSED_TESTS}"
    printf "  Failed:        ${C_RED}%d${C_RESET}\n" "${FAILED_TESTS}"
    printf "  Skipped:       ${C_YELLOW}%d${C_RESET}\n" "${SKIPPED_TESTS}"

    if [[ ${FAILED_TESTS} -gt 0 ]]; then
        echo -e "\n${C_RED}${C_BOLD}  Failed Test Cases:${C_RESET}"
        for item in "${FAILED_TEST_LIST[@]}"; do
            echo -e "    ${C_RED}✖ ${item}${C_RESET}"
        done
        echo -e "\n${C_RED}${C_BOLD}  Result: NON-COMPLIANT (Review failures above)${C_RESET}"
        echo -e "${C_BOLD}================================================================================${C_RESET}\n"
        return 1
    else
        echo -e "\n${C_GREEN}${C_BOLD}  Result: SELECTED ENGINEERING CHECKS PASSED${C_RESET}"
        echo -e "  This verifies the checks above; it is not a legal opinion or a full deployment acceptance test."
        echo -e "${C_BOLD}================================================================================${C_RESET}\n"
        return 0
    fi
}

main() {
    echo -e "${C_BOLD}${C_CYAN}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_CYAN}  GENYEE Unified Commercial Compliance & Dependency Hygiene Verification Suite  ${C_RESET}"
    echo -e "${C_BOLD}${C_CYAN}================================================================================${C_RESET}"
    echo -e "  Repository: ${REPO_ROOT}"
    echo -e "  Mode:       Development / Compliance Gate"
    if [[ -n "${TARGET_TIER}" ]]; then
        echo -e "  Target:     Tier ${TARGET_TIER} only"
    else
        echo -e "  Target:     All Tiers (1-5)"
    fi

    case "${TARGET_TIER}" in
        1) run_tier_1 ;;
        2) run_tier_2 ;;
        3) run_tier_3 ;;
        4) run_tier_4 ;;
        5) run_tier_5 ;;
        *)
            run_tier_1
            run_tier_2
            run_tier_3
            run_tier_4
            run_tier_5
            ;;
    esac

    print_summary
}

main "$@"
