#!/usr/bin/env python3
"""列出 MCP Demo 暴露的工具（需先启动 server.py）。"""

from __future__ import annotations

import asyncio
import os
import sys
from pathlib import Path

import httpx
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client

MIN_AUTH_TOKEN_LENGTH = 32
DEFAULT_TOKEN_FILE = Path(__file__).resolve().parent / ".mcp-demo-token"


def _resolve_auth_token() -> str:
    token = os.getenv("MCP_SERVER_AUTH_TOKEN", "").strip()
    if not token and DEFAULT_TOKEN_FILE.is_file():
        token = DEFAULT_TOKEN_FILE.read_text(encoding="utf-8").strip()
    if len(token) < MIN_AUTH_TOKEN_LENGTH:
        raise RuntimeError(
            f"MCP_SERVER_AUTH_TOKEN must be at least {MIN_AUTH_TOKEN_LENGTH} characters "
            "(run ./start.sh first to generate .mcp-demo-token or export MCP_SERVER_AUTH_TOKEN)."
        )
    return token


async def main() -> int:
    base = os.getenv("MCP_DEMO_URL", "http://127.0.0.1:8010/mcp").strip()
    token = _resolve_auth_token()

    async with (
        httpx.AsyncClient(
            headers={"Authorization": f"Bearer {token}"},
            timeout=30.0,
        ) as http_client,
        streamable_http_client(base, http_client=http_client) as (read, write),
        ClientSession(read, write) as session,
    ):
        await session.initialize()
        tools = await session.list_tools()
        print(f"connected: {base}")
        print(f"tools ({len(tools.tools)}):")
        for tool in tools.tools:
            print(f"  - {tool.name}: {tool.description}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(asyncio.run(main()))
    except Exception as exc:
        print(f"failed: {exc}", file=sys.stderr)
        print("start the server first: ./start.sh", file=sys.stderr)
        raise SystemExit(1) from exc
