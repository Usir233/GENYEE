#!/usr/bin/env bash
# =============================================================================
# GENYEE 离线交付包构建脚本 (Air-Gapped / Offline Bundle Builder)
#
# 功能:
#   1. 导出 GENYEE 核心服务与可选 Profile 的全部固定版本 Docker 镜像
#   2. 收集部署编排文件 (docker-compose.yml, .env.example, config/config.yaml,
#      docker/searxng/settings.yml, misc/dex-config.yaml, helm/)
#   3. 收集商业与开源合规声明及 AGPL/GPL 书面要约 (LICENSE,
#      THIRD_PARTY_NOTICES.md, licenses/, OFFLINE_LICENSES/)
#   4. 生成 MANIFEST.json 镜像与许可证清单及 SHA256SUMS 校验文件
#
# 用法:
#   ./scripts/offline-bundle.sh [选项]
#
# 选项:
#   -o, --output <dir>       输出目录 (默认: ./dist/genyee-offline-bundle)
#   -p, --profiles <list>    附加打包的可选 profile，逗号分隔或 all
#                            可选: minio,qdrant,neo4j,milvus,weaviate,doris,searxng,dex,langfuse,odl-hybrid,mcp,all
#                            (默认: minio,qdrant)
#   -r, --registry <prefix>  自研镜像仓库前缀 (默认读取 GENYEE_REGISTRY 或 genyee)
#   -t, --tag <version>      自研镜像版本标签 (默认读取 WEKNORA_VERSION 或 latest)
#   --platform <arch>        目标架构平台 (默认: linux/amd64)
#   --skip-pull              仅打包本地已有镜像，不执行 docker pull
#   --manifest-only          仅生成 MANIFEST.json、合规文档与脚本骨架（不拉取/导出镜像）
#   --dry-run                同 --manifest-only，用于 CI 校验清单生成
#   -h, --help               显示帮助信息
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

OUTPUT_DIR="${PROJECT_ROOT}/dist/genyee-offline-bundle"
PROFILES="minio,qdrant"
GENYEE_REGISTRY="${GENYEE_REGISTRY:-genyee}"
IMAGE_TAG="${WEKNORA_VERSION:-latest}"
PLATFORM="${PLATFORM:-linux/amd64}"
SKIP_PULL=false
MANIFEST_ONLY=false

show_help() {
    sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

fail() { echo "[ERROR] $*" >&2; exit 1; }
require_value() { [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || fail "$1 requires a value"; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        -o|--output)
            require_value "$@"
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -p|--profiles)
            [[ $# -ge 2 ]] || fail "$1 requires a value (use an empty string for core only)"
            PROFILES="$2"
            shift 2
            ;;
        -r|--registry)
            require_value "$@"
            GENYEE_REGISTRY="$2"
            shift 2
            ;;
        -t|--tag|--version)
            require_value "$@"
            IMAGE_TAG="$2"
            shift 2
            ;;
        --platform)
            require_value "$@"
            PLATFORM="$2"
            shift 2
            ;;
        --skip-pull)
            SKIP_PULL=true
            shift
            ;;
        --manifest-only|--dry-run)
            MANIFEST_ONLY=true
            shift
            ;;
        -h|--help)
            show_help
            ;;
        *)
            fail "未知选项: $1"
            ;;
    esac
done

GENYEE_REGISTRY="${GENYEE_REGISTRY%/}"
[[ "$GENYEE_REGISTRY" =~ ^[a-z0-9][a-z0-9._:/-]*$ ]] || fail "Invalid image registry prefix"
[[ "$IMAGE_TAG" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$ && ${#IMAGE_TAG} -le 128 ]] || fail "Invalid image tag"
[[ "$PLATFORM" =~ ^linux/(amd64|arm64|arm/v7)$ ]] || fail "Unsupported image platform: $PLATFORM"
if [[ -n "$PROFILES" ]]; then
    IFS=',' read -r -a selected_profiles <<< "$PROFILES"
    for profile in "${selected_profiles[@]}"; do
        case "$profile" in
            minio|qdrant|neo4j|milvus|weaviate|doris|searxng|dex|langfuse|odl-hybrid|mcp|all|full) ;;
            *) fail "Unknown profile: $profile" ;;
        esac
    done
fi
# Never silently mix a previous package's images or credentials into a new one.
[[ ! -e "$OUTPUT_DIR" || ( -d "$OUTPUT_DIR" && -z "$(ls -A "$OUTPUT_DIR")" ) ]] || fail "Output directory must be empty: $OUTPUT_DIR"
command -v openssl >/dev/null || fail "openssl is required to generate deployment secrets"
if [[ "$MANIFEST_ONLY" == false ]]; then
    command -v docker >/dev/null || fail "docker is required to export images"
fi

profile_enabled() {
    local target="$1"
    if [[ ",${PROFILES}," == *",all,"* ]]; then
        return 0
    fi
    # Match Compose's full profile. Langfuse remains an explicit opt-in.
    if [[ ",${PROFILES}," == *",full,"* ]]; then
        case "$target" in minio|qdrant|neo4j|searxng|dex|mcp) return 0 ;; esac
    fi
    [[ ",${PROFILES}," == *",${target},"* ]]
}

# 格式: "category|profile|image|license|source_offer_required"
IMAGES=(
    "self-built|core|${GENYEE_REGISTRY}/genyee-app:${IMAGE_TAG}|LicenseRef-GENYEE-Commercial AND MIT|true"
    "self-built|core|${GENYEE_REGISTRY}/genyee-ui:${IMAGE_TAG}|LicenseRef-GENYEE-Commercial AND MIT|false"
    "self-built|core|${GENYEE_REGISTRY}/genyee-docreader:${IMAGE_TAG}|LicenseRef-GENYEE-Commercial AND MIT|true"
    "self-built|core|${GENYEE_REGISTRY}/genyee-sandbox:${IMAGE_TAG}|LicenseRef-GENYEE-Commercial AND MIT|true"
    "third-party|core|paradedb/paradedb:v0.25.11-pg17|AGPL-3.0-only|true"
    "third-party|core|redis:7.0-alpine|BSD-3-Clause|false"
    "third-party|helm|redis:7.2-alpine|BSD-3-Clause|false"
)

if profile_enabled "minio"; then
    if profile_enabled "langfuse"; then
        IMAGES+=("third-party|minio,langfuse|minio/minio:RELEASE.2025-09-07T16-13-09Z|AGPL-3.0-only|true")
    else
        IMAGES+=("third-party|minio|minio/minio:RELEASE.2025-09-07T16-13-09Z|AGPL-3.0-only|true")
    fi
elif profile_enabled "langfuse"; then
    IMAGES+=("third-party|langfuse|minio/minio:RELEASE.2025-09-07T16-13-09Z|AGPL-3.0-only|true")
fi
if profile_enabled "qdrant"; then
    IMAGES+=("third-party|qdrant|qdrant/qdrant:v1.16.2|Apache-2.0|false")
fi
if profile_enabled "neo4j"; then
    IMAGES+=("third-party|neo4j|neo4j:2025.10.1|GPL-3.0-or-later / AGPL-3.0 (APOC)|true")
fi
if profile_enabled "milvus"; then
    IMAGES+=("third-party|milvus|milvusdb/milvus:v2.6.11|Apache-2.0|false")
fi
if profile_enabled "weaviate"; then
    IMAGES+=("third-party|weaviate|semitechnologies/weaviate:1.28.4|BSD-3-Clause|false")
fi
if profile_enabled "doris"; then
    IMAGES+=(
        "third-party|doris|apache/doris:fe-4.1.0|Apache-2.0|false"
        "third-party|doris|apache/doris:be-4.1.0|Apache-2.0|false"
    )
fi
if profile_enabled "searxng"; then
    IMAGES+=(
        "third-party|searxng|busybox:1.36|GPL-2.0-only|true"
        "third-party|searxng|searxng/searxng:2025.5.18-5286903|AGPL-3.0-or-later|true"
    )
fi
if profile_enabled "dex"; then
    IMAGES+=("third-party|dex|dexidp/dex:v2.41.1|Apache-2.0|false")
fi
if profile_enabled "langfuse"; then
    IMAGES+=(
        "third-party|langfuse|clickhouse/clickhouse-server:24.8.14.39|Apache-2.0|false"
        "third-party|langfuse|langfuse/langfuse:3.29.0|MIT (core) / Langfuse Commercial (ee)|false"
        "third-party|langfuse|langfuse/langfuse-worker:3.29.0|MIT (core) / Langfuse Commercial (ee)|false"
    )
fi
if profile_enabled "odl-hybrid"; then
    IMAGES+=("self-built|odl-hybrid|${GENYEE_REGISTRY}/genyee-odl-hybrid:${IMAGE_TAG}|LicenseRef-GENYEE-Commercial AND MIT|true")
fi
if profile_enabled "mcp"; then
    IMAGES+=("self-built|mcp|${GENYEE_REGISTRY}/genyee-mcp:${IMAGE_TAG}|LicenseRef-GENYEE-Commercial AND MIT|false")
fi

mkdir -p "${OUTPUT_DIR}/images" "${OUTPUT_DIR}/OFFLINE_LICENSES" "${OUTPUT_DIR}/scripts" \
         "${OUTPUT_DIR}/config" "${OUTPUT_DIR}/docker/searxng" "${OUTPUT_DIR}/misc" "${OUTPUT_DIR}/licenses"

echo "[INFO] 复制编排配置与合规许可证文件到 ${OUTPUT_DIR} ..."
for f in docker-compose.yml .env.example LICENSE THIRD_PARTY_NOTICES.md; do
    if [[ -f "${PROJECT_ROOT}/${f}" ]]; then
        cp "${PROJECT_ROOT}/${f}" "${OUTPUT_DIR}/"
    fi
done

# Start from the complete deployment template; use the same variable names as
# docker-compose.yml. A custom .env.offline is an explicit deployment override.
bundle_umask="$(umask)"
umask 077
if [[ -f "${PROJECT_ROOT}/.env.offline" ]]; then
    cp "${PROJECT_ROOT}/.env.offline" "${OUTPUT_DIR}/.env.offline"
else
    cp "${PROJECT_ROOT}/.env.example" "${OUTPUT_DIR}/.env.offline"
fi

set_bundle_env() {
    local key="$1" value="$2" file="${OUTPUT_DIR}/.env.offline"
    awk -v key="$key" -v value="$value" '
        index($0, key "=") == 1 { if (!found++) print key "=" value; next }
        { print }
        END { if (!found) print key "=" value }
    ' "$file" > "${file}.tmp"
    mv "${file}.tmp" "$file"
}

set_secret_env() {
    local key="$1" bytes="$2" secret
    # Do not hide openssl's exit status in an argument to another command.
    secret="$(openssl rand -hex "$bytes")" || fail "Cannot generate secret for $key"
    [[ "$secret" =~ ^[0-9a-f]+$ && ${#secret} -eq $((bytes * 2)) ]] || fail "Invalid random output for $key"
    set_bundle_env "$key" "$secret"
}

if [[ ! -f "${PROJECT_ROOT}/.env.offline" ]]; then
    set_bundle_env DB_NAME genyee
    set_secret_env DB_PASSWORD 24
    set_secret_env REDIS_PASSWORD 24
    set_secret_env JWT_SECRET 32
    set_secret_env SYSTEM_AES_KEY 16
    set_bundle_env WEKNORA_WEKNORACLOUD_ENABLED false
    set_bundle_env LANGFUSE_ENABLED false
    if profile_enabled mcp; then
        set_secret_env MCP_SERVER_AUTH_TOKEN 32
    fi
    if profile_enabled neo4j; then
        set_secret_env NEO4J_PASSWORD 24
    fi
    if profile_enabled langfuse; then
        for key in LANGFUSE_NEXTAUTH_SECRET LANGFUSE_SALT LANGFUSE_CLICKHOUSE_PASSWORD LANGFUSE_MINIO_PASSWORD; do
            set_secret_env "$key" 24
        done
        set_secret_env LANGFUSE_ENCRYPTION_KEY 32
    fi
    if profile_enabled minio; then
        set_bundle_env STORAGE_TYPE minio
        set_bundle_env MINIO_ENDPOINT minio:9000
        set_bundle_env MINIO_ACCESS_KEY_ID genyeeadmin
        set_secret_env MINIO_SECRET_ACCESS_KEY 24
        set_bundle_env MINIO_BUCKET_NAME genyee-data
        set_bundle_env MINIO_USE_SSL false
    fi
    if profile_enabled qdrant; then
        set_bundle_env RETRIEVE_DRIVER qdrant
    fi
fi
set_bundle_env WEKNORA_VERSION "$IMAGE_TAG"
set_bundle_env GENYEE_REGISTRY "$GENYEE_REGISTRY"
set_bundle_env WEKNORA_SANDBOX_DOCKER_IMAGE "${GENYEE_REGISTRY}/genyee-sandbox:${IMAGE_TAG}"
if [[ ",${PROFILES}," == *",all,"* ]]; then
    set_bundle_env COMPOSE_PROFILES full,minio,qdrant,neo4j,milvus,weaviate,doris,searxng,dex,langfuse,odl-hybrid,mcp
else
    set_bundle_env COMPOSE_PROFILES "$PROFILES"
fi
cp "${OUTPUT_DIR}/.env.offline" "${OUTPUT_DIR}/.env"
chmod 600 "${OUTPUT_DIR}/.env" "${OUTPUT_DIR}/.env.offline"
umask "$bundle_umask"

if [[ -f "${PROJECT_ROOT}/config/config.yaml" ]]; then
    cp "${PROJECT_ROOT}/config/config.yaml" "${OUTPUT_DIR}/config/config.yaml"
fi

if [[ -f "${PROJECT_ROOT}/docker/searxng/settings.yml" ]]; then
    cp "${PROJECT_ROOT}/docker/searxng/settings.yml" "${OUTPUT_DIR}/docker/searxng/settings.yml"
fi

if [[ -f "${PROJECT_ROOT}/misc/dex-config.yaml" ]]; then
    cp "${PROJECT_ROOT}/misc/dex-config.yaml" "${OUTPUT_DIR}/misc/dex-config.yaml"
fi

if [[ -d "${PROJECT_ROOT}/licenses" ]]; then
    cp -R "${PROJECT_ROOT}/licenses/." "${OUTPUT_DIR}/licenses/"
fi

if [[ -d "${PROJECT_ROOT}/OFFLINE_LICENSES" ]]; then
    cp -R "${PROJECT_ROOT}/OFFLINE_LICENSES/." "${OUTPUT_DIR}/OFFLINE_LICENSES/"
fi

if [[ -d "${PROJECT_ROOT}/helm" ]]; then
    rm -rf "${OUTPUT_DIR}/helm"
    cp -R "${PROJECT_ROOT}/helm" "${OUTPUT_DIR}/helm"
fi

for s in offline-load.sh; do
    if [[ -f "${PROJECT_ROOT}/scripts/${s}" ]]; then
        cp "${PROJECT_ROOT}/scripts/${s}" "${OUTPUT_DIR}/scripts/"
        chmod +x "${OUTPUT_DIR}/scripts/${s}"
    fi
done

# The source checkout's start_all.sh can pull or build images and is not an
# offline launcher. Ship explicit commands that use the already imported images.
cat > "${OUTPUT_DIR}/DEPLOYMENT.txt" <<'EOF'
GENYEE offline deployment

This package targets the architecture and image versions in MANIFEST.json.
The target needs Docker Engine and Docker Compose v2. A --manifest-only package
contains no image archives and cannot be deployed.

1. On the target, from this directory:
     bash scripts/offline-load.sh
   The loader verifies the original deployment files and image archives before
   importing any images. SHA256SUMS detects file corruption; obtain this package
   from your trusted distribution channel.

2. Review the generated .env (credentials are unique to this package), ports,
   storage and COMPOSE_PROFILES. Keep .env private and backed up. Verify the
   original package before editing configuration; retain an unmodified copy for
   repeat integrity checks. WeKnoraCloud and Langfuse tracing default to disabled.

3. Start local imported images without pulling or rebuilding:
     docker compose -f docker-compose.yml up -d --no-build --pull never
   The source checkout's scripts/start_all.sh is not an offline launcher.

Private registry alternative (requires access to that registry):
     bash scripts/offline-load.sh --registry harbor.example/project --push
     docker compose -f docker-compose.yml -f docker-compose.registry.yml up -d --no-build
   The generated override includes both self-built and infrastructure images.
   It is not part of the original checksummed package.

Optional profiles must have been included at packaging time. Local model assets
and model endpoints must also be prepared for an air-gapped environment; this
package does not download model weights on the target. For odl-hybrid, build the
image with PRELOAD_MODELS=true before packaging if local layout models are needed.

License and source-delivery terms: LICENSE, THIRD_PARTY_NOTICES.md, licenses/
and OFFLINE_LICENSES/. This image package does not contain the project source
archive; export customer source separately using scripts/export-source.sh in
the source checkout.
EOF

sanitize_filename() {
    echo "$1" | sed -E 's|[/:@]+|_|g'
}

BUILD_TIMESTAMP="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
GIT_COMMIT="$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")"

MANIFEST_FILE="${OUTPUT_DIR}/MANIFEST.json"
# A simple validated index lets the air-gapped loader avoid a jq/Python runtime
# dependency. Both the human-readable JSON and this index are checksummed.
printf 'category\timage\tarchive\n' > "${OUTPUT_DIR}/IMAGES.tsv"
# Record the service/image mapping while the source Compose file is available.
# The loader uses it to generate an override for ALL packaged services, including
# third-party infrastructure, without requiring Compose or a YAML library.
awk -v registry="$GENYEE_REGISTRY" -v tag="$IMAGE_TAG" '
    /^  [a-zA-Z0-9_-]+:$/ { service=$1; sub(/:$/, "", service) }
    /^    image: / {
        image=$2;
        gsub(/\$\{GENYEE_REGISTRY:-genyee\}/, registry, image);
        gsub(/\$\{WEKNORA_VERSION:-latest\}/, tag, image);
        print service "\t" image;
    }
' "${PROJECT_ROOT}/docker-compose.yml" > "${OUTPUT_DIR}/COMPOSE_IMAGES.tsv"
{
    echo "{"
    echo "  \"bundle_format_version\": \"1.1\","
    echo "  \"product\": \"GENYEE\","
    echo "  \"version\": \"${IMAGE_TAG}\","
    echo "  \"git_commit\": \"${GIT_COMMIT}\","
    echo "  \"platform\": \"${PLATFORM}\","
    echo "  \"default_registry\": \"${GENYEE_REGISTRY}\","
    echo "  \"profiles\": \"${PROFILES}\","
    echo "  \"created_at\": \"${BUILD_TIMESTAMP}\","
    echo "  \"manifest_only\": ${MANIFEST_ONLY},"
    echo "  \"images\": ["

    total="${#IMAGES[@]}"
    idx=0
    for entry in "${IMAGES[@]}"; do
        idx=$((idx + 1))
        IFS='|' read -r category profile image license offer_req <<< "$entry"
        archive_name="images/$(sanitize_filename "$image").tar.gz"
        printf '%s\t%s\t%s\n' "$category" "$image" "$archive_name" >> "${OUTPUT_DIR}/IMAGES.tsv"
        digest="pending"

        if [[ "$MANIFEST_ONLY" == "false" ]]; then
            if [[ "$SKIP_PULL" == "false" ]] && [[ "$category" == "third-party" ]]; then
                echo "[INFO] 拉取镜像 (${PLATFORM}): ${image}" >&2
                docker pull --platform "${PLATFORM}" "${image}" >&2
            fi
            actual_platform="$(docker image inspect --format '{{.Os}}/{{.Architecture}}{{if .Variant}}/{{.Variant}}{{end}}' "$image")"
            if [[ "$actual_platform" != "$PLATFORM" && !( "$PLATFORM" == linux/arm64 && "$actual_platform" == linux/arm64/v8 ) ]]; then
                fail "Image $image is $actual_platform; requested $PLATFORM. Build/pull the matching architecture before packaging."
            fi
            echo "[INFO] 导出镜像: ${image} -> ${archive_name}" >&2
            docker save "${image}" | gzip -c > "${OUTPUT_DIR}/${archive_name}"
            digest="$(docker image inspect --format '{{index .RepoDigests 0}}' "${image}" 2>/dev/null || docker image inspect --format '{{.Id}}' "${image}" 2>/dev/null || echo "local")"
        fi

        comma=","
        if [[ $idx -eq $total ]]; then
            comma=""
        fi
        cat <<EOF
    {
      "category": "${category}",
      "profile": "${profile}",
      "image": "${image}",
      "archive": "${archive_name}",
      "digest": "${digest}",
      "license": "${license}",
      "written_offer_applicable": ${offer_req}
    }${comma}
EOF
    done

    echo "  ]"
    echo "}"
} > "${MANIFEST_FILE}"

echo "[INFO] 生成 SHA256SUMS 校验清单..."
(
    cd "${OUTPUT_DIR}"
    rm -f SHA256SUMS
    if command -v sha256sum >/dev/null 2>&1; then
        find . -type f ! -name 'SHA256SUMS' -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
    else
        find . -type f ! -name 'SHA256SUMS' -print0 | sort -z | xargs -0 shasum -a 256 > SHA256SUMS
    fi
)

echo "[SUCCESS] GENYEE 离线交付包生成完毕: ${OUTPUT_DIR}"
echo "  - 清单文件: ${MANIFEST_FILE}"
echo "  - 校验文件: ${OUTPUT_DIR}/SHA256SUMS"
echo "  - 许可证与源码获取要约: ${OUTPUT_DIR}/OFFLINE_LICENSES/"
