#!/usr/bin/env python3
"""Unit tests for examples/mcp-demo and examples/skills/pdf-processing/scripts."""

from __future__ import annotations

import asyncio
import importlib.util
import json
import os
import subprocess
import sys
import types
import unittest
from pathlib import Path
from typing import Any, ClassVar
from unittest.mock import patch

REPO_EXAMPLES_DIR = Path(__file__).resolve().parent.parent
MCP_DEMO_DIR = REPO_EXAMPLES_DIR / "mcp-demo"
PDF_SCRIPTS_DIR = REPO_EXAMPLES_DIR / "skills" / "pdf-processing" / "scripts"


def _load_module(name: str, path: Path) -> Any:
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load module spec from {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def _load_server_module() -> Any:
    try:
        import mcp.server  # noqa: F401
    except ModuleNotFoundError:
        mcp_pkg = types.ModuleType("mcp")
        mcp_server_mod = types.ModuleType("mcp.server")
        mcp_ts_mod = types.ModuleType("mcp.server.transport_security")

        class StubMCPServer:
            def __init__(self, name: str, version: str = "0.0.0") -> None:
                self.name = name
                self.version = version

            def tool(self) -> Any:
                def decorator(fn: Any) -> Any:
                    return fn

                return decorator

        class StubTransportSecuritySettings:
            def __init__(self, **kwargs: Any) -> None:
                self.kwargs = kwargs

        mcp_server_mod.MCPServer = StubMCPServer  # type: ignore[attr-defined]
        mcp_ts_mod.TransportSecuritySettings = StubTransportSecuritySettings  # type: ignore[attr-defined]
        sys.modules.setdefault("mcp", mcp_pkg)
        sys.modules.setdefault("mcp.server", mcp_server_mod)
        sys.modules.setdefault("mcp.server.transport_security", mcp_ts_mod)

    return _load_module("mcp_demo_server", MCP_DEMO_DIR / "server.py")


class MCPDemoServerTests(unittest.TestCase):
    server: ClassVar[Any]

    @classmethod
    def setUpClass(cls) -> None:
        cls.server = _load_server_module()

    def test_lookup_policy_exact_key_not_shadowed_by_alias(self) -> None:
        res = self.server.lookup_policy("poc_deadline")
        self.assertEqual(res.get("topic"), "poc_deadline")
        self.assertIn("2024-03-01", res.get("answer", ""))

    def test_lookup_policy_case_insensitive_alias_and_no_single_char_false_match(
        self,
    ) -> None:
        poc_res = self.server.lookup_policy("POC")
        self.assertEqual(poc_res.get("topic"), "poc_owner")

        unrelated = self.server.lookup_policy("xyz")
        self.assertIn("available_topics", unrelated)
        self.assertNotIn("answer", unrelated)

    def test_uvicorn_tls_kwargs_requires_both_cert_and_key(self) -> None:
        with (
            patch.dict(
                os.environ,
                {"MCP_SSL_CERTFILE": "/tmp/cert.pem", "MCP_SSL_KEYFILE": ""},
                clear=False,
            ),
            self.assertRaises(ValueError),
        ):
            self.server._uvicorn_tls_kwargs()

    def test_auth_middleware_enforces_bearer_and_trimmed_header_token(self) -> None:
        secret = "a" * 32
        called = False

        async def ok_app(scope: dict[str, Any], receive: Any, send: Any) -> None:
            nonlocal called
            called = True
            await send({"type": "http.response.start", "status": 200, "headers": []})
            await send({"type": "http.response.body", "body": b"ok"})

        middleware = self.server.MCPAuthMiddleware(ok_app, secret)

        async def invoke(scope: dict[str, Any]) -> list[dict[str, Any]]:
            events: list[dict[str, Any]] = []

            async def send(msg: dict[str, Any]) -> None:
                events.append(msg)

            async def receive() -> dict[str, Any]:
                return {"type": "http.request", "body": b""}

            await middleware(scope, receive, send)
            return events

        unauth_events = asyncio.run(invoke({"type": "http", "headers": []}))
        self.assertEqual(unauth_events[0]["status"], 401)
        self.assertFalse(called)

        header_events = asyncio.run(
            invoke(
                {
                    "type": "http",
                    "headers": [
                        (b"x-mcp-auth-token", f"  {secret}\n".encode("latin-1"))
                    ],
                }
            )
        )
        self.assertEqual(header_events[0]["status"], 200)
        self.assertTrue(called)

        ws_events = asyncio.run(invoke({"type": "websocket", "headers": []}))
        self.assertEqual(ws_events[0]["type"], "websocket.close")


class PDFProcessingSkillScriptsTests(unittest.TestCase):
    def test_analyze_form_returns_valid_json(self) -> None:
        mod = _load_module("skill_analyze_form", PDF_SCRIPTS_DIR / "analyze_form.py")
        payload = json.loads(mod.analyze_form("sample.pdf"))
        self.assertIn("name", payload)
        self.assertTrue(payload["name"]["required"])

    def test_extract_text_cli_rejects_invalid_page_and_accepts_positive_page(
        self,
    ) -> None:
        script = str(PDF_SCRIPTS_DIR / "extract_text.py")
        env = {**os.environ, "PYTHONDONTWRITEBYTECODE": "1"}

        bad_zero = subprocess.run(
            [sys.executable, script, "sample.pdf", "--page", "0"],
            capture_output=True,
            text=True,
            env=env,
            check=False,
        )
        self.assertNotEqual(bad_zero.returncode, 0)

        missing_val = subprocess.run(
            [sys.executable, script, "sample.pdf", "--page"],
            capture_output=True,
            text=True,
            env=env,
            check=False,
        )
        self.assertNotEqual(missing_val.returncode, 0)

        good = subprocess.run(
            [sys.executable, script, "sample.pdf", "--page", "2"],
            capture_output=True,
            text=True,
            env=env,
            check=False,
        )
        self.assertEqual(good.returncode, 0)
        self.assertIn("Page: 2", good.stdout)


if __name__ == "__main__":
    unittest.main()
