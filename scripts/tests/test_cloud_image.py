"""Image selection and ops shell regressions (dev.sh restart, check-env.sh, build_images.sh)."""

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

SELECTOR = Path(__file__).resolve().parents[1] / "cloud-image" / "select-old-images.sh"
REPO_ROOT = Path(__file__).resolve().parents[2]


class CloudImageSelectionTest(unittest.TestCase):
    def select(self, registry, keep, references):
        result = subprocess.run(
            ["sh", str(SELECTOR), registry, keep],
            input="\n".join(references) + "\n",
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout.splitlines()

    def test_default_registry_keeps_current_and_infrastructure(self):
        self.assertEqual(
            self.select(
                "genyee",
                "v0.9.0",
                [
                    "genyee/genyee-app:v0.8.3",
                    "genyee/genyee-ui:v0.9.0",
                    "genyee/genyee-sandbox:v0.9.0",
                    "redis:7.2-alpine",
                    "paradedb/paradedb:v0.25.11-pg17",
                ],
            ),
            ["genyee/genyee-app:v0.8.3"],
        )

    def test_private_registry_does_not_select_default_registry(self):
        self.assertEqual(
            self.select(
                "registry.example:5000/team",
                "latest",
                [
                    "registry.example:5000/team/genyee-app:old",
                    "registry.example:5000/team/genyee-app:latest",
                    "genyee/genyee-app:old",
                    "registry.example:5000/other/genyee-app:old",
                    "registry.example:5000/team/other-app:old",
                ],
            ),
            ["registry.example:5000/team/genyee-app:old"],
        )

    def test_registry_and_tag_are_literal_values(self):
        self.assertEqual(
            self.select(
                "registry.example/team",
                "v0.9.0",
                [
                    "registryXexample/team/genyee-app:old",
                    "registry.example/team/genyee-app:v0x9x0",
                    "registry.example/team/genyee-app:v0.9.0",
                    "registry.example/team/genyee-app:v0.9.0-extra",
                ],
            ),
            [
                "registry.example/team/genyee-app:v0x9x0",
                "registry.example/team/genyee-app:v0.9.0-extra",
            ],
        )

    def test_empty_input_selects_nothing(self):
        self.assertEqual(self.select("genyee", "latest", []), [])

    def test_dev_restart_forwards_profile_flags_and_check_env_supports_sqlite_and_aes_validation(
        self,
    ):
        with tempfile.TemporaryDirectory(prefix="genyee-ops-shell-") as tmp:
            workdir = Path(tmp)
            scripts_dir = workdir / "scripts"
            scripts_dir.mkdir()
            shutil.copy2(REPO_ROOT / "scripts" / "dev.sh", scripts_dir / "dev.sh")
            shutil.copy2(REPO_ROOT / "scripts" / "check-env.sh", scripts_dir / "check-env.sh")

            bin_dir = workdir / "bin"
            bin_dir.mkdir()
            call_log = workdir / "docker.log"
            docker_stub = bin_dir / "docker"
            docker_stub.write_text(
                f'#!/usr/bin/env bash\nprintf "%s\\n" "$*" >> "{call_log}"\nexit 0\n',
                encoding="utf-8",
            )
            docker_stub.chmod(0o755)
            for tool, out in (
                ("go", "go version go1.27.3 linux/amd64"),
                ("npm", "10.0.0"),
                ("curl", "ok"),
            ):
                stub = bin_dir / tool
                stub.write_text(f"#!/usr/bin/env bash\necho '{out}'\n", encoding="utf-8")
                stub.chmod(0o755)
            # Replace sleep so restart runs instantaneously in tests.
            sleep_stub = bin_dir / "sleep"
            sleep_stub.write_text("#!/usr/bin/env bash\nexit 0\n", encoding="utf-8")
            sleep_stub.chmod(0o755)

            shutil.copy2(REPO_ROOT / ".env.example", workdir / ".env")
            env = {
                **os.environ,
                "PATH": f"{bin_dir}{os.pathsep}{os.environ.get('PATH', '')}",
            }
            env.pop("DEV_REMOTE_HOST", None)
            res = subprocess.run(
                ["bash", str(scripts_dir / "dev.sh"), "restart", "--minio", "--neo4j"],
                cwd=workdir,
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
            calls = call_log.read_text(encoding="utf-8")
            self.assertIn("--profile minio", calls)
            self.assertIn("--profile neo4j", calls)

            # SQLite Lite .env without DB_HOST/DB_PASSWORD/OLLAMA_BASE_URL must pass check-env.sh
            (workdir / ".env").write_text(
                "DB_DRIVER=sqlite\n"
                "DB_PATH=./data/genyee.db\n"
                "STORAGE_TYPE=local\n"
                "STREAM_MANAGER_TYPE=memory\n"
                "JWT_SECRET=0123456789abcdef0123456789abcdef\n"
                "SYSTEM_AES_KEY=0123456789abcdef0123456789abcdef\n"
                "DOCREADER_ALLOW_UNAUTHENTICATED=true\n",
                encoding="utf-8",
            )
            sqlite_ok = subprocess.run(
                ["bash", str(scripts_dir / "check-env.sh")],
                cwd=workdir,
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertEqual(sqlite_ok.returncode, 0, sqlite_ok.stdout + sqlite_ok.stderr)
            self.assertNotIn("INIT_LLM_MODEL_NAME", sqlite_ok.stdout)

            # Wrong-length SYSTEM_AES_KEY must fail check-env.sh
            (workdir / ".env").write_text(
                "DB_DRIVER=sqlite\n"
                "STORAGE_TYPE=local\n"
                "STREAM_MANAGER_TYPE=memory\n"
                "JWT_SECRET=0123456789abcdef0123456789abcdef\n"
                "SYSTEM_AES_KEY=too-short\n"
                "DOCREADER_ALLOW_UNAUTHENTICATED=true\n",
                encoding="utf-8",
            )
            bad_aes = subprocess.run(
                ["bash", str(scripts_dir / "check-env.sh")],
                cwd=workdir,
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )
            self.assertNotEqual(bad_aes.returncode, 0)


if __name__ == "__main__":
    unittest.main()
