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

"""Verifies executionEnvironment and kubernetesPlacement rendering in the Helm chart."""

import ast
import json
import re
import unittest

from chart_test_lib import render_template, require_helm

ACTUATOR_CONFIGMAP = "templates/actuator/configmap.yaml"
ACTUATOR_DEPLOYMENT = "templates/actuator/deployment.yaml"
ESS_DEPLOYMENT = "templates/ess/deployment.yaml"
RECONCILER_DEPLOYMENT = "templates/reconciler/deployment.yaml"


class ExecutionEnvironmentConfigTest(unittest.TestCase):
    """Asserts executionEnvironment flags and kubernetesPlacement ConfigMap JSON."""

    @classmethod
    def setUpClass(cls) -> None:
        require_helm()

    def test_default_execution_environment_is_gke(self) -> None:
        """Default values must wire gke across actuator, ess, and reconciler."""
        actuator = render_template(ACTUATOR_DEPLOYMENT, {})
        ess = render_template(ESS_DEPLOYMENT, {})
        reconciler = render_template(RECONCILER_DEPLOYMENT, {})

        self.assertIn("-default_execution_environment=gke", actuator)
        self.assertIn("-ess_default_execution_environment=gke", ess)
        self.assertIn("-default_execution_environment=gke", reconciler)

    def test_kubernetes_execution_environment_propagates_to_deployments(self) -> None:
        """Setting executionEnvironment=kubernetes updates all three deployments."""
        overrides = {"executionEnvironment": "kubernetes"}
        actuator = render_template(ACTUATOR_DEPLOYMENT, overrides)
        ess = render_template(ESS_DEPLOYMENT, overrides)
        reconciler = render_template(RECONCILER_DEPLOYMENT, overrides)

        self.assertIn("-default_execution_environment=kubernetes", actuator)
        self.assertIn("-ess_default_execution_environment=kubernetes", ess)
        self.assertIn("-default_execution_environment=kubernetes", reconciler)

    def test_kubernetes_placement_renders_valid_json_in_actuator_config(self) -> None:
        """kubernetesPlacement values serialize cleanly as JSON in actuator-config."""
        manifest = render_template(
            ACTUATOR_CONFIGMAP,
            {
                "executionEnvironment": "kubernetes",
                "kubernetesPlacement.gpu.schedulerName": "gang-scheduler",
                "kubernetesPlacement.gpu.exclusivePlacement": "true",
            },
        )
        match = re.search(
            r"^\s*kubernetes_placement_config:\s*(\".*\")\s*$",
            manifest,
            re.MULTILINE,
        )
        self.assertIsNotNone(match, f"kubernetes_placement_config missing in:\n{manifest}")
        assert match is not None
        raw_json = ast.literal_eval(match.group(1))
        parsed = json.loads(raw_json)
        self.assertEqual(parsed["gpu"]["schedulerName"], "gang-scheduler")
        self.assertIs(parsed["gpu"]["exclusivePlacement"], True)


if __name__ == "__main__":
    unittest.main()
