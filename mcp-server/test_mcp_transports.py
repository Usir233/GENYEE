#!/usr/bin/env python3
"""Regression tests for MCP 2.x transport compatibility."""

import asyncio
import os
import subprocess
import sys
import threading
import time
import unittest
from pathlib import Path

MCP_SERVER_DIR = Path(__file__).resolve().parent


class TransportRegressionTest(unittest.TestCase):
    def test_http_transport_is_stateless(self) -> None:
        from mcp.server import MCPServer

        import genyee_mcp_server as srv

        probe = MCPServer("probe")
        probe.streamable_http_app(host="127.0.0.1", stateless_http=srv.STREAMABLE_HTTP_STATELESS)
        self.assertTrue(probe.session_manager.stateless)

    def test_sse_message_path_matches_legacy_mount(self) -> None:
        from mcp.server import MCPServer
        from starlette.routing import Mount, Route

        import genyee_mcp_server as srv

        probe = MCPServer("probe")
        app = probe.sse_app(host="127.0.0.1", message_path=srv.SSE_MESSAGE_PATH)
        mount_paths = [route.path for route in app.routes if isinstance(route, (Mount, Route))]
        self.assertIn("/sse", mount_paths)
        self.assertIn(
            srv.SSE_MESSAGE_PATH.rstrip("/"),
            {path.rstrip("/") for path in mount_paths},
        )

    def test_genyee_client_session_is_thread_local(self) -> None:
        from genyee_mcp_server import GENYEEClient

        client = GENYEEClient("http://localhost:8080/api/v1", "test-key")
        try:
            barrier = threading.Barrier(2)
            sessions: dict[str, object] = {}

            def worker(name: str) -> None:
                barrier.wait()
                sessions[name] = client.session

            threads = [threading.Thread(target=worker, args=(name,)) for name in ("a", "b")]
            for thread in threads:
                thread.start()
            for thread in threads:
                thread.join()

            self.assertEqual(len(sessions), 2)
            self.assertIsNot(sessions["a"], sessions["b"])
        finally:
            client.close()


class StdioToolsListTest(unittest.TestCase):
    def test_tools_list_returns_31_tools(self) -> None:
        async def _run() -> int:
            from mcp import ClientSession, StdioServerParameters
            from mcp.client.stdio import stdio_client

            params = StdioServerParameters(
                command=sys.executable,
                args=[str(MCP_SERVER_DIR / "genyee_mcp_server.py")],
                env={
                    **os.environ,
                    "GENYEE_API_KEY": "test-key",
                },
            )
            async with (
                stdio_client(params) as (read, write),
                ClientSession(read, write) as session,
            ):
                await session.initialize()
                tools = await session.list_tools()
                return len(tools.tools)

        count = asyncio.run(_run())
        self.assertEqual(count, 31)


class HttpStatelessSmokeTest(unittest.TestCase):
    def test_initialize_does_not_require_mcp_session_id(self) -> None:
        port = 19876
        test_token = "test-token-0123456789abcdef01234567"
        env = {
            **os.environ,
            "MCP_SERVER_AUTH_TOKEN": test_token,
            "GENYEE_API_KEY": "test-key",
        }
        proc = subprocess.Popen(
            [
                sys.executable,
                "genyee_mcp_server.py",
                "--transport",
                "http",
                "--host",
                "127.0.0.1",
                "--port",
                str(port),
            ],
            cwd=MCP_SERVER_DIR,
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        try:
            deadline = time.time() + 10
            while time.time() < deadline:
                probe = subprocess.run(
                    [
                        "curl",
                        "-s",
                        "-D",
                        "-",
                        "-o",
                        "/dev/null",
                        "-X",
                        "POST",
                        f"http://127.0.0.1:{port}/mcp",
                        "-H",
                        f"Authorization: Bearer {test_token}",
                        "-H",
                        "Content-Type: application/json",
                        "-H",
                        "Accept: application/json, text/event-stream",
                        "-d",
                        (
                            '{"jsonrpc":"2.0","id":1,"method":"initialize",'
                            '"params":{"protocolVersion":"2025-03-26",'
                            '"capabilities":{},"clientInfo":{"name":"t","version":"1"}}}'
                        ),
                    ],
                    capture_output=True,
                    text=True,
                )
                if probe.returncode == 0 and "HTTP/" in probe.stdout:
                    break
                time.sleep(0.2)
            else:
                self.fail("HTTP server did not become ready in time")

            headers = probe.stdout.lower()
            self.assertIn("200 ok", headers)
            self.assertNotIn("mcp-session-id:", headers)
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()


if __name__ == "__main__":
    unittest.main()
