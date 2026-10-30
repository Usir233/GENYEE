FROM python:3.11-slim

WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY . .
RUN pip install --no-cache-dir -e .

LABEL org.opencontainers.image.vendor="GENYEE" \
      org.opencontainers.image.title="genyee-mcp-server" \
      org.opencontainers.image.licenses="LicenseRef-GENYEE-Commercial AND MIT"
COPY LICENSE LICENSE.GENYEE THIRD_PARTY_NOTICES.md /licenses/

ENV MCP_HOST=0.0.0.0
ENV MCP_PORT=8000
ENV WEKNORA_BASE_URL=http://app:8080/api/v1
# Set MCP_SERVER_AUTH_TOKEN at runtime; HTTP transport refuses to start without it.

EXPOSE 8000

CMD ["weknora-mcp-server", "--transport", "http", "--host", "0.0.0.0", "--port", "8000"]
