"""MarkItDown keeps its table format inside an explicit workbook expansion budget."""

import io
import os
import unittest
import zipfile
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from types import SimpleNamespace
from unittest.mock import patch

import grpc
import openpyxl
import pandas as pd
import pptx
from docreader import config
from docreader.main import DocReaderServicer
from docreader.parser import Parser, markitdown_parser, xlsx_rows
from docreader.parser.archive import ArchiveLimitError, detect_ooxml_package_type
from docreader.parser.concurrency import ParserCancelled
from docreader.parser.markitdown_parser import MarkitdownParser, StdMarkitdownParser
from docreader.proto import docreader_pb2 as pb
from docreader.proto import docreader_pb2_grpc as rpc
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner
from docx import Document as WordDocument

KEY = "DOCREADER_MARKITDOWN_XLSX_MAX_TABLE_CELLS"
FIELD = "markitdown_xlsx_max_table_cells"


def workbook_bytes(sheets=1, row=3, col=3):
    book = openpyxl.Workbook()
    for i in range(sheets):
        sheet = book.active if i == 0 else book.create_sheet()
        sheet["A1"] = "First"
        sheet.cell(row, col, "Last")
    data = io.BytesIO()
    book.save(data)
    return data.getvalue()


@contextmanager
def budget(limit):
    cfg = SimpleNamespace(**{**vars(markitdown_parser.CONFIG), FIELD: limit})
    with patch.object(markitdown_parser, "CONFIG", cfg):
        yield


class MarkitdownXLSXBudgetTest(unittest.TestCase):
    def parse(self, content):
        return Parser().parse_file("generated.xlsx", "xlsx", content, parser_engine="markitdown")

    def test_over_limit_rejected_before_dense_read(self):
        with (
            budget(8),
            patch.object(pd, "read_excel", side_effect=AssertionError("dense allocation reached")),
            self.assertRaisesRegex(ArchiveLimitError, r"9.*8.*builtin"),
        ):
            self.parse(workbook_bytes())

    def test_default_budget_protects_extreme_sparse_coordinates(self):
        with (
            budget(200000),
            patch.object(pd, "read_excel", side_effect=AssertionError("dense allocation reached")),
            self.assertRaisesRegex(ArchiveLimitError, "builtin"),
        ):
            self.parse(workbook_bytes(row=1048576, col=16384))

    def test_exact_budget_keeps_original_markitdown_table(self):
        content = workbook_bytes()
        with budget(100):
            baseline = self.parse(content)
        with budget(9), patch.object(pd, "read_excel", wraps=pd.read_excel) as read:
            document = self.parse(content)
        read.assert_called_once()
        self.assertEqual(document.content, baseline.content)
        self.assertIn("## Sheet", document.content)
        self.assertIn("|", document.content)
        self.assertIn("Last", document.content)

    def test_budget_is_cumulative_across_worksheets(self):
        with budget(17), self.assertRaisesRegex(ArchiveLimitError, r"18.*17.*builtin"):
            self.parse(workbook_bytes(sheets=2))

    def test_stored_blank_and_uncached_formula_rows_count_toward_work(self):
        for formula in (False, True):
            with self.subTest(formula=formula):
                book = openpyxl.Workbook()
                sheet = book.active
                sheet["A1"] = "First"
                if formula:
                    sheet["D4"] = "=1+1"
                else:
                    sheet["D4"].number_format = "0%"
                data = io.BytesIO()
                book.save(data)
                with budget(9), self.assertRaisesRegex(ArchiveLimitError, r"16.*9.*builtin"):
                    self.parse(data.getvalue())

    def test_empty_sheet_does_not_consume_expansion_budget(self):
        book = openpyxl.Workbook()
        data = io.BytesIO()
        book.save(data)
        with budget(1):
            self.assertIn("## Sheet", self.parse(data.getvalue()).content)

    def test_request_override_cannot_raise_deployment_limit(self):
        with budget(8), self.assertRaises(ArchiveLimitError):
            Parser().parse_file(
                "generated.xlsx",
                "xlsx",
                workbook_bytes(),
                parser_engine="markitdown",
                engine_overrides={FIELD: "999999999", KEY: "999999999"},
            )

    def test_cancelled_budget_scan_never_enters_conversion(self):
        class Context:
            active = True

            def is_active(self):
                return self.active

        context = Context()
        original = xlsx_rows._decoded_rows

        def cancel_after_first_row(sheet, current_context):
            for row in original(sheet, current_context):
                yield row
                context.active = False

        with (
            budget(9),
            patch.object(xlsx_rows, "_decoded_rows", side_effect=cancel_after_first_row),
            patch.object(pd, "read_excel") as read,
            self.assertRaises(ParserCancelled),
        ):
            Parser().parse_file(
                "generated.xlsx",
                "xlsx",
                workbook_bytes(),
                parser_engine="markitdown",
                context=context,
            )
        read.assert_not_called()

    def test_untrusted_dimension_does_not_expand_real_bounds(self):
        data = io.BytesIO()
        with (
            zipfile.ZipFile(io.BytesIO(workbook_bytes())) as source,
            zipfile.ZipFile(data, "w", zipfile.ZIP_DEFLATED) as target,
        ):
            for item in source.infolist():
                content = source.read(item.filename)
                if item.filename == "xl/worksheets/sheet1.xml":
                    content = content.replace(b'ref="A1:C3"', b'ref="A1:XFD1048576"')
                target.writestr(item, content)
        with budget(9):
            self.assertIn("Last", self.parse(data.getvalue()).content)

    def test_both_rpc_modes_return_budget_error_without_partial_content(self):
        service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        pool = ThreadPoolExecutor(max_workers=2)
        server = grpc.server(pool)
        rpc.add_DocReaderServicer_to_server(service, server)
        port = server.add_insecure_port("127.0.0.1:0")
        server.start()
        request = pb.ReadRequest(
            file_name="generated.xlsx",
            file_type="xlsx",
            file_content=workbook_bytes(),
            config=pb.ReadConfig(parser_engine="markitdown"),
        )
        try:
            with budget(8), grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
                client = rpc.DocReaderStub(channel)
                unary = client.Read(request, timeout=5)
                frames = list(client.ReadStream(request, timeout=5))
            self.assertEqual(len(frames), 1)
            for result in (unary, frames[0].meta):
                self.assertIn("builtin", result.error)
                self.assertIn("limit", result.error)
                self.assertFalse(result.markdown_content)
        finally:
            server.stop(0).wait()
            pool.shutdown()
            service.close()

    def test_renamed_docx_and_pptx_sparse_xlsx_bombs_blocked_without_dense_read(self):
        sparse_bomb = workbook_bytes(row=1048576, col=16384)
        for ext in ("docx", "pptx", "pdf", "csv"):
            for engine in ("", "builtin", "markitdown"):
                if ext == "pdf" and engine in ("", "builtin"):
                    continue
                with (
                    self.subTest(ext=ext, engine=engine),
                    budget(200000),
                    patch.object(
                        pd,
                        "read_excel",
                        side_effect=AssertionError("dense allocation reached"),
                    ),
                    self.assertRaisesRegex(ArchiveLimitError, "builtin"),
                ):
                    Parser().parse_file(f"evil.{ext}", ext, sparse_bomb, parser_engine=engine)

    def test_mismatched_small_xlsx_disguised_as_docx_or_pptx_never_cross_sniffs(self):
        small_xlsx = workbook_bytes(row=2, col=2)
        with patch.object(
            pd, "read_excel", side_effect=AssertionError("cross-sniffed into read_excel")
        ):
            for ext in ("docx", "pptx"):
                with self.subTest(ext=ext), self.assertRaises(ValueError):
                    StdMarkitdownParser(
                        file_name=f"disguised.{ext}", file_type=ext
                    ).parse_into_text(small_xlsx)

    def test_nested_zip_archives_never_expand_inner_xlsx_or_bomb(self):
        sparse_bomb = workbook_bytes(row=1048576, col=16384)
        nested_buf = io.BytesIO()
        with zipfile.ZipFile(nested_buf, "w", zipfile.ZIP_DEFLATED) as outer:
            outer.writestr("nested/evil.xlsx", sparse_bomb)
            outer.writestr("readme.txt", "benign outer note")
        nested_zip = nested_buf.getvalue()

        parser = StdMarkitdownParser(file_type="pptx")
        converter_names = {type(reg.converter).__name__ for reg in parser.markitdown._converters}
        self.assertNotIn("ZipConverter", converter_names)
        self.assertNotIn("EpubConverter", converter_names)

        with patch.object(
            pd, "read_excel", side_effect=AssertionError("nested zip reached read_excel")
        ):
            for ext in ("docx", "pptx", "xlsx"):
                for engine in ("builtin", "markitdown"):
                    with self.subTest(ext=ext, engine=engine):
                        try:
                            result = Parser().parse_file(
                                f"nested.{ext}", ext, nested_zip, parser_engine=engine
                            )
                        except (RuntimeError, ValueError, KeyError):
                            result = None
                        if result is not None:
                            self.assertEqual(result.content, "")
                            self.assertNotIn("benign outer note", result.content)

        ratio_bomb_buf = io.BytesIO()
        with zipfile.ZipFile(ratio_bomb_buf, "w", zipfile.ZIP_DEFLATED) as outer:
            outer.writestr("huge.xml", b"0" * (2 * 1024 * 1024))
        with patch(
            "docreader.parser.markitdown_parser.verify_zip_stream_limits",
            wraps=markitdown_parser.verify_zip_stream_limits,
        ) as verify_mock:
            with self.assertRaises(ArchiveLimitError):
                MarkitdownParser(file_name="bomb.pptx", file_type="pptx").parse(
                    ratio_bomb_buf.getvalue()
                )
            verify_mock.assert_called()

    def test_legitimate_docx_pptx_and_xlsx_still_parse_cleanly(self):
        docx_buf = io.BytesIO()
        wdoc = WordDocument()
        wdoc.add_paragraph("Legitimate Word Content")
        wdoc.save(docx_buf)

        pptx_buf = io.BytesIO()
        pres = pptx.Presentation()
        slide = pres.slides.add_slide(pres.slide_layouts[0])
        slide.shapes.title.text = "Legitimate Slide Title"
        pres.save(pptx_buf)

        xlsx_bytes = workbook_bytes(row=2, col=2)

        with zipfile.ZipFile(io.BytesIO(docx_buf.getvalue())) as zf:
            self.assertEqual(detect_ooxml_package_type(zf), "docx")
        with zipfile.ZipFile(io.BytesIO(pptx_buf.getvalue())) as zf:
            self.assertEqual(detect_ooxml_package_type(zf), "pptx")
        with zipfile.ZipFile(io.BytesIO(xlsx_bytes)) as zf:
            self.assertEqual(detect_ooxml_package_type(zf), "xlsx")

        for engine in ("builtin", "markitdown"):
            with self.subTest(engine=engine):
                parsed_docx = Parser().parse_file(
                    "valid.docx", "docx", docx_buf.getvalue(), parser_engine=engine
                )
                self.assertIn("Legitimate Word Content", parsed_docx.content)

                parsed_pptx = Parser().parse_file(
                    "valid.pptx", "pptx", pptx_buf.getvalue(), parser_engine=engine
                )
                self.assertIn("Legitimate Slide Title", parsed_pptx.content)

                parsed_xlsx = Parser().parse_file(
                    "valid.xlsx", "xlsx", xlsx_bytes, parser_engine=engine
                )
                self.assertIn("Last", parsed_xlsx.content)


class MarkitdownXLSXConfigTest(unittest.TestCase):
    def test_positive_deployment_budget_and_invalid_values(self):
        for value, expected in (
            ("7", 7),
            ("0", 200000),
            ("-1", 200000),
            ("invalid", 200000),
            ("", 200000),
        ):
            with self.subTest(value=value), patch.dict(os.environ, {KEY: value}, clear=True):
                cfg = config.load_config()
                self.assertEqual(getattr(cfg, FIELD, None), expected)
                with patch.object(config, "CONFIG", cfg):
                    self.assertEqual(config.dump_config().get(KEY), expected)


if __name__ == "__main__":
    unittest.main()
