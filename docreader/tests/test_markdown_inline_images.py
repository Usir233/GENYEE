"""Inline Markdown examples must not become extracted image resources."""

import base64
from pathlib import Path
import shutil
import struct
import subprocess
import tarfile
import tempfile
import time
import unittest
from unittest import mock
import zlib

from PIL import Image

from docreader.parser import markdown_parser
from docreader.parser.markdown_parser import MarkdownImageUtil
from docreader.parser.parser import Parser

REPO_ROOT = Path(__file__).resolve().parents[2]

PNG_B64 = (
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4"
    "z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)
IMAGE = f"![example](data:image/png;base64,{PNG_B64})"


def _make_png_bomb_bytes(width: int = 10000, height: int = 10000) -> bytes:
    """Build a valid 1-bit grayscale PNG whose pixel count exceeds 40M."""

    def chunk(tag: bytes, payload: bytes) -> bytes:
        crc = zlib.crc32(tag + payload) & 0xFFFFFFFF
        return struct.pack(">I", len(payload)) + tag + payload + struct.pack(">I", crc)

    ihdr = struct.pack(">IIBBBBB", width, height, 1, 0, 0, 0, 0)
    row_bytes = 1 + (width + 7) // 8
    idat = zlib.compress(b"\x00" * (row_bytes * height), level=1)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", idat)
        + chunk(b"IEND", b"")
    )


class MarkdownInlineImageTests(unittest.TestCase):
    def parse(self, text):
        return Parser().parse_file("example.md", "md", text.encode())

    def test_literal_examples_survive_public_file_parser(self):
        for code in (
            f"`{IMAGE}`",
            f"`` {IMAGE} ` literal ``",
            f"`first line\n{IMAGE}\nlast line`",
            "`![example](data:image/png;base64,a)`",
            f"`{IMAGE}\\`",
            f"\\``{IMAGE}`",
        ):
            with self.subTest(code=code):
                raw = f"Use {code} in your documentation.\n"
                doc = self.parse(raw)
                self.assertEqual(doc.content, raw)
                self.assertEqual(doc.images, {})

    def test_only_real_images_are_extracted(self):
        code = f"`{IMAGE}`"
        raw = f"{IMAGE} and {code} and {IMAGE}"
        doc = self.parse(raw)
        self.assertIn(code, doc.content)
        self.assertEqual(len(doc.images), 2)
        for path, payload in doc.images.items():
            self.assertIn(f"![example]({path})", doc.content)
            self.assertEqual(payload, PNG_B64)

    def test_unmatched_and_escaped_openers_do_not_hide_images(self):
        for raw in (
            f"`{IMAGE}",
            f"``{IMAGE}`",
            f"\\`{IMAGE}`",
            f"`unmatched\n\n{IMAGE}\n\n`",
        ):
            with self.subTest(raw=raw):
                doc = self.parse(raw)
                self.assertEqual(len(doc.images), 1)

    def test_even_backslashes_still_allow_code(self):
        raw = f"\\\\`{IMAGE}`"
        doc = self.parse(raw)
        self.assertEqual(doc.content, raw)
        self.assertEqual(doc.images, {})

    def test_code_in_alt_text_does_not_hide_real_image(self):
        raw = IMAGE.replace("[example]", "[a `literal` example]")
        doc = self.parse(raw)
        self.assertEqual(len(doc.images), 1)
        path = next(iter(doc.images))
        self.assertEqual(doc.content, f"![a `literal` example]({path})")

    def test_replace_false_ignores_code_resources_too(self):
        raw = f"`{IMAGE}` and {IMAGE}"
        text, images = MarkdownImageUtil().extract_base64(raw, replace=False)
        self.assertEqual(text, raw)
        self.assertEqual(list(images.values()), [base64.b64decode(PNG_B64)])

    def test_inline_and_fenced_examples_together(self):
        raw = f"`{IMAGE}`\n\n```md\n{IMAGE}\n```\n\n{IMAGE}"
        doc = self.parse(raw)
        self.assertIn(f"`{IMAGE}`", doc.content)
        self.assertIn(f"```md\n{IMAGE}\n```", doc.content)
        self.assertEqual(len(doc.images), 1)

    def test_helper_preserves_multiline_crlf_code(self):
        raw = f"`first\r\n{IMAGE}\r\nlast`\r\n"
        self.assertEqual(MarkdownImageUtil().extract_base64(raw), (raw, {}))

    def test_up_doc_py_redos_linear_time_and_inline_code_unclosed_opener(self):
        util = MarkdownImageUtil()
        for label, malformed in (
            ("unclosed-b64", "![a](data:image/png;base64,AAAA" * 2000),
            ("missing-semi", "![a](data:image/foo" * 2000 + ")"),
            ("unclosed-img", "![a](img.png" * 2000),
        ):
            with self.subTest(case=label):
                t0 = time.perf_counter()
                if label == "unclosed-img":
                    text, extracted = util.extract_image(malformed)
                    self.assertEqual(text, malformed)
                    self.assertEqual(extracted, [])
                    replaced = util.replace_path(malformed, {"img.png": "https://cdn.example/a.png"})
                    self.assertEqual(replaced, malformed)
                else:
                    text, extracted = util.extract_base64(malformed)
                    self.assertEqual(text, malformed)
                    self.assertEqual(extracted, {})
                elapsed = time.perf_counter() - t0
                self.assertLess(elapsed, 0.25, f"{label} took {elapsed:.3f}s (ReDoS)")

        # An unclosed "![" inside an inline code span must not swallow a real image after it.
        mixed = f"Use `![alt` syntax before {IMAGE}\n"
        doc = self.parse(mixed)
        self.assertEqual(len(doc.images), 1)
        img_path = next(iter(doc.images))
        self.assertEqual(doc.content, f"Use `![alt` syntax before ![example]({img_path})\n")

        # An unclosed prose "![" on a preceding line must not swallow a real image after it.
        unclosed_prose = f"![broken opener\n{IMAGE}\n"
        doc2 = self.parse(unclosed_prose)
        self.assertEqual(len(doc2.images), 1)
        img_path2 = next(iter(doc2.images))
        self.assertEqual(doc2.content, f"![broken opener\n![example]({img_path2})\n")

    def test_up_doc_py_inline_image_size_cap_pixel_bomb_and_raster_mime_allowlist(self):
        self.assertEqual(Image.MAX_IMAGE_PIXELS, 40_000_000)
        util = MarkdownImageUtil()

        # 1. Pre-decode base64 size cap must reject BEFORE calling endecode.encode_image.
        with (
            mock.patch.object(markdown_parser, "MAX_INLINE_IMAGE_BYTES", 1024, create=True),
            mock.patch("docreader.parser.markdown_parser.endecode.encode_image") as mock_decode,
        ):
            oversized = "![big](data:image/png;base64," + ("A" * 100_000) + ")"
            out_text, out_imgs = util.extract_base64(oversized)
            self.assertEqual(out_text, "big")
            self.assertEqual(out_imgs, {})
            mock_decode.assert_not_called()

        # 2. Decompression bomb (> 40,000,000 pixels) must be rejected.
        bomb_b64 = base64.b64encode(_make_png_bomb_bytes(10000, 10000)).decode("ascii")
        bomb_md = f"![bomb](data:image/png;base64,{bomb_b64})"
        doc_bomb = self.parse(bomb_md)
        self.assertEqual(doc_bomb.content, "bomb")
        self.assertEqual(doc_bomb.images, {})

        # 3. SVG / vector / path-traversal MIME subtypes and spoofed non-raster payloads must be rejected.
        svg_b64 = base64.b64encode(
            b'<svg xmlns="http://www.w3.org/2000/svg"><image href="http://169.254.169.254/latest/meta-data/"/></svg>'
        ).decode("ascii")
        for bad_md in (
            f"![svg](data:image/svg+xml;base64,{svg_b64})",
            f"![emf](data:image/x-emf;base64,{PNG_B64})",
            f"![trav](data:image/../../evil.png;base64,{PNG_B64})",
            f"![spoof](data:image/png;base64,{svg_b64})",
        ):
            with self.subTest(bad_md=bad_md[:40]):
                doc_bad = self.parse(bad_md)
                self.assertEqual(doc_bad.images, {})
                self.assertNotIn("data:image/", doc_bad.content)
                self.assertNotIn("..", doc_bad.content)

    def test_up_ops_docreader_workflow_enforces_grpc_auth_and_shell_hygiene(self):
        wf = (REPO_ROOT / ".github/workflows/docreader.yml").read_text(encoding="utf-8")
        self.assertIn("GRPC_AUTH_TOKEN", wf)
        self.assertNotIn("/tmp/docreader.pid", wf)
        self.assertIn("RUNNER_TEMP", wf)
        # Ensure no ${{ ... }} template interpolation appears inside `run:` shell blocks.
        in_run_block = False
        run_indent = 0
        for line in wf.splitlines():
            stripped = line.lstrip(" ")
            indent = len(line) - len(stripped)
            if in_run_block:
                if stripped and indent <= run_indent:
                    in_run_block = False
                else:
                    self.assertNotIn("${{", line, f"Template interpolation in run block: {line}")
                    continue
            if stripped.startswith("run:") or stripped.startswith("- run:"):
                if "|" in stripped or ">" in stripped:
                    in_run_block = True
                    run_indent = indent
                else:
                    self.assertNotIn("${{", line, f"Template interpolation in run line: {line}")

    def test_up_ops_build_anydoc_lib_sha256_symlink_and_llvm_strip_safety(self):
        with tempfile.TemporaryDirectory(prefix="genyee-anydoc-sec-") as tmp_dir:
            root = Path(tmp_dir)
            (root / "scripts").mkdir()
            shutil.copy2(REPO_ROOT / "scripts/build-anydoc-lib.sh", root / "scripts/build-anydoc-lib.sh")
            crate = root / "third_party/anydoc-go"
            crate.mkdir(parents=True)
            (crate / "Cargo.toml").write_text('anydoc = "=0.2.4"\n', encoding="utf-8")
            (crate / "version.go").write_text('const Version = "0.2.4"\n', encoding="utf-8")

            # Build a crate tarball containing a symlink to verify symlink rejection on unpack.
            src_pkg = root / "pkg-src/anydoc-0.2.4/src"
            src_pkg.mkdir(parents=True)
            (src_pkg / "lib.rs").write_text("use render::markdown::document_to_markdown;\n", encoding="utf-8")
            (src_pkg.parent / "Cargo.toml").write_text('[dependencies.zip]\nversion = "8.6.0"\n', encoding="utf-8")
            os_symlink = src_pkg / "escape_link.rs"
            os_symlink.symlink_to("/etc/hosts")
            cache_dir = root / "cargo-home/registry/cache/index"
            cache_dir.mkdir(parents=True)
            crate_tar = cache_dir / "anydoc-0.2.4.crate"
            with tarfile.open(crate_tar, "w:gz") as tf:
                tf.add(src_pkg.parent, arcname="anydoc-0.2.4", recursive=True)

            import hashlib

            tar_sha = hashlib.sha256(crate_tar.read_bytes()).hexdigest()
            (root / "licenses/sources").mkdir(parents=True)
            (root / "licenses/sources/anydoc.tsv").write_text(f"0.2.4 {tar_sha}\n", encoding="utf-8")

            bin_dir = root / "bin"
            bin_dir.mkdir()
            (bin_dir / "cargo").write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
            (bin_dir / "cargo").chmod(0o755)

            env = dict(
                PATH=f"{bin_dir}:/usr/bin:/bin",
                HOME=str(root),
                CARGO_HOME=str(root / "cargo-home"),
                TARGET="x86_64-unknown-linux-gnu",
            )
            res = subprocess.run(
                ["bash", str(root / "scripts/build-anydoc-lib.sh"), "--prepare-only"],
                cwd=root,
                env=env,
                text=True,
                capture_output=True,
            )
            self.assertNotEqual(res.returncode, 0, "Expected unpacked crate with symlink to be rejected")
            self.assertFalse((crate / "patched-anydoc").exists())

            # Verify strip_llvm_lto_sections handles dotted `.llvm.lto` section names,
            # stages via temp file without corrupting archive on objcopy failure, and
            # never overwrites through a destination symlink.
            patched = crate / "patched-anydoc"
            patched.mkdir()
            (patched / "Cargo.toml").write_text("fixture\n", encoding="utf-8")
            (patched / ".genyee-patched").write_text(f"0.2.4-zip9-v2-{tar_sha}\n", encoding="utf-8")
            target_out = root / "target-out"
            rel_lib = target_out / "x86_64-unknown-linux-gnu/release/libanydoc_go.a"
            rel_lib.parent.mkdir(parents=True)
            rel_lib.write_bytes(b"ARCHIVE_BYTES_INTACT")

            (bin_dir / "go").write_text(
                f"#!/bin/sh\nif [ \"$1\" = env ]; then echo linux; exit 0; fi\n"
                f"for a in \"$@\"; do [ \"$a\" = --print-target-dir ] && echo '{target_out}' && exit 0; done\n",
                encoding="utf-8",
            )
            (bin_dir / "go").chmod(0o755)
            (bin_dir / "readelf").write_text(
                "#!/bin/sh\necho '  [ 4] .llvm.lto PROGBITS'\n",
                encoding="utf-8",
            )
            (bin_dir / "readelf").chmod(0o755)
            # Simulate failing cross-arch objcopy that writes partial junk to output before failing.
            (bin_dir / "objcopy").write_text(
                "#!/bin/sh\necho CORRUPT > \"$3\"\nexit 1\n",
                encoding="utf-8",
            )
            (bin_dir / "objcopy").chmod(0o755)

            dest_dir = crate / "lib/linux_amd64_gnu"
            dest_dir.mkdir(parents=True)
            victim = root / "victim.txt"
            victim.write_bytes(b"DO_NOT_OVERWRITE")
            (dest_dir / "libanydoc_go.a").symlink_to(victim)

            res2 = subprocess.run(
                ["bash", str(root / "scripts/build-anydoc-lib.sh")],
                cwd=root,
                env=env,
                text=True,
                capture_output=True,
            )
            self.assertEqual(res2.returncode, 0, res2.stdout + res2.stderr)
            self.assertEqual(victim.read_bytes(), b"DO_NOT_OVERWRITE")
            out_archive = dest_dir / "libanydoc_go.a"
            self.assertFalse(out_archive.is_symlink())
            self.assertEqual(out_archive.read_bytes(), b"ARCHIVE_BYTES_INTACT")


if __name__ == "__main__":
    unittest.main()
