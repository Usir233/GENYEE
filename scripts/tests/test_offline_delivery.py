"""Exercise the real bundle/loader scripts without downloading container images.

COMPOSE_BIN and HELM_BIN enable real configuration rendering in addition to the
Docker command fixture. No daemon or registry is contacted by these tests.
"""

import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class OfflineDeliveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="genyee-delivery-test-")
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name)
        binary = self.path / "bin"
        binary.mkdir()
        docker = binary / "docker"
        docker.write_text(
            """#!/usr/bin/env python3
import json, os, sys
with open(os.environ['DOCKER_CALL_LOG'], 'a') as f:
    f.write(json.dumps(sys.argv[1:]) + '\\n')
args = sys.argv[1:]
if args[0] == 'save':
    sys.stdout.buffer.write(b'image archive fixture')
elif args[:2] == ['image', 'inspect']:
    plat = os.environ.get('TEST_IMAGE_PLATFORM', 'linux/amd64')
    print(plat if '.Os' in args[3] else 'sha256:fixture')
else:
    print('Docker progress output: ' + ' '.join(args))
""",
        )
        docker.chmod(0o755)
        self.log = self.path / "docker-calls.jsonl"
        self.env = dict(
            os.environ,
            PATH=str(binary) + os.pathsep + os.environ["PATH"],
            DOCKER_CALL_LOG=str(self.log),
        )
        # Never let the operator's deployment configuration change the fixture.
        for key in (
            "GENYEE_REGISTRY",
            "GENYEE_VERSION",
            "PLATFORM",
            "COMPOSE_PROFILES",
        ):
            self.env.pop(key, None)
        self.bundle = self.path / "bundle"

    def command(self, script, *args, success=True):
        result = subprocess.run(
            ["bash", str(ROOT / "scripts" / script), *map(str, args)],
            cwd=ROOT,
            env=self.env,
            text=True,
            capture_output=True,
            check=False,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def build(self, *args):
        return self.command("offline-bundle.sh", "--output", self.bundle, *args)

    def manifest(self):
        return json.loads((self.bundle / "MANIFEST.json").read_text())

    def environment(self):
        return dict(
            line.split("=", 1)
            for line in (self.bundle / ".env").read_text().splitlines()
            if line and not line.startswith("#") and "=" in line
        )

    def calls(self):
        return (
            [json.loads(line) for line in self.log.read_text().splitlines()]
            if self.log.exists()
            else []
        )

    def test_real_export_manifest_and_loader_registry_roundtrip(self):
        self.build(
            "--profiles",
            "all",
            "--registry",
            "build.example/customer",
            "--tag",
            "0.9.0",
        )
        manifest = self.manifest()
        self.assertFalse(manifest["manifest_only"])
        self.assertEqual(manifest["version"], "0.9.0")
        self.assertTrue(any(c[0] == "pull" for c in self.calls()))
        self.assertEqual(
            len(manifest["images"]),
            sum(c[0] == "save" for c in self.calls()),
        )
        self.command(
            "offline-load.sh",
            "--bundle-dir",
            self.bundle,
            "--registry",
            "harbor.example/customer",
            "--push",
        )
        calls = self.calls()
        self.assertEqual(len(manifest["images"]), sum(c[0] == "load" for c in calls))
        self.assertIn(
            [
                "tag",
                "build.example/customer/genyee-app:0.9.0",
                "harbor.example/customer/genyee-app:0.9.0",
            ],
            calls,
        )
        self.assertIn(
            ["push", "harbor.example/customer/paradedb/paradedb:v0.25.11-pg17"],
            calls,
        )
        self.assertEqual(self.environment()["GENYEE_VERSION"], "0.9.0")

    def test_generated_env_matches_selected_images_and_secure_defaults(self):
        self.build(
            "--manifest-only",
            "--profiles",
            "neo4j",
            "--tag",
            "customer-7",
            "--registry",
            "registry.example/customer",
        )
        env = self.environment()
        self.assertEqual(env["COMPOSE_PROFILES"], "neo4j")
        self.assertEqual(env["GENYEE_VERSION"], "customer-7")
        self.assertEqual(env["GENYEE_REGISTRY"], "registry.example/customer")
        self.assertEqual(
            env["GENYEE_SANDBOX_DOCKER_IMAGE"],
            "registry.example/customer/genyee-sandbox:customer-7",
        )
        self.assertEqual(env["DB_NAME"], "genyee")
        self.assertEqual(env["DB_USER"], "postgres")
        for key in ("DB_PASSWORD", "REDIS_PASSWORD", "JWT_SECRET", "NEO4J_PASSWORD"):
            self.assertGreaterEqual(len(env[key]), 32)
        self.assertEqual(len(env["SYSTEM_AES_KEY"]), 32)
        self.assertEqual(env["LANGFUSE_ENABLED"], "false")
        self.assertFalse(self.calls())

    def test_langfuse_secrets_are_generated_only_for_explicit_profile(self):
        self.build("--manifest-only", "--profiles", "langfuse")
        env = self.environment()
        keys = (
            "LANGFUSE_NEXTAUTH_SECRET",
            "LANGFUSE_SALT",
            "LANGFUSE_CLICKHOUSE_PASSWORD",
            "LANGFUSE_MINIO_PASSWORD",
            "LANGFUSE_ENCRYPTION_KEY",
        )
        for key in keys:
            self.assertGreaterEqual(len(env[key]), 48, key)
        self.assertEqual(len(env["LANGFUSE_ENCRYPTION_KEY"]), 64)
        self.assertEqual(len({env[key] for key in keys}), len(keys))
        self.assertEqual(env["LANGFUSE_ENABLED"], "false")

    def test_valid_tag_with_repeated_dots_roundtrips(self):
        self.build("--skip-pull", "--tag", "customer..7")
        self.command("offline-load.sh", "--bundle-dir", self.bundle)

    def test_missing_checksum_entry_fails_before_loading(self):
        self.build("--skip-pull")
        self.log.write_text("")
        checksum = self.bundle / "SHA256SUMS"
        content = checksum.read_text()
        for name in (
            "MANIFEST.json",
            "IMAGES.tsv",
            "COMPOSE_IMAGES.tsv",
            "docker-compose.yml",
            ".env",
            "config/config.yaml",
            "LICENSE",
            "helm/templates/app.yaml",
            self.manifest()["images"][-1]["archive"],
        ):
            suffix = "./" + name
            kept = [ln for ln in content.splitlines() if not ln.endswith(suffix)]
            checksum.write_text("\n".join(kept) + "\n")
            self.command("offline-load.sh", "--bundle-dir", self.bundle, success=False)
            self.assertFalse(self.calls(), name)
        checksum.write_text(content)

    def test_default_storage_configuration_is_usable(self):
        self.build("--manifest-only")
        env = self.environment()
        self.assertEqual(env["STORAGE_TYPE"], "minio")
        self.assertEqual(env["RETRIEVE_DRIVER"], "qdrant")
        self.assertEqual(env["MINIO_BUCKET_NAME"], "genyee-data")
        self.assertGreaterEqual(len(env["MINIO_SECRET_ACCESS_KEY"]), 32)

    def test_failed_or_invalid_randomness_never_produces_a_bundle(self):
        openssl = self.path / "bin" / "openssl"
        for output, status in (("", 1), ("bad", 0)):
            with self.subTest(output=output, status=status):
                openssl.write_text(
                    f"#!/bin/sh\nprintf '%s' '{output}'\nexit {status}\n",
                )
                openssl.chmod(0o755)
                self.command(
                    "offline-bundle.sh",
                    "--output",
                    self.bundle,
                    "--manifest-only",
                    success=False,
                )
                self.assertFalse((self.bundle / "MANIFEST.json").exists())
                self.assertFalse(self.calls())
                self.assertEqual(
                    (self.bundle / ".env.offline").stat().st_mode & 0o777,
                    0o600,
                )
                shutil.rmtree(self.bundle)

    def test_full_does_not_implicitly_enable_langfuse(self):
        self.build("--manifest-only", "--profiles", "full")
        self.assertFalse(
            any("langfuse" in row["profile"] for row in self.manifest()["images"]),
        )
        self.assertEqual(self.environment()["COMPOSE_PROFILES"], "full")

    def test_all_does_not_bundle_langfuse_images(self):
        self.build("--manifest-only", "--profiles", "all")
        self.assertFalse(
            any("langfuse" in row["profile"] for row in self.manifest()["images"]),
        )
        self.assertNotIn("langfuse", self.environment()["COMPOSE_PROFILES"].split(","))

    def test_core_only_bundle_and_manifest_only_dry_run(self):
        self.build("--manifest-only", "--profiles", "")
        self.assertEqual(self.environment()["COMPOSE_PROFILES"], "")
        self.command("offline-load.sh", "--bundle-dir", self.bundle, "--dry-run")
        self.command("offline-load.sh", "--bundle-dir", self.bundle, success=False)
        self.assertFalse(self.calls())

    def test_skip_pull_and_platform_mismatch(self):
        self.env["TEST_IMAGE_PLATFORM"] = "linux/arm64"
        self.command(
            "offline-bundle.sh",
            "--output",
            self.bundle,
            "--skip-pull",
            success=False,
        )
        self.assertFalse(any(c[0] in ("save", "pull") for c in self.calls()))

    def test_missing_checksums_and_tampering_fail_before_loading(self):
        self.build("--skip-pull")
        self.log.write_text("")
        archive = self.bundle / self.manifest()["images"][0]["archive"]
        archive.write_bytes(b"tampered")
        self.command("offline-load.sh", "--bundle-dir", self.bundle, success=False)
        self.assertFalse(self.calls())
        (self.bundle / "SHA256SUMS").unlink()
        self.command("offline-load.sh", "--bundle-dir", self.bundle, success=False)
        self.assertFalse(self.calls())

    def test_incomplete_packages_fail_before_loading_even_when_verification_skipped(
        self,
    ):
        self.build("--skip-pull")
        self.log.write_text("")
        (self.bundle / self.manifest()["images"][-1]["archive"]).unlink()
        self.command(
            "offline-load.sh",
            "--bundle-dir",
            self.bundle,
            "--skip-verify",
            success=False,
        )
        self.assertFalse(self.calls())

    def test_rejects_unsafe_inputs_and_stale_output(self):
        for args in [
            ("--tag", 'bad"tag'),
            ("--profiles", "typo"),
            ("--tag",),
            ("--typo",),
        ]:
            self.command(
                "offline-bundle.sh",
                "--output",
                self.bundle,
                *args,
                success=False,
            )
        self.build("--manifest-only")
        before = (self.bundle / ".env").read_bytes()
        self.command(
            "offline-bundle.sh",
            "--output",
            self.bundle,
            "--manifest-only",
            success=False,
        )
        self.assertEqual(before, (self.bundle / ".env").read_bytes())

    def test_compose_renders_config_and_every_enabled_image_is_packaged(self):
        compose = os.environ.get("COMPOSE_BIN") or shutil.which("docker-compose")
        if not compose:
            self.skipTest(
                "COMPOSE_BIN/docker-compose required for real Compose rendering",
            )
        self.build("--manifest-only", "--profiles", "all", "--tag", "customer-7")
        result = subprocess.run(
            [
                compose,
                "--project-directory",
                str(self.bundle),
                "-f",
                str(self.bundle / "docker-compose.yml"),
                "config",
                "--format",
                "json",
            ],
            capture_output=True,
            text=True,
            env=self.env,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        services = json.loads(result.stdout)["services"]
        packaged = {row["image"] for row in self.manifest()["images"]}
        self.assertFalse({v["image"] for v in services.values()} - packaged)
        env = self.environment()
        self.assertEqual(
            services["postgres"]["environment"]["POSTGRES_PASSWORD"],
            env["DB_PASSWORD"],
        )
        self.assertEqual(
            services["app"]["environment"]["JWT_SECRET"],
            env["JWT_SECRET"],
        )
        self.assertEqual(
            services["minio"]["environment"]["MINIO_ROOT_PASSWORD"],
            env["MINIO_SECRET_ACCESS_KEY"],
        )

    def test_private_registry_override_covers_every_compose_image(self):
        compose = os.environ.get("COMPOSE_BIN") or shutil.which("docker-compose")
        if not compose:
            self.skipTest(
                "COMPOSE_BIN/docker-compose required for real Compose rendering",
            )
        self.build("--skip-pull", "--profiles", "all", "--tag", "customer..7")
        checksum_before = hashlib.sha256(
            (self.bundle / "SHA256SUMS").read_bytes(),
        ).digest()
        self.command(
            "offline-load.sh",
            "--bundle-dir",
            self.bundle,
            "--registry",
            "harbor.example/project",
        )
        result = subprocess.run(
            [
                compose,
                "--project-directory",
                str(self.bundle),
                "-f",
                str(self.bundle / "docker-compose.yml"),
                "-f",
                str(self.bundle / "docker-compose.registry.yml"),
                "config",
                "--format",
                "json",
            ],
            capture_output=True,
            text=True,
            env=self.env,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        services = json.loads(result.stdout)["services"]
        tagged = {call[2] for call in self.calls() if call[0] == "tag"}
        self.assertFalse({service["image"] for service in services.values()} - tagged)
        for service in services.values():
            self.assertTrue(service["image"].startswith("harbor.example/project/"))
        self.assertEqual(
            services["app"]["environment"]["GENYEE_SANDBOX_DOCKER_IMAGE"],
            "harbor.example/project/genyee-sandbox:customer..7",
        )
        self.assertEqual(
            checksum_before,
            hashlib.sha256((self.bundle / "SHA256SUMS").read_bytes()).digest(),
        )
        self.command("offline-load.sh", "--bundle-dir", self.bundle)


if __name__ == "__main__":
    unittest.main()
