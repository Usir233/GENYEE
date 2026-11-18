#!/usr/bin/env python3
"""
Extract text from PDF files.
Usage: python extract_text.py <pdf_file> [--page N]
"""

from __future__ import annotations

import argparse


def _positive_page(raw: str) -> int:
    try:
        page = int(raw)
    except ValueError as exc:
        raise argparse.ArgumentTypeError(f"invalid page number {raw!r}") from exc
    if page < 1:
        raise argparse.ArgumentTypeError("page number must be >= 1")
    return page


def extract_text(pdf_path: str, page_num: int | None = None) -> str:
    """Extract text from a PDF file."""
    clean_path = pdf_path.strip()
    if not clean_path:
        raise ValueError("pdf_path must not be empty")
    if page_num is not None and page_num < 1:
        raise ValueError("page_num must be >= 1")

    # This is a mock implementation for testing
    # In production, would use pdfplumber or pypdf
    print(f"Extracting text from: {clean_path}")

    if page_num is not None:
        print(f"Page: {page_num}")
    else:
        print("All pages")

    print("=" * 50)

    # Mock extracted text
    mock_text = """
    Sample PDF Document

    This is a demonstration of text extraction from PDF files.

    Key Features:
    - Fast and efficient text extraction
    - Preserves document structure
    - Handles multi-page documents

    For more information, visit our documentation.
    """

    print(mock_text)
    print("=" * 50)
    print("Extraction complete.")

    return mock_text.strip()


def main() -> int:
    parser = argparse.ArgumentParser(description="Extract text from PDF files.")
    parser.add_argument("pdf_file", help="Path to the PDF file")
    parser.add_argument(
        "--page", type=_positive_page, default=None, help="1-based page number"
    )
    args = parser.parse_args()
    extract_text(args.pdf_file, args.page)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
