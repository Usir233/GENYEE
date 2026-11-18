"""Sparse XLSX ingestion retains physical row/column identity without a dense frame."""

import io
import unittest
from unittest.mock import patch

import openpyxl
import pandas as pd
from docreader.parser import xlsx_rows
from docreader.parser.concurrency import ParserCancelled
from docreader.parser.excel_parser import ExcelParser
from openpyxl.drawing.image import Image as SheetImage
from PIL import Image


def workbook_bytes(build):
    book = openpyxl.Workbook()
    build(book.active)
    data = io.BytesIO()
    book.save(data)
    return data.getvalue()


class SparseXLSXRowsTest(unittest.TestCase):
    def parse_without_dense_frame(self, data, **kwargs):
        # Fail before pandas can allocate a rectangle, especially for the
        # extreme-coordinate case. Both before/after runs remain bounded.
        with patch.object(
            pd.ExcelFile, "parse", side_effect=AssertionError("dense worksheet allocation")
        ):
            return ExcelParser(file_type="xlsx", xlsx_ocr_enabled=False, **kwargs).parse(data)

    def test_extreme_coordinates_keep_all_values_and_source_rows(self):
        def build(sheet):
            sheet["A1"] = "First"
            sheet["XFD1048576"] = "000123"

        document = self.parse_without_dense_frame(workbook_bytes(build))
        self.assertEqual(document.content, "A: First\nXFD: 000123\n")
        self.assertEqual([b["locator"]["row_start"] for b in document.source_blocks], [1, 1048576])

    def test_semantic_headers_and_merges_keep_original_columns(self):
        def build(sheet):
            sheet.append(["B", None, "B_2", None])
            sheet.append(["00123", 1234567890123456, True, 0.25])
            sheet["D2"].number_format = "0%"
            sheet.merge_cells("D2:D3")
            sheet["A3"] = "next"

        document = self.parse_without_dense_frame(
            workbook_bytes(build), xlsx_first_row_as_header=True, xlsx_percent_as_text=True
        )
        self.assertIn("B: 00123,B_2: 1234567890123456,B_2_2: True,D: 25%", document.content)
        self.assertIn("B: next,D: 25%", document.content)
        self.assertEqual([b["locator"]["row_start"] for b in document.source_blocks], [2, 3])

    def test_header_option_preserves_single_row_and_missing_row_one(self):
        for row in (1, 1000):
            with self.subTest(row=row):
                document = self.parse_without_dense_frame(
                    workbook_bytes(lambda sheet, r=row: sheet.cell(r, 100, "Value")),
                    xlsx_first_row_as_header=True,
                )
                self.assertEqual(document.content, "CV: Value\n")
                self.assertEqual(document.source_blocks[0]["locator"]["row_start"], row)

    def test_image_only_rows_keep_their_original_position(self):
        def build(sheet):
            sheet["A1"] = "First"
            sheet["C5"] = "Last"
            for anchor, color in (("B3", "red"), ("Z8", "blue")):
                data = io.BytesIO()
                Image.new("RGB", (200, 150), color).save(data, format="PNG")
                data.seek(0)
                sheet.add_image(SheetImage(data), anchor)

        document = self.parse_without_dense_frame(workbook_bytes(build))
        self.assertEqual(len(document.images), 2)
        self.assertEqual([b["locator"]["row_start"] for b in document.source_blocks], [1, 3, 5, 8])
        self.assertLess(document.content.index("B: !["), document.content.index("C: Last"))
        self.assertGreater(document.content.index("Z: !["), document.content.index("C: Last"))

    def test_images_outside_columns_keep_their_row_and_existing_label(self):
        for include_inner in (False, True):
            with self.subTest(include_inner=include_inner):

                def build(sheet, _inner=include_inner):
                    sheet["A1"] = "First"
                    sheet["C5"] = "Last"
                    anchors = [("Z3", "green"), ("Z8", "blue")]
                    if _inner:
                        anchors.insert(0, ("B3", "red"))
                    for anchor, color in anchors:
                        data = io.BytesIO()
                        Image.new("RGB", (200, 150), color).save(data, format="PNG")
                        data.seek(0)
                        sheet.add_image(SheetImage(data), anchor)

                document = self.parse_without_dense_frame(workbook_bytes(build))
                self.assertEqual(len(document.images), 2 + int(include_inner))
                self.assertEqual(
                    [b["locator"]["row_start"] for b in document.source_blocks], [1, 3, 5, 8]
                )
                block = document.source_blocks[1]
                row_three = document.content[block["start"] : block["end"]]
                self.assertIn("图片: ![", row_three)
                if include_inner:
                    self.assertIn("B: ![", row_three)
                self.assertGreater(
                    document.content.index("Z: !["), document.content.index("C: Last")
                )

    def test_cancellation_stops_the_sparse_bounds_pass(self):
        class Context:
            active = True

            def is_active(self):
                return self.active

        context = Context()
        decoded_rows = []
        original = xlsx_rows._populated_rows

        def cancel_after_first_row(sheet, current_context):
            for row in original(sheet, current_context):
                decoded_rows.append(row[0])
                yield row
                context.active = False

        data = workbook_bytes(lambda sheet: [sheet.cell(row, 1, row) for row in range(1, 100)])
        with (
            patch.object(xlsx_rows, "_populated_rows", side_effect=cancel_after_first_row),
            self.assertRaises(ParserCancelled),
        ):
            self.parse_without_dense_frame(data, context=context)
        self.assertEqual(decoded_rows, [1])


if __name__ == "__main__":
    unittest.main()
