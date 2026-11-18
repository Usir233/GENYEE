"""Regression coverage for archive admission, PDF locking and RPC cancellation."""

import io
import threading
import unittest
import zipfile
from concurrent import futures
from dataclasses import replace
from unittest.mock import patch

import grpc

import docreader.parser.pdf_parser as pdf_parser
from docreader.main import DocReaderServicer
from docreader.parser.archive import ArchiveLimitError
from docreader.parser.chain_parser import FirstParser
from docreader.parser.concurrency import ParserCancelled, cancellable_result
from docreader.parser.excel_convert import normalize_excel_bytes
from docreader.parser.excel_parser import ExcelParser, _prepare_xlsx_bytes
from docreader.parser.parser import Parser
from docreader.parser.xlsx_media import XLSXImageExtractor
from docreader.proto import docreader_pb2_grpc
from docreader.proto.docreader_pb2 import ReadRequest
from docreader.tests.test_docreader_stream import _NoopPreprocessRunner
from docreader.tests.test_pdf_router import _make_image_only_pdf


class Context:
    active = True

    def is_active(self):
        return self.active

    def time_remaining(self):
        return None


class ArchiveAdmissionTest(unittest.TestCase):
    def test_binary_junk_does_not_reach_libreoffice_text_import(self):
        with patch("docreader.parser.excel_convert.convert_excel_to_xlsx_bytes") as convert:
            with self.assertRaisesRegex(ValueError, "binary spreadsheet"):
                normalize_excel_bytes(bytes(range(256)) * 8, "xlsx")
            convert.assert_not_called()

    def test_high_ratio_drawing_is_rejected_before_any_decompression(self):
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
            archive.writestr("xl/drawings/drawing1.xml", b"A" * (2 * 1024 * 1024))
            archive.writestr("[Content_Types].xml", b"<Types/>")
        content = buffer.getvalue()
        paths = [
            lambda: ExcelParser(file_name="drawing.xlsx").parse(content),
            lambda: _prepare_xlsx_bytes(content),
            lambda: XLSXImageExtractor(content).extract_cell_images(),
            lambda: Parser().parse_file("drawing.xlsx", "xlsx", content),
            lambda: Parser().parse_file_stream("drawing.xlsx", "xlsx", content),
        ]
        for index, parse in enumerate(paths):
            with (
                self.subTest(path=index),
                patch.object(
                    zipfile.ZipFile, "read", side_effect=AssertionError("entry decompressed")
                ) as read,
            ):
                with self.assertRaises(ArchiveLimitError):
                    parse()
                read.assert_not_called()

    def test_archive_limit_and_cancellation_do_not_try_next_parser(self):
        for exception in (ArchiveLimitError("too large"), ParserCancelled("cancelled")):
            chain = FirstParser()
            first, fallback = unittest.mock.Mock(), unittest.mock.Mock()
            first.parse_into_text.side_effect = exception
            chain._parsers = [first, fallback]
            with self.subTest(exception=type(exception)), self.assertRaises(type(exception)):
                chain.parse_into_text(b"payload")
            fallback.parse_into_text.assert_not_called()


class PDFSafetyTest(unittest.TestCase):
    def test_real_pdf_renders_are_locked_across_all_routes(self):
        content = _make_image_only_pdf(2)
        original = pdf_parser._render_page_to_jpeg
        renders = []

        def checked_render(*args, **kwargs):
            self.assertTrue(pdf_parser._PDFIUM_LOCK.locked())
            result = original(*args, **kwargs)
            renders.append(result)
            return result

        config = replace(pdf_parser.CONFIG, pdf_render_parallelism=1, pdf_render_max_workers=1)
        with (
            patch.object(pdf_parser, "CONFIG", config),
            patch.object(pdf_parser, "_render_page_to_jpeg", side_effect=checked_render),
        ):
            for force in (False, True):
                parser = pdf_parser.PDFParser(file_name="scan.pdf", pdf_force_scanned=force)
                self.assertEqual(len(parser.parse(content).images), 2)
                stream = parser.parse_stream(content)
                try:
                    self.assertEqual(len(list(stream.images)), 2)
                finally:
                    stream.close()
        self.assertEqual(len(renders), 8)

    def test_concurrent_unary_forced_and_streamed_parses_complete(self):
        content = _make_image_only_pdf(2)
        config = replace(pdf_parser.CONFIG, pdf_render_parallelism=1, pdf_render_max_workers=1)

        def parse(mode):
            parser = pdf_parser.PDFParser(file_name="scan.pdf", pdf_force_scanned=mode == 1)
            if mode != 2:
                return len(parser.parse(content).images)
            stream = parser.parse_stream(content)
            try:
                return len(list(stream.images))
            finally:
                stream.close()

        with (
            patch.object(pdf_parser, "CONFIG", config),
            futures.ThreadPoolExecutor(max_workers=3) as pool,
        ):
            jobs = [pool.submit(parse, mode) for mode in (0, 1, 2) * 3]
            self.assertEqual([job.result(timeout=15) for job in jobs], [2] * 9)

    def test_cancelled_stream_does_not_render_another_page(self):
        context = Context()
        parser = pdf_parser.PDFParser(file_name="scan.pdf", context=context, pdf_force_scanned=True)
        with patch.object(
            pdf_parser, "CONFIG", replace(pdf_parser.CONFIG, pdf_render_parallelism=1)
        ):
            stream = parser.parse_stream(_make_image_only_pdf(3))
            try:
                self.assertTrue(next(stream.images).image_data)
                context.active = False
                with patch.object(pdf_parser, "_render_page_to_jpeg") as render:
                    with self.assertRaises(ParserCancelled):
                        next(stream.images)
                    render.assert_not_called()
            finally:
                stream.close()

    def test_cancelled_router_does_not_fall_back_to_scanning(self):
        for method in ("parse_into_text", "parse_stream"):
            parser = pdf_parser.PDFParser(file_name="scan.pdf")
            with (
                self.subTest(method=method),
                patch.object(parser, "_route_locked", side_effect=ParserCancelled("cancelled")),
                patch.object(pdf_parser.PDFScannedParser, method) as fallback,
            ):
                with self.assertRaises(ParserCancelled):
                    getattr(parser, method)(_make_image_only_pdf())
                fallback.assert_not_called()


class RPCCancellationTest(unittest.TestCase):
    def test_cancel_while_waiting_for_render_future(self):
        context = Context()
        pending: futures.Future[object] = futures.Future()
        timer = threading.Timer(0.02, lambda: setattr(context, "active", False))
        timer.start()
        try:
            with self.assertRaises(ParserCancelled):
                cancellable_result(pending, context)
            self.assertTrue(pending.cancelled())
        finally:
            timer.cancel()
            timer.join()

    def test_live_grpc_cancellation_reaches_real_pdf_parser(self):
        entered = threading.Event()
        release = threading.Event()
        cancelled = threading.Event()
        finished = threading.Event()
        pages_read = []
        original_extract = pdf_parser._extract_page_text
        original_read = DocReaderServicer.Read

        def extract(page):
            text = original_extract(page)
            pages_read.append(text)
            entered.set()
            if not release.wait(10):
                raise AssertionError("test did not release parser")
            return text

        def read(service, request, context):
            context.add_callback(cancelled.set)
            try:
                return original_read(service, request, context)
            finally:
                finished.set()

        service = DocReaderServicer(preprocess_runner=_NoopPreprocessRunner())
        server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
        port = server.add_insecure_port("127.0.0.1:0")
        channel = grpc.insecure_channel(f"127.0.0.1:{port}")
        try:
            with (
                patch.object(pdf_parser, "_extract_page_text", side_effect=extract),
                patch.object(pdf_parser.PDFScannedParser, "parse_into_text") as fallback,
                patch.object(DocReaderServicer, "Read", read),
            ):
                docreader_pb2_grpc.add_DocReaderServicer_to_server(service, server)
                server.start()
                stub = docreader_pb2_grpc.DocReaderStub(channel)
                call = stub.Read.future(
                    ReadRequest(
                        file_name="cancel.pdf",
                        file_type="pdf",
                        file_content=_make_image_only_pdf(3),
                    ),
                    timeout=10,
                )
                self.assertTrue(entered.wait(10))
                self.assertTrue(call.cancel())
                self.assertTrue(cancelled.wait(10))
                release.set()
                self.assertTrue(finished.wait(10))
                self.assertEqual(len(pages_read), 1)
                fallback.assert_not_called()
        finally:
            release.set()
            channel.close()
            server.stop(0).wait(10)
            service.close()


if __name__ == "__main__":
    unittest.main()
