"""
XLSX Media and Cell Image Extractor.

Extracts embedded and anchored images from .xlsx workbooks:
1. Office 365 richData in-cell images (xl/richData/rdrichvalue.xml, xl/metadata.xml)
2. WPS Office DISPIMG in-cell images (xl/cellimages.xml, =DISPIMG(...) formulas)
3. Standard floating drawings (xl/drawings/drawing*.xml anchors)
4. Media deduplication by SHA-256 hash and generation of Document.images payloads.
"""

from __future__ import annotations

import base64
import contextlib
import hashlib
import io
import logging
import posixpath
import re
import uuid
import zipfile
from xml.etree.ElementTree import Element

from defusedxml import ElementTree as ET

from docreader.parser.archive import (
    ArchiveLimitError,
    safe_read_zip_member,
    validate_zip_archive,
)

logger = logging.getLogger(__name__)

# Security constants
MAX_UNCOMPRESSED_ARCHIVE_BYTES = 100 * 1024 * 1024  # 100 MB
MAX_COMPRESSION_RATIO = 100.0

# Regex patterns
_CELL_COORD_RE = re.compile(r"^([A-Za-z]+)(\d+)$")
_DISPIMG_RE = re.compile(r'DISPIMG\s*\(\s*(?:["\']([^"\']+)["\']|([^"\'\s,\)]+))', re.IGNORECASE)


def _local_tag(element: Element) -> str:
    """Return XML element tag without namespace prefix."""
    tag = element.tag
    if "}" in tag:
        return tag.split("}", 1)[1]
    return tag


def _get_attribute(element: Element, attr_suffix: str) -> str | None:
    """Return attribute value matching an attribute name or suffix."""
    for k, v in element.attrib.items():
        if k == attr_suffix or k.endswith(":" + attr_suffix) or k.endswith("}" + attr_suffix):
            return str(v)
    return None


def _resolve_target_path(base_dir: str, target: str) -> str:
    """Resolve and normalize OOXML relationship target path."""
    if target.startswith("/"):
        path = target.lstrip("/")
    else:
        path = posixpath.normpath(posixpath.join(base_dir, target))
    if path.startswith("media/"):
        path = "xl/" + path
    return path


def _get_element_text(element: Element) -> str:
    """Extract concatenated text of all child <t> nodes."""
    texts = [elem.text for elem in element.iter() if _local_tag(elem) == "t" and elem.text]
    return "".join(texts)


def _column_index_to_letter(col_idx: int) -> str:
    """Convert 1-based column index to Excel column letter (1 -> A, 27 -> AA)."""
    try:
        from openpyxl.utils import get_column_letter

        return str(get_column_letter(col_idx))
    except ImportError:
        result = []
        while col_idx > 0:
            col_idx, remainder = divmod(col_idx - 1, 26)
            result.append(chr(65 + remainder))
        return "".join(reversed(result))


class XLSXImageExtractor:
    """Extracts in-cell and floating images from raw .xlsx byte archives."""

    def __init__(
        self,
        content: bytes,
        max_uncompressed_bytes: int = MAX_UNCOMPRESSED_ARCHIVE_BYTES,
        max_compression_ratio: float = MAX_COMPRESSION_RATIO,
    ) -> None:
        self.content = content
        self.max_uncompressed_bytes = max_uncompressed_bytes
        self.max_compression_ratio = max_compression_ratio

        self.doc_images: dict[str, str] = {}
        self.extracted_bytes: dict[str, bytes] = {}
        self.image_bytes_by_ref: dict[str, bytes] = self.extracted_bytes
        self.hash_to_ref: dict[str, str] = {}
        self.path_to_ref: dict[str, str] = {}
        self.cell_images_by_sheet: dict[str, dict[tuple[int, str], tuple[str, str]]] = {}

    def extract_cell_images(
        self,
    ) -> tuple[dict[str, dict[tuple[int, str], tuple[str, str]]], dict[str, str]]:
        """Extract all cell images from the workbook archive.

        Returns:
            Tuple of:
            1. cell_images_by_sheet:
               sheet_name -> (row_1_based, col_letter) -> (ref_path, alt_text)
            2. doc_images:
               ref_path -> base64_encoded_str
        """
        if not self.content or not zipfile.is_zipfile(io.BytesIO(self.content)):
            return {}, {}

        try:
            with zipfile.ZipFile(io.BytesIO(self.content), "r") as zf:
                self._validate_zip(zf)
                sheet_xml_map = self._load_sheet_mapping(zf)
                if not sheet_xml_map:
                    return {}, {}

                shared_strings = self._load_shared_strings(zf)

                # 1. Extract Office 365 richData
                self._extract_office365_rich_data(zf, sheet_xml_map)

                # 2. Extract WPS DISPIMG embedded images
                self._extract_wps_dispimg(zf, sheet_xml_map, shared_strings)

                # 3. Extract standard floating drawings
                self._extract_floating_drawings(zf, sheet_xml_map)

        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.warning("Gracefully failed to extract XLSX images: %s", exc)
            return {}, {}

        return self.cell_images_by_sheet, self.doc_images

    def _validate_zip(self, zf: zipfile.ZipFile) -> None:
        validate_zip_archive(zf, self.max_uncompressed_bytes, self.max_compression_ratio)

    def _read_zip_member(self, zf: zipfile.ZipFile, member_path: str) -> bytes:
        return safe_read_zip_member(
            zf,
            member_path,
            max_member_bytes=self.max_uncompressed_bytes,
            max_total_bytes=self.max_uncompressed_bytes,
            max_ratio=self.max_compression_ratio,
        )

    def _load_sheet_mapping(self, zf: zipfile.ZipFile) -> dict[str, str]:
        """Map sheet name to its worksheet xml zip path."""
        wb_path = "xl/workbook.xml"
        wb_rels_path = "xl/_rels/workbook.xml.rels"
        names = set(zf.namelist())
        if wb_path not in names:
            return {}

        rel_map: dict[str, str] = {}
        if wb_rels_path in names:
            try:
                rels_root = ET.fromstring(self._read_zip_member(zf, wb_rels_path))
                for r in rels_root:
                    r_id = r.attrib.get("Id") or r.attrib.get("id")
                    target = r.attrib.get("Target") or r.attrib.get("target")
                    if r_id and target:
                        rel_map[r_id] = _resolve_target_path("xl", target)
            except ArchiveLimitError:
                raise
            except Exception as exc:
                logger.debug("Failed to parse workbook rels: %s", exc)

        sheet_map: dict[str, str] = {}
        try:
            wb_root = ET.fromstring(self._read_zip_member(zf, wb_path))
            sheet_index = 0
            for elem in wb_root.iter():
                if _local_tag(elem) == "sheet":
                    sheet_index += 1
                    name = elem.attrib.get("name")
                    r_id = _get_attribute(elem, "id") or elem.attrib.get("id")
                    sheet_path = rel_map.get(r_id) if r_id else None
                    if not sheet_path:
                        candidate = f"xl/worksheets/sheet{sheet_index}.xml"
                        if candidate in names:
                            sheet_path = candidate
                    if name and sheet_path:
                        sheet_map[name] = sheet_path
        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.debug("Failed to parse workbook xml: %s", exc)

        return sheet_map

    def _load_shared_strings(self, zf: zipfile.ZipFile) -> list[str]:
        """Load strings from xl/sharedStrings.xml."""
        sst_path = "xl/sharedStrings.xml"
        if sst_path not in zf.namelist():
            return []
        strings: list[str] = []
        try:
            root = ET.fromstring(self._read_zip_member(zf, sst_path))
            for elem in root:
                if _local_tag(elem) == "si":
                    strings.append(_get_element_text(elem))
        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.debug("Failed to parse shared strings: %s", exc)
        return strings

    def _extract_media(self, zf: zipfile.ZipFile, target_path: str) -> str | None:
        """Read media file from zip, deduplicate by content hash, and return ref_path."""
        clean_path = target_path.lstrip("/")
        if clean_path in self.path_to_ref:
            return self.path_to_ref[clean_path]

        data: bytes | None = None
        try:
            data = self._read_zip_member(zf, clean_path)
        except ArchiveLimitError:
            raise
        except Exception:
            alt_paths = []
            if not clean_path.startswith("xl/"):
                alt_paths.append("xl/" + clean_path)
            fname = posixpath.basename(clean_path)
            alt_paths.append(f"xl/media/{fname}")
            alt_paths.append(f"media/{fname}")
            for ap in alt_paths:
                try:
                    data = self._read_zip_member(zf, ap)
                    clean_path = ap
                    break
                except ArchiveLimitError:
                    raise
                except Exception:
                    continue
        if data is None:
            return None

        if len(data) > self.max_uncompressed_bytes:
            return None

        content_hash = hashlib.sha256(data).hexdigest()
        if content_hash in self.hash_to_ref:
            ref = self.hash_to_ref[content_hash]
            self.path_to_ref[clean_path] = ref
            return ref

        ext = posixpath.splitext(clean_path)[1].lower()
        # Rasterize supported TIFF images if needed (unsafe vector formats disabled)
        if ext in (".tif", ".tiff"):
            try:
                from docreader.parser.pptx_media import rasterize_media_bytes

                rasterized = rasterize_media_bytes(posixpath.basename(clean_path), data)
                if rasterized:
                    data = rasterized
                    ext = ".png"
            except Exception:
                pass

        if not ext:
            ext = ".png"

        ref_path = f"images/{uuid.uuid4().hex}{ext}"
        self.doc_images[ref_path] = base64.b64encode(data).decode("utf-8")
        self.extracted_bytes[ref_path] = data
        self.hash_to_ref[content_hash] = ref_path
        self.path_to_ref[clean_path] = ref_path
        return ref_path

    def _extract_office365_rich_data(
        self, zf: zipfile.ZipFile, sheet_xml_map: dict[str, str]
    ) -> None:
        """Parse Office 365 richData structures and associate images with cells."""
        meta_path = "xl/metadata.xml"
        rd_rv_path = "xl/richData/rdrichvalue.xml"
        rd_rel_path = "xl/richData/richValueRel.xml"
        rd_rels_path = "xl/richData/_rels/richValueRel.xml.rels"

        names = set(zf.namelist())
        if not (meta_path in names and rd_rv_path in names and rd_rel_path in names):
            return

        # 1. Parse richValueRel.xml.rels (rId -> target)
        rel_to_target: dict[str, str] = {}
        if rd_rels_path in names:
            try:
                root = ET.fromstring(self._read_zip_member(zf, rd_rels_path))
                for r in root:
                    r_id = r.attrib.get("Id") or r.attrib.get("id")
                    tgt = r.attrib.get("Target") or r.attrib.get("target")
                    if r_id and tgt:
                        rel_to_target[r_id] = _resolve_target_path("xl/richData", tgt)
            except ArchiveLimitError:
                raise
            except Exception as exc:
                logger.debug("Failed to parse richValueRel rels: %s", exc)

        # 2. Parse richValueRel.xml (0-based rel_idx -> rId)
        rel_id_list: list[str] = []
        try:
            root = ET.fromstring(self._read_zip_member(zf, rd_rel_path))
            for elem in root:
                r_id = _get_attribute(elem, "id") or elem.attrib.get("id") or elem.attrib.get("Id")
                if r_id:
                    rel_id_list.append(r_id)
        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.debug("Failed to parse richValueRel.xml: %s", exc)
            return

        # 3. Parse rdrichvalue.xml (0-based rv_idx -> (target_path, alt_text))
        rv_items: list[tuple[str, str]] = []
        try:
            root = ET.fromstring(self._read_zip_member(zf, rd_rv_path))
            for elem in root.iter():
                if _local_tag(elem) == "rv":
                    v_nodes = [v.text for v in elem if _local_tag(v) == "v"]
                    if v_nodes and v_nodes[0] is not None and v_nodes[0].isdigit():
                        rel_idx = int(v_nodes[0])
                        alt_text = v_nodes[2] if len(v_nodes) > 2 and v_nodes[2] else ""
                        if 0 <= rel_idx < len(rel_id_list):
                            r_id = rel_id_list[rel_idx]
                            target = rel_to_target.get(r_id, "")
                            rv_items.append((target, alt_text))
                        else:
                            rv_items.append(("", ""))
        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.debug("Failed to parse rdrichvalue.xml: %s", exc)
            return

        # 4. Parse metadata.xml (valueMetadata 0-based bk index -> rv_idx)
        vm_indices: list[int | None] = []
        try:
            root = ET.fromstring(self._read_zip_member(zf, meta_path))
            for child in root:
                if _local_tag(child) == "valueMetadata":
                    for bk in child:
                        if _local_tag(bk) == "bk":
                            found_v = None
                            for rc in bk:
                                if _local_tag(rc) == "rc" and "v" in rc.attrib:
                                    try:
                                        found_v = int(rc.attrib["v"])
                                    except ValueError:
                                        found_v = None
                                    break
                            vm_indices.append(found_v)
                    break
        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.debug("Failed to parse metadata.xml: %s", exc)
            return

        # 5. Map worksheets cell @vm to images
        for sheet_name, sheet_path in sheet_xml_map.items():
            if sheet_path not in names:
                continue
            try:
                sheet_root = ET.fromstring(self._read_zip_member(zf, sheet_path))
                sheet_dict = self.cell_images_by_sheet.setdefault(sheet_name, {})
                for c in sheet_root.iter():
                    if _local_tag(c) == "c" and "vm" in c.attrib:
                        try:
                            vm_idx = int(c.attrib["vm"]) - 1
                        except ValueError:
                            continue
                        if 0 <= vm_idx < len(vm_indices) and vm_indices[vm_idx] is not None:
                            rv_idx = vm_indices[vm_idx]
                            if rv_idx is not None and 0 <= rv_idx < len(rv_items):
                                tgt, alt = rv_items[rv_idx]
                                if tgt:
                                    ref = self._extract_media(zf, tgt)
                                    cell_coord = c.attrib.get("r", "")
                                    m = _CELL_COORD_RE.match(cell_coord)
                                    if ref and m:
                                        col_letter = m.group(1).upper()
                                        row_number = int(m.group(2))
                                        sheet_dict[(row_number, col_letter)] = (
                                            ref,
                                            alt,
                                        )
            except ArchiveLimitError:
                raise
            except Exception as exc:
                logger.debug("Failed to parse sheet richData for %s: %s", sheet_name, exc)

    def _extract_wps_dispimg(
        self,
        zf: zipfile.ZipFile,
        sheet_xml_map: dict[str, str],
        shared_strings: list[str],
    ) -> None:
        """Parse WPS DISPIMG images from xl/cellimages.xml and match cell formulas."""
        ci_path = "xl/cellimages.xml"
        ci_rels_path = "xl/_rels/cellimages.xml.rels"
        names = set(zf.namelist())
        if ci_path not in names:
            return

        # 1. Parse cellimages.xml.rels (rId -> target)
        rel_to_target: dict[str, str] = {}
        if ci_rels_path in names:
            try:
                root = ET.fromstring(self._read_zip_member(zf, ci_rels_path))
                for r in root:
                    r_id = r.attrib.get("Id") or r.attrib.get("id")
                    tgt = r.attrib.get("Target") or r.attrib.get("target")
                    if r_id and tgt:
                        rel_to_target[r_id] = _resolve_target_path("xl", tgt)
            except ArchiveLimitError:
                raise
            except Exception as exc:
                logger.debug("Failed to parse cellimages rels: %s", exc)

        # 2. Parse cellimages.xml (dispimg_id -> (target, alt))
        dispimg_map: dict[str, tuple[str, str]] = {}
        try:
            root = ET.fromstring(self._read_zip_member(zf, ci_path))
            for elem in root.iter():
                if _local_tag(elem) == "cellImage":
                    name = None
                    alt = ""
                    embed_id = None
                    for sub in elem.iter():
                        tag = _local_tag(sub)
                        if tag == "cNvPr":
                            name = sub.attrib.get("name")
                            alt = sub.attrib.get("descr") or ""
                        elif tag == "blip":
                            embed_id = _get_attribute(sub, "embed")
                    if name and embed_id and embed_id in rel_to_target:
                        dispimg_map[name] = (rel_to_target[embed_id], alt)
        except ArchiveLimitError:
            raise
        except Exception as exc:
            logger.debug("Failed to parse cellimages.xml: %s", exc)
            return

        if not dispimg_map:
            return

        # 3. Match cells in each worksheet
        for sheet_name, sheet_path in sheet_xml_map.items():
            if sheet_path not in names:
                continue
            try:
                sheet_root = ET.fromstring(self._read_zip_member(zf, sheet_path))
                sheet_dict = self.cell_images_by_sheet.setdefault(sheet_name, {})
                for c in sheet_root.iter():
                    if _local_tag(c) != "c":
                        continue
                    cell_coord = c.attrib.get("r", "")
                    m_coord = _CELL_COORD_RE.match(cell_coord)
                    if not m_coord:
                        continue

                    # Search formula, value, inline text, or shared string
                    matched_id = None
                    for sub in c:
                        tag = _local_tag(sub)
                        if tag == "f" and sub.text:
                            m = _DISPIMG_RE.search(sub.text)
                            if m:
                                matched_id = m.group(1) or m.group(2)
                                break
                        elif tag == "v" and sub.text:
                            if c.attrib.get("t") == "s":
                                try:
                                    s_idx = int(sub.text)
                                    if 0 <= s_idx < len(shared_strings):
                                        m = _DISPIMG_RE.search(shared_strings[s_idx])
                                        if m:
                                            matched_id = m.group(1) or m.group(2)
                                            break
                                except ValueError:
                                    pass
                            else:
                                m = _DISPIMG_RE.search(sub.text)
                                if m:
                                    matched_id = m.group(1) or m.group(2)
                                    break
                        elif tag == "is":
                            txt = _get_element_text(sub)
                            m = _DISPIMG_RE.search(txt)
                            if m:
                                matched_id = m.group(1) or m.group(2)
                                break

                    if matched_id and matched_id in dispimg_map:
                        tgt, alt = dispimg_map[matched_id]
                        ref = self._extract_media(zf, tgt)
                        if ref:
                            col_letter = m_coord.group(1).upper()
                            row_number = int(m_coord.group(2))
                            sheet_dict[(row_number, col_letter)] = (ref, alt)
            except ArchiveLimitError:
                raise
            except Exception as exc:
                logger.debug("Failed to parse WPS DISPIMG for %s: %s", sheet_name, exc)

    def _extract_floating_drawings(
        self, zf: zipfile.ZipFile, sheet_xml_map: dict[str, str]
    ) -> None:
        """Parse standard floating drawings linked from sheet rels."""
        names = set(zf.namelist())
        for sheet_name, sheet_path in sheet_xml_map.items():
            sheet_dir = posixpath.dirname(sheet_path)
            sheet_file = posixpath.basename(sheet_path)
            sheet_rels_path = posixpath.join(sheet_dir, "_rels", f"{sheet_file}.rels")

            if sheet_rels_path not in names:
                continue

            drawing_paths: list[str] = []
            try:
                rels_root = ET.fromstring(self._read_zip_member(zf, sheet_rels_path))
                for r in rels_root:
                    if r.attrib.get("Type", "").endswith("/drawing"):
                        tgt = r.attrib.get("Target") or r.attrib.get("target")
                        if tgt:
                            drawing_paths.append(_resolve_target_path(sheet_dir, tgt))
            except ArchiveLimitError:
                raise
            except Exception as exc:
                logger.debug("Failed to parse sheet rels for %s: %s", sheet_name, exc)
                continue

            for dp in drawing_paths:
                if dp not in names:
                    continue

                dp_dir = posixpath.dirname(dp)
                dp_file = posixpath.basename(dp)
                dp_rels_path = posixpath.join(dp_dir, "_rels", f"{dp_file}.rels")

                drawing_rels: dict[str, str] = {}
                if dp_rels_path in names:
                    try:
                        d_rels_root = ET.fromstring(self._read_zip_member(zf, dp_rels_path))
                        for r in d_rels_root:
                            r_id = r.attrib.get("Id") or r.attrib.get("id")
                            tgt = r.attrib.get("Target") or r.attrib.get("target")
                            if r_id and tgt:
                                drawing_rels[r_id] = _resolve_target_path(dp_dir, tgt)
                    except ArchiveLimitError:
                        raise
                    except Exception as exc:
                        logger.debug(
                            "Failed to parse drawing rels %s: %s",
                            dp_rels_path,
                            exc,
                        )

                try:
                    dr_root = ET.fromstring(self._read_zip_member(zf, dp))
                    sheet_dict = self.cell_images_by_sheet.setdefault(sheet_name, {})
                    for anchor in dr_root:
                        anchor_tag = _local_tag(anchor)
                        if anchor_tag in ("twoCellAnchor", "oneCellAnchor"):
                            col_0 = None
                            row_0 = None
                            alt = ""
                            embed_id = None
                            for sub in anchor.iter():
                                sub_tag = _local_tag(sub)
                                if sub_tag == "from":
                                    for coord in sub:
                                        c_tag = _local_tag(coord)
                                        if c_tag == "col" and coord.text:
                                            with contextlib.suppress(ValueError):
                                                col_0 = int(coord.text)
                                        elif c_tag == "row" and coord.text:
                                            with contextlib.suppress(ValueError):
                                                row_0 = int(coord.text)
                                elif sub_tag == "cNvPr":
                                    alt = sub.attrib.get("descr") or sub.attrib.get("name") or ""
                                elif sub_tag == "blip":
                                    embed_id = _get_attribute(sub, "embed")

                            if (
                                col_0 is not None
                                and row_0 is not None
                                and embed_id
                                and embed_id in drawing_rels
                            ):
                                tgt = drawing_rels[embed_id]
                                ref = self._extract_media(zf, tgt)
                                if ref:
                                    col_letter = _column_index_to_letter(col_0 + 1)
                                    row_number = row_0 + 1
                                    # Do not overwrite explicit in-cell images if already populated
                                    if (row_number, col_letter) not in sheet_dict:
                                        sheet_dict[(row_number, col_letter)] = (
                                            ref,
                                            alt,
                                        )
                except ArchiveLimitError:
                    raise
                except Exception as exc:
                    logger.debug("Failed to parse drawing xml %s: %s", dp, exc)
