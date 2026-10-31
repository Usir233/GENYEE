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
from typing import List, Optional, Union

import numpy as np
from PIL import Image

logger = logging.getLogger(__name__)


def decode_image(image: Union[str, bytes, Image.Image, np.ndarray]) -> str:
    """Convert image to base64 encoded string.

    This function handles multiple image input formats and converts them
    to a base64 encoded string representation, which is useful for embedding
    images in JSON, HTML, or other text-based formats.

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

    Example:
        >>> # From file path
        >>> base64_str = decode_image("/path/to/image.png")
        >>> # From PIL Image
        >>> from PIL import Image
        >>> img = Image.open("photo.jpg")
        >>> base64_str = decode_image(img)
    """
    if isinstance(image, str):
        # Handle file path: read file and encode to base64
        with open(image, "rb") as image_file:
            return base64.b64encode(image_file.read()).decode()

    elif isinstance(image, bytes):
        # Handle raw bytes: directly encode to base64
        return base64.b64encode(image).decode()

    elif isinstance(image, Image.Image):
        # Handle PIL Image: save to buffer then encode
        buffer = io.BytesIO()
        # Use original format if available, otherwise default to PNG
        img_format = image.format if image.format else "PNG"
        image.save(buffer, format=img_format)
        return base64.b64encode(buffer.getvalue()).decode()

    elif isinstance(image, np.ndarray):
        # Handle numpy array: convert to PIL Image, then encode as PNG
        pil_image = Image.fromarray(image)
        buffer = io.BytesIO()
        pil_image.save(buffer, format="PNG")
        return base64.b64encode(buffer.getvalue()).decode()

    raise ValueError(f"Unsupported image type: {type(image)}")


def encode_image(image: str, errors="strict") -> bytes:
    """Decode a base64 encoded image string back to bytes.

    This function converts a base64 encoded string representation of an image
    back into its original binary bytes format.

    Args:
        image: Base64 encoded string representation of an image
        errors: Error handling scheme for decoding errors:
            - 'strict' (default): Raise on decoding errors
            - 'ignore': Return empty bytes on decoding errors
            - Any other name registered with codecs.register_error

    Returns:
        bytes: Decoded image bytes, or empty bytes if errors='ignore' and decoding fails

    Raises:
        ValueError: If decoding fails and errors='strict'. base64.b64decode
            signals bad padding or a bad alphabet with binascii.Error, and a
            non-ASCII str with a plain ValueError; binascii.Error subclasses
            ValueError, so both arrive as one.

    Example:
        >>> base64_str = "iVBORw0KGgoAAAANSUhEUgAAAAUA..."
        >>> image_bytes = encode_image(base64_str)
        >>> # With error handling
        >>> image_bytes = encode_image(base64_str, errors="ignore")
    """
    try:
        # Attempt to decode the base64 string to bytes
        image_bytes = base64.b64decode(image)
    except ValueError as e:
        # Catching binascii.Error alone would miss the non-ASCII case: a str
        # that is not pure ASCII fails before the decoder runs and surfaces as
        # a plain ValueError, which errors="ignore" is supposed to absorb.
        if errors == "ignore":
            return b""
        else:
            raise e
    return image_bytes


def encode_bytes(content: str) -> bytes:
    """Convert a string to bytes using UTF-8 encoding.

    Args:
        content: String to be encoded

    Returns:
        bytes: UTF-8 encoded bytes representation of the string

    Example:
        >>> text = "Hello, 世界"
        >>> encoded = encode_bytes(text)
        >>> type(encoded)
        <class 'bytes'>
    """
    return content.encode()


def decode_bytes(
    content: bytes,
    encodings: Optional[List[str]] = None,
) -> str:
    """Decode bytes to string with automatic encoding detection.

    With default encodings, a Unicode byte order mark is decoded first.
    Explicit encoding lists retain their supplied priority.

    This function attempts to decode bytes using multiple encoding formats
    in order of priority. It's particularly useful for handling text files
    with unknown or mixed encodings, especially for Chinese text.

    The function tries encodings in the provided order and returns the first
    successful decode. If all encodings fail, it falls back to latin-1 with
    error replacement to ensure a result is always returned.

    Args:
        content: Bytes content to be decoded
        encodings: List of encoding formats to try, in order of priority.
            Default includes common encodings for Chinese and Western text:
            - utf-8: Universal encoding (tried first)
            - gb18030, gb2312, gbk: Chinese encodings (Simplified)
            - big5: Chinese encoding (Traditional)
            - ascii, latin-1: Western encodings

    Returns:
        str: Decoded string content

    Note:
        - If all encodings fail, latin-1 with error='replace' is used as fallback
        - The fallback may result in character replacement (�) for invalid bytes
        - A warning is logged when fallback encoding is used

    Example:
        >>> # Decode with default encodings
        >>> text = decode_bytes(b"\\xe4\\xb8\\xad\\xe6\\x96\\x87")  # UTF-8 Chinese
        >>> print(text)
        中文
        >>> # Decode with custom encodings
        >>> text = decode_bytes(content, encodings=["utf-8", "gbk"])
    """
    if encodings is None:
        # UTF-32 LE starts with the UTF-16 LE signature: check it first.
        for signatures, encoding in [
            ((codecs.BOM_UTF32_LE, codecs.BOM_UTF32_BE), "utf-32"),
            ((codecs.BOM_UTF16_LE, codecs.BOM_UTF16_BE), "utf-16"),
            ((codecs.BOM_UTF8,), "utf-8-sig"),
        ]:
            if content.startswith(signatures):
                try:
                    return content.decode(encoding)
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
                # Disambiguate tied/spurious matches (e.g. GBK/GB18030 misclassified as CP949/Big5, EUC-JP as Big5)
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
                    # Mathematical discriminator:
                    # When Chinese GBK text is decoded as CP949, it decodes into a mixture of CJK Unified Ideographs AND Hangul syllables.
                    # When genuine Korean text is decoded as CP949, it contains ONLY Hangul syllables (and basic Latin/punctuation) and ZERO CJK Unified Ideographs.
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

                    if (
                        best_match.encoding in ("cp949", "euc_kr", "johab")
                        and "Hangul Syllables" in getattr(best_match, "alphabets", [])
                    ):
                        # If best_match has CJK ideographs mixed with Hangul, it is Chinese GBK misdecoded as CP949
                        if (
                            gb_match is not None
                            and "CJK Unified Ideographs" in getattr(best_match, "alphabets", [])
                        ):
                            detected_encoding = gb_match.encoding
                        else:
                            detected_encoding = best_match.encoding
                    elif cp_match is not None and best_match.encoding in (
                        "big5", "big5hkscs", "utf_16_be", "utf_16_le", "cp874", "cp1252", "iso8859_1"
                    ):
                        # If charset-normalizer misidentifies short Korean text as Big5, UTF-16, CP874, etc., recover pure Korean CP949
                        if best_match.encoding in ("big5", "big5hkscs") and gb_match is not None:
                            from charset_normalizer.constant import COMMON_CHINESE_CHARACTERS

                            text_gb = content.decode(gb_match.encoding, errors="ignore")
                            cjk_chars = [ch for ch in text_gb if "\u4e00" <= ch <= "\u9fff"]
                            gb_ratio = (
                                sum(ch in COMMON_CHINESE_CHARACTERS for ch in cjk_chars) / len(cjk_chars)
                                if cjk_chars
                                else 0.0
                            )
                            if gb_ratio >= 0.5:
                                detected_encoding = gb_match.encoding
                            else:
                                detected_encoding = cp_match.encoding
                        else:
                            detected_encoding = cp_match.encoding
                    elif (
                        gb_match is not None
                        and best_match.encoding in ("big5", "big5hkscs", "euc_jis_2004")
                    ):
                        if (
                            "Hiragana" in getattr(gb_match, "alphabets", [])
                            and "Hiragana" not in getattr(best_match, "alphabets", [])
                        ):
                            detected_encoding = best_match.encoding
                        else:
                            detected_encoding = gb_match.encoding
                    else:
                        # 3. Western encodings disambiguation:
                        # Prefer Windows-1252 / ISO-8859-1 over Central European (CP1250) or legacy DOS/Mac codepages.
                        cp1252_match = next(
                            (m for m in matches if m.chaos == 0.0 and m.encoding in ("cp1252", "windows-1252")),
                            None,
                        )
                        if cp1252_match is not None and best_match.encoding in (
                            "cp1250", "cp1257", "cp775", "iso8859_10", "iso8859_16", "iso8859_4", "iso8859_3"
                        ):
                            detected_encoding = "cp1252"
                        elif best_match.encoding in (
                            "mac_iceland", "cp852", "cp775", "cp437", "cp1250", "cp1251", "koi8_t", "mac_greek", "ptcp154", "cp864"
                        ):
                            try:
                                text_cp1252 = content.decode("cp1252")
                                if any(ch in text_cp1252 for ch in "\u201c\u201d\u2018\u2019\u2014\u2013\u20ac\u2026\u2122\u2022"):
                                    detected_encoding = "cp1252"
                            except Exception:
                                pass

                try:
                    text = content.decode(detected_encoding)
                    logger.debug(
                        f"Decode content with charset-normalizer ({detected_encoding}): {len(text)} characters"
                    )
                    return text
                except UnicodeDecodeError:
                    pass
        except Exception as e:
            logger.debug(f"Charset detection failed: {e}")

        encodings = [
            "utf-8",
            "gb18030",
            "gb2312",
            "gbk",
            "big5",
            "ascii",
            "latin-1",
        ]
    # Try decoding with each encoding format in order
    for encoding in encodings:
        try:
            text = content.decode(encoding)
            logger.debug(f"Decode content with {encoding}: {len(text)} characters")
            return text
        except UnicodeDecodeError:
            # This encoding didn't work, try the next one
            continue

    # Fallback: use latin-1 with error replacement if all encodings fail
    # latin-1 can decode any byte sequence, but may produce incorrect characters
    text = content.decode(encoding="latin-1", errors="replace")
    logger.warning(
        "Unable to determine correct encoding, using latin-1 as fallback. "
        "This may cause character issues."
    )
    return text


if __name__ == "__main__":
    # Example: Test encode_image with error handling
    # This demonstrates decoding a base64 string with 'ignore' error mode
    img = "test![](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAMgA)test"
    encode_image(img, errors="ignore")
