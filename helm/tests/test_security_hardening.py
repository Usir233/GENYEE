"""Regression tests for Helm YAML escaping, secret validation, probes, and NetworkPolicy."""

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

import yaml

CHART = Path(os.environ.get("HELM_CHART_DIR", Path(__file__).resolve().parents[1]))


def render_chart(values=None, use_existing_secret=True):
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / "values.yaml"
        path.write_text(yaml.safe_dump(values or {}), encoding="utf-8")
        cmd = [
            "helm",
            "template",
            "audit",
            str(CHART),
            "--namespace",
            "audit",
        ]
        if use_existing_secret:
            cmd.extend(["--set", "secrets.existingSecret=fixture-secret"])
        cmd.extend(["-f", str(path)])
        return subprocess.run(cmd, capture_output=True, text=True, check=False)


class HelmSecurityHardeningTests(unittest.TestCase):
    def documents(self, values=None, use_existing_secret=True):
        result = render_chart(values, use_existing_secret=use_existing_secret)
        self.assertEqual(result.returncode, 0, result.stderr)
        return [doc for doc in yaml.safe_load_all(result.stdout) if doc]

    def test_scalar_values_are_quoted_against_yaml_coercion_and_injection(self):
        docs = self.documents(
            {
                "serviceAccount": {"create": True, "name": "true"},
                "secrets": {"existingSecret": "12345"},
                "ingress": {
                    "enabled": True,
                    "className": "yes",
                    "tls": {"enabled": True, "secretName": "false"},
                },
                "app": {
                    "persistence": {
                        "enabled": True,
                        "existingClaim": "",
                        "size": "20Gi",
                    }
                },
                "frontend": {"env": {"yes": "enabled"}},
                "neo4j": {"enabled": True},
            },
            use_existing_secret=False,
        )
        by_kind: dict[str, list[dict]] = {}
        for doc in docs:
            by_kind.setdefault(doc["kind"], []).append(doc)

        sa = by_kind["ServiceAccount"][0]
        self.assertIsInstance(sa["metadata"]["name"], str)
        self.assertEqual(sa["metadata"]["name"], "true")

        for dep in by_kind["Deployment"]:
            spec = dep["spec"]["template"]["spec"]
            self.assertIsInstance(spec["serviceAccountName"], str)
            self.assertEqual(spec["serviceAccountName"], "true")
            for container in spec["containers"]:
                for env_entry in container.get("env", []):
                    self.assertIsInstance(env_entry["name"], str)
                    secret_ref = (env_entry.get("valueFrom") or {}).get("secretKeyRef")
                    if secret_ref:
                        self.assertIsInstance(secret_ref["name"], str)
                        self.assertEqual(secret_ref["name"], "12345")

        ingress = by_kind["Ingress"][0]
        self.assertIsInstance(ingress["spec"]["ingressClassName"], str)
        self.assertEqual(ingress["spec"]["ingressClassName"], "yes")
        self.assertIsInstance(ingress["spec"]["tls"][0]["secretName"], str)
        self.assertEqual(ingress["spec"]["tls"][0]["secretName"], "false")

        for pvc in by_kind["PersistentVolumeClaim"]:
            storage_req = pvc["spec"]["resources"]["requests"]["storage"]
            self.assertIsInstance(storage_req, str)

    def test_format_image_rejects_whitespace_and_quotes_while_keeping_image_unquoted(
        self,
    ):
        for bad_val in (
            {"app": {"image": {"tag": "v1\n  privileged: true"}}},
            {"global": {"imageRegistry": 'evil.example/"bad'}},
            {"frontend": {"image": {"repository": "genyee-ui bad"}}},
        ):
            res = render_chart(bad_val)
            self.assertNotEqual(res.returncode, 0)
            self.assertIn("invalid image", res.stderr.lower())

        ok = render_chart({"app": {"image": {"tag": "1.2.3"}}})
        self.assertEqual(ok.returncode, 0, ok.stderr)
        image_lines = [
            line.strip()
            for line in ok.stdout.splitlines()
            if line.strip().startswith("image:")
        ]
        self.assertTrue(
            any(line == "image: genyee/genyee-app:1.2.3" for line in image_lines)
        )

    def test_system_aes_key_enforces_exact_32_byte_length(self):
        base_secrets = {
            "existingSecret": "",
            "dbPassword": "db-password-strong",
            "redisPassword": "redis-password-strong",
            "jwtSecret": "jwt-secret-strong-value-at-least-32",
        }
        bad = render_chart(
            {"secrets": {**base_secrets, "systemAesKey": "too-short-16byte"}},
            use_existing_secret=False,
        )
        self.assertNotEqual(bad.returncode, 0)
        self.assertIn("systemAesKey", bad.stderr)

        empty_ok = render_chart(
            {"secrets": {**base_secrets, "systemAesKey": ""}},
            use_existing_secret=False,
        )
        self.assertEqual(empty_ok.returncode, 0, empty_ok.stderr)

        exact_ok = render_chart(
            {
                "secrets": {
                    **base_secrets,
                    "systemAesKey": "0123456789abcdef0123456789abcdef",
                }
            },
            use_existing_secret=False,
        )
        self.assertEqual(exact_ok.returncode, 0, exact_ok.stderr)

    def test_postgres_probes_use_secret_backed_pguser_and_app_networkpolicy_allows_peer_pods(
        self,
    ):
        docs = self.documents({"networkPolicy": {"enabled": True}})
        pg_dep = next(
            d
            for d in docs
            if d["kind"] == "Deployment"
            and d["metadata"]["labels"].get("app.kubernetes.io/component") == "database"
        )
        pg_container = pg_dep["spec"]["template"]["spec"]["containers"][0]
        env_by_name = {e["name"]: e for e in pg_container["env"]}
        self.assertIn("PGUSER", env_by_name)
        self.assertEqual(
            env_by_name["PGUSER"]["valueFrom"]["secretKeyRef"]["key"], "DB_USER"
        )
        self.assertIn("PGDATABASE", env_by_name)
        self.assertEqual(
            env_by_name["PGDATABASE"]["valueFrom"]["secretKeyRef"]["key"], "DB_NAME"
        )
        self.assertEqual(
            pg_container["livenessProbe"]["exec"]["command"], ["pg_isready"]
        )
        self.assertEqual(
            pg_container["readinessProbe"]["exec"]["command"], ["pg_isready"]
        )

        app_netpol = next(
            d
            for d in docs
            if d["kind"] == "NetworkPolicy"
            and d["spec"]["podSelector"]["matchLabels"].get(
                "app.kubernetes.io/component"
            )
            == "app"
        )
        allowed_components = set()
        for rule in app_netpol["spec"]["ingress"]:
            ports = {p.get("port") for p in rule.get("ports", [])}
            if 8080 in ports:
                for peer in rule.get("from", []):
                    comp = (
                        (peer.get("podSelector") or {})
                        .get("matchLabels", {})
                        .get("app.kubernetes.io/component")
                    )
                    if comp:
                        allowed_components.add(comp)
        self.assertIn("frontend", allowed_components)
        self.assertIn("app", allowed_components)

    def test_pod_security_context_merges_global_seccomp_with_component_overrides(self):
        docs = self.documents(
            {
                "app": {"podSecurityContext": {"fsGroup": 2000}},
                "frontend": {"podSecurityContext": {"fsGroup": 2001}},
                "docreader": {"podSecurityContext": {"fsGroup": 10002}},
                "postgresql": {"podSecurityContext": {"fsGroup": 999}},
                "redis": {"podSecurityContext": {"fsGroup": 999}},
                "neo4j": {"enabled": True, "podSecurityContext": {"fsGroup": 7474}},
            }
        )
        expected_fsgroups = {
            "app": 2000,
            "frontend": 2001,
            "docreader": 10002,
            "database": 999,
            "cache": 999,
            "graph": 7474,
        }
        deployments = {
            d["metadata"]["labels"]["app.kubernetes.io/component"]: d
            for d in docs
            if d["kind"] == "Deployment"
        }
        for comp, expected_fsgroup in expected_fsgroups.items():
            sec_ctx = deployments[comp]["spec"]["template"]["spec"]["securityContext"]
            self.assertEqual(sec_ctx.get("fsGroup"), expected_fsgroup, comp)
            self.assertEqual(
                sec_ctx.get("seccompProfile"),
                {"type": "RuntimeDefault"},
                f"{comp} lost global seccompProfile on podSecurityContext override",
            )

    def test_memory_limits_skill_bundle_upload_size_and_clean_docs(self):
        docs = self.documents({"ingress": {"enabled": True}})
        deployments = {
            d["metadata"]["labels"]["app.kubernetes.io/component"]: d
            for d in docs
            if d["kind"] == "Deployment"
        }
        self.assertEqual(
            deployments["docreader"]["spec"]["template"]["spec"]["containers"][0][
                "resources"
            ]["limits"]["memory"],
            "4Gi",
        )
        self.assertEqual(
            deployments["database"]["spec"]["template"]["spec"]["containers"][0][
                "resources"
            ]["limits"]["memory"],
            "2Gi",
        )

        fe_env = {
            e["name"]: e.get("value")
            for e in deployments["frontend"]["spec"]["template"]["spec"]["containers"][
                0
            ]["env"]
        }
        app_env = {
            e["name"]: e.get("value")
            for e in deployments["app"]["spec"]["template"]["spec"]["containers"][0][
                "env"
            ]
        }
        self.assertEqual(fe_env.get("MAX_SKILL_BUNDLE_SIZE_MB"), "256")
        self.assertEqual(app_env.get("MAX_SKILL_BUNDLE_SIZE_MB"), "256")

        ingress = next(d for d in docs if d["kind"] == "Ingress")
        self.assertEqual(
            ingress["metadata"]["annotations"][
                "nginx.ingress.kubernetes.io/proxy-body-size"
            ],
            "256m",
        )

        scaled_docs = self.documents(
            {"global": {"maxSkillBundleSizeMB": 512}, "ingress": {"enabled": True}}
        )
        scaled_ingress = next(d for d in scaled_docs if d["kind"] == "Ingress")
        self.assertEqual(
            scaled_ingress["metadata"]["annotations"][
                "nginx.ingress.kubernetes.io/proxy-body-size"
            ],
            "512m",
        )

        notes_text = (CHART / "templates" / "NOTES.txt").read_text(encoding="utf-8")
        readme_text = (CHART / "README.md").read_text(encoding="utf-8")
        self.assertNotIn("INIT_LLM_MODEL_NAME", notes_text)
        self.assertNotIn("INIT_LLM_MODEL_NAME", readme_text)


if __name__ == "__main__":
    unittest.main()
