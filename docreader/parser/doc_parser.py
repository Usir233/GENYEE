from __future__ import annotations

import contextlib
import logging
import os
import selectors
import shutil
import signal
import subprocess
import time
from collections.abc import Sequence
from typing import Any

from docreader.config import mask_proxy_url
from docreader.models.document import Document
from docreader.parser.archive import MAX_UNCOMPRESSED_ARCHIVE_BYTES, ArchiveLimitError
from docreader.parser.concurrency import ParserCancelled, check_cancellation
from docreader.parser.docx2_parser import Docx2Parser
from docreader.parser.soffice_sandbox import (
    BLOCKING_LOCAL_PROXY,
    build_minimal_sandbox_env,
    maybe_wrap_with_bwrap,
    run_soffice_convert,
)
from docreader.utils.tempfile import TempDirContext, TempFileContext

logger = logging.getLogger(__name__)

MAX_SANDBOX_OUTPUT_BYTES = 32 * 1024 * 1024
_POLL_INTERVAL_SECONDS = 0.1


def _cancellable_sleep(seconds: float, context: Any = None) -> None:
    """Sleep up to ``seconds`` while polling ``context`` for cancellation."""
    check_cancellation(context)
    if seconds <= 0:
        return
    if context is None:
        time.sleep(seconds)
        return
    deadline = time.monotonic() + seconds
    while True:
        check_cancellation(context)
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        time.sleep(min(_POLL_INTERVAL_SECONDS, remaining))
    check_cancellation(context)


class SandboxExecutor:
    """Sandbox executor for running commands with isolated clean environment."""

    def __init__(
        self,
        proxy: str | None = None,
        default_timeout: int = 60,
        *,
        max_output_bytes: int = MAX_SANDBOX_OUTPUT_BYTES,
        context: Any = None,
    ) -> None:
        """Initialize sandbox executor with configuration.

        Args:
            proxy: Proxy URL to use for network access. Defaults to dead loopback
                127.0.0.1:1 so document converters never inherit external proxies.
            default_timeout: Default timeout in seconds for command execution.
            max_output_bytes: Hard cap in bytes for captured stdout/stderr streams.
            context: Optional gRPC cancellation context polled during execution.
        """
        self.proxy = proxy or BLOCKING_LOCAL_PROXY
        self.default_timeout = max(1, int(default_timeout))
        self.max_output_bytes = max(1, int(max_output_bytes))
        self.context = context

    def execute_in_sandbox(
        self,
        cmd: list[str],
        *,
        timeout: int | None = None,
        context: Any = None,
    ) -> tuple[bytes, bytes, int]:
        """Execute command in sandbox with proxy configuration, timeout, and output bounds."""
        effective_context = self.context if context is None else context
        check_cancellation(effective_context)

        sandbox_methods = [
            self._execute_with_proxy,
        ]

        for method in sandbox_methods:
            try:
                return method(cmd, timeout=timeout, context=effective_context)
            except (ParserCancelled, ArchiveLimitError):
                raise
            except Exception as e:
                logger.warning("Sandbox method %s failed: %s", method.__name__, e)
                continue

        raise RuntimeError("All sandbox methods failed")

    def _execute_with_proxy(
        self,
        cmd: list[str],
        *,
        timeout: int | None = None,
        context: Any = None,
    ) -> tuple[bytes, bytes, int]:
        """Execute command with minimal clean environment and blocking proxy."""
        effective_timeout = max(1, int(timeout)) if timeout is not None else self.default_timeout
        effective_context = self.context if context is None else context
        check_cancellation(effective_context)

        env = build_minimal_sandbox_env(proxy=self.proxy)

        logger.info("Executing command with proxy: %s", " ".join(cmd))
        if self.proxy:
            logger.info("Using proxy: %s", mask_proxy_url(self.proxy))

        is_posix = os.name == "posix"
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            # On POSIX, start a fresh group so a timed-out command can be
            # terminated together with children it spawned (e.g. soffice.bin).
            start_new_session=is_posix,
        )
        # On POSIX start_new_session makes the direct child the group leader, so
        # retain this ID even if the direct child exits before cleanup starts.
        process_group_id = process.pid if is_posix else None

        stdout_fd: int | None = None
        stderr_fd: int | None = None
        if is_posix and process.stdout is not None and process.stderr is not None:
            with contextlib.suppress(Exception):
                s_fd = process.stdout.fileno()
                e_fd = process.stderr.fileno()
                if isinstance(s_fd, int) and isinstance(e_fd, int):
                    stdout_fd = s_fd
                    stderr_fd = e_fd

        try:
            if stdout_fd is not None and stderr_fd is not None:
                return self._communicate_posix_bounded(
                    process=process,
                    cmd=cmd,
                    stdout_fd=stdout_fd,
                    stderr_fd=stderr_fd,
                    timeout=effective_timeout,
                    context=effective_context,
                )
            return self._communicate_fallback(
                process=process,
                timeout=effective_timeout,
                context=effective_context,
            )
        except ParserCancelled:
            try:
                self._kill_timed_out_command(process, process_group_id)
            finally:
                self._reap_timed_out_process(process)
            raise
        except ArchiveLimitError:
            try:
                self._kill_timed_out_command(process, process_group_id)
            finally:
                self._reap_timed_out_process(process)
            raise
        except subprocess.TimeoutExpired as exc:
            # Terminate the POSIX process group (or the direct child elsewhere),
            # then reap with a bounded wait. Do not drain pipes here:
            # communicate() waits for EOF and can hang if a descendant left
            # the group (setsid) or otherwise kept a PIPE writer open.
            try:
                self._kill_timed_out_command(process, process_group_id)
            finally:
                self._reap_timed_out_process(process)
            raise RuntimeError(
                f"Command execution timeout after {effective_timeout} seconds"
            ) from exc

    def _communicate_posix_bounded(
        self,
        *,
        process: subprocess.Popen[bytes],
        cmd: list[str],
        stdout_fd: int,
        stderr_fd: int,
        timeout: int,
        context: Any,
    ) -> tuple[bytes, bytes, int]:
        deadline = time.monotonic() + timeout
        stdout_chunks: list[bytes] = []
        stderr_chunks: list[bytes] = []
        stdout_total = 0
        stderr_total = 0

        sel = selectors.DefaultSelector()
        try:
            sel.register(stdout_fd, selectors.EVENT_READ, data="stdout")
            if stderr_fd != stdout_fd:
                sel.register(stderr_fd, selectors.EVENT_READ, data="stderr")

            while sel.get_map():
                check_cancellation(context)
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise subprocess.TimeoutExpired(cmd, timeout)

                step = min(remaining, _POLL_INTERVAL_SECONDS)
                events = sel.select(timeout=step)
                for key, _mask in events:
                    try:
                        chunk = os.read(key.fd, 65536)
                    except OSError:
                        chunk = b""
                    if not chunk:
                        sel.unregister(key.fileobj)
                        continue
                    if key.data == "stdout":
                        stdout_total += len(chunk)
                        if stdout_total > self.max_output_bytes:
                            raise ArchiveLimitError(
                                f"Sandbox stdout exceeded limit ({self.max_output_bytes} bytes)"
                            )
                        stdout_chunks.append(chunk)
                    else:
                        stderr_total += len(chunk)
                        if stderr_total > self.max_output_bytes:
                            raise ArchiveLimitError(
                                f"Sandbox stderr exceeded limit ({self.max_output_bytes} bytes)"
                            )
                        stderr_chunks.append(chunk)

            while True:
                check_cancellation(context)
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise subprocess.TimeoutExpired(cmd, timeout)
                try:
                    process.wait(timeout=min(remaining, _POLL_INTERVAL_SECONDS))
                    break
                except subprocess.TimeoutExpired:
                    continue
        finally:
            sel.close()

        for pipe in (process.stdout, process.stderr):
            if pipe is not None:
                with contextlib.suppress(OSError):
                    pipe.close()

        return (
            b"".join(stdout_chunks),
            b"".join(stderr_chunks),
            int(process.returncode or 0),
        )

    def _communicate_fallback(
        self,
        *,
        process: subprocess.Popen[bytes],
        timeout: int,
        context: Any,
    ) -> tuple[bytes, bytes, int]:
        if context is None:
            stdout, stderr = process.communicate(timeout=timeout)
        else:
            deadline = time.monotonic() + timeout
            while True:
                check_cancellation(context)
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise subprocess.TimeoutExpired(process.args, timeout)
                try:
                    stdout, stderr = process.communicate(
                        timeout=min(remaining, _POLL_INTERVAL_SECONDS)
                    )
                    break
                except subprocess.TimeoutExpired:
                    continue

        out_bytes = stdout or b""
        err_bytes = stderr or b""
        if len(out_bytes) > self.max_output_bytes or len(err_bytes) > self.max_output_bytes:
            raise ArchiveLimitError(
                f"Sandbox output exceeded limit ({self.max_output_bytes} bytes)"
            )
        return out_bytes, err_bytes, int(process.returncode or 0)

    def _kill_timed_out_command(
        self, process: subprocess.Popen[Any], process_group_id: int | None
    ) -> None:
        if process_group_id is None:
            with contextlib.suppress(OSError):
                process.kill()
            return

        self._signal_process_group(process_group_id, signal.SIGTERM)
        grace_period = 0.5
        grace_deadline = time.monotonic() + grace_period
        with contextlib.suppress(subprocess.TimeoutExpired):
            process.wait(timeout=grace_period)
        remaining_grace_period = grace_deadline - time.monotonic()
        if remaining_grace_period > 0:
            time.sleep(remaining_grace_period)
        # The direct child can exit after SIGTERM while a descendant
        # ignores it. Always finish the grace period with SIGKILL.
        self._signal_process_group(process_group_id, signal.SIGKILL)

    @staticmethod
    def _signal_process_group(process_group_id: int, sig: int) -> None:
        with contextlib.suppress(OSError):
            os.killpg(process_group_id, sig)

    @staticmethod
    def _reap_timed_out_process(process: subprocess.Popen[Any], timeout: float = 1.0) -> None:
        try:
            process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            with contextlib.suppress(OSError):
                process.kill()
            try:
                process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                logger.warning(
                    "Timed-out command pid=%s did not exit after SIGKILL",
                    process.pid,
                )
        finally:
            for pipe in (process.stdout, process.stderr):
                if pipe is not None:
                    with contextlib.suppress(OSError):
                        pipe.close()


class DocParser(Docx2Parser):
    """DOC document parser."""

    def __init__(self, *args: Any, **kwargs: Any) -> None:
        """Initialize DOC parser with sandbox executor."""
        super().__init__(*args, **kwargs)
        self.sandbox_executor = SandboxExecutor(context=self.context)

    def parse_into_text(self, content: bytes) -> Document:
        logger.info("Parsing DOC document, content size: %d bytes", len(content))

        handle_chain = [
            # 1. Try to convert to docx format to extract images
            self._parse_with_docx,
            # 2. If image extraction is not needed or conversion failed,
            # try using antiword to extract text
            self._parse_with_antiword,
        ]

        # Save byte content as a temporary file
        with TempFileContext(content, ".doc") as temp_file_path:
            for handle in handle_chain:
                check_cancellation(self.context)
                try:
                    document = handle(temp_file_path)
                    if document and document.is_valid():
                        return document
                except (ArchiveLimitError, ParserCancelled):
                    raise
                except Exception as e:
                    logger.warning("Failed to parse DOC with %s %s", handle.__name__, e)

            return Document(content="")

    def _parse_with_docx(self, temp_file_path: str) -> Document:
        logger.info("Multimodal enabled, attempting to extract images from DOC")

        docx_content = self._try_convert_doc_to_docx(temp_file_path)
        if not docx_content:
            raise RuntimeError("Failed to convert DOC to DOCX")

        logger.info("Successfully converted DOC to DOCX, using DocxParser")
        # Use existing DocxParser to parse the converted docx
        document = super().parse_into_text(docx_content)
        logger.info("Extracted %d characters using DocxParser", len(document.content))
        return document

    def _parse_with_antiword(self, temp_file_path: str) -> Document:
        logger.info("Attempting to parse DOC file with antiword")

        # Check if antiword is installed
        antiword_path = self._try_find_antiword()
        if not antiword_path:
            raise RuntimeError("antiword not found in PATH")

        # Use antiword to extract text directly in sandbox
        with TempDirContext() as work_dir:
            cmd = maybe_wrap_with_bwrap(
                [antiword_path, os.path.abspath(temp_file_path)],
                work_dir=work_dir,
                input_path=temp_file_path,
            )
            logger.info("Executing antiword in sandbox with proxy configuration")

            stdout, stderr, returncode = self.sandbox_executor.execute_in_sandbox(cmd)

        if returncode != 0:
            raise RuntimeError(
                f"antiword extraction failed: {stderr.decode('utf-8', errors='ignore')}"
            )
        text = stdout.decode("utf-8", errors="ignore")
        logger.info("Successfully extracted %d characters using antiword", len(text))
        return Document(content=text)

    def _try_convert_doc_to_docx(self, doc_path: str) -> bytes | None:
        """Convert DOC file to DOCX format using hardened LibreOffice."""
        logger.info("Converting DOC to DOCX: %s", doc_path)

        soffice_path = self._try_find_soffice()
        if not soffice_path:
            return None

        logger.info("Using %s to convert DOC to DOCX", soffice_path)

        # LibreOffice shares a single user profile by default, so concurrent
        # `soffice` invocations contend for the same profile lock and the loser
        # silently fails to convert. Give each attempt a dedicated profile dir
        # and retry a few times so concurrent requests don't fall back to the
        # lower-fidelity antiword path.
        max_attempts = 3
        for attempt in range(1, max_attempts + 1):
            check_cancellation(self.context)
            with TempDirContext() as temp_dir, TempDirContext() as profile_dir:
                logger.info(
                    "Running hardened soffice conversion in sandbox (attempt %d/%d)",
                    attempt,
                    max_attempts,
                )
                try:
                    _stdout, stderr, returncode = run_soffice_convert(
                        soffice_path,
                        convert_to="docx",
                        outdir=temp_dir,
                        input_path=doc_path,
                        profile_dir=profile_dir,
                        executor=self.sandbox_executor,
                    )
                except (ArchiveLimitError, ParserCancelled):
                    raise
                except RuntimeError as exc:
                    logger.warning(
                        "Sandbox error converting DOC to DOCX (attempt %d/%d): %s",
                        attempt,
                        max_attempts,
                        exc,
                    )
                    return None

                if returncode != 0:
                    logger.warning(
                        "Error converting DOC to DOCX (attempt %d/%d): %s",
                        attempt,
                        max_attempts,
                        stderr.decode("utf-8", errors="ignore"),
                    )
                    if attempt < max_attempts:
                        _cancellable_sleep(0.5 * attempt, self.context)
                        continue
                    return None

                docx_files = [file for file in os.listdir(temp_dir) if file.endswith(".docx")]
                logger.info("Found %d DOCX file(s) in temporary directory", len(docx_files))
                for file in docx_files:
                    converted_file = os.path.join(temp_dir, file)
                    if os.path.islink(converted_file) or not os.path.isfile(converted_file):
                        continue
                    file_size = os.path.getsize(converted_file)
                    if file_size > MAX_UNCOMPRESSED_ARCHIVE_BYTES:
                        raise ArchiveLimitError(
                            f"Converted DOCX size ({file_size}) exceeds limit "
                            f"({MAX_UNCOMPRESSED_ARCHIVE_BYTES})"
                        )
                    logger.info("Found converted file: %s", converted_file)

                    with open(converted_file, "rb") as f:
                        docx_content = f.read()
                        logger.info("Successfully read DOCX file, size: %d", len(docx_content))
                        return docx_content

                logger.warning(
                    "No DOCX produced despite success (attempt %d/%d)",
                    attempt,
                    max_attempts,
                )
                if attempt < max_attempts:
                    _cancellable_sleep(0.5 * attempt, self.context)
        return None

    def _try_find_executable_path(
        self,
        executable_name: str,
        possible_path: Sequence[str] | None = None,
        environment_variable: Sequence[str] | None = None,
    ) -> str | None:
        """Find executable path via configured env vars, explicit paths, or PATH."""
        candidates: list[str] = []
        if environment_variable:
            for env_var in environment_variable:
                env_val = os.environ.get(env_var, "").strip()
                if env_val:
                    candidates.append(env_val)
        if possible_path:
            candidates.extend(p for p in possible_path if p)

        seen: set[str] = set()
        for path in candidates:
            if path in seen:
                continue
            seen.add(path)
            if os.path.isfile(path) and os.access(path, os.X_OK):
                logger.info("Found %s at %s", executable_name, path)
                return path

        found = shutil.which(executable_name)
        if found:
            logger.info("Found %s at %s", executable_name, found)
            return found

        logger.warning("Failed to find %s", executable_name)
        return None

    def _try_find_soffice(self) -> str | None:
        """Find LibreOffice/OpenOffice executable path."""
        possible_paths = [
            "/usr/bin/soffice",
            "/usr/lib/libreoffice/program/soffice",
            "/opt/libreoffice25.2/program/soffice",
            "/Applications/LibreOffice.app/Contents/MacOS/soffice",
            "C:\\Program Files\\LibreOffice\\program\\soffice.exe",
            "C:\\Program Files (x86)\\LibreOffice\\program\\soffice.exe",
        ]
        return self._try_find_executable_path(
            executable_name="soffice",
            possible_path=possible_paths,
            environment_variable=["LIBREOFFICE_PATH"],
        )

    def _try_find_antiword(self) -> str | None:
        """Find antiword executable path."""
        possible_paths = [
            "/usr/bin/antiword",
            "/usr/local/bin/antiword",
            "C:\\Program Files\\Antiword\\antiword.exe",
            "C:\\Program Files (x86)\\Antiword\\antiword.exe",
        ]
        return self._try_find_executable_path(
            executable_name="antiword",
            possible_path=possible_paths,
            environment_variable=["ANTIWORD_PATH"],
        )


if __name__ == "__main__":
    logging.basicConfig(level=logging.DEBUG)

    file_name = "/path/to/your/test.doc"
    logger.info("Processing file: %s", file_name)
    doc_parser = DocParser(
        file_name=file_name,
        enable_multimodal=True,
        chunk_size=512,
        chunk_overlap=60,
    )
    with open(file_name, "rb") as f:
        content = f.read()

    document = doc_parser.parse_into_text(content)
    logger.info("Processing complete, extracted text length: %d", len(document.content))
    logger.info("Sample text: %s...", document.content[:200])
