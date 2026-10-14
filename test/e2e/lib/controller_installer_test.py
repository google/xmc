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

"""Unit tests for test.e2e.lib.controller_installer."""

from __future__ import annotations

import os
import tempfile
import unittest
from unittest import mock

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.controller_installer import ControllerInstaller

_CONTEXT = "kind-test-cluster"
_WEBHOOK_CA = "jsonpath={.webhooks[0].clientConfig.caBundle}"
_CRD_CONVERSION_CA = "jsonpath={.spec.conversion.webhook.clientConfig.caBundle}"


def _kubectl(*args: str) -> list[str]:
    """Returns a kubectl command line against the test cluster context."""
    return ["kubectl", "--context", _CONTEXT, *args]


def _rollout(deployment: str, namespace: str) -> list[str]:
    """Returns the kubectl command line that waits for a controller deployment rollout."""
    return _kubectl("rollout", "status", deployment, "-n", namespace, "--timeout=180s")


def _ca_query(webhook_configuration: str) -> list[str]:
    """Returns the kubectl command line that reads a webhook configuration's CA bundle."""
    return _kubectl("get", webhook_configuration, "-o", _WEBHOOK_CA)


def _conversion_ca_query(crd: str) -> list[str]:
    """Returns the kubectl command line that reads a CRD's conversion webhook CA bundle."""
    return _kubectl("get", crd, "-o", _CRD_CONVERSION_CA)


class ControllerInstallerTest(unittest.TestCase):
    """Tests for ControllerInstaller."""

    def setUp(self) -> None:
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp_dir.cleanup)
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root=self.tmp_dir.name,
            screenshots_dir=os.path.join(self.tmp_dir.name, "screenshots"),
            temp_dir=self.tmp_dir.name,
        )
        self.installer = ControllerInstaller(self.cfg)

    @mock.patch.object(
        ControllerInstaller, "_resolve_manifest", side_effect=lambda t: f"/manifests/{t}.yaml"
    )
    @mock.patch("test.e2e.lib.controller_installer.run_cmd")
    def test_install_all_applies_jobset_first_then_waits_for_both(
        self, mock_run: mock.MagicMock, mock_resolve: mock.MagicMock
    ) -> None:
        """install_all applies both releases in order before waiting until each one serves."""
        mock_run.return_value = mock.MagicMock(stdout="Y2EtYnVuZGxl\n")
        kueue_crds = [
            f"crd/{plural}.kueue.x-k8s.io"
            for plural in (
                "clusterqueues",
                "localqueues",
                "resourceflavors",
                "workloadpriorityclasses",
                "workloads",
            )
        ]

        self.installer.install_all()

        apply = ("apply", "--server-side", "--force-conflicts", "-f")
        established = ("wait", "--for=condition=established", "--timeout=60s")
        expected = [
            _kubectl(*apply, "/manifests/jobset_manifests.yaml"),
            _kubectl(*established, "crd/jobsets.jobset.x-k8s.io"),
            _kubectl(*apply, "/manifests/kueue_manifests.yaml"),
            _kubectl(*established, *kueue_crds),
            _rollout("deployment/jobset-controller-manager", "jobset-system"),
            _ca_query("mutatingwebhookconfiguration/jobset-mutating-webhook-configuration"),
            _ca_query("validatingwebhookconfiguration/jobset-validating-webhook-configuration"),
            _rollout("deployment/kueue-controller-manager", "kueue-system"),
            _kubectl(
                "wait",
                "--for=condition=Available",
                "--timeout=60s",
                "apiservice/v1beta1.visibility.kueue.x-k8s.io",
                "apiservice/v1beta2.visibility.kueue.x-k8s.io",
            ),
            _ca_query("mutatingwebhookconfiguration/kueue-mutating-webhook-configuration"),
            _ca_query("validatingwebhookconfiguration/kueue-validating-webhook-configuration"),
            _conversion_ca_query("crd/clusterqueues.kueue.x-k8s.io"),
            _conversion_ca_query("crd/localqueues.kueue.x-k8s.io"),
            _conversion_ca_query("crd/workloads.kueue.x-k8s.io"),
        ]
        self.assertEqual([c.args[0] for c in mock_run.call_args_list], expected)
        self.assertEqual(
            mock_resolve.call_args_list,
            [mock.call("jobset_manifests"), mock.call("kueue_manifests")],
        )

    @mock.patch("test.e2e.lib.polling.time.sleep")
    @mock.patch("test.e2e.lib.controller_installer.run_cmd")
    def test_wait_for_ca_bundle_polls_until_injected(
        self, mock_run: mock.MagicMock, mock_sleep: mock.MagicMock
    ) -> None:
        """The CA bundle wait keeps polling while the injected bundle is empty."""
        mock_run.side_effect = [
            mock.MagicMock(stdout=""),
            mock.MagicMock(stdout="  \n"),
            mock.MagicMock(stdout="Y2EtYnVuZGxl"),
        ]

        self.installer._wait_for_ca_bundle("crd/workloads.kueue.x-k8s.io", "{.caBundle}")

        self.assertEqual(mock_run.call_count, 3)
        mock_run.assert_called_with(
            _kubectl("get", "crd/workloads.kueue.x-k8s.io", "-o", "jsonpath={.caBundle}"),
            check=False,
            capture_output=True,
        )
        self.assertEqual(mock_sleep.call_count, 2)

    @mock.patch("test.e2e.lib.controller_installer.run_cmd")
    def test_resolve_manifest_returns_existing_bazel_bin_file(
        self, mock_run: mock.MagicMock
    ) -> None:
        """_resolve_manifest returns a non-empty bazel-bin output without building."""
        manifest_dir = os.path.join(self.tmp_dir.name, "bazel-bin", "test", "e2e", "manifests")
        os.makedirs(manifest_dir, exist_ok=True)
        manifest_path = os.path.join(manifest_dir, "kueue_manifests.yaml")
        with open(manifest_path, "w", encoding="utf-8") as f:
            f.write("kind: Namespace\n")

        resolved = self.installer._resolve_manifest("kueue_manifests")

        self.assertEqual(resolved, manifest_path)
        mock_run.assert_not_called()

    @mock.patch("test.e2e.lib.controller_installer.run_cmd")
    def test_resolve_manifest_builds_when_absent(self, mock_run: mock.MagicMock) -> None:
        """_resolve_manifest builds the target and falls back to 'bazel info bazel-bin'."""
        mock_run.return_value = mock.MagicMock(
            stdout="Starting local Bazel server...\n/custom/bazel-bin\n"
        )

        resolved = self.installer._resolve_manifest("jobset_manifests")

        self.assertEqual(
            resolved,
            os.path.join("/custom/bazel-bin", "test", "e2e", "manifests", "jobset_manifests.yaml"),
        )
        mock_run.assert_any_call(
            ["bazel", "build", "//test/e2e/manifests:jobset_manifests"],
            cwd=self.tmp_dir.name,
        )

    @mock.patch("test.e2e.lib.controller_installer.run_cmd")
    def test_resolve_manifest_raises_on_empty_bazel_bin(self, mock_run: mock.MagicMock) -> None:
        """_resolve_manifest raises when 'bazel info bazel-bin' prints nothing."""
        mock_run.return_value = mock.MagicMock(stdout="   \n")

        with self.assertRaisesRegex(RuntimeError, "Failed to resolve bazel-bin"):
            self.installer._resolve_manifest("jobset_manifests")


if __name__ == "__main__":
    unittest.main()
