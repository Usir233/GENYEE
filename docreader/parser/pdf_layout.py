"""Layout-aware PDF text, column, figure-region, and line-box extraction helpers."""

from __future__ import annotations

import logging
import os
import re
import statistics
from collections import Counter
from typing import Any

from docreader.parser.source_locator import PageGeometry
from docreader.utils.image_ref import image_markdown

logger = logging.getLogger("docreader.parser.pdf_parser")


def _env_float(name: str, default: float) -> float:
    v = os.environ.get(name)
    if v is None or not str(v).strip():
        return default
    try:
        return float(v)
    except ValueError:
        return default


def _env_int(name: str, default: int) -> int:
    v = os.environ.get(name)
    if v is None or not str(v).strip():
        return default
    try:
        return int(str(v).strip())
    except ValueError:
        return default


def _env_bool(name: str, default: bool) -> bool:
    v = os.environ.get(name)
    if v is None or not str(v).strip():
        return default
    return str(v).strip().lower() in {"1", "true", "yes", "y", "on"}


# --- Layout-aware text extraction (native text pages) ---------------------
LAYOUT_ORDERING = _env_bool("DOCREADER_PDF_LAYOUT_ORDERING", True)
WORD_GAP_WIDTH_RATIO = _env_float("DOCREADER_PDF_WORD_GAP_WIDTH_RATIO", 0.4)
DETECT_HEADINGS = _env_bool("DOCREADER_PDF_DETECT_HEADINGS", True)
FILTER_HIDDEN_TEXT = _env_bool("DOCREADER_PDF_FILTER_HIDDEN_TEXT", True)
MARGIN_COL_WIDTH_RATIO = _env_float("DOCREADER_PDF_MARGIN_COL_WIDTH_RATIO", 0.12)
MIN_HEADING_LINE_CHARS = _env_int("DOCREADER_PDF_MIN_HEADING_LINE_CHARS", 8)
SANITIZE_PDF_TEXT = _env_bool("DOCREADER_PDF_SANITIZE_TEXT", True)
STRIP_CHART_TEXT_DEBRIS = _env_bool("DOCREADER_PDF_STRIP_CHART_DEBRIS", True)
RENDER_VECTOR_FIGURES = _env_bool("DOCREADER_PDF_RENDER_VECTOR_FIGURES", True)
MIN_CHART_REGION_CHARS = _env_int("DOCREADER_PDF_MIN_CHART_REGION_CHARS", 18)
MIN_CHART_REGION_AREA_RATIO = _env_float("DOCREADER_PDF_MIN_CHART_REGION_AREA", 0.015)
MAX_CHART_REGION_AREA_RATIO = _env_float("DOCREADER_PDF_MAX_CHART_REGION_AREA", 0.42)
MAX_FIGURE_HEIGHT_RATIO = _env_float("DOCREADER_PDF_MAX_FIGURE_HEIGHT_RATIO", 0.38)

# A visual line is cut where a gap wider than one glyph height coincides with
# a jump in the text stream: the gutter between two columns, not a word space.
LINE_SEGMENT_GAP_RATIO = 1.0
LINE_SEGMENT_STREAM_JUMP = 8
LINE_SEGMENT_MIN_CHARS = 3

# pdfium / Adobe text layers often emit U+FFFE for missing hyphenation or ligatures.
_PDF_ARTIFACT_RE = re.compile(r"[\u00ad\u200b-\u200f\ufeff\ufffe\uffff]")
_PDF_ARTIFACT_JOIN_RE = re.compile(r"(\w)[\u00ad\ufffe](\w)")
_CHART_DEBRIS_LINE_RE = re.compile(
    r"^(?:"
    r"[\d\s.]+|"
    r"\d{1,2}|"
    r"\d+-layer|"
    r"iter\.\s*\(1e4\)|"
    r"(?:training|test)\s+error\s*\(%\)"
    r")$",
    re.IGNORECASE,
)
_CHART_LAYER_RE = re.compile(r"^\d+-layer$", re.IGNORECASE)
_FIGURE_CAPTION_RE = re.compile(r"^Figure\s+\d+\b", re.IGNORECASE)
_FIGURE_CAPTION_SEARCH_RE = re.compile(r"\bFigure\s+(\d+)\b", re.IGNORECASE)
_ARXIV_LINE_RE = re.compile(r"^arXiv:\s*\S+", re.IGNORECASE)
_PAGE_NUM_LINE_RE = re.compile(r"^\d{1,3}$")
_PAGE_CONTEXT_RE = re.compile(
    r"(?:\bpage\b|\bp\.\s*\d+\b|\b\d+\s*(?:of|/)\s*\d+\b)",
    re.IGNORECASE,
)
_CHART_CONTEXT_RE = re.compile(
    r"(?:\bFigure\s+\d+\b|\biter\.\s*\(1e4\)|"
    r"\b(?:training|test)\s+error\b|\b\d+-layer\b)",
    re.IGNORECASE,
)
_NUMERIC_ROW_RE = re.compile(
    r"^(?P<label>[^\d\n]+?)\s+"
    r"(?P<value>[+-]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?%?)$"
)
_NUMERIC_VALUE_LINE_RE = re.compile(r"^[+-]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?%?$")


def _close_pdfium_resource(resource: Any) -> None:
    close = getattr(resource, "close", None)
    if close:
        close()


def _extract_plain_page_text(page: Any) -> str:
    """Plain top-to-bottom text extraction (fallback path)."""
    textpage = None
    try:
        textpage = page.get_textpage()
        return str(textpage.get_text_range())
    finally:
        _close_pdfium_resource(textpage)


def _sanitize_pdf_text(text: str) -> str:
    """Remove PDF text-layer placeholders and repair broken hyphenations."""
    if not text:
        return text
    text = _PDF_ARTIFACT_RE.sub("", text)
    text = _PDF_ARTIFACT_JOIN_RE.sub(r"\1\2", text)
    return text


def _is_chart_debris_line(line: str) -> bool:
    t = line.strip()
    if not t:
        return False
    if _CHART_DEBRIS_LINE_RE.match(t):
        return True
    if _CHART_LAYER_RE.match(t):
        return True
    return bool(
        re.fullmatch(r"[\d\s.()-]+", t) and len(t) <= 24 and sum(c.isdigit() for c in t) >= 3
    )


def _strip_chart_text_debris(text: str) -> str:
    """Drop runs of axis/legend lines leaked from vector figures into the text layer."""
    if not text:
        return text
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    out: list[str] = []
    i = 0
    while i < len(lines):
        if _is_chart_debris_line(lines[i]):
            j = i
            while j < len(lines) and (_is_chart_debris_line(lines[j]) or not lines[j].strip()):
                j += 1
            context_start = max(0, i - 1)
            context_end = min(len(lines), j + 1)
            has_chart_context = any(
                _CHART_CONTEXT_RE.search(lines[k]) for k in range(context_start, context_end)
            )
            table_context = _tail_looks_like_numeric_table(lines[max(0, i - 2) : j])
            if j - i >= 3 and has_chart_context and not table_context:
                i = j
                continue
        out.append(lines[i])
        i += 1
    return "\n".join(out)


def _is_probable_page_num_line(lines: list[str], index: int) -> bool:
    """Return whether a standalone number is likely a page marker."""
    if index < 0 or index >= len(lines):
        return False
    if not _PAGE_NUM_LINE_RE.fullmatch(lines[index].strip()):
        return False
    nonempty = [i for i, line in enumerate(lines) if line.strip()]
    if not nonempty:
        return False
    pos = nonempty.index(index)
    neighbors: list[int] = []
    if pos:
        neighbors.append(nonempty[pos - 1])
    if pos + 1 < len(nonempty):
        neighbors.append(nonempty[pos + 1])
    if any(_ARXIV_LINE_RE.match(lines[i].strip()) for i in neighbors):
        return True
    if index in (nonempty[0], nonempty[-1]):
        return any(_PAGE_CONTEXT_RE.search(lines[i]) for i in neighbors)
    return False


def _strip_arxiv_and_page_num_lines(text: str) -> str:
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    kept: list[str] = []
    for i, ln in enumerate(lines):
        t = ln.strip()
        if _ARXIV_LINE_RE.match(t):
            continue
        if _is_probable_page_num_line(lines, i):
            continue
        if "arXiv:" in ln:
            ln = re.sub(r"\s*arXiv:\s*\S+\s*(?:\[[^\]]+\])?\s*[^\n]*", "", ln).strip()
            if not ln:
                continue
        kept.append(ln)
    return "\n".join(kept)


def _strip_lines_above_figure_captions(text: str) -> str:
    """Remove diagram/chart label lines that sit immediately above a Figure caption."""
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    out: list[str] = []
    for ln in lines:
        if _line_has_figure_caption(ln):
            while out and _is_figure_interior_line(out[-1]):
                if _tail_looks_like_numeric_table(out):
                    break
                out.pop()
            out.append(ln)
        else:
            out.append(ln)
    return "\n".join(out)


def _tail_looks_like_numeric_table(lines: list[str]) -> bool:
    """Detect labelled or alternating label/value rows at the text tail."""
    tail = [line for line in lines[-10:] if line.strip()]
    rows = _structured_numeric_rows("\n".join(tail))
    if rows and rows[-1][0] == len(tail) - 1:
        return True

    numeric = re.compile(r"^[+-]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?%?$")
    for index, header in enumerate(tail[:-2]):
        if (
            header.strip()
            and any(char.isalpha() for char in header)
            and all(numeric.fullmatch(value.strip()) for value in tail[index + 1 :])
        ):
            return True
    pairs = 0
    index = len(tail) - 1
    while index >= 1:
        label = tail[index - 1].strip()
        value = tail[index].strip()
        if (
            label
            and any(char.isalpha() for char in label)
            and len(label) <= 60
            and numeric.fullmatch(value)
        ):
            pairs += 1
            index -= 2
        else:
            break
    return pairs >= 2


def _is_body_paragraph_line(text: str) -> bool:
    t = text.strip()
    if len(t) < 48:
        return False
    return len(t.split()) >= 8


def _is_figure_interior_line(text: str) -> bool:
    """Short, non-body line directly above a Figure caption (diagram labels, ticks)."""
    t = text.strip()
    if not t or _FIGURE_CAPTION_RE.match(t):
        return False
    if _ARXIV_LINE_RE.match(t) or _PAGE_NUM_LINE_RE.match(t):
        return True
    if _is_body_paragraph_line(t):
        return False
    if _is_chart_debris_line(t):
        return True
    if t.endswith((".", "。", "!", "?", "！")) and len(t) >= 15:
        return False
    if len(t.split()) >= 7:
        return False
    return len(t) <= 40


def _postprocess_pdf_text(text: str) -> str:
    if SANITIZE_PDF_TEXT:
        text = _sanitize_pdf_text(text)
    text = _strip_arxiv_and_page_num_lines(text)
    text = _strip_lines_above_figure_captions(text)
    if STRIP_CHART_TEXT_DEBRIS:
        text = _strip_chart_text_debris(text)
    return text


def _char_looks_chart_axis_tick(ch: str) -> bool:
    """Axis tick / numeric chart labels only (not words like ``layer`` in diagrams)."""
    t = ch.strip()
    if not t:
        return False
    if len(t) == 1 and t in "0123456789.%()-":
        return True
    if _CHART_LAYER_RE.match(t):
        return True
    if re.fullmatch(r"iter\.\s*\(1e4\)", t, re.I):
        return True
    return bool(re.fullmatch(r"(?:training|test)\s+error\s*\(%\)", t, re.I))


def _chars_bbox(
    char_list: list[dict[str, Any]],
) -> tuple[float, float, float, float]:
    return (
        float(min(c["x0"] for c in char_list)),
        float(min(c["y0"] for c in char_list)),
        float(max(c["x1"] for c in char_list)),
        float(max(c["y1"] for c in char_list)),
    )


def _bbox_area_ratio(
    bbox: tuple[float, float, float, float], page_w: float, page_h: float
) -> float:
    page_area = float(page_w) * float(page_h)
    if page_area <= 0:
        return 0.0
    x0, y0, x1, y1 = bbox
    return float(max(0.0, (x1 - x0) * (y1 - y0) / page_area))


def _chart_region_bbox(
    chars: list[dict[str, Any]], page_w: float, page_h: float
) -> tuple[float, float, float, float] | None:
    """Bounding box of numeric chart axis labels (fallback when caption walk fails)."""
    chart = [c for c in chars if _char_looks_chart_axis_tick(c["ch"])]
    if len(chart) < MIN_CHART_REGION_CHARS:
        return None
    bbox = _chars_bbox(chart)
    ratio = _bbox_area_ratio(bbox, page_w, page_h)
    if ratio < MIN_CHART_REGION_AREA_RATIO or ratio > MAX_CHART_REGION_AREA_RATIO:
        return None
    x0, y0, x1, y1 = bbox
    pad_x = max(8.0, (x1 - x0) * 0.08)
    pad_y = max(8.0, (y1 - y0) * 0.08)
    return (
        max(0.0, x0 - pad_x),
        max(0.0, y0 - pad_y),
        min(page_w, x1 + pad_x),
        min(page_h, y1 + pad_y),
    )


def _expand_chart_bbox(
    bbox: tuple[float, float, float, float],
    page_w: float,
    page_h: float,
    margin_frac: float = 0.18,
) -> tuple[float, float, float, float]:
    x0, y0, x1, y1 = bbox
    dx = (x1 - x0) * margin_frac
    dy = (y1 - y0) * margin_frac
    return (
        max(0.0, x0 - dx),
        max(0.0, y0 - dy),
        min(page_w, x1 + dx),
        min(page_h, y1 + dy),
    )


def _group_lines_with_chars(chars: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Group glyphs into lines; each line includes its char list and bbox."""
    if not chars:
        return []
    heights = [c["y1"] - c["y0"] for c in chars if c["y1"] > c["y0"]]
    med_h = statistics.median(heights) if heights else 1.0
    ordered = sorted(chars, key=lambda c: -(c["y0"] + c["y1"]) / 2)
    groups: list[list[dict[str, Any]]] = []
    cur: list[dict[str, Any]] = []
    ref: float | None = None
    for c in ordered:
        yc = (c["y0"] + c["y1"]) / 2
        if ref is None or abs(yc - ref) <= 0.5 * med_h:
            cur.append(c)
            ref = yc if ref is None else ref
        else:
            groups.append(cur)
            cur = [c]
            ref = yc
    if cur:
        groups.append(cur)

    lines: list[dict[str, Any]] = []
    for grp in groups:
        grp_sorted = sorted(grp, key=lambda c: c["x0"])
        text = _join_line_glyphs(grp_sorted)
        if not text:
            continue
        hs = [c["y1"] - c["y0"] for c in grp_sorted if c["y1"] > c["y0"]]
        lines.append(
            {
                "text": text,
                "h": statistics.median(hs) if hs else med_h,
                "chars": grp_sorted,
                "bbox": _chars_bbox(grp_sorted),
            }
        )
    return lines


def _line_has_figure_caption(text: str) -> bool:
    return bool(_FIGURE_CAPTION_SEARCH_RE.search((text or "").strip()))


def _bbox_above_caption(
    lines: list[dict[str, Any]], cap_i: int, page_w: float, page_h: float
) -> tuple[float, float, float, float]:
    """Region above a Figure caption line (PDF coords, bottom-left origin)."""
    cap_bbox = lines[cap_i]["bbox"]
    cap_top = float(cap_bbox[3])
    x0, x1 = float(cap_bbox[0]), float(cap_bbox[2])
    fig_h = page_h * min(MAX_FIGURE_HEIGHT_RATIO, 0.35)
    y_bottom = cap_top
    y_top = min(page_h, cap_top + fig_h)

    for j in range(cap_i - 1, -1, -1):
        t = lines[j]["text"]
        b = lines[j]["bbox"]
        if b[3] < y_bottom - 4:
            continue
        if b[1] > y_top + 4:
            break
        if _is_body_paragraph_line(t) and not _is_figure_interior_line(t):
            break
        if _is_figure_interior_line(t) or _is_chart_debris_line(t) or not t.strip():
            x0 = min(x0, float(b[0]))
            x1 = max(x1, float(b[2]))
            y_top = max(y_top, min(page_h, float(b[3]) + fig_h * 0.15))

    min_h = page_h * 0.08
    if y_top - y_bottom < min_h:
        y_top = min(page_h, y_bottom + min_h)
    margin_x = max(8.0, (x1 - x0) * 0.05)
    return (
        max(0.0, x0 - margin_x),
        y_bottom,
        min(page_w, x1 + margin_x),
        y_top,
    )


def _cap_bbox_height(
    bbox: tuple[float, float, float, float], page_h: float, cap_y_top: float
) -> tuple[float, float, float, float]:
    """Limit figure bbox height (PDF coords, bottom-left origin)."""
    x0, y0, x1, y1 = bbox
    max_top = min(y1, cap_y_top + page_h * MAX_FIGURE_HEIGHT_RATIO)
    if max_top <= y0:
        return bbox
    return (x0, y0, x1, max_top)


def _inject_figure_markdown_before_captions(
    text: str, clips: list[tuple[str, str, float, str]]
) -> str:
    """Place ``![...]()`` immediately before each Figure caption line in page text."""
    if not clips:
        return text
    lines = text.replace("\r\n", "\n").replace("\r", "\n").split("\n")
    clip_idx = 0
    for i, ln in enumerate(lines):
        if clip_idx >= len(clips):
            break
        if not _line_has_figure_caption(ln):
            continue
        if i > 0 and lines[i - 1].lstrip().startswith("!["):
            continue
        ref_path = clips[clip_idx][0]
        img_md = image_markdown(ref_path)
        lines[i] = f"{img_md}\n\n{ln}"
        clip_idx += 1
    return "\n".join(lines)


def _collect_invisible_boxes(page: Any, raw: Any) -> list[tuple[float, float, float, float]]:
    """Bounding boxes of invisible (render-mode 3) text objects on the page."""
    boxes: list[tuple[float, float, float, float]] = []
    try:
        for obj in page.get_objects():
            if obj.type != raw.FPDF_PAGEOBJ_TEXT:
                continue
            try:
                mode = raw.FPDFTextObj_GetTextRenderMode(obj.raw)
            except Exception:
                continue
            if mode != raw.FPDF_TEXTRENDERMODE_INVISIBLE:
                continue
            try:
                left, bottom, right, top = obj.get_bounds()
            except Exception:
                continue
            boxes.append(
                (
                    float(min(left, right)),
                    float(min(bottom, top)),
                    float(max(left, right)),
                    float(max(bottom, top)),
                )
            )
    except Exception:
        return []
    return boxes


def _point_in_boxes(x: float, y: float, boxes: list[tuple[float, float, float, float]]) -> bool:
    return any(x0 <= x <= x1 and y0 <= y <= y1 for x0, y0, x1, y1 in boxes)


def _page_chars(
    textpage: Any, page: Any, raw: Any, *, return_filter_info: bool = False
) -> tuple[Any, ...]:
    """Return ``(chars, page_width)`` with hidden/off-page glyphs filtered."""
    n = textpage.count_chars()
    if n <= 0:
        return ([], 0.0, False) if return_filter_info else ([], 0.0)
    try:
        crop_left, crop_bottom, crop_right, crop_top = page.get_cropbox()
        crop_left, crop_right = min(crop_left, crop_right), max(crop_left, crop_right)
        crop_bottom, crop_top = min(crop_bottom, crop_top), max(crop_bottom, crop_top)
    except Exception:
        width, height = page.get_size()
        crop_left, crop_bottom, crop_right, crop_top = 0, 0, width, height
    width = float(crop_right - crop_left)
    invisible = _collect_invisible_boxes(page, raw) if FILTER_HIDDEN_TEXT else []

    chars: list[dict[str, Any]] = []
    filtered_count = 0
    for i in range(n):
        try:
            left, _, right, _ = textpage.get_charbox(i)
            try:
                _, bottom, _, top = textpage.get_charbox(i, loose=True)
            except TypeError:
                _, bottom, _, top = textpage.get_charbox(i)
        except Exception:
            continue
        ch = textpage.get_text_range(i, 1)
        if ch in ("\r", "\n"):
            continue
        x0, x1 = (left, right) if left <= right else (right, left)
        y0, y1 = (bottom, top) if bottom <= top else (top, bottom)
        if FILTER_HIDDEN_TEXT:
            if x1 < crop_left or x0 > crop_right or y1 < crop_bottom or y0 > crop_top:
                filtered_count += 1
                continue
            if invisible and _point_in_boxes((x0 + x1) / 2, (y0 + y1) / 2, invisible):
                filtered_count += 1
                continue
        chars.append({"x0": x0, "y0": y0, "x1": x1, "y1": y1, "ch": ch, "i": i})
    if return_filter_info:
        return chars, width, filtered_count > 0
    return chars, width


def _page_has_invisible_text(page: Any, raw: Any) -> bool:
    """Whether ``page`` contains text objects hidden from normal rendering."""
    if not FILTER_HIDDEN_TEXT:
        return False
    try:
        return bool(_collect_invisible_boxes(page, raw))
    except Exception:
        return False


def _page_has_filtered_text(page: Any, raw: Any) -> bool:
    """Whether layout filtering would discard hidden or off-page glyphs."""
    if not FILTER_HIDDEN_TEXT:
        return False
    if _page_has_invisible_text(page, raw):
        return True
    textpage = None
    try:
        textpage = page.get_textpage()
        _, _, filtered = _page_chars(textpage, page, raw, return_filter_info=True)
        return bool(filtered)
    except Exception:
        return False
    finally:
        _close_pdfium_resource(textpage)


def _find_split(items: list[dict[str, Any]], axis: str, min_gap: float) -> float | None:
    """Return a coordinate at the widest clean gap on ``axis`` ('x'), or None."""
    lo, hi = ("x0", "x1") if axis == "x" else ("y0", "y1")
    intervals = sorted(((s[lo], s[hi]) for s in items), key=lambda iv: iv[0])
    cur_end = float(intervals[0][1])
    best_gap = 0.0
    best_cut: float | None = None
    for a, b in intervals[1:]:
        gap = float(a) - cur_end
        if gap >= min_gap and gap > best_gap:
            best_gap, best_cut = gap, cur_end + gap / 2
        if float(b) > cur_end:
            cur_end = float(b)
    return best_cut


def _split_columns(
    chars: list[dict[str, Any]], scale: float, width: float, depth: int = 0
) -> list[list[dict[str, Any]]]:
    """Split glyphs into reading-order columns at full-height gutters."""
    if len(chars) <= 1 or depth > 10:
        return [chars]
    min_gap = max(scale * 2.5, width * 0.04)
    cut = _find_split(chars, "x", min_gap)
    if cut is None:
        return [chars]
    left = [c for c in chars if (c["x0"] + c["x1"]) / 2 < cut]
    right = [c for c in chars if (c["x0"] + c["x1"]) / 2 >= cut]
    if not left or not right:
        return [chars]
    return _split_columns(left, scale, width, depth + 1) + _split_columns(
        right, scale, width, depth + 1
    )


def _column_x_span(chars: list[dict[str, Any]]) -> float:
    if not chars:
        return 0.0
    return float(max(c["x1"] for c in chars) - min(c["x0"] for c in chars))


def _column_single_line_fraction(lines: list[dict[str, Any]]) -> float:
    if not lines:
        return 0.0
    single = sum(1 for ln in lines if len(ln["text"]) <= 2)
    return single / len(lines)


def _is_artifact_column(chars: list[dict[str, Any]], width: float) -> bool:
    """Detect margin strips and vertical watermarks (e.g. arXiv sidebar)."""
    if not chars or width <= 0:
        return True
    span = _column_x_span(chars)
    if span <= 0:
        return True
    lines = _group_lines(chars)
    single_frac = _column_single_line_fraction(lines)
    narrow = span / width < MARGIN_COL_WIDTH_RATIO
    if narrow and single_frac >= 0.45:
        return True
    ys = [(c["y0"] + c["y1"]) / 2 for c in chars]
    y_span = max(ys) - min(ys)
    return bool(y_span > span * 3.5 and len(chars) >= 8 and single_frac >= 0.35)


def _filter_reading_columns(
    chars: list[dict[str, Any]], scale: float, width: float
) -> list[list[dict[str, Any]]]:
    """Split into columns and drop margin / watermark strips."""
    cols = _split_columns(chars, scale, width)
    table_pair = _find_numeric_table_columns(chars, scale, width, cols)
    if table_pair is not None:
        value_col, label_col = table_pair
        table_cols = [
            c for c in cols if not _is_artifact_column(c, width) or c is value_col or c is label_col
        ]
        if table_cols:
            return [[char for column in table_cols for char in column]]
        return [chars]
    kept = [c for c in cols if not _is_artifact_column(c, width)]
    if kept:
        return kept
    if len(cols) > 1:
        return [max(cols, key=_column_x_span)]
    return cols


def _is_numeric_value_line(text: str) -> bool:
    t = (text or "").strip()
    return bool(
        _NUMERIC_VALUE_LINE_RE.fullmatch(t)
        or (re.fullmatch(r"[\d\s./()%+-]+", t) and any(char.isdigit() for char in t))
    )


def _looks_like_numeric_table_columns(
    chars: list[dict[str, Any]],
    scale: float,
    width: float,
    columns: list[list[dict[str, Any]]] | None = None,
) -> bool:
    """Identify a narrow, aligned value column before artifact filtering."""
    return _find_numeric_table_columns(chars, scale, width, columns) is not None


def _find_numeric_table_columns(
    chars: list[dict[str, Any]],
    scale: float,
    width: float,
    columns: list[list[dict[str, Any]]] | None = None,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]] | None:
    """Return the aligned value/label columns when a numeric table is found."""
    if not chars or width <= 0:
        return None
    cols = columns if columns is not None else _split_columns(chars, scale, width)
    if len(cols) < 2:
        return None

    for value_col in cols:
        span_ratio = _column_x_span(value_col) / width
        if span_ratio > 0.22:
            continue
        value_lines = _group_lines_with_chars(value_col)
        if len(value_lines) < 2:
            continue
        numeric_lines = [line for line in value_lines if _is_numeric_value_line(line["text"])]
        if not numeric_lines or len(numeric_lines) / len(value_lines) < 0.4:
            continue
        text_lines = [
            line
            for line in value_lines
            if not _is_numeric_value_line(line["text"])
            and any(char.isalpha() for char in line["text"])
        ]
        if not text_lines:
            continue

        value_ys = [(line["bbox"][1] + line["bbox"][3]) / 2 for line in numeric_lines]
        for label_col in cols:
            if label_col is value_col:
                continue
            label_lines = _group_lines_with_chars(label_col)
            label_ys = [(line["bbox"][1] + line["bbox"][3]) / 2 for line in label_lines]
            label_text_ys = [
                (line["bbox"][1] + line["bbox"][3]) / 2
                for line in label_lines
                if any(char.isalpha() for char in line["text"])
            ]
            tolerance = max(scale * 0.75, 2.0)
            header_ys = [(line["bbox"][1] + line["bbox"][3]) / 2 for line in text_lines]
            header_aligned = any(
                abs(y - other_y) <= tolerance for y in header_ys for other_y in label_text_ys
            )
            if not header_aligned:
                continue
            aligned = sum(
                any(abs(y - other_y) <= tolerance for other_y in label_ys) for y in value_ys
            )
            required_alignment = 1 if len(value_ys) == 1 else max(2, (len(value_ys) + 1) // 2)
            if aligned >= required_alignment:
                return value_col, label_col
    return None


def _merge_orphan_punctuation_lines(
    lines: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Attach lines that are only punctuation to the previous visual line."""
    if not lines:
        return []
    merged: list[dict[str, Any]] = []
    for ln in lines:
        t = ln["text"].strip()
        if merged and t and len(t) <= 4 and all(c in ".,;:!?…·" or c.isspace() for c in t):
            suffix = "".join(t.split())
            prev = merged[-1]["text"]
            if suffix and prev and not prev.endswith((" ", "-")):
                merged[-1]["text"] = prev + suffix
            else:
                merged[-1]["text"] = (prev + " " + t).strip()
            continue
        merged.append(dict(ln))
    return merged


def _join_line_glyphs(ln_sorted: list[dict[str, Any]]) -> str:
    """Join a visual line's glyphs, inferring word spaces from horizontal gaps."""
    if not ln_sorted:
        return ""
    widths = [c["x1"] - c["x0"] for c in ln_sorted if c["x1"] > c["x0"]]
    med_w = statistics.median(widths) if widths else 1.0
    gap_threshold = med_w * WORD_GAP_WIDTH_RATIO

    parts: list[str] = []
    for i, cur in enumerate(ln_sorted):
        ch = cur["ch"]
        if i == 0:
            parts.append(ch)
            continue
        prev = ln_sorted[i - 1]
        if ch.isspace() or prev["ch"].isspace():
            if not ch.isspace() or (parts and not parts[-1].endswith(" ")):
                parts.append(ch)
            continue
        gap = cur["x0"] - prev["x1"]
        numeric_pair = (
            prev["ch"] in "0123456789.,%+-"
            and ch in "0123456789.,%+-"
            and (prev["ch"].isdigit() or ch.isdigit())
        )
        if gap > gap_threshold and not (numeric_pair and gap <= med_w * 1.25):
            parts.append(" ")
        parts.append(ch)
    return "".join(parts).strip()


def _group_lines(chars: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Group a column's glyphs into lines (top-to-bottom, glyphs sorted by x)."""
    if not chars:
        return []
    heights = [c["y1"] - c["y0"] for c in chars if c["y1"] - c["y0"] > 0]
    med_h = statistics.median(heights) if heights else 1.0

    ordered = sorted(chars, key=lambda c: -(c["y0"] + c["y1"]) / 2)
    lines: list[list[dict[str, Any]]] = []
    cur: list[dict[str, Any]] = []
    ref: float | None = None
    for c in ordered:
        yc = (c["y0"] + c["y1"]) / 2
        if ref is None or abs(yc - ref) <= 0.5 * med_h:
            cur.append(c)
            ref = yc if ref is None else ref
        else:
            lines.append(cur)
            cur = [c]
            ref = yc
    if cur:
        lines.append(cur)

    out: list[dict[str, Any]] = []
    for ln in lines:
        ln_sorted = sorted(ln, key=lambda c: c["x0"])
        text = _join_line_glyphs(ln_sorted)
        if not text:
            continue
        hs = [c["y1"] - c["y0"] for c in ln_sorted if c["y1"] - c["y0"] > 0]
        out.append({"h": statistics.median(hs) if hs else med_h, "text": text})
    return out


def _segments_to_markdown(lines: list[dict[str, Any]]) -> str:
    """Render merged lines to text, promoting visually large lines to headings."""
    if not lines:
        return ""
    body = statistics.median([ln["h"] for ln in lines])

    def level(ln: dict[str, Any]) -> int:
        txt = ln["text"]
        if not DETECT_HEADINGS or body <= 0 or len(txt) > 80 or len(txt) < MIN_HEADING_LINE_CHARS:
            return 0
        if txt[-1:] in ".。!！?？,，;；:：":
            return 0
        r = ln["h"] / body
        if r >= 2.0:
            return 1
        if r >= 1.6:
            return 2
        if r >= 1.35:
            return 3
        return 0

    levels = [level(ln) for ln in lines]
    if sum(1 for x in levels if x) > max(1, int(0.4 * len(lines))):
        levels = [0] * len(lines)

    out = [
        ("#" * lv + " " + ln["text"]) if lv else str(ln["text"])
        for ln, lv in zip(lines, levels, strict=False)
    ]
    return "\n".join(out)


def _chars_to_layout_markdown(chars: list[dict[str, Any]], scale: float, width: float) -> str:
    blocks: list[str] = []
    for col in _filter_reading_columns(chars, scale, width):
        lines = _merge_orphan_punctuation_lines(_group_lines(col))
        md = _segments_to_markdown(lines)
        if md:
            blocks.append(md)
    return "\n".join(blocks)


def _layout_line_stats(text: str) -> tuple[int, int, int]:
    """Return (line_count, single_char_line_count, punct_only_line_count)."""
    lines = [ln.strip() for ln in text.splitlines() if ln.strip()]
    if not lines:
        return 0, 0, 0
    single = sum(1 for ln in lines if len(ln) <= 2)
    punct_only = sum(1 for ln in lines if len(ln) <= 4 and re.fullmatch(r"[\s.,;:!?…·\-–—]+", ln))
    return len(lines), single, punct_only


def _layout_garbled_line_fraction(text: str) -> float:
    """Share of lines that look like broken OCR (many 1–2 letter tokens)."""
    lines = [ln.strip() for ln in text.splitlines() if ln.strip()]
    if not lines:
        return 0.0
    garbled = 0
    for ln in lines:
        words = ln.split()
        if len(words) >= 6 and sum(1 for w in words if len(w) <= 2) / len(words) > 0.45:
            garbled += 1
    return garbled / len(lines)


def _numeric_row_values(text: str) -> list[tuple[int, str, str]]:
    """Return compact ``(line_index, label, value)`` rows from plain text."""
    rows: list[tuple[int, str, str]] = []
    for index, line in enumerate((text or "").splitlines()):
        match = _NUMERIC_ROW_RE.fullmatch(line.strip())
        if not match:
            continue
        label = match.group("label").strip()
        if (
            not label
            or len(label) > 60
            or len(label.split()) > 4
            or label[-1:] in ".,;:!?"
            or not any(char.isalpha() for char in label)
        ):
            continue
        rows.append((index, label, match.group("value")))
    return rows


def _structured_numeric_rows(text: str) -> list[tuple[int, str, str]]:
    """Return numeric rows only when at least two occur near each other."""
    rows = _numeric_row_values(text)
    if len(rows) < 2:
        return []
    for first, second in zip(rows, rows[1:], strict=False):
        if second[0] - first[0] <= 3:
            return rows
    return []


def _normalized_numeric_value(value: str) -> str:
    return value.replace(",", "").lower()


def _plain_is_well_formed(plain: str) -> bool:
    """True when pdfium plain text already has usable words and punctuation."""
    plain = (plain or "").strip()
    if not plain:
        return False
    if re.search(r"\[\w+,\s", plain):
        return True
    if plain.count(" . . ") >= 2:
        return True
    words = re.findall(r"\S+", plain)
    if len(words) < 30:
        return False
    avg_len = sum(len(w) for w in words) / len(words)
    return avg_len >= 5.0


def _should_prefer_plain(plain: str, layout: str, *, allow_structured_rows: bool = True) -> bool:
    """Fall back to pdfium plain text when layout reconstruction looks broken."""
    layout = (layout or "").strip()
    plain = (plain or "").strip()
    if not layout:
        return True
    if not plain:
        return False

    numbers = re.compile(r"[+−-]?\d+(?:[.,:/]\d+)*(?:[%‰])?")
    if Counter(numbers.findall(plain)) != Counter(numbers.findall(layout)):
        return True

    plain_rows = _structured_numeric_rows(plain) if allow_structured_rows else []
    if plain_rows:
        layout_rows = _structured_numeric_rows(layout)
        plain_values = Counter(_normalized_numeric_value(row[2]) for row in plain_rows)
        layout_values = Counter(_normalized_numeric_value(row[2]) for row in layout_rows)
        missing_values = sum((plain_values - layout_values).values())
        if missing_values:
            return True
        if len(plain_rows) >= len(layout_rows):
            return True

    n, single, punct_only = _layout_line_stats(layout)
    if n == 0:
        return True
    if single / n >= 0.18 or punct_only / n >= 0.12:
        return True
    garbled = _layout_garbled_line_fraction(layout)
    if garbled >= 0.20 and _layout_garbled_line_fraction(plain) < 0.08:
        return True
    if re.search(r"\[\w+,\s", plain) and re.search(r"\[\w+\s+\w+\s+\d", layout):
        return True
    for ln in plain.splitlines():
        probe = ln.strip()
        if len(probe) < 24:
            continue
        alnum = "".join(c for c in probe if c.isalnum())[:16]
        if len(alnum) < 12:
            continue
        layout_alnum = "".join(c for c in layout if c.isalnum())
        if alnum not in layout_alnum:
            return True
        break
    return False


def _extract_layout_text(page: Any, raw: Any, *, return_metadata: bool = False) -> Any:
    """Layout-aware extraction: reading order + headings + hidden-text filter."""
    textpage = None
    filtered = False
    table_hint = False
    try:
        textpage = page.get_textpage()
        chars, width, filtered = _page_chars(textpage, page, raw, return_filter_info=True)
        if not chars:
            result = ""
        else:
            heights = [c["y1"] - c["y0"] for c in chars if c["y1"] - c["y0"] > 0]
            scale = (statistics.median(heights) if heights else 1.0) or 1.0
            table_hint = _looks_like_numeric_table_columns(chars, scale, width)
            result = _chars_to_layout_markdown(chars, scale, width)
    except Exception:
        logger.debug("layout extraction failed; using plain text", exc_info=True)
        result = _extract_plain_page_text(page)
    finally:
        _close_pdfium_resource(textpage)
    if return_metadata:
        return result, bool(filtered), table_hint
    return result


def _split_line_at_gaps(line: dict[str, Any]) -> list[dict[str, Any]]:
    """Cut a visual line where it crosses from one column into another."""
    whole = [{"text": line["text"], "bbox": line["bbox"]}]
    chars = [c for c in line["chars"] if not c["ch"].isspace()]
    if len(chars) < 2 * LINE_SEGMENT_MIN_CHARS:
        return whole
    min_gap = max(max(c["y1"] - c["y0"] for c in chars), 1.0) * LINE_SEGMENT_GAP_RATIO
    segments: list[list[dict[str, Any]]] = [[chars[0]]]
    for prev, cur in zip(chars, chars[1:], strict=False):
        jump = abs(cur.get("i", 0) - prev.get("i", 0))
        if cur["x0"] - prev["x1"] > min_gap and jump > LINE_SEGMENT_STREAM_JUMP:
            segments.append([cur])
        else:
            segments[-1].append(cur)
    if len(segments) == 1 or any(len(seg) < LINE_SEGMENT_MIN_CHARS for seg in segments):
        return whole
    out = []
    for seg in segments:
        text = _join_line_glyphs(seg)
        if text.strip():
            out.append({"text": text, "bbox": _chars_bbox(seg)})
    return out or whole


def _page_line_boxes(page: Any, raw: Any) -> list[dict[str, Any]]:
    """Visual lines of a text page as ``{"text", "bbox"}`` in PDF points."""
    textpage = None
    try:
        textpage = page.get_textpage()
        chars, width = _page_chars(textpage, page, raw)
        if not chars:
            return []
        heights = [c["y1"] - c["y0"] for c in chars if c["y1"] - c["y0"] > 0]
        scale = (statistics.median(heights) if heights else 1.0) or 1.0
        lines: list[dict[str, Any]] = []
        for col in _split_columns(chars, scale, width):
            for ln in _group_lines_with_chars(col):
                lines.extend(_split_line_at_gaps(ln))
        return lines
    except Exception:
        logger.debug("line box extraction failed", exc_info=True)
        return []
    finally:
        _close_pdfium_resource(textpage)


def _page_geometry(page: Any) -> PageGeometry | None:
    try:
        return PageGeometry(page.get_cropbox(), page.get_rotation())
    except Exception:
        logger.debug("page geometry unavailable", exc_info=True)
        return None


def _strip_repeating_lines(texts: list[str], classes: list[str]) -> list[str]:
    """Remove running headers/footers that repeat across most text pages."""
    text_indices = [i for i, c in enumerate(classes) if c == "text"]
    if len(text_indices) < 4:
        return list(texts)

    counter: Counter[str] = Counter()
    for i in text_indices:
        lines = [ln.strip() for ln in texts[i].splitlines() if ln.strip()]
        if not lines:
            continue
        for edge in {lines[0], lines[-1]}:
            if len(edge) <= 80:
                counter[edge] += 1

    threshold = max(2, int(len(text_indices) * 0.6))
    repeating = {line for line, count in counter.items() if count >= threshold}
    if not repeating:
        return list(texts)

    cleaned: list[str] = []
    for i, text in enumerate(texts):
        if classes[i] != "text":
            cleaned.append(text)
            continue
        kept = [ln for ln in text.splitlines() if ln.strip() not in repeating]
        cleaned.append("\n".join(kept))
    return cleaned
