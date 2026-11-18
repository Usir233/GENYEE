#!/usr/bin/env python3
"""
GENYEE MCP Server 模组测试脚本

测试模组的各种启动方式和功能。unittest discover 会收集本文件中的 TestCase；
也可直接运行: python test_module.py
"""

import os
import subprocess
import sys
import unittest
from pathlib import Path

MCP_SERVER_DIR = Path(__file__).resolve().parent

REQUIRED_FILES = [
    "upload_paths.py",
    "main.py",
    "run_server.py",
    "genyee_mcp_server.py",
    "requirements.txt",
    "setup.py",
    "pyproject.toml",
    "README.md",
    "INSTALL.md",
    "LICENSE",
    "MANIFEST.in",
]


class ModuleIntegrationTest(unittest.TestCase):
    def test_imports(self) -> None:
        import mcp  # noqa: F401
        import requests  # noqa: F401

        import genyee_mcp_server  # noqa: F401
        import main  # noqa: F401
        from genyee_mcp_server import GENYEEClient, run  # noqa: F401

    def test_environment_optional_vars(self) -> None:
        os.getenv("GENYEE_BASE_URL")
        os.getenv("GENYEE_API_KEY")

    def test_client_creation(self) -> None:
        from genyee_mcp_server import GENYEEClient

        base_url = os.getenv("GENYEE_BASE_URL", "http://localhost:8080/api/v1")
        api_key = os.getenv("GENYEE_API_KEY", "test_key")
        client = GENYEEClient(base_url, api_key)
        self.assertEqual(client.base_url, base_url.rstrip("/"))
        self.assertEqual(client.api_key, api_key)

    def test_required_files_exist_and_no_invalid_package_init(self) -> None:
        missing = [name for name in REQUIRED_FILES if not (MCP_SERVER_DIR / name).exists()]
        self.assertEqual(missing, [], f"Missing files: {missing}")
        self.assertFalse(
            (MCP_SERVER_DIR / "__init__.py").exists(),
            "Hyphenated mcp-server directory must not contain __init__.py",
        )

    def test_main_help(self) -> None:
        result = subprocess.run(
            [sys.executable, "main.py", "--help"],
            capture_output=True,
            text=True,
            timeout=10,
            cwd=MCP_SERVER_DIR,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_main_check_only(self) -> None:
        result = subprocess.run(
            [sys.executable, "main.py", "--check-only"],
            capture_output=True,
            text=True,
            timeout=10,
            cwd=MCP_SERVER_DIR,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_wiki_tools(self) -> None:
        import genyee_mcp_server

        client = genyee_mcp_server.GENYEEClient("http://localhost:8080/api/v1", "test")
        for method in ["wiki_search", "wiki_read_page", "wiki_index_view"]:
            self.assertTrue(hasattr(client, method), f"GENYEEClient missing: {method}")
            self.assertTrue(callable(getattr(client, method)), f"{method} not callable")

    def test_pyproject_and_setup_exclude_test_module_and_keep_upload_paths(
        self,
    ) -> None:
        pyproject_text = (MCP_SERVER_DIR / "pyproject.toml").read_text(encoding="utf-8")
        setup_text = (MCP_SERVER_DIR / "setup.py").read_text(encoding="utf-8")
        self.assertIn("genyee-mcp", pyproject_text)
        self.assertIn("GENYEE MCP Server", pyproject_text)
        self.assertIn('"upload_paths"', pyproject_text)
        self.assertIn('"upload_paths"', setup_text)
        self.assertNotIn('"test_module"', pyproject_text)
        self.assertNotIn('"test_module"', setup_text)


if __name__ == "__main__":
    unittest.main(verbosity=2)
