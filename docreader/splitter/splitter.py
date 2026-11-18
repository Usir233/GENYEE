"""Token splitter.

This module provides text splitting functionality with support for:
- Configurable chunk size and overlap
- Protected regex patterns (e.g., math formulas, images, links, tables)
- Header tracking for context preservation
- Smart merging with overlap handling
"""

import datetime
import itertools
import logging
import os
import re
import tempfile
from collections.abc import Callable, Sequence
from typing import Any, Generic, TypeVar

from pydantic import BaseModel, Field, PrivateAttr

from docreader.splitter.header_hook import (
    HeaderTracker,
    header_column_mismatch,
)
from docreader.utils.split import split_by_char, split_by_sep

# Default configuration for text chunking
# Aligned with internal/infrastructure/chunker/splitter.go (DefaultChunkOverlap = 80,
# DefaultChunkSize = 512). The Go splitter is now the production path; this
# Python splitter is kept for the docreader sidecar where it's still used.
DEFAULT_CHUNK_OVERLAP = 80  # Number of characters to overlap between chunks (~15% of chunk size)
DEFAULT_CHUNK_SIZE = 512  # Maximum size of each chunk in characters

_DEFAULT_SEPARATORS: tuple[str, ...] = ("\n", "。", " ")
_DEFAULT_PROTECTED_REGEX: tuple[str, ...] = (
    # math formula - LaTeX style formulas enclosed in $$
    r"\$\$[\s\S]*?\$\$",
    # image - Markdown image syntax ![alt](url)
    r"!\[.*?\]\(.*?\)",
    # link - Markdown link syntax [text](url)
    r"\[.*?\]\(.*?\)",
    # table header - Markdown table header with separator line
    r"[ ]*(?:\|[^|\n]*)+\|[\r\n]+\s*(?:\|\s*:?-{3,}:?\s*)+\|[\r\n]+",
    # table body - Markdown table rows
    r"[ ]*(?:\|[^|\n]*)+\|[\r\n]+",
    # code header - Code block start with language identifier
    r"```(?:\w+)[\r\n]+[^\r\n]*",
)

T = TypeVar("T")

logger = logging.getLogger(__name__)


class TextSplitter(BaseModel, Generic[T]):
    """Text splitter with support for protected patterns and header tracking.

    This class splits text into chunks while:
    - Respecting chunk size and overlap constraints
    - Preserving protected patterns (formulas, tables, code blocks)
    - Tracking headers for context preservation
    - Maintaining text integrity with smart merging
    """

    chunk_size: int = Field(description="The token chunk size for each chunk.")
    chunk_overlap: int = Field(description="The token overlap of each chunk when splitting.")
    separators: list[str] = Field(description="Default separators for splitting into words")

    # Try to keep the matched characters as a whole.
    # If it's too long, the content will be further segmented.
    # 尝试将匹配的字符作为整体保留，如果太长则进一步分段
    protected_regex: list[str] = Field(description="Protected regex for splitting into words")
    len_function: Callable[[str], int] = Field(description="The length function.")
    # Header tracking Hook related attributes
    # 标题跟踪钩子相关属性
    header_hook: HeaderTracker = Field(default_factory=HeaderTracker, exclude=True)

    # Compiled regex patterns for protected content
    _protected_fns: list[re.Pattern[str]] = PrivateAttr()
    # Split functions for different separators
    _split_fns: list[Callable[[str], list[str]]] = PrivateAttr()

    def __init__(
        self,
        chunk_size: int = DEFAULT_CHUNK_SIZE,
        chunk_overlap: int = DEFAULT_CHUNK_OVERLAP,
        separators: Sequence[str] | None = None,
        protected_regex: Sequence[str] | None = None,
        length_function: Callable[[str], int] = len,
    ) -> None:
        """Initialize with parameters.

        Args:
            chunk_size: Maximum size of each chunk
            chunk_overlap: Number of tokens to overlap between chunks
            separators: List of separators to use for splitting (in priority order)
            protected_regex: Regex patterns for content that should be kept intact
            length_function: Function to calculate text length (default: character count)

        Raises:
            ValueError: If chunk_overlap is larger than chunk_size
        """
        if chunk_size <= 0:
            raise ValueError(f"chunk_size ({chunk_size}) must be greater than 0.")
        if chunk_overlap < 0:
            raise ValueError(f"chunk_overlap ({chunk_overlap}) must be non-negative.")
        if chunk_overlap >= chunk_size:
            raise ValueError(
                f"Got a chunk overlap ({chunk_overlap}) >= chunk size "
                f"({chunk_size}), should be smaller."
            )

        sep_list = list(_DEFAULT_SEPARATORS if separators is None else separators)
        prot_list = list(_DEFAULT_PROTECTED_REGEX if protected_regex is None else protected_regex)

        super().__init__(
            chunk_size=chunk_size,
            chunk_overlap=chunk_overlap,
            separators=sep_list,
            protected_regex=prot_list,
            len_function=length_function,
        )
        # Compile all protected regex patterns for efficient matching
        self._protected_fns = [re.compile(reg) for reg in prot_list]
        # Create split functions: one for each separator, plus character-level fallback
        self._split_fns = [split_by_sep(sep) for sep in sep_list] + [split_by_char()]

    def split_text(self, text: str) -> list[tuple[int, int, str]]:
        """Split text into chunks with overlap and protected pattern handling.

        Args:
            text: The input text to split

        Returns:
            List of tuples (start_pos, end_pos, chunk_text) representing each chunk
        """
        if text == "":
            return []

        # Reset stateful header tracker per split_text invocation so table headers
        # from an earlier document never leak into subsequent documents.
        self.header_hook = HeaderTracker(header_hook_configs=self.header_hook.header_hook_configs)

        # Step 1: Split text by separators recursively
        splits = self._split(text)
        # Step 2: Extract protected content positions
        protect = self._split_protected(text)
        # Step 3: Merge splits with protected content to ensure integrity
        splits = self._join(splits, protect)

        # Verify that joining all splits reconstructs the original text
        if "".join(splits) != text:
            raise ValueError("Split text does not reconstruct original text")

        # Step 4: Merge splits into final chunks with overlap
        return self._merge(splits)

    def _split(self, text: str) -> list[str]:
        """Break text into splits that are smaller than chunk size.

        This method recursively splits text using separators in priority order.
        It tries each separator until it finds one that can split the text,
        then recursively processes any splits that are still too large.

        NOTE: the splits contain the separators.

        Args:
            text: The text to split

        Returns:
            List of text splits, each smaller than chunk_size
        """
        if self.len_function(text) <= self.chunk_size:
            return [text]

        splits: list[str] = []
        for split_fn in self._split_fns:
            splits = split_fn(text)
            if len(splits) > 1:
                break

        if len(splits) <= 1:
            return [text]

        new_splits: list[str] = []
        for split in splits:
            split_len = self.len_function(split)
            if split_len <= self.chunk_size:
                new_splits.append(split)
            else:
                new_splits.extend(self._split(split))
        return new_splits

    def _merge(self, splits: list[str]) -> list[tuple[int, int, str]]:
        """Merge splits into chunks with overlap and header tracking."""
        chunks: list[tuple[int, int, str]] = []
        cur_chunk: list[tuple[int, int, str]] = []

        cur_headers, cur_len = "", 0
        cur_start, cur_end = 0, 0

        for split in splits:
            cur_end = cur_start + len(split)
            split_len = self.len_function(split)

            if split_len > self.chunk_size:
                logger.error(
                    "Got a split of size %d, larger than chunk size %d.",
                    split_len,
                    self.chunk_size,
                )

            self.header_hook.update(split)
            if self.header_hook.header_ended_this_unit and len(cur_chunk) > 0:
                chunks.append(
                    (
                        cur_chunk[0][0],
                        cur_chunk[-1][1],
                        "".join([c[2] for c in cur_chunk]),
                    )
                )
                cur_chunk = []
                cur_len = 0
            cur_headers = self.header_hook.get_headers()
            cur_headers_len = self.len_function(cur_headers)

            if cur_headers_len > self.chunk_size:
                logger.error(
                    "Got headers of size %d, larger than chunk size %d.",
                    cur_headers_len,
                    self.chunk_size,
                )
                cur_headers, cur_headers_len = "", 0

            if cur_len + split_len + cur_headers_len > self.chunk_size:
                if len(cur_chunk) > 0:
                    chunks.append(
                        (
                            cur_chunk[0][0],
                            cur_chunk[-1][1],
                            "".join([c[2] for c in cur_chunk]),
                        )
                    )

                while cur_chunk and (
                    cur_len > self.chunk_overlap
                    or cur_len + split_len + cur_headers_len > self.chunk_size
                ):
                    first_chunk = cur_chunk.pop(0)
                    cur_len -= self.len_function(first_chunk[2])

                    if cur_chunk and first_chunk[0] == first_chunk[1]:
                        first_chunk = cur_chunk.pop(0)
                        cur_len -= self.len_function(first_chunk[2])

                if (
                    cur_headers
                    and split_len + cur_headers_len < self.chunk_size
                    and cur_headers not in split
                    and not header_column_mismatch(cur_headers, split)
                ):
                    next_start = cur_chunk[0][0] if cur_chunk else cur_start
                    cur_chunk.insert(0, (next_start, next_start, cur_headers))
                    cur_len += cur_headers_len

            cur_chunk.append((cur_start, cur_end, split))
            cur_len += split_len
            cur_start = cur_end

        if not cur_chunk:
            raise RuntimeError("Unexpected empty chunk during text splitting")
        chunks.append(
            (
                cur_chunk[0][0],
                cur_chunk[-1][1],
                "".join([c[2] for c in cur_chunk]),
            )
        )

        return chunks

    def _split_protected(self, text: str) -> list[tuple[int, str]]:
        """Extract protected content from text based on regex patterns."""
        matches = [
            (match.start(), match.end())
            for pattern in self._protected_fns
            for match in pattern.finditer(text)
        ]
        matches.sort(key=lambda x: (x[0], -x[1]))

        res: list[tuple[int, str]] = []

        def fold(initial: int, current: tuple[int, int]) -> int:
            if current[0] >= initial:
                if current[1] - current[0] < self.chunk_size:
                    res.append((current[0], text[current[0] : current[1]]))
                else:
                    logger.warning("Protected text ignore: %s", current)
            return max(initial, current[1])

        list(itertools.accumulate(matches, fold, initial=-1))
        return res

    def _join(
        self,
        splits: list[str],
        protect: list[tuple[int, str]],
    ) -> list[str]:
        """Merge splits with protected content to ensure protected patterns remain intact."""
        j = 0
        point, start = 0, 0
        res: list[str] = []

        for split in splits:
            end = start + len(split)
            cur = split[point - start :]

            while j < len(protect):
                p_start, p_content = protect[j]
                p_end = p_start + len(p_content)

                if end <= p_start:
                    break

                if point < p_start:
                    local_end = p_start - point
                    res.append(cur[:local_end])
                    cur = cur[local_end:]
                    point = p_start

                res.append(p_content)
                j += 1

                if point < p_end:
                    local_start = p_end - point
                    cur = cur[local_start:]
                    point = p_end

                if not cur:
                    break

            if cur:
                res.append(cur)
                point = end

            start = end
        return res

    def _validate_chunks(
        self,
        chunks: list[tuple[int, int, str]],
        original_text: str,
    ) -> None:
        """Validate chunks order and test text restoration."""
        errors: list[str] = []

        for i in range(1, len(chunks)):
            prev_start = chunks[i - 1][0]
            curr_start = chunks[i][0]
            if curr_start < prev_start:
                error_msg = (
                    f"Chunk order error: chunk[{i}] start position ({curr_start}) "
                    f"is less than chunk[{i - 1}] start position ({prev_start})"
                )
                errors.append(error_msg)
                logger.error(error_msg)

        try:
            restored_text = self.restore_text(chunks)
            if restored_text != original_text:
                error_msg = (
                    f"Restoration failed: restored text differs from original. "
                    f"Original length: {len(original_text)}, "
                    f"Restored length: {len(restored_text)}"
                )
                errors.append(error_msg)
                logger.error(error_msg)

                min_len = min(len(original_text), len(restored_text))
                diff_pos = -1
                for i in range(min_len):
                    if original_text[i] != restored_text[i]:
                        diff_pos = i
                        break

                if diff_pos >= 0:
                    context_start = max(0, diff_pos - 50)
                    context_end = min(len(original_text), diff_pos + 50)
                    errors.append(
                        f"First difference at position {diff_pos}:\n"
                        f"Original: {original_text[context_start:context_end]!r}\n"
                        f"Restored: {restored_text[context_start:context_end]!r}"
                    )
                elif len(original_text) != len(restored_text):
                    errors.append(f"Texts match up to position {min_len}, but lengths differ")
        except Exception as e:
            error_msg = f"Restoration exception: {e}"
            errors.append(error_msg)
            logger.error(error_msg)

        if errors:
            timestamp = datetime.datetime.now().strftime("%Y%m%d_%H%M%S")
            error_file = os.path.join(tempfile.gettempdir(), f"chunk_error_{timestamp}.md")

            with open(error_file, "w", encoding="utf-8") as f:
                f.write("# Chunk Validation Error Report\n\n")
                f.write(f"Timestamp: {timestamp}\n\n")

                f.write("## Errors\n\n")
                for error in errors:
                    f.write(f"- {error}\n\n")

                f.write("\n## Original Text\n\n")
                f.write(f"Length: {len(original_text)}\n\n")
                f.write("```\n")
                f.write(original_text)
                f.write("\n```\n\n")

                f.write("\n## Chunks Information\n\n")
                f.write(f"Total chunks: {len(chunks)}\n\n")
                for i, (start, end, chunk_text) in enumerate(chunks):
                    f.write(f"### Chunk {i}\n\n")
                    f.write(f"- Position: [{start}:{end}]\n")
                    f.write(f"- Length: {len(chunk_text)}\n")
                    f.write(f"- Content:\n\n```\n{chunk_text}\n```\n\n")

                try:
                    restored_text = self.restore_text(chunks)
                    f.write("\n## Restored Text\n\n")
                    f.write(f"Length: {len(restored_text)}\n\n")
                    f.write("```\n")
                    f.write(restored_text)
                    f.write("\n```\n")
                except Exception as e:
                    f.write("\n## Restoration Failed\n\n")
                    f.write(f"Error: {e}\n")

            logger.error("Validation errors saved to: %s", error_file)

    def restore_text(self, chunks: list[tuple[int, int, str]]) -> str:
        """Restore original text from chunks with overlap handling."""
        if not chunks:
            return ""

        sorted_chunks = sorted(chunks, key=lambda x: (x[1], x[0]))

        result_parts: list[str] = []
        last_end = 0

        for _start_pos, end_pos, chunk_text in sorted_chunks:
            if end_pos <= last_end:
                continue
            result_parts.append(chunk_text[last_end - end_pos :])
            last_end = end_pos

        return "".join(result_parts)


if __name__ == "__main__":
    s = """
    这是一些普通文本。

    | 姓名 | 年龄 | 城市 |
    |------|------|------|
    | 张三 | 25   | 北京 |
    | 李四 | 30   | 上海 |
    | 王五 | 28   | 广州 |
    | 张三 | 25   | 北京 |
    | 李四 | 30   | 上海 |
    | 王五 | 28   | 广州 |

    这是文本结束。

"""

    sp: TextSplitter[Any] = TextSplitter(
        chunk_size=200,
        chunk_overlap=10,
        separators=["\n\n", "\n", "。", "？", "！", "，", "；", "："],
    )
    ck = sp.split_text(s)
    for c in ck:
        print("------", len(c))
        print(c)
