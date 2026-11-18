from __future__ import annotations

import base64
import io
import logging
import os
import warnings
from urllib.parse import quote

from PIL import Image, UnidentifiedImageError

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.utils.image_ref import image_markdown

logger = logging.getLogger(__name__)

MAX_IMAGE_PIXELS = 40_000_000
MAX_IMAGE_FRAMES = 256
_ALLOWED_PIL_FORMATS = frozenset({"PNG", "JPEG", "GIF", "WEBP", "BMP", "TIFF", "MPO"})


def _validate_image_payload(content: bytes) -> None:
    """Validate that standalone image bytes represent a safe bounded raster image."""
    if not content:
        raise ValueError("Invalid image file: empty content")

    prev_max = Image.MAX_IMAGE_PIXELS
    Image.MAX_IMAGE_PIXELS = MAX_IMAGE_PIXELS
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("error", Image.DecompressionBombWarning)
            with Image.open(io.BytesIO(content)) as img:
                fmt = (img.format or "").upper()
                if fmt not in _ALLOWED_PIL_FORMATS:
                    raise ValueError(f"Unsupported or invalid image format: {fmt or 'unknown'}")
                width, height = img.size
                if width <= 0 or height <= 0 or width * height > MAX_IMAGE_PIXELS:
                    raise ValueError(
                        f"Image dimensions ({width}x{height}) exceed "
                        f"pixel budget ({MAX_IMAGE_PIXELS})"
                    )
                n_frames = int(getattr(img, "n_frames", 1) or 1)
                if n_frames > MAX_IMAGE_FRAMES or width * height * n_frames > MAX_IMAGE_PIXELS * 2:
                    raise ValueError("Image frame budget exceeded")
                img.verify()
    except (
        UnidentifiedImageError,
        Image.DecompressionBombError,
        Image.DecompressionBombWarning,
        OSError,
        SyntaxError,
        EOFError,
    ) as exc:
        raise ValueError(f"Invalid or corrupt image file: {exc}") from exc
    finally:
        Image.MAX_IMAGE_PIXELS = prev_max


def _safe_image_filename(file_name: str, file_type: str | None) -> str:
    base = os.path.basename(file_name) if file_name else ""
    if base in ("", ".", ".."):
        ext = os.path.basename(file_type or "").lstrip(".") or "png"
        if ext in (".", "..") or "/" in ext or "\\" in ext:
            ext = "png"
        return f"image.{ext}"
    return base


class ImageParser(BaseParser):
    """Parser for standalone image files.

    Returns the image as a markdown reference with the raw image data
    in Document.images so that the Go-side ImageResolver (or main.py's
    _resolve_images) can handle storage upload.
    """

    def parse_into_text(self, content: bytes) -> Document:
        logger.info("Parsing image file=%s, size=%d bytes", self.file_name, len(content))
        _validate_image_payload(content)

        filename = _safe_image_filename(self.file_name, self.file_type)
        ref_path = f"images/{quote(filename, safe='')}"

        text = image_markdown(ref_path)
        images = {ref_path: base64.b64encode(content).decode()}

        return Document(content=text, images=images)
