"""Regression tests for docreader build scripts, dependency metadata, and docs."""

from __future__ import annotations

import importlib.util
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[2]
DOCREADER_DIR = REPO_ROOT / "docreader"


def _parse_version_tuple(spec: str) -> tuple[int, ...]:
    m = re.search(r">=\s*([0-9]+(?:\.[0-9]+)*)", spec)
    if not m:
        return ()
    return tuple(int(part) for part in m.group(1).split("."))


class TestDocreaderBuildAndMetadataRegressions(unittest.TestCase):
    """Regression coverage for Makefile, generate_proto.sh, pyproject.toml, and README.md."""

    def test_generate_proto_sh_posix_and_cwd_independent(self):
        script_src = DOCREADER_DIR / "scripts" / "generate_proto.sh"
        sh_check = subprocess.run(["sh", "-n", str(script_src)], capture_output=True, text=True)
        self.assertEqual(sh_check.returncode, 0, sh_check.stderr)
        bash_check = subprocess.run(["bash", "-n", str(script_src)], capture_output=True, text=True)
        self.assertEqual(bash_check.returncode, 0, bash_check.stderr)

        with tempfile.TemporaryDirectory(prefix="proto dir with spaces ") as tmp:
            tmp_root = Path(tmp)
            scripts_dir = tmp_root / "docreader" / "scripts"
            proto_dir = tmp_root / "docreader" / "proto"
            bin_dir = tmp_root / "bin"
            scripts_dir.mkdir(parents=True)
            proto_dir.mkdir(parents=True)
            bin_dir.mkdir(parents=True)

            copied_script = scripts_dir / "generate_proto.sh"
            shutil.copy2(script_src, copied_script)
            copied_script.chmod(0o755)
            (proto_dir / "docreader.proto").write_text(
                'syntax = "proto3";\npackage docreader;\n', encoding="utf-8"
            )

            # Stub python3 that writes raw grpc_tools output with unqualified import,
            # while intentionally omitting protoc and protoc-gen-go from PATH.
            stub_py = bin_dir / "python3"
            stub_py.write_text(
                "#!/bin/sh\n"
                'for arg in "$@"; do\n'
                '  case "$arg" in\n'
                "    --grpc_python_out=*)\n"
                '      out_dir="${arg#--grpc_python_out=}"\n'
                '      printf "import docreader_pb2 as docreader__pb2\\n"'
                ' > "${out_dir}/docreader_pb2_grpc.py"\n'
                "      ;;\n"
                "  esac\n"
                "done\n",
                encoding="utf-8",
            )
            stub_py.chmod(0o755)

            for tool in ("uname", "sed", "dirname"):
                real_tool = shutil.which(tool)
                if real_tool:
                    (bin_dir / tool).symlink_to(real_tool)

            # Execute under POSIX /bin/sh from CWD=<tmp>/docreader (make -C docreader proto).
            env = {"PATH": str(bin_dir)}
            proc = subprocess.run(
                ["/bin/sh", "./scripts/generate_proto.sh"],
                cwd=str(tmp_root / "docreader"),
                env=env,
                capture_output=True,
                text=True,
            )
            self.assertEqual(
                proc.returncode,
                0,
                f"generate_proto.sh failed under /bin/sh: "
                f"stdout={proc.stdout}\nstderr={proc.stderr}",
            )
            rewritten = (proto_dir / "docreader_pb2_grpc.py").read_text(encoding="utf-8")
            self.assertIn("from docreader.proto import docreader_pb2 as docreader__pb2", rewritten)

    def test_makefile_targets_reference_existing_entrypoints(self):
        makefile = (DOCREADER_DIR / "Makefile").read_text(encoding="utf-8")
        self.assertNotIn("./src/client", makefile)
        self.assertNotIn("src/server/server.py", makefile)
        self.assertNotIn("docker-build", makefile)
        self.assertIn("./docreader/client", makefile)
        self.assertIn("docreader.main", makefile)

    def test_pyproject_grpc_floor_and_direct_runtime_dependencies(self):
        pyproject = tomllib.loads((DOCREADER_DIR / "pyproject.toml").read_text(encoding="utf-8"))
        uv_lock = tomllib.loads((DOCREADER_DIR / "uv.lock").read_text(encoding="utf-8"))
        pb2_grpc = (DOCREADER_DIR / "proto" / "docreader_pb2_grpc.py").read_text(encoding="utf-8")
        m = re.search(r"GRPC_GENERATED_VERSION\s*=\s*'([^']+)'", pb2_grpc)
        self.assertIsNotNone(m, "GRPC_GENERATED_VERSION missing in docreader_pb2_grpc.py")
        assert m is not None
        generated_ver = tuple(int(x) for x in m.group(1).split("."))

        deps = pyproject["project"]["dependencies"]
        dep_map: dict[str, str] = {}
        for raw in deps:
            pkg = re.split(r"[\[<>=!~;\s]", raw, maxsplit=1)[0].strip().lower()
            dep_map[pkg] = raw

        for grpc_pkg in ("grpcio", "grpcio-health-checking", "grpcio-tools"):
            self.assertIn(grpc_pkg, dep_map)
            floor = _parse_version_tuple(dep_map[grpc_pkg])
            self.assertGreaterEqual(
                floor,
                generated_ver,
                f"{grpc_pkg} floor {dep_map[grpc_pkg]!r} < stub gate {m.group(1)}",
            )

        for direct_pkg in ("defusedxml", "charset-normalizer", "python-pptx"):
            self.assertIn(
                direct_pkg,
                dep_map,
                f"Direct runtime import {direct_pkg!r} is undeclared in docreader/pyproject.toml",
            )

        docreader_lock = next(p for p in uv_lock["package"] if p["name"] == "docreader")
        locked_dep_names = {d["name"] for d in docreader_lock["dependencies"]}
        for direct_pkg in ("defusedxml", "charset-normalizer", "python-pptx"):
            self.assertIn(direct_pkg, locked_dep_names)

    def test_readme_documents_fail_closed_auth_and_excludes_cloud_secret_keys(self):
        readme = (DOCREADER_DIR / "README.md").read_text(encoding="utf-8")
        self.assertIn("GRPC_AUTH_TOKEN", readme)
        self.assertIn("DOCREADER_ALLOW_UNAUTHENTICATED", readme)
        self.assertIn("GRPC_TLS_CERT", readme)
        self.assertIn("GRPC_TLS_KEY", readme)
        self.assertNotIn("GRPC_TLS_CERT_FILE", readme)
        self.assertNotIn("GRPC_TLS_KEY_FILE", readme)
        self.assertNotIn("GRPC_TLS_CA_FILE", readme)
        for forbidden_secret in (
            "MINIO_SECRET_ACCESS_KEY",
            "COS_SECRET_KEY",
            "OSS_ACCESS_KEY_SECRET",
            "minioadmin",
        ):
            self.assertNotIn(
                forbidden_secret,
                readme,
                f"docreader/README.md must not inject {forbidden_secret} into docreader",
            )

    def test_security_regressions_discoverable_by_unittest_without_pytest(self):
        if importlib.util.find_spec("lxml") is None or importlib.util.find_spec("pdfminer") is None:
            self.skipTest("docreader runtime dependencies not installed in active interpreter")
        # Even if pytest is absent from the environment, unittest discovery must
        # import and load security regression tests from both modules.
        sec_path = DOCREADER_DIR / "tests" / "test_security_audit_fixes.py"
        dep_path = DOCREADER_DIR / "tests" / "test_dependency_security.py"
        for mod_name, rel_path, min_cases in (
            ("sec_audit_unittest_check", sec_path, 14),
            ("dep_sec_unittest_check", dep_path, 1),
        ):
            spec = importlib.util.spec_from_file_location(mod_name, rel_path)
            self.assertIsNotNone(spec)
            assert spec is not None
            mod = importlib.util.module_from_spec(spec)
            with mock.patch.dict(sys.modules, {"pytest": None}):
                assert spec.loader is not None
                spec.loader.exec_module(mod)
            suite = unittest.defaultTestLoader.loadTestsFromModule(mod)
            count = suite.countTestCases()
            self.assertGreaterEqual(
                count,
                min_cases,
                f"{rel_path.name} exposed only {count} unittest cases (want >= {min_cases})",
            )

    def test_pyproject_mypy_explicit_package_bases_enabled(self):
        pyproject = tomllib.loads((DOCREADER_DIR / "pyproject.toml").read_text(encoding="utf-8"))
        mypy_cfg = pyproject.get("tool", {}).get("mypy", {})
        self.assertIs(mypy_cfg.get("explicit_package_bases"), True)

    def test_parse_local_handles_raw_image_bytes_and_none_document(self):
        from docreader.models.document import Document
        from docreader.scripts import parse_local

        raw_png = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00A"
        with tempfile.TemporaryDirectory() as tmp:
            tmp_path = Path(tmp)
            src = tmp_path / "sample.md"
            out_md = tmp_path / "out.md"
            src.write_text("# Sample\n", encoding="utf-8")

            fake_doc = Document(content="# Parsed\n")
            fake_doc.images = {"images/raw.png": raw_png}  # type: ignore[dict-item]
            with (
                mock.patch.object(parse_local.Parser, "parse_file", return_value=fake_doc),
                mock.patch.object(sys, "argv", ["parse_local.py", str(src), "--out", str(out_md)]),
            ):
                rc = parse_local.main()
            self.assertEqual(rc, 0)
            self.assertEqual((tmp_path / "images" / "raw.png").read_bytes(), raw_png)

            with (
                mock.patch.object(parse_local.Parser, "parse_file", return_value=None),
                mock.patch.object(sys, "argv", ["parse_local.py", str(src)]),
            ):
                rc_none = parse_local.main()
            self.assertEqual(rc_none, 1)

    def test_source_wire_skips_nan_and_non_dict_locators(self):
        from types import SimpleNamespace

        from docreader.source_wire import source_blocks_to_proto

        nan_loc = {"type": "pdf", "page": 1, "bbox": [float("nan")]}
        ok_loc = {"type": "pdf", "page": 1, "bbox": [0.1, 0.2, 0.3, 0.4]}
        doc = SimpleNamespace(
            source_blocks=[
                {"start": 0, "end": 5, "locator": nan_loc},
                {"start": 0, "end": 5, "locator": ["not", "a", "dict"]},
                {"start": 0, "end": 5, "locator": ok_loc},
            ]
        )
        blocks = source_blocks_to_proto(doc)
        self.assertEqual(len(blocks), 1)
        self.assertNotIn("NaN", blocks[0].locator_json)

    def test_auth_trims_whitespace_in_tls_paths_and_bearer_header(self):
        from docreader.auth import AuthInterceptor, TLSConfigError, load_tls_credentials

        with (
            mock.patch.dict(
                "os.environ",
                {"GRPC_TLS_ENABLED": "true", "GRPC_TLS_CERT": "   ", "GRPC_TLS_KEY": "   "},
                clear=True,
            ),
            self.assertRaisesRegex(TLSConfigError, "not set"),
        ):
            load_tls_credentials()

        with mock.patch.dict(
            "os.environ",
            {"GRPC_AUTH_TOKEN": "0123456789abcdef"},
            clear=True,
        ):
            interceptor = AuthInterceptor()
            called = False

            def continuation(_details):
                nonlocal called
                called = True
                return "ok-handler"

            details = type(
                "_Details",
                (),
                {
                    "method": "/docreader.DocReader/Read",
                    "invocation_metadata": [("authorization", "Bearer   0123456789abcdef  ")],
                },
            )()
            self.assertEqual(interceptor.intercept_service(continuation, details), "ok-handler")
            self.assertTrue(called)


if __name__ == "__main__":
    unittest.main()
