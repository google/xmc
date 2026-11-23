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

"""Hermetic blueprint and Terraform module tests for pinned ML Diagnostics operator setup."""

import os
import re
import unittest

from chart_test_lib import BLUEPRINT_PATH, extract_module_block, find_repo_file

MODULE_DIR = os.path.join("deploy", "ctk_v2", "modules", "mldiagnostics-operator")
EXPECTED_PINNED_VERSION = "0.24.0"


class MldiagnosticsOperatorBlueprintTest(unittest.TestCase):
    """Verifies CTK v2 blueprint pins mldiagnostics-connection-operator and guards conflicts."""

    def setUp(self) -> None:
        with open(find_repo_file(BLUEPRINT_PATH), encoding="utf-8") as blueprint_file:
            self.blueprint = blueprint_file.read()

    def test_blueprint_declares_default_flags_and_pinned_version(self) -> None:
        self.assertRegex(
            self.blueprint,
            r"(?m)^\s*deploy_mldiagnostics_operator:\s*false\s*$",
        )
        self.assertRegex(
            self.blueprint,
            rf'(?m)^\s*mldiagnostics_operator_chart_version:\s*"{re.escape(EXPECTED_PINNED_VERSION)}"\s*$',
        )

    def test_kubernetes_prereqs_wires_pinned_operator_module(self) -> None:
        block = extract_module_block(self.blueprint, "install_mldiagnostics_operator")
        self.assertIn("source: ./deploy/ctk_v2/modules/mldiagnostics-operator", block)
        self.assertIn("use: [compute_cluster]", block)
        self.assertIn("enabled: $(vars.deploy_mldiagnostics_operator)", block)
        self.assertIn(
            "enable_managed_mldiagnostics: $(vars.enable_managed_mldiagnostics)",
            block,
        )
        self.assertIn("release_name: mldiagnostics-connection-operator", block)
        self.assertIn(
            "chart_version: $(vars.mldiagnostics_operator_chart_version)",
            block,
        )
        # Explicit namespace is required because `use: [compute_cluster]` otherwise auto-wires
        # `compute_cluster`'s `namespace` output ("xmc") over the module's default.
        self.assertIn("namespace: gke-mldiagnostics", block)
        self.assertIn("create_namespace: true", block)
        self.assertIn(
            "dependencies: [$(install_cert_manager.release_completion_id)]",
            block,
        )

    def test_iam_and_api_grants_cover_both_managed_and_helm_operator_modes(self) -> None:
        expected_cond = "vars.enable_managed_mldiagnostics || vars.deploy_mldiagnostics_operator"
        for module_id, expected_grant in (
            ("cloud_api", "hypercomputecluster.googleapis.com"),
            ("workload_service_account", "hypercomputecluster.editor"),
            ("node_pool_service_account", "storage.objectAdmin"),
        ):
            with self.subTest(module_id=module_id):
                block = extract_module_block(self.blueprint, module_id)
                self.assertIn(expected_cond, block)
                self.assertIn(expected_grant, block)

        cluster_block = extract_module_block(self.blueprint, "compute_cluster")
        self.assertIn("enable_ml_diagnostics: $(vars.enable_managed_mldiagnostics)", cluster_block)
        self.assertNotIn("deploy_mldiagnostics_operator", cluster_block)

    def test_terraform_module_pins_default_version_and_enforces_exclusivity(self) -> None:
        variables_tf_path = find_repo_file(os.path.join(MODULE_DIR, "variables.tf"))
        main_tf_path = find_repo_file(os.path.join(MODULE_DIR, "main.tf"))
        with open(variables_tf_path, encoding="utf-8") as vf:
            variables_tf = vf.read()
        with open(main_tf_path, encoding="utf-8") as mf:
            main_tf = mf.read()

        self.assertIn(f'default     = "{EXPECTED_PINNED_VERSION}"', variables_tf)
        self.assertIn('lower(trimspace(var.chart_version)) != "latest"', variables_tf)
        self.assertIn("version          = var.chart_version", main_tf)
        self.assertIn("condition     = !var.enable_managed_mldiagnostics", main_tf)


if __name__ == "__main__":
    unittest.main()
