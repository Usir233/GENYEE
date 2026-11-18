"""Hardened LibreOffice (soffice) execution helpers.

Centralizes sandboxed conversion for legacy DOC, XLS/ET, and PPT documents:
- Minimal clean environment (strips GRPC_AUTH_TOKEN, storage AK/SK, DB secrets,
  and external HTTP(S)_PROXY; forces dead loopback proxy 127.0.0.1:1)
- Isolated temporary LibreOffice UserInstallation pre-seeded with
  registrymodifications.xcu disabling external link updates, untrusted OLE/URI
  links, and macros
- Automatic bubblewrap (bwrap) --unshare-net + minimal read-only filesystem
  isolation when bwrap is available and permitted by the kernel
- Process-group isolation (start_new_session=True) and os.killpg cleanup via
  SandboxExecutor on timeout
"""

from __future__ import annotations

import functools
import logging
import os
import re
import shutil
import subprocess
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)

BLOCKING_LOCAL_PROXY = "http://127.0.0.1:1"

_SAFE_ENV_KEYS = (
    "PATH",
    "LANG",
    "LC_ALL",
    "LC_CTYPE",
    "TZ",
    "SYSTEMROOT",
    "WINDIR",
)

_SYSTEM_RO_MOUNTS = (
    "/usr",
    "/bin",
    "/sbin",
    "/lib",
    "/lib64",
    "/lib32",
    "/etc/alternatives",
    "/etc/ld.so.cache",
    "/etc/ld.so.conf",
    "/etc/ld.so.conf.d",
    "/etc/fonts",
    "/etc/localtime",
    "/opt",
)

# Pre-seeded LibreOffice configuration locking down external links, OLE, and macros.
_LIBREOFFICE_LOCKDOWN_XCU = """<?xml version="1.0" encoding="UTF-8"?>
<oor:items xmlns:oor="http://openoffice.org/2001/registry"
           xmlns:xs="http://www.w3.org/2001/XMLSchema"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <item oor:path="/org.openoffice.Office.Common/Security/Scripting">
    <prop oor:name="MacroSecurityLevel" oor:op="fuse"><value>3</value></prop>
  </item>
  <item oor:path="/org.openoffice.Office.Common/Security/Scripting">
    <prop oor:name="DisableMacrosExecution" oor:op="fuse"><value>true</value></prop>
  </item>
  <item oor:path="/org.openoffice.Office.Common/Security/Scripting">
    <prop oor:name="BlockUntrustedRefererLinks" oor:op="fuse"><value>true</value></prop>
  </item>
  <item oor:path="/org.openoffice.Office.Writer/Content/Update">
    <prop oor:name="Link" oor:op="fuse"><value>0</value></prop>
  </item>
  <item oor:path="/org.openoffice.Office.Calc/Content/Update">
    <prop oor:name="Link" oor:op="fuse"><value>2</value></prop>
  </item>
</oor:items>
"""


def sanitize_temp_suffix(suffix: str, default: str = ".bin") -> str:
    """Normalize a file suffix to safe alphanumeric characters starting with '.'."""
    cleaned = re.sub(r"[^a-zA-Z0-9]", "", (suffix or "").lstrip("."))
    if not cleaned:
        return default
    return f".{cleaned[:16].lower()}"


def build_minimal_sandbox_env(
    proxy: str | None = None,
    profile_dir: str | None = None,
    base_env: Mapping[str, str] | None = None,
) -> dict[str, str]:
    """Build a minimal clean environment stripped of application secrets."""
    src = os.environ if base_env is None else base_env
    env: dict[str, str] = {}
    for key in _SAFE_ENV_KEYS:
        val = src.get(key)
        if val:
            env[key] = val

    env.setdefault("PATH", "/usr/local/bin:/usr/bin:/bin")
    env.setdefault("LANG", "C.UTF-8")

    safe_home = profile_dir if profile_dir else "/tmp"
    env["HOME"] = safe_home
    env["TMPDIR"] = safe_home
    env["TEMP"] = safe_home
    env["TMP"] = safe_home
    if profile_dir:
        env["XDG_CONFIG_HOME"] = os.path.join(profile_dir, ".config")
        env["XDG_CACHE_HOME"] = os.path.join(profile_dir, ".cache")

    env["SAL_USE_VCLPLUGIN"] = "svp"
    env["OOO_DISABLE_RECOVERY"] = "1"

    effective_proxy = proxy if proxy is not None else BLOCKING_LOCAL_PROXY
    if effective_proxy:
        env["http_proxy"] = effective_proxy
        env["https_proxy"] = effective_proxy
        env["HTTP_PROXY"] = effective_proxy
        env["HTTPS_PROXY"] = effective_proxy
        env["no_proxy"] = ""
        env["NO_PROXY"] = ""
    return env


def prepare_libreoffice_profile(profile_dir: str) -> str:
    """Populate an isolated LibreOffice UserInstallation disabling links & macros."""
    user_dir = os.path.join(profile_dir, "user")
    os.makedirs(user_dir, exist_ok=True)
    xcu_path = os.path.join(user_dir, "registrymodifications.xcu")
    with open(xcu_path, "w", encoding="utf-8") as handle:
        handle.write(_LIBREOFFICE_LOCKDOWN_XCU)
    return Path(profile_dir).resolve().as_uri()


def _path_is_within(child: str, parent: str) -> bool:
    try:
        child_real = os.path.realpath(child)
        parent_real = os.path.realpath(parent)
        return os.path.commonpath([child_real, parent_real]) == parent_real
    except ValueError:
        return False


def build_bwrap_command(
    cmd: Sequence[str],
    *,
    bwrap_path: str,
    work_dir: str,
    profile_dir: str | None = None,
    input_path: str | None = None,
) -> list[str]:
    """Wrap a command in bubblewrap with --unshare-net and minimal RO mounts.

    Note: intentionally omits --new-session so SandboxExecutor's
    start_new_session=True + os.killpg(pgid, ...) reaps the entire sandbox tree.
    """
    wrapped: list[str] = [
        bwrap_path,
        "--die-with-parent",
        "--unshare-net",
        "--unshare-ipc",
        "--unshare-uts",
        "--proc",
        "/proc",
        "--dev",
        "/dev",
        "--tmpfs",
        "/tmp",
    ]

    for sys_path in _SYSTEM_RO_MOUNTS:
        if os.path.islink(sys_path):
            target = os.readlink(sys_path)
            wrapped.extend(["--symlink", target, sys_path])
        elif os.path.exists(sys_path):
            wrapped.extend(["--ro-bind", sys_path, sys_path])

    work_abs = os.path.abspath(work_dir)
    wrapped.extend(["--bind", work_abs, work_abs])
    work_real = os.path.realpath(work_abs)
    if work_real != work_abs and os.path.exists(work_real):
        wrapped.extend(["--bind", work_real, work_real])

    if profile_dir:
        prof_abs = os.path.abspath(profile_dir)
        if prof_abs != work_abs:
            wrapped.extend(["--bind", prof_abs, prof_abs])
            prof_real = os.path.realpath(prof_abs)
            if prof_real not in (prof_abs, work_real) and os.path.exists(prof_real):
                wrapped.extend(["--bind", prof_real, prof_real])

    if input_path:
        in_abs = os.path.abspath(input_path)
        if not _path_is_within(in_abs, work_abs) and (
            not profile_dir or not _path_is_within(in_abs, os.path.abspath(profile_dir))
        ):
            wrapped.extend(["--ro-bind", in_abs, in_abs])
            in_real = os.path.realpath(in_abs)
            if in_real != in_abs and os.path.exists(in_real):
                wrapped.extend(["--ro-bind", in_real, in_real])

    wrapped.extend(["--chdir", work_abs, "--", *cmd])
    return wrapped


@functools.lru_cache(maxsize=4)
def _is_bwrap_usable(bwrap_path: str) -> bool:
    """Return True if bwrap can create network-isolated sandboxes on this host."""
    true_bin = shutil.which("true") or "/bin/true"
    if not os.path.exists(true_bin):
        return False
    try:
        probe = subprocess.run(
            [
                bwrap_path,
                "--die-with-parent",
                "--unshare-net",
                "--ro-bind",
                "/",
                "/",
                "--",
                true_bin,
            ],
            capture_output=True,
            timeout=3,
            env={"PATH": "/usr/bin:/bin"},
        )
        if probe.returncode == 0:
            return True
        logger.warning(
            "bwrap present at %s but unusable in current container/kernel (%s); "
            "falling back to clean-env + loopback-proxy isolation",
            bwrap_path,
            probe.stderr.decode("utf-8", errors="ignore").strip(),
        )
        return False
    except Exception as exc:
        logger.warning("bwrap probe failed (%s); falling back to process sandbox", exc)
        return False


def maybe_wrap_with_bwrap(
    cmd: Sequence[str],
    *,
    work_dir: str,
    profile_dir: str | None = None,
    input_path: str | None = None,
) -> list[str]:
    """Wrap cmd with bwrap (--unshare-net + minimal RO mounts) when available."""
    if os.name != "posix":
        return list(cmd)
    bwrap_path = shutil.which("bwrap")
    if not bwrap_path or not _is_bwrap_usable(bwrap_path):
        return list(cmd)
    return build_bwrap_command(
        cmd,
        bwrap_path=bwrap_path,
        work_dir=work_dir,
        profile_dir=profile_dir,
        input_path=input_path,
    )


def build_soffice_convert_cmd(
    soffice_path: str,
    *,
    convert_to: str,
    outdir: str,
    input_path: str,
    profile_dir: str,
) -> list[str]:
    """Build hardened headless soffice conversion command."""
    user_installation = prepare_libreoffice_profile(profile_dir)
    return [
        soffice_path,
        "--headless",
        "--norestore",
        "--nolockcheck",
        "--nodefault",
        "--nofirststartwizard",
        f"-env:UserInstallation={user_installation}",
        "--convert-to",
        convert_to,
        "--outdir",
        os.path.abspath(outdir),
        os.path.abspath(input_path),
    ]


def run_soffice_convert(
    soffice_path: str,
    *,
    convert_to: str,
    outdir: str,
    input_path: str,
    profile_dir: str,
    executor: Any = None,
    timeout: int = 120,
    context: Any = None,
) -> tuple[bytes, bytes, int]:
    """Run headless LibreOffice conversion inside bwrap + SandboxExecutor."""
    from docreader.parser.doc_parser import SandboxExecutor

    cmd = build_soffice_convert_cmd(
        soffice_path,
        convert_to=convert_to,
        outdir=outdir,
        input_path=input_path,
        profile_dir=profile_dir,
    )
    sandboxed_cmd = maybe_wrap_with_bwrap(
        cmd,
        work_dir=outdir,
        profile_dir=profile_dir,
        input_path=input_path,
    )
    if executor is None:
        default_executor = SandboxExecutor(default_timeout=timeout, context=context)
        return default_executor.execute_in_sandbox(sandboxed_cmd)
    if isinstance(executor, SandboxExecutor):
        return executor.execute_in_sandbox(sandboxed_cmd, timeout=timeout, context=context)
    out, err, code = executor.execute_in_sandbox(sandboxed_cmd)
    return bytes(out), bytes(err), int(code)
