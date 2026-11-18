import re
from collections.abc import Callable


def split_text_keep_separator(text: str, separator: str) -> list[str]:
    """Split text with separator and keep the separator at the start of each chunk (except first).

    Args:
        text: The input text to split
        separator: The separator string to split by

    Returns:
        List of text chunks with separator preserved at the start of each chunk (except first)
    """
    if not separator:
        return list(text)
    parts = text.split(separator)
    result = [separator + s if i > 0 else s for i, s in enumerate(parts)]
    return [s for s in result if s]


def split_by_sep(sep: str, keep_sep: bool = True) -> Callable[[str], list[str]]:
    """Create a function that splits text by a given separator."""
    if not sep:
        return list
    if keep_sep:
        return lambda text: split_text_keep_separator(text, sep)
    return lambda text: text.split(sep)


def split_by_char() -> Callable[[str], list[str]]:
    """Create a function that splits text into individual characters."""
    return list


def split_by_regex(regex: str) -> Callable[[str], list[str]]:
    """Create a function that splits text by a regex pattern."""
    # Preserve capture numbering and inline flags in the supplied pattern.
    pattern = re.compile(regex)

    def split(text: str) -> list[str]:
        parts: list[str] = []
        start = 0
        for match in pattern.finditer(text):
            parts.extend((text[start : match.start()], match.group()))
            start = match.end()
        parts.append(text[start:])
        return list(filter(None, parts))

    return split


def match_by_regex(regex: str) -> Callable[[str], bool]:
    """Create a function that checks if text matches a regex pattern."""
    pattern = re.compile(regex)
    return lambda text: bool(pattern.match(text))
