"""Wire form of source blocks (see docreader.parser.source_locator).

Kept apart from docreader.main so it can be imported without the parser
registry.
"""

import json
import re

from docreader.proto.docreader_pb2 import SourceBlock

_SURROGATE_RE = re.compile(r"[\ud800-\udfff]")


def source_blocks_to_proto(document) -> list:
    """Convert a document's source blocks to their wire form.

    Malformed entries are skipped: positions are an optional extra and must
    never fail a parse.
    """
    out = []
    for block in getattr(document, "source_blocks", None) or []:
        try:
            start, end = int(block["start"]), int(block["end"])
            locator = block["locator"]
            if end <= start or start < 0 or not locator.get("type"):
                continue
            raw_json = json.dumps(locator, ensure_ascii=False)
            safe_json = _SURROGATE_RE.sub("\ufffd", raw_json)
            out.append(
                SourceBlock(
                    start=start,
                    end=end,
                    locator_json=safe_json,
                )
            )
        except (KeyError, TypeError, ValueError, AttributeError):
            continue
    return out
