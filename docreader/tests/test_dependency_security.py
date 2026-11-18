"""Regression coverage for dependency vulnerabilities in document input paths."""

import gzip
import io
import pickle
import tempfile
import unittest
from pathlib import Path

from pdfminer.high_level import extract_text

from docreader.parser.markitdown_parser import StdMarkitdownParser


class _PickleMarker:
    """A harmless marker that an unsafe CMap pickle loader would execute."""

    def __init__(self, path):
        self.path = path

    def __reduce__(self):
        return Path.write_text, (self.path, "unsafe CMap deserialization")


def _pdf_with_cmap(cmap_path):
    # PDF name escaping permits an absolute path in an Encoding name. Old
    # pdfminer versions followed it and deserialized <path>.pickle.gz.
    encoding = str(cmap_path).replace("#", "#23").replace("/", "#2F")
    stream = b"BT /F1 12 Tf 20 40 Td <0001> Tj ET"
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] "
        b"/Resources << /Font << /F1 4 0 R >> >> /Contents 6 0 R >>",
        (
            "<< /Type /Font /Subtype /Type0 /BaseFont /Test-Identity-H "
            f"/Encoding /{encoding} /DescendantFonts [5 0 R] >>"
        ).encode(),
        b"<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Test "
        b"/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> "
        b"/DW 1000 >>",
        b"<< /Length " + str(len(stream)).encode() + b" >>\nstream\n" + stream + b"\nendstream",
    ]
    data = bytearray(b"%PDF-1.4\n")
    offsets = [0]
    for index, obj in enumerate(objects, 1):
        offsets.append(len(data))
        data.extend(f"{index} 0 obj\n".encode() + obj + b"\nendobj\n")
    xref = len(data)
    data.extend(f"xref\n0 {len(offsets)}\n0000000000 65535 f \n".encode())
    for offset in offsets[1:]:
        data.extend(f"{offset:010} 00000 n \n".encode())
    data.extend(
        f"trailer\n<< /Size {len(offsets)} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n".encode()
    )
    return bytes(data)


class TestDependencySecurity(unittest.TestCase):
    """Ensure security regression checks execute under both pytest and unittest."""

    def test_pdf_cmap_cannot_deserialize_an_external_pickle(self):
        with tempfile.TemporaryDirectory() as raw_tmp:
            tmp_path = Path(raw_tmp)
            marker = tmp_path / "unpickled.txt"
            cmap = tmp_path / "external-cmap"
            with gzip.open(str(cmap) + ".pickle.gz", "wb") as file:
                pickle.dump({"CODE2CID": _PickleMarker(marker)}, file)

            pdf = _pdf_with_cmap(cmap)
            # Run the actual dependency and the application's selectable PDF engine.
            extract_text(io.BytesIO(pdf))
            StdMarkitdownParser(file_name="external-cmap.pdf", file_type="pdf").parse(pdf)
            self.assertFalse(marker.exists(), "PDF input executed an external CMap pickle")


if __name__ == "__main__":
    unittest.main()
