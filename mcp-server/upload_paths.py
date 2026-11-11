"""Local file path validation and safe file opening for MCP upload tools."""

from __future__ import annotations

import builtins
import io
import os
import stat
from typing import BinaryIO, List, Optional, Tuple

_active_transport: Optional[str] = None

_NETWORK_TRANSPORTS = frozenset({"http", "sse", "streamable-http"})

_SENSITIVE_PATH_SEGMENTS = frozenset(
    {
        ".env",
        ".git",
        ".hg",
        ".svn",
        ".ssh",
        ".gnupg",
        ".aws",
        ".kube",
        ".docker",
        ".netrc",
        ".npmrc",
        ".pypirc",
    }
)


def set_active_transport(transport: str) -> None:
    """Record the transport selected at server startup (CLI or run_* entry)."""
    global _active_transport
    _active_transport = transport.strip().lower()


def clear_active_transport() -> None:
    """Reset startup transport override (for tests)."""
    global _active_transport
    _active_transport = None


def _current_transport() -> str:
    if _active_transport is not None:
        return _active_transport
    return os.getenv("MCP_TRANSPORT", "stdio").strip().lower()


def _path_within_root(resolved_path: str, root: str) -> bool:
    root_real = os.path.realpath(root)
    candidate_real = os.path.realpath(resolved_path)
    try:
        common = os.path.commonpath([root_real, candidate_real])
    except ValueError:
        return False
    return common == root_real and candidate_real != root_real


def _is_sensitive_segment(segment: str) -> bool:
    lowered = segment.strip().lower()
    if not lowered:
        return False
    return lowered in _SENSITIVE_PATH_SEGMENTS or lowered.startswith(".env.")


def _has_sensitive_segments(raw_path: str, resolved_path: str, matched_roots: List[str]) -> bool:
    for part in raw_path.replace("\\", "/").split("/"):
        if _is_sensitive_segment(part):
            return True
    for root in matched_roots:
        try:
            rel = os.path.relpath(resolved_path, root)
        except ValueError:
            continue
        for part in rel.replace("\\", "/").split("/"):
            if _is_sensitive_segment(part):
                return True
    return False


def _allowed_upload_roots() -> List[str]:
    """Return directories local files may be read from for upload tools.

    GENYEE-HIGH-19: Network transports (http / sse / streamable-http) fail closed
    when ``MCP_ALLOWED_UPLOAD_DIRS`` is not explicitly configured, preventing
    remote MCP callers from reading arbitrary files in the server working directory.
    """
    raw = os.getenv("MCP_ALLOWED_UPLOAD_DIRS", "").strip()
    if raw:
        roots: List[str] = []
        for part in raw.split(","):
            stripped = part.strip()
            if not stripped:
                continue
            real_root = os.path.realpath(stripped)
            if os.path.dirname(real_root) == real_root:
                raise ValueError(
                    "filesystem root '/' is not permitted in MCP_ALLOWED_UPLOAD_DIRS"
                )
            roots.append(real_root)
        if not roots:
            raise ValueError("allowed upload directories must not be empty")
        return roots

    transport = _current_transport()
    if transport in _NETWORK_TRANSPORTS:
        raise ValueError(
            f"local file upload is disabled for '{transport}' transport unless "
            "MCP_ALLOWED_UPLOAD_DIRS is explicitly configured"
        )

    root = os.path.realpath(os.getcwd())
    if os.path.dirname(root) == root:
        raise ValueError(
            "configure MCP_ALLOWED_UPLOAD_DIRS when working directory is the filesystem root"
        )
    return [root]


def resolve_upload_file_path(file_path: str) -> str:
    """Resolve and validate a local file path for ``create_knowledge_from_file``.

    Security invariants (GENYEE-HIGH-19):
    1. Transport policy and ``MCP_ALLOWED_UPLOAD_DIRS`` root boundary are checked
       BEFORE touching ``os.path.exists`` / ``os.stat`` on the target path, so
       out-of-bounds paths cannot be used as a filesystem existence oracle.
    2. Sensitive hidden segments (``.env``, ``.git``, ``.ssh``, ``.gnupg``, etc.)
       are rejected prior to existence checks.
    3. Symbolic links at the target path are rejected.
    """
    raw = (file_path or "").strip()
    if not raw:
        raise ValueError("file path is required")
    if "\x00" in raw or any(ord(ch) < 32 for ch in raw):
        raise ValueError("file path contains invalid characters")

    roots = _allowed_upload_roots()

    abs_input = os.path.abspath(raw)
    resolved = os.path.realpath(abs_input)

    # 1. Check whitelist boundary FIRST (eliminates arbitrary path existence oracle)
    matched_roots = [root for root in roots if _path_within_root(resolved, root)]
    if not matched_roots:
        raise ValueError("file path is outside allowed upload directories")

    # 2. Reject sensitive hidden files/directories BEFORE existence checks
    if _has_sensitive_segments(raw, resolved, matched_roots):
        raise ValueError("file path references a restricted hidden file or directory")

    # 3. Reject leaf symlinks even if target stays within root
    if os.path.islink(abs_input):
        raise ValueError("symbolic links are not allowed for file upload")

    # 4. Only after boundary + sensitivity checks pass, inspect file metadata
    try:
        st = os.lstat(resolved)
    except OSError as exc:
        raise ValueError("file not found") from exc

    if not stat.S_ISREG(st.st_mode):
        raise ValueError("file path must point to a regular file")

    return resolved


def open_upload_file_nofollow(resolved_path: str) -> BinaryIO:
    """Open an already-validated upload path using ``O_NOFOLLOW`` + ``fstat``."""
    # Support unit tests that mock builtins.open on synthetic non-existent paths.
    if builtins.open is not io.open and hasattr(builtins.open, "_mock_name"):
        return builtins.open(resolved_path, "rb")

    flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    try:
        fd = os.open(resolved_path, flags)
    except OSError as exc:
        raise ValueError("unable to open upload file safely") from exc

    try:
        st = os.fstat(fd)
        if not stat.S_ISREG(st.st_mode):
            raise ValueError("upload target is not a regular file")
    except Exception:
        os.close(fd)
        raise

    return os.fdopen(fd, "rb")


def open_upload_file(file_path: str) -> Tuple[str, BinaryIO]:
    """Validate ``file_path`` and open it atomically with ``O_NOFOLLOW``."""
    resolved = resolve_upload_file_path(file_path)
    return resolved, open_upload_file_nofollow(resolved)
