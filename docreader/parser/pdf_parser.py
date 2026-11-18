"""PDF parsing with per-page routing between native text and scanned images.

Design (aligned with how MinerU / Docling / DeepDoc route PDFs):

* The dominant signal for "this page is scanned" is the **image-area coverage
  ratio** (image bounding-box area / page area), not the raw character count.
  A scanned page is essentially one big image covering the whole page, even
  when it carries a (often low-quality) embedded OCR text layer. Trusting that
  embedded text layer is what produced garbled RAG content before.
* Pages are classified independently so hybrid PDFs (some native, some scanned)
  are handled correctly. Native pages contribute their text layer; scanned
  pages are rendered to JPEG and tagged ``image_source_type=scanned_pdf`` so the
  Go App performs OCR/VLM on them (docreader itself never runs OCR).

No external services (e.g. MinerU) are required: the builtin engine is fully
self-sufficient using pypdfium2 + the Go-side OCR that already exists.
"""

from __future__ import annotations

import base64
import contextlib
import hashlib
import io
import json
import logging
import multiprocessing as mp
import os
import re
import tempfile
import threading
from collections import defaultdict, deque
from collections.abc import Iterator
from concurrent.futures import Future, ProcessPoolExecutor
from decimal import ROUND_CEILING, Decimal
from typing import Any
from urllib.parse import quote

from PIL import Image

from docreader.config import CONFIG
from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.concurrency import (
    ParserCancelled,
    cancellable_result,
    check_cancellation,
    parser_worker_limit,
)
from docreader.parser.pdf_layout import (
    _FIGURE_CAPTION_SEARCH_RE,
    LAYOUT_ORDERING,
    MAX_CHART_REGION_AREA_RATIO,
    MIN_CHART_REGION_AREA_RATIO,
    RENDER_VECTOR_FIGURES,
    _bbox_above_caption,
    _bbox_area_ratio,
    _cap_bbox_height,
    _chars_to_layout_markdown,
    _chart_region_bbox,
    _close_pdfium_resource,
    _env_bool,
    _env_float,
    _env_int,
    _expand_chart_bbox,
    _extract_layout_text,
    _filter_reading_columns,
    _group_lines,
    _group_lines_with_chars,
    _inject_figure_markdown_before_captions,
    _is_artifact_column,
    _join_line_glyphs,
    _line_has_figure_caption,
    _looks_like_numeric_table_columns,
    _merge_orphan_punctuation_lines,
    _page_chars,
    _page_geometry,
    _page_has_filtered_text,
    _page_line_boxes,
    _plain_is_well_formed,
    _point_in_boxes,
    _postprocess_pdf_text,
    _segments_to_markdown,
    _should_prefer_plain,
    _split_columns,
    _split_line_at_gaps,
    _strip_repeating_lines,
    _tail_looks_like_numeric_table,
)
from docreader.parser.source_locator import PageGeometry, page_blocks
from docreader.parser.stream import ParsedDocumentStream, StreamedImage
from docreader.utils.image_ref import image_markdown

logger = logging.getLogger(__name__)

# pdfium (the C library behind pypdfium2) is process-global and NOT
# thread-safe: two gRPC worker threads parsing PDFs at the same time corrupt
# its shared state and can deadlock the whole process indefinitely.
_PDFIUM_LOCK = threading.Lock()

# A page whose image objects cover at least this fraction of the page area is
# treated as scanned (image-dominated).
SCAN_IMAGE_AREA_RATIO = _env_float("DOCREADER_PDF_SCAN_IMAGE_RATIO", 0.5)
SCAN_MIN_CHARS_PER_PAGE = _env_int("DOCREADER_PDF_SCAN_MIN_CHARS", 10)
_LOW_TEXT_IMAGE_RATIO = 0.1

# --- Embedded figure extraction (text pages) ------------------------------
EXTRACT_EMBEDDED_IMAGES = _env_bool("DOCREADER_PDF_EXTRACT_EMBEDDED_IMAGES", True)
EMBED_MIN_PIXELS = _env_int("DOCREADER_PDF_EMBED_MIN_PIXELS", 80)
EMBED_MIN_AREA_RATIO = _env_float("DOCREADER_PDF_EMBED_MIN_AREA_RATIO", 0.01)
EMBED_REPEAT_PAGE_FRAC = _env_float("DOCREADER_PDF_EMBED_REPEAT_PAGE_FRAC", 0.5)
EMBED_MAX_IMAGES = _env_int("DOCREADER_PDF_EMBED_MAX_IMAGES", 50)

# --- Force scanned mode & source locators ---------------------------------
FORCE_SCANNED_PDF = _env_bool("DOCREADER_PDF_FORCE_SCANNED", False)
SOURCE_BOXES = _env_bool("DOCREADER_PDF_SOURCE_BOXES", True)


def _normalize_image_quality(quality: int) -> int:
    return min(95, max(1, quality))


def _classify_page(image_area_ratio: float, text_len: int) -> str:
    """Classify a page as ``"scanned"`` or ``"text"``."""
    if image_area_ratio >= SCAN_IMAGE_AREA_RATIO:
        return "scanned"
    if text_len < SCAN_MIN_CHARS_PER_PAGE and image_area_ratio >= _LOW_TEXT_IMAGE_RATIO:
        return "scanned"
    return "text"


def _page_image_area_ratio(page: Any, raw: Any) -> float:
    """Return the fraction of the page area covered by image objects."""
    width, height = page.get_size()
    page_area = float(width) * float(height)
    if page_area <= 0:
        return 0.0

    image_area = 0.0
    for obj in page.get_objects():
        try:
            if obj.type == raw.FPDF_PAGEOBJ_IMAGE:
                left, bottom, right, top = obj.get_bounds()
                image_area += abs((right - left) * (top - bottom))
        except Exception:
            continue
    return image_area / page_area


def _extract_page_text(page: Any) -> str:
    """Plain top-to-bottom text extraction (fallback path)."""
    textpage = None
    try:
        textpage = page.get_textpage()
        return str(textpage.get_text_range())
    finally:
        _close_pdfium_resource(textpage)


def _effective_scale(page: Any, scale: float, max_edge: int) -> float:
    """Reduce ``scale`` so the rendered long edge never exceeds ``max_edge`` px."""
    if max_edge <= 0:
        return scale
    width, height = page.get_size()
    longest_pt = max(float(width), float(height))
    if longest_pt <= 0:
        return scale
    return min(scale, max_edge / longest_pt)


def _pil_to_jpeg_bytes(pil: Image.Image, quality: int) -> bytes:
    buf = io.BytesIO()
    if pil.mode not in ("RGB", "L"):
        pil = pil.convert("RGB")
    pil.save(buf, format="JPEG", quality=quality, optimize=True)
    return buf.getvalue()


def _render_page_clip_jpeg(
    page: Any,
    bbox: tuple[float, float, float, float],
    scale: float,
    quality: int,
    max_edge: int,
) -> bytes:
    """Render a PDF page region to JPEG (bbox in PDF points, bottom-left origin)."""
    left, bottom, right, top = bbox
    scale_eff = _effective_scale(page, scale, max_edge)
    bitmap = None
    try:
        bitmap = page.render(scale=scale_eff)
        pil = bitmap.to_pil().convert("RGB")
    finally:
        _close_pdfium_resource(bitmap)
    _page_w, page_h = page.get_size()
    x0 = int(left * scale_eff)
    x1 = int(right * scale_eff)
    y0 = int((page_h - top) * scale_eff)
    y1 = int((page_h - bottom) * scale_eff)
    if x1 <= x0 or y1 <= y0:
        raise ValueError("degenerate clip bbox")
    return _pil_to_jpeg_bytes(pil.crop((x0, y0, x1, y1)), quality)


def _extract_vector_figure_clips(
    page: Any,
    page_index: int,
    plain_text: str,
    raw: Any,
    base_name: str,
    scale: float,
    quality: int,
    max_edge: int,
) -> list[tuple[str, str, float, str]]:
    """Render vector figure regions anchored at each ``Figure N.`` caption on the page."""
    if not RENDER_VECTOR_FIGURES or not re.search(r"\bFigure\s+\d+", plain_text, re.I):
        return []
    textpage = None
    try:
        textpage = page.get_textpage()
        chars, page_w = _page_chars(textpage, page, raw)
        if not chars:
            return []
        page_h = float(page.get_size()[1])
        lines = _merge_orphan_punctuation_lines(_group_lines_with_chars(chars))
        caption_indices = [i for i, ln in enumerate(lines) if _line_has_figure_caption(ln["text"])]
        if not caption_indices:
            return []

        results: list[tuple[str, str, float, str]] = []
        for fig_idx, cap_i in enumerate(caption_indices):
            cap_line = lines[cap_i]["text"].strip()
            m = _FIGURE_CAPTION_SEARCH_RE.search(cap_line)
            if m:
                cap_line = cap_line[m.start() :].split("\n", 1)[0].strip()

            bbox: tuple[float, float, float, float] | None = _bbox_above_caption(
                lines, cap_i, page_w, page_h
            )
            if bbox is None:
                bbox = _chart_region_bbox(chars, page_w, page_h)
            if bbox is None:
                continue

            ratio = _bbox_area_ratio(bbox, page_w, page_h)
            if ratio > MAX_CHART_REGION_AREA_RATIO:
                bbox = _cap_bbox_height(bbox, page_h, float(lines[cap_i]["bbox"][3]))
                ratio = _bbox_area_ratio(bbox, page_w, page_h)
                if ratio > MAX_CHART_REGION_AREA_RATIO:
                    continue
            if ratio < MIN_CHART_REGION_AREA_RATIO:
                continue

            bbox = _expand_chart_bbox(bbox, page_w, page_h, margin_frac=0.06)
            jpeg = _render_page_clip_jpeg(page, bbox, scale, quality, max_edge)
            fname = f"{base_name}_p{page_index + 1}_fig{fig_idx + 1}.jpg"
            ref_path = f"images/{fname}"
            results.append(
                (
                    ref_path,
                    base64.b64encode(jpeg).decode("utf-8"),
                    bbox[3],
                    cap_line,
                )
            )
        return results
    except Exception:
        logger.debug("vector figure clip failed on page %d", page_index, exc_info=True)
        return []
    finally:
        _close_pdfium_resource(textpage)


def _render_page_to_jpeg(page: Any, scale: float, quality: int, max_edge: int = 0) -> bytes:
    bitmap = None
    try:
        bitmap = page.render(scale=_effective_scale(page, scale, max_edge))
        img_obj = bitmap.to_pil()
        if img_obj.mode != "RGB":
            img_obj = img_obj.convert("RGB")
        buf = io.BytesIO()
        img_obj.save(buf, format="JPEG", quality=quality, optimize=True)
        return buf.getvalue()
    finally:
        _close_pdfium_resource(bitmap)


# --- Parallel scanned-page rendering --------------------------------------
PDF_STREAM_RENDER_BATCH_PAGES = 16
_WORKER_RENDER_DOC: Any = None


def _render_pool_init(pdf_path: str) -> None:
    global _WORKER_RENDER_DOC
    import pypdfium2 as pdfium

    _WORKER_RENDER_DOC = pdfium.PdfDocument(pdf_path)


def _render_pool_task(args: tuple[int, float, int, int]) -> tuple[int, bytes]:
    index, scale, quality, max_edge = args
    if _WORKER_RENDER_DOC is None:
        raise RuntimeError("PDF render worker document is not initialized")
    page = _WORKER_RENDER_DOC[index]
    try:
        return index, _render_page_to_jpeg(page, scale, quality, max_edge)
    finally:
        _close_pdfium_resource(page)


def _select_mp_context() -> Any:
    """Pick the safest available multiprocessing start method."""
    for method in ("forkserver", "fork"):
        try:
            return mp.get_context(method)
        except ValueError:
            continue
    return None


def _render_pages_parallel(
    content: bytes,
    indices: list[int],
    scale: float,
    quality: int,
    max_edge: int,
    workers: int,
    context: Any = None,
) -> dict[int, bytes] | None:
    """Render ``indices`` in parallel. Returns ``{index: jpeg_bytes}`` or None."""
    if workers <= 1 or len(indices) <= 1:
        return None
    ctx = _select_mp_context()
    if ctx is None:
        return None

    tmp_path: str | None = None
    try:
        with tempfile.NamedTemporaryFile(
            prefix="docreader_render_", suffix=".pdf", delete=False
        ) as tmp:
            tmp.write(content)
            tmp_path = tmp.name

        max_workers = min(workers, len(indices))
        tasks = [(i, scale, quality, max_edge) for i in indices]
        result: dict[int, bytes] = {}
        with ProcessPoolExecutor(
            max_workers=max_workers,
            mp_context=ctx,
            initializer=_render_pool_init,
            initargs=(tmp_path,),
        ) as ex:
            tasks_iter = iter(tasks)
            pending: deque[Future[tuple[int, bytes]]] = deque()
            try:
                for _ in range(max_workers * 2):
                    task = next(tasks_iter, None)
                    if task is not None:
                        pending.append(ex.submit(_render_pool_task, task))
                while pending:
                    index, jpeg = cancellable_result(pending.popleft(), context)
                    result[index] = jpeg
                    task = next(tasks_iter, None)
                    if task is not None:
                        check_cancellation(context)
                        pending.append(ex.submit(_render_pool_task, task))
            finally:
                for future in pending:
                    future.cancel()
        return result
    except ParserCancelled:
        raise
    except Exception:
        logger.warning(
            "parallel page rendering failed; falling back to serial",
            exc_info=True,
        )
        return None
    finally:
        if tmp_path:
            with contextlib.suppress(OSError):
                os.unlink(tmp_path)


def _write_pdf_temp_file(content: bytes) -> str:
    path: str | None = None
    try:
        with tempfile.NamedTemporaryFile(
            prefix="docreader_stream_", suffix=".pdf", delete=False
        ) as tmp:
            path = tmp.name
            tmp.write(content)
        return path
    except Exception:
        if path is not None:
            _unlink_pdf_temp_file(path)
        raise


def _unlink_pdf_temp_file(path: str) -> None:
    try:
        os.unlink(path)
    except FileNotFoundError:
        pass
    except OSError:
        logger.warning("Failed to remove temporary PDF %s", path, exc_info=True)


def _iter_scanned_pages_from_open_pdf(
    pdf: Any,
    indices: list[int],
    base_name: str,
    scale: float,
    quality: int,
    max_edge: int,
    context: Any = None,
) -> Iterator[StreamedImage]:
    """Render one page at a time from an already-open PdfDocument."""
    for index in indices:
        with (
            parser_worker_limit("pdf_render", CONFIG.pdf_render_max_workers, context=context),
            _PDFIUM_LOCK,
        ):
            check_cancellation(context)
            page = pdf[index]
            try:
                image_bytes = _render_page_to_jpeg(page, scale, quality, max_edge)
            finally:
                _close_pdfium_resource(page)
        yield StreamedImage(
            ref_path=f"images/{base_name}_page_{index + 1}.jpg",
            image_data=image_bytes,
        )


def _iter_scanned_pages_serial_batch(
    pdf_path: str,
    indices: list[int],
    base_name: str,
    scale: float,
    quality: int,
    max_edge: int,
    context: Any = None,
) -> Iterator[StreamedImage]:
    import pypdfium2 as pdfium

    check_cancellation(context)
    with _PDFIUM_LOCK:
        pdf = pdfium.PdfDocument(pdf_path)
    try:
        yield from _iter_scanned_pages_from_open_pdf(
            pdf, indices, base_name, scale, quality, max_edge, context
        )
    finally:
        with _PDFIUM_LOCK:
            _close_pdfium_resource(pdf)


def _iter_scanned_pages_parallel_batch(
    pdf_path: str,
    indices: list[int],
    base_name: str,
    scale: float,
    quality: int,
    max_edge: int,
    workers: int,
    mp_context: Any,
    context: Any = None,
) -> Iterator[StreamedImage]:
    import concurrent.futures

    max_workers = min(workers, len(indices))
    window_size = max_workers * 2
    pending: deque[Future[tuple[int, bytes]]] = deque()
    executor: Any = None
    sent = False
    fallback = False

    with parser_worker_limit("pdf_render", CONFIG.pdf_render_max_workers, context=context):
        try:
            executor = concurrent.futures.ProcessPoolExecutor(
                max_workers=max_workers,
                mp_context=mp_context,
                initializer=_render_pool_init,
                initargs=(pdf_path,),
            )
            indices_iter = iter(indices)
            for _ in range(window_size):
                try:
                    index = next(indices_iter)
                except StopIteration:
                    break
                pending.append(
                    executor.submit(
                        _render_pool_task,
                        (index, scale, quality, max_edge),
                    )
                )

            while pending:
                check_cancellation(context)
                future = pending.popleft()
                index, image_bytes = cancellable_result(future, context)
                check_cancellation(context)
                try:
                    next_index = next(indices_iter)
                except StopIteration:
                    pass
                else:
                    pending.append(
                        executor.submit(
                            _render_pool_task,
                            (next_index, scale, quality, max_edge),
                        )
                    )
                sent = True
                yield StreamedImage(
                    ref_path=f"images/{base_name}_page_{index + 1}.jpg",
                    image_data=image_bytes,
                )
        except ParserCancelled:
            raise
        except Exception:
            if sent:
                raise
            logger.warning(
                "parallel streamed PDF rendering failed before the first image; "
                "falling back to serial rendering for this batch",
                exc_info=True,
            )
            fallback = True
        finally:
            if executor is not None:
                for future in pending:
                    future.cancel()
                executor.shutdown(wait=True, cancel_futures=True)
                pending.clear()

    if fallback:
        yield from _iter_scanned_pages_serial_batch(
            pdf_path, indices, base_name, scale, quality, max_edge, context
        )


def _iter_scanned_pages_from_path(
    pdf_path: str,
    indices: list[int],
    base_name: str,
    scale: float,
    quality: int,
    max_edge: int,
    workers: int,
    context: Any = None,
) -> Iterator[StreamedImage]:
    """Yield images in fixed-size batches to cap PDFium and result memory."""
    for start in range(0, len(indices), PDF_STREAM_RENDER_BATCH_PAGES):
        check_cancellation(context)
        batch = indices[start : start + PDF_STREAM_RENDER_BATCH_PAGES]
        mp_context = _select_mp_context() if workers > 1 and len(batch) > 1 else None
        if workers > 1 and len(batch) > 1 and mp_context is not None:
            yield from _iter_scanned_pages_parallel_batch(
                pdf_path,
                batch,
                base_name,
                scale,
                quality,
                max_edge,
                workers,
                mp_context,
                context,
            )
        else:
            yield from _iter_scanned_pages_serial_batch(
                pdf_path, batch, base_name, scale, quality, max_edge, context
            )


def _render_scanned_pages(
    pdf: Any,
    content: bytes,
    indices: list[int],
    scale: float,
    quality: int,
    max_edge: int,
    context: Any = None,
) -> dict[int, bytes]:
    """Render the given (scanned) page indices to JPEG bytes."""
    check_cancellation(context)
    parallel = _render_pages_parallel(
        content,
        indices,
        scale,
        quality,
        max_edge,
        CONFIG.pdf_render_parallelism,
        context,
    )
    if parallel is not None:
        return parallel

    out: dict[int, bytes] = {}
    for i in indices:
        check_cancellation(context)
        with _PDFIUM_LOCK:
            check_cancellation(context)
            page = pdf[i]
            try:
                out[i] = _render_page_to_jpeg(page, scale, quality, max_edge)
            finally:
                _close_pdfium_resource(page)
    return out


def _encode_rendered_page_images(rendered: dict[int, bytes], base_name: str) -> dict[str, str]:
    """Encode rendered pages while releasing each raw JPEG immediately."""
    encoded: dict[str, str] = {}
    for index in sorted(rendered):
        image_bytes = rendered.pop(index)
        ref_path = f"images/{base_name}_page_{index + 1}.jpg"
        encoded[ref_path] = base64.b64encode(image_bytes).decode("utf-8")
    return encoded


def _select_embedded_images(
    meta: list[dict[str, Any]],
    num_text_pages: int,
    *,
    min_pixels: int = EMBED_MIN_PIXELS,
    min_area_ratio: float = EMBED_MIN_AREA_RATIO,
    repeat_frac: float = EMBED_REPEAT_PAGE_FRAC,
    max_images: int = EMBED_MAX_IMAGES,
) -> list[int]:
    """Decide which embedded-image candidates to keep (pure function)."""
    hash_pages: dict[str, set[int]] = defaultdict(set)
    for m in meta:
        hash_pages[m["hash"]].add(m["page"])

    repeat_threshold = (
        max(
            2,
            int(
                (Decimal(str(repeat_frac)) * num_text_pages).to_integral_value(
                    rounding=ROUND_CEILING
                )
            ),
        )
        if num_text_pages
        else 2
    )
    banned = {h for h, pages in hash_pages.items() if len(pages) >= repeat_threshold}

    kept: list[int] = []
    seen: set[tuple[int, str]] = set()
    for idx, m in enumerate(meta):
        if m["area_ratio"] < min_area_ratio:
            continue
        if m["width"] < min_pixels or m["height"] < min_pixels:
            continue
        if m["hash"] in banned:
            continue
        key = (m["page"], m["hash"])
        if key in seen:
            continue
        seen.add(key)
        kept.append(idx)
        if len(kept) >= max_images:
            break
    return kept


def _decode_embedded_image_pil(obj: Any) -> Image.Image:
    """Decode an embedded image the way a PDF viewer displays it."""
    bitmap = None
    try:
        try:
            bitmap = obj.get_bitmap(render=True)
        except Exception:
            logger.warning(
                "get_bitmap(render=True) failed; falling back to raw decode",
                exc_info=True,
            )
            _close_pdfium_resource(bitmap)
            bitmap = obj.get_bitmap()
        pil: Image.Image = bitmap.to_pil()
        if pil.mode in ("RGBA", "LA", "PA"):
            src_mode = pil.mode
            rgba = pil.convert("RGBA")
            extrema = rgba.getchannel("A").getextrema()
            min_alpha = extrema[0] if isinstance(extrema, tuple) else 255
            if isinstance(min_alpha, (int, float)) and min_alpha < 255:
                background = Image.new("RGBA", rgba.size, (255, 255, 255, 255))
                return Image.alpha_composite(background, rgba).convert("RGB")
            if src_mode == "LA":
                return rgba.convert("L")
            return rgba.convert("RGB")
        if pil.mode in ("RGB", "L"):
            return pil.copy()
        return pil.convert("RGB")
    finally:
        _close_pdfium_resource(bitmap)


def _extract_embedded_images(
    pdf: Any, classes: list[str], raw: Any, base_name: str, quality: int
) -> dict[int, list[tuple[str, str, float]]]:
    """Extract filtered embedded figures from native text pages."""
    text_indices = [i for i, c in enumerate(classes) if c == "text"]
    if not text_indices:
        return {}

    candidates: list[tuple[int, int, float]] = []
    meta: list[dict[str, Any]] = []
    for i in text_indices:
        page = pdf[i]
        try:
            width, height = page.get_size()
            page_area = float(width) * float(height)
            if page_area <= 0:
                continue
            for object_index, obj in enumerate(page.get_objects()):
                if obj.type != raw.FPDF_PAGEOBJ_IMAGE:
                    continue
                try:
                    left, bottom, right, top = obj.get_bounds()
                except Exception:
                    continue
                area_ratio = abs((right - left) * (top - bottom)) / page_area
                if area_ratio < EMBED_MIN_AREA_RATIO:
                    continue
                try:
                    pil = _decode_embedded_image_pil(obj)
                except Exception:
                    continue
                try:
                    content_hash = hashlib.md5(pil.tobytes()).hexdigest()
                    candidates.append((i, object_index, float(top)))
                    meta.append(
                        {
                            "page": i,
                            "width": pil.width,
                            "height": pil.height,
                            "area_ratio": area_ratio,
                            "hash": content_hash,
                        }
                    )
                finally:
                    pil.close()
        finally:
            _close_pdfium_resource(page)

    kept_idx = _select_embedded_images(meta, len(text_indices))
    if not kept_idx:
        return {}

    result: dict[int, list[tuple[str, str, float]]] = defaultdict(list)
    per_page_count: dict[int, int] = defaultdict(int)
    max_edge = CONFIG.pdf_render_max_edge
    for idx in kept_idx:
        page_i, object_index, y_top = candidates[idx]
        page = pdf[page_i]
        pil_img: Image.Image | None = None
        try:
            for index, obj in enumerate(page.get_objects()):
                if index == object_index:
                    pil_img = _decode_embedded_image_pil(obj)
                    break
            if pil_img is None:
                raise ValueError("Selected embedded image is no longer available")
            if pil_img.mode not in ("RGB", "L"):
                converted = pil_img.convert("RGB")
                pil_img.close()
                pil_img = converted
            if max_edge > 0 and max(pil_img.size) > max_edge:
                ratio = max_edge / max(pil_img.size)
                resized = pil_img.resize(
                    (
                        max(1, int(pil_img.width * ratio)),
                        max(1, int(pil_img.height * ratio)),
                    )
                )
                pil_img.close()
                pil_img = resized
            with io.BytesIO() as buf:
                pil_img.save(buf, format="JPEG", quality=quality, optimize=True)
                per_page_count[page_i] += 1
                fname = f"{base_name}_p{page_i + 1}_img{per_page_count[page_i]}.jpg"
                ref_path = f"images/{fname}"
                result[page_i].append(
                    (
                        ref_path,
                        base64.b64encode(buf.getvalue()).decode("utf-8"),
                        y_top,
                    )
                )
        finally:
            if pil_img is not None:
                pil_img.close()
            _close_pdfium_resource(page)

    for page_i in result:
        result[page_i].sort(key=lambda item: item[2], reverse=True)
    return result


class PDFScannedParser(BaseParser):
    """Render every PDF page to a JPEG image."""

    def parse_into_text(self, content: bytes) -> Document:
        check_cancellation(self.context)
        if not content or len(content.strip()) == 0:
            logger.warning("PDFScannedParser received empty content for %s", self.file_name)
            return Document(content="", metadata={"page_count": 0})
        if len(content) < 8 or b"%PDF-" not in content[:1024]:
            logger.error("PDFScannedParser failed to parse PDF: invalid PDF header")
            raise ValueError(
                f"Invalid PDF file {self.file_name or 'document'}: missing %PDF- signature"
            )

        import pypdfium2 as pdfium

        images: dict[str, str] = {}
        markdown_lines: list[str] = []
        base_name = quote(os.path.splitext(self.file_name or "document")[0], safe="")

        logger.info(
            "PDFScannedParser: Rendering PDF pages to JPEG images for %s",
            self.file_name,
        )

        try:
            with parser_worker_limit(
                "pdf_render", CONFIG.pdf_render_max_workers, context=self.context
            ):
                with _PDFIUM_LOCK:
                    check_cancellation(self.context)
                    pdf = pdfium.PdfDocument(content)
                    page_count = len(pdf)
                try:
                    scale = max(1, CONFIG.pdf_render_dpi) / 72
                    quality = _normalize_image_quality(CONFIG.pdf_jpeg_quality)

                    rendered = _render_scanned_pages(
                        pdf,
                        content,
                        list(range(page_count)),
                        scale,
                        quality,
                        CONFIG.pdf_render_max_edge,
                        self.context,
                    )
                finally:
                    with _PDFIUM_LOCK:
                        _close_pdfium_resource(pdf)

            source_blocks: list[dict[str, Any]] = []
            offset = 0
            images.update(_encode_rendered_page_images(rendered, base_name))
            for i in range(page_count):
                page_filename = f"{base_name}_page_{i + 1}.jpg"
                ref_path = f"images/{page_filename}"
                line = image_markdown(ref_path)
                if markdown_lines:
                    offset += 2
                markdown_lines.append(line)
                source_blocks.append(
                    {
                        "start": offset,
                        "end": offset + len(line),
                        "locator": {"type": "pdf", "page": i + 1, "mapping": "exact"},
                    }
                )
                offset += len(line)

            text = "\n\n".join(markdown_lines)
            return Document(
                content=text,
                images=images,
                metadata={
                    "image_source_type": "scanned_pdf",
                    "page_count": page_count,
                },
                source_blocks=source_blocks,
            )
        except Exception as e:
            logger.exception("PDFScannedParser failed to parse PDF: %s", e)
            import pypdfium2 as pdfium

            if isinstance(e, (pdfium.PdfiumError, ValueError)):
                raise ValueError(
                    f"Failed to load PDF document: invalid or corrupt PDF structure ({e})"
                ) from e
            raise e

    def parse_stream(self, content: bytes) -> ParsedDocumentStream:
        """Return metadata first and lazily render each scanned page."""
        check_cancellation(self.context)
        if not content or len(content.strip()) == 0:
            return ParsedDocumentStream(
                document=Document(content="", metadata={"page_count": 0}),
                image_count=0,
            )
        if len(content) < 8 or b"%PDF-" not in content[:1024]:
            logger.error("PDFScannedParser failed to parse PDF: invalid PDF header")
            raise ValueError(
                f"Invalid PDF file {self.file_name or 'document'}: missing %PDF- signature"
            )

        import pypdfium2 as pdfium

        base_name = quote(os.path.splitext(self.file_name or "document")[0], safe="")
        with _PDFIUM_LOCK:
            try:
                pdf = pdfium.PdfDocument(content)
                try:
                    page_count = len(pdf)
                finally:
                    _close_pdfium_resource(pdf)
            except pdfium.PdfiumError as e:
                raise ValueError(
                    f"Failed to load PDF document: invalid or corrupt PDF structure ({e})"
                ) from e

        markdown_lines: list[str] = []
        source_blocks: list[dict[str, Any]] = []
        offset = 0
        for index in range(page_count):
            page_filename = f"{base_name}_page_{index + 1}.jpg"
            ref_path = f"images/{page_filename}"
            line = image_markdown(ref_path)
            if markdown_lines:
                offset += 2
            markdown_lines.append(line)
            source_blocks.append(
                {
                    "start": offset,
                    "end": offset + len(line),
                    "locator": {"type": "pdf", "page": index + 1, "mapping": "exact"},
                }
            )
            offset += len(line)

        document = Document(
            content="\n\n".join(markdown_lines),
            metadata={"image_source_type": "scanned_pdf", "page_count": page_count},
            source_blocks=source_blocks,
        )
        image_count = page_count
        if page_count == 0:
            return ParsedDocumentStream(document=document, image_count=0)

        scale = max(1, CONFIG.pdf_render_dpi) / 72
        quality = _normalize_image_quality(CONFIG.pdf_jpeg_quality)
        indices = list(range(page_count))

        pdf_path = _write_pdf_temp_file(content)
        return ParsedDocumentStream(
            document=document,
            image_count=image_count,
            images=_iter_scanned_pages_from_path(
                pdf_path,
                indices,
                base_name,
                scale,
                quality,
                CONFIG.pdf_render_max_edge,
                CONFIG.pdf_render_parallelism,
                self.context,
            ),
            _cleanup=lambda: _unlink_pdf_temp_file(pdf_path),
        )


class PDFParser(BaseParser):
    """Per-page router between native text extraction and scanned rendering."""

    def __init__(self, file_name: str = "", file_type: str | None = None, **kwargs: Any) -> None:
        raw = kwargs.pop("pdf_force_scanned", None)
        super().__init__(file_name=file_name, file_type=file_type, **kwargs)
        if raw is not None:
            self._force_scanned = str(raw).strip().lower() in {
                "1",
                "true",
                "yes",
                "y",
                "on",
            }
        else:
            self._force_scanned = FORCE_SCANNED_PDF

    def parse_into_text(self, content: bytes) -> Document:
        check_cancellation(self.context)
        if not content or len(content.strip()) == 0:
            logger.warning("PDFParser received empty content for %s", self.file_name)
            return Document(content="", metadata={"page_count": 0})
        if len(content) < 8 or b"%PDF-" not in content[:1024]:
            logger.error("PDFParser failed to parse PDF: invalid PDF header")
            raise ValueError(
                f"Invalid PDF file {self.file_name or 'document'}: missing %PDF- signature"
            )

        if self._force_scanned:
            logger.info(
                "PDFParser: force scanned mode enabled for %s",
                self.file_name,
            )
            doc = PDFScannedParser(
                file_name=self.file_name,
                file_type=self.file_type,
                context=self.context,
            ).parse_into_text(content)

            page_count = doc.metadata.get("page_count", 0)
            doc.metadata.update(
                {
                    "scanned_page_count": page_count,
                    "text_page_count": 0,
                    "embedded_image_count": 0,
                    "vector_figure_count": 0,
                }
            )
            return doc

        try:
            return self._route(content)
        except ParserCancelled:
            raise
        except Exception:
            logger.exception(
                "PDFParser: per-page routing failed for %s; falling back to full image rendering",
                self.file_name,
            )
            import pypdfium2 as pdfium

            try:
                return PDFScannedParser(
                    file_name=self.file_name,
                    file_type=self.file_type,
                    context=self.context,
                ).parse_into_text(content)
            except (pdfium.PdfiumError, ValueError) as scan_exc:
                raise ValueError(
                    f"Failed to load PDF document: invalid or corrupt PDF structure ({scan_exc})"
                ) from scan_exc

    def parse_stream(self, content: bytes) -> ParsedDocumentStream:
        """Prepare PDF metadata, then render scanned pages only as consumed."""
        check_cancellation(self.context)
        if not content or len(content.strip()) == 0:
            return ParsedDocumentStream(
                document=Document(content="", metadata={"page_count": 0}),
                image_count=0,
            )
        if len(content) < 8 or b"%PDF-" not in content[:1024]:
            logger.error("PDFParser failed to parse PDF: invalid PDF header")
            raise ValueError(
                f"Invalid PDF file {self.file_name or 'document'}: missing %PDF- signature"
            )

        if self._force_scanned:
            stream = PDFScannedParser(
                file_name=self.file_name,
                file_type=self.file_type,
                context=self.context,
            ).parse_stream(content)
            page_count = stream.document.metadata.get("page_count", 0)
            stream.document.metadata.update(
                {
                    "scanned_page_count": page_count,
                    "text_page_count": 0,
                    "embedded_image_count": 0,
                    "vector_figure_count": 0,
                }
            )
            return stream

        try:
            document, scanned_indices = self._route_locked(content, include_scanned_images=False)
        except ParserCancelled:
            raise
        except Exception:
            logger.exception(
                "PDFParser: per-page streaming preparation failed for %s; "
                "falling back to full image rendering",
                self.file_name,
            )
            import pypdfium2 as pdfium

            try:
                return PDFScannedParser(
                    file_name=self.file_name,
                    file_type=self.file_type,
                    context=self.context,
                ).parse_stream(content)
            except (pdfium.PdfiumError, ValueError) as scan_exc:
                raise ValueError(
                    f"Failed to load PDF document: invalid or corrupt PDF structure ({scan_exc})"
                ) from scan_exc

        image_count = len(document.images or {}) + len(scanned_indices)
        if not scanned_indices:
            return ParsedDocumentStream(document=document, image_count=image_count)

        scale = max(1, CONFIG.pdf_render_dpi) / 72
        quality = _normalize_image_quality(CONFIG.pdf_jpeg_quality)
        base_name = quote(os.path.splitext(self.file_name or "document")[0], safe="")

        pdf_path = _write_pdf_temp_file(content)
        return ParsedDocumentStream(
            document=document,
            image_count=image_count,
            images=_iter_scanned_pages_from_path(
                pdf_path,
                scanned_indices,
                base_name,
                scale,
                quality,
                CONFIG.pdf_render_max_edge,
                CONFIG.pdf_render_parallelism,
                self.context,
            ),
            _cleanup=lambda: _unlink_pdf_temp_file(pdf_path),
        )

    def _route(self, content: bytes) -> Document:
        document, _scanned_indices = self._route_locked(content)
        return document

    def _route_locked(
        self, content: bytes, *, include_scanned_images: bool = True
    ) -> tuple[Document, list[int]]:
        import pypdfium2 as pdfium
        import pypdfium2.raw as pdfium_r

        base_name = quote(os.path.splitext(self.file_name or "document")[0], safe="")
        scale = max(1, CONFIG.pdf_render_dpi) / 72
        quality = _normalize_image_quality(CONFIG.pdf_jpeg_quality)

        with _PDFIUM_LOCK:
            pdf = pdfium.PdfDocument(content)
            page_count = len(pdf)
        images: dict[str, str] = {}
        try:
            texts: list[str] = []
            classes: list[str] = []
            vector_clips: dict[int, list[tuple[str, str, float, str]]] = {}
            line_boxes: dict[int, list[dict[str, Any]]] = {}
            geometries: dict[int, PageGeometry | None] = {}
            for i in range(page_count):
                check_cancellation(self.context)
                layout_data: tuple[str, bool, bool] | None = None
                clips: list[tuple[str, str, float, str]] | None = None
                with _PDFIUM_LOCK:
                    page = pdf[i]
                    try:
                        geometries[i] = _page_geometry(page)
                        plain = _extract_page_text(page)
                        ratio = _page_image_area_ratio(page, pdfium_r)
                        cls = _classify_page(ratio, len(plain.strip()))
                        plain_well_formed = False
                        has_filtered = False
                        if cls == "text" and LAYOUT_ORDERING:
                            plain_well_formed = _plain_is_well_formed(plain)
                            has_filtered = _page_has_filtered_text(page, pdfium_r)
                            if not plain_well_formed or has_filtered:
                                raw_layout = _extract_layout_text(
                                    page, pdfium_r, return_metadata=True
                                )
                                if isinstance(raw_layout, tuple) and len(raw_layout) == 3:
                                    layout_data = (
                                        str(raw_layout[0]),
                                        bool(raw_layout[1]),
                                        bool(raw_layout[2]),
                                    )
                        if cls == "text":
                            clips = _extract_vector_figure_clips(
                                page,
                                i,
                                plain,
                                pdfium_r,
                                base_name,
                                scale,
                                quality,
                                CONFIG.pdf_render_max_edge,
                            )
                        if cls == "text" and SOURCE_BOXES:
                            line_boxes[i] = _page_line_boxes(page, pdfium_r)
                    finally:
                        _close_pdfium_resource(page)

                if cls == "text" and LAYOUT_ORDERING:
                    if (plain_well_formed and not has_filtered) or layout_data is None:
                        text = plain
                    else:
                        layout, filtered_text, table_hint = layout_data
                        if filtered_text or (
                            layout
                            and not _should_prefer_plain(
                                plain, layout, allow_structured_rows=table_hint
                            )
                        ):
                            text = layout
                        else:
                            text = plain
                else:
                    text = plain

                if cls == "text" and clips:
                    vector_clips[i] = clips
                    for ref_path, b64, _y, _cap in clips:
                        images[ref_path] = b64

                text = _postprocess_pdf_text(text)
                if cls == "text" and vector_clips.get(i):
                    text = _inject_figure_markdown_before_captions(text, vector_clips[i])

                texts.append(text)
                classes.append(cls)

            texts = _strip_repeating_lines(texts, classes)
            scanned_indices = [i for i, c in enumerate(classes) if c == "scanned"]

            if scanned_indices and include_scanned_images:
                with parser_worker_limit(
                    "pdf_render", CONFIG.pdf_render_max_workers, context=self.context
                ):
                    rendered = _render_scanned_pages(
                        pdf,
                        content,
                        scanned_indices,
                        scale,
                        quality,
                        CONFIG.pdf_render_max_edge,
                        self.context,
                    )
                images.update(_encode_rendered_page_images(rendered, base_name))

            embedded: dict[int, list[tuple[str, str, float]]] = {}
            if EXTRACT_EMBEDDED_IMAGES:
                with _PDFIUM_LOCK:
                    embedded = _extract_embedded_images(pdf, classes, pdfium_r, base_name, quality)
                for refs in embedded.values():
                    for ref_path, b64, _y in refs:
                        images[ref_path] = b64
        finally:
            with _PDFIUM_LOCK:
                _close_pdfium_resource(pdf)

        embedded_count = 0
        vector_figure_count = 0
        blocks: list[str] = []
        source_blocks: list[dict[str, Any]] = []
        image_source_types: dict[str, str] = {}
        offset = 0

        def place(part: str) -> int:
            nonlocal offset
            if blocks:
                offset += 2
            start = offset
            blocks.append(part)
            offset += len(part)
            return start

        for i in range(page_count):
            page_only: dict[str, Any] = {
                "type": "pdf",
                "page": i + 1,
                "mapping": "exact",
            }
            if classes[i] == "scanned":
                page_filename = f"{base_name}_page_{i + 1}.jpg"
                image_source_types[f"images/{page_filename}"] = "scanned_pdf"
                part = image_markdown(f"images/{page_filename}")
                start = place(part)
                source_blocks.append(
                    {"start": start, "end": start + len(part), "locator": page_only}
                )
            else:
                stripped = texts[i].strip()
                if stripped:
                    start = place(stripped)
                    source_blocks.extend(
                        page_blocks(
                            stripped,
                            start,
                            i + 1,
                            line_boxes.get(i) or [],
                            geometries.get(i),
                        )
                    )
                vector_figure_count += len(vector_clips.get(i, []))
                for ref_path, _b64, _y, _caption in vector_clips.get(i, []):
                    image_source_types[ref_path] = "pdf_text_layer"
                page_images = list(embedded.get(i, []))
                page_images.sort(key=lambda item: item[2], reverse=True)
                for ref_path, _b64, _y in page_images:
                    image_source_types[ref_path] = "pdf_text_layer"
                    part = image_markdown(ref_path)
                    start = place(part)
                    source_blocks.append(
                        {
                            "start": start,
                            "end": start + len(part),
                            "locator": dict(page_only),
                        }
                    )
                    embedded_count += 1

        content_text = "\n\n".join(blocks)
        metadata: dict[str, Any] = {
            "page_count": page_count,
            "scanned_page_count": len(scanned_indices),
            "text_page_count": page_count - len(scanned_indices),
            "embedded_image_count": embedded_count,
            "vector_figure_count": vector_figure_count,
            "image_source_type": ("scanned_pdf" if scanned_indices else "pdf_text_layer"),
            "image_source_types": json.dumps(image_source_types, ensure_ascii=False),
        }

        logger.info(
            "PDFParser: %s -> %d pages (%d scanned, %d text), embedded_images=%d, content_len=%d",
            self.file_name,
            page_count,
            len(scanned_indices),
            page_count - len(scanned_indices),
            embedded_count,
            len(content_text),
        )
        document = Document(
            content=content_text,
            images=images,
            metadata=metadata,
            source_blocks=source_blocks,
        )
        return document, scanned_indices


__all__ = [
    "EMBED_MAX_IMAGES",
    "EMBED_MIN_AREA_RATIO",
    "EMBED_MIN_PIXELS",
    "EMBED_REPEAT_PAGE_FRAC",
    "EXTRACT_EMBEDDED_IMAGES",
    "FORCE_SCANNED_PDF",
    "LAYOUT_ORDERING",
    "PDFParser",
    "PDFScannedParser",
    "PDF_STREAM_RENDER_BATCH_PAGES",
    "RENDER_VECTOR_FIGURES",
    "SCAN_IMAGE_AREA_RATIO",
    "SCAN_MIN_CHARS_PER_PAGE",
    "SOURCE_BOXES",
    "_chars_to_layout_markdown",
    "_classify_page",
    "_decode_embedded_image_pil",
    "_encode_rendered_page_images",
    "_extract_embedded_images",
    "_extract_layout_text",
    "_extract_page_text",
    "_extract_vector_figure_clips",
    "_filter_reading_columns",
    "_group_lines",
    "_is_artifact_column",
    "_iter_scanned_pages_from_path",
    "_join_line_glyphs",
    "_looks_like_numeric_table_columns",
    "_merge_orphan_punctuation_lines",
    "_normalize_image_quality",
    "_page_chars",
    "_page_image_area_ratio",
    "_plain_is_well_formed",
    "_point_in_boxes",
    "_postprocess_pdf_text",
    "_render_page_to_jpeg",
    "_segments_to_markdown",
    "_select_embedded_images",
    "_select_mp_context",
    "_should_prefer_plain",
    "_split_columns",
    "_split_line_at_gaps",
    "_strip_repeating_lines",
    "_tail_looks_like_numeric_table",
    "_write_pdf_temp_file",
]
