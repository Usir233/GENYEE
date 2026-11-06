"""Render the delivery contracts used by the offline image loader."""
import unittest
import yaml

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
            deployments = self.deployments({"app": {"env": {"WEKNORA_WEKNORACLOUD_ENABLED": enabled}},
                                           "frontend": {"env": {"DOCS_BASE_URL": "/customer-docs/"}}})
            for component in ("app", "frontend"):
                env = {e["name"]: e.get("value") for e in
                       deployments[component]["spec"]["template"]["spec"]["containers"][0]["env"]}
                self.assertEqual(env["WEKNORA_WEKNORACLOUD_ENABLED"], enabled)
                if component == "frontend":
                    self.assertEqual(env["DOCS_BASE_URL"], "/customer-docs/")


if __name__ == "__main__":
    unittest.main()
