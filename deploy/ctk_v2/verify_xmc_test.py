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

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_k8s_resources(self, mock_run):
        mock_run.return_value = "NAME   READY   STATUS\npod-1   1/1     Running\n"
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_k8s_resources()

        self.assertEqual(mock_run.call_count, 5)
        output = captured.getvalue()
        self.assertIn("--- 1. Pod Status ---", output)
        self.assertIn("--- 5. cert-manager Certificates ---", output)
        self.assertIn("pod-1   1/1     Running", output)

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
            verify_xmc._check_connectivity()

        output = captured.getvalue()
        self.assertIn("Gateway IP Address: 35.240.10.20", output)
        self.assertIn("Run: kubectl port-forward -n xmc actuator-pod-abc 8080:8080", output)

    def test_check_log_monitor_missing_env(self):
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_log_monitor(None, "us-central1", "proj")

        output = captured.getvalue()
        self.assertIn("Skipping Log Monitor check", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_log_monitor_ready(self, mock_run):
        mock_run.return_value = "True\n"
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_log_monitor("dev", "us-central1", "proj")

        output = captured.getvalue()
        self.assertIn("Service Status (Ready): True", output)
        self.assertIn("Log Monitor Cloud Run service is running and Ready.", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_log_monitor_not_ready(self, mock_run):
        mock_run.return_value = "False\n"
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_log_monitor("dev", "us-central1", "proj")

        output = captured.getvalue()
        self.assertIn("Log Monitor Cloud Run service is NOT Ready.", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_log_monitor_not_found(self, mock_run):
        mock_run.return_value = ""
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_log_monitor("dev", "us-central1", "proj")

        output = captured.getvalue()
        self.assertIn("not found or status is unavailable", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_components_disabled(self, mock_run):
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_components(enable_components=False)

        mock_run.assert_not_called()
        output = captured.getvalue()
        self.assertIn("--- 8. Component Controllers Status ---", output)
        self.assertIn("Skipping Component Controllers check: enable_components is false.", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_components_enabled(self, mock_run):
        mock_run.return_value = "components-controller-abc   1/1     Running\n"
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_components(enable_components=True)

        mock_run.assert_called_once_with(
            ["kubectl", "get", "pods", "-n", "xmc", "-l", "app=components-controller"]
        )
        output = captured.getvalue()
        self.assertIn("--- 8. Component Controllers Status ---", output)
        self.assertIn("components-controller-abc   1/1     Running", output)

    @mock.patch.object(verify_xmc, "_run_command")
    def test_check_components_enabled_empty_output(self, mock_run):
        mock_run.return_value = ""
        captured = io.StringIO()
        with mock.patch.object(sys, "stdout", captured):
            verify_xmc._check_components(enable_components=True)

        mock_run.assert_called_once_with(
            ["kubectl", "get", "pods", "-n", "xmc", "-l", "app=components-controller"]
        )
        output = captured.getvalue()
        self.assertIn("--- 8. Component Controllers Status ---", output)
        self.assertIn("❌ No component controller pods found or failed to query cluster.", output)

    @mock.patch.object(verify_xmc, "ensure_venv")
    @mock.patch.object(verify_xmc, "_resolve_config")
    @mock.patch.object(verify_xmc, "_check_k8s_resources")
    @mock.patch.object(verify_xmc, "_check_connectivity")
    @mock.patch.object(verify_xmc, "_check_log_monitor")
    @mock.patch.object(verify_xmc, "_check_components")
    def test_main(
        self,
        mock_check_comp,
        mock_check_log,
        mock_check_conn,
        mock_check_k8s,
        mock_resolve,
        mock_ensure_venv,
    ):
        mock_resolve.return_value = ("dev", "us-central1", "test-proj", True)
        res = verify_xmc.main()
        self.assertEqual(res, 0)
        mock_ensure_venv.assert_called_once()
        mock_resolve.assert_called_once()
        mock_check_k8s.assert_called_once()
        mock_check_conn.assert_called_once()
        mock_check_log.assert_called_once_with("dev", "us-central1", "test-proj")
        mock_check_comp.assert_called_once_with(True)


if __name__ == "__main__":
    unittest.main()
