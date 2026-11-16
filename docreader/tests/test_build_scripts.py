"""Regression tests for docreader build scripts, dependency metadata, and docs."""

from __future__ import annotations

import importlib.util
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib
import unittest
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[2]
DOCREADER_DIR = REPO_ROOT / "docreader"


def _parse_version_tuple(spec: str) -> tuple[int, ...]:
    m = re.search(r">=\s*([0-9]+(?:\.[0-9]+)*)", spec)
    if not m:
        return ()
    return tuple(int(part) for part in m.group(1).split("."))


class TestDocreaderBuildAndMetadataRegressions(unittest.TestCase):
    """Regression coverage for docreader/Makefile, generate_proto.sh, pyproject.toml, and README.md."""

    def test_generate_proto_sh_posix_and_cwd_independent(self):
        script_src = DOCREADER_DIR / "scripts" / "generate_proto.sh"
        sh_check = subprocess.run(
            ["sh", "-n", str(script_src)], capture_output=True, text=True
        )
        self.assertEqual(sh_check.returncode, 0, sh_check.stderr)
        bash_check = subprocess.run(
            ["bash", "-n", str(script_src)], capture_output=True, text=True
        )
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
                '      printf "import docreader_pb2 as docreader__pb2\\n" > "${out_dir}/docreader_pb2_grpc.py"\n'
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

            # Execute under POSIX /bin/sh from CWD=<tmp>/docreader (matching `make -C docreader proto`).
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
                f"generate_proto.sh failed under /bin/sh from docreader/: stdout={proc.stdout}\nstderr={proc.stderr}",
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
        pyproject = tomllib.loads(
            (DOCREADER_DIR / "pyproject.toml").read_text(encoding="utf-8")
        )
        uv_lock = tomllib.loads(
            (DOCREADER_DIR / "uv.lock").read_text(encoding="utf-8")
        )
        pb2_grpc = (DOCREADER_DIR / "proto" / "docreader_pb2_grpc.py").read_text(
            encoding="utf-8"
        )
        m = re.search(r"GRPC_GENERATED_VERSION\s*=\s*'([^']+)'", pb2_grpc)
        self.assertIsNotNone(m, "GRPC_GENERATED_VERSION missing in docreader_pb2_grpc.py")
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
                f"{grpc_pkg} floor {dep_map[grpc_pkg]!r} is lower than generated stub gate {m.group(1)}",
            )

        for direct_pkg in ("defusedxml", "charset-normalizer", "python-pptx"):
            self.assertIn(
                direct_pkg,
                dep_map,
                f"Direct runtime import {direct_pkg!r} is undeclared in docreader/pyproject.toml",
            )

        docreader_lock = next(
            p for p in uv_lock["package"] if p["name"] == "docreader"
        )
        locked_dep_names = {d["name"] for d in docreader_lock["dependencies"]}
        for direct_pkg in ("defusedxml", "charset-normalizer", "python-pptx"):
            self.assertIn(direct_pkg, locked_dep_names)

    def test_readme_documents_fail_closed_auth_and_excludes_cloud_secret_keys(self):
        readme = (DOCREADER_DIR / "README.md").read_text(encoding="utf-8")
        self.assertIn("GRPC_AUTH_TOKEN", readme)
        self.assertIn("DOCREADER_ALLOW_UNAUTHENTICATED", readme)
        for forbidden_secret in (
            "MINIO_SECRET_ACCESS_KEY",
            "COS_SECRET_KEY",
            "OSS_ACCESS_KEY_SECRET",
            "minioadmin",
        ):
            self.assertNotIn(
                forbidden_secret,
                readme,
                f"docreader/README.md must not instruct injecting {forbidden_secret} into docreader",
            )

    def test_security_regressions_discoverable_by_unittest_without_pytest(self):
        if importlib.util.find_spec("lxml") is None or importlib.util.find_spec("pdfminer") is None:
            self.skipTest("docreader runtime dependencies not installed in active interpreter")
        # Even if pytest is absent from the environment, unittest discovery must
        # import and load security regression tests from both modules.
        for mod_name, rel_path, min_cases in (
            ("sec_audit_unittest_check", DOCREADER_DIR / "tests" / "test_security_audit_fixes.py", 15),
            ("dep_sec_unittest_check", DOCREADER_DIR / "tests" / "test_dependency_security.py", 1),
        ):
            spec = importlib.util.spec_from_file_location(mod_name, rel_path)
            self.assertIsNotNone(spec)
            mod = importlib.util.module_from_spec(spec)
            with mock.patch.dict(sys.modules, {"pytest": None}):
                spec.loader.exec_module(mod)  # type: ignore[union-attr]
            suite = unittest.defaultTestLoader.loadTestsFromModule(mod)
            self.assertGreaterEqual(
                suite.countTestCases(),
                min_cases,
                f"{rel_path.name} exposed only {suite.countTestCases()} unittest cases (want >= {min_cases})",
            )


if __name__ == "__main__":
    unittest.main()
