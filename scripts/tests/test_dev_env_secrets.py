"""Unit tests for scripts/dev.sh initial secret generation, CRLF .env support, and scripts/migrate.sh defaults."""

import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]

EXPECTED_SECRET_KEYS = (
    "DB_PASSWORD",
    "REDIS_PASSWORD",
    "JWT_SECRET",
    "SYSTEM_AES_KEY",
    "SYSTEM_SIGNING_KEY",
    "GRPC_AUTH_TOKEN",
    "ODL_HYBRID_AUTH_TOKEN",
)


class DevEnvSecretsAndMigrateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="genyee-dev-env-test-")
        self.addCleanup(self.tmp.cleanup)
        self.workdir = Path(self.tmp.name)
        scripts_dir = self.workdir / "scripts"
        scripts_dir.mkdir(parents=True)
        shutil.copy2(ROOT / "scripts" / "dev.sh", scripts_dir / "dev.sh")
        shutil.copy2(ROOT / "scripts" / "check-env.sh", scripts_dir / "check-env.sh")
        shutil.copy2(ROOT / "scripts" / "migrate.sh", scripts_dir / "migrate.sh")

    def _parse_env_lines(self, raw_text: str) -> dict[str, str]:
        values: dict[str, str] = {}
        for line in raw_text.splitlines():
            stripped = line.rstrip("\r")
            if not stripped or stripped.lstrip().startswith("#") or "=" not in stripped:
                continue
            key, val = stripped.split("=", 1)
            values[key] = val
        return values

    def test_ensure_dev_secrets_populates_all_required_keys_from_lf_env_example(self):
        env_path = self.workdir / ".env"
        shutil.copy2(ROOT / ".env.example", env_path)

        before = self._parse_env_lines(env_path.read_text(encoding="utf-8"))
        for key in EXPECTED_SECRET_KEYS:
            self.assertEqual(before.get(key), "", f"Expected {key} to start empty in .env.example")

        cmd = (
            'source "./scripts/dev.sh" && '
            'ensure_dev_secrets ".env" && '
            'load_env_files && '
            'for k in DB_PASSWORD REDIS_PASSWORD JWT_SECRET SYSTEM_AES_KEY SYSTEM_SIGNING_KEY GRPC_AUTH_TOKEN ODL_HYBRID_AUTH_TOKEN; do '
            '  printf "%s=%s\\n" "$k" "${!k}"; '
            'done'
        )
        res = subprocess.run(
            ["bash", "-c", cmd],
            cwd=self.workdir,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)

        after_raw = env_path.read_bytes()
        self.assertNotIn(b"\r\n", after_raw)
        after = self._parse_env_lines(after_raw.decode("utf-8"))
        loaded = self._parse_env_lines(res.stdout)

        generated_values = set()
        for key in EXPECTED_SECRET_KEYS:
            val = after.get(key, "")
            self.assertRegex(val, r"^[A-Za-z0-9]{32}$", f"Invalid generated secret for {key}")
            self.assertEqual(loaded.get(key), val)
            generated_values.add(val)

        # Every secret should be independently generated, not reusing a single random value.
        self.assertEqual(len(generated_values), len(EXPECTED_SECRET_KEYS))
        self.assertFalse((self.workdir / ".env.bak").exists())

        # Verify scripts/check-env.sh succeeds on the populated .env when Go & npm are mocked.
        mock_bin = self.workdir / "bin"
        mock_bin.mkdir()
        for tool, out in (("go", "go version go1.24.0 linux/amd64"), ("npm", "10.0.0"), ("docker", "Docker 27.0")):
            p = mock_bin / tool
            p.write_text(f"#!/usr/bin/env bash\nif [[ \"${{1:-}}\" == \"info\" ]]; then exit 0; fi\necho '{out}'\n", encoding="utf-8")
            p.chmod(0o755)

        env = dict(os.environ, PATH=f"{mock_bin}{os.pathsep}{os.environ.get('PATH', '')}")
        check_res = subprocess.run(
            ["bash", str(self.workdir / "scripts" / "check-env.sh")],
            cwd=self.workdir,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(check_res.returncode, 0, check_res.stdout + check_res.stderr)

    def test_ensure_dev_secrets_supports_crlf_and_preserves_non_empty_values(self):
        example_lf = (ROOT / ".env.example").read_text(encoding="utf-8")
        # Pre-set two keys (including one ending in 'r' to catch BSD sed \r regressions) and convert to CRLF.
        preset = re.sub(r"^DB_PASSWORD=.*$", "DB_PASSWORD=CustomDbPassEndingIn_r", example_lf, flags=re.M)
        preset = re.sub(r"^JWT_SECRET=.*$", "JWT_SECRET=CustomJwtSecretValue1234567890!!", preset, flags=re.M)
        crlf_bytes = preset.replace("\r\n", "\n").replace("\n", "\r\n").encode("utf-8")

        env_path = self.workdir / ".env"
        env_path.write_bytes(crlf_bytes)

        mock_bin = self.workdir / "mock_bin"
        mock_bin.mkdir()
        docker_stub = mock_bin / "docker"
        docker_stub.write_text(
            "#!/usr/bin/env bash\n"
            "exit 0\n",
            encoding="utf-8",
        )
        docker_stub.chmod(0o755)

        env = dict(
            os.environ,
            PATH=f"{mock_bin}{os.pathsep}{os.environ.get('PATH', '')}",
            DEV_REMOTE_HOST="127.0.0.1",
        )
        # Run `./scripts/dev.sh start` end-to-end (DEV_REMOTE_HOST short-circuits compose up after secret gen & load).
        res = subprocess.run(
            ["bash", str(self.workdir / "scripts" / "dev.sh"), "start"],
            cwd=self.workdir,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)

        updated_text = env_path.read_bytes().decode("utf-8")
        after = self._parse_env_lines(updated_text)
        self.assertEqual(after["DB_PASSWORD"], "CustomDbPassEndingIn_r")
        self.assertEqual(after["JWT_SECRET"], "CustomJwtSecretValue1234567890!!")

        for key in EXPECTED_SECRET_KEYS:
            if key in ("DB_PASSWORD", "JWT_SECRET"):
                continue
            self.assertRegex(after.get(key, ""), r"^[A-Za-z0-9]{32}$", f"CRLF empty {key} was not populated")

        # Verify load_env_files strips CR cleanly and preserves trailing 'r' characters.
        verify_cmd = (
            'source "./scripts/dev.sh" && '
            'load_env_files && '
            'printf "DB=%s|GRPC=%s" "$DB_PASSWORD" "$GRPC_AUTH_TOKEN"'
        )
        load_res = subprocess.run(
            ["bash", "-c", verify_cmd],
            cwd=self.workdir,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(load_res.returncode, 0, load_res.stderr)
        self.assertNotIn("\r", load_res.stdout)
        self.assertEqual(
            load_res.stdout,
            f"DB=CustomDbPassEndingIn_r|GRPC={after['GRPC_AUTH_TOKEN']}",
        )

    def test_migrate_script_defaults_to_lowercase_genyee_db_name(self):
        mock_bin = self.workdir / "migrate_bin"
        mock_bin.mkdir()
        recorded = self.workdir / "migrate_args.txt"
        migrate_stub = mock_bin / "migrate"
        migrate_stub.write_text(
            "#!/usr/bin/env bash\n"
            f'printf "%s\\n" "$@" > "{recorded}"\n'
            "exit 0\n",
            encoding="utf-8",
        )
        migrate_stub.chmod(0o755)

        env = {
            "PATH": f"{mock_bin}{os.pathsep}{os.environ.get('PATH', '')}",
            "HOME": str(self.workdir),
        }
        res = subprocess.run(
            ["bash", str(self.workdir / "scripts" / "migrate.sh"), "up"],
            cwd=self.workdir,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )
        self.assertEqual(res.returncode, 0, res.stdout + res.stderr)
        args = recorded.read_text(encoding="utf-8").splitlines()
        self.assertIn("postgres://postgres:@localhost:5432/genyee?sslmode=disable", args)
        self.assertIn("DB_NAME: genyee", res.stdout)


if __name__ == "__main__":
    unittest.main()
