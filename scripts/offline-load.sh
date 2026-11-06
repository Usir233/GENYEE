#!/usr/bin/env bash
# =============================================================================
# GENYEE 离线交付包加载与私有仓库同步脚本 (Air-Gapped Image Loader)
#
# 功能:
#   1. 校验离线交付包 SHA256SUMS 完整性
#   2. 批量执行 docker load 导入 images/*.tar.gz 离线镜像
#   3. 可选 (--registry <private-registry>) 将镜像重打标签并推送至客户内网镜像仓库
#   4. 输出 .env (GENYEE_REGISTRY) 与 Helm (global.imageRegistry) 配置指引
#
# 用法:
#   ./scripts/offline-load.sh [选项]
#
# 选项:
#   -d, --bundle-dir <dir>   离线包根目录 (默认: 脚本所在目录的上一级或当前目录)
#   -r, --registry <prefix>  目标私有镜像仓库前缀 (例如: harbor.corp.internal/genyee)
#   --push                   指定 --registry 时，重打标签后自动执行 docker push
#   --skip-verify            跳过 SHA256SUMS 校验
#   --dry-run                仅打印将要执行的操作，不实际调用 docker load/tag/push
#   -h, --help               显示帮助信息
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_BUNDLE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

BUNDLE_DIR="${DEFAULT_BUNDLE_DIR}"
TARGET_REGISTRY=""
PUSH_IMAGES=false
SKIP_VERIFY=false
DRY_RUN=false

show_help() {
    sed -n '2,23p' "$0" | sed 's/^# \{0,1\}//'
    exit 0
}

fail() { echo "[ERROR] $*" >&2; exit 1; }
require_value() { [[ $# -ge 2 && -n "$2" && "$2" != --* ]] || fail "$1 requires a value"; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        -d|--bundle-dir)
            require_value "$@"
            BUNDLE_DIR="$2"
            shift 2
            ;;
        -r|--registry)
            require_value "$@"
            TARGET_REGISTRY="${2%/}"
            shift 2
            ;;
        --push)
            PUSH_IMAGES=true
            shift
            ;;
        --skip-verify)
            SKIP_VERIFY=true
            shift
            ;;
        --dry-run)
            DRY_RUN=true
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

[[ -z "$TARGET_REGISTRY" || "$TARGET_REGISTRY" =~ ^[a-z0-9][a-z0-9._:/-]*$ ]] || fail "Invalid target registry prefix"
[[ "$PUSH_IMAGES" == false || -n "$TARGET_REGISTRY" ]] || fail "--push requires --registry"

if [[ ! -d "$BUNDLE_DIR" ]]; then
    echo "[ERROR] 离线包目录不存在: $BUNDLE_DIR" >&2
    exit 1
fi
BUNDLE_DIR="$(cd "$BUNDLE_DIR" && pwd)"

[[ -s "${BUNDLE_DIR}/MANIFEST.json" && -s "${BUNDLE_DIR}/IMAGES.tsv" && -s "${BUNDLE_DIR}/COMPOSE_IMAGES.tsv" ]] || fail "Missing bundle manifest/index; rebuild the bundle with offline-bundle.sh"
require_checksum() {
    [[ "$SKIP_VERIFY" == true ]] && return 0
    awk -v path="./$1" '
        $2 == path && length($1) == 64 && $1 !~ /[^0-9a-f]/ { found++ }
        END { exit found == 1 ? 0 : 1 }
    ' "${BUNDLE_DIR}/SHA256SUMS" || fail "Checksum missing or duplicated for $1"
}
if [[ "$SKIP_VERIFY" == "false" ]]; then
    [[ -s "${BUNDLE_DIR}/SHA256SUMS" ]] || fail "Missing SHA256SUMS"
    require_checksum MANIFEST.json
    require_checksum IMAGES.tsv
    require_checksum COMPOSE_IMAGES.tsv
    for required in docker-compose.yml .env .env.offline .env.example LICENSE THIRD_PARTY_NOTICES.md DEPLOYMENT.txt \
        config/config.yaml docker/searxng/settings.yml misc/dex-config.yaml scripts/offline-load.sh \
        helm/Chart.yaml helm/values.yaml OFFLINE_LICENSES/README.md OFFLINE_LICENSES/WRITTEN_OFFER.md; do
        [[ -s "${BUNDLE_DIR}/${required}" && ! -L "${BUNDLE_DIR}/${required}" ]] || fail "Missing bundle file: $required"
        require_checksum "$required"
    done
    # A truncated checksum list must not silently leave deployment files or
    # license materials unverified. The registry override is generated locally
    # after loading and deliberately is not part of the original package.
    while IFS= read -r -d '' bundle_file; do
        require_checksum "${bundle_file#"${BUNDLE_DIR}/"}"
    done < <(find "$BUNDLE_DIR" -type f ! -name SHA256SUMS ! -path "${BUNDLE_DIR}/docker-compose.registry.yml" -print0)
    echo "[INFO] 正在校验 SHA256SUMS 完整性..."
    (
        cd "$BUNDLE_DIR"
        if command -v sha256sum >/dev/null 2>&1; then
            sha256sum -c SHA256SUMS
        else
            shasum -a 256 -c SHA256SUMS
        fi
    )
    echo "[SUCCESS] SHA256SUMS 校验通过"
fi

images=()
archives=()
categories=()
image_tag=""
while IFS=$'\t' read -r category src_img archive extra; do
    [[ "$category" == category && "$src_img" == image && "$archive" == archive ]] && continue
    [[ "$category" == self-built || "$category" == third-party ]] || fail "Invalid image category in IMAGES.tsv"
    [[ -z "$extra" && "$src_img" =~ ^[a-z0-9][a-zA-Z0-9._:/-]*:[a-zA-Z0-9_][a-zA-Z0-9_.-]*$ ]] || fail "Invalid image in IMAGES.tsv"
    [[ "$archive" =~ ^images/[a-zA-Z0-9_.-]+\.tar\.gz$ ]] || fail "Invalid archive path in IMAGES.tsv"
    if [[ "$DRY_RUN" == false ]]; then
        [[ -s "${BUNDLE_DIR}/${archive}" && ! -L "${BUNDLE_DIR}/${archive}" ]] || fail "Missing image archive: $archive"
        require_checksum "$archive"
    fi
    images+=("$src_img")
    archives+=("$archive")
    categories+=("$category")
    if [[ "$category" == self-built ]]; then image_tag="${src_img##*:}"; fi
done < "${BUNDLE_DIR}/IMAGES.tsv"
[[ ${#images[@]} -gt 0 ]] || fail "Bundle contains no images"
if [[ "$DRY_RUN" == false ]]; then
    command -v docker >/dev/null || fail "docker is required to load images"
fi

for archive in "${archives[@]}"; do
    echo "[INFO] 导入镜像归档: $(basename "$archive")"
    if [[ "$DRY_RUN" == false ]]; then
        docker load -i "${BUNDLE_DIR}/${archive}"
    else
        echo "  [DRY-RUN] docker load -i \"${BUNDLE_DIR}/${archive}\""
    fi
done

# 若指定了私有镜像仓库前缀，根据 MANIFEST.json 中的镜像列表进行重打标签与可选推送
if [[ -n "$TARGET_REGISTRY" ]]; then
    echo "[INFO] 正在将镜像重打标签至私有仓库: ${TARGET_REGISTRY}"
    for index in "${!images[@]}"; do
        src_img="${images[$index]}"
        # 对于自研镜像 (<orig_registry>/genyee-*:tag)，替换为 ${TARGET_REGISTRY}/genyee-*:tag
        # 对于第三方镜像 (repo/name:tag 或 name:tag)，替换为 ${TARGET_REGISTRY}/repo/name:tag
        if [[ "${categories[$index]}" == self-built ]]; then
            img_basename="${src_img##*/}"
            dst_img="${TARGET_REGISTRY}/${img_basename}"
        else
            dst_img="${TARGET_REGISTRY}/${src_img}"
        fi

        echo "[INFO] Tag: ${src_img} -> ${dst_img}"
        if [[ "$DRY_RUN" == "false" ]]; then
            docker tag "$src_img" "$dst_img"
            if [[ "$PUSH_IMAGES" == "true" ]]; then
                docker push "$dst_img"
            fi
        else
            echo "  [DRY-RUN] docker tag \"$src_img\" \"$dst_img\""
            if [[ "$PUSH_IMAGES" == "true" ]]; then
                echo "  [DRY-RUN] docker push \"$dst_img\""
            fi
        fi
    done

    # Emit a Compose override so nodes using the private registry do not fall
    # back to public infrastructure repositories. Original checksummed files
    # stay unchanged and repeat loads continue to verify the same package.
    registry_override="$(
        echo 'services:'
        while IFS=$'\t' read -r service src_img extra; do
            [[ "$service" =~ ^[a-zA-Z0-9_-]+$ && -z "$extra" ]] || fail "Invalid Compose image mapping"
            for index in "${!images[@]}"; do
                [[ "$src_img" == "${images[$index]}" ]] || continue
                if [[ "${categories[$index]}" == self-built ]]; then
                    dst_img="${TARGET_REGISTRY}/${src_img##*/}"
                else
                    dst_img="${TARGET_REGISTRY}/${src_img}"
                fi
                printf '  %s:\n    image: %s\n' "$service" "$dst_img"
                if [[ "$service" == app ]]; then
                    printf '    environment:\n      WEKNORA_SANDBOX_DOCKER_IMAGE: %s/genyee-sandbox:%s\n' "$TARGET_REGISTRY" "$image_tag"
                fi
            done
        done < "${BUNDLE_DIR}/COMPOSE_IMAGES.tsv"
    )"
    if [[ "$DRY_RUN" == false ]]; then
        printf '%s\n' "$registry_override" > "${BUNDLE_DIR}/docker-compose.registry.yml"
    else
        printf '[DRY-RUN] docker-compose.registry.yml:\n%s\n' "$registry_override"
    fi

    cat <<EOF

=============================================================================
私有镜像仓库配置指引 (${TARGET_REGISTRY})
=============================================================================
1. Docker Compose 部署（在离线包目录执行，覆盖文件已包含全部已打包服务的私有仓库镜像）:
   docker compose -f docker-compose.yml -f docker-compose.registry.yml up -d --no-build

2. Kubernetes Helm 部署:
   helm upgrade --install genyee ./helm \\
     --set global.imageRegistry=${TARGET_REGISTRY} \\
     --set-string app.image.tag=${image_tag},frontend.image.tag=${image_tag},docreader.image.tag=${image_tag}
=============================================================================
EOF
fi

echo "[SUCCESS] 离线镜像加载流程完成"
