"""Gate 2 Empirical Adversarial Fuzzing and Stress Suite.

Designed by challenger_gate_2 to empirically stress-test:
1. CJK Character Encoding Fuzzing (GBK, GB2312, GB18030, CP949, EUC-JP, Shift-JIS, CSV).
2. ZipBomb & Archive Stress (Docx2Parser, DocxParser, ExcelParser, MarkitdownParser, EPUBParser).
3. Corrupt / Fuzzed PDF Stream Fuzzing (PDFParser, PDFScannedParser, parse_into_text, parse_stream).
"""

import io
import os
import unittest
import zipfile

from docreader.parser.docx2_parser import Docx2Parser
from docreader.parser.docx_parser import DocxParser
from docreader.parser.epub_parser import EPUBParser
from docreader.parser.excel_parser import ExcelParser
from docreader.parser.markitdown_parser import MarkitdownParser
from docreader.parser.parser import Parser
from docreader.parser.pdf_parser import PDFParser, PDFScannedParser
from docreader.utils.endecode import decode_bytes


def _has_hangul(text: str) -> bool:
    """Check if text contains any Korean Hangul syllables (U+AC00 - U+D7A3)."""
    return any(0xAC00 <= ord(c) <= 0xD7A3 for c in text)


def _has_hiragana(text: str) -> bool:
    """Check if text contains any Japanese Hiragana (U+3040 - U+309F)."""
    return any(0x3040 <= ord(c) <= 0x309F for c in text)


def _has_katakana(text: str) -> bool:
    """Check if text contains any Japanese Katakana (U+30A0 - U+30FF)."""
    return any(0x30A0 <= ord(c) <= 0x30FF for c in text)


class TestCJKEncodingAdversarial(unittest.TestCase):
    """Stress test CJK encoding detection and disambiguation."""

    def test_simplified_chinese_short_phrases(self):
        """Short phrases often fail statistical detection; verify zero Hangul mojibake."""
        short_phrases = [
            "你好",
            "中国",
            "北京",
            "测试",
            "文档解析",
            "人工智能",
            "数据安全",
            "系统架构",
            "知识库检索",
            "机器学习模型",
        ]
        for phrase in short_phrases:
            with self.subTest(phrase=phrase):
                encoded = phrase.encode("gbk")
                decoded = decode_bytes(encoded)
                self.assertFalse(
                    _has_hangul(decoded),
                    f"Phrase '{phrase}' produced Hangul mojibake: '{decoded}'"
                )
                self.assertEqual(decoded, phrase)

    def test_simplified_chinese_diverse_domains(self):
        """Test technical, legal, financial, and colloquial sentences in GBK."""
        sentences = [
            "中华人民共和国行政许可法第十二条规定，下列事项可以设定行政许可。",
            "截止2026年第三季度，公司净利润同比增长15.8%，研发投入超5000万元。",
            "在分布式计算框架中，RPC通信延迟与节点心跳超时时间配置密切相关。",
            "天气预报：明日北京白天晴转多云，北风三四级，最高气温18度。",
            "“区块链”与“零知识证明”等现代密码学技术为隐私保护计算提供了坚实理论依据。",
            "混合字符测试：Model-v3.5.2 在 100,000 个测试样本中准确率达 98.7%！",
        ]
        for s in sentences:
            with self.subTest(sentence=s[:15]):
                for enc in ("gbk", "gb2312", "gb18030"):
                    encoded = s.encode(enc)
                    decoded = decode_bytes(encoded)
                    self.assertFalse(
                        _has_hangul(decoded),
                        f"Sentence produced Hangul mojibake in {enc}: '{decoded}'"
                    )
                    self.assertEqual(decoded, s)

    def test_rare_cjk_characters_gb18030(self):
        """Test uncommon characters present in GB18030."""
        text = "朱镕基、张赟、陶喆、孙堃等名字中的生僻字测试。"
        encoded = text.encode("gb18030")
        decoded = decode_bytes(encoded)
        self.assertFalse(_has_hangul(decoded))
        self.assertEqual(decoded, text)

    def test_genuine_korean_cp949(self):
        """Genuine Korean text in CP949 must decode to Korean, not Chinese or garbled text."""
        korean_samples = [
            "안녕하세요, 반갑습니다. 오늘 날씨가 참 좋네요.",
            "한국어 테스트 문자열입니다. 정확히 디코딩되어야 합니다.",
            "대한민국 서울특별시 종로구 세종대로",
            "자연어 처리 및 인공지능 기반 문서 파싱 엔진",
            "환영합니다.",
            "감사합니다.",
            "좋은 아침입니다.",
            "식사하셨습니까?",
            "주말 잘 보내세요.",
            "대한민국 만세",
            "한국어",
            "서울",
            "테스트",
            "가나다라",
            "밥 먹었어? 오늘 뭐 해?",
            "대한민국 국회는 오늘 본회의를 열고 내년도 예산안을 심의 의결할 예정이다.",
        ]
        for k in korean_samples:
            with self.subTest(korean=k[:10]):
                encoded = k.encode("cp949")
                decoded = decode_bytes(encoded)
                self.assertTrue(
                    _has_hangul(decoded),
                    f"Korean text '{k}' failed to decode as Hangul: '{decoded}'"
                )
                self.assertEqual(decoded, k)

    def test_japanese_shift_jis_and_euc_jp(self):
        """Japanese text with Kanji, Hiragana, and Katakana must decode cleanly."""
        japanese_samples = [
            "吾輩は猫である。名前はまだ無い。どこで生れたかとんと見当がつかぬ。",
            "こんにちは世界！プログラミング言語Pythonのテストです。",
            "東京都千代田区霞が関一丁目",
            "自然言語処理と機械学習アルゴリズムの実装検証",
            "日本の伝統芸能である歌舞伎や能楽はユネスコ無形文化遺産に登録されています。",
        ]
        for j in japanese_samples:
            for enc in ("shift_jis", "euc_jp"):
                with self.subTest(jp=j[:10], enc=enc):
                    encoded = j.encode(enc)
                    decoded = decode_bytes(encoded)
                    self.assertFalse(_has_hangul(decoded))
                    self.assertEqual(decoded, j)

    def test_csv_tables_gbk_multi_format(self):
        """Verify GBK CSV parsing in Markitdown and Parser facade."""
        csv_text = (
            "员工编号,姓名,部门,职级,月薪,备注\n"
            "EMP001,张伟,研发中心-架构组,T4,35000,\"核心架构师，负责分布式存储\"\n"
            "EMP002,李娜,产品设计中心,P3,22000,\"高级产品经理，负责用户端\"\n"
            "EMP003,王磊,财务部,M2,28000,\"财务合规与审计负责人\"\n"
            "EMP004,赵强,运维支持,O2,16000,\"基础网络与K8s集群维护\"\n"
        )
        csv_bytes = csv_text.encode("gbk")

        # 1. Test via MarkitdownParser directly
        markit_parser = MarkitdownParser(file_type="csv")
        doc_markit = markit_parser.parse(csv_bytes)
        self.assertIn("员工编号", doc_markit.content)
        self.assertIn("分布式存储", doc_markit.content)
        self.assertIn("核心架构师", doc_markit.content)
        self.assertFalse(_has_hangul(doc_markit.content))

        # 2. Test via Parser facade
        facade_parser = Parser()
        doc_facade = facade_parser.parse_file("staff_records.csv", "csv", csv_bytes)
        self.assertIn("员工编号", doc_facade.content)
        self.assertIn("财务合规与审计", doc_facade.content)
        self.assertFalse(_has_hangul(doc_facade.content))


class TestZipBombAdversarial(unittest.TestCase):
    """Stress test ZipBomb protections across all supported archive parsers."""

    def _create_single_file_bomb(self, entry_name: str, uncompressed_size: int = 105 * 1024 * 1024) -> bytes:
        """Create a zip bomb where a single entry exceeds 100MB."""
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
            z.writestr(entry_name, b"\x00" * uncompressed_size)
        return buf.getvalue()

    def _create_multi_file_bomb(self, prefix: str, file_count: int = 150, size_per_file: int = 800 * 1024) -> bytes:
        """Create a zip bomb where cumulative uncompressed size exceeds 100MB (150 * 800KB = 120MB)."""
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
            for i in range(file_count):
                z.writestr(f"{prefix}/part_{i}.xml", b"A" * size_per_file)
        return buf.getvalue()

    def _create_high_ratio_bomb(self, entry_name: str, uncompressed_size: int = 15 * 1024 * 1024) -> bytes:
        """Create a bomb with compression ratio > 100:1 (15MB of zeros compresses to ~15KB)."""
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
            z.writestr(entry_name, b"0" * uncompressed_size)
        return buf.getvalue()

    def test_docx2_parser_single_entry_bomb(self):
        bomb = self._create_single_file_bomb("word/document.xml")
        parser = Docx2Parser(file_name="bomb.docx", file_type="docx")
        with self.assertRaises(ValueError) as ctx:
            parser.parse(bomb)
        self.assertTrue(any(s in str(ctx.exception).lower() for s in ("exceeds limit", "limit", "zip")))

    def test_docx2_parser_cumulative_bomb(self):
        bomb = self._create_multi_file_bomb("word")
        parser = Docx2Parser(file_name="bomb_cumulative.docx", file_type="docx")
        with self.assertRaises(ValueError) as ctx:
            parser.parse(bomb)
        self.assertTrue(any(s in str(ctx.exception).lower() for s in ("cumulative", "exceeds limit", "limit")))

    def test_docx_parser_direct_bomb(self):
        """DocxParser direct invocation must raise ValueError and not bypass."""
        bomb = self._create_single_file_bomb("word/document.xml")
        parser = DocxParser(file_name="bomb_direct.docx", file_type="docx")
        with self.assertRaises(ValueError):
            parser.parse(bomb)

    def test_excel_parser_bomb(self):
        """ExcelParser must reject workbook exceeding 100MB uncompressed."""
        bomb = self._create_single_file_bomb("xl/worksheets/sheet1.xml")
        parser = ExcelParser(file_name="bomb.xlsx", file_type="xlsx")
        with self.assertRaises(ValueError):
            parser.parse(bomb)

    def test_markitdown_xlsx_bomb(self):
        """MarkitdownParser must reject XLSX zip bomb."""
        bomb = self._create_single_file_bomb("xl/worksheets/sheet1.xml")
        parser = MarkitdownParser(file_name="bomb.xlsx", file_type="xlsx")
        with self.assertRaises(ValueError):
            parser.parse(bomb)

    def test_epub_parser_single_and_cumulative_bomb(self):
        """EPUBParser must reject both single entry and cumulative zip bombs."""
        single_bomb = self._create_single_file_bomb("OEBPS/chapter1.xhtml")
        parser = EPUBParser(file_name="bomb_single.epub", file_type="epub")
        with self.assertRaises(ValueError):
            parser.parse_into_text(single_bomb)

        cumul_bomb = self._create_multi_file_bomb("OEBPS", file_count=150, size_per_file=800 * 1024)
        with self.assertRaises(ValueError):
            parser.parse_into_text(cumul_bomb)


class TestCorruptPdfAdversarial(unittest.TestCase):
    """Stress test PDF parsers with fuzzed, truncated, and corrupt PDF byte sequences."""

    def test_fuzzed_pdf_various_sizes(self):
        """Test random byte fuzzing with %PDF- header across multiple size brackets."""
        sizes = [16, 64, 256, 1024, 4096, 16384, 65536]
        for sz in sizes:
            with self.subTest(size=sz):
                fuzzed_data = b"%PDF-1.4\n" + os.urandom(sz)
                # 1. PDFParser.parse_into_text
                with self.assertRaises(ValueError) as ctx1:
                    PDFParser(file_name="fuzz.pdf").parse_into_text(fuzzed_data)
                self.assertNotIn("PdfiumError", type(ctx1.exception).__name__)

                # 2. PDFParser.parse_stream
                with self.assertRaises(ValueError) as ctx2:
                    PDFParser(file_name="fuzz.pdf").parse_stream(fuzzed_data)
                self.assertNotIn("PdfiumError", type(ctx2.exception).__name__)

                # 3. PDFScannedParser.parse_into_text
                with self.assertRaises(ValueError) as ctx3:
                    PDFScannedParser(file_name="fuzz_scanned.pdf").parse_into_text(fuzzed_data)
                self.assertNotIn("PdfiumError", type(ctx3.exception).__name__)

                # 4. PDFScannedParser.parse_stream
                with self.assertRaises(ValueError) as ctx4:
                    PDFScannedParser(file_name="fuzz_scanned.pdf").parse_stream(fuzzed_data)
                self.assertNotIn("PdfiumError", type(ctx4.exception).__name__)

    def test_truncated_pdf_structures(self):
        """Test truncated minimal PDF document at various cut points."""
        # Minimal valid PDF structure
        minimal_pdf = (
            b"%PDF-1.4\n"
            b"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"
            b"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n"
            b"3 0 obj<</Type/Page/MediaBox[0 0 3 3]/Parent 2 0 R>>endobj\n"
            b"xref\n0 4\n0000000000 65535 f \n"
            b"0000000009 00000 n \n0000000052 00000 n \n0000000101 00000 n \n"
            b"trailer<</Size 4/Root 1 0 R>>\nstartxref\n162\n%%EOF"
        )
        cut_points = [10, 30, 60, 100, 150]
        for cp in cut_points:
            with self.subTest(cut_point=cp):
                truncated = minimal_pdf[:cp]
                with self.assertRaises(ValueError) as ctx:
                    PDFParser(file_name="truncated.pdf").parse_into_text(truncated)
                self.assertNotIn("PdfiumError", type(ctx.exception).__name__)

    def test_corrupted_xref_and_trailer(self):
        """Test corrupted xref table and corrupted trailer offsets."""
        corrupted_xref = (
            b"%PDF-1.4\n"
            b"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"
            b"trailer<</Size 2/Root 1 0 R>>\nstartxref\n999999\n%%EOF"
        )
        with self.assertRaises(ValueError) as ctx:
            PDFParser(file_name="bad_xref.pdf").parse_into_text(corrupted_xref)
        self.assertNotIn("PdfiumError", type(ctx.exception).__name__)


if __name__ == "__main__":
    unittest.main()
