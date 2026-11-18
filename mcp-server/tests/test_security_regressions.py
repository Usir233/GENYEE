"""Security regression tests for Python MCP Server (GENYEE-HIGH-18, HIGH-19, MED-12)."""

from __future__ import annotations

import asyncio
import importlib.util
import os
import subprocess
import sys
import threading
from collections.abc import Callable, Iterator, MutableMapping
from http.server import BaseHTTPRequestHandler, HTTPServer, ThreadingHTTPServer
from pathlib import Path
from typing import Any, BinaryIO, cast
from unittest import mock

import pytest
import uvicorn
from starlette.testclient import TestClient

import genyee_mcp_server as srv
import run_server
from upload_paths import (
    clear_active_transport,
    open_upload_file,
    open_upload_file_nofollow,
    resolve_upload_file_path,
    set_active_transport,
)


@pytest.fixture(autouse=True)
def _clean_mcp_env(monkeypatch: pytest.MonkeyPatch) -> Iterator[None]:
    clear_active_transport()
    for key in (
        "MCP_TRANSPORT",
        "MCP_ALLOWED_UPLOAD_DIRS",
        "MCP_SERVER_AUTH_TOKEN",
        "MCP_ENABLE_ADMIN_TOOLS",
        "MCP_ALLOWED_HOSTS",
        "MCP_ALLOWED_ORIGINS",
        "MCP_DISABLE_DNS_REBINDING_PROTECTION",
        "MCP_SSL_CERTFILE",
        "MCP_SSL_KEYFILE",
    ):
        monkeypatch.delenv(key, raising=False)
    yield
    clear_active_transport()


# ---------------------------------------------------------------------------
# GENYEE-HIGH-18: Client-Side Path Traversal (CSPT) protection
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "bad_segment",
    [
        "",
        "   ",
        " leading",
        "trailing ",
        ".",
        "..",
        "../tenants",
        "kb/../../tenants/1/api-keys",
        "kb\\..\\tenants",
        "kb?admin=true",
        "kb#fragment",
        "kb%2f..%2ftenants",
        "kb\x00null",
        "kb\r\nheader:1",
        "a" * 257,
    ],
)
def test_safe_path_segment_rejects_traversal_and_special_chars(
    bad_segment: str,
) -> None:
    with pytest.raises(ValueError):
        srv._safe_path_segment(bad_segment, "resource_id")


def test_safe_path_segment_allows_safe_identifiers() -> None:
    assert srv._safe_path_segment("kb-00000001", "kb_id") == "kb-00000001"
    assert (
        srv._safe_path_segment("550e8400-e29b-41d4-a716-446655440000", "kb_id")
        == "550e8400-e29b-41d4-a716-446655440000"
    )
    assert srv._safe_path_segment("wiki_page.v1", "slug") == "wiki_page.v1"


def test_wiki_read_page_allows_hierarchical_slugs_and_blocks_cspt(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = srv.GENYEEClient("http://example.invalid/api/v1", "placeholder-key")
    called_endpoints: list[str] = []

    def fake_request(method: str, endpoint: str, **kwargs: Any) -> dict[str, Any]:
        del kwargs
        called_endpoints.append(f"{method} {endpoint}")
        return {"data": {"slug": endpoint}}

    monkeypatch.setattr(client, "_request", fake_request)
    client.wiki_read_page("kb-001", "entity/acme-corp")
    client.wiki_read_page("kb-001", "concept/rag architecture")
    client.wiki_search("kb-001", "architecture", limit=5)
    client.wiki_index_view("kb-001", limit=20)
    assert called_endpoints == [
        "GET /knowledge-bases/kb-001/wiki/pages/entity/acme-corp",
        "GET /knowledge-bases/kb-001/wiki/pages/concept/rag%20architecture",
        "GET /knowledge-bases/kb-001/wiki/search",
        "GET /knowledge-bases/kb-001/wiki/index",
    ]

    for bad_slug in (
        "",
        "/entity/acme",
        "entity/acme/",
        "entity//acme",
        "entity/../tenants",
        "../tenants/1/api-keys",
        "entity\\acme",
        "entity/acme?admin=1",
        "entity/acme#frag",
        "entity/acme%2f..%2fsecret",
    ):
        with pytest.raises(ValueError):
            client.wiki_read_page("kb-001", bad_slug)
    assert len(called_endpoints) == 4


@pytest.mark.parametrize(
    "call_fn",
    [
        lambda c, bad: c.get_tenant(bad),
        lambda c, bad: c.get_knowledge_base(bad),
        lambda c, bad: c.update_knowledge_base(bad, {"name": "n"}),
        lambda c, bad: c.delete_knowledge_base(bad),
        lambda c, bad: c.hybrid_search(bad, "q", {}),
        lambda c, bad: c.create_knowledge_from_file(bad, "note.txt"),
        lambda c, bad: c.create_knowledge_from_url(bad, "https://example.com"),
        lambda c, bad: c.create_knowledge_from_text(bad, "t", "body"),
        lambda c, bad: c.update_knowledge_from_text(bad, "body"),
        lambda c, bad: c.list_knowledge(bad),
        lambda c, bad: c.get_knowledge(bad),
        lambda c, bad: c.delete_knowledge(bad),
        lambda c, bad: c.get_model(bad),
        lambda c, bad: c.get_session(bad),
        lambda c, bad: c.delete_session(bad),
        lambda c, bad: c.chat(bad, "hello"),
        lambda c, bad: c.agent_chat(bad, "hello", "agent-1"),
        lambda c, bad: c.get_agent(bad),
        lambda c, bad: c.list_chunks(bad),
        lambda c, bad: c.delete_chunk(bad, "chunk-1"),
        lambda c, bad: c.delete_chunk("know-1", bad),
        lambda c, bad: c.wiki_search(bad, "q"),
        lambda c, bad: c.wiki_read_page(bad, "overview"),
        lambda c, bad: c.wiki_read_page("kb-1", bad),
        lambda c, bad: c.wiki_index_view(bad),
    ],
)
def test_all_genyee_client_path_methods_block_cspt_before_network_io(
    monkeypatch: pytest.MonkeyPatch,
    call_fn: Callable[[srv.GENYEEClient, str], object],
) -> None:
    client = srv.GENYEEClient("http://example.invalid/api/v1", "placeholder-key")
    try:
        monkeypatch.setattr(
            client,
            "_request",
            lambda *a, **kw: pytest.fail("HTTP request must not be sent on CSPT input"),
        )
        monkeypatch.setattr(
            client,
            "_consume_sse_stream",
            lambda *a, **kw: pytest.fail("SSE request must not be sent on CSPT input"),
        )
        monkeypatch.setattr(
            client.session,
            "request",
            lambda *a, **kw: pytest.fail("Session request must not be sent on CSPT input"),
        )

        with pytest.raises(ValueError):
            call_fn(client, "../tenants/1/api-keys")
    finally:
        client.close()


# ---------------------------------------------------------------------------
# GENYEE-HIGH-19: Remote upload fail-closed, existence oracle & O_NOFOLLOW
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("transport", ["http", "sse", "streamable-http"])
def test_remote_transports_fail_closed_without_explicit_upload_dirs(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, transport: str
) -> None:
    doc = tmp_path / "doc.txt"
    doc.write_text("allowed only when configured", encoding="utf-8")
    monkeypatch.chdir(tmp_path)
    set_active_transport(transport)

    with pytest.raises(ValueError, match="disabled"):
        resolve_upload_file_path(str(doc))


def test_resolve_upload_file_path_eliminates_outside_existence_oracle(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    allowed_dir = tmp_path / "uploads"
    allowed_dir.mkdir()
    outside_dir = tmp_path / "outside"
    outside_dir.mkdir()
    existing_outside = outside_dir / "real_secret.txt"
    existing_outside.write_text("sensitive", encoding="utf-8")
    missing_outside = outside_dir / "nonexistent_secret.txt"

    set_active_transport("http")
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(allowed_dir))

    with pytest.raises(ValueError) as exc_existing:
        resolve_upload_file_path(str(existing_outside))
    with pytest.raises(ValueError) as exc_missing:
        resolve_upload_file_path(str(missing_outside))

    assert str(exc_existing.value) == str(exc_missing.value)
    assert "outside allowed upload directories" in str(exc_existing.value)


@pytest.mark.parametrize(
    "rel_sensitive",
    [
        ".env",
        ".env.production",
        ".git/config",
        ".ssh/id_ed25519",
        ".gnupg/secring.gpg",
        "subdir/.env",
    ],
)
def test_resolve_upload_file_path_blocks_sensitive_hidden_segments(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, rel_sensitive: str
) -> None:
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(tmp_path))
    target = tmp_path / rel_sensitive
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("placeholder", encoding="utf-8")

    with pytest.raises(ValueError, match="restricted hidden"):
        resolve_upload_file_path(str(target))

    missing_sensitive = tmp_path / "missing_dir" / rel_sensitive
    with pytest.raises(ValueError, match="restricted hidden"):
        resolve_upload_file_path(str(missing_sensitive))


def test_open_upload_file_nofollow_blocks_symlink_swap(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(tmp_path))
    real_file = tmp_path / "doc.txt"
    real_file.write_text("safe content", encoding="utf-8")
    other_file = tmp_path / "other.txt"
    other_file.write_text("other content", encoding="utf-8")

    resolved, handle = open_upload_file(str(real_file))
    with handle:
        assert handle.read() == b"safe content"

    real_file.unlink()
    real_file.symlink_to(other_file)
    with pytest.raises(ValueError):
        open_upload_file_nofollow(resolved)

    sub_dir = tmp_path / "nested"
    sub_dir.mkdir()
    nested_file = sub_dir / "report.txt"
    nested_file.write_text("report", encoding="utf-8")
    outside_dir = tmp_path.parent / f"outside_{tmp_path.name}"
    outside_dir.mkdir(exist_ok=True)
    (outside_dir / "report.txt").write_text("secret", encoding="utf-8")
    try:
        nested_resolved = resolve_upload_file_path(str(nested_file))
        nested_file.unlink()
        sub_dir.rmdir()
        sub_dir.symlink_to(outside_dir, target_is_directory=True)
        with pytest.raises(ValueError):
            open_upload_file_nofollow(nested_resolved)
    finally:
        if sub_dir.is_symlink():
            sub_dir.unlink()
        if (outside_dir / "report.txt").exists():
            (outside_dir / "report.txt").unlink()
        if outside_dir.exists():
            outside_dir.rmdir()


def test_open_upload_file_nofollow_fails_closed_outside_roots_and_on_root_swap(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    allowed_dir = tmp_path / "allowed"
    allowed_dir.mkdir()
    outside_dir = tmp_path / "outside"
    outside_dir.mkdir()
    outside_secret = outside_dir / "secret.txt"
    outside_secret.write_text("do-not-open", encoding="utf-8")

    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(allowed_dir))

    with (
        mock.patch("upload_paths.os.open", side_effect=AssertionError("os.open called")),
        pytest.raises(ValueError, match="outside allowed upload directories"),
    ):
        open_upload_file_nofollow(str(outside_secret))

    monkeypatch.delenv("MCP_ALLOWED_UPLOAD_DIRS", raising=False)
    set_active_transport("http")
    safe_file = allowed_dir / "doc.txt"
    safe_file.write_text("content", encoding="utf-8")
    with (
        mock.patch("upload_paths.os.open", side_effect=AssertionError("os.open called")),
        pytest.raises(ValueError, match="disabled"),
    ):
        open_upload_file_nofollow(str(safe_file))

    clear_active_transport()
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(allowed_dir))
    resolved = resolve_upload_file_path(str(safe_file))
    safe_file.unlink()
    allowed_dir.rmdir()
    allowed_dir.symlink_to(outside_dir, target_is_directory=True)
    try:
        with pytest.raises(ValueError):
            open_upload_file_nofollow(resolved)
    finally:
        if allowed_dir.is_symlink():
            allowed_dir.unlink()

    allowed_dir.mkdir()
    fresh_doc = allowed_dir / "doc.txt"
    fresh_doc.write_text("original root content", encoding="utf-8")
    resolved_fresh = resolve_upload_file_path(str(fresh_doc))
    replacement_dir = tmp_path / "replacement_allowed"
    replacement_dir.mkdir()
    (replacement_dir / "doc.txt").write_text("swapped root content", encoding="utf-8")
    fresh_doc.unlink()
    allowed_dir.rmdir()
    replacement_dir.rename(allowed_dir)
    with pytest.raises(ValueError, match="upload root directory changed"):
        open_upload_file_nofollow(resolved_fresh)


def test_open_upload_file_nofollow_closes_root_fd_on_fstat_mismatch(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    allowed_dir = tmp_path / "allowed_fstat"
    allowed_dir.mkdir()
    doc = allowed_dir / "doc.txt"
    doc.write_text("ok", encoding="utf-8")
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(allowed_dir))

    resolved = resolve_upload_file_path(str(doc))

    real_os_open = os.open
    real_os_close = os.close
    real_os_fstat = os.fstat
    opened_fds: list[int] = []
    closed_fds: list[int] = []

    def tracking_open(path: str, flags: int, *args: Any, **kwargs: Any) -> int:
        fd = real_os_open(path, flags, *args, **kwargs)
        opened_fds.append(fd)
        return fd

    def tracking_close(fd: int) -> None:
        closed_fds.append(fd)
        real_os_close(fd)

    def mismatched_fstat(fd: int) -> os.stat_result:
        st = real_os_fstat(fd)
        return os.stat_result(
            (
                st.st_mode,
                st.st_ino + 999999,
                st.st_dev,
                st.st_nlink,
                st.st_uid,
                st.st_gid,
                st.st_size,
                int(st.st_atime),
                int(st.st_mtime),
                int(st.st_ctime),
            )
        )

    monkeypatch.setattr("upload_paths.os.open", tracking_open)
    monkeypatch.setattr("upload_paths.os.close", tracking_close)
    monkeypatch.setattr("upload_paths.os.fstat", mismatched_fstat)

    with pytest.raises(ValueError, match="upload root directory changed during open"):
        open_upload_file_nofollow(resolved)

    assert len(opened_fds) == 1
    assert closed_fds == opened_fds


def test_run_server_bootstraps_sys_path_outside_mcp_dir(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    run_server_path = Path(__file__).resolve().parents[1] / "run_server.py"
    mcp_dir = str(run_server_path.parent)
    monkeypatch.chdir(tmp_path)
    cleaned_path = [p for p in sys.path if os.path.abspath(p or ".") != mcp_dir]
    monkeypatch.setattr(sys, "path", cleaned_path)
    monkeypatch.setattr(sys, "argv", ["run_server.py"])

    spec = importlib.util.spec_from_file_location("run_server_isolated", run_server_path)
    assert spec and spec.loader
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)

    async def _noop_run() -> None:
        return None

    assert mcp_dir in sys.path
    with mock.patch("genyee_mcp_server.run", side_effect=_noop_run):
        mod.main([])


# ---------------------------------------------------------------------------
# GENYEE-MED-12: Token length, TransportSecuritySettings & admin tools gate
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("transport", ["http", "sse"])
def test_require_network_transport_auth_enforces_min_32_char_token(
    monkeypatch: pytest.MonkeyPatch, transport: str
) -> None:
    monkeypatch.setenv("MCP_SERVER_AUTH_TOKEN", "short-token-12345")
    with pytest.raises(SystemExit) as exc:
        srv.require_network_transport_auth(transport)
    assert exc.value.code == 1

    strong = "a" * 32
    monkeypatch.setenv("MCP_SERVER_AUTH_TOKEN", strong)
    assert srv.require_network_transport_auth(transport) == strong


def test_wildcard_bind_still_enforces_dns_rebinding_host_and_origin_allowlist(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    strong_token = "t" * 32
    monkeypatch.setenv("MCP_ALLOWED_HOSTS", "mcp.internal.test")
    monkeypatch.setenv("MCP_ALLOWED_ORIGINS", "https://portal.internal.test")

    security = srv.build_transport_security_settings("0.0.0.0", 8000)
    assert security.enable_dns_rebinding_protection is True

    probe_mcp = srv.MCPServer("security-probe")
    app = srv.MCPAuthMiddleware(
        probe_mcp.streamable_http_app(
            host="0.0.0.0",
            stateless_http=True,
            transport_security=security,
        ),
        strong_token,
    )

    init_payload = {
        "jsonrpc": "2.0",
        "id": 1,
        "method": "initialize",
        "params": {
            "protocolVersion": "2025-03-26",
            "capabilities": {},
            "clientInfo": {"name": "sec-test", "version": "1.0"},
        },
    }
    base_headers = {
        "Authorization": f"Bearer {strong_token}",
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
    }

    with TestClient(app) as client:
        bad_host_resp = client.post(
            "/mcp",
            json=init_payload,
            headers={**base_headers, "Host": "attacker-rebind.invalid:8000"},
        )
        assert bad_host_resp.status_code == 421

        bad_origin_resp = client.post(
            "/mcp",
            json=init_payload,
            headers={
                **base_headers,
                "Host": "127.0.0.1:8000",
                "Origin": "http://evil.invalid",
            },
        )
        assert bad_origin_resp.status_code == 403

        good_resp = client.post(
            "/mcp",
            json=init_payload,
            headers={
                **base_headers,
                "Host": "mcp.internal.test:8000",
                "Origin": "https://portal.internal.test",
            },
        )
        assert good_resp.status_code == 200


def test_admin_tools_disabled_by_default_and_opt_in_via_env(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    with pytest.raises(PermissionError, match="MCP_ENABLE_ADMIN_TOOLS"):
        srv.create_tenant("t1", "desc", "biz")
    with pytest.raises(PermissionError, match="MCP_ENABLE_ADMIN_TOOLS"):
        srv.list_tenants()
    with pytest.raises(PermissionError, match="MCP_ENABLE_ADMIN_TOOLS"):
        srv.create_model("m1", "Embedding", "desc")

    monkeypatch.setenv("MCP_ENABLE_ADMIN_TOOLS", "true")
    with mock.patch.object(srv.client, "list_tenants", return_value={"data": []}):
        assert srv.list_tenants() == {"data": []}


def test_mcp_demo_server_enforces_token_length_and_dns_rebinding_protection(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    demo_path = Path(__file__).resolve().parents[2] / "examples" / "mcp-demo" / "server.py"
    spec = importlib.util.spec_from_file_location("mcp_demo_server_test", demo_path)
    assert spec and spec.loader
    demo = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(demo)

    monkeypatch.setenv("MCP_SERVER_AUTH_TOKEN", "too-short")
    with pytest.raises(SystemExit):
        demo.require_network_transport_auth("http")

    settings = demo.build_transport_security_settings("0.0.0.0", 8010)
    assert settings.enable_dns_rebinding_protection is True
    assert "127.0.0.1:*" in settings.allowed_hosts


# ---------------------------------------------------------------------------
# Wave 1 Audit Regressions: Redirect API-Key Leak, Stream Bounds, ASGI & Env
# ---------------------------------------------------------------------------


def test_genyee_client_blocks_cross_origin_redirect_api_key_leak(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    captured_headers: list[dict[str, str]] = []

    class AttackerHandler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            captured_headers.append({k.lower(): v for k, v in self.headers.items()})
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"data": []}')

        def log_message(self, format: str, *args: Any) -> None:
            del format, args

    attacker_srv = HTTPServer(("127.0.0.1", 0), AttackerHandler)
    attacker_port = attacker_srv.server_address[1]
    attacker_thread = threading.Thread(target=attacker_srv.serve_forever, daemon=True)
    attacker_thread.start()

    class OriginHandler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            if self.path == "/api/v1/same-origin-hop":
                self.send_response(307)
                self.send_header("Location", "/api/v1/same-origin-final")
                self.end_headers()
                return
            if self.path == "/api/v1/same-origin-final":
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"ok": true}')
                return
            self.send_response(302)
            self.send_header("Location", f"http://127.0.0.1:{attacker_port}/steal")
            self.end_headers()

        def log_message(self, format: str, *args: Any) -> None:
            del format, args

    origin_srv = HTTPServer(("127.0.0.1", 0), OriginHandler)
    origin_port = origin_srv.server_address[1]
    origin_thread = threading.Thread(target=origin_srv.serve_forever, daemon=True)
    origin_thread.start()

    client = srv.GENYEEClient(f"http://127.0.0.1:{origin_port}/api/v1", "secret-x-api-key")
    try:
        assert client._request("GET", "/same-origin-hop") == {"ok": True}

        with pytest.raises(srv.RequestException):
            client.list_tenants()
        assert captured_headers == [], "X-API-Key must never reach redirected cross-origin host"
    finally:
        client.close()
        origin_srv.shutdown()
        attacker_srv.shutdown()
        origin_srv.server_close()
        attacker_srv.server_close()

    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(tmp_path))
    upload_doc = tmp_path / "note.txt"
    upload_doc.write_text("hello", encoding="utf-8")
    upload_client = srv.GENYEEClient("http://example.invalid/api/v1", "secret-key")
    try:
        with mock.patch.object(upload_client.session, "request") as req_mock:
            req_mock.return_value = mock.Mock(
                status_code=200,
                raise_for_status=lambda: None,
                json=lambda: {"data": {"id": "k-1"}},
            )
            upload_client.create_knowledge_from_file("kb-1", str(upload_doc))
            assert req_mock.call_args.kwargs.get("allow_redirects") is False
            assert req_mock.call_args.kwargs.get("stream") is True
    finally:
        upload_client.close()


def test_iter_sse_lines_and_request_bound_oversized_responses(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(srv, "MAX_SSE_EVENT_BYTES", 2048)

    class FakeStreamingResponse:
        def iter_content(self, chunk_size: int = 512) -> Iterator[bytes]:
            del chunk_size
            for _ in range(10):
                yield b"A" * 512

    with pytest.raises(srv.RequestException, match="SSE line exceeds"):
        list(srv._iter_sse_lines(FakeStreamingResponse()))

    monkeypatch.setattr(srv, "MAX_API_RESPONSE_BYTES", 2048)

    class OversizedJSONHandler(BaseHTTPRequestHandler):
        def do_GET(self) -> None:
            body = ('{"pad":"' + ("B" * 8192) + '"}').encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, format: str, *args: Any) -> None:
            del format, args

    json_srv = HTTPServer(("127.0.0.1", 0), OversizedJSONHandler)
    port = json_srv.server_address[1]
    thread = threading.Thread(target=json_srv.serve_forever, daemon=True)
    thread.start()
    client = srv.GENYEEClient(f"http://127.0.0.1:{port}/api/v1", "key")
    try:
        with pytest.raises(srv.RequestException, match="exceeds"):
            client.list_tenants()
    finally:
        client.close()
        json_srv.shutdown()
        json_srv.server_close()


def test_mcp_auth_middleware_gates_websocket_and_preserves_lifespan() -> None:
    async def run() -> None:
        called: list[str] = []
        sent: list[MutableMapping[str, Any]] = []

        async def app(
            scope: srv.ASGIScope,
            receive: srv.ASGIReceive,
            send: srv.ASGISend,
        ) -> None:
            del receive, send
            called.append(str(scope["type"]))

        async def send(msg: MutableMapping[str, Any]) -> None:
            sent.append(msg)

        async def receive() -> MutableMapping[str, Any]:
            return {"type": "websocket.connect"}

        mw = srv.MCPAuthMiddleware(app, "secret-token")

        await mw({"type": "lifespan"}, receive, send)
        assert called == ["lifespan"]

        sent.clear()
        await mw({"type": "websocket", "headers": []}, receive, send)
        assert called == ["lifespan"]
        assert any(m.get("type") == "websocket.close" and m.get("code") == 1008 for m in sent)

        await mw(
            {
                "type": "websocket",
                "headers": [(b"authorization", b"Bearer secret-token")],
            },
            receive,
            send,
        )
        assert called == ["lifespan", "websocket"]

    asyncio.run(run())


def test_non_positive_timeouts_and_invalid_mcp_port_env_handling() -> None:
    mcp_dir = Path(__file__).resolve().parents[1]

    for var_name in ("GENYEE_API_TIMEOUT", "GENYEE_CHAT_TIMEOUT"):
        for bad_val in ("0", "-5"):
            env = dict(os.environ)
            env["PYTHONDONTWRITEBYTECODE"] = "1"
            env[var_name] = bad_val
            res = subprocess.run(
                [
                    sys.executable,
                    "-c",
                    f"import genyee_mcp_server as s; print(s.{var_name})",
                ],
                cwd=mcp_dir,
                env=env,
                capture_output=True,
                text=True,
                timeout=20,
            )
            assert res.returncode == 0, res.stderr
            assert int(res.stdout.strip()) == 300

    for bad_port in ("", "not-a-port"):
        env = dict(os.environ)
        env["PYTHONDONTWRITEBYTECODE"] = "1"
        env["MCP_PORT"] = bad_port
        res = subprocess.run(
            [sys.executable, "main.py", "--check-only"],
            cwd=mcp_dir,
            env=env,
            capture_output=True,
            text=True,
            timeout=20,
        )
        assert res.returncode == 0, res.stderr


# ---------------------------------------------------------------------------
# Wave 5 Shard D Regressions: Streaming Upload, Session Lifecycle, SSE Cap &
# UTF-8 Safety, 204/Error Handling, KB Resolution, Bounds & Entrypoint Parity
# ---------------------------------------------------------------------------


def test_create_knowledge_from_file_streams_chunks_via_session_without_header_mutation(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    received_requests: list[tuple[dict[str, str], bytes]] = []

    class UploadHandler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0"))
            body = self.rfile.read(length)
            received_requests.append(({k.lower(): v for k, v in self.headers.items()}, body))
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            resp = b'{"data":{"id":"k-streamed"}}'
            self.send_header("Content-Length", str(len(resp)))
            self.end_headers()
            self.wfile.write(resp)

        def log_message(self, format: str, *args: Any) -> None:
            del format, args

    upload_srv = ThreadingHTTPServer(("127.0.0.1", 0), UploadHandler)
    upload_thread = threading.Thread(target=upload_srv.serve_forever, daemon=True)
    upload_thread.start()

    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(tmp_path))
    doc = tmp_path / "large.bin"
    payload = b"0123456789abcdef" * 8192  # 128 KiB (> 64 KiB chunk size)
    doc.write_bytes(payload)

    real_open_nofollow = srv.open_upload_file_nofollow
    read_sizes: list[int] = []

    def tracking_open(path: str) -> BinaryIO:
        raw_handle = real_open_nofollow(path)

        class TrackingReader:
            def __init__(self, wrapped: BinaryIO) -> None:
                self._wrapped = wrapped

            def fileno(self) -> int:
                return self._wrapped.fileno()

            def tell(self) -> int:
                return self._wrapped.tell()

            def read(self, size: int = -1) -> bytes:
                read_sizes.append(size)
                return self._wrapped.read(size)

            def close(self) -> None:
                self._wrapped.close()

            def __enter__(self) -> TrackingReader:
                return self

            def __exit__(self, exc_type: object, exc: object, tb: object) -> None:
                self.close()

        return cast(BinaryIO, TrackingReader(raw_handle))

    monkeypatch.setattr(srv, "open_upload_file_nofollow", tracking_open)

    try:
        client = srv.GENYEEClient(
            f"http://127.0.0.1:{upload_srv.server_address[1]}/api/v1",
            "upload-api-key",
        )
        client.session.trust_env = False
        try:
            with mock.patch.object(
                client.session, "request", wraps=client.session.request
            ) as session_req:
                res = client.create_knowledge_from_file(
                    "kb-1",
                    str(doc),
                    enable_multimodal=False,
                    file_name="docs/large.bin",
                )
            assert res == {"data": {"id": "k-streamed"}}
            assert session_req.called
            assert session_req.call_args.kwargs.get("stream") is True
            assert session_req.call_args.kwargs.get("allow_redirects") is False
            assert client.session.headers.get("Content-Type") == "application/json"
            assert read_sizes and all(0 < s <= 65536 for s in read_sizes)
            assert len(received_requests) == 1
            headers, wire_body = received_requests[0]
            assert headers.get("x-api-key") == "upload-api-key"
            assert "multipart/form-data; boundary=" in headers.get("content-type", "")
            assert b'name="enable_multimodal"\r\n\r\nfalse' in wire_body
            assert b'name="file_name"\r\n\r\ndocs/large.bin' in wire_body
            assert payload in wire_body
        finally:
            client.close()
    finally:
        upload_srv.shutdown()
        upload_srv.server_close()


def test_genyee_client_closes_dead_worker_thread_sessions_and_on_close() -> None:
    client = srv.GENYEEClient("http://example.invalid/api/v1", "key")
    closed_flags: list[bool] = []

    def spawn_and_finish() -> None:
        sess = client.session
        orig_close = sess.close

        def mark_closed() -> None:
            closed_flags.append(True)
            orig_close()

        sess.__dict__["close"] = mark_closed

    t1 = threading.Thread(target=spawn_and_finish)
    t1.start()
    t1.join()

    t2 = threading.Thread(target=spawn_and_finish)
    t2.start()
    t2.join()
    assert len(closed_flags) >= 1

    client.close()
    assert len(closed_flags) == 2


def test_sse_stream_enforces_cumulative_cap_utf8_safety_and_omits_debug_events(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = srv.GENYEEClient("http://example.invalid/api/v1", "key")
    try:
        valid_wire = (
            'data: {"response_type":"answer","content":"你好🚀"}\n\n'
            'data: {"response_type":"complete"}\n\n'
        ).encode()
        byte_chunks = [bytes([b]) for b in valid_wire]
        resp_ok = srv.requests.Response()
        resp_ok.status_code = 200
        resp_ok._content_consumed = True
        with (
            mock.patch.object(resp_ok, "iter_content", return_value=iter(byte_chunks)),
            mock.patch.object(client.session, "post", return_value=resp_ok),
        ):
            out = client.chat("sess-1", "hi")
        assert out["answer"] == "你好🚀"
        assert "_debug_events" not in out

        bad_utf8_wire = (
            b'data: {"response_type":"answer","content":"prefix-\xff-suffix"}\n\n'
            b'data: {"response_type":"complete"}\n\n'
        )
        resp_bad = srv.requests.Response()
        resp_bad.status_code = 200
        resp_bad._content_consumed = True
        with (
            mock.patch.object(resp_bad, "iter_content", return_value=iter([bad_utf8_wire])),
            mock.patch.object(client.session, "post", return_value=resp_bad),
        ):
            out_bad = client.chat("sess-1", "hi")
        assert "\ufffd" in out_bad["answer"]

        monkeypatch.setattr(srv, "MAX_SSE_STREAM_BYTES", 120)
        many_frames = (
            b'data: {"response_type":"answer","content":"0123456789abcdef"}\n\n' * 5
            + b'data: {"response_type":"complete"}\n\n'
        )
        resp_cap = srv.requests.Response()
        resp_cap.status_code = 200
        resp_cap._content_consumed = True
        with (
            mock.patch.object(resp_cap, "iter_content", return_value=iter([many_frames])),
            mock.patch.object(client.session, "post", return_value=resp_cap),
            mock.patch.object(resp_cap, "close", wraps=resp_cap.close) as close_spy,
        ):
            with pytest.raises(srv.RequestException, match="SSE stream exceeds"):
                client.chat("sess-1", "hi")
            assert close_spy.called
    finally:
        client.close()


def test_read_bounded_json_handles_204_structured_errors_and_non_json() -> None:
    resp_204 = srv.requests.Response()
    resp_204.status_code = 204
    resp_204.headers["Content-Length"] = "0"
    with mock.patch.object(resp_204, "iter_content", return_value=iter([])):
        assert srv._read_bounded_json_response(resp_204) == {}

    resp_400 = srv.requests.Response()
    resp_400.status_code = 400
    resp_400.url = "http://example.invalid/api/v1/knowledge-bases"
    err_bytes = b'{"success":false,"error":{"code":1000,"message":"Invalid chunk overlap"}}'
    with mock.patch.object(resp_400, "iter_content", return_value=iter([err_bytes])):
        with pytest.raises(srv.requests.HTTPError, match="Invalid chunk overlap") as exc_info:
            srv._read_bounded_json_response(resp_400)
        assert exc_info.value.response is resp_400

    resp_502 = srv.requests.Response()
    resp_502.status_code = 502
    resp_502.url = "http://example.invalid/api/v1/models"
    with mock.patch.object(
        resp_502, "iter_content", return_value=iter([b"<html>Bad Gateway</html>"])
    ):
        with pytest.raises(srv.requests.HTTPError, match="502") as exc_502:
            srv._read_bounded_json_response(resp_502)
        assert exc_502.value.response is resp_502

    resp_html = srv.requests.Response()
    resp_html.status_code = 200
    resp_html.url = "http://example.invalid/api/v1/models"
    with (
        mock.patch.object(resp_html, "iter_content", return_value=iter([b"<html>login</html>"])),
        pytest.raises(srv.RequestException, match="non-JSON"),
    ):
        srv._read_bounded_json_response(resp_html)


def test_resolve_kb_id_hardening_tool_resolution_and_parameter_bounds(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = srv.GENYEEClient("http://example.invalid/api/v1", "key")
    try:
        monkeypatch.setattr(
            client,
            "_request",
            lambda *a, **kw: pytest.fail("must not send HTTP request on blank kb_id"),
        )
        for bad_kb in ("", "   "):
            with pytest.raises(ValueError):
                client.resolve_kb_id(bad_kb)

        def fake_kb_list(method: str, path: str, **kwargs: Any) -> dict[str, Any]:
            del method, kwargs
            if path == "/knowledge-bases":
                return {
                    "data": [
                        {"id": "kb-null-name", "name": None},
                        {"id": "kb-custom-id", "name": "Engineering KB"},
                    ]
                }
            return {"data": []}

        monkeypatch.setattr(client, "_request", fake_kb_list)
        assert client.resolve_kb_id("kb-custom-id") == "kb-custom-id"
        assert client.resolve_kb_id("engineering kb") == "kb-custom-id"

        monkeypatch.setattr(
            client,
            "_request",
            lambda *a, **kw: pytest.fail("must not send HTTP request on invalid bounds"),
        )
        with pytest.raises(ValueError):
            client.list_knowledge("kb-1", page=0)
        with pytest.raises(ValueError):
            client.list_knowledge("kb-1", page_size=101)
        with pytest.raises(ValueError):
            client.list_sessions(page=True)
        with pytest.raises(ValueError):
            client.list_chunks("know-1", page_size=0)
        with pytest.raises(ValueError):
            client.wiki_search("kb-1", "q", limit=0)
        with pytest.raises(ValueError):
            client.wiki_index_view("kb-1", limit=101)
        with pytest.raises(ValueError):
            client.hybrid_search("kb-1", "q", {"vector_threshold": 1.5})
        with pytest.raises(ValueError):
            client.hybrid_search("kb-1", "q", {"keyword_threshold": -0.1})
        with pytest.raises(ValueError):
            client.hybrid_search("kb-1", "q", {"match_count": 0})
        with pytest.raises(ValueError):
            client.create_knowledge_from_url("kb-1", "file:///etc/passwd")
        with pytest.raises(ValueError):
            client.create_knowledge_from_text("kb-1", "t", "c", status="invalid")
        with pytest.raises(ValueError):
            client.update_knowledge_from_text("know-1", "c", status="archived")
    finally:
        client.close()

    with (
        mock.patch.object(srv.client, "resolve_kb_id", return_value="uuid-kb") as rkb,
        mock.patch.object(srv.client, "wiki_search", return_value={"data": []}) as ws,
    ):
        srv.wiki_search("Engineering KB", "auth", limit=5)
    rkb.assert_called_once_with("Engineering KB")
    ws.assert_called_once_with("uuid-kb", "auth", 5)


def test_agent_chat_sets_agent_enabled_and_list_agents_drops_dead_pagination(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    client = srv.GENYEEClient("http://example.invalid/api/v1", "key")
    try:
        captured: dict[str, Any] = {}

        def fake_sse(url: str, body: dict[str, Any]) -> dict[str, Any]:
            captured["url"] = url
            captured["body"] = body
            return {"answer": "ok", "references": []}

        monkeypatch.setattr(client, "_consume_sse_stream", fake_sse)
        client.agent_chat("sess-1", "hello", "agent-1")
        assert captured["body"]["agent_enabled"] is True

        req_calls: list[tuple[str, str, dict[str, Any]]] = []

        def fake_req(method: str, path: str, **kw: Any) -> dict[str, Any]:
            req_calls.append((method, path, kw))
            return {"data": []}

        monkeypatch.setattr(client, "_request", fake_req)
        assert client.list_agents() == {"data": []}
        assert req_calls == [("GET", "/agents", {})]
    finally:
        client.close()

    tools = {tool.name: tool for tool in asyncio.run(srv.mcp.list_tools())}
    assert set(tools["list_agents"].input_schema.get("properties", {}).keys()) == set()
    for tool_name in ("create_knowledge_from_file", "create_knowledge_from_url"):
        props = set(tools[tool_name].input_schema.get("properties", {}).keys())
        assert "enable_multimodal" in props
        assert "enable_multimodel" not in props


def test_run_server_transport_parity_and_uvicorn_graceful_shutdown_timeout(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setenv("MCP_TRANSPORT", " HTTP ")
    monkeypatch.setenv("MCP_HOST", "0.0.0.0")
    monkeypatch.setenv("MCP_PORT", "9123")
    args = run_server.parse_arguments([])
    assert args.transport == "http"
    assert args.host == "0.0.0.0"
    assert args.port == 9123

    dispatched: list[tuple[str, str, int]] = []

    async def fake_run_http(host: str, port: int) -> None:
        dispatched.append(("http", host, port))

    with mock.patch("genyee_mcp_server.run_http", side_effect=fake_run_http):
        run_server.main(["--transport", "http", "--host", "127.0.0.1", "--port", "8765"])
    assert dispatched == [("http", "127.0.0.1", 8765)]

    monkeypatch.setenv("MCP_SERVER_AUTH_TOKEN", "x" * 32)
    captured_configs: list[uvicorn.Config] = []

    class DummyServer:
        def __init__(self, cfg: uvicorn.Config) -> None:
            captured_configs.append(cfg)

        async def serve(self) -> None:
            return None

    with mock.patch.object(uvicorn, "Server", DummyServer):
        asyncio.run(srv.run_http("127.0.0.1", 8765))
        asyncio.run(srv.run_sse("127.0.0.1", 8766))

    assert len(captured_configs) == 2
    for cfg in captured_configs:
        assert cfg.timeout_graceful_shutdown == srv.MCP_GRACEFUL_SHUTDOWN_TIMEOUT
