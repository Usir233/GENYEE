#!/usr/bin/env python3
"""Tests for MCP file_name / folder_path passthrough (issue #3173)."""

import sys
import types
import unittest
from collections.abc import Callable
from typing import TypeVar
from unittest import mock

_F = TypeVar("_F", bound=Callable[..., object])

# Lightweight stub so unit tests can import the client module without the
# full MCP SDK installed (CI installs real deps; this only covers the HTTP client).
if "mcp" not in sys.modules:
    mcp_pkg = types.ModuleType("mcp")
    mcp_server = types.ModuleType("mcp.server")

    class _MCPServer:
        def __init__(self, *args: object, **kwargs: object) -> None:
            del args, kwargs

        def tool(self) -> Callable[[_F], _F]:
            def decorator(fn: _F) -> _F:
                return fn

            return decorator

    mcp_server.__dict__["MCPServer"] = _MCPServer
    mcp_pkg.__dict__["server"] = mcp_server
    sys.modules["mcp"] = mcp_pkg
    sys.modules["mcp.server"] = mcp_server

import genyee_mcp_server as srv  # noqa: E402


class CreateKnowledgeFromFileTest(unittest.TestCase):
    def test_includes_file_name_in_multipart_when_set(self) -> None:
        client = srv.GENYEEClient("http://localhost:8080/api/v1", "k")
        try:
            payload = b"pdf-bytes"
            with (
                mock.patch("builtins.open", mock.mock_open(read_data=payload)),
                mock.patch.object(srv, "resolve_upload_file_path", return_value="/safe/doc.pdf"),
                mock.patch.object(client.session, "request") as req,
            ):
                req.return_value = mock.Mock(
                    status_code=200,
                    raise_for_status=lambda: None,
                    json=lambda: {"id": "k1"},
                )
                client.create_knowledge_from_file(
                    "kb-1",
                    "doc.pdf",
                    enable_multimodel=True,
                    file_name="docs/spec/doc.pdf",
                )
            body_stream = req.call_args.kwargs["data"]
            self.assertEqual(body_stream.fields["fileName"], "docs/spec/doc.pdf")
            self.assertEqual(body_stream.fields["enable_multimodel"], "true")
            wire = body_stream.read()
            self.assertIn(b'name="fileName"\r\n\r\ndocs/spec/doc.pdf', wire)
            self.assertIn(payload, wire)
        finally:
            client.close()

    def test_omits_file_name_when_empty(self) -> None:
        client = srv.GENYEEClient("http://localhost:8080/api/v1", "k")
        try:
            with (
                mock.patch("builtins.open", mock.mock_open(read_data=b"x")),
                mock.patch.object(srv, "resolve_upload_file_path", return_value="/safe/doc.pdf"),
                mock.patch.object(client.session, "request") as req,
            ):
                req.return_value = mock.Mock(
                    status_code=200,
                    raise_for_status=lambda: None,
                    json=lambda: {"id": "k1"},
                )
                client.create_knowledge_from_file("kb-1", "doc.pdf")
            body_stream = req.call_args.kwargs["data"]
            self.assertNotIn("fileName", body_stream.fields)
        finally:
            client.close()


class ListKnowledgeFolderTest(unittest.TestCase):
    def test_forwards_folder_path_and_scope(self) -> None:
        client = srv.GENYEEClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"items": []}) as req:
            client.list_knowledge(
                "kb-1", page=2, page_size=10, folder_path="docs", folder_scope="all"
            )
        params = req.call_args.kwargs["params"]
        self.assertEqual(params["page"], 2)
        self.assertEqual(params["page_size"], 10)
        self.assertEqual(params["folder_path"], "docs")
        self.assertEqual(params["folder_scope"], "all")

    def test_omits_folder_params_when_unset(self) -> None:
        client = srv.GENYEEClient("http://localhost:8080/api/v1", "k")
        with mock.patch.object(client, "_request", return_value={"items": []}) as req:
            client.list_knowledge("kb-1")
        params = req.call_args.kwargs["params"]
        self.assertNotIn("folder_path", params)
        self.assertNotIn("folder_scope", params)


if __name__ == "__main__":
    unittest.main()
