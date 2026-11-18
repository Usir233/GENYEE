import contextlib
import logging
import os
import re
import tempfile
import threading
import time
import traceback
from concurrent.futures import (
    ProcessPoolExecutor as _RealProcessPoolExecutor,
)
from concurrent.futures import (
    ThreadPoolExecutor,
    as_completed,
)
from dataclasses import dataclass, field
from io import BytesIO
from typing import Any

from docx import Document
from docx.image.exceptions import (
    InvalidImageStreamError,
    UnexpectedEndOfFileError,
    UnrecognizedImageError,
)
from docx.opc.oxml import parse_xml
from docx.opc.pkgreader import _SerializedRelationship, _SerializedRelationships
from docx.oxml.ns import qn
from docx.table import Table
from PIL import Image

from docreader.config import CONFIG
from docreader.models.document import Document as DocumentModel
from docreader.parser.archive import ArchiveLimitError
from docreader.parser.base_parser import BaseParser
from docreader.parser.concurrency import ParserCancelled
from docreader.utils import endecode

ProcessPoolExecutor = _RealProcessPoolExecutor


def _select_mp_context():
    """Select a fork-safe multiprocessing context for multithreaded gRPC servers.

    ``forkserver`` (and ``spawn`` where ``forkserver`` is unavailable) avoids
    calling bare ``os.fork()`` inside a multithreaded gRPC process where locks
    such as ``cygrpc`` or PDFium may be held by concurrent threads.
    """
    import multiprocessing as mp

    for method in ("forkserver", "spawn"):
        try:
            return mp.get_context(method)
        except ValueError:
            continue
    return None


# patch from https://github.com/python-openxml/python-docx/issues/1105#issuecomment-1298075246
def load_from_xml_v2(baseURI, rels_item_xml):
    """
    Return |_SerializedRelationships| instance loaded with the
    relationships contained in *rels_item_xml*. Returns an empty
    collection if *rels_item_xml* is |None|.
    """
    srels = _SerializedRelationships()
    if rels_item_xml is not None:
        rels_elm = parse_xml(rels_item_xml)
        for rel_elm in rels_elm.Relationship_lst:
            if rel_elm.target_ref in ("../NULL", "NULL"):
                continue
            srels._srels.append(_SerializedRelationship(baseURI, rel_elm))
    return srels


_SerializedRelationships.load_from_xml = staticmethod(  # type: ignore[method-assign]
    load_from_xml_v2
)
Image.MAX_IMAGE_PIXELS = 40_000_000

logger = logging.getLogger(__name__)

_W_P = qn("w:p")
_W_TBL = qn("w:tbl")
_W_R = qn("w:r")
_W_SDT = qn("w:sdt")
_W_SDT_PR = qn("w:sdtPr")
_W_SDT_CONTENT = qn("w:sdtContent")
_W_CUSTOM_XML = qn("w:customXml")

# Inline wrappers whose runs Word shows as part of the paragraph. python-docx's
# Paragraph.text reads only w:r and w:hyperlink children, so the text of a
# tracked insertion (w:ins) or move destination (w:moveTo), a smart tag,
# custom XML markup or a simple field was dropped, and that of an inline
# content control (w:sdt, read through _sdt_content). Deletions (w:del,
# w:moveFrom) hold their text in w:delText, not w:t, and stay out, which is
# the document as it reads with changes accepted.
_INLINE_TEXT_CONTAINERS = frozenset(
    qn(tag)
    for tag in (
        "w:hyperlink",
        "w:ins",
        "w:moveTo",
        "w:smartTag",
        "w:customXml",
        "w:fldSimple",
    )
)


def _sdt_content(sdt: Any) -> Any | None:
    """The w:sdtContent of a content control that holds document text, or None.

    A control still showing its placeholder ("Click or tap here to enter
    text.") holds none, and a table of contents repeats the headings with
    their page numbers, so both are left out as before.
    """
    props = sdt.find(_W_SDT_PR)
    if props is not None:
        if props.find(qn("w:showingPlcHdr")) is not None:
            return None
        gallery = props.find(f"{qn('w:docPartObj')}/{qn('w:docPartGallery')}")
        if gallery is not None and gallery.get(qn("w:val")) == "Table of Contents":
            return None
    return sdt.find(_W_SDT_CONTENT)


def _paragraph_text(p_element: Any) -> str:
    """The text of a w:p as Word shows it, wrapped runs included."""
    parts: list[str] = []

    def walk(parent: Any) -> None:
        for child in parent:
            if child.tag == _W_R:
                run_text = getattr(child, "text", None)
                if isinstance(run_text, str):
                    parts.append(run_text)
            elif child.tag == _W_SDT:
                content = _sdt_content(child)
                if content is not None:
                    walk(content)
            elif child.tag in _INLINE_TEXT_CONTAINERS:
                walk(child)

    walk(p_element)
    return "".join(parts)


def _block_children(container: Any):
    """Yield the paragraphs and tables of a body or cell in document order.

    A block content control (w:sdt) and block custom XML hold paragraphs and
    tables of their own, which Word shows in place; they are opened instead of
    skipped. Yields ``(element, direct)``, where ``direct`` is False for an
    element found inside one of them.
    """
    for child in container:
        if child.tag in (_W_P, _W_TBL):
            yield child, True
        elif child.tag == _W_SDT:
            content = _sdt_content(child)
            if content is not None:
                for element, _ in _block_children(content):
                    yield element, False
        elif child.tag == _W_CUSTOM_XML:
            for element, _ in _block_children(child):
                yield element, False


def _collect_document_paragraphs(
    doc: Any,
) -> tuple[list[tuple[Any, bool]], list[Any]]:
    """Collect all paragraphs in reading order across body, SDTs, and tables.

    Returns ``(paragraphs, retained_elements)`` where each entry in
    ``paragraphs`` is ``(p_element, is_body_level)`` and ``retained_elements``
    keeps XML element proxies alive for stable lookup while composing output.
    """
    paragraphs: list[tuple[Any, bool]] = []
    retained: list[Any] = []
    seen_tc: set = set()

    def walk_container(container: Any, parent: Any, is_body_level: bool) -> None:
        for element, _ in _block_children(container):
            if element.tag == _W_P:
                paragraphs.append((element, is_body_level))
                retained.append(element)
            elif element.tag == _W_TBL:
                table = Table(element, parent)
                for row in table.rows:
                    for cell in row.cells:
                        tc = cell._tc
                        if tc in seen_tc:
                            continue
                        seen_tc.add(tc)
                        retained.append(tc)
                        walk_container(tc, cell, False)

    walk_container(doc.element.body, doc._body, True)
    return paragraphs, retained


def _paragraph_has_page_break(p_element: Any) -> bool:
    """Return True when a paragraph XML element carries a page or section break."""
    xml = p_element.xml
    if "lastRenderedPageBreak" not in xml and 'type="page"' not in xml and "sectPr" not in xml:
        return False
    return bool(
        p_element.xpath(".//w:lastRenderedPageBreak | .//w:br[@w:type='page'] | .//w:sectPr")
    )


def _cell_text(
    cell: Any,
    para_index_by_elem: dict[Any, int] | None = None,
    images_by_paragraph: dict[int, list[str]] | None = None,
    allowed_paragraphs: set | None = None,
    emitted_image_paras: set | None = None,
) -> str:
    """Keep cell paragraphs, cell images, and nested table text in document order."""
    parts: list[str] = []
    for element, _ in _block_children(cell._tc):
        if element.tag == _W_P:
            p_text = _paragraph_text(element)
            img_refs: list[str] = []
            if para_index_by_elem is not None and images_by_paragraph:
                p_idx = para_index_by_elem.get(element)
                if (
                    p_idx is not None
                    and (allowed_paragraphs is None or p_idx in allowed_paragraphs)
                    and (emitted_image_paras is None or p_idx not in emitted_image_paras)
                ):
                    img_refs = images_by_paragraph.get(p_idx, [])
                    if img_refs and emitted_image_paras is not None:
                        emitted_image_paras.add(p_idx)
            if p_text and img_refs:
                parts.append(f"{p_text} {' '.join(img_refs)}")
            elif img_refs:
                parts.append(" ".join(img_refs))
            else:
                parts.append(p_text)
        elif element.tag == _W_TBL:
            for row in Table(element, cell).rows:
                parts.append(
                    " ".join(
                        _cell_text(
                            nested,
                            para_index_by_elem=para_index_by_elem,
                            images_by_paragraph=images_by_paragraph,
                            allowed_paragraphs=allowed_paragraphs,
                            emitted_image_paras=emitted_image_paras,
                        )
                        for nested in row.cells
                    )
                )
    return "\n".join(parts)


def _gfm_cell_text(text: str | None) -> str:
    """Collapse whitespace and escape pipes so a cell stays one GFM column."""
    return re.sub(r"\s+", " ", text or "").strip().replace("|", "\\|")


def table_to_gfm_markdown(
    table: Any,
    para_index_by_elem: dict[Any, int] | None = None,
    images_by_paragraph: dict[int, list[str]] | None = None,
    allowed_paragraphs: set | None = None,
    emitted_image_paras: set | None = None,
) -> str:
    """Render a python-docx Table as GFM Markdown.

    ``row.cells`` already expands merged regions to one entry per grid slot
    (repeating the same Cell object). Emitting every slot keeps column
    alignment; comparing cell text would collapse adjacent equal/empty cells.
    """
    rows: list[list[str]] = []
    for row in table.rows:
        cells = [
            _gfm_cell_text(
                _cell_text(
                    cell,
                    para_index_by_elem=para_index_by_elem,
                    images_by_paragraph=images_by_paragraph,
                    allowed_paragraphs=allowed_paragraphs,
                    emitted_image_paras=emitted_image_paras,
                )
            )
            for cell in row.cells
        ]
        if cells:
            # Omitted edge cells occupy grid positions but are absent from
            # row.cells; retain them so values stay under the right headers.
            rows.append([""] * row.grid_cols_before + cells + [""] * row.grid_cols_after)
    if not rows:
        return ""
    width = max(len(row) for row in rows)
    if width == 0:
        return ""
    padded = [row + [""] * (width - len(row)) for row in rows]
    lines = [
        "| " + " | ".join(padded[0]) + " |",
        "| " + " | ".join("---" for _ in padded[0]) + " |",
    ]
    for row in padded[1:]:
        lines.append("| " + " | ".join(row) + " |")
    return "\n".join(lines)


class ImageData:
    """Represents a processed image of document content"""

    local_path: str = ""
    object: Image.Image | None = None
    url: str = ""
    paragraph_index: int = -1


@dataclass
class LineData:
    """Represents a processed line of document content with associated images"""

    text: str = ""  # Extracted text content
    images: list[ImageData] = field(default_factory=list)  # List of images or image paths
    extra_info: str = ""  # Placeholder for additional info (currently unused)
    page_num: int = 0  # Page number
    content_sequence: list[tuple[str, Any]] = field(
        default_factory=list
    )  # Sequence of content items (text/images)


class DocxParser(BaseParser):
    """DOCX document parser"""

    def __init__(
        self,
        max_pages: int | None = None,  # Maximum number of pages to process
        **kwargs,
    ):
        """Initialize DOCX document parser

        Args:
            file_name: File name
            file_type: File type, if None, infer from file name
            enable_multimodal: Whether to enable multimodal processing
            chunk_size: Chunk size
            chunk_overlap: Chunk overlap
            separators: List of separators
            ocr_backend: OCR engine type
            ocr_config: OCR engine configuration
            max_image_size: Maximum image size limit
            max_concurrent_tasks: Maximum number of concurrent tasks
            max_pages: Maximum number of pages to process
        """
        super().__init__(**kwargs)
        self.max_pages = CONFIG.docx_max_pages if max_pages is None else max_pages
        if self.max_pages <= 0:
            self.max_pages = 100000  # no limit (matches Docx.__call__ default)
        logger.info(f"DocxParser initialized with max_pages={self.max_pages}")

    def parse_into_text(self, content: bytes) -> DocumentModel:
        """Parse DOCX document, extract text content and image Markdown links"""
        logger.info(f"Parsing DOCX document, content size: {len(content)} bytes")
        logger.info(f"Max pages limit set to: {self.max_pages}")

        if content.startswith(b"PK"):
            import zipfile

            from docreader.parser.docx_merge import _validate_zip_archive

            try:
                with zipfile.ZipFile(BytesIO(content)) as archive:
                    _validate_zip_archive(archive)
            except zipfile.BadZipFile:
                pass

        start_time = time.time()
        # Use concurrent processing to handle the document
        max_workers = min(
            4, os.cpu_count() or 2
        )  # Reduce thread count to avoid excessive memory consumption
        logger.info(f"Setting max_workers to {max_workers} for document processing")

        try:
            inline_images: dict[str, str] = {}

            def _inline_upload(local_path: str) -> str:
                """Read temp image file, base64-encode, and return a ref path.

                The Go-side ImageResolver (or main.py _resolve_images) handles
                actual storage upload from Document.images.
                """
                import base64
                import uuid as _uuid

                try:
                    with open(local_path, "rb") as f:
                        raw = f.read()
                    ext = os.path.splitext(local_path)[1].lower() or ".png"
                    ref = f"images/{_uuid.uuid4().hex}{ext}"
                    inline_images[ref] = base64.b64encode(raw).decode()
                    return ref
                except Exception as exc:
                    logger.warning("Failed to read temp image %s: %s", local_path, exc)
                    return ""

            logger.info(f"Starting Docx processing with max_pages={self.max_pages}")
            docx_processor = Docx(
                max_image_size=1920,
                enable_multimodal=True,
                upload_file=_inline_upload,
            )
            all_lines, ordered_text = docx_processor(
                binary=content,
                max_workers=max_workers,
                to_page=self.max_pages,
            )
            processing_time = time.time() - start_time
            logger.info(
                f"Docx processing completed in {processing_time:.2f}s, "
                f"extracted {len(all_lines)} sections"
            )

            logger.info("Processing document sections")
            section_start_time = time.time()

            text_parts = []
            image_parts: dict[str, str] = {}

            for sec_idx, line in enumerate(all_lines):
                try:
                    if line.text is not None and line.text != "":
                        text_parts.append(line.text)
                        if sec_idx < 3 or sec_idx % 50 == 0:
                            logger.info(
                                f"Added section {sec_idx + 1} text: {line.text[:50]}..."
                                if len(line.text) > 50
                                else f"Added section {sec_idx + 1} text: {line.text}"
                            )
                    if line.images:
                        for image_data in line.images:
                            if image_data.url and image_data.object:
                                image_parts[image_data.url] = endecode.decode_image(
                                    image_data.object
                                )
                                image_data.object.close()
                except Exception as e:
                    logger.error(f"Error processing section {sec_idx + 1}: {str(e)}")
                    logger.error(f"Detailed stack trace: {traceback.format_exc()}")
                    continue

            # Combine text
            section_processing_time = time.time() - section_start_time
            logger.info(f"Section processing completed in {section_processing_time:.2f}s")
            logger.info("Combining all text parts")
            # Prefer body-order markdown (paragraphs interleaved with tables).
            # Fall back to page-section text if composition produced nothing.
            text = (ordered_text or "").strip()
            if not text:
                text = "\n\n".join([part for part in text_parts if part])

            # Check if the generated text is empty
            if not text:
                logger.warning("Generated text is empty, trying alternative method")
                return self._parse_using_simple_method(content)

            total_processing_time = time.time() - start_time
            logger.info(
                f"Parsing complete in {total_processing_time:.2f}s, "
                f"generated {len(text)} characters of text"
            )

            image_parts.update(inline_images)
            return DocumentModel(content=text, images=image_parts)
        except (ArchiveLimitError, ParserCancelled):
            raise
        except Exception as e:
            logger.error(f"Error parsing DOCX document: {str(e)}")
            logger.error(f"Detailed stack trace: {traceback.format_exc()}")
            return self._parse_using_simple_method(content)

    def _parse_using_simple_method(self, content: bytes) -> DocumentModel:
        """Parse document using a simplified method, as a fallback

        Args:
            content: Document content

        Returns:
            Parsed text
        """
        logger.info("Attempting to parse document using simplified method")
        start_time = time.time()
        try:
            doc = Document(BytesIO(content))
            logger.info(
                f"Successfully loaded document in simplified method, "
                f"contains {len(doc.paragraphs)} paragraphs "
                f"and {len(doc.tables)} tables"
            )
            text_parts = []

            # Extract paragraph text
            para_count = len(doc.paragraphs)
            logger.info(f"Extracting text from {para_count} paragraphs")
            para_with_text = 0
            for i, para in enumerate(doc.paragraphs):
                if i % 100 == 0:
                    logger.info(f"Processing paragraph {i + 1}/{para_count}")
                if para.text.strip():
                    text_parts.append(para.text.strip())
                    para_with_text += 1

            logger.info(f"Extracted text from {para_with_text}/{para_count} paragraphs")

            # Extract table text
            table_count = len(doc.tables)
            logger.info(f"Extracting text from {table_count} tables")
            tables_with_content = 0
            rows_processed = 0
            for i, table in enumerate(doc.tables):
                if i % 10 == 0:
                    logger.info(f"Processing table {i + 1}/{table_count}")

                table_has_content = False
                for row in table.rows:
                    rows_processed += 1
                    row_text = " | ".join(
                        [cell.text.strip() for cell in row.cells if cell.text.strip()]
                    )
                    if row_text:
                        text_parts.append(row_text)
                        table_has_content = True

                if table_has_content:
                    tables_with_content += 1

            logger.info(
                f"Extracted content from {tables_with_content}/{table_count} tables, "
                f"processed {rows_processed} rows"
            )

            # Combine text
            result_text = "\n\n".join(text_parts)
            processing_time = time.time() - start_time
            logger.info(
                f"Simplified parsing complete in {processing_time:.2f}s, "
                f"generated {len(result_text)} characters of text"
            )

            # If the result is still empty, return an error message
            if not result_text:
                logger.warning("No text extracted using simplified method")
                return DocumentModel()

            return DocumentModel(content=result_text)
        except Exception as backup_error:
            processing_time = time.time() - start_time
            logger.error(f"Simplified parsing failed {processing_time:.2f}s: {backup_error}")
            logger.error(f"Detailed traceback: {traceback.format_exc()}")
            return DocumentModel()


class Docx:
    def __init__(self, max_image_size=1920, enable_multimodal=False, upload_file=None):
        logger.info("Initializing DOCX processor")
        self.max_image_size = max_image_size  # Maximum image size limit
        # Image cache to avoid processing the same image repeatedly
        self.picture_cache = {}
        self.enable_multimodal = enable_multimodal
        self.upload_file = upload_file
        self._doc_paragraphs: list[tuple[Any, bool]] = []
        self._retained_xml_refs: list[Any] = []

    def get_pictures(self, document, paragraph) -> list[Image.Image]:
        logger.info("Extracting images from paragraph")
        p_element = getattr(paragraph, "_element", paragraph)
        pics = p_element.xpath(".//pic:pic")
        if not pics:
            logger.info("No image found in paragraph")
            return []
        extracted: list[Image.Image] = []
        for pic in pics:
            try:
                embeds = pic.xpath(".//a:blip/@r:embed")
                if not embeds:
                    continue
                embed = embeds[0]
                if embed not in document.part.related_parts:
                    continue
                related_part = document.part.related_parts[embed]
                logger.info(f"Found embedded image with ID: {embed}")

                try:
                    image_blob = related_part.image.blob
                except UnrecognizedImageError:
                    logger.warning("Unrecognized image format. Skipping image.")
                    continue
                except UnexpectedEndOfFileError:
                    logger.warning(
                        "EOF was unexpectedly encountered while reading an image stream. "
                        "Skipping image."
                    )
                    continue
                except InvalidImageStreamError:
                    logger.warning(
                        "The recognized image stream appears to be corrupted. Skipping image."
                    )
                    continue

                try:
                    logger.info("Converting image blob to PIL Image")
                    with Image.open(BytesIO(image_blob)) as opened_img:
                        image = opened_img.convert("RGBA")
                    logger.info(f"Successfully extracted image, size: {image.width}x{image.height}")
                    extracted.append(image)
                except Exception as e:
                    logger.error(f"Failed to open image: {str(e)}")
                    continue
            except Exception as e:
                logger.error(f"Error extracting image: {str(e)}")
                continue
        return extracted

    def get_picture(self, document, paragraph) -> Image.Image | None:
        images = self.get_pictures(document, paragraph)
        return images[0] if images else None

    def _identify_page_paragraph_mapping(self, max_page=100000):
        """Identify the paragraph range included on each page

        Args:
            max_page: Maximum number of pages to process

        Returns:
            dict: Mapping of page numbers to lists of paragraph indices
        """
        start_time = time.time()
        logger.info(f"Identifying page to paragraph mapping (max_page={max_page})")
        page_to_paragraphs: dict[int, list[int]] = {}
        current_page = 0

        # Initialize page 0
        page_to_paragraphs[current_page] = []

        self._doc_paragraphs, self._retained_xml_refs = _collect_document_paragraphs(self.doc)
        # Record the total number of paragraphs processed
        total_paragraphs = len(self._doc_paragraphs)
        logger.info(f"Total paragraphs to map: {total_paragraphs}")

        # Heuristic method: estimate the number of paragraphs per page
        # For large documents, using a heuristic can reduce XML parsing overhead
        if total_paragraphs > 1000:
            logger.info("Large document detected, using heuristic paragraph mapping")
            estimated_paras_per_page = 25  # Estimate approximately 25 paragraphs per page

            # Create an estimated page mapping
            for p_idx in range(total_paragraphs):
                est_page = p_idx // estimated_paras_per_page
                if est_page > max_page:
                    logger.info(
                        f"Reached max page limit ({max_page}) at paragraph {p_idx}, "
                        "stopping paragraph mapping"
                    )
                    break

                if est_page not in page_to_paragraphs:
                    page_to_paragraphs[est_page] = []

                page_to_paragraphs[est_page].append(p_idx)

                if p_idx > 0 and p_idx % 1000 == 0:
                    logger.info(
                        f"Heuristic mapping: processed {p_idx}/{total_paragraphs} paragraphs"
                    )

            mapping_time = time.time() - start_time
            logger.info(
                f"Created heuristic mapping with {len(page_to_paragraphs)} pages "
                f"in {mapping_time:.2f}s"
            )
            return page_to_paragraphs

        # Standard method: iterate through all paragraphs to find page breaks
        logger.info("Using standard paragraph mapping method")
        page_breaks_found = 0
        for p_idx, (p_elem, is_body_level) in enumerate(self._doc_paragraphs):
            # Add the current paragraph to the current page
            page_to_paragraphs[current_page].append(p_idx)

            # Log every 100 paragraphs
            if p_idx > 0 and p_idx % 100 == 0:
                logger.info(f"Processed {p_idx}/{total_paragraphs} paragraphs in page mapping")

            # Check for page breaks on body-level paragraphs
            page_break_found = is_body_level and _paragraph_has_page_break(p_elem)

            # If a page break is found, create a new page
            if page_break_found:
                page_breaks_found += 1
                current_page += 1
                if current_page > max_page:
                    logger.info(f"Reached max page limit ({max_page}), stopping page mapping")
                    break

                # Initialize the paragraph list for the new page
                if current_page not in page_to_paragraphs:
                    page_to_paragraphs[current_page] = []

                if page_breaks_found % 10 == 0:
                    logger.info(
                        f"Found {page_breaks_found} page breaks so far, "
                        f"current page: {current_page}"
                    )

        # Handle potential empty page mappings
        empty_pages = [page for page, paras in page_to_paragraphs.items() if not paras]
        if empty_pages:
            logger.info(f"Removing {len(empty_pages)} empty pages from mapping")
            for page in empty_pages:
                del page_to_paragraphs[page]

        mapping_time = time.time() - start_time
        logger.info(
            f"Created paragraph mapping with {len(page_to_paragraphs)} pages in {mapping_time:.2f}s"
        )

        # Check the validity of the result
        if not page_to_paragraphs:
            logger.warning("No valid page mapping created, using fallback method")
            # All paragraphs are on page 0
            page_to_paragraphs[0] = list(range(total_paragraphs))

        # Log page distribution statistics
        page_sizes = [len(paragraphs) for paragraphs in page_to_paragraphs.values()]
        if page_sizes:
            avg_paragraphs = sum(page_sizes) / len(page_sizes)
            min_paragraphs = min(page_sizes)
            max_paragraphs = max(page_sizes)
            logger.info(
                f"Page statistics: avg={avg_paragraphs:.1f}, "
                f"min={min_paragraphs}, max={max_paragraphs} paragraphs per page"
            )

        return page_to_paragraphs

    def __call__(
        self,
        binary: bytes | None = None,
        from_page: int = 0,
        to_page: int = 100000,
        max_workers: int | None = None,
    ) -> tuple[list[LineData], str]:
        """
        Process DOCX document, supporting concurrent processing of each page

        Args:
            binary: DOCX document binary content
            from_page: Starting page number
            to_page: Ending page number
            max_workers: Maximum number of workers, default to None (system decides)

        Returns:
            tuple: (List of LineData objects with document content,
            body-order markdown with tables interleaved)
        """
        logger.info("Processing DOCX document")
        if not binary:
            return [], ""

        # Check CPU core count to determine parallel strategy
        cpu_count = os.cpu_count() or 2
        logger.info(f"System has {cpu_count} CPU cores available")

        # Load document
        self.doc = self._load_document(binary)
        if not self.doc:
            return [], ""

        # Identify page structure
        self.para_page_mapping = self._identify_page_paragraph_mapping(to_page)
        logger.info(f"Identified page to paragraph mapping for {len(self.para_page_mapping)} pages")

        # Apply page limits
        pages_to_process = self._apply_page_limit(self.para_page_mapping, from_page, to_page)
        if not pages_to_process:
            logger.warning("No pages to process after applying page limits!")
            return [], ""

        # Initialize shared resources
        self._init_shared_resources()

        # Process document content
        self._process_document(
            binary,
            pages_to_process,
            from_page,
            to_page,
            max_workers,
        )

        ordered_text = self._compose_ordered_markdown(pages_to_process)

        # Clean up document resources
        self._doc_paragraphs = []
        self._retained_xml_refs = []
        self.doc = None

        logger.info(
            f"Document processing complete, "
            f"extracted {len(self.all_lines)} text sections, "
            f"{len(ordered_text)} characters of body-order markdown"
        )
        return self.all_lines, ordered_text

    def _load_document(self, binary):
        """Load document

        Args:
            binary: Document binary content

        Returns:
            Document: Document object, or None (if loading fails)
        """
        if not binary:
            return None
        try:
            doc = Document(BytesIO(binary))
            logger.info("Successfully loaded document from binary content")
            return doc
        except Exception as e:
            logger.error(f"Failed to load DOCX document: {str(e)}")
            return None

    def _init_shared_resources(self):
        """Initialize shared resources"""
        # Create shared resource locks to protect data structures shared between threads
        self.lines_lock = threading.Lock()

        # Initialize result containers
        self.all_lines = []

    def _get_request_id(self):
        """Get current request ID"""
        current_request_id = None
        try:
            from docreader.utils.request import get_request_id

            current_request_id = get_request_id()
            logger.info(
                f"Getting current request ID: {current_request_id} to pass to processing threads"
            )
        except Exception as e:
            logger.warning(f"Failed to get current request ID: {str(e)}")
        return current_request_id

    def _apply_page_limit(self, para_page_mapping, from_page, to_page):
        """Apply page limits, return the list of pages to process

        Args:
            para_page_mapping: Mapping of pages to paragraphs
            from_page: Starting page number
            to_page: Ending page number

        Returns:
            list: List of pages to process
        """
        # Add page limits
        total_pages = len(para_page_mapping)
        if total_pages > to_page:
            logger.info(
                f"Document has {total_pages} pages, limiting processing to first {to_page} pages"
            )
            logger.info(f"Setting to_page limit to {to_page}")
        else:
            logger.info(
                f"Document has {total_pages} pages, processing all pages (limit: {to_page})"
            )

        # Filter out pages outside the range
        all_pages = sorted(para_page_mapping.keys())
        pages_to_process = [p for p in all_pages if from_page <= p < to_page]

        # Output the actual number of pages processed for debugging
        if pages_to_process:
            end_page = min(to_page, pages_to_process[-1])
            logger.info(
                f"Will process {len(pages_to_process)} pages "
                f"from page {from_page} to page {end_page}"
            )

            if len(pages_to_process) < len(all_pages):
                logger.info(
                    f"Skipping {len(all_pages) - len(pages_to_process)} pages due to page limit"
                )

            # Log detailed page index information
            if len(pages_to_process) <= 10:
                logger.info(f"Pages to process: {pages_to_process}")
            else:
                logger.info(
                    f"First 5 pages to process: {pages_to_process[:5]}, "
                    f"last 5: {pages_to_process[-5:]}"
                )

        return pages_to_process

    def _process_document(
        self,
        binary,
        pages_to_process,
        from_page,
        to_page,
        max_workers,
    ):
        """Process large documents, using multiprocessing

        Args:
            binary: Document binary content
            pages_to_process: List of pages to process
            from_page: Starting page number
            to_page: Ending page number
            max_workers: Maximum number of workers
        """
        # If the number of pages is too large, process in batches to reduce memory consumption
        cpu_count = os.cpu_count() or 2

        # Check if the document contains images to optimize processing speed
        doc_contains_images = self._check_document_has_images()

        # Optimize process count: dynamically adjust based on number of pages and CPU cores
        if max_workers is None:
            max_workers = self._calculate_optimal_workers(
                doc_contains_images, pages_to_process, cpu_count
            )

        temp_file_path = self._prepare_document_sharing(binary)
        try:
            # Prepare multiprocess processing arguments
            args_list = self._prepare_multiprocess_args(
                pages_to_process,
                from_page,
                to_page,
                doc_contains_images,
                temp_file_path,
            )

            # Execute multiprocess tasks
            self._execute_multiprocess_tasks(args_list, max_workers)
        finally:
            # Clean up temporary file
            self._cleanup_temp_file(temp_file_path)

    def _check_document_has_images(self):
        """Check if the document contains images

        Returns:
            bool: Whether the document contains images
        """
        if hasattr(self.doc, "inline_shapes") and len(self.doc.inline_shapes) > 0:
            logger.info(f"Document contains {len(self.doc.inline_shapes)} inline images")
            return True
        if self.doc is not None and bool(self.doc.element.body.xpath(".//pic:pic")):
            logger.info("Document contains embedded pic:pic image elements")
            return True
        return False

    def _calculate_optimal_workers(self, doc_contains_images, pages_to_process, cpu_count):
        """Calculate the optimal number of workers

        Args:
            doc_contains_images: Whether the document contains images
            pages_to_process: List of pages to process
            cpu_count: Number of CPU cores

        Returns:
            int: Optimal number of workers
        """
        # If no images or few pages, use fewer processes to avoid overhead
        if not doc_contains_images or len(pages_to_process) < cpu_count:
            max_workers = min(len(pages_to_process), max(1, cpu_count - 1))
        else:
            max_workers = min(len(pages_to_process), cpu_count)
        logger.info(f"Automatically set worker count to {max_workers}")
        return max_workers

    def _prepare_document_sharing(self, binary):
        """Prepare document sharing method

        Args:
            binary: Document binary content

        Returns:
            str: Temporary file path, or None if not using
        """

        temp_file = tempfile.NamedTemporaryFile(delete=False)  # noqa: SIM115
        temp_file_path = temp_file.name
        try:
            temp_file.write(binary)
            temp_file.close()
        except BaseException:
            with contextlib.suppress(OSError):
                temp_file.close()
            self._cleanup_temp_file(temp_file_path)
            raise
        return temp_file_path

    def _prepare_multiprocess_args(
        self,
        pages_to_process,
        from_page,
        to_page,
        doc_contains_images,
        temp_file_path,
    ):
        """Prepare a list of arguments for multiprocess processing

        Args:
            pages_to_process: List of pages to process
            from_page: Starting page number
            to_page: Ending page number
            doc_contains_images: Whether the document contains images
            temp_file_path: Temporary file path

        Returns:
            list: List of arguments
        """
        args_list = []
        for page_num in pages_to_process:
            args_list.append(
                (
                    page_num,
                    self.para_page_mapping[page_num],
                    from_page,
                    to_page,
                    doc_contains_images,
                    self.max_image_size,
                    temp_file_path,
                    self.enable_multimodal,
                )
            )

        return args_list

    def _execute_multiprocess_tasks(self, args_list, max_workers):
        """Execute page extraction tasks without unsafe fork() in multithreaded processes.

        Args:
            args_list: List of arguments
            max_workers: Maximum number of workers
        """
        self.all_lines = []
        if not args_list:
            return

        effective_workers = max(1, int(max_workers or 1))
        batch_start_time = time.time()

        # Single-page documents (or single-worker calls) run directly in-process
        # to avoid subprocess startup overhead and semaphore usage.
        if len(args_list) <= 1 or effective_workers <= 1:
            logger.info(f"Processing {len(args_list)} page(s) synchronously in-process")
            results: list[LineData] = []
            temp_img_paths = set()
            try:
                for args in args_list:
                    page_num = args[0]
                    try:
                        page_lines = process_page_multiprocess(*args)
                        for line in page_lines:
                            for image_data in line.images:
                                if image_data.local_path and self._is_docx_temp_img_path(
                                    image_data.local_path
                                ):
                                    temp_img_paths.add(image_data.local_path)
                        results.extend(page_lines)
                    except Exception as e:
                        logger.error(f"Error processing page {page_num}: {str(e)}")
                        logger.error(
                            f"Detailed traceback for page {page_num}: {traceback.format_exc()}"
                        )
                self._process_multiprocess_results(results)
            finally:
                self._cleanup_temp_image_files(temp_img_paths)
            return

        logger.info(f"Processing {len(args_list)} pages using {effective_workers} workers")

        def _submit_and_collect(executor):
            future_to_idx = {
                executor.submit(process_page_multiprocess, *args): i
                for i, args in enumerate(args_list)
            }
            logger.info(f"Submitted {len(future_to_idx)} processing tasks to executor")
            self._collect_process_results(future_to_idx, args_list, batch_start_time)

        import sys as _sys

        executor_cls = ProcessPoolExecutor
        module_obj = _sys.modules.get(__name__)
        is_picklable_target = (
            getattr(module_obj, "process_page_multiprocess", None) is process_page_multiprocess
        )
        if executor_cls is _RealProcessPoolExecutor:
            ctx = _select_mp_context() if is_picklable_target else None
            if ctx is not None:
                try:
                    with executor_cls(max_workers=effective_workers, mp_context=ctx) as executor:
                        logger.info(
                            f"Started ProcessPoolExecutor ({ctx.get_start_method()}) "
                            f"with {effective_workers} workers"
                        )
                        _submit_and_collect(executor)
                    return
                except (PermissionError, OSError) as exc:
                    logger.warning(
                        "ProcessPoolExecutor unavailable (%s); falling back to ThreadPoolExecutor",
                        exc,
                    )
            with ThreadPoolExecutor(max_workers=effective_workers) as thread_executor:
                _submit_and_collect(thread_executor)
        else:
            with executor_cls(max_workers=effective_workers) as executor:
                _submit_and_collect(executor)

    @staticmethod
    def _is_docx_temp_img_path(path: str) -> bool:
        """Check if path points to a temporary image created for docx processing."""
        if not path:
            return False
        try:
            temp_dir = os.path.dirname(os.path.abspath(path))
            base_dir = os.path.abspath(tempfile.gettempdir())
            if not os.path.basename(temp_dir).startswith("docx_img_"):
                return False
            parent = os.path.dirname(temp_dir)
            try:
                return (
                    os.path.samefile(parent, base_dir)
                    or parent == "/tmp"
                    or temp_dir.startswith("/tmp/docx_img_")
                )
            except OSError:
                return (
                    parent == base_dir or parent == "/tmp" or temp_dir.startswith("/tmp/docx_img_")
                )
        except Exception:
            return "docx_img_" in path

    def _collect_process_results(self, future_to_idx, args_list, batch_start_time):
        """Collect multiprocess processing results

        Args:
            future_to_idx: Mapping of Future to index
            args_list: List of arguments
            batch_start_time: Batch start time

        Returns:
            List[LineData]: Processed results as LineData objects
        """
        # Collect results
        completed_count = 0
        results = []
        temp_img_paths = set()  # Collect all temporary image paths

        try:
            for future in as_completed(future_to_idx):
                idx = future_to_idx[future]
                page_num = args_list[idx][0]
                try:
                    page_lines = future.result()

                    # Collect temporary image paths for later cleanup
                    for line in page_lines:
                        for image_data in line.images:
                            if image_data.local_path and self._is_docx_temp_img_path(
                                image_data.local_path
                            ):
                                temp_img_paths.add(image_data.local_path)

                    results.extend(page_lines)
                    completed_count += 1

                    if completed_count % max(
                        1, len(args_list) // 10
                    ) == 0 or completed_count == len(args_list):
                        elapsed_ms = int((time.time() - batch_start_time) * 1000)
                        progress_pct = int((completed_count / len(args_list)) * 100)
                        logger.info(
                            f"Progress: {completed_count}/{len(args_list)} pages processed "
                            f"({progress_pct}%, elapsed: {elapsed_ms}ms)"
                        )

                except Exception as e:
                    logger.error(f"Error processing page {page_num}: {str(e)}")
                    logger.error(
                        f"Detailed traceback for page {page_num}: {traceback.format_exc()}"
                    )

            # Process completion
            processing_elapsed_ms = int((time.time() - batch_start_time) * 1000)
            logger.info(f"All processing completed in {processing_elapsed_ms}ms")

            # Process results
            self._process_multiprocess_results(results)
        finally:
            # Clean up temporary image files
            self._cleanup_temp_image_files(temp_img_paths)

    def _process_multiprocess_results(self, results: list[LineData]):
        """Process multiprocess results

        Args:
            results: List of processed LineData results
        """
        # Sort upfront so image upload and reconstruction run in deterministic page order
        lines = sorted(results, key=lambda x: x.page_num)

        # Process images - must be handled in the main process for upload
        # If images are being processed, they need to be handled in the main process for upload
        image_upload_start = time.time()

        # Count total images to process
        images_to_process = []
        processed_lines = []
        for i, line_data in enumerate(lines):
            # Check if there are images
            if line_data.images and len(line_data.images) > 0:
                images_to_process.append(i)
                logger.info(f"Found line {i} with {len(line_data.images)} images to process")

        # Process images if needed
        image_url_map = {}  # Map from image path to URL
        if images_to_process:
            logger.info(
                f"Found {len(images_to_process)} lines with images to process in main process"
            )

            # First, create a mapping of image paths to uploaded URLs
            for line_idx in images_to_process:
                line_data = lines[line_idx]
                image_paths = line_data.images
                page_num = line_data.page_num

                # Process all image data objects
                for image_data in image_paths:
                    if (
                        image_data.local_path
                        and os.path.exists(image_data.local_path)
                        and image_data.local_path not in image_url_map
                    ):
                        try:
                            # Upload the image if it doesn't have a URL yet
                            if not image_data.url:
                                image_url = self.upload_file(image_data.local_path)
                                if image_url:
                                    # Store the URL in the ImageData object
                                    image_data.url = image_url
                                    # Add image URL as Markdown format
                                    markdown_image = f"![]({image_url})"
                                    image_url_map[image_data.local_path] = markdown_image
                                    logger.info(
                                        f"Added image URL for {image_data.local_path}: {image_url}"
                                    )
                                else:
                                    logger.warning(
                                        f"Failed to upload image: {image_data.local_path}"
                                    )
                            else:
                                # Already has a URL, use it
                                markdown_image = f"![]({image_data.url})"
                                image_url_map[image_data.local_path] = markdown_image
                                logger.info(
                                    f"Using existing URL for image {image_data.local_path}: "
                                    f"{image_data.url}"
                                )
                        except Exception as e:
                            logger.error(f"Error processing image from page {page_num}: {str(e)}")

            image_upload_elapsed = time.time() - image_upload_start
            logger.info(
                f"Finished uploading {len(image_url_map)} images in {image_upload_elapsed:.2f}s"
            )

            # Process content in original sequence order
            for line_data in lines:
                page_num = line_data.page_num
                if line_data.content_sequence:
                    combined_parts = []
                    for content_type, content in line_data.content_sequence:
                        if content_type == "text":
                            combined_parts.append(content)
                        elif content_type == "image":
                            # For ImageData objects, use the URL
                            if isinstance(content, str) and content in image_url_map:
                                combined_parts.append(image_url_map[content])
                            elif (
                                hasattr(content, "local_path")
                                and content.local_path in image_url_map
                            ):
                                combined_parts.append(image_url_map[content.local_path])

                    final_text = "\n\n".join(part for part in combined_parts if part)
                else:
                    final_text = line_data.text or ""
                processed_lines.append(
                    LineData(text=final_text, page_num=page_num, images=line_data.images)
                )
        else:
            processed_lines = lines

        # Sort results by page number
        sorted_lines = sorted(processed_lines, key=lambda x: x.page_num)
        self.all_lines = sorted_lines

        logger.info(
            f"Finished processing {len(self.all_lines)} lines with interleaved images and text"
        )

    def _cleanup_temp_image_files(self, temp_paths):
        """Clean up temporary image files created by multiprocessing

        Args:
            temp_paths: Set of temporary file paths
        """
        if not temp_paths:
            return

        logger.info(f"Cleaning up {len(temp_paths)} temporary image files")
        deleted_count = 0
        error_count = 0

        for path in temp_paths:
            try:
                if os.path.exists(path):
                    os.unlink(path)
                    deleted_count += 1
                    # Delete temporary directory (if empty)
                    try:
                        temp_dir = os.path.dirname(path)
                        if self._is_docx_temp_img_path(path) and os.path.exists(temp_dir):
                            os.rmdir(temp_dir)
                    except OSError:
                        # If directory is not empty, ignore error
                        pass
            except Exception as e:
                logger.error(f"Failed to delete temp file {path}: {str(e)}")
                error_count += 1

        logger.info(f"Temporary file cleanup: deleted {deleted_count}, errors {error_count}")

    def _cleanup_temp_file(self, temp_file_path):
        """Clean up temporary file

        Args:
            temp_file_path: Temporary file path
        """
        if temp_file_path and os.path.exists(temp_file_path):
            try:
                os.unlink(temp_file_path)
                logger.info(f"Removed temporary file: {temp_file_path}")
            except Exception as e:
                logger.error(f"Failed to remove temporary file: {str(e)}")

    def _compose_ordered_markdown(self, pages_to_process: list[int]) -> str:
        """Walk body paragraphs and tables in document order and emit GFM.

        Paragraph processing is page-limited; tables are included only when their
        paragraphs sit within the allowed page mapping so max_pages does not leak
        later tables or images into the parsed text.
        """
        allowed = set()
        for page in pages_to_process:
            allowed.update(self.para_page_mapping.get(page, []))
        min_allowed = min(allowed) if allowed else 0
        max_allowed = max(allowed) if allowed else -1

        doc_paragraphs = self._doc_paragraphs
        if not doc_paragraphs and self.doc is not None:
            doc_paragraphs, self._retained_xml_refs = _collect_document_paragraphs(self.doc)
            self._doc_paragraphs = doc_paragraphs

        para_index_by_elem: dict[Any, int] = {
            p_elem: idx for idx, (p_elem, _) in enumerate(doc_paragraphs)
        }

        images_by_paragraph: dict[int, list[str]] = {}
        for line in self.all_lines:
            for image in line.images:
                if image.url and image.paragraph_index in allowed:
                    images_by_paragraph.setdefault(image.paragraph_index, []).append(
                        f"![]({image.url})"
                    )

        emitted_image_paras: set = set()
        parts: list[str] = []
        last_seen_idx = 0

        def in_page_range() -> bool:
            return not allowed or min_allowed <= last_seen_idx <= max_allowed + 1

        def _collect_table_para_indices(table_obj: Any, seen_tc: set) -> list[int]:
            indices: list[int] = []
            for row in table_obj.rows:
                for cell in row.cells:
                    tc = cell._tc
                    if tc in seen_tc:
                        continue
                    seen_tc.add(tc)
                    for elem, _ in _block_children(tc):
                        if elem.tag == _W_P:
                            idx = para_index_by_elem.get(elem)
                            if idx is not None:
                                indices.append(idx)
                        elif elem.tag == _W_TBL:
                            indices.extend(_collect_table_para_indices(Table(elem, cell), seen_tc))
            return indices

        for child, _direct in _block_children(self.doc.element.body):
            if child.tag == _W_P:
                para_idx = para_index_by_elem.get(child)
                if para_idx is not None:
                    last_seen_idx = para_idx + 1
                    include = para_idx in allowed
                else:
                    include = in_page_range()
                if include:
                    text = _paragraph_text(child).strip()
                    if text:
                        parts.append(text)
                    if para_idx is not None and para_idx not in emitted_image_paras:
                        img_refs = images_by_paragraph.get(para_idx, [])
                        if img_refs:
                            emitted_image_paras.add(para_idx)
                            parts.extend(img_refs)
                continue

            table = Table(child, self.doc._body)
            if len(table.rows) == 0 or all(len(row.cells) == 0 for row in table.rows):
                continue
            table_indices = _collect_table_para_indices(table, set())
            if table_indices:
                last_seen_idx = max(table_indices) + 1
                include_table = not allowed or any(idx in allowed for idx in table_indices)
            else:
                include_table = in_page_range()
            if include_table:
                markdown = table_to_gfm_markdown(
                    table,
                    para_index_by_elem=para_index_by_elem,
                    images_by_paragraph=images_by_paragraph,
                    allowed_paragraphs=allowed if allowed else None,
                    emitted_image_paras=emitted_image_paras,
                )
                if markdown:
                    parts.append(markdown)

        return "\n\n".join(parts)

    def _safe_concat_images(self, images):
        """Safely concatenate image lists

        Args:
            images: List of images

        Returns:
            Image: Concatenated image, or the first image (if concatenation fails)
        """
        if not images:
            return None

        if len(images) == 1:
            return images[0]

        try:
            logger.info(f"Attempting to concatenate {len(images)} images")
            from PIL import Image

            Image.MAX_IMAGE_PIXELS = 40_000_000

            # Calculate the size of the concatenated image
            total_width = max(img.width for img in images if hasattr(img, "width"))
            total_height = sum(img.height for img in images if hasattr(img, "height"))

            if total_width <= 0 or total_height <= 0 or (total_width * total_height) > 40_000_000:
                logger.warning(
                    "Invalid or oversized concatenated image dimensions, returning the first image"
                )
                return images[0]

            # Create a new image
            new_image = Image.new("RGBA", (total_width, total_height), (0, 0, 0, 0))

            # Paste images one by one
            y_offset = 0
            for img in images:
                if not hasattr(img, "width") or not hasattr(img, "height"):
                    continue

                new_image.paste(img, (0, y_offset))
                y_offset += img.height

            logger.info(
                f"Successfully concatenated images, final size: {total_width}x{total_height}"
            )
            return new_image
        except Exception as e:
            logger.error(f"Failed to concatenate images: {str(e)}")
            logger.error(f"Detailed error: {traceback.format_exc()}")
            # If concatenation fails, return the first image
            return images[0]


def _save_image_to_temp(logger, image, page_num, img_idx):
    """Save image to a temporary file to pass between processes

    Args:
        logger: Logger
        image: PIL image object
        page_num: Page number
        img_idx: Image index

    Returns:
        str: Temporary file path, or None (if saving fails)
    """
    if not image:
        return None

    import os
    import tempfile

    temp_dir = None
    temp_file_path = None
    try:
        # Create a temporary file
        temp_dir = tempfile.mkdtemp(prefix="docx_img_")
        temp_file_path = os.path.join(temp_dir, f"page_{page_num}_img_{img_idx}.png")

        # Save the image
        image.save(temp_file_path, format="PNG")
        logger.info(f"[PID:{os.getpid()}] Saved image to temporary file: {temp_file_path}")

        return temp_file_path
    except Exception as e:
        if temp_file_path and os.path.exists(temp_file_path):
            with contextlib.suppress(OSError):
                os.unlink(temp_file_path)
        if temp_dir and os.path.exists(temp_dir):
            with contextlib.suppress(OSError):
                os.rmdir(temp_dir)
        logger.error(f"[PID:{os.getpid()}] Failed to save image to temp file: {str(e)}")
        return None


def process_page_multiprocess(
    page_num: int,
    paragraphs: list[int],
    from_page: int,
    to_page: int,
    doc_contains_images: bool,
    max_image_size: int,
    temp_file_path: str | None,
    enable_multimodal: bool,
) -> list[LineData]:
    """Page processing function specifically designed for multiprocessing

    Args:
        page_num: Page number
        paragraphs: List of paragraph indices
        from_page: Starting page number
        to_page: Ending page number
        doc_contains_images: Whether the document contains images
        max_image_size: Maximum image size
        doc_binary: Document binary content
        temp_file_path: Temporary file path, if using
        enable_multimodal: Whether to enable multimodal processing

    Returns:
        list: List of processed result lines
    """
    saved_temp_paths: list[str] = []
    try:
        # Set process-level logging
        process_logger = logging.getLogger(__name__)

        # If outside processing range, do not process
        if page_num < from_page or page_num >= to_page:
            process_logger.info(
                f"[PID:{os.getpid()}] Skipping page {page_num} (out of requested range)"
            )
            return []

        process_logger.info(
            f"[PID:{os.getpid()}] Processing page {page_num} with {len(paragraphs)} paragraphs, "
            f"enable_multimodal={enable_multimodal}"
        )
        start_time = time.time()

        # Load document in the process
        doc = _load_document_in_process(process_logger, page_num, temp_file_path)
        if not doc:
            return []

        # If paragraph indices are empty, return empty result
        if not paragraphs:
            process_logger.info(f"[PID:{os.getpid()}] No paragraphs to process for page {page_num}")
            return []

        # Extract page content
        combined_text, image_objects, content_sequence = _extract_page_content_in_process(
            process_logger,
            doc,
            page_num,
            paragraphs,
            enable_multimodal,
            max_image_size,
        )

        # Process content sequence to maintain order between processes
        processed_content = []
        temp_image_index = 0
        image_data_list = []

        if enable_multimodal:
            # First pass: save all images to temporary files
            for i, (paragraph_index, image_object) in enumerate(image_objects):
                img_path = _save_image_to_temp(process_logger, image_object, page_num, i)
                if img_path:
                    saved_temp_paths.append(img_path)
                    # Create ImageData object
                    image_data = ImageData()
                    image_data.local_path = img_path
                    image_data.object = image_object
                    image_data.paragraph_index = paragraph_index
                    image_data_list.append(image_data)

            process_logger.info(
                f"[PID:{os.getpid()}] Saved {len(image_data_list)} images "
                f"to temp files for page {page_num}"
            )

            # Second pass: reconstruct the content sequence with image data objects
            for content_type, content in content_sequence:
                if content_type == "text":
                    processed_content.append(("text", content))
                else:  # image
                    if temp_image_index < len(image_data_list):
                        processed_content.append(("image", image_data_list[temp_image_index]))
                        temp_image_index += 1

        # Create result line with the ordered content sequence
        line_data = LineData(
            text=combined_text,
            images=image_data_list,
            page_num=page_num,
            content_sequence=processed_content,
        )
        page_lines = [line_data]

        processing_time = time.time() - start_time
        process_logger.info(
            f"[PID:{os.getpid()}] Page {page_num} processing completed in {processing_time:.2f}s"
        )

        return page_lines

    except Exception as e:
        for orphan_path in saved_temp_paths:
            try:
                if os.path.exists(orphan_path):
                    os.unlink(orphan_path)
                parent_dir = os.path.dirname(orphan_path)
                if os.path.exists(parent_dir):
                    os.rmdir(parent_dir)
            except OSError:
                pass
        process_logger = logging.getLogger(__name__)
        process_logger.error(f"[PID:{os.getpid()}] Error processing page {page_num}: {str(e)}")
        process_logger.error(f"[PID:{os.getpid()}] Traceback: {traceback.format_exc()}")
        return []


def _load_document_in_process(logger, page_num, temp_file_path):
    """Load document in a process

    Args:
        logger: Logger
        page_num: Page number
        temp_file_path: Temporary file path

    Returns:
        Document: Loaded document object, or None (if loading fails)
    """
    logger.info(f"[PID:{os.getpid()}] Loading document in process for page {page_num}")
    try:
        # Load document from temporary file
        if temp_file_path is not None and os.path.exists(temp_file_path):
            doc = Document(temp_file_path)
            logger.info(f"[PID:{os.getpid()}] Loaded document from temp file: {temp_file_path}")
        else:
            logger.error(f"[PID:{os.getpid()}] No document source provided")
            return None
        return doc

    except Exception as e:
        logger.error(f"[PID:{os.getpid()}] Failed to load document: {str(e)}")
        logger.error(f"[PID:{os.getpid()}] Error traceback: {traceback.format_exc()}")
        return None


def _extract_page_content_in_process(
    logger,
    doc,
    page_num: int,
    paragraphs: list[int],
    enable_multimodal: bool,
    max_image_size: int,
) -> tuple[str, list[tuple[int, Any]], list[tuple[str, Any]]]:
    """Extract page content in a process

    Args:
        logger: Logger
        doc: Document object
        page_num: Page number
        paragraphs: List of paragraph indices
        enable_multimodal: Whether to enable multimodal processing
        max_image_size: Maximum image size

    Returns:
        tuple: (Extracted text, List of paragraph-index/image pairs, Content sequence)
    """
    logger.info(
        f"[PID:{os.getpid()}] Page {page_num}: Processing {len(paragraphs)} paragraphs, "
        f"enable_multimodal={enable_multimodal}"
    )

    doc_paragraphs, _retained = _collect_document_paragraphs(doc)

    # Instead of separate collections, track content in paragraph sequence
    content_sequence: list[tuple[str, Any]] = []
    current_text = ""

    processed_paragraphs = 0
    paragraphs_with_text = 0
    paragraphs_with_images = 0

    for para_idx in paragraphs:
        if para_idx < 0 or para_idx >= len(doc_paragraphs):
            logger.warning(f"[PID:{os.getpid()}] Paragraph index {para_idx} out of range")
            continue

        p_element, _is_body = doc_paragraphs[para_idx]
        processed_paragraphs += 1

        # Extract text content
        text = _paragraph_text(p_element).strip()
        if text:
            # Clean text
            cleaned_text = re.sub(r"\u3000", " ", text).strip()
            current_text += cleaned_text + "\n"
            paragraphs_with_text += 1

        # Process images - if multimodal processing is enabled
        if enable_multimodal:
            image_objects_in_para = _extract_images_in_process(
                logger, doc, p_element, page_num, para_idx, max_image_size
            )
            if image_objects_in_para:
                # If we have accumulated text, add it to sequence first
                if current_text:
                    content_sequence.append(("text", current_text))
                    current_text = ""

                # Add all images in this paragraph in exact document order
                for image_object in image_objects_in_para:
                    content_sequence.append(("image", (para_idx, image_object)))
                paragraphs_with_images += 1

        if processed_paragraphs % 50 == 0:
            logger.info(
                f"[PID:{os.getpid()}] "
                f"Page {page_num}: Processed {processed_paragraphs}/{len(paragraphs)} paragraphs"
            )

    # Add any remaining text
    if current_text:
        content_sequence.append(("text", current_text))

    logger.info(
        f"[PID:{os.getpid()}] Page {page_num}: Completed content extraction, "
        f"found {paragraphs_with_text} paragraphs with text, "
        f"{paragraphs_with_images} with images, "
        f"total content items: {len(content_sequence)}"
    )

    # Extract text and images in their original sequence
    text_parts: list[str] = []
    images: list[tuple[int, Any]] = []

    # Split content sequence into text and images
    for content_type, content in content_sequence:
        if content_type == "text":
            text_parts.append(str(content))
        else:  # image
            images.append(content)

    combined_text = "\n\n".join(text_parts) if text_parts else ""

    return combined_text, images, content_sequence


def _extract_images_in_process(
    logger, doc, paragraph, page_num, para_idx, max_image_size
) -> list[Image.Image]:
    """Extract all valid images from a paragraph (or w:p element) in order."""
    p_element = getattr(paragraph, "_element", paragraph)
    try:
        pic_elements = p_element.xpath(".//pic:pic")
    except Exception as e:
        logger.error(f"[PID:{os.getpid()}] Error querying pic elements: {str(e)}")
        logger.error(f"[PID:{os.getpid()}] Error traceback: {traceback.format_exc()}")
        return []

    if not pic_elements:
        return []

    extracted_images: list[Image.Image] = []
    for pic in pic_elements:
        logger.info(
            f"[PID:{os.getpid()}] Page {page_num}: Found pic element in paragraph {para_idx}"
        )
        try:
            embed = pic.xpath(".//a:blip/@r:embed")
            if not embed:
                logger.warning(
                    f"[PID:{os.getpid()}] Page {page_num}: No embed attribute found in image"
                )
                continue

            embed_id = embed[0]
            if embed_id not in doc.part.related_parts:
                logger.warning(
                    f"[PID:{os.getpid()}] Page {page_num}: "
                    f"Embed ID {embed_id} not found in related parts"
                )
                continue

            related_part = doc.part.related_parts[embed_id]
            logger.info(f"[PID:{os.getpid()}] Found embedded image with ID: {embed_id}")

            try:
                image_blob = related_part.image.blob
                logger.info(
                    f"[PID:{os.getpid()}] Successfully extracted image blob, "
                    f"size: {len(image_blob)} bytes"
                )
            except Exception as blob_error:
                logger.warning(
                    f"[PID:{os.getpid()}] Error extracting image blob: {str(blob_error)}"
                )
                continue

            try:
                with Image.open(BytesIO(image_blob)) as opened_img:
                    image = opened_img.convert("RGBA")

                if hasattr(image, "width") and hasattr(image, "height"):
                    logger.info(
                        f"[PID:{os.getpid()}] Successfully created image object, "
                        f"size: {image.width}x{image.height}"
                    )

                    # Skip small images (usually decorative elements)
                    if image.width < 50 or image.height < 50:
                        logger.info(
                            f"[PID:{os.getpid()}] "
                            f"Skipping small image ({image.width}x{image.height})"
                        )
                        image.close()
                        continue

                    # Scale large images
                    if image.width > max_image_size or image.height > max_image_size:
                        scale = min(
                            max_image_size / image.width,
                            max_image_size / image.height,
                        )
                        new_width = int(image.width * scale)
                        new_height = int(image.height * scale)
                        resized = image.resize((new_width, new_height))
                        image.close()
                        image = resized
                        logger.info(
                            f"[PID:{os.getpid()}] Resized image to {new_width}x{new_height}"
                        )

                logger.info(f"[PID:{os.getpid()}] Found image in paragraph {para_idx}")
                extracted_images.append(image)
            except Exception as e:
                logger.error(f"[PID:{os.getpid()}] Failed to create image from blob: {str(e)}")
                logger.error(f"[PID:{os.getpid()}] Error traceback: {traceback.format_exc()}")
                continue
        except Exception as e:
            logger.error(f"[PID:{os.getpid()}] Error extracting image: {str(e)}")
            logger.error(f"[PID:{os.getpid()}] Error traceback: {traceback.format_exc()}")
            continue

    return extracted_images


def _extract_image_in_process(logger, doc, paragraph, page_num, para_idx, max_image_size):
    """Backward-compatible helper returning the first extracted image, if any."""
    images = _extract_images_in_process(logger, doc, paragraph, page_num, para_idx, max_image_size)
    return images[0] if images else None
