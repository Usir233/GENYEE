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

while [[ $# -gt 0 ]]; do
    case "$1" in
        -d|--bundle-dir)
            BUNDLE_DIR="$2"
            shift 2
            ;;
        -r|--registry)
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
            echo "[ERROR] 未知选项: $1" >&2
            show_help
            ;;
    esac
done

if [[ ! -d "$BUNDLE_DIR" ]]; then
    echo "[ERROR] 离线包目录不存在: $BUNDLE_DIR" >&2
    exit 1
fi

if [[ "$SKIP_VERIFY" == "false" ]] && [[ -f "${BUNDLE_DIR}/SHA256SUMS" ]]; then
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

IMAGES_DIR="${BUNDLE_DIR}/images"
if [[ -d "$IMAGES_DIR" ]]; then
    shopt -s nullglob
    archives=("${IMAGES_DIR}"/*.tar.gz "${IMAGES_DIR}"/*.tar)
    shopt -u nullglob

    if [[ ${#archives[@]} -eq 0 ]]; then
        echo "[WARNING] ${IMAGES_DIR} 下未找到镜像归档文件 (*.tar.gz / *.tar)"
    else
        for archive in "${archives[@]}"; do
            echo "[INFO] 导入镜像归档: $(basename "$archive")"
            if [[ "$DRY_RUN" == "false" ]]; then
                docker load -i "$archive"
            else
                echo "  [DRY-RUN] docker load -i \"$archive\""
            fi
        done
    fi
fi

# 若指定了私有镜像仓库前缀，根据 MANIFEST.json 中的镜像列表进行重打标签与可选推送
if [[ -n "$TARGET_REGISTRY" ]] && [[ -f "${BUNDLE_DIR}/MANIFEST.json" ]]; then
    echo "[INFO] 正在将镜像重打标签至私有仓库: ${TARGET_REGISTRY}"
    while IFS= read -r src_img; do
        [[ -z "$src_img" ]] && continue
        # 对于自研镜像 (<orig_registry>/genyee-*:tag)，替换为 ${TARGET_REGISTRY}/genyee-*:tag
        # 对于第三方镜像 (repo/name:tag 或 name:tag)，替换为 ${TARGET_REGISTRY}/repo/name:tag
        if [[ "$src_img" == */genyee-* ]]; then
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
    done < <(grep '"image":' "${BUNDLE_DIR}/MANIFEST.json" | sed -E 's/.*"image": *"([^"]+)".*/\1/')

    cat <<EOF

=============================================================================
私有镜像仓库配置指引 (${TARGET_REGISTRY})
=============================================================================
1. Docker Compose 部署 (.env 配置):
   GENYEE_REGISTRY=${TARGET_REGISTRY}
   WEKNORA_SANDBOX_DOCKER_IMAGE=${TARGET_REGISTRY}/genyee-sandbox:latest

2. Kubernetes Helm 部署:
   helm upgrade --install genyee ./helm \\
     --set global.imageRegistry=${TARGET_REGISTRY}
=============================================================================
EOF
fi

echo "[SUCCESS] 离线镜像加载流程完成"
