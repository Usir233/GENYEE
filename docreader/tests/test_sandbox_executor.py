import contextlib
import os
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import ANY, Mock, call, patch

from docreader.parser.doc_parser import SandboxExecutor


def _mock_process(pid=4321):
    process = Mock(pid=pid)
    process.stdout = Mock()
    process.stderr = Mock()
    return process


@unittest.skipUnless(os.name == "posix", "process-group cleanup is POSIX-specific")
class TestSandboxExecutorProcessGroup(unittest.TestCase):
    def test_timeout_kills_whole_process_group(self):
        """A timed-out command must terminate its parent and descendants."""
        with tempfile.TemporaryDirectory() as temp_dir:
            child_pid_file = Path(temp_dir) / "child.pid"
            command = [
                "sh",
                "-c",
                f"sleep 30 & echo $! > {shlex.quote(str(child_pid_file))}; wait",
            ]
            executor = SandboxExecutor(default_timeout=1)

            with self.assertRaises(RuntimeError):
                executor.execute_in_sandbox(command)

            child_pid = int(child_pid_file.read_text().strip())
            deadline = time.monotonic() + 1
            while True:
                try:
                    status_path = Path(f"/proc/{child_pid}/status")
                    if status_path.exists() and "State:\tZ (zombie)" in status_path.read_text():
                        break
                    os.kill(child_pid, 0)
                except ProcessLookupError:
                    break
                if time.monotonic() >= deadline:
                    self.fail("timed-out command left a child process running")
                time.sleep(0.01)

    def test_timeout_returns_when_descendant_leaves_process_group(self):
        """setsid descendants can survive killpg; cleanup must still return."""
        with tempfile.TemporaryDirectory() as temp_dir:
            child_pid_file = Path(temp_dir) / "child.pid"
            command = [
                sys.executable,
                "-c",
                (
                    "import os, time\n"
                    f"pid_file = {str(child_pid_file)!r}\n"
                    "if os.fork() == 0:\n"
                    "    os.setsid()\n"
                    "    with open(pid_file, 'w', encoding='utf-8') as handle:\n"
                    "        handle.write(str(os.getpid()))\n"
                    "    time.sleep(60)\n"
                    "time.sleep(30)\n"
                ),
            ]
            executor = SandboxExecutor(default_timeout=1)
            started = time.monotonic()
            with self.assertRaises(RuntimeError):
                executor.execute_in_sandbox(command)
            self.assertLess(
                time.monotonic() - started,
                4,
                "timeout cleanup hung waiting on an escaped descendant",
            )

            deadline = time.monotonic() + 2
            while not child_pid_file.exists() or not child_pid_file.stat().st_size:
                if time.monotonic() >= deadline:
                    self.fail("escaped child did not write its pid file")
                time.sleep(0.01)
            child_pid = int(child_pid_file.read_text().strip())
            try:
                os.kill(child_pid, 0)
            except ProcessLookupError:
                self.fail("expected setsid descendant to survive process-group kill")
            with contextlib.suppress(ProcessLookupError):
                os.kill(child_pid, signal.SIGKILL)


class TestSandboxExecutorMock(unittest.TestCase):
    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_terminates_group_and_reaps_process(self):
        process = _mock_process()
        process.communicate.side_effect = [subprocess.TimeoutExpired(["soffice"], 1)]
        process.wait.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 0.5),
            0,
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch("docreader.parser.doc_parser.subprocess.Popen", return_value=process) as popen,
            patch("docreader.parser.doc_parser.os.killpg") as killpg,
            patch("docreader.parser.doc_parser.time.monotonic", side_effect=[0, 0.5]),
            patch("docreader.parser.doc_parser.time.sleep") as sleep,
            self.assertRaisesRegex(RuntimeError, "timeout after 1"),
        ):
            SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        popen.assert_called_once_with(
            ["soffice"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=ANY,
            start_new_session=True,
        )
        killpg.assert_has_calls(
            [
                call(4321, signal.SIGTERM),
                call(4321, signal.SIGKILL),
            ]
        )
        sleep.assert_not_called()
        self.assertEqual(process.communicate.call_args_list, [call(timeout=1)])
        self.assertEqual(
            process.wait.call_args_list,
            [call(timeout=0.5), call(timeout=1.0)],
        )
        process.stdout.close.assert_called_once_with()
        process.stderr.close.assert_called_once_with()

    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_waits_full_grace_period_when_child_exits_early(self):
        process = _mock_process()
        process.communicate.side_effect = [subprocess.TimeoutExpired(["soffice"], 1)]
        process.wait.side_effect = [0, 0]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch("docreader.parser.doc_parser.subprocess.Popen", return_value=process),
            patch("docreader.parser.doc_parser.os.killpg") as killpg,
            patch("docreader.parser.doc_parser.time.monotonic", side_effect=[10, 10.2]),
            patch("docreader.parser.doc_parser.time.sleep") as sleep,
            self.assertRaisesRegex(RuntimeError, "timeout after 1"),
        ):
            SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        self.assertAlmostEqual(sleep.call_args.args[0], 0.3)
        killpg.assert_has_calls(
            [
                call(4321, signal.SIGTERM),
                call(4321, signal.SIGKILL),
            ]
        )
        self.assertEqual(process.communicate.call_args_list, [call(timeout=1)])
        self.assertEqual(
            process.wait.call_args_list,
            [call(timeout=0.5), call(timeout=1.0)],
        )

    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_reaps_when_group_already_exited(self):
        process = _mock_process()
        process.communicate.side_effect = [subprocess.TimeoutExpired(["soffice"], 1)]
        process.wait.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 0.5),
            0,
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch("docreader.parser.doc_parser.subprocess.Popen", return_value=process),
            patch(
                "docreader.parser.doc_parser.os.killpg",
                side_effect=ProcessLookupError,
            ) as killpg,
            patch("docreader.parser.doc_parser.time.monotonic", side_effect=[0, 0.5]),
            patch("docreader.parser.doc_parser.time.sleep") as sleep,
            self.assertRaisesRegex(RuntimeError, "timeout after 1"),
        ):
            SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        self.assertEqual(killpg.call_count, 2)
        sleep.assert_not_called()
        self.assertEqual(process.wait.call_args_list[-1], call(timeout=1.0))

    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_reaps_when_killpg_raises_permission_error(self):
        process = _mock_process()
        process.communicate.side_effect = [subprocess.TimeoutExpired(["soffice"], 1)]
        process.wait.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 0.5),
            0,
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch("docreader.parser.doc_parser.subprocess.Popen", return_value=process),
            patch(
                "docreader.parser.doc_parser.os.killpg",
                side_effect=PermissionError,
            ) as killpg,
            patch("docreader.parser.doc_parser.time.monotonic", side_effect=[0, 0.5]),
            patch("docreader.parser.doc_parser.time.sleep") as sleep,
            self.assertRaisesRegex(RuntimeError, "timeout after 1"),
        ):
            SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        self.assertEqual(killpg.call_count, 2)
        sleep.assert_not_called()
        self.assertEqual(process.wait.call_args_list[-1], call(timeout=1.0))
        process.stdout.close.assert_called_once_with()

    @unittest.skipUnless(os.name == "posix", "POSIX signals are required")
    def test_posix_timeout_returns_when_final_wait_times_out(self):
        process = _mock_process()
        process.communicate.side_effect = [subprocess.TimeoutExpired(["soffice"], 1)]
        process.wait.side_effect = [
            subprocess.TimeoutExpired(["soffice"], 0.5),
            subprocess.TimeoutExpired(["soffice"], 1.0),
            subprocess.TimeoutExpired(["soffice"], 1.0),
        ]

        with (
            patch("docreader.parser.doc_parser.os.name", "posix"),
            patch("docreader.parser.doc_parser.subprocess.Popen", return_value=process),
            patch("docreader.parser.doc_parser.os.killpg"),
            patch("docreader.parser.doc_parser.time.monotonic", side_effect=[0, 0.5]),
            patch("docreader.parser.doc_parser.time.sleep"),
            self.assertRaisesRegex(RuntimeError, "timeout after 1"),
        ):
            SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        self.assertEqual(process.kill.call_count, 1)
        process.stdout.close.assert_called_once_with()
        process.stderr.close.assert_called_once_with()

    def test_non_posix_timeout_kills_direct_child_and_reaps_process(self):
        process = _mock_process()
        process.communicate.side_effect = [subprocess.TimeoutExpired(["soffice"], 1)]
        process.wait.return_value = 0

        with (
            patch("docreader.parser.doc_parser.os.name", "nt"),
            patch("docreader.parser.doc_parser.subprocess.Popen", return_value=process) as popen,
            patch("docreader.parser.doc_parser.os.killpg", create=True) as killpg,
            self.assertRaisesRegex(RuntimeError, "timeout after 1"),
        ):
            SandboxExecutor(default_timeout=1)._execute_with_proxy(["soffice"])

        popen.assert_called_once_with(
            ["soffice"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=ANY,
            start_new_session=False,
        )
        process.kill.assert_called_once_with()
        killpg.assert_not_called()
        self.assertEqual(process.communicate.call_args_list, [call(timeout=1)])
        self.assertEqual(process.wait.call_args_list, [call(timeout=1.0)])
        process.stdout.close.assert_called_once_with()
        process.stderr.close.assert_called_once_with()


class TestSandboxBoundsAndOdlHybridRegressions(unittest.TestCase):
    def test_sandbox_executor_cancels_active_process_on_context_cancellation(self):
        import threading

        from docreader.parser.concurrency import ParserCancelled

        class _CancellableCtx:
            def __init__(self) -> None:
                self._active = True

            def is_active(self) -> bool:
                return self._active

            def time_remaining(self) -> float:
                return 30.0

            def cancel(self) -> None:
                self._active = False

        ctx = _CancellableCtx()
        timer = threading.Timer(0.15, ctx.cancel)
        timer.start()
        started = time.monotonic()
        try:
            executor = SandboxExecutor(default_timeout=30, context=ctx)
            with self.assertRaises(ParserCancelled):
                executor.execute_in_sandbox([sys.executable, "-c", "import time; time.sleep(30)"])
        finally:
            timer.cancel()
        self.assertLess(time.monotonic() - started, 3.0)

    def test_sandbox_executor_enforces_stdout_and_stderr_byte_bounds(self):
        from docreader.parser.archive import ArchiveLimitError

        executor = SandboxExecutor(default_timeout=5, max_output_bytes=4096)
        with self.assertRaises(ArchiveLimitError):
            executor.execute_in_sandbox(
                [sys.executable, "-c", "import sys; sys.stdout.buffer.write(b'x' * 16384)"]
            )

    def test_run_soffice_convert_honors_timeout_when_executor_passed(self):
        from docreader.parser.soffice_sandbox import run_soffice_convert

        executor = SandboxExecutor(default_timeout=60)
        captured: list[int | None] = []

        def fake_exec(cmd, *, timeout=None, context=None):
            captured.append(timeout)
            return b"", b"", 0

        with (
            tempfile.TemporaryDirectory() as outdir,
            tempfile.TemporaryDirectory() as profdir,
            patch.object(executor, "execute_in_sandbox", side_effect=fake_exec),
        ):
            in_file = Path(outdir) / "in.doc"
            in_file.write_bytes(b"doc")
            run_soffice_convert(
                "/usr/bin/soffice",
                convert_to="docx",
                outdir=outdir,
                input_path=str(in_file),
                profile_dir=profdir,
                executor=executor,
                timeout=120,
            )
        self.assertEqual(captured, [120])

    def test_odl_runner_terminate_process_group_reaps_after_final_kill(self):
        import importlib.util

        repo_root = Path(__file__).resolve().parents[2]
        runner_path = repo_root / "docker" / "odl-hybrid" / "opendataloader_pdf" / "runner.py"
        spec = importlib.util.spec_from_file_location("odl_runner_reap_test", runner_path)
        assert spec is not None and spec.loader is not None
        runner_mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(runner_mod)

        proc = _mock_process(pid=9876)
        proc.wait.side_effect = [
            subprocess.TimeoutExpired(["java"], 1.0),
            0,
        ]
        runner_mod._terminate_process_group(proc, None)
        self.assertEqual(proc.kill.call_count, 2)
        self.assertEqual(proc.wait.call_count, 2)
        proc.stdout.close.assert_called_once_with()
        proc.stderr.close.assert_called_once_with()

    def test_odl_hybrid_server_port_bounds_max_file_size_bytes_and_413_over_starlette_500(self):
        import asyncio
        import importlib.util
        from types import SimpleNamespace

        repo_root = Path(__file__).resolve().parents[2]
        server_path = (
            repo_root / "docker" / "odl-hybrid" / "opendataloader_pdf" / "hybrid_server.py"
        )
        spec = importlib.util.spec_from_file_location("odl_hybrid_server_bug_test", server_path)
        assert spec is not None and spec.loader is not None
        hybrid_mod = importlib.util.module_from_spec(spec)
        sys.modules["odl_hybrid_server_bug_test"] = hybrid_mod
        spec.loader.exec_module(hybrid_mod)

        self.assertEqual(hybrid_mod.MAX_FILE_SIZE, 100 * 1024 * 1024)

        with patch.dict(os.environ, {"ODL_HYBRID_MAX_FILE_SIZE_MB": "2"}, clear=False):
            wrapped_default = hybrid_mod.create_app(
                auth_token="s" * 32,
                bind_host="127.0.0.1",
                _inner_app=lambda scope, receive, send: None,
            )
            self.assertEqual(wrapped_default.config.max_file_size_bytes, 2 * 1024 * 1024)

        for bad_port in ("0", "-1", "65536", "not-an-int"):
            with self.assertRaises(SystemExit) as cm:
                hybrid_mod.main(["--port", bad_port])
            self.assertEqual(cm.exception.code, 2)

        # Simulate Starlette ServerErrorMiddleware catching Exception from receive()
        # and attempting to start an HTTP 500 response before re-raising/returning.
        async def starlette_like_inner(scope, receive, send):
            try:
                while True:
                    msg = await receive()
                    if not msg.get("more_body", False):
                        break
            except Exception:
                await send({"type": "http.response.start", "status": 500, "headers": []})
                await send({"type": "http.response.body", "body": b"Internal Server Error"})
                raise

        app = hybrid_mod.create_app(
            max_file_size=64,
            auth_token="hybrid-secret-token-0123456789",
            bind_host="0.0.0.0",
            _inner_app=starlette_like_inner,
        )

        async def run_oversized():
            sent = []
            chunks = [
                {"type": "http.request", "body": b"a" * 40, "more_body": True},
                {"type": "http.request", "body": b"b" * 40, "more_body": False},
            ]
            it = iter(chunks)

            async def receive():
                return next(it, {"type": "http.request", "body": b"", "more_body": False})

            async def send(msg):
                sent.append(msg)

            await app(
                {
                    "type": "http",
                    "path": "/v1/convert/file",
                    "method": "POST",
                    "headers": [(b"authorization", b"Bearer hybrid-secret-token-0123456789")],
                },
                receive,
                send,
            )
            return sent

        sent_msgs = asyncio.run(run_oversized())
        self.assertEqual(sent_msgs[0]["status"], 413)

        hybrid_mod._strip_profile_route(SimpleNamespace(router=None))
        routes = [SimpleNamespace(path="/v1/profile/file"), SimpleNamespace(path="/health")]
        hybrid_mod._strip_profile_route(SimpleNamespace(router=SimpleNamespace(routes=routes)))
        self.assertEqual([r.path for r in routes], ["/health"])


if __name__ == "__main__":
    unittest.main()
