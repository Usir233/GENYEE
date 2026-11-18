"""DOC fallback behavior without requiring installed document converters."""

import io
import unittest
from pathlib import Path
from unittest.mock import patch

from docreader.models.document import Document
from docreader.parser.archive import ArchiveLimitError
from docreader.parser.concurrency import ParserCancelled
from docreader.parser.doc_parser import DocParser
from docx import Document as WordDocument

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
                parser,
                "_try_convert_doc_to_docx",
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
                    self.assertRaises(type(error)),
                ):
                    parser.parse(LEGACY_DOC.read_bytes())
                fallback.assert_not_called()

    def test_executable_lookup_avoids_mutable_defaults_and_uses_shutil_which(self):
        import inspect
        import os
        import tempfile

        sig = inspect.signature(DocParser._try_find_executable_path)
        for param_name in ("possible_path", "environment_variable"):
            self.assertIsNone(sig.parameters[param_name].default)

        parser = DocParser(file_name="text.doc", file_type="doc")
        with tempfile.TemporaryDirectory() as tmpdir:
            non_exec = Path(tmpdir) / "fake_soffice"
            non_exec.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
            non_exec.chmod(0o644)

            with (
                patch.dict(os.environ, {"TEST_EXEC_ENV": tmpdir}, clear=False),
                patch("docreader.parser.doc_parser.shutil.which", return_value="/bin/soffice") as w,
                patch("docreader.parser.doc_parser.subprocess.run") as subproc_run,
            ):
                found = parser._try_find_executable_path(
                    "soffice",
                    possible_path=[str(non_exec)],
                    environment_variable=["TEST_EXEC_ENV"],
                )
            self.assertEqual(found, "/bin/soffice")
            w.assert_called_once_with("soffice")
            subproc_run.assert_not_called()

            non_exec.chmod(0o755)
            with patch("docreader.parser.doc_parser.shutil.which", return_value=None):
                found_exec = parser._try_find_executable_path(
                    "soffice",
                    possible_path=[str(non_exec)],
                )
            self.assertEqual(found_exec, str(non_exec))

    def test_try_convert_doc_to_docx_checks_cancellation_between_retries(self):
        class _CancelOnRetryCtx:
            def __init__(self) -> None:
                self.active = True

            def is_active(self) -> bool:
                return self.active

        ctx = _CancelOnRetryCtx()
        parser = DocParser(file_name="text.doc", file_type="doc", context=ctx)

        def fail_and_cancel(*_args, **_kwargs):
            ctx.active = False
            return b"", b"lock busy", 1

        with (
            patch.object(parser, "_try_find_soffice", return_value="/usr/bin/soffice"),
            patch("docreader.parser.doc_parser.run_soffice_convert", side_effect=fail_and_cancel),
            self.assertRaises(ParserCancelled),
        ):
            parser._try_convert_doc_to_docx("/tmp/nonexistent.doc")


if __name__ == "__main__":
    unittest.main()
