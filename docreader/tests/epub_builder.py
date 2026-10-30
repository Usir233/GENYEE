"""Helpers that write small EPUB 3 test fixtures with only the standard library.

The layout mirrors what common EPUB writers produce: ``mimetype`` stored
uncompressed as the first entry, ``META-INF/container.xml`` pointing at
``EPUB/content.opf``, every manifest item under ``EPUB/``, an EPUB 3
navigation document (``nav.xhtml``) and an EPUB 2 ``toc.ncx``.
"""

from dataclasses import dataclass
from datetime import datetime, timezone
from html import escape
from io import BytesIO
from typing import Iterable, Sequence, Union
import zipfile

from lxml import etree
import lxml.html

NAV = "nav"
NCX = "ncx"

_XHTML = "http://www.w3.org/1999/xhtml"
_OPS = "http://www.idpf.org/2007/ops"
_XML = "http://www.w3.org/XML/1998/namespace"
_XHTML_TYPE = "application/xhtml+xml"


@dataclass
class Chapter:
    """An XHTML content document. ``content`` is HTML; its body is kept."""

    file_name: str
    title: str
    content: str
    uid: str | None = None
    lang: str = "en"
    properties: Sequence[str] = ()


@dataclass
class Item:
    """Any other manifest resource (image, SVG, CSS...)."""

    uid: str
    file_name: str
    media_type: str
    content: bytes


@dataclass
class TocLink:
    href: str
    title: str
    uid: str


Entry = Union[Chapter, Item, str]


def _serialize(root: etree._Element) -> bytes:
    return etree.tostring(
        root,
        pretty_print=True,
        xml_declaration=True,
        encoding="utf-8",
        doctype="<!DOCTYPE html>" if etree.QName(root).localname == "html" else None,
    )


def _xhtml_root(title: str, lang: str, *, epub_prefix: bool) -> etree._Element:
    root = etree.Element(f"{{{_XHTML}}}html", nsmap={None: _XHTML, "epub": _OPS})
    if epub_prefix:
        root.set(
            f"{{{_OPS}}}prefix",
            "z3998: http://www.daisy.org/z3998/2012/vocab/structure/#",
        )
    root.set("lang", lang)
    root.set(f"{{{_XML}}}lang", lang)
    head = etree.SubElement(root, f"{{{_XHTML}}}head")
    etree.SubElement(head, f"{{{_XHTML}}}title").text = title
    return root


def _chapter_document(chapter: Chapter) -> bytes:
    """Wrap the chapter body in a well-formed XHTML document."""
    body = lxml.html.document_fromstring(chapter.content).body
    inner = escape(body.text or "", quote=False) + "".join(
        etree.tostring(child, method="xml", encoding="unicode") for child in body
    )
    xhtml_body = etree.fromstring(f'<body xmlns="{_XHTML}">{inner}</body>')
    root = _xhtml_root(chapter.title, chapter.lang, epub_prefix=True)
    root.append(xhtml_body)
    return _serialize(root)


def _nav_document(title: str, language: str, toc: Sequence[TocLink]) -> bytes:
    root = _xhtml_root(title, language, epub_prefix=False)
    body = etree.SubElement(root, f"{{{_XHTML}}}body")
    nav = etree.SubElement(
        body,
        f"{{{_XHTML}}}nav",
        {f"{{{_OPS}}}type": "toc", "id": "id", "role": "doc-toc"},
    )
    etree.SubElement(nav, f"{{{_XHTML}}}h2").text = title
    ol = etree.SubElement(nav, f"{{{_XHTML}}}ol")
    for link in toc:
        li = etree.SubElement(ol, f"{{{_XHTML}}}li")
        etree.SubElement(li, f"{{{_XHTML}}}a", href=link.href).text = link.title
    return _serialize(root)


def _ncx_document(identifier: str, title: str, toc: Sequence[TocLink]) -> bytes:
    ns = "http://www.daisy.org/z3986/2005/ncx/"
    root = etree.Element(f"{{{ns}}}ncx", nsmap={None: ns}, version="2005-1")
    head = etree.SubElement(root, f"{{{ns}}}head")
    for name, content in (
        ("dtb:uid", identifier),
        ("dtb:depth", "0"),
        ("dtb:totalPageCount", "0"),
        ("dtb:maxPageNumber", "0"),
    ):
        etree.SubElement(head, f"{{{ns}}}meta", content=content, name=name)
    doc_title = etree.SubElement(root, f"{{{ns}}}docTitle")
    etree.SubElement(doc_title, f"{{{ns}}}text").text = title
    nav_map = etree.SubElement(root, f"{{{ns}}}navMap")
    for link in toc:
        point = etree.SubElement(nav_map, f"{{{ns}}}navPoint", id=link.uid)
        label = etree.SubElement(point, f"{{{ns}}}navLabel")
        etree.SubElement(label, f"{{{ns}}}text").text = link.title
        etree.SubElement(point, f"{{{ns}}}content", src=link.href)
    return _serialize(root)


def _package_document(
    identifier: str,
    title: str,
    language: str,
    authors: Iterable[str],
    manifest: Sequence[tuple[str, str, str, str]],
    spine: Sequence[str],
) -> bytes:
    opf = "http://www.idpf.org/2007/opf"
    dc = "http://purl.org/dc/elements/1.1/"
    root = etree.Element(
        f"{{{opf}}}package",
        {
            "unique-identifier": "id",
            "version": "3.0",
            "prefix": "rendition: http://www.idpf.org/vocab/rendition/#",
        },
        nsmap={None: opf},
    )
    metadata = etree.SubElement(root, f"{{{opf}}}metadata", nsmap={"dc": dc})
    modified = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    etree.SubElement(
        metadata, f"{{{opf}}}meta", property="dcterms:modified"
    ).text = modified
    etree.SubElement(metadata, f"{{{dc}}}identifier", id="id").text = identifier
    etree.SubElement(metadata, f"{{{dc}}}title").text = title
    etree.SubElement(metadata, f"{{{dc}}}language").text = language
    for index, author in enumerate(authors):
        creator_id = "creator" if index == 0 else f"creator_{index}"
        etree.SubElement(metadata, f"{{{dc}}}creator", id=creator_id).text = author
    manifest_element = etree.SubElement(root, f"{{{opf}}}manifest")
    for uid, href, media_type, properties in manifest:
        attributes = {"href": href, "id": uid, "media-type": media_type}
        if properties:
            attributes["properties"] = properties
        etree.SubElement(manifest_element, f"{{{opf}}}item", attributes)
    spine_element = etree.SubElement(root, f"{{{opf}}}spine", toc=NCX)
    for idref in spine:
        etree.SubElement(spine_element, f"{{{opf}}}itemref", idref=idref)
    return _serialize(root)


def build_epub(
    *,
    identifier: str,
    title: str,
    items: Sequence[Entry],
    spine: Sequence[Entry],
    language: str = "en",
    authors: Iterable[str] = (),
    toc: Sequence[Union[Chapter, TocLink]] = (),
) -> bytes:
    """Write an EPUB 3 archive and return its bytes.

    ``items`` is the manifest order. ``NCX`` and ``NAV`` may appear in it to
    place the navigation files; otherwise they are appended. ``spine`` holds
    chapters, items or manifest ids such as ``NAV``.
    """
    entries = list(items)
    for sentinel in (NCX, NAV):
        if sentinel not in entries:
            entries.append(sentinel)

    chapter_ids = {}
    chapter_index = 0
    for entry in entries:
        if isinstance(entry, Chapter):
            if entry.uid:
                chapter_ids[id(entry)] = entry.uid
            else:
                chapter_ids[id(entry)] = f"chapter_{chapter_index}"
                chapter_index += 1

    def entry_id(entry: Entry) -> str:
        if isinstance(entry, str):
            return entry
        if isinstance(entry, Chapter):
            return chapter_ids[id(entry)]
        return entry.uid

    toc_links = [
        link
        if isinstance(link, TocLink)
        else TocLink(link.file_name, link.title, entry_id(link))
        for link in toc
    ]

    manifest = []
    files = []
    for entry in entries:
        if entry == NCX:
            manifest.append((NCX, "toc.ncx", "application/x-dtbncx+xml", ""))
            files.append(("toc.ncx", _ncx_document(identifier, title, toc_links)))
        elif entry == NAV:
            manifest.append((NAV, "nav.xhtml", _XHTML_TYPE, "nav"))
            files.append(("nav.xhtml", _nav_document(title, language, toc_links)))
        elif isinstance(entry, Chapter):
            manifest.append(
                (
                    entry_id(entry),
                    entry.file_name,
                    _XHTML_TYPE,
                    " ".join(entry.properties),
                )
            )
            files.append((entry.file_name, _chapter_document(entry)))
        else:
            manifest.append((entry.uid, entry.file_name, entry.media_type, ""))
            files.append((entry.file_name, entry.content))

    package = _package_document(
        identifier,
        title,
        language,
        authors,
        manifest,
        [entry_id(entry) for entry in spine],
    )
    container = (
        '<?xml version="1.0" encoding="utf-8"?>\n'
        '<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"'
        ' version="1.0">\n  <rootfiles>\n'
        '    <rootfile media-type="application/oebps-package+xml"'
        ' full-path="EPUB/content.opf"/>\n  </rootfiles>\n</container>\n'
    )

    output = BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr(
            zipfile.ZipInfo("mimetype"), "application/epub+zip", zipfile.ZIP_STORED
        )
        archive.writestr("META-INF/container.xml", container)
        archive.writestr("EPUB/content.opf", package)
        for file_name, data in files:
            archive.writestr(f"EPUB/{file_name}", data)
    return output.getvalue()
