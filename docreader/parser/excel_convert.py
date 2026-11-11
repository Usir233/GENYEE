"""LibreOffice helpers for normalizing legacy or unusual Excel uploads."""

from __future__ import annotations

import codecs
import logging
import os
import subprocess
import tempfile
import time
from typing import Optional

from docreader.parser.soffice_sandbox import (
    run_soffice_convert,
    sanitize_temp_suffix,
)

logger = logging.getLogger(__name__)

_XLS_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"
_ZIP_MAGIC = b"PK\x03\x04"


def detect_excel_format(content: bytes) -> str | None:
    """Return pandas/excel format id: xlsx, xls, xlsb, ods, or None."""
    if not content:
        return None

    from pandas.io.excel._base import inspect_excel_format

    ext = inspect_excel_format(content_or_path=content)
    if ext in ("xlsx", "xls", "xlsb", "ods"):
        return ext
    if ext == "zip":
        return "xlsx"

    if content.startswith(_ZIP_MAGIC):
        return "xlsx"
    if len(content) >= len(_XLS_MAGIC) and content.startswith(_XLS_MAGIC):
        return "xls"
    return None


def engine_for_format(ext: str | None) -> str:
    if ext == "xls":
        return "xlrd"
    if ext in ("xlsx", "xlsb"):
        return "openpyxl"
    if ext == "ods":
        return "odf"
    return "openpyxl"


def convert_excel_to_xlsx_bytes(content: bytes, suffix: str = ".xlsx") -> bytes | None:
    """Convert arbitrary spreadsheet bytes to XLSX using sandboxed LibreOffice."""
    soffice = find_soffice()
    if not soffice:
        return None

    safe_suffix = sanitize_temp_suffix(suffix, default=".xlsx")
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
                    convert_to="xlsx",
                    outdir=out_dir,
                    input_path=src,
                    profile_dir=profile_dir,
                    timeout=120,
                )
            except (OSError, RuntimeError) as exc:
                logger.warning("LibreOffice convert failed: %s", exc)
                return None

            if returncode != 0:
                stderr = stderr_bytes.decode("utf-8", errors="ignore")
                logger.warning(
                    "LibreOffice convert failed (attempt %s/%s): %s",
                    attempt,
                    max_attempts,
                    stderr,
                )
                if attempt < max_attempts:
                    time.sleep(0.5 * attempt)
                    continue
                return None

            for name in os.listdir(out_dir):
                if name.endswith(".xlsx"):
                    out_path = os.path.join(out_dir, name)
                    if os.path.islink(out_path) or not os.path.isfile(out_path):
                        continue
                    with open(out_path, "rb") as handle:
                        converted = handle.read()
                    logger.info(
                        "Converted spreadsheet via LibreOffice (%s -> xlsx, %d bytes)",
                        safe_suffix,
                        len(converted),
                    )
                    return converted

            if attempt < max_attempts:
                time.sleep(0.5 * attempt)
    return None


def normalize_excel_bytes(content: bytes, file_type: str | None = None) -> bytes:
    """Return bytes readable by pandas, converting via LibreOffice when needed."""
    ext = detect_excel_format(content)
    if ext is not None:
        return content

    # LibreOffice's text import accepts arbitrary binary junk as a one-cell
    # workbook. Only offer unrecognized *text* formats (CSV, HTML, SpreadsheetML)
    # to that fallback; known binary XLS/XLSX formats were handled above.
    sample = content[:8192]
    for bom, encoding in (
        (codecs.BOM_UTF32_LE, "utf-32"), (codecs.BOM_UTF32_BE, "utf-32"),
        (codecs.BOM_UTF16_LE, "utf-16"), (codecs.BOM_UTF16_BE, "utf-16"),
    ):
        if content.startswith(bom):
            try:
                sample = content.decode(encoding).encode("utf-8")[:8192]
            except UnicodeError as exc:
                raise ValueError("Invalid Unicode spreadsheet text") from exc
            break
    control_count = sum(byte < 32 and byte not in (9, 10, 12, 13) for byte in sample)
    if control_count > max(4, len(sample) // 100):
        raise ValueError("Unrecognized binary spreadsheet format")

    suffixes = []
    if file_type:
        suffixes.append(f".{file_type.lstrip('.')}")
    suffixes.extend([".xlsx", ".xls", ".et", ".csv"])
    seen: set[str] = set()
    for suffix in suffixes:
        if suffix in seen:
            continue
        seen.add(suffix)
        converted = convert_excel_to_xlsx_bytes(content, suffix=suffix)
        if converted and detect_excel_format(converted) is not None:
            return converted

    raise ValueError(
        "Unrecognized Excel file format; the file may be corrupt, encrypted, "
        "or not a spreadsheet"
    )


def find_soffice() -> Optional[str]:
    possible_paths = [
        "/usr/bin/soffice",
        "/usr/lib/libreoffice/program/soffice",
        "/opt/libreoffice25.2/program/soffice",
        "/Applications/LibreOffice.app/Contents/MacOS/soffice",
        "C:\\Program Files\\LibreOffice\\program\\soffice.exe",
        "C:\\Program Files (x86)\\LibreOffice\\program\\soffice.exe",
    ]
    for path in possible_paths:
        if path and os.path.exists(path):
            return path

    result = subprocess.run(["which", "soffice"], capture_output=True, text=True)
    if result.returncode == 0 and result.stdout.strip():
        return result.stdout.strip()
    return None
