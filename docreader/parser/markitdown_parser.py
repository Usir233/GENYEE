import io
import logging
import time
import zipfile
from typing import BinaryIO

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.archive import (
    detect_ooxml_package_type,
    verify_zip_stream_limits,
)
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.parser.concurrency import parser_worker_limit
from docreader.parser.docx_merge import fill_vertical_merged_cells_docx
from docreader.parser.markdown_parser import MarkdownParser
from docreader.parser.ppt_convert import normalize_ppt_bytes
from docreader.parser.pptx_media import (
    attach_pptx_media_to_markdown,
    markdown_needs_pptx_media_attach,
)
from docreader.parser.xlsx_repair import (
    sanitize_xlsx_styles,
    strip_unreadable_ranges_xlsx,
)
from docreader.parser.xlsx_rows import validate_table_expansion
from markitdown import MarkItDown
from markitdown._stream_info import StreamInfo
from markitdown.converters import (
    AudioConverter,
    BingSerpConverter,
    EpubConverter,
    ImageConverter,
    IpynbConverter,
    OutlookMsgConverter,
    RssConverter,
    WikipediaConverter,
    YouTubeConverter,
    ZipConverter,
)

logger = logging.getLogger(__name__)

_DISABLED_CONVERTER_TYPES = (
    ZipConverter,
    EpubConverter,
    OutlookMsgConverter,
    IpynbConverter,
    AudioConverter,
    ImageConverter,
    BingSerpConverter,
    YouTubeConverter,
    WikipediaConverter,
    RssConverter,
)

_EXPLICIT_STREAM_INFOS: dict[str, StreamInfo] = {
    ".xlsx": StreamInfo(
        extension=".xlsx",
        mimetype="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    ),
    ".xlsm": StreamInfo(
        extension=".xlsx",
        mimetype="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
    ),
    ".xls": StreamInfo(
        extension=".xls",
        mimetype="application/vnd.ms-excel",
    ),
    ".docx": StreamInfo(
        extension=".docx",
        mimetype="application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    ),
    ".pptx": StreamInfo(
        extension=".pptx",
        mimetype="application/vnd.openxmlformats-officedocument.presentationml.presentation",
    ),
    ".pdf": StreamInfo(
        extension=".pdf",
        mimetype="application/pdf",
    ),
    ".csv": StreamInfo(
        extension=".csv",
        mimetype="text/csv",
        charset="utf-8",
    ),
    ".md": StreamInfo(
        extension=".md",
        mimetype="text/markdown",
        charset="utf-8",
    ),
    ".markdown": StreamInfo(
        extension=".markdown",
        mimetype="text/markdown",
        charset="utf-8",
    ),
    ".txt": StreamInfo(
        extension=".txt",
        mimetype="text/plain",
        charset="utf-8",
    ),
    ".text": StreamInfo(
        extension=".txt",
        mimetype="text/plain",
        charset="utf-8",
    ),
}

_UNGUARDED_SNIFF_EXTENSIONS = frozenset(
    {".xlsx", ".xlsm", ".xltx", ".xltm", ".xls", ".zip", ".epub"}
)
_UNGUARDED_SNIFF_MIME_TOKENS = (
    "spreadsheetml",
    "ms-excel",
    "zip",
    "epub",
)


class _BoundedMarkItDown(MarkItDown):
    """MarkItDown wrapper that disables nested archive expansion and cross-format sniffing."""

    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._converters = [
            reg
            for reg in self._converters
            if not isinstance(reg.converter, _DISABLED_CONVERTER_TYPES)
        ]

    def _get_stream_info_guesses(
        self, file_stream: BinaryIO, base_guess: StreamInfo
    ) -> list[StreamInfo]:
        ext_lower = (base_guess.extension or "").lower()
        if ext_lower and not ext_lower.startswith("."):
            ext_lower = "." + ext_lower
        explicit = _EXPLICIT_STREAM_INFOS.get(ext_lower)
        if explicit is not None:
            return [explicit]

        guesses = super()._get_stream_info_guesses(file_stream, base_guess)
        safe_guesses: list[StreamInfo] = []
        for guess in guesses:
            g_ext = (guess.extension or "").lower()
            g_mime = (guess.mimetype or "").lower()
            if g_ext in _UNGUARDED_SNIFF_EXTENSIONS:
                continue
            if any(token in g_mime for token in _UNGUARDED_SNIFF_MIME_TOKENS):
                continue
            safe_guesses.append(guess)
        return safe_guesses


def _inspect_zip_content(content: bytes) -> str | None:
    """Validate streaming decompression limits and return OOXML package type if ZIP."""
    if not content:
        return None
    try:
        if not zipfile.is_zipfile(io.BytesIO(content)):
            return None
        with zipfile.ZipFile(io.BytesIO(content), "r") as archive:
            verify_zip_stream_limits(archive)
            return detect_ooxml_package_type(archive)
    except zipfile.BadZipFile:
        return None


class StdMarkitdownParser(BaseParser):
    """
    Standard MarkItDown Parser Wrapper

    This parser uses the markitdown library to convert various document formats
    (docx, pptx, pdf, etc.) into text/markdown.
    """

    def __init__(self, *args, **kwargs):
        # 这里的 super() 会调用 BaseParser 的初始化，确保 self.file_type 被正确赋值
        super().__init__(*args, **kwargs)
        self.markitdown = _BoundedMarkItDown()

    def parse_into_text(self, content: bytes) -> Document:
        """
        Parses content using MarkItDown.
        Uses self.file_type (inherited from BaseParser) to hint the stream format.
        """
        ext = self.file_type
        ft = (ext or "").lstrip(".").lower()
        pptx_bytes: bytes | None = None
        preprocess_started = time.monotonic()

        ooxml_type = _inspect_zip_content(content)
        has_spreadsheet_xml = ooxml_type == "xlsx"
        xlsx = ft in ("xlsx", "xlsm") or has_spreadsheet_xml

        if not xlsx and ft in ("ppt", "pptx"):
            content, ext = normalize_ppt_bytes(content, ft)
            post_ooxml_type = _inspect_zip_content(content)
            if post_ooxml_type == "xlsx":
                has_spreadsheet_xml = True
                xlsx = True
            else:
                pptx_bytes = content
                ft = "pptx"
        elif not xlsx and ft == "docx":
            content = fill_vertical_merged_cells_docx(content)
        elif not xlsx and ft == "csv":
            from docreader.utils.endecode import decode_bytes

            decoded_text = decode_bytes(content)
            content = decoded_text.encode("utf-8")

        if xlsx:
            # MarkItDown reads spreadsheets through pandas/openpyxl, so the
            # ranges openpyxl cannot parse (#3599) and the non-conforming
            # styles.xml fills (#3637) that break the builtin ExcelParser break
            # this engine too. Strip the ranges first: the fill repair alone
            # would still leave the range TypeError. Both passes return None
            # for clean workbooks.
            readable = strip_unreadable_ranges_xlsx(content)
            if readable is not None:
                content = readable
            sanitized = sanitize_xlsx_styles(content)
            if sanitized is not None:
                content = sanitized
            logger.info(
                "MarkItDown XLSX preprocessing completed in %.3fs (bytes=%d)",
                time.monotonic() - preprocess_started,
                len(content),
            )
        if ext and not ext.startswith("."):
            ext = "." + ext
        elif has_spreadsheet_xml and not ext:
            ext = ".xlsx"

        # Embedded data URIs can make spreadsheet output and subsequent chunking
        # disproportionately expensive. XLSX conversion therefore starts with
        # them disabled; all other formats retain the historical image-first
        # behavior and its single bounded fallback.
        first_keep_data_uris = not xlsx
        with parser_worker_limit("markitdown", CONFIG.markitdown_max_workers, context=self.context):
            if xlsx:
                # Bound admission work with the same worker limit as conversion.
                # Raise outside its exception wrapper so ArchiveLimitError stays
                # explicit; do not truncate or switch the requested engine.
                validate_table_expansion(
                    content, CONFIG.markitdown_xlsx_max_table_cells, self.context
                )
                if has_spreadsheet_xml and ft and ft not in ("xlsx", "xlsm"):
                    raise ValueError(
                        f"OOXML spreadsheet content does not match declared file type {ft!r}"
                    )
            started = time.monotonic()
            try:
                result = self._convert_markitdown(content, ext, keep_data_uris=first_keep_data_uris)
            except Exception as exc:
                logger.exception(
                    "MarkItDown conversion failed (format=%s bytes=%d keep_data_uris=%s)",
                    ft or ext,
                    len(content),
                    first_keep_data_uris,
                )
                raise RuntimeError(
                    f"MarkItDown conversion failed for {ft or ext or 'document'}"
                ) from exc
            logger.info(
                "MarkItDown conversion completed in %.3fs "
                "(format=%s bytes=%d text=%d keep_data_uris=%s)",
                time.monotonic() - started,
                ft or ext,
                len(content),
                len(result.text_content) if result is not None else 0,
                first_keep_data_uris,
            )
            if result is None and first_keep_data_uris:
                logger.warning(
                    "MarkItDown failed with embedded images for %s; retrying without data URIs",
                    ft or ext,
                )
                started = time.monotonic()
                try:
                    result = self._convert_markitdown(content, ext, keep_data_uris=False)
                except Exception as exc:
                    logger.exception(
                        "MarkItDown fallback failed (format=%s bytes=%d)",
                        ft or ext,
                        len(content),
                    )
                    raise RuntimeError(
                        f"MarkItDown conversion failed for {ft or ext or 'document'}"
                    ) from exc
                logger.info(
                    "MarkItDown fallback completed in %.3fs "
                    "(format=%s bytes=%d text=%d keep_data_uris=false)",
                    time.monotonic() - started,
                    ft or ext,
                    len(content),
                    len(result.text_content) if result is not None else 0,
                )

        if result is None:
            raise RuntimeError(f"MarkItDown conversion failed for {ft or ext or 'document'}")
        text = result.text_content
        images: dict[str, str] = {}
        if pptx_bytes is not None and markdown_needs_pptx_media_attach(text):
            text, images = attach_pptx_media_to_markdown(text, pptx_bytes)
        return Document(content=text, images=images)

    def _convert_markitdown(self, content: bytes, ext: str | None, *, keep_data_uris: bool):
        try:
            return self.markitdown.convert(
                io.BytesIO(content),
                file_extension=ext,
                keep_data_uris=keep_data_uris,
            )
        except Exception:
            if keep_data_uris:
                return None
            raise


class MarkitdownParser(PipelineParser):
    _parser_cls = (StdMarkitdownParser, MarkdownParser)
