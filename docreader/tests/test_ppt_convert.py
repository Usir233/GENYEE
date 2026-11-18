import io
import os
import shutil
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch

from docreader.parser.ppt_convert import (
    convert_ppt_to_pptx_bytes,
    is_ole_compound,
    is_zip_openxml,
    needs_ppt_to_pptx_conversion,
    normalize_ppt_bytes,
)
from lxml import etree
from PIL import Image
from pptx import Presentation
from pptx.util import Inches

REPO_ROOT = Path(__file__).resolve().parents[2]
TESTDATA = REPO_ROOT / "testdata" / "rag_test"
UPSTREAM_TESTDATA = (
    REPO_ROOT / "internal" / "infrastructure" / "docparser" / "anydoc" / "testdata" / "upstream"
)
LEGACY_PPT = (
    TESTDATA / "ppt_old" / "en_38256.ppt"
    if (TESTDATA / "ppt_old" / "en_38256.ppt").is_file()
    else UPSTREAM_TESTDATA / "ppt" / "pres.ppt"
)
WMF_IMAGE_PPT = LEGACY_PPT
IMAGE_HEAVY_PPT = (
    TESTDATA / "ppt_old" / "en_41384.ppt"
    if (TESTDATA / "ppt_old" / "en_41384.ppt").is_file()
    else LEGACY_PPT
)
PPTX_SAMPLE = (
    TESTDATA / "pptx" / "en_marker.pptx"
    if (TESTDATA / "pptx" / "en_marker.pptx").is_file()
    else UPSTREAM_TESTDATA / "pptx" / "pres.pptx"
)


def _png_bytes(color: tuple[int, int, int]) -> bytes:
    out = io.BytesIO()
    Image.new("RGB", (16, 16), color).save(out, format="PNG")
    return out.getvalue()


def _pptx_with_wmf_and_text() -> bytes:
    pres = Presentation()
    slide = pres.slides.add_slide(pres.slide_layouts[6])
    textbox = slide.shapes.add_textbox(Inches(1), Inches(0.5), Inches(4), Inches(1))
    textbox.text_frame.text = "Legacy PPT slide text preserved"
    pic = slide.shapes.add_picture(io.BytesIO(_png_bytes((220, 30, 40))), Inches(1), Inches(2))
    pic.name = "wmfpic"
    raw_buf = io.BytesIO()
    pres.save(raw_buf)

    with zipfile.ZipFile(io.BytesIO(raw_buf.getvalue())) as zin:
        files = {name: zin.read(name) for name in zin.namelist()}
    files["ppt/media/image1.wmf"] = b"\xd7\xcd\xc6\x9a\x00\x00" + b"\x00" * 64
    del files["ppt/media/image1.png"]
    for name, blob in list(files.items()):
        if name.endswith(".rels"):
            files[name] = blob.replace(b"image1.png", b"image1.wmf")
    types_root = etree.fromstring(files["[Content_Types].xml"])
    etree.SubElement(
        types_root,
        "{http://schemas.openxmlformats.org/package/2006/content-types}Default",
        Extension="wmf",
        ContentType="image/x-wmf",
    )
    files["[Content_Types].xml"] = etree.tostring(types_root)
    out = io.BytesIO()
    with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zout:
        for name, blob in files.items():
            zout.writestr(name, blob)
    return out.getvalue()


def _pptx_with_multiple_images() -> bytes:
    pres = Presentation()
    slide = pres.slides.add_slide(pres.slide_layouts[6])
    textbox = slide.shapes.add_textbox(Inches(1), Inches(0.5), Inches(4), Inches(1))
    textbox.text_frame.text = "Image heavy presentation"
    p1 = slide.shapes.add_picture(io.BytesIO(_png_bytes((220, 30, 40))), Inches(1), Inches(1.5))
    p1.name = "img1"
    p2 = slide.shapes.add_picture(io.BytesIO(_png_bytes((30, 80, 220))), Inches(1), Inches(3.5))
    p2.name = "img2"
    out = io.BytesIO()
    pres.save(out)
    return out.getvalue()


class TestPptConvert(unittest.TestCase):
    def test_legacy_ppt_magic(self):
        content = LEGACY_PPT.read_bytes()
        self.assertTrue(is_ole_compound(content))
        self.assertFalse(is_zip_openxml(content))
        self.assertTrue(needs_ppt_to_pptx_conversion(content, "ppt"))

    def test_pptx_does_not_need_conversion(self):
        content = PPTX_SAMPLE.read_bytes()
        self.assertTrue(is_zip_openxml(content))
        self.assertFalse(needs_ppt_to_pptx_conversion(content, "pptx"))

    def test_normalize_pptx_passthrough(self):
        content = PPTX_SAMPLE.read_bytes()
        out, ext = normalize_ppt_bytes(content, "pptx")
        self.assertEqual(out, content)
        self.assertEqual(ext, ".pptx")

    def test_legacy_ppt_requires_soffice(self):
        raw_ppt = LEGACY_PPT.read_bytes()
        if not shutil.which("soffice"):
            with self.assertRaises(ValueError) as ctx:
                normalize_ppt_bytes(raw_ppt, "ppt")
            self.assertIn("LibreOffice", str(ctx.exception))

            sample_pptx = PPTX_SAMPLE.read_bytes()

            def _fake_soffice_convert(
                _soffice, *, convert_to, outdir, input_path, profile_dir, timeout=120
            ):
                self.assertEqual(convert_to, "pptx")
                self.assertTrue(os.path.isfile(input_path))
                self.assertTrue(os.path.isdir(profile_dir))
                with open(os.path.join(outdir, "input.pptx"), "wb") as handle:
                    handle.write(sample_pptx)
                return b"", b"", 0

            with (
                patch("docreader.parser.ppt_convert.find_soffice", return_value="/bin/soffice"),
                patch(
                    "docreader.parser.ppt_convert.run_soffice_convert",
                    side_effect=_fake_soffice_convert,
                ),
            ):
                converted = convert_ppt_to_pptx_bytes(raw_ppt, suffix=".ppt")
                self.assertIsNotNone(converted)
                assert converted is not None
                self.assertTrue(is_zip_openxml(converted))
                out, ext = normalize_ppt_bytes(raw_ppt, "ppt")
                self.assertEqual(ext, ".pptx")
                self.assertTrue(is_zip_openxml(out))
            return

        converted = convert_ppt_to_pptx_bytes(raw_ppt, suffix=".ppt")
        self.assertIsNotNone(converted)
        assert converted is not None
        self.assertTrue(is_zip_openxml(converted))
        out, ext = normalize_ppt_bytes(raw_ppt, "ppt")
        self.assertEqual(ext, ".pptx")
        self.assertTrue(is_zip_openxml(out))

    def test_wmf_legacy_ppt_skips_disallowed_vector_delegate(self):
        from docreader.parser.markitdown_parser import MarkitdownParser

        if shutil.which("soffice") and (TESTDATA / "ppt_old" / "en_38256.ppt").is_file():
            doc = MarkitdownParser(file_type="ppt").parse_into_text(WMF_IMAGE_PPT.read_bytes())
        else:
            wmf_pptx = _pptx_with_wmf_and_text()
            with patch(
                "docreader.parser.ppt_convert.convert_ppt_to_pptx_bytes",
                return_value=wmf_pptx,
            ):
                doc = MarkitdownParser(file_type="ppt").parse_into_text(LEGACY_PPT.read_bytes())
        # MED-15 disables WMF/EMF/SVG/EPS delegate conversion while preserving slide text.
        self.assertTrue(doc.content.strip())
        self.assertEqual(len(doc.images), 0)

    def test_image_heavy_legacy_ppt_extracts_images(self):
        from docreader.parser.markitdown_parser import MarkitdownParser

        if shutil.which("soffice") and (TESTDATA / "ppt_old" / "en_41384.ppt").is_file():
            doc = MarkitdownParser(file_type="ppt").parse_into_text(IMAGE_HEAVY_PPT.read_bytes())
        else:
            rich_pptx = _pptx_with_multiple_images()
            with patch(
                "docreader.parser.ppt_convert.convert_ppt_to_pptx_bytes",
                return_value=rich_pptx,
            ):
                doc = MarkitdownParser(file_type="ppt").parse_into_text(LEGACY_PPT.read_bytes())
        self.assertGreaterEqual(len(doc.images), 2)
        self.assertNotIn("![](.jpg)", doc.content)
        for ref in doc.images:
            self.assertTrue(ref.startswith("images/"))


if __name__ == "__main__":
    unittest.main()
