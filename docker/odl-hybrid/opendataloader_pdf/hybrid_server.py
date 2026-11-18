#!/usr/bin/env python3
"""Hardened OpenDataLoader PDF Hybrid Server wrapper.

Security controls over upstream opendataloader_pdf.hybrid_server:
1. Safe loopback default bind host (127.0.0.1) and fail-closed startup/request
   authentication via ODL_HYBRID_AUTH_TOKEN (constant-time hmac.compare_digest).
   Non-loopback binds refuse to start without ODL_HYBRID_AUTH_TOKEN unless
   ODL_HYBRID_ALLOW_UNAUTHENTICATED=true is explicitly set for isolated dev.
2. Hard upload payload limit (default 100 MB, configurable via
   ODL_HYBRID_MAX_FILE_SIZE_MB or --max-file-size, enforced both on
   Content-Length and incremental ASGI body chunks, returning HTTP 413).
3. Dangerous benchmark/model-pull endpoint /v1/profile/file removed and blocked
   at the ASGI entry layer by default (HTTP 404).
"""

from __future__ import annotations

import argparse
import contextlib
import hmac
import importlib.util
import ipaddress
import json
import logging
import os
import sys
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import Any

logger = logging.getLogger(__name__)

DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 5002
DEFAULT_MAX_FILE_SIZE_MB = 100
DEFAULT_MAX_FILE_SIZE_BYTES = DEFAULT_MAX_FILE_SIZE_MB * 1024 * 1024
MAX_FILE_SIZE = DEFAULT_MAX_FILE_SIZE_BYTES

ASGIReceive = Callable[[], Awaitable[dict[str, Any]]]
ASGISend = Callable[[dict[str, Any]], Awaitable[None]]


def _env_bool(name: str, default: bool = False) -> bool:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def _env_positive_int(name: str, default: int) -> int:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return default
    try:
        parsed = int(raw.strip())
        return parsed if parsed > 0 else default
    except ValueError:
        return default


def _env_port(name: str, default: int = DEFAULT_PORT) -> int:
    raw = os.getenv(name)
    if raw is None or not raw.strip():
        return default
    try:
        parsed = int(raw.strip())
        return parsed if 1 <= parsed <= 65535 else default
    except ValueError:
        return default


def is_loopback_bind_host(host: str) -> bool:
    """Return True if host strictly binds to local loopback."""
    cleaned = (host or "").strip().strip("[]").lower()
    if cleaned in {"127.0.0.1", "::1", "localhost"}:
        return True
    try:
        return ipaddress.ip_address(cleaned).is_loopback
    except ValueError:
        return False


def resolve_max_file_size_bytes(max_file_size_bytes: int | None = None) -> int:
    """Resolve positive hard upload cap in bytes (default 100 MiB)."""
    default_mb = _env_positive_int("ODL_HYBRID_MAX_FILE_SIZE_MB", DEFAULT_MAX_FILE_SIZE_MB)
    default_bytes = default_mb * 1024 * 1024
    if max_file_size_bytes is None or max_file_size_bytes <= 0:
        return default_bytes
    return int(max_file_size_bytes)


def validate_startup_security(
    host: str,
    auth_token: str | None = None,
    allow_unauthenticated: bool | None = None,
) -> None:
    """Refuse unauthenticated startup on non-loopback interfaces."""
    effective_token = (
        auth_token if auth_token is not None else os.getenv("ODL_HYBRID_AUTH_TOKEN", "")
    ).strip()
    effective_allow_unauth = (
        _env_bool("ODL_HYBRID_ALLOW_UNAUTHENTICATED", False)
        if allow_unauthenticated is None
        else bool(allow_unauthenticated)
    )
    if effective_token:
        if len(effective_token.encode("utf-8")) < 16:
            logger.warning(
                "ODL_HYBRID_AUTH_TOKEN is shorter than 16 bytes; use a high-entropy secret"
            )
        return

    if is_loopback_bind_host(host) or effective_allow_unauth:
        if not effective_token:
            logger.warning("ODL hybrid running without ODL_HYBRID_AUTH_TOKEN on host=%s", host)
        return

    raise RuntimeError(
        f"Refusing to start ODL hybrid server on non-loopback host '{host}' without "
        "ODL_HYBRID_AUTH_TOKEN (bind to 127.0.0.1 or set ODL_HYBRID_AUTH_TOKEN)"
    )


@dataclass(frozen=True)
class HybridSecurityConfig:
    auth_token: bytes | None
    bind_host: str = DEFAULT_HOST
    allow_unauthenticated: bool = False
    max_file_size_bytes: int = DEFAULT_MAX_FILE_SIZE_BYTES
    enable_profile_endpoint: bool = False

    @classmethod
    def from_env(
        cls,
        *,
        auth_token: str | None = None,
        bind_host: str | None = None,
        allow_unauthenticated: bool | None = None,
        max_file_size_bytes: int | None = None,
        enable_profile_endpoint: bool | None = None,
    ) -> HybridSecurityConfig:
        raw_token = (
            os.getenv("ODL_HYBRID_AUTH_TOKEN", "") if auth_token is None else auth_token
        ).strip()
        effective_host = (
            os.getenv("ODL_HYBRID_HOST", DEFAULT_HOST) if bind_host is None else bind_host
        ).strip() or DEFAULT_HOST
        effective_allow = (
            _env_bool("ODL_HYBRID_ALLOW_UNAUTHENTICATED", False)
            if allow_unauthenticated is None
            else bool(allow_unauthenticated)
        )
        effective_profile = (
            _env_bool("ODL_HYBRID_ENABLE_PROFILE_ENDPOINT", False)
            if enable_profile_endpoint is None
            else bool(enable_profile_endpoint)
        )
        return cls(
            auth_token=raw_token.encode("utf-8") if raw_token else None,
            bind_host=effective_host,
            allow_unauthenticated=effective_allow,
            max_file_size_bytes=resolve_max_file_size_bytes(max_file_size_bytes),
            enable_profile_endpoint=effective_profile,
        )


class _PayloadTooLargeError(Exception):
    """Raised when streamed request body exceeds max_file_size_bytes."""


async def _send_json(
    send: ASGISend,
    status_code: int,
    payload: dict[str, Any],
    extra_headers: list[tuple[bytes, bytes]] | None = None,
) -> None:
    body = json.dumps(payload).encode("utf-8")
    headers = [
        (b"content-type", b"application/json"),
        (b"content-length", str(len(body)).encode("ascii")),
    ]
    if extra_headers:
        headers.extend(extra_headers)
    await send(
        {
            "type": "http.response.start",
            "status": status_code,
            "headers": headers,
        }
    )
    await send({"type": "http.response.body", "body": body})


def _extract_request_token(headers: list[tuple[bytes, bytes]]) -> bytes:
    auth_val = ""
    api_key_val = ""
    for raw_k, raw_v in headers:
        k = raw_k.decode("latin-1").lower()
        if k == "authorization":
            auth_val = raw_v.decode("latin-1").strip()
        elif k == "x-api-key":
            api_key_val = raw_v.decode("latin-1").strip()
    if auth_val:
        if auth_val.lower().startswith("bearer "):
            return auth_val[7:].strip().encode("utf-8")
        return auth_val.encode("utf-8")
    if api_key_val:
        return api_key_val.encode("utf-8")
    return b""


class HybridSecurityASGIWrapper:
    """Pure-ASGI security gate enforcing auth, body size limits, and route lockdown."""

    def __init__(self, app: Callable[..., Any], config: HybridSecurityConfig) -> None:
        self.app = app
        self.config = config

    def _is_authenticated(self, headers: list[tuple[bytes, bytes]]) -> bool:
        if self.config.auth_token is not None:
            candidate = _extract_request_token(headers)
            if not candidate:
                return False
            return hmac.compare_digest(candidate, self.config.auth_token)
        return is_loopback_bind_host(self.config.bind_host) or self.config.allow_unauthenticated

    async def __call__(
        self,
        scope: dict[str, Any],
        receive: ASGIReceive,
        send: ASGISend,
    ) -> None:
        if scope.get("type") != "http":
            await self.app(scope, receive, send)
            return

        path = (scope.get("path") or "").rstrip("/") or "/"
        method = (scope.get("method") or "GET").upper()
        headers: list[tuple[bytes, bytes]] = list(scope.get("headers") or [])

        # 1. Disable dangerous /v1/profile/file endpoint by default
        if not self.config.enable_profile_endpoint and path == "/v1/profile/file":
            await _send_json(
                send,
                404,
                {"status": "failure", "errors": ["Endpoint /v1/profile/file is disabled"]},
            )
            return

        # 2. Allow unauthenticated liveness probes on /health only
        is_health = path == "/health" and method in {"GET", "HEAD"}
        if not is_health and not self._is_authenticated(headers):
            await _send_json(
                send,
                401,
                {
                    "status": "failure",
                    "errors": ["Unauthorized: invalid or missing authentication token"],
                },
                extra_headers=[(b"www-authenticate", b"Bearer")],
            )
            return

        # 3. Enforce hard upload size ceiling via Content-Length + streaming counter
        max_bytes = self.config.max_file_size_bytes
        if max_bytes > 0:
            for raw_k, raw_v in headers:
                if raw_k.decode("latin-1").lower() == "content-length":
                    try:
                        declared_len = int(raw_v.decode("latin-1").strip())
                    except ValueError:
                        await _send_json(
                            send,
                            400,
                            {"status": "failure", "errors": ["Invalid Content-Length header"]},
                        )
                        return
                    if declared_len < 0:
                        await _send_json(
                            send,
                            400,
                            {"status": "failure", "errors": ["Invalid Content-Length header"]},
                        )
                        return
                    if declared_len > max_bytes:
                        await _send_json(
                            send,
                            413,
                            {
                                "status": "failure",
                                "errors": [
                                    f"File size exceeds maximum allowed "
                                    f"({max_bytes // (1024 * 1024)}MB)"
                                ],
                            },
                        )
                        return

        received_bytes = 0
        response_started = False
        payload_too_large = False

        async def guarded_receive() -> dict[str, Any]:
            nonlocal received_bytes, payload_too_large
            message: dict[str, Any] = await receive()
            if message.get("type") == "http.request":
                chunk = message.get("body", b"") or b""
                received_bytes += len(chunk)
                if max_bytes > 0 and received_bytes > max_bytes:
                    payload_too_large = True
                    raise _PayloadTooLargeError()
            return message

        async def tracked_send(message: dict[str, Any]) -> None:
            nonlocal response_started
            if payload_too_large and not response_started:
                # Suppress inner ServerErrorMiddleware / ExceptionMiddleware 500
                # responses triggered when guarded_receive rejects oversized bodies.
                return
            if message.get("type") == "http.response.start":
                response_started = True
            await send(message)

        with contextlib.suppress(_PayloadTooLargeError):
            await self.app(scope, guarded_receive, tracked_send)

        if payload_too_large and not response_started:
            await _send_json(
                send,
                413,
                {
                    "status": "failure",
                    "errors": [
                        f"File size exceeds maximum allowed ({max_bytes // (1024 * 1024)}MB)"
                    ],
                },
            )


def _load_upstream_hybrid_module() -> Any:
    """Load the site-packages opendataloader_pdf/hybrid_server.py implementation."""
    this_file = os.path.realpath(__file__)
    import opendataloader_pdf

    for pkg_dir in getattr(opendataloader_pdf, "__path__", []):
        candidate = os.path.realpath(os.path.join(pkg_dir, "hybrid_server.py"))
        if candidate != this_file and os.path.isfile(candidate):
            spec = importlib.util.spec_from_file_location(
                "_opendataloader_pdf_upstream_hybrid_server", candidate
            )
            if spec and spec.loader:
                module = importlib.util.module_from_spec(spec)
                spec.loader.exec_module(module)
                return module
    raise ImportError("Upstream opendataloader_pdf.hybrid_server module not found")


def _strip_profile_route(app: Any) -> None:
    """Remove /v1/profile/file from FastAPI/Starlette route table if present."""
    router = getattr(app, "router", None)
    if router is None:
        return
    routes = getattr(router, "routes", None)
    if isinstance(routes, list):
        routes[:] = [r for r in routes if getattr(r, "path", None) != "/v1/profile/file"]


def create_app(
    force_ocr: bool = False,
    disable_ocr: bool = False,
    ocr_engine: str = "easyocr",
    psm: int | None = None,
    ocr_lang: list[str] | None = None,
    enrich_formula: bool = False,
    enrich_picture_description: bool = False,
    picture_description_prompt: str | None = None,
    max_file_size: int | None = None,
    device: str = "auto",
    *,
    auth_token: str | None = None,
    bind_host: str | None = None,
    allow_unauthenticated: bool | None = None,
    enable_profile_endpoint: bool | None = None,
    _inner_app: Callable[..., Any] | None = None,
) -> HybridSecurityASGIWrapper:
    """Create hardened hybrid server ASGI application."""
    security_cfg = HybridSecurityConfig.from_env(
        auth_token=auth_token,
        bind_host=bind_host,
        allow_unauthenticated=allow_unauthenticated,
        max_file_size_bytes=max_file_size,
        enable_profile_endpoint=enable_profile_endpoint,
    )

    if _inner_app is None:
        upstream = _load_upstream_hybrid_module()
        inner = upstream.create_app(
            force_ocr=force_ocr,
            disable_ocr=disable_ocr,
            ocr_engine=ocr_engine,
            psm=psm,
            ocr_lang=ocr_lang,
            enrich_formula=enrich_formula,
            enrich_picture_description=enrich_picture_description,
            picture_description_prompt=picture_description_prompt,
            max_file_size=security_cfg.max_file_size_bytes,
            device=device,
        )
    else:
        inner = _inner_app

    if not security_cfg.enable_profile_endpoint:
        _strip_profile_route(inner)

    return HybridSecurityASGIWrapper(inner, security_cfg)


def _positive_mb_arg(value: str) -> int:
    try:
        parsed = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError(f"invalid int value: {value!r}") from exc
    if parsed <= 0:
        raise argparse.ArgumentTypeError("--max-file-size must be > 0 (MB)")
    return parsed


def _port_arg(value: str) -> int:
    try:
        parsed = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError(f"invalid int value: {value!r}") from exc
    if not (1 <= parsed <= 65535):
        raise argparse.ArgumentTypeError("--port must be between 1 and 65535")
    return parsed


def main(argv: list[str] | None = None) -> None:
    """Run the hardened ODL hybrid server."""
    parser = argparse.ArgumentParser(description="Hardened Docling Hybrid Server")
    parser.add_argument(
        "--host",
        default=os.getenv("ODL_HYBRID_HOST", DEFAULT_HOST),
        help=f"Host to bind to (default: {DEFAULT_HOST})",
    )
    parser.add_argument(
        "--port",
        type=_port_arg,
        default=_env_port("ODL_HYBRID_PORT", DEFAULT_PORT),
        help=f"Port to bind to (default: {DEFAULT_PORT})",
    )
    parser.add_argument(
        "--log-level",
        default="info",
        choices=["debug", "info", "warning", "error"],
    )
    ocr_group = parser.add_mutually_exclusive_group()
    ocr_group.add_argument("--force-ocr", action="store_true")
    ocr_group.add_argument("--no-ocr", action="store_true")
    parser.add_argument("--ocr-engine", type=str, default="easyocr")
    parser.add_argument("--psm", type=int, default=None)
    parser.add_argument("--ocr-lang", type=str, default=None)
    parser.add_argument("--enrich-formula", action="store_true", default=False)
    parser.add_argument("--no-enrich-formula", action="store_false", dest="enrich_formula")
    parser.add_argument("--enrich-picture-description", action="store_true", default=False)
    parser.add_argument(
        "--no-enrich-picture-description",
        action="store_false",
        dest="enrich_picture_description",
    )
    parser.add_argument("--picture-description-prompt", type=str, default=None)
    parser.add_argument(
        "--max-file-size",
        type=_positive_mb_arg,
        default=_env_positive_int("ODL_HYBRID_MAX_FILE_SIZE_MB", DEFAULT_MAX_FILE_SIZE_MB),
        help=f"Maximum upload file size in MB (default: {DEFAULT_MAX_FILE_SIZE_MB})",
    )
    parser.add_argument(
        "--device",
        type=str,
        default="auto",
        choices=["auto", "cpu", "cuda", "mps", "xpu"],
    )
    args = parser.parse_args(argv)

    try:
        validate_startup_security(args.host)
    except RuntimeError as exc:
        logger.error("%s", exc)
        sys.exit(1)

    upstream = _load_upstream_hybrid_module()
    upstream._check_dependencies()
    if not args.no_ocr:
        ok, err = upstream._check_ocr_engine_available(args.ocr_engine)
        if not ok:
            logger.error(err)
            sys.exit(2)

    ocr_lang = (
        [lang.strip() for lang in args.ocr_lang.split(",") if lang.strip()]
        if args.ocr_lang
        else None
    )
    max_file_size_bytes = args.max_file_size * 1024 * 1024

    app = create_app(
        force_ocr=args.force_ocr,
        disable_ocr=args.no_ocr,
        ocr_engine=args.ocr_engine,
        psm=args.psm,
        ocr_lang=ocr_lang,
        enrich_formula=args.enrich_formula,
        enrich_picture_description=args.enrich_picture_description,
        picture_description_prompt=args.picture_description_prompt,
        max_file_size=max_file_size_bytes,
        device=args.device,
        bind_host=args.host,
    )

    import uvicorn

    uvicorn.run(
        app,
        host=args.host,
        port=args.port,
        log_level=args.log_level,
        loop=upstream._get_loop_setting(),
    )


if __name__ == "__main__":
    main()
