"""EPUB parser.

Parses EPUB files into markdown text and optional embedded images.
"""

import base64
from dataclasses import dataclass, field
from io import BytesIO
import logging
import os
import posixpath
from urllib.parse import unquote, urlsplit
import uuid
import zipfile
from typing import Dict, List, Optional

from bs4 import BeautifulSoup
from lxml import etree

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.mhtml_parser import MHTMLParser

from docreader.parser.archive import (
    ArchiveLimitError, MAX_COMPRESSION_RATIO, MAX_UNCOMPRESSED_ARCHIVE_BYTES,
    validate_zip_archive as _validate_zip_archive,
)

logger = logging.getLogger(__name__)

# OCF / OPF constants (EPUB 3.3 OCF section 4.2, OPF 2.0.1 / EPUB 3.3 Packages).
_CONTAINER_PATH = "META-INF/container.xml"
_OPF_MEDIA_TYPE = "application/oebps-package+xml"
_DC_NAMESPACE = "http://purl.org/dc/elements/1.1/"
_XHTML_MEDIA_TYPE = "application/xhtml+xml"
# Manifest items whose media type is not image/* (e.g. application/octet-stream)
# are still treated as images when the file name says so.
_IMAGE_EXTENSIONS = (".jpg", ".jpeg", ".png", ".gif", ".tif", ".tiff")


@dataclass(eq=False)
class _ManifestItem:
    """One OPF manifest ``<item>``.

    Equality is identity: two manifest entries pointing at the same file are
    still two items, exactly as they appear in the manifest.
    """

    id: str
    # Unquoted href relative to the OPF directory (what image aliases key on).
    name: str
    # Normalized archive member path, or None when the archive lacks the file
    # or the href points outside the archive (remote resource).
    zip_path: Optional[str]
    media_type: str
    properties: str = ""

    @property
    def is_document(self) -> bool:
        return self.media_type == _XHTML_MEDIA_TYPE

    @property
    def is_image(self) -> bool:
        if self.is_document:
            return False
        return self.media_type.startswith("image/") or self.name.lower().endswith(
            _IMAGE_EXTENSIONS
        )


@dataclass
class _EpubPackage:
    # DC local-name -> element text values, in document order.
    metadata: Dict[str, List[Optional[str]]] = field(default_factory=dict)
    # Manifest order (image alias precedence depends on it).
    manifest: List[_ManifestItem] = field(default_factory=list)
    # Spine itemref idrefs, in reading order.
    spine: List[str] = field(default_factory=list)


def _parse_xml(data: bytes) -> etree._Element:
    """Parse untrusted package XML without DTDs, entities or network access."""
    parser = etree.XMLParser(
        resolve_entities=False, no_network=True, load_dtd=False
    )
    return etree.fromstring(data, parser)


def _local_name(element: etree._Element) -> str:
    return etree.QName(element).localname


def _namespace(element: etree._Element) -> str:
    return etree.QName(element).namespace or ""


def _elements(root: etree._Element, local_name: str) -> List[etree._Element]:
    """All descendant elements with ``local_name``, in document order.

    Matching by local name accepts OPF 2, OPF 3 and namespace-less packages.
    """
    return [
        element
        for element in root.iter()
        if isinstance(element.tag, str) and _local_name(element) == local_name
    ]


def _first(root: etree._Element, local_name: str) -> Optional[etree._Element]:
    for element in root.iter():
        if isinstance(element.tag, str) and _local_name(element) == local_name:
            return element
    return None


def _normalize_archive_path(path: str) -> str:
    normalized = posixpath.normpath(path.replace("\\", "/"))
    return "" if normalized == "." else normalized.lstrip("/")


def _find_member(members: set, *paths: str) -> Optional[str]:
    for path in paths:
        if path in members:
            return path
    return None


def _rootfile_path(epub_zip: zipfile.ZipFile, members: set) -> str:
    container = _parse_xml(epub_zip.read(_CONTAINER_PATH))
    rootfiles = [
        rootfile
        for rootfile in _elements(container, "rootfile")
        if (rootfile.get("full-path") or "").strip()
    ]
    if not rootfiles:
        raise ValueError("container.xml lists no rootfile")
    # The first OPF rootfile is the default rendition.
    rootfile = next(
        (
            rootfile
            for rootfile in rootfiles
            if (rootfile.get("media-type") or "").strip().lower() == _OPF_MEDIA_TYPE
        ),
        rootfiles[0],
    )
    full_path = rootfile.get("full-path").strip()
    opf_path = _find_member(
        members,
        _normalize_archive_path(full_path),
        _normalize_archive_path(unquote(full_path)),
    )
    if opf_path is None:
        raise KeyError(f"OPF package {full_path!r} is not in the archive")
    return opf_path


def _read_package(epub_zip: zipfile.ZipFile) -> _EpubPackage:
    """Read the OPF package: container.xml -> first rootfile -> OPF.

    Raises on a broken container or OPF, which sends the caller to
    ``_parse_epub_fallback``.
    """
    members = set(epub_zip.namelist())
    opf_path = _rootfile_path(epub_zip, members)
    opf_dir = posixpath.dirname(opf_path)
    package = _parse_xml(epub_zip.read(opf_path))
    result = _EpubPackage()

    metadata = _first(package, "metadata")
    if metadata is not None:
        for element in metadata.iter():
            if not isinstance(element.tag, str) or element is metadata:
                continue
            # Dublin Core elements, or bare names in a namespace-less package.
            if _namespace(element) not in (_DC_NAMESPACE, ""):
                continue
            result.metadata.setdefault(_local_name(element), []).append(element.text)

    manifest = _first(package, "manifest")
    if manifest is not None:
        for element in _elements(manifest, "item"):
            href = (element.get("href") or "").strip()
            if not href:
                continue
            name = unquote(href)
            # A remote href (https://...) never matches an archive member, so
            # it ends up like a missing file: zip_path None.
            zip_path = _find_member(
                members,
                _normalize_archive_path(posixpath.join(opf_dir, name)),
                # Some packagers store the still-encoded href verbatim.
                _normalize_archive_path(posixpath.join(opf_dir, href)),
            )
            media_type = (element.get("media-type") or "").split(";", 1)[0]
            result.manifest.append(
                _ManifestItem(
                    id=element.get("id") or "",
                    name=name,
                    zip_path=zip_path,
                    media_type=media_type.strip().lower(),
                    properties=element.get("properties") or "",
                )
            )

    spine = _first(package, "spine")
    if spine is not None:
        result.spine = [
            itemref.get("idref") or "" for itemref in _elements(spine, "itemref")
        ]
    return result


class EPUBParser(BaseParser):
    """Parser for EPUB e-book files."""

    def __init__(self, *args, extract_images: bool = True, **kwargs):
        super().__init__(*args, **kwargs)
        self.extract_images = extract_images

    def parse_into_text(self, content: bytes) -> Document:
        logger.info(
            "Parsing EPUB file: %s, size: %d bytes", self.file_name, len(content)
        )
        try:
            with zipfile.ZipFile(BytesIO(content), "r") as epub_zip:
                _validate_zip_archive(epub_zip)
                package = _read_package(epub_zip)
                metadata = self._extract_metadata(package)
                markdown_content, images = self._extract_content(epub_zip, package)

            metadata["source_format"] = "epub"
            metadata["file_size"] = len(content)
            metadata["chapter_count"] = len(
                [part for part in markdown_content.split("\n## ") if part.strip()]
            )
            metadata["image_count"] = len(images)
            return Document(content=markdown_content, images=images, metadata=metadata)
        except ArchiveLimitError:
            raise
        except ValueError as e:
            if "uncompressed size" in str(e).lower() or "compression ratio" in str(e).lower():
                raise
            logger.warning(
                "EPUB package parsing failed: %s, trying ZIP fallback", str(e)
            )
            return self._parse_epub_fallback(content)
        except Exception as e:
            logger.warning(
                "EPUB package parsing failed: %s, trying ZIP fallback", str(e)
            )
            return self._parse_epub_fallback(content)

    def _parse_epub_fallback(self, content: bytes) -> Document:
        """Parse EPUB directly as a ZIP when its OPF package cannot be read."""
        import re
        import zipfile
        from io import BytesIO

        metadata = {"source_format": "epub", "file_size": len(content)}
        images: Dict[str, str] = {}
        image_aliases: Dict[str, str] = {}
        markdown_parts = []

        with zipfile.ZipFile(BytesIO(content), "r") as epub_zip:
            _validate_zip_archive(epub_zip)
            html_files = [
                f
                for f in epub_zip.namelist()
                if f.endswith((".html", ".xhtml", ".htm"))
            ]

            def chapter_num(filename: str) -> int:
                match = re.search(r"chapter(\d+)", filename, re.IGNORECASE)
                return int(match.group(1)) if match else 999999

            html_files.sort(key=chapter_num)

            if self.extract_images:
                img_exts = (".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg")
                for img_file in epub_zip.namelist():
                    if img_file.lower().endswith(img_exts):
                        try:
                            img_data = epub_zip.read(img_file)
                            ext = os.path.splitext(img_file)[1]
                            img_path = f"images/{uuid.uuid4().hex}{ext}"
                            images[img_path] = base64.b64encode(img_data).decode(
                                "utf-8"
                            )
                            self._add_image_aliases(image_aliases, img_file, img_path)
                        except Exception as e:
                            logger.warning(
                                "Failed to extract image %s: %s", img_file, e
                            )

            for html_file in html_files:
                try:
                    html_content = epub_zip.read(html_file).decode(
                        "utf-8", errors="ignore"
                    )
                    chapter_md = self._html_to_markdown(
                        html_content,
                        image_aliases=image_aliases,
                        base_path=posixpath.dirname(html_file),
                    )
                    base = os.path.basename(html_file)
                    title = base.replace(".html", "").replace(".xhtml", "")
                    title = re.sub(
                        r"chapter[_-]?", "Chapter ", title, flags=re.IGNORECASE
                    )
                    title = title.replace("_", " ").replace("-", " ").title()
                    if chapter_md.strip():
                        markdown_parts.append(f"## {title}\n\n{chapter_md}")
                except Exception as e:
                    logger.warning("Failed to process %s: %s", html_file, e)

        metadata["chapter_count"] = len(markdown_parts)
        metadata["image_count"] = len(images)
        return Document(
            content="\n\n".join(markdown_parts), images=images, metadata=metadata
        )

    def _extract_metadata(self, package: _EpubPackage) -> Dict[str, str]:
        metadata: Dict[str, str] = {}
        mapping = {
            "title": "title",
            "creator": "author",
            "publisher": "publisher",
            "language": "language",
            "description": "description",
            "date": "date",
            "identifier": "isbn",
        }
        for dc_key, out_key in mapping.items():
            values = package.metadata.get(dc_key)
            if values:
                if out_key == "author":
                    # An empty <dc:creator/> has no text; skip it instead of
                    # failing the whole book.
                    authors = [value for value in values if value is not None]
                    if authors:
                        metadata[out_key] = ", ".join(authors)
                else:
                    metadata[out_key] = values[0]
        return metadata

    def _extract_content(
        self, epub_zip: zipfile.ZipFile, package: _EpubPackage
    ) -> tuple[str, Dict[str, str]]:
        markdown_parts = []
        images: Dict[str, str] = {}
        image_aliases: Dict[str, str] = {}

        if self.extract_images:
            for item in package.manifest:
                if item.is_image:
                    img_data = self._read_item(epub_zip, item)
                    if img_data is None:
                        continue
                    ext = os.path.splitext(item.name)[1]
                    img_path = f"images/{uuid.uuid4().hex}{ext}"
                    images[img_path] = base64.b64encode(img_data).decode("utf-8")
                    self._add_image_aliases(image_aliases, item.name, img_path)

        for item in self._documents_in_reading_order(package):
            html_content = self._read_item(epub_zip, item)
            if html_content is None:
                continue
            markdown_parts.append(
                self._process_chapter(
                    item,
                    html_content,
                    toc_index=len(markdown_parts),
                    image_aliases=image_aliases,
                )
            )

        return "\n\n".join(part for part in markdown_parts if part.strip()), images

    @staticmethod
    def _read_item(epub_zip: zipfile.ZipFile, item: _ManifestItem) -> bytes | None:
        """Read a manifest item, or None when the archive does not contain it.

        A single missing manifest file must not discard the rest of the book.
        """
        if item.zip_path is None:
            logger.warning(
                "EPUB manifest item %s (%s) is not in the archive, skipping",
                item.id,
                item.name,
            )
            return None
        return epub_zip.read(item.zip_path)

    @staticmethod
    def _documents_in_reading_order(package: _EpubPackage) -> list:
        """Return the book's documents in spine order.

        The spine is the reading order. The manifest can list files in any
        order: Standard Ebooks puts the title page after the last chapter
        there. Documents the spine leaves out follow, in manifest order.
        """
        documents = [item for item in package.manifest if item.is_document]
        document_set = set(documents)
        items_by_id: Dict[str, _ManifestItem] = {}
        for item in package.manifest:
            items_by_id.setdefault(item.id, item)
        ordered = []
        seen = set()
        for idref in package.spine:
            item = items_by_id.get(idref)
            if item in document_set and item not in seen:
                ordered.append(item)
                seen.add(item)
        ordered.extend(item for item in documents if item not in seen)
        return ordered

    def _process_chapter(
        self,
        html_item: _ManifestItem,
        html_content: bytes,
        toc_index: int = 0,
        image_aliases: Dict[str, str] | None = None,
    ) -> str:
        try:
            soup = BeautifulSoup(html_content, "lxml")
            title_tag = soup.find(["h1", "h2"])
            if title_tag:
                chapter_title = title_tag.get_text().strip()
                title_tag.decompose()
            else:
                chapter_title = html_item.name.replace("/", "")
                chapter_title = chapter_title.replace(".xhtml", "")
                chapter_title = chapter_title.replace("-", " ").title()
            body_html = str(soup.body) if soup.body else str(html_content)
            chapter_md = self._html_to_markdown(
                body_html,
                image_aliases=image_aliases,
                base_path=posixpath.dirname(html_item.name),
            )
            return f"## {chapter_title}\n\n{chapter_md}"
        except Exception as e:
            logger.error(
                "Failed to process chapter %s: %s", html_item.name, e
            )
            return f"## Chapter {toc_index + 1}\n\n[Error processing chapter: {e}]"

    def _html_to_markdown(
        self,
        html_content: str,
        image_aliases: Dict[str, str] | None = None,
        base_path: str = "",
    ) -> str:
        try:
            from bs4 import Comment
            from markdownify import markdownify as md

            soup = BeautifulSoup(html_content, "lxml")
            for element in soup(["script", "style"]):
                element.decompose()
            for comment in soup.find_all(
                string=lambda text: isinstance(text, Comment)
            ):
                comment.extract()
            self._strip_internal_links(soup)
            if image_aliases:
                self._rewrite_image_sources(soup, image_aliases, base_path)
            markdown_text = md(str(soup), heading_style="ATX")
            # Keep the blank lines and indentation that delimit Markdown blocks
            # (paragraphs, quotes, lists, tables, fenced code) and only collapse
            # runs of blank lines, as the MHTML and static HTML parsers do.
            return MHTMLParser._normalize_markdown(markdown_text)
        except ImportError:
            logger.warning("markdownify not available, using HTML as-is")
            return f"```html\n{html_content}\n```"
        except Exception as e:
            logger.error("HTML to Markdown conversion failed: %s", e)
            return f"```html\n{html_content}\n```"

    @staticmethod
    def _strip_internal_links(soup: BeautifulSoup) -> None:
        """Unwrap links that don't point to an external resource.

        EPUB internal links (other chapter files, ``#fragment`` anchors, TOC
        entries) become dead links after extraction. Keep only external links
        and replace everything else with its text.
        """
        external = ("http://", "https://", "mailto:", "tel:")
        for link in soup.find_all("a"):
            href = (link.get("href") or "").strip().lower()
            if not href or not href.startswith(external):
                link.unwrap()

    @staticmethod
    def _add_image_aliases(
        image_aliases: Dict[str, str],
        original_path: str,
        image_path: str,
    ) -> None:
        normalized = EPUBParser._normalize_epub_path(original_path)
        aliases = {
            original_path,
            normalized,
        }
        for alias in aliases:
            if alias:
                image_aliases[alias] = image_path
        # A basename fallback must not overwrite a real archive-root path.
        image_aliases.setdefault(posixpath.basename(normalized), image_path)

    @staticmethod
    def _rewrite_image_sources(
        soup: BeautifulSoup,
        image_aliases: Dict[str, str],
        base_path: str = "",
    ) -> None:
        for img in soup.find_all("img"):
            src = (img.get("src") or "").strip()
            if not src:
                continue
            # Only archive-local references may use the basename fallback.
            # A remote image can share its filename with an unrelated ZIP entry.
            try:
                reference = urlsplit(src)
            except ValueError:
                continue
            if reference.scheme or reference.netloc:
                continue
            # Only src is a URI reference. Strip its URI suffixes before decoding
            # once; archive paths from the OPF manifest and ZIP already contain
            # literal characters, including #, ? and percent-encoded-looking names.
            raw_path = src.split("#", 1)[0].split("?", 1)[0].replace("\\", "/")
            src_path = unquote(raw_path).replace("\\", "/")
            normalized_src = EPUBParser._normalize_epub_path(src_path)
            # Some EPUBs reference literal percent-looking names verbatim.
            # Try the undecoded path after the decoded path at each location.
            raw_normalized = EPUBParser._normalize_epub_path(raw_path)
            candidates = [normalized_src, raw_normalized]
            if base_path:
                joined = EPUBParser._normalize_epub_path(
                    posixpath.join(base_path, src_path)
                )
                raw_joined = EPUBParser._normalize_epub_path(
                    posixpath.join(base_path, raw_path)
                )
                # Resolve relative to the chapter before trying ambiguous aliases.
                candidates[:0] = [joined, raw_joined]
            candidates.append(posixpath.basename(normalized_src))
            for candidate in candidates:
                if candidate in image_aliases:
                    img["src"] = image_aliases[candidate]
                    break

    @staticmethod
    def _normalize_epub_path(path: str) -> str:
        """Normalize a decoded archive path without interpreting it as a URI."""
        normalized = posixpath.normpath(path.replace("\\", "/"))
        return "" if normalized == "." else normalized.lstrip("/")
