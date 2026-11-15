"""Image references agree with the Go resolver's shared contract fixtures."""

import base64
from email.message import EmailMessage
import io
import json
from pathlib import Path
import unittest
from urllib.parse import quote

from PIL import Image

from docreader.parser import Parser
from docreader.parser.mhtml_parser import MHTMLParser
from docreader.parser.pdf_parser import PDFParser, PDFScannedParser
from docreader.tests.test_docreader_stream import _scanned_pdf
from docreader.tests.test_pdf_image_sources import mixed_image_pdf


CASES = json.loads(
    (Path(__file__).parent / "fixtures/image_filename_refs.json").read_text()
)


class ImageFilenameReferenceTest(unittest.TestCase):
    def test_standalone_images_keep_encoded_keys_and_readable_labels(self):
        data = io.BytesIO()
        Image.new("RGB", (200, 150), "red").save(data, format="PNG")
        payload = data.getvalue()
        for case in CASES:
            with self.subTest(filename=case["filename"]):
                doc = Parser().parse_file(case["filename"], "png", payload)
                self.assertEqual(doc.content, case["image_markdown"])
                self.assertEqual(set(doc.images), {case["image_ref"]})
                self.assertEqual(base64.b64decode(doc.images[case["image_ref"]]), payload)

    def test_pdf_unary_stream_keys_and_source_locations_agree(self):
        content = _scanned_pdf(1)
        for parser_class in (PDFScannedParser, PDFParser):
            for case in CASES:
                with self.subTest(parser=parser_class.__name__, filename=case["pdf_filename"]):
                    parser = parser_class(file_name=case["pdf_filename"], file_type="pdf")
                    doc = parser.parse(content)
                    expected = {case["pdf_page_ref"]}
                    self.assertEqual(doc.content, case["pdf_page_markdown"])
                    self.assertEqual(set(doc.images), expected)
                    with parser.parse_stream(content) as stream:
                        refs = set(stream.document.images)
                        refs.update(image.ref_path for image in stream.images)
                        self.assertEqual(refs, expected)
                        self.assertEqual(stream.document.content, doc.content)
                        self.assertEqual(stream.document.metadata, doc.metadata)
                        self.assertEqual(stream.document.source_blocks, doc.source_blocks)
                    for block in doc.source_blocks:
                        self.assertEqual(doc.content[block["start"]:block["end"]], doc.content)
                    if parser_class is PDFParser:
                        self.assertEqual(set(json.loads(doc.metadata["image_source_types"])), expected)

    def test_native_pdf_figure_metadata_uses_the_same_encoded_refs(self):
        parser = PDFParser(file_name="notes%) [报告].pdf", file_type="pdf")
        content = mixed_image_pdf(include_scan=False)
        doc = parser.parse(content)
        self.assertTrue(doc.images)
        self.assertEqual(set(json.loads(doc.metadata["image_source_types"])), set(doc.images))
        for ref in doc.images:
            self.assertIn(ref, doc.content)
            self.assertNotIn(")", ref)
            self.assertNotIn(" ", ref)
            self.assertIn("%25%29", ref)
        with parser.parse_stream(content) as stream:
            self.assertEqual(stream.document.content, doc.content)
            self.assertEqual(stream.document.metadata, doc.metadata)
            self.assertEqual(set(stream.document.images), set(doc.images))
            self.assertEqual(list(stream.images), [])

    def test_mhtml_image_keys_and_markdown_refs_agree_with_shared_contract(self):
        data = io.BytesIO()
        Image.new("RGB", (64, 64), "blue").save(data, format="PNG")
        payload = data.getvalue()
        for case in CASES:
            filename = case["filename"]
            # Empty filenames and path separators fall back to random UUID refs in MHTML.
            if not filename or "/" in filename or "\\" in filename or "\n" in filename:
                continue
            with self.subTest(filename=filename):
                encoded_segment = quote(filename, safe="")
                location = f"https://example.com/media/{encoded_segment}"
                root = EmailMessage()
                root["Subject"] = "Contract MHTML"
                root.make_related()

                main = EmailMessage()
                main.set_content(
                    f'<html><body><p><img alt="pic" src="{location}"></p></body></html>',
                    subtype="html",
                )
                main["Content-Location"] = "https://example.com/page.html"
                root.attach(main)

                img_part = EmailMessage()
                img_part.set_content(payload, maintype="image", subtype="png")
                img_part["Content-Location"] = location
                root.attach(img_part)

                doc = MHTMLParser(
                    file_name="contract.mhtml", file_type="mhtml"
                ).parse_into_text(root.as_bytes())
                self.assertEqual(set(doc.images), {case["image_ref"]})
                self.assertEqual(doc.content, f"![pic]({case['image_ref']})")
                self.assertEqual(base64.b64decode(doc.images[case["image_ref"]]), payload)


if __name__ == "__main__":
    unittest.main()
