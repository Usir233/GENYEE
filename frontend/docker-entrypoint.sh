#!/bin/sh
set -eu

# Only emit whitelisted locale tags to avoid config.js injection from env values.
RUNTIME_DEFAULT_LOCALE=""
case "${DEFAULT_LOCALE:-}" in
  zh-CN|zh-TW|en-US|ru-RU|ko-KR|ja-JP) RUNTIME_DEFAULT_LOCALE="${DEFAULT_LOCALE}" ;;
esac

# 生成运行时配置文件，注入环境变量到前端
positive_integer() {
  case "$1" in
    ''|*[!0-9]*) echo "Upload limits must be positive integers" >&2; exit 1 ;;
  esac
  # Canonical decimal notation avoids JavaScript interpreting 010 as octal
  # while nginx and the backend use a different request-size limit.
  value=$(printf '%s' "$1" | sed 's/^0*//')
  # The frontend converts MiB to bytes; keep that integer exactly representable
  # by JavaScript Number (floor(Number.MAX_SAFE_INTEGER / 1024 / 1024)).
  [ -n "$value" ] && [ "$value" -gt 0 ] 2>/dev/null && [ "$value" -le 8589934591 ] 2>/dev/null || {
    echo "Upload limits must be positive integers within the supported range" >&2; exit 1;
  }
  printf '%s' "$value"
}
FILE_MB=$(positive_integer "${MAX_FILE_SIZE_MB:-50}")
SKILL_MB=$(positive_integer "${MAX_SKILL_BUNDLE_SIZE_MB:-256}")
if [ "$SKILL_MB" -lt "$FILE_MB" ] 2>/dev/null; then
  SKILL_MB=$FILE_MB
fi
if [ "$SKILL_MB" -gt 512 ] 2>/dev/null; then
  SKILL_MB=512
fi

# Values come from deployment configuration and may contain quotes, backslashes
# or newlines. Emit JSON strings instead of interpolating raw JavaScript.
json_string() {
  # A sentinel preserves trailing newlines, which awk record splitting would
  # otherwise discard. Encode every JSON control character, not just tabs.
  printf '%sx' "$1" | LC_ALL=C awk '
    BEGIN { for (i = 1; i < 32; i++) control[sprintf("%c", i)] = sprintf("\\u%04x", i) }
    { text = text (NR > 1 ? "\n" : "") $0 }
    END {
      text = substr(text, 1, length(text) - 1);
      printf "\"";
      for (i = 1; i <= length(text); i++) {
        c = substr(text, i, 1);
        if (c == "\\") printf "\\\\";
        else if (c == "\"") printf "\\\"";
        else if (c in control) printf "%s", control[c];
        else printf "%s", c;
      }
      printf "\"";
    }'
}

cat > /usr/share/nginx/html/config.js << EOF
window.__RUNTIME_CONFIG__ = {
  MAX_FILE_SIZE_MB: ${FILE_MB},
  MAX_SKILL_BUNDLE_SIZE_MB: ${SKILL_MB},
  DEFAULT_LOCALE: $(json_string "$RUNTIME_DEFAULT_LOCALE"),
  WEKNORA_WEKNORACLOUD_ENABLED: $(json_string "${WEKNORA_WEKNORACLOUD_ENABLED:-false}"),
  GENYEE_CHROME_EXTENSION_URL: $(json_string "${GENYEE_CHROME_EXTENSION_URL:-}"),
  GENYEE_CLAWHUB_SKILL_URL: $(json_string "${GENYEE_CLAWHUB_SKILL_URL:-}"),
  DOCS_BASE_URL: $(json_string "${DOCS_BASE_URL:-}"),
  WEBSITE_URL: $(json_string "${WEBSITE_URL:-}"),
  GITHUB_URL: $(json_string "${GITHUB_URL:-}")
};
EOF

# 处理 nginx 配置。
# 两个上限分开注入：全站保持知识库的 MAX_FILE_SIZE，只有技能 zip 上传的两条
# 集合路由放宽到 MAX_SKILL_BUNDLE_SIZE（不含 /install、PATCH 等子路径）。
# 合成一个全站上限会让每个上传端点都能收到技能包那么大的 body。
export MAX_FILE_SIZE=${FILE_MB}M
export MAX_SKILL_BUNDLE_SIZE=${SKILL_MB}M
export APP_HOST=${APP_HOST:-app}
export APP_PORT=${APP_PORT:-8080}
export APP_SCHEME=${APP_SCHEME:-http}
envsubst '${MAX_FILE_SIZE} ${MAX_SKILL_BUNDLE_SIZE} ${APP_HOST} ${APP_PORT} ${APP_SCHEME}' \
  < /etc/nginx/templates/default.conf.template > /etc/nginx/conf.d/default.conf

# 启动 nginx
exec nginx -g 'daemon off;'
