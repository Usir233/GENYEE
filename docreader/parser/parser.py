import logging
from collections.abc import Mapping
from typing import Any, cast

from docreader.models.document import Document
from docreader.parser.archive import validate_zip_content
from docreader.parser.concurrency import check_cancellation
from docreader.parser.registry import registry
from docreader.parser.stream import ParsedDocumentStream
from docreader.parser.web_parser import WebParser

logger = logging.getLogger(__name__)

# Strict allowlist of request-level parser_engine_overrides keys accepted by docreader.
# Outbound endpoint overrides such as odl_hybrid_url are never allowed from requests.
ALLOWED_ENGINE_OVERRIDE_KEYS: frozenset[str] = frozenset(
    {
        "pdf_force_scanned",
        "xlsx_percent_as_text",
        "odl_hybrid",
        "odl_hybrid_mode",
        "odl_hybrid_fallback",
        "odl_markdown_with_html",
        "extract_images",
        "enable_multimodal",
        "parser_max_workers",
    }
)

FORBIDDEN_ENGINE_OVERRIDE_KEYS: frozenset[str] = frozenset(
    {
        "odl_hybrid_url",
    }
)


def sanitize_engine_overrides(
    overrides: Mapping[str, Any] | None,
    *,
    reject_forbidden: bool = True,
) -> dict[str, Any]:
    """Validate and filter request-supplied parser_engine_overrides against allowlist."""
    if not overrides:
        return {}

    cleaned: dict[str, Any] = {}
    dropped: list[str] = []
    for raw_key, value in overrides.items():
        key = str(raw_key)
        if key in FORBIDDEN_ENGINE_OVERRIDE_KEYS:
            if value is not None and str(value).strip() != "":
                if reject_forbidden:
                    raise RuntimeError(
                        f"Parser engine override '{key}' is disallowed by SSRF security policy"
                    )
                dropped.append(key)
            continue
        if key in ALLOWED_ENGINE_OVERRIDE_KEYS:
            cleaned[key] = value
        else:
            dropped.append(key)

    if dropped:
        logger.warning(
            "Dropped non-whitelisted parser_engine_overrides keys: %s",
            ", ".join(sorted(dropped)),
        )
    return cleaned


# OLE Compound File magic used by legacy binary Microsoft Office files.
# Some WPS/Word documents keep this payload while being renamed to .docx.
_OLE_COMPOUND_FILE_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"


def detect_effective_file_type(file_type: str, content: bytes) -> str:
    """Return the parser file type after checking trustworthy file magic.

    OOXML ``.docx`` files are ZIP containers, while legacy ``.doc`` files
    use the OLE Compound File format.  Word and WPS tolerate a legacy file
    renamed to ``.docx``, so route that well-known mismatch through the DOC
    parser instead of feeding binary OLE data to the DOCX parser.
    """
    normalized = file_type.lower().lstrip(".")
    if normalized == "docx" and content.startswith(_OLE_COMPOUND_FILE_MAGIC):
        logger.warning("Detected legacy DOC content with a DOCX extension; using DOC parser")
        return "doc"
    return normalized


class Parser:
    """Document parser facade (lightweight version).

    Converts files/URLs to markdown + image references.
    No chunking, no storage, no OCR, no VLM.
    """

    def __init__(self):
        self.registry = registry
        logger.info(
            "Parser initialized with engines: %s",
            ", ".join(self.registry.get_engine_names()),
        )

    def parse_file(
        self,
        file_name: str,
        file_type: str,
        content: bytes,
        parser_engine: str | None = None,
        engine_overrides: dict[str, Any] | None = None,
        context: Any = None,
    ) -> Document:
        """Parse file content to markdown."""
        engine = parser_engine or ""
        overrides = sanitize_engine_overrides(engine_overrides, reject_forbidden=True)
        logger.info(
            "Parsing file: %s, type: %s, engine: %s",
            file_name,
            file_type,
            engine or "builtin",
        )

        check_cancellation(context)
        validate_zip_content(content)
        effective_file_type = detect_effective_file_type(file_type, content)
        cls = self.registry.get_parser_class(engine, effective_file_type)
        logger.info(
            "Creating %s parser instance for %s file",
            cls.__name__,
            effective_file_type,
        )
        parser = cls(
            file_name=file_name,
            file_type=effective_file_type,
            **{**overrides, "context": context},
        )

        logger.info("Starting to parse file content, size: %d bytes", len(content))
        result = parser.parse(content)

        if not result.content:
            logger.warning("Parser returned empty content for file: %s", file_name)
        logger.info("Parsed file %s, content length=%d", file_name, len(result.content))
        return result

    def parse_file_stream(
        self,
        file_name: str,
        file_type: str,
        content: bytes,
        parser_engine: str | None = None,
        engine_overrides: dict[str, Any] | None = None,
        context: Any = None,
    ) -> ParsedDocumentStream:
        """Parse a file for ReadStream, using a parser's lazy image path when available."""
        engine = parser_engine or ""
        overrides = sanitize_engine_overrides(engine_overrides, reject_forbidden=True)
        check_cancellation(context)
        validate_zip_content(content)
        effective_file_type = detect_effective_file_type(file_type, content)
        cls = self.registry.get_parser_class(engine, effective_file_type)
        parser = cls(
            file_name=file_name,
            file_type=effective_file_type,
            **{**overrides, "context": context},
        )

        parse_stream = getattr(parser, "parse_stream", None)
        if callable(parse_stream):
            return cast(ParsedDocumentStream, parse_stream(content))

        # Non-streaming parsers retain their established behavior. Their images
        # are still sent one at a time by ReadStream, but parsing itself may
        # materialize them before the first frame.
        result = parser.parse(content)
        return ParsedDocumentStream(
            document=result,
            image_count=len(result.images or {}),
        )

    def parse_url(
        self,
        url: str,
        title: str,
        parser_engine: str | None = None,
        engine_overrides: dict[str, Any] | None = None,
        context: Any = None,
    ) -> Document:
        """Parse content from a URL to markdown."""
        logger.info("Parsing URL: %s, title: %s", url, title)

        check_cancellation(context)
        parser = WebParser(title=title, context=context)
        logger.info("Starting to parse URL content")
        result = parser.parse(url.encode())

        if not result.content:
            logger.warning("Parser returned empty content for url: %s", url)
        logger.info("Parsed url %s, content length=%d", url, len(result.content))
        return result
