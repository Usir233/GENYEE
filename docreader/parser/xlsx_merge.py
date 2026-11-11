"""Fill merged cell values before pandas reads an XLSX workbook."""

from __future__ import annotations

import logging
import os
import re
import zipfile
from decimal import Decimal, ROUND_HALF_UP, localcontext
from io import BytesIO

from defusedxml import ElementTree as DefusedET

from docreader.parser.archive import (
    ArchiveLimitError,
    ZipReadBudget,
    safe_read_zip_member,
    validate_zip_archive,
)

logger = logging.getLogger(__name__)

# Maximum total merged cells allowed to be materialized on a single worksheet.
MAX_MATERIALIZED_MERGE_CELLS = 200_000

# --- Percentage number formats (opt-in) --------------------------------------
# Excel keeps a percentage as the stored fraction plus a display format: the
# cell holds 0.61123366 and "0.00%" renders it as 61.12%. openpyxl and pandas
# read only the fraction, so a value the user sees as 61.12% reaches the
# pipeline as 0.6112 — a 100x error that a model will copy verbatim into a
# report. When this switch is on, percentage cells are materialized as the
# literal text the spreadsheet shows ("61.12%") instead.
#
# It is off by default on purpose: the cell changes from a number into a
# string, so anything that sorts, aggregates or computes with it sees different
# input. With it off, every workbook is read exactly as before. A single upload
# can override it through parser_engine_overrides.xlsx_percent_as_text.
PERCENT_AS_TEXT_ENV = "DOCREADER_XLSX_PERCENT_AS_TEXT"

# Only percentages are materialized. Currency and thousands separators change
# how a number is displayed, not what it means ("1,234,567" and 1234567 are the
# same number), so rewriting them would churn cells for no semantic gain, and
# date formats are already converted to datetimes by openpyxl.

# --- Same-row hint fallback (opt-in, off by default) -------------------------
# Workbooks occasionally leave one cell of a percentage row without a
# percentage format (a real financial statement had 折旧率 0.0592 formatted as
# "0.00_ ;[Red]\-0.00\ "). When a row carries at least this many explicitly
# percentage-formatted cells, an unformatted 0 < |value| < 1 number in that row
# is treated as a percentage too. Only consulted while percent materialization
# is on; 0 disables the fallback.
ROW_HINT_MIN_ENV = "DOCREADER_XLSX_PCT_ROW_HINT_MIN"
_DEFAULT_ROW_HINT_MIN = 0

# Only materialize a known numeric percentage pattern. Excel literals, conditions,
# scaling and arbitrary decorations require a full format renderer; leave those
# values untouched rather than guessing that every '%' means multiply by 100.
_PERCENT_PATTERN = re.compile(
    r"(?P<sign>-?)(?P<open>\()?(?P<group>#,##)?0"
    r"(?:\.(?P<decimals>0+))?%(?P<close>\))?"
)
_FORMAT_COLOR = re.compile(
    r"\[(?:Black|Blue|Cyan|Green|Magenta|Red|White|Yellow|Color[0-9]+)\]", re.I
)


def _env_bool(name: str, default: bool) -> bool:
    value = os.environ.get(name)
    if value is None or not str(value).strip():
        return default
    return str(value).strip().lower() in {"1", "true", "yes", "y", "on"}


def _env_int(name: str, default: int) -> int:
    try:
        return int(str(os.environ.get(name, default)).strip())
    except (TypeError, ValueError):
        return default


def percent_as_text_default() -> bool:
    """Deployment-wide default for materializing percentage formats.

    Read per call (not at import time) so the value can be flipped without a
    module reload; a per-upload override still wins over it.
    """
    return _env_bool(PERCENT_AS_TEXT_ENV, False)


def _percentage_number(value, decimals: int, grouping: str = "") -> str:
    # Excel rounds displayed ties away from zero; Python float formatting
    # rounds ties to even. Scale in decimal to avoid introducing binary ties.
    with localcontext() as context:
        context.rounding = ROUND_HALF_UP
        context.prec = max(28, len(str(value)) + decimals + 4)
        amount = Decimal(str(value)) * 100
        return f"{amount:{grouping}.{decimals}f}"


def _format_percentage(value, number_format: str):
    """Render supported Excel percentage formats, leaving other formats intact."""
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    if not number_format or "%" not in number_format:
        return None
    # Quoted/escaped percentages are literal text, not percentage operators.
    # Reject unsupported conditions across all sections before selecting one.
    cleaned = _FORMAT_COLOR.sub("", number_format)
    if any(char in cleaned for char in ('"', "\\", "_", "*", "[", "]")):
        return None
    sections = cleaned.split(";")
    if len(sections) > 4:
        return None
    section_index = 1 if value < 0 and len(sections) >= 2 else 0
    if value == 0 and len(sections) >= 3:
        section_index = 2
    match = _PERCENT_PATTERN.fullmatch(sections[section_index].strip())
    if not match or bool(match["open"]) != bool(match["close"]):
        return None
    decimals = len(match["decimals"] or "")
    # Excel's negative section formats the magnitude; a single section instead
    # supplies the minus sign automatically.
    amount = abs(value) if section_index == 1 else value
    grouping = "," if match["group"] else ""
    try:
        number = _percentage_number(amount, decimals, grouping)
        return f"{match['sign']}{match['open'] or ''}{number}%{match['close'] or ''}"
    except (TypeError, ValueError, OverflowError):
        return None


def _materialize_percentage_cells(ws, row_hint_min: int) -> tuple[int, int]:
    """Rewrite the percentage-formatted numbers of *ws* as their display text.

    Must run before merged ranges are filled: only the master cell of a merge
    carries a number_format while the covered cells are General, so filling
    first would copy a bare fraction into rows 2..n and leave it unconverted —
    the same row would then show both "0.55%" and 0.005509433890047721.

    Returns ``(materialized, hinted)`` cell counts.
    """
    row_hints: dict = {}
    if row_hint_min > 0:
        # Count the explicit percentage formats before converting anything:
        # the conversion below resets number_format to General.
        for row in ws.iter_rows():
            count = sum(
                1
                for cell in row
                if type(cell.value) in (int, float)  # noqa: E721 - excludes bool
                and _format_percentage(cell.value, cell.number_format) is not None
            )
            if count:
                row_hints[row[0].row] = count

    materialized = 0
    hinted = 0
    for row in ws.iter_rows():
        hint = row_hints.get(row[0].row, 0)
        for cell in row:
            if type(cell.value) not in (int, float):  # noqa: E721 - excludes bool
                continue
            text = _format_percentage(cell.value, cell.number_format)
            if (
                text is None
                and "%" not in str(cell.number_format or "")
                and hint >= row_hint_min > 0
                and 0 < abs(cell.value) < 1
            ):
                # Fallback for a cell that lost its percentage format while its
                # row kept one. Logged because it rewrites an unformatted value.
                text = _percentage_number(cell.value, 2) + "%"
                hinted += 1
                logger.info(
                    "XLSX pre-parse: %s!%s%d has no percentage format but its row "
                    "does; reading it as %.2f%% (stored value %r)",
                    ws.title,
                    cell.column_letter,
                    cell.row,
                    float(cell.value) * 100,
                    cell.value,
                )
            if text is None:
                continue
            cell.value = text
            cell.number_format = "General"
            materialized += 1
    return materialized, hinted


def _local_tag(tag: str) -> str:
    return tag.rsplit("}", 1)[-1] if "}" in tag else tag


def _preclip_xlsx_merge_ranges_xml(
    content: bytes, max_merge_cells: int
) -> tuple[bytes, bool]:
    """Clip unpopulated tail merge ranges and enforce cell budget BEFORE openpyxl loads.

    openpyxl's ``bind_merged_cells`` allocates a ``MergedCell`` object for every
    coordinate in ``<mergeCell ref="..."/>`` during ``load_workbook()`` itself,
    so a single ``<mergeCell ref="A1:XFD1048576"/>`` would hang/OOM inside
    ``load_workbook`` unless bounded at the XML layer first.
    """
    from openpyxl.utils.cell import (
        coordinate_from_string,
        column_index_from_string,
        get_column_letter,
        range_boundaries,
    )

    with zipfile.ZipFile(BytesIO(content), "r") as zf:
        validate_zip_archive(zf)
        read_budget = ZipReadBudget()
        cached_parts: dict[str, bytes] = {}
        rewritten_parts: dict[str, bytes] = {}

        for info in zf.infolist():
            norm_name = info.filename.replace("\\", "/")
            if not (
                norm_name.startswith("xl/worksheets/") and norm_name.endswith(".xml")
            ):
                continue
            raw_xml = safe_read_zip_member(zf, info, budget=read_budget)
            cached_parts[info.filename] = raw_xml
            if b"mergeCell" not in raw_xml:
                continue

            try:
                root = DefusedET.fromstring(raw_xml)
            except Exception:
                continue

            max_row = 0
            max_col = 0
            for elem in root.iter():
                ltag = _local_tag(elem.tag)
                if ltag == "c":
                    ref = elem.attrib.get("r")
                    if ref:
                        try:
                            col_str, row_idx = coordinate_from_string(ref)
                            col_idx = column_index_from_string(col_str)
                            if row_idx > max_row:
                                max_row = row_idx
                            if col_idx > max_col:
                                max_col = col_idx
                        except ValueError:
                            pass
                    elif len(elem) > 0:
                        # Row-relative cell without explicit 'r' attribute
                        pass
                elif ltag == "row" and max_col == 0:
                    r_attr = elem.attrib.get("r")
                    if r_attr and r_attr.isdigit():
                        max_row = max(max_row, int(r_attr))

            sheet_changed = False
            sheet_budget = 0
            for merge_parent in [
                el for el in root.iter() if _local_tag(el.tag) == "mergeCells"
            ]:
                for merge_el in list(merge_parent):
                    if _local_tag(merge_el.tag) != "mergeCell":
                        continue
                    ref = merge_el.attrib.get("ref", "")
                    if not ref:
                        merge_parent.remove(merge_el)
                        sheet_changed = True
                        continue
                    try:
                        min_col, min_row, rng_max_col, rng_max_row = range_boundaries(
                            ref
                        )
                    except ValueError:
                        merge_parent.remove(merge_el)
                        sheet_changed = True
                        continue

                    if max_row <= 0 or max_col <= 0:
                        merge_parent.remove(merge_el)
                        sheet_changed = True
                        continue

                    raw_span = (rng_max_row - min_row + 1) * (
                        rng_max_col - min_col + 1
                    )
                    unbounded_tail = (
                        (rng_max_row - max_row) > 64
                        or (rng_max_col - max_col) > 32
                        or raw_span > max_merge_cells
                    )
                    clip_max_row = (
                        min(rng_max_row, max_row) if unbounded_tail else rng_max_row
                    )
                    clip_max_col = (
                        min(rng_max_col, max_col) if unbounded_tail else rng_max_col
                    )
                    if clip_max_row < min_row or clip_max_col < min_col:
                        merge_parent.remove(merge_el)
                        sheet_changed = True
                        continue

                    span_cells = (clip_max_row - min_row + 1) * (
                        clip_max_col - min_col + 1
                    )
                    sheet_budget += span_cells
                    if sheet_budget > max_merge_cells:
                        raise ArchiveLimitError(
                            f"Worksheet '{norm_name}' merged cell expansion "
                            f"({sheet_budget}) exceeds limit ({max_merge_cells})"
                        )

                    if clip_max_row != rng_max_row or clip_max_col != rng_max_col:
                        if clip_max_row == min_row and clip_max_col == min_col:
                            merge_parent.remove(merge_el)
                        else:
                            new_ref = (
                                f"{get_column_letter(min_col)}{min_row}:"
                                f"{get_column_letter(clip_max_col)}{clip_max_row}"
                            )
                            merge_el.set("ref", new_ref)
                        sheet_changed = True

                remaining = [
                    c for c in merge_parent if _local_tag(c.tag) == "mergeCell"
                ]
                if sheet_changed:
                    merge_parent.set("count", str(len(remaining)))

            if sheet_changed:
                rewritten_parts[info.filename] = DefusedET.tostring(
                    root, encoding="utf-8", xml_declaration=True
                )

        if not rewritten_parts:
            return content, False

        out_zip = BytesIO()
        with zipfile.ZipFile(out_zip, "w", compression=zipfile.ZIP_DEFLATED) as dst:
            for info in zf.infolist():
                payload = rewritten_parts.get(info.filename)
                if payload is None:
                    payload = cached_parts.get(info.filename)
                if payload is None:
                    payload = safe_read_zip_member(zf, info, budget=read_budget)
                dst.writestr(info, payload)
        return out_zip.getvalue(), True


def _worksheet_populated_bounds(ws) -> tuple[int, int]:
    """Return (max_row, max_col) of worksheet cells after XML merge pre-clipping."""
    cells = getattr(ws, "_cells", None)
    if not cells:
        return 0, 0
    max_row = 0
    max_col = 0
    for row_idx, col_idx in cells.keys():
        if row_idx > max_row:
            max_row = row_idx
        if col_idx > max_col:
            max_col = col_idx
    return max_row, max_col


def fill_merged_cells_xlsx(
    content: bytes,
    percent_as_text: bool | None = None,
    *,
    max_merge_cells: int = MAX_MATERIALIZED_MERGE_CELLS,
) -> bytes:
    """Unmerge ranges and copy the master cell value into every covered cell.

    openpyxl only stores values on the top-left cell of a merge; pandas then
    sees NaN in the rest. Filling makes row-wise RAG chunks retain context.

    ``percent_as_text`` also materializes percentage-formatted numbers as the
    text the spreadsheet displays ("0.6112" -> "61.12%"), because the stored
    fraction is 100x off from what a reader sees. ``None`` (the default) uses
    the ``DOCREADER_XLSX_PERCENT_AS_TEXT`` deployment default, which is off, so
    the default path is unchanged.
    """
    if percent_as_text is None:
        percent_as_text = percent_as_text_default()

    if not zipfile.is_zipfile(BytesIO(content)):
        return content

    content, preclipped = _preclip_xlsx_merge_ranges_xml(content, max_merge_cells)

    from openpyxl import load_workbook

    wb = load_workbook(BytesIO(content), data_only=True)
    changed = preclipped
    materialized_cells = 0
    hinted_cells = 0
    row_hint_min = (
        _env_int(ROW_HINT_MIN_ENV, _DEFAULT_ROW_HINT_MIN) if percent_as_text else 0
    )

    for ws in wb.worksheets:
        if percent_as_text:
            # Before the merge fill below — see _materialize_percentage_cells.
            materialized, hinted = _materialize_percentage_cells(ws, row_hint_min)
            materialized_cells += materialized
            hinted_cells += hinted
            if materialized:
                changed = True

        if not ws.merged_cells.ranges:
            continue
        bound_row, bound_col = _worksheet_populated_bounds(ws)
        sheet_materialized_budget = 0
        for merge_range in list(ws.merged_cells.ranges):
            master_value = (
                ws._cells.get((merge_range.min_row, merge_range.min_col)).value
                if (merge_range.min_row, merge_range.min_col) in ws._cells
                else None
            )
            ws.unmerge_cells(str(merge_range))
            changed = True
            if bound_row <= 0 or bound_col <= 0:
                continue
            clipped_max_row = min(merge_range.max_row, bound_row)
            clipped_max_col = min(merge_range.max_col, bound_col)
            if clipped_max_row < merge_range.min_row or clipped_max_col < merge_range.min_col:
                continue
            span_cells = (clipped_max_row - merge_range.min_row + 1) * (
                clipped_max_col - merge_range.min_col + 1
            )
            sheet_materialized_budget += span_cells
            if sheet_materialized_budget > max_merge_cells:
                raise ArchiveLimitError(
                    f"Worksheet '{ws.title}' merged cell expansion "
                    f"({sheet_materialized_budget}) exceeds limit ({max_merge_cells})"
                )
            for row in range(merge_range.min_row, clipped_max_row + 1):
                for col in range(merge_range.min_col, clipped_max_col + 1):
                    ws.cell(row, col).value = master_value

    if not changed:
        return content

    # Charts are not part of the cell text extracted by DocReader. Remove
    # dedicated chart sheets through the public workbook API, then clear
    # embedded worksheet charts (openpyxl has no public removal API for them)
    # before saving this temporary workbook.
    active_sheet = wb.active
    for chart_sheet in list(wb.chartsheets):
        wb.remove(chart_sheet)
    if active_sheet in wb.worksheets:
        wb.active = active_sheet
    elif wb.worksheets:
        wb.active = wb.worksheets[0]
    for ws in wb.worksheets:
        ws._charts.clear()

    out = BytesIO()
    wb.save(out)
    if materialized_cells:
        logger.info(
            "Materialized %d percentage cell(s) in XLSX before parse "
            "(%d recovered by the same-row hint fallback, threshold=%d)",
            materialized_cells,
            hinted_cells,
            row_hint_min,
        )
    logger.info("Filled merged cells in XLSX before parse")
    return out.getvalue()
