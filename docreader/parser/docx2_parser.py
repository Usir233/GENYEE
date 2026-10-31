import io
import logging
import zipfile

from docreader.models.document import Document
from docreader.parser.chain_parser import FirstParser
from docreader.parser.docx_merge import _validate_zip_archive
from docreader.parser.docx_parser import DocxParser
from docreader.parser.markitdown_parser import MarkitdownParser

logger = logging.getLogger(__name__)


class Docx2Parser(FirstParser):
    _parser_cls = (MarkitdownParser, DocxParser)

    def parse_into_text(self, content: bytes) -> Document:
        """Validate ZIP archive boundaries before attempting parsing cascade."""
        if content.startswith(b"PK"):
            try:
                with zipfile.ZipFile(io.BytesIO(content)) as archive:
                    _validate_zip_archive(archive)
            except zipfile.BadZipFile:
                pass
        return super().parse_into_text(content)


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    your_file = "/path/to/your/file.docx"
    parser = Docx2Parser(separators=[".", "?", "!", "。", "？", "！"])
    with open(your_file, "rb") as f:
        content = f.read()

        document = parser.parse(content)
        for cc in document.chunks:
            logger.info(f"chunk: {cc}")

        # document = parser.parse_into_text(content)
        # logger.info(f"docx content: {document.content}")
        # logger.info(f"find images {document.images.keys()}")
