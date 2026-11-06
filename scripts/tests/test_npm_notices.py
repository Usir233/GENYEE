"""Validate the artifact generator with isolated installed-package fixtures."""
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
NODE = shutil.which("node")
GENERATOR = ROOT / "frontend/scripts/generate-npm-notices.mjs"


@unittest.skipUnless(NODE, "Node.js is required")
class NpmNoticesTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="genyee-notices-test-")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.package = self.root / "node_modules/example"
        self.package.mkdir(parents=True)
        self.metadata = {"name": "example", "version": "1.2.3", "license": "MIT"}
        self.lock = {"name": "fixture", "lockfileVersion": 3, "packages": {
            "": {"name": "fixture"}, "node_modules/example": {
                "version": "1.2.3", "resolved": "https://registry.example/example-1.2.3.tgz",
                "integrity": "sha512-fixture", "license": "MIT"}}}
        self.original = b"Copyright (c) Example contributors\r\nOriginal license text.\r\n"
        (self.package / "LICENSE").write_bytes(self.original)
        self.output = self.root / "dist/licenses"
        self.save_metadata()

    def save_metadata(self):
        (self.package / "package.json").write_text(json.dumps(self.metadata))
        (self.root / "package-lock.json").write_text(json.dumps(self.lock))

    def generate(self, success=True):
        result = subprocess.run([NODE, str(GENERATOR), "--root", str(self.root),
                                 "--output", str(self.output), "--distribution", str(self.root / "dist")],
                                text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
        return result

    def test_context_copies_stay_identical(self):
        self.assertEqual(GENERATOR.read_bytes(), (ROOT / "website-docs/scripts/generate-npm-notices.mjs").read_bytes())

    def test_preserves_original_and_nested_licenses_with_deterministic_source_inventory(self):
        vendor = self.package / "vendor/licenses"
        vendor.mkdir(parents=True)
        (vendor / "BSD.txt").write_text("Third party original copyright and license\n")
        self.generate()
        notice = (self.output / "npm-third-party-notices.txt").read_bytes()
        manifest_file = self.output / "npm-dependencies.json"
        before = manifest_file.read_bytes()
        manifest = json.loads(before)
        self.assertIn(self.original, notice)
        self.assertIn(b"Third party original copyright and license", notice)
        self.assertEqual(manifest["noticesSha256"], hashlib.sha256(notice).hexdigest())
        entry = manifest["packages"][0]
        self.assertEqual(entry["version"], "1.2.3")
        self.assertEqual(entry["source"], self.lock["packages"]["node_modules/example"]["resolved"])
        self.assertEqual(entry["notices"][0]["sha256"], hashlib.sha256(self.original).hexdigest())
        self.assertNotIn(str(self.root), before.decode())
        self.generate()
        self.assertEqual(before, manifest_file.read_bytes())
        self.assertEqual(notice, (self.output / "npm-third-party-notices.txt").read_bytes())

    def test_missing_license_text_fails_instead_of_silently_omitting_package(self):
        (self.package / "LICENSE").unlink()
        result = self.generate(False)
        self.assertIn("No license text for example@1.2.3", result.stderr)
        self.assertFalse(self.output.exists())

    def test_missing_metadata_or_version_drift_fails(self):
        for key in ("resolved", "integrity", "license"):
            with self.subTest(key=key):
                saved = self.lock["packages"]["node_modules/example"].pop(key)
                if key == "license":
                    self.metadata.pop("license")
                self.save_metadata()
                self.assertIn("Missing license/source/integrity", self.generate(False).stderr)
                self.lock["packages"]["node_modules/example"][key] = saved
                self.metadata["license"] = "MIT"
        self.metadata["version"] = "9.9.9"
        self.save_metadata()
        self.assertIn("differs from lockfile", self.generate(False).stderr)

    def test_native_build_only_exclusion_cannot_hide_a_distributed_binary(self):
        self.metadata["name"] = "@esbuild/fixture-platform"
        self.save_metadata()
        (self.package / "LICENSE").unlink()
        self.generate()
        manifest = json.loads((self.output / "npm-dependencies.json").read_text())
        self.assertEqual(manifest["packages"], [])
        self.assertIn("Native build tool", manifest["excluded"][0]["reason"])
        native = self.root / "dist/node_modules/platform/leaked.node"
        native.parent.mkdir(parents=True)
        native.write_bytes(b"native fixture")
        self.assertIn("Native binary in static distribution", self.generate(False).stderr)

    def test_static_symlink_cannot_bypass_native_distribution_check(self):
        self.output.mkdir(parents=True)
        (self.root / "dist/native-link").symlink_to(self.package, target_is_directory=True)
        self.assertIn("Symlink in static distribution", self.generate(False).stderr)


if __name__ == "__main__":
    unittest.main()
