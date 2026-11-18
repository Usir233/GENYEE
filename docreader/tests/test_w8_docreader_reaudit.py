"""Wave 8 zero-trust deep re-audit regression tests for docreader runtime,
models, parsers, splitter, and utils.
"""

from __future__ import annotations

import asyncio
import io
import ipaddress
import json
import logging
import os
import struct
import tempfile
import tracemalloc
import urllib.error
import urllib.request
import zipfile
from unittest import mock

import grpc
import pytest

from docreader import config as doc_config
from docreader.auth import AuthInterceptor
from docreader.main import DocReaderServicer
from docreader.models.document import Chunk, Document
from docreader.parser.archive import (
    ArchiveLimitError,
    safe_read_zip_member,
    verify_zip_stream_limits,
)
from docreader.parser.concurrency import ParserCancelled
from docreader.parser.doc_parser import DocParser
from docreader.parser.docx_parser import (
    Docx,
    DocxParser,
    _save_image_to_temp,
)
from docreader.parser.epub_parser import EPUBParser
from docreader.parser.opendataloader_parser import (
    _local_hybrid_auth_proxy,
    _ping_hybrid,
)
from docreader.parser.web_parser import StdWebParser
from docreader.parser.xmind_parser import XMindParser
from docreader.proto.docreader_pb2 import ReadRequest
from docreader.source_wire import source_blocks_to_proto
from docreader.splitter.splitter import TextSplitter
from docreader.utils.request import RequestIdFilter, request_id_context
from docreader.utils.ssrf import is_ssrf_safe_url, reset_ssrf_whitelist_cache_for_test
from docreader.utils.ssrf_proxy import SSRFProxy
from docreader.utils.tempfile import TempFileContext


class _NoopPreprocessRunner:
    class _Limits:
        max_workers = 1

    limits = _Limits()

    def run(self, image, context):
        raise NotImplementedError

    def close(self):
        pass


def _build_spoofed_lzma_zip(entry_name: str, payload: bytes, spoofed_size: int = 16) -> bytes:
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", compression=zipfile.ZIP_LZMA) as zf:
        zf.writestr(entry_name, payload)
    raw = bytearray(buf.getvalue())
    lfh = raw.index(b"PK\x03\x04")
    cdh = raw.index(b"PK\x01\x02")
    raw[lfh + 22 : lfh + 26] = struct.pack("<I", spoofed_size)
    raw[cdh + 24 : cdh + 28] = struct.pack("<I", spoofed_size)
    return bytes(raw)


# ---------------------------------------------------------------------------
# Unit 1: docreader (auth.py, config.py, main.py, source_wire.py)
# ---------------------------------------------------------------------------


def test_w8_doc_redacts_secrets_and_sanitizes_surrogates_in_rpc_and_wire(monkeypatch, caplog):
    servicer = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
    try:
        secret_err = RuntimeError(
            "Upstream VLM failed: Authorization: Bearer sk-liveSecretKey1234567890 "
            "api_key=myTopSecret999 url=https://admin:UltraPass123@vlm.example.com/v1 "
            "bad_surrogate=\ud800"
        )
        monkeypatch.setattr(
            servicer.parser,
            "parse_url",
            mock.Mock(side_effect=secret_err),
        )

        req = ReadRequest(
            url="https://user:UrlPass789@example.com/doc.html?api_key=QuerySecret111",
            title="test",
            request_id="req-secret-test",
        )
        with caplog.at_level(logging.INFO):
            resp = servicer.Read(req, context=None)
            stream_frames = list(servicer.ReadStream(req, context=None))

        for leaked in (
            "sk-liveSecretKey1234567890",
            "myTopSecret999",
            "UltraPass123",
            "UrlPass789",
            "QuerySecret111",
        ):
            assert leaked not in resp.error
            assert leaked not in stream_frames[0].meta.error
            assert leaked not in caplog.text

        # Lone UTF-16 surrogates inside source_blocks locators must serialize
        # cleanly rather than dropping the block or raising UnicodeEncodeError.
        doc = Document(
            content="hello world",
            source_blocks=[
                {
                    "start": 0,
                    "end": 5,
                    "locator": {"type": "pdf", "page": 1, "title": "sec\ud800tion"},
                }
            ],
        )
        proto_blocks = source_blocks_to_proto(doc)
        assert len(proto_blocks) == 1
        parsed_loc = json.loads(proto_blocks[0].locator_json)
        assert parsed_loc["page"] == 1
        assert "\ufffd" in parsed_loc["title"]
    finally:
        servicer.close()


def test_w8_doc_auth_interceptor_case_insensitive_bearer_and_config_bounds(
    monkeypatch,
):
    monkeypatch.setenv("GRPC_AUTH_TOKEN", "strong-shared-secret-token-1234")
    interceptor = AuthInterceptor()

    called = []

    def continuation(details):
        called.append(details.method)
        return grpc.unary_unary_rpc_method_handler(lambda req, ctx: "ok")

    class _Details:
        method = "/docreader.DocReader/GetInfo"
        invocation_metadata = [("authorization", "bearer strong-shared-secret-token-1234")]

    interceptor.intercept_service(continuation, _Details())
    assert called == ["/docreader.DocReader/GetInfo"]

    # Non-positive gRPC file-size, worker, port, and render knobs must fall back
    # to positive defaults instead of producing negative/unlimited gRPC caps.
    bad_env = {
        "DOCREADER_GRPC_MAX_WORKERS": "0",
        "DOCREADER_GRPC_MAX_FILE_SIZE_MB": "-1",
        "DOCREADER_GRPC_PORT": "-50",
        "DOCREADER_PDF_RENDER_DPI": "0",
        "DOCREADER_PDF_RENDER_PARALLELISM": "-2",
    }
    with mock.patch.dict(os.environ, bad_env, clear=True):
        cfg = doc_config.load_config()
    assert cfg.grpc_max_workers == 4
    assert cfg.grpc_max_file_size_mb == 50 * 1024 * 1024
    assert cfg.grpc_max_message_size_bytes > 50 * 1024 * 1024
    assert cfg.grpc_port == 50051
    assert cfg.pdf_render_dpi == 200
    assert cfg.pdf_render_parallelism >= 1


# ---------------------------------------------------------------------------
# Unit 3: docreader/models (document.py)
# ---------------------------------------------------------------------------


def test_w8_models_chunk_eq_non_chunk_and_from_dict_immutability():
    chunk = Chunk(content="alpha", seq=1, start=0, end=5)
    assert (chunk == None) is False  # noqa: E711
    assert (chunk == "alpha") is False
    assert chunk == Chunk(content="alpha", seq=9, start=10, end=15)

    raw = chunk.to_dict()
    snapshot = dict(raw)
    rebuilt = Chunk.from_dict(raw, seq=42)
    assert rebuilt.seq == 42
    assert raw == snapshot
    assert "class_name" in raw


# ---------------------------------------------------------------------------
# Unit 4: docreader/parser (archive.py, xmind_parser.py, docx_parser.py,
#         epub_parser.py, doc_parser.py, opendataloader_parser.py, web_parser.py)
# ---------------------------------------------------------------------------


def test_w8_parser_lzma_spoofed_zip_bomb_aborts_with_bounded_memory():
    spoofed_lzma = _build_spoofed_lzma_zip(
        "word/document.xml", b"\x00" * (24 * 1024 * 1024), spoofed_size=16
    )

    tracemalloc.start()
    try:
        with (
            zipfile.ZipFile(io.BytesIO(spoofed_lzma)) as zf,
            pytest.raises(ArchiveLimitError),
        ):
            verify_zip_stream_limits(zf, max_bytes=65536)
        _, peak = tracemalloc.get_traced_memory()
    finally:
        tracemalloc.stop()

    # Must abort at ~64 KiB cap (+8 MiB preset-6 LZMA dictionary) rather than
    # decompressing all 24 MiB (~70 MiB peak in stdlib) or silently passing.
    assert peak < 10 * 1024 * 1024

    with zipfile.ZipFile(io.BytesIO(spoofed_lzma)) as zf, pytest.raises(ArchiveLimitError):
        safe_read_zip_member(zf, "word/document.xml", max_member_bytes=65536, max_total_bytes=65536)

    # Forged 512 MiB LZMA dictionary header bomb must be rejected BEFORE
    # liblzma allocates the dictionary buffer.
    dict_bomb = bytearray(_build_spoofed_lzma_zip("bomb.xml", b"hello", spoofed_size=5))
    lfh = dict_bomb.index(b"PK\x03\x04")
    fname_len, extra_len = struct.unpack("<HH", dict_bomb[lfh + 26 : lfh + 30])
    data_off = lfh + 30 + fname_len + extra_len
    # ZIP LZMA stream starts with 4-byte header (<BBH) + 5-byte LZMA1 props
    dict_bomb[data_off + 5 : data_off + 9] = struct.pack("<I", 512 * 1024 * 1024)

    tracemalloc.start()
    try:
        with (
            zipfile.ZipFile(io.BytesIO(bytes(dict_bomb))) as zf,
            pytest.raises(ArchiveLimitError, match="dictionary size"),
        ):
            verify_zip_stream_limits(zf, max_bytes=65536)
        _, dict_peak = tracemalloc.get_traced_memory()
    finally:
        tracemalloc.stop()
    assert dict_peak < 512 * 1024


def test_w8_parser_xmind_lzma_bomb_and_deep_topic_recursion_guarded():
    spoofed_xmind = _build_spoofed_lzma_zip(
        "content.json", b" " * (4 * 1024 * 1024), spoofed_size=32
    )
    with (
        mock.patch("docreader.parser.xmind_parser.MAX_CONTENT_BYTES", 65536),
        pytest.raises((ArchiveLimitError, ValueError)),
    ):
        XMindParser().parse_into_text(spoofed_xmind)

    # Deeply nested XML topics (>300 levels) must fail cleanly with ValueError
    # rather than crashing with RecursionError.
    nested_xml = ["<xmap-content><sheet><title>S</title>"]
    depth = 400
    for idx in range(depth):
        nested_xml.append(f"<topic><title>T{idx}</title><children><topics>")
    nested_xml.append("<topic><title>Leaf</title></topic>")
    for _ in range(depth):
        nested_xml.append("</topics></children></topic>")
    nested_xml.append("</sheet></xmap-content>")

    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", compression=zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("content.xml", "".join(nested_xml).encode("utf-8"))

    with pytest.raises(ValueError, match="depth"):
        XMindParser().parse_into_text(buf.getvalue())


def test_w8_parser_docx_and_epub_propagate_cancellation_and_cleanup_tempfiles(
    monkeypatch, tmp_path
):
    # 1. DocxParser must not swallow ParserCancelled or ArchiveLimitError into fallback
    docx_parser = DocxParser(file_name="cancel.docx", file_type="docx")
    fallback_spy = mock.Mock(return_value=Document(content="fallback"))
    monkeypatch.setattr(docx_parser, "_parse_using_simple_method", fallback_spy)

    with (
        mock.patch(
            "docreader.parser.docx_parser.Docx.__call__",
            side_effect=ParserCancelled("cancelled"),
        ),
        pytest.raises(ParserCancelled),
    ):
        docx_parser.parse_into_text(b"not-pk")
    fallback_spy.assert_not_called()

    # 2. EPUBParser must not swallow ParserCancelled into _parse_epub_fallback
    epub_parser = EPUBParser(file_name="cancel.epub", file_type="epub")
    epub_fallback_spy = mock.Mock(return_value=Document(content="fallback"))
    monkeypatch.setattr(epub_parser, "_parse_epub_fallback", epub_fallback_spy)
    with mock.patch(
        "docreader.parser.epub_parser._read_package",
        side_effect=ParserCancelled("cancelled"),
    ):
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as zf:
            zf.writestr("mimetype", b"application/epub+zip")
        with pytest.raises(ParserCancelled):
            epub_parser.parse_into_text(buf.getvalue())
    epub_fallback_spy.assert_not_called()

    # 3. _save_image_to_temp must remove its mkdtemp directory when image.save fails
    created_dirs: list[str] = []
    real_mkdtemp = tempfile.mkdtemp

    def tracking_mkdtemp(*args, **kwargs):
        d = real_mkdtemp(*args, **kwargs)
        created_dirs.append(d)
        return d

    bad_img = mock.Mock()
    bad_img.save.side_effect = OSError("disk full")
    with mock.patch("tempfile.mkdtemp", side_effect=tracking_mkdtemp):
        out = _save_image_to_temp(logging.getLogger("test"), bad_img, 0, 0)
    assert out is None
    assert len(created_dirs) == 1
    assert not os.path.exists(created_dirs[0])

    # 4. Docx._prepare_document_sharing must unlink its temp file if write fails
    created_files: list[str] = []
    real_ntf = tempfile.NamedTemporaryFile

    def failing_ntf(*args, **kwargs):
        f = real_ntf(*args, **kwargs)
        created_files.append(f.name)

        class _Wrap:
            name = f.name

            def write(self, _b):
                raise OSError("ENOSPC")

            def close(self):
                f.close()

        return _Wrap()

    with (
        mock.patch("docreader.parser.docx_parser.tempfile.NamedTemporaryFile", failing_ntf),
        pytest.raises(OSError),
    ):
        Docx()._prepare_document_sharing(b"docx-bytes")
    assert len(created_files) == 1
    assert not os.path.exists(created_files[0])

    # 5. DocParser._try_convert_doc_to_docx must skip symlinks in output directory
    doc_parser = DocParser(file_name="legacy.doc", file_type="doc")
    secret_target = tmp_path / "secret.txt"
    secret_target.write_bytes(b"TOP_SECRET_HOST_FILE")

    def fake_soffice_convert(_soffice, *, convert_to, outdir, input_path, profile_dir, executor):
        os.symlink(str(secret_target), os.path.join(outdir, "escaped.docx"))
        return b"", b"", 0

    monkeypatch.setattr(doc_parser, "_try_find_soffice", lambda: "/usr/bin/soffice")
    monkeypatch.setattr("docreader.parser.doc_parser.run_soffice_convert", fake_soffice_convert)
    monkeypatch.setattr("docreader.parser.doc_parser.time.sleep", lambda _s: None)
    assert doc_parser._try_convert_doc_to_docx(str(tmp_path / "in.doc")) is None


def test_w8_parser_odl_proxy_bounds_upstream_response_and_pins_dns(monkeypatch):
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class _GiantResponseHandler(BaseHTTPRequestHandler):
        def log_message(self, format, *args):  # noqa: A002
            pass

        def do_POST(self):
            payload = b"X" * 4096
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

    srv = ThreadingHTTPServer(("127.0.0.1", 0), _GiantResponseHandler)
    th = threading.Thread(target=srv.serve_forever, daemon=True)
    th.start()
    upstream_url = f"http://127.0.0.1:{srv.server_port}"
    try:
        monkeypatch.setattr(
            "docreader.parser.opendataloader_parser.is_ssrf_safe_url",
            lambda u: (True, ""),
        )
        with _local_hybrid_auth_proxy(
            upstream_url, "secret-token", timeout_sec=5.0, max_body_bytes=512
        ) as local_url:
            req = urllib.request.Request(f"{local_url}/v1/convert/file", data=b"hi", method="POST")
            with pytest.raises(urllib.error.HTTPError) as err:
                urllib.request.urlopen(req, timeout=5)
            assert err.value.code == 502
            err.value.close()

        # Verify DNS rebinding between is_ssrf_safe_url and socket connect is blocked
        monkeypatch.setattr(
            "docreader.parser.opendataloader_parser._resolve_host_ips",
            lambda host: ((ipaddress.ip_address("169.254.169.254"),), None),
        )
        monkeypatch.setattr(
            "docreader.parser.opendataloader_parser._is_whitelisted",
            lambda host: False,
        )
        ok, msg = _ping_hybrid("http://rebind.example.com:8080", retries=1)
        assert ok is False
        assert "SSRF" in msg or "restricted" in msg
    finally:
        srv.shutdown()
        srv.server_close()
        th.join(timeout=2.0)

    from dataclasses import replace as dc_replace

    patched_cfg = dc_replace(
        doc_config.CONFIG,
        external_https_proxy="",
        external_http_proxy="http://proxy.example.com:8080",
    )
    monkeypatch.setattr("docreader.parser.web_parser.CONFIG", patched_cfg)
    assert StdWebParser(title="t").proxy == "http://proxy.example.com:8080"


# ---------------------------------------------------------------------------
# Unit 5: docreader/splitter (splitter.py)
# ---------------------------------------------------------------------------


def test_w8_splitter_guards_overlap_recursion_logging_header_state_and_restore(
    caplog,
):
    # 1. Invalid chunk_size / chunk_overlap combinations must raise ValueError
    for bad_size, bad_overlap in ((0, 0), (-5, 0), (10, -1), (10, 10), (10, 12)):
        with pytest.raises(ValueError):
            TextSplitter(chunk_size=bad_size, chunk_overlap=bad_overlap)

    # 2. Single unsplittable multi-byte character exceeding byte-length chunk_size
    #    must not trigger infinite recursion, nor raise logging TypeError.
    byte_splitter: TextSplitter = TextSplitter(
        chunk_size=2,
        chunk_overlap=0,
        separators=[" "],
        length_function=lambda s: len(s.encode("utf-8")),
    )
    with caplog.at_level(logging.ERROR):
        chunks = byte_splitter.split_text("你好")
    assert [c[2] for c in chunks] == ["你", "好"]

    # 3. Reusing a TextSplitter across two documents must not leak table headers
    #    from document 1 into document 2.
    table_splitter: TextSplitter = TextSplitter(
        chunk_size=60, chunk_overlap=0, separators=["\n\n", "\n"]
    )
    doc1 = "| ColA | ColB |\n| --- | --- |\n| 1 | 2 |\n"
    doc2 = (
        "First paragraph with enough characters to fill the first chunk cleanly.\n"
        "Second paragraph that starts a brand new chunk without any table."
    )
    table_splitter.split_text(doc1)
    doc2_chunks = table_splitter.split_text(doc2)
    for _, _, chunk_text in doc2_chunks:
        assert "ColA" not in chunk_text

    # 4. restore_text must ignore duplicate/contained end_pos chunks cleanly
    restored = table_splitter.restore_text([(0, 5, "hello"), (0, 5, "hello"), (5, 11, " world")])
    assert restored == "hello world"


# ---------------------------------------------------------------------------
# Unit 6: docreader/utils (ssrf.py, ssrf_proxy.py, tempfile.py, request.py)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "unsafe_url",
    [
        "http://169.254.169.254\\@example.com/latest/meta-data/",
        "https://127.0.0.1\\@example.com/",
        "http:\\\\127.0.0.1/admin",
        "http://user:pass@example.com/private",
        "http://127.1/admin",
        "http://0/admin",
        "http://017700000001/admin",
        "http://127.0x0.0.1/admin",
        "http://[fe80::1%25eth0]/",
    ],
)
def test_w8_utils_ssrf_blocks_backslash_userinfo_shorthand_ips_before_dns(monkeypatch, unsafe_url):
    monkeypatch.setenv("SSRF_WHITELIST", "")
    monkeypatch.setenv("SSRF_WHITELIST_EXTRA", "")
    monkeypatch.setenv("SSRF_DNS_WHITELIST_ONLY", "")
    reset_ssrf_whitelist_cache_for_test()
    monkeypatch.setattr(
        "docreader.utils.ssrf.socket.getaddrinfo",
        lambda *a, **kw: pytest.fail(f"DNS lookup must not run for {unsafe_url}"),
    )
    safe, reason = is_ssrf_safe_url(unsafe_url)
    assert safe is False
    assert reason


def test_w8_utils_ssrf_enforces_port_bounds_and_blocked_ports_on_whitelisted_host(
    monkeypatch,
):
    monkeypatch.setenv("SSRF_WHITELIST", "docs.internal")
    monkeypatch.setenv("SSRF_WHITELIST_EXTRA", "")
    monkeypatch.setenv("SSRF_DNS_WHITELIST_ONLY", "true")
    reset_ssrf_whitelist_cache_for_test()
    try:
        safe_ok, _ = is_ssrf_safe_url("https://docs.internal:8443/api")
        assert safe_ok is True

        safe_bad_port, reason_bad = is_ssrf_safe_url("https://docs.internal:99999/api")
        assert safe_bad_port is False
        assert "port" in reason_bad.lower()

        safe_redis, reason_redis = is_ssrf_safe_url("http://docs.internal:6379/")
        assert safe_redis is False
        assert "6379" in reason_redis
    finally:
        reset_ssrf_whitelist_cache_for_test()


def test_w8_utils_ssrf_proxy_handles_malformed_upstream_and_tempfile_request_hygiene():
    async def _exercise_malformed_upstream():
        async def bad_upstream(reader, writer):
            try:
                await reader.readuntil(b"\r\n\r\n")
                writer.write(b"MALFORMED_STATUS_LINE\r\n\r\n")
                await writer.drain()
            finally:
                writer.close()

        srv = await asyncio.start_server(bad_upstream, "127.0.0.1", 0)
        u_port = srv.sockets[0].getsockname()[1]
        try:
            with mock.patch(
                "docreader.utils.ssrf_proxy.checked_address",
                new=mock.AsyncMock(return_value=("93.184.216.34", 443)),
            ):
                async with SSRFProxy(f"http://127.0.0.1:{u_port}") as proxy:
                    p_port = int(proxy.url.rsplit(":", 1)[1])
                    reader, writer = await asyncio.open_connection("127.0.0.1", p_port)
                    writer.write(
                        b"CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"
                    )
                    await writer.drain()
                    reply = await reader.read()
                    assert b"403 Forbidden" in reply
                    writer.close()
                    await writer.wait_closed()
        finally:
            srv.close()
            await srv.wait_closed()

    asyncio.run(_exercise_malformed_upstream())

    # TempFileContext must sanitize path-traversal suffixes
    with TempFileContext(b"hello", "../../evil/report.doc") as path:
        assert os.path.exists(path)
        assert path.endswith("report.doc")
        assert os.path.dirname(os.path.abspath(path)) == os.path.abspath(tempfile.gettempdir())
    assert not os.path.exists(path)

    # request_id_context must scrub CR/LF log injection characters
    with request_id_context("req123\r\nFAKE_LOG_LINE") as sanitized_id:
        assert sanitized_id is not None
        assert "\r" not in sanitized_id
        assert "\n" not in sanitized_id
        rec = logging.LogRecord("test", logging.INFO, __file__, 1, "msg", (), None)
        RequestIdFilter().filter(rec)
        req_id = str(getattr(rec, "request_id", ""))
        assert "\n" not in req_id
        assert "\r" not in req_id
