"""Archive admission checks, performed before any entry is decompressed."""

import io
import zipfile

MAX_UNCOMPRESSED_ARCHIVE_BYTES = 100 * 1024 * 1024
MAX_COMPRESSION_RATIO = 100.0


class ArchiveLimitError(ValueError):
    """A resource limit violation that must never trigger parser fallback."""


def validate_zip_archive(
    archive: zipfile.ZipFile,
    max_bytes: int = MAX_UNCOMPRESSED_ARCHIVE_BYTES,
    max_ratio: float = MAX_COMPRESSION_RATIO,
) -> None:
    total = 0
    for info in archive.infolist():
        if info.file_size > max_bytes:
            raise ArchiveLimitError(
                f"Archive entry {info.filename} uncompressed size ({info.file_size}) exceeds limit ({max_bytes})"
            )
        if (info.compress_size > 0 and info.file_size > 1024 * 1024
                and info.file_size / info.compress_size > max_ratio):
            raise ArchiveLimitError(
                f"Suspicious compression ratio for entry {info.filename}: "
                f"{info.file_size / info.compress_size:.1f} > {max_ratio}"
            )
        total += info.file_size
        if total > max_bytes:
            raise ArchiveLimitError(
                f"Archive cumulative uncompressed size ({total}) exceeds limit ({max_bytes})"
            )


def validate_zip_content(content: bytes) -> None:
    if zipfile.is_zipfile(io.BytesIO(content)):
        with zipfile.ZipFile(io.BytesIO(content)) as archive:
            validate_zip_archive(archive)
