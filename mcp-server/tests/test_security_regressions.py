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
