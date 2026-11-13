"""Network admission and installed CLI compatibility regressions."""

import asyncio
import contextlib
import io
import os
from pathlib import Path
import sys
from unittest.mock import patch

import pytest

import run_server
import genyee_mcp_server as server


@pytest.mark.parametrize("transport", ["http", "sse"])
def test_network_transport_requires_a_secret(transport):
    with patch.dict(os.environ, {"MCP_SERVER_AUTH_TOKEN": "  "}):
        with pytest.raises(SystemExit) as stopped:
            server.require_network_transport_auth(transport)
        assert stopped.value.code == 1


@pytest.mark.parametrize("headers,expected", [
    ([], 401),
    ([(b"authorization", b"Bearer wrong")], 401),
    ([(b"authorization", b"Bearer \xff")], 401),
    ([(b"authorization", b"Bearer local-secret")], 204),
    ([(b"x-mcp-auth-token", b"local-secret")], 204),
])
def test_network_auth_rejects_bad_headers_without_crashing(headers, expected):
    async def run():
        messages = []

        async def endpoint(scope, receive, send):
            await send({"type": "http.response.start", "status": 204, "headers": []})
            await send({"type": "http.response.body", "body": b""})

        async def send(message):
            messages.append(message)

        async def receive():
            return {"type": "http.request", "body": b""}

        await server.MCPAuthMiddleware(endpoint, "local-secret")(
            {"type": "http", "headers": headers}, receive, send
        )
        assert messages[0]["status"] == expected

    asyncio.run(run())


def test_legacy_launcher_diagnostics_recognize_new_environment_names():
    out = io.StringIO()
    with patch.dict(os.environ, {
        "GENYEE_BASE_URL": "http://example.invalid/api/v1",
        "GENYEE_API_KEY": "local-placeholder",
    }), contextlib.redirect_stderr(out):
        run_server.check_environment()
    assert "example.invalid" in out.getvalue()
    assert "未设置" not in out.getvalue()
    assert "local-placeholder" not in out.getvalue()


@pytest.mark.parametrize("command", [
    "genyee-mcp-server", "genyee-server",
])
def test_installed_commands_speak_mcp_stdio(command):
    async def run():
        from mcp import ClientSession, StdioServerParameters
        from mcp.client.stdio import stdio_client

        executable = Path(sys.executable).with_name(command)
        parameters = StdioServerParameters(command=str(executable), args=[], env={
            **os.environ, "MCP_TRANSPORT": "stdio", "GENYEE_API_KEY": "local-placeholder",
        })
        async with stdio_client(parameters) as (read, write):
            async with ClientSession(read, write) as session:
                initialized = await session.initialize()
                result = await session.list_tools()
                assert initialized.server_info.name == "genyee-server"
                assert len(result.tools) == 31

    asyncio.run(run())
