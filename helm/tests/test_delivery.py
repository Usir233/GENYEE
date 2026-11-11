"""Render the delivery contracts used by the offline image loader."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_app_storage import render


class DeliveryTests(unittest.TestCase):
    def documents(self, values=None):
        result = render(values)
        self.assertEqual(result.returncode, 0, result.stderr)
        return [doc for doc in yaml.safe_load_all(result.stdout) if doc]

    def deployments(self, values=None):
        return {d["metadata"]["labels"]["app.kubernetes.io/component"]: d
                for d in self.documents(values) if d["kind"] == "Deployment"}

    def test_private_registry_matches_offline_loader_paths(self):
        deployments = self.deployments({
            "global": {"imageRegistry": "harbor.example/customer/"},
            "app": {"image": {"tag": "customer-7"}},
            "frontend": {"image": {"tag": "customer-7"}},
            "docreader": {"image": {"tag": "customer-7"}},
        })
        for component, repository in {
            "app": "genyee-app:customer-7",
            "frontend": "genyee-ui:customer-7",
            "docreader": "genyee-docreader:customer-7",
            "database": "paradedb/paradedb:v0.25.11-pg17",
            "cache": "redis:7.2-alpine",
        }.items():
            actual = deployments[component]["spec"]["template"]["spec"]["containers"][0]["image"]
            self.assertEqual(actual, "harbor.example/customer/" + repository)

    def test_optional_cloud_switch_reaches_backend_and_frontend(self):
        for enabled in ("false", "true"):
            deployments = self.deployments({"app": {"env": {"GENYEE_WEKNORACLOUD_ENABLED": enabled}},
                                           "frontend": {"env": {"DOCS_BASE_URL": "/customer-docs/"}}})
            for component in ("app", "frontend"):
                env = {e["name"]: e.get("value") for e in
                       deployments[component]["spec"]["template"]["spec"]["containers"][0]["env"]}
                self.assertEqual(env["GENYEE_WEKNORACLOUD_ENABLED"], enabled)
                if component == "frontend":
                    self.assertEqual(env["DOCS_BASE_URL"], "/customer-docs/")

    def test_infrastructure_image_baseline_consistency(self):
        """Verify that infrastructure images in helm/values.yaml match the project baseline
        (docker-compose.yml and scripts/offline-bundle.sh) and Helm template limitations are documented.
        """
        repo_root = Path(__file__).resolve().parent.parent.parent
        values_path = repo_root / "helm" / "values.yaml"
        compose_path = repo_root / "docker-compose.yml"
        bundle_script_path = repo_root / "scripts" / "offline-bundle.sh"

        with open(values_path, "r", encoding="utf-8") as f:
            values_content = f.read()
            values = yaml.safe_load(values_content)

        with open(compose_path, "r", encoding="utf-8") as f:
            compose = yaml.safe_load(f)

        with open(bundle_script_path, "r", encoding="utf-8") as f:
            bundle_script = f.read()

        # 1. MinIO alignment in helm/values.yaml, docker-compose.yml, docker-compose.dev.yml, and scripts/offline-bundle.sh
        compose_dev_path = repo_root / "docker-compose.dev.yml"
        with open(compose_dev_path, "r", encoding="utf-8") as f:
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
        self.assertEqual(compose["services"]["postgres"]["image"], "paradedb/paradedb:v0.25.11-pg17")

        self.assertEqual(values["qdrant"]["image"]["repository"], "qdrant/qdrant")
        self.assertEqual(values["qdrant"]["image"]["tag"], "v1.16.2")
        self.assertEqual(compose["services"]["qdrant"]["image"], "qdrant/qdrant:v1.16.2")

        self.assertEqual(values["neo4j"]["image"]["repository"], "neo4j")
        self.assertEqual(values["neo4j"]["image"]["tag"], "2025.10.1")
        self.assertEqual(compose["services"]["neo4j"]["image"], "neo4j:2025.10.1")

        # 3. Redis alignment (pinned to 7.2-alpine for BSD-3-Clause compliance)
        self.assertEqual(values["redis"]["image"]["repository"], "redis")
        self.assertEqual(values["redis"]["image"]["tag"], "7.2-alpine")
        self.assertIn("redis:7.2-alpine", bundle_script)

        # 4. Comments verifying absence of built-in StatefulSets for MinIO/Qdrant
        self.assertIn("helm/templates currently does not bundle a built-in MinIO StatefulSet", values_content)
        self.assertIn("helm/templates currently does not bundle a built-in Qdrant StatefulSet", values_content)

    def test_homebrew_formula_linux_wrapper_execution(self):
        repo_root = Path(__file__).resolve().parent.parent.parent
        formula_path = repo_root / "Formula" / "genyee-lite.rb"
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
        dedented = "\n".join(line[min_indent:] if len(line) >= min_indent else line for line in raw_lines)

        with tempfile.TemporaryDirectory() as tmp_dir:
            pkgshare = Path(tmp_dir) / "pkgshare"
            (pkgshare / "config").mkdir(parents=True)
            (pkgshare / "config" / "config.yaml").write_text("server:\n  port: 8080\n", encoding="utf-8")
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
                'printf "DB_PATH=%s\\nLOCAL_STORAGE_BASE_DIR=%s\\n" "$DB_PATH" "$LOCAL_STORAGE_BASE_DIR"\n'
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

            res = subprocess.run(["bash", str(script_file)], env=env, capture_output=True, text=True, check=False)
            self.assertEqual(res.returncode, 0, f"stderr={res.stderr}\nstdout={res.stdout}")
            self.assertEqual(res.stderr, "", f"unexpected stderr: {res.stderr}")
            self.assertIn(f"DB_PATH={data_dir}/genyee.db\nLOCAL_STORAGE_BASE_DIR={data_dir}/files", res.stdout)
            self.assertTrue((config_dir / "config" / "config.yaml").is_file())
            self.assertFalse((config_dir / "config" / "config").exists())
            env_lite = (config_dir / ".env.lite").read_text(encoding="utf-8")
            self.assertIn(f"DB_PATH={data_dir}/genyee.db", env_lite)
            self.assertIn(f"LOCAL_STORAGE_BASE_DIR={data_dir}/files", env_lite)
            self.assertFalse((config_dir / ".env.lite.tmp").exists())
            self.assertFalse((config_dir / ".env.lite-e").exists())


if __name__ == "__main__":
    unittest.main()
