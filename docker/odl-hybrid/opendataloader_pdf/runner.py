"""Hardened low-level JAR runner for opendataloader-pdf.

Enforces:
- Bounded JVM heap (-Xmx, configurable via ODL_JAVA_MAX_HEAP_MB /
  DOCREADER_ODL_JAVA_MAX_HEAP_MB, default 1024 MB)
- Disabled automatic HTTP redirects (-Dhttp.followRedirects=false)
- Minimal clean subprocess environment (no leaked service tokens or cloud keys)
- Process-group isolation (start_new_session=True) with hard timeout and
  SIGTERM -> SIGKILL os.killpg cleanup
"""

from __future__ import annotations

import contextlib
import importlib.resources as resources
import os
import signal
import subprocess
import sys
import time
from typing import Any

_JAR_NAME = "opendataloader-pdf-cli.jar"
DEFAULT_TIMEOUT_SECONDS = 120
DEFAULT_MAX_HEAP_MB = 1024

_SAFE_ENV_KEYS = ("PATH", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "JAVA_HOME", "SYSTEMROOT")


def _positive_env_int(names: tuple[str, ...], default: int) -> int:
    for name in names:
        raw = os.getenv(name)
        if raw is None or not raw.strip():
            continue
        try:
            parsed = int(raw.strip())
            if parsed > 0:
                return parsed
        except ValueError:
            continue
    return default


def _build_clean_java_env() -> dict[str, str]:
    env: dict[str, str] = {}
    for key in _SAFE_ENV_KEYS:
        val = os.environ.get(key)
        if val:
            env[key] = val
    env.setdefault("PATH", "/usr/local/bin:/usr/bin:/bin")
    env.setdefault("LANG", "C.UTF-8")
    env["HOME"] = "/tmp"
    env["TMPDIR"] = "/tmp"
    return env


def _terminate_process_group(process: subprocess.Popen[Any], pgid: int | None) -> None:
    if pgid is None:
        with contextlib.suppress(OSError):
            process.kill()
    else:
        for sig, grace in ((signal.SIGTERM, 0.5), (signal.SIGKILL, 0.0)):
            with contextlib.suppress(OSError):
                os.killpg(pgid, sig)
            if grace > 0:
                deadline = time.monotonic() + grace
                with contextlib.suppress(subprocess.TimeoutExpired):
                    process.wait(timeout=grace)
                    rem = deadline - time.monotonic()
                    if rem > 0:
                        time.sleep(rem)
    try:
        process.wait(timeout=1.0)
    except subprocess.TimeoutExpired:
        with contextlib.suppress(OSError):
            process.kill()
        with contextlib.suppress(subprocess.TimeoutExpired, OSError):
            process.wait(timeout=1.0)
    finally:
        for pipe in (process.stdout, process.stderr):
            if pipe is not None:
                with contextlib.suppress(OSError):
                    pipe.close()


def build_java_command(
    jar_path: str,
    args: list[str],
    *,
    max_heap_mb: int | None = None,
) -> list[str]:
    heap_mb = max(
        128,
        int(
            max_heap_mb
            if max_heap_mb is not None
            else _positive_env_int(
                ("ODL_JAVA_MAX_HEAP_MB", "DOCREADER_ODL_JAVA_MAX_HEAP_MB"),
                DEFAULT_MAX_HEAP_MB,
            )
        ),
    )
    return [
        "java",
        f"-Xmx{heap_mb}m",
        "-Djava.awt.headless=true",
        "-Dapple.awt.UIElement=true",
        "-Dhttp.followRedirects=false",
        "-jar",
        str(jar_path),
        *args,
    ]


def run_jar(
    args: list[str],
    quiet: bool = False,
    *,
    timeout_seconds: int | None = None,
    max_heap_mb: int | None = None,
) -> str:
    """Run the opendataloader-pdf JAR with bounded heap, clean env, and hard timeout."""
    effective_timeout = max(
        1,
        int(
            timeout_seconds
            if timeout_seconds is not None
            else _positive_env_int(
                ("ODL_TIMEOUT_SECONDS", "DOCREADER_ODL_TIMEOUT_SECONDS"),
                DEFAULT_TIMEOUT_SECONDS,
            )
        ),
    )

    try:
        jar_ref = resources.files("opendataloader_pdf").joinpath("jar", _JAR_NAME)
        with resources.as_file(jar_ref) as jar_path:
            command = build_java_command(
                str(jar_path),
                args,
                max_heap_mb=max_heap_mb,
            )
            env = _build_clean_java_env()
            is_posix = os.name == "posix"

            process = subprocess.Popen(
                command,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE if quiet else subprocess.STDOUT,
                text=True,
                encoding="utf-8",
                errors="replace",
                env=env,
                start_new_session=is_posix,
            )
            pgid = process.pid if is_posix else None

            try:
                stdout, stderr = process.communicate(timeout=effective_timeout)
            except subprocess.TimeoutExpired as exc:
                _terminate_process_group(process, pgid)
                raise TimeoutError(
                    f"opendataloader-pdf CLI timed out after {effective_timeout}s"
                ) from exc

            if not quiet and stdout:
                sys.stdout.write(stdout)
                sys.stdout.flush()

            if process.returncode != 0:
                raise subprocess.CalledProcessError(
                    process.returncode,
                    command,
                    output=stdout,
                    stderr=stderr,
                )
            return stdout or ""

    except FileNotFoundError:
        print(
            "Error: 'java' command not found. Please ensure Java is installed and in PATH.",
            file=sys.stderr,
        )
        raise
    except subprocess.CalledProcessError as error:
        print("Error running opendataloader-pdf CLI.", file=sys.stderr)
        print(f"Return code: {error.returncode}", file=sys.stderr)
        if quiet:
            if error.stdout:
                print(f"Stdout: {error.stdout}", file=sys.stderr)
            if error.stderr:
                print(f"Stderr: {error.stderr}", file=sys.stderr)
        raise
