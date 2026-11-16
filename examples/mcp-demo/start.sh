#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

TOKEN_FILE=".mcp-demo-token"
if [[ -z "${MCP_SERVER_AUTH_TOKEN:-}" ]]; then
  if [[ ! -s "$TOKEN_FILE" ]]; then
    (
      umask 077
      python3 -c 'import secrets; print(secrets.token_hex(24))' > "$TOKEN_FILE"
    )
  fi
  chmod 0600 "$TOKEN_FILE"
  MCP_SERVER_AUTH_TOKEN="$(tr -d '\r\n ' < "$TOKEN_FILE")"
  export MCP_SERVER_AUTH_TOKEN
  AUTH_HINT="stored in $(pwd)/${TOKEN_FILE} (mode 0600)"
else
  export MCP_SERVER_AUTH_TOKEN
  AUTH_HINT="provided via MCP_SERVER_AUTH_TOKEN environment variable"
fi

export MCP_HOST="${MCP_HOST:-127.0.0.1}"
export MCP_PORT="${MCP_PORT:-8010}"
export MCP_TRANSPORT="${MCP_TRANSPORT:-http}"

VENV_DIR=".venv"
if [[ ! -d "$VENV_DIR" ]]; then
  echo "Creating virtualenv in $VENV_DIR ..."
  python3 -m venv "$VENV_DIR"
fi
# shellcheck disable=SC1091
source "$VENV_DIR/bin/activate"

if ! python -c "import mcp" 2>/dev/null; then
  echo "Installing dependencies..."
  pip install -r requirements.txt
fi

ENDPOINT_PATH="$([ "$MCP_TRANSPORT" = http ] && echo mcp || echo sse)"
TRANSPORT_LABEL="$([ "$MCP_TRANSPORT" = http ] && echo "HTTP Streamable" || echo "SSE")"

echo "MCP Demo"
echo "  transport : ${MCP_TRANSPORT}"
echo "  endpoint  : http://${MCP_HOST}:${MCP_PORT}/${ENDPOINT_PATH}"
echo "  auth token: ${AUTH_HINT}"
echo
echo "GENYEE UI → 设置 → MCP 服务 → 新建"
echo "  传输: ${TRANSPORT_LABEL}"
echo "  URL : http://${MCP_HOST}:${MCP_PORT}/${ENDPOINT_PATH}"
echo "  认证: Bearer (${AUTH_HINT})"
echo

exec python server.py --transport "$MCP_TRANSPORT" --host "$MCP_HOST" --port "$MCP_PORT"
