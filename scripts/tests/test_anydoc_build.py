"""Exercise build script artifact selection without compiling Rust or downloading."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
TARGET = "aarch64-apple-darwin"


class AnydocBuildTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="genyee-anydoc-build-test-")
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        scripts = self.root / "scripts"
        scripts.mkdir()
        shutil.copy2(ROOT / "scripts/build-anydoc-lib.sh", scripts)
        self.crate = self.root / "third_party/anydoc-go"
        self.crate.mkdir(parents=True)
        (self.crate / "Cargo.toml").write_text('anydoc = "=0.2.4"\n')
        (self.crate / "version.go").write_text('const Version = "0.2.4"\n')
        patched = self.crate / "patched-anydoc"
        patched.mkdir()
        (patched / "Cargo.toml").write_text("fixture manifest\n")
        sha = "a" * 64
        (patched / ".genyee-patched").write_text(f"0.2.4-zip9-v2-{sha}\n")
        sources = self.root / "licenses/sources"
        sources.mkdir(parents=True)
        (sources / "anydoc.tsv").write_text(f"0.2.4 {sha}\n")
        binary = self.root / "bin"
        binary.mkdir()
        # The real collector parses metadata. This stand-in exposes the same
        # resolved target directory so the script can be tested independently.
        (binary / "go").write_text('''#!/usr/bin/env python3
import os, sys
from pathlib import Path
if sys.argv[1:] == ['env', 'GOHOSTOS']:
    print('darwin')
elif sys.argv[1:] == ['env', 'GOHOSTARCH']:
    print('arm64')
elif sys.argv[1:3] == ['run', '-mod=readonly']:
    if '--print-target-dir' in sys.argv:
        print(Path(os.environ['FIXTURE_TARGET_DIR']).resolve())
    else:
        print('Rust notices: fixture')
else:
    raise SystemExit('Unexpected go arguments: ' + repr(sys.argv))
''')
        (binary / "cargo").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
Path(os.environ['FIXTURE_CALLS']).write_text(json.dumps(sys.argv[1:]))
assert sys.argv[1] == 'build', sys.argv
assert '--release' in sys.argv and '--locked' in sys.argv, sys.argv
assert '--target-dir' not in sys.argv, 'Must preserve Cargo config and environment'
if os.environ.get('FIXTURE_BUILD_FAIL'):
    raise SystemExit(7)
target = sys.argv[sys.argv.index('--target') + 1]
artifact = Path(os.environ['FIXTURE_TARGET_DIR']) / target / 'release/libanydoc_go.a'
artifact.parent.mkdir(parents=True, exist_ok=True)
artifact.write_bytes(b'new native archive')
''')
        for path in binary.iterdir():
            path.chmod(0o755)
        self.calls = self.root / "cargo-calls.json"
        self.env = dict(os.environ, PATH=str(binary) + os.pathsep + os.environ["PATH"],
                        TARGET=TARGET, FIXTURE_CALLS=str(self.calls))
        self.env.pop("CARGO_TARGET_DIR", None)

    def build(self, output, *args, success=True):
        self.env["FIXTURE_TARGET_DIR"] = str(output)
        result = subprocess.run(["bash", str(self.root / "scripts/build-anydoc-lib.sh"), *args],
                                cwd=self.root, env=self.env, text=True, capture_output=True)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def test_custom_target_dirs_select_fresh_artifact(self):
        for mode, directory in (
                ("env", self.root / 'external output "quoted"'),
                ("relative-env", Path("relative output")),
                ("cargo-config", self.root / "config output")):
            with self.subTest(mode=mode):
                if mode.endswith("env"):
                    self.env["CARGO_TARGET_DIR"] = str(directory)
                else:
                    self.env.pop("CARGO_TARGET_DIR", None)
                stale = self.crate / "target" / TARGET / "release/libanydoc_go.a"
                stale.parent.mkdir(parents=True, exist_ok=True)
                stale.write_bytes(b'stale archive must never ship')
                self.build(directory)
                self.assertEqual((self.crate / "lib/darwin_arm64/libanydoc_go.a").read_bytes(),
                                 b'new native archive')
                self.assertEqual(json.loads(self.calls.read_text())[-2:], ["--target", TARGET])

    def test_failed_build_does_not_publish_old_artifact(self):
        destination = self.crate / "lib/darwin_arm64/libanydoc_go.a"
        destination.parent.mkdir(parents=True)
        destination.write_bytes(b'previous successful build')
        self.env["FIXTURE_BUILD_FAIL"] = "1"
        self.build(self.root / "custom target", success=False)
        self.assertEqual(destination.read_bytes(), b'previous successful build')

    def test_prepare_only_does_not_compile(self):
        self.build(self.root / "unused target", "--prepare-only")
        self.assertFalse(self.calls.exists())
        self.assertFalse((self.crate / "lib").exists())


if __name__ == "__main__":
    unittest.main()
