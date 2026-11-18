import asyncio
import contextlib
import ipaddress
import os
import unittest
from unittest.mock import patch

from docreader.utils.ssrf import reset_ssrf_whitelist_cache_for_test
from docreader.utils.ssrf_proxy import SSRFProxy, checked_address


class TestSSRFProxy(unittest.IsolatedAsyncioTestCase):
    async def test_dns_rebinding_is_checked_before_connect(self):
        for address in (
            "127.0.0.1",
            "64:ff9b:1::a9fe:a9fe",
            "64:ff9b:1::808:808",
            "64:ff9b:1:ffff:ffff:ffff:ffff:ffff",
        ):
            with (
                self.subTest(address=address),
                patch("docreader.utils.ssrf_proxy.is_ssrf_safe_url", return_value=(True, "")),
                patch(
                    "docreader.utils.ssrf_proxy._resolve_host_ips",
                    return_value=((ipaddress.ip_address(address),), None),
                ),
                patch("docreader.utils.ssrf_proxy._is_whitelisted", return_value=False),
                self.assertRaises(ValueError),
            ):
                await checked_address("https://rebind.example/")

    async def test_proxy_refuses_restricted_connect(self):
        with patch.dict(os.environ, {"SSRF_WHITELIST": "", "SSRF_WHITELIST_EXTRA": ""}):
            reset_ssrf_whitelist_cache_for_test()
            async with SSRFProxy() as proxy:
                port = int(proxy.url.rsplit(":", 1)[1])
                reader, writer = await asyncio.open_connection("127.0.0.1", port)
                writer.write(b"CONNECT 127.0.0.1:443 HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
                await writer.drain()
                self.assertIn(b"403 Forbidden", await reader.read())
                writer.close()
                await writer.wait_closed()
            reset_ssrf_whitelist_cache_for_test()

    async def test_upstream_proxy_receives_numeric_connect(self):
        seen: list[bytes] = []

        async def upstream(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
            try:
                seen.append(await reader.readuntil(b"\r\n\r\n"))
                writer.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                await writer.drain()
            finally:
                writer.close()

        server = await asyncio.start_server(upstream, "127.0.0.1", 0)
        assert server.sockets
        port = server.sockets[0].getsockname()[1]
        try:
            proxy = SSRFProxy(f"http://127.0.0.1:{port}")
            _reader, writer = await proxy._connect("8.8.8.8", 443)
            self.assertIn(b"CONNECT 8.8.8.8:443 HTTP/1.1", seen[0])
            writer.close()
            await writer.wait_closed()
        finally:
            server.close()
            await server.wait_closed()

    async def test_proxy_rejects_bare_newline_header_smuggling(self):
        forwarded_heads: list[bytes] = []

        async def origin(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
            try:
                forwarded_heads.append(await reader.readuntil(b"\r\n\r\n"))
                writer.write(b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
                await writer.drain()
            finally:
                writer.close()

        origin_server = await asyncio.start_server(origin, "127.0.0.1", 0)
        assert origin_server.sockets
        origin_port = origin_server.sockets[0].getsockname()[1]
        whitelist_env = {"SSRF_WHITELIST": "127.0.0.1", "SSRF_WHITELIST_EXTRA": ""}
        try:
            with patch.dict(os.environ, whitelist_env):
                reset_ssrf_whitelist_cache_for_test()
                try:
                    async with SSRFProxy() as proxy:
                        proxy_port = int(proxy.url.rsplit(":", 1)[1])
                        reader, writer = await asyncio.open_connection("127.0.0.1", proxy_port)
                        smuggled = (
                            f"GET http://127.0.0.1:{origin_port}/ HTTP/1.1\r\n".encode("ascii")
                            + f"Host: 127.0.0.1:{origin_port}\r\n".encode("ascii")
                            + b"X-Smuggle: a\nHost: evil.internal\r\n\r\n"
                        )
                        writer.write(smuggled)
                        await writer.drain()
                        resp = await reader.read()
                        self.assertIn(b"403 Forbidden", resp)
                        self.assertEqual(forwarded_heads, [])
                        writer.close()
                        await writer.wait_closed()
                finally:
                    reset_ssrf_whitelist_cache_for_test()
        finally:
            origin_server.close()
            await origin_server.wait_closed()

    async def test_webkit_redirects_and_subresources_use_proxy(self):
        secret_hits: list[bytes] = []
        connections: set[asyncio.StreamWriter] = set()

        async def serve(reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
            connections.add(writer)
            try:
                head = await reader.readuntil(b"\r\n\r\n")
                path = head.split(b" ")[1]
                if path == b"/blocked":
                    location = f"http://localhost:{port}/secret".encode()
                    reply = (
                        b"HTTP/1.1 302 Found\r\nLocation: "
                        + location
                        + b"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"
                    )
                elif path == b"/redirect":
                    reply = (
                        b"HTTP/1.1 302 Found\r\nLocation: /ok\r\n"
                        b"Content-Length: 0\r\nConnection: close\r\n\r\n"
                    )
                else:
                    if path == b"/secret":
                        secret_hits.append(path)
                    content = (
                        f'<html>public content<img src="http://localhost:{port}/secret"></html>'
                    ).encode()
                    reply = (
                        b"HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nConnection: close\r\n"
                        b"Content-Length: " + str(len(content)).encode() + b"\r\n\r\n" + content
                    )
                writer.write(reply)
                await writer.drain()
            finally:
                writer.close()
                connections.discard(writer)

        def exercise_via_http_client(proxy_url: str, target_port: int) -> None:
            import requests

            with requests.Session() as session:
                session.trust_env = False
                session.proxies = {"http": proxy_url, "https": proxy_url}
                ok_resp = session.get(f"http://127.0.0.1:{target_port}/redirect", timeout=5)
                self.assertEqual(ok_resp.status_code, 200)
                self.assertTrue(ok_resp.url.endswith("/ok"))
                self.assertIn("public content", ok_resp.text)
                blocked_redirect = session.get(f"http://127.0.0.1:{target_port}/blocked", timeout=5)
                self.assertEqual(blocked_redirect.status_code, 403)
                blocked_subresource = session.get(
                    f"http://localhost:{target_port}/secret", timeout=5
                )
                self.assertEqual(blocked_subresource.status_code, 403)

        server = await asyncio.start_server(serve, "127.0.0.1", 0)
        assert server.sockets
        port = server.sockets[0].getsockname()[1]
        whitelist_env = {"SSRF_WHITELIST": "127.0.0.1", "SSRF_WHITELIST_EXTRA": ""}
        try:
            with patch.dict(os.environ, whitelist_env):
                reset_ssrf_whitelist_cache_for_test()
                async with SSRFProxy() as proxy:
                    browser = None
                    try:
                        from playwright.async_api import async_playwright

                        async with async_playwright() as p:
                            browser = await p.webkit.launch(
                                proxy={"server": proxy.url, "bypass": ""}
                            )
                            try:
                                page = await browser.new_page(service_workers="block")
                                await page.goto(
                                    f"http://127.0.0.1:{port}/redirect", wait_until="load"
                                )
                                self.assertTrue(page.url.endswith("/ok"))
                                self.assertIn("public content", await page.content())
                                with contextlib.suppress(Exception):
                                    await page.goto(
                                        f"http://127.0.0.1:{port}/blocked", wait_until="load"
                                    )
                            finally:
                                await browser.close()
                    except Exception as e:
                        if (
                            isinstance(e, ImportError)
                            or "Host system is missing dependencies" in str(e)
                            or "Executable doesn't exist" in str(e)
                        ):
                            await asyncio.to_thread(exercise_via_http_client, proxy.url, port)
                        else:
                            raise
                    self.assertEqual(secret_hits, [])
        finally:
            server.close()
            await server.wait_closed()
            for writer in connections:
                writer.close()
            reset_ssrf_whitelist_cache_for_test()
