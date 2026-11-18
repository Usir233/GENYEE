"""Render checks for docreader gRPC TLS configuration and dynamic service port."""

import sys
import unittest
from pathlib import Path

import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))
from test_app_storage import render


class DocreaderTlsAndPortTests(unittest.TestCase):
    def documents(self, values=None):
        result = render(values)
        self.assertEqual(result.returncode, 0, result.stderr)
        return [doc for doc in yaml.safe_load_all(result.stdout) if doc]

    def by_component(self, docs, kind):
        return {
            d["metadata"]["labels"]["app.kubernetes.io/component"]: d
            for d in docs
            if d["kind"] == kind
            and "app.kubernetes.io/component" in d["metadata"].get("labels", {})
        }

    def env_map(self, deployment):
        container = deployment["spec"]["template"]["spec"]["containers"][0]
        return {entry["name"]: entry for entry in container.get("env", [])}

    def test_defaults_keep_plaintext_probes_and_port_50051(self):
        docs = self.documents()
        deployments = self.by_component(docs, "Deployment")
        services = self.by_component(docs, "Service")
        netpols = self.by_component(docs, "NetworkPolicy")

        docreader_container = deployments["docreader"]["spec"]["template"]["spec"]["containers"][0]
        docreader_env = self.env_map(deployments["docreader"])
        app_env = self.env_map(deployments["app"])

        self.assertEqual(docreader_container["ports"][0]["containerPort"], 50051)
        self.assertEqual(docreader_env["DOCREADER_GRPC_PORT"]["value"], "50051")
        self.assertNotIn("GRPC_TLS_ENABLED", docreader_env)
        self.assertNotIn("GRPC_TLS_CA", docreader_env)
        self.assertNotIn("GRPC_TLS_ENABLED", app_env)
        self.assertNotIn("GRPC_TLS_CA", app_env)
        self.assertEqual(app_env["DOCREADER_ADDR"]["value"], "docreader:50051")

        for probe in ("livenessProbe", "readinessProbe"):
            self.assertEqual(
                docreader_container[probe]["exec"]["command"],
                ["grpc_health_probe", "-addr=localhost:50051"],
            )

        self.assertEqual(services["docreader"]["spec"]["ports"][0]["port"], 50051)
        self.assertEqual(netpols["docreader"]["spec"]["ingress"][0]["ports"][0]["port"], 50051)

    def test_tls_enabled_without_secret_name_fails_validation(self):
        for bad_secret in ("", "   "):
            with self.subTest(secretName=bad_secret):
                result = render({"docreader": {"tls": {"enabled": True, "secretName": bad_secret}}})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(
                    "docreader.tls.secretName is required when docreader.tls.enabled is true",
                    result.stderr,
                )

        external_result = render(
            {
                "docreader": {
                    "enabled": False,
                    "tls": {"enabled": True, "secretName": ""},
                },
            }
        )
        self.assertNotEqual(external_result.returncode, 0)
        self.assertIn(
            "docreader.tls.secretName is required when docreader.tls.enabled is true",
            external_result.stderr,
        )

    def test_tls_enabled_without_custom_ca_supports_public_ca_secrets(self):
        docs = self.documents(
            {
                "docreader": {
                    "tls": {
                        "enabled": True,
                        "secretName": "docreader-public-tls",
                        "caProvided": False,
                        "serverName": "docreader.genyee.example",
                    },
                },
            }
        )
        deployments = self.by_component(docs, "Deployment")
        docreader_spec = deployments["docreader"]["spec"]["template"]["spec"]
        docreader_container = docreader_spec["containers"][0]
        docreader_env = self.env_map(deployments["docreader"])

        app_spec = deployments["app"]["spec"]["template"]["spec"]
        app_container = app_spec["containers"][0]
        app_env = self.env_map(deployments["app"])

        # Server has cert + key mounted, no GRPC_TLS_CA env (prevents accidental mTLS).
        self.assertEqual(docreader_env["GRPC_TLS_ENABLED"]["value"], "true")
        self.assertEqual(docreader_env["GRPC_TLS_CERT"]["value"], "/etc/docreader-tls/tls.crt")
        self.assertEqual(docreader_env["GRPC_TLS_KEY"]["value"], "/etc/docreader-tls/tls.key")
        self.assertNotIn("GRPC_TLS_CA", docreader_env)

        docreader_mounts = {m["name"]: m for m in docreader_container.get("volumeMounts", [])}
        self.assertEqual(docreader_mounts["docreader-tls"]["mountPath"], "/etc/docreader-tls")
        self.assertTrue(docreader_mounts["docreader-tls"]["readOnly"])
        docreader_volumes = {v["name"]: v for v in docreader_spec.get("volumes", [])}
        self.assertEqual(
            docreader_volumes["docreader-tls"]["secret"]["secretName"],
            "docreader-public-tls",
        )

        expected_probe_cmd = [
            "grpc_health_probe",
            "-addr=localhost:50051",
            "-tls",
            "-tls-server-name=docreader.genyee.example",
        ]
        for probe in ("livenessProbe", "readinessProbe"):
            self.assertEqual(docreader_container[probe]["exec"]["command"], expected_probe_cmd)

        # App client uses system CA roots without requiring ca.crt or mounting server private key.
        self.assertEqual(app_env["GRPC_TLS_ENABLED"]["value"], "true")
        self.assertEqual(app_env["GRPC_TLS_SERVER_NAME"]["value"], "docreader.genyee.example")
        self.assertNotIn("GRPC_TLS_CA", app_env)
        app_mount_names = {m["name"] for m in app_container.get("volumeMounts", [])}
        app_volume_names = {v["name"] for v in app_spec.get("volumes", [])}
        self.assertNotIn("docreader-tls", app_mount_names)
        self.assertNotIn("docreader-tls", app_volume_names)

    def test_tls_enabled_with_custom_ca_injects_ca_into_app_and_probes(self):
        for ca_key_cfg, expected_key in (
            (None, "ca.crt"),
            ("corp-root.pem", "corp-root.pem"),
        ):
            with self.subTest(caKey=ca_key_cfg):
                tls_cfg = {
                    "enabled": True,
                    "secretName": "docreader-private-tls",
                    "caProvided": True,
                }
                if ca_key_cfg is not None:
                    tls_cfg["caKey"] = ca_key_cfg

                docs = self.documents({"docreader": {"tls": tls_cfg}})
                deployments = self.by_component(docs, "Deployment")
                docreader_container = deployments["docreader"]["spec"]["template"]["spec"][
                    "containers"
                ][0]
                app_spec = deployments["app"]["spec"]["template"]["spec"]
                app_container = app_spec["containers"][0]
                app_env = self.env_map(deployments["app"])

                expected_ca_path = f"/etc/docreader-tls/{expected_key}"
                self.assertEqual(app_env["GRPC_TLS_ENABLED"]["value"], "true")
                self.assertEqual(app_env["GRPC_TLS_CA"]["value"], expected_ca_path)

                app_mounts = {m["name"]: m for m in app_container.get("volumeMounts", [])}
                self.assertEqual(app_mounts["docreader-tls"]["mountPath"], "/etc/docreader-tls")
                self.assertTrue(app_mounts["docreader-tls"]["readOnly"])
                app_volumes = {v["name"]: v for v in app_spec.get("volumes", [])}
                self.assertEqual(
                    app_volumes["docreader-tls"]["secret"],
                    {
                        "secretName": "docreader-private-tls",
                        "items": [{"key": expected_key, "path": expected_key}],
                    },
                )

                expected_probe_cmd = [
                    "grpc_health_probe",
                    "-addr=localhost:50051",
                    "-tls",
                    f"-tls-ca-cert={expected_ca_path}",
                ]
                for probe in ("livenessProbe", "readinessProbe"):
                    self.assertEqual(
                        docreader_container[probe]["exec"]["command"],
                        expected_probe_cmd,
                    )

    def test_tls_invalid_ca_key_path_traversal_is_rejected(self):
        result = render(
            {
                "docreader": {
                    "tls": {
                        "enabled": True,
                        "secretName": "docreader-private-tls",
                        "caProvided": True,
                        "caKey": "../evil.crt",
                    },
                },
            }
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("docreader.tls.caKey must be a valid Secret key name", result.stderr)

    def test_custom_docreader_service_port_propagates_everywhere(self):
        docs = self.documents(
            {
                "docreader": {
                    "service": {
                        "name": "genyee-docreader",
                        "port": 50061,
                    },
                    "tls": {
                        "enabled": True,
                        "secretName": "docreader-tls-secret",
                        "caProvided": True,
                    },
                },
            }
        )
        deployments = self.by_component(docs, "Deployment")
        services = self.by_component(docs, "Service")
        netpols = self.by_component(docs, "NetworkPolicy")

        docreader_container = deployments["docreader"]["spec"]["template"]["spec"]["containers"][0]
        docreader_env = self.env_map(deployments["docreader"])
        app_env = self.env_map(deployments["app"])

        self.assertEqual(docreader_container["ports"][0]["containerPort"], 50061)
        self.assertEqual(docreader_env["DOCREADER_GRPC_PORT"]["value"], "50061")
        self.assertEqual(app_env["DOCREADER_ADDR"]["value"], "genyee-docreader:50061")
        self.assertEqual(services["docreader"]["spec"]["ports"][0]["port"], 50061)
        self.assertEqual(netpols["docreader"]["spec"]["ingress"][0]["ports"][0]["port"], 50061)

        expected_probe_cmd = [
            "grpc_health_probe",
            "-addr=localhost:50061",
            "-tls",
            "-tls-ca-cert=/etc/docreader-tls/ca.crt",
        ]
        for probe in ("livenessProbe", "readinessProbe"):
            self.assertEqual(docreader_container[probe]["exec"]["command"], expected_probe_cmd)

    def test_docreader_env_loop_extra_env_and_tmp_emptydir(self):
        default_docs = self.documents()
        default_dep = self.by_component(default_docs, "Deployment")["docreader"]
        default_env = self.env_map(default_dep)
        self.assertNotIn("STORAGE_TYPE", default_env)
        mounts = {
            m["name"]: m
            for m in default_dep["spec"]["template"]["spec"]["containers"][0].get(
                "volumeMounts", []
            )
        }
        volumes = {v["name"]: v for v in default_dep["spec"]["template"]["spec"].get("volumes", [])}
        self.assertIn("tmp", mounts)
        self.assertEqual(mounts["tmp"]["mountPath"], "/tmp")
        self.assertIn("emptyDir", volumes["tmp"])

        custom_docs = self.documents(
            {
                "docreader": {
                    "env": {
                        "DOCREADER_GRPC_MAX_WORKERS": "8",
                        "LOG_LEVEL": "info",
                    },
                    "extraEnv": [
                        {
                            "name": "ODL_HYBRID_AUTH_TOKEN",
                            "valueFrom": {"secretKeyRef": {"name": "odl-secret", "key": "token"}},
                        }
                    ],
                }
            }
        )
        custom_env = self.env_map(self.by_component(custom_docs, "Deployment")["docreader"])
        self.assertEqual(custom_env["DOCREADER_GRPC_MAX_WORKERS"]["value"], "8")
        self.assertEqual(custom_env["LOG_LEVEL"]["value"], "info")
        self.assertEqual(
            custom_env["ODL_HYBRID_AUTH_TOKEN"]["valueFrom"]["secretKeyRef"],
            {"name": "odl-secret", "key": "token"},
        )


if __name__ == "__main__":
    unittest.main()
