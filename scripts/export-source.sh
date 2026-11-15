#!/usr/bin/env bash
# =============================================================================
# GENYEE 客户交付源码包导出脚本 (Clean Customer Source Archive Exporter)
#
# 功能:
#   使用 `git archive --worktree-attributes` 导出不含 .git 历史与内部维护文档
#   (见 .gitattributes 中的 export-ignore 规则) 的纯净源码交付包，并自动校验
#   敏感内部文件已被剔除。
#
# 用法:
#   ./scripts/export-source.sh [输出文件路径.tar.gz] [git-ref]
#
# 示例:
#   ./scripts/export-source.sh
#   ./scripts/export-source.sh dist/genyee-source-v1.0.0.tar.gz HEAD
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

VERSION_STR="latest"
if [[ -f "${PROJECT_ROOT}/VERSION" ]]; then
    VERSION_STR="$(tr -d '\n\r ' < "${PROJECT_ROOT}/VERSION")"
fi

usage() {
    cat <<EOF
用法:
  $0 [输出文件路径.tar.gz] [git-ref]

选项:
  -h, --help    显示帮助信息并退出

示例:
  $0
  $0 dist/genyee-source-v1.0.0.tar.gz HEAD
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --)
            shift
            break
            ;;
        -*)
            echo "[ERROR] 未知选项: $1" >&2
            usage >&2
            exit 1
            ;;
        *)
            break
            ;;
    esac
done

OUTPUT_TAR="${1:-${PROJECT_ROOT}/dist/genyee-source-${VERSION_STR}.tar.gz}"
GIT_REF="${2:-HEAD}"

mkdir -p -- "$(dirname -- "$OUTPUT_TAR")"

echo "[INFO] 正在从 ${GIT_REF} 导出客户交付源码包: ${OUTPUT_TAR}"
git -C "$PROJECT_ROOT" archive \
    --worktree-attributes \
    --format=tar.gz \
    --prefix="genyee-${VERSION_STR}/" \
    -o "$OUTPUT_TAR" \
    "$GIT_REF"

echo "[INFO] 正在校验导出的源码包是否已剔除内部维护文件..."
FORBIDDEN_PATTERNS=(
    "^genyee-${VERSION_STR}/\.git/"
    "^genyee-${VERSION_STR}/\.gitattributes$"
    "^genyee-${VERSION_STR}/\.gitignore$"
    "^genyee-${VERSION_STR}/COMMERCIAL_COMPLIANCE_AUDIT\.md$"
    "^genyee-${VERSION_STR}/AUDIT_REPORT\.md$"
    "^genyee-${VERSION_STR}/REBRAND_RESERVED\.md$"
    "^genyee-${VERSION_STR}/BRANCH_GUIDE\.md$"
    "^genyee-${VERSION_STR}/[^/]*_RESIDUE_REMEDIATION\.md$"
    "^genyee-${VERSION_STR}/test-reports(/|$)"
    "^genyee-${VERSION_STR}/tests/(adversarial|e2e|stress)(/|$)"
    "^genyee-${VERSION_STR}/\.trellis/"
    "^genyee-${VERSION_STR}/\.claude/"
    "^genyee-${VERSION_STR}/\.agents/"
    "^genyee-${VERSION_STR}/\.agents$"
    "^genyee-${VERSION_STR}/\.github/"
    "^genyee-${VERSION_STR}/docs/superpowers/"
    "^genyee-${VERSION_STR}/docs/poc/"
    "^genyee-${VERSION_STR}/docs/design/"
    "^genyee-${VERSION_STR}/scripts/verify-commercial-compliance\.sh$"
    "^genyee-${VERSION_STR}/Formula/"
    "^genyee-${VERSION_STR}/deploy/"
    "^genyee-${VERSION_STR}/cli/AGENTS\.md$"
)

TAR_LIST="$(tar -tzf "$OUTPUT_TAR")"
for pattern in "${FORBIDDEN_PATTERNS[@]}"; do
    if grep -Eq "$pattern" <<< "$TAR_LIST"; then
        echo "[ERROR] 导出包中仍包含应被排除的内部路径 (匹配规则: ${pattern})" >&2
        exit 1
    fi
done

echo "[INFO] 正在校验导出的源码包是否保留必需的合规与运行文件..."
REQUIRED_PATTERNS=(
    "^genyee-${VERSION_STR}/misc/dex-config\.yaml$"
    "^genyee-${VERSION_STR}/LICENSE$"
    "^genyee-${VERSION_STR}/THIRD_PARTY_NOTICES\.md$"
    "^genyee-${VERSION_STR}/licenses/"
    "^genyee-${VERSION_STR}/OFFLINE_LICENSES/"
    "^genyee-${VERSION_STR}/website-docs/deploy/nginx\\.conf$"
    "^genyee-${VERSION_STR}/website-docs/LICENSE$"
    "^genyee-${VERSION_STR}/website-docs/THIRD_PARTY_NOTICES\\.md$"
    "^genyee-${VERSION_STR}/mcp-server/\\.dockerignore$"
    "^genyee-${VERSION_STR}/packages/dsh-genyee/LICENSE$"
)

for pattern in "${REQUIRED_PATTERNS[@]}"; do
    if ! grep -Eq "$pattern" <<< "$TAR_LIST"; then
        echo "[ERROR] 导出包中缺失必需文件或目录 (匹配规则: ${pattern})" >&2
        exit 1
    fi
done

if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$OUTPUT_TAR" > "${OUTPUT_TAR}.sha256"
else
    shasum -a 256 "$OUTPUT_TAR" > "${OUTPUT_TAR}.sha256"
fi

echo "[SUCCESS] 客户交付纯净源码包导出并校验完成:"
echo "  - 归档文件: ${OUTPUT_TAR}"
echo "  - 校验文件: ${OUTPUT_TAR}.sha256"
