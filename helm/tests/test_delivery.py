"""Render the delivery contracts used by the offline image loader."""

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_app_storage import render


class DeliveryTests(unittest.TestCase):
    def documents(self, values=None):
        result = render(values)
        self.assertEqual(result.returncode, 0, result.stderr)
        return [doc for doc in yaml.safe_load_all(result.stdout) if doc]

    def deployments(self, values=None):
        return {
            d["metadata"]["labels"]["app.kubernetes.io/component"]: d
            for d in self.documents(values)
            if d["kind"] == "Deployment"
        }

    def test_private_registry_matches_offline_loader_paths(self):
        deployments = self.deployments(
            {
                "global": {"imageRegistry": "harbor.example/customer/"},
                "app": {"image": {"tag": "customer-7"}},
                "frontend": {"image": {"tag": "customer-7"}},
                "docreader": {"image": {"tag": "customer-7"}},
            }
        )
        for component, repository in {
            "app": "genyee-app:customer-7",
            "frontend": "genyee-ui:customer-7",
            "docreader": "genyee-docreader:customer-7",
            "database": "paradedb/paradedb:v0.25.11-pg17",
            "cache": "redis:7.2-alpine",
        }.items():
            actual = deployments[component]["spec"]["template"]["spec"]["containers"][0]["image"]
            self.assertEqual(actual, "harbor.example/customer/" + repository)

    def test_frontend_documentation_link(self):
        deployments = self.deployments({"frontend": {"env": {"DOCS_BASE_URL": "/customer-docs/"}}})
        env = {
            e["name"]: e.get("value")
            for e in deployments["frontend"]["spec"]["template"]["spec"]["containers"][0]["env"]
        }
        self.assertEqual(env["DOCS_BASE_URL"], "/customer-docs/")

    def test_infrastructure_image_baseline_consistency(self):
        """Verify that infrastructure images in helm/values.yaml match the project baseline
        (docker-compose.yml and scripts/offline-bundle.sh) and template caveats are documented.
        """
        repo_root = Path(__file__).resolve().parent.parent.parent
        values_path = repo_root / "helm" / "values.yaml"
        compose_path = repo_root / "docker-compose.yml"
        bundle_script_path = repo_root / "scripts" / "offline-bundle.sh"

        with open(values_path, encoding="utf-8") as f:
            values_content = f.read()
            values = yaml.safe_load(values_content)

        with open(compose_path, encoding="utf-8") as f:
            compose = yaml.safe_load(f)

        with open(bundle_script_path, encoding="utf-8") as f:
            bundle_script = f.read()

        # 1. MinIO alignment across Helm, Compose (prod & dev), and offline-bundle.sh
        compose_dev_path = repo_root / "docker-compose.dev.yml"
        with open(compose_dev_path, encoding="utf-8") as f:
            compose_dev = yaml.safe_load(f)

        expected_minio = "minio/minio:RELEASE.2025-09-07T16-13-09Z"
        self.assertEqual(values["minio"]["image"]["repository"], "minio/minio")
        self.assertEqual(values["minio"]["image"]["tag"], "RELEASE.2025-09-07T16-13-09Z")
        self.assertEqual(compose["services"]["minio"]["image"], expected_minio)
        self.assertEqual(compose["services"]["langfuse-minio"]["image"], expected_minio)
        self.assertEqual(compose_dev["services"]["minio"]["image"], expected_minio)
        self.assertEqual(compose_dev["services"]["langfuse-minio"]["image"], expected_minio)
        self.assertIn(expected_minio, bundle_script)
        self.assertNotIn("pgsty/minio", values_content)
        self.assertNotIn("pgsty/minio", bundle_script)

        # 2. Database & Vector DB alignment
        self.assertEqual(values["postgresql"]["image"]["repository"], "paradedb/paradedb")
        self.assertEqual(values["postgresql"]["image"]["tag"], "v0.25.11-pg17")
        self.assertEqual(
            compose["services"]["postgres"]["image"], "paradedb/paradedb:v0.25.11-pg17"
        )

        self.assertEqual(values["qdrant"]["image"]["repository"], "qdrant/qdrant")
        self.assertEqual(values["qdrant"]["image"]["tag"], "v1.16.2")
        self.assertEqual(compose["services"]["qdrant"]["image"], "qdrant/qdrant:v1.16.2")

        self.assertEqual(values["neo4j"]["image"]["repository"], "neo4j")
        self.assertEqual(values["neo4j"]["image"]["tag"], "2025.10.1")
        self.assertEqual(compose["services"]["neo4j"]["image"], "neo4j:2025.10.1")

        # 3. Redis alignment (pinned to 7.2-alpine for BSD-3-Clause compliance)
        self.assertEqual(values["redis"]["image"]["repository"], "redis")
        self.assertEqual(values["redis"]["image"]["tag"], "7.2-alpine")
        self.assertEqual(compose["services"]["redis"]["image"], "redis:7.2-alpine")
        self.assertEqual(compose_dev["services"]["redis"]["image"], "redis:7.2-alpine")
        self.assertIn("redis:7.2-alpine", bundle_script)
        self.assertNotIn("redis:7.0-alpine", bundle_script)

        # 4. Compose Redis persistence, exec-args & healthcheck + dev parity + Dockerfile hardening
        self.assertIn("redis_data:/data", compose["services"]["redis"]["volumes"])
        self.assertIn("redis_data", compose["volumes"])
        for c_name, c_doc in (
            ("docker-compose.yml", compose),
            ("docker-compose.dev.yml", compose_dev),
        ):
            redis_svc = c_doc["services"]["redis"]
            self.assertIsInstance(redis_svc["command"], list, c_name)
            self.assertIn("healthcheck", redis_svc, c_name)
            dr_probe = " ".join(c_doc["services"]["docreader"]["healthcheck"]["test"])
            self.assertIn("DOCREADER_GRPC_PORT", dr_probe, c_name)
            self.assertIn("GRPC_TLS_ENABLED", dr_probe, c_name)

        self.assertEqual(
            compose_dev["services"]["odl-hybrid"]["container_name"],
            "GENYEE-odl-hybrid-dev",
        )
        self.assertIn(
            "PRELOAD_MODELS",
            str(compose_dev["services"]["odl-hybrid"]["build"].get("args", "")),
        )
        dev_dr_env = "\n".join(compose_dev["services"]["docreader"]["environment"])
        for key in (
            "DOCREADER_HTML_MAIN_CONTENT",
            "DOCREADER_XLSX_PERCENT_AS_TEXT",
            "DOCREADER_XLSX_PCT_ROW_HINT_MIN",
            "GENYEE_PREPROCESS_PAD",
            "GENYEE_PREPROCESS_MIN_SCORE",
            "GENYEE_PREPROCESS_ANG_TOL",
            "GENYEE_PREPROCESS_SIZE_MIN",
            "GENYEE_PREPROCESS_QUAD_TOL",
        ):
            self.assertIn(key, dev_dr_env)

        prod_app_env = "\n".join(compose["services"]["app"]["environment"])
        self.assertIn("GENYEE_CORS_ALLOWED_ORIGINS", prod_app_env)
        self.assertIn("OIDC_USER_INFO_MAPPING_USERNAME", prod_app_env)
        self.assertNotIn("OIDC_USER_INFO_MAPPING_USER_NAME=", prod_app_env)
        self.assertNotIn("APK_MIRROR_ARG=", prod_app_env)
        self.assertNotIn("GO_VERSION_ARG", str(compose["services"]["app"]["build"].get("args", "")))

        mcp_dockerfile = (repo_root / "mcp-server" / "Dockerfile").read_text(encoding="utf-8")
        self.assertIn('org.opencontainers.image.title="genyee-mcp"', mcp_dockerfile)
        self.assertIn("MCP_TRANSPORT=http", mcp_dockerfile)
        self.assertIn('CMD ["genyee-mcp-server"]', mcp_dockerfile)
        self.assertIn("HEALTHCHECK", mcp_dockerfile)
        self.assertNotIn("COPY . .", mcp_dockerfile)

        app_dockerfile = (repo_root / "docker" / "Dockerfile.app").read_text(encoding="utf-8")
        self.assertNotIn("default-mysql-client", app_dockerfile)
        self.assertNotIn("COPY --from=builder /app/scripts ./scripts", app_dockerfile)

        dr_dockerfile = (repo_root / "docker" / "Dockerfile.docreader").read_text(encoding="utf-8")
        self.assertNotIn("COPY testdata/", dr_dockerfile)

        # 4. Comments verifying absence of built-in StatefulSets for MinIO/Qdrant
        self.assertIn(
            "helm/templates currently does not bundle a built-in MinIO StatefulSet",
            values_content,
        )
        self.assertIn(
            "helm/templates currently does not bundle a built-in Qdrant StatefulSet",
            values_content,
        )

    def test_homebrew_formula_linux_wrapper_execution(self):
        repo_root = Path(__file__).resolve().parent.parent.parent
        formula_path = repo_root / "Formula" / "genyee-lite.rb"
        if not formula_path.exists():
            self.skipTest("Formula/genyee-lite.rb is omitted in exported source archives")
        content = formula_path.read_text(encoding="utf-8")

        self.assertNotIn("sed -i ''", content)
        self.assertNotIn(".env.lite-e", content)
        self.assertIn('cp -R "#{pkgshare}/config/." "$CONFIG_DIR/config/"', content)

        start_marker = '(bin/"genyee-lite").write <<~SH\n'
        end_marker = "    SH\n"
        start_idx = content.index(start_marker) + len(start_marker)
        end_idx = content.index(end_marker, start_idx)
        raw_lines = content[start_idx:end_idx].splitlines()
        min_indent = min(len(line) - len(line.lstrip()) for line in raw_lines if line.strip())
        dedented = "\n".join(
            line[min_indent:] if len(line) >= min_indent else line for line in raw_lines
        )

        with tempfile.TemporaryDirectory() as tmp_dir:
            pkgshare = Path(tmp_dir) / "pkgshare"
            (pkgshare / "config").mkdir(parents=True)
            (pkgshare / "config" / "config.yaml").write_text(
                "server:\n  port: 8080\n", encoding="utf-8"
            )
            (pkgshare / "migrations" / "sqlite").mkdir(parents=True)
            (pkgshare / "web").mkdir(parents=True)
            (pkgshare / "web" / "index.html").write_text("<html></html>\n", encoding="utf-8")
            (pkgshare / ".env.lite.example").write_text(
                "DB_DRIVER=sqlite\nDB_PATH=./data/genyee.db\nLOCAL_STORAGE_BASE_DIR=./data/files\n",
                encoding="utf-8",
            )
            libexec_dir = Path(tmp_dir) / "libexec"
            libexec_dir.mkdir(parents=True)
            mock_bin = libexec_dir / "GENYEE-lite"
            mock_bin.write_text(
                "#!/usr/bin/env bash\n"
                'printf "DB_PATH=%s\\nLOCAL_STORAGE_BASE_DIR=%s\\n" '
                '"$DB_PATH" "$LOCAL_STORAGE_BASE_DIR"\n'
                "exit 0\n",
                encoding="utf-8",
            )
            mock_bin.chmod(0o755)

            rendered = (
                dedented.replace("#{pkgshare}", str(pkgshare))
                .replace("#{libexec}", str(libexec_dir))
                .replace(r"\\", "\\")
            )
            script_file = Path(tmp_dir) / "genyee-lite.sh"
            script_file.write_text(rendered + "\n", encoding="utf-8")
            script_file.chmod(0o755)

            data_dir = Path(tmp_dir) / "data&dir|special $var `uname`"
            config_dir = Path(tmp_dir) / "config_dir"
            env = os.environ.copy()
            env["GENYEE_DATA_DIR"] = str(data_dir)
            env["GENYEE_CONFIG_DIR"] = str(config_dir)

            res = subprocess.run(
                ["bash", str(script_file)],
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(res.returncode, 0, f"stderr={res.stderr}\nstdout={res.stdout}")
            self.assertEqual(res.stderr, "", f"unexpected stderr: {res.stderr}")
            self.assertIn(
                f"DB_PATH={data_dir}/genyee.db\nLOCAL_STORAGE_BASE_DIR={data_dir}/files",
                res.stdout,
            )
            self.assertTrue((config_dir / "config" / "config.yaml").is_file())
            self.assertFalse((config_dir / "config" / "config").exists())
            env_lite = (config_dir / ".env.lite").read_text(encoding="utf-8")
            self.assertIn(f"DB_PATH={data_dir}/genyee.db", env_lite)
            self.assertIn(f"LOCAL_STORAGE_BASE_DIR={data_dir}/files", env_lite)
            self.assertEqual((config_dir / ".env.lite").stat().st_mode & 0o777, 0o600)
            self.assertFalse((config_dir / ".env.lite.tmp").exists())
            self.assertFalse((config_dir / ".env.lite-e").exists())

    def test_security_hardening_secrets_probes_networkpolicy_and_ingress(self):
        chart_dir = Path(__file__).resolve().parents[1]
        secret_render = subprocess.run(
            [
                "helm",
                "template",
                "audit",
                str(chart_dir),
                "--namespace",
                "audit",
                "--set",
                "secrets.dbPassword=test-db-pw",
                "--set",
                "secrets.redisPassword=test-redis-pw",
                "--set",
                "secrets.jwtSecret=test-jwt-secret-0123456789abcdef",
            ],
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(secret_render.returncode, 0, secret_render.stderr)
        secret_docs = [d for d in yaml.safe_load_all(secret_render.stdout) if d]
        secret = next(d for d in secret_docs if d["kind"] == "Secret")
        self.assertIn("GRPC_AUTH_TOKEN", secret["stringData"])
        self.assertIn("SYSTEM_SIGNING_KEY", secret["stringData"])
        self.assertGreaterEqual(len(secret["stringData"]["GRPC_AUTH_TOKEN"]), 32)
        self.assertGreaterEqual(len(secret["stringData"]["SYSTEM_SIGNING_KEY"]), 32)

        docs = self.documents(
            {
                "ingress": {
                    "enabled": True,
                    "hosts": [
                        {
                            "host": "genyee.example.com",
                            "paths": [{"path": "/", "pathType": "Prefix"}],
                        }
                    ],
                },
            }
        )
        deployments = {
            d["metadata"]["labels"]["app.kubernetes.io/component"]: d
            for d in docs
            if d["kind"] == "Deployment"
        }

        app_env = {
            e["name"]: e
            for e in deployments["app"]["spec"]["template"]["spec"]["containers"][0]["env"]
        }
        docreader_env = {
            e["name"]: e
            for e in deployments["docreader"]["spec"]["template"]["spec"]["containers"][0]["env"]
        }
        self.assertEqual(
            app_env["GRPC_AUTH_TOKEN"]["valueFrom"]["secretKeyRef"]["key"],
            "GRPC_AUTH_TOKEN",
        )
        self.assertEqual(
            app_env["SYSTEM_SIGNING_KEY"]["valueFrom"]["secretKeyRef"]["key"],
            "SYSTEM_SIGNING_KEY",
        )
        self.assertEqual(
            docreader_env["GRPC_AUTH_TOKEN"]["valueFrom"]["secretKeyRef"]["key"],
            "GRPC_AUTH_TOKEN",
        )
        self.assertEqual(
            app_env["GRPC_AUTH_TOKEN"]["valueFrom"]["secretKeyRef"]["name"],
            docreader_env["GRPC_AUTH_TOKEN"]["valueFrom"]["secretKeyRef"]["name"],
        )

        # Redis probes must use REDISCLI_AUTH + direct argv instead of sh -c "$REDIS_PASSWORD"
        redis_container = deployments["cache"]["spec"]["template"]["spec"]["containers"][0]
        redis_env_names = {e["name"] for e in redis_container["env"]}
        self.assertIn("REDISCLI_AUTH", redis_env_names)
        for probe_name in ("livenessProbe", "readinessProbe"):
            cmd = redis_container[probe_name]["exec"]["command"]
            self.assertEqual(cmd, ["redis-cli", "-e", "--no-auth-warning", "ping"])

        # NetworkPolicy baseline rendered by default
        netpols = [d for d in docs if d["kind"] == "NetworkPolicy"]
        self.assertGreaterEqual(len(netpols), 3)

        # Ingress blocks external access to /api/v1/local-browser/internal
        ingress = next(d for d in docs if d["kind"] == "Ingress")
        ingress_paths = ingress["spec"]["rules"][0]["http"]["paths"]
        self.assertEqual(ingress_paths[0]["path"], "/api/v1/local-browser/internal")

        headless_docs = self.documents(
            {
                "frontend": {"enabled": False},
                "ingress": {"enabled": True, "host": "api.genyee.example.com"},
            }
        )
        headless_ingress = next(d for d in headless_docs if d["kind"] == "Ingress")
        headless_annotations = headless_ingress["metadata"].get("annotations", {})
        self.assertIn(
            "/api/v1/local-browser/internal",
            headless_annotations.get("nginx.ingress.kubernetes.io/server-snippet", ""),
        )


if __name__ == "__main__":
    unittest.main()
