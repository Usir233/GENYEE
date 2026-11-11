#!/usr/bin/env bash
set -euo pipefail

#
# 更新 Homebrew Formula 中的版本号和 sha256
#
# 用法:
#   ./scripts/update-homebrew-formula.sh v0.2.0
#
# 会自动从 GitHub Releases 下载 .sha256 文件来填充 Formula。
# 在 CI 中被 release-lite workflow 调用。
#

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

VERSION="${1:?Usage: $0 <version>  (e.g. v0.2.0)}"
if [[ ! "${VERSION}" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9._-]+)?$ ]]; then
    echo "Error: Invalid version format '${VERSION}'" >&2
    exit 1
fi
VERSION_BARE="${VERSION#v}"
FORMULA="${ROOT_DIR}/Formula/genyee-lite.rb"
REPO="${GENYEE_REPO:-genyee/genyee}"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

if [ ! -f "${FORMULA}" ]; then
    echo "Error: Formula not found at ${FORMULA}"
    exit 1
fi

echo "Updating Formula to version ${VERSION_BARE}..."

fetch_sha256() {
    local file="$1"
    local url="${BASE_URL}/${file}.sha256"
    local sha
    sha=$(curl -fsSL "${url}" | awk '{print $1}')
    if [[ ! "${sha}" =~ ^[0-9a-fA-F]{64}$ ]]; then
        echo "Error: Invalid or missing sha256 digest from ${url}" >&2
        exit 1
    fi
    printf '%s\n' "${sha,,}"
}

SHA_DARWIN_ARM64=$(fetch_sha256 "GENYEE-lite_${VERSION}_darwin_arm64.tar.gz")
SHA_DARWIN_AMD64=$(fetch_sha256 "GENYEE-lite_${VERSION}_darwin_amd64.tar.gz")
SHA_LINUX_ARM64=$(fetch_sha256  "GENYEE-lite_${VERSION}_linux_arm64.tar.gz")
SHA_LINUX_AMD64=$(fetch_sha256  "GENYEE-lite_${VERSION}_linux_amd64.tar.gz")

echo "  darwin_arm64 : ${SHA_DARWIN_ARM64}"
echo "  darwin_amd64 : ${SHA_DARWIN_AMD64}"
echo "  linux_arm64  : ${SHA_LINUX_ARM64}"
echo "  linux_amd64  : ${SHA_LINUX_AMD64}"

# Use a temp file for portable sed
TMP=$(mktemp)
cp "${FORMULA}" "${TMP}"

# Update version
sed -i.bak "s/^  version \".*\"/  version \"${VERSION_BARE}\"/" "${TMP}"

# Update sha256 values in order of appearance.
# The formula has sha256 lines in this order:
#   1. darwin arm64
#   2. darwin amd64
#   3. linux arm64
#   4. linux amd64
awk -v s1="${SHA_DARWIN_ARM64}" \
    -v s2="${SHA_DARWIN_AMD64}" \
    -v s3="${SHA_LINUX_ARM64}" \
    -v s4="${SHA_LINUX_AMD64}" \
    'BEGIN{n=0} /sha256 "/{n++; if(n==1) sub(/"[^"]*"$/,"\"" s1 "\""); else if(n==2) sub(/"[^"]*"$/,"\"" s2 "\""); else if(n==3) sub(/"[^"]*"$/,"\"" s3 "\""); else if(n==4) sub(/"[^"]*"$/,"\"" s4 "\"")} {print}' \
    "${TMP}" > "${FORMULA}"

rm -f "${TMP}" "${TMP}.bak"

echo "Formula updated: ${FORMULA}"
