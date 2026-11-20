# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Hermetic unit tests for Artifact Registry IAM bindings in the CTK v2 blueprint."""

import re
import unittest

from chart_test_lib import (
    BLUEPRINT_PATH,
    extract_module_block,
    find_repo_file,
)


def _read_repo_file(rel_path: str) -> str:
    with open(find_repo_file(rel_path), encoding="utf-8") as f:
        return f.read()


class ArtifactRegistryIamTest(unittest.TestCase):
    """Verifies repository-scoped Artifact Registry reader IAM setup for custom node SAs."""

    def setUp(self) -> None:
        super().setUp()
        self.blueprint = _read_repo_file(BLUEPRINT_PATH)
        self.template = _read_repo_file("deploy/ctk_v2/xmc-deployment-template.yaml")

    def test_blueprint_and_template_declare_reader_service_accounts(self) -> None:
        """Verifies blueprint and template expose reader_service_accounts."""
        self.assertRegex(
            self.blueprint,
            re.compile(r"^  reader_service_accounts:\s*\[\]", re.MULTILINE),
        )
        self.assertRegex(
            self.template,
            re.compile(r"^  reader_service_accounts:\s*\[\]", re.MULTILINE),
        )

    def test_artifact_registry_reader_iam_and_compute_cluster_wired(self) -> None:
        """Verifies artifact_registry_reader_iam and compute_cluster use node_pool_service_account."""
        module_block = extract_module_block(self.blueprint, "artifact_registry_reader_iam")
        self.assertIn(
            "source: ./deploy/ctk_v2/modules/artifact-registry-iam",
            module_block,
        )
        self.assertIn("use: [node_pool_service_account]", module_block)
        self.assertIn("image_registry: $(vars.image_registry)", module_block)
        self.assertIn(
            "node_service_account_email: $(node_pool_service_account.service_account_email)",
            module_block,
        )
        self.assertIn(
            "reader_service_accounts: $(vars.reader_service_accounts)",
            module_block,
        )

        cluster_block = extract_module_block(self.blueprint, "compute_cluster")
        self.assertIn("use: [network, node_pool_service_account]", cluster_block)

        workload_sa_block = extract_module_block(self.blueprint, "workload_service_account")
        self.assertNotIn("artifactregistry.reader", workload_sa_block)

        node_sa_block = extract_module_block(self.blueprint, "node_pool_service_account")
        self.assertNotIn("artifactregistry.reader", node_sa_block)
        self.assertNotIn("storage.objectViewer", node_sa_block)


if __name__ == "__main__":
    unittest.main()
