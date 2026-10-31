"""
Milestone 1 Empirical Challenger Verification Test Suite.

Adversarial stress-testing of ExcelParser image extraction, OCR graceful degradation,
deduplication, base64 decoding integrity, and row markdown references.
"""

import base64
import concurrent.futures
import hashlib
import io
import os
import re
import unittest
from unittest.mock import MagicMock, patch

from PIL import Image

from docreader.parser.excel_parser import ExcelParser, _ocr_local
from docreader.parser.xlsx_media import XLSXImageExtractor

REAL_SAMPLE_PATH = (
    "/root/.cursor/projects/home-cursor/uploads/ee547e04-d882-41d6-ae25-f9088c077369/"
    "储能设备采购供应商明细_含图片.xlsx"
)

# 1x1 8-bit PNG payload for synthetic test workbooks
DUMMY_PNG_BYTES = (
    b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01"
    b"\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff"
    b"?\x00\x05\xfe\x02\xfe\r\xef\x8f\x85\x00\x00\x00\x00IEND\xaeB`\x82"
)


def _reset_ocr_local():
    """Clear thread-local OCR engine to guarantee mock isolation."""
    if hasattr(_ocr_local, "engine"):
        del _ocr_local.engine


class TestExcelOCRGracefulDegradationChallenger(unittest.TestCase):
    """Adversarial stress testing of RapidOCR failure modes and graceful degradation."""

    def setUp(self):
        _reset_ocr_local()
        if not os.path.isfile(REAL_SAMPLE_PATH):
            self.skipTest(f"Real sample not found at {REAL_SAMPLE_PATH}")
        with open(REAL_SAMPLE_PATH, "rb") as f:
            self.sample_bytes = f.read()

    def tearDown(self):
        _reset_ocr_local()

    def test_ocr_simulated_memory_error_graceful_degradation(self):
        """When RapidOCR raises MemoryError (e.g. GPU/system OOM), parser must degrade to plain image tags."""
        mock_engine = MagicMock()
        mock_engine.side_effect = MemoryError("Out of memory during ONNX tensor allocation")

        with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_engine):
            parser = ExcelParser(xlsx_ocr_enabled=True, xlsx_first_row_as_header=False)
            doc = parser.parse_into_text(self.sample_bytes)

            self.assertEqual(len(doc.images), 36)
            self.assertEqual(len(doc.chunks), 1281)
            self.assertNotIn("Out of memory", doc.content)
            self.assertNotIn("识别结果", doc.content)

            # All 2560 tags must be plain image tags
            img_pattern = re.compile(r"!\[(.*?)\]\((images/[a-f0-9]+\.[a-z0-9]+)\)")
            matches = img_pattern.findall(doc.content)
            self.assertEqual(len(matches), 2560)
            for alt, ref in matches:
                self.assertIn(ref, doc.images)

    def test_ocr_simulated_timeout_error_graceful_degradation(self):
        """When RapidOCR raises TimeoutError, parser must not fail and degrade cleanly."""
        mock_engine = MagicMock()
        mock_engine.side_effect = TimeoutError("OCR inference timed out after 5.0s")

        with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_engine):
            parser = ExcelParser(xlsx_ocr_enabled=True, xlsx_first_row_as_header=False)
            doc = parser.parse_into_text(self.sample_bytes)

            self.assertEqual(len(doc.images), 36)
            self.assertEqual(len(doc.chunks), 1281)
            self.assertNotIn("timed out", doc.content)
            self.assertNotIn("识别结果", doc.content)

    def test_ocr_simulated_concurrent_timeout_graceful_degradation(self):
        """When RapidOCR raises concurrent.futures.TimeoutError, parser degrades cleanly."""
        mock_engine = MagicMock()
        mock_engine.side_effect = concurrent.futures.TimeoutError("Executor future timed out")

        with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_engine):
            parser = ExcelParser(xlsx_ocr_enabled=True, xlsx_first_row_as_header=False)
            doc = parser.parse_into_text(self.sample_bytes)

            self.assertEqual(len(doc.images), 36)
            self.assertNotIn("Executor future timed out", doc.content)
            self.assertNotIn("识别结果", doc.content)

    def test_ocr_simulated_runtime_and_os_errors(self):
        """Various runtime exceptions (CUDA crash, DLL missing, OSError) must all degrade gracefully."""
        errors = [
            RuntimeError("ONNX Runtime execution failed: CUDA error"),
            OSError("Model weights file corrupted"),
            ValueError("Unexpected tensor dimensions (1, 0, 400, 400)"),
            Exception("Generic unhandled OCR failure"),
        ]
        for err in errors:
            with self.subTest(error_type=type(err).__name__):
                mock_engine = MagicMock()
                mock_engine.side_effect = err
                with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_engine):
                    parser = ExcelParser(xlsx_ocr_enabled=True, xlsx_first_row_as_header=False)
                    doc = parser.parse_into_text(self.sample_bytes)
                    self.assertEqual(len(doc.images), 36)
                    self.assertEqual(len(doc.chunks), 1281)
                    self.assertNotIn(str(err), doc.content)
                    self.assertNotIn("识别结果", doc.content)

    def test_ocr_engine_initialization_memory_error(self):
        """When RapidOCR() constructor raises MemoryError, _get_ocr_engine returns None safely."""
        with patch("rapidocr.RapidOCR", side_effect=MemoryError("Failed to load onnx models")):
            parser = ExcelParser(xlsx_ocr_enabled=True)
            doc = parser.parse_into_text(self.sample_bytes)
            self.assertEqual(len(doc.images), 36)
            self.assertEqual(len(doc.chunks), 1281)
            self.assertNotIn("识别结果", doc.content)

    def test_ocr_engine_returns_malformed_structures(self):
        """Test engine returning non-standard, malformed, or hostile structures."""
        # Class with exploding properties
        class HostileOutput:
            @property
            def txts(self):
                raise RuntimeError("Exploding property access")

        malformed_cases = [
            None,
            "",
            12345,
            {"arbitrary": "dictionary"},
            [123, 456],
            ([("invalid_tuple",)], "not_a_float"),
            HostileOutput(),
        ]
        for case in malformed_cases:
            with self.subTest(case=type(case).__name__):
                mock_engine = MagicMock()
                mock_engine.return_value = case
                with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_engine):
                    parser = ExcelParser(xlsx_ocr_enabled=True, xlsx_first_row_as_header=False)
                    doc = parser.parse_into_text(self.sample_bytes)
                    self.assertEqual(len(doc.images), 36)
                    self.assertNotIn("识别结果", doc.content)

    def test_ocr_intermittent_failures_and_caching(self):
        """Intermittent failures: some images fail with MemoryError/Timeout, others succeed."""
        call_count = [0]

        def dynamic_ocr(img_bytes):
            call_count[0] += 1
            if call_count[0] % 3 == 1:
                raise MemoryError("Intermittent OOM")
            elif call_count[0] % 3 == 2:
                raise TimeoutError("Intermittent Timeout")
            else:
                out = MagicMock()
                out.txts = (f"TEXT_{call_count[0]}",)
                return out

        mock_engine = MagicMock(side_effect=dynamic_ocr)
        with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_engine):
            parser = ExcelParser(xlsx_ocr_enabled=True, xlsx_first_row_as_header=False)
            doc = parser.parse_into_text(self.sample_bytes)

            self.assertEqual(len(doc.images), 36)
            self.assertEqual(len(doc.chunks), 1281)

            # RapidOCR must be called exactly 36 times (once per unique image), not 2560 times
            self.assertEqual(call_count[0], 36)

            # Succeeded images should have recognition text, failed ones should degrade
            self.assertIn("识别结果", doc.content)
            self.assertIn("TEXT_", doc.content)
            self.assertNotIn("Intermittent OOM", doc.content)
            self.assertNotIn("Intermittent Timeout", doc.content)

    def test_ocr_disabled_flag_skips_ocr_entirely(self):
        """When xlsx_ocr_enabled=False, RapidOCR engine must never be acquired or invoked."""
        mock_engine = MagicMock()
        with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_engine):
            parser = ExcelParser(xlsx_ocr_enabled=False, xlsx_first_row_as_header=False)
            doc = parser.parse_into_text(self.sample_bytes)

            self.assertEqual(len(doc.images), 36)
            mock_engine.assert_not_called()
            self.assertNotIn("识别结果", doc.content)


class TestRealWorldSampleVerificationChallenger(unittest.TestCase):
    """Empirical verification of real-world sample 储能设备采购供应商明细_含图片.xlsx."""

    def setUp(self):
        if not os.path.isfile(REAL_SAMPLE_PATH):
            self.skipTest(f"Real sample not found at {REAL_SAMPLE_PATH}")
        with open(REAL_SAMPLE_PATH, "rb") as f:
            self.sample_bytes = f.read()

    def test_real_sample_deduplication_exact_36_images(self):
        """Verify deduplication down to exactly 36 distinct images in doc.images."""
        extractor = XLSXImageExtractor(self.sample_bytes)
        cell_images, doc_images = extractor.extract_cell_images()

        self.assertEqual(
            len(doc_images),
            36,
            f"Expected exactly 36 images in doc_images, got {len(doc_images)}",
        )

        # Check total mapped cell locations
        total_cell_refs = sum(len(cells) for cells in cell_images.values())
        self.assertEqual(
            total_cell_refs,
            2560,
            f"Expected 2560 mapped cell locations across rows, got {total_cell_refs}",
        )

        # Check deduplication integrity: all 2560 cells point to the 36 images
        for sheet_name, cells in cell_images.items():
            for coord, (ref_path, alt_text) in cells.items():
                self.assertIn(
                    ref_path,
                    doc_images,
                    f"Cell {coord} references {ref_path} which is missing in doc_images",
                )

    def test_real_sample_base64_payloads_valid_image_bytes(self):
        """Verify base64 decoding of all 36 images yields valid JPEG image binaries."""
        extractor = XLSXImageExtractor(self.sample_bytes)
        _, doc_images = extractor.extract_cell_images()

        unique_hashes = set()
        for ref_path, b64_str in doc_images.items():
            # Must be string
            self.assertIsInstance(b64_str, str)
            self.assertGreater(len(b64_str), 0)

            # Must decode without error
            raw_bytes = base64.b64decode(b64_str)
            self.assertGreater(len(raw_bytes), 100)

            # Must start with JPEG magic bytes \xff\xd8\xff
            self.assertTrue(
                raw_bytes.startswith(b"\xff\xd8\xff"),
                f"Image {ref_path} lacks JPEG magic bytes",
            )

            # Verify with PIL Image parser
            bio = io.BytesIO(raw_bytes)
            img = Image.open(bio)
            img.verify()

            # Re-open for geometry check
            bio.seek(0)
            img = Image.open(bio)
            self.assertEqual(img.format, "JPEG")
            self.assertEqual(img.size, (400, 400))
            self.assertEqual(img.mode, "RGB")

            # Check uniqueness of binary hash
            content_hash = hashlib.sha256(raw_bytes).hexdigest()
            self.assertNotIn(
                content_hash,
                unique_hashes,
                f"Duplicate binary content detected for ref {ref_path}",
            )
            unique_hashes.add(content_hash)

        self.assertEqual(
            len(unique_hashes),
            36,
            "Expected exactly 36 unique image content hashes",
        )

    def test_real_sample_row_markdown_references_valid_keys(self):
        """Verify that every markdown image reference in row chunks points to a valid doc.images key."""
        # Use xlsx_ocr_enabled=False to focus on syntax and reference integrity
        parser = ExcelParser(xlsx_ocr_enabled=False, xlsx_first_row_as_header=False)
        doc = parser.parse_into_text(self.sample_bytes)

        self.assertEqual(len(doc.images), 36)
        self.assertEqual(len(doc.chunks), 1281)

        img_md_regex = re.compile(r"!\[(.*?)\]\((images/[a-f0-9]+\.[a-z0-9]+)\)")
        referenced_keys = set()
        total_references = 0

        for idx, chunk in enumerate(doc.chunks):
            matches = img_md_regex.findall(chunk.content)
            for alt_text, ref_path in matches:
                total_references += 1
                referenced_keys.add(ref_path)
                self.assertIn(
                    ref_path,
                    doc.images,
                    f"Chunk {idx} references '{ref_path}' which does not exist in doc.images!",
                )
                self.assertGreater(len(alt_text), 0, f"Chunk {idx} has empty alt text")

        self.assertEqual(
            total_references,
            2560,
            f"Expected 2560 image references across chunks, found {total_references}",
        )
        self.assertEqual(
            len(referenced_keys),
            36,
            f"Expected exactly 36 unique image references across chunks, got {len(referenced_keys)}",
        )
        self.assertEqual(
            referenced_keys,
            set(doc.images.keys()),
            "Set of referenced images in markdown must match doc.images keys exactly",
        )

    def test_real_sample_header_mode_labels_and_row_count(self):
        """Verify header mode produces 1280 chunks with semantic labels on images."""
        parser = ExcelParser(xlsx_ocr_enabled=False, xlsx_first_row_as_header=True)
        doc = parser.parse_into_text(self.sample_bytes)

        self.assertEqual(len(doc.images), 36)
        # 1281 rows total minus 1 header row = 1280 data rows
        self.assertEqual(len(doc.chunks), 1280)

        # Check that header labels '产品图片' and '组件图片' precede image tags
        first_chunk = doc.chunks[0].content
        self.assertIn("产品图片: ![", first_chunk)
        self.assertIn("组件图片: ![", first_chunk)
        self.assertIn("供应商名称: 澄岳电芯", first_chunk)

    def test_real_sample_source_blocks_alignment(self):
        """Verify source_blocks mapping matches the Excel row indices accurately."""
        parser = ExcelParser(xlsx_ocr_enabled=False, xlsx_first_row_as_header=True)
        doc = parser.parse_into_text(self.sample_bytes)

        self.assertEqual(len(doc.source_blocks), 1280)
        for idx, sb in enumerate(doc.source_blocks):
            locator = sb["locator"]
            self.assertEqual(locator["type"], "sheet")
            self.assertEqual(locator["sheet"], "采购供应商明细")
            # In header mode, row 0 of df corresponds to row 2 of spreadsheet
            expected_row = idx + 2
            self.assertEqual(locator["row_start"], expected_row)
            self.assertEqual(locator["row_end"], expected_row)


class TestPureTextAndResilienceCompatibilityChallenger(unittest.TestCase):
    """Verify that pure text files and corrupted archives remain completely uncompromised."""

    def test_pure_text_xlsx_has_zero_images(self):
        import openpyxl

        wb = openpyxl.Workbook()
        ws = wb.active
        ws.append(["Name", "Age", "Role"])
        ws.append(["Alice", "30", "Engineer"])
        ws.append(["Bob", "25", "Designer"])
        bio = io.BytesIO()
        wb.save(bio)

        doc = ExcelParser().parse_into_text(bio.getvalue())
        self.assertEqual(len(doc.images), 0)
        self.assertNotIn("images/", doc.content)
        self.assertEqual(len(doc.chunks), 3)

    def test_corrupted_empty_bytes_graceful_handling(self):
        doc = ExcelParser().parse_into_text(b"")
        self.assertEqual(len(doc.images), 0)
        self.assertEqual(len(doc.chunks), 0)

    def test_corrupted_random_zip_bytes(self):
        doc = ExcelParser().parse_into_text(b"PK\x03\x04random_noise_data")
        self.assertEqual(len(doc.images), 0)
        self.assertEqual(len(doc.chunks), 0)


if __name__ == "__main__":
    unittest.main()
