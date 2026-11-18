"""Regression tests for Wave 7 docker, odl-hybrid, root-runtime, scripts, cloud-image, and mcp-demo fixes."""

from __future__ import annotations

import asyncio
import importlib.util
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tarfile
import tempfile
import types
import unittest
from unittest import mock

import yaml


REPO_ROOT = Path(__file__).resolve().parents[2]


def _load_module_with_stubs(module_name: str, file_path: Path, stubs: dict[str, types.ModuleType]):
    saved = {k: sys.modules.get(k) for k in stubs}
    saved_self = sys.modules.get(module_name)
    try:
        sys.modules.update(stubs)
        spec = importlib.util.spec_from_file_location(module_name, file_path)
        assert spec is not None and spec.loader is not None
        mod = importlib.util.module_from_spec(spec)
        sys.modules[module_name] = mod
        spec.loader.exec_module(mod)
        return mod
    finally:
        if saved_self is None:
            sys.modules.pop(module_name, None)
        else:
            sys.modules[module_name] = saved_self
        for k, prev in saved.items():
            if prev is None:
                sys.modules.pop(k, None)
            else:
                sys.modules[k] = prev


def _mcp_stubs() -> dict[str, types.ModuleType]:
    mcp_mod = types.ModuleType("mcp")
    server_mod = types.ModuleType("mcp.server")
    fastmcp_mod = types.ModuleType("mcp.server.fastmcp")
    ts_mod = types.ModuleType("mcp.server.transport_security")

    class _TransportSecuritySettings:
        def __init__(self, **kwargs):
            self.__dict__.update(kwargs)

    class _FastMCP:
        def __init__(self, *args, **kwargs):
            self.kwargs = kwargs

        def tool(self, *args, **kwargs):
            return lambda fn: fn

        def resource(self, *args, **kwargs):
            return lambda fn: fn

        def prompt(self, *args, **kwargs):
            return lambda fn: fn

    server_mod.MCPServer = _FastMCP
    fastmcp_mod.FastMCP = _FastMCP
    ts_mod.TransportSecuritySettings = _TransportSecuritySettings
    return {
        "mcp": mcp_mod,
        "mcp.server": server_mod,
        "mcp.server.fastmcp": fastmcp_mod,
        "mcp.server.transport_security": ts_mod,
    }


class DockerAndOdlHybridSecurityTests(unittest.TestCase):
    def _load_compose(self, name: str) -> dict:
        return yaml.safe_load((REPO_ROOT / name).read_text(encoding="utf-8"))

    def test_minio_enforces_non_empty_credentials_and_searxng_avoids_static_public_secret(self):
        for compose_name in ("docker-compose.yml", "docker-compose.dev.yml"):
            raw = (REPO_ROOT / compose_name).read_text(encoding="utf-8")
            self.assertNotIn(
                "genyee-default-searxng-secret-rotate-before-exposing-publicly",
                raw,
                f"{compose_name} must not ship a static public SearXNG HMAC secret",
            )
            doc = yaml.safe_load(raw)
            services = doc["services"]

            minio = services["minio"]
            minio_cmd = " ".join(
                minio["command"] if isinstance(minio["command"], list) else [str(minio["command"])]
            )
            self.assertEqual(minio.get("entrypoint"), "sh")
            self.assertIn("MINIO_ROOT_USER", minio_cmd)
            self.assertIn("MINIO_ROOT_PASSWORD", minio_cmd)

            searxng_init = services["searxng-init"]
            init_cmd = " ".join(
                searxng_init["command"]
                if isinstance(searxng_init["command"], list)
                else [str(searxng_init["command"])]
            )
            self.assertIn(".autogen_secret", init_cmd)
            self.assertIn("/dev/urandom", init_cmd)

    def test_langfuse_db_init_and_postgres_healthcheck_avoid_host_shell_sql_interpolation(self):
        for compose_name in ("docker-compose.yml", "docker-compose.dev.yml"):
            doc = self._load_compose(compose_name)
            services = doc["services"]

            pg_test = " ".join(services["postgres"]["healthcheck"]["test"])
            self.assertNotIn("${DB_USER", pg_test)
            self.assertNotIn("${DB_NAME", pg_test)
            self.assertIn("$$POSTGRES_USER", pg_test)
            self.assertIn("$$POSTGRES_DB", pg_test)

            lf_init = services["langfuse-db-init"]
            lf_env = lf_init.get("environment", {})
            if isinstance(lf_env, list):
                lf_env = dict(item.split("=", 1) for item in lf_env)
            self.assertIn("PGUSER", lf_env)
            self.assertIn("LF_DB_NAME", lf_env)
            lf_cmd = " ".join(
                lf_init["command"]
                if isinstance(lf_init["command"], list)
                else [str(lf_init["command"])]
            )
            self.assertNotIn("${DB_USER", lf_cmd)
            self.assertNotIn("${LANGFUSE_DB_NAME", lf_cmd)
            self.assertIn("[!A-Za-z0-9_-]", lf_cmd)

    def test_dockerfile_duckdb_chown_odl_shlex_cmd_and_compose_env_passthrough(self):
        dockerfile_app = (REPO_ROOT / "docker/Dockerfile.app").read_text(encoding="utf-8")
        self.assertIn(
            "COPY --from=builder --chown=appuser:appuser /root/.duckdb /home/appuser/.duckdb",
            dockerfile_app,
        )

        dockerfile_odl = (REPO_ROOT / "docker/Dockerfile.odl-hybrid").read_text(encoding="utf-8")
        self.assertNotIn("${ODL_HYBRID_EXTRA_ARGS}", dockerfile_odl)
        self.assertIn("shlex.split", dockerfile_odl)

        prod_compose = (REPO_ROOT / "docker-compose.yml").read_text(encoding="utf-8")
        for var in (
            "GENYEE_ALLOW_ROOT_DOCKER_SOCK_GID",
            "GENYEE_CORS_ALLOWED_ORIGINS",
            "DUCKDB_SKIP_EXTENSION_LOAD",
            "GENYEE_LLM_CHAT_TIMEOUT_SECONDS",
            "GENYEE_LLM_STREAM_TIMEOUT_SECONDS",
            "GENYEE_WEB_FETCH_CHROMIUM_CONCURRENCY",
            "ODL_HYBRID_MAX_FILE_SIZE_MB",
        ):
            self.assertIn(var, prod_compose)
        dev_compose = (REPO_ROOT / "docker-compose.dev.yml").read_text(encoding="utf-8")
        self.assertIn("ODL_HYBRID_MAX_FILE_SIZE_MB", dev_compose)

    def test_odl_hybrid_empty_port_env_fallback_and_negative_content_length_rejected(self):
        mod = _load_module_with_stubs(
            "odl_hybrid_under_test",
            REPO_ROOT / "docker/odl-hybrid/opendataloader_pdf/hybrid_server.py",
            {},
        )
        with mock.patch.dict(os.environ, {"ODL_HYBRID_PORT": ""}, clear=False):
            with self.assertRaises(SystemExit) as cm:
                mod.main(["--help"])
            self.assertEqual(cm.exception.code, 0)

        sent: list[dict] = []

        async def inner_app(scope, receive, send):
            await send({"type": "http.response.start", "status": 200, "headers": []})
            await send({"type": "http.response.body", "body": b"ok"})

        wrapped = mod.HybridSecurityASGIWrapper(
            inner_app,
            mod.HybridSecurityConfig.from_env(
                auth_token="s" * 32,
                max_file_size_bytes=1024,
                enable_profile_endpoint=False,
            ),
        )

        async def run_req():
            scope = {
                "type": "http",
                "path": "/v1/convert",
                "headers": [
                    (b"authorization", b"Bearer " + b"s" * 32),
                    (b"content-length", b"-5"),
                ],
            }

            async def receive():
                return {"type": "http.request", "body": b"abc", "more_body": False}

            async def send(msg):
                sent.append(msg)

            await wrapped(scope, receive, send)

        asyncio.run(run_req())
        self.assertEqual(sent[0]["status"], 400)


class RootRuntimeSecurityTests(unittest.TestCase):
    def test_makefile_quotes_get_version_env_eval_and_orphan_assets_removed(self):
        self.assertFalse((REPO_ROOT / "rerank_server_demo.py").exists())
        self.assertFalse((REPO_ROOT / "test_agent_config.sh").exists())
        self.assertFalse((REPO_ROOT / "migrations/mysql").exists())

        makefile_text = (REPO_ROOT / "Makefile").read_text(encoding="utf-8")
        self.assertNotIn("@eval $$(./scripts/get_version.sh env)", makefile_text)
        self.assertIn('@eval "$$(./scripts/get_version.sh env)"', makefile_text)

    def test_homebrew_formula_skips_invalid_env_identifiers(self):
        formula = (REPO_ROOT / "Formula/genyee-lite.rb").read_text(encoding="utf-8")
        self.assertIn("[!A-Za-z0-9_]", formula)


class ScriptsAndCloudImageSecurityTests(unittest.TestCase):
    def test_build_images_enforces_gosumdb_by_default_and_quotes_build_args(self):
        script = (REPO_ROOT / "scripts/build_images.sh").read_text(encoding="utf-8")
        self.assertIn("GOSUMDB_ARG=${GOSUMDB:-sum.golang.org}", script)
        self.assertNotIn("GOSUMDB:-off", script)
        self.assertIn('"$PROJECT_ROOT/VERSION"', script)
        self.assertNotIn("--platform $PLATFORM", script)

    def test_get_version_quotes_env_output_and_resolves_project_root_from_any_cwd(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            pwn_file = Path(tmpdir) / "pwned"
            res = subprocess.run(
                ["bash", str(REPO_ROOT / "scripts/get_version.sh"), "env"],
                cwd=tmpdir,
                env={**os.environ, "EDITION": f"standard; touch {pwn_file}"},
                capture_output=True,
                text=True,
                check=True,
            )
            self.assertNotIn("unknown", res.stdout.splitlines()[0])
            eval_res = subprocess.run(
                ["bash", "-c", f'eval "$1"\nprintf "%s" "$EDITION"', "_", res.stdout],
                cwd=tmpdir,
                capture_output=True,
                text=True,
                check=True,
            )
            self.assertFalse(pwn_file.exists(), "eval of get_version.sh env must never execute commands")
            self.assertRegex(eval_res.stdout, r"^[A-Za-z0-9._+-]+$")

            json_res = subprocess.run(
                ["bash", str(REPO_ROOT / "scripts/get_version.sh"), "json"],
                cwd=tmpdir,
                env={**os.environ, "EDITION": 'bad"quote\nline'},
                capture_output=True,
                text=True,
                check=True,
            )
            parsed = yaml.safe_load(json_res.stdout)
            self.assertIsInstance(parsed["version"], str)
            self.assertNotEqual(parsed["version"], "unknown")

    def test_start_all_and_dev_populate_precopied_crlf_env_secrets_with_0600_mode(self):
        start_all = (REPO_ROOT / "scripts/start_all.sh").read_text(encoding="utf-8")
        # Extract gen_rand + ensure_env_secrets helper or verify start_all populates pre-copied .env with 0600.
        self.assertIn("chmod 600", start_all.replace("0600", "600"))
        self.assertIn("grep -Fxq --", start_all)

        # Execute dev.sh ensure_dev_secrets on a pre-existing 0644 .env file and verify 0600 mode.
        with tempfile.TemporaryDirectory() as tmpdir:
            env_file = Path(tmpdir) / ".env"
            env_file.write_text("DB_PASSWORD=\nJWT_SECRET=\nSYSTEM_AES_KEY=\n", encoding="utf-8")
            os.chmod(env_file, 0o644)
            dev_script = (REPO_ROOT / "scripts/dev.sh").read_text(encoding="utf-8")
            m_keys = re.search(r"(DEV_AUTO_SECRET_KEYS=\(.*?\n\))", dev_script, re.S)
            m_rand = re.search(r"(_gen_dev_secret\(\)\s*\(.*?\n\))", dev_script, re.S)
            m_ensure = re.search(r"(ensure_dev_secrets\(\)\s*\{.*?\n\})", dev_script, re.S)
            self.assertIsNotNone(m_keys)
            self.assertIsNotNone(m_rand)
            self.assertIsNotNone(m_ensure)
            snippet = (
                "set -euo pipefail\n"
                "log_info() { :; }\n"
                f"{m_keys.group(1)}\n"
                f"{m_rand.group(1)}\n"
                f"{m_ensure.group(1)}\n"
                f'ensure_dev_secrets "{env_file}"\n'
            )
            subprocess.run(["bash", "-c", snippet], check=True)
            mode = stat.S_IMODE(env_file.stat().st_mode)
            self.assertEqual(mode, 0o600)

            # Also verify start_all.sh check_env_file populates pre-copied CRLF .env with 0600 mode
            proj = Path(tmpdir) / "proj"
            proj.mkdir()
            crlf_env = b"DB_DRIVER=postgres\r\nSTORAGE_TYPE=local\r\nDB_PASSWORD=\r\nJWT_SECRET=\r\nSYSTEM_AES_KEY=\r\n"
            (proj / ".env.example").write_bytes(crlf_env)
            (proj / ".env").write_bytes(crlf_env)
            os.chmod(proj / ".env", 0o644)
            m_src = re.search(r"(_source_env_file\(\)\s*\{.*?\n\})", start_all, re.S)
            m_chk = re.search(r"(check_env_file\(\)\s*\{.*?\n\})", start_all, re.S)
            self.assertIsNotNone(m_src)
            self.assertIsNotNone(m_chk)
            start_snippet = (
                "set -euo pipefail\n"
                "log_info() { :; }\nlog_warning() { :; }\nlog_success() { :; }\nlog_error() { :; }\n"
                f'PROJECT_ROOT="{proj}"\n'
                f"{m_src.group(1)}\n"
                f"{m_chk.group(1)}\n"
                "check_env_file\n"
            )
            subprocess.run(["bash", "-c", start_snippet], check=True)
            self.assertEqual(stat.S_IMODE((proj / ".env").stat().st_mode), 0o600)
            populated = (proj / ".env").read_text(encoding="utf-8")
            self.assertRegex(populated, r"(?m)^DB_PASSWORD=[A-Za-z0-9]{32}\r?$")
            self.assertRegex(populated, r"(?m)^SYSTEM_AES_KEY=[A-Za-z0-9]{32}\r?$")

    def test_migrate_respects_caller_env_urlencodes_user_and_db_and_redacts_query_password(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            bin_dir = tmp / "bin"
            bin_dir.mkdir()
            fake_migrate = bin_dir / "migrate"
            log_file = tmp / "migrate.args"
            fake_migrate.write_text(
                f'#!/usr/bin/env bash\nprintf "%s\\n" "$@" > "{log_file}"\n',
                encoding="utf-8",
            )
            fake_migrate.chmod(0o755)

            # Create fake project layout with CRLF .env trying to override caller DB_HOST/DB_USER.
            scripts_dir = tmp / "scripts"
            scripts_dir.mkdir()
            (tmp / "migrations" / "versioned").mkdir(parents=True)
            migrate_copy = scripts_dir / "migrate.sh"
            migrate_copy.write_text(
                (REPO_ROOT / "scripts/migrate.sh").read_text(encoding="utf-8"),
                encoding="utf-8",
            )
            migrate_copy.chmod(0o755)
            (tmp / ".env").write_bytes(
                b"DB_HOST=from-dotenv.internal\r\nDB_USER=dotenvuser\r\nDB_PASSWORD=p@ss:1/2\r\nDB_NAME=db/name\r\n"
            )

            res = subprocess.run(
                ["bash", str(migrate_copy), "version"],
                cwd=tmp,
                env={
                    **os.environ,
                    "PATH": f"{bin_dir}:{os.environ['PATH']}",
                    "DB_HOST": "caller-host.example",
                    "DB_USER": "user:name@corp",
                },
                capture_output=True,
                text=True,
            )
            self.assertEqual(res.returncode, 0, res.stderr + res.stdout)
            recorded = log_file.read_text(encoding="utf-8").splitlines()
            db_arg = recorded[recorded.index("-database") + 1]
            self.assertIn("caller-host.example", db_arg)
            self.assertIn("user%3Aname%40corp:p%40ss%3A1%2F2@caller-host.example", db_arg)
            self.assertIn("/db%2Fname?", db_arg)

            # Query-string password redaction + force integer validation
            res_q = subprocess.run(
                ["bash", str(migrate_copy), "version"],
                cwd=tmp,
                env={
                    **os.environ,
                    "PATH": f"{bin_dir}:{os.environ['PATH']}",
                    "DB_URL": "postgres://db.example:5432/genyee?sslmode=disable&password=super-secret-query-pw",
                },
                capture_output=True,
                text=True,
            )
            self.assertEqual(res_q.returncode, 0)
            self.assertNotIn("super-secret-query-pw", res_q.stdout + res_q.stderr)

            bad_force = subprocess.run(
                ["bash", str(migrate_copy), "force", "not-an-int"],
                cwd=tmp,
                env={**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"},
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(bad_force.returncode, 0)

    def test_offline_load_rejects_symlinks_bundle_sets_searxng_secret_and_export_source_blocks_option_refs(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            bundle = Path(tmpdir) / "bundle"
            (bundle / "images").mkdir(parents=True)
            (bundle / "manifest.env").write_text("BUNDLE_VERSION=1.0.0\n", encoding="utf-8")
            (bundle / "SHA256SUMS").write_text("", encoding="utf-8")
            os.symlink("/etc/passwd", bundle / "images" / "escaped.tar")

            res = subprocess.run(
                ["bash", str(REPO_ROOT / "scripts/offline-load.sh"), "--bundle-dir", str(bundle), "--dry-run"],
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(res.returncode, 0)
            self.assertIn("symlink", (res.stderr + res.stdout).lower())

        bundle_script = (REPO_ROOT / "scripts/offline-bundle.sh").read_text(encoding="utf-8")
        self.assertIn("SEARXNG_SECRET", bundle_script)

        with tempfile.TemporaryDirectory() as tmpdir:
            bad_ref = subprocess.run(
                [
                    "bash",
                    str(REPO_ROOT / "scripts/export-source.sh"),
                    str(Path(tmpdir) / "out.tar.gz"),
                    "--exec=touch_pwn",
                ],
                cwd=tmpdir,
                capture_output=True,
                text=True,
            )
            self.assertNotEqual(bad_ref.returncode, 0)

            rel_out = subprocess.run(
                [
                    "bash",
                    str(REPO_ROOT / "scripts/export-source.sh"),
                    "rel-dir/source.tar.gz",
                    "HEAD",
                ],
                cwd=tmpdir,
                capture_output=True,
                text=True,
            )
            self.assertEqual(rel_out.returncode, 0, rel_out.stderr + rel_out.stdout)
            self.assertTrue((Path(tmpdir) / "rel-dir" / "source.tar.gz").is_file())

    def test_cloud_image_prepare_copies_bind_configs_substitutes_genyee_dir_and_cleanup_avoids_tmp_sink(self):
        cleanup_text = (REPO_ROOT / "scripts/cloud-image/cleanup.sh").read_text(encoding="utf-8")
        self.assertNotIn("/tmp/cleanup-secrets-found.txt", cleanup_text)

        prepare_text = (REPO_ROOT / "scripts/cloud-image/prepare.sh").read_text(encoding="utf-8")
        self.assertIn("--no-same-owner", prepare_text)
        self.assertIn("docker/searxng/settings.yml", prepare_text)
        self.assertIn("misc/dex-config.yaml", prepare_text)

        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            archive_dir = tmp / "repo" / "archive"
            archive_dir.mkdir(parents=True)
            source_tar = archive_dir / "main.tar.gz"
            with tarfile.open(source_tar, "w:gz") as tf:
                for rel_path, body in {
                    "docker-compose.yml": "services: {}\n",
                    ".env.example": "DB_PASSWORD=\nREDIS_PASSWORD=\nJWT_SECRET=\nSYSTEM_AES_KEY=\n",
                    "config/config.yaml": "server: {}\n",
                    "docker/searxng/settings.yml": "general: {}\n",
                    "misc/dex-config.yaml": "issuer: http://dex\n",
                }.items():
                    src_file = tmp / "src_stage" / "GENYEE-main" / rel_path
                    src_file.parent.mkdir(parents=True, exist_ok=True)
                    src_file.write_text(body, encoding="utf-8")
                    tf.add(src_file, arcname=f"GENYEE-main/{rel_path}")

            fake_bin = tmp / "bin"
            fake_bin.mkdir()
            for cmd_name in ("systemctl", "docker"):
                stub = fake_bin / cmd_name
                stub.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
                stub.chmod(0o755)

            target_dir = tmp / "custom-genyee"
            systemd_dir = tmp / "systemd"
            firstboot_bin = tmp / "sbin" / "genyee-firstboot.sh"

            res = subprocess.run(
                ["bash", str(REPO_ROOT / "scripts/cloud-image/prepare.sh")],
                env={
                    **os.environ,
                    "PATH": f"{fake_bin}:{os.environ['PATH']}",
                    "GENYEE_PREPARE_ALLOW_NON_ROOT": "1",
                    "GENYEE_REPO": f"file://{tmp}/repo",
                    "GENYEE_REF": "main",
                    "GENYEE_DIR": str(target_dir),
                    "SYSTEMD_UNIT_DIR": str(systemd_dir),
                    "FIRSTBOOT_BIN": str(firstboot_bin),
                },
                capture_output=True,
                text=True,
            )
            self.assertEqual(res.returncode, 0, res.stderr + res.stdout)
            self.assertTrue((target_dir / "docker/searxng/settings.yml").is_file())
            self.assertTrue((target_dir / "misc/dex-config.yaml").is_file())
            app_unit = (systemd_dir / "genyee.service").read_text(encoding="utf-8")
            firstboot_unit = (systemd_dir / "genyee-firstboot.service").read_text(encoding="utf-8")
            self.assertIn(f"WorkingDirectory={target_dir}", app_unit)
            self.assertNotIn("/opt/GENYEE", app_unit)
            self.assertIn(f"GENYEE_DIR={target_dir}", firstboot_unit)
            self.assertNotIn("/opt/GENYEE", firstboot_unit)


class McpDemoSecurityTests(unittest.TestCase):
    def test_mcp_auth_middleware_blocks_unauthenticated_websocket_and_start_sh_mints_32char_token_without_echoing_secret(self):
        mod = _load_module_with_stubs(
            "mcp_demo_server_under_test",
            REPO_ROOT / "examples/mcp-demo/server.py",
            _mcp_stubs(),
        )
        inner_called: list[str] = []

        async def inner_app(scope, receive, send):
            inner_called.append(scope["type"])

        mw = mod.MCPAuthMiddleware(inner_app, token="t" * 40)

        async def exercise():
            # Lifespan passes through
            await mw({"type": "lifespan"}, None, lambda msg: asyncio.sleep(0))
            # WebSocket without auth must NOT reach inner_app and must close with 4401
            ws_sent: list[dict] = []

            async def ws_send(msg):
                ws_sent.append(msg)

            await mw({"type": "websocket", "headers": []}, None, ws_send)
            return ws_sent

        ws_events = asyncio.run(exercise())
        self.assertEqual(inner_called, ["lifespan"])
        self.assertTrue(
            any(e.get("type") == "websocket.close" and e.get("code") == 4401 for e in ws_events),
            f"expected websocket.close 4401, got {ws_events}",
        )

        # Verify start.sh mints >= 32-char .mcp-demo-token (0600) and never prints caller secret
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = Path(tmpdir)
            start_copy = tmp / "start.sh"
            start_copy.write_text(
                (REPO_ROOT / "examples/mcp-demo/start.sh").read_text(encoding="utf-8"),
                encoding="utf-8",
            )
            start_copy.chmod(0o755)
            # Stub .venv/bin/{activate,python} + server.py so start.sh runs offline without pip/uvicorn
            (tmp / "server.py").write_text(
                "import os\nassert len(os.environ['MCP_SERVER_AUTH_TOKEN']) >= 32\n",
                encoding="utf-8",
            )
            venv_bin = tmp / ".venv" / "bin"
            venv_bin.mkdir(parents=True)
            (venv_bin / "activate").write_text(
                f'export PATH="{venv_bin}:$PATH"\n',
                encoding="utf-8",
            )
            py_stub = venv_bin / "python"
            py_stub.write_text(
                '#!/usr/bin/env bash\nif [[ "$1" == "-c" ]]; then exit 0; fi\nexec python3 "$@"\n',
                encoding="utf-8",
            )
            py_stub.chmod(0o755)

            env_no_tok = {k: v for k, v in os.environ.items() if k != "MCP_SERVER_AUTH_TOKEN"}
            res1 = subprocess.run(
                ["bash", str(start_copy)], cwd=tmp, env=env_no_tok, capture_output=True, text=True
            )
            self.assertEqual(res1.returncode, 0, res1.stderr + res1.stdout)
            token_file = tmp / ".mcp-demo-token"
            self.assertTrue(token_file.is_file())
            self.assertEqual(stat.S_IMODE(token_file.stat().st_mode), 0o600)
            minted = token_file.read_text(encoding="utf-8").strip()
            self.assertGreaterEqual(len(minted), 32)
            self.assertNotIn(minted, res1.stdout)

            caller_secret = "caller-super-secret-token-value-0123456789"
            res2 = subprocess.run(
                ["bash", str(start_copy)],
                cwd=tmp,
                env={**env_no_tok, "MCP_SERVER_AUTH_TOKEN": caller_secret},
                capture_output=True,
                text=True,
            )
            self.assertEqual(res2.returncode, 0, res2.stderr + res2.stdout)
            self.assertNotIn(caller_secret, res2.stdout)


if __name__ == "__main__":
    unittest.main()
