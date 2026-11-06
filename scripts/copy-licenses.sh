#!/usr/bin/env bash
set -euo pipefail

# Assemble notices and verified corresponding source for backend/desktop releases.
if [ "$#" -lt 1 ] || [ -z "$1" ]; then
    echo "Usage: $0 DESTINATION_DIRECTORY [--goos OS --goarch ARCH --cgo 0|1] [--target PACKAGE=TAGS]..." >&2
    exit 1
fi

license_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
license_dest="$1"
shift
notice_args=()
while [ "$#" -gt 0 ]; do
    case "$1" in
        --goos|--goarch|--cgo|--target)
            [ "$#" -ge 2 ] && [ -n "$2" ] || { echo "Missing value for $1" >&2; exit 1; }
            notice_args+=("$1" "$2")
            shift 2
            ;;
        *) echo "Unknown notice option: $1" >&2; exit 1 ;;
    esac
done
bash "${license_root}/scripts/check-license-bundle.sh"

# Use Go's module cache and configured GOPROXY, including offline cache hits.
# Archives are release inputs, not files vendored in the source repository.
source_stage="$(mktemp -d)"
trap 'rm -rf "${source_stage}"' EXIT
module_cache="$(cd "${license_root}" && go env GOMODCACHE)"
while read -r module_name module_version _checksum; do
    (cd "${license_root}" && go mod download "${module_name}@${module_version}")
    cp "${module_cache}/cache/download/${module_name}/@v/${module_version}.zip" \
        "${source_stage}/${module_name##*/}-${module_version}.zip"
done < "${license_root}/licenses/sources/modules.tsv"
bash "${license_root}/scripts/check-license-bundle.sh" "${source_stage}"

# Run the stdlib-only collector on the build host, but inspect the exact target
# platform, CGO setting and each binary's own tags. Never enumerate/download all
# modules from go.mod: only go list -deps for these release entry points.
target_goos="$(cd "${license_root}" && go env GOOS)"
target_goarch="$(cd "${license_root}" && go env GOARCH)"
target_cgo="$(cd "${license_root}" && go env CGO_ENABLED)"
host_goos="$(cd "${license_root}" && go env GOHOSTOS)"
host_goarch="$(cd "${license_root}" && go env GOHOSTARCH)"
(
    cd "${license_root}"
    GOOS="${host_goos}" GOARCH="${host_goarch}" CGO_ENABLED=0 \
        go run -mod=readonly ./scripts/go-notices/main.go \
        --root "${license_root}" --output "${source_stage}/go-notices" \
        --goos "${target_goos}" --goarch "${target_goarch}" --cgo "${target_cgo}" \
        ${notice_args[@]+"${notice_args[@]}"}
)

mkdir -p "${license_dest}/licenses"
cp "${license_root}/LICENSE" "${license_root}/THIRD_PARTY_NOTICES.md" "${license_dest}/"
cp -R "${license_root}/licenses/." "${license_dest}/licenses/"
cp "${source_stage}/"*.zip "${license_dest}/licenses/sources/"
rm -rf "${license_dest}/licenses/go"
cp -R "${source_stage}/go-notices" "${license_dest}/licenses/go"

if [ -d "${license_root}/frontend" ]; then
    cp "${license_root}/LICENSE" "${license_root}/THIRD_PARTY_NOTICES.md" "${license_root}/frontend/"
fi
if [ -d "${license_root}/website-docs" ]; then
    cp "${license_root}/LICENSE" "${license_root}/THIRD_PARTY_NOTICES.md" "${license_root}/website-docs/"
fi
if [ -d "${license_root}/mcp-server" ]; then
    cp "${license_root}/LICENSE" "${license_root}/mcp-server/LICENSE.GENYEE"
    cp "${license_root}/THIRD_PARTY_NOTICES.md" "${license_root}/mcp-server/THIRD_PARTY_NOTICES.md"
fi
