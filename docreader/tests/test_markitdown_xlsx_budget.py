"""MarkItDown keeps its table format inside an explicit workbook expansion budget."""

import io
import os
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import zipfile

import grpc
import openpyxl
import pandas as pd

from docreader import config
from docreader.main import DocReaderServicer
from docreader.parser import Parser, markitdown_parser
from docreader.parser.archive import ArchiveLimitError
from docreader.parser.concurrency import ParserCancelled
from docreader.parser import xlsx_rows
from docreader.proto import docreader_pb2 as pb, docreader_pb2_grpc as rpc
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner


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
        with budget(8), patch.object(pd, "read_excel", side_effect=AssertionError("dense allocation reached")):
            with self.assertRaisesRegex(ArchiveLimitError, r"9.*8.*builtin"):
                self.parse(workbook_bytes())

    def test_default_budget_protects_extreme_sparse_coordinates(self):
        with budget(200000), patch.object(pd, "read_excel", side_effect=AssertionError("dense allocation reached")):
            with self.assertRaisesRegex(ArchiveLimitError, "builtin"):
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
                "generated.xlsx", "xlsx", workbook_bytes(), parser_engine="markitdown",
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
                "generated.xlsx", "xlsx", workbook_bytes(),
                parser_engine="markitdown", context=context,
            )
        read.assert_not_called()

    def test_untrusted_dimension_does_not_expand_real_bounds(self):
        data = io.BytesIO()
        with zipfile.ZipFile(io.BytesIO(workbook_bytes())) as source, zipfile.ZipFile(data, "w", zipfile.ZIP_DEFLATED) as target:
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
        request = pb.ReadRequest(file_name="generated.xlsx", file_type="xlsx", file_content=workbook_bytes(), config=pb.ReadConfig(parser_engine="markitdown"))
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


class MarkitdownXLSXConfigTest(unittest.TestCase):
    def test_positive_deployment_budget_and_invalid_values(self):
        for value, expected in (("7", 7), ("0", 200000), ("-1", 200000), ("invalid", 200000), ("", 200000)):
            with self.subTest(value=value), patch.dict(os.environ, {KEY: value}, clear=True):
                cfg = config.load_config()
                self.assertEqual(getattr(cfg, FIELD, None), expected)
                with patch.object(config, "CONFIG", cfg):
                    self.assertEqual(config.dump_config().get(KEY), expected)


if __name__ == "__main__":
    unittest.main()
