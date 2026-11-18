"""
Markdown Parser Module

This module provides comprehensive Markdown parsing functionality including:
- Table formatting and standardization
- Base64 image extraction and conversion
- Image path replacement and URL generation
- Pipeline-based parsing with multiple stages

The parser uses a pipeline approach to process Markdown content through
multiple stages: table formatting -> image processing.
"""

import base64
import bisect
import io
import logging
import re
import uuid
import warnings
from collections.abc import Callable

from PIL import Image, UnidentifiedImageError

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.chain_parser import PipelineParser
from docreader.utils import endecode

# Get logger object
logger = logging.getLogger(__name__)

MAX_IMAGE_PIXELS = 40_000_000
Image.MAX_IMAGE_PIXELS = MAX_IMAGE_PIXELS

MAX_INLINE_IMAGE_BYTES = 20 * 1024 * 1024  # 20 MiB per decoded inline image
MAX_INLINE_TOTAL_IMAGE_BYTES = 100 * 1024 * 1024  # 100 MiB cumulative per document
MAX_INLINE_IMAGES_PER_DOCUMENT = 1000
MAX_INLINE_IMAGE_FRAMES = 256

_ALLOWED_INLINE_RASTER_SUBTYPES: dict[str, str] = {
    "png": "png",
    "x-png": "png",
    "jpeg": "jpeg",
    "jpg": "jpg",
    "pjpeg": "jpeg",
    "gif": "gif",
    "webp": "webp",
    "bmp": "bmp",
    "x-bmp": "bmp",
    "x-ms-bmp": "bmp",
    "tiff": "tiff",
    "tif": "tif",
}
_ALLOWED_PIL_FORMATS = frozenset({"PNG", "JPEG", "GIF", "WEBP", "BMP", "TIFF", "MPO"})
_B64_RUN = re.compile(r"[A-Za-z0-9+/= \t\r\n]+")
_WS_RUN = re.compile(r"[ \t\r\n]*")

_SEPARATOR_CELL = re.compile(r"^:?-{3,}:?$")
# A backslash escape, or a bare pipe. Scanning both keeps an escaped pipe
# (``\|``) out of the cell delimiters without a lookbehind, in linear time.
_ESCAPE_OR_PIPE = re.compile(r"\\.|\|", re.DOTALL)
# Cells of a GFM alignment row as matched by the previous ``align_pattern``.
# Looser than ``_SEPARATOR_CELL``: any non-empty run of ``:``/``-`` qualifies.
_ALIGNMENT_CELL = re.compile(r"^[:-]+$")
_CODE_FENCE = re.compile(r" {0,3}(`{3,}|~{3,})(.*)$")


def _transform_outside_fences(content: str, transform: Callable[[str], str]) -> str:
    """Transform prose while leaving fenced code and its line endings intact."""
    out: list[str] = []
    pending: list[str] = []
    fence: str | None = None

    def flush_pending() -> None:
        if pending:
            out.append(transform("\n".join(pending)))
            pending.clear()

    for line in content.split("\n"):
        marker = _CODE_FENCE.fullmatch(line.removesuffix("\r"))
        if fence is not None:
            out.append(line)
            if (
                marker
                and marker[1][0] == fence[0]
                and len(marker[1]) >= len(fence)
                and not marker[2].strip(" \t")
            ):
                fence = None
        elif marker and (marker[1][0] != "`" or "`" not in marker[2]):
            flush_pending()
            fence = marker[1]
            out.append(line)
        else:
            pending.append(line)
    flush_pending()
    return "\n".join(out)


def _inline_code_spans(content: str) -> list[tuple[int, int]]:
    """Find paired backtick runs without rescanning unmatched suffixes."""
    tokens = list(re.finditer(r"`+|\n[ \t\r]*\n", content))
    following: dict[int, int] = {}
    closers: dict[int, int] = {}
    for i in range(len(tokens) - 1, -1, -1):
        token = tokens[i]
        if token[0].startswith("\n"):
            following.clear()  # Inline code cannot cross a blank paragraph.
            continue
        start = token.start()
        while start > 0 and content[start - 1] == "\\":
            start -= 1
        # Outside code, an odd backslash run escapes the first backtick.
        length = len(token[0]) - (token.start() - start) % 2
        if length in following:
            closers[i] = following[length]
        following[len(token[0])] = i

    spans: list[tuple[int, int]] = []
    i = 0
    while i < len(tokens):
        closing = closers.get(i)
        if closing is None:
            i += 1
        else:
            spans.append((tokens[i].start(), tokens[closing].end()))
            i = closing + 1
    return spans


class MarkdownTableUtil:
    """Utility class for formatting Markdown tables.

    This class standardizes Markdown table formatting by:
    - Normalizing column alignment markers (e.g., :---, :---:, ---:)
    - Adding consistent spacing around pipes (|)
    - Preserving indentation levels
    - Handling both header rows and data rows

    Example:
        Input:  |姓名|年龄|城市|
                |:---|---:|:---:|
                |张三|25|北京|

        Output: | 姓名 | 年龄 | 城市 |
                | :--- | ---: | :---: |
                | 张三 | 25 | 北京 |
    """

    @staticmethod
    def _split_row_cells(row_line: str) -> list[str]:
        """Split a markdown table row into cells, preserving empty cells.

        As in GFM, a pipe escaped as ``\\|`` is text inside its cell rather
        than a cell delimiter.
        """
        inner = row_line.strip()
        if not inner.startswith("|"):
            return []
        parts: list[str] = []
        start = 0
        for match in _ESCAPE_OR_PIPE.finditer(inner):
            if match.group() == "|":
                parts.append(inner[start : match.start()])
                start = match.end()
        parts.append(inner[start:])
        if parts and parts[0].strip() == "":
            parts = parts[1:]
        if parts and parts[-1].strip() == "":
            parts = parts[:-1]
        return [part.strip() for part in parts]

    @staticmethod
    def _is_table_row(line: str) -> bool:
        stripped = line.strip()
        return stripped.startswith("|") and "|" in stripped[1:]

    @classmethod
    def _is_separator_row(cls, line: str) -> bool:
        cells = cls._split_row_cells(line)
        return bool(cells) and all(_SEPARATOR_CELL.match(cell) for cell in cells)

    @classmethod
    def _is_alignment_row(cls, line: str) -> bool:
        """Detect a GFM alignment row, matching the previous ``align_pattern``.

        Deliberately looser than :meth:`_is_separator_row` (which needs three
        or more dashes per cell) so formatting output is unchanged for rows
        like ``| - | - |``.
        """
        cells = cls._split_row_cells(line)
        return bool(cells) and all(_ALIGNMENT_CELL.match(cell) for cell in cells)

    @classmethod
    def _is_empty_row(cls, line: str) -> bool:
        cells = cls._split_row_cells(line)
        return bool(cells) and all(cell == "" for cell in cells)

    @classmethod
    def _separator_row_for(cls, header_line: str) -> str:
        cells = cls._split_row_cells(header_line)
        return "| " + " | ".join("---" for _ in cells) + " |"

    @classmethod
    def _normalize_table_block(cls, block: list[str]) -> list[str]:
        """Fix MarkItDown-style tables: drop bogus prefix rows, ensure GFM delimiter."""
        while block and cls._is_empty_row(block[0]):
            block.pop(0)
        if block and cls._is_separator_row(block[0]):
            block.pop(0)
        # GFM/marked need "| --- |" after the first row. Headerless Word tables
        # only have data rows after we strip the fake empty+separator prefix.
        if len(block) >= 2 and not cls._is_separator_row(block[1]):
            sep = cls._separator_row_for(block[0])
            block = [block[0], sep] + block[1:]
        return block

    def normalize_spurious_table_prefixes(self, content: str) -> str:
        """Remove bogus empty/separator prefix rows from MarkItDown table output."""
        lines = content.split("\n")
        out: list[str] = []
        i = 0
        while i < len(lines):
            line = lines[i]
            if not self._is_table_row(line):
                out.append(line)
                i += 1
                continue
            block: list[str] = []
            while i < len(lines) and self._is_table_row(lines[i]):
                block.append(lines[i])
                i += 1
            out.extend(self._normalize_table_block(block))
        return "\n".join(out)

    def format_table(self, content: str) -> str:
        """Normalize tables outside fenced code blocks, preserving code examples."""
        unified = content.replace("\r\n", "\n").replace("\r", "\n")
        return _transform_outside_fences(unified, self._format_table_block)

    def _format_table_block(self, content: str) -> str:
        """Format all Markdown tables in the content.

        Line-based and regex-free: only well-formed rows (leading *and*
        trailing ``|``, with at least one cell) are treated as tables, so a
        malformed row can never trigger the catastrophic backtracking
        described in https://github.com/Usir233/GENYEE/issues/2768.
        CRLF/CR input is normalized to LF before scanning.

        Args:
            content: Raw Markdown text containing tables

        Returns:
            Formatted Markdown text with standardized table formatting
        """

        def format_alignment_row(prefix: str, columns: list[str]) -> str:
            """Standardize an alignment row, preserving ``:`` markers."""
            processed = []
            for col in columns:
                # Preserve left alignment marker (:---)
                left_colon = ":" if col.startswith(":") else ""
                # Preserve right alignment marker (---:)
                right_colon = ":" if col.endswith(":") else ""
                processed.append(left_colon + "---" + right_colon)
            return prefix + "| " + " | ".join(processed) + " |"

        def format_data_row(prefix: str, columns: list[str]) -> str:
            """Standardize a header or data row."""
            return prefix + "| " + " | ".join(columns) + " |"

        # Unify CRLF/CR first so a trailing ``\r`` left by split("\n") cannot
        # be mistaken for "ends with |" via str.rstrip(), and so we do not
        # emit mixed line endings. Trailing newlines are preserved.
        unified = content.replace("\r\n", "\n").replace("\r", "\n")
        lines = unified.split("\n")
        out: list[str] = []
        for line in lines:
            stripped = line.lstrip(" \t")
            # A row must both begin and end with a pipe to be a table row;
            # anything else is passed through unchanged. This also keeps
            # unclosed rows (the old regex's exponential worst case) cheap.
            # rstrip only spaces/tabs (old ``\|[\t ]*$``), not all whitespace.
            if stripped.startswith("|") and line.rstrip(" \t").endswith("|"):
                prefix = line[: len(line) - len(stripped)]
                columns = self._split_row_cells(line)
                # Lone "|" is not a table row: the old line_pattern required
                # opening *and* closing pipes. Formatting it to "|  |" would
                # let normalize_spurious_table_prefixes drop the line.
                if not columns:
                    out.append(line)
                elif self._is_alignment_row(line):
                    out.append(format_alignment_row(prefix, columns))
                else:
                    out.append(format_data_row(prefix, columns))
            else:
                out.append(line)
        return self.normalize_spurious_table_prefixes("\n".join(out))

    @staticmethod
    def _self_test():
        test_content = """
# 测试表格
普通文本---不会被匹配

## 表格1（无前置空格）

| 姓名   | 年龄  | 城市          |
|      :---------- | -------: | :------      |
| 张三 | 25 | 北京 |

## 表格3（前置4个空格+首尾|）
    |   产品   |   价格   |   库存   |
    | :-------------: | ----------- | :-----------: |
    | 手机 | 5999       | 100 |
"""
        util = MarkdownTableUtil()
        format_content = util.format_table(test_content)
        print(format_content)


class MarkdownTableFormatter(BaseParser):
    """Parser for formatting Markdown tables.

    This parser standardizes the formatting of all Markdown tables in the
    document to ensure consistent spacing and alignment markers.

    Example:
        >>> formatter = MarkdownTableFormatter()
        >>> content = b"|Name|Age|\n|---|---|\n|John|30|"
        >>> doc = formatter.parse_into_text(content)
        >>> print(doc.content)
        | Name | Age |
        | --- | --- |
        | John | 30 |
    """

    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.table_helper = MarkdownTableUtil()

    def parse_into_text(self, content: bytes) -> Document:
        """Parse and format Markdown tables.

        Args:
            content: Raw Markdown content as bytes

        Returns:
            Document with formatted table content
        """
        # Decode bytes to string with automatic encoding detection
        text = endecode.decode_bytes(content)
        # Format all tables in the content
        text = self.table_helper.format_table(text)
        return Document(content=text)


def _validate_raster_image_bytes(data: bytes) -> bool:
    """Verify decoded bytes represent a safe raster image within pixel/frame bounds."""
    Image.MAX_IMAGE_PIXELS = MAX_IMAGE_PIXELS
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("error", Image.DecompressionBombWarning)
            with Image.open(io.BytesIO(data)) as img:
                fmt = (img.format or "").upper()
                if fmt not in _ALLOWED_PIL_FORMATS:
                    return False
                width, height = img.size
                if width <= 0 or height <= 0 or width * height > MAX_IMAGE_PIXELS:
                    return False
                n_frames = getattr(img, "n_frames", 1) or 1
                if n_frames > MAX_INLINE_IMAGE_FRAMES:
                    return False
                if width * height * n_frames > MAX_IMAGE_PIXELS * 2:
                    return False
    except (
        UnidentifiedImageError,
        Image.DecompressionBombError,
        Image.DecompressionBombWarning,
        OSError,
        ValueError,
        SyntaxError,
        EOFError,
    ):
        return False
    return True


def _parse_b64_title(prose: str, pos: int) -> tuple[tuple[str, int] | None, bool]:
    """Parse an optional Markdown image title token at ``pos``.

    Returns ``((title_str, next_index), hit_eof)``.
    """
    n = len(prose)
    quote = prose[pos]
    if quote in ('"', "'"):
        i = pos + 1
        while i < n:
            ch = prose[i]
            if ch == "\\":
                i += 2
                if i > n:
                    return None, True
                continue
            if ch == quote:
                return (prose[pos : i + 1], i + 1), False
            i += 1
        return None, True
    if quote == "(":
        i = pos + 1
        while i < n:
            ch = prose[i]
            if ch == "\\":
                i += 2
                if i > n:
                    return None, True
                continue
            if ch == "(":
                return None, False
            if ch == ")":
                return (prose[pos : i + 1], i + 1), False
            i += 1
        return None, True
    return None, False


def _sub_markdown_images(
    content: str,
    transform_fn: Callable[[str, str, str], str],
) -> str:
    """Linear-time O(N) replacement for standard ``![alt](target)`` references."""
    n = len(content)
    pos = 0
    last_emit = 0
    out_parts: list[str] = []

    while pos < n:
        img_start = content.find("![", pos)
        if img_start < 0:
            break
        mid = content.find("](", img_start + 2)
        if mid < 0:
            break
        next_open = content.find("![", img_start + 2, mid)
        while next_open >= 0:
            img_start = next_open
            next_open = content.find("![", img_start + 2, mid)

        r = mid + 2
        while r < n:
            ch = content[r]
            if ch == ")" or ch in "\r\n" or (ch == "!" and r + 1 < n and content[r + 1] == "["):
                break
            r += 1
        if r == n:
            break
        if content[r] != ")":
            pos = r if r > mid + 2 else mid + 2
            continue
        if r == mid + 2:
            pos = r + 1
            continue

        alt = content[img_start + 2 : mid]
        target = content[mid + 2 : r]
        full = content[img_start : r + 1]
        out_parts.append(content[last_emit:img_start])
        out_parts.append(transform_fn(full, alt, target))
        last_emit = r + 1
        pos = r + 1

    if not out_parts:
        return content
    out_parts.append(content[last_emit:])
    return "".join(out_parts)


class MarkdownImageUtil:
    """Utility class for handling images in Markdown.

    This class provides functionality to:
    - Extract base64-encoded images from Markdown
    - Extract image paths from Markdown
    - Replace image paths with new URLs
    - Convert base64 images to binary format

    Supported formats:
    - Base64 embedded images: ![alt](data:image/png;base64,iVBORw0...)
    - Regular image links: ![alt](path/to/image.png)
    """

    def __init__(self):
        # Retained for attribute compatibility; scanning uses linear O(N) parsers
        # to eliminate O(N^3) backtracking on unclosed Markdown image syntax.
        self.b64_pattern = re.compile(
            r"!\[([^\]\r\n]*)\]\(data:image/([^;)\r\n]+);base64,([^)]+)\)"
        )
        self.image_pattern = re.compile(r"!\[([^\]\r\n]*)\]\(([^)\r\n]+)\)")
        self.replace_pattern = self.image_pattern

    def extract_image(
        self,
        content: str,
        path_prefix: str | None = None,
        replace: bool = True,
    ) -> tuple[str, list[str]]:
        """Extract image paths from Markdown content."""
        images: list[str] = []

        def repl(full_match: str, title: str, image_path: str) -> str:
            if path_prefix:
                image_path = f"{path_prefix}/{image_path}"
            images.append(image_path)
            if not replace:
                return full_match
            return f"![{title}]({image_path})"

        text = _sub_markdown_images(content, repl)
        logger.debug(f"Extracted {len(images)} images from markdown")
        return text, images

    def extract_base64(
        self,
        content: str,
        path_prefix: str | None = None,
        replace: bool = True,
    ) -> tuple[str, dict[str, bytes]]:
        """Extract and decode base64 images outside fenced and inline code."""
        images: dict[str, bytes] = {}
        total_decoded_bytes = 0
        max_b64_chars = ((MAX_INLINE_IMAGE_BYTES + 2) // 3) * 4 + 65536

        def _rejected(full_match: str, title: str) -> str:
            return title if replace else full_match

        def handle_candidate(
            prose: str,
            full_start: int,
            full_end: int,
            title: str,
            img_ext: str,
            payload_start: int,
            payload_end: int,
            image_title: str | None,
        ) -> str:
            nonlocal total_decoded_bytes
            full_match = prose[full_start:full_end]

            normalized_subtype = img_ext.strip().lower()
            safe_ext = _ALLOWED_INLINE_RASTER_SUBTYPES.get(normalized_subtype)
            if not safe_ext:
                logger.warning(
                    "Skipping disallowed or non-raster inline image MIME subtype: image/%s",
                    img_ext[:64],
                )
                return _rejected(full_match, title)

            payload_len = payload_end - payload_start
            if payload_len > max_b64_chars:
                preview = prose[payload_start : min(payload_end, payload_start + 100)] + "..."
                logger.warning(
                    "Skipping oversized inline base64 image before decode (len=%d > max=%d): %s",
                    payload_len,
                    max_b64_chars,
                    preview,
                )
                return _rejected(full_match, title)

            img_b64 = prose[payload_start:payload_end]
            image_byte = endecode.encode_image(img_b64, errors="ignore")
            if not image_byte:
                preview = img_b64[:100] + ("..." if len(img_b64) > 100 else "")
                logger.error(
                    f"Failed to decode base64 image (len={len(img_b64)}) skip it: {preview}"
                )
                return _rejected(full_match, title)

            if len(image_byte) > MAX_INLINE_IMAGE_BYTES:
                logger.warning(
                    "Skipping inline image exceeding byte cap (%d > %d)",
                    len(image_byte),
                    MAX_INLINE_IMAGE_BYTES,
                )
                return _rejected(full_match, title)

            if (
                len(images) >= MAX_INLINE_IMAGES_PER_DOCUMENT
                or total_decoded_bytes + len(image_byte) > MAX_INLINE_TOTAL_IMAGE_BYTES
            ):
                logger.warning(
                    "Skipping inline image exceeding per-document budget (count=%d, bytes=%d)",
                    len(images),
                    total_decoded_bytes + len(image_byte),
                )
                return _rejected(full_match, title)

            if not _validate_raster_image_bytes(image_byte):
                logger.warning(
                    "Skipping invalid, spoofed, or decompression-bomb inline image "
                    "(declared=image/%s, bytes=%d)",
                    safe_ext,
                    len(image_byte),
                )
                return _rejected(full_match, title)

            image_path = f"{uuid.uuid4()}.{safe_ext}"
            if path_prefix:
                image_path = f"{path_prefix}/{image_path}"
            images[image_path] = image_byte
            total_decoded_bytes += len(image_byte)

            if not replace:
                return full_match

            suffix = f" {image_title}" if image_title else ""
            return f"![{title}]({image_path}{suffix})"

        def extract_prose(prose: str) -> str:
            spans = _inline_code_spans(prose)
            span_starts = [s[0] for s in spans]

            def code_span_at(offset: int) -> tuple[int, int] | None:
                idx = bisect.bisect_right(span_starts, offset) - 1
                if idx >= 0 and spans[idx][0] <= offset < spans[idx][1]:
                    return spans[idx]
                return None

            n = len(prose)
            pos = 0
            last_emit = 0
            out_parts: list[str] = []

            while pos < n:
                img_start = prose.find("![", pos)
                if img_start < 0:
                    break
                sp = code_span_at(img_start)
                if sp is not None:
                    pos = sp[1]
                    continue

                search_from = img_start + 2
                while True:
                    mid = prose.find("](", search_from)
                    if mid < 0:
                        break
                    mid_sp = code_span_at(mid)
                    if mid_sp is None:
                        break
                    search_from = mid_sp[1]
                if mid < 0:
                    break

                next_open = prose.find("![", img_start + 2, mid)
                while next_open >= 0:
                    open_sp = code_span_at(next_open)
                    if open_sp is not None:
                        next_open = prose.find("![", open_sp[1], mid)
                    else:
                        img_start = next_open
                        next_open = prose.find("![", img_start + 2, mid)

                target_start = mid + 2
                if prose[target_start : target_start + 11].lower() != "data:image/":
                    pos = target_start
                    continue

                fmt_start = target_start + 11
                fmt_limit = min(n, fmt_start + 128)
                semi_pos = -1
                i = fmt_start
                while i < fmt_limit:
                    ch = prose[i]
                    if ch == ";":
                        semi_pos = i
                        break
                    if ch in ")\r\n" or (ch == "!" and i + 1 < n and prose[i + 1] == "["):
                        break
                    i += 1
                if semi_pos <= fmt_start or prose[semi_pos : semi_pos + 8].lower() != ";base64,":
                    pos = i if i > target_start else target_start + 1
                    continue

                img_ext = prose[fmt_start:semi_pos]
                b64_start = semi_pos + 8
                m_b64 = _B64_RUN.match(prose, b64_start)
                k = m_b64.end() if m_b64 else b64_start
                if k >= n:
                    break

                payload_end = -1
                match_end = -1
                image_title: str | None = None
                fallback_from = k

                if k > b64_start and prose[k] == ")":
                    payload_end = k
                    match_end = k + 1
                elif k > b64_start and prose[k - 1] in " \t\r\n" and prose[k] in "\"'(":
                    parsed_title, hit_eof = _parse_b64_title(prose, k)
                    if hit_eof:
                        break
                    if parsed_title is not None:
                        t_str, after_title = parsed_title
                        m_ws = _WS_RUN.match(prose, after_title)
                        end_ws = m_ws.end() if m_ws else after_title
                        if end_ws >= n:
                            break
                        if prose[end_ws] == ")":
                            payload_end = k
                            image_title = t_str
                            match_end = end_ws + 1
                        else:
                            fallback_from = end_ws

                if match_end < 0:
                    r = fallback_from
                    while r < n:
                        ch = prose[r]
                        if (
                            ch == ")"
                            or ch in "\r\n"
                            or (ch == "!" and r + 1 < n and prose[r + 1] == "[")
                        ):
                            break
                        r += 1
                    if r >= n:
                        break
                    if prose[r] != ")":
                        pos = r if r > b64_start else b64_start
                        continue
                    if r == b64_start:
                        pos = r + 1
                        continue
                    payload_end = r
                    match_end = r + 1

                title = prose[img_start + 2 : mid]
                out_parts.append(prose[last_emit:img_start])
                out_parts.append(
                    handle_candidate(
                        prose,
                        img_start,
                        match_end,
                        title,
                        img_ext,
                        b64_start,
                        payload_end,
                        image_title,
                    )
                )
                last_emit = match_end
                pos = match_end

            if not out_parts:
                return prose
            out_parts.append(prose[last_emit:])
            return "".join(out_parts)

        text = _transform_outside_fences(content, extract_prose)
        logger.debug(f"Extracted {len(images)} base64 images from markdown")
        return text, images

    def replace_path(self, content: str, images: dict[str, str]) -> str:
        """Replace image paths in Markdown with new URLs."""
        content_replace: set = set()

        def repl(full_match: str, title: str, image_path: str) -> str:
            if image_path not in images:
                return full_match
            content_replace.add(image_path)
            mapped_path = images[image_path]
            return f"![{title}]({mapped_path})" if mapped_path else title

        text = _sub_markdown_images(content, repl)
        logger.debug(f"Replaced {len(content_replace)} images in markdown")
        return text

    @staticmethod
    def _self_test():
        your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAA)test"
        image_handle = MarkdownImageUtil()
        text, images = image_handle.extract_base64(your_content)
        print(text)

        for image_url, image_byte in images.items():
            with open(image_url, "wb") as f:
                f.write(image_byte)


class MarkdownImageBase64(BaseParser):
    """Parser for extracting base64 images from Markdown.

    Extracts base64-encoded images, replaces them with path references,
    and returns the raw image data in Document.images for the Go-side
    ImageResolver (or main.py _resolve_images) to handle storage.
    """

    def __init__(self, **kwargs):
        super().__init__(**kwargs)
        self.image_helper = MarkdownImageUtil()

    def parse_into_text(self, content: bytes) -> Document:
        text = endecode.decode_bytes(content)
        text, img_b64 = self.image_helper.extract_base64(text, path_prefix="images")

        images: dict[str, str] = {}
        for ipath, raw_bytes in img_b64.items():
            images[ipath] = base64.b64encode(raw_bytes).decode()

        logger.debug("Extracted %d base64 images from markdown", len(images))
        return Document(content=text, images=images)


class MarkdownParser(PipelineParser):
    """Complete Markdown parser using pipeline approach.

    This parser processes Markdown content through multiple stages:
    1. MarkdownTableFormatter: Standardizes table formatting
    2. MarkdownImageBase64: Extracts and uploads base64 images

    The pipeline ensures that content flows through each parser in sequence,
    with each stage's output becoming the next stage's input.
    """

    _parser_cls = (MarkdownTableFormatter, MarkdownImageBase64)


if __name__ == "__main__":
    # Example usage and testing
    logging.basicConfig(level=logging.DEBUG)

    # Test the complete MarkdownParser pipeline
    your_content = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    parser = MarkdownParser()

    # Parse content and display results
    document = parser.parse_into_text(your_content.encode())
    logger.info(document.content)
    logger.info(f"Images: {len(document.images)}, name: {document.images.keys()}")

    # Run individual utility tests
    MarkdownImageUtil._self_test()
    MarkdownTableUtil._self_test()
