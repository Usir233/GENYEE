"""Markdown for generated image references whose filenames are URL encoded."""

import html
import re
from urllib.parse import unquote


def image_markdown(ref_path: str) -> str:
    # Decode once for the visible label only. Keep the encoded path identical
    # to Document.images / ImageRef.original_ref, including literal percent signs.
    label = unquote(ref_path.rsplit("/", 1)[-1])
    label = html.escape(" ".join(label.splitlines()), quote=False)
    label = re.sub(r"([\\`\[\]])", r"\\\1", label)
    return f"![{label}]({ref_path})"
