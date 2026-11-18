"""Extract and rasterize images embedded in PPTX when MarkItDown cannot inline them."""

from __future__ import annotations

import base64
import contextlib
import io
import logging
import os
import re
import shutil
import signal
import subprocess
import tempfile
import uuid
import zipfile
from collections import deque
from typing import Any

try:
    from PIL import Image as _PILImage

    _PILImage.MAX_IMAGE_PIXELS = 40_000_000
except ImportError:  # pragma: no cover
    pass

from docreader.parser.archive import (
    MAX_COMPRESSION_RATIO,
    MAX_UNCOMPRESSED_ARCHIVE_BYTES,
    safe_read_zip_member,
)
from docreader.parser.archive import (
    validate_zip_archive as _validate_zip_archive,
)

logger = logging.getLogger(__name__)

MAX_IMAGE_PIXELS = 40_000_000
_MARKDOWN_IMAGE = re.compile(r"!\[([^\]]*)\]\(([^)]+)\)")
_RASTER_EXT = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff"}
_VECTOR_EXT = {".wmf", ".emf", ".svg", ".eps"}


def _find_convert() -> str | None:
    for path in ("/usr/bin/convert", "/usr/local/bin/convert"):
        if os.path.isfile(path) and os.access(path, os.X_OK):
            return path
    return shutil.which("convert")


# MED-15: Exclude vector/delegate formats (eps/svg/wmf/emf) to block ImageMagick
# Ghostscript / rsvg / libwmf delegate RCE, LFI, and memory-corruption vectors.
ALLOWED_CONVERT_FORMATS = {
    "tif",
    "tiff",
    "png",
    "jpg",
    "jpeg",
    "bmp",
    "gif",
    "webp",
}


def _rasterize_with_imagemagick(data: bytes, suffix: str) -> bytes | None:
    convert = _find_convert()
    if not convert:
        return None
    fmt = suffix.lstrip(".").lower()
    if fmt not in ALLOWED_CONVERT_FORMATS:
        logger.warning("Disallowed or untrusted media format for convert: %s", fmt)
        return None
    clean_env = {
        "PATH": "/usr/local/bin:/usr/bin:/bin",
        "LANG": "C.UTF-8",
        "LC_ALL": "C.UTF-8",
        "MAGICK_THREAD_LIMIT": "2",
        "MAGICK_AREA_LIMIT": "40MP",
        "MAGICK_MEMORY_LIMIT": "256MiB",
        "MAGICK_MAP_LIMIT": "512MiB",
        "MAGICK_DISK_LIMIT": "512MiB",
    }
    with tempfile.TemporaryDirectory() as temp_dir:
        clean_env["TMPDIR"] = temp_dir
        src = os.path.join(temp_dir, f"input.{fmt}")
        dst = os.path.join(temp_dir, "output.png")
        with open(src, "wb") as handle:
            handle.write(data)
        proc: subprocess.Popen[bytes] | None = None
        try:
            # Prefix format explicitly to prevent delegate execution exploits
            proc = subprocess.Popen(
                [convert, f"{fmt}:{src}", f"png:{dst}"],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                env=clean_env,
                start_new_session=True,
            )
            _, stderr_bytes = proc.communicate(timeout=60)
        except subprocess.TimeoutExpired as exc:
            if proc is not None:
                try:
                    os.killpg(proc.pid, signal.SIGKILL)
                except OSError:
                    with contextlib.suppress(OSError):
                        proc.kill()
                with contextlib.suppress(subprocess.TimeoutExpired, OSError):
                    proc.wait(timeout=1.0)
            logger.warning("ImageMagick convert timed out: %s", exc)
            return None
        except (OSError, subprocess.SubprocessError) as exc:
            logger.warning("ImageMagick convert failed: %s", exc)
            return None
        finally:
            if proc is not None:
                for pipe in (proc.stdout, proc.stderr):
                    if pipe is not None:
                        with contextlib.suppress(OSError):
                            pipe.close()
        if proc.returncode != 0 or not os.path.isfile(dst):
            stderr = (stderr_bytes or b"").decode("utf-8", errors="ignore")
            logger.warning("ImageMagick convert exit %s: %s", proc.returncode, stderr)
            return None
        with open(dst, "rb") as handle:
            return handle.read()


def _rasterize_with_pillow(data: bytes) -> bytes | None:
    try:
        from PIL import Image
    except ImportError:
        return None
    Image.MAX_IMAGE_PIXELS = MAX_IMAGE_PIXELS
    try:
        with Image.open(io.BytesIO(data)) as opened_img:
            if opened_img.width * opened_img.height > MAX_IMAGE_PIXELS:
                logger.warning(
                    "Image pixel count (%d) exceeds MAX_IMAGE_PIXELS (%d)",
                    opened_img.width * opened_img.height,
                    MAX_IMAGE_PIXELS,
                )
                return None
            out = io.BytesIO()
            # These PNG-compatible modes retain alpha channels or palette transparency.
            if opened_img.mode not in ("RGB", "RGBA", "L", "LA", "P"):
                converted_img = opened_img.convert("RGB")
                try:
                    converted_img.save(out, format="PNG")
                finally:
                    converted_img.close()
            else:
                opened_img.save(out, format="PNG")
            return out.getvalue()
    except Exception as exc:
        logger.debug("Pillow could not open media bytes: %s", exc)
        return None


def rasterize_media_bytes(name: str, data: bytes) -> bytes | None:
    ext = os.path.splitext(name)[1].lower()
    if ext in _VECTOR_EXT:
        logger.warning("Skipping disallowed vector/delegate media format: %s", ext)
        return None
    if ext in _RASTER_EXT:
        png = _rasterize_with_pillow(data)
        if png:
            return png
        converted = _rasterize_with_imagemagick(data, ext or ".bin")
        if converted:
            return converted
        return None
    # Unknown extension: try Pillow safely (never delegate unknown extensions to ImageMagick)
    return _rasterize_with_pillow(data)


def list_pptx_media(pptx_bytes: bytes) -> list[tuple[str, bytes]]:
    """Return (zip path, raw bytes) for each file under ppt/media/, in archive order."""
    items: list[tuple[str, bytes]] = []
    with zipfile.ZipFile(io.BytesIO(pptx_bytes)) as archive:
        _validate_zip_archive(archive)
        for info in archive.infolist():
            name = info.filename
            if not name.startswith("ppt/media/"):
                continue
            base = os.path.basename(name)
            if not base or base.startswith("."):
                continue
            data = safe_read_zip_member(
                archive,
                info,
                max_member_bytes=MAX_UNCOMPRESSED_ARCHIVE_BYTES,
                max_total_bytes=MAX_UNCOMPRESSED_ARCHIVE_BYTES,
                max_ratio=MAX_COMPRESSION_RATIO,
            )
            items.append((name, data))
    return items


def extract_pptx_media_rasterized(pptx_bytes: bytes) -> list[bytes]:
    """Rasterize all ppt/media assets to PNG bytes, skipping failures."""
    rasterized: list[bytes] = []
    for path, raw in list_pptx_media(pptx_bytes):
        png = rasterize_media_bytes(os.path.basename(path), raw)
        if png:
            rasterized.append(png)
            logger.info("Rasterized pptx media %s (%d -> %d bytes)", path, len(raw), len(png))
        else:
            logger.warning("Failed to rasterize pptx media %s", path)
    return rasterized


def _is_unresolved_image_ref(url: str) -> bool:
    if not url or url.startswith("data:") or url.startswith("images/"):
        return False
    return not url.startswith(("http://", "https://"))


def _picture_sources(pptx_bytes: bytes) -> dict[str, deque[tuple[str, bytes] | None]]:
    """Resolve MarkItDown picture placeholders in slide/shape order."""
    from pptx import Presentation
    from pptx.enum.shapes import MSO_SHAPE_TYPE
    from pptx.exc import PythonPptxError

    sources: dict[str, deque[tuple[str, bytes] | None]] = {}

    def visit(shapes: Any) -> None:
        # Match MarkItDown, which emits shapes (and group members) sorted by
        # position rather than in z-order.
        for shape in sorted(
            shapes,
            key=lambda x: (
                float("-inf") if not x.top else x.top,
                float("-inf") if not x.left else x.left,
            ),
        ):
            if shape.shape_type == MSO_SHAPE_TYPE.GROUP:
                visit(shape.shapes)
                continue
            is_picture = shape.shape_type == MSO_SHAPE_TYPE.PICTURE or (
                shape.shape_type == MSO_SHAPE_TYPE.PLACEHOLDER and hasattr(shape, "image")
            )
            if not is_picture:
                continue

            # MarkItDown's no-data-URI mode uses the shape name, not the media
            # part name. Names may repeat across slides or normalize equally.
            placeholder = re.sub(r"\W", "", shape.name) + ".jpg"
            source = None
            try:
                blip = shape._element.blipFill.blip
                relationship = blip.rEmbed if blip is not None else None
                if relationship:
                    part = shape.part.related_part(relationship)
                    # Read the part directly: shape.image.content_type can
                    # require a decoder for the same vector format that
                    # triggered fallback.
                    source = (str(part.partname), part.blob)
            except (AttributeError, KeyError, ValueError, PythonPptxError) as exc:
                # A missing blipFill or blip, a dangling or external r:embed,
                # or a media part dropped at load time must only lose this
                # picture, not the whole document. None keeps the queue
                # position.
                logger.warning("Skipping unresolvable pptx picture %s: %s", shape.name, exc)
            sources.setdefault(placeholder, deque()).append(source)

    presentation = Presentation(io.BytesIO(pptx_bytes))
    for slide in presentation.slides:
        visit(slide.shapes)
    return sources


def attach_pptx_media_to_markdown(markdown: str, pptx_bytes: bytes) -> tuple[str, dict[str, str]]:
    """Attach the media referenced by MarkItDown's fallback picture placeholders."""
    sources = _picture_sources(pptx_bytes)
    if not sources:
        return markdown, {}

    images: dict[str, str] = {}
    converted: dict[str, bytes | None] = {}

    def repl(match: re.Match[str]) -> str:
        alt, url = match.group(1), match.group(2)
        if not _is_unresolved_image_ref(url):
            return match.group(0)
        pending = sources.get(url)
        if not pending:
            return match.group(0)
        source = pending.popleft()
        if source is None:
            return match.group(0)
        name, raw = source
        if name not in converted:
            converted[name] = rasterize_media_bytes(name, raw)
            if converted[name] is None:
                logger.warning("Failed to rasterize pptx media %s", name)
        png = converted[name]
        if not png:
            return match.group(0)
        ref = f"images/{uuid.uuid4()}.png"
        images[ref] = base64.b64encode(png).decode()
        return f"![{alt}]({ref})"

    return _MARKDOWN_IMAGE.sub(repl, markdown), images


def markdown_needs_pptx_media_attach(markdown: str) -> bool:
    return any(_is_unresolved_image_ref(m.group(2)) for m in _MARKDOWN_IMAGE.finditer(markdown))
