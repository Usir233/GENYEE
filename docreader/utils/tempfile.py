import contextlib
import logging
import os
import tempfile
from typing import Any, Literal

logger = logging.getLogger(__name__)


def _sanitize_suffix(suffix: str) -> str:
    raw = str(suffix or "").replace("\x00", "").replace("\\", "/")
    base = os.path.basename(raw).strip()
    if base in {"", ".", ".."}:
        return ""
    return base


class TempFileContext:
    def __init__(self, file_content: bytes, suffix: str) -> None:
        """Initialize the temporary file context."""
        self.file_content = file_content
        self.suffix = _sanitize_suffix(suffix)
        self.file: Any = None
        self.temp_file: Any = None

    def _cleanup(self) -> None:
        """Close and unlink the temp file. close() may re-raise a prior flush
        error (ENOSPC), so unlink must still run and cleanup errors must not
        replace the original exception.
        """
        temp_file = getattr(self, "temp_file", None)
        if not temp_file:
            return
        with contextlib.suppress(OSError):
            temp_file.close()
        with contextlib.suppress(OSError):
            os.remove(temp_file.name)

    def __enter__(self) -> str:
        """Create file when entering context."""
        self.temp_file = tempfile.NamedTemporaryFile(suffix=self.suffix, delete=False)
        # If write/flush fails (e.g. ENOSPC), __exit__ is never called because
        # the exception escapes __enter__, so clean up here or the file leaks
        # permanently (delete=False means GC will not remove it either).
        try:
            self.temp_file.write(self.file_content)
            self.temp_file.flush()
            self.temp_file.close()
        except BaseException:
            self._cleanup()
            raise
        name = str(self.temp_file.name)
        logger.info("Saved %s content to temporary file: %s", self.suffix, name)
        return name

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> Literal[False]:
        """Delete file when exiting context."""
        del exc_type, exc_val, exc_tb
        if getattr(self, "temp_file", None):
            name = str(self.temp_file.name)
            self._cleanup()
            logger.info("File %s has been deleted.", name)
        return False


class TempDirContext:
    def __init__(self) -> None:
        """Initialize the temporary directory context."""
        self.temp_dir: tempfile.TemporaryDirectory[str] | None = None

    def __enter__(self) -> str:
        """Create directory when entering context."""
        self.temp_dir = tempfile.TemporaryDirectory(ignore_cleanup_errors=True)
        logger.info("Created temporary directory: %s", self.temp_dir.name)
        return self.temp_dir.name

    def __exit__(self, exc_type: Any, exc_val: Any, exc_tb: Any) -> Literal[False]:
        """Delete directory when exiting context."""
        del exc_type, exc_val, exc_tb
        if self.temp_dir and os.path.exists(self.temp_dir.name):
            name = self.temp_dir.name
            self.temp_dir.cleanup()
            logger.info("Directory %s has been deleted.", name)
        return False


if __name__ == "__main__":
    example_bytes = b"Hello, this is a test file."
    file_name = "test_file.txt"

    with TempFileContext(example_bytes, file_name) as temp_path:
        print(f"Does file {temp_path} exist: {os.path.exists(temp_path)}")
