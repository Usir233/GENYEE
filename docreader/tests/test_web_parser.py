import asyncio
import os
import threading
import time
import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

import docreader.parser.web_parser as web_parser
from docreader.parser.concurrency import ParserCancelled
from docreader.parser.web_parser import (
    StdWebParser,
    WebParseError,
    WebParser,
    _ScrapeResult,
    build_visible_text_fallback,
    extract_markdown_from_html,
    install_ssrf_route_guard,
)
from docreader.utils.ssrf import is_ssrf_safe_url, reset_ssrf_whitelist_cache_for_test


class TestWebParserHelpers(unittest.TestCase):
    def setUp(self) -> None:
        self._env_patch = patch.dict(
            os.environ,
            {"SSRF_WHITELIST": "", "SSRF_WHITELIST_EXTRA": ""},
            clear=False,
        )
        self._env_patch.start()
        reset_ssrf_whitelist_cache_for_test()

    def tearDown(self) -> None:
        self._env_patch.stop()
        reset_ssrf_whitelist_cache_for_test()

    def test_extract_markdown_empty_html(self):
        self.assertIsNone(extract_markdown_from_html(""))
        self.assertIsNone(extract_markdown_from_html("   "))

    def test_extract_markdown_article_html(self):
        html = """
        <html><head><title>Demo</title></head><body>
        <article><h1>Hello</h1><p>World paragraph with enough text for extraction.</p></article>
        </body></html>
        """
        md = extract_markdown_from_html(html)
        self.assertIsNotNone(md)
        assert md is not None
        self.assertIn("Hello", md)

    def test_build_fallback_too_short(self):
        self.assertIsNone(build_visible_text_fallback("short"))
        self.assertIsNone(build_visible_text_fallback(""))

    def test_build_fallback_with_title(self):
        text = "A" * 60
        md = build_visible_text_fallback(text, page_title="GENYEE")
        self.assertIsNotNone(md)
        assert md is not None
        self.assertTrue(md.startswith("# GENYEE"))
        self.assertIn(text, md)

    def test_build_fallback_without_title(self):
        text = "B" * 60
        md = build_visible_text_fallback(text, page_title="")
        self.assertEqual(md, text)

    def test_install_ssrf_route_guard_is_importable(self):
        self.assertTrue(callable(install_ssrf_route_guard))

    def test_redirect_target_blocked_before_navigation(self):
        safe, reason = is_ssrf_safe_url("http://127.0.0.1:39127/audit.txt")
        self.assertFalse(safe)
        self.assertTrue(reason)


class TestStdWebParserFailures(unittest.TestCase):
    """Scrape/parse failures must raise, not become indexable document body."""

    def _parser(self) -> StdWebParser:
        return StdWebParser(title="page")

    def test_empty_scrape_raises_instead_of_error_body(self):
        empty = _ScrapeResult(
            html="",
            visible_text="",
            page_title="",
            error="navigation failed: Timeout",
        )
        parser = self._parser()
        with (
            patch.object(parser, "scrape", new=AsyncMock(return_value=empty)),
            self.assertRaises(WebParseError) as ctx,
        ):
            parser.parse_into_text(b"https://example.com/blocked")
        message = str(ctx.exception)
        self.assertIn("https://example.com/blocked", message)
        self.assertIn("navigation failed", message)
        self.assertNotIn("Error parsing web page:", message)

    def test_empty_scrape_without_error_field_still_raises(self):
        empty = _ScrapeResult(html="", visible_text="", page_title="")
        parser = self._parser()
        with (
            patch.object(parser, "scrape", new=AsyncMock(return_value=empty)),
            self.assertRaises(WebParseError) as ctx,
        ):
            parser.parse_into_text(b"https://example.com/blank")
        self.assertIn("no HTML or visible text", str(ctx.exception))

    def test_unextractable_page_raises_instead_of_error_body(self):
        scrape = _ScrapeResult(
            html="<html><body><div></div></body></html>",
            visible_text="short",
            page_title="",
        )
        parser = self._parser()
        with (
            patch.object(parser, "scrape", new=AsyncMock(return_value=scrape)),
            patch(
                "docreader.parser.web_parser.extract_markdown_from_html",
                return_value=None,
            ),
            patch(
                "docreader.parser.web_parser.build_visible_text_fallback",
                return_value=None,
            ),
            self.assertRaises(WebParseError) as ctx,
        ):
            parser.parse_into_text(b"https://example.com/empty")
        self.assertIn("Failed to parse web page", str(ctx.exception))
        self.assertIn("https://example.com/empty", str(ctx.exception))

    def test_successful_scrape_returns_markdown_document(self):
        html = """
        <html><head><title>Demo</title></head><body>
        <article><h1>Hello</h1><p>World paragraph with enough text for extraction.</p></article>
        </body></html>
        """
        scrape = _ScrapeResult(
            html=html,
            visible_text="Hello World paragraph with enough text for extraction.",
            page_title="Demo",
        )
        parser = self._parser()
        with patch.object(parser, "scrape", new=AsyncMock(return_value=scrape)):
            doc = parser.parse_into_text(b"https://example.com/ok")
        self.assertTrue(doc.is_valid())
        self.assertNotIn("Error parsing web page:", doc.content)
        self.assertIn("Hello", doc.content)

    def test_pipeline_web_parser_does_not_index_scrape_error(self):
        empty = _ScrapeResult(
            html="",
            visible_text="",
            page_title="",
            error="URL blocked by SSRF guard: loopback",
        )
        pipeline = WebParser(title="page")
        with (
            patch.object(StdWebParser, "scrape", new=AsyncMock(return_value=empty)),
            self.assertRaises(WebParseError),
        ):
            pipeline.parse_into_text(b"https://example.com/ssrf")


class _FakeAsyncContext:
    def __init__(self, value):
        self.value = value

    async def __aenter__(self):
        return self.value

    async def __aexit__(self, *exc):
        return False


class _CancellableContext:
    def __init__(self) -> None:
        self.active = True

    def is_active(self) -> bool:
        return self.active

    def time_remaining(self) -> float:
        return 30.0


class TestWebParserConcurrencyAndSafety(unittest.TestCase):
    def test_install_ssrf_route_guard_offloads_dns_to_worker_thread(self):
        captured: dict[str, object] = {}

        async def _run() -> tuple[int, int]:
            loop_tid = threading.get_ident()
            call_tids: list[int] = []

            def fake_ssrf(url: str) -> tuple[bool, str]:
                call_tids.append(threading.get_ident())
                return True, ""

            async def register_route(pattern: str, handler) -> None:
                captured["pattern"] = pattern
                captured["handler"] = handler

            page = SimpleNamespace(route=register_route)
            await install_ssrf_route_guard(page)
            handler = captured["handler"]
            assert callable(handler)
            route = SimpleNamespace(
                request=SimpleNamespace(url="https://example.com/sub.js"),
                abort=AsyncMock(),
                continue_=AsyncMock(),
            )
            with patch("docreader.parser.web_parser.is_ssrf_safe_url", side_effect=fake_ssrf):
                await handler(route)
            route.continue_.assert_awaited_once()
            return loop_tid, call_tids[0]

        loop_tid, worker_tid = asyncio.run(_run())
        self.assertNotEqual(loop_tid, worker_tid)

    def test_scrape_closes_browser_when_post_navigation_step_raises(self):
        page = SimpleNamespace(
            goto=AsyncMock(return_value=SimpleNamespace(status=200)),
            title=AsyncMock(return_value="Title"),
            content=AsyncMock(return_value="<html><body>ok</body></html>"),
        )
        browser = SimpleNamespace(
            new_page=AsyncMock(return_value=page),
            close=AsyncMock(),
        )
        driver = SimpleNamespace(webkit=SimpleNamespace(launch=AsyncMock(return_value=browser)))
        proxy = _FakeAsyncContext(SimpleNamespace(url="http://127.0.0.1:12345"))
        with (
            patch("docreader.parser.web_parser.is_ssrf_safe_url", return_value=(True, "")),
            patch("docreader.parser.web_parser.SSRFProxy", return_value=proxy),
            patch(
                "docreader.parser.web_parser.async_playwright",
                return_value=_FakeAsyncContext(driver),
            ),
            patch("docreader.parser.web_parser.install_ssrf_route_guard", new=AsyncMock()),
            patch("docreader.parser.web_parser.wait_for_rendered_content", new=AsyncMock()),
            patch(
                "docreader.parser.web_parser.read_visible_text",
                new=AsyncMock(side_effect=RuntimeError("Execution context destroyed")),
            ),
        ):
            res = asyncio.run(StdWebParser(title="t").scrape("https://example.com"))

        self.assertIn("Execution context destroyed", res.error)
        browser.close.assert_awaited_once()

    def test_web_parser_enforces_worker_limit_and_propagates_task_cancellation(self):
        limit_calls: list[tuple[str, int]] = []
        orig_limit = (
            web_parser.parser_worker_limit if hasattr(web_parser, "parser_worker_limit") else None
        )

        from contextlib import contextmanager

        @contextmanager
        def tracking_limit(name: str, max_workers: int, timeout=None, context=None):
            limit_calls.append((name, max_workers))
            assert orig_limit is not None
            with orig_limit(name, max_workers, timeout=timeout, context=context):
                yield

        ctx = _CancellableContext()
        entered_goto = threading.Event()

        async def slow_goto(*args, **kwargs):
            entered_goto.set()
            await asyncio.sleep(10.0)
            return SimpleNamespace(status=200)

        page = SimpleNamespace(
            goto=slow_goto,
            title=AsyncMock(return_value="Title"),
            content=AsyncMock(return_value="<html></html>"),
        )
        browser = SimpleNamespace(
            new_page=AsyncMock(return_value=page),
            close=AsyncMock(),
        )
        driver = SimpleNamespace(webkit=SimpleNamespace(launch=AsyncMock(return_value=browser)))
        proxy = _FakeAsyncContext(SimpleNamespace(url="http://127.0.0.1:12345"))

        def cancel_soon() -> None:
            if entered_goto.wait(timeout=2.0):
                time.sleep(0.03)
                ctx.active = False

        canceller = threading.Thread(target=cancel_soon, daemon=True)
        canceller.start()
        t0 = time.monotonic()
        try:
            with (
                patch.object(web_parser, "parser_worker_limit", tracking_limit, create=True),
                patch("docreader.parser.web_parser.is_ssrf_safe_url", return_value=(True, "")),
                patch("docreader.parser.web_parser.SSRFProxy", return_value=proxy),
                patch(
                    "docreader.parser.web_parser.async_playwright",
                    return_value=_FakeAsyncContext(driver),
                ),
                patch("docreader.parser.web_parser.install_ssrf_route_guard", new=AsyncMock()),
            ):
                parser = StdWebParser(title="cancel-test", context=ctx)
                with self.assertRaises(ParserCancelled):
                    parser.parse_into_text(b"https://example.com/slow")
        finally:
            canceller.join(timeout=2.0)

        elapsed = time.monotonic() - t0
        self.assertLess(elapsed, 2.0)
        self.assertTrue(limit_calls and limit_calls[0][0] == "web")
        browser.close.assert_awaited_once()

    def test_scrape_rejects_oversized_html_payload(self):
        oversized_html = "<html><body>" + ("x" * 1024) + "</body></html>"
        page = SimpleNamespace(
            goto=AsyncMock(return_value=SimpleNamespace(status=200)),
            title=AsyncMock(return_value="Big"),
            content=AsyncMock(return_value=oversized_html),
        )
        browser = SimpleNamespace(
            new_page=AsyncMock(return_value=page),
            close=AsyncMock(),
        )
        driver = SimpleNamespace(webkit=SimpleNamespace(launch=AsyncMock(return_value=browser)))
        proxy = _FakeAsyncContext(SimpleNamespace(url="http://127.0.0.1:12345"))
        with (
            patch.object(web_parser, "MAX_WEB_HTML_BYTES", 256, create=True),
            patch("docreader.parser.web_parser.is_ssrf_safe_url", return_value=(True, "")),
            patch("docreader.parser.web_parser.SSRFProxy", return_value=proxy),
            patch(
                "docreader.parser.web_parser.async_playwright",
                return_value=_FakeAsyncContext(driver),
            ),
            patch("docreader.parser.web_parser.install_ssrf_route_guard", new=AsyncMock()),
            patch("docreader.parser.web_parser.wait_for_rendered_content", new=AsyncMock()),
            patch(
                "docreader.parser.web_parser.read_visible_text",
                new=AsyncMock(return_value="x" * 80),
            ),
            self.assertRaisesRegex(WebParseError, "exceeds"),
        ):
            StdWebParser(title="big").parse_into_text(b"https://example.com/big")
        browser.close.assert_awaited_once()


if __name__ == "__main__":
    unittest.main()
