"""Unit tests for OpenDataLoader parser helpers (no JVM required)."""

import os
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
from types import SimpleNamespace
from unittest import mock

from docreader.parser.concurrency import ParserCancelled
from docreader.parser.opendataloader_parser import (
    OpenDataLoaderParser,
    _collect_images_under_output,
    _find_markdown_file,
    _kill_process_group,
    _normalize_odl_image_url,
    _patched_odl_runner,
    _ping_hybrid,
    _rewrite_markdown_image_refs,
    _run_convert,
    _run_odl_jar_bounded,
    opendataloader_available,
)


class OpenDataLoaderHelpersTest(unittest.TestCase):
    def test_hybrid_health_probe_blocks_private_url_before_request(self):
        with (
            mock.patch(
                "docreader.parser.opendataloader_parser.is_ssrf_safe_url",
                return_value=(False, "restricted test address"),
            ),
            mock.patch("urllib.request.build_opener") as build_opener,
        ):
            ok, msg = _ping_hybrid("http://127.0.0.1:8080", retries=1)

        self.assertFalse(ok)
        self.assertIn("SSRF", msg)
        build_opener.assert_not_called()

    def test_convert_blocks_private_hybrid_url_at_final_sink(self):
        fake_module = mock.Mock()
        with (
            mock.patch.dict(sys.modules, {"opendataloader_pdf": fake_module}),
            self.assertRaisesRegex(RuntimeError, "SSRF"),
        ):
            _run_convert(
                "/tmp/input.pdf",
                "/tmp/output",
                "/tmp/output/images",
                overrides={
                    "odl_hybrid": "docling-fast",
                    "odl_hybrid_url": "http://169.254.169.254/latest/meta-data",
                },
            )

        fake_module.convert.assert_not_called()

    def test_find_markdown_prefers_stem_match(self):
        with tempfile.TemporaryDirectory() as d:
            other = os.path.join(d, "other.md")
            target = os.path.join(d, "paper.md")
            with open(other, "w") as f:
                f.write("x")
            with open(target, "w") as f:
                f.write("# Title")
            self.assertEqual(_find_markdown_file(d, "paper"), target)

    def test_collect_and_rewrite_images(self):
        with tempfile.TemporaryDirectory() as d:
            img_dir = os.path.join(d, "images")
            os.makedirs(img_dir)
            png = os.path.join(img_dir, "fig1.png")
            with open(png, "wb") as f:
                f.write(b"\x89PNG\r\n\x1a\n")
            images = _collect_images_under_output(d)
            self.assertIn("images/fig1.png", images)
            md = "See ![fig](images/fig1.png) and ![alt](./fig1.png)."
            out = _rewrite_markdown_image_refs(md, images)
            self.assertIn("![fig](images/fig1.png)", out)
            self.assertIn("![alt](images/fig1.png)", out)

    def test_rewrite_odl_angle_bracket_and_entity_urls(self):
        images = {"images/imageFile1.png": "e30="}
        for md_in in (
            "![image 1](<images/imageFile1.png>)",
            "![image 1](&lt;images/imageFile1.png&gt;)",
        ):
            out = _rewrite_markdown_image_refs(md_in, images)
            self.assertEqual("![image 1](images/imageFile1.png)", out)

    def test_normalize_odl_image_url(self):
        self.assertEqual(
            _normalize_odl_image_url("&lt;images/imageFile2.png&gt;"),
            "images/imageFile2.png",
        )
        self.assertEqual(
            _normalize_odl_image_url("<images/imageFile2.png>"),
            "images/imageFile2.png",
        )

    def test_rewrite_skips_data_uris(self):
        md = "![x](data:image/png;base64,abc)"
        self.assertEqual(_rewrite_markdown_image_refs(md, {"images/a.png": "e30="}), md)


class OpenDataLoaderParserTest(unittest.TestCase):
    @mock.patch("docreader.parser.opendataloader_parser.opendataloader_available")
    @mock.patch("docreader.parser.opendataloader_parser._run_convert")
    def test_parse_reads_markdown_and_images(self, mock_convert, mock_avail):
        mock_avail.return_value = (True, "")

        def fake_convert(pdf_path, output_dir, image_dir, overrides=None):
            stem = os.path.splitext(os.path.basename(pdf_path))[0]
            md_path = os.path.join(output_dir, f"{stem}.md")
            with open(md_path, "w") as f:
                f.write("# Hello\n\n![pic](images/pic.png)\n")
            os.makedirs(image_dir, exist_ok=True)
            with open(os.path.join(image_dir, "pic.png"), "wb") as f:
                f.write(b"png")

        mock_convert.side_effect = fake_convert

        parser = OpenDataLoaderParser(file_name="doc.pdf", file_type="pdf")
        doc = parser.parse_into_text(b"%PDF-1.4 fake")
        self.assertIn("# Hello", doc.content)
        self.assertIn("images/pic.png", doc.content)
        self.assertIn("images/pic.png", doc.images)
        self.assertEqual(doc.metadata.get("parser_engine"), "opendataloader")

    @mock.patch("docreader.parser.opendataloader_parser.shutil.which", return_value=None)
    def test_availability_requires_java(self, _which):
        with mock.patch(
            "docreader.parser.opendataloader_parser._package_available",
            return_value=(True, ""),
        ):
            ok, msg = opendataloader_available()
        self.assertFalse(ok)
        self.assertIn("Java", msg)

    def test_patched_odl_runner_is_thread_safe_under_overlapping_calls(self):
        orig_runner = mock.MagicMock(name="orig_run_jar")
        fake_runner_mod = SimpleNamespace(run_jar=orig_runner)
        fake_pkg = SimpleNamespace(runner=fake_runner_mod)

        t1_entered = threading.Event()
        t2_entered = threading.Event()
        allow_t1_exit = threading.Event()
        t1_exited = threading.Event()
        allow_t2_exit = threading.Event()
        errors: list[BaseException] = []

        def worker1() -> None:
            try:
                with _patched_odl_runner():
                    t1_entered.set()
                    assert allow_t1_exit.wait(timeout=3.0)
                t1_exited.set()
            except BaseException as exc:  # pragma: no cover
                errors.append(exc)

        def worker2() -> None:
            try:
                assert t1_entered.wait(timeout=3.0)
                with _patched_odl_runner():
                    t2_entered.set()
                    assert allow_t2_exit.wait(timeout=3.0)
            except BaseException as exc:  # pragma: no cover
                errors.append(exc)

        with mock.patch.dict(
            sys.modules,
            {
                "opendataloader_pdf": fake_pkg,
                "opendataloader_pdf.runner": fake_runner_mod,
            },
        ):
            th1 = threading.Thread(target=worker1)
            th2 = threading.Thread(target=worker2)
            th1.start()
            th2.start()
            self.assertTrue(t2_entered.wait(timeout=3.0))
            self.assertIs(fake_runner_mod.run_jar, _run_odl_jar_bounded)

            # Exiting worker1 while worker2 is still inside must keep bounded runner active
            allow_t1_exit.set()
            self.assertTrue(t1_exited.wait(timeout=3.0))
            self.assertIs(fake_runner_mod.run_jar, _run_odl_jar_bounded)

            # Exiting worker2 must restore the true original runner
            allow_t2_exit.set()
            th1.join(timeout=3.0)
            th2.join(timeout=3.0)
            self.assertEqual(errors, [])
            self.assertIs(fake_runner_mod.run_jar, orig_runner)

    def test_kill_process_group_reaps_process_after_timeout_kill(self):
        events: list[str] = []
        wait_calls = 0

        def fake_wait(timeout=None):
            nonlocal wait_calls
            wait_calls += 1
            events.append(f"wait:{timeout}")
            if wait_calls == 1:
                raise subprocess.TimeoutExpired(cmd="java", timeout=timeout or 1.0)
            return -9

        proc = mock.MagicMock()
        proc.wait.side_effect = fake_wait
        proc.kill.side_effect = lambda: events.append("kill")
        proc.stdout = mock.MagicMock()
        proc.stderr = mock.MagicMock()

        _kill_process_group(proc, pgid=None)

        # After initial kill + TimeoutExpired + final kill, wait() must be called to reap zombie
        self.assertGreaterEqual(wait_calls, 2)
        self.assertTrue(events[-1].startswith("wait:"))

    def test_ping_hybrid_and_parse_abort_promptly_on_context_cancellation(self):
        class _Ctx:
            def __init__(self) -> None:
                self.active = True

            def is_active(self) -> bool:
                return self.active

            def time_remaining(self) -> float:
                return 30.0

        ctx = _Ctx()
        attempts = 0

        def failing_open(_req, timeout=5.0):
            nonlocal attempts
            attempts += 1
            ctx.active = False
            raise urllib.error.URLError("hybrid starting")

        fake_opener = SimpleNamespace(open=failing_open)
        t0 = time.monotonic()
        with (
            mock.patch(
                "docreader.parser.opendataloader_parser._resolve_and_pin_target_ip",
                return_value=("127.0.0.1", "localhost"),
            ),
            mock.patch(
                "docreader.parser.opendataloader_parser._build_pinned_opener",
                return_value=fake_opener,
            ),
            self.assertRaises(ParserCancelled),
        ):
            _ping_hybrid(
                "http://localhost:5002",
                retries=6,
                retry_delay_sec=5.0,
                timeout_sec=5.0,
                context=ctx,
            )
        self.assertLess(time.monotonic() - t0, 1.0)
        self.assertEqual(attempts, 1)


if __name__ == "__main__":
    unittest.main()
