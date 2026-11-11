"""PDF parser backed by OpenDataLoader PDF (Apache-2.0).

Requires Java 11+ on PATH and the ``opendataloader-pdf`` Python package.
Each ``convert()`` spawns a JVM; concurrency is limited via
``DOCREADER_ODL_MAX_WORKERS``.

Hybrid mode (``docling-fast``, etc.) needs a running
``opendataloader-pdf-hybrid`` server — configure ``DOCREADER_ODL_HYBRID_URL``.
"""

from __future__ import annotations

import base64
import contextlib
import html
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.resources as resources
import json
import logging
import os
import re
import shutil
import signal
import subprocess
import tempfile
import threading
import time
import urllib.error
from urllib.parse import urlsplit
import urllib.request
from typing import Any, Dict, Iterator, List, Mapping, Optional, Tuple

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.concurrency import check_cancellation, parser_worker_limit
from docreader.parser.soffice_sandbox import build_minimal_sandbox_env
from docreader.utils.ssrf import is_ssrf_safe_url

logger = logging.getLogger(__name__)

_MIN_CHARS_PER_PAGE = 20
_IMAGE_SUFFIXES = (".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp")
_MD_IMAGE_RE = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")
_IMAGE_FILE_NUM_RE = re.compile(r"^imageFile(\d+)\.", re.I)
_JAR_NAME = "opendataloader-pdf-cli.jar"
ALLOWED_ODL_OVERRIDE_KEYS: frozenset[str] = frozenset(
    {
        "odl_hybrid",
        "odl_hybrid_mode",
        "odl_hybrid_fallback",
        "odl_markdown_with_html",
    }
)


class _NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Health probes never need redirects; refusing them closes redirect SSRF."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _override_str(overrides: Optional[Mapping[str, Any]], key: str, default: str = "") -> str:
    if overrides:
        v = overrides.get(key)
        if v is not None and str(v).strip() != "":
            return str(v).strip()
    return default


def _override_bool(overrides: Optional[Mapping[str, Any]], key: str, default: bool) -> bool:
    if overrides:
        v = overrides.get(key)
        if v is not None and str(v).strip() != "":
            return str(v).strip().lower() in {"1", "true", "yes", "y", "on"}
    return default


def _java_available() -> Tuple[bool, str]:
    if not shutil.which("java"):
        return False, "需要 Java 11+（JRE），请安装并在 PATH 中配置 java"
    return True, ""


def _package_available() -> Tuple[bool, str]:
    try:
        import opendataloader_pdf  # noqa: F401
    except ImportError as e:
        return False, f"opendataloader-pdf 未安装: {e}"
    return True, ""


def _ping_hybrid(
    url: str,
    *,
    timeout_sec: float = 5.0,
    retries: int = 3,
    retry_delay_sec: float = 2.0,
) -> Tuple[bool, str]:
    import time

    base = url.rstrip("/")
    health_url = f"{base}/health"
    safe, reason = is_ssrf_safe_url(health_url)
    if not safe:
        return False, f"OpenDataLoader hybrid URL 被 SSRF 防护拦截: {reason}"

    opener = urllib.request.build_opener(_NoRedirectHandler())
    last_err = ""
    for attempt in range(max(1, retries)):
        try:
            req = urllib.request.Request(health_url, method="GET")
            with opener.open(req, timeout=timeout_sec) as resp:
                if 200 <= resp.status < 300:
                    return True, ""
                last_err = f"hybrid 健康检查 HTTP {resp.status}: {health_url}"
        except urllib.error.URLError as e:
            last_err = f"无法连接 OpenDataLoader hybrid 服务 ({health_url}): {e}"
        except Exception as e:
            last_err = f"hybrid 健康检查失败: {e}"
        if attempt + 1 < retries:
            time.sleep(retry_delay_sec)
    hint = (
        "；若刚执行 make dev-start --odl-hybrid，请等待镜像构建/服务就绪"
        "（docker logs WeKnora-odl-hybrid）"
    )
    return False, last_err + hint


def _has_hybrid_url_override(overrides: Optional[Mapping[str, Any]]) -> bool:
    if not overrides:
        return False
    raw = overrides.get("odl_hybrid_url")
    return raw is not None and str(raw).strip() != ""


def opendataloader_available(
    overrides: Optional[Mapping[str, Any]] = None,
    quick: bool = False,
) -> Tuple[bool, str]:
    """Registry / ListEngines availability probe.

    When ``quick`` is set the hybrid health check uses a single short-timeout
    attempt with no retries, so it can be used for fast status listing without
    blocking on long retry/backoff loops. Parsing keeps the patient retry
    behavior to tolerate a hybrid service that is still starting up.
    """
    if _has_hybrid_url_override(overrides):
        return (
            False,
            "OpenDataLoader hybrid URL override is disallowed by SSRF security policy",
        )

    ok, msg = _java_available()
    if not ok:
        return False, msg
    ok, msg = _package_available()
    if not ok:
        return False, msg

    hybrid = _resolve_hybrid(overrides)
    if hybrid and hybrid.lower() not in ("off", ""):
        url = _resolve_hybrid_url(overrides)
        if url:
            if quick:
                return _ping_hybrid(url, retries=1, timeout_sec=2.0)
            return _ping_hybrid(url, retries=6, retry_delay_sec=5.0, timeout_sec=5.0)
    return True, ""


def _resolve_hybrid(overrides: Optional[Mapping[str, Any]] = None) -> str:
    return _override_str(overrides, "odl_hybrid", CONFIG.odl_hybrid)


def _resolve_hybrid_url(overrides: Optional[Mapping[str, Any]] = None) -> str:
    if _has_hybrid_url_override(overrides):
        raise RuntimeError(
            "OpenDataLoader hybrid URL override is disallowed by SSRF security policy"
        )
    return CONFIG.odl_hybrid_url


def _resolve_hybrid_auth_token() -> str:
    """Resolve server-side ODL hybrid bearer token (never overridable by requests)."""
    for env_key in ("ODL_HYBRID_AUTH_TOKEN", "DOCREADER_ODL_HYBRID_AUTH_TOKEN"):
        if env_key in os.environ:
            return (os.environ.get(env_key) or "").strip()
    return (getattr(CONFIG, "odl_hybrid_auth_token", "") or "").strip()


_HOP_BY_HOP_HEADERS: frozenset[str] = frozenset(
    {
        "authorization",
        "connection",
        "content-length",
        "expect",
        "host",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
    }
)


def _read_chunked_request_body(rfile: Any, max_bytes: int) -> bytes:
    """Decode an HTTP/1.1 Transfer-Encoding: chunked body with hard byte ceiling."""
    chunks: List[bytes] = []
    total = 0
    while True:
        size_line = rfile.readline(4096)
        if not size_line:
            raise ValueError("Unexpected EOF reading chunk size")
        size_token = size_line.split(b";", 1)[0].strip()
        try:
            chunk_len = int(size_token, 16)
        except ValueError as exc:
            raise ValueError("Malformed chunk size") from exc
        if chunk_len < 0:
            raise ValueError("Negative chunk size")
        if chunk_len == 0:
            # Consume optional trailer headers up to blank CRLF
            while True:
                trailer = rfile.readline(4096)
                if not trailer or trailer in (b"\r\n", b"\n"):
                    break
            break
        if total + chunk_len > max_bytes:
            raise OverflowError(f"Chunked payload exceeds {max_bytes} bytes")
        data = rfile.read(chunk_len)
        if len(data) != chunk_len:
            raise ValueError("Truncated chunk payload")
        crlf = rfile.read(2)
        if crlf not in (b"\r\n", b"\n"):
            raise ValueError("Missing chunk CRLF terminator")
        chunks.append(data)
        total += chunk_len
    return b"".join(chunks)


@contextlib.contextmanager
def _local_hybrid_auth_proxy(
    upstream_url: str,
    auth_token: str,
    *,
    timeout_sec: float = 60.0,
    max_body_bytes: Optional[int] = None,
) -> Iterator[str]:
    """Run an ephemeral 127.0.0.1:0 HTTP forwarder that injects Bearer auth upstream.

    ``opendataloader-pdf-cli.jar`` (``DoclingFastServerClient``) cannot attach custom
    HTTP headers, and the Java child process runs with a scrubbed environment that
    must not hold ``ODL_HYBRID_AUTH_TOKEN``. Binding an ephemeral loopback forwarder
    for the lifetime of a single ``convert()`` call keeps secrets out of the JVM
    process while authenticating every upstream ``odl-hybrid`` request under SSRF
    and no-redirect enforcement.
    """
    upstream_base = upstream_url.strip().rstrip("/")
    upstream_parts = urlsplit(upstream_base)
    if upstream_parts.scheme not in ("http", "https") or not upstream_parts.netloc:
        raise RuntimeError(f"Invalid ODL hybrid upstream URL: {upstream_url!r}")

    eff_max_body = (
        max_body_bytes
        if max_body_bytes is not None and max_body_bytes > 0
        else max(int(CONFIG.grpc_max_file_size_mb), 100 * 1024 * 1024) + (4 << 20)
    )
    eff_timeout = max(5.0, float(timeout_sec))

    class _HybridAuthForwardHandler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
            logger.debug("odl-hybrid local auth proxy: " + format, *args)

        def _send_json(self, status: int, payload: Dict[str, Any]) -> None:
            raw = json.dumps(payload).encode("utf-8")
            self.close_connection = True
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(raw)

        def _forward(self) -> None:
            self.close_connection = True
            req_path = self.path or "/"
            if (
                "\r" in req_path
                or "\n" in req_path
                or "\x00" in req_path
                or ".." in req_path
                or req_path.startswith("//")
                or "://" in req_path
            ):
                self._send_json(400, {"error": "Invalid proxy request path"})
                return

            rel_parts = urlsplit(req_path)
            norm_path = "/" + rel_parts.path.lstrip("/")
            target_url = f"{upstream_base}{norm_path}"
            if rel_parts.query:
                target_url = f"{target_url}?{rel_parts.query}"

            target_parts = urlsplit(target_url)
            if (
                target_parts.scheme != upstream_parts.scheme
                or target_parts.netloc != upstream_parts.netloc
            ):
                self._send_json(400, {"error": "Upstream origin mismatch"})
                return

            safe, reason = is_ssrf_safe_url(target_url)
            if not safe:
                self._send_json(
                    403,
                    {"error": f"OpenDataLoader hybrid URL blocked by SSRF policy: {reason}"},
                )
                return

            transfer_enc = (self.headers.get("Transfer-Encoding") or "").lower()
            content_len_hdr = self.headers.get("Content-Length")
            body: Optional[bytes] = None
            try:
                if "chunked" in transfer_enc:
                    body = _read_chunked_request_body(self.rfile, eff_max_body)
                elif content_len_hdr is not None:
                    clen = int(content_len_hdr.strip())
                    if clen < 0:
                        self._send_json(400, {"error": "Invalid Content-Length"})
                        return
                    if clen > eff_max_body:
                        self._send_json(413, {"error": "Payload exceeds maximum size"})
                        return
                    body = self.rfile.read(clen) if clen > 0 else b""
                elif self.command in ("POST", "PUT", "PATCH"):
                    body = b""
            except OverflowError:
                self._send_json(413, {"error": "Payload exceeds maximum size"})
                return
            except ValueError as exc:
                self._send_json(400, {"error": f"Malformed request body: {exc}"})
                return

            out_headers: Dict[str, str] = {}
            for hdr_key, hdr_val in self.headers.items():
                if hdr_key.lower() in _HOP_BY_HOP_HEADERS:
                    continue
                if "\r" in hdr_val or "\n" in hdr_val:
                    continue
                out_headers[hdr_key] = hdr_val
            out_headers["Authorization"] = f"Bearer {auth_token}"
            if body is not None:
                out_headers["Content-Length"] = str(len(body))

            opener = urllib.request.build_opener(
                urllib.request.ProxyHandler({}),
                _NoRedirectHandler(),
            )
            upstream_req = urllib.request.Request(
                target_url,
                data=body,
                headers=out_headers,
                method=self.command,
            )
            try:
                with opener.open(upstream_req, timeout=eff_timeout) as resp:
                    resp_body = resp.read()
                    resp_ct = resp.headers.get("Content-Type", "application/json")
                    self.send_response(resp.status)
                    self.send_header("Content-Type", resp_ct)
                    self.send_header("Content-Length", str(len(resp_body)))
                    self.send_header("Connection", "close")
                    self.end_headers()
                    if self.command != "HEAD" and resp_body:
                        self.wfile.write(resp_body)
            except urllib.error.HTTPError as exc:
                try:
                    if exc.code in (301, 302, 303, 307, 308):
                        self._send_json(
                            502,
                            {"error": "Upstream redirect disallowed by SSRF security policy"},
                        )
                        return
                    err_body = exc.read() if exc.fp is not None else b""
                    err_ct = (
                        exc.headers.get("Content-Type", "application/json")
                        if exc.headers is not None
                        else "application/json"
                    )
                    self.send_response(exc.code)
                    self.send_header("Content-Type", err_ct)
                    self.send_header("Content-Length", str(len(err_body)))
                    self.send_header("Connection", "close")
                    self.end_headers()
                    if self.command != "HEAD" and err_body:
                        self.wfile.write(err_body)
                finally:
                    exc.close()
            except Exception as exc:
                logger.warning(
                    "odl-hybrid upstream forward failed (%s): %s",
                    self.command,
                    type(exc).__name__,
                )
                self._send_json(502, {"error": "Failed to reach OpenDataLoader hybrid service"})

        do_GET = _forward
        do_POST = _forward
        do_PUT = _forward
        do_HEAD = _forward

    server = ThreadingHTTPServer(("127.0.0.1", 0), _HybridAuthForwardHandler)
    server.daemon_threads = True
    thread = threading.Thread(
        target=server.serve_forever,
        kwargs={"poll_interval": 0.05},
        name="odl-hybrid-auth-proxy",
        daemon=True,
    )
    thread.start()
    local_url = f"http://127.0.0.1:{server.server_port}"
    try:
        yield local_url
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2.0)


def _find_markdown_file(output_dir: str, pdf_stem: str) -> str:
    candidates = []
    for root, _, files in os.walk(output_dir):
        for name in files:
            if name.lower().endswith(".md"):
                path = os.path.join(root, name)
                candidates.append(path)
    if not candidates:
        raise FileNotFoundError(f"OpenDataLoader 未在 {output_dir} 生成 markdown 文件")
    for path in candidates:
        base = os.path.splitext(os.path.basename(path))[0]
        if base == pdf_stem or base.startswith(pdf_stem):
            return path
    candidates.sort(key=lambda p: os.path.getmtime(p), reverse=True)
    return candidates[0]


def _normalize_odl_image_url(raw: str) -> str:
    """OpenDataLoader wraps paths as ``<images/foo.png>``; storage may HTML-escape them."""
    s = html.unescape((raw or "").strip())
    s = s.replace("&lt;", "<").replace("&gt;", ">").replace("&amp;", "&")
    s = s.strip().strip("<>").strip().strip('"').strip("'")
    if s.startswith("./"):
        s = s[2:]
    return s.replace("\\", "/")


def _canonical_image_ref(abs_path: str, output_dir: str) -> str:
    """Use ``images/<file>`` keys to match OpenDataLoader markdown conventions."""
    rel = os.path.relpath(abs_path, output_dir).replace("\\", "/")
    name = os.path.basename(abs_path)
    if rel.startswith("images/"):
        return rel
    return f"images/{name}"


def _collect_images_under_output(output_dir: str) -> Dict[str, str]:
    """Collect every extracted image under the convert output tree."""
    images: Dict[str, str] = {}
    for root, _, files in os.walk(output_dir):
        for name in files:
            if not name.lower().endswith(_IMAGE_SUFFIXES):
                continue
            abs_path = os.path.join(root, name)
            ref = _canonical_image_ref(abs_path, output_dir)
            if ref in images:
                continue
            with open(abs_path, "rb") as f:
                images[ref] = base64.b64encode(f.read()).decode("utf-8")
    return images


def _register_image_alias(aliases: Dict[str, str], alias: str, canonical: str) -> None:
    key = _normalize_odl_image_url(alias)
    if key:
        aliases[key] = canonical


def _build_path_alias_map(images: Dict[str, str]) -> Dict[str, str]:
    """Map ODL markdown spellings (angle brackets, entities, basenames) to dict keys."""
    aliases: Dict[str, str] = {}
    for ref in images:
        base = os.path.basename(ref)
        variants = [
            ref,
            base,
            f"images/{base}",
            f"<{ref}>",
            f"<images/{base}>",
            f"&lt;{ref}&gt;",
            f"&lt;images/{base}&gt;",
        ]
        for variant in variants:
            _register_image_alias(aliases, variant, ref)
    return aliases


def _resolve_image_ref(url: str, aliases: Dict[str, str]) -> Optional[str]:
    key = _normalize_odl_image_url(url)
    if not key or key.startswith("data:"):
        return None
    if key in aliases:
        return aliases[key]
    base = os.path.basename(key)
    for candidate in (base, f"images/{base}"):
        if candidate in aliases:
            return aliases[candidate]
    m = _IMAGE_FILE_NUM_RE.match(base)
    if m:
        num = int(m.group(1))
        numbered = []
        for ref in {aliases[k] for k in aliases}:
            bm = _IMAGE_FILE_NUM_RE.match(os.path.basename(ref))
            if bm:
                numbered.append((int(bm.group(1)), ref))
        numbered.sort(key=lambda x: x[0])
        for n, ref in numbered:
            if n == num:
                return ref
        if numbered and 1 <= num <= len(numbered):
            return numbered[num - 1][1]
    return None


def _rewrite_markdown_image_refs(
    markdown: str, images: Dict[str, str]
) -> str:
    if not images:
        return markdown
    aliases = _build_path_alias_map(images)

    def repl(match: re.Match[str]) -> str:
        alt, raw_url = match.group(1), match.group(2)
        url = raw_url.strip().split()[0] if raw_url else ""
        canonical = _resolve_image_ref(url, aliases)
        if canonical is None:
            return match.group(0)
        return f"![{alt}]({canonical})"

    return _MD_IMAGE_RE.sub(repl, markdown)


def _kill_process_group(process: subprocess.Popen, pgid: Optional[int]) -> None:
    if pgid is None:
        try:
            process.kill()
        except OSError:
            pass
    else:
        for sig, wait_sec in ((signal.SIGTERM, 0.5), (signal.SIGKILL, 0.0)):
            try:
                os.killpg(pgid, sig)
            except OSError:
                pass
            if wait_sec > 0:
                deadline = time.monotonic() + wait_sec
                try:
                    process.wait(timeout=wait_sec)
                    rem = deadline - time.monotonic()
                    if rem > 0:
                        time.sleep(rem)
                except subprocess.TimeoutExpired:
                    pass
    try:
        process.wait(timeout=1.0)
    except subprocess.TimeoutExpired:
        try:
            process.kill()
        except OSError:
            pass
    finally:
        for pipe in (process.stdout, process.stderr):
            if pipe is not None:
                try:
                    pipe.close()
                except OSError:
                    pass


def _run_odl_jar_bounded(
    args: List[str],
    quiet: bool = True,
    *,
    timeout_seconds: Optional[int] = None,
    max_heap_mb: Optional[int] = None,
) -> str:
    """Execute opendataloader-pdf CLI JAR with -Xmx cap, hard timeout, and killpg."""
    effective_timeout = max(1, int(timeout_seconds or CONFIG.odl_timeout_seconds))
    effective_heap_mb = max(128, int(max_heap_mb or CONFIG.odl_java_max_heap_mb))
    jar_ref = resources.files("opendataloader_pdf").joinpath("jar", _JAR_NAME)
    with resources.as_file(jar_ref) as jar_path:
        command = [
            "java",
            f"-Xmx{effective_heap_mb}m",
            "-Djava.awt.headless=true",
            "-Dapple.awt.UIElement=true",
            "-Dhttp.followRedirects=false",
            "-jar",
            str(jar_path),
            *args,
        ]
        env = build_minimal_sandbox_env(proxy="")
        is_posix = os.name == "posix"
        process = subprocess.Popen(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            env=env,
            start_new_session=is_posix,
        )
        pgid = process.pid if is_posix else None
        try:
            stdout, stderr = process.communicate(timeout=effective_timeout)
        except subprocess.TimeoutExpired as exc:
            _kill_process_group(process, pgid)
            raise RuntimeError(
                f"OpenDataLoader CLI timed out after {effective_timeout}s"
            ) from exc

        if process.returncode != 0:
            raise subprocess.CalledProcessError(
                process.returncode,
                command,
                output=stdout,
                stderr=stderr,
            )
        return stdout


@contextlib.contextmanager
def _patched_odl_runner():
    """Patch opendataloader_pdf's JAR runner when backed by the real package."""
    targets = []
    for mod_name in ("opendataloader_pdf.runner", "opendataloader_pdf.convert_generated"):
        try:
            mod = __import__(mod_name, fromlist=["run_jar"])
        except Exception:
            mod = None
        if mod is not None and hasattr(mod, "run_jar"):
            targets.append((mod, getattr(mod, "run_jar")))

    for mod, _orig in targets:
        setattr(mod, "run_jar", _run_odl_jar_bounded)
    try:
        yield
    finally:
        for mod, orig in targets:
            setattr(mod, "run_jar", orig)


def _run_convert(
    pdf_path: str,
    output_dir: str,
    image_dir: str,
    overrides: Optional[Mapping[str, Any]] = None,
) -> None:
    if _has_hybrid_url_override(overrides):
        raise RuntimeError(
            "OpenDataLoader hybrid URL override is disallowed by SSRF security policy"
        )

    import opendataloader_pdf

    kwargs: Dict[str, Any] = {
        "input_path": pdf_path,
        "output_dir": output_dir,
        "format": "markdown",
        "image_output": "external",
        "image_dir": image_dir,
        "quiet": True,
        "markdown_with_html": _override_bool(
            overrides, "odl_markdown_with_html", CONFIG.odl_markdown_with_html
        ),
    }
    hybrid = _resolve_hybrid(overrides)
    upstream_hybrid_url = ""
    hybrid_timeout_ms = max(1000, int(CONFIG.odl_hybrid_timeout_ms))
    if hybrid and hybrid.lower() not in ("off", ""):
        kwargs["hybrid"] = hybrid
        hybrid_url = _resolve_hybrid_url(overrides)
        if hybrid_url:
            safe, reason = is_ssrf_safe_url(hybrid_url)
            if not safe:
                raise RuntimeError(
                    f"OpenDataLoader hybrid URL 被 SSRF 防护拦截: {reason}"
                )
            kwargs["hybrid_url"] = hybrid_url
            upstream_hybrid_url = hybrid_url
        kwargs["hybrid_timeout"] = str(hybrid_timeout_ms)
        hybrid_mode = _override_str(overrides, "odl_hybrid_mode", CONFIG.odl_hybrid_mode)
        if hybrid_mode:
            kwargs["hybrid_mode"] = hybrid_mode
        if _override_bool(overrides, "odl_hybrid_fallback", CONFIG.odl_hybrid_fallback):
            kwargs["hybrid_fallback"] = True

    auth_token = _resolve_hybrid_auth_token() if upstream_hybrid_url else ""
    with contextlib.ExitStack() as stack:
        if upstream_hybrid_url and auth_token:
            local_proxy_url = stack.enter_context(
                _local_hybrid_auth_proxy(
                    upstream_hybrid_url,
                    auth_token,
                    timeout_sec=max(5.0, hybrid_timeout_ms / 1000.0 + 5.0),
                )
            )
            kwargs["hybrid_url"] = local_proxy_url
        stack.enter_context(_patched_odl_runner())
        opendataloader_pdf.convert(**kwargs)


class OpenDataLoaderParser(BaseParser):
    """Parse PDFs with OpenDataLoader (layout-aware markdown + external images)."""

    def __init__(self, *args: Any, **kwargs: Any):
        nested_overrides = (
            kwargs.get("overrides")
            if isinstance(kwargs.get("overrides"), Mapping)
            else None
        )
        if _has_hybrid_url_override(kwargs) or _has_hybrid_url_override(
            nested_overrides
        ):
            raise RuntimeError(
                "OpenDataLoader hybrid URL override is disallowed by SSRF security policy"
            )
        merged_overrides: Dict[str, Any] = {}
        if nested_overrides:
            for k, v in nested_overrides.items():
                if k in ALLOWED_ODL_OVERRIDE_KEYS:
                    merged_overrides[k] = v
        for k, v in kwargs.items():
            if k in ALLOWED_ODL_OVERRIDE_KEYS:
                merged_overrides[k] = v
        self._engine_overrides = merged_overrides
        super().__init__(*args, **kwargs)

    def parse_into_text(self, content: bytes) -> Document:
        ok, msg = opendataloader_available(self._engine_overrides)
        if not ok:
            raise RuntimeError(msg)

        safe_name = os.path.basename(self.file_name) or "document.pdf"
        if not safe_name.lower().endswith(".pdf"):
            safe_name = f"{os.path.splitext(safe_name)[0] or 'document'}.pdf"
        pdf_stem = os.path.splitext(safe_name)[0]

        max_workers = CONFIG.odl_max_workers
        with parser_worker_limit("opendataloader", max_workers, context=self.context):
            with tempfile.TemporaryDirectory(prefix="genyee-odl-") as tmp_dir:
                pdf_path = os.path.join(tmp_dir, safe_name)
                with open(pdf_path, "wb") as f:
                    f.write(content)
                image_dir = os.path.join(tmp_dir, "images")
                os.makedirs(image_dir, exist_ok=True)

                _run_convert(
                    pdf_path,
                    tmp_dir,
                    image_dir,
                    overrides=self._engine_overrides,
                )

                md_path = _find_markdown_file(tmp_dir, pdf_stem)
                with open(md_path, encoding="utf-8", errors="replace") as f:
                    text = f.read()

                images = _collect_images_under_output(tmp_dir)
                text = _rewrite_markdown_image_refs(text, images)

        check_cancellation(self.context)
        if len(text.strip()) < _MIN_CHARS_PER_PAGE:
            logger.info(
                "OpenDataLoaderParser: %s yielded little text; "
                "falling back to builtin scanned renderer",
                self.file_name,
            )
            from docreader.parser.pdf_parser import PDFScannedParser

            return PDFScannedParser(
                file_name=self.file_name, file_type=self.file_type, context=self.context
            ).parse_into_text(content)

        logger.info(
            "OpenDataLoaderParser: %s -> content_len=%d images=%d",
            self.file_name,
            len(text),
            len(images),
        )
        return Document(
            content=text,
            images=images,
            metadata={
                "parser_engine": "opendataloader",
                "odl_hybrid": _resolve_hybrid(self._engine_overrides) or "off",
            },
        )
