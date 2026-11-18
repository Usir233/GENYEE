#!/bin/bash
# 检查开发环境配置

# 设置颜色
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # 无颜色

# 获取项目根目录
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

log_info() {
    printf "%b\n" "${BLUE}[INFO]${NC} $1"
}

log_success() {
    printf "%b\n" "${GREEN}[✓]${NC} $1"
}

log_error() {
    printf "%b\n" "${RED}[✗]${NC} $1"
}

log_warning() {
    printf "%b\n" "${YELLOW}[!]${NC} $1"
}

echo ""
printf "%b\n" "${GREEN}========================================${NC}"
printf "%b\n" "${GREEN}  GENYEE 开发环境配置检查${NC}"
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

cd "$PROJECT_ROOT" || exit 1

# 检查 .env 文件
log_info "检查 .env 文件..."
if [ -f ".env" ]; then
    log_success ".env 文件存在"
else
    log_error ".env 文件不存在"
    echo ""
    log_info "解决方法："
    echo "  1. 复制示例文件: cp .env.example .env"
    echo "  2. 编辑 .env 文件并配置必要的环境变量"
    exit 1
fi

echo ""
log_info "检查必要的环境变量..."

# 加载 .env 文件（去掉行尾 \r 以兼容 CRLF）
_tmp_env="$(mktemp)"
tr -d '\r' < .env > "$_tmp_env"
set -a
# shellcheck source=/dev/null
source "$_tmp_env"
set +a
rm -f "$_tmp_env"

# 检查必要的环境变量
errors=0

check_var() {
    local var_name=$1
    local var_value="${!var_name:-}"
    local display_value="$var_value"
    
    if [ -z "$var_value" ]; then
        log_error "$var_name 未设置"
        errors=$((errors + 1))
    else
        case "$var_name" in
            *PASSWORD*|*SECRET*|*ACCESS_KEY*|*API_KEY*|*TOKEN*|*PRIVATE_KEY*|*AES_KEY*|*SIGNING_KEY*)
                display_value="<REDACTED>"
                ;;
        esac
        log_success "$var_name = $display_value"
    fi
}

# 数据库配置
log_info "数据库配置:"
check_var "DB_DRIVER"
case "${DB_DRIVER:-}" in
    sqlite)
        if [ -n "${DB_PATH:-}" ]; then
            log_success "DB_PATH = $DB_PATH"
        else
            log_info "DB_PATH 未设置（将使用默认路径 ./data/genyee.db）"
        fi
        ;;
    postgres)
        check_var "DB_HOST"
        check_var "DB_PORT"
        check_var "DB_USER"
        check_var "DB_PASSWORD"
        check_var "DB_NAME"
        ;;
    "")
        ;;
    *)
        log_error "不支持的 DB_DRIVER: $DB_DRIVER（仅支持 postgres 或 sqlite）"
        errors=$((errors + 1))
        ;;
esac

echo ""
log_info "存储配置:"
check_var "STORAGE_TYPE"

case "${STORAGE_TYPE:-}" in
    minio)
        check_var "MINIO_ACCESS_KEY_ID"
        check_var "MINIO_SECRET_ACCESS_KEY"
        check_var "MINIO_BUCKET_NAME"
        ;;
    cos)
        check_var "COS_SECRET_ID"
        check_var "COS_SECRET_KEY"
        check_var "COS_REGION"
        check_var "COS_BUCKET_NAME"
        ;;
    tos)
        check_var "TOS_ENDPOINT"
        check_var "TOS_REGION"
        check_var "TOS_ACCESS_KEY"
        check_var "TOS_SECRET_KEY"
        check_var "TOS_BUCKET_NAME"
        ;;
    s3)
        check_var "S3_REGION"
        check_var "S3_BUCKET_NAME"
        if [ -n "${S3_ACCESS_KEY:-}" ] || [ -n "${S3_SECRET_KEY:-}" ]; then
            check_var "S3_ACCESS_KEY"
            check_var "S3_SECRET_KEY"
        fi
        ;;
    obs)
        check_var "OBS_ENDPOINT"
        check_var "OBS_REGION"
        check_var "OBS_ACCESS_KEY"
        check_var "OBS_SECRET_KEY"
        check_var "OBS_BUCKET_NAME"
        ;;
    oss)
        check_var "OSS_ENDPOINT"
        check_var "OSS_REGION"
        check_var "OSS_ACCESS_KEY"
        check_var "OSS_SECRET_KEY"
        check_var "OSS_BUCKET_NAME"
        ;;
esac

echo ""
log_info "Redis、密钥与内部鉴权配置:"
if [ "${STREAM_MANAGER_TYPE:-}" = "redis" ] || { [ -z "${STREAM_MANAGER_TYPE:-}" ] && [ "${DB_DRIVER:-}" != "sqlite" ]; }; then
    check_var "REDIS_ADDR"
else
    log_info "STREAM_MANAGER_TYPE = ${STREAM_MANAGER_TYPE:-memory}（跳过 REDIS_ADDR 强制检查）"
fi
check_var "JWT_SECRET"
check_var "SYSTEM_AES_KEY"
if [ -n "${SYSTEM_AES_KEY:-}" ] && [ "${#SYSTEM_AES_KEY}" -ne 32 ]; then
    log_error "SYSTEM_AES_KEY 长度必须精确为 32 字节（当前: ${#SYSTEM_AES_KEY}）"
    errors=$((errors + 1))
fi
if [ "${DOCREADER_ALLOW_UNAUTHENTICATED:-false}" != "true" ]; then
    check_var "GRPC_AUTH_TOKEN"
fi

echo ""
log_info "模型与 Ollama 配置:"
if [ -n "${OLLAMA_BASE_URL:-}" ]; then
    log_success "OLLAMA_BASE_URL = $OLLAMA_BASE_URL"
elif [ "${OLLAMA_OPTIONAL:-true}" = "false" ]; then
    check_var "OLLAMA_BASE_URL"
else
    log_warning "OLLAMA_BASE_URL 未设置（可选，使用云端或 OpenAI 兼容模型时无需配置）"
fi

if [ -n "${LLM_MODEL_NAME:-}" ]; then
    log_success "LLM_MODEL_NAME = $LLM_MODEL_NAME"
else
    log_info "LLM_MODEL_NAME 未设置（可在 Web 界面「设置 → 模型」中配置）"
fi

if [ -n "${EMBEDDING_MODEL_NAME:-}" ]; then
    log_success "EMBEDDING_MODEL_NAME = $EMBEDDING_MODEL_NAME"
else
    log_info "EMBEDDING_MODEL_NAME 未设置（可在 Web 界面「设置 → 模型」中配置）"
fi

# 检查 Go 环境
echo ""
log_info "检查 Go 环境..."
if command -v go &> /dev/null; then
    go_version=$(go version)
    log_success "Go 已安装: $go_version"
else
    log_error "Go 未安装"
    errors=$((errors + 1))
fi

# 检查 Air
if command -v air &> /dev/null; then
    log_success "Air 已安装（支持热重载）"
else
    log_warning "Air 未安装（可选，用于热重载）"
    log_info "安装命令: go install github.com/air-verse/air@latest"
fi

# 检查 npm
echo ""
log_info "检查 Node.js 环境..."
if command -v npm &> /dev/null; then
    npm_version=$(npm --version)
    log_success "npm 已安装: $npm_version"
else
    log_error "npm 未安装"
    errors=$((errors + 1))
fi

# 检查 Docker（SQLite Lite 模式下为可选）
echo ""
log_info "检查 Docker 环境..."
if command -v docker &> /dev/null; then
    docker_version=$(docker --version)
    log_success "Docker 已安装: $docker_version"
    
    if docker info &> /dev/null; then
        log_success "Docker 服务正在运行"
    elif [ "${DB_DRIVER:-}" = "sqlite" ]; then
        log_warning "Docker 服务未运行（SQLite Lite 模式可选）"
    else
        log_error "Docker 服务未运行"
        errors=$((errors + 1))
    fi
elif [ "${DB_DRIVER:-}" = "sqlite" ]; then
    log_warning "Docker 未安装（SQLite Lite 模式可选）"
else
    log_error "Docker 未安装"
    errors=$((errors + 1))
fi

# 检查 Docker Compose
if docker compose version &> /dev/null; then
    compose_version=$(docker compose version)
    log_success "Docker Compose 已安装: $compose_version"
elif command -v docker-compose &> /dev/null; then
    compose_version=$(docker-compose --version)
    log_success "docker-compose 已安装: $compose_version"
elif [ "${DB_DRIVER:-}" = "sqlite" ]; then
    log_warning "Docker Compose 未安装（SQLite Lite 模式可选）"
else
    log_error "Docker Compose 未安装"
    errors=$((errors + 1))
fi

# 总结
echo ""
printf "%b\n" "${GREEN}========================================${NC}"
if [ $errors -eq 0 ]; then
    log_success "所有检查通过！环境配置正常"
    echo ""
    log_info "下一步："
    echo "  1. 启动开发环境: make dev-start"
    echo "  2. 启动后端: make dev-app"
    echo "  3. 启动前端: make dev-frontend"
else
    log_error "发现 $errors 个问题，请修复后再启动开发环境"
    echo ""
    log_info "常见问题："
    echo "  - 如果 .env 文件不存在，请复制 .env.example"
    echo "  - 确保 DB_DRIVER 设置为 'postgres' 或 'sqlite'"
    echo "  - 确保 Docker 服务正在运行"
fi
printf "%b\n" "${GREEN}========================================${NC}"
echo ""

exit $errors
