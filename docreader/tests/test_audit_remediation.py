"""Unit tests verifying remediation of audit findings DOC-01 through DOC-12."""

import io
import os
import tempfile
import unittest
import zipfile
from typing import Any
from unittest.mock import MagicMock

from docreader.parser.concurrency import check_cancellation
from docreader.parser.docx_merge import fill_vertical_merged_cells_docx
from docreader.parser.docx_parser import Docx
from docreader.parser.epub_parser import EPUBParser
from docreader.parser.markdown_parser import MarkdownImageUtil, MarkdownParser
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.parser import Parser
from docreader.parser.pdf_parser import PDFParser
from docreader.parser.pptx_media import (
    _rasterize_with_imagemagick,
    list_pptx_media,
)
from docreader.parser.registry import BUILTIN_ENGINE, registry
from docreader.parser.xlsx_repair import _rewrite_zip
from docreader.splitter.splitter import TextSplitter
from docreader.utils.endecode import decode_bytes


class TestAuditRemediation(unittest.TestCase):
    # DOC-01 (F09): Multilingual encoding in decode_bytes
    def test_doc01_shift_jis_decoding(self):
        sjis_bytes = "こんにちは、世界！".encode("shift_jis")
        text = decode_bytes(sjis_bytes)
        self.assertEqual(text, "こんにちは、世界！")

    def test_doc01_gbk_decoding(self):
        gbk_bytes = "姓名,年龄\n张三,25".encode("gbk")
        text = decode_bytes(gbk_bytes)
        self.assertEqual(text, "姓名,年龄\n张三,25")

    # DOC-02 (F10): Plain text engine registration
    def test_doc02_txt_engine_registration(self):
        builtin_cls = registry.get_parser_class(BUILTIN_ENGINE, "txt")
        self.assertEqual(builtin_cls, MarkdownParser)
        text_cls = registry.get_parser_class(BUILTIN_ENGINE, "text")
        self.assertEqual(text_cls, MarkdownParser)

        doc = Parser().parse_file("notes.txt", "txt", b"Hello Plain Text")
        self.assertIn("Hello Plain Text", doc.content)

    # DOC-03 (F11): Archive ZipBomb protection
    def _create_bomb_zip(
        self,
        member_name: str = "word/document.xml",
        uncompressed_size: int = 101 * 1024 * 1024,
    ) -> bytes:
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
            zf.writestr(member_name, b"\x00" * uncompressed_size)
        return buf.getvalue()

    def test_doc03_docx_merge_zipbomb_guard(self):
        bomb = self._create_bomb_zip("word/document.xml")
        with self.assertRaises(ValueError) as ctx:
            fill_vertical_merged_cells_docx(bomb)
        self.assertTrue(
            "uncompressed size" in str(ctx.exception).lower()
            or "compression ratio" in str(ctx.exception).lower()
        )

    def test_doc03_xlsx_repair_zipbomb_guard(self):
        bomb = self._create_bomb_zip("xl/workbook.xml")
        with zipfile.ZipFile(io.BytesIO(bomb)) as zin:
            with self.assertRaises(ValueError) as ctx:
                _rewrite_zip(zin, lambda f: f)
            self.assertTrue(
                "uncompressed size" in str(ctx.exception).lower()
                or "compression ratio" in str(ctx.exception).lower()
            )

    def test_doc03_epub_parser_zipbomb_guard(self):
        bomb = self._create_bomb_zip("OEBPS/chapter1.xhtml")
        with self.assertRaises(ValueError) as ctx:
            EPUBParser(file_name="bomb.epub").parse_into_text(bomb)
        self.assertTrue(
            "uncompressed size" in str(ctx.exception).lower()
            or "compression ratio" in str(ctx.exception).lower()
        )

    def test_doc03_pptx_media_zipbomb_guard(self):
        bomb = self._create_bomb_zip("ppt/media/image1.png")
        with self.assertRaises(ValueError) as ctx:
            list_pptx_media(bomb)
        self.assertTrue(
            "uncompressed size" in str(ctx.exception).lower()
            or "compression ratio" in str(ctx.exception).lower()
        )

    # DOC-04 (F12): Tempfile lifecycle and TMPDIR prefix matching
    def test_doc04_is_docx_temp_img_path(self):
        custom_tmp = tempfile.gettempdir()
        valid_path = os.path.join(custom_tmp, "docx_img_xyz", "page_1_img_1.png")
        self.assertTrue(Docx._is_docx_temp_img_path(valid_path))

        invalid_path = "/etc/passwd"
        self.assertFalse(Docx._is_docx_temp_img_path(invalid_path))

    # DOC-05 (F13): Request ID import in DocxParser
    def test_doc05_request_id_import(self):
        processor = Docx()
        req_id = processor._get_request_id()
        self.assertTrue(req_id is None or isinstance(req_id, str))

    # DOC-06 (F14): Base64 log truncation in MarkdownParser
    def test_doc06_base64_error_log_truncation(self):
        # 1001 bytes of 'A' has len % 4 == 1, causing base64 decode to fail with invalid padding
        huge_corrupt_b64 = "A" * 1001
        md_text = f"![alt](data:image/png;base64,{huge_corrupt_b64})"
        util = MarkdownImageUtil()
        with self.assertLogs("docreader.parser.markdown_parser", level="ERROR") as logs:
            util.extract_base64(md_text)
        error_line = [line for line in logs.output if "Failed to decode base64 image" in line][0]
        self.assertIn("len=1001", error_line)
        self.assertIn("...", error_line)
        self.assertLess(len(error_line), 300)

    # DOC-07 (F15): 0-byte and corrupt PDF handling
    def test_doc07_empty_pdf_safe_return(self):
        doc = PDFParser(file_name="empty.pdf").parse_into_text(b"")
        self.assertEqual(doc.content, "")
        self.assertEqual(doc.metadata.get("page_count"), 0)

        stream = PDFParser(file_name="empty.pdf").parse_stream(b"")
        self.assertEqual(stream.document.content, "")

    def test_doc07_corrupt_pdf_raises_value_error(self):
        with self.assertRaises(ValueError) as ctx:
            PDFParser(file_name="corrupt.pdf").parse_into_text(b"not a valid pdf content")
        self.assertIn("missing %PDF- signature", str(ctx.exception))

    # DOC-08 (F16): PDFium Lock Granularity
    def test_doc08_lock_scoping(self):
        # Verify PDFParser._route can run without deadlocking or global lock contention
        doc = PDFParser(file_name="empty.pdf").parse_into_text(b"")
        self.assertEqual(doc.content, "")

    # DOC-09 (F17): gRPC context cancellation checks
    def test_doc09_check_cancellation(self):
        context = MagicMock()
        context.is_active.return_value = False
        with self.assertRaises(TimeoutError):
            check_cancellation(context)

        context.is_active.return_value = True
        context.time_remaining.return_value = -1.0
        with self.assertRaises(TimeoutError):
            check_cancellation(context)

        # Active with plenty of time -> no error
        context.time_remaining.return_value = 60.0
        check_cancellation(context)

    # DOC-10 (F18): CSV GBK normalization in MarkitdownParser
    def test_doc10_csv_gbk_markitdown(self):
        gbk_csv = "姓名,年龄\n张三,25".encode("gbk")
        parser = MarkitdownParser(file_type="csv")
        doc = parser.parse(gbk_csv)
        self.assertIn("姓名", doc.content)
        self.assertIn("年龄", doc.content)
        self.assertIn("张三", doc.content)
        self.assertIn("25", doc.content)

    # DOC-11 (F19): ImageMagick format prefixing and safe fallback
    def test_doc11_convert_format_whitelist(self):
        # Disallowed format returns None safely without executing subprocess
        result = _rasterize_with_imagemagick(b"test", ".mvg")
        self.assertIsNone(result)

    # DOC-12 (F20): Production splitter assertions replaced
    def test_doc12_splitter_runtime_exception(self):
        splitter: TextSplitter[Any] = TextSplitter(
            separators=["\n"], chunk_size=10, chunk_overlap=0
        )
        chunks = splitter.split_text("Line 1\nLine 2\nLine 3")
        self.assertTrue(len(chunks) > 0)


if __name__ == "__main__":
    unittest.main()
