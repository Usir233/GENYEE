import base64
import logging
import os
import re
import signal
import sys
import threading
import traceback
import uuid
from collections.abc import Generator, Iterator, Mapping
from concurrent import futures
from typing import Any

import grpc
from grpc_health.v1 import health_pb2_grpc
from grpc_health.v1.health import HealthServicer
from PIL import Image

from docreader import config
from docreader.auth import (
    AuthConfigError,
    AuthInterceptor,
    TLSConfigError,
    load_tls_credentials,
)
from docreader.config import CONFIG, redact_secrets
from docreader.models.document import Document
from docreader.parser import Parser
from docreader.parser.concurrency import check_cancellation
from docreader.parser.parser import sanitize_engine_overrides
from docreader.parser.registry import registry
from docreader.parser.stream import ParsedDocumentStream
from docreader.preprocess import PreprocessLimits, PreprocessRunner
from docreader.proto import docreader_pb2_grpc
from docreader.proto.docreader_pb2 import (
    GetInfoRequest,
    GetInfoResponse,
    ImageRef,
    ListEnginesRequest,
    ListEnginesResponse,
    ParserEngineInfo,
    PreprocessImageRequest,
    PreprocessImageResponse,
    ReadRequest,
    ReadResponse,
    ReadStreamMeta,
    ReadStreamResponse,
)
from docreader.source_wire import source_blocks_to_proto
from docreader.utils.request import init_logging_request_id, request_id_context
from docreader.utils.request_memory import RequestThreadPoolExecutor, track_request_size

# Tighten Pillow decompression-bomb pixel ceiling across all in-process parsers.
Image.MAX_IMAGE_PIXELS = 40_000_000

_SURROGATE_RE = re.compile(r"[\ud800-\udfff]")
_MIME_MAP: dict[str, str] = {
    ".png": "image/png",
    ".jpg": "image/jpeg",
    ".jpeg": "image/jpeg",
    ".gif": "image/gif",
    ".webp": "image/webp",
    ".bmp": "image/bmp",
}


def to_valid_utf8_text(s: str | None) -> str:
    if not s:
        return ""
    s = _SURROGATE_RE.sub("\ufffd", s)
    return s.encode("utf-8", errors="replace").decode("utf-8")


def _safe_error_text(s: str | None) -> str:
    return to_valid_utf8_text(redact_secrets(s or ""))


for _existing_handler in logging.root.handlers[:]:
    logging.root.removeHandler(_existing_handler)

handler = logging.StreamHandler(sys.stdout)
logging.root.addHandler(handler)

_level_name = (os.environ.get("LOG_LEVEL") or "INFO").upper()
_level = getattr(logging, _level_name, logging.INFO)
logging.root.setLevel(_level)

logger = logging.getLogger(__name__)
logger.info("Initializing server logging, level=%s", _level_name)

init_logging_request_id()


def _decode_inline_image_data(raw_data: str | bytes | bytearray | memoryview) -> bytes:
    """Decode a base64 image string or preserve already-decoded raw image bytes."""
    if isinstance(raw_data, bytes):
        return raw_data
    if isinstance(raw_data, (bytearray, memoryview)):
        return bytes(raw_data)
    compact = "".join(raw_data.split())
    try:
        return base64.b64decode(compact, validate=True)
    except Exception:
        return raw_data.encode("utf-8")


def _mime_for_ref(ref_path: str) -> tuple[str, str]:
    """Return (filename, mime_type) for an image reference path."""
    fname = os.path.basename(ref_path) or f"{uuid.uuid4().hex}.png"
    ext = os.path.splitext(fname)[1].lower()
    return fname, _MIME_MAP.get(ext, "application/octet-stream")


def _resolve_images(
    images: Mapping[str, str | bytes | bytearray | memoryview] | None,
    request_id: str,
    storage_map: Mapping[str, Any] | None = None,
) -> tuple[str, list[ImageRef]]:
    """Resolve document images into inline bytes for the Go App to persist.

    ``images`` is a dict of {relative_path: raw_data} where raw_data is
    base64-encoded string or raw bytes.

    The Go App is solely responsible for persisting images to the configured
    storage backend (local/minio/cos/tos). This function only decodes images
    and returns them as inline bytes via ImageRef.

    Returns ("", list[ImageRef]).  image_dir_path is always empty.
    """
    del request_id, storage_map
    if not images:
        return "", []

    refs: list[ImageRef] = []
    for ref_path, raw_data in images.items():
        img_bytes = _decode_inline_image_data(raw_data)
        fname, mime = _mime_for_ref(ref_path)
        refs.append(
            ImageRef(
                filename=fname,
                original_ref=ref_path,
                mime_type=mime,
                image_data=img_bytes,
            )
        )

    logger.info("Resolved %d images (mode=inline)", len(refs))
    return "", refs


def _iter_image_refs(images: dict[str, Any]) -> Iterator[ImageRef]:
    """Yield ImageRef one at a time, freeing each source entry as we go.

    Used by the streaming RPC so we never hold every decoded image plus its
    base64 source in memory simultaneously (the inline path's peak-memory and
    message-size problem for large scanned PDFs).
    """
    for ref_path in list(images.keys()):
        raw_data = images.pop(ref_path)
        img_bytes = _decode_inline_image_data(raw_data)
        del raw_data
        fname, mime = _mime_for_ref(ref_path)
        yield ImageRef(
            filename=fname,
            original_ref=ref_path,
            mime_type=mime,
            image_data=img_bytes,
        )


def _image_ref_from_streamed_data(ref_path: str, image_data: bytes) -> ImageRef:
    """Build one image reference from raw bytes yielded by a parser."""
    fname, mime = _mime_for_ref(ref_path)
    return ImageRef(
        filename=fname,
        original_ref=ref_path,
        mime_type=mime,
        image_data=image_data,
    )


def _validate_file_content_size(content: bytes) -> None:
    if len(content) > CONFIG.grpc_max_file_size_mb:
        raise ValueError(
            f"File content size {len(content)} exceeds maximum {CONFIG.grpc_max_file_size_mb}"
        )


class _BoundPreprocessHandler:
    """Callable RPC handler carrying gRPC's per-method experimental_thread_pool."""

    def __init__(
        self,
        runner: Any,
        executor: futures.ThreadPoolExecutor,
    ) -> None:
        self._runner = runner
        self.experimental_thread_pool = executor

    def __call__(
        self,
        request: PreprocessImageRequest,
        context: Any,
    ) -> PreprocessImageResponse:
        result: PreprocessImageResponse = self._runner.run(request.image, context)
        return result


class DocReaderServicer(docreader_pb2_grpc.DocReaderServicer):
    def __init__(self, preprocess_runner: Any = None) -> None:
        super().__init__()
        self.parser = Parser()
        self._preprocess_runner = preprocess_runner or PreprocessRunner(
            PreprocessLimits.from_config(CONFIG)
        )
        # gRPC supports a per-method executor. Leave one admission thread free
        # to decline busy requests while the admitted calls await their child.
        # Ordinary Read/ReadStream/ListEngines RPCs retain the server's executor.
        self._preprocess_executor = futures.ThreadPoolExecutor(
            max_workers=self._preprocess_runner.limits.max_workers + 1,
            thread_name_prefix="image-preprocess",
        )
        self._preprocess_handler = _BoundPreprocessHandler(
            self._preprocess_runner, self._preprocess_executor
        )

    @property
    def PreprocessImage(self) -> _BoundPreprocessHandler:
        return self._preprocess_handler

    def close(self) -> None:
        self._preprocess_runner.close()
        self._preprocess_executor.shutdown(wait=True, cancel_futures=True)

    def GetInfo(self, request: GetInfoRequest, context: Any) -> GetInfoResponse:
        """Return metadata embedded in the image that runs this instance."""
        del request, context
        return GetInfoResponse(
            version=os.getenv("DOCREADER_VERSION", "unknown"),
            commit_id=os.getenv("DOCREADER_COMMIT_ID", "unknown"),
        )

    def _parse_request(
        self,
        request: ReadRequest,
        context: Any = None,
    ) -> tuple[Document | None, str]:
        """Run the parser for a ReadRequest, returning (result, source_desc).

        Shared by the unary Read and streaming ReadStream RPCs.
        """
        check_cancellation(context)
        cfg = request.config
        parser_engine = cfg.parser_engine if cfg else ""
        engine_overrides = dict(cfg.parser_engine_overrides) if cfg else {}

        if request.url:
            safe_url = _safe_error_text(request.url)
            logger.info("Read(URL): url=%s", safe_url)
            url_result = self.parser.parse_url(
                request.url,
                request.title,
                parser_engine=parser_engine,
                engine_overrides=engine_overrides,
                context=context,
            )
            return url_result, safe_url

        file_type = request.file_type or os.path.splitext(request.file_name)[1][1:]
        file_content = request.file_content
        track_request_size(len(file_content))
        _validate_file_content_size(file_content)
        logger.info(
            "Read(File): file=%s, type=%s, size=%d bytes",
            request.file_name,
            file_type,
            len(file_content),
        )
        file_result = self.parser.parse_file(
            request.file_name,
            file_type,
            file_content,
            parser_engine=parser_engine,
            engine_overrides=engine_overrides,
            context=context,
        )
        return file_result, request.file_name

    def Read(self, request: ReadRequest, context: Any) -> ReadResponse:
        """Unified read: file mode (file_content set) or URL mode (url set)."""
        request_id = request.request_id or str(uuid.uuid4())

        with request_id_context(request_id):
            try:
                result, source_desc = self._parse_request(request, context)

                if not result or not result.content:
                    error_msg = _safe_error_text(f"Failed to parse: {source_desc}")
                    logger.error(error_msg)
                    return ReadResponse(error=error_msg)

                _c = to_valid_utf8_text
                image_dir, image_refs = _resolve_images(result.images, request_id)

                response = ReadResponse(
                    markdown_content=_c(result.content),
                    image_refs=image_refs,
                    image_dir_path=image_dir,
                    metadata={k: _c(str(v)) for k, v in result.metadata.items()}
                    if result.metadata
                    else {},
                    source_blocks=source_blocks_to_proto(result),
                )
                logger.info(
                    "Read response: content_len=%d, images=%d",
                    len(result.content),
                    len(image_refs),
                )
                return response

            except Exception as e:
                safe_exc = _safe_error_text(str(e))
                error_msg = f"Error reading document: {safe_exc}"
                logger.error(error_msg)
                logger.info("Traceback: %s", _safe_error_text(traceback.format_exc()))
                return ReadResponse(error=safe_exc)

    def ReadStream(
        self, request: ReadRequest, context: Any
    ) -> Generator[ReadStreamResponse, None, None]:
        """Yield metadata first, then images as each parser produces them."""
        request_id = request.request_id or str(uuid.uuid4())

        with request_id_context(request_id):
            _c = to_valid_utf8_text
            parsed_stream: ParsedDocumentStream | None = None
            result: Document | None = None
            try:
                if request.url or not callable(getattr(self.parser, "parse_file_stream", None)):
                    result, source_desc = self._parse_request(request, context)
                    if result is not None:
                        parsed_stream = ParsedDocumentStream(
                            document=result,
                            image_count=len(result.images or {}),
                        )
                else:
                    cfg = request.config
                    parser_engine = cfg.parser_engine if cfg else ""
                    engine_overrides = dict(cfg.parser_engine_overrides) if cfg else {}
                    file_type = request.file_type or os.path.splitext(request.file_name)[1][1:]
                    file_content = request.file_content
                    try:
                        track_request_size(len(file_content))
                        _validate_file_content_size(file_content)
                        logger.info(
                            "Read(File stream): file=%s, type=%s, size=%d bytes",
                            request.file_name,
                            file_type,
                            len(file_content),
                        )
                        parsed_stream = self.parser.parse_file_stream(
                            request.file_name,
                            file_type,
                            file_content,
                            parser_engine=parser_engine,
                            engine_overrides=engine_overrides,
                            context=context,
                        )
                    finally:
                        del file_content
                    result = parsed_stream.document
                    source_desc = request.file_name
            except Exception as e:
                safe_exc = _safe_error_text(str(e))
                logger.error("Error reading document: %s", safe_exc)
                logger.info("Traceback: %s", _safe_error_text(traceback.format_exc()))
                if parsed_stream is not None:
                    parsed_stream.close()
                del request
                yield ReadStreamResponse(meta=ReadStreamMeta(error=safe_exc))
                return

            # PDF parser streams own a temporary source file; drop the request's
            # full bytes field while the client drains page images.
            del request

            if parsed_stream is None or not result or not result.content:
                error_msg = _safe_error_text(f"Failed to parse: {source_desc}")
                logger.error(error_msg)
                if parsed_stream is not None:
                    parsed_stream.close()
                yield ReadStreamResponse(meta=ReadStreamMeta(error=error_msg))
                return

            images = result.images or {}
            content_len = len(result.content)
            sent = 0
            try:
                yield ReadStreamResponse(
                    meta=ReadStreamMeta(
                        markdown_content=_c(result.content),
                        image_dir_path="",
                        metadata={k: _c(str(v)) for k, v in result.metadata.items()}
                        if result.metadata
                        else {},
                        image_count=parsed_stream.image_count,
                        source_blocks=source_blocks_to_proto(result),
                    )
                )

                for ref in _iter_image_refs(images):
                    check_cancellation(context)
                    yield ReadStreamResponse(image=ref)
                    sent += 1

                for image in parsed_stream.images:
                    check_cancellation(context)
                    yield ReadStreamResponse(
                        image=_image_ref_from_streamed_data(image.ref_path, image.image_data)
                    )
                    sent += 1
            except Exception:
                logger.exception("Error streaming images for %s", source_desc)
                raise
            finally:
                parsed_stream.close()

            logger.info(
                "ReadStream response: content_len=%d, images=%d",
                content_len,
                sent,
            )

    def ListEngines(self, request: ListEnginesRequest, context: Any) -> ListEnginesResponse:
        del context
        raw_overrides = dict(getattr(request, "config_overrides", None) or {})
        overrides = sanitize_engine_overrides(raw_overrides, reject_forbidden=False)
        engines_data = registry.list_engines(overrides=overrides or None)
        engines = [
            ParserEngineInfo(
                name=e["name"],
                description=e["description"],
                file_types=e["file_types"],
                available=e.get("available", True),
                unavailable_reason=e.get("unavailable_reason", ""),
            )
            for e in engines_data
        ]
        return ListEnginesResponse(engines=engines)


def main() -> None:
    config.print_config()

    try:
        interceptors: list[grpc.ServerInterceptor] = [AuthInterceptor()]
    except AuthConfigError as e:
        logger.error("Refusing to start: %s", e)
        sys.exit(1)

    executor = RequestThreadPoolExecutor(max_workers=CONFIG.grpc_max_workers)
    server = grpc.server(
        executor,
        options=[
            ("grpc.max_send_message_length", CONFIG.grpc_max_message_size_bytes),
            ("grpc.max_receive_message_length", CONFIG.grpc_max_message_size_bytes),
        ],
        interceptors=interceptors,
    )

    servicer = DocReaderServicer()
    docreader_pb2_grpc.add_DocReaderServicer_to_server(servicer, server)

    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    try:
        tls_credentials = load_tls_credentials()
    except TLSConfigError as e:
        servicer.close()
        executor.shutdown(wait=True)
        logger.error("Refusing to start: %s", e)
        sys.exit(1)

    if tls_credentials:
        server.add_secure_port(f"[::]:{CONFIG.grpc_port}", tls_credentials)
        logger.info("Server starting on port %d with TLS", CONFIG.grpc_port)
    else:
        server.add_insecure_port(f"[::]:{CONFIG.grpc_port}")
        logger.warning("Server starting on port %d WITHOUT TLS (insecure mode)", CONFIG.grpc_port)

    server.start()

    logger.info("Server started on port %d", CONFIG.grpc_port)
    logger.info("Server is ready to accept connections")

    grace_seconds = float(getattr(CONFIG, "grpc_shutdown_grace_seconds", 10))
    stop_requested = threading.Event()
    shutdown_started = threading.Event()
    watcher_exiting = threading.Event()

    def _begin_graceful_drain(reason: str, *, wait: bool = False) -> None:
        if not shutdown_started.is_set():
            shutdown_started.set()
            logger.info(
                "%s; entering graceful gRPC shutdown (grace=%ss)",
                reason,
                grace_seconds,
            )
            health_servicer.enter_graceful_shutdown()
        stop_event = server.stop(grace_seconds)
        if wait:
            stop_event.wait()

    def _shutdown_watcher() -> None:
        stop_requested.wait()
        if watcher_exiting.is_set():
            return
        _begin_graceful_drain("Received SIGTERM")

    watcher = threading.Thread(
        target=_shutdown_watcher,
        name="docreader-sigterm-drain",
        daemon=True,
    )
    watcher.start()

    signal.signal(signal.SIGTERM, lambda _signum, _frame: stop_requested.set())
    try:
        server.wait_for_termination()
    except KeyboardInterrupt:
        logger.info("Received termination signal, shutting down server")
    finally:
        watcher_exiting.set()
        stop_requested.set()
        _begin_graceful_drain("Stopping gRPC server", wait=True)
        watcher.join(timeout=1.0)
        servicer.close()
        executor.shutdown(wait=True)


if __name__ == "__main__":
    main()
