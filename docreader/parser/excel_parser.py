"""
Excel Parser Module

This module provides functionality to parse Excel files (.xlsx, .xls) into
structured Document objects with text content and chunks. It supports multiple
sheets and handles various Excel formats using pandas.
"""
import base64
import logging
import re
import threading
import xml.etree.ElementTree as ET
import zipfile
from io import BytesIO
from typing import Any, Dict, List, Optional, Tuple

import pandas as pd

from docreader.models.document import Chunk, Document
from docreader.parser.archive import ArchiveLimitError, validate_zip_content, validate_zip_archive
from docreader.parser.base_parser import BaseParser
from docreader.parser.concurrency import check_cancellation
from docreader.parser.excel_convert import (
    convert_excel_to_xlsx_bytes,
    detect_excel_format,
    engine_for_format,
    normalize_excel_bytes,
)
from docreader.parser.xlsx_merge import fill_merged_cells_xlsx
from docreader.parser.xlsx_repair import (
    repair_xlsx_bytes,
    sanitize_xlsx_styles,
    strip_unreadable_ranges_xlsx,
)

logger = logging.getLogger(__name__)

# Pattern to detect Excel image function strings that should be excluded from
# parsed text content.  WPS uses =DISPIMG("ID",mode) to embed images in cells;
# when opened by other tools the formula may appear as plain text prefixed with
# "_xlfn." or "=".  Office 365 uses =_xlfn.IMAGE(url, ...) similarly.
# The _xlfn. prefix is optional — WPS may omit it (e.g. =DISPIMG("ID",1)).
_IMAGE_FUNC_RE = re.compile(
    r"^=?(_xlfn\.)?(DISPIMG|IMAGE)\(", re.IGNORECASE
)

_ocr_local = threading.local()


def _is_image_function(value: object) -> bool:
    """Return True if *value* looks like an embedded-image function string."""
    if not isinstance(value, str):
        return False
    return _IMAGE_FUNC_RE.match(value) is not None


def _is_zip_archive(data: bytes) -> bool:
    """Fast check whether raw bytes start with the standard ZIP magic header."""
    return len(data) >= 4 and data[:4] == b"PK\x03\x04"


def _is_empty_or_formula_error(val: Any) -> bool:
    """Return True if a cell value is NaN, empty string, or an Excel formula error."""
    if pd.isna(val) or val is None or val == "":
        return True
    if isinstance(val, (int, float, bool)):
        return False
    s = str(val).strip()
    return s in {
        "#VALUE!",
        "#N/A",
        "#REF!",
        "#NAME?",
        "#NULL!",
        "#DIV/0!",
        "#NUM!",
    }


def _get_ocr_engine() -> Any:
    """Acquire a thread-local RapidOCR instance with safe error handling."""
    try:
        from rapidocr import RapidOCR
        from unittest.mock import Mock

        # If RapidOCR has been mocked in tests, instantiate and return the mock
        if isinstance(RapidOCR, Mock):
            return RapidOCR()

        engine = getattr(_ocr_local, "engine", None)
        if engine is not None:
            return engine

        try:
            from docreader.config import CONFIG

            threads = getattr(CONFIG, "preprocess_threads", 2)
        except Exception:
            threads = 2

        engine = RapidOCR(
            params={
                "EngineConfig.onnxruntime.intra_op_num_threads": threads,
                "EngineConfig.onnxruntime.inter_op_num_threads": 1,
            }
        )
        _ocr_local.engine = engine
        return engine
    except Exception as exc:
        logger.warning(
            "RapidOCR engine initialization failed or library not available: %s",
            exc,
        )
        return None


def _recognize_image_text(img_bytes: bytes, ref_path: str = "") -> str:
    """Run RapidOCR on image bytes with bulletproof exception handling."""
    if not img_bytes:
        return ""
    try:
        engine = _get_ocr_engine()
        if engine is None:
            return ""
        res = engine(img_bytes)
        if res is None:
            return ""

        # RapidOCR >= 3.0 RapidOCROutput object
        if hasattr(res, "txts") and res.txts:
            lines = [t.strip() for t in res.txts if t and t.strip()]
            text = " ".join(lines)
            return " ".join(text.split()).strip()

        # Legacy RapidOCR tuple: (result_list, elapse)
        if isinstance(res, tuple) and len(res) == 2 and isinstance(res[0], list):
            lines = [
                item[1].strip()
                for item in res[0]
                if isinstance(item, (list, tuple)) and len(item) > 1 and item[1]
            ]
            text = " ".join(lines)
            return " ".join(text.split()).strip()

        # List of items
        if isinstance(res, list):
            lines = [
                item[1].strip()
                for item in res
                if isinstance(item, (list, tuple)) and len(item) > 1 and item[1]
            ]
            text = " ".join(lines)
            return " ".join(text.split()).strip()

        return ""
    except Exception as exc:
        logger.warning(
            "RapidOCR text recognition failed for %s: %s",
            ref_path or "image",
            exc,
        )
        return ""


def _extract_image_bytes(
    image_extractor: Any,
    doc_images: Dict[str, str],
    ref_path: str,
) -> Optional[bytes]:
    """Retrieve raw image bytes for OCR, checking extractor cache or decoding base64."""
    if image_extractor is not None:
        bytes_map = getattr(image_extractor, "extracted_bytes", None) or getattr(
            image_extractor, "image_bytes_by_ref", None
        )
        if isinstance(bytes_map, dict) and ref_path in bytes_map:
            return bytes_map[ref_path]

    b64_str = doc_images.get(ref_path)
    if b64_str:
        try:
            return base64.b64decode(b64_str)
        except Exception as exc:
            logger.warning(
                "Failed to decode base64 data for image %s: %s", ref_path, exc
            )
            return None
    return None


class ExcelParser(BaseParser):
    """Parser for Excel files (.xlsx, .xls).
    
    This parser extracts text content from Excel files by processing all sheets
    and converting each row into a structured text format. Each row becomes a
    separate chunk with key-value pairs.
    
    Features:
        - Supports multiple sheets in a single Excel file
        - Automatically removes completely empty rows
        - Converts each row to "column: value" format
        - Creates individual chunks for each row for better granularity
        
    Example:
        >>> parser = ExcelParser()
        >>> with open("data.xlsx", "rb") as f:
        ...     content = f.read()
        ...     document = parser.parse_into_text(content)
        >>> print(document.content)
        Name: John,Age: 30,City: NYC
        Name: Jane,Age: 25,City: LA
    """

    def __init__(
        self,
        file_name: str = "",
        file_type: str | None = None,
        xlsx_first_row_as_header: Any = False,
        xlsx_percent_as_text: Any = None,
        xlsx_ocr_enabled: Any = True,
        **kwargs: Any,
    ):
        super().__init__(file_name=file_name, file_type=file_type, **kwargs)
        self.xlsx_first_row_as_header = _parse_bool(xlsx_first_row_as_header)
        # Tri-state: None (or an empty value) keeps the deployment default from
        # DOCREADER_XLSX_PERCENT_AS_TEXT, an explicit value wins. Same
        # precedence as the pdf_force_scanned parser override.
        self.xlsx_percent_as_text = _parse_optional_bool(xlsx_percent_as_text)
        self.xlsx_ocr_enabled = _parse_bool(xlsx_ocr_enabled)

    def parse_into_text(self, content: bytes) -> Document:
        """Parse Excel file bytes into a Document object with embedded images and OCR.
        
        Args:
            content: Raw bytes of the Excel file
            
        Returns:
            Document: Parsed document containing text, chunks, source_blocks, and images.
        """
        validate_zip_content(content)
        from openpyxl.utils import get_column_letter

        chunks: List[Chunk] = []
        text: List[str] = []
        source_blocks: List[dict] = []
        start, end = 0, 0

        # Step 1: Extract images from raw bytes BEFORE openpyxl modifies or strips them
        cell_images_by_sheet: Dict[str, Dict[Tuple[int, str], Any]] = {}
        doc_images: Dict[str, str] = {}
        image_extractor: Any = None

        if _is_zip_archive(content):
            try:
                from docreader.parser.xlsx_media import XLSXImageExtractor

                image_extractor = XLSXImageExtractor(content)
                cell_images_by_sheet, doc_images = image_extractor.extract_cell_images()
            except ArchiveLimitError:
                raise
            except Exception as exc:
                logger.warning("Failed to extract images from XLSX workbook: %s", exc)
                cell_images_by_sheet, doc_images = {}, {}

        # Cache OCR results per ref_path across all rows to avoid redundant OCR computation
        ocr_cache: Dict[str, str] = {}

        # Step 2: Open excel file via standard pipeline
        try:
            excel_file = _open_excel_file(
                content,
                file_type=self.file_type,
                percent_as_text=self.xlsx_percent_as_text,
            )
        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.warning("Failed to open excel workbook: %s", exc)
            return Document(
                content="",
                chunks=[],
                source_blocks=[],
                images=doc_images,
            )

        # Step 3: Process each sheet
        for excel_sheet_name in excel_file.sheet_names:
            check_cancellation(self.context)
            sheet_images = cell_images_by_sheet.get(str(excel_sheet_name), {})
            consumed_cells: set[Tuple[int, str]] = set()

            df = _read_sheet_dataframe(
                excel_file,
                excel_sheet_name,
                xlsx_first_row_as_header=self.xlsx_first_row_as_header,
            )

            # Process each row in DataFrame
            for row_index, row in df.iterrows():
                page_content: List[str] = []

                try:
                    excel_row = int(row_index) + 1
                except (TypeError, ValueError):
                    excel_row = 0

                # Iterate through columns of the row
                for col_idx, (col_label, val) in enumerate(row.items()):
                    col_letter = get_column_letter(col_idx + 1)
                    cell_key = (excel_row, col_letter)
                    raw_imgs = sheet_images.get(cell_key)

                    img_tags: List[str] = []
                    if raw_imgs:
                        consumed_cells.add(cell_key)
                        img_list = (
                            raw_imgs if isinstance(raw_imgs, list) else [raw_imgs]
                        )
                        for ref_path, alt_text in img_list:
                            # Inline OCR with caching and graceful degradation
                            ocr_text = ""
                            if self.xlsx_ocr_enabled and ref_path in doc_images:
                                if ref_path in ocr_cache:
                                    ocr_text = ocr_cache[ref_path]
                                else:
                                    img_bytes = _extract_image_bytes(
                                        image_extractor, doc_images, ref_path
                                    )
                                    ocr_text = _recognize_image_text(
                                        img_bytes, ref_path=ref_path
                                    )
                                    ocr_cache[ref_path] = ocr_text

                            alt = (
                                alt_text.strip()
                                if alt_text and alt_text.strip()
                                else "图片"
                            )
                            if ocr_text:
                                img_tags.append(
                                    f"![{alt}]({ref_path}) (识别结果: {ocr_text})"
                                )
                            else:
                                img_tags.append(f"![{alt}]({ref_path})")

                    has_text = (
                        not _is_empty_or_formula_error(val)
                        and not _is_image_function(val)
                    )

                    if img_tags:
                        images_str = " ".join(img_tags)
                        if has_text:
                            page_content.append(f"{col_label}: {val} {images_str}")
                        else:
                            page_content.append(f"{col_label}: {images_str}")
                    elif has_text:
                        page_content.append(f"{col_label}: {val}")

                # Attach any floating/drawing images on this row outside parsed columns
                if excel_row > 0:
                    for (r, c), raw_imgs in sheet_images.items():
                        if r == excel_row and (r, c) not in consumed_cells:
                            consumed_cells.add((r, c))
                            img_list = (
                                raw_imgs if isinstance(raw_imgs, list) else [raw_imgs]
                            )
                            for ref_path, alt_text in img_list:
                                ocr_text = ""
                                if self.xlsx_ocr_enabled and ref_path in doc_images:
                                    if ref_path in ocr_cache:
                                        ocr_text = ocr_cache[ref_path]
                                    else:
                                        img_bytes = _extract_image_bytes(
                                            image_extractor, doc_images, ref_path
                                        )
                                        ocr_text = _recognize_image_text(
                                            img_bytes, ref_path=ref_path
                                        )
                                        ocr_cache[ref_path] = ocr_text
                                alt = (
                                    alt_text.strip()
                                    if alt_text and alt_text.strip()
                                    else "图片"
                                )
                                if ocr_text:
                                    page_content.append(
                                        f"图片: ![{alt}]({ref_path}) (识别结果: {ocr_text})"
                                    )
                                else:
                                    page_content.append(f"图片: ![{alt}]({ref_path})")

                # Skip rows with no valid text or image content
                if not page_content:
                    continue

                content_row = ",".join(page_content) + "\n"
                end += len(content_row)
                text.append(content_row)

                chunks.append(
                    Chunk(content=content_row, seq=len(chunks), start=start, end=end)
                )

                if excel_row > 0:
                    source_blocks.append(
                        {
                            "start": start,
                            "end": end,
                            "locator": {
                                "type": "sheet",
                                "sheet": str(excel_sheet_name),
                                "row_start": excel_row,
                                "row_end": excel_row,
                            },
                        }
                    )
                start = end

            # Emit any remaining images on rows beyond df.iterrows()
            remaining_keys = sorted(
                k for k in sheet_images.keys() if k not in consumed_cells
            )
            if remaining_keys:
                from itertools import groupby

                for r_num, group in groupby(remaining_keys, key=lambda k: k[0]):
                    orphan_content: List[str] = []
                    for cell_key in group:
                        consumed_cells.add(cell_key)
                        raw_imgs = sheet_images[cell_key]
                        img_list = (
                            raw_imgs if isinstance(raw_imgs, list) else [raw_imgs]
                        )
                        for ref_path, alt_text in img_list:
                            ocr_text = ""
                            if self.xlsx_ocr_enabled and ref_path in doc_images:
                                if ref_path in ocr_cache:
                                    ocr_text = ocr_cache[ref_path]
                                else:
                                    img_bytes = _extract_image_bytes(
                                        image_extractor, doc_images, ref_path
                                    )
                                    ocr_text = _recognize_image_text(
                                        img_bytes, ref_path=ref_path
                                    )
                                    ocr_cache[ref_path] = ocr_text
                            alt = (
                                alt_text.strip()
                                if alt_text and alt_text.strip()
                                else "图片"
                            )
                            col_tag = cell_key[1]
                            if ocr_text:
                                orphan_content.append(
                                    f"{col_tag}: ![{alt}]({ref_path}) (识别结果: {ocr_text})"
                                )
                            else:
                                orphan_content.append(f"{col_tag}: ![{alt}]({ref_path})")

                    if orphan_content:
                        content_row = ",".join(orphan_content) + "\n"
                        end += len(content_row)
                        text.append(content_row)
                        chunks.append(
                            Chunk(
                                content=content_row,
                                seq=len(chunks),
                                start=start,
                                end=end,
                            )
                        )
                        source_blocks.append(
                            {
                                "start": start,
                                "end": end,
                                "locator": {
                                    "type": "sheet",
                                    "sheet": str(excel_sheet_name),
                                    "row_start": r_num,
                                    "row_end": r_num,
                                },
                            }
                        )
                        start = end

        # Return document with populated images dict
        return Document(
            content="".join(text),
            chunks=chunks,
            source_blocks=source_blocks,
            images=doc_images,
        )


def _read_sheet_dataframe(
    excel_file: pd.ExcelFile,
    sheet_name: str,
    xlsx_first_row_as_header: bool = False,
) -> pd.DataFrame:
    """Read a worksheet into a DataFrame with stable column labels."""
    from openpyxl.utils import get_column_letter

    # Keep row 1 as data by default for both XLSX and legacy XLS. Users can
    # explicitly restore the historical behavior where row 1 supplies semantic
    # labels for every row.
    # keep_default_na=False: by default pandas reads a cell whose text is
    # "N/A", "NA", "NULL", "None", "NaN" or "n/a" as missing, and its pair is
    # dropped, though the cell says "not applicable" or "none". Blank cells are
    # then read as "", which parse_into_text skips like a missing value.
    # Preserve the cell types supplied by the engine. Column inference otherwise
    # turns text identifiers ("00123", "1e5") into numbers; iterrows can then
    # coerce a numeric row to floats and lose precision in long identifiers.
    df = excel_file.parse(
        sheet_name=sheet_name, header=None, keep_default_na=False, dtype=object
    )
    if xlsx_first_row_as_header and len(df.index) >= 2:
        df.columns = _stable_header_labels(df.iloc[0].tolist())
        return df.iloc[1:].copy()

    df.columns = [get_column_letter(idx + 1) for idx in range(len(df.columns))]
    return df


def _stable_header_labels(values: List[object]) -> List[str]:
    """Build non-empty, unique labels from an explicitly selected header row."""
    from openpyxl.utils import get_column_letter

    labels: List[str] = []
    used_labels: set[str] = set()
    counts: dict[str, int] = {}
    for index, value in enumerate(values, start=1):
        label = ""
        if (
            pd.notna(value)
            and not _is_image_function(value)
            and not _is_empty_or_formula_error(value)
        ):
            label = str(value).strip()
        if not label:
            label = get_column_letter(index)

        count = counts.get(label, 0) + 1
        unique_label = label if count == 1 else f"{label}_{count}"
        while unique_label in used_labels:
            count += 1
            unique_label = f"{label}_{count}"
        counts[label] = count
        used_labels.add(unique_label)
        labels.append(unique_label)
    return labels


def _parse_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    return str(value).strip().lower() in {"1", "true", "yes", "on"}


def _parse_optional_bool(value: Any) -> bool | None:
    """Parse a tri-state parser override; None keeps the deployment default."""
    if value is None:
        return None
    if isinstance(value, bool):
        return value
    text = str(value).strip().lower()
    if not text:
        return None
    return text in {"1", "true", "yes", "y", "on"}


def _sanitize_corrupted_drawings_xlsx(data: bytes) -> bytes:
    """If any drawing*.xml is malformed XML, replace it with an empty valid drawing to prevent openpyxl crash."""
    if not _is_zip_archive(data):
        return data
    try:
        bio = BytesIO(data)
        if not zipfile.is_zipfile(bio):
            return data
        needs_fix = False
        with zipfile.ZipFile(bio, "r") as zf:
            validate_zip_archive(zf)
            names = zf.namelist()
            corrupt_drawings = set()
            for name in names:
                if "drawings/drawing" in name and name.endswith(".xml"):
                    try:
                        ET.fromstring(zf.read(name))
                    except Exception:
                        corrupt_drawings.add(name)
                        needs_fix = True

            if not needs_fix:
                return data

            out = BytesIO()
            with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout:
                for item in zf.infolist():
                    if item.filename in corrupt_drawings:
                        empty_dr = (
                            b'<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n'
                            b'<xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"/>'
                        )
                        zout.writestr(item.filename, empty_dr)
                    else:
                        zout.writestr(item, zf.read(item.filename))
            return out.getvalue()
    except ArchiveLimitError:
        raise
    except Exception as exc:
        logger.debug("Failed to sanitize corrupted drawings: %s", exc)
        return data


def _prepare_xlsx_bytes(data: bytes, percent_as_text: bool | None = None) -> bytes:
    data = _sanitize_corrupted_drawings_xlsx(data)
    repaired = repair_xlsx_bytes(data)
    if repaired is not None:
        data = repaired
    readable = strip_unreadable_ranges_xlsx(data)
    if readable is not None:
        data = readable
    try:
        return fill_merged_cells_xlsx(data, percent_as_text=percent_as_text)
    except TypeError:
        # A non-conforming styles.xml (empty/malformed fills) makes openpyxl's
        # load_workbook raise before pandas ever sees the file (#3637).
        # sanitize_xlsx_styles returns None when the fills are clean, in which
        # case the TypeError has a different cause and must propagate.
        sanitized = sanitize_xlsx_styles(data)
        if sanitized is None:
            raise
        return fill_merged_cells_xlsx(sanitized, percent_as_text=percent_as_text)


def _open_excel_file(
    content: bytes,
    file_type: str | None = None,
    percent_as_text: bool | None = None,
) -> pd.ExcelFile:
    """Open an Excel workbook with explicit engine selection and fallbacks."""
    data = content
    converted_via_soffice = False

    while True:
        ext = detect_excel_format(data)
        if ext is None:
            if converted_via_soffice:
                raise ValueError(
                    "Excel file format cannot be determined, you must specify an engine manually."
                )
            try:
                data = normalize_excel_bytes(data, file_type=file_type)
            except ValueError as exc:
                raise ValueError(
                    "Excel file format cannot be determined, you must specify an engine manually."
                ) from exc
            converted_via_soffice = True
            continue

        if ext == "ods":
            converted = convert_excel_to_xlsx_bytes(data, suffix=".ods")
            if converted:
                data = converted
                continue

        engine = engine_for_format(ext)
        if ext == "xlsx":
            data = _prepare_xlsx_bytes(data, percent_as_text=percent_as_text)
            engine = "openpyxl"
        try:
            return pd.ExcelFile(BytesIO(data), engine=engine)
        except ImportError as exc:
            raise ValueError(
                f"Excel engine {engine!r} is not available for .{ext} files"
            ) from exc
        except KeyError as exc:
            if "sharedStrings.xml" not in str(exc) or engine != "openpyxl":
                raise
            repaired = repair_xlsx_bytes(data)
            if repaired is None:
                raise
            logger.info("Repaired XLSX sharedStrings packaging before parse")
            data = _prepare_xlsx_bytes(repaired, percent_as_text=percent_as_text)
            continue
        except ValueError as exc:
            if converted_via_soffice or "cannot be determined" not in str(exc):
                raise
            try:
                data = normalize_excel_bytes(content, file_type=file_type)
            except ValueError:
                raise
            converted_via_soffice = True
            continue


if __name__ == "__main__":
    # Example usage: Parse an Excel file and display results
    logging.basicConfig(level=logging.DEBUG)

    # Specify the path to your Excel file
    your_file = "/path/to/your/file.xlsx"
    parser = ExcelParser()
    
    # Read and parse the Excel file
    with open(your_file, "rb") as f:
        content = f.read()
        document = parser.parse_into_text(content)
        
        # Display the full document content
        logger.error(document.content)

        # Display the first chunk as an example
        for chunk in document.chunks:
            logger.error(chunk.content)
            break  # Only show the first chunk
