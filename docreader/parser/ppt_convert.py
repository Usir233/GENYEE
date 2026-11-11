"""LibreOffice helpers for legacy binary PowerPoint (.ppt) uploads."""

from __future__ import annotations

import logging
import os
import tempfile
import time

from docreader.parser.excel_convert import find_soffice
from docreader.parser.soffice_sandbox import (
    run_soffice_convert,
    sanitize_temp_suffix,
)

logger = logging.getLogger(__name__)

_OLE_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"
_ZIP_MAGIC = b"PK\x03\x04"


def is_ole_compound(content: bytes) -> bool:
    return len(content) >= len(_OLE_MAGIC) and content.startswith(_OLE_MAGIC)


def is_zip_openxml(content: bytes) -> bool:
    return len(content) >= len(_ZIP_MAGIC) and content.startswith(_ZIP_MAGIC)


def needs_ppt_to_pptx_conversion(content: bytes, file_type: str | None) -> bool:
    """True when content is legacy .ppt (OLE), not modern .pptx (ZIP)."""
    ext = (file_type or "").lstrip(".").lower()
    if ext == "pptx" or is_zip_openxml(content):
        return False
    if ext == "ppt" or is_ole_compound(content):
        return is_ole_compound(content) or ext == "ppt"
    return False


def convert_ppt_to_pptx_bytes(content: bytes, suffix: str = ".ppt") -> bytes | None:
    """Convert legacy PowerPoint bytes to PPTX using sandboxed LibreOffice."""
    soffice = find_soffice()
    if not soffice:
        return None

    safe_suffix = sanitize_temp_suffix(suffix, default=".ppt")
    max_attempts = 3
    for attempt in range(1, max_attempts + 1):
        with tempfile.TemporaryDirectory() as temp_dir, tempfile.TemporaryDirectory() as profile_dir:
            src_dir = os.path.join(temp_dir, "in")
            out_dir = os.path.join(temp_dir, "out")
            os.makedirs(src_dir, exist_ok=True)
            os.makedirs(out_dir, exist_ok=True)
            src = os.path.join(src_dir, f"input{safe_suffix}")
            with open(src, "wb") as handle:
                handle.write(content)

            try:
                _stdout, stderr_bytes, returncode = run_soffice_convert(
                    soffice,
                    convert_to="pptx",
                    outdir=out_dir,
                    input_path=src,
                    profile_dir=profile_dir,
                    timeout=120,
                )
            except (OSError, RuntimeError) as exc:
                logger.warning("LibreOffice PPT convert failed: %s", exc)
                return None

            if returncode != 0:
                stderr = stderr_bytes.decode("utf-8", errors="ignore")
                logger.warning(
                    "LibreOffice PPT convert failed (attempt %s/%s): %s",
                    attempt,
                    max_attempts,
                    stderr,
                )
                if attempt < max_attempts:
                    time.sleep(0.5 * attempt)
                    continue
                return None

            for name in os.listdir(out_dir):
                if name.endswith(".pptx"):
                    out_path = os.path.join(out_dir, name)
                    if os.path.islink(out_path) or not os.path.isfile(out_path):
                        continue
                    with open(out_path, "rb") as handle:
                        converted = handle.read()
                    logger.info(
                        "Converted presentation via LibreOffice (%s -> pptx, %d bytes)",
                        safe_suffix,
                        len(converted),
                    )
                    return converted

            if attempt < max_attempts:
                time.sleep(0.5 * attempt)
    return None


def normalize_ppt_bytes(content: bytes, file_type: str | None) -> tuple[bytes, str]:
    """Return (bytes, extension) suitable for MarkItDown (pptx when converted)."""
    ext = (file_type or "").lstrip(".").lower()

    if is_zip_openxml(content):
        return content, ".pptx"

    if not needs_ppt_to_pptx_conversion(content, ext):
        dotted = f".{ext}" if ext else ".pptx"
        return content, dotted

    suffix = ".ppt" if ext in ("", "ppt") else f".{ext}"
    converted = convert_ppt_to_pptx_bytes(content, suffix=suffix)
    if converted:
        return converted, ".pptx"

    raise ValueError(
        "Legacy PowerPoint (.ppt) is not supported by MarkItDown directly; "
        "LibreOffice is required to convert it to .pptx. Install LibreOffice "
        "(soffice) in the docreader environment or upload .pptx instead."
    )
