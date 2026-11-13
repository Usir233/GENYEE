"""DOC fallback behavior without requiring installed document converters."""

import io
from pathlib import Path
import unittest
from unittest.mock import patch

from docx import Document as WordDocument

from docreader.models.document import Document
from docreader.parser.archive import ArchiveLimitError
from docreader.parser.concurrency import ParserCancelled
from docreader.parser.doc_parser import DocParser


LEGACY_DOC = (
    Path(__file__).resolve().parents[2]
    / "internal/infrastructure/docparser/anydoc/testdata/upstream/doc/text.doc"
)


def converted_docx(text=""):
    doc = WordDocument()
    if text:
        doc.add_paragraph(text)
    result = io.BytesIO()
    doc.save(result)
    return result.getvalue()


class DocFallbackTest(unittest.TestCase):
    def test_empty_converted_docx_continues_to_antiword(self):
        original = LEGACY_DOC.read_bytes()
        parser = DocParser(file_name="text.doc", file_type="doc")
        paths = []

        def extract(cmd):
            # Substitute only the converter process boundary. The real fallback
            # must receive the original DOC, not the converted ZIP.
            path = Path(cmd[-1])
            self.assertEqual(path.read_bytes(), original)
            paths.append(path)
            return b"Retained fallback text", b"", 0

        with (
            patch.object(parser, "_try_convert_doc_to_docx", return_value=converted_docx()),
            patch.object(parser, "_try_find_antiword", return_value="/fixture/antiword"),
            patch(
                "docreader.parser.doc_parser.maybe_wrap_with_bwrap",
                side_effect=lambda cmd, **kwargs: cmd,
            ),
            patch.object(parser.sandbox_executor, "execute_in_sandbox", side_effect=extract),
        ):
            document = parser.parse(original)
        self.assertEqual(document.content, "Retained fallback text")
        self.assertEqual(len(paths), 1)
        self.assertFalse(paths[0].exists(), "temporary original DOC must be removed")

    def test_nonempty_docx_keeps_first_choice(self):
        parser = DocParser(file_name="text.doc", file_type="doc")
        with (
            patch.object(
                parser, "_try_convert_doc_to_docx",
                return_value=converted_docx("Converted document text"),
            ),
            patch.object(parser, "_parse_with_antiword") as fallback,
        ):
            document = parser.parse(LEGACY_DOC.read_bytes())
        self.assertIn("Converted document text", document.content)
        fallback.assert_not_called()

    def test_empty_fallback_result_remains_empty(self):
        parser = DocParser(file_name="text.doc", file_type="doc")
        with (
            patch.object(parser, "_parse_with_docx", return_value=Document()),
            patch.object(parser, "_parse_with_antiword", return_value=Document()) as fallback,
        ):
            document = parser.parse(LEGACY_DOC.read_bytes())
        self.assertEqual(document.content, "")
        fallback.assert_called_once()

    def test_limits_and_cancellation_do_not_trigger_fallback(self):
        for error in (
            ArchiveLimitError("archive exceeds limit"),
            ParserCancelled("request canceled"),
        ):
            with self.subTest(error=type(error).__name__):
                parser = DocParser(file_name="text.doc", file_type="doc")
                with (
                    patch.object(parser, "_parse_with_docx", side_effect=error),
                    patch.object(parser, "_parse_with_antiword") as fallback,
                ):
                    with self.assertRaises(type(error)):
                        parser.parse(LEGACY_DOC.read_bytes())
                fallback.assert_not_called()


if __name__ == "__main__":
    unittest.main()
