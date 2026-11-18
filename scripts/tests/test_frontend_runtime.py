"""Execute the runtime entrypoint and parse its output in a real JS engine."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
NODE = shutil.which("node")
CONFIG_KEYS = (
    "MAX_FILE_SIZE_MB",
    "MAX_SKILL_BUNDLE_SIZE_MB",
    "DEFAULT_LOCALE",
    "GENYEE_CHROME_EXTENSION_URL",
    "GENYEE_CLAWHUB_SKILL_URL",
    "DOCS_BASE_URL",
    "WEBSITE_URL",
    "GITHUB_URL",
    "APP_HOST",
    "APP_PORT",
    "APP_SCHEME",
)


@unittest.skipUnless(
    NODE,
    "Node.js is required to parse the generated runtime configuration",
)
class FrontendRuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="genyee-runtime-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.config = self.root / "config.js"
        self.nginx_config = self.root / "nginx.conf"
        self.template = self.root / "nginx.conf.template"
        self.template.write_text((ROOT / "frontend/nginx.conf").read_text())
        script = (ROOT / "frontend/docker-entrypoint.sh").read_text()
        # Redirect container-only paths; all entrypoint logic runs unchanged.
        for old, new in {
            "/usr/share/nginx/html/config.js": self.config,
            "/etc/nginx/templates/default.conf.template": self.template,
            "/etc/nginx/conf.d/default.conf": self.nginx_config,
        }.items():
            script = script.replace(old, str(new))
        self.script = self.root / "entrypoint.sh"
        self.script.write_text(script)
        # macOS has no envsubst/nginx. These stand-ins only emulate the external
        # commands; quoting, number validation and output use the real shell/awk.
        self._command(
            "envsubst",
            r"""#!/usr/bin/env python3
import os, re, sys
allowed = set(re.findall(r'\$\{([A-Z_]+)\}', sys.argv[1]))
pattern = r'\$\{([A-Z_]+)\}'
repl = lambda m: os.environ.get(m[1], '') if m[1] in allowed else m[0]
sys.stdout.write(re.sub(pattern, repl, sys.stdin.read()))
""",
        )
        self._command(
            "nginx",
            '#!/bin/sh\nprintf \'%s\\n\' "$@" > "$NGINX_INVOCATION"\n',
        )

    def _command(self, name, body):
        file = self.bin / name
        file.write_text(body)
        file.chmod(0o755)

    def run_entrypoint(self, values=None):
        env = os.environ.copy()
        for name in CONFIG_KEYS:
            env.pop(name, None)
        env.update(
            {
                "PATH": str(self.bin) + os.pathsep + env.get("PATH", ""),
                "NGINX_INVOCATION": str(self.root / "nginx-invocation"),
            },
        )
        env.update(values or {})
        return subprocess.run(
            ["/bin/sh", str(self.script)],
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )

    def read_config(self):
        result = subprocess.run(
            [
                NODE,
                "-e",
                """
const fs = require('node:fs');
const vm = require('node:vm');
const scope = {window: {}};
vm.runInNewContext(fs.readFileSync(process.argv[1], 'utf8'), scope, {timeout: 1000});
if (scope.pwned || scope.window.pwned) throw new Error('Config escaped its string literal');
process.stdout.write(JSON.stringify(scope.window.__RUNTIME_CONFIG__));
""",
                str(self.config),
            ],
            text=True,
            capture_output=True,
            check=True,
        )
        return json.loads(result.stdout)

    def test_defaults_match_nginx_and_start_server(self):
        result = self.run_entrypoint()
        self.assertEqual(result.returncode, 0, result.stderr)
        config = self.read_config()
        self.assertEqual(config["MAX_FILE_SIZE_MB"], 50)
        self.assertEqual(config["MAX_SKILL_BUNDLE_SIZE_MB"], 256)
        self.assertEqual(config["DEFAULT_LOCALE"], "")
        nginx = self.nginx_config.read_text()
        self.assertIn("client_max_body_size 51M;", nginx)
        self.assertIn("client_max_body_size 256M;", nginx)
        self.assertIn("http://app:8080", nginx)
        self.assertEqual(
            (self.root / "nginx-invocation").read_text(),
            "-g\ndaemon off;\n",
        )

    def test_all_runtime_strings_roundtrip_without_code_execution(self):
        payload = (
            'https://例子.test/\\path?q="}; window.pwned=true; //\n'
            + "".join(chr(i) for i in range(1, 32))
            + "\u2028\u2029\n\n"
        )
        url_keys = CONFIG_KEYS[3 : CONFIG_KEYS.index("APP_HOST")]
        values = {name: payload for name in url_keys}
        values["DEFAULT_LOCALE"] = 'en-US"}; window.pwned=true; //'
        result = self.run_entrypoint(values)
        self.assertEqual(result.returncode, 0, result.stderr)
        config = self.read_config()
        for name, value in values.items():
            self.assertEqual(
                config[name],
                "" if name == "DEFAULT_LOCALE" else value,
                name,
            )
        # Every emitted string is also JSON-compatible, including C0 controls.
        for line in self.config.read_text().split("\n"):
            if ': "' in line:
                json.loads(line.split(": ", 1)[1].removesuffix(","))

    def test_locale_allowlist(self):
        for locale in ("zh-CN", "zh-TW", "en-US", "ru-RU", "ko-KR", "ja-JP"):
            with self.subTest(locale=locale):
                result = self.run_entrypoint({"DEFAULT_LOCALE": locale})
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(self.read_config()["DEFAULT_LOCALE"], locale)

    def test_decimal_normalization_and_skill_bounds(self):
        for file_mb, skill_mb, expected in (
            ("00010", "00007", (10, 10)),
            ("1", "1", (1, 1)),
            ("200", "0512", (200, 512)),
            ("600", "700", (600, 512)),
            ("8589934591", "256", (8589934591, 512)),
        ):
            with self.subTest(file_mb=file_mb, skill_mb=skill_mb):
                result = self.run_entrypoint(
                    {"MAX_FILE_SIZE_MB": file_mb, "MAX_SKILL_BUNDLE_SIZE_MB": skill_mb},
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                config = self.read_config()
                self.assertEqual(
                    (config["MAX_FILE_SIZE_MB"], config["MAX_SKILL_BUNDLE_SIZE_MB"]),
                    expected,
                )
                nginx = self.nginx_config.read_text()
                self.assertIn(f"client_max_body_size {expected[0] + 1}M;", nginx)
                self.assertIn(f"client_max_body_size {expected[1]}M;", nginx)

    def test_invalid_or_unrepresentable_limits_stop_before_writing_config(self):
        for name in ("MAX_FILE_SIZE_MB", "MAX_SKILL_BUNDLE_SIZE_MB"):
            for value in (
                "0",
                "000",
                "-1",
                "1.5",
                " 10",
                "1e3",
                "1; window.pwned=true",
                "8589934592",
                "9223372036854775808",
                "9" * 100,
            ):
                with self.subTest(name=name, value=value):
                    result = self.run_entrypoint({name: value})
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(
                        "Upload limits must be positive integers",
                        result.stderr,
                    )
                    self.assertFalse(self.config.exists())
                    self.assertFalse((self.root / "nginx-invocation").exists())


if __name__ == "__main__":
    unittest.main()
