"""Real gRPC message budgets are distinct from raw uploaded file bytes."""

import hashlib
import os
import time
import unittest
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from typing import Any, cast
from unittest.mock import patch

import grpc

from docreader import main
from docreader.models.document import Document
from docreader.parser.stream import ParsedDocumentStream
from docreader.proto import docreader_pb2 as pb
from docreader.proto import docreader_pb2_grpc as rpc
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner


class _MemoryParser:
    def __init__(self) -> None:
        self.calls = 0
        self.reply_bytes = 0
        self.parse_file_stream: Any = self._parse_file_stream

    def parse_file(self, file_name: str, file_type: str, content: bytes, **kwargs: Any) -> Document:
        del file_name, file_type, kwargs
        self.calls += 1
        text = "x" * self.reply_bytes if self.reply_bytes else hashlib.sha256(content).hexdigest()
        return Document(content=text)

    def _parse_file_stream(
        self, file_name: str, file_type: str, content: bytes, **kwargs: Any
    ) -> ParsedDocumentStream:
        document = self.parse_file(file_name, file_type, content, **kwargs)
        return ParsedDocumentStream(document=document, image_count=0)


class DocReaderEnvelopeTest(unittest.TestCase):
    def setUp(self) -> None:
        self.limit = 1 << 20
        self.config_patch = patch.object(
            main,
            "CONFIG",
            replace(main.CONFIG, grpc_max_file_size_mb=self.limit),
        )
        self.config_patch.start()
        self.addCleanup(self.config_patch.stop)
        self.pool = ThreadPoolExecutor(max_workers=2)
        self.addCleanup(self.pool.shutdown)
        self.service = main.DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        self.addCleanup(self.service.close)
        self.parser = _MemoryParser()
        self.service.parser = cast(Any, self.parser)
        # Before the fix, the server applies the raw file cap as a wire cap.
        wire_limit = getattr(main.CONFIG, "grpc_max_message_size_bytes", self.limit)
        self.server = grpc.server(
            self.pool,
            options=[
                ("grpc.max_send_message_length", wire_limit),
                ("grpc.max_receive_message_length", wire_limit),
            ],
        )
        rpc.add_DocReaderServicer_to_server(self.service, self.server)
        port = self.server.add_insecure_port("127.0.0.1:0")
        self.server.start()
        self.addCleanup(lambda: self.server.stop(0).wait())
        self.channel = grpc.insecure_channel(
            f"127.0.0.1:{port}",
            options=[
                ("grpc.max_send_message_length", self.limit + (1 << 20)),
                ("grpc.max_receive_message_length", self.limit + (1 << 20)),
            ],
        )
        self.addCleanup(self.channel.close)
        self.stub = rpc.DocReaderStub(self.channel)

    def request(self, size, metadata=0):
        return pb.ReadRequest(
            file_content=b"a" * size,
            file_name="report.txt",
            file_type="txt",
            config=pb.ReadConfig(
                parser_engine="builtin",
                parser_engine_overrides={"metadata": "m" * metadata},
            ),
        )

    def read(self, request, stream):
        if stream:
            frames = list(self.stub.ReadStream(request, timeout=5))
            self.assertEqual(len(frames), 1)
            return frames[0].meta
        return self.stub.Read(request, timeout=5)

    def test_exact_file_cap_and_metadata_survive_both_rpcs(self):
        for stream in (False, True):
            for size, metadata in ((self.limit, 0), (self.limit - 512, 4096)):
                with self.subTest(stream=stream, size=size):
                    request = self.request(size, metadata)
                    result = self.read(request, stream)
                    self.assertFalse(result.error)
                    expected = hashlib.sha256(request.file_content).hexdigest()
                    self.assertEqual(result.markdown_content, expected)

    def test_response_at_raw_file_cap_survives_protobuf_framing(self):
        self.parser.reply_bytes = self.limit
        for stream in (False, True):
            with self.subTest(stream=stream):
                result = self.read(self.request(16), stream)
                self.assertFalse(result.error)
                self.assertEqual(result.markdown_content, "x" * self.limit)

    def test_raw_file_over_cap_is_rejected_before_parsing(self):
        for stream in (False, True):
            with self.subTest(stream=stream):
                result = self.read(self.request(self.limit + 1), stream)
                self.assertIn("exceeds", result.error)
                self.assertFalse(result.markdown_content)
                self.assertEqual(self.parser.calls, 0)

    def test_stream_fallback_without_parser_streaming_preserves_raw_cap(self):
        self.parser.parse_file_stream = None
        result = self.read(self.request(self.limit + 1), stream=True)
        self.assertIn("exceeds", result.error)
        self.assertEqual(self.parser.calls, 0)

    def test_wire_budget_still_rejects_oversized_metadata(self):
        for stream in (False, True):
            with self.subTest(stream=stream), self.assertRaises(grpc.RpcError) as caught:
                self.read(self.request(16, 3 << 20), stream)
            self.assertEqual(caught.exception.code(), grpc.StatusCode.RESOURCE_EXHAUSTED)
        self.assertEqual(self.parser.calls, 0)


class DocReaderEnvelopeStartupTest(unittest.TestCase):
    def test_main_server_uses_envelope_budget_in_both_directions(self):
        limit = 1 << 20
        cfg = replace(main.CONFIG, grpc_max_file_size_mb=limit, grpc_port=0)
        service = main.DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        parser: Any = _MemoryParser()
        service.parser = parser
        real_server = grpc.server
        pools = []
        port = None

        def create_server(pool, **kwargs):
            nonlocal port
            pools.append(pool)
            server = real_server(pool, **kwargs)
            bind = server.add_insecure_port

            def bind_local(_address):
                nonlocal port
                port = bind("127.0.0.1:0")
                return port

            def probe():
                with grpc.insecure_channel(
                    f"127.0.0.1:{port}",
                    options=[
                        ("grpc.max_send_message_length", 2 * limit),
                        ("grpc.max_receive_message_length", 2 * limit),
                    ],
                ) as channel:
                    stub = rpc.DocReaderStub(channel)
                    req = pb.ReadRequest(
                        file_content=b"a" * limit,
                        file_name="report.txt",
                        file_type="txt",
                    )
                    result = stub.Read(req, timeout=5)
                    self.assertFalse(result.error)
                    expected = hashlib.sha256(req.file_content).hexdigest()
                    self.assertEqual(result.markdown_content, expected)
                    parser.reply_bytes = limit
                    frames = list(stub.ReadStream(req, timeout=5))
                    self.assertEqual(frames[0].meta.markdown_content, "x" * limit)

            server.add_insecure_port = bind_local
            server.wait_for_termination = probe
            return server

        unauth_env = {
            "GRPC_AUTH_TOKEN": "",
            "DOCREADER_ALLOW_UNAUTHENTICATED": "true",
        }
        try:
            with (
                patch.object(main, "CONFIG", cfg),
                patch.dict(os.environ, unauth_env),
                patch.object(main.grpc, "server", create_server),
                patch.object(main, "DocReaderServicer", return_value=service),
                patch.object(main, "load_tls_credentials", return_value=None),
                patch.object(main.signal, "signal"),
                patch.object(main.config, "print_config"),
            ):
                main.main()
        finally:
            for pool in pools:
                pool.shutdown()
            service.close()


class RawImageBytesAndGracefulShutdownTest(unittest.TestCase):
    def test_resolve_images_and_iter_image_refs_preserve_raw_binary_images(self):
        import base64

        # Raw PNG header + chunk bytes whose ASCII base64-alphabet subset has
        # length % 4 == 0 (8 chars: PNGIHDRA), causing unguarded
        # base64.b64decode(raw_png) to return silently corrupted bytes!
        raw_png = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00A"
        b64_png = base64.b64encode(raw_png).decode("ascii")
        wrapped_b64 = b64_png[:8] + "\r\n  " + b64_png[8:]

        _, resolved = main._resolve_images(
            {
                "images/raw.png": raw_png,
                "images/b64.png": b64_png,
                "images/wrapped.png": wrapped_b64,
            },
            "req-img-test",
        )
        by_name = {ref.filename: ref.image_data for ref in resolved}
        self.assertEqual(by_name["raw.png"], raw_png)
        self.assertEqual(by_name["b64.png"], raw_png)
        self.assertEqual(by_name["wrapped.png"], raw_png)

        stream_dict = {
            "images/raw.png": raw_png,
            "images/b64.png": b64_png,
            "images/wrapped.png": wrapped_b64,
        }
        streamed = {ref.filename: ref.image_data for ref in main._iter_image_refs(stream_dict)}
        self.assertEqual(streamed["raw.png"], raw_png)
        self.assertEqual(streamed["b64.png"], raw_png)
        self.assertEqual(streamed["wrapped.png"], raw_png)

    def test_sigterm_enters_graceful_shutdown_and_drains_inflight_rpc(self):
        import signal as sig_mod
        import threading

        from grpc_health.v1 import health_pb2, health_pb2_grpc

        rpc_entered = threading.Event()
        release_rpc = threading.Event()

        class _SlowParser:
            def parse_file(self, file_name, file_type, content, **kwargs):
                rpc_entered.set()
                release_rpc.wait(timeout=5)
                return Document(content="drained cleanly")

            def parse_file_stream(self, file_name, file_type, content, **kwargs):
                return ParsedDocumentStream(
                    document=self.parse_file(file_name, file_type, content, **kwargs),
                    image_count=0,
                )

        cfg = replace(main.CONFIG, grpc_port=0, grpc_shutdown_grace_seconds=5)
        service = main.DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        slow_parser: Any = _SlowParser()
        service.parser = slow_parser
        real_server = grpc.server
        bound_port = 0
        registered_handler = None

        def capture_signal(signum, handler):
            nonlocal registered_handler
            if signum == sig_mod.SIGTERM:
                registered_handler = handler
            return None

        def create_server(pool, **kwargs):
            server = real_server(pool, **kwargs)
            orig_bind = server.add_insecure_port

            def bind_ephemeral(_addr):
                nonlocal bound_port
                bound_port = orig_bind("127.0.0.1:0")
                return bound_port

            server.add_insecure_port = bind_ephemeral
            return server

        server_thread = threading.Thread(
            target=lambda: main.main(),
            daemon=True,
        )
        with (
            patch.object(main, "CONFIG", cfg),
            patch.dict(
                os.environ,
                {"GRPC_AUTH_TOKEN": "", "DOCREADER_ALLOW_UNAUTHENTICATED": "true"},
            ),
            patch.object(main.grpc, "server", create_server),
            patch.object(main, "DocReaderServicer", return_value=service),
            patch.object(main, "load_tls_credentials", return_value=None),
            patch.object(main.signal, "signal", side_effect=capture_signal),
            patch.object(main.config, "print_config"),
        ):
            server_thread.start()
            deadline = time.monotonic() + 5
            while (not bound_port or registered_handler is None) and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertGreater(bound_port, 0)
            self.assertIsNotNone(registered_handler)

            with grpc.insecure_channel(f"127.0.0.1:{bound_port}") as channel:
                stub = rpc.DocReaderStub(channel)
                health_stub = health_pb2_grpc.HealthStub(channel)
                watch_call = health_stub.Watch(health_pb2.HealthCheckRequest(), timeout=5)
                first_health = next(watch_call)
                self.assertEqual(first_health.status, health_pb2.HealthCheckResponse.SERVING)

                req = pb.ReadRequest(file_content=b"hello", file_name="doc.txt", file_type="txt")
                inflight = stub.Read.future(req, timeout=5)
                self.assertTrue(rpc_entered.wait(timeout=5))

                # Trigger SIGTERM handler while RPC is in flight
                assert callable(registered_handler)
                registered_handler(sig_mod.SIGTERM, None)

                # Health watcher receives NOT_SERVING from enter_graceful_shutdown()
                second_health = next(watch_call)
                self.assertEqual(second_health.status, health_pb2.HealthCheckResponse.NOT_SERVING)
                watch_call.cancel()

                # Release slow parser; in-flight RPC must finish without being aborted!
                release_rpc.set()
                result = inflight.result(timeout=5)
                self.assertEqual(result.markdown_content, "drained cleanly")

            server_thread.join(timeout=5)
            self.assertFalse(server_thread.is_alive())
