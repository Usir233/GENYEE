FROM python:3.11.18-slim-bookworm

WORKDIR /app
ENV UV_PYTHON_DOWNLOADS=never \
    PYTHONDONTWRITEBYTECODE=1
RUN pip install --no-cache-dir "uv==0.13.0"

# Install locked third-party dependencies first so source edits preserve layer cache.
COPY pyproject.toml uv.lock README.md LICENSE LICENSE.GENYEE THIRD_PARTY_NOTICES.md ./
RUN uv sync --locked --no-dev --no-install-project --no-cache

# Copy only production server modules (do not ship test_*.py or tests/ into /app).
COPY genyee_mcp_server.py upload_paths.py main.py run_server.py run.py ./
RUN uv sync --locked --no-dev --no-editable --no-cache
ENV PATH="/app/.venv/bin:${PATH}"

LABEL org.opencontainers.image.vendor="GENYEE" \
      org.opencontainers.image.title="genyee-mcp" \
      org.opencontainers.image.licenses="LicenseRef-GENYEE-Commercial AND MIT"
COPY LICENSE LICENSE.GENYEE THIRD_PARTY_NOTICES.md /licenses/

RUN groupadd -g 10001 appuser && \
    useradd -u 10001 -g 10001 -m -s /usr/sbin/nologin appuser && \
    chmod -R a+rX,go-w /app /licenses

ENV MCP_TRANSPORT=http \
    MCP_HOST=0.0.0.0 \
    MCP_PORT=8000 \
    GENYEE_BASE_URL=http://app:8080/api/v1
# Set MCP_SERVER_AUTH_TOKEN at runtime; HTTP/SSE transports refuse to start without it.

USER 10001:10001

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["python", "-c", "import os, socket, sys; t = os.getenv('MCP_TRANSPORT', 'http').strip().lower(); sys.exit(0) if t == 'stdio' else None; h = os.getenv('MCP_HOST', '127.0.0.1').strip(); h = '127.0.0.1' if h in ('', '0.0.0.0') else ('::1' if h == '::' else h); p = int(os.getenv('MCP_PORT', '8000')); s = socket.create_connection((h, p), timeout=3); s.close()"]

CMD ["genyee-mcp-server"]
