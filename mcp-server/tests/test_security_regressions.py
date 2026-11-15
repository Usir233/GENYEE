"""Security regression tests for Python MCP Server (GENYEE-HIGH-18, HIGH-19, MED-12)."""

from __future__ import annotations

import asyncio
import importlib.util
import os
from pathlib import Path
import tempfile
from unittest import mock

import pytest
from starlette.testclient import TestClient

from upload_paths import (
    clear_active_transport,
    open_upload_file,
    open_upload_file_nofollow,
    resolve_upload_file_path,
    set_active_transport,
)
import genyee_mcp_server as srv


@pytest.fixture(autouse=True)
def _clean_mcp_env(monkeypatch):
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
def test_safe_path_segment_rejects_traversal_and_special_chars(bad_segment):
    with pytest.raises(ValueError):
        srv._safe_path_segment(bad_segment, "resource_id")


def test_safe_path_segment_allows_safe_identifiers():
    assert srv._safe_path_segment("kb-00000001", "kb_id") == "kb-00000001"
    assert (
        srv._safe_path_segment("550e8400-e29b-41d4-a716-446655440000", "kb_id")
        == "550e8400-e29b-41d4-a716-446655440000"
    )
    assert srv._safe_path_segment("wiki_page.v1", "slug") == "wiki_page.v1"


def test_wiki_read_page_allows_hierarchical_slugs_and_blocks_cspt(monkeypatch):
    client = srv.GENYEEClient("http://example.invalid/api/v1", "placeholder-key")
    called_endpoints: list[str] = []

    def fake_request(method, endpoint, **kwargs):
        called_endpoints.append(f"{method} {endpoint}")
        return {"data": {"slug": endpoint}}

    monkeypatch.setattr(client, "_request", fake_request)
    client.wiki_read_page("kb-001", "entity/acme-corp")
    client.wiki_read_page("kb-001", "concept/rag architecture")
    assert called_endpoints == [
        "GET /knowledgebase/kb-001/wiki/pages/entity/acme-corp",
        "GET /knowledgebase/kb-001/wiki/pages/concept/rag%20architecture",
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
    assert len(called_endpoints) == 2


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
    monkeypatch, call_fn
):
    client = srv.GENYEEClient("http://example.invalid/api/v1", "placeholder-key")
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
        srv.requests,
        "post",
        lambda *a, **kw: pytest.fail("Multipart POST must not be sent on CSPT input"),
    )

    with pytest.raises(ValueError):
        call_fn(client, "../tenants/1/api-keys")


# ---------------------------------------------------------------------------
# GENYEE-HIGH-19: Remote upload fail-closed, existence oracle & O_NOFOLLOW
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("transport", ["http", "sse", "streamable-http"])
def test_remote_transports_fail_closed_without_explicit_upload_dirs(
    monkeypatch, tmp_path, transport
):
    doc = tmp_path / "doc.txt"
    doc.write_text("allowed only when configured", encoding="utf-8")
    monkeypatch.chdir(tmp_path)
    set_active_transport(transport)

    with pytest.raises(ValueError, match="disabled"):
        resolve_upload_file_path(str(doc))


def test_resolve_upload_file_path_eliminates_outside_existence_oracle(
    monkeypatch, tmp_path
):
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

    # Identical error message: caller cannot distinguish existing vs missing files
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
    monkeypatch, tmp_path, rel_sensitive
):
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(tmp_path))
    target = tmp_path / rel_sensitive
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("placeholder", encoding="utf-8")

    with pytest.raises(ValueError, match="restricted hidden"):
        resolve_upload_file_path(str(target))

    # Non-existent sensitive path also yields the exact same error (no oracle)
    missing_sensitive = tmp_path / "missing_dir" / rel_sensitive
    with pytest.raises(ValueError, match="restricted hidden"):
        resolve_upload_file_path(str(missing_sensitive))


def test_open_upload_file_nofollow_blocks_symlink_swap(monkeypatch, tmp_path):
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(tmp_path))
    real_file = tmp_path / "doc.txt"
    real_file.write_text("safe content", encoding="utf-8")
    other_file = tmp_path / "other.txt"
    other_file.write_text("other content", encoding="utf-8")

    resolved, handle = open_upload_file(str(real_file))
    with handle:
        assert handle.read() == b"safe content"

    # Simulate TOCTOU replacement of validated path with a symlink prior to open
    real_file.unlink()
    real_file.symlink_to(other_file)
    with pytest.raises(ValueError):
        open_upload_file_nofollow(resolved)

    # Simulate TOCTOU replacement of an intermediate directory with a symlink
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
    monkeypatch, tmp_path
):
    allowed_dir = tmp_path / "allowed"
    allowed_dir.mkdir()
    outside_dir = tmp_path / "outside"
    outside_dir.mkdir()
    outside_secret = outside_dir / "secret.txt"
    outside_secret.write_text("do-not-open", encoding="utf-8")

    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(allowed_dir))

    # Direct call outside allowed roots must fail closed before touching os.open
    with mock.patch("upload_paths.os.open", side_effect=AssertionError("os.open called")):
        with pytest.raises(ValueError, match="outside allowed upload directories"):
            open_upload_file_nofollow(str(outside_secret))

    # Network transport with unconfigured MCP_ALLOWED_UPLOAD_DIRS must also fail closed
    monkeypatch.delenv("MCP_ALLOWED_UPLOAD_DIRS", raising=False)
    set_active_transport("http")
    safe_file = allowed_dir / "doc.txt"
    safe_file.write_text("content", encoding="utf-8")
    with mock.patch("upload_paths.os.open", side_effect=AssertionError("os.open called")):
        with pytest.raises(ValueError, match="disabled"):
            open_upload_file_nofollow(str(safe_file))

    # Root directory swapped to symlink pointing outside between validate and open
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

    # Root directory replaced by a distinct directory (different inode) between validate and open
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
    monkeypatch, tmp_path
):
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

    def tracking_open(path, flags, *args, **kwargs):
        fd = real_os_open(path, flags, *args, **kwargs)
        opened_fds.append(fd)
        return fd

    def tracking_close(fd):
        closed_fds.append(fd)
        return real_os_close(fd)

    def mismatched_fstat(fd):
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
                st.st_atime,
                st.st_mtime,
                st.st_ctime,
            )
        )

    monkeypatch.setattr("upload_paths.os.open", tracking_open)
    monkeypatch.setattr("upload_paths.os.close", tracking_close)
    monkeypatch.setattr("upload_paths.os.fstat", mismatched_fstat)

    with pytest.raises(ValueError, match="upload root directory changed during open"):
        open_upload_file_nofollow(resolved)

    assert len(opened_fds) == 1
    assert closed_fds == opened_fds


def test_run_server_bootstraps_sys_path_outside_mcp_dir(monkeypatch, tmp_path):
    run_server_path = Path(__file__).resolve().parents[1] / "run_server.py"
    mcp_dir = str(run_server_path.parent)
    monkeypatch.chdir(tmp_path)
    cleaned_path = [p for p in os.sys.path if os.path.abspath(p or ".") != mcp_dir]
    monkeypatch.setattr(os.sys, "path", cleaned_path)

    spec = importlib.util.spec_from_file_location("run_server_isolated", run_server_path)
    assert spec and spec.loader
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)

    async def _noop_run():
        return None

    assert mcp_dir in os.sys.path
    with mock.patch("genyee_mcp_server.run", side_effect=_noop_run):
        mod.main()


# ---------------------------------------------------------------------------
# GENYEE-MED-12: Token length, TransportSecuritySettings & admin tools gate
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("transport", ["http", "sse"])
def test_require_network_transport_auth_enforces_min_32_char_token(
    monkeypatch, transport
):
    monkeypatch.setenv("MCP_SERVER_AUTH_TOKEN", "short-token-12345")
    with pytest.raises(SystemExit) as exc:
        srv.require_network_transport_auth(transport)
    assert exc.value.code == 1

    strong = "a" * 32
    monkeypatch.setenv("MCP_SERVER_AUTH_TOKEN", strong)
    assert srv.require_network_transport_auth(transport) == strong


def test_wildcard_bind_still_enforces_dns_rebinding_host_and_origin_allowlist(
    monkeypatch,
):
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
        # Untrusted Host header (DNS rebinding) -> 421 Misdirected Request
        bad_host_resp = client.post(
            "/mcp",
            json=init_payload,
            headers={**base_headers, "Host": "attacker-rebind.invalid:8000"},
        )
        assert bad_host_resp.status_code == 421

        # Untrusted Origin header -> 403 Forbidden
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

        # Allowed configured Host + Origin -> 200 OK
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


def test_admin_tools_disabled_by_default_and_opt_in_via_env(monkeypatch):
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
    monkeypatch,
):
    demo_path = (
        Path(__file__).resolve().parents[2] / "examples" / "mcp-demo" / "server.py"
    )
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


def test_genyee_client_blocks_cross_origin_redirect_api_key_leak(tmp_path, monkeypatch):
    from http.server import BaseHTTPRequestHandler, HTTPServer
    import threading

    captured_headers: list[dict[str, str]] = []

    class AttackerHandler(BaseHTTPRequestHandler):
        def do_GET(self):
            captured_headers.append({k.lower(): v for k, v in self.headers.items()})
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"data": []}')

        def log_message(self, *_args):
            pass

    attacker_srv = HTTPServer(("127.0.0.1", 0), AttackerHandler)
    attacker_port = attacker_srv.server_address[1]
    attacker_thread = threading.Thread(target=attacker_srv.serve_forever, daemon=True)
    attacker_thread.start()

    class OriginHandler(BaseHTTPRequestHandler):
        def do_GET(self):
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

        def log_message(self, *_args):
            pass

    origin_srv = HTTPServer(("127.0.0.1", 0), OriginHandler)
    origin_port = origin_srv.server_address[1]
    origin_thread = threading.Thread(target=origin_srv.serve_forever, daemon=True)
    origin_thread.start()

    try:
        client = srv.GENYEEClient(
            f"http://127.0.0.1:{origin_port}/api/v1", "secret-x-api-key"
        )
        # Same-origin redirect remains allowed
        assert client._request("GET", "/same-origin-hop") == {"ok": True}

        # Cross-origin redirect must fail before forwarding X-API-Key
        with pytest.raises(srv.RequestException):
            client.list_tenants()
        assert captured_headers == [], "X-API-Key must never reach redirected cross-origin host"
    finally:
        origin_srv.shutdown()
        attacker_srv.shutdown()
        origin_srv.server_close()
        attacker_srv.server_close()

    # Multipart upload must also disable cross-origin redirect following
    monkeypatch.setenv("MCP_ALLOWED_UPLOAD_DIRS", str(tmp_path))
    upload_doc = tmp_path / "note.txt"
    upload_doc.write_text("hello", encoding="utf-8")
    with mock.patch.object(srv.requests, "post") as post_mock:
        post_mock.return_value = mock.Mock(
            raise_for_status=lambda: None, json=lambda: {"data": {"id": "k-1"}}
        )
        upload_client = srv.GENYEEClient("http://example.invalid/api/v1", "secret-key")
        upload_client.create_knowledge_from_file("kb-1", str(upload_doc))
        assert post_mock.call_args.kwargs.get("allow_redirects") is False


def test_iter_sse_lines_and_request_bound_oversized_responses(monkeypatch):
    from http.server import BaseHTTPRequestHandler, HTTPServer
    import threading

    monkeypatch.setattr(srv, "MAX_SSE_EVENT_BYTES", 2048)

    class FakeStreamingResponse:
        def iter_content(self, chunk_size=512):
            for _ in range(10):
                yield b"A" * 512

    with pytest.raises(srv.RequestException, match="SSE line exceeds"):
        list(srv._iter_sse_lines(FakeStreamingResponse()))

    monkeypatch.setattr(srv, "MAX_API_RESPONSE_BYTES", 2048)

    class OversizedJSONHandler(BaseHTTPRequestHandler):
        def do_GET(self):
            body = ('{"pad":"' + ("B" * 8192) + '"}').encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_args):
            pass

    json_srv = HTTPServer(("127.0.0.1", 0), OversizedJSONHandler)
    port = json_srv.server_address[1]
    thread = threading.Thread(target=json_srv.serve_forever, daemon=True)
    thread.start()
    try:
        client = srv.GENYEEClient(f"http://127.0.0.1:{port}/api/v1", "key")
        with pytest.raises(srv.RequestException, match="exceeds"):
            client.list_tenants()
    finally:
        json_srv.shutdown()
        json_srv.server_close()


def test_mcp_auth_middleware_gates_websocket_and_preserves_lifespan():
    async def run():
        called: list[str] = []
        sent: list[dict] = []

        async def app(scope, receive, send):
            called.append(scope["type"])

        async def send(msg):
            sent.append(msg)

        async def receive():
            return {"type": "websocket.connect"}

        mw = srv.MCPAuthMiddleware(app, "secret-token")

        # Lifespan passes through without auth headers
        await mw({"type": "lifespan"}, receive, send)
        assert called == ["lifespan"]

        # Unauthenticated websocket must be rejected and never reach app
        sent.clear()
        await mw({"type": "websocket", "headers": []}, receive, send)
        assert called == ["lifespan"]
        assert any(m.get("type") == "websocket.close" and m.get("code") == 1008 for m in sent)

        # Authenticated websocket passes through
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


def test_non_positive_timeouts_and_invalid_mcp_port_env_handling():
    import subprocess
    import sys

    mcp_dir = Path(__file__).resolve().parents[1]

    for var_name in ("GENYEE_API_TIMEOUT", "GENYEE_CHAT_TIMEOUT"):
        for bad_val in ("0", "-5"):
            env = dict(os.environ)
            env["PYTHONDONTWRITEBYTECODE"] = "1"
            env[var_name] = bad_val
            res = subprocess.run(
                [sys.executable, "-c", f"import genyee_mcp_server as s; print(s.{var_name})"],
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
