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

while [[ $# -gt 0 ]]; do
    case "$1" in
        -o|--output)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -p|--profiles)
            PROFILES="$2"
            shift 2
            ;;
        -r|--registry)
            GENYEE_REGISTRY="$2"
            shift 2
            ;;
        -t|--tag|--version)
            IMAGE_TAG="$2"
            shift 2
            ;;
        --platform)
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
            echo "[ERROR] 未知选项: $1" >&2
            show_help
            ;;
    esac
done

profile_enabled() {
    local target="$1"
    if [[ ",${PROFILES}," == *",all,"* ]] || [[ ",${PROFILES}," == *",full,"* ]]; then
        return 0
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
        IMAGES+=("third-party|minio,langfuse|pgsty/minio:RELEASE.2026-08-04T00-00-00Z|AGPL-3.0-only|true")
    else
        IMAGES+=("third-party|minio|pgsty/minio:RELEASE.2026-08-04T00-00-00Z|AGPL-3.0-only|true")
    fi
elif profile_enabled "langfuse"; then
    IMAGES+=("third-party|langfuse|pgsty/minio:RELEASE.2026-08-04T00-00-00Z|AGPL-3.0-only|true")
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

# 生成/复制政企推荐的离线部署环境模版 (.env.offline 与 .env)
if [[ -f "${PROJECT_ROOT}/.env.offline" ]]; then
    cp "${PROJECT_ROOT}/.env.offline" "${OUTPUT_DIR}/.env.offline"
else
    cat <<'EOF' > "${OUTPUT_DIR}/.env.offline"
# =====================================================================
# GENYEE 政企断网离线交付环境配置模版 (.env.offline)
# 适用场景：完全断网离线环境 (Air-Gapped / Isolated Private Network)
# =====================================================================

# ========== 1. 基础运行配置 ==========
GIN_MODE=release
TZ=Asia/Shanghai
AUTO_MIGRATE=true
AUTO_RECOVER_DIRTY=true
WEKNORA_VERSION=latest

# ========== 2. 默认离线 Docker 镜像仓库配置（如内网 Harbor） ==========
# GENYEE_REGISTRY=harbor.internal.corp/genyee

# ========== 3. 推荐启用离线 Profile (对象存储 MinIO + 向量库 Qdrant) ==========
COMPOSE_PROFILES=minio,qdrant

# ========== 4. 数据存储服务配置 ==========
POSTGRES_USER=postgres
POSTGRES_PASSWORD=postgres_offline_secret
POSTGRES_DB=genyee
REDIS_HOST=redis
REDIS_PORT=6379

# ========== 5. 对象存储 (内置 MinIO 离线实例) ==========
MINIO_ROOT_USER=genyeeadmin
MINIO_ROOT_PASSWORD=genyeeadmin_offline_secret
MINIO_ENDPOINT=minio:9000
MINIO_BUCKET=genyee-data
MINIO_USE_SSL=false

# ========== 6. 本地/内网离线大模型与嵌入服务 (Ollama / vLLM) ==========
DEFAULT_LLM_PROVIDER=ollama
OLLAMA_BASE_URL=http://host.docker.internal:11434
DEFAULT_EMBEDDING_PROVIDER=ollama
OLLAMA_EMBEDDING_BASE_URL=http://host.docker.internal:11434

# ========== 7. 网络与安全边界 (断网隔离) ==========
SSRF_DNS_WHITELIST_ONLY=false
SSRF_WHITELIST_EXTRA=minio,qdrant,searxng,redis,paradedb

# ========== 8. 默认管理员自举 (首次启动自动创建/提权) ==========
# WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL=admin@internal.corp
EOF
fi

if [[ ! -f "${OUTPUT_DIR}/.env" ]]; then
    cp "${OUTPUT_DIR}/.env.offline" "${OUTPUT_DIR}/.env"
fi

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

for s in offline-load.sh start_all.sh get_version.sh; do
    if [[ -f "${PROJECT_ROOT}/scripts/${s}" ]]; then
        cp "${PROJECT_ROOT}/scripts/${s}" "${OUTPUT_DIR}/scripts/"
        chmod +x "${OUTPUT_DIR}/scripts/${s}"
    fi
done

sanitize_filename() {
    echo "$1" | sed -E 's|[/:@]+|_|g'
}

BUILD_TIMESTAMP="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
GIT_COMMIT="$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || echo "unknown")"

MANIFEST_FILE="${OUTPUT_DIR}/MANIFEST.json"
{
    echo "{"
    echo "  \"bundle_format_version\": \"1.0\","
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
        digest="pending"

        if [[ "$MANIFEST_ONLY" == "false" ]]; then
            if [[ "$SKIP_PULL" == "false" ]] && [[ "$category" == "third-party" ]]; then
                echo "[INFO] 拉取镜像 (${PLATFORM}): ${image}"
                docker pull --platform "${PLATFORM}" "${image}"
            fi
            echo "[INFO] 导出镜像: ${image} -> ${archive_name}"
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
