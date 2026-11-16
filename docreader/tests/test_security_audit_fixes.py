"""Security regression tests for docreader, odl-hybrid, and peripheral Python services.

Covers audit items:
- GENYEE-HIGH-13: docreader gRPC fail-closed auth & odl-hybrid auth/upload-cap/profile lockdown
- GENYEE-HIGH-14: LibreOffice soffice sandbox env scrubbing, profile lockdown, bwrap & 127.0.0.1:1 proxy fix
- GENYEE-HIGH-15: odl_hybrid_url request override block, engine override allowlist, Java heap/timeout bounds
- GENYEE-HIGH-16: Streaming ZIP member read budget against spoofed ZipInfo.file_size & entry count cap
- GENYEE-HIGH-17: XLSX merged-cell bounds/budget & DOCX gridSpan/gridBefore/clone caps
- GENYEE-MED-13 / MED-20: rerank_server_demo.py & serve.py safe defaults
- GENYEE-MED-14: FQDN trailing root dot & empty DNS label SSRF bypass block
- GENYEE-MED-15: ImageMagick disallowed vector formats & PIL.Image.MAX_IMAGE_PIXELS=40_000_000
- GENYEE-LOW-05: Proxy credential masking in config/logs
- GENYEE-LOW-06: defusedxml enforcement & parse_local.py image path traversal guard
"""

from __future__ import annotations

import ast
import asyncio
import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import struct
import sys
import tempfile
import unittest
import zipfile
from unittest import mock

from lxml import etree
import openpyxl
from PIL import Image

try:
    import pytest  # type: ignore[import-not-found]
except ImportError:
    pytest = None  # type: ignore[assignment]


class _FallbackRaisesContext:
    def __init__(self, expected_exc, match: str | None = None):
        self.expected_exc = expected_exc
        self.match = match
        self.value = None

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, _tb):
        if exc_type is None:
            raise AssertionError(f"DID NOT RAISE {self.expected_exc}")
        if not issubclass(exc_type, self.expected_exc):
            return False
        self.value = exc_val
        if self.match and not re.search(self.match, str(exc_val)):
            raise AssertionError(
                f"Regex pattern {self.match!r} does not match {str(exc_val)!r}"
            )
        return True


class _FallbackMark:
    @staticmethod
    def parametrize(argnames: str, argvalues):
        def _decorator(fn):
            fn._parametrize_spec = (argnames, list(argvalues))
            return fn

        return _decorator


class _FallbackPytest:
    mark = _FallbackMark()

    @staticmethod
    def raises(expected_exc, match: str | None = None):
        return _FallbackRaisesContext(expected_exc, match=match)

    @staticmethod
    def fail(msg: str = "test failed"):
        raise AssertionError(msg)

    @staticmethod
    def skip(reason: str = "skipped"):
        raise unittest.SkipTest(reason)


if pytest is None:
    pytest = _FallbackPytest()  # type: ignore[assignment]

from docreader.auth import AuthConfigError, AuthInterceptor
from docreader.config import dump_config, load_config, mask_proxy_url
from docreader.parser import pptx_media
from docreader.parser.archive import (
    ArchiveLimitError,
    safe_read_zip_member,
    validate_zip_archive,
)
from docreader.parser.doc_parser import SandboxExecutor
from docreader.parser.docx_merge import (
    MAX_DOCX_GRID_SPAN,
    _grid_before,
    _grid_span,
    fill_vertical_merged_cells_docx,
)
from docreader.parser.opendataloader_parser import (
    OpenDataLoaderParser,
    _run_convert,
)
from docreader.parser.parser import sanitize_engine_overrides
from docreader.parser.soffice_sandbox import (
    BLOCKING_LOCAL_PROXY,
    build_bwrap_command,
    build_minimal_sandbox_env,
    prepare_libreoffice_profile,
    sanitize_temp_suffix,
)
from docreader.parser.xlsx_media import XLSXImageExtractor
from docreader.parser.xlsx_merge import (
    MAX_MATERIALIZED_MERGE_CELLS,
    fill_merged_cells_xlsx,
)
from docreader.parser.xmind_parser import XMindParser
from docreader.scripts.parse_local import resolve_safe_image_dest
from docreader.utils.ssrf import (
    is_ssrf_safe_url,
    normalize_hostname,
    reset_ssrf_whitelist_cache_for_test,
)
from docreader.utils.ssrf_proxy import checked_address

REPO_ROOT = Path(__file__).resolve().parents[2]


def _load_module_from_path(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    assert spec and spec.loader
    mod = importlib.util.module_from_spec(spec)
    sys.modules[name] = mod
    spec.loader.exec_module(mod)
    return mod


def _build_spoofed_zip_bytes(entry_name: str, real_payload: bytes, spoofed_size: int = 16) -> bytes:
    """Construct an in-memory ZIP whose headers declare ``spoofed_size`` while
    storing compressed bytes that expand to ``real_payload``."""
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        zf.writestr(entry_name, real_payload)
    raw = bytearray(buf.getvalue())

    # Patch uncompressed size field in both Local File Header (offset +22) and
    # Central Directory Header (offset +24) to simulate a forged header.
    lfh = raw.index(b"PK\x03\x04")
    cdh = raw.index(b"PK\x01\x02")
    raw[lfh + 22 : lfh + 26] = struct.pack("<I", spoofed_size)
    raw[cdh + 24 : cdh + 28] = struct.pack("<I", spoofed_size)
    return bytes(raw)


# ---------------------------------------------------------------------------
# GENYEE-HIGH-13: docreader gRPC fail-closed auth & odl-hybrid ASGI wrapper
# ---------------------------------------------------------------------------


def test_docreader_auth_interceptor_fails_closed_by_default(monkeypatch):
    monkeypatch.delenv("GRPC_AUTH_TOKEN", raising=False)
    monkeypatch.delenv("DOCREADER_ALLOW_UNAUTHENTICATED", raising=False)

    with pytest.raises(AuthConfigError):
        AuthInterceptor()

    monkeypatch.setenv("GRPC_AUTH_TOKEN", "   ")
    with pytest.raises(AuthConfigError):
        AuthInterceptor()

    # Explicit opt-in for isolated local test environments
    monkeypatch.setenv("DOCREADER_ALLOW_UNAUTHENTICATED", "true")
    interceptor = AuthInterceptor()
    assert interceptor.allow_unauthenticated is True


def test_odl_hybrid_server_auth_size_limit_and_disabled_profile_endpoint(monkeypatch):
    hybrid_mod = _load_module_from_path(
        "odl_hybrid_server_sec_test",
        REPO_ROOT / "docker" / "odl-hybrid" / "opendataloader_pdf" / "hybrid_server.py",
    )

    monkeypatch.delenv("ODL_HYBRID_AUTH_TOKEN", raising=False)
    monkeypatch.delenv("ODL_HYBRID_ALLOW_UNAUTHENTICATED", raising=False)

    # Non-loopback bind without token must fail closed at startup
    with pytest.raises(RuntimeError, match="ODL_HYBRID_AUTH_TOKEN"):
        hybrid_mod.validate_startup_security("0.0.0.0")

    # Loopback default is 127.0.0.1
    assert hybrid_mod.DEFAULT_HOST == "127.0.0.1"
    hybrid_mod.validate_startup_security("127.0.0.1")

    async def dummy_inner(scope, receive, send):
        body = b""
        while True:
            msg = await receive()
            body += msg.get("body", b"")
            if not msg.get("more_body", False):
                break
        resp = json.dumps({"received": len(body)}).encode("utf-8")
        await send(
            {
                "type": "http.response.start",
                "status": 200,
                "headers": [(b"content-type", b"application/json")],
            }
        )
        await send({"type": "http.response.body", "body": resp})

    app = hybrid_mod.create_app(
        max_file_size=64,
        auth_token="hybrid-secret-token-0123456789",
        bind_host="0.0.0.0",
        _inner_app=dummy_inner,
    )

    async def invoke(path: str, method: str = "POST", headers=None, chunks=(b"ok",)):
        sent = []
        it = iter(
            [
                {
                    "type": "http.request",
                    "body": chunk,
                    "more_body": idx < len(chunks) - 1,
                }
                for idx, chunk in enumerate(chunks)
            ]
        )

        async def receive():
            return next(it, {"type": "http.request", "body": b"", "more_body": False})

        async def send(msg):
            sent.append(msg)

        await app(
            {
                "type": "http",
                "path": path,
                "method": method,
                "headers": headers or [],
            },
            receive,
            send,
        )
        return sent[0]["status"]

    # 1. Dangerous /v1/profile/file endpoint disabled (404) even with valid auth
    auth_hdr = [(b"authorization", b"Bearer hybrid-secret-token-0123456789")]
    assert asyncio.run(invoke("/v1/profile/file", headers=auth_hdr)) == 404

    # 2. Missing or invalid token -> 401; /health -> 200
    assert asyncio.run(invoke("/v1/convert/file", headers=[])) == 401
    assert (
        asyncio.run(
            invoke("/v1/convert/file", headers=[(b"authorization", b"Bearer wrong")])
        )
        == 401
    )
    assert asyncio.run(invoke("/health", method="GET", headers=[])) == 200

    # 3. Valid token within size cap -> 200
    assert asyncio.run(invoke("/v1/convert/file", headers=auth_hdr, chunks=(b"pdf",))) == 200

    # 4. Streamed chunks exceeding max_file_size (without Content-Length) -> 413
    assert (
        asyncio.run(
            invoke(
                "/v1/convert/file",
                headers=auth_hdr,
                chunks=(b"x" * 40, b"y" * 40),
            )
        )
        == 413
    )


# ---------------------------------------------------------------------------
# GENYEE-HIGH-14 & LOW-05: LibreOffice sandbox & proxy credential masking
# ---------------------------------------------------------------------------


def test_soffice_blocking_proxy_and_minimal_env_strip_secrets(tmp_path):
    assert SandboxExecutor().proxy == "http://127.0.0.1:1"
    assert BLOCKING_LOCAL_PROXY == "http://127.0.0.1:1"

    host_env = {
        "PATH": "/usr/bin:/bin",
        "LANG": "C.UTF-8",
        "GRPC_AUTH_TOKEN": "super-secret-grpc-token",
        "MINIO_SECRET_ACCESS_KEY": "minio-secret",
        "COS_SECRET_KEY": "cos-secret",
        "SYSTEM_AES_KEY": "0123456789abcdef",
        "DB_PASSWORD": "db-password",
        "HTTP_PROXY": "http://user:pass@corp-proxy.internal:8080",
        "HTTPS_PROXY": "http://user:pass@corp-proxy.internal:8080",
    }
    clean_env = build_minimal_sandbox_env(
        profile_dir=str(tmp_path), base_env=host_env
    )
    for forbidden in (
        "GRPC_AUTH_TOKEN",
        "MINIO_SECRET_ACCESS_KEY",
        "COS_SECRET_KEY",
        "SYSTEM_AES_KEY",
        "DB_PASSWORD",
    ):
        assert forbidden not in clean_env
    assert clean_env["http_proxy"] == "http://127.0.0.1:1"
    assert clean_env["HTTPS_PROXY"] == "http://127.0.0.1:1"

    uri = prepare_libreoffice_profile(str(tmp_path))
    assert uri.startswith("file://")
    xcu = (tmp_path / "user" / "registrymodifications.xcu").read_text(encoding="utf-8")
    assert "DisableMacrosExecution" in xcu
    assert "BlockUntrustedRefererLinks" in xcu

    bwrap_cmd = build_bwrap_command(
        ["soffice", "--headless"],
        bwrap_path="/usr/bin/bwrap",
        work_dir=str(tmp_path),
        profile_dir=str(tmp_path),
    )
    assert "--unshare-net" in bwrap_cmd
    assert "--new-session" not in bwrap_cmd
    assert sanitize_temp_suffix("../evil;rm -rf") == ".evilrmrf"


def test_proxy_url_credentials_masked_in_config_dump(monkeypatch):
    raw_proxy = "http://proxy_user:TopSecretPass123@proxy.example.com:8080"
    masked = mask_proxy_url(raw_proxy)
    assert "TopSecretPass123" not in masked
    assert "proxy_user" not in masked
    assert "proxy.example.com:8080" in masked

    monkeypatch.setenv("DOCREADER_EXTERNAL_HTTP_PROXY", raw_proxy)
    dumped = json.dumps(dump_config(mask_secrets=True))
    assert "TopSecretPass123" not in dumped


# ---------------------------------------------------------------------------
# GENYEE-HIGH-15: odl_hybrid_url override block & Java runner bounds
# ---------------------------------------------------------------------------


def test_odl_hybrid_url_request_override_blocked_and_java_heap_bounded():
    cleaned = sanitize_engine_overrides(
        {
            "odl_hybrid_url": "http://169.254.169.254/latest/meta-data/",
            "odl_hybrid": "docling-fast",
            "unexpected_key": "dropped",
        },
        reject_forbidden=False,
    )
    assert "odl_hybrid_url" not in cleaned
    assert "unexpected_key" not in cleaned
    assert cleaned == {"odl_hybrid": "docling-fast"}

    with pytest.raises(RuntimeError, match="odl_hybrid_url"):
        sanitize_engine_overrides(
            {"odl_hybrid_url": "http://127.0.0.1:9000"}, reject_forbidden=True
        )

    with pytest.raises(RuntimeError, match="SSRF"):
        OpenDataLoaderParser(odl_hybrid_url="http://127.0.0.1:9000")

    with pytest.raises(RuntimeError, match="SSRF"):
        OpenDataLoaderParser(overrides={"odl_hybrid_url": "http://127.0.0.1:9000"})

    runner_mod = _load_module_from_path(
        "odl_runner_sec_test",
        REPO_ROOT / "docker" / "odl-hybrid" / "opendataloader_pdf" / "runner.py",
    )
    java_cmd = runner_mod.build_java_command(
        "/opt/odl/cli.jar", ["--help"], max_heap_mb=512
    )
    assert "-Xmx512m" in java_cmd
    assert "-Dhttp.followRedirects=false" in java_cmd


def test_odl_run_convert_local_auth_forwarder_end_to_end(monkeypatch, tmp_path):
    import hmac
    import http.client
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    import threading
    from urllib.parse import urlsplit
    import urllib.error
    import urllib.request

    secret_token = "odl-e2e-shared-bearer-secret-9876543210"
    upstream_hits: list[tuple[str, str, bytes]] = []

    class _MockAuthenticatedHybridHandler(BaseHTTPRequestHandler):
        def log_message(self, format, *args):  # noqa: A002
            pass

        def do_POST(self):
            self.close_connection = True
            if self.path == "/v1/redirect-trap":
                self.send_response(302)
                self.send_header("Location", "http://169.254.169.254/latest/meta-data/")
                self.send_header("Content-Length", "0")
                self.end_headers()
                return

            auth = self.headers.get("Authorization", "")
            expected = f"Bearer {secret_token}"
            clen = int(self.headers.get("Content-Length", "0"))
            raw_body = self.rfile.read(clen) if clen > 0 else b""
            upstream_hits.append((self.path, auth, raw_body))

            if not hmac.compare_digest(auth.encode("utf-8"), expected.encode("utf-8")):
                err = b'{"detail":"Invalid or missing authentication token"}'
                self.send_response(401)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(err)))
                self.end_headers()
                self.wfile.write(err)
                return

            ok_body = json.dumps(
                {"status": "success", "echo_len": len(raw_body)}
            ).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(ok_body)))
            self.end_headers()
            self.wfile.write(ok_body)

    upstream_srv = ThreadingHTTPServer(("127.0.0.1", 0), _MockAuthenticatedHybridHandler)
    upstream_thread = threading.Thread(target=upstream_srv.serve_forever, daemon=True)
    upstream_thread.start()
    upstream_url = f"http://127.0.0.1:{upstream_srv.server_port}"

    try:
        # Direct unauthenticated call to upstream must return 401
        direct_req = urllib.request.Request(
            f"{upstream_url}/v1/convert/file",
            data=b"pdf-bytes",
            headers={"Connection": "close"},
            method="POST",
        )
        with pytest.raises(urllib.error.HTTPError) as direct_err:
            urllib.request.urlopen(direct_req, timeout=5)
        assert direct_err.value.code == 401
        direct_err.value.close()

        monkeypatch.setenv("ODL_HYBRID_AUTH_TOKEN", secret_token)
        monkeypatch.setenv("DOCREADER_ODL_HYBRID_URL", upstream_url)
        monkeypatch.setenv("DOCREADER_ODL_HYBRID", "docling-fast")

        reloaded_cfg = load_config()
        assert reloaded_cfg.odl_hybrid_auth_token == secret_token
        monkeypatch.setattr("docreader.config.CONFIG", reloaded_cfg)
        monkeypatch.setattr(
            "docreader.parser.opendataloader_parser.CONFIG", reloaded_cfg
        )
        assert secret_token not in json.dumps(dump_config(mask_secrets=True))

        # Allow the test upstream loopback endpoint through SSRF guard while
        # rejecting everything else.
        monkeypatch.setattr(
            "docreader.parser.opendataloader_parser.is_ssrf_safe_url",
            lambda u: (
                (True, "")
                if u.startswith(upstream_url)
                else (False, f"blocked test target: {u}")
            ),
        )

        captured_local_url: list[str] = []

        def _fake_odl_convert(**kwargs):
            local_url = kwargs["hybrid_url"]
            captured_local_url.append(local_url)
            assert local_url.startswith("http://127.0.0.1:")
            assert local_url != upstream_url
            assert secret_token not in json.dumps(kwargs)

            # Scrubbed Java child env must not leak ODL_HYBRID_AUTH_TOKEN
            java_env = build_minimal_sandbox_env(proxy="")
            assert "ODL_HYBRID_AUTH_TOKEN" not in java_env
            assert secret_token not in json.dumps(java_env)

            # 1. Standard Content-Length POST without Authorization header (simulating CLI JAR)
            req = urllib.request.Request(
                f"{local_url}/v1/convert/file",
                data=b"%PDF-1.4-multipart-payload",
                headers={"Content-Type": "multipart/form-data; boundary=xyz"},
                method="POST",
            )
            with urllib.request.urlopen(req, timeout=5) as resp:
                assert resp.status == 200
                payload = json.loads(resp.read().decode("utf-8"))
                assert payload == {"status": "success", "echo_len": 26}

            # 2. Transfer-Encoding: chunked POST (Java 11 HttpClient streaming mode)
            parts = urlsplit(local_url)
            conn = http.client.HTTPConnection(parts.hostname, parts.port, timeout=5)
            try:
                conn.putrequest("POST", "/v1/convert/file")
                conn.putheader("Transfer-Encoding", "chunked")
                conn.putheader("Authorization", "Bearer spoofed-client-token")
                conn.endheaders()
                for chunk in (b"chunk-one-", b"chunk-two"):
                    conn.send(f"{len(chunk):x}\r\n".encode("ascii") + chunk + b"\r\n")
                conn.send(b"0\r\n\r\n")
                c_resp = conn.getresponse()
                c_body = json.loads(c_resp.read().decode("utf-8"))
                assert c_resp.status == 200
                assert c_body == {"status": "success", "echo_len": 19}
            finally:
                conn.close()

            # 3. Upstream 302 redirect is refused (502) and never followed
            redir_req = urllib.request.Request(
                f"{local_url}/v1/redirect-trap", data=b"x", method="POST"
            )
            with pytest.raises(urllib.error.HTTPError) as redir_err:
                urllib.request.urlopen(redir_req, timeout=5)
            assert redir_err.value.code == 502
            redir_err.value.close()

            # 4. Path traversal segment rejected locally with 400
            trav_req = urllib.request.Request(
                f"{local_url}/../admin", data=b"x", method="POST"
            )
            with pytest.raises(urllib.error.HTTPError) as trav_err:
                urllib.request.urlopen(trav_req, timeout=5)
            assert trav_err.value.code == 400
            trav_err.value.close()

        fake_pkg = mock.MagicMock()
        fake_pkg.convert.side_effect = _fake_odl_convert
        pdf_file = tmp_path / "sample.pdf"
        pdf_file.write_bytes(b"%PDF-1.4\n%%EOF")

        with mock.patch.dict(sys.modules, {"opendataloader_pdf": fake_pkg}):
            _run_convert(
                str(pdf_file),
                str(tmp_path),
                str(tmp_path / "images"),
                overrides={"odl_hybrid": "docling-fast"},
            )

        assert len(captured_local_url) == 1
        # Verify upstream saw our Bearer token on both forwarded requests
        assert upstream_hits[-2] == (
            "/v1/convert/file",
            f"Bearer {secret_token}",
            b"%PDF-1.4-multipart-payload",
        )
        assert upstream_hits[-1] == (
            "/v1/convert/file",
            f"Bearer {secret_token}",
            b"chunk-one-chunk-two",
        )

        # Verify ephemeral local proxy shut down immediately after _run_convert exited
        with pytest.raises(urllib.error.URLError):
            urllib.request.urlopen(f"{captured_local_url[0]}/v1/convert/file", timeout=1)
    finally:
        upstream_srv.shutdown()
        upstream_srv.server_close()
        upstream_thread.join(timeout=2.0)


# ---------------------------------------------------------------------------
# GENYEE-HIGH-16: Spoofed ZIP header streaming enforcement & entry count cap
# ---------------------------------------------------------------------------


def test_safe_read_zip_member_aborts_on_spoofed_file_size_and_entry_cap():
    # Real uncompressed payload is 32 KiB, but ZIP headers claim only 16 bytes.
    spoofed_bytes = _build_spoofed_zip_bytes("payload.xml", b"A" * 32768, spoofed_size=16)

    with zipfile.ZipFile(io.BytesIO(spoofed_bytes)) as zf:
        # Header-only check sees file_size == 16, so streaming read MUST catch it.
        validate_zip_archive(zf, max_bytes=4096)
        with pytest.raises(ArchiveLimitError):
            safe_read_zip_member(
                zf, "payload.xml", max_member_bytes=4096, max_total_bytes=4096
            )

    # Entry count cap
    many_buf = io.BytesIO()
    with zipfile.ZipFile(many_buf, "w") as zf:
        for idx in range(15):
            zf.writestr(f"f{idx}.txt", b"x")
    with zipfile.ZipFile(io.BytesIO(many_buf.getvalue())) as zf:
        with pytest.raises(ArchiveLimitError, match="entry count"):
            validate_zip_archive(zf, max_entries=10)


def test_xlsx_media_extractor_propagates_archive_limit_on_spoofed_member():
    spoofed_xlsx = _build_spoofed_zip_bytes(
        "xl/workbook.xml", b"<workbook>" + (b" " * 16384) + b"</workbook>", spoofed_size=32
    )
    extractor = XLSXImageExtractor(spoofed_xlsx, max_uncompressed_bytes=2048)
    with pytest.raises(ArchiveLimitError):
        extractor.extract_cell_images()


# ---------------------------------------------------------------------------
# GENYEE-HIGH-17: XLSX merged-cell bounds/budget & DOCX gridSpan/clone caps
# ---------------------------------------------------------------------------


def _inject_xlsx_merge_ref(xlsx_bytes: bytes, merge_ref: str) -> bytes:
    """Inject a raw ``<mergeCells><mergeCell ref=.../></mergeCells>`` into sheet1.xml."""
    out = io.BytesIO()
    with zipfile.ZipFile(io.BytesIO(xlsx_bytes), "r") as src, zipfile.ZipFile(
        out, "w", compression=zipfile.ZIP_DEFLATED
    ) as dst:
        for info in src.infolist():
            data = src.read(info.filename)
            if info.filename == "xl/worksheets/sheet1.xml":
                text = data.decode("utf-8")
                merge_block = (
                    f'<mergeCells count="1"><mergeCell ref="{merge_ref}"/></mergeCells>'
                )
                text = text.replace("</sheetData>", f"</sheetData>{merge_block}", 1)
                data = text.encode("utf-8")
            dst.writestr(info, data)
    return out.getvalue()


def test_xlsx_merge_clips_unpopulated_tail_and_enforces_cell_budget():
    assert MAX_MATERIALIZED_MERGE_CELLS == 200_000

    # 1. Sparse huge merge A1:XFD1048576 on a sheet whose populated data ends at B2
    wb = openpyxl.Workbook()
    ws = wb.active
    ws["A1"] = "Header"
    ws["B2"] = "Data"
    buf = io.BytesIO()
    wb.save(buf)
    sparse_bomb = _inject_xlsx_merge_ref(buf.getvalue(), "A1:XFD1048576")

    # Should clip to populated bounds (2x2) at the XML layer and finish in milliseconds
    repaired = fill_merged_cells_xlsx(sparse_bomb)
    wb_out = openpyxl.load_workbook(io.BytesIO(repaired))
    assert wb_out.active["B1"].value == "Header"

    # 2. Populated boundary far enough to exceed budget -> raises ArchiveLimitError
    wb2 = openpyxl.Workbook()
    ws2 = wb2.active
    ws2["A1"] = "TopLeft"
    ws2["ZZ1000"] = "Anchor"  # 702 * 1000 = 702,000 > MAX_MATERIALIZED_MERGE_CELLS (200,000)
    buf2 = io.BytesIO()
    wb2.save(buf2)
    dense_bomb = _inject_xlsx_merge_ref(buf2.getvalue(), "A1:ZZ1000")

    with pytest.raises(ArchiveLimitError, match="merged cell"):
        fill_merged_cells_xlsx(dense_bomb)


def test_docx_merge_clamps_huge_grid_span(monkeypatch):
    w_ns = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    tc_pr = etree.fromstring(
        f'<w:tcPr xmlns:w="{w_ns}"><w:gridSpan w:val="999999999"/></w:tcPr>'
    )
    row_el = etree.fromstring(
        f'<w:tr xmlns:w="{w_ns}"><w:trPr><w:gridBefore w:val="999999999"/></w:trPr></w:tr>'
    )
    assert _grid_span(tc_pr) == MAX_DOCX_GRID_SPAN
    assert _grid_before(row_el) == MAX_DOCX_GRID_SPAN

    doc_xml = f"""<?xml version="1.0" encoding="UTF-8"?>
    <w:document xmlns:w="{w_ns}">
      <w:body>
        <w:tbl>
          <w:tr>
            <w:trPr><w:gridBefore w:val="999999999"/></w:trPr>
            <w:tc>
              <w:tcPr>
                <w:gridSpan w:val="999999999"/>
                <w:vMerge w:val="restart"/>
              </w:tcPr>
              <w:p><w:r><w:t>MergedCell</w:t></w:r></w:p>
            </w:tc>
          </w:tr>
          <w:tr>
            <w:trPr><w:gridBefore w:val="999999999"/></w:trPr>
            <w:tc>
              <w:tcPr>
                <w:gridSpan w:val="999999999"/>
                <w:vMerge/>
              </w:tcPr>
              <w:p/>
            </w:tc>
          </w:tr>
        </w:tbl>
      </w:body>
    </w:document>""".encode("utf-8")

    docx_buf = io.BytesIO()
    with zipfile.ZipFile(docx_buf, "w") as zf:
        zf.writestr("word/document.xml", doc_xml)

    expanded = fill_vertical_merged_cells_docx(docx_buf.getvalue())
    with zipfile.ZipFile(io.BytesIO(expanded)) as zf:
        out_xml = zf.read("word/document.xml").decode("utf-8")
    assert out_xml.count("MergedCell") == 2

    monkeypatch.setattr("docreader.parser.docx_merge.MAX_TABLE_CLONED_ELEMENTS", 2)
    with pytest.raises(ArchiveLimitError, match="cloned element count"):
        fill_vertical_merged_cells_docx(docx_buf.getvalue())


# ---------------------------------------------------------------------------
# GENYEE-MED-14: SSRF FQDN trailing dot & empty DNS label normalization
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "url",
    [
        "http://localhost./admin",
        "http://metadata.google.internal./computeMetadata/v1/",
        "http://service.default.svc.cluster.local./api",
        "http://127.0.0.1./",
        "http://example..com/",
        "http://.example.com/",
        "http://./",
    ],
)
def test_ssrf_blocks_fqdn_trailing_dot_and_empty_labels_before_dns(monkeypatch, url):
    reset_ssrf_whitelist_cache_for_test()
    monkeypatch.setattr(
        "docreader.utils.ssrf.socket.getaddrinfo",
        lambda *a, **kw: pytest.fail(f"DNS lookup must not be reached for {url}"),
    )
    safe, reason = is_ssrf_safe_url(url)
    assert safe is False
    assert reason

    with pytest.raises(ValueError):
        asyncio.run(checked_address(url))


def test_normalize_hostname_strips_single_trailing_root_dot():
    host, err = normalize_hostname("Example.COM.")
    assert err == ""
    assert host == "example.com"


# ---------------------------------------------------------------------------
# GENYEE-MED-15 & LOW-06: Media limits, defusedxml, parse_local & rerank demo
# ---------------------------------------------------------------------------


def test_pptx_media_disallows_vector_delegates_and_caps_pillow_pixels():
    for unsafe_fmt in ("wmf", "emf", "svg", "eps"):
        assert unsafe_fmt not in pptx_media.ALLOWED_CONVERT_FORMATS
    assert Image.MAX_IMAGE_PIXELS == 40_000_000

    with mock.patch.object(
        pptx_media,
        "_find_convert",
        side_effect=AssertionError("ImageMagick must not be called for SVG"),
    ):
        assert pptx_media.rasterize_media_bytes("slide1.svg", b"<svg/>") is None


def test_xmind_and_parse_local_enforce_defusedxml_and_safe_image_paths(tmp_path):
    dtd_xml = (
        '<?xml version="1.0"?>'
        '<!DOCTYPE xmap [<!ENTITY xxe "blocked">]>'
        '<xmap-content><sheet><title>&xxe;</title>'
        '<topic><title>Root</title></topic></sheet></xmap-content>'
    )
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as zf:
        zf.writestr("content.xml", dtd_xml.encode("utf-8"))

    with pytest.raises(ValueError, match="invalid XMind content.xml"):
        XMindParser().parse_into_text(buf.getvalue())

    # parse_local.py image path traversal protection
    safe_dest = resolve_safe_image_dest(str(tmp_path), "images/pic01.png")
    assert Path(safe_dest).parent == (tmp_path / "images").resolve()

    for unsafe_ref in ("../../escaped.png", "/tmp/escaped.png", "images/../../a.png"):
        with pytest.raises(ValueError):
            resolve_safe_image_dest(str(tmp_path), unsafe_ref)


def test_rerank_server_demo_static_security_invariants():
    demo_path = REPO_ROOT / "rerank_server_demo.py"
    if not demo_path.exists():
        pytest.skip("rerank_server_demo.py is omitted in exported source archives")
    source = demo_path.read_text(encoding="utf-8")
    tree = ast.parse(source)
    assert tree is not None
    assert "RERANK_MODEL_PATH" in source
    assert "RERANK_API_KEY" in source
    assert "compare_digest" in source
    assert " use_safetensors=True" in source or "use_safetensors=True" in source
    assert '"127.0.0.1"' in source
    assert "MAX_RERANK_DOCUMENTS" in source


@contextlib.contextmanager
def _temp_monkeypatch():
    env_snapshot = os.environ.copy()
    stack = contextlib.ExitStack()

    class _PatchHelper:
        def setenv(self, key: str, value: str) -> None:
            os.environ[key] = str(value)

        def delenv(self, key: str, raising: bool = True) -> None:
            if key in os.environ:
                del os.environ[key]
            elif raising:
                raise KeyError(key)

        def setattr(self, target, name=mock.DEFAULT, value=mock.DEFAULT) -> None:
            if value is mock.DEFAULT:
                stack.enter_context(mock.patch(target, new=name))
            else:
                stack.enter_context(mock.patch.object(target, name, new=value))

    try:
        yield _PatchHelper()
    finally:
        stack.close()
        os.environ.clear()
        os.environ.update(env_snapshot)


class _SecurityAuditUnittestCases(unittest.TestCase):
    """Bridge standalone security regression functions into unittest discovery."""

    def test_docreader_auth_interceptor_fails_closed_by_default(self):
        with _temp_monkeypatch() as mp:
            test_docreader_auth_interceptor_fails_closed_by_default(mp)

    def test_odl_hybrid_server_auth_size_limit_and_disabled_profile_endpoint(self):
        with _temp_monkeypatch() as mp:
            test_odl_hybrid_server_auth_size_limit_and_disabled_profile_endpoint(mp)

    def test_soffice_blocking_proxy_and_minimal_env_strip_secrets(self):
        with tempfile.TemporaryDirectory() as td:
            test_soffice_blocking_proxy_and_minimal_env_strip_secrets(Path(td))

    def test_proxy_url_credentials_masked_in_config_dump(self):
        with _temp_monkeypatch() as mp:
            test_proxy_url_credentials_masked_in_config_dump(mp)

    def test_odl_hybrid_url_request_override_blocked_and_java_heap_bounded(self):
        test_odl_hybrid_url_request_override_blocked_and_java_heap_bounded()

    def test_odl_run_convert_local_auth_forwarder_end_to_end(self):
        with _temp_monkeypatch() as mp, tempfile.TemporaryDirectory() as td:
            test_odl_run_convert_local_auth_forwarder_end_to_end(mp, Path(td))

    def test_safe_read_zip_member_aborts_on_spoofed_file_size_and_entry_cap(self):
        test_safe_read_zip_member_aborts_on_spoofed_file_size_and_entry_cap()

    def test_xlsx_media_extractor_propagates_archive_limit_on_spoofed_member(self):
        test_xlsx_media_extractor_propagates_archive_limit_on_spoofed_member()

    def test_xlsx_merge_clips_unpopulated_tail_and_enforces_cell_budget(self):
        test_xlsx_merge_clips_unpopulated_tail_and_enforces_cell_budget()

    def test_docx_merge_clamps_huge_grid_span(self):
        with _temp_monkeypatch() as mp:
            test_docx_merge_clamps_huge_grid_span(mp)

    def test_ssrf_blocks_fqdn_trailing_dot_and_empty_labels_before_dns(self):
        for bad_url in (
            "http://localhost./admin",
            "http://metadata.google.internal./computeMetadata/v1/",
            "http://service.default.svc.cluster.local./api",
            "http://127.0.0.1./",
            "http://example..com/",
            "http://.example.com/",
            "http://./",
        ):
            with self.subTest(url=bad_url), _temp_monkeypatch() as mp:
                test_ssrf_blocks_fqdn_trailing_dot_and_empty_labels_before_dns(mp, bad_url)

    def test_normalize_hostname_strips_single_trailing_root_dot(self):
        test_normalize_hostname_strips_single_trailing_root_dot()

    def test_pptx_media_disallows_vector_delegates_and_caps_pillow_pixels(self):
        test_pptx_media_disallows_vector_delegates_and_caps_pillow_pixels()

    def test_xmind_and_parse_local_enforce_defusedxml_and_safe_image_paths(self):
        with tempfile.TemporaryDirectory() as td:
            test_xmind_and_parse_local_enforce_defusedxml_and_safe_image_paths(Path(td))

    def test_rerank_server_demo_static_security_invariants(self):
        test_rerank_server_demo_static_security_invariants()


def load_tests(loader: unittest.TestLoader, _tests, _pattern):
    return loader.loadTestsFromTestCase(_SecurityAuditUnittestCases)
