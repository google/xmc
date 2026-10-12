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
        self.screenshots_dir = os.path.join(self.tmp_dir.name, "screenshots")
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir=self.screenshots_dir,
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
            f.write(
                "spec:\n"
                "  containers:\n"
                "    - imagePullPolicy: Always\n"
                '    - imagePullPolicy: "Always"\n'
            )

        self.deployer._patch_image_pull_policy()

        with open(sample_yaml, encoding="utf-8") as f:
            content = f.read()
        self.assertEqual(
            content,
            "spec:\n"
            "  containers:\n"
            "    - imagePullPolicy: IfNotPresent\n"
            "    - imagePullPolicy: IfNotPresent\n",
        )

    def test_generate_values_file(self):
        self.deployer._generate_values_file()

        self.assertTrue(os.path.isfile(self.deployer.values_file))
        with open(self.deployer.values_file, encoding="utf-8") as f:
            values = f.read()

        self.assertIn('projectId: "test-proj"', values)
        self.assertIn("e2e-ess-1234-abc", values)
        self.assertIn("e2e-xid-1234-abc", values)
        self.assertIn('jwksUrl: "http://dex.dex.svc.cluster.local:5556/dex/keys"', values)
        self.assertIn('issuerUrl: "http://localhost:5556/dex"', values)
        self.assertIn('allowedEmails: "admin@xmc.local,viewer@xmc.local"', values)
        self.assertIn('username: "admin@xmc.local"', values)
        self.assertIn('password: "password123"', values)

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_wait_for_rollouts_success(self, mock_run):
        self.deployer._wait_for_rollouts()

        context = f"kind-{self.cfg.cluster_name}"
        mock_run.assert_any_call(
            [
                "kubectl",
                "--context",
                context,
                "rollout",
                "status",
                "deployment/dex",
                "-n",
                "dex",
                "--timeout=180s",
            ]
        )
        for deploy_name in (
            "xid-service-deployment",
            "experiment-state-server-deployment",
            "actuator-deployment",
            "reconciler-deployment",
            "ui-deployment",
            "gateway-proxy",
        ):
            mock_run.assert_any_call(
                [
                    "kubectl",
                    "--context",
                    context,
                    "rollout",
                    "status",
                    f"deployment/{deploy_name}",
                    "-n",
                    "xmc",
                    "--timeout=180s",
                ]
            )

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_wait_for_rollouts_failure_captures_diagnostics(self, mock_run):
        def _side_effect(cmd, *args, **kwargs):
            if "rollout" in cmd:
                raise RuntimeError("Rollout timed out")
            return unittest.mock.MagicMock(stdout="mock-pod-1", stderr="")

        mock_run.side_effect = _side_effect

        with self.assertRaises(RuntimeError):
            self.deployer._wait_for_rollouts()

        diag_file = os.path.join(self.cfg.screenshots_dir, "k8s_diagnostics.log")
        self.assertTrue(os.path.isfile(diag_file))

    def test_prepare_helm_chart_prunes_unsupported_resources_and_keeps_kueue_dev_and_wpc(self):
        src_helm = os.path.join(self.cfg.temp_dir, "workspace_helm")
        os.makedirs(os.path.join(src_helm, "templates", "certs"), exist_ok=True)
        os.makedirs(os.path.join(src_helm, "templates", "kueue-dev"), exist_ok=True)
        os.makedirs(os.path.join(src_helm, "templates", "wpc"), exist_ok=True)
        os.makedirs(os.path.join(src_helm, "templates", "ess"), exist_ok=True)
        with open(
            os.path.join(src_helm, "templates", "kueue-dev", "cpu-queue.yaml"),
            "w",
            encoding="utf-8",
        ) as f:
            f.write("kind: ClusterQueue\n")
        with open(
            os.path.join(src_helm, "templates", "wpc", "exp_medium_wu_medium.yaml"),
            "w",
            encoding="utf-8",
        ) as f:
            f.write("kind: WorkloadPriorityClass\n")
        with open(
            os.path.join(src_helm, "templates", "ess", "gateway.yaml"), "w", encoding="utf-8"
        ) as f:
            f.write("kind: Gateway\n")
        with open(
            os.path.join(src_helm, "templates", "ess", "deployment.yaml"), "w", encoding="utf-8"
        ) as f:
            f.write("kind: Deployment\n")

        self.deployer.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root=self.cfg.temp_dir,
            screenshots_dir=self.cfg.screenshots_dir,
            temp_dir=self.cfg.temp_dir,
        )
        # Point deploy/helm inside workspace_root
        os.makedirs(os.path.join(self.cfg.temp_dir, "deploy"), exist_ok=True)
        os.rename(src_helm, os.path.join(self.cfg.temp_dir, "deploy", "helm"))

        self.deployer._prepare_helm_chart()

        self.assertTrue(
            os.path.isfile(
                os.path.join(self.deployer.helm_dir, "templates", "ess", "deployment.yaml")
            )
        )
        self.assertTrue(
            os.path.isfile(
                os.path.join(self.deployer.helm_dir, "templates", "kueue-dev", "cpu-queue.yaml")
            )
        )
        self.assertTrue(
            os.path.isfile(
                os.path.join(
                    self.deployer.helm_dir, "templates", "wpc", "exp_medium_wu_medium.yaml"
                )
            )
        )
        self.assertFalse(os.path.exists(os.path.join(self.deployer.helm_dir, "templates", "certs")))
        self.assertFalse(
            os.path.exists(os.path.join(self.deployer.helm_dir, "templates", "ess", "gateway.yaml"))
        )

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_resolve_crds_manifest_returns_existing_bazel_bin_file(self, mock_run):
        manifest_dir = os.path.join(self.tmp_dir.name, "bazel-bin", "test", "e2e", "manifests")
        os.makedirs(manifest_dir, exist_ok=True)
        manifest_path = os.path.join(manifest_dir, "jobset_kueue_crds.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write("kind: CustomResourceDefinition\n")

        self.deployer.cfg.workspace_root = self.tmp_dir.name
        resolved = self.deployer._resolve_crds_manifest()

        self.assertEqual(resolved, manifest_path)
        mock_run.assert_not_called()

    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_resolve_crds_manifest_builds_when_absent(self, mock_run):
        mock_run.return_value = unittest.mock.MagicMock(
            stdout="Starting local Bazel server...\n/custom/bazel-bin\n"
        )
        self.deployer.cfg.workspace_root = self.tmp_dir.name

        resolved = self.deployer._resolve_crds_manifest()

        self.assertEqual(
            resolved,
            os.path.join("/custom/bazel-bin", "test", "e2e", "manifests", "jobset_kueue_crds.yaml"),
        )
        mock_run.assert_any_call(
            ["bazel", "build", "//test/e2e/manifests:jobset_kueue_crds"],
            cwd=self.tmp_dir.name,
        )

    @patch.object(HelmDeployer, "_resolve_crds_manifest", return_value="/path/to/crds.yaml")
    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_apply_crds_uses_server_side_apply(self, mock_run, mock_resolve):
        self.deployer._apply_crds()

        mock_resolve.assert_called_once()
        mock_run.assert_called_once_with(
            [
                "kubectl",
                "--context",
                "kind-test-cluster",
                "apply",
                "--server-side",
                "-f",
                "/path/to/crds.yaml",
            ]
        )

    @patch.object(HelmDeployer, "_prepare_helm_chart")
    @patch.object(HelmDeployer, "_patch_image_pull_policy")
    @patch.object(HelmDeployer, "_generate_values_file")
    @patch.object(HelmDeployer, "_apply_crds")
    @patch.object(HelmDeployer, "_wait_for_rollouts")
    @patch("test.e2e.lib.helm_deployer.run_cmd")
    def test_deploy_orchestrates_release(
        self, mock_run, mock_wait, mock_apply_crds, mock_gen_vals, mock_patch, mock_prep
    ):
        self.deployer.deploy()

        mock_prep.assert_called_once()
        mock_patch.assert_called_once()
        mock_gen_vals.assert_called_once()
        mock_apply_crds.assert_called_once()
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
