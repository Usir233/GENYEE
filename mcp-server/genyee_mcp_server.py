#!/usr/bin/env python3
"""
GENYEE MCP Server

A Model Context Protocol server that provides access to the GENYEE knowledge management API.
"""

from __future__ import annotations

import argparse
import asyncio
import functools
import io
import json
import logging
import math
import os
import re
import secrets
import sys
import threading
import urllib.parse
import weakref
from collections.abc import Awaitable, Callable, Iterator, MutableMapping, Sequence
from typing import TYPE_CHECKING, Any, BinaryIO, Protocol, cast

import requests
import urllib3
from mcp.server import MCPServer
from requests.exceptions import HTTPError, RequestException

from upload_paths import (
    open_upload_file_nofollow,
    resolve_upload_file_path,
    set_active_transport,
)

if TYPE_CHECKING:
    from mcp.server.transport_security import TransportSecuritySettings

# Set up logging configuration for the MCP server
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def _positive_int_env(name: str, default: int) -> int:
    raw = os.getenv(name, str(default))
    try:
        parsed = int(raw)
    except ValueError:
        logger.warning("%s is not a valid integer; falling back to %ds.", name, default)
        return default
    if parsed <= 0:
        logger.warning("%s must be a positive integer; falling back to %ds.", name, default)
        return default
    return parsed


def _normalize_transport(value: str) -> str:
    cleaned = value.strip().lower()
    if cleaned == "streamable-http":
        return "http"
    return cleaned


def _default_mcp_transport(default: str = "stdio") -> str:
    raw = os.getenv("MCP_TRANSPORT")
    if raw is None or not raw.strip():
        return default
    return _normalize_transport(raw)


def _default_mcp_port(default: int = 8000) -> int:
    raw = os.getenv("MCP_PORT")
    if raw is None or not raw.strip():
        return default
    try:
        port = int(raw.strip())
    except ValueError:
        logger.warning("MCP_PORT is not a valid integer; falling back to %d.", default)
        return default
    if not (1 <= port <= 65535):
        logger.warning("MCP_PORT=%d is out of range (1-65535); falling back to %d.", port, default)
        return default
    return port


# Configuration - Load from environment variables with defaults
GENYEE_BASE_URL = os.getenv("GENYEE_BASE_URL", "http://localhost:8080/api/v1")
GENYEE_API_KEY = os.getenv("GENYEE_API_KEY", "")
# Chat SSE read timeout in seconds. LLM responses can be slow; default 300s.
GENYEE_CHAT_TIMEOUT = _positive_int_env("GENYEE_CHAT_TIMEOUT", 300)

# Timeout in seconds for the non-chat API requests (CRUD, search, uploads).
# Without it requests waits forever, so a hung backend blocks the calling
# tool indefinitely. Default 300s, matching the chat read timeout.
GENYEE_API_TIMEOUT = _positive_int_env("GENYEE_API_TIMEOUT", 300)

# Maximum graceful drain window for network uvicorn servers before closing active SSE streams.
MCP_GRACEFUL_SHUTDOWN_TIMEOUT = _positive_int_env("MCP_GRACEFUL_SHUTDOWN_TIMEOUT", 10)

# Bound per-frame SSE buffering, cumulative SSE stream size, and JSON API responses.
MAX_SSE_EVENT_BYTES = 8 * 1024 * 1024
MAX_SSE_STREAM_BYTES = 32 * 1024 * 1024
MAX_API_RESPONSE_BYTES = 32 * 1024 * 1024
UPLOAD_STREAM_CHUNK_BYTES = 65536

# Network transport defaults kept for backward compatibility with pre-2.x deployments.
SSE_MESSAGE_PATH = "/sse/messages/"
STREAMABLE_HTTP_STATELESS = True

MIN_NETWORK_AUTH_TOKEN_LENGTH = 32


def _env_flag(name: str, default: bool = False) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}


def _safe_path_segment(value: str, name: str = "id") -> str:
    """Validate and percent-encode a single REST URL path segment (GENYEE-HIGH-18)."""
    if not isinstance(value, str):
        raise ValueError(f"Invalid {name}: expected non-empty string")
    if not value or not value.strip() or value != value.strip():
        raise ValueError(
            f"Invalid {name}: must not be empty or contain leading/trailing whitespace"
        )
    if len(value) > 256:
        raise ValueError(f"Invalid {name}: exceeds maximum length (256)")
    if value in (".", "..") or ".." in value:
        raise ValueError(f"Invalid {name}: path traversal sequence is forbidden")
    for ch in value:
        code = ord(ch)
        if code < 32 or code == 127 or ch in ("/", "\\", "?", "#", "%"):
            raise ValueError(f"Invalid {name}: contains disallowed URL character")
    return urllib.parse.quote(value, safe="")


def _safe_wiki_slug(value: str, name: str = "slug") -> str:
    """Validate and percent-encode a hierarchical wiki page slug (e.g. 'entity/acme-corp').

    GENYEE backend mounts GET /knowledgebase/:kb_id/wiki/pages/*slug, so multi-segment
    slugs separated by '/' are valid while empty segments, dot-segments ('.', '..'),
    backslashes, query/fragment delimiters, and pre-encoded '%' sequences are forbidden.
    """
    if not isinstance(value, str):
        raise ValueError(f"Invalid {name}: expected non-empty string")
    if not value or not value.strip() or value != value.strip():
        raise ValueError(
            f"Invalid {name}: must not be empty or contain leading/trailing whitespace"
        )
    if len(value) > 512:
        raise ValueError(f"Invalid {name}: exceeds maximum length (512)")
    if value.startswith("/") or value.endswith("/"):
        raise ValueError(f"Invalid {name}: must not start or end with '/'")
    for ch in value:
        code = ord(ch)
        if code < 32 or code == 127 or ch in ("\\", "?", "#", "%"):
            raise ValueError(f"Invalid {name}: contains disallowed URL character")
    parts = value.split("/")
    encoded_parts: list[str] = []
    for part in parts:
        if not part or part != part.strip():
            raise ValueError(f"Invalid {name}: empty or whitespace-padded slug segment")
        if part in (".", "..") or ".." in part:
            raise ValueError(f"Invalid {name}: path traversal sequence is forbidden")
        encoded_parts.append(urllib.parse.quote(part, safe=""))
    return "/".join(encoded_parts)


def _validate_int_range(value: int, name: str, minimum: int, maximum: int | None = None) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        raise ValueError(f"Invalid {name}: expected integer")
    if value < minimum or (maximum is not None and value > maximum):
        bound_desc = f"between {minimum} and {maximum}" if maximum is not None else f">= {minimum}"
        raise ValueError(f"Invalid {name}: must be {bound_desc}")
    return value


def _validate_unit_float(value: float, name: str) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"Invalid {name}: expected number between 0.0 and 1.0")
    numeric = float(value)
    if not math.isfinite(numeric) or not (0.0 <= numeric <= 1.0):
        raise ValueError(f"Invalid {name}: must be finite and between 0.0 and 1.0")
    return numeric


def _validate_http_url(url: str) -> str:
    if not isinstance(url, str) or not url.strip():
        raise ValueError("Invalid url: expected non-empty HTTP(S) URL")
    cleaned = url.strip()
    if any(ord(ch) < 32 or ord(ch) == 127 for ch in cleaned):
        raise ValueError("Invalid url: contains control characters")
    parsed = urllib.parse.urlsplit(cleaned)
    if parsed.scheme.lower() not in ("http", "https") or not parsed.netloc:
        raise ValueError("Invalid url: scheme must be http or https")
    return cleaned


def _validate_knowledge_status(status: str) -> str:
    if not isinstance(status, str) or status not in ("publish", "draft"):
        raise ValueError("Invalid status: must be 'publish' or 'draft'")
    return status


def admin_tools_enabled() -> bool:
    """Whether high-privilege tenant/model management MCP tools are enabled (MED-12)."""
    return _env_flag("MCP_ENABLE_ADMIN_TOOLS", default=False)


def _require_admin_tools_enabled(tool_name: str) -> None:
    if not admin_tools_enabled():
        raise PermissionError(
            f"Administrative MCP tool '{tool_name}' is disabled by default; "
            "set MCP_ENABLE_ADMIN_TOOLS=true to enable it."
        )


def build_transport_security_settings(host: str, port: int) -> TransportSecuritySettings:
    """Build explicit Host/Origin allowlists for network MCP transports (MED-12).

    Prevents MCP SDK from silently disabling DNS rebinding protection when
    ``host`` is ``0.0.0.0`` or ``::``.
    """
    from mcp.server.transport_security import TransportSecuritySettings

    if _env_flag("MCP_DISABLE_DNS_REBINDING_PROTECTION", default=False):
        logger.warning(
            "DNS rebinding Host/Origin validation is DISABLED via "
            "MCP_DISABLE_DNS_REBINDING_PROTECTION=true."
        )
        return TransportSecuritySettings(enable_dns_rebinding_protection=False)

    allowed_hosts: list[str] = [
        "127.0.0.1:*",
        "localhost:*",
        "[::1]:*",
        "127.0.0.1",
        "localhost",
        "[::1]",
    ]
    allowed_origins: list[str] = [
        "http://127.0.0.1:*",
        "https://127.0.0.1:*",
        "http://localhost:*",
        "https://localhost:*",
        "http://[::1]:*",
        "https://[::1]:*",
        "http://127.0.0.1",
        "https://127.0.0.1",
        "http://localhost",
        "https://localhost",
    ]

    clean_host = (host or "").strip()
    if clean_host and clean_host not in {"0.0.0.0", "::", "*"}:
        host_bracketed = (
            f"[{clean_host}]"
            if ":" in clean_host and not clean_host.startswith("[")
            else clean_host
        )
        for candidate in (
            f"{host_bracketed}:{port}",
            f"{host_bracketed}:*",
            host_bracketed,
        ):
            if candidate not in allowed_hosts:
                allowed_hosts.append(candidate)
        for scheme in ("http", "https"):
            for candidate in (
                f"{scheme}://{host_bracketed}:{port}",
                f"{scheme}://{host_bracketed}",
            ):
                if candidate not in allowed_origins:
                    allowed_origins.append(candidate)

    extra_hosts = os.getenv("MCP_ALLOWED_HOSTS", "").strip()
    if extra_hosts:
        for raw_entry in extra_hosts.split(","):
            entry = raw_entry.strip()
            if not entry:
                continue
            if entry not in allowed_hosts:
                allowed_hosts.append(entry)
            if ":" not in entry and f"{entry}:*" not in allowed_hosts:
                allowed_hosts.append(f"{entry}:*")

    extra_origins = os.getenv("MCP_ALLOWED_ORIGINS", "").strip()
    if extra_origins:
        for raw_origin in extra_origins.split(","):
            origin = raw_origin.strip()
            if origin and origin not in allowed_origins:
                allowed_origins.append(origin)

    return TransportSecuritySettings(
        enable_dns_rebinding_protection=True,
        allowed_hosts=allowed_hosts,
        allowed_origins=allowed_origins,
    )


def _uvicorn_tls_paths() -> tuple[str | None, str | None]:
    certfile = os.getenv("MCP_SSL_CERTFILE", "").strip()
    keyfile = os.getenv("MCP_SSL_KEYFILE", "").strip()
    if bool(certfile) != bool(keyfile):
        raise ValueError(
            "Both MCP_SSL_CERTFILE and MCP_SSL_KEYFILE must be set together to enable TLS."
        )
    if certfile and keyfile:
        return certfile, keyfile
    return None, None


def _uvicorn_tls_kwargs() -> dict[str, str]:
    certfile, keyfile = _uvicorn_tls_paths()
    if certfile and keyfile:
        return {"ssl_certfile": certfile, "ssl_keyfile": keyfile}
    return {}


def network_transport_auth_token() -> str:
    """Shared secret clients must present for SSE/HTTP transports."""
    return os.getenv("MCP_SERVER_AUTH_TOKEN", "").strip()


def require_network_transport_auth(transport: str) -> str:
    """SSE/HTTP must not start without a strong (>=32-char) configured auth token."""
    token = network_transport_auth_token()
    if transport in ("sse", "http", "streamable-http"):
        if not token:
            logger.error(
                "MCP_SERVER_AUTH_TOKEN is required for %s transport. "
                "Set a strong shared secret (>= %d chars); clients must send "
                "Authorization: Bearer <token> or X-MCP-Auth-Token.",
                transport,
                MIN_NETWORK_AUTH_TOKEN_LENGTH,
            )
            sys.exit(1)
        if len(token) < MIN_NETWORK_AUTH_TOKEN_LENGTH:
            logger.error(
                "MCP_SERVER_AUTH_TOKEN is too short (%d chars); at least %d "
                "characters are required for %s transport.",
                len(token),
                MIN_NETWORK_AUTH_TOKEN_LENGTH,
                transport,
            )
            sys.exit(1)
    return token


ASGIScope = MutableMapping[str, Any]
ASGIMessage = MutableMapping[str, Any]
ASGIReceive = Callable[[], Awaitable[ASGIMessage]]
ASGISend = Callable[[ASGIMessage], Awaitable[None]]
ASGIApp = Callable[[ASGIScope, ASGIReceive, ASGISend], Awaitable[None]]


class MCPAuthMiddleware:
    """ASGI middleware that gates network MCP transports behind a shared secret."""

    def __init__(self, app: ASGIApp, token: str) -> None:
        self.app = app
        self.token = token

    async def __call__(
        self,
        scope: ASGIScope,
        receive: ASGIReceive,
        send: ASGISend,
    ) -> None:
        scope_type = scope.get("type")
        if scope_type == "lifespan":
            await self.app(scope, receive, send)
            return
        if scope_type not in ("http", "websocket"):
            return

        headers = {
            k.decode("latin-1").lower(): v.decode("latin-1") for k, v in scope.get("headers", [])
        }
        provided = ""
        auth = headers.get("authorization", "")
        if auth.lower().startswith("bearer "):
            provided = auth[7:].strip()
        elif "x-mcp-auth-token" in headers:
            provided = headers["x-mcp-auth-token"]

        if not provided or not secrets.compare_digest(
            provided.encode("utf-8"), self.token.encode("utf-8")
        ):
            if scope_type == "websocket":
                await send({"type": "websocket.close", "code": 1008})
                return
            body = b'{"error":"unauthorized"}'
            await send(
                {
                    "type": "http.response.start",
                    "status": 401,
                    "headers": [[b"content-type", b"application/json"]],
                }
            )
            await send({"type": "http.response.body", "body": body})
            return

        await self.app(scope, receive, send)


def _normalize_kb_entries(resp: object) -> list[dict[str, Any]]:
    """Flatten owned and shared knowledge-base list API responses.

    GET /knowledge-bases returns ``data: [{id, name, ...}, ...]`` (see
    KnowledgeBaseHandler.buildKBListResponse).

    GET /shared-knowledge-bases returns ``data: [{knowledge_base: {id, name,
    ...}, share_id, ...}, ...]`` (see organization handler sharedKBRow).
    """
    data: Any = resp.get("data", resp) if isinstance(resp, dict) else resp
    if isinstance(data, dict):
        data = data.get("list", data.get("items", []))
    out: list[dict[str, Any]] = []
    if not isinstance(data, list):
        return out
    for item in data:
        if not isinstance(item, dict):
            continue
        nested = item.get("knowledge_base")
        if isinstance(nested, dict) and nested.get("id"):
            out.append(nested)
        elif item.get("id"):
            out.append(item)
    return out


class _StreamingBytesResponse(Protocol):
    def iter_content(self, chunk_size: int = ...) -> Iterator[bytes]: ...


def _iter_sse_lines(response: _StreamingBytesResponse) -> Iterator[bytes]:
    """Split SSE lines without treating a CRLF split across chunks as two breaks.

    Raw bytes are accumulated up to CR/LF delimiters before UTF-8 decoding so
    multi-byte UTF-8 code points split across arbitrary TCP chunks stay intact.
    """
    pending = bytearray()
    skip_lf = False
    # Allow small SSE field-prefix/BOM framing ("data: " + UTF-8 BOM) on top of
    # MAX_SSE_EVENT_BYTES so exact-boundary event payloads remain valid.
    max_line_bytes = MAX_SSE_EVENT_BYTES + 16
    for raw_chunk in response.iter_content(chunk_size=512):
        if not raw_chunk:
            continue
        chunk = bytes(raw_chunk)
        if skip_lf and chunk.startswith(b"\n"):
            chunk = chunk[1:]
        # Emit bare CR immediately. If the next chunk starts with LF, it
        # belongs to this same line ending.
        skip_lf = chunk.endswith(b"\r")
        for part in chunk.splitlines(keepends=True):
            if part.endswith((b"\r", b"\n")):
                body_part = part.rstrip(b"\r\n")
                if len(pending) + len(body_part) > max_line_bytes:
                    raise RequestException(f"SSE line exceeds {MAX_SSE_EVENT_BYTES} bytes")
                pending.extend(body_part)
                yield bytes(pending)
                pending.clear()
            else:
                if len(pending) + len(part) > max_line_bytes:
                    raise RequestException(f"SSE line exceeds {MAX_SSE_EVENT_BYTES} bytes")
                pending.extend(part)
    if pending:
        yield bytes(pending)


def _canonical_http_origin(raw_url: str) -> tuple[str, str, int | None]:
    parsed = urllib.parse.urlsplit(raw_url or "")
    scheme = parsed.scheme.lower()
    host = (parsed.hostname or "").lower()
    port = parsed.port
    if port is None:
        if scheme == "http":
            port = 80
        elif scheme == "https":
            port = 443
    return scheme, host, port


class _SameOriginSession(requests.Session):
    """Refuses cross-origin or scheme-downgrade redirects to protect X-API-Key."""

    def rebuild_auth(
        self,
        prepared_request: requests.PreparedRequest,
        response: requests.Response,
    ) -> None:
        super().rebuild_auth(prepared_request, response)
        target = urllib.parse.urlsplit(prepared_request.url or "")
        if target.username is not None or target.password is not None:
            response.close()
            raise RequestException(
                "Refusing to follow redirect containing embedded URL credentials"
            )
        src_origin = _canonical_http_origin(
            response.request.url if response.request and response.request.url else ""
        )
        dst_origin = _canonical_http_origin(prepared_request.url or "")
        if src_origin != dst_origin or dst_origin[0] not in ("http", "https"):
            response.close()
            raise RequestException(
                "Refusing to follow cross-origin or scheme-changing redirect with API credentials"
            )


class _MultipartFileStream:
    """Streams a multipart/form-data upload from a file handle in bounded chunks.

    Avoids loading the entire file (and 2x concatenated multipart payload) into
    memory while providing an exact ``__len__`` for ``Content-Length``.
    """

    def __init__(
        self,
        fields: dict[str, str],
        file_field_name: str,
        filename: str,
        file_obj: BinaryIO,
        chunk_size: int = UPLOAD_STREAM_CHUNK_BYTES,
    ) -> None:
        self.fields = dict(fields)
        self.filename = filename
        self._chunk_size = max(1, chunk_size)
        boundary = f"genyee-mcp-{secrets.token_hex(16)}"
        self.boundary = boundary
        self.content_type = f"multipart/form-data; boundary={boundary}"

        preamble = bytearray()
        for key, val in fields.items():
            rf = urllib3.fields.RequestField(name=key, data=val)
            rf.make_multipart()
            preamble.extend(f"--{boundary}\r\n".encode())
            preamble.extend(rf.render_headers().encode("utf-8"))
            preamble.extend(val.encode("utf-8"))
            preamble.extend(b"\r\n")

        file_rf = urllib3.fields.RequestField(name=file_field_name, data=b"", filename=filename)
        file_rf.make_multipart(content_type="application/octet-stream")
        preamble.extend(f"--{boundary}\r\n".encode())
        preamble.extend(file_rf.render_headers().encode("utf-8"))

        self._preamble = memoryview(bytes(preamble))
        self._preamble_pos = 0
        self._epilogue = memoryview(f"\r\n--{boundary}--\r\n".encode())
        self._epilogue_pos = 0

        file_size: int | None = None
        try:
            fd = file_obj.fileno()
            if isinstance(fd, int):
                pos = file_obj.tell()
                offset = pos if isinstance(pos, int) and pos >= 0 else 0
                file_size = max(0, os.fstat(fd).st_size - offset)
        except (AttributeError, OSError, TypeError, ValueError):
            file_size = None

        if file_size is None:
            seekable_fn = getattr(file_obj, "seekable", None)
            if callable(seekable_fn) and seekable_fn() is True:
                cur = file_obj.tell()
                end = file_obj.seek(0, os.SEEK_END)
                file_obj.seek(cur, os.SEEK_SET)
                file_size = max(0, end - cur)

        if file_size is not None:
            self._source: BinaryIO = file_obj
            self._file_remaining = file_size
            self._file_size = file_size
        else:
            # Fallback only for non-seekable test doubles (e.g. unittest.mock.mock_open).
            fallback = bytearray()
            while True:
                piece = file_obj.read(self._chunk_size)
                if not piece:
                    break
                fallback.extend(piece)
            self._source = io.BytesIO(bytes(fallback))
            self._file_remaining = len(fallback)
            self._file_size = len(fallback)

    def __len__(self) -> int:
        return len(self._preamble) + self._file_size + len(self._epilogue)

    def read(self, size: int = -1) -> bytes:
        want_all = size is None or size < 0
        remaining_budget = len(self) if want_all else size
        if remaining_budget == 0:
            return b""

        out = bytearray()
        while remaining_budget > 0:
            if self._preamble_pos < len(self._preamble):
                take = min(remaining_budget, len(self._preamble) - self._preamble_pos)
                out.extend(self._preamble[self._preamble_pos : self._preamble_pos + take])
                self._preamble_pos += take
                remaining_budget -= take
                if not want_all and out:
                    break
                continue

            if self._file_remaining > 0:
                read_len = min(remaining_budget, self._chunk_size, self._file_remaining)
                chunk = self._source.read(read_len)
                if not chunk:
                    raise RequestException(
                        "Upload file ended prematurely before Content-Length was satisfied"
                    )
                out.extend(chunk)
                self._file_remaining -= len(chunk)
                remaining_budget -= len(chunk)
                if not want_all and out:
                    break
                continue

            if self._epilogue_pos < len(self._epilogue):
                take = min(remaining_budget, len(self._epilogue) - self._epilogue_pos)
                out.extend(self._epilogue[self._epilogue_pos : self._epilogue_pos + take])
                self._epilogue_pos += take
                remaining_budget -= take
                if not want_all and out:
                    break
                continue

            break

        return bytes(out)


def _extract_error_detail(raw_body: bytes) -> str:
    if not raw_body or not raw_body.strip():
        return ""
    decoded = raw_body.decode("utf-8", errors="replace").strip()
    try:
        parsed = json.loads(decoded)
    except json.JSONDecodeError:
        return decoded[:200]
    if isinstance(parsed, dict):
        err_field = parsed.get("error")
        if isinstance(err_field, dict):
            msg = err_field.get("message")
            code = err_field.get("code")
            if isinstance(msg, str) and msg.strip():
                return f"{msg.strip()} (code={code})" if code is not None else msg.strip()
        if isinstance(err_field, str) and err_field.strip():
            return err_field.strip()
        for key in ("message", "msg", "detail"):
            val = parsed.get(key)
            if isinstance(val, str) and val.strip():
                return val.strip()
    return decoded[:200]


def _read_bounded_json_response(response: Any) -> dict[str, Any]:
    if not isinstance(response, requests.Response):
        response.raise_for_status()
        mock_status = getattr(response, "status_code", 200)
        if mock_status in (204, 205):
            return {}
        mock_payload: Any = response.json()
        return mock_payload if isinstance(mock_payload, dict) else {"data": mock_payload}
    try:
        content_length = response.headers.get("Content-Length") if response.headers else None
        if content_length is not None:
            try:
                if int(content_length) > MAX_API_RESPONSE_BYTES:
                    raise RequestException(
                        f"API response Content-Length {content_length} "
                        f"exceeds {MAX_API_RESPONSE_BYTES} bytes"
                    )
            except ValueError:
                pass
        buf = bytearray()
        for chunk in response.iter_content(chunk_size=65536):
            if not chunk:
                continue
            if len(buf) + len(chunk) > MAX_API_RESPONSE_BYTES:
                raise RequestException(f"API response exceeds {MAX_API_RESPONSE_BYTES} bytes")
            buf.extend(chunk)
        raw_bytes = bytes(buf)
        response._content = raw_bytes
        response._content_consumed = True

        if 400 <= response.status_code < 600:
            detail = _extract_error_detail(raw_bytes)
            if detail:
                url_desc = response.url or "<unknown>"
                raise HTTPError(
                    f"HTTP {response.status_code} Error for url {url_desc}: {detail}",
                    response=response,
                    request=response.request,
                )
            response.raise_for_status()

        response.raise_for_status()
        if response.status_code in (204, 205) or not raw_bytes.strip():
            return {}

        try:
            parsed: Any = json.loads(raw_bytes.decode("utf-8", errors="replace"))
        except json.JSONDecodeError as exc:
            raise RequestException(
                f"Expected JSON response from {response.url or '<unknown>'}, got non-JSON body"
            ) from exc
        return parsed if isinstance(parsed, dict) else {"data": parsed}
    finally:
        response.close()


class GENYEEClient:
    """Client for interacting with GENYEE API"""

    def __init__(self, base_url: str, api_key: str) -> None:
        """Initialize the GENYEE API client with base URL and authentication"""
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        # SSL verification: enabled by default. Set GENYEE_VERIFY_SSL=false to disable
        # (e.g. for self-signed certs in dev environments — NOT recommended for production).
        verify_env = os.getenv("GENYEE_VERIFY_SSL", "true")
        self.verify_ssl = verify_env.lower() != "false"
        if not self.verify_ssl:
            logger.warning(
                "SSL certificate verification is DISABLED (GENYEE_VERIFY_SSL=false). "
                "This is insecure and should not be used in production."
            )
            urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)
        # MCP 2.x runs sync @mcp.tool() handlers on worker threads; use a
        # thread-local Session because requests.Session is not thread-safe, and
        # sweep finished worker threads so connection pools never leak.
        self._session_local = threading.local()
        self._sessions_lock = threading.Lock()
        self._session_gen = 0
        self._tracked_sessions: list[
            tuple[weakref.ReferenceType[threading.Thread], requests.Session]
        ] = []

    def _new_session(self) -> requests.Session:
        session = _SameOriginSession()
        session.verify = self.verify_ssl
        session.headers.update(
            {
                "X-API-Key": self.api_key,
                "Content-Type": "application/json",
            }
        )
        return session

    def _sweep_dead_sessions_locked(self) -> list[requests.Session]:
        alive: list[tuple[weakref.ReferenceType[threading.Thread], requests.Session]] = []
        stale: list[requests.Session] = []
        for thread_ref, sess in self._tracked_sessions:
            thread = thread_ref()
            if thread is None or not thread.is_alive():
                stale.append(sess)
            else:
                alive.append((thread_ref, sess))
        self._tracked_sessions = alive
        return stale

    @property
    def session(self) -> requests.Session:
        existing: requests.Session | None = getattr(self._session_local, "session", None)
        existing_gen: int | None = getattr(self._session_local, "session_gen", None)
        if existing is not None and existing_gen == self._session_gen:
            return existing

        new_sess = self._new_session()
        current_thread = threading.current_thread()
        with self._sessions_lock:
            stale = self._sweep_dead_sessions_locked()
            gen = self._session_gen
            self._tracked_sessions.append((weakref.ref(current_thread), new_sess))
        for old_sess in stale:
            old_sess.close()
        self._session_local.session = new_sess
        self._session_local.session_gen = gen
        return new_sess

    def close(self) -> None:
        """Close all pooled thread-local HTTP sessions owned by this client."""
        with self._sessions_lock:
            self._session_gen += 1
            to_close = [sess for _, sess in self._tracked_sessions]
            self._tracked_sessions.clear()
        self._session_local.__dict__.clear()
        for sess in to_close:
            sess.close()

    def __enter__(self) -> GENYEEClient:
        return self

    def __exit__(self, exc_type: object, exc: object, tb: object) -> None:
        self.close()

    def _request(self, method: str, endpoint: str, **kwargs: Any) -> dict[str, Any]:
        """Make a request to the GENYEE API."""
        url = f"{self.base_url}{endpoint}"
        kwargs.setdefault("timeout", GENYEE_API_TIMEOUT)
        kwargs.setdefault("stream", True)
        try:
            response = self.session.request(method, url, **kwargs)
            return _read_bounded_json_response(response)
        except RequestException as e:
            logger.error("API request failed: %s", e)
            raise

    # Tenant Management - Methods for managing multi-tenant configurations
    def create_tenant(
        self,
        name: str,
        description: str,
        business: str,
        retriever_engines: dict[str, Any],
    ) -> dict[str, Any]:
        """Create a new tenant with specified configuration"""
        data: dict[str, Any] = {
            "name": name,
            "description": description,
            "business": business,
            "retriever_engines": retriever_engines,
        }
        return self._request("POST", "/tenants", json=data)

    def get_tenant(self, tenant_id: str) -> dict[str, Any]:
        """Get tenant information"""
        safe_tenant_id = _safe_path_segment(tenant_id, "tenant_id")
        return self._request("GET", f"/tenants/{safe_tenant_id}")

    def list_tenants(self) -> dict[str, Any]:
        """List all tenants"""
        return self._request("GET", "/tenants")

    # Knowledge Base Management - Methods for managing knowledge bases
    def create_knowledge_base(
        self, name: str, description: str, config: dict[str, Any]
    ) -> dict[str, Any]:
        """Create a new knowledge base with chunking and model configuration"""
        data: dict[str, Any] = {
            "name": name,
            "description": description,
            **config,
        }
        return self._request("POST", "/knowledge-bases", json=data)

    def list_knowledge_bases(self) -> dict[str, Any]:
        """List all knowledge bases"""
        return self._request("GET", "/knowledge-bases")

    def list_shared_knowledge_bases(self) -> dict[str, Any]:
        """List knowledge bases shared from other workspaces"""
        return self._request("GET", "/shared-knowledge-bases")

    def get_knowledge_base(self, kb_id: str) -> dict[str, Any]:
        """Get knowledge base details"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        return self._request("GET", f"/knowledge-bases/{safe_kb_id}")

    def update_knowledge_base(self, kb_id: str, updates: dict[str, Any]) -> dict[str, Any]:
        """Update knowledge base"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        return self._request("PUT", f"/knowledge-bases/{safe_kb_id}", json=updates)

    def delete_knowledge_base(self, kb_id: str) -> dict[str, Any]:
        """Delete knowledge base"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        return self._request("DELETE", f"/knowledge-bases/{safe_kb_id}")

    # ── UUID pattern (8-4-4-4-12 hex) ──────────────────────────────────────
    _UUID_RE = re.compile(
        r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
        re.IGNORECASE,
    )

    def resolve_agent_id(self, agent_id_or_name: str) -> str:
        """Resolve an agent ID or name to its canonical ID.

        If *agent_id_or_name* is already a UUID it is returned unchanged.
        Otherwise an exact ``id`` match takes precedence over the first
        case-insensitive ``name`` match.
        Raises ValueError when no match is found.
        """
        if not isinstance(agent_id_or_name, str) or not agent_id_or_name.strip():
            raise ValueError("Invalid agent_id: expected non-empty string")
        candidate = agent_id_or_name.strip()
        if len(candidate) > 256 or any(ord(ch) < 32 or ord(ch) == 127 for ch in candidate):
            raise ValueError("Invalid agent_id: contains disallowed characters or length")
        if self._UUID_RE.fullmatch(candidate):
            return candidate
        resp = self._request("GET", "/agents")
        agents: Any = resp.get("data", resp) if isinstance(resp, dict) else resp
        if isinstance(agents, dict):
            agents = agents.get("list", agents.get("items", []))
        needle = candidate.lower()
        name_match: str | None = None
        if isinstance(agents, list):
            for agent in agents:
                if not isinstance(agent, dict):
                    continue
                raw_id = agent.get("id")
                if not isinstance(raw_id, str) or not raw_id:
                    continue
                if raw_id == candidate:
                    _safe_path_segment(raw_id, "agent_id")
                    return raw_id
                raw_name = agent.get("name")
                if name_match is None and isinstance(raw_name, str) and raw_name.lower() == needle:
                    name_match = raw_id
        if name_match is not None:
            _safe_path_segment(name_match, "agent_id")
            return name_match
        raise ValueError(
            f"Agent {agent_id_or_name!r} not found. "
            "Use list_agents to see available agent IDs and names."
        )

    def resolve_kb_id(self, kb_id_or_name: str) -> str:
        """Resolve a knowledge base ID or name to its canonical ID.

        If *kb_id_or_name* is already a UUID it is returned unchanged without
        network I/O. Otherwise owned knowledge bases are searched before shared
        knowledge bases; within each source an exact ``id`` match takes
        precedence over the first case-insensitive ``name`` match.
        Raises ValueError when no match is found or the input is blank/invalid.
        """
        if not isinstance(kb_id_or_name, str) or not kb_id_or_name.strip():
            raise ValueError("Invalid kb_id: expected non-empty string")
        candidate = kb_id_or_name.strip()
        if len(candidate) > 256 or any(ord(ch) < 32 or ord(ch) == 127 for ch in candidate):
            raise ValueError("Invalid kb_id: contains disallowed characters or length")
        if self._UUID_RE.fullmatch(candidate):
            return candidate

        needle = candidate.lower()
        for source in (self.list_knowledge_bases, self.list_shared_knowledge_bases):
            entries = _normalize_kb_entries(source())
            for kb in entries:
                raw_id = kb.get("id")
                if isinstance(raw_id, str) and raw_id == candidate:
                    _safe_path_segment(raw_id, "kb_id")
                    return raw_id
            for kb in entries:
                raw_id = kb.get("id")
                raw_name = kb.get("name")
                if (
                    isinstance(raw_id, str)
                    and raw_id
                    and isinstance(raw_name, str)
                    and raw_name.lower() == needle
                ):
                    _safe_path_segment(raw_id, "kb_id")
                    return raw_id
        raise ValueError(
            f"Knowledge base {kb_id_or_name!r} not found. "
            "Use list_knowledge_bases or list_shared_knowledge_bases "
            "to see available IDs and names."
        )

    def hybrid_search(self, kb_id: str, query: str, config: dict[str, Any]) -> dict[str, Any]:
        """Perform hybrid search combining vector and keyword search"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        validated_config = dict(config)
        if "vector_threshold" in validated_config:
            validated_config["vector_threshold"] = _validate_unit_float(
                validated_config["vector_threshold"], "vector_threshold"
            )
        if "keyword_threshold" in validated_config:
            validated_config["keyword_threshold"] = _validate_unit_float(
                validated_config["keyword_threshold"], "keyword_threshold"
            )
        if "match_count" in validated_config:
            validated_config["match_count"] = _validate_int_range(
                validated_config["match_count"], "match_count", 1, 100
            )
        data: dict[str, Any] = {
            "query_text": query,
            **validated_config,
        }
        return self._request("POST", f"/knowledge-bases/{safe_kb_id}/hybrid-search", json=data)

    # Knowledge Management - Methods for creating and managing knowledge entries
    def create_knowledge_from_file(
        self,
        kb_id: str,
        file_path: str,
        enable_multimodel: bool = True,
        file_name: str = "",
    ) -> dict[str, Any]:
        """Create knowledge from a local file with optional multimodal processing.

        Streams the multipart body directly from the validated file handle in
        64 KiB chunks while reusing ``self.session`` with ``stream=True``.
        """
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        safe_path = resolve_upload_file_path(file_path)
        form_fields: dict[str, str] = {"enable_multimodel": str(bool(enable_multimodel)).lower()}
        if file_name:
            form_fields["fileName"] = file_name

        url = f"{self.base_url}/knowledge-bases/{safe_kb_id}/knowledge/file"
        try:
            with open_upload_file_nofollow(safe_path) as f:
                body_stream = _MultipartFileStream(
                    fields=form_fields,
                    file_field_name="file",
                    filename=os.path.basename(safe_path),
                    file_obj=f,
                )
                response = self.session.request(
                    "POST",
                    url,
                    data=cast(Any, body_stream),
                    headers={"Content-Type": body_stream.content_type},
                    timeout=GENYEE_API_TIMEOUT,
                    allow_redirects=False,
                    stream=True,
                )
                return _read_bounded_json_response(response)
        except RequestException as e:
            logger.error("Multipart upload request failed: %s", e)
            raise

    def create_knowledge_from_url(
        self, kb_id: str, url: str, enable_multimodel: bool = True
    ) -> dict[str, Any]:
        """Create knowledge from a web URL with optional multimodal processing"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        safe_url = _validate_http_url(url)
        data: dict[str, Any] = {
            "url": safe_url,
            "enable_multimodel": bool(enable_multimodel),
        }
        return self._request("POST", f"/knowledge-bases/{safe_kb_id}/knowledge/url", json=data)

    def create_knowledge_from_text(
        self,
        kb_id: str,
        title: str,
        content: str,
        tag_ids: list[str] | None = None,
        status: str = "publish",
    ) -> dict[str, Any]:
        """Create a knowledge entry from raw Markdown text (manual knowledge)."""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        safe_status = _validate_knowledge_status(status)
        data: dict[str, Any] = {
            "title": title,
            "content": content,
            "status": safe_status,
        }
        if tag_ids:
            data["tag_ids"] = tag_ids
        return self._request("POST", f"/knowledge-bases/{safe_kb_id}/knowledge/manual", json=data)

    def update_knowledge_from_text(
        self,
        knowledge_id: str,
        content: str,
        title: str = "",
        status: str = "publish",
    ) -> dict[str, Any]:
        """Update an existing manual Markdown knowledge entry."""
        safe_knowledge_id = _safe_path_segment(knowledge_id, "knowledge_id")
        safe_status = _validate_knowledge_status(status)
        data: dict[str, Any] = {
            "title": title,
            "content": content,
            "status": safe_status,
        }
        return self._request("PUT", f"/knowledge/manual/{safe_knowledge_id}", json=data)

    def list_knowledge(
        self,
        kb_id: str,
        page: int = 1,
        page_size: int = 20,
        folder_path: str | None = None,
        folder_scope: str = "",
    ) -> dict[str, Any]:
        """List knowledge in a knowledge base."""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        safe_page = _validate_int_range(page, "page", 1)
        safe_page_size = _validate_int_range(page_size, "page_size", 1, 100)
        params: dict[str, int | str] = {
            "page": safe_page,
            "page_size": safe_page_size,
        }
        if folder_path is not None:
            params["folder_path"] = folder_path
        if folder_scope:
            params["folder_scope"] = folder_scope
        return self._request("GET", f"/knowledge-bases/{safe_kb_id}/knowledge", params=params)

    def get_knowledge(self, knowledge_id: str) -> dict[str, Any]:
        """Get knowledge details"""
        safe_knowledge_id = _safe_path_segment(knowledge_id, "knowledge_id")
        return self._request("GET", f"/knowledge/{safe_knowledge_id}")

    def delete_knowledge(self, knowledge_id: str) -> dict[str, Any]:
        """Delete knowledge"""
        safe_knowledge_id = _safe_path_segment(knowledge_id, "knowledge_id")
        return self._request("DELETE", f"/knowledge/{safe_knowledge_id}")

    # Model Management - Methods for managing AI models (LLM, Embedding, Rerank)
    def create_model(
        self,
        name: str,
        model_type: str,
        source: str,
        description: str,
        parameters: dict[str, Any],
        is_default: bool = False,
    ) -> dict[str, Any]:
        """Create a new AI model configuration"""
        data: dict[str, Any] = {
            "name": name,
            "type": model_type,
            "source": source,
            "description": description,
            "parameters": parameters,
            "is_default": is_default,
        }
        return self._request("POST", "/models", json=data)

    def list_models(self) -> dict[str, Any]:
        """List all models"""
        return self._request("GET", "/models")

    def get_model(self, model_id: str) -> dict[str, Any]:
        """Get model details"""
        safe_model_id = _safe_path_segment(model_id, "model_id")
        return self._request("GET", f"/models/{safe_model_id}")

    # Session Management - Methods for managing chat sessions
    def create_session(
        self,
        title: str = "",
        description: str = "",
    ) -> dict[str, Any]:
        """Create a session with optional title and description."""
        data: dict[str, Any] = {}
        if title:
            data["title"] = title
        if description:
            data["description"] = description
        return self._request("POST", "/sessions", json=data)

    def get_session(self, session_id: str) -> dict[str, Any]:
        """Get session details"""
        safe_session_id = _safe_path_segment(session_id, "session_id")
        return self._request("GET", f"/sessions/{safe_session_id}")

    def list_sessions(self, page: int = 1, page_size: int = 20) -> dict[str, Any]:
        """List sessions"""
        safe_page = _validate_int_range(page, "page", 1)
        safe_page_size = _validate_int_range(page_size, "page_size", 1, 100)
        params = {"page": safe_page, "page_size": safe_page_size}
        return self._request("GET", "/sessions", params=params)

    def delete_session(self, session_id: str) -> dict[str, Any]:
        """Delete session"""
        safe_session_id = _safe_path_segment(session_id, "session_id")
        return self._request("DELETE", f"/sessions/{safe_session_id}")

    # Chat Functionality - Methods for conversational interactions
    def _consume_sse_stream(self, url: str, body: dict[str, Any]) -> dict[str, Any]:
        """POST to *url* with *body*, consume the SSE stream, and return the assembled result."""
        try:
            response = self.session.post(
                url,
                json=body,
                stream=True,
                timeout=(10, GENYEE_CHAT_TIMEOUT),
            )

            answer_chunks: list[str] = []
            references: list[Any] = []
            data_lines: list[str] = []
            event_bytes = 0
            total_stream_bytes = 0

            with response:
                response.raise_for_status()
                first_line = True
                for line in _iter_sse_lines(response):
                    total_stream_bytes += len(line) + 1
                    if total_stream_bytes > MAX_SSE_STREAM_BYTES:
                        raise RequestException(f"SSE stream exceeds {MAX_SSE_STREAM_BYTES} bytes")
                    raw_line = line.decode("utf-8", errors="replace")
                    if first_line:
                        # SSE ignores exactly one BOM at the start of the stream.
                        raw_line = raw_line.removeprefix("\ufeff")
                        first_line = False
                    if raw_line:
                        field, _, value = raw_line.partition(":")
                        if field == "data":
                            # SSE removes at most one leading space from a value.
                            if value.startswith(" "):
                                value = value[1:]
                            event_bytes += len(value.encode("utf-8")) + 1
                            if event_bytes > MAX_SSE_EVENT_BYTES:
                                raise RequestException(
                                    f"SSE event exceeds {MAX_SSE_EVENT_BYTES} bytes"
                                )
                            data_lines.append(value)
                        continue
                    # Dispatch only at the blank-line boundary. Incomplete data
                    # at EOF is discarded, as required by the SSE protocol.
                    if not data_lines:
                        continue
                    payload = "\n".join(data_lines)
                    data_lines = []
                    event_bytes = 0
                    try:
                        event_data = json.loads(payload)
                    except json.JSONDecodeError:
                        continue
                    if not isinstance(event_data, dict):
                        continue

                    response_type = event_data.get("response_type", "")
                    if response_type == "answer":
                        chunk = event_data.get("content", "")
                        if isinstance(chunk, str) and chunk:
                            answer_chunks.append(chunk)
                    elif response_type == "references":
                        refs = event_data.get("knowledge_references")
                        references = refs if isinstance(refs, list) else []
                    elif response_type == "error":
                        raise RequestException(
                            f"Server error: {event_data.get('content', 'unknown error')}"
                        )
                    elif response_type in ("complete", "stop"):
                        result: dict[str, Any] = {
                            "answer": "".join(answer_chunks),
                            "references": references,
                        }
                        if response_type == "stop":
                            result["stopped"] = True
                        return result

            raise RequestException("SSE stream ended before complete event")
        except RequestException as e:
            logger.error("SSE stream request failed (%s): %s", url, e)
            raise

    def chat(
        self,
        session_id: str,
        query: str,
        knowledge_base_ids: list[str] | None = None,
        web_search_enabled: bool = False,
    ) -> dict[str, Any]:
        """Send a message to the RAG pipeline (knowledge-chat) and return the assembled answer."""
        safe_session_id = _safe_path_segment(session_id, "session_id")
        url = f"{self.base_url}/knowledge-chat/{safe_session_id}"
        body: dict[str, Any] = {"query": query, "channel": "api"}
        if knowledge_base_ids:
            body["knowledge_base_ids"] = knowledge_base_ids
        if web_search_enabled:
            body["web_search_enabled"] = True
        result = self._consume_sse_stream(url, body)
        result["session_id"] = session_id
        return result

    def agent_chat(
        self,
        session_id: str,
        query: str,
        agent_id: str,
        knowledge_base_ids: list[str] | None = None,
        web_search_enabled: bool = False,
    ) -> dict[str, Any]:
        """Send a message to the agentic pipeline (agent-chat) and return the assembled answer."""
        safe_session_id = _safe_path_segment(session_id, "session_id")
        url = f"{self.base_url}/agent-chat/{safe_session_id}"
        body: dict[str, Any] = {
            "query": query,
            "agent_id": agent_id,
            "agent_enabled": True,
            "channel": "api",
        }
        if knowledge_base_ids:
            body["knowledge_base_ids"] = knowledge_base_ids
        if web_search_enabled:
            body["web_search_enabled"] = True
        result = self._consume_sse_stream(url, body)
        result["session_id"] = session_id
        return result

    def list_agents(self) -> dict[str, Any]:
        """List all custom agents available to the current tenant."""
        return self._request("GET", "/agents")

    def get_agent(self, agent_id: str) -> dict[str, Any]:
        """Get full config of a single agent by UUID."""
        safe_agent_id = _safe_path_segment(agent_id, "agent_id")
        return self._request("GET", f"/agents/{safe_agent_id}")

    # Chunk Management - Methods for managing knowledge chunks (text segments)
    def list_chunks(self, knowledge_id: str, page: int = 1, page_size: int = 20) -> dict[str, Any]:
        """List text chunks of a knowledge entry with pagination"""
        safe_knowledge_id = _safe_path_segment(knowledge_id, "knowledge_id")
        safe_page = _validate_int_range(page, "page", 1)
        safe_page_size = _validate_int_range(page_size, "page_size", 1, 100)
        params = {"page": safe_page, "page_size": safe_page_size}
        return self._request("GET", f"/chunks/{safe_knowledge_id}", params=params)

    def delete_chunk(self, knowledge_id: str, chunk_id: str) -> dict[str, Any]:
        """Delete a chunk"""
        safe_knowledge_id = _safe_path_segment(knowledge_id, "knowledge_id")
        safe_chunk_id = _safe_path_segment(chunk_id, "chunk_id")
        return self._request("DELETE", f"/chunks/{safe_knowledge_id}/{safe_chunk_id}")

    # Wiki Read-Only - Methods for querying LLM-generated wiki pages
    def wiki_search(self, kb_id: str, query: str, limit: int = 10) -> dict[str, Any]:
        """Search wiki pages by full-text query"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        safe_limit = _validate_int_range(limit, "limit", 1, 100)
        return self._request(
            "GET",
            f"/knowledgebase/{safe_kb_id}/wiki/search",
            params={"q": query, "limit": safe_limit},
        )

    def wiki_read_page(self, kb_id: str, slug: str) -> dict[str, Any]:
        """Read a wiki page by slug, returns full markdown + metadata + links"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        safe_slug = _safe_wiki_slug(slug, "slug")
        return self._request("GET", f"/knowledgebase/{safe_kb_id}/wiki/pages/{safe_slug}")

    def wiki_index_view(self, kb_id: str, limit: int = 50) -> dict[str, Any]:
        """Get structured wiki index with per-type directory groups"""
        safe_kb_id = _safe_path_segment(kb_id, "kb_id")
        safe_limit = _validate_int_range(limit, "limit", 1, 100)
        return self._request(
            "GET",
            f"/knowledgebase/{safe_kb_id}/wiki/index",
            params={"limit": safe_limit},
        )


# Initialize MCP server instance (mcp 2.x high-level API).
mcp = MCPServer("genyee-server", version="1.1.1")
client = GENYEEClient(GENYEE_BASE_URL, GENYEE_API_KEY)


# ---------------------------------------------------------------------------
# Tool registrations
# ---------------------------------------------------------------------------


@mcp.tool()
def create_tenant(
    name: str,
    description: str,
    business: str,
    retriever_engines: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Create a new tenant in GENYEE."""
    _require_admin_tools_enabled("create_tenant")
    engines = retriever_engines or {
        "engines": [
            {"retriever_type": "keywords", "retriever_engine_type": "postgres"},
            {"retriever_type": "vector", "retriever_engine_type": "postgres"},
        ]
    }
    return client.create_tenant(name, description, business, engines)


@mcp.tool()
def list_tenants() -> dict[str, Any]:
    """List all tenants."""
    _require_admin_tools_enabled("list_tenants")
    return client.list_tenants()


@mcp.tool()
def create_knowledge_base(
    name: str,
    description: str,
    embedding_model_id: str = "",
    summary_model_id: str = "",
) -> dict[str, Any]:
    """Create a new knowledge base."""
    config: dict[str, Any] = {
        "chunking_config": {
            "chunk_size": 1000,
            "chunk_overlap": 200,
            "separators": ["."],
            "enable_multimodal": True,
        },
        "embedding_model_id": embedding_model_id,
        "summary_model_id": summary_model_id,
    }
    return client.create_knowledge_base(name, description, config)


@mcp.tool()
def list_knowledge_bases() -> dict[str, Any]:
    """List all knowledge bases in the current workspace."""
    return client.list_knowledge_bases()


@mcp.tool()
def list_shared_knowledge_bases() -> dict[str, Any]:
    """List knowledge bases shared from other workspaces."""
    return client.list_shared_knowledge_bases()


@mcp.tool()
def get_knowledge_base(kb_id: str) -> dict[str, Any]:
    """Get knowledge base details (kb_id may be a UUID or knowledge-base name)."""
    return client.get_knowledge_base(client.resolve_kb_id(kb_id))


@mcp.tool()
def delete_knowledge_base(kb_id: str) -> dict[str, Any]:
    """Delete a knowledge base (kb_id may be a UUID or knowledge-base name)."""
    return client.delete_knowledge_base(client.resolve_kb_id(kb_id))


@mcp.tool()
def hybrid_search(
    kb_id: str,
    query: str,
    vector_threshold: float = 0.5,
    keyword_threshold: float = 0.3,
    match_count: int = 5,
) -> dict[str, Any]:
    """Perform hybrid (vector + keyword) search in a knowledge base.

    kb_id may be a UUID or a knowledge-base name (resolved automatically).
    Use list_knowledge_bases or list_shared_knowledge_bases to discover available knowledge bases.
    """
    config: dict[str, Any] = {
        "vector_threshold": _validate_unit_float(vector_threshold, "vector_threshold"),
        "keyword_threshold": _validate_unit_float(keyword_threshold, "keyword_threshold"),
        "match_count": _validate_int_range(match_count, "match_count", 1, 100),
    }
    resolved = client.resolve_kb_id(kb_id)
    return client.hybrid_search(resolved, query, config)


@mcp.tool()
def create_knowledge_from_file(
    kb_id: str,
    file_path: str,
    enable_multimodel: bool = True,
    file_name: str = "",
) -> dict[str, Any]:
    """Create knowledge from a local file on the server filesystem.

    ``file_name`` is optional. Pass a path-qualified name such as
    ``docs/spec/design.pdf`` to place the entry under a knowledge folder
    (backend splits folder path + display name). Omit it to keep the
    original filename at the knowledge-base root.
    """
    return client.create_knowledge_from_file(
        client.resolve_kb_id(kb_id),
        file_path,
        enable_multimodel,
        file_name=file_name,
    )


@mcp.tool()
def create_knowledge_from_url(
    kb_id: str,
    url: str,
    enable_multimodel: bool = True,
) -> dict[str, Any]:
    """Create knowledge from a web URL."""
    safe_url = _validate_http_url(url)
    return client.create_knowledge_from_url(
        client.resolve_kb_id(kb_id), safe_url, enable_multimodel
    )


@mcp.tool()
def create_knowledge_from_text(
    kb_id: str,
    title: str,
    content: str,
    tag_ids: list[str] | None = None,
    status: str = "publish",
) -> dict[str, Any]:
    """Create a knowledge entry from raw Markdown text.

    Use this when you have the document content directly (e.g. an abstract or
    pasted text) instead of a file path or URL. ``kb_id`` may be a UUID or a
    knowledge-base name (resolved automatically). ``title`` and ``content``
    are required. ``status`` defaults to ``"publish"`` so the entry is indexed
    and searchable immediately; pass ``"draft"`` to save without indexing.
    """
    safe_status = _validate_knowledge_status(status)
    return client.create_knowledge_from_text(
        client.resolve_kb_id(kb_id),
        title,
        content,
        tag_ids=tag_ids,
        status=safe_status,
    )


@mcp.tool()
def update_knowledge_from_text(
    knowledge_id: str,
    content: str,
    title: str = "",
    status: str = "publish",
) -> dict[str, Any]:
    """Update an existing manual Markdown knowledge entry.

    ``knowledge_id`` is the ID returned by ``create_knowledge_from_text`` or
    ``list_knowledge``. ``content`` is required. Leave ``title`` empty to keep
    the current title. ``status`` defaults to ``"publish"`` so the new content
    is re-indexed; pass ``"draft"`` to save without indexing.
    """
    safe_status = _validate_knowledge_status(status)
    return client.update_knowledge_from_text(knowledge_id, content, title=title, status=safe_status)


@mcp.tool()
def list_knowledge(
    kb_id: str,
    page: int = 1,
    page_size: int = 20,
    folder_path: str | None = None,
    folder_scope: str = "",
) -> dict[str, Any]:
    """List knowledge entries in a knowledge base.

    ``folder_path`` optionally filters to one folder (``""`` = root).
    ``folder_scope`` is forwarded when the backend supports scoped listing.
    """
    safe_page = _validate_int_range(page, "page", 1)
    safe_page_size = _validate_int_range(page_size, "page_size", 1, 100)
    return client.list_knowledge(
        client.resolve_kb_id(kb_id),
        safe_page,
        safe_page_size,
        folder_path=folder_path,
        folder_scope=folder_scope,
    )


@mcp.tool()
def get_knowledge(knowledge_id: str) -> dict[str, Any]:
    """Get knowledge details."""
    return client.get_knowledge(knowledge_id)


@mcp.tool()
def delete_knowledge(knowledge_id: str) -> dict[str, Any]:
    """Delete a knowledge entry."""
    return client.delete_knowledge(knowledge_id)


@mcp.tool()
def create_model(
    name: str,
    type: str,
    description: str,
    source: str = "local",
    base_url: str = "",
    api_key: str = "",
    is_default: bool = False,
) -> dict[str, Any]:
    """Create a new model configuration (type: KnowledgeQA, Embedding, or Rerank)."""
    _require_admin_tools_enabled("create_model")
    parameters = {"base_url": base_url, "api_key": api_key}
    return client.create_model(name, type, source, description, parameters, is_default)


@mcp.tool()
def list_models() -> dict[str, Any]:
    """List all models."""
    return client.list_models()


@mcp.tool()
def get_model(model_id: str) -> dict[str, Any]:
    """Get model details."""
    return client.get_model(model_id)


@mcp.tool()
def create_session(
    title: str = "",
    description: str = "",
) -> dict[str, Any]:
    """Create a chat session.

    Sessions are knowledge-base-independent; pass ``knowledge_base_ids`` when
    calling ``chat`` or ``agent_chat`` to retrieve from specific knowledge bases.
    """
    return client.create_session(title=title, description=description)


@mcp.tool()
def get_session(session_id: str) -> dict[str, Any]:
    """Get session details."""
    return client.get_session(session_id)


@mcp.tool()
def list_sessions(page: int = 1, page_size: int = 20) -> dict[str, Any]:
    """List chat sessions."""
    return client.list_sessions(page, page_size)


@mcp.tool()
def delete_session(session_id: str) -> dict[str, Any]:
    """Delete a session."""
    return client.delete_session(session_id)


@mcp.tool()
async def chat(
    session_id: str,
    query: str,
    knowledge_base_ids: list[str] | None = None,
    web_search_enabled: bool = False,
) -> dict[str, Any]:
    """RAG pipeline chat: retrieve relevant chunks from knowledge bases, then summarise with LLM.

    ALWAYS provide knowledge_base_ids (names like 'my-knowledge-base' or UUIDs) so
    retrieval can run — without them the answer is based on LLM knowledge only.
    Use list_knowledge_bases or list_shared_knowledge_bases to discover available knowledge bases.
    For multi-step reasoning or tool-calling use agent_chat instead.
    """
    kb_ids = (
        [await asyncio.to_thread(client.resolve_kb_id, k) for k in knowledge_base_ids]
        if knowledge_base_ids
        else None
    )
    fn = functools.partial(
        client.chat,
        session_id,
        query,
        knowledge_base_ids=kb_ids,
        web_search_enabled=web_search_enabled,
    )
    return await asyncio.get_running_loop().run_in_executor(None, fn)


@mcp.tool()
async def agent_chat(
    session_id: str,
    query: str,
    agent_id: str,
    knowledge_base_ids: list[str] | None = None,
    web_search_enabled: bool = False,
) -> dict[str, Any]:
    """Agentic pipeline chat: autonomously call tools (knowledge_search, web_search, SQL).

    REQUIRED: agent_id (name or UUID) — use list_agents to discover agents.
    IMPORTANT: many agents have KBSelectionMode=none and NO built-in knowledge bases.
    In that case you MUST pass knowledge_base_ids, otherwise the agent will fail
    with 'no search targets available'. Use get_agent to inspect an agent's
    kb_selection_mode and knowledge_bases before calling. If kb_selection_mode is
    'none' or 'selected' with an empty list, always provide knowledge_base_ids.
    """
    resolved_agent_id = await asyncio.to_thread(client.resolve_agent_id, agent_id)
    kb_ids = (
        [await asyncio.to_thread(client.resolve_kb_id, k) for k in knowledge_base_ids]
        if knowledge_base_ids
        else None
    )
    if not kb_ids:
        try:
            agent_info = await asyncio.to_thread(client.get_agent, resolved_agent_id)
            raw_agent = agent_info.get("data") or agent_info
            cfg = (raw_agent.get("config") if isinstance(raw_agent, dict) else None) or {}
            mode = cfg.get("kb_selection_mode", "selected")
            built_in_kbs = cfg.get("knowledge_bases") or []
            needs_kbs = (mode == "none") or (mode in ("selected", "") and not built_in_kbs)
            if needs_kbs:
                all_kbs = _normalize_kb_entries(
                    await asyncio.to_thread(client.list_knowledge_bases)
                ) + _normalize_kb_entries(
                    await asyncio.to_thread(client.list_shared_knowledge_bases)
                )
                seen_ids: set[str] = set()
                unique_kbs: list[dict[str, Any]] = []
                for kb in all_kbs:
                    found_id = kb.get("id")
                    if isinstance(found_id, str) and found_id and found_id not in seen_ids:
                        seen_ids.add(found_id)
                        unique_kbs.append(kb)
                kb_summary = ", ".join(
                    f"{kb.get('name')} ({kb.get('id')})" for kb in unique_kbs[:10]
                )
                raise ValueError(
                    f"Agent '{agent_id}' has kb_selection_mode='{mode}' with no built-in "
                    f"knowledge bases. You must provide knowledge_base_ids. "
                    f"Available knowledge bases: [{kb_summary}]"
                )
        except ValueError:
            raise
        except Exception as preflight_err:
            logger.warning("agent_chat preflight KB check failed (non-fatal): %s", preflight_err)
    fn = functools.partial(
        client.agent_chat,
        session_id,
        query,
        resolved_agent_id,
        knowledge_base_ids=kb_ids,
        web_search_enabled=web_search_enabled,
    )
    return await asyncio.get_running_loop().run_in_executor(None, fn)


@mcp.tool()
def list_agents() -> dict[str, Any]:
    """List all custom agents available to the current tenant.

    Use this to discover agent IDs, names, and their KB selection mode before
    calling agent_chat.
    """
    return client.list_agents()


@mcp.tool()
def get_agent(agent_id: str) -> dict[str, Any]:
    """Get full configuration of a single agent by UUID or name.

    Check kb_selection_mode and knowledge_bases fields: if kb_selection_mode is
    'none' or 'selected' with an empty knowledge_bases list, you MUST pass
    knowledge_base_ids when calling agent_chat.
    """
    resolved_id = client.resolve_agent_id(agent_id)
    return client.get_agent(resolved_id)


@mcp.tool()
def list_chunks(knowledge_id: str, page: int = 1, page_size: int = 20) -> dict[str, Any]:
    """List chunks (text segments) of a knowledge entry."""
    return client.list_chunks(knowledge_id, page, page_size)


@mcp.tool()
def delete_chunk(knowledge_id: str, chunk_id: str) -> dict[str, Any]:
    """Delete a chunk."""
    return client.delete_chunk(knowledge_id, chunk_id)


@mcp.tool()
def wiki_search(kb_id: str, query: str, limit: int = 10) -> dict[str, Any]:
    """Search wiki pages by full-text query.

    Returns matching wiki pages with title, slug, summary, and content snippets.
    """
    safe_limit = _validate_int_range(limit, "limit", 1, 100)
    return client.wiki_search(client.resolve_kb_id(kb_id), query, safe_limit)


@mcp.tool()
def wiki_read_page(kb_id: str, slug: str) -> dict[str, Any]:
    """Read a wiki page by its slug.

    Returns full markdown content, metadata, inbound/outbound links, and source
    references. slug example: 'entity/acme-corp', 'concept/rag'.
    """
    safe_slug = _safe_wiki_slug(slug, "slug")
    return client.wiki_read_page(client.resolve_kb_id(kb_id), safe_slug)


@mcp.tool()
def wiki_index_view(kb_id: str, limit: int = 50) -> dict[str, Any]:
    """Get a structured wiki index with per-type directory groups.

    Returns an overview of all wiki pages organized by type (entity, concept,
    summary, etc.).
    """
    safe_limit = _validate_int_range(limit, "limit", 1, 100)
    return client.wiki_index_view(client.resolve_kb_id(kb_id), safe_limit)


# ---------------------------------------------------------------------------
# Transports
# ---------------------------------------------------------------------------


async def run_stdio() -> None:
    """Run the MCP server using stdio transport."""
    set_active_transport("stdio")
    try:
        await mcp.run_stdio_async()
    finally:
        client.close()


async def run_sse(host: str, port: int) -> None:
    """Run the MCP server using SSE transport (legacy MCP clients)."""
    set_active_transport("sse")
    auth_token = require_network_transport_auth("sse")
    try:
        import uvicorn
    except ImportError as e:
        raise ImportError(
            f"SSE transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn\n{e}"
        ) from e

    transport_security = build_transport_security_settings(host, port)
    ssl_certfile, ssl_keyfile = _uvicorn_tls_paths()
    scheme = "https" if ssl_certfile else "http"
    starlette_app = MCPAuthMiddleware(
        mcp.sse_app(
            host=host,
            message_path=SSE_MESSAGE_PATH,
            transport_security=transport_security,
        ),
        auth_token,
    )

    logger.info("Starting SSE MCP server on %s:%d", host, port)
    logger.info("SSE endpoint:  %s://%s:%d/sse", scheme, host, port)
    logger.info("SSE messages: %s://%s:%d%s", scheme, host, port, SSE_MESSAGE_PATH)
    config = uvicorn.Config(
        starlette_app,
        host=host,
        port=port,
        log_level="info",
        timeout_graceful_shutdown=MCP_GRACEFUL_SHUTDOWN_TIMEOUT,
        ssl_certfile=ssl_certfile,
        ssl_keyfile=ssl_keyfile,
    )
    server = uvicorn.Server(config)
    try:
        await server.serve()
    finally:
        client.close()


async def run_http(host: str, port: int) -> None:
    """Run the MCP server using Streamable HTTP transport (MCP 2025-03-26 spec)."""
    set_active_transport("http")
    auth_token = require_network_transport_auth("http")
    try:
        import uvicorn
    except ImportError as e:
        raise ImportError(
            f"HTTP transport requires 'starlette' and 'uvicorn': pip install starlette uvicorn\n{e}"
        ) from e

    transport_security = build_transport_security_settings(host, port)
    ssl_certfile, ssl_keyfile = _uvicorn_tls_paths()
    scheme = "https" if ssl_certfile else "http"
    starlette_app = MCPAuthMiddleware(
        mcp.streamable_http_app(
            host=host,
            stateless_http=STREAMABLE_HTTP_STATELESS,
            transport_security=transport_security,
        ),
        auth_token,
    )

    logger.info("Starting Streamable HTTP MCP server on %s:%d", host, port)
    logger.info("MCP endpoint:  %s://%s:%d/mcp", scheme, host, port)
    config = uvicorn.Config(
        starlette_app,
        host=host,
        port=port,
        log_level="info",
        timeout_graceful_shutdown=MCP_GRACEFUL_SHUTDOWN_TIMEOUT,
        ssl_certfile=ssl_certfile,
        ssl_keyfile=ssl_keyfile,
    )
    server = uvicorn.Server(config)
    try:
        await server.serve()
    finally:
        client.close()


# Backward-compatible alias used by run_server.py
run = run_stdio


def main(argv: Sequence[str] | None = None) -> None:
    """Main entry point — supports stdio, sse, and http transports."""
    parser = argparse.ArgumentParser(description="GENYEE MCP Server")
    parser.add_argument(
        "--transport",
        type=_normalize_transport,
        choices=["stdio", "sse", "http"],
        default=_default_mcp_transport("stdio"),
        help="Transport type: stdio (default), sse, or http",
    )
    parser.add_argument(
        "--host",
        default=os.getenv("MCP_HOST", "127.0.0.1"),
        help="Bind host for network transports (default: 127.0.0.1)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=_default_mcp_port(8000),
        help="Bind port for network transports (default: 8000)",
    )
    args = parser.parse_args(argv)

    if args.transport in ("sse", "http") and not (1 <= args.port <= 65535):
        logger.error("Invalid port %d: must be between 1 and 65535.", args.port)
        sys.exit(1)

    if args.transport == "stdio":
        asyncio.run(run_stdio())
    elif args.transport == "sse":
        asyncio.run(run_sse(args.host, args.port))
    elif args.transport == "http":
        asyncio.run(run_http(args.host, args.port))


if __name__ == "__main__":
    main()
