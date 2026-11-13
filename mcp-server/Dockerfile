FROM python:3.11.18-slim-bookworm

WORKDIR /app
RUN pip install --no-cache-dir "uv==0.13.0"
COPY . .
ENV UV_PYTHON_DOWNLOADS=never
RUN uv sync --locked --no-dev --no-editable --no-cache
ENV PATH="/app/.venv/bin:${PATH}"

LABEL org.opencontainers.image.vendor="GENYEE" \
      org.opencontainers.image.title="genyee-mcp-server" \
      org.opencontainers.image.licenses="LicenseRef-GENYEE-Commercial AND MIT"
COPY LICENSE LICENSE.GENYEE THIRD_PARTY_NOTICES.md /licenses/

RUN groupadd -g 10001 appuser && \
    useradd -u 10001 -g 10001 -m -s /usr/sbin/nologin appuser && \
    chmod -R a+rX,go-w /app /licenses

ENV MCP_HOST=0.0.0.0
ENV MCP_PORT=8000
ENV GENYEE_BASE_URL=http://app:8080/api/v1
# Set MCP_SERVER_AUTH_TOKEN at runtime; HTTP transport refuses to start without it.

USER 10001:10001

EXPOSE 8000

CMD ["genyee-mcp-server", "--transport", "http", "--host", "0.0.0.0", "--port", "8000"]
