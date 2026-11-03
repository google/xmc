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

"""Unit tests for deploy/ctk_v2/verify_xmc.py."""

import io
import os
import pathlib
import sys
import unittest
from unittest import mock

from deploy.ctk_v2 import verify_xmc


class VerifyXmcTest(unittest.TestCase):
    def test_parse_bool(self):
        self.assertTrue(verify_xmc._parse_bool("true"))
        self.assertTrue(verify_xmc._parse_bool("True"))
        self.assertTrue(verify_xmc._parse_bool("1"))
        self.assertTrue(verify_xmc._parse_bool("yes"))
        self.assertTrue(verify_xmc._parse_bool(True))
        self.assertFalse(verify_xmc._parse_bool("false"))
        self.assertFalse(verify_xmc._parse_bool("0"))
        self.assertFalse(verify_xmc._parse_bool("no"))
        self.assertFalse(verify_xmc._parse_bool(False))
        self.assertFalse(verify_xmc._parse_bool(None))
        self.assertFalse(verify_xmc._parse_bool(""))

    def test_resolve_config_from_env(self):
        env_dict = {
            "ENVIRONMENT": "dev",
            "REGION": "us-central1",
            "PROJECT_ID": "my-project",
        }
        with mock.patch.dict(os.environ, env_dict, clear=True):
            env, region, proj, enable_components = verify_xmc._resolve_config()
            self.assertEqual(env, "dev")
            self.assertEqual(region, "us-central1")
            self.assertEqual(proj, "my-project")
            self.assertFalse(enable_components)

    def test_resolve_config_from_env_with_enable_components(self):
        env_dict = {
            "ENVIRONMENT": "dev",
            "REGION": "us-central1",
            "PROJECT_ID": "my-project",
            "ENABLE_COMPONENTS": "true",
        }
        with mock.patch.dict(os.environ, env_dict, clear=True):
            env, region, proj, enable_components = verify_xmc._resolve_config()
            self.assertEqual(env, "dev")
            self.assertEqual(region, "us-central1")
            self.assertEqual(proj, "my-project")
            self.assertTrue(enable_components)

    def test_resolve_config_from_yaml(self):
        yaml_content = """
vars:
  environment: staging
  region: us-east1
  project_id: yaml-proj-123
"""
        mock_path = mock.MagicMock(spec=pathlib.Path)
        mock_path.is_file.return_value = True
        mock_path.read_text.return_value = yaml_content

        with mock.patch.dict(os.environ, {}, clear=True):
            env, region, proj, enable_components = verify_xmc._resolve_config(config_path=mock_path)
            self.assertEqual(env, "staging")
            self.assertEqual(region, "us-east1")
            self.assertEqual(proj, "yaml-proj-123")
            self.assertFalse(enable_components)

    def test_resolve_config_from_yaml_with_enable_components(self):
        yaml_content = """
vars:
  environment: staging
  region: us-east1
  project_id: yaml-proj-123
  enable_components: true
"""
        mock_path = mock.MagicMock(spec=pathlib.Path)
        mock_path.is_file.return_value = True
        mock_path.read_text.return_value = yaml_content

        with mock.patch.dict(os.environ, {}, clear=True):
            env, region, proj, enable_components = verify_xmc._resolve_config(config_path=mock_path)
            self.assertEqual(env, "staging")
            self.assertEqual(region, "us-east1")
            self.assertEqual(proj, "yaml-proj-123")
            self.assertTrue(enable_components)

    def test_resolve_config_env_overrides_yaml(self):
        yaml_content = """
vars:
  environment: staging
  region: us-east1
  project_id: yaml-proj-123
  enable_components: true
"""
        mock_path = mock.MagicMock(spec=pathlib.Path)
        mock_path.is_file.return_value = True
        mock_path.read_text.return_value = yaml_content

        with mock.patch.dict(os.environ, {"ENABLE_COMPONENTS": "false"}, clear=True):
            _, _, _, enable_components = verify_xmc._resolve_config(config_path=mock_path)
            self.assertFalse(enable_components)

    def test_resolve_config_missing(self):
        mock_path = mock.MagicMock(spec=pathlib.Path)
        mock_path.is_file.return_value = False

        with mock.patch.dict(os.environ, {}, clear=True):
            env, region, proj, enable_components = verify_xmc._resolve_config(config_path=mock_path)
            self.assertIsNone(env)
            self.assertIsNone(region)
            self.assertIsNone(proj)
            self.assertFalse(enable_components)

    def test_resolve_log_monitor_deployment_enabled_from_env(self):
        with mock.patch.dict(os.environ, {"LOG_MONITOR_DEPLOYMENT_ENABLED": "true"}, clear=True):
            self.assertTrue(verify_xmc._resolve_log_monitor_deployment_enabled())

        with mock.patch.dict(os.environ, {"LOG_MONITOR_DEPLOYMENT_ENABLED": "false"}, clear=True):
            self.assertFalse(verify_xmc._resolve_log_monitor_deployment_enabled())

    def test_resolve_log_monitor_deployment_enabled_from_yaml(self):
        mock_path = mock.MagicMock(spec=pathlib.Path)
        mock_path.is_file.return_value = True

        with mock.patch.dict(os.environ, {}, clear=True):
            mock_path.read_text.return_value = "vars:\n  log_monitor_deployment_enabled: true\n"
            self.assertTrue(
                verify_xmc._resolve_log_monitor_deployment_enabled(config_path=mock_path)
            )
            mock_path.read_text.return_value = "vars:\n  log_monitor_deployment_enabled: false\n"
            self.assertFalse(
                verify_xmc._resolve_log_monitor_deployment_enabled(config_path=mock_path)
            )
            mock_path.read_text.return_value = "vars:\n  environment: dev\n"
            self.assertTrue(
                verify_xmc._resolve_log_monitor_deployment_enabled(config_path=mock_path)
            )

    def test_resolve_log_monitor_deployment_enabled_default_true(self):
        mock_path = mock.MagicMock(spec=pathlib.Path)
        mock_path.is_file.return_value = False

        with mock.patch.dict(os.environ, {}, clear=True):
            self.assertTrue(
                verify_xmc._resolve_log_monitor_deployment_enabled(config_path=mock_path)
            )

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_k8s_resources(self, mock_run):
        mock_run.return_value = (
            "NAME   READY   STATUS\npod-1   1/1     Running\npod-job 0/1     Completed\n"
        )
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_k8s_resources()

        self.assertTrue(res)
        self.assertEqual(mock_run.call_count, 5)
        output = captured.getvalue()
        self.assertIn("--- 1. Pod Status ---", output)
        self.assertIn("--- 5. cert-manager Certificates ---", output)
        self.assertIn("pod-1   1/1     Running", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_k8s_resources_unready_pod(self, mock_run):
        mock_run.return_value = "NAME   READY   STATUS\npod-1   0/1     Running\n"
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_k8s_resources()
        self.assertFalse(res)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_k8s_resources_crashloop_or_pending(self, mock_run):
        for bad_line in (
            "pod-1   0/1     CrashLoopBackOff",
            "pod-1   0/1     Pending",
        ):
            mock_run.return_value = f"NAME   READY   STATUS\n{bad_line}\n"
            captured = io.StringIO()
            with mock.patch.object(sys, "stdout", captured):
                self.assertFalse(verify_xmc._check_k8s_resources())

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_k8s_resources_empty_output(self, mock_run):
        mock_run.return_value = ""
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            self.assertFalse(verify_xmc._check_k8s_resources())

    def test_are_pods_healthy_ignores_terminating_evicted_and_warnings(self):
        pods_out = (
            "NAME      READY   STATUS        RESTARTS   AGE\n"
            "Warning: some kubectl warning line\n"
            "shortline\n"
            "pod-1     1/1     Running       0          5m\n"
            "pod-old   1/1     Terminating   0          10m\n"
            "pod-ev    0/1     Evicted       0          12m\n"
            "pod-job   0/1     Succeeded     0          2m\n"
        )
        self.assertTrue(verify_xmc._are_pods_healthy(pods_out))

    def test_are_pods_healthy_only_non_pod_lines_returns_false(self):
        pods_out = "NAME      READY   STATUS\nWarning: no real pods here\nshort\n"
        self.assertFalse(verify_xmc._are_pods_healthy(pods_out))

    def test_are_pods_healthy_only_inactive_pods_returns_false(self):
        pods_out = (
            "NAME      READY   STATUS        RESTARTS   AGE\n"
            "pod-job   0/1     Completed     0          5m\n"
            "pod-old   1/1     Terminating   0          10m\n"
            "pod-ev    0/1     Evicted       0          12m\n"
        )
        self.assertFalse(verify_xmc._are_pods_healthy(pods_out))

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_connectivity(self, mock_run):
        def run_cmd_side_effect(cmd):
            cmd_str = " ".join(cmd)
            if "gateway" in cmd_str:
                return "35.240.10.20"
            if "app=actuator" in cmd_str:
                return "actuator-pod-abc"
            return ""

        mock_run.side_effect = run_cmd_side_effect
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_connectivity()

        self.assertTrue(res)
        output = captured.getvalue()
        self.assertIn("Gateway IP Address: 35.240.10.20", output)
        self.assertIn("Run: kubectl port-forward -n xmc actuator-pod-abc 8080:8080", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_connectivity_missing_ip_or_pod(self, mock_run):
        mock_run.return_value = ""
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            self.assertFalse(verify_xmc._check_connectivity())

    def test_check_log_monitor_missing_env(self):
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_log_monitor(None, "us-central1", "proj")

        self.assertFalse(res)
        output = captured.getvalue()
        self.assertIn("Skipping Log Monitor check", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_log_monitor_deployment_enabled_skips_cloud_run(self, mock_run):
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_log_monitor(
                "dev", "us-central1", "proj", log_monitor_deployment_enabled=True
            )

        self.assertTrue(res)
        mock_run.assert_not_called()
        output = captured.getvalue()
        self.assertIn(
            "Skipping Cloud Run Log Monitor check: log_monitor_deployment_enabled is true",
            output,
        )

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_log_monitor_ready(self, mock_run):
        mock_run.return_value = "True\n"
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_log_monitor("dev", "us-central1", "proj")

        self.assertTrue(res)
        output = captured.getvalue()
        self.assertIn("Service Status (Ready): True", output)
        self.assertIn("Log Monitor Cloud Run service is running and Ready.", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_log_monitor_not_ready(self, mock_run):
        mock_run.return_value = "False\n"
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_log_monitor("dev", "us-central1", "proj")

        self.assertFalse(res)
        output = captured.getvalue()
        self.assertIn("Log Monitor Cloud Run service is NOT Ready.", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_log_monitor_not_found(self, mock_run):
        mock_run.return_value = ""
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_log_monitor("dev", "us-central1", "proj")

        self.assertFalse(res)
        output = captured.getvalue()
        self.assertIn("not found or status is unavailable", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_components_disabled(self, mock_run):
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_components(enable_components=False)

        self.assertTrue(res)
        mock_run.assert_not_called()
        output = captured.getvalue()
        self.assertIn("--- 8. Component Controllers Status ---", output)
        self.assertIn("Skipping Component Controllers check: enable_components is false.", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_components_enabled_success(self, mock_run):
        mock_run.side_effect = [
            "customresourcedefinition.apiextensions.k8s.io/components.xmc.dev\n",
            '{"items": [{"status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}]}}]}',
            (
                '{"items": ['
                '{"metadata": {"name": "comp-a"}, "spec": {"enabled": true}, '
                '"status": {"conditions": [{"type": "Ready", "status": "True"}]}},'
                '{"metadata": {"name": "comp-b"}, "spec": {"enabled": false}, '
                '"status": {"conditions": [{"type": "Ready", "status": "False", "reason": "Disabled"}]}}'
                "]}"
            ),
        ]
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            res = verify_xmc._check_components(enable_components=True)

        self.assertTrue(res)
        self.assertEqual(mock_run.call_count, 3)
        output = captured.getvalue()
        self.assertIn("Component CRD found", output)
        self.assertIn("xmc-bridge pod is Running and Ready", output)
        self.assertIn("All 2 Component CRs verified in expected state", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_components_failure_cases(self, mock_run):
        # 1. Missing CRD
        mock_run.return_value = ""
        with mock.patch.object(sys, "stdout", io.StringIO()):
            self.assertFalse(verify_xmc._check_components(enable_components=True))

        # 2. Pod not ready
        mock_run.side_effect = [
            "customresourcedefinition.apiextensions.k8s.io/components.xmc.dev\n",
            '{"items": [{"status": {"phase": "Pending"}}]}',
        ]
        with mock.patch.object(sys, "stdout", io.StringIO()):
            self.assertFalse(verify_xmc._check_components(enable_components=True))

        # 3. Enabled CR not ready
        mock_run.side_effect = [
            "customresourcedefinition.apiextensions.k8s.io/components.xmc.dev\n",
            '{"items": [{"status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}]}}]}',
            '{"items": [{"metadata": {"name": "comp-a"}, "spec": {"enabled": true}, "status": {"conditions": [{"type": "Ready", "status": "False"}]}}]}',
        ]
        with mock.patch.object(sys, "stdout", io.StringIO()):
            self.assertFalse(verify_xmc._check_components(enable_components=True))

        # 4. Unreconciled disabled CR (empty status: {}) fails
        mock_run.side_effect = [
            "customresourcedefinition.apiextensions.k8s.io/components.xmc.dev\n",
            '{"items": [{"status": {"phase": "Running", "conditions": [{"type": "Ready", "status": "True"}]}}]}',
            '{"items": [{"metadata": {"name": "comp-b"}, "spec": {"enabled": false}, "status": {}}]}',
        ]
        with mock.patch.object(sys, "stdout", io.StringIO()):
            self.assertFalse(verify_xmc._check_components(enable_components=True))

    @mock.patch.object(verify_xmc, "_check_components", return_value=True)
    @mock.patch.object(verify_xmc, "_check_log_monitor", return_value=True)
    @mock.patch.object(verify_xmc, "_check_connectivity", return_value=True)
    @mock.patch.object(verify_xmc, "_check_k8s_resources", return_value=True)
    @mock.patch.object(verify_xmc, "_resolve_log_monitor_deployment_enabled")
    @mock.patch.object(verify_xmc, "_resolve_config")
    @mock.patch.object(verify_xmc, "ensure_venv")
    def test_main(
        self,
        mock_ensure_venv,
        mock_resolve_config,
        mock_resolve_lm_enabled,
        mock_k8s,
        mock_connectivity,
        mock_log_monitor,
        mock_components,
    ):
        mock_resolve_config.return_value = ("dev", "us-central1", "test-proj", True)
        mock_resolve_lm_enabled.return_value = False
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            exit_code = verify_xmc.main()

        self.assertEqual(exit_code, 0)
        mock_ensure_venv.assert_called_once()
        mock_resolve_config.assert_called_once()
        mock_resolve_lm_enabled.assert_called_once()
        mock_k8s.assert_called_once()
        mock_connectivity.assert_called_once()
        mock_log_monitor.assert_called_once_with("dev", "us-central1", "test-proj", False)
        mock_components.assert_called_once_with(True)

    @mock.patch.object(verify_xmc, "_check_components", return_value=True)
    @mock.patch.object(verify_xmc, "_check_log_monitor", return_value=True)
    @mock.patch.object(verify_xmc, "_check_connectivity", return_value=True)
    @mock.patch.object(verify_xmc, "_check_k8s_resources", return_value=False)
    @mock.patch.object(verify_xmc, "_resolve_log_monitor_deployment_enabled", return_value=False)
    @mock.patch.object(
        verify_xmc, "_resolve_config", return_value=("dev", "us-central1", "test-proj", False)
    )
    @mock.patch.object(verify_xmc, "ensure_venv")
    def test_main_returns_1_on_check_failure(
        self,
        mock_ensure_venv,
        mock_resolve_config,
        mock_resolve_lm_enabled,
        mock_k8s,
        mock_connectivity,
        mock_log_monitor,
        mock_components,
    ):
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            exit_code = verify_xmc.main()
        self.assertEqual(exit_code, 1)


if __name__ == "__main__":
    unittest.main()
