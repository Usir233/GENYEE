"""Regression coverage for internal tests/{stress,adversarial,e2e} harnesses."""

from __future__ import annotations

import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

REPO_ROOT = Path(__file__).resolve().parents[2]


class TestRepoSuitesRegressions(unittest.TestCase):
    """Regression coverage for tests/{stress,adversarial,e2e} suites."""

    def test_r3_r2_challenger_uses_formula_script_and_genyee_db(self):
        challenger_path = (
            REPO_ROOT / "tests" / "stress" / "test_r3_r2_adversarial_challenger.py"
        )
        src = challenger_path.read_text(encoding="utf-8")
        self.assertNotIn("weknora.db", src)
        self.assertNotIn("weknora-lite.rb", src)
        self.assertNotIn("/weknora", src)
        self.assertIn("_extract_env_init_block", src)
        self.assertIn("genyee.db", src)

    def test_hybrid_search_stress_fails_on_pg_active_connection_leak(self):
        stress_path = REPO_ROOT / "tests" / "stress" / "test_hybrid_search_stress.py"
        spec = importlib.util.spec_from_file_location(
            "hybrid_stress_check", stress_path
        )
        mod = importlib.util.module_from_spec(spec)  # type: ignore[arg-type]
        spec.loader.exec_module(mod)  # type: ignore[union-attr]

        fake_ok = {
            "status": 200,
            "elapsed_sec": 0.01,
            "count": 6,
            "chunks": [
                {"knowledge_title": "doc", "score": 0.9, "match_type": "hybrid"}
            ],
            "error": None,
        }
        with (
            mock.patch.object(mod, "get_auth_token", return_value="tok"),
            mock.patch.object(
                mod, "get_pg_stats", side_effect=[{"active": 0}, {"active": 4}]
            ),
            mock.patch.object(mod, "get_docker_memory", return_value={}),
            mock.patch.object(mod, "execute_search", return_value=fake_ok),
            mock.patch.object(mod.time, "sleep", return_value=None),
        ):
            self.assertFalse(
                mod.run_stress_suite(),
                "run_stress_suite() must fail when active PostgreSQL connections > 1 after stress run",
            )

    def test_adversarial_auth_suite_brand_and_explicit_credentials_and_main_summary(
        self,
    ):
        adv_path = (
            REPO_ROOT / "tests" / "adversarial" / "test_security_auth_adversarial.py"
        )
        spec = importlib.util.spec_from_file_location("adv_auth_check", adv_path)
        mod = importlib.util.module_from_spec(spec)  # type: ignore[arg-type]
        spec.loader.exec_module(mod)  # type: ignore[union-attr]

        # 1. Standalone "weknora" in /api/v1/auth/me must fail brand scan.
        class _Resp:
            def __init__(
                self, status_code: int, text: str, headers: dict | None = None
            ):
                self.status_code = status_code
                self.text = text
                self.headers = headers or {}

            def json(self):
                return json.loads(self.text)

        def _fake_get(url, **kwargs):
            if url.endswith("/"):
                return _Resp(200, "<html><head><title>GENYEE</title></head></html>")
            if url.endswith("/api/v1/auth/me"):
                return _Resp(200, '{"username":"legacy-weknora-admin"}')
            if url.endswith("/api/v1/sessions"):
                return _Resp(200, '{"data":[]}')
            if url.endswith("/api/v1/memory/export"):
                return _Resp(401, "Unauthorized")
            return _Resp(404, "not found")

        with (
            mock.patch.object(mod, "_require_live_server_or_skip", return_value=None),
            mock.patch.object(
                mod.subprocess,
                "run",
                return_value=mock.Mock(returncode=1, stdout="", stderr=""),
            ),
            mock.patch.object(mod.requests, "get", side_effect=_fake_get),
            mock.patch.object(
                mod.requests,
                "post",
                return_value=_Resp(401, '{"error":"invalid credentials"}'),
            ),
        ):
            with self.assertRaises(AssertionError):
                mod.test_brand_residue_scan(valid_token="tok")

            # 2. Explicit credentials with missing valid_token must fail, not false-green pass.
            with (
                mock.patch.object(mod, "EXPLICIT_TEST_CREDENTIALS", True),
                self.assertRaises(AssertionError),
            ):
                mod.test_negative_export(valid_token=None)

        # 3. Standalone main() must write challenger_1_summary.json even if an early suite raises AssertionError.
        with tempfile.TemporaryDirectory() as tmp:
            tmp_reports = Path(tmp)
            with (
                mock.patch.object(mod, "REPORTS_DIR", tmp_reports),
                mock.patch.object(
                    mod,
                    "test_results",
                    {"total": 1, "passed": 0, "failed": 1, "tests": []},
                ),
                mock.patch.object(
                    mod, "_run_auth_login_boundaries", return_value=("tok", [])
                ),
                mock.patch.object(
                    mod,
                    "test_protected_endpoints_tampered_jwt",
                    side_effect=AssertionError("suite 1 boom"),
                ),
                mock.patch.object(
                    mod, "test_jwt_audience_validation_in_middleware", return_value=None
                ),
                mock.patch.object(mod, "test_brand_residue_scan", return_value=None),
                mock.patch.object(mod, "test_negative_export", return_value=None),
            ):
                with self.assertRaises(SystemExit) as cm:
                    mod.main()
                self.assertEqual(cm.exception.code, 1)
                self.assertTrue((tmp_reports / "challenger_1_summary.json").is_file())

    def test_browser_e2e_safe_screenshot_and_empty_results_status(self):
        e2e_path = REPO_ROOT / "tests" / "e2e" / "test_browser_e2e.py"

        fake_pw = mock.MagicMock()
        with mock.patch.dict(
            sys.modules, {"playwright": fake_pw, "playwright.sync_api": fake_pw}
        ):
            spec = importlib.util.spec_from_file_location("browser_e2e_check", e2e_path)
            mod = importlib.util.module_from_spec(spec)  # type: ignore[arg-type]
            spec.loader.exec_module(mod)  # type: ignore[union-attr]

        with tempfile.TemporaryDirectory() as tmp:
            tmp_dir = Path(tmp)
            with (
                mock.patch.object(mod, "REPORTS_DIR", tmp_dir),
                mock.patch.object(mod, "SCREENSHOTS_DIR", tmp_dir / "screenshots"),
            ):
                (tmp_dir / "screenshots").mkdir(parents=True, exist_ok=True)
                runner = mod.E2ETestRunner()

                # Empty results must NOT report overall PASSED.
                runner.generate_reports()
                summary = json.loads(
                    (tmp_dir / "e2e_summary.json").read_text(encoding="utf-8")
                )
                self.assertEqual(summary["execution"]["status"], "FAILED")

                # Secondary screenshot failure inside stage except block must not abort before record_stage.
                broken_page = mock.MagicMock()
                broken_page.goto.side_effect = RuntimeError("navigation crashed")
                broken_page.screenshot.side_effect = RuntimeError("target closed")
                self.assertFalse(runner.run_stage_1_login_page(broken_page))
                self.assertEqual(len(runner.results), 1)
                self.assertFalse(runner.results[0]["passed"])


if __name__ == "__main__":
    unittest.main()
