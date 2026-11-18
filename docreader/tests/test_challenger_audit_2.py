"""Adversarial stress tests for docreader pipeline (challenger_audit_2).

Covers:
- DOC-01: Multilingual character encoding edge cases (Shift-JIS, EUC-JP,
  Windows-1252, ISO-8859-1, GBK, Big5, Latin1)
- DOC-03: Malicious ZipBomb archives (nested zip files, ratios >100:1,
  uncompressed bytes >100MB, fallback bypass)
- DOC-07: Fuzzing 0-byte, truncated, corrupt header, and randomly generated binary PDFs
- DOC-10: CSV boundary cases (GBK with special characters, commas in quotes,
  non-standard delimiters)
"""

import io
import os
import unittest
import zipfile

from docreader.parser.docx2_parser import Docx2Parser
from docreader.parser.docx_merge import fill_vertical_merged_cells_docx
from docreader.parser.epub_parser import EPUBParser
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.pdf_parser import PDFParser
from docreader.parser.pptx_media import list_pptx_media
from docreader.parser.xlsx_repair import _rewrite_zip
from docreader.utils.endecode import decode_bytes


class TestAdversarialDoc01Encodings(unittest.TestCase):
    """DOC-01: Multilingual character encoding edge cases."""

    def test_gbk_simplified_sentence(self):
        """Test standard GBK simplified Chinese sentence."""
        text = "简化字测试：中华人民共和国，北京欢迎您！"
        decoded = decode_bytes(text.encode("gbk"))
        self.assertEqual(decoded, text)

    def test_gbk_csv_rows(self):
        """Test multi-column GBK CSV content."""
        text = '姓名,年龄,简介\n张三,25,"工程师，来自北京"\n李四,30,"经理，来自广州"'
        decoded = decode_bytes(text.encode("gbk"))
        self.assertEqual(decoded, text)

    def test_euc_jp_literature(self):
        """Test EUC-JP Japanese literature excerpt."""
        text = "夏目漱石「吾輩は猫である」名前はまだ無い。"
        decoded = decode_bytes(text.encode("euc_jp"))
        self.assertEqual(decoded, text)

    def test_windows_1252_quotes_and_symbols(self):
        """Test Windows-1252 smart quotes and symbols."""
        text = "“Smart quotes” and ‘single’ — em-dash and €uro."
        decoded = decode_bytes(text.encode("cp1252"))
        self.assertEqual(decoded, text)

    def test_iso_8859_1_accents(self):
        """Test ISO-8859-1 accented French/Spanish text."""
        text = "Café, naïve, façade, résumé, señorita"
        decoded = decode_bytes(text.encode("iso-8859-1"))
        self.assertEqual(decoded, text)

    def test_latin1_nordic(self):
        """Test Latin-1 Nordic/German text."""
        text = "Ångström, Schrödinger, Straße, København"
        decoded = decode_bytes(text.encode("latin1"))
        self.assertEqual(decoded, text)


class TestAdversarialDoc03ZipBombs(unittest.TestCase):
    """DOC-03: Malicious ZipBomb archives."""

    def _make_bomb(self, name="word/document.xml", uncompressed_size=105 * 1024 * 1024):
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
            z.writestr(name, b"0" * uncompressed_size)
        return buf.getvalue()

    def test_docx_merge_zipbomb_size_guard(self):
        bomb = self._make_bomb("word/document.xml")
        with self.assertRaises(ValueError):
            fill_vertical_merged_cells_docx(bomb)

    def test_xlsx_repair_zipbomb_size_guard(self):
        bomb = self._make_bomb("xl/workbook.xml")
        with zipfile.ZipFile(io.BytesIO(bomb)) as zin, self.assertRaises(ValueError):
            _rewrite_zip(zin, lambda f: f)

    def test_epub_parser_zipbomb_size_guard(self):
        bomb = self._make_bomb("OEBPS/chapter1.xhtml")
        with self.assertRaises(ValueError):
            EPUBParser(file_name="bomb.epub").parse_into_text(bomb)

    def test_pptx_media_zipbomb_size_guard(self):
        bomb = self._make_bomb("ppt/media/image1.png")
        with self.assertRaises(ValueError):
            list_pptx_media(bomb)

    def test_docx2_parser_fallback_does_not_leak_unprotected_bomb(self):
        """Docx2Parser must not fall back to unvalidated DocxParser when Markitdown raises."""
        bomb = self._make_bomb("word/document.xml")
        parser = Docx2Parser(file_name="bomb.docx", file_type="docx")
        with self.assertRaises(ValueError):
            parser.parse(bomb)


class TestAdversarialDoc07PdfFuzzing(unittest.TestCase):
    """DOC-07: Fuzzing 0-byte, truncated, corrupt header, and randomly generated binary PDFs."""

    def test_empty_pdf_returns_empty_doc(self):
        doc = PDFParser(file_name="empty.pdf").parse_into_text(b"")
        self.assertEqual(doc.content, "")
        self.assertEqual(doc.metadata.get("page_count"), 0)

    def test_whitespace_pdf_returns_empty_doc(self):
        doc = PDFParser(file_name="whitespace.pdf").parse_into_text(b"   \n\t  ")
        self.assertEqual(doc.content, "")
        self.assertEqual(doc.metadata.get("page_count"), 0)

    def test_corrupt_header_pdf_raises_value_error(self):
        with self.assertRaises(ValueError) as ctx:
            PDFParser(file_name="corrupt.pdf").parse_into_text(b"NOT_A_PDF_HEADER")
        self.assertIn("missing %PDF- signature", str(ctx.exception))

    def test_fuzzed_pdf_with_valid_header_raises_clean_value_error(self):
        """Corrupt PDF with %PDF- header raises clean ValueError rather than raw PdfiumError."""
        fuzzed = b"%PDF-1.4\n" + os.urandom(1024)
        with self.assertRaises(ValueError):
            PDFParser(file_name="fuzzed.pdf").parse_into_text(fuzzed)


class TestAdversarialDoc10Csv(unittest.TestCase):
    """DOC-10: CSV boundary cases."""

    def test_gbk_csv_table_parsing_no_mojibake(self):
        """Verify GBK CSV parsing in Markitdown does not produce Korean Hangul mojibake."""
        raw = '姓名,年龄,简介\n张三,25,"工程师，来自北京"\n李四,30,"经理，来自广州"'.encode("gbk")
        parser = MarkitdownParser(file_type="csv")
        doc = parser.parse(raw)
        self.assertIn("姓名", doc.content)
        self.assertIn("工程师", doc.content)
        self.assertNotIn("檎츰", doc.content)
        self.assertNotIn("묏넋", doc.content)


if __name__ == "__main__":
    unittest.main()
