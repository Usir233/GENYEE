"""Archive admission and streaming decompression checks."""

from __future__ import annotations

import bz2
from dataclasses import dataclass
import io
import weakref
import zipfile
import zlib

MAX_UNCOMPRESSED_ARCHIVE_BYTES = 100 * 1024 * 1024
MAX_COMPRESSION_RATIO = 100.0
MAX_ARCHIVE_ENTRIES = 10_000
ZIP_STREAM_CHUNK_BYTES = 64 * 1024
_RATIO_MIN_DECOMPRESSED_BYTES = 1024 * 1024


class ArchiveLimitError(ValueError):
    """A resource limit violation that must never trigger parser fallback."""


@dataclass
class ZipReadBudget:
    """Cumulative real decompressed byte tracker across a ZipFile session."""

    max_total_bytes: int = MAX_UNCOMPRESSED_ARCHIVE_BYTES
    max_member_bytes: int = MAX_UNCOMPRESSED_ARCHIVE_BYTES
    max_ratio: float = MAX_COMPRESSION_RATIO
    total_bytes: int = 0


_ARCHIVE_BUDGETS: "weakref.WeakKeyDictionary[zipfile.ZipFile, ZipReadBudget]" = (
    weakref.WeakKeyDictionary()
)


def _get_or_create_budget(
    archive: zipfile.ZipFile,
    *,
    max_member_bytes: int | None = None,
    max_total_bytes: int | None = None,
    max_ratio: float | None = None,
) -> ZipReadBudget:
    budget = _ARCHIVE_BUDGETS.get(archive)
    if budget is None:
        budget = ZipReadBudget(
            max_total_bytes=(
                MAX_UNCOMPRESSED_ARCHIVE_BYTES
                if max_total_bytes is None
                else max_total_bytes
            ),
            max_member_bytes=(
                MAX_UNCOMPRESSED_ARCHIVE_BYTES
                if max_member_bytes is None
                else max_member_bytes
            ),
            max_ratio=MAX_COMPRESSION_RATIO if max_ratio is None else max_ratio,
        )
        try:
            _ARCHIVE_BUDGETS[archive] = budget
        except TypeError:
            pass
    return budget


def validate_zip_archive(
    archive: zipfile.ZipFile,
    max_bytes: int = MAX_UNCOMPRESSED_ARCHIVE_BYTES,
    max_ratio: float = MAX_COMPRESSION_RATIO,
    max_entries: int = MAX_ARCHIVE_ENTRIES,
) -> None:
    """Validate ZipInfo header metadata and entry count before decompression."""
    entries = archive.infolist()
    if len(entries) > max_entries:
        raise ArchiveLimitError(
            f"Archive entry count ({len(entries)}) exceeds limit ({max_entries})"
        )

    total = 0
    for info in entries:
        if info.file_size > max_bytes:
            raise ArchiveLimitError(
                f"Archive entry {info.filename} uncompressed size ({info.file_size}) exceeds limit ({max_bytes})"
            )
        if (
            info.compress_size > 0
            and info.file_size > _RATIO_MIN_DECOMPRESSED_BYTES
            and info.file_size / info.compress_size > max_ratio
        ):
            raise ArchiveLimitError(
                f"Suspicious compression ratio for entry {info.filename}: "
                f"{info.file_size / info.compress_size:.1f} > {max_ratio}"
            )
        total += info.file_size
        if total > max_bytes:
            raise ArchiveLimitError(
                f"Archive cumulative uncompressed size ({total}) exceeds limit ({max_bytes})"
            )

    # Seed per-archive streaming read budget with the validated ceiling.
    _get_or_create_budget(
        archive,
        max_member_bytes=max_bytes,
        max_total_bytes=max_bytes,
        max_ratio=max_ratio,
    )


def _stream_zip_entry_bounded(
    archive: zipfile.ZipFile,
    info: zipfile.ZipInfo,
    *,
    eff_max_member: int,
    eff_max_total: int,
    eff_max_ratio: float,
    active_budget: ZipReadBudget,
    chunk_size: int = ZIP_STREAM_CHUNK_BYTES,
    collect_bytes: bool = True,
) -> bytes:
    """Decompress a single ZIP member in bounded chunks without trusting ZipInfo.file_size."""
    if info.file_size > eff_max_member:
        raise ArchiveLimitError(
            f"Archive entry {info.filename} uncompressed size ({info.file_size}) exceeds limit ({eff_max_member})"
        )
    if (
        info.compress_size > 0
        and info.file_size > _RATIO_MIN_DECOMPRESSED_BYTES
        and info.file_size / info.compress_size > eff_max_ratio
    ):
        raise ArchiveLimitError(
            f"Suspicious compression ratio for entry {info.filename}: "
            f"{info.file_size / info.compress_size:.1f} > {eff_max_ratio}"
        )

    chunks: list[bytes] = []
    member_bytes = 0
    running_crc = 0
    read_step = max(1024, int(chunk_size))

    def _record_chunk(out: bytes) -> None:
        nonlocal member_bytes, running_crc
        if not out:
            return
        chunk_len = len(out)
        member_bytes += chunk_len
        active_budget.total_bytes += chunk_len
        if member_bytes > eff_max_member:
            raise ArchiveLimitError(
                f"Archive entry {info.filename} real decompressed size "
                f"(>{eff_max_member}) exceeds limit ({eff_max_member})"
            )
        if active_budget.total_bytes > eff_max_total:
            raise ArchiveLimitError(
                f"Archive cumulative real decompressed size "
                f"({active_budget.total_bytes}) exceeds limit ({eff_max_total})"
            )
        if (
            info.compress_size > 0
            and member_bytes > _RATIO_MIN_DECOMPRESSED_BYTES
            and member_bytes / info.compress_size > eff_max_ratio
        ):
            raise ArchiveLimitError(
                f"Suspicious real compression ratio for entry {info.filename}: "
                f"{member_bytes / info.compress_size:.1f} > {eff_max_ratio}"
            )
        running_crc = zlib.crc32(out, running_crc)
        if collect_bytes:
            chunks.append(out)

    with archive.open(info, "r") as stream:
        raw_fileobj = getattr(stream, "_fileobj", None)
        is_unencrypted = getattr(stream, "_decrypter", None) is None
        if (
            raw_fileobj is not None
            and is_unencrypted
            and info.compress_type
            in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED, zipfile.ZIP_BZIP2)
        ):
            comp_left = int(info.compress_size)
            if info.compress_type == zipfile.ZIP_STORED:
                while comp_left > 0:
                    rem_budget = min(
                        eff_max_member - member_bytes,
                        eff_max_total - active_budget.total_bytes,
                    )
                    step_cap = min(read_step, comp_left, max(1, rem_budget + 1))
                    raw = raw_fileobj.read(step_cap)
                    if not raw:
                        raise EOFError(
                            f"Truncated stored ZIP entry: {info.filename!r}"
                        )
                    comp_left -= len(raw)
                    _record_chunk(raw)
            elif info.compress_type == zipfile.ZIP_DEFLATED:
                decompressor = zlib.decompressobj(-15)
                try:
                    while not decompressor.eof:
                        rem_budget = min(
                            eff_max_member - member_bytes,
                            eff_max_total - active_budget.total_bytes,
                        )
                        step_cap = min(read_step, max(1, rem_budget + 1))
                        if decompressor.unconsumed_tail:
                            out = decompressor.decompress(
                                decompressor.unconsumed_tail, step_cap
                            )
                        elif comp_left > 0:
                            raw = raw_fileobj.read(min(read_step, comp_left))
                            if not raw:
                                raise EOFError(
                                    f"Truncated deflated ZIP entry: {info.filename!r}"
                                )
                            comp_left -= len(raw)
                            out = decompressor.decompress(raw, step_cap)
                        else:
                            out = decompressor.flush()
                            _record_chunk(out)
                            break
                        _record_chunk(out)
                except zlib.error as exc:
                    raise zipfile.BadZipFile(
                        f"Corrupt deflate stream for {info.filename!r}: {exc}"
                    ) from exc
            else:
                bz2_dec = bz2.BZ2Decompressor()
                while not bz2_dec.eof:
                    rem_budget = min(
                        eff_max_member - member_bytes,
                        eff_max_total - active_budget.total_bytes,
                    )
                    step_cap = min(read_step, max(1, rem_budget + 1))
                    if not bz2_dec.needs_input:
                        out = bz2_dec.decompress(b"", max_length=step_cap)
                    elif comp_left > 0:
                        raw = raw_fileobj.read(min(read_step, comp_left))
                        if not raw:
                            raise EOFError(
                                f"Truncated bzip2 ZIP entry: {info.filename!r}"
                            )
                        comp_left -= len(raw)
                        out = bz2_dec.decompress(raw, max_length=step_cap)
                    else:
                        break
                    _record_chunk(out)

            expected_crc = getattr(info, "CRC", None)
            if (
                expected_crc is not None
                and (running_crc & 0xFFFFFFFF) != (expected_crc & 0xFFFFFFFF)
            ):
                raise zipfile.BadZipFile(f"Bad CRC-32 for file {info.filename!r}")
        else:
            if hasattr(stream, "_left"):
                stream._left = max(int(stream._left), eff_max_member + 1)
            while True:
                rem_budget = min(
                    eff_max_member - member_bytes,
                    eff_max_total - active_budget.total_bytes,
                )
                step_cap = min(read_step, max(1, rem_budget + 1))
                chunk = stream.read(step_cap)
                if not chunk:
                    break
                _record_chunk(chunk)

    return b"".join(chunks) if collect_bytes else b""


def safe_read_zip_member(
    archive: zipfile.ZipFile,
    name_or_info: str | zipfile.ZipInfo,
    *,
    max_member_bytes: int | None = None,
    max_total_bytes: int | None = None,
    max_ratio: float | None = None,
    budget: ZipReadBudget | None = None,
    chunk_size: int = ZIP_STREAM_CHUNK_BYTES,
) -> bytes:
    """Stream-read a ZIP entry in bounded chunks, tracking real decompressed bytes.

    Never trusts ``ZipInfo.file_size`` alone: aborts immediately if either the
    single-entry stream or the cumulative decompressed bytes read from ``archive``
    exceeds the configured limit.
    """
    info = (
        name_or_info
        if isinstance(name_or_info, zipfile.ZipInfo)
        else archive.getinfo(name_or_info)
    )
    active_budget = budget or _get_or_create_budget(
        archive,
        max_member_bytes=max_member_bytes,
        max_total_bytes=max_total_bytes,
        max_ratio=max_ratio,
    )
    eff_max_member = (
        active_budget.max_member_bytes
        if max_member_bytes is None
        else max_member_bytes
    )
    eff_max_total = (
        active_budget.max_total_bytes if max_total_bytes is None else max_total_bytes
    )
    eff_max_ratio = active_budget.max_ratio if max_ratio is None else max_ratio

    return _stream_zip_entry_bounded(
        archive,
        info,
        eff_max_member=eff_max_member,
        eff_max_total=eff_max_total,
        eff_max_ratio=eff_max_ratio,
        active_budget=active_budget,
        chunk_size=chunk_size,
        collect_bytes=True,
    )


def verify_zip_stream_limits(
    archive: zipfile.ZipFile,
    max_bytes: int = MAX_UNCOMPRESSED_ARCHIVE_BYTES,
    max_ratio: float = MAX_COMPRESSION_RATIO,
    max_entries: int = MAX_ARCHIVE_ENTRIES,
    chunk_size: int = ZIP_STREAM_CHUNK_BYTES,
) -> None:
    """Verify real streaming decompressed byte counts without buffering entries."""
    validate_zip_archive(
        archive,
        max_bytes=max_bytes,
        max_ratio=max_ratio,
        max_entries=max_entries,
    )
    stream_budget = ZipReadBudget(
        max_total_bytes=max_bytes,
        max_member_bytes=max_bytes,
        max_ratio=max_ratio,
    )
    for info in archive.infolist():
        if info.is_dir():
            continue
        if info.flag_bits & 0x1:
            # Password-encrypted entries cannot be decompressed without a password;
            # downstream parsers reject encrypted archives safely.
            continue
        try:
            _stream_zip_entry_bounded(
                archive,
                info,
                eff_max_member=max_bytes,
                eff_max_total=max_bytes,
                eff_max_ratio=max_ratio,
                active_budget=stream_budget,
                chunk_size=chunk_size,
                collect_bytes=False,
            )
        except ArchiveLimitError:
            raise
        except (
            NotImplementedError,
            RuntimeError,
            EOFError,
            zipfile.BadZipFile,
            OSError,
        ):
            continue


def validate_zip_content(content: bytes) -> None:
    if zipfile.is_zipfile(io.BytesIO(content)):
        with zipfile.ZipFile(io.BytesIO(content)) as archive:
            verify_zip_stream_limits(archive)
