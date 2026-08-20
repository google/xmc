# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Unit tests for HelmDeployer."""

import os
import tempfile
import unittest
from unittest.mock import patch

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.helm_deployer import HelmDeployer


class TestHelmDeployer(unittest.TestCase):
    """Tests for HelmDeployer."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            ess_db="e2e-ess-1234-abc",
            xid_db="e2e-xid-1234-abc",
            temp_dir=self.tmp_dir.name,
        )
        self.deployer = HelmDeployer(self.cfg)

    def tearDown(self):
        self.tmp_dir.cleanup()

    def test_patch_image_pull_policy(self):
        # Create a mock template in helm_dir
        os.makedirs(os.path.join(self.deployer.helm_dir, "templates"), exist_ok=True)
        sample_yaml = os.path.join(self.deployer.helm_dir, "templates", "deploy.yaml")
        with open(sample_yaml, "w", encoding="utf-8") as f:
            f.write("spec:\n  containers:\n    - imagePullPolicy: Always\n")

        self.deployer._patch_image_pull_policy()

        with open(sample_yaml, encoding="utf-8") as f:
            content = f.read()
        self.assertIn("imagePullPolicy: IfNotPresent", content)
        self.assertNotIn("imagePullPolicy: Always", content)

    def test_generate_values_file(self):
        self.deployer._generate_values_file()

        self.assertTrue(os.path.isfile(self.deployer.values_file))
        with open(self.deployer.values_file, encoding="utf-8") as f:
            values = f.read()

        self.assertIn('projectId: "test-proj"', values)
        self.assertIn("e2e-ess-1234-abc", values)
        self.assertIn("e2e-xid-1234-abc", values)
        self.assertIn("http://dex.dex.svc.cluster.local:5556/dex", values)

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_wait_for_rollouts_success(self, mock_run):
        self.deployer._wait_for_rollouts()

        mock_run.assert_any_call(
            [
                "kubectl",
                "rollout",
                "status",
                "deployment/dex",
                "-n",
                "dex",
                "--timeout=180s",
            ]
        )
        mock_run.assert_any_call(
            [
                "kubectl",
                "rollout",
                "status",
                "deployment/gateway-proxy",
                "-n",
                "xmc",
                "--timeout=180s",
            ]
        )

    @patch.object(HelmDeployer, "_prepare_helm_chart")
    @patch.object(HelmDeployer, "_patch_image_pull_policy")
    @patch.object(HelmDeployer, "_generate_values_file")
    @patch.object(HelmDeployer, "_wait_for_rollouts")
    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_deploy_orchestrates_release(
        self, mock_run, mock_wait, mock_gen_vals, mock_patch, mock_prep
    ):
        self.deployer.deploy()

        mock_prep.assert_called_once()
        mock_patch.assert_called_once()
        mock_gen_vals.assert_called_once()
        mock_wait.assert_called_once()

        mock_run.assert_any_call(
            [
                "helm",
                "upgrade",
                "--install",
                "xmc",
                self.deployer.helm_dir,
                "--kube-context",
                "kind-test-cluster",
                "-n",
                "xmc",
                "-f",
                self.deployer.values_file,
            ]
        )


if __name__ == "__main__":
    unittest.main()
