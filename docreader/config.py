import logging
import os
import re
from collections.abc import Iterable
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlsplit, urlunsplit

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)


def _get_first_env(keys: Iterable[str]) -> tuple[str | None, str | None]:
    """Return (value, key) for the first existing env var in keys."""
    for k in keys:
        if k in os.environ:
            return os.environ.get(k), k
    return None, None


def _get_str(keys: Iterable[str], default: str = "") -> str:
    v, _ = _get_first_env(keys)
    return default if v is None else str(v)


def _get_int(keys: Iterable[str], default: int) -> int:
    v, _ = _get_first_env(keys)
    if v is None or str(v).strip() == "":
        return default
    try:
        return int(str(v).strip())
    except Exception:
        return default


def _get_bool(keys: Iterable[str], default: bool) -> bool:
    v, _ = _get_first_env(keys)
    if v is None or str(v).strip() == "":
        return default
    return str(v).strip().lower() in {"1", "true", "yes", "y", "on"}


def _positive_int(key: str, default: int) -> int:
    value = _get_int([key], default)
    if value <= 0:
        logger.warning("%s must be positive; using %d", key, default)
        return default
    return value


def _positive_int_keys(keys: Iterable[str], default: int) -> int:
    key_list = list(keys)
    value = _get_int(key_list, default)
    if value <= 0:
        logger.warning("%s must be positive; using %d", "/".join(key_list), default)
        return default
    return value


def _non_negative_int(key: str, default: int) -> int:
    value = _get_int([key], default)
    if value < 0:
        logger.warning("%s must be non-negative; using %d", key, default)
        return default
    return value


def _clamped_int(key: str, default: int, minimum: int, maximum: int) -> int:
    value = _get_int([key], default)
    if value < minimum:
        logger.warning("%s below minimum %d; clamping to %d", key, minimum, minimum)
        return minimum
    if value > maximum:
        logger.warning("%s above maximum %d; clamping to %d", key, maximum, maximum)
        return maximum
    return value


def _mask_secret(v: str) -> str:
    if not v:
        return ""
    if len(v) <= 6:
        return "***"
    return f"{v[:2]}***{v[-2:]}"


_RAW_URL_CRED_RE = re.compile(r"((?:[a-zA-Z][a-zA-Z0-9+.-]*://)?[^/\s@:]+:)([^/\s@]+)(@)")
_URL_TOKEN_RE = re.compile(r"https?://[^\s\"'<>)\]]+", re.IGNORECASE)
_AUTH_HEADER_SECRET_RE = re.compile(
    r"(?i)(\bauthorization\s*[:=]\s*(?:bearer|basic|token)\s+)([^\s,;\"']+)"
)
_BEARER_SECRET_RE = re.compile(r"(?i)(\bbearer\s+)(sk-[A-Za-z0-9_-]{8,}|[A-Za-z0-9._~+/=-]{12,})")
_KV_SECRET_RE = re.compile(
    r"(?i)(\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|secret|password)\s*[=:]\s*)([^\s,&\"'?;]+)"
)
_SK_TOKEN_RE = re.compile(r"\bsk-[A-Za-z0-9_-]{12,}\b")


def redact_secrets(text: str) -> str:
    """Redact credentials, bearer tokens, and API keys from log/error strings."""
    if not text:
        return ""
    out = _URL_TOKEN_RE.sub(lambda m: mask_proxy_url(m.group(0)), str(text))
    out = _RAW_URL_CRED_RE.sub(r"\1***\3", out)
    out = _AUTH_HEADER_SECRET_RE.sub(r"\1***", out)
    out = _BEARER_SECRET_RE.sub(r"\1***", out)
    out = _KV_SECRET_RE.sub(r"\1***", out)
    out = _SK_TOKEN_RE.sub("sk-***", out)
    return out


def mask_proxy_url(url: str) -> str:
    """Mask userinfo credentials in proxy/endpoint URLs before logging."""
    if not url:
        return ""
    raw = str(url).strip()
    if not raw:
        return ""
    try:
        has_scheme = "://" in raw
        probe = raw if has_scheme else f"http://{raw}"
        parts = urlsplit(probe)
        if parts.username is None and parts.password is None:
            return raw
        masked_user = _mask_secret(parts.username) if parts.username else ""
        if parts.password is not None:
            userinfo = f"{masked_user}:***" if masked_user else "***"
        else:
            userinfo = masked_user
        host = parts.hostname or ""
        if ":" in host and not host.startswith("["):
            host = f"[{host}]"
        port_part = f":{parts.port}" if parts.port is not None else ""
        netloc = f"{userinfo}@{host}{port_part}" if userinfo else f"{host}{port_part}"
        rebuilt = urlunsplit(
            (parts.scheme if has_scheme else "", netloc, parts.path, parts.query, parts.fragment)
        )
        if not has_scheme and rebuilt.startswith("//"):
            return rebuilt[2:]
        return rebuilt
    except Exception:
        return _RAW_URL_CRED_RE.sub(r"\1***\3", raw)


@dataclass(frozen=True)
class DocReaderConfig:
    # gRPC
    grpc_max_workers: int
    grpc_max_file_size_mb: int
    grpc_port: int

    # Parser
    docx_max_pages: int
    markitdown_max_workers: int
    markitdown_xlsx_max_table_cells: int
    odl_max_workers: int
    odl_hybrid: str
    odl_hybrid_url: str
    odl_hybrid_mode: str
    odl_hybrid_fallback: bool
    odl_markdown_with_html: bool
    html_main_content: bool
    pdf_render_max_workers: int
    pdf_render_parallelism: int
    pdf_render_dpi: int
    pdf_jpeg_quality: int
    pdf_render_max_edge: int

    # Optional image preprocessing, isolated from the parser workers.
    preprocess_max_workers: int
    preprocess_timeout_seconds: int
    preprocess_threads: int
    preprocess_memory_mb: int
    preprocess_max_pixels: int
    preprocess_max_edge: int
    preprocess_probe_edge: int

    # Proxy
    external_http_proxy: str
    external_https_proxy: str

    # Temp image output directory (shared with Go app via volume, local mode fallback)
    image_output_dir: str

    # OpenDataLoader timeout, JVM memory bounds & hybrid service token
    odl_hybrid_timeout_ms: int = 30000
    odl_timeout_seconds: int = 120
    odl_java_max_heap_mb: int = 1024
    odl_hybrid_auth_token: str = ""
    grpc_shutdown_grace_seconds: int = 10

    @property
    def grpc_max_message_size_bytes(self) -> int:
        # Same bounded envelope allowance as the app's HTTP upload and gRPC
        # client. The configured file limit still applies to raw file bytes.
        return self.grpc_max_file_size_mb + (1 << 20)


def load_config() -> DocReaderConfig:
    """Load config from environment variables (lightweight version)."""

    grpc_max_workers = _positive_int_keys(["DOCREADER_GRPC_MAX_WORKERS", "GRPC_MAX_WORKERS"], 4)
    grpc_max_file_size_mb = (
        _positive_int_keys(["DOCREADER_GRPC_MAX_FILE_SIZE_MB", "MAX_FILE_SIZE_MB"], 50)
        * 1024
        * 1024
    )
    raw_port = _get_int(["DOCREADER_GRPC_PORT", "PORT"], 50051)
    if 1 <= raw_port <= 65535:
        grpc_port = raw_port
    else:
        logger.warning("DOCREADER_GRPC_PORT/PORT %d out of range 1..65535; using 50051", raw_port)
        grpc_port = 50051
    grpc_shutdown_grace_seconds = _non_negative_int("DOCREADER_GRPC_SHUTDOWN_GRACE_SECONDS", 10)

    docx_max_pages = _non_negative_int("DOCREADER_DOCX_MAX_PAGES", 0)
    markitdown_max_workers = _non_negative_int("DOCREADER_MARKITDOWN_MAX_WORKERS", 1)
    markitdown_xlsx_max_table_cells = _positive_int(
        "DOCREADER_MARKITDOWN_XLSX_MAX_TABLE_CELLS", 200_000
    )
    odl_max_workers = _non_negative_int("DOCREADER_ODL_MAX_WORKERS", 1)
    odl_hybrid = _get_str(["DOCREADER_ODL_HYBRID"], "off")
    odl_hybrid_url = _get_str(
        ["DOCREADER_ODL_HYBRID_URL"],
        "http://127.0.0.1:5002",
    )
    odl_hybrid_mode = _get_str(["DOCREADER_ODL_HYBRID_MODE"], "auto")
    odl_hybrid_fallback = _get_bool(["DOCREADER_ODL_HYBRID_FALLBACK"], False)
    odl_markdown_with_html = _get_bool(["DOCREADER_ODL_MARKDOWN_WITH_HTML"], False)
    # Index only the main content of an uploaded HTML file, the way the URL
    # import path already does (trafilatura with a full-page fallback). It is
    # off by default on purpose, so upgrading never changes what a knowledge
    # base indexes for documents it already ingests: with it off every
    # uploaded HTML file is converted in full, exactly as before.
    html_main_content = _get_bool(["DOCREADER_HTML_MAIN_CONTENT"], False)
    pdf_render_max_workers = _non_negative_int("DOCREADER_PDF_RENDER_MAX_WORKERS", 1)
    # Intra-document render parallelism: how many worker processes render the
    # scanned pages of a SINGLE PDF in parallel. pdfium is not thread-safe, so
    # page rendering is fanned out across processes (each opens its own
    # document). A large scanned PDF rendered serially can take >1h on
    # CPU-constrained containers; this is the main lever to cut that wall time.
    # Default scales with CPU count but is capped so we don't oversubscribe.
    _cpu = os.cpu_count() or 1
    pdf_render_parallelism = _positive_int("DOCREADER_PDF_RENDER_PARALLELISM", max(1, min(4, _cpu)))
    pdf_render_dpi = _positive_int("DOCREADER_PDF_RENDER_DPI", 200)
    pdf_jpeg_quality = _clamped_int("DOCREADER_PDF_JPEG_QUALITY", 85, 1, 95)
    # Cap the long edge (px) of rendered/extracted page images. Without this,
    # PDFs declaring very large page boxes render to 100+ MP JPEGs that blow the
    # gRPC message limit (and are far higher-res than OCR needs). ~2000px keeps
    # dense CJK text legible for OCR while keeping page images well under ~1MB.
    pdf_render_max_edge = _positive_int("DOCREADER_PDF_RENDER_MAX_EDGE", 2000)

    external_http_proxy = _get_str(["DOCREADER_EXTERNAL_HTTP_PROXY", "EXTERNAL_HTTP_PROXY"], "")
    external_https_proxy = _get_str(["DOCREADER_EXTERNAL_HTTPS_PROXY", "EXTERNAL_HTTPS_PROXY"], "")

    image_output_dir = _get_str(
        ["DOCREADER_IMAGE_OUTPUT_DIR", "IMAGE_OUTPUT_DIR"], "/tmp/docreader"
    )

    return DocReaderConfig(
        grpc_max_workers=grpc_max_workers,
        grpc_max_file_size_mb=grpc_max_file_size_mb,
        grpc_port=grpc_port,
        docx_max_pages=docx_max_pages,
        markitdown_max_workers=markitdown_max_workers,
        markitdown_xlsx_max_table_cells=markitdown_xlsx_max_table_cells,
        odl_max_workers=odl_max_workers,
        odl_hybrid=odl_hybrid,
        odl_hybrid_url=odl_hybrid_url,
        odl_hybrid_mode=odl_hybrid_mode,
        odl_hybrid_fallback=odl_hybrid_fallback,
        odl_markdown_with_html=odl_markdown_with_html,
        html_main_content=html_main_content,
        pdf_render_max_workers=pdf_render_max_workers,
        pdf_render_parallelism=pdf_render_parallelism,
        pdf_render_dpi=pdf_render_dpi,
        pdf_jpeg_quality=pdf_jpeg_quality,
        pdf_render_max_edge=pdf_render_max_edge,
        preprocess_max_workers=_positive_int("DOCREADER_PREPROCESS_MAX_WORKERS", 1),
        preprocess_timeout_seconds=_positive_int("DOCREADER_PREPROCESS_TIMEOUT_SECONDS", 30),
        preprocess_threads=_positive_int("DOCREADER_PREPROCESS_THREADS", 2),
        preprocess_memory_mb=_positive_int("DOCREADER_PREPROCESS_MEMORY_MB", 2048),
        preprocess_max_pixels=_positive_int("DOCREADER_PREPROCESS_MAX_PIXELS", 16_000_000),
        preprocess_max_edge=_positive_int("DOCREADER_PREPROCESS_MAX_EDGE", 2000),
        preprocess_probe_edge=_positive_int("DOCREADER_PREPROCESS_PROBE_EDGE", 2000),
        external_http_proxy=external_http_proxy,
        external_https_proxy=external_https_proxy,
        image_output_dir=image_output_dir,
        odl_hybrid_timeout_ms=_positive_int("DOCREADER_ODL_HYBRID_TIMEOUT_MS", 30000),
        odl_timeout_seconds=_positive_int("DOCREADER_ODL_TIMEOUT_SECONDS", 120),
        odl_java_max_heap_mb=_positive_int("DOCREADER_ODL_JAVA_MAX_HEAP_MB", 1024),
        odl_hybrid_auth_token=_get_str(
            ["ODL_HYBRID_AUTH_TOKEN", "DOCREADER_ODL_HYBRID_AUTH_TOKEN"], ""
        ).strip(),
        grpc_shutdown_grace_seconds=grpc_shutdown_grace_seconds,
    )


CONFIG = load_config()


def dump_config(mask_secrets: bool = True) -> dict[str, Any]:
    cfg = CONFIG
    mask_url = mask_proxy_url if mask_secrets else (lambda u: u)
    d: dict[str, Any] = {
        "DOCREADER_GRPC_MAX_WORKERS": cfg.grpc_max_workers,
        "DOCREADER_GRPC_MAX_FILE_SIZE_MB": cfg.grpc_max_file_size_mb // (1024 * 1024),
        "DOCREADER_GRPC_PORT": cfg.grpc_port,
        "DOCREADER_GRPC_SHUTDOWN_GRACE_SECONDS": cfg.grpc_shutdown_grace_seconds,
        "DOCREADER_DOCX_MAX_PAGES": cfg.docx_max_pages,
        "DOCREADER_MARKITDOWN_MAX_WORKERS": cfg.markitdown_max_workers,
        "DOCREADER_MARKITDOWN_XLSX_MAX_TABLE_CELLS": cfg.markitdown_xlsx_max_table_cells,
        "DOCREADER_ODL_MAX_WORKERS": cfg.odl_max_workers,
        "DOCREADER_ODL_HYBRID": cfg.odl_hybrid,
        "DOCREADER_ODL_HYBRID_URL": mask_url(cfg.odl_hybrid_url),
        "DOCREADER_ODL_HYBRID_MODE": cfg.odl_hybrid_mode,
        "DOCREADER_ODL_HYBRID_FALLBACK": cfg.odl_hybrid_fallback,
        "DOCREADER_ODL_HYBRID_TIMEOUT_MS": cfg.odl_hybrid_timeout_ms,
        "DOCREADER_ODL_TIMEOUT_SECONDS": cfg.odl_timeout_seconds,
        "DOCREADER_ODL_JAVA_MAX_HEAP_MB": cfg.odl_java_max_heap_mb,
        "ODL_HYBRID_AUTH_TOKEN": (
            _mask_secret(cfg.odl_hybrid_auth_token) if mask_secrets else cfg.odl_hybrid_auth_token
        ),
        "DOCREADER_ODL_MARKDOWN_WITH_HTML": cfg.odl_markdown_with_html,
        "DOCREADER_HTML_MAIN_CONTENT": cfg.html_main_content,
        "DOCREADER_PDF_RENDER_MAX_WORKERS": cfg.pdf_render_max_workers,
        "DOCREADER_PDF_RENDER_PARALLELISM": cfg.pdf_render_parallelism,
        "DOCREADER_PDF_RENDER_DPI": cfg.pdf_render_dpi,
        "DOCREADER_PDF_JPEG_QUALITY": cfg.pdf_jpeg_quality,
        "DOCREADER_PDF_RENDER_MAX_EDGE": cfg.pdf_render_max_edge,
        "DOCREADER_PREPROCESS_MAX_WORKERS": cfg.preprocess_max_workers,
        "DOCREADER_PREPROCESS_TIMEOUT_SECONDS": cfg.preprocess_timeout_seconds,
        "DOCREADER_PREPROCESS_THREADS": cfg.preprocess_threads,
        "DOCREADER_PREPROCESS_MEMORY_MB": cfg.preprocess_memory_mb,
        "DOCREADER_PREPROCESS_MAX_PIXELS": cfg.preprocess_max_pixels,
        "DOCREADER_PREPROCESS_MAX_EDGE": cfg.preprocess_max_edge,
        "DOCREADER_PREPROCESS_PROBE_EDGE": cfg.preprocess_probe_edge,
        "DOCREADER_EXTERNAL_HTTP_PROXY": mask_url(cfg.external_http_proxy),
        "DOCREADER_EXTERNAL_HTTPS_PROXY": mask_url(cfg.external_https_proxy),
        "DOCREADER_IMAGE_OUTPUT_DIR": cfg.image_output_dir,
    }
    return d


def print_config() -> None:
    d = dump_config(mask_secrets=True)
    logger.info("DocReader env/config (effective values):")
    for k in sorted(d.keys()):
        logger.info("%s=%s", k, d[k])
