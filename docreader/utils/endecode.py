"""
Encoding and Decoding Utilities Module

This module provides utilities for encoding and decoding various data types,
with a focus on image and text data conversion:
- Image encoding/decoding (base64)
- Text encoding/decoding (multiple character sets)
- Bytes conversion utilities
"""

import base64
import codecs
import io
import logging
from collections.abc import Sequence
from typing import Any

import numpy as np
from PIL import Image

logger = logging.getLogger(__name__)
_ASCII_WS_DELETE = str.maketrans("", "", " \t\r\n\f\v")


def decode_image(image: str | bytes | Image.Image | np.ndarray[Any, Any]) -> str:
    """Convert image to base64 encoded string.

    Args:
        image: Image in one of the following formats:
            - str: File path to an image file
            - bytes: Raw image bytes data
            - Image.Image: PIL/Pillow Image object
            - np.ndarray: NumPy array representing image data

    Returns:
        str: Base64 encoded string representation of the image

    Raises:
        ValueError: If the image type is not supported
    """
    if isinstance(image, str):
        with open(image, "rb") as image_file:
            return base64.b64encode(image_file.read()).decode()

    if isinstance(image, bytes):
        return base64.b64encode(image).decode()

    if isinstance(image, Image.Image):
        with io.BytesIO() as buffer:
            img_format = image.format if image.format else "PNG"
            image.save(buffer, format=img_format)
            return base64.b64encode(buffer.getvalue()).decode()

    if isinstance(image, np.ndarray):
        with Image.fromarray(image) as pil_image, io.BytesIO() as buffer:
            pil_image.save(buffer, format="PNG")
            return base64.b64encode(buffer.getvalue()).decode()

    raise ValueError(f"Unsupported image type: {type(image)}")


def encode_image(image: str | bytes, errors: str = "strict") -> bytes:
    """Decode a base64 encoded image string back to bytes.

    Args:
        image: Base64 encoded string or ASCII bytes representation of an image
        errors: Error handling scheme for decoding errors:
            - 'strict' (default): Raise on decoding errors
            - 'ignore': Return empty bytes on decoding errors

    Returns:
        bytes: Decoded image bytes, or empty bytes if errors='ignore' and decoding fails

    Raises:
        ValueError: If decoding fails and errors='strict'. base64.b64decode
            signals bad padding or a bad alphabet with binascii.Error, and a
            non-ASCII str with a plain ValueError; binascii.Error subclasses
            ValueError, so both arrive as one.
    """
    try:
        payload: str | bytes = (
            image.translate(_ASCII_WS_DELETE) if isinstance(image, str) else b"".join(image.split())
        )
        return base64.b64decode(payload, validate=True)
    except ValueError:
        if errors == "ignore":
            return b""
        raise


def encode_bytes(content: str) -> bytes:
    """Convert a string to bytes using UTF-8 encoding."""
    return content.encode()


def decode_bytes(
    content: bytes,
    encodings: Sequence[str] | None = None,
) -> str:
    """Decode bytes to string with automatic encoding detection.

    With default encodings, a Unicode byte order mark is decoded first.
    Explicit encoding lists retain their supplied priority.
    """
    if encodings is None:
        # UTF-32 LE starts with the UTF-16 LE signature: check it first.
        bom_candidates: list[tuple[tuple[bytes, ...], str]] = [
            ((codecs.BOM_UTF32_LE, codecs.BOM_UTF32_BE), "utf-32"),
            ((codecs.BOM_UTF16_LE, codecs.BOM_UTF16_BE), "utf-16"),
            ((codecs.BOM_UTF8,), "utf-8-sig"),
        ]
        for signatures, bom_encoding in bom_candidates:
            if content.startswith(signatures):
                try:
                    return content.decode(bom_encoding)
                except UnicodeDecodeError:
                    break
        # First try strict UTF-8
        try:
            return content.decode("utf-8")
        except UnicodeDecodeError:
            pass

        # Try charset-normalizer statistical detection
        try:
            from charset_normalizer import from_bytes

            matches = from_bytes(content)
            best_match = matches.best()
            if best_match is not None and best_match.encoding:
                detected_encoding = best_match.encoding

                # 1. Japanese disambiguation:
                jp_match = next(
                    (
                        m
                        for m in matches
                        if "Hiragana" in getattr(m, "alphabets", [])
                        and m.encoding in ("euc_jis_2004", "euc_jp", "shift_jis", "cp932")
                    ),
                    None,
                )
                if jp_match is not None and (
                    best_match.encoding in ("big5", "big5hkscs")
                    or "Hiragana" not in getattr(best_match, "alphabets", [])
                ):
                    detected_encoding = jp_match.encoding
                else:
                    # 2. CJK Chinese vs Korean (CP949/EUC-KR/Johab):
                    gb_match = next(
                        (m for m in matches if m.chaos == 0.0 and m.encoding in ("gb18030", "gbk")),
                        None,
                    )
                    cp_match = next(
                        (
                            m
                            for m in matches
                            if m.chaos == 0.0
                            and m.encoding in ("cp949", "euc_kr")
                            and "Hangul Syllables" in getattr(m, "alphabets", [])
                            and "CJK Unified Ideographs" not in getattr(m, "alphabets", [])
                        ),
                        None,
                    )

                    if best_match.encoding in (
                        "cp949",
                        "euc_kr",
                        "johab",
                    ) and "Hangul Syllables" in getattr(best_match, "alphabets", []):
                        if gb_match is not None and "CJK Unified Ideographs" in getattr(
                            best_match, "alphabets", []
                        ):
                            detected_encoding = gb_match.encoding
                        else:
                            detected_encoding = best_match.encoding
                    elif cp_match is not None and best_match.encoding in (
                        "big5",
                        "big5hkscs",
                        "utf_16_be",
                        "utf_16_le",
                        "cp874",
                        "cp1252",
                        "iso8859_1",
                    ):
                        if best_match.encoding in ("big5", "big5hkscs") and gb_match is not None:
                            from charset_normalizer.constant import (
                                COMMON_CHINESE_CHARACTERS,
                            )

                            text_gb = content.decode(gb_match.encoding, errors="ignore")
                            cjk_chars = [ch for ch in text_gb if "\u4e00" <= ch <= "\u9fff"]
                            gb_ratio = (
                                sum(ch in COMMON_CHINESE_CHARACTERS for ch in cjk_chars)
                                / len(cjk_chars)
                                if cjk_chars
                                else 0.0
                            )
                            if gb_ratio >= 0.5:
                                detected_encoding = gb_match.encoding
                            else:
                                detected_encoding = cp_match.encoding
                        else:
                            detected_encoding = cp_match.encoding
                    elif gb_match is not None and best_match.encoding in (
                        "big5",
                        "big5hkscs",
                        "euc_jis_2004",
                    ):
                        if "Hiragana" in getattr(
                            gb_match, "alphabets", []
                        ) and "Hiragana" not in getattr(best_match, "alphabets", []):
                            detected_encoding = best_match.encoding
                        else:
                            detected_encoding = gb_match.encoding
                    else:
                        # 3. Western encodings disambiguation:
                        cp1252_match = next(
                            (
                                m
                                for m in matches
                                if m.chaos == 0.0 and m.encoding in ("cp1252", "windows-1252")
                            ),
                            None,
                        )
                        if cp1252_match is not None and best_match.encoding in (
                            "cp1250",
                            "cp1257",
                            "cp775",
                            "iso8859_10",
                            "iso8859_16",
                            "iso8859_4",
                            "iso8859_3",
                        ):
                            detected_encoding = "cp1252"
                        elif best_match.encoding in (
                            "mac_iceland",
                            "cp852",
                            "cp775",
                            "cp437",
                            "cp1250",
                            "cp1251",
                            "koi8_t",
                            "mac_greek",
                            "ptcp154",
                            "cp864",
                        ):
                            with_quotes = (
                                "\u201c\u201d\u2018\u2019\u2014\u2013\u20ac\u2026\u2122\u2022"
                            )
                            try:
                                text_cp1252 = content.decode("cp1252")
                                if any(ch in text_cp1252 for ch in with_quotes):
                                    detected_encoding = "cp1252"
                            except Exception:
                                pass

                try:
                    text = content.decode(detected_encoding)
                    logger.debug(
                        "Decode content with charset-normalizer (%s): %d characters",
                        detected_encoding,
                        len(text),
                    )
                    return text
                except UnicodeDecodeError:
                    pass
        except Exception as e:
            logger.debug("Charset detection failed: %s", e)

        encodings = [
            "utf-8",
            "gb18030",
            "gb2312",
            "gbk",
            "big5",
            "ascii",
            "latin-1",
        ]

    for encoding in encodings:
        try:
            text = content.decode(encoding)
            logger.debug("Decode content with %s: %d characters", encoding, len(text))
            return text
        except UnicodeDecodeError:
            continue

    text = content.decode(encoding="latin-1", errors="replace")
    logger.warning(
        "Unable to determine correct encoding, using latin-1 as fallback. "
        "This may cause character issues."
    )
    return text


if __name__ == "__main__":
    img = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    encode_image(img, errors="ignore")
