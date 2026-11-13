"""Image selection must never prune outside the configured registry."""

from pathlib import Path
import subprocess
import unittest


SELECTOR = Path(__file__).resolve().parents[1] / "cloud-image" / "select-old-images.sh"


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
            self.select("genyee", "v0.9.0", [
                "genyee/genyee-app:v0.8.3",
                "genyee/genyee-ui:v0.9.0",
                "genyee/genyee-sandbox:v0.9.0",
                "redis:7.0-alpine",
                "paradedb/paradedb:v0.25.11-pg17",
            ]),
            ["genyee/genyee-app:v0.8.3"],
        )

    def test_private_registry_does_not_select_default_registry(self):
        self.assertEqual(
            self.select("registry.example:5000/team", "latest", [
                "registry.example:5000/team/genyee-app:old",
                "registry.example:5000/team/genyee-app:latest",
                "genyee/genyee-app:old",
                "registry.example:5000/other/genyee-app:old",
                "registry.example:5000/team/other-app:old",
            ]),
            ["registry.example:5000/team/genyee-app:old"],
        )

    def test_registry_and_tag_are_literal_values(self):
        self.assertEqual(
            self.select("registry.example/team", "v0.9.0", [
                "registryXexample/team/genyee-app:old",
                "registry.example/team/genyee-app:v0x9x0",
                "registry.example/team/genyee-app:v0.9.0",
                "registry.example/team/genyee-app:v0.9.0-extra",
            ]),
            [
                "registry.example/team/genyee-app:v0x9x0",
                "registry.example/team/genyee-app:v0.9.0-extra",
            ],
        )

    def test_empty_input_selects_nothing(self):
        self.assertEqual(self.select("genyee", "latest", []), [])


if __name__ == "__main__":
    unittest.main()
