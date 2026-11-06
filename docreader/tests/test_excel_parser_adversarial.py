"""
Adversarial stress testing suite for docreader/parser/excel_parser.py
and docreader/parser/xlsx_media.py.

Covers:
1. Malformed/corrupted zip files, zip bombs, and XML bombs (Billion Laughs, quadratic expansion, malformed tags).
2. Multi-sheet workbooks with mixed image types (WPS DISPIMG + floating drawings + Office 365 richData).
3. Overlapping coordinates, boundary anchors, formula errors, and large workbook scaling.
"""

from __future__ import annotations

import base64
import io
import os
import unittest
import zipfile
from unittest.mock import Mock, patch

import openpyxl

from docreader.parser.excel_parser import ExcelParser
from docreader.parser.xlsx_media import XLSXImageExtractor

# Standard 1x1 transparent PNG bytes for synthetic test fixtures
DUMMY_PNG_BYTES = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
)

# Alternative 1x1 JPEG bytes for multi-image tests
DUMMY_JPEG_BYTES = (
    b"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00"
    b"\xff\xdb\x00C\x00\x08\x06\x06\x07\x06\x05\x08\x07\x07\x07\t\t"
    b"\x08\n\x0c\x14\r\x0c\x0b\x0b\x0c\x19\x12\x13\x0f\x14\x1d\x1a"
    b"\x1f\x1e\x1d\x1a\x1c\x1c $.' \",#\x1c\x1c(7),01444\x1f'9=82<.342"
    b"\xff\xc0\x00\x0b\x08\x00\x01\x00\x01\x01\x01\x11\x00\xff\xc4"
    b"\x00\x1f\x00\x00\x01\x05\x01\x01\x01\x01\x01\x01\x00\x00\x00"
    b"\x00\x00\x00\x00\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\x0b"
    b"\xff\xda\x00\x08\x01\x01\x00\x00?\x00\xbf\x00\xff\xd9"
)

# Distinct 1x1 GIF bytes for testing 3rd format
DUMMY_GIF_BYTES = (
    b"GIF89a\x01\x00\x01\x00\x80\x00\x00\xff\xff\xff\x00\x00\x00!\xf9"
    b"\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"
)


def _build_multisheet_mixed_xlsx(
    sheet1_rows: list[list[str]],
    wps_cellimages: list[tuple[str, str, bytes]],
    sheet2_rows: list[list[str]],
    floating_anchors: list[dict],
    sheet3_rows: list[list[str]],
    rich_images: list[tuple[int, str, str, bytes]],
    sheet_names: tuple[str, str, str] = ("SheetWPS", "SheetDrawings", "SheetRichData"),
) -> bytes:
    """Build in-memory 3-sheet XLSX workbook containing all three image mechanisms:
    1. WPS DISPIMG formulas and xl/cellimages.xml on Sheet 1
    2. Floating drawings (twoCell/oneCell anchors) on Sheet 2
    3. Office 365 richData metadata/rdrichvalue on Sheet 3
    """
    wb = openpyxl.Workbook()
    ws1 = wb.active
    ws1.title = sheet_names[0]
    for r_idx, row in enumerate(sheet1_rows, start=1):
        for c_idx, val in enumerate(row, start=1):
            ws1.cell(row=r_idx, column=c_idx, value=val)

    ws2 = wb.create_sheet(title=sheet_names[1])
    for r_idx, row in enumerate(sheet2_rows, start=1):
        for c_idx, val in enumerate(row, start=1):
            ws2.cell(row=r_idx, column=c_idx, value=val)

    ws3 = wb.create_sheet(title=sheet_names[2])
    for r_idx, row in enumerate(sheet3_rows, start=1):
        for c_idx, val in enumerate(row, start=1):
            ws3.cell(row=r_idx, column=c_idx, value=val)

    bio = io.BytesIO()
    wb.save(bio)

    media_map: dict[str, bytes] = {}

    # --- 1. WPS DISPIMG setup ---
    pics_xml = []
    wps_rels_xml = []
    for idx, (img_id, descr, img_data) in enumerate(wps_cellimages, start=1):
        r_id = f"rIdWps{idx}"
        target_name = f"media/wps_img_{idx}.png"
        media_map[f"xl/{target_name}"] = img_data
        pics_xml.append(f"""
        <etc:cellImage>
          <xdr:pic>
            <xdr:nvPicPr>
              <xdr:cNvPr id="{idx}" name="{img_id}" descr="{descr}"/>
              <xdr:cNvSpPr/>
            </xdr:nvPicPr>
            <xdr:blipFill>
              <a:blip r:embed="{r_id}"/>
            </xdr:blipFill>
          </xdr:pic>
        </etc:cellImage>
        """)
        wps_rels_xml.append(
            f'<Relationship Id="{r_id}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="{target_name}"/>'
        )

    cellimages_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <etc:cellImages xmlns:etc="http://www.wps.cn/officeDocument/2017/etCustomData"
                    xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"
                    xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
                    xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
      {"".join(pics_xml)}
    </etc:cellImages>"""

    wps_rels_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      {"".join(wps_rels_xml)}
    </Relationships>"""

    # --- 2. Floating drawings setup for Sheet 2 ---
    anchors_xml = []
    drawing_rels = []
    for idx, anc in enumerate(floating_anchors, start=1):
        r_id = f"rIdDraw{idx}"
        target = f"../media/draw_img_{idx}.png"
        media_map[f"xl/media/draw_img_{idx}.png"] = anc.get("data", DUMMY_PNG_BYTES)
        drawing_rels.append(
            f'<Relationship Id="{r_id}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="{target}"/>'
        )

        c = anc["col"]
        r = anc["row"]
        name = anc.get("name", f"Picture {idx}")
        descr_attr = f'descr="{anc["descr"]}"' if "descr" in anc else ""

        if anc.get("type") == "oneCellAnchor":
            anchors_xml.append(f"""
            <xdr:oneCellAnchor>
              <xdr:from>
                <xdr:col>{c}</xdr:col><xdr:colOff>0</xdr:colOff>
                <xdr:row>{r}</xdr:row><xdr:rowOff>0</xdr:rowOff>
              </xdr:from>
              <xdr:ext cx="50000" cy="50000"/>
              <xdr:pic>
                <xdr:nvPicPr>
                  <xdr:cNvPr id="{idx}" name="{name}" {descr_attr}/>
                  <xdr:cNvSpPr/>
                </xdr:nvPicPr>
                <xdr:blipFill><a:blip r:embed="{r_id}"/></xdr:blipFill>
              </xdr:pic>
              <xdr:clientData/>
            </xdr:oneCellAnchor>""")
        else:
            anchors_xml.append(f"""
            <xdr:twoCellAnchor editAs="oneCell">
              <xdr:from>
                <xdr:col>{c}</xdr:col><xdr:colOff>0</xdr:colOff>
                <xdr:row>{r}</xdr:row><xdr:rowOff>0</xdr:rowOff>
              </xdr:from>
              <xdr:to>
                <xdr:col>{c + 1}</xdr:col><xdr:colOff>0</xdr:colOff>
                <xdr:row>{r + 1}</xdr:row><xdr:rowOff>0</xdr:rowOff>
              </xdr:to>
              <xdr:pic>
                <xdr:nvPicPr>
                  <xdr:cNvPr id="{idx}" name="{name}" {descr_attr}/>
                  <xdr:cNvSpPr/>
                </xdr:nvPicPr>
                <xdr:blipFill><a:blip r:embed="{r_id}"/></xdr:blipFill>
              </xdr:pic>
              <xdr:clientData/>
            </xdr:twoCellAnchor>""")

    drawing1_xml = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"
              xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
              xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
      {"".join(anchors_xml)}
    </xdr:wsDr>"""

    drawing1_rels = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      {"".join(drawing_rels)}
    </Relationships>"""

    ws2_rels = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      <Relationship Id="rIdDrawing1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/drawing" Target="../drawings/drawing1.xml"/>
    </Relationships>"""

    # --- 3. Office 365 richData setup for Sheet 3 ---
    rd_rv_items = []
    rd_rel_items = []
    rd_rels_items = []
    meta_bk_items = []
    sheet3_cell_vms: dict[str, int] = {}

    for idx, (vm_val, cell_coord, alt_text, img_data) in enumerate(rich_images, start=1):
        rel_idx = idx - 1
        r_id = f"rIdRich{idx}"
        target = f"../media/rich_img_{idx}.png"
        media_map[f"xl/media/rich_img_{idx}.png"] = img_data

        rd_rels_items.append(
            f'<Relationship Id="{r_id}" Type="http://schemas.microsoft.com/office/2017/06/relationships/image" Target="{target}"/>'
        )
        rd_rel_items.append(f'<rel r:id="{r_id}"/>')
        rd_rv_items.append(f'<rv s="0"><v>{rel_idx}</v><v>5</v><v>{alt_text}</v></rv>')
        meta_bk_items.append(f'<bk><rc t="1" v="{rel_idx}"/></bk>')
        sheet3_cell_vms[cell_coord] = vm_val

    meta_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <metadata xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
      <valueMetadata count="{len(meta_bk_items)}">
        {"".join(meta_bk_items)}
      </valueMetadata>
    </metadata>"""

    rd_rv_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <rvData xmlns="http://schemas.microsoft.com/office/spreadsheetml/2017/richdata">
      {"".join(rd_rv_items)}
    </rvData>"""

    rd_rel_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <richValueRels xmlns="http://schemas.microsoft.com/office/spreadsheetml/2017/richdata"
                   xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
      {"".join(rd_rel_items)}
    </richValueRels>"""

    rd_rels_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
    <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
      {"".join(rd_rels_items)}
    </Relationships>"""

    # Assemble package
    out = io.BytesIO()
    with zipfile.ZipFile(bio, "r") as zin, zipfile.ZipFile(
        out, "w", zipfile.ZIP_DEFLATED
    ) as zout:
        for item in zin.infolist():
            content = zin.read(item.filename)
            if item.filename == "xl/worksheets/sheet2.xml":
                zout.writestr(item, content)
            elif item.filename == "xl/worksheets/sheet3.xml" and sheet3_cell_vms:
                text_xml = content.decode("utf-8")
                for coord, vm in sheet3_cell_vms.items():
                    text_xml = text_xml.replace(f'<c r="{coord}"', f'<c r="{coord}" vm="{vm}"')
                zout.writestr(item, text_xml.encode("utf-8"))
            else:
                zout.writestr(item, content)

        if wps_cellimages:
            zout.writestr("xl/cellimages.xml", cellimages_content)
            zout.writestr("xl/_rels/cellimages.xml.rels", wps_rels_content)

        if floating_anchors:
            zout.writestr("xl/drawings/drawing1.xml", drawing1_xml)
            zout.writestr("xl/drawings/_rels/drawing1.xml.rels", drawing1_rels)
            zout.writestr("xl/worksheets/_rels/sheet2.xml.rels", ws2_rels)

        if rich_images:
            zout.writestr("xl/metadata.xml", meta_content)
            zout.writestr("xl/richData/rdrichvalue.xml", rd_rv_content)
            zout.writestr("xl/richData/richValueRel.xml", rd_rel_content)
            zout.writestr("xl/richData/_rels/richValueRel.xml.rels", rd_rels_content)

        for path, data in media_map.items():
            zout.writestr(path, data)

    return out.getvalue()


class AdversarialZipAndXmlBombsTest(unittest.TestCase):
    """Adversarial stress testing against zip corruption, zip bombs, and XML bombs."""

    def test_corrupt_zip_zero_bytes(self):
        """Zero-byte file input returns empty Document gracefully without exception."""
        parser = ExcelParser(file_name="empty.xlsx", file_type="xlsx")
        doc = parser.parse_into_text(b"")
        self.assertEqual(doc.content, "")
        self.assertEqual(len(doc.chunks), 0)
        self.assertEqual(len(doc.images), 0)

    def test_corrupt_zip_random_junk(self):
        """Random uncompressed junk data returns empty Document safely."""
        junk = os.urandom(2048)
        parser = ExcelParser(file_name="junk.xlsx", file_type="xlsx")
        doc = parser.parse_into_text(junk)
        self.assertEqual(doc.content, "")
        self.assertEqual(len(doc.chunks), 0)
        self.assertEqual(len(doc.images), 0)

    def test_corrupt_zip_fake_magic_header(self):
        """ZIP magic header followed by random garbage fails safely."""
        fake_zip = b"PK\x03\x04" + os.urandom(512)
        parser = ExcelParser(file_name="fake.xlsx", file_type="xlsx")
        doc = parser.parse_into_text(fake_zip)
        self.assertEqual(doc.content, "")
        self.assertEqual(len(doc.chunks), 0)
        self.assertEqual(len(doc.images), 0)

    def test_corrupt_zip_truncated_valid_archive(self):
        """A valid zip truncated halfway through should not raise unhandled exceptions."""
        valid_xlsx = _build_multisheet_mixed_xlsx(
            [["A", "B"]], [], [["C", "D"]], [], [["E", "F"]], []
        )
        truncated_zip = valid_xlsx[: len(valid_xlsx) // 2]
        parser = ExcelParser(file_name="truncated.xlsx", file_type="xlsx")
        doc = parser.parse_into_text(truncated_zip)
        self.assertIsInstance(doc.content, str)
        self.assertIsInstance(doc.images, dict)

    def test_zip_bomb_huge_entry_rejected(self):
        """A zip archive with an entry exceeding 100MB uncompressed limit is rejected by extractor."""
        bio = io.BytesIO()
        with zipfile.ZipFile(bio, "w", zipfile.ZIP_DEFLATED) as zf:
            zf.writestr("xl/media/huge.png", b"\x00" * (105 * 1024 * 1024))
        extractor = XLSXImageExtractor(bio.getvalue())
        with self.assertRaisesRegex(ValueError, "exceeds limit"):
            extractor.extract_cell_images()

    def test_zip_bomb_high_compression_ratio_rejected(self):
        """A zip archive with > 100:1 compression ratio on > 1MB entry is rejected."""
        bio = io.BytesIO()
        with zipfile.ZipFile(bio, "w", zipfile.ZIP_DEFLATED) as zf:
            zf.writestr("xl/media/ratio_bomb.png", b"\x00" * (5 * 1024 * 1024))
        extractor = XLSXImageExtractor(bio.getvalue())
        with self.assertRaisesRegex(ValueError, "compression ratio"):
            extractor.extract_cell_images()

    def test_xml_bomb_billion_laughs_in_cellimages(self):
        """Billion Laughs entity expansion attack in xl/cellimages.xml is safely stopped."""
        xml_bomb = """<?xml version="1.0"?>
        <!DOCTYPE lolz [
         <!ENTITY lol "lol">
         <!ELEMENT lolz (#PCDATA)>
         <!ENTITY lol1 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
         <!ENTITY lol2 "&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;">
         <!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">
         <!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;">
         <!ENTITY lol5 "&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;">
         <!ENTITY lol6 "&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;">
         <!ENTITY lol7 "&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;">
         <!ENTITY lol8 "&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;">
         <!ENTITY lol9 "&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;">
        ]>
        <etc:cellImages xmlns:etc="http://www.wps.cn/officeDocument/2017/etCustomData">&lol9;</etc:cellImages>
        """
        valid_xlsx = _build_multisheet_mixed_xlsx(
            [["Item", "Price"], ["Solar Panel", "120.00"]],
            [],
            [["Desc"]],
            [],
            [["Note"]],
            [],
        )
        bio_in = io.BytesIO(valid_xlsx)
        bio_out = io.BytesIO()
        with zipfile.ZipFile(bio_in, "r") as zin, zipfile.ZipFile(bio_out, "w") as zout:
            for item in zin.infolist():
                zout.writestr(item, zin.read(item.filename))
            zout.writestr("xl/cellimages.xml", xml_bomb)

        parser = ExcelParser(file_name="bomb.xlsx", file_type="xlsx")
        doc = parser.parse_into_text(bio_out.getvalue())
        self.assertIn("Solar Panel", doc.content)
        self.assertEqual(len(doc.images), 0)

    def test_xml_bomb_billion_laughs_in_drawings(self):
        """Billion Laughs attack in xl/drawings/drawing1.xml is safely stopped."""
        xml_bomb = """<?xml version="1.0"?>
        <!DOCTYPE lolz [
         <!ENTITY lol "lol">
         <!ELEMENT lolz (#PCDATA)>
         <!ENTITY lol1 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
         <!ENTITY lol2 "&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;&lol1;">
         <!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;">
         <!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;">
         <!ENTITY lol5 "&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;">
         <!ENTITY lol6 "&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;&lol5;">
         <!ENTITY lol7 "&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;&lol6;">
         <!ENTITY lol8 "&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;&lol7;">
         <!ENTITY lol9 "&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;&lol8;">
        ]>
        <xdr:wsDr xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing">&lol9;</xdr:wsDr>
        """
        valid_xlsx = _build_multisheet_mixed_xlsx(
            [["Col1"]],
            [],
            [["Row1"], ["Row2"]],
            [{"type": "twoCellAnchor", "col": 0, "row": 0, "name": "Pic", "data": DUMMY_PNG_BYTES}],
            [["Note"]],
            [],
        )
        bio_in = io.BytesIO(valid_xlsx)
        bio_out = io.BytesIO()
        with zipfile.ZipFile(bio_in, "r") as zin, zipfile.ZipFile(bio_out, "w") as zout:
            for item in zin.infolist():
                if item.filename == "xl/drawings/drawing1.xml":
                    zout.writestr(item.filename, xml_bomb)
                else:
                    zout.writestr(item, zin.read(item.filename))

        parser = ExcelParser(file_name="bomb_draw.xlsx", file_type="xlsx")
        doc = parser.parse_into_text(bio_out.getvalue())
        self.assertIn("Row1", doc.content)
        self.assertIn("Row2", doc.content)

    def test_malformed_xml_unclosed_tags_and_null_bytes(self):
        """Malformed XML with unclosed tags and embedded null bytes does not crash parser."""
        bad_xml = b"<root><unclosed>This is invalid XML\x00\x01\x02</root"
        valid_xlsx = _build_multisheet_mixed_xlsx([["Data"]], [], [["Data2"]], [], [["Data3"]], [])
        bio_in = io.BytesIO(valid_xlsx)
        bio_out = io.BytesIO()
        with zipfile.ZipFile(bio_in, "r") as zin, zipfile.ZipFile(bio_out, "w") as zout:
            for item in zin.infolist():
                zout.writestr(item, zin.read(item.filename))
            zout.writestr("xl/cellimages.xml", bad_xml)
            zout.writestr("xl/richData/rdrichvalue.xml", bad_xml)

        parser = ExcelParser(file_name="bad_xml.xlsx", file_type="xlsx")
        doc = parser.parse_into_text(bio_out.getvalue())
        self.assertIn("Data", doc.content)


class AdversarialMultiSheetMixedImagesTest(unittest.TestCase):
    """Stress testing multi-sheet workbooks with mixed image vectors (WPS + drawings + richData)."""

    def test_multisheet_all_three_image_types_extracted_simultaneously(self):
        """Simultaneous extraction of WPS DISPIMG on Sheet 1, drawings on Sheet 2, and richData on Sheet 3."""
        sheet1_rows = [
            ["SKU", "Image", "Qty"],
            ["WPS-101", '=_xlfn.DISPIMG("WPS_IMG_1", 1)', "50"],
        ]
        wps_cellimages = [("WPS_IMG_1", "WPS Battery Pack", DUMMY_PNG_BYTES)]

        sheet2_rows = [
            ["SKU", "Diagram", "Status"],
            ["DRW-202", "", "Active"],
        ]
        floating_anchors = [
            {
                "type": "twoCellAnchor",
                "col": 1,  # Col B
                "row": 1,  # Row 2 (0-based 1)
                "name": "DrawDiag",
                "descr": "Wiring Schematic",
                "data": DUMMY_JPEG_BYTES,
            }
        ]

        sheet3_rows = [
            ["SKU", "Spec", "RichPhoto"],
            ["RCH-303", "100Ah", ""],
        ]
        rich_images = [
            (1, "C2", "RichData Cell Photo", DUMMY_GIF_BYTES)
        ]

        xlsx_data = _build_multisheet_mixed_xlsx(
            sheet1_rows,
            wps_cellimages,
            sheet2_rows,
            floating_anchors,
            sheet3_rows,
            rich_images,
            sheet_names=("SheetWPS", "SheetDrawings", "SheetRichData"),
        )

        parser = ExcelParser(
            file_name="mixed_multisheet.xlsx",
            file_type="xlsx",
            xlsx_ocr_enabled=False,
        )
        doc = parser.parse_into_text(xlsx_data)

        # 3 distinct images must be extracted
        self.assertEqual(len(doc.images), 3)

        # Verify Sheet 1 contains WPS image link
        chunk1 = doc.chunks[1].content
        self.assertIn("A: WPS-101", chunk1)
        self.assertIn("B: ![WPS Battery Pack](images/", chunk1)

        # Verify Sheet 2 contains Drawing image link
        chunk3 = doc.chunks[3].content
        self.assertIn("A: DRW-202", chunk3)
        self.assertIn("B: ![Wiring Schematic](images/", chunk3)

        # Verify Sheet 3 contains RichData image link
        chunk5 = doc.chunks[5].content
        self.assertIn("A: RCH-303", chunk5)
        self.assertIn("C: ![RichData Cell Photo](images/", chunk5)

        # Verify source_blocks have correct sheet locators
        sheets_in_blocks = {b["locator"]["sheet"] for b in doc.source_blocks}
        self.assertIn("SheetWPS", sheets_in_blocks)
        self.assertIn("SheetDrawings", sheets_in_blocks)
        self.assertIn("SheetRichData", sheets_in_blocks)

    def test_multisheet_cross_sheet_media_deduplication(self):
        """When identical image binaries are used in Sheet 1 (WPS) and Sheet 2 (Drawings), they deduplicate."""
        shared_image = DUMMY_PNG_BYTES
        sheet1_rows = [["Item", "Img"], ["A1", '=_xlfn.DISPIMG("WPS_SHARED", 1)']]
        wps_cellimages = [("WPS_SHARED", "Shared Image WPS", shared_image)]

        sheet2_rows = [["Item", "Img"], ["B1", ""]]
        floating_anchors = [
            {
                "type": "twoCellAnchor",
                "col": 1,
                "row": 1,
                "name": "DrawShared",
                "descr": "Shared Image Draw",
                "data": shared_image,  # Same binary
            }
        ]

        xlsx_data = _build_multisheet_mixed_xlsx(
            sheet1_rows,
            wps_cellimages,
            sheet2_rows,
            floating_anchors,
            [["Placeholder"]],
            [],
        )

        parser = ExcelParser(
            file_name="shared.xlsx", file_type="xlsx", xlsx_ocr_enabled=False
        )
        doc = parser.parse_into_text(xlsx_data)

        # Exactly 1 deduplicated image entry in doc.images
        self.assertEqual(len(doc.images), 1)
        single_ref = list(doc.images.keys())[0]

        # Both sheet chunks should point to this same reference
        self.assertIn(f"({single_ref})", doc.chunks[1].content)
        self.assertIn(f"({single_ref})", doc.chunks[3].content)

    def test_multisheet_chinese_and_special_names_no_collision(self):
        """Sheet names with Unicode Chinese characters and symbols do not collide or corrupt locators."""
        sheet_names = ("储能设备_明细", "光伏逆变器 (2026)", "备品备件&电池")
        sheet1_rows = [["序号", "图片"], ["1", '=_xlfn.DISPIMG("WPS_CN", 1)']]
        wps_cellimages = [("WPS_CN", "储能系统外观图", DUMMY_PNG_BYTES)]

        sheet2_rows = [["型号", "图纸"], ["SG50CX", ""]]
        floating_anchors = [
            {
                "type": "twoCellAnchor",
                "col": 1,
                "row": 1,
                "name": "DrawCN",
                "descr": "逆变器尺寸图",
                "data": DUMMY_JPEG_BYTES,
            }
        ]

        xlsx_data = _build_multisheet_mixed_xlsx(
            sheet1_rows,
            wps_cellimages,
            sheet2_rows,
            floating_anchors,
            [["备件名称"], ["电缆"]],
            [],
            sheet_names=sheet_names,
        )

        parser = ExcelParser(
            file_name="chinese_sheets.xlsx", file_type="xlsx", xlsx_ocr_enabled=False
        )
        doc = parser.parse_into_text(xlsx_data)

        self.assertEqual(len(doc.images), 2)
        sheets_found = [b["locator"]["sheet"] for b in doc.source_blocks]
        self.assertIn("储能设备_明细", sheets_found)
        self.assertIn("光伏逆变器 (2026)", sheets_found)
        self.assertIn("备品备件&电池", sheets_found)


class AdversarialOverlappingAndEdgeCoordinatesTest(unittest.TestCase):
    """Stress testing overlapping coordinates, out-of-bounds anchors, and formula error handling."""

    def test_overlapping_coordinates_wps_and_drawing_same_cell(self):
        """When both WPS DISPIMG and floating drawing target the exact same cell (Row 2, Col B)."""
        sheet1_rows = [
            ["SKU", "Photo"],
            ["MOD-01", '=_xlfn.DISPIMG("WPS_CELL_B2", 1)'],
        ]
        wps_cellimages = [("WPS_CELL_B2", "WPS Image B2", DUMMY_PNG_BYTES)]

        # Sheet 2 has 2 drawings pointing to identical (row 1, col 1) -> (2, "B")
        sheet2_rows = [
            ["Code", "Diagram"],
            ["C-100", ""],
        ]
        floating_anchors = [
            {
                "type": "twoCellAnchor",
                "col": 1,
                "row": 1,
                "name": "Draw_First",
                "descr": "First Diagram",
                "data": DUMMY_PNG_BYTES,
            },
            {
                "type": "twoCellAnchor",
                "col": 1,
                "row": 1,
                "name": "Draw_Second",
                "descr": "Second Diagram",
                "data": DUMMY_JPEG_BYTES,
            },
        ]

        xlsx_data = _build_multisheet_mixed_xlsx(
            sheet1_rows,
            wps_cellimages,
            sheet2_rows,
            floating_anchors,
            [["Empty"]],
            [],
        )

        parser = ExcelParser(
            file_name="overlapping.xlsx", file_type="xlsx", xlsx_ocr_enabled=False
        )
        doc = parser.parse_into_text(xlsx_data)

        # All distinct media binaries must be safely kept in doc.images
        self.assertGreaterEqual(len(doc.images), 2)
        # Sheet 2 row chunk must contain the diagram without crashing
        self.assertIn("B: ![", doc.chunks[3].content)

    def test_orphan_floating_drawings_beyond_dataframe_rows(self):
        """Drawings anchored at row 50, while sheet only has 2 rows of tabular data."""
        sheet1_rows = [["Header1"], ["Value1"]]
        floating_anchors = [
            {
                "type": "twoCellAnchor",
                "col": 0,  # Col A
                "row": 49,  # Row 50 (0-based 49)
                "name": "OrphanPic",
                "descr": "Detached Footer Diagram",
                "data": DUMMY_PNG_BYTES,
            }
        ]

        xlsx_data = _build_multisheet_mixed_xlsx(
            sheet1_rows,
            [],
            [["Row1"], ["Row2"]],
            floating_anchors,
            [["Empty"]],
            [],
        )

        parser = ExcelParser(
            file_name="orphan.xlsx", file_type="xlsx", xlsx_ocr_enabled=False
        )
        doc = parser.parse_into_text(xlsx_data)

        # Orphan image must NOT be dropped
        self.assertEqual(len(doc.images), 1)

        # An orphan chunk must be emitted for SheetDrawings
        found_orphan_chunk = any(
            "Detached Footer Diagram" in ch.content for ch in doc.chunks
        )
        self.assertTrue(found_orphan_chunk, "Orphan floating image not found in chunks")

        # And source block locator must reflect row 50
        orphan_blocks = [
            b for b in doc.source_blocks if b["locator"]["row_start"] == 50
        ]
        self.assertEqual(len(orphan_blocks), 1)

    def test_floating_anchor_out_of_bounds_and_negative_coords(self):
        """Floating anchors with col=-1, row=-1, or col=500 are handled safely."""
        sheet1_rows = [["Col1", "Col2"], ["Val1", "Val2"]]
        floating_anchors = [
            {
                "type": "twoCellAnchor",
                "col": -1,  # Negative column
                "row": 1,
                "name": "NegCol",
                "descr": "Invalid Neg Col",
                "data": DUMMY_PNG_BYTES,
            },
            {
                "type": "twoCellAnchor",
                "col": 500,  # Far out-of-bounds column
                "row": 1,
                "name": "FarCol",
                "descr": "Far Column Diagram",
                "data": DUMMY_JPEG_BYTES,
            },
        ]

        xlsx_data = _build_multisheet_mixed_xlsx(
            sheet1_rows,
            [],
            [["R1", "R2"], ["V1", "V2"]],
            floating_anchors,
            [["Empty"]],
            [],
        )

        parser = ExcelParser(
            file_name="bad_coords.xlsx", file_type="xlsx", xlsx_ocr_enabled=False
        )
        doc = parser.parse_into_text(xlsx_data)

        # Valid text rows parsed successfully
        self.assertIn("Val1", doc.content)
        self.assertIn("V1", doc.content)

    def test_formula_errors_suppressed_when_images_present(self):
        """Cells with formula errors (#VALUE!, #REF!, #N/A, #DIV/0!) don't leak error strings into image tags."""
        sheet1_rows = [
            ["SKU", "ImageCell", "Status"],
            ["ERR-01", "#VALUE!", "OK"],
            ["ERR-02", "#REF!", "OK"],
        ]
        wb = openpyxl.Workbook()
        ws = wb.active
        for r_idx, row in enumerate(sheet1_rows, start=1):
            for c_idx, val in enumerate(row, start=1):
                ws.cell(row=r_idx, column=c_idx, value=val)
        bio = io.BytesIO()
        wb.save(bio)

        pics_xml = [
            '<etc:cellImage><xdr:pic><xdr:nvPicPr><xdr:cNvPr id="1" name="IMG1" descr="Clean Image 1"/><xdr:cNvSpPr/></xdr:nvPicPr><xdr:blipFill><a:blip r:embed="rId1"/></xdr:blipFill></xdr:pic></etc:cellImage>',
            '<etc:cellImage><xdr:pic><xdr:nvPicPr><xdr:cNvPr id="2" name="IMG2" descr="Clean Image 2"/><xdr:cNvSpPr/></xdr:nvPicPr><xdr:blipFill><a:blip r:embed="rId2"/></xdr:blipFill></xdr:pic></etc:cellImage>',
        ]
        rels_xml = [
            '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>',
            '<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image2.png"/>',
        ]
        cellimages_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
        <etc:cellImages xmlns:etc="http://www.wps.cn/officeDocument/2017/etCustomData"
                        xmlns:xdr="http://schemas.openxmlformats.org/drawingml/2006/spreadsheetDrawing"
                        xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
                        xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
          {"".join(pics_xml)}
        </etc:cellImages>"""
        rels_content = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
        <Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
          {"".join(rels_xml)}
        </Relationships>"""

        out = io.BytesIO()
        with zipfile.ZipFile(bio, "r") as zin, zipfile.ZipFile(out, "w") as zout:
            for item in zin.infolist():
                content = zin.read(item.filename)
                if item.filename == "xl/worksheets/sheet1.xml":
                    text_xml = content.decode("utf-8")
                    text_xml = text_xml.replace(
                        '<c r="B2" t="e"><v>#VALUE!</v></c>',
                        '<c r="B2" t="e"><f>_xlfn.DISPIMG("IMG1",1)</f><v>#VALUE!</v></c>',
                    )
                    text_xml = text_xml.replace(
                        '<c r="B3" t="e"><v>#REF!</v></c>',
                        '<c r="B3" t="e"><f>_xlfn.DISPIMG("IMG2",1)</f><v>#REF!</v></c>',
                    )
                    zout.writestr(item, text_xml.encode("utf-8"))
                else:
                    zout.writestr(item, content)
            zout.writestr("xl/cellimages.xml", cellimages_content)
            zout.writestr("xl/_rels/cellimages.xml.rels", rels_content)
            zout.writestr("xl/media/image1.png", DUMMY_PNG_BYTES)
            zout.writestr("xl/media/image2.png", DUMMY_PNG_BYTES + b"_distinct_2")

        parser = ExcelParser(
            file_name="errors.xlsx", file_type="xlsx", xlsx_ocr_enabled=False
        )
        doc = parser.parse_into_text(out.getvalue())

        # Formula errors must NOT appear alongside image markdown in B2
        chunk1 = doc.chunks[1].content
        self.assertNotIn("#VALUE!", chunk1)
        self.assertIn("B: ![Clean Image 1](images/", chunk1)

        # In B3, #REF! must not appear
        chunk2 = doc.chunks[2].content
        self.assertNotIn("#REF!", chunk2)
        self.assertIn("B: ![Clean Image 2](images/", chunk2)


class AdversarialScaleAndConcurrencyTest(unittest.TestCase):
    """Scale testing with large row counts and RapidOCR exception isolation."""

    def test_large_workbook_scale(self):
        """A workbook with 1,000 rows x 10 columns and 5 deduplicated images processes cleanly."""
        headers = [f"Col_{idx}" for idx in range(1, 11)]
        rows = [headers]
        for r in range(1, 1001):
            row_data = [f"Val_{r}_{c}" for c in range(1, 11)]
            # Every 20th row has a DISPIMG formula in Col 2
            if r % 20 == 0:
                img_num = (r // 20) % 5 + 1
                row_data[1] = f'=_xlfn.DISPIMG("IMG_{img_num}", 1)'
            rows.append(row_data)

        # 5 distinct image binaries
        cellimages = [
            (f"IMG_{i}", f"Scale Image {i}", DUMMY_PNG_BYTES + bytes([i])) for i in range(1, 6)
        ]

        from docreader.tests.test_excel_parser import _build_synthetic_dispimg_xlsx

        xlsx_data = _build_synthetic_dispimg_xlsx(rows, cellimages)

        parser = ExcelParser(
            file_name="large_scale.xlsx",
            file_type="xlsx",
            xlsx_ocr_enabled=False,
        )
        doc = parser.parse_into_text(xlsx_data)

        # 5 distinct deduplicated images
        self.assertEqual(len(doc.images), 5)
        # Exactly 1001 chunks (1 header + 1000 rows)
        self.assertEqual(len(doc.chunks), 1001)
        self.assertEqual(len(doc.source_blocks), 1001)

    def test_ocr_mock_exception_isolation_under_load(self):
        """When RapidOCR raises unexpected exceptions on certain images, parsing completes without failure."""
        mock_ocr = Mock()
        call_count = 0

        def side_effect(img_bytes):
            nonlocal call_count
            call_count += 1
            if call_count % 2 == 1:
                raise RuntimeError("Simulated RapidOCR crash under concurrency")
            mock_res = Mock()
            mock_res.txts = ["Recognized Text"]
            return mock_res

        mock_ocr.side_effect = side_effect

        rows = [
            ["Item", "Img"],
            ["Row1", '=_xlfn.DISPIMG("ID1", 1)'],
            ["Row2", '=_xlfn.DISPIMG("ID2", 1)'],
        ]
        cellimages = [
            ("ID1", "Image One", DUMMY_PNG_BYTES),
            ("ID2", "Image Two", DUMMY_JPEG_BYTES),
        ]
        from docreader.tests.test_excel_parser import _build_synthetic_dispimg_xlsx

        xlsx_data = _build_synthetic_dispimg_xlsx(rows, cellimages)

        with patch("docreader.parser.excel_parser._get_ocr_engine", return_value=mock_ocr):
            parser = ExcelParser(
                file_name="ocr_crash.xlsx",
                file_type="xlsx",
                xlsx_ocr_enabled=True,
            )
            doc = parser.parse_into_text(xlsx_data)

        self.assertEqual(len(doc.images), 2)
        # Document still parses completely
        self.assertEqual(len(doc.chunks), 3)


if __name__ == "__main__":
    unittest.main()
