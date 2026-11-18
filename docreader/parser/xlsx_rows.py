"""Read decoded XLSX cells without expanding gaps into a dense worksheet."""

import zipfile
from io import BytesIO

from docreader.parser.archive import ArchiveLimitError
from docreader.parser.concurrency import check_cancellation
from openpyxl import load_workbook
from openpyxl.utils import column_index_from_string, get_column_letter
from openpyxl.worksheet._reader import WorkSheetParser


def _cell_value(cell):
    """Keep the scalar conversion used by pandas' openpyxl reader."""
    value = cell["value"]
    if value is None:
        return ""
    if cell["data_type"] == "e":
        return float("nan")
    if cell["data_type"] == "n":
        integer = int(value)
        return integer if integer == value else float(value)
    return value


def _decoded_rows(sheet, context):
    # ReadOnlyWorksheet.iter_rows() synthesizes all missing rows and columns.
    # Use the same openpyxl XML decoder directly to retain types/styles/shared
    # strings while visiting only cells actually stored in the worksheet.
    with sheet._get_source() as source:
        parser = WorkSheetParser(
            source,
            sheet._shared_strings,
            data_only=sheet.parent.data_only,
            epoch=sheet.parent.epoch,
            date_formats=sheet.parent._date_formats,
            timedelta_formats=sheet.parent._timedelta_formats,
        )
        for row_number, cells in parser.parse():
            check_cancellation(context)
            yield row_number, cells


def _populated_rows(sheet, context):
    for row_number, cells in _decoded_rows(sheet, context):
        values = {}
        for cell in cells:
            value = _cell_value(cell)
            if value != "":
                values[cell["column"]] = value
        if values:
            yield row_number, values


def validate_table_expansion(content, max_cells, context=None):
    """Bound the workbook rectangles a table converter may materialize.

    Use stored rows/cells, not the untrusted dimension hint. Include formatted
    blanks and uncached formulas: pandas visits these before trimming empties.
    """
    if not zipfile.is_zipfile(BytesIO(content)):
        return
    book = load_workbook(BytesIO(content), read_only=True, data_only=True, keep_links=False)
    try:
        total = 0
        for sheet in book.worksheets:
            max_row = max_col = 0
            for row_number, cells in _decoded_rows(sheet, context):
                max_row = max(max_row, row_number)
                max_col = max(max_col, max((cell["column"] for cell in cells), default=1))
                expanded = total + max_row * max_col
                if expanded > max_cells:
                    raise ArchiveLimitError(
                        f"MarkItDown XLSX table expansion ({expanded} cells) exceeds limit "
                        f"({max_cells}); choose the builtin parser engine for sparse worksheets"
                    )
            total += max_row * max_col
    finally:
        book.close()


def iter_worksheet_rows(sheet, image_cells, first_row_as_header, header_labeler, context=None):
    """Yield (zero-based source row, [(source column, label, value), ...]).

    Two passes retain the old last-data-row/column and header semantics without
    keeping a rectangular frame. Image-only rows up to the last data row retain
    their position; callers keep their outside-column and orphan-row handling.
    """
    max_row = max_col = 0
    header = {}
    for row_number, values in _populated_rows(sheet, context):
        max_row = max(max_row, row_number)
        max_col = max(max_col, max(values))
        if row_number == 1:
            header = values
    if max_row == 0:
        return

    use_header = first_row_as_header and max_row >= 2
    labels = (
        header_labeler([header.get(col, "") for col in range(1, max_col + 1)])
        if use_header
        else None
    )
    image_columns: dict[int, set[int]] = {}
    for row_number, col_letter in image_cells:
        if 1 <= row_number <= max_row and not (use_header and row_number == 1):
            columns = image_columns.setdefault(row_number, set())
            try:
                col = column_index_from_string(col_letter)
            except ValueError:
                continue
            if 1 <= col <= max_col:
                columns.add(col)
    image_rows = iter(sorted(image_columns))
    next_image_row = next(image_rows, None)

    def assemble(row_number, values):
        for col in image_columns.get(row_number, ()):
            values.setdefault(col, None)
        return row_number - 1, [
            (col - 1, labels[col - 1] if labels else get_column_letter(col), values[col])
            for col in sorted(values)
        ]

    for row_number, values in _populated_rows(sheet, context):
        if use_header and row_number == 1:
            continue
        while next_image_row is not None and next_image_row < row_number:
            check_cancellation(context)
            yield assemble(next_image_row, {})
            next_image_row = next(image_rows, None)
        if next_image_row == row_number:
            next_image_row = next(image_rows, None)
        yield assemble(row_number, values)
    while next_image_row is not None:
        check_cancellation(context)
        yield assemble(next_image_row, {})
        next_image_row = next(image_rows, None)
