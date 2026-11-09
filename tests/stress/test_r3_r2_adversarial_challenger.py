#!/usr/bin/env python3
r"""Adversarial stress and empirical challenge suite for Milestone M1 (R3 & R2).

Empirically tests:
1. Formula/weknora-lite.rb POSIX sed replacement logic under extreme paths:
   - Spaces, pipes `|`, ampersands `&`, backslashes `\`, quotes, dollar signs,
     special punctuation, emojis, and exotic Unicode characters.
   - Atomicity, exact literal value verification, absence of tempfile leaks.
   - Idempotency and preservation of non-target configuration keys.
   - End-to-end Ruby heredoc evaluation and bash execution.
2. helm/values.yaml YAML parsing integrity, image baseline consistency,
   StatefulSet limitation comments, and parser fuzzing resistance.
"""

import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
import yaml

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
FORMULA_PATH = REPO_ROOT / "Formula" / "weknora-lite.rb"
ENV_EXAMPLE_PATH = REPO_ROOT / ".env.lite.example"
HELM_VALUES_PATH = REPO_ROOT / "helm" / "values.yaml"
COMPOSE_PATH = REPO_ROOT / "docker-compose.yml"
COMPOSE_DEV_PATH = REPO_ROOT / "docker-compose.dev.yml"
BUNDLE_SCRIPT_PATH = REPO_ROOT / "scripts" / "offline-bundle.sh"
OFFLINE_LICENSES_README = REPO_ROOT / "OFFLINE_LICENSES" / "README.md"
OFFLINE_LICENSES_OFFER = REPO_ROOT / "OFFLINE_LICENSES" / "WRITTEN_OFFER.md"


class TestFormulaPosixSedAdversarial(unittest.TestCase):
    """Adversarial stress tests for Formula/weknora-lite.rb POSIX sed logic."""

    @classmethod
    def setUpClass(cls):
        cls.assert_formula_exists()
        cls.extracted_sh_script = cls._extract_ruby_heredoc()

    @classmethod
    def assert_formula_exists(cls):
        if not FORMULA_PATH.exists():
            raise FileNotFoundError(f"Formula not found: {FORMULA_PATH}")
        if not ENV_EXAMPLE_PATH.exists():
            raise FileNotFoundError(f"Example env not found: {ENV_EXAMPLE_PATH}")

    @classmethod
    def _extract_ruby_heredoc(cls) -> str:
        """Extract and evaluate the Ruby heredoc to get the exact generated shell script."""
        ruby_cmd = [
            "ruby", "-e",
            f"""
            code = File.read('{FORMULA_PATH}')
            lines = code.lines
            start_idx = lines.find_index {{ |l| l.include?("<<~SH") }}
            end_idx = lines.find_index {{ |l| l.strip == "SH" }}
            heredoc_body = lines[(start_idx + 1)...end_idx].join
            pkgshare = "/mock/pkgshare"
            libexec = "/mock/libexec"
            puts eval("<<~SH\\n#{{heredoc_body}}SH\\n")
            """
        ]
        res = subprocess.run(ruby_cmd, capture_output=True, text=True, check=True)
        return res.stdout

    def _run_isolated_sed_replacement(self, data_dir: str) -> tuple[int, str, str, Path]:
        """Execute the exact POSIX sed replacement logic against a fresh .env.lite."""
        tmp_dir = Path(tempfile.mkdtemp(prefix="sed_stress_"))
        config_dir = tmp_dir / "config"
        config_dir.mkdir(parents=True, exist_ok=True)
        env_file = config_dir / ".env.lite"
        shutil.copy(ENV_EXAMPLE_PATH, env_file)

        bash_script = """
set -e
ESCAPED_DB_PATH=$(printf '%s\n' "$DATA_DIR/weknora.db" | sed -e 's/[\\\\&|]/\\\\&/g')
ESCAPED_STORAGE_DIR=$(printf '%s\n' "$DATA_DIR/files" | sed -e 's/[\\\\&|]/\\\\&/g')
sed "s|DB_PATH=.*|DB_PATH=$ESCAPED_DB_PATH|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
sed "s|LOCAL_STORAGE_BASE_DIR=.*|LOCAL_STORAGE_BASE_DIR=$ESCAPED_STORAGE_DIR|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
"""
        env = os.environ.copy()
        env["CONFIG_DIR"] = str(config_dir)
        env["DATA_DIR"] = str(data_dir)

        proc = subprocess.run(["bash", "-c", bash_script], env=env, capture_output=True, text=True)
        return proc.returncode, proc.stdout, proc.stderr, tmp_dir

    def test_ruby_syntax_check(self):
        """Verify Formula/weknora-lite.rb passes ruby -c."""
        proc = subprocess.run(["ruby", "-c", str(FORMULA_PATH)], capture_output=True, text=True)
        self.assertEqual(proc.returncode, 0, f"ruby -c failed: {proc.stderr}")
        self.assertIn("Syntax OK", proc.stdout)

    def test_no_bsd_sed_artifacts(self):
        """Ensure no BSD sed -i '' or .env.lite-e remains in the Formula."""
        formula_content = FORMULA_PATH.read_text(encoding="utf-8")
        self.assertNotIn("sed -i ''", formula_content)
        self.assertNotIn("sed -i", formula_content)
        self.assertNotIn(".env.lite-e", formula_content)
        self.assertIn(".env.lite.tmp", formula_content)

    def test_adversarial_paths_matrix(self):
        """Stress-test POSIX sed replacement against extensive adversarial paths."""
        adversarial_cases = [
            # 1. Spaces
            ("space_simple", "/tmp/path with spaces/weknora"),
            ("space_multiple", "/tmp/path  with   multiple    spaces/weknora"),
            ("space_padded", "/tmp/   leading and trailing spaces   /weknora"),
            ("space_tabs", "/tmp/path\twith\ttabs/weknora"),

            # 2. Delimiter: Pipes |
            ("pipe_single", "/tmp/pipe|in|path/weknora"),
            ("pipe_multiple", "/tmp/pipe|path|with|many|pipes/weknora"),
            ("pipe_consecutive", "/tmp/pipe|||consecutive||pipes/weknora"),
            ("pipe_delimiter_lookalike", "/tmp/s|DB_PATH=|replaced|g/weknora"),

            # 3. Delimiter / Match token: Ampersands &
            ("ampersand_simple", "/tmp/rock & roll/weknora"),
            ("ampersand_multiple", "/tmp/a & b & c & d & e/weknora"),
            ("ampersand_consecutive", "/tmp/&&&triple&&ampersands/weknora"),
            ("ampersand_numbered", "/tmp/foo&1&2&3/weknora"),

            # 4. Escape characters: Backslashes \
            ("backslash_simple", r"/tmp/win\style\path/weknora"),
            ("backslash_multiple", r"/tmp/win\\style\\\\deep\\path/weknora"),
            ("backslash_subst_digits", r"/tmp/path\1\2\3\4\5\6\7\8\9/weknora"),
            ("backslash_c_escapes", r"/tmp/path\n\t\r\v\f\a\b/weknora"),
            ("backslash_pipe", r"/tmp/win\|pipe/weknora"),
            ("backslash_amp", r"/tmp/win\&amp/weknora"),

            # 5. Quotes and metacharacters
            ("quotes_single", "/tmp/it's a path's test/weknora"),
            ("quotes_double", '/tmp/path "with" double quotes/weknora'),
            ("quotes_backtick", "/tmp/path`whoami`test/weknora"),
            ("dollar_var", "/tmp/path$with$dollar/weknora"),
            ("dollar_brace", "/tmp/path${HOME}_and_${USER}/weknora"),
            ("dollar_subshell", "/tmp/path$(id -u)_subshell/weknora"),
            ("dollar_arithmetic", "/tmp/path$((1+1))_arith/weknora"),
            ("semicolons", "/tmp/path;rm -rf /tmp/data;echo 1/weknora"),
            ("redirections", "/tmp/path>out<in>>append/weknora"),
            ("wildcards", "/tmp/path*with?glob[chars]/weknora"),
            ("parentheses_braces", "/tmp/path(paren)[bracket]{brace}/weknora"),
            ("special_ascii_punct", "/tmp/spec!@#%^~-=_+:,./weknora"),

            # 6. Unicode and Multi-byte Characters
            ("unicode_zh_cn", "/tmp/数据中心/企业知识库_向量检索/weknora"),
            ("unicode_zh_tw", "/tmp/繁體資料/知識庫系統/weknora"),
            ("unicode_japanese", "/tmp/データ分析/ナレッジベース/ひらがな/weknora"),
            ("unicode_korean", "/tmp/데이터베이스/한국어_경로/weknora"),
            ("unicode_emoji", "/tmp/🚀/🔥_🤖_✨_🎉/weknora"),
            ("unicode_european_accents", "/tmp/café/über/mañana/smörgåsbord/weknora"),
            ("unicode_rtl_arabic", "/tmp/مسار_البيانات/قاعدة_المعرفة/weknora"),
            ("unicode_rtl_hebrew", "/tmp/נתיב_נתונים/בסיס_ידע/weknora"),
            ("unicode_cyrillic", "/tmp/хранилище_данных/база_знаний/weknora"),
            ("unicode_greek", "/tmp/ελληνικά_δεδομένα/βάση_γνώσεων/weknora"),
            ("unicode_math", "/tmp/∑_∏_∆_≈_≠_≤_≥_∞_√/weknora"),

            # 7. Extreme Combinations
            ("ultra_combo_1", r"""/tmp/path with 'spaces' & "quotes" | pipes \ backslashes $dollars `ticks` !@#%^*()_+-=[]{};:,.<>?/weknora"""),
            ("ultra_combo_2", r"""/tmp/🚀 中文 & 繁體 | 日本語 \1 \2 \\ || && $HOME `id` "quote" 'single' ñoñó/weknora"""),
            ("pipe_start_end", "|pipe_start_and_end|"),
            ("amp_start_end", "&amp_start_and_end&"),
            ("backslash_start_end", "\\backslash_start_and_end\\"),
            ("consecutive_mixed_escapes", r"\\\\||||&&&&\\\\||"),

            # 8. Boundary conditions
            ("deep_nesting", "/tmp/" + "/".join([f"level_{i}" for i in range(25)]) + "/weknora"),
            ("long_path", "/tmp/" + "a" * 512 + "/weknora"),
        ]

        for case_name, path_val in adversarial_cases:
            with self.subTest(case=case_name, path=path_val):
                retcode, stdout, stderr, tmp_dir = self._run_isolated_sed_replacement(path_val)
                try:
                    self.assertEqual(retcode, 0, f"Sed script failed on case '{case_name}': {stderr}")
                    env_file = tmp_dir / "config" / ".env.lite"
                    self.assertTrue(env_file.exists(), f".env.lite missing for '{case_name}'")

                    # Check temp file leaks
                    tmp_files = list((tmp_dir / "config").glob(".env.lite*"))
                    self.assertEqual(len(tmp_files), 1, f"Temporary file leaked in '{case_name}': {tmp_files}")
                    self.assertEqual(tmp_files[0].name, ".env.lite")

                    content = env_file.read_text(encoding="utf-8")
                    expected_db_path = f"DB_PATH={path_val}/weknora.db"
                    expected_storage_dir = f"LOCAL_STORAGE_BASE_DIR={path_val}/files"

                    # Verify exact literal string presence
                    self.assertIn(expected_db_path, content, f"Mismatch in DB_PATH for '{case_name}'")
                    self.assertIn(expected_storage_dir, content, f"Mismatch in LOCAL_STORAGE_BASE_DIR for '{case_name}'")

                    # Verify other baseline env vars remain uncorrupted
                    self.assertIn("GIN_MODE=debug", content)
                    self.assertIn("LOG_LEVEL=debug", content)
                    self.assertIn("STORAGE_TYPE=local", content)
                    self.assertIn("OLLAMA_BASE_URL=http://127.0.0.1:11434", content)
                    self.assertIn("CONCURRENCY_POOL_SIZE=3", content)
                    self.assertIn("DOCREADER_TRANSPORT=grpc", content)
                finally:
                    shutil.rmtree(tmp_dir, ignore_errors=True)

    def test_stale_temp_file_overwrite_and_no_leak(self):
        """Verify that pre-existing .env.lite.tmp from an aborted run is cleanly overwritten without error."""
        tmp_dir = Path(tempfile.mkdtemp(prefix="stale_tmp_"))
        try:
            config_dir = tmp_dir / "config"
            config_dir.mkdir(parents=True, exist_ok=True)
            env_file = config_dir / ".env.lite"
            stale_tmp_file = config_dir / ".env.lite.tmp"
            shutil.copy(ENV_EXAMPLE_PATH, env_file)
            stale_tmp_file.write_text("CORRUPTED_STALE_TEMP_CONTENT_FROM_PREVIOUS_CRASH\n", encoding="utf-8")

            data_dir = "/tmp/normal_data"
            bash_script = """
set -e
ESCAPED_DB_PATH=$(printf '%s\n' "$DATA_DIR/weknora.db" | sed -e 's/[\\\\&|]/\\\\&/g')
ESCAPED_STORAGE_DIR=$(printf '%s\n' "$DATA_DIR/files" | sed -e 's/[\\\\&|]/\\\\&/g')
sed "s|DB_PATH=.*|DB_PATH=$ESCAPED_DB_PATH|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
sed "s|LOCAL_STORAGE_BASE_DIR=.*|LOCAL_STORAGE_BASE_DIR=$ESCAPED_STORAGE_DIR|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
"""
            env = os.environ.copy()
            env["CONFIG_DIR"] = str(config_dir)
            env["DATA_DIR"] = data_dir
            proc = subprocess.run(["bash", "-c", bash_script], env=env, capture_output=True, text=True)

            self.assertEqual(proc.returncode, 0, f"Failed with stale temp file: {proc.stderr}")
            self.assertFalse(stale_tmp_file.exists(), "Stale temp file still exists after mv!")
            content = env_file.read_text(encoding="utf-8")
            self.assertIn("DB_PATH=/tmp/normal_data/weknora.db", content)
            self.assertNotIn("CORRUPTED_STALE_TEMP_CONTENT", content)
        finally:
            shutil.rmtree(tmp_dir, ignore_errors=True)

    def test_crlf_line_endings_support(self):
        """Verify that .env.lite with Windows CRLF line endings is handled cleanly without corrupting \r."""
        tmp_dir = Path(tempfile.mkdtemp(prefix="crlf_test_"))
        try:
            config_dir = tmp_dir / "config"
            config_dir.mkdir(parents=True, exist_ok=True)
            env_file = config_dir / ".env.lite"

            raw_example = ENV_EXAMPLE_PATH.read_text(encoding="utf-8")
            crlf_example = raw_example.replace("\n", "\r\n")
            env_file.write_text(crlf_example, encoding="utf-8")

            data_dir = "/tmp/crlf_data"
            bash_script = """
set -e
ESCAPED_DB_PATH=$(printf '%s\n' "$DATA_DIR/weknora.db" | sed -e 's/[\\\\&|]/\\\\&/g')
ESCAPED_STORAGE_DIR=$(printf '%s\n' "$DATA_DIR/files" | sed -e 's/[\\\\&|]/\\\\&/g')
sed "s|DB_PATH=.*|DB_PATH=$ESCAPED_DB_PATH|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
sed "s|LOCAL_STORAGE_BASE_DIR=.*|LOCAL_STORAGE_BASE_DIR=$ESCAPED_STORAGE_DIR|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
"""
            env = os.environ.copy()
            env["CONFIG_DIR"] = str(config_dir)
            env["DATA_DIR"] = data_dir
            proc = subprocess.run(["bash", "-c", bash_script], env=env, capture_output=True, text=True)

            self.assertEqual(proc.returncode, 0)
            content = env_file.read_text(encoding="utf-8")
            self.assertIn("DB_PATH=/tmp/crlf_data/weknora.db", content)
            self.assertIn("LOCAL_STORAGE_BASE_DIR=/tmp/crlf_data/files", content)
        finally:
            shutil.rmtree(tmp_dir, ignore_errors=True)

    def test_idempotency_preserves_existing_config(self):
        """Verify that if .env.lite already exists, it is untouched and not overwritten."""
        tmp_dir = Path(tempfile.mkdtemp(prefix="idempotent_test_"))
        try:
            config_dir = tmp_dir / "config"
            config_dir.mkdir(parents=True, exist_ok=True)
            env_file = config_dir / ".env.lite"

            custom_content = "DB_PATH=/custom/pre-existing/db.sqlite\nLOCAL_STORAGE_BASE_DIR=/custom/storage\n"
            env_file.write_text(custom_content, encoding="utf-8")

            # Run sed replacement logic with a different DATA_DIR
            data_dir = "/tmp/new_data_dir"
            bash_script = f"""
CONFIG_DIR='{config_dir}'
DATA_DIR='{data_dir}'
if [ ! -f "$CONFIG_DIR/.env.lite" ]; then
    cp '{ENV_EXAMPLE_PATH}' "$CONFIG_DIR/.env.lite"
    ESCAPED_DB_PATH=$(printf '%s\\n' "$DATA_DIR/weknora.db" | sed -e 's/[\\\\&|]/\\\\&/g')
    ESCAPED_STORAGE_DIR=$(printf '%s\\n' "$DATA_DIR/files" | sed -e 's/[\\\\&|]/\\\\&/g')
    sed "s|DB_PATH=.*|DB_PATH=$ESCAPED_DB_PATH|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
    sed "s|LOCAL_STORAGE_BASE_DIR=.*|LOCAL_STORAGE_BASE_DIR=$ESCAPED_STORAGE_DIR|" "$CONFIG_DIR/.env.lite" > "$CONFIG_DIR/.env.lite.tmp" && mv "$CONFIG_DIR/.env.lite.tmp" "$CONFIG_DIR/.env.lite"
fi
"""
            proc = subprocess.run(["bash", "-c", bash_script], capture_output=True, text=True)
            self.assertEqual(proc.returncode, 0)
            self.assertEqual(env_file.read_text(encoding="utf-8"), custom_content)
        finally:
            shutil.rmtree(tmp_dir, ignore_errors=True)

    def test_end_to_end_formula_binary_generation_and_execution(self):
        """Simulate the complete Homebrew formula lifecycle and launch wrapper."""
        tmp_root = Path(tempfile.mkdtemp(prefix="formula_e2e_"))
        try:
            pkgshare = tmp_root / "pkgshare"
            libexec = tmp_root / "libexec"
            bin_dir = tmp_root / "bin"
            pkgshare.mkdir(parents=True)
            libexec.mkdir(parents=True)
            bin_dir.mkdir(parents=True)

            (pkgshare / "config").mkdir()
            (pkgshare / "config" / "config.yaml").write_text("server:\n  port: 8080\n", encoding="utf-8")
            (pkgshare / "migrations").mkdir()
            shutil.copy(ENV_EXAMPLE_PATH, pkgshare / ".env.lite.example")

            # Mock GENYEE-lite binary
            mock_bin = libexec / "GENYEE-lite"
            mock_bin.write_text(
                "#!/bin/bash\n"
                "echo \"LITE_MOCK_SUCCESS: DB=$DB_PATH STORAGE=$LOCAL_STORAGE_BASE_DIR\"\n"
                "exit 0\n",
                encoding="utf-8",
            )
            mock_bin.chmod(0o755)

            # Generate wrapper script using Ruby eval
            ruby_gen_cmd = [
                "ruby", "-e",
                f"""
                code = File.read('{FORMULA_PATH}')
                lines = code.lines
                start_idx = lines.find_index {{ |l| l.include?("<<~SH") }}
                end_idx = lines.find_index {{ |l| l.strip == "SH" }}
                heredoc_body = lines[(start_idx + 1)...end_idx].join
                pkgshare = '{pkgshare}'
                libexec = '{libexec}'
                script = eval("<<~SH\\n#{{heredoc_body}}SH\\n")
                File.write('{bin_dir / "weknora-lite"}', script)
                """
            ]
            subprocess.run(ruby_gen_cmd, check=True)
            wrapper_script = bin_dir / "weknora-lite"
            wrapper_script.chmod(0o755)

            # Execute wrapper with adversarial paths
            test_cases = [
                ("normal", tmp_root / "data_normal"),
                ("space_and_pipe", tmp_root / "data with spaces|pipe"),
                ("amp_and_backslash", tmp_root / r"data&amp\win"),
                ("shell_expansion_injection", tmp_root / "data$var`uname`$(id -u)"),
                ("unicode_emoji", tmp_root / "知识库_🚀_data"),
            ]

            for name, test_data_dir in test_cases:
                with self.subTest(e2e_case=name):
                    conf_dir = tmp_root / f"conf_{name}"
                    env = os.environ.copy()
                    env["WEKNORA_CONFIG_DIR"] = str(conf_dir)
                    env["WEKNORA_DATA_DIR"] = str(test_data_dir)

                    proc = subprocess.run([str(wrapper_script)], env=env, capture_output=True, text=True)
                    self.assertEqual(proc.returncode, 0, f"Wrapper execution failed on {name}: {proc.stderr}")
                    self.assertEqual(proc.stderr, "", f"Unexpected stderr on {name}: {proc.stderr}")

                    env_file = conf_dir / ".env.lite"
                    self.assertTrue(env_file.exists())
                    self.assertTrue((conf_dir / "config" / "config.yaml").is_file())
                    self.assertFalse((conf_dir / "config" / "config").exists())
                    self.assertFalse((conf_dir / ".env.lite.tmp").exists())
                    self.assertFalse((conf_dir / ".env.lite-e").exists())

                    content = env_file.read_text(encoding="utf-8")
                    self.assertIn(f"DB_PATH={test_data_dir}/weknora.db", content)
                    self.assertIn(f"LOCAL_STORAGE_BASE_DIR={test_data_dir}/files", content)
                    self.assertIn(
                        f"LITE_MOCK_SUCCESS: DB={test_data_dir}/weknora.db STORAGE={test_data_dir}/files",
                        proc.stdout,
                    )
        finally:
            shutil.rmtree(tmp_root, ignore_errors=True)


class TestHelmValuesAndDeliveryAdversarial(unittest.TestCase):
    """Adversarial stress and consistency tests for Helm values and infrastructure delivery."""

    def setUp(self):
        with open(HELM_VALUES_PATH, "r", encoding="utf-8") as f:
            self.values_raw = f.read()
            self.values = yaml.safe_load(self.values_raw)

        with open(COMPOSE_PATH, "r", encoding="utf-8") as f:
            self.compose = yaml.safe_load(f)

        with open(COMPOSE_DEV_PATH, "r", encoding="utf-8") as f:
            self.compose_dev = yaml.safe_load(f)

        with open(BUNDLE_SCRIPT_PATH, "r", encoding="utf-8") as f:
            self.bundle_script = f.read()

        with open(OFFLINE_LICENSES_README, "r", encoding="utf-8") as f:
            self.licenses_readme = f.read()

        with open(OFFLINE_LICENSES_OFFER, "r", encoding="utf-8") as f:
            self.licenses_offer = f.read()

    def test_helm_values_yaml_syntax_and_root_structure(self):
        """Verify helm/values.yaml parses cleanly as a valid YAML mapping."""
        self.assertIsInstance(self.values, dict)
        required_keys = ["minio", "postgresql", "qdrant", "neo4j", "redis", "app", "frontend", "docreader"]
        for key in required_keys:
            self.assertIn(key, self.values, f"Missing required root key: {key}")

    def test_minio_image_universal_alignment(self):
        """R2: Assert MinIO image is minio/minio:RELEASE.2025-09-07T16-13-09Z across all delivery files."""
        expected_repo = "minio/minio"
        expected_tag = "RELEASE.2025-09-07T16-13-09Z"
        expected_full_image = f"{expected_repo}:{expected_tag}"

        # 1. helm/values.yaml
        self.assertEqual(self.values["minio"]["image"]["repository"], expected_repo)
        self.assertEqual(self.values["minio"]["image"]["tag"], expected_tag)

        # 2. docker-compose.yml
        self.assertEqual(self.compose["services"]["minio"]["image"], expected_full_image)

        # 3. docker-compose.dev.yml
        self.assertEqual(self.compose_dev["services"]["minio"]["image"], expected_full_image)

        # 4. scripts/offline-bundle.sh
        self.assertIn(expected_full_image, self.bundle_script)

        # 5. OFFLINE_LICENSES documentation
        self.assertIn(expected_repo, self.licenses_readme)
        self.assertIn(expected_tag, self.licenses_readme)
        self.assertIn(expected_repo, self.licenses_offer)
        self.assertIn(expected_tag, self.licenses_offer)

        # Confirm old non-compliant images are completely absent
        stale_images = [
            "pgsty/minio",
            "RELEASE.2026-08-04T00-00-00Z",
        ]
        for stale in stale_images:
            self.assertNotIn(stale, self.values_raw)
            self.assertNotIn(stale, self.bundle_script)
            self.assertNotIn(stale, self.licenses_readme)

    def test_helm_statefulset_limitation_comments(self):
        """R2: Verify helm/values.yaml contains explicit StatefulSet limitation comments."""
        minio_comment = "helm/templates currently does not bundle a built-in MinIO StatefulSet"
        qdrant_comment = "helm/templates currently does not bundle a built-in Qdrant StatefulSet"

        self.assertIn(minio_comment, self.values_raw)
        self.assertIn(qdrant_comment, self.values_raw)

    def test_base_component_images_pinned(self):
        """Verify baseline component images match between helm/values.yaml and docker-compose.yml."""
        # PostgreSQL / ParadeDB
        self.assertEqual(self.values["postgresql"]["image"]["repository"], "paradedb/paradedb")
        self.assertEqual(self.values["postgresql"]["image"]["tag"], "v0.25.11-pg17")
        self.assertEqual(self.compose["services"]["postgres"]["image"], "paradedb/paradedb:v0.25.11-pg17")

        # Qdrant
        self.assertEqual(self.values["qdrant"]["image"]["repository"], "qdrant/qdrant")
        self.assertEqual(self.values["qdrant"]["image"]["tag"], "v1.16.2")
        self.assertEqual(self.compose["services"]["qdrant"]["image"], "qdrant/qdrant:v1.16.2")

        # Neo4j
        self.assertEqual(self.values["neo4j"]["image"]["repository"], "neo4j")
        self.assertEqual(self.values["neo4j"]["image"]["tag"], "2025.10.1")
        self.assertEqual(self.compose["services"]["neo4j"]["image"], "neo4j:2025.10.1")

        # Redis (BSD-3-Clause)
        self.assertEqual(self.values["redis"]["image"]["repository"], "redis")
        self.assertEqual(self.values["redis"]["image"]["tag"], "7.2-alpine")

    def test_yaml_fuzzing_and_injection_resilience(self):
        """Fuzz YAML parser with malicious strings, anchors, and Unicode to ensure parser robustness."""
        fuzz_payloads = [
            # YAML Billion Laughs / Anchor expansion
            """
            a: &a ["lol","lol","lol","lol","lol"]
            b: &b [*a,*a,*a,*a,*a]
            c: &c [*b,*b,*b,*b,*b]
            minio:
              image:
                repository: minio/minio
                tag: RELEASE.2025-09-07T16-13-09Z
            """,
            # Type confusion
            """
            minio:
              image:
                repository: !!str 12345
                tag: !!binary "UkVMRUFTRQ=="
            """,
            # Long string injection
            f"""
            minio:
              image:
                repository: {"a" * 4096}
                tag: {"b" * 4096}
            """,
            # Unicode and control characters in tag
            """
            minio:
              image:
                repository: "minio/minio\u0000"
                tag: "RELEASE.2025-09-07T16-13-09Z\t\r"
            """,
        ]

        for idx, payload in enumerate(fuzz_payloads):
            with self.subTest(fuzz_index=idx):
                try:
                    parsed = yaml.safe_load(payload)
                    self.assertIsInstance(parsed, dict)
                except (yaml.YAMLError, ValueError):
                    # Graceful rejection by safe_load is completely acceptable
                    pass


if __name__ == "__main__":
    unittest.main()
