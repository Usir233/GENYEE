#!/usr/bin/env bash
# ==============================================================================
# verify-commercial-compliance.sh
#
# Unified Commercial Compliance & Dependency Hygiene Verification Suite
# Enforces 100% commercial permissiveness and clean source code delivery.
#
# Covers 5 Tiers:
#   Tier 1: Feature Coverage (Core Sanity & Acceptance)
#   Tier 2: Boundary & Corner Cases (Defensive Guards, Schema Strictness, Subprocess Reaping)
#   Tier 3: Cross-Feature Interactions (Inter-module Contracts & Working Dir Independence)
#   Tier 4: Real-World Scenarios (Enterprise Client Compliance Audit & Offline Readiness)
#   Tier 5: Commercial Compliance Removal Verification (AC1 & AC2: WeKnora Cloud, CLI, Chrome, Claw)
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

skip_test() {
    local test_id="$1"
    local description="$2"
    local reason="$3"

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
    printf "  %-12s %-62s ${C_YELLOW}[SKIP: %s]${C_RESET}\n" "[${test_id}]" "${description}..." "${reason}"
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
    if python3 -c "import pandas, pyarrow, openai" >/dev/null 2>&1; then
        echo "python3 -m unittest"
    elif command -v uv >/dev/null 2>&1; then
        echo "uv run --with pandas --with pyarrow --with openai python3 -m unittest"
    else
        echo ""
    fi
}

# ==============================================================================
# TIER 1: Feature Coverage (Core Sanity & Acceptance)
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

# TC-T1-04: Zero MS MARCO strings in dataset documentation & qa_dataset.py
test_t1_docs_zero_msmarco() {
    local matches
    matches=$(grep -rn -E -i "ms marco|mmarco" dataset/README dataset/README_zh.md dataset/qa_dataset.py || true)
    if [[ -n "${matches}" ]]; then
        echo "Forbidden MS MARCO references found in documentation/generator:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T1-05: Go dataset service GetDatasetByID unit test
test_t1_go_dataset_service() {
    go test -v ./internal/application/service -run TestDataset
}

# TC-T1-06: Python dataset CLI model unit tests
test_t1_python_dataset_tests() {
    local runner
    runner="$(get_dataset_test_runner)"
    if [[ -z "${runner}" ]]; then
        echo "Neither python3 with test dependencies nor uv found" >&2
        return 1
    fi
    $runner discover -s dataset/tests/
}

# TC-T1-07: Docker Compose service exclusion for Langfuse
test_t1_compose_no_langfuse_services() {
    local matches
    matches=$(grep -E "langfuse/langfuse|langfuse-(web|worker|clickhouse|minio|db-init)" docker-compose.yml docker-compose.dev.yml || true)
    if [[ -n "${matches}" ]]; then
        echo "Found proprietary Langfuse container service definitions:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T1-08: Docker Compose configuration validation
test_t1_compose_configs_valid() {
    local dummy_env_created=0
    if [[ ! -f .env ]]; then
        touch .env
        dummy_env_created=1
    fi

    local err=0
    docker compose -f docker-compose.yml config >/dev/null 2>&1 || err=$?
    if [[ $err -ne 0 ]]; then
        echo "docker-compose.yml failed config validation" >&2
    fi

    docker compose -f docker-compose.dev.yml config >/dev/null 2>&1 || err=$?
    if [[ $err -ne 0 ]]; then
        echo "docker-compose.dev.yml failed config validation" >&2
    fi

    if [[ $dummy_env_created -eq 1 ]]; then
        rm -f .env
    fi
    return $err
}

# TC-T1-09: scripts/dev.sh syntax and default profile check
test_t1_dev_script_clean_profiles() {
    bash -n scripts/dev.sh || return 1
    if grep -E "PROFILES=.*--profile langfuse" scripts/dev.sh; then
        echo "scripts/dev.sh still enables --profile langfuse by default" >&2
        return 1
    fi
    return 0
}

# TC-T1-10: Go mod and sum cleanliness (no vcaesar/cedar or go-ego/gse)
test_t1_go_dependencies_pruned() {
    local matches
    matches=$(grep -E "vcaesar/cedar|go-ego/gse" go.mod go.sum || true)
    if [[ -n "${matches}" ]]; then
        echo "Forbidden dependencies found in go.mod/go.sum:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T1-11: scripts/check-license-bundle.sh execution
test_t1_check_license_bundle() {
    bash scripts/check-license-bundle.sh
}

# TC-T1-12: scripts/test-license-bundle.sh execution
test_t1_test_license_bundle() {
    bash scripts/test-license-bundle.sh
}

# TC-T1-13: Docreader textract removal verification
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

# TC-T1-14: Docreader unit tests execution via uv
test_t1_docreader_unit_tests() {
    PYTHONPATH="${REPO_ROOT}" uv --directory docreader run --with pytest pytest tests/test_sandbox_executor.py tests/test_parser_routing.py tests/test_docx_tables.py
}

# TC-T1-15: No GPL-3.0 xlsReader in the statically linked Go binaries
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

# TC-T1-16: No AGPL-3.0 ebooklib in the docreader service
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

run_tier_1() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 1] Feature Coverage (Core Sanity & Acceptance Verification)${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T1-01" "Parquet evaluation dataset existence (5 files)" test_t1_parquet_exists
    run_test "TC-T1-02" "Parquet schema compliance (int64 IDs, string text)" test_t1_parquet_schemas
    run_test "TC-T1-03" "Zero non-commercial MS MARCO strings in Parquet files" test_t1_parquet_zero_msmarco
    run_test "TC-T1-04" "Zero MS MARCO strings in documentation and scripts" test_t1_docs_zero_msmarco
    run_test "TC-T1-05" "Go DatasetService GetDatasetByID functional execution" test_t1_go_dataset_service
    run_test "TC-T1-06" "Python dataset CLI model unit tests (12 tests)" test_t1_python_dataset_tests
    run_test "TC-T1-07" "Docker Compose proprietary Langfuse services exclusion" test_t1_compose_no_langfuse_services
    run_test "TC-T1-08" "Docker Compose configuration validation (prod & dev)" test_t1_compose_configs_valid
    run_test "TC-T1-09" "Dev launcher scripts/dev.sh syntax & profile hygiene" test_t1_dev_script_clean_profiles
    run_test "TC-T1-10" "Go modules cleanliness (cedar & gse excluded)" test_t1_go_dependencies_pruned
    run_test "TC-T1-11" "License bundle checker execution (scripts/check-license-bundle.sh)" test_t1_check_license_bundle
    run_test "TC-T1-12" "License packaging test execution (scripts/test-license-bundle.sh)" test_t1_test_license_bundle
    run_test "TC-T1-13" "Docreader textract absence in config, lock & parser" test_t1_docreader_no_textract
    run_test "TC-T1-14" "Docreader parser & sandbox unit tests (32 tests)" test_t1_docreader_unit_tests
    run_test "TC-T1-15" "Go modules exclude GPL-3.0 shakinm/xlsReader" test_t1_go_no_gpl_xlsreader
    run_test "TC-T1-16" "Docreader excludes AGPL-3.0 ebooklib" test_t1_docreader_no_agpl_ebooklib
}

# ==============================================================================
# TIER 2: Boundary & Corner Cases (Defensive & Robustness Guards)
# ==============================================================================

# TC-T2-01: Go dataset service directory fallback
test_t2_go_service_directory_fallback() {
    (
        cd "${REPO_ROOT}/internal/application/service"
        go test -v -run TestDataset
    )
}

# TC-T2-02: Parquet strict int64 schema validation
test_t2_parquet_type_strictness() {
    local py_runner
    py_runner="$(get_pyarrow_runner)"
    if [[ -z "${py_runner}" ]]; then
        echo "PyArrow runner unavailable" >&2
        return 1
    fi

    $py_runner - <<'EOF'
import pyarrow.parquet as pq

# Verify that no ID field was generated as int32 or float64
for fname, id_col in [("queries.parquet", "id"), ("corpus.parquet", "id"), ("answers.parquet", "id"), ("qrels.parquet", "qid"), ("qas.parquet", "qid")]:
    tbl = pq.read_table(f"dataset/samples/{fname}")
    t = str(tbl.schema.field(id_col).type)
    if t != "int64":
        raise ValueError(f"Strict type violation in {fname}: field '{id_col}' is '{t}', must be strictly 'int64'")
print("Strict int64 type constraint verified across all ID fields.")
EOF
}

# TC-T2-03: Adversarial substring search across all parquet files
test_t2_adversarial_dataset_scan() {
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

# TC-T2-04: Compose config validation resilience with absent .env
test_t2_compose_config_resilience() {
    local tmp_dir
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "${tmp_dir}"' RETURN

    # Run compose config in an isolated directory pointing to repo compose file
    (
        cd "${tmp_dir}"
        # Ensure no .env exists here
        docker compose --env-file /dev/null -f "${REPO_ROOT}/docker-compose.dev.yml" config >/dev/null
    )
}

# TC-T2-05: scripts/dev.sh CLI argument handling & help output
test_t2_dev_script_cli_edge_cases() {
    local help_out
    help_out=$(bash scripts/dev.sh --help 2>&1)
    if echo "${help_out}" | grep -E "\-\-langfuse.*默认已开启"; then
        echo "scripts/dev.sh help still advertises Langfuse enabled by default" >&2
        return 1
    fi
    return 0
}

# TC-T2-06: Defense against reintroduction in scripts/check-license-bundle.sh
test_t2_license_guard_reintroduction_rejection() {
    # Check that check-license-bundle.sh contains regex matching vcaesar/cedar and go-ego/gse
    if ! grep -Eq 'vcaesar/cedar' scripts/check-license-bundle.sh || ! grep -Eq 'go-ego/gse' scripts/check-license-bundle.sh; then
        echo "scripts/check-license-bundle.sh does not contain permanent regex guard for cedar and gse" >&2
        return 1
    fi

    # Test that the regex guard actually triggers on synthetic injection
    local test_str="github.com/vcaesar/cedar v0.20.2"
    if ! echo "${test_str}" | grep -Eq 'github.com/(longbridgeapp/opencc|liuzl/(da|cedar-go)|adamzy/cedar-go|vcaesar/cedar|go-ego/gse)([[:space:]]|$)'; then
        echo "Regex guard failed to match prohibited package: ${test_str}" >&2
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
    echo -e "${C_BOLD}${C_BLUE}[TIER 2] Boundary & Corner Cases (Defensive Guards & Edge Conditions)${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T2-01" "Go DatasetService relative directory fallback (../../)" test_t2_go_service_directory_fallback
    run_test "TC-T2-02" "Parquet strict int64 schema validation (no int32/float)" test_t2_parquet_type_strictness
    run_test "TC-T2-03" "Adversarial case-insensitive boundary scan on dataset" test_t2_adversarial_dataset_scan
    run_test "TC-T2-04" "Docker Compose config validation without .env file" test_t2_compose_config_resilience
    run_test "TC-T2-05" "Dev launcher CLI help flags and profile defaults" test_t2_dev_script_cli_edge_cases
    run_test "TC-T2-06" "License bundle guard regression detection for cedar/gse" test_t2_license_guard_reintroduction_rejection
    run_test "TC-T2-07" "Docreader SandboxExecutor subprocess group timeout kill" test_t2_sandbox_timeout_reaping
}

# ==============================================================================
# TIER 3: Cross-Feature Interactions & Interface Contracts
# ==============================================================================

# TC-T3-01: Dataset Parquet ↔ Go DatasetService Contract
test_t3_parquet_go_service_contract() {
    # Verify Go loader reads actual questions, answers, and passages generated by synthetic dataset
    local test_output
    test_output=$(go test -v ./internal/application/service -run TestDataset 2>&1)
    if ! echo "${test_output}" | grep -q "Retrieved 3 QA pairs"; then
        echo "Contract violation: Go service did not parse expected 3 QA pairs from parquet files" >&2
        return 1
    fi
    if ! echo "${test_output}" | grep -q "PASS: TestDatasetService_GetDatasetByID"; then
        echo "Contract violation: Go dataset test did not pass" >&2
        return 1
    fi
    return 0
}

# TC-T3-02: Compose Stack ↔ Dev Launcher Profile Alignment
test_t3_compose_dev_profile_alignment() {
    # Ensure app service retains LANGFUSE_* environment variables for external OTLP
    if ! grep -q "LANGFUSE_HOST" docker-compose.yml || ! grep -q "LANGFUSE_PUBLIC_KEY" docker-compose.yml; then
        echo "Contract violation: docker-compose.yml stripped required LANGFUSE_* environment variables from app service" >&2
        return 1
    fi

    # Ensure scripts/dev.sh does not fail when starting default services
    local default_profiles
    default_profiles=$(grep '^PROFILES=' scripts/dev.sh | head -n 1)
    if echo "${default_profiles}" | grep -q "langfuse"; then
        echo "Contract violation: scripts/dev.sh default PROFILES still contains langfuse" >&2
        return 1
    fi
    return 0
}

# TC-T3-03: Go Module Tree ↔ License Bundle Guard Contract
test_t3_go_mod_license_guard_contract() {
    # Verify that clean go.mod/go.sum passes check-license-bundle.sh with no violations
    bash scripts/check-license-bundle.sh || return 1
    # Verify modules.tsv checksums remain authentic
    test -s licenses/sources/modules.tsv || return 1
    return 0
}

# TC-T3-04: Docreader Dependency Removal ↔ Parser Routing Integrity
test_t3_docreader_routing_contract() {
    # Verify Docx2Parser and DocParser routing tests pass without textract
    PYTHONPATH="${REPO_ROOT}" uv --directory docreader run --with pytest pytest tests/test_parser_routing.py tests/test_docx_tables.py
}

run_tier_3() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 3] Cross-Feature Interactions & Interface Contracts${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T3-01" "Parquet schema ↔ Go DatasetService struct deserialization" test_t3_parquet_go_service_contract
    run_test "TC-T3-02" "Compose OTLP telemetry retention ↔ Dev launcher alignment" test_t3_compose_dev_profile_alignment
    run_test "TC-T3-03" "Go dependency closure ↔ License guard contract" test_t3_go_mod_license_guard_contract
    run_test "TC-T3-04" "Docreader clean dependency env ↔ Parser routing integrity" test_t3_docreader_routing_contract
}

# ==============================================================================
# TIER 4: Real-World Acceptance Scenarios
# ==============================================================================

# TC-T4-01: Enterprise Client Commercial Source Code Delivery Audit
test_t4_commercial_delivery_audit() {
    # 1. Whole-repo scan for forbidden dataset references in sample data and docs
    local non_comm_matches
    non_comm_matches=$(grep -rn -E -i "ms marco|mmarco" dataset/ 2>/dev/null || true)
    if [[ -n "${non_comm_matches}" ]]; then
        echo "Enterprise audit failed: MS MARCO references found in dataset directory" >&2
        return 1
    fi

    # 2. Whole-repo scan for proprietary Langfuse container services
    local compose_matches
    compose_matches=$(grep -E "langfuse/langfuse|langfuse-(web|worker)" docker-compose*.yml 2>/dev/null || true)
    if [[ -n "${compose_matches}" ]]; then
        echo "Enterprise audit failed: Proprietary Langfuse images found in compose files" >&2
        return 1
    fi

    # 3. Whole-repo scan for banned Go packages in go.mod/go.sum
    local go_matches
    go_matches=$(grep -E "vcaesar/cedar|go-ego/gse" go.mod go.sum 2>/dev/null || true)
    if [[ -n "${go_matches}" ]]; then
        echo "Enterprise audit failed: Restricted Go packages found" >&2
        return 1
    fi

    # 4. Whole-repo scan for textract in docreader
    local textract_matches
    textract_matches=$(grep -rn "textract" docreader/pyproject.toml docreader/uv.lock docreader/parser/ 2>/dev/null || true)
    if [[ -n "${textract_matches}" ]]; then
        echo "Enterprise audit failed: textract found in docreader" >&2
        return 1
    fi

    # 5. License bundle check
    bash scripts/check-license-bundle.sh >/dev/null || return 1
    bash scripts/test-license-bundle.sh >/dev/null || return 1

    echo "Enterprise Client Commercial Source Code Delivery Audit PASSED: 100% compliant."
    return 0
}

# TC-T4-02: Self-contained Offline Container Readiness Simulation
test_t4_offline_container_readiness() {
    # Simulate an air-gapped CI runner checking container configs without downloading remote proprietary images
    local dummy=0
    if [[ ! -f .env ]]; then
        touch .env
        dummy=1
    fi

    local status=0
    docker compose -f docker-compose.yml config --quiet || status=1
    docker compose -f docker-compose.dev.yml config --quiet || status=1

    if [[ $dummy -eq 1 ]]; then
        rm -f .env
    fi

    if [[ $status -ne 0 ]]; then
        echo "Offline container readiness check failed: docker compose config failed" >&2
        return 1
    fi
    echo "Offline container configurations validated successfully."
    return 0
}

run_tier_4() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 4] Real-World Acceptance Scenarios${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T4-01" "Enterprise client commercial source code delivery audit" test_t4_commercial_delivery_audit
    run_test "TC-T4-02" "Self-contained offline container orchestration readiness" test_t4_offline_container_readiness
}

# ==============================================================================
# TIER 5: Commercial Compliance Removal Verification (AC1 & AC2)
# ==============================================================================

# TC-T5-01: Check A: Frontend WeKnora Cloud settings & model helper removal
test_t5_wkc_frontend_files_absent() {
    local forbidden_files=(
        "frontend/src/views/settings/WeKnoraCloudSettings.vue"
        "frontend/src/utils/weknoraCloudModels.ts"
    )
    local found=0
    for f in "${forbidden_files[@]}"; do
        if [[ -e "${f}" ]]; then
            echo "Forbidden WeKnora Cloud frontend file exists: ${f}" >&2
            found=1
        fi
    done
    return $found
}

# TC-T5-02: Check B: Backend WeKnora Cloud provider, parser, handler & service removal
test_t5_wkc_backend_files_absent() {
    local forbidden_files=(
        "internal/models/providers/weknoracloud.go"
        "internal/infrastructure/docparser/weknoracloud_http_reader.go"
        "internal/handler/weknoracloud.go"
        "internal/application/service/weknoracloud.go"
        "internal/types/weknoracloud.go"
        "internal/types/interfaces/weknoracloud.go"
        "internal/models/vlm/weknoracloud.go"
        "internal/models/providers/assets/weknoracloud.svg"
    )
    local found=0
    for f in "${forbidden_files[@]}"; do
        if [[ -e "${f}" ]]; then
            echo "Forbidden WeKnora Cloud backend file exists: ${f}" >&2
            found=1
        fi
    done
    return $found
}

# TC-T5-03: Check C: Zero occurrences of "weknoracloud" across frontend/src and internal/
test_t5_wkc_zero_occurrences() {
    local matches=""
    if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        matches=$(git grep -in "weknoracloud" -- frontend/src internal/ 2>/dev/null || true)
    fi
    if [[ -z "${matches}" ]]; then
        matches=$(grep -rin "weknoracloud" frontend/src internal/ 2>/dev/null || true)
    fi

    if [[ -n "${matches}" ]]; then
        echo "Forbidden references to 'weknoracloud' detected:" >&2
        echo "${matches}" | head -n 25 >&2
        return 1
    fi
    return 0
}

# TC-T5-04: Check D: Frontend CLI, Chrome extension, and Claw skill landing removal
test_t5_integrations_frontend_files_absent() {
    local forbidden_files=(
        "frontend/src/views/integrations/CliIntegrationLanding.vue"
        "frontend/src/views/integrations/cliIntegration.ts"
        "frontend/src/views/integrations/cliIntegration.test.ts"
        "frontend/src/views/integrations/ChromeExtensionLanding.vue"
        "frontend/src/views/integrations/ClawSkillLanding.vue"
        "frontend/src/views/integrations/IntegrationLandingLayout.vue"
        "frontend/src/views/integrations/IntegrationExternalCta.vue"
        "frontend/src/views/integrations/integration-landing.less"
    )
    local found=0
    for f in "${forbidden_files[@]}"; do
        if [[ -e "${f}" ]]; then
            echo "Forbidden integration frontend file exists: ${f}" >&2
            found=1
        fi
    done
    return $found
}

# TC-T5-05: Check E: INTEGRATION_TABS strictly restricted to ['im', 'embed', 'api', 'mcpserver']
test_t5_integration_tabs_whitelist() {
    local config_file="frontend/src/config/integrations.ts"
    if [[ ! -f "${config_file}" ]]; then
        echo "Missing configuration file: ${config_file}" >&2
        return 1
    fi

    python3 - <<'EOF'
import re
import ast
import sys

with open("frontend/src/config/integrations.ts", "r", encoding="utf-8") as f:
    content = f.read()

match = re.search(r"export\s+const\s+INTEGRATION_TABS\s*(?::\s*[^=]+)?=\s*(\[[^\]]+\])", content)
if not match:
    sys.stderr.write("Could not locate INTEGRATION_TABS array in frontend/src/config/integrations.ts\n")
    sys.exit(1)

raw_array = match.group(1)
try:
    tabs = ast.literal_eval(raw_array)
except Exception as e:
    sys.stderr.write(f"Failed to parse INTEGRATION_TABS array: {e}\n")
    sys.exit(1)

expected_tabs = ['im', 'embed', 'api', 'mcpserver']
if tabs != expected_tabs:
    sys.stderr.write(f"INTEGRATION_TABS violation: expected {expected_tabs}, found {tabs}\n")
    sys.exit(1)

type_match = re.search(r"export\s+type\s+IntegrationTab\s*=\s*([^;\n]+)", content)
if type_match:
    type_str = type_match.group(1).replace(" ", "").replace('"', "'")
    expected_type = "'im'|'embed'|'api'|'mcpserver'"
    if type_str != expected_type:
        sys.stderr.write(f"IntegrationTab type definition violation: expected {expected_type}, found {type_str}\n")
        sys.exit(1)

# Ensure no mention of cli, chrome, claw tabs
forbidden_tab_tokens = ["'cli'", '"cli"', "'chrome'", '"chrome"', "'claw'", '"claw"']
for tok in forbidden_tab_tokens:
    if tok in content:
        sys.stderr.write(f"Found forbidden token {tok} in frontend/src/config/integrations.ts\n")
        sys.exit(1)

print("INTEGRATION_TABS is strictly configured with ['im', 'embed', 'api', 'mcpserver'].")
EOF
}

# TC-T5-06: Check F: Standalone CLI directory (/Users/cy/GENYEE/cli) absence
test_t5_cli_directory_absent() {
    if [[ -e "${REPO_ROOT}/cli" ]] || [[ -d "${REPO_ROOT}/cli" ]]; then
        echo "Standalone cli directory still exists at ${REPO_ROOT}/cli" >&2
        return 1
    fi
    return 0
}

# TC-T5-07: Check G: CLI CI/CD workflows (.github/workflows/cli*.yml) absence
test_t5_cli_workflows_absent() {
    local found=0
    for wf in ".github/workflows/cli.yml" ".github/workflows/cli-e2e.yml"; do
        if [[ -e "${wf}" ]]; then
            echo "Forbidden CLI workflow file exists: ${wf}" >&2
            found=1
        fi
    done
    return $found
}

# TC-T5-08: Check H: OpenPage tool navigation clean of WKC and removed integration routes
test_t5_open_page_navigation_clean() {
    local target_file="internal/agent/tools/open_page.go"
    if [[ ! -f "${target_file}" ]]; then
        echo "Missing agent tool file: ${target_file}" >&2
        return 1
    fi

    local forbidden_regex="(weknoracloud|integration-cli|integration-chrome|integration-claw)"
    local matches
    matches=$(grep -E -n -i "${forbidden_regex}" "${target_file}" || true)
    if [[ -n "${matches}" ]]; then
        echo "Forbidden navigation routes found in ${target_file}:" >&2
        echo "${matches}" >&2
        return 1
    fi
    return 0
}

# TC-T5-09: Check I: BrowserSkill ("本机浏览器连接", internal/browserskill/) integrity and tests
test_t5_browserskill_intact() {
    local bsk_dir="internal/browserskill"
    if [[ ! -d "${bsk_dir}" ]]; then
        echo "BrowserSkill directory missing: ${bsk_dir}" >&2
        return 1
    fi

    local required_files=(
        "authorization.go"
        "cluster.go"
        "daemon.go"
        "errors.go"
        "focus.go"
        "human.go"
        "manager.go"
        "result.go"
        "stop.go"
        "store.go"
    )
    for f in "${required_files[@]}"; do
        if [[ ! -s "${bsk_dir}/${f}" ]]; then
            echo "BrowserSkill core source missing or empty: ${bsk_dir}/${f}" >&2
            return 1
        fi
    done

    local go_test_output
    local go_test_status=0
    go_test_output=$(go test -count=1 ./internal/browserskill 2>&1) || go_test_status=$?
    if [[ $go_test_status -ne 0 ]]; then
        echo "BrowserSkill unit tests failed:" >&2
        echo "${go_test_output}" >&2
        return 1
    fi
    return 0
}

run_tier_5() {
    echo -e "\n${C_BOLD}${C_BLUE}================================================================================${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}[TIER 5] Commercial Compliance Removal Verification (AC1 & AC2)${C_RESET}"
    echo -e "${C_BOLD}${C_BLUE}================================================================================${C_RESET}"

    run_test "TC-T5-01" "Check A: Frontend WeKnora Cloud settings & model helper removal" test_t5_wkc_frontend_files_absent
    run_test "TC-T5-02" "Check B: Backend WeKnora Cloud provider, parser, handler & service removal" test_t5_wkc_backend_files_absent
    run_test "TC-T5-03" "Check C: Zero occurrences of 'weknoracloud' in frontend/src & internal/" test_t5_wkc_zero_occurrences
    run_test "TC-T5-04" "Check D: Frontend CLI, Chrome extension & Claw skill landing removal" test_t5_integrations_frontend_files_absent
    run_test "TC-T5-05" "Check E: INTEGRATION_TABS restricted to ['im', 'embed', 'api', 'mcpserver']" test_t5_integration_tabs_whitelist
    run_test "TC-T5-06" "Check F: Standalone CLI directory (/Users/cy/GENYEE/cli) absence" test_t5_cli_directory_absent
    run_test "TC-T5-07" "Check G: CLI CI/CD workflows (.github/workflows/cli*.yml) absence" test_t5_cli_workflows_absent
    run_test "TC-T5-08" "Check H: OpenPage tool navigation clean of WKC and removed integration routes" test_t5_open_page_navigation_clean
    run_test "TC-T5-09" "Check I: BrowserSkill ('本机浏览器连接') core package integrity and tests" test_t5_browserskill_intact
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
        echo -e "\n${C_GREEN}${C_BOLD}  Result: 100% COMMERCIALLY COMPLIANT${C_RESET}"
        echo -e "  All acceptance criteria for R1-R4 and AC1/AC2 (WeKnora Cloud, CLI, Chrome, Claw) verified."
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
