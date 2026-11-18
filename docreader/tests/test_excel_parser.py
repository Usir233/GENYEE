import base64
import io
import os
import re
import shutil
import subprocess
import tempfile
import unittest
import zipfile
from unittest.mock import MagicMock, patch

import openpyxl
import pandas as pd
from openpyxl.chart import BarChart, Reference

from docreader.parser.excel_convert import detect_excel_format, engine_for_format
from docreader.parser.excel_parser import ExcelParser
from docreader.parser.markitdown_parser import StdMarkitdownParser
from docreader.parser.xlsx_merge import fill_merged_cells_xlsx
from docreader.parser.xlsx_repair import repair_xlsx_bytes, strip_unreadable_ranges_xlsx


def _xlsx_with_phantom_shared_strings() -> bytes:
    """Workbook with inline strings but a dangling sharedStrings manifest entry."""
    wb = openpyxl.Workbook()
    ws = wb.active
    ws["A1"] = "hello"
    ws["B1"] = 42
    bio = io.BytesIO()
    wb.save(bio)

    with tempfile.TemporaryDirectory() as tmpdir:
        with zipfile.ZipFile(io.BytesIO(bio.getvalue()), "r") as zin:
            zin.extractall(tmpdir)

        ct_path = f"{tmpdir}/[Content_Types].xml"
        with open(ct_path, encoding="utf-8") as f:
            ct = f.read()
        override = (
            '<Override PartName="/xl/sharedStrings.xml" '
            'ContentType="application/vnd.openxmlformats-officedocument.'
            'spreadsheetml.sharedStrings+xml"/>'
        )
        with open(ct_path, "w", encoding="utf-8") as f:
            f.write(ct.replace("</Types>", override + "</Types>"))

        out = io.BytesIO()
        with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout:
            for root, _, files in os.walk(tmpdir):
                for name in files:
                    path = os.path.join(root, name)
                    arc = os.path.relpath(path, tmpdir)
                    zout.write(path, arc)
        return out.getvalue()


def _xlsx_with_sheet_fragment(fragment: str) -> bytes:
    """A two-row workbook with ``fragment`` spliced into sheet1 before pageMargins,
    which is where OOXML puts data validations and scenarios."""
    wb = openpyxl.Workbook()
    ws = wb.active
    ws["A1"], ws["B1"] = "name", "qty"
    ws["A2"], ws["B2"] = "apple", 3
    bio = io.BytesIO()
    wb.save(bio)

    out = io.BytesIO()
    with (
        zipfile.ZipFile(io.BytesIO(bio.getvalue()), "r") as zin,
        zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout,
    ):
        for info in zin.infolist():
            data = zin.read(info.filename)
            if info.filename == "xl/worksheets/sheet1.xml":
                sheet = data.decode("utf-8")
                at = sheet.index("<pageMargins")
                data = (sheet[:at] + fragment + sheet[at:]).encode("utf-8")
            zout.writestr(info, data)
    return out.getvalue()


def _validation(sqref: str, formula: str = '"a,b"') -> str:
    return (
        f'<dataValidation type="list" allowBlank="1" sqref="{sqref}">'
        f"<formula1>{formula}</formula1></dataValidation>"
    )


class ExcelFormatDetectionTest(unittest.TestCase):
    def test_detect_xlsx_and_engine(self):
        wb = openpyxl.Workbook()
        bio = io.BytesIO()
        wb.save(bio)
        content = bio.getvalue()
        self.assertEqual(detect_excel_format(content), "xlsx")
        self.assertEqual(engine_for_format("xlsx"), "openpyxl")

    def test_detect_xls_magic(self):
        content = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1" + b"\x00" * 512
        self.assertEqual(detect_excel_format(content), "xls")
        self.assertEqual(engine_for_format("xls"), "xlrd")

    def test_open_legacy_xls_bytes_with_xlsx_extension(self):
        if shutil.which("soffice"):
            wb = openpyxl.Workbook()
            ws = wb.active
            ws["A1"] = "legacy"
            xlsx_bio = io.BytesIO()
            wb.save(xlsx_bio)
            with tempfile.TemporaryDirectory() as tmpdir:
                src = os.path.join(tmpdir, "sheet.xlsx")
                with open(src, "wb") as handle:
                    handle.write(xlsx_bio.getvalue())
                subprocess.run(
                    [
                        "soffice",
                        "--headless",
                        "--convert-to",
                        "xls",
                        "--outdir",
                        tmpdir,
                        src,
                    ],
                    check=True,
                    capture_output=True,
                )
                xls_path = os.path.join(tmpdir, "sheet.xls")
                with open(xls_path, "rb") as handle:
                    xls_bytes = handle.read()
            expected_token = "legacy"
        else:
            fixture_xls = os.path.join(
                os.path.dirname(__file__),
                "..",
                "..",
                "internal",
                "agent",
                "tools",
                "testdata",
                "legacy.xls",
            )
            with open(fixture_xls, "rb") as handle:
                xls_bytes = handle.read()
            expected_token = "苹果"

        document = ExcelParser(file_name="fake.xlsx", file_type="xlsx").parse_into_text(xls_bytes)
        self.assertIn(expected_token, document.content)


class XlsxUnreadableRangesTest(unittest.TestCase):
    """#3599: a range openpyxl cannot parse must not cost the whole document."""

    def test_whole_column_validation_aborts_openpyxl_without_the_fix(self):
        broken = _xlsx_with_sheet_fragment(
            f'<dataValidations count="1">{_validation("C:C")}</dataValidations>'
        )
        with self.assertRaisesRegex(TypeError, "MultiCellRange"):
            openpyxl.load_workbook(io.BytesIO(broken), data_only=True)
        with self.assertRaisesRegex(TypeError, "MultiCellRange"):
            pd.read_excel(io.BytesIO(broken), engine="openpyxl")

    def test_drops_each_range_spelling_openpyxl_rejects(self):
        # Measured on openpyxl 3.1.5; every one of these raised the reported
        # TypeError before the fix.
        for sqref in ("C:C", "1:1", "A1:B2,C3", "#REF!", "A1:XFD1048577"):
            with self.subTest(sqref=sqref):
                broken = _xlsx_with_sheet_fragment(
                    f'<dataValidations count="1">{_validation(sqref)}</dataValidations>'
                )
                fixed = strip_unreadable_ranges_xlsx(broken)
                self.assertIsNotNone(fixed)
                assert fixed is not None
                df = pd.read_excel(io.BytesIO(fixed), header=None, engine="openpyxl")
                self.assertEqual(df.values.tolist(), [["name", "qty"], ["apple", 3]])

    def test_drops_a_scenario_list_with_an_unreadable_range(self):
        broken = _xlsx_with_sheet_fragment(
            # The range belongs to the list, not to each scenario.
            '<scenarios sqref="A:A"><scenario name="s" count="1">'
            '<inputCells r="A1" val="1"/></scenario></scenarios>'
        )
        with self.assertRaisesRegex(TypeError, "MultiCellRange"):
            openpyxl.load_workbook(io.BytesIO(broken), data_only=True)
        fixed = strip_unreadable_ranges_xlsx(broken)
        self.assertIsNotNone(fixed)
        assert fixed is not None
        openpyxl.load_workbook(io.BytesIO(fixed), data_only=True)

    def test_keeps_a_readable_validation_next_to_an_unreadable_one(self):
        broken = _xlsx_with_sheet_fragment(
            '<dataValidations count="2">'
            f"{_validation('C:C')}{_validation('D2:D10', chr(34) + 'keep' + chr(34))}"
            "</dataValidations>"
        )
        fixed = strip_unreadable_ranges_xlsx(broken)
        self.assertIsNotNone(fixed)
        assert fixed is not None
        ws = openpyxl.load_workbook(io.BytesIO(fixed)).active
        self.assertEqual([str(dv.sqref) for dv in ws.data_validations.dataValidation], ["D2:D10"])

    def test_leaves_a_readable_workbook_untouched(self):
        fine = _xlsx_with_sheet_fragment(
            f'<dataValidations count="1">{_validation("C2:C10")}</dataValidations>'
        )
        self.assertIsNone(strip_unreadable_ranges_xlsx(fine))
        self.assertIsNone(strip_unreadable_ranges_xlsx(b"not a zip"))

    def test_excel_parser_reads_a_workbook_with_a_whole_column_validation(self):
        broken = _xlsx_with_sheet_fragment(
            f'<dataValidations count="1">{_validation("C:C")}</dataValidations>'
        )
        document = ExcelParser(file_name="feishu.xlsx", file_type="xlsx").parse_into_text(broken)
        self.assertIn("apple", document.content)

    def test_markitdown_parser_reads_a_workbook_with_a_whole_column_validation(self):
        broken = _xlsx_with_sheet_fragment(
            f'<dataValidations count="1">{_validation("C:C")}</dataValidations>'
        )
        document = StdMarkitdownParser(file_name="feishu.xlsx", file_type="xlsx").parse_into_text(
            broken
        )
        self.assertIn("apple", document.content)


class XlsxRepairTest(unittest.TestCase):
    def test_repair_removes_phantom_shared_strings_reference(self):
        broken = _xlsx_with_phantom_shared_strings()
        with self.assertRaises(KeyError):
            pd.read_excel(io.BytesIO(broken))

        repaired = repair_xlsx_bytes(broken)
        self.assertIsNotNone(repaired)
        assert repaired is not None
        df = pd.read_excel(io.BytesIO(repaired), header=None)
        self.assertEqual(df.values.tolist(), [["hello", 42]])

    def test_repair_skips_when_shared_string_cells_need_table(self):
        import xlsxwriter

        bio = io.BytesIO()
        wb = xlsxwriter.Workbook(bio, {"in_memory": True})
        ws = wb.add_worksheet()
        ws.write(0, 0, "hello")
        wb.close()

        with tempfile.TemporaryDirectory() as tmpdir:
            with zipfile.ZipFile(io.BytesIO(bio.getvalue()), "r") as zin:
                zin.extractall(tmpdir)
            os.remove(f"{tmpdir}/xl/sharedStrings.xml")

            out = io.BytesIO()
            with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout:
                for root, _, files in os.walk(tmpdir):
                    for name in files:
                        path = os.path.join(root, name)
                        arc = os.path.relpath(path, tmpdir)
                        zout.write(path, arc)
            broken = out.getvalue()

        self.assertIsNone(repair_xlsx_bytes(broken))


class XlsxMergeFillTest(unittest.TestCase):
    @staticmethod
    def _workbook_with_merged_cells_and_chart() -> bytes:
        wb = openpyxl.Workbook()
        ws = wb.active
        ws["A1"] = "title"
        ws.merge_cells("A1:B1")
        ws.append(["category", "value"])
        ws.append(["one", 1])
        ws.append(["two", 2])

        chart = BarChart()
        chart.add_data(
            Reference(ws, min_col=2, min_row=2, max_row=4),
            titles_from_data=True,
        )
        chart.set_categories(Reference(ws, min_col=1, min_row=3, max_row=4))
        ws.add_chart(chart, "D2")

        bio = io.BytesIO()
        wb.save(bio)
        return bio.getvalue()

    def test_fill_merged_cells_propagates_master_value(self):
        wb = openpyxl.Workbook()
        ws = wb.active
        ws["A1"] = "title"
        ws.merge_cells("A1:B1")
        ws["A2"] = "left"
        ws["B2"] = "right"
        ws.merge_cells("A2:A3")
        ws["B3"] = "only-b"
        bio = io.BytesIO()
        wb.save(bio)

        filled = fill_merged_cells_xlsx(bio.getvalue())
        out_wb = openpyxl.load_workbook(io.BytesIO(filled), data_only=True)
        out_ws = out_wb.active
        self.assertEqual(out_ws["B1"].value, "title")
        self.assertEqual(out_ws["A3"].value, "left")
        self.assertEqual(out_ws["B3"].value, "only-b")

    def test_fill_merged_cells_does_not_serialize_charts(self):
        content = self._workbook_with_merged_cells_and_chart()

        with patch(
            "openpyxl.chart._chart.ChartBase._write",
            side_effect=AttributeError("'Typed' object has no attribute 'to_tree'"),
        ):
            filled = fill_merged_cells_xlsx(content)

        out_wb = openpyxl.load_workbook(io.BytesIO(filled), data_only=True)
        out_ws = out_wb.active
        self.assertEqual(out_ws["B1"].value, "title")
        self.assertEqual(out_ws["A3"].value, "one")
        self.assertEqual(out_ws["B4"].value, 2)
        self.assertEqual(out_ws._charts, [])

    def test_excel_parser_reads_merged_workbook_with_chart(self):
        document = ExcelParser().parse_into_text(self._workbook_with_merged_cells_and_chart())

        self.assertIn("A: title,B: title", document.content)
        self.assertIn("A: one,B: 1", document.content)
        self.assertIn("A: two,B: 2", document.content)

    def test_chart_workbook_without_merges_passes_through_unchanged(self):
        content = self._workbook_with_merged_cells_and_chart()
        wb = openpyxl.load_workbook(io.BytesIO(content))
        wb.active.unmerge_cells("A1:B1")
        bio = io.BytesIO()
        wb.save(bio)
        content = bio.getvalue()

        self.assertEqual(fill_merged_cells_xlsx(content), content)

    def test_fill_does_not_serialize_dedicated_chart_sheets(self):
        content = self._workbook_with_merged_cells_and_chart()
        wb = openpyxl.load_workbook(io.BytesIO(content))
        ws = wb.active
        chart = BarChart()
        chart.add_data(
            Reference(ws, min_col=2, min_row=2, max_row=4),
            titles_from_data=True,
        )
        chart.set_categories(Reference(ws, min_col=1, min_row=3, max_row=4))
        chart_sheet = wb.create_chartsheet("Summary")
        chart_sheet.add_chart(chart)
        wb.active = chart_sheet
        bio = io.BytesIO()
        wb.save(bio)

        with patch(
            "openpyxl.chart._chart.ChartBase._write",
            side_effect=AttributeError("'Typed' object has no attribute 'to_tree'"),
        ):
            filled = fill_merged_cells_xlsx(bio.getvalue())

        out_wb = openpyxl.load_workbook(io.BytesIO(filled), data_only=True)
        self.assertEqual(out_wb.active._charts, [])
        self.assertEqual(out_wb.active.title, "Sheet")
        self.assertEqual(out_wb.chartsheets, [])

    def test_fill_keeps_active_worksheet_after_removing_earlier_chart_sheet(self):
        content = self._workbook_with_merged_cells_and_chart()
        wb = openpyxl.load_workbook(io.BytesIO(content))
        data_sheet = wb.active
        chart = BarChart()
        chart.add_data(
            Reference(data_sheet, min_col=2, min_row=2, max_row=4),
            titles_from_data=True,
        )
        chart_sheet = wb.create_chartsheet("Summary", 0)
        chart_sheet.add_chart(chart)
        wb.active = data_sheet
        bio = io.BytesIO()
        wb.save(bio)

        filled = fill_merged_cells_xlsx(bio.getvalue())

        out_wb = openpyxl.load_workbook(io.BytesIO(filled), data_only=True)
        self.assertEqual(out_wb.active.title, "Sheet")
        self.assertEqual(out_wb.chartsheets, [])

    def test_parse_en_mergecell_workbook(self):
        path = os.path.join(
            os.path.dirname(__file__),
            "..",
            "..",
            "testdata",
            "rag_test",
            "xlsx",
            "en_mergecell.xlsx",
        )
        if os.path.isfile(path):
            with open(path, "rb") as handle:
                payload = handle.read()
        else:
            wb = openpyxl.Workbook()
            ws = wb.active
            ws["A1"] = "A1"
            ws["A2"] = "A2"
            ws["B3"] = "B3"
            ws["C4"] = "C4"
            ws["C5"] = "C5"
            ws["C6"] = "C6"
            ws["A7"] = "A7"
            ws.merge_cells("A7:A8")
            ws["C9"] = "C9"
            ws["D10"] = "D10"
            ws["C11"] = "C11"
            ws["C12"] = "C12"
            bio = io.BytesIO()
            wb.save(bio)
            payload = bio.getvalue()
        document = ExcelParser().parse_into_text(payload)

        chunks = [chunk.content.strip() for chunk in document.chunks]
        self.assertEqual(len(chunks), 12)
        self.assertIn("A: A1", chunks[0])
        self.assertIn("A: A2", chunks[1])
        self.assertIn("B: B3", chunks[2])
        self.assertNotIn("Unnamed:", document.content)
        self.assertIn("A: A7", chunks[6])
        self.assertIn("A: A7", chunks[7])
        self.assertIn("D: D10", chunks[9])


class ExcelImageFilterTest(unittest.TestCase):
    """Tests for filtering embedded image function strings (#1779)."""

    def _xlsx_with_image_functions(self) -> bytes:
        """Create an XLSX where image functions are stored as text values.

        WPS embeds images using =DISPIMG("ID",1) which may appear as plain
        text (not a formula) in some export scenarios.
        """
        wb = openpyxl.Workbook()
        ws = wb.active
        ws["A1"] = "Name"
        ws["B1"] = "Photo"
        ws["A2"] = "Alice"
        ws["B2"] = '_xlfn.DISPIMG("ID_ABCDEF123",1)'
        ws["A3"] = "Bob"
        ws["B3"] = '_xlfn.DISPIMG("ID_GHIJKL456",1)'
        ws["A4"] = "Charlie"
        ws["B4"] = "real data"
        bio = io.BytesIO()
        wb.save(bio)
        return bio.getvalue()

    def test_dispimg_text_values_are_excluded(self):
        """Image function strings stored as text must not appear in output."""
        document = ExcelParser().parse_into_text(self._xlsx_with_image_functions())
        self.assertNotIn("DISPIMG", document.content)
        self.assertNotIn("_xlfn", document.content)
        # Real data must still be present
        self.assertIn("Alice", document.content)
        self.assertIn("Bob", document.content)
        self.assertIn("Charlie", document.content)
        self.assertIn("real data", document.content)

    def test_dispimg_with_equals_prefix(self):
        """=_xlfn.DISPIMG(...) as text (not formula) should also be filtered."""
        wb = openpyxl.Workbook()
        ws = wb.active
        ws["A1"] = "Name"
        ws["B1"] = "Photo"
        ws["A2"] = "Alice"
        # Stored as text with = prefix (some exporters do this)
        ws["B2"] = '=_xlfn.DISPIMG("ID_123",1)'
        bio = io.BytesIO()
        wb.save(bio)
        # Note: openpyxl treats strings starting with = as formulas,
        # so data_only=True in fill_merged_cells_xlsx will turn them to None.
        # This test verifies the formula path also works correctly.
        document = ExcelParser().parse_into_text(bio.getvalue())
        self.assertNotIn("DISPIMG", document.content)
        self.assertIn("Alice", document.content)

    def test_image_function_variations(self):
        """Various image function patterns should all be filtered."""
        wb = openpyxl.Workbook()
        ws = wb.active
        ws["A1"] = '_xlfn.DISPIMG("ID_001",1)'
        ws["A2"] = 'DISPIMG("ID_002",1)'  # no prefix at all
        ws["A3"] = '_xlfn.IMAGE("https://example.com/img.png")'
        ws["A4"] = 'IMAGE("https://example.com/img.png",1)'
        ws["A5"] = "Normal text"
        bio = io.BytesIO()
        wb.save(bio)
        document = ExcelParser().parse_into_text(bio.getvalue())
        self.assertNotIn("DISPIMG", document.content)
        self.assertNotIn("IMAGE(", document.content)
        self.assertIn("Normal text", document.content)

    def test_real_world_wps_dispimg(self):
        """Exact pattern from issue #1779 screenshot: =DISPIMG("ID_...",1)."""
        wb = openpyxl.Workbook()
        ws = wb.active
        ws["A1"] = "Product"
        ws["B1"] = "Image"
        ws["C1"] = "Price"
        ws["A2"] = "Basic"
        # This is the exact format shown in the issue screenshot
        ws["B2"] = 'DISPIMG("ID_5A60F9ED501E48A38EBEE5D326E18235",1)'
        ws["C2"] = "39.9"
        ws["A3"] = "Pro"
        ws["B3"] = 'DISPIMG("ID_AABBCCDD",1)'
        ws["C3"] = "99.9"
        bio = io.BytesIO()
        wb.save(bio)
        document = ExcelParser().parse_into_text(bio.getvalue())
        self.assertNotIn("DISPIMG", document.content)
        self.assertNotIn("ID_5A60F9ED", document.content)
        self.assertIn("Basic", document.content)
        self.assertIn("39.9", document.content)
        self.assertIn("Pro", document.content)
        self.assertIn("99.9", document.content)


class ExcelParserTest(unittest.TestCase):
    @staticmethod
    def _workbook_bytes(rows):
        wb = openpyxl.Workbook()
        ws = wb.active
        for row in rows:
            ws.append(row)
        bio = io.BytesIO()
        wb.save(bio)
        return bio.getvalue()

    def test_numeric_looking_text_retains_its_exact_spelling(self):
        rows = [
            ["00123", "1e5", "+001", "202600012345678901"],
            ["00456", "2e5", "+002", "202600012345678902"],
        ]
        document = ExcelParser(file_name="codes.xlsx").parse_into_text(self._workbook_bytes(rows))
        self.assertEqual(
            [chunk.content.strip() for chunk in document.chunks],
            [
                "A: 00123,B: 1e5,C: +001,D: 202600012345678901",
                "A: 00456,B: 2e5,C: +002,D: 202600012345678902",
            ],
        )
        self.assertEqual(
            [block["locator"]["row_start"] for block in document.source_blocks],
            [1, 2],
        )
        for chunk in document.chunks:
            self.assertEqual(document.content[chunk.start : chunk.end], chunk.content)

    def test_integer_cells_are_not_coerced_by_a_decimal_column(self):
        document = ExcelParser(file_name="quantities.xlsx").parse_into_text(
            self._workbook_bytes([[123456789012345, 1.25], [7, 2.5]])
        )
        self.assertEqual(
            document.content,
            "A: 123456789012345,B: 1.25\nA: 7,B: 2.5\n",
        )

    def test_numeric_looking_headers_remain_distinct(self):
        document = ExcelParser(
            file_name="codes.xlsx", xlsx_first_row_as_header=True
        ).parse_into_text(self._workbook_bytes([["001", "1"], ["002", "2"]]))
        self.assertEqual(document.content, "001: 002,1: 2\n")

    def test_mixed_numeric_and_text_cells_keep_their_types(self):
        from datetime import datetime

        rows = [[7, 1.25, True, datetime(2026, 1, 2), "001"], [8, 2.5, False, None, "002"]]
        document = ExcelParser(file_name="types.xlsx").parse_into_text(self._workbook_bytes(rows))
        self.assertEqual(
            [chunk.content.strip() for chunk in document.chunks],
            [
                "A: 7,B: 1.25,C: True,D: 2026-01-02 00:00:00,E: 001",
                "A: 8,B: 2.5,C: False,E: 002",
            ],
        )

    def test_xlsx_first_row_header_mode_repeats_column_context(self):
        content = self._workbook_bytes(
            [
                ["Name", "Age", "City"],
                ["Alice", 30, "Shenzhen"],
                ["Bob", 28, "Shanghai"],
            ]
        )

        document = ExcelParser(
            file_name="people.xlsx",
            file_type="xlsx",
            xlsx_first_row_as_header="true",
        ).parse_into_text(content)

        chunks = [chunk.content.strip() for chunk in document.chunks]
        self.assertEqual(
            chunks,
            [
                "Name: Alice,Age: 30,City: Shenzhen",
                "Name: Bob,Age: 28,City: Shanghai",
            ],
        )
        self.assertNotIn("A: Name", document.content)

    def test_xlsx_keeps_first_row_as_data_by_default(self):
        content = self._workbook_bytes(
            [
                ["Name", "City"],
                ["Alice", "Shenzhen"],
            ]
        )

        document = ExcelParser(file_name="people.xlsx", file_type="xlsx").parse_into_text(content)

        chunks = [chunk.content.strip() for chunk in document.chunks]
        self.assertEqual(len(chunks), 2)
        self.assertEqual(chunks[0], "A: Name,B: City")
        self.assertEqual(chunks[1], "A: Alice,B: Shenzhen")

    def test_single_row_xlsx_is_not_consumed_in_header_mode(self):
        content = self._workbook_bytes([["Name", "Age", "City"]])

        document = ExcelParser(
            file_name="single.xlsx",
            file_type="xlsx",
            xlsx_first_row_as_header=True,
        ).parse_into_text(content)

        self.assertEqual(
            [chunk.content.strip() for chunk in document.chunks],
            ["A: Name,B: Age,C: City"],
        )

    def test_header_mode_generates_stable_labels_for_duplicate_and_empty_cells(self):
        content = self._workbook_bytes(
            [
                ["Name", "Name", None],
                ["Alice", "Alias", "Shenzhen"],
            ]
        )

        document = ExcelParser(
            file_name="people.xlsx",
            file_type="xlsx",
            xlsx_first_row_as_header="yes",
        ).parse_into_text(content)

        self.assertEqual(
            [chunk.content.strip() for chunk in document.chunks],
            ["Name: Alice,Name_2: Alias,C: Shenzhen"],
        )

    def test_header_mode_avoids_collisions_with_generated_labels(self):
        cases = (
            (
                ["Name", "Name", "Name_2"],
                "Name: left,Name_2: middle,Name_2_2: right",
            ),
            (
                ["Name", "Name_2", "Name"],
                "Name: left,Name_2: middle,Name_3: right",
            ),
            (
                ["B", None, "B_2"],
                "B: left,B_2: middle,B_2_2: right",
            ),
        )
        for headers, expected in cases:
            with self.subTest(headers=headers):
                content = self._workbook_bytes([headers, ["left", "middle", "right"]])

                document = ExcelParser(
                    file_name="headers.xlsx",
                    file_type="xlsx",
                    xlsx_first_row_as_header=True,
                ).parse_into_text(content)

                self.assertEqual([chunk.content.strip() for chunk in document.chunks], [expected])
                workbook = openpyxl.load_workbook(io.BytesIO(content))
                self.assertEqual(next(workbook.active.values), tuple(headers))
                workbook.close()

    def test_xlsx_explicit_false_override_keeps_first_row_as_data(self):
        content = self._workbook_bytes(
            [
                ["Name", "Age"],
                ["Alice", 30],
            ]
        )

        document = ExcelParser(
            file_name="people.xlsx",
            file_type="xlsx",
            xlsx_first_row_as_header="false",
        ).parse_into_text(content)

        chunks = [chunk.content.strip() for chunk in document.chunks]
        self.assertEqual(chunks[0], "A: Name,B: Age")
        self.assertEqual(chunks[1], "A: Alice,B: 30")

    def test_cells_that_say_na_none_or_null_are_kept(self):
        # pandas reads these strings as missing values by default, which
        # dropped the pair although the cell has an answer in it.
        content = self._workbook_bytes(
            [
                ["Patient", "Allergies", "Region", "Result", "Note"],
                ["P1", "None", "NA", "N/A", "ok"],
                ["P2", "Penicillin", "EU", "NULL", None],
                ["P3", "n/a", "NaN", 42, "nan"],
            ]
        )

        document = ExcelParser(
            file_name="patients.xlsx",
            file_type="xlsx",
            xlsx_first_row_as_header=True,
        ).parse_into_text(content)

        self.assertEqual(
            [chunk.content.strip() for chunk in document.chunks],
            [
                "Patient: P1,Allergies: None,Region: NA,Result: N/A,Note: ok",
                "Patient: P2,Allergies: Penicillin,Region: EU,Result: NULL",
                "Patient: P3,Allergies: n/a,Region: NaN,Result: 42,Note: nan",
            ],
        )

    def test_a_header_cell_that_says_na_keeps_its_label(self):
        content = self._workbook_bytes([["Item", "NA", "EU"], ["pens", 3, 4]])

        document = ExcelParser(
            file_name="sales.xlsx",
            file_type="xlsx",
            xlsx_first_row_as_header=True,
        ).parse_into_text(content)

        self.assertEqual(document.chunks[0].content.strip(), "Item: pens,NA: 3,EU: 4")

    def test_blank_cells_and_blank_rows_are_still_skipped(self):
        content = self._workbook_bytes(
            [
                ["Item", "Count"],
                ["pens", None],
                [None, None],
                ["ink", 2],
            ]
        )

        document = ExcelParser(file_name="stock.xlsx", file_type="xlsx").parse_into_text(content)

        self.assertEqual(
            [chunk.content.strip() for chunk in document.chunks],
            ["A: Item,B: Count", "A: pens", "A: ink,B: 2"],
        )
        self.assertEqual(
            [block["locator"]["row_start"] for block in document.source_blocks],
            [1, 2, 4],
        )

    def test_parse_phantom_shared_strings_workbook(self):
        document = ExcelParser().parse_into_text(_xlsx_with_phantom_shared_strings())
        self.assertIn("hello", document.content)
        self.assertIn("42", document.content)
        self.assertGreater(len(document.chunks), 0)

    def test_parse_en_calcchain_shared_strings_case(self):
        path = os.path.join(
            os.path.dirname(__file__),
            "..",
            "..",
            "testdata",
            "rag_test",
            "xlsx",
            "en_calcchain.xlsx",
        )
        if os.path.isfile(path):
            with open(path, "rb") as f:
                payload = f.read()
        else:
            import xlsxwriter

            bio = io.BytesIO()
            wb = xlsxwriter.Workbook(bio, {"in_memory": True})
            ws = wb.add_worksheet()
            ws.write(0, 0, "calc")
            ws.write_formula(0, 1, "=1+2", None, 3)
            wb.close()
            repacked = io.BytesIO()
            with (
                zipfile.ZipFile(io.BytesIO(bio.getvalue()), "r") as zin,
                zipfile.ZipFile(repacked, "w", zipfile.ZIP_DEFLATED) as zout,
            ):
                for item in zin.infolist():
                    target = (
                        "xl/SharedStrings.xml"
                        if item.filename == "xl/sharedStrings.xml"
                        else item.filename
                    )
                    zout.writestr(target, zin.read(item.filename))
            payload = repacked.getvalue()
        document = ExcelParser().parse_into_text(payload)
        self.assertGreater(len(document.content), 0)
        self.assertGreater(len(document.chunks), 0)


DUMMY_PNG_BYTES = (
    b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02"
    b"\x00\x00\x00\x90wS\xde\x00\x00\x00\x0cIDATx\x9cc\xf8\xcf\xc0\x00\x00\x03\x01"
    b"\x01\x00\xc9\xfe\x92\xef\x00\x00\x00\x00IEND\xaeB`\x82"
)


def _build_synthetic_dispimg_xlsx(rows: list, cellimages: list[tuple[str, str, bytes]]) -> bytes:
    """Build in-memory XLSX with WPS cellimages.xml and DISPIMG formulas.

    cellimages: list of (image_id, descr, image_bytes)
    """
    wb = openpyxl.Workbook()
    ws = wb.active
    for r_idx, row in enumerate(rows, start=1):
        for c_idx, val in enumerate(row, start=1):
            ws.cell(row=r_idx, column=c_idx, value=val)
    bio = io.BytesIO()
    wb.save(bio)

    pics_xml = []
    rels_xml = []
    media_map = {}

    for idx, (img_id, descr, img_data) in enumerate(cellimages, start=1):
        r_id = f"rId{idx}"
        target_name = f"media/image_{idx}.png"
        media_map[f"xl/{target_name}"] = img_data
        pics_xml.append(f"""
        <etc:cellImage>
          <xdr:pic>
            <xdr:nvPicPr>
              <xdr:cNvPr id="{idx}" name="{img_id}" descr="{descr}"/>
              <xdr:cNvSpPr/>
            </xdr:nvPicPr>
            <xdr:blipFill>
              <a:blip r:embed="{r_id}"/>
            </xdr:blipFill>
          </xdr:pic>
        </etc:cellImage>
        """)
        rels_xml.append(
            f'<Relationship Id="{r_id}" '
            'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" '
            f'Target="{target_name}"/>'
        )

    cellimages_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <etc:cellImages xmlns:etc="http://www.wps.cn/officeDocument/2017/etCustomData"
                    xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"
                    xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
                    xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
      {"".join(pics_xml)}
    </etc:cellImages>"""

    rels_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      {"".join(rels_xml)}
    </Relationships>"""

    out = io.BytesIO()
    with zipfile.ZipFile(bio, "r") as zin, zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout:
        for item in zin.infolist():
            zout.writestr(item, zin.read(item.filename))
        zout.writestr("xl/cellimages.xml", cellimages_content)
        zout.writestr("xl/_rels/cellimages.xml.rels", rels_content)
        for path, data in media_map.items():
            zout.writestr(path, data)
    return out.getvalue()


def _build_synthetic_drawings_xlsx(rows: list, anchors: list[dict]) -> bytes:
    """Build in-memory XLSX with standard floating drawings (oneCell/twoCell)."""
    wb = openpyxl.Workbook()
    ws = wb.active
    for r_idx, row in enumerate(rows, start=1):
        for c_idx, val in enumerate(row, start=1):
            ws.cell(row=r_idx, column=c_idx, value=val)
    bio = io.BytesIO()
    wb.save(bio)

    anchors_xml = []
    drawing_rels = []
    media_map = {}

    for idx, anc in enumerate(anchors, start=1):
        r_id = f"rIdD{idx}"
        target = f"../media/drawing_img_{idx}.png"
        media_map[f"xl/media/drawing_img_{idx}.png"] = anc.get("data", DUMMY_PNG_BYTES)
        drawing_rels.append(
            f'<Relationship Id="{r_id}" '
            'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" '
            f'Target="{target}"/>'
        )

        c = anc["col"]
        r = anc["row"]
        name = anc.get("name", f"Picture {idx}")
        descr_attr = f'descr="{anc["descr"]}"' if "descr" in anc else ""

        if anc.get("type") == "oneCellAnchor":
            anchors_xml.append(f"""
            <xdr:oneCellAnchor>
              <xdr:from>
                <xdr:col>{c}</xdr:col><xdr:colOff>0</xdr:colOff>
                <xdr:row>{r}</xdr:row><xdr:rowOff>0</xdr:rowOff>
              </xdr:from>
              <xdr:ext cx="50000" cy="50000"/>
              <xdr:pic>
                <xdr:nvPicPr>
                  <xdr:cNvPr id="{idx}" name="{name}" {descr_attr}/>
                  <xdr:cNvSpPr/>
                </xdr:nvPicPr>
                <xdr:blipFill><a:blip r:embed="{r_id}"/></xdr:blipFill>
              </xdr:pic>
              <xdr:clientData/>
            </xdr:oneCellAnchor>""")
        else:
            anchors_xml.append(f"""
            <xdr:twoCellAnchor editAs="oneCell">
              <xdr:from>
                <xdr:col>{c}</xdr:col><xdr:colOff>0</xdr:colOff>
                <xdr:row>{r}</xdr:row><xdr:rowOff>0</xdr:rowOff>
              </xdr:from>
              <xdr:to>
                <xdr:col>{c + 1}</xdr:col><xdr:colOff>0</xdr:colOff>
                <xdr:row>{r + 1}</xdr:row><xdr:rowOff>0</xdr:rowOff>
              </xdr:to>
              <xdr:pic>
                <xdr:nvPicPr>
                  <xdr:cNvPr id="{idx}" name="{name}" {descr_attr}/>
                  <xdr:cNvSpPr/>
                </xdr:nvPicPr>
                <xdr:blipFill><a:blip r:embed="{r_id}"/></xdr:blipFill>
              </xdr:pic>
              <xdr:clientData/>
            </xdr:twoCellAnchor>""")

    ws_rels = (
        '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n'
        '    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">\n'
        '      <Relationship Id="rIdDrawing1" '
        'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing" '
        'Target="../drawings/drawing1.xml"/>\n'
        "    </Relationships>"
    )

    drawing_xml = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"
              xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
              xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
      {"".join(anchors_xml)}
    </xdr:wsDr>"""

    draw_rels_xml = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      {"".join(drawing_rels)}
    </Relationships>"""

    out = io.BytesIO()
    with zipfile.ZipFile(bio, "r") as zin, zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout:
        for item in zin.infolist():
            zout.writestr(item, zin.read(item.filename))
        zout.writestr("xl/worksheets/_rels/sheet1.xml.rels", ws_rels)
        zout.writestr("xl/drawings/drawing1.xml", drawing_xml)
        zout.writestr("xl/drawings/_rels/drawing1.xml.rels", draw_rels_xml)
        for path, data in media_map.items():
            zout.writestr(path, data)
    return out.getvalue()


class ExcelRealWorkloadImageTest(unittest.TestCase):
    """Tier 4 Workload: Validate real-world Excel supplier sheet with embedded images."""

    SAMPLE_PATH = os.environ.get(
        "EXCEL_WORKLOAD_SAMPLE_PATH",
        "local-test-data/supplier_sample_with_images.xlsx",
    )
    _synthetic_cache: bytes | None = None

    @classmethod
    def _build_supplier_workload_xlsx(cls) -> bytes:
        if cls._synthetic_cache is not None:
            return cls._synthetic_cache
        jpeg_prefix = b"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00\xff\xd9"
        rows = [["产品图片", "产品", "组件图片"]]
        for r_idx in range(1280):
            img_a = f"SUP_IMG_{(r_idx % 18) + 1}"
            img_c = f"SUP_IMG_{(r_idx % 18) + 19}"
            prod = "随行储能 300W/256Wh" if r_idx == 0 else f"储能产品-{r_idx}"
            rows.append([f'=_xlfn.DISPIMG("{img_a}",1)', prod, f'=_xlfn.DISPIMG("{img_c}",1)'])

        wb = openpyxl.Workbook()
        ws = wb.active
        for r_num, row_vals in enumerate(rows, start=1):
            for c_num, val in enumerate(row_vals, start=1):
                ws.cell(row=r_num, column=c_num, value=val)
        bio = io.BytesIO()
        wb.save(bio)

        pics_xml: list[str] = []
        rels_xml: list[str] = []
        media_map: dict[str, bytes] = {}
        for idx in range(1, 37):
            r_id = f"rIdSup{idx}"
            target_name = f"media/sup_{idx}.jpeg"
            media_map[f"xl/{target_name}"] = jpeg_prefix + bytes([idx])
            pics_xml.append(
                f"<etc:cellImage><xdr:pic><xdr:nvPicPr>"
                f'<xdr:cNvPr id="{idx}" name="SUP_IMG_{idx}" descr="图片"/>'
                f"<xdr:cNvSpPr/></xdr:nvPicPr>"
                f'<xdr:blipFill><a:blip r:embed="{r_id}"/></xdr:blipFill>'
                f"</xdr:pic></etc:cellImage>"
            )
            rels_xml.append(
                f'<Relationship Id="{r_id}" '
                'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" '
                f'Target="{target_name}"/>'
            )

        cellimages_xml = (
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<etc:cellImages xmlns:etc="http://www.wps.cn/officeDocument/2017/etCustomData" '
            'xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing" '
            'xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" '
            'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
            f"{''.join(pics_xml)}</etc:cellImages>"
        )
        rels_content = (
            '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>'
            '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
            f"{''.join(rels_xml)}</Relationships>"
        )
        out = io.BytesIO()
        with (
            zipfile.ZipFile(bio, "r") as zin,
            zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout,
        ):
            for item in zin.infolist():
                zout.writestr(item, zin.read(item.filename))
            zout.writestr("xl/cellimages.xml", cellimages_xml)
            zout.writestr("xl/_rels/cellimages.xml.rels", rels_content)
            for m_path, m_bytes in media_map.items():
                zout.writestr(m_path, m_bytes)
        cls._synthetic_cache = out.getvalue()
        return cls._synthetic_cache

    def setUp(self):
        if os.path.isfile(self.SAMPLE_PATH):
            with open(self.SAMPLE_PATH, "rb") as f:
                self.content = f.read()
        else:
            self.content = self._build_supplier_workload_xlsx()

    def test_real_xlsx_image_extraction_and_deduplication(self):
        """Extracts 36 unique deduplicated images from 2560 cell references."""
        parser = ExcelParser(file_name="supplier.xlsx", file_type="xlsx", xlsx_ocr_enabled=False)
        doc = parser.parse_into_text(self.content)

        # 1. Total deduplicated image count must match the 36 media files
        self.assertEqual(len(doc.images), 36)

        # 2. Key format must strictly follow images/{uuid}.{ext}
        for ref_path, b64_data in doc.images.items():
            self.assertTrue(
                ref_path.startswith("images/"),
                f"Invalid ref prefix: {ref_path}",
            )
            self.assertTrue(
                ref_path.endswith((".jpeg", ".jpg", ".png")),
                f"Invalid ext: {ref_path}",
            )

            # Base64 data must decode to valid JPEG magic header
            decoded = base64.b64decode(b64_data)
            self.assertGreater(len(decoded), 0)
            self.assertEqual(decoded[:3], b"\xff\xd8\xff", f"Image {ref_path} is not valid JPEG")

    def test_real_xlsx_row_chunks_contain_markdown_refs(self):
        """Row chunks contain markdown image references linked to doc.images."""
        parser = ExcelParser(file_name="supplier.xlsx", file_type="xlsx", xlsx_ocr_enabled=False)
        doc = parser.parse_into_text(self.content)

        self.assertEqual(len(doc.chunks), 1281)

        # Row 1 is header (Chunk 0)
        self.assertIn("A: 产品图片", doc.chunks[0].content)
        self.assertIn("C: 组件图片", doc.chunks[0].content)

        # Row 2 (Chunk 1) must contain image markdown for columns A and C
        chunk1 = doc.chunks[1].content
        self.assertIn("A: ![", chunk1)
        self.assertIn("C: ![", chunk1)
        self.assertIn("B: 随行储能 300W/256Wh", chunk1)

        # Verify all markdown image references in chunks point to keys in doc.images
        img_refs_in_chunks = re.findall(r"\((images/[^\)]+)\)", doc.content)
        self.assertGreater(len(img_refs_in_chunks), 0)
        for ref in img_refs_in_chunks:
            self.assertIn(ref, doc.images, f"Chunk referenced unknown image {ref}")

    def test_real_xlsx_header_mode_image_labels(self):
        """When first row is header, image markdown uses column names."""
        parser = ExcelParser(
            file_name="supplier.xlsx",
            file_type="xlsx",
            xlsx_first_row_as_header=True,
            xlsx_ocr_enabled=False,
        )
        doc = parser.parse_into_text(self.content)

        # 1280 data rows
        self.assertEqual(len(doc.chunks), 1280)
        chunk0 = doc.chunks[0].content
        self.assertIn("产品图片: ![", chunk0)
        self.assertIn("组件图片: ![", chunk0)
        self.assertIn("产品: 随行储能 300W/256Wh", chunk0)


class ExcelWPSDispimgTest(unittest.TestCase):
    """Tests for WPS embedded cell images via DISPIMG formulas (#1779)."""

    def test_wps_dispimg_single_cell_extraction(self):
        rows = [
            ["Item", "Thumbnail", "Price"],
            ["Inverter 5kW", '=_xlfn.DISPIMG("ID_WPS_01",1)', "399.00"],
        ]
        cellimages = [("ID_WPS_01", "5kW Inverter Photo", DUMMY_PNG_BYTES)]
        xlsx_bytes = _build_synthetic_dispimg_xlsx(rows, cellimages)

        doc = ExcelParser(file_name="wps.xlsx", file_type="xlsx").parse_into_text(xlsx_bytes)

        self.assertEqual(len(doc.images), 1)
        self.assertNotIn("DISPIMG", doc.content)
        self.assertNotIn("ID_WPS_01", doc.content)

        # Verify markdown chunk contains image tag
        chunk1 = doc.chunks[1].content
        self.assertIn("A: Inverter 5kW", chunk1)
        self.assertIn("B: ![5kW Inverter Photo](images/", chunk1)
        self.assertIn("C: 399.00", chunk1)

    def test_wps_dispimg_multiple_cells_deduplication(self):
        rows = [
            ["Model", "Photo"],
            ["Mod A", '=_xlfn.DISPIMG("ID_WPS_SAME",1)'],
            ["Mod B", '=_xlfn.DISPIMG("ID_WPS_SAME",1)'],
        ]
        cellimages = [("ID_WPS_SAME", "Standard Battery", DUMMY_PNG_BYTES)]
        xlsx_bytes = _build_synthetic_dispimg_xlsx(rows, cellimages)

        doc = ExcelParser(file_name="wps.xlsx", file_type="xlsx").parse_into_text(xlsx_bytes)

        # Deduplicated to 1 image entry
        self.assertEqual(len(doc.images), 1)
        self.assertEqual(len(doc.chunks), 3)
        self.assertIn("B: ![Standard Battery](images/", doc.chunks[1].content)
        self.assertIn("B: ![Standard Battery](images/", doc.chunks[2].content)

    def test_wps_dispimg_unresolved_id_graceful_skip(self):
        """Unresolved DISPIMG ID must not crash the parser."""
        rows = [
            ["Model", "Photo"],
            ["Mod X", '=_xlfn.DISPIMG("ID_NONEXISTENT",1)'],
        ]
        xlsx_bytes = _build_synthetic_dispimg_xlsx(rows, [])

        doc = ExcelParser(file_name="wps.xlsx", file_type="xlsx").parse_into_text(xlsx_bytes)
        self.assertEqual(len(doc.images), 0)
        self.assertIn("Mod X", doc.content)
        self.assertNotIn("DISPIMG", doc.content)


class ExcelFloatingDrawingsTest(unittest.TestCase):
    """Tests for standard floating drawing anchors (twoCellAnchor & oneCellAnchor)."""

    def test_floating_drawing_two_cell_anchor(self):
        rows = [
            ["Name", "Diagram"],
            ["Transformer", ""],
        ]
        anchors = [
            {
                "type": "twoCellAnchor",
                "col": 1,  # Col B
                "row": 1,  # Row 2 (0-based 1)
                "name": "Trans1",
                "descr": "Wiring Diagram",
                "data": DUMMY_PNG_BYTES,
            }
        ]
        xlsx_bytes = _build_synthetic_drawings_xlsx(rows, anchors)

        doc = ExcelParser(file_name="draw.xlsx", file_type="xlsx").parse_into_text(xlsx_bytes)

        self.assertEqual(len(doc.images), 1)
        chunk1 = doc.chunks[1].content
        self.assertIn("A: Transformer", chunk1)
        self.assertIn("B: ![Wiring Diagram](images/", chunk1)

    def test_floating_drawing_one_cell_anchor(self):
        rows = [
            ["Header Logo", "Title"],
            ["Data Row", "Info"],
        ]
        anchors = [
            {
                "type": "oneCellAnchor",
                "col": 0,  # Col A
                "row": 0,  # Row 1 (0-based 0)
                "name": "Logo",
                "descr": "Company Brand",
                "data": DUMMY_PNG_BYTES,
            }
        ]
        xlsx_bytes = _build_synthetic_drawings_xlsx(rows, anchors)

        doc = ExcelParser(file_name="draw.xlsx", file_type="xlsx").parse_into_text(xlsx_bytes)

        self.assertEqual(len(doc.images), 1)
        chunk0 = doc.chunks[0].content
        self.assertIn("A: ", chunk0)
        self.assertIn("![Company Brand](images/", chunk0)
        self.assertIn("B: Title", chunk0)

    def test_floating_drawing_fallback_alt_text(self):
        """When descr attribute is missing, alt text falls back to name."""
        rows = [["Item", "Pic"], ["Sensor", ""]]
        anchors = [
            {
                "type": "twoCellAnchor",
                "col": 1,
                "row": 1,
                "name": "FallbackName",
                "data": DUMMY_PNG_BYTES,
            }
        ]
        xlsx_bytes = _build_synthetic_drawings_xlsx(rows, anchors)

        doc = ExcelParser().parse_into_text(xlsx_bytes)
        self.assertIn("B: ![FallbackName](images/", doc.chunks[1].content)


class ExcelPureTextNumericalTest(unittest.TestCase):
    """Pure text and numerical workbooks must have zero images and exact baseline chunks."""

    def test_pure_text_and_numerical_xlsx_zero_images(self):
        wb = openpyxl.Workbook()
        ws = wb.active
        ws.title = "Data"
        ws.append(["ID", "Revenue", "Margin", "Active"])
        ws.append([101, 150000.50, 0.23, True])
        ws.append([102, 89000.00, 0.15, False])
        bio = io.BytesIO()
        wb.save(bio)

        doc = ExcelParser(file_name="pure.xlsx", file_type="xlsx").parse_into_text(bio.getvalue())

        self.assertEqual(len(doc.images), 0)
        self.assertNotIn("![", doc.content)
        self.assertNotIn("images/", doc.content)
        self.assertEqual(len(doc.chunks), 3)
        self.assertEqual(
            doc.chunks[0].content.strip(),
            "A: ID,B: Revenue,C: Margin,D: Active",
        )

    def test_legacy_xls_zero_images_compatibility(self):
        xls_paths = [
            os.path.join(
                os.path.dirname(__file__),
                "..",
                "..",
                "internal",
                "infrastructure",
                "docparser",
                "anydoc",
                "testdata",
                "upstream",
                "xls",
                "sheet.xls",
            ),
            os.path.join(
                os.path.dirname(__file__),
                "..",
                "..",
                "internal",
                "agent",
                "tools",
                "testdata",
                "legacy.xls",
            ),
        ]
        found = None
        for p in xls_paths:
            if os.path.isfile(p):
                found = p
                break
        if not found:
            self.skipTest("No legacy .xls fixture found")
        with open(found, "rb") as f:
            data = f.read()
        doc = ExcelParser(file_name="test.xls", file_type="xls").parse_into_text(data)
        self.assertEqual(len(doc.images), 0)
        self.assertNotIn("images/", doc.content)
        self.assertGreater(len(doc.chunks), 0)


class ExcelCorruptResilienceTest(unittest.TestCase):
    """Graceful error handling for corrupted zip and malformed XML."""

    def test_corrupt_zip_bytes_returns_gracefully(self):
        corrupt_data = b"PK\x03\x04random_corrupt_stream_not_a_valid_archive"
        # Must not raise unhandled BadZipFile or KeyError
        doc = ExcelParser(file_name="corrupt.xlsx", file_type="xlsx").parse_into_text(corrupt_data)
        self.assertEqual(len(doc.images), 0)

    def test_truncated_xlsx_bytes_handled(self):
        wb = openpyxl.Workbook()
        ws = wb.active
        ws["A1"] = "Test"
        bio = io.BytesIO()
        wb.save(bio)
        truncated = bio.getvalue()[:150]

        doc = ExcelParser(file_name="trunc.xlsx", file_type="xlsx").parse_into_text(truncated)
        self.assertEqual(len(doc.images), 0)

    def test_corrupted_drawing_xml_resilience(self):
        rows = [["Col1", "Col2"], ["Val1", "Val2"]]
        wb = openpyxl.Workbook()
        ws = wb.active
        for row in rows:
            ws.append(row)
        bio = io.BytesIO()
        wb.save(bio)

        out = io.BytesIO()
        with (
            zipfile.ZipFile(bio, "r") as zin,
            zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout,
        ):
            for item in zin.infolist():
                zout.writestr(item, zin.read(item.filename))
            zout.writestr(
                "xl/worksheets/_rels/sheet1.xml.rels",
                '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                '<Relationship Id="rId1" Type="'
                'http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing" '
                'Target="../drawings/drawing1.xml"/></Relationships>',
            )
            zout.writestr("xl/drawings/drawing1.xml", "<xdr:wsDr><unclosed_malformed_xml")

        doc = ExcelParser().parse_into_text(out.getvalue())
        # Should gracefully ignore corrupted drawing and parse regular table rows
        self.assertEqual(len(doc.images), 0)
        self.assertIn("Val1", doc.content)

    def test_corrupted_cellimages_xml_resilience(self):
        rows = [["Col1", "Col2"], ["Val1", "Val2"]]
        wb = openpyxl.Workbook()
        ws = wb.active
        for row in rows:
            ws.append(row)
        bio = io.BytesIO()
        wb.save(bio)

        out = io.BytesIO()
        with (
            zipfile.ZipFile(bio, "r") as zin,
            zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout,
        ):
            for item in zin.infolist():
                zout.writestr(item, zin.read(item.filename))
            zout.writestr("xl/cellimages.xml", "<etc:cellImages><unclosed_malformed_xml")

        doc = ExcelParser().parse_into_text(out.getvalue())
        self.assertEqual(len(doc.images), 0)
        self.assertIn("Val1", doc.content)


class ExcelOCRGracefulDegradationTest(unittest.TestCase):
    """OCR degradation when RapidOCR succeeds, times out, or throws exceptions."""

    def setUp(self):
        rows = [["Device", "Plate"], ["Inverter", ""]]
        anchors = [
            {
                "type": "twoCellAnchor",
                "col": 1,
                "row": 1,
                "name": "PlateImg",
                "descr": "Nameplate",
                "data": DUMMY_PNG_BYTES,
            }
        ]
        self.xlsx_bytes = _build_synthetic_drawings_xlsx(rows, anchors)

    def test_ocr_success_appends_recognition_text(self):
        """When RapidOCR succeeds, chunk includes OCR recognition text."""
        mock_output = MagicMock()
        mock_output.txts = ("SN123456", "220V 50Hz")

        with patch("rapidocr.RapidOCR") as MockOCR:
            mock_instance = MockOCR.return_value
            mock_instance.return_value = mock_output

            doc = ExcelParser().parse_into_text(self.xlsx_bytes)

            chunk1 = doc.chunks[1].content
            self.assertIn("images/", chunk1)
            # OCR text must be appended to the chunk
            self.assertTrue(
                "SN123456" in chunk1 or "识别结果" in chunk1,
                f"OCR text not found in chunk: {chunk1}",
            )

    def test_ocr_runtime_exception_degrades_to_plain_tag(self):
        """When RapidOCR raises an exception, parser falls back to plain image link."""
        with patch("rapidocr.RapidOCR") as MockOCR:
            mock_instance = MockOCR.return_value
            mock_instance.side_effect = RuntimeError("ONNX Runtime execution failed: GPU OOM")

            doc = ExcelParser().parse_into_text(self.xlsx_bytes)

            # Must not crash; plain image tag must be present
            self.assertEqual(len(doc.images), 1)
            chunk1 = doc.chunks[1].content
            self.assertIn("B: ![Nameplate](images/", chunk1)
            self.assertNotIn("GPU OOM", chunk1)

    def test_ocr_timeout_degrades_to_plain_tag(self):
        """When RapidOCR times out, parser falls back to plain image link."""
        with patch("rapidocr.RapidOCR") as MockOCR:
            mock_instance = MockOCR.return_value
            mock_instance.side_effect = TimeoutError("OCR inference timed out after 5.0s")

            doc = ExcelParser().parse_into_text(self.xlsx_bytes)

            self.assertEqual(len(doc.images), 1)
            chunk1 = doc.chunks[1].content
            self.assertIn("B: ![Nameplate](images/", chunk1)

    def test_ocr_import_error_degrades_gracefully(self):
        """When rapidocr import fails, parser falls back to plain image link."""
        with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=None):
            doc = ExcelParser().parse_into_text(self.xlsx_bytes)
            self.assertEqual(len(doc.images), 1)
            chunk1 = doc.chunks[1].content
            self.assertIn("B: ![Nameplate](images/", chunk1)
            self.assertNotIn("识别结果", chunk1)

    def test_ocr_empty_text_omits_ocr_suffix(self):
        """When RapidOCR detects no text, no empty suffix is appended."""
        mock_output = MagicMock()
        mock_output.txts = ()

        with patch("rapidocr.RapidOCR") as MockOCR:
            mock_instance = MockOCR.return_value
            mock_instance.return_value = mock_output

            doc = ExcelParser().parse_into_text(self.xlsx_bytes)

            chunk1 = doc.chunks[1].content
            self.assertIn("B: ![Nameplate](images/", chunk1)
            self.assertNotIn("(识别结果: )", chunk1)


class ExcelSourceBlocksTest(unittest.TestCase):
    def test_rows_carry_sheet_and_row_number(self):
        import io

        import openpyxl

        from docreader.parser.excel_parser import ExcelParser

        wb = openpyxl.Workbook()
        ws = wb.active
        ws.title = "明细"
        ws["B3"] = "螺栓"
        ws["A5"] = "合计"
        buf = io.BytesIO()
        wb.save(buf)

        doc = ExcelParser(file_name="a.xlsx", file_type="xlsx").parse_into_text(buf.getvalue())
        rows = [(b["locator"]["sheet"], b["locator"]["row_start"]) for b in doc.source_blocks]
        self.assertEqual(rows, [("明细", 3), ("明细", 5)])
        first = doc.source_blocks[0]
        self.assertEqual(doc.content[first["start"] : first["end"]], "B: 螺栓\n")

    def test_find_soffice_uses_shutil_which_and_executable_check(self):
        from docreader.parser import excel_convert

        with (
            patch("docreader.parser.excel_convert.os.path.isfile", return_value=False),
            patch(
                "docreader.parser.excel_convert.shutil.which",
                return_value="/usr/local/bin/soffice",
            ) as mock_which,
        ):
            self.assertEqual(excel_convert.find_soffice(), "/usr/local/bin/soffice")
            mock_which.assert_called_once_with("soffice")


if __name__ == "__main__":
    unittest.main()
