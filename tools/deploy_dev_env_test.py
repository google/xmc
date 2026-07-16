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

"""Unit tests for deploy_dev_env.py."""

import argparse
import io
import json
import os
import pathlib
import subprocess
import unittest
from unittest.mock import MagicMock, mock_open, patch, call, ANY

import sys
# Import the module under test
sys.path.insert(0, str(pathlib.Path(__file__).parent.resolve()))
import deploy_dev_env


class TestDeployDevEnv(unittest.TestCase):
  """Unit tests for deploy_dev_env.py."""

  def setUp(self):
    self.valid_config_yaml = """
deployment_name: xmc-dev
project_id: my-project
environment: dev
region: us-central1
domain_name: example.com
xmc_acr_target: us-central1-docker.pkg.dev/my-project/xmc
"""
    self.valid_config_dict = {
        "deployment_name": "xmc-dev",
        "project_id": "my-project",
        "environment": "dev",
        "region": "us-central1",
        "domain_name": "example.com",
        "xmc_acr_target": "us-central1-docker.pkg.dev/my-project/xmc",
    }
    self.dns_project = "dns-project"
    self.dns_zone = "dns-zone"

  @patch("sys.stderr", new_callable=io.StringIO)
  def test_parse_args_success(self, mock_stderr):
    args = [
        "--config",
        "config.yaml",
        "--dns-project",
        "p",
        "--dns-zone",
        "z",
        "--start-from",
        "compile",
    ]
    parsed = deploy_dev_env.parse_args(args)
    self.assertEqual(parsed.config, "config.yaml")
    self.assertEqual(parsed.dns_project, "p")
    self.assertEqual(parsed.dns_zone, "z")
    self.assertEqual(parsed.start_from, "compile")
    self.assertIsNone(parsed.only)

  def test_parse_args_mutual_exclusion(self):
    args = [
        "--config",
        "config.yaml",
        "--dns-project",
        "p",
        "--dns-zone",
        "z",
        "--start-from",
        "compile",
        "--only",
        "primary",
    ]
    # argparse raises SystemExit on error
    with self.assertRaises(SystemExit):
      deploy_dev_env.parse_args(args)

  @patch("pathlib.Path.exists", return_value=True)
  def test_load_and_validate_config_success(self, mock_exists):
    mo = mock_open(read_data=self.valid_config_yaml)
    with patch("builtins.open", mo):
      config = deploy_dev_env.load_and_validate_config("dummy.yaml")
      self.assertEqual(config, self.valid_config_dict)

  @patch("pathlib.Path.exists", return_value=True)
  def test_load_and_validate_config_missing_key(self, mock_exists):
    invalid_yaml = """
deployment_name: xmc-dev
project_id: my-project
# environment is missing
region: us-central1
domain_name: example.com
xmc_acr_target: us-central1-docker.pkg.dev/my-project/xmc
"""
    mo = mock_open(read_data=invalid_yaml)
    with patch("builtins.open", mo):
      with self.assertRaises(SystemExit):
        deploy_dev_env.load_and_validate_config("dummy.yaml")

  @patch("pathlib.Path.exists", return_value=True)
  def test_load_and_validate_config_invalid_value(self, mock_exists):
    invalid_yaml = """
deployment_name: xmc-dev! # Invalid character
project_id: my-project
environment: dev
region: us-central1
domain_name: example.com
xmc_acr_target: us-central1-docker.pkg.dev/my-project/xmc
"""
    mo = mock_open(read_data=invalid_yaml)
    with patch("builtins.open", mo):
      with self.assertRaises(SystemExit):
        deploy_dev_env.load_and_validate_config("dummy.yaml")

  @patch("pathlib.Path.exists", return_value=True)
  def test_load_config_success(self, mock_exists):
    yaml_content = """
key1: value1
key2: "value2#with#hash"
key3: 'value3#with#hash'
key4: value4 # trailing comment
key5: "value5" # trailing comment with quotes
# comment line
  key6: value6 # indented key
"""
    mo = mock_open(read_data=yaml_content)
    with patch("builtins.open", mo):
      config = deploy_dev_env._load_config("dummy.yaml")
      expected = {
          "key1": "value1",
          "key2": "value2#with#hash",
          "key3": "value3#with#hash",
          "key4": "value4",
          "key5": "value5",
          "key6": "value6",
      }
      self.assertEqual(config, expected)

  def test_validate_config_success(self):
    config = {
        "deployment_name": "xmc-dev",
        "project_id": "my-project",
        "environment": "dev",
        "region": "us-central1",
        "domain_name": "example.com",
        "xmc_acr_target": "us-central1-docker.pkg.dev/my-project/xmc",
        "extra_key": "ignored",
    }
    validated = deploy_dev_env._validate_config(config, "dummy.yaml")
    self.assertEqual(validated, self.valid_config_dict)

  def test_validate_config_missing_key(self):
    config = {
        "deployment_name": "xmc-dev",
        # project_id is missing
        "environment": "dev",
        "region": "us-central1",
        "domain_name": "example.com",
        "xmc_acr_target": "us-central1-docker.pkg.dev/my-project/xmc",
    }
    with self.assertRaises(SystemExit):
      deploy_dev_env._validate_config(config, "dummy.yaml")

  @patch("shutil.which", return_value=True)
  @patch("pathlib.Path.is_file", return_value=True)
  @patch("os.access", return_value=True)
  @patch("deploy_dev_env.run_command")
  def test_run_checks_success(
      self, mock_run, mock_access, mock_isfile, mock_which
  ):
    # Mock terraform version output
    tf_version_mock = MagicMock()
    tf_version_mock.stdout = '{"terraform_version": "1.13.0"}'
    # Mock active project output
    active_project_mock = MagicMock()
    active_project_mock.stdout = "my-project\n"
    # Mock DNS check (returns success)
    dns_check_mock = MagicMock()

    mock_run.side_effect = [
        tf_version_mock,
        active_project_mock,
        dns_check_mock,
    ]

    # Should not raise any exception/SystemExit
    deploy_dev_env.run_checks(
        self.valid_config_dict, self.dns_project, self.dns_zone
    )

    # Verify calls
    mock_which.assert_any_call("terraform")
    mock_run.assert_any_call(
        ["terraform", "version", "-json"], capture_output=True
    )
    mock_run.assert_any_call(
        ["gcloud", "config", "get-value", "project"], capture_output=True
    )
    mock_run.assert_any_call(
        [
            "gcloud",
            "dns",
            "managed-zones",
            "describe",
            self.dns_zone,
            f"--project={self.dns_project}",
        ],
        capture_output=True,
    )

  @patch("shutil.which", return_value=False)
  def test_run_checks_missing_binary(self, mock_which):
    with self.assertRaises(SystemExit):
      deploy_dev_env.run_checks(
          self.valid_config_dict, self.dns_project, self.dns_zone
      )

  @patch("shutil.which", return_value=True)
  @patch("pathlib.Path.is_file", return_value=True)
  @patch("os.access", return_value=True)
  @patch("deploy_dev_env.run_command")
  def test_run_checks_tf_version_low(
      self, mock_run, mock_access, mock_isfile, mock_which
  ):
    tf_version_mock = MagicMock()
    tf_version_mock.stdout = '{"terraform_version": "1.11.0"}'
    mock_run.return_value = tf_version_mock

    with self.assertRaises(SystemExit):
      deploy_dev_env.run_checks(
          self.valid_config_dict, self.dns_project, self.dns_zone
      )

  @patch("shutil.which", return_value=True)
  @patch("pathlib.Path.is_file", return_value=True)
  @patch("os.access", return_value=True)
  @patch("deploy_dev_env.run_command")
  def test_run_checks_project_mismatch(
      self, mock_run, mock_access, mock_isfile, mock_which
  ):
    tf_version_mock = MagicMock()
    tf_version_mock.stdout = '{"terraform_version": "1.13.0"}'
    active_project_mock = MagicMock()
    active_project_mock.stdout = "different-project\n"

    mock_run.side_effect = [tf_version_mock, active_project_mock]

    with self.assertRaises(SystemExit):
      deploy_dev_env.run_checks(
          self.valid_config_dict, self.dns_project, self.dns_zone
      )

  @patch("shutil.which", return_value=True)
  @patch("pathlib.Path.is_file", return_value=True)
  @patch("os.access", return_value=True)
  def test_verify_binaries_success(self, mock_access, mock_is_file, mock_which):
    # Should not raise
    deploy_dev_env._verify_binaries()

  @patch("shutil.which", side_effect=lambda bin: bin != "terraform")
  def test_verify_binaries_missing_binary(self, mock_which):
    with self.assertRaises(SystemExit):
      deploy_dev_env._verify_binaries()

  @patch("shutil.which", return_value=True)
  @patch("pathlib.Path.is_file", return_value=False)
  def test_verify_binaries_missing_gcluster(self, mock_is_file, mock_which):
    with self.assertRaises(SystemExit):
      deploy_dev_env._verify_binaries()

  @patch("deploy_dev_env.run_command")
  def test_check_terraform_version_success(self, mock_run):
    tf_version_mock = MagicMock()
    tf_version_mock.stdout = '{"terraform_version": "1.12.2"}'
    mock_run.return_value = tf_version_mock
    # Should not raise
    deploy_dev_env._check_terraform_version()

  @patch("deploy_dev_env.run_command")
  def test_check_terraform_version_too_low(self, mock_run):
    tf_version_mock = MagicMock()
    tf_version_mock.stdout = '{"terraform_version": "1.12.1"}'
    mock_run.return_value = tf_version_mock
    with self.assertRaises(SystemExit):
      deploy_dev_env._check_terraform_version()

  @patch("deploy_dev_env.run_command")
  def test_check_gcp_project_alignment_success(self, mock_run):
    active_project_mock = MagicMock()
    active_project_mock.stdout = "my-project\n"
    mock_run.return_value = active_project_mock
    # Should not raise
    deploy_dev_env._check_gcp_project_alignment("my-project")

  @patch("deploy_dev_env.run_command")
  def test_check_gcp_project_alignment_mismatch(self, mock_run):
    active_project_mock = MagicMock()
    active_project_mock.stdout = "different-project\n"
    mock_run.return_value = active_project_mock
    with self.assertRaises(SystemExit):
      deploy_dev_env._check_gcp_project_alignment("my-project")

  @patch("deploy_dev_env.run_command")
  def test_check_dns_access_success(self, mock_run):
    # Should not raise
    deploy_dev_env._check_dns_access(self.dns_project, self.dns_zone)
    mock_run.assert_called_once_with(
        [
            "gcloud",
            "dns",
            "managed-zones",
            "describe",
            self.dns_zone,
            f"--project={self.dns_project}",
        ],
        capture_output=True,
    )

  @patch(
      "deploy_dev_env.run_command",
      side_effect=subprocess.CalledProcessError(1, "cmd"),
  )
  def test_check_dns_access_failure(self, mock_run):
    with self.assertRaises(SystemExit):
      deploy_dev_env._check_dns_access(self.dns_project, self.dns_zone)

  @patch("deploy_dev_env.run_command")
  def test_dns_transaction_abort_success(self, mock_run):
    deploy_dev_env._dns_transaction_abort(self.dns_project, self.dns_zone)
    mock_run.assert_called_once_with(
        [
            "gcloud",
            "dns",
            "record-sets",
            "transaction",
            "abort",
            f"--zone={self.dns_zone}",
            f"--project={self.dns_project}",
        ],
        capture_output=True,
        log_on_error=False,
    )

  @patch(
      "deploy_dev_env.run_command",
      side_effect=subprocess.CalledProcessError(1, "cmd"),
  )
  def test_dns_transaction_abort_ignores_failure(self, mock_run):
    # Should not raise
    deploy_dev_env._dns_transaction_abort(self.dns_project, self.dns_zone)

  @patch("deploy_dev_env.run_command")
  def test_dns_transaction_start(self, mock_run):
    deploy_dev_env._dns_transaction_start(self.dns_project, self.dns_zone)
    mock_run.assert_called_once_with([
        "gcloud",
        "dns",
        "record-sets",
        "transaction",
        "start",
        f"--zone={self.dns_zone}",
        f"--project={self.dns_project}",
    ])

  @patch("deploy_dev_env.run_command")
  def test_dns_transaction_execute(self, mock_run):
    deploy_dev_env._dns_transaction_execute(self.dns_project, self.dns_zone)
    mock_run.assert_called_once_with([
        "gcloud",
        "dns",
        "record-sets",
        "transaction",
        "execute",
        f"--zone={self.dns_zone}",
        f"--project={self.dns_project}",
    ])

  @patch("deploy_dev_env.run_command")
  def test_dns_get_existing_ip_found(self, mock_run):
    mock_res = MagicMock()
    mock_res.stdout = "1.2.3.4\n"
    mock_run.return_value = mock_res
    ip = deploy_dev_env._dns_get_existing_ip(
        "dev.example.com.", self.dns_project, self.dns_zone
    )
    self.assertEqual(ip, "1.2.3.4")

  @patch("deploy_dev_env.run_command")
  def test_dns_get_existing_ip_not_found(self, mock_run):
    mock_res = MagicMock()
    mock_res.stdout = ""
    mock_run.return_value = mock_res
    ip = deploy_dev_env._dns_get_existing_ip(
        "dev.example.com.", self.dns_project, self.dns_zone
    )
    self.assertEqual(ip, "")

  @patch("deploy_dev_env.run_command")
  def test_dns_add_record(self, mock_run):
    deploy_dev_env._dns_add_record(
        "dev.example.com.", "1.2.3.4", self.dns_project, self.dns_zone
    )
    mock_run.assert_called_once_with([
        "gcloud",
        "dns",
        "record-sets",
        "transaction",
        "add",
        "1.2.3.4",
        "--name=dev.example.com.",
        "--ttl=300",
        "--type=A",
        f"--zone={self.dns_zone}",
        f"--project={self.dns_project}",
    ])

  @patch("deploy_dev_env.run_command")
  def test_apply_dns_records_all_exist_correct(self, mock_run):
    # Mock DNS list calls to return the correct IP
    list_mock = MagicMock()
    list_mock.stdout = "1.2.3.4\n"

    # gcloud dns record-sets transaction abort (might fail/pass, we mock
    # success)
    abort_mock = MagicMock()
    start_mock = MagicMock()

    mock_run.side_effect = [
        abort_mock,  # initial abort
        start_mock,  # transaction start
        list_mock,  # list app
        list_mock,  # list api
        list_mock,  # list grpc
        abort_mock,  # final abort (since no changes)
    ]

    deploy_dev_env.apply_dns_records(
        "1.2.3.4", self.valid_config_dict, self.dns_project, self.dns_zone
    )

    # Verify no 'transaction add' or 'transaction execute' was called
    for call_args in mock_run.call_args_list:
      args = call_args[0][0]
      self.assertNotIn("add", args)
      self.assertNotIn("execute", args)

  @patch("deploy_dev_env.run_command")
  def test_apply_dns_records_conflict(self, mock_run):
    # Mock DNS list to return a different IP for one of them
    list_mock_correct = MagicMock()
    list_mock_correct.stdout = "1.2.3.4\n"
    list_mock_diff = MagicMock()
    list_mock_diff.stdout = "5.6.7.8\n"

    mock_run.side_effect = [
        MagicMock(),  # initial abort
        MagicMock(),  # transaction start
        list_mock_correct,  # list app (correct)
        list_mock_diff,  # list api (different!)
        MagicMock(),  # abort (due to conflict)
    ]

    with self.assertRaises(SystemExit):
      deploy_dev_env.apply_dns_records(
          "1.2.3.4", self.valid_config_dict, self.dns_project, self.dns_zone
      )

    # Verify transaction was aborted
    mock_run.assert_any_call(
        [
            "gcloud",
            "dns",
            "record-sets",
            "transaction",
            "abort",
            f"--zone={self.dns_zone}",
            f"--project={self.dns_project}",
        ],
        capture_output=True,
        log_on_error=False,
    )

  @patch("deploy_dev_env.run_command")
  def test_apply_dns_records_create_all(self, mock_run):
    # Mock DNS list to return empty (not found)
    list_mock_empty = MagicMock()
    list_mock_empty.stdout = ""

    # We expect interleaved calls: abort, start, list, add, list, add, list,
    # add, execute
    mock_run.side_effect = [
        MagicMock(),  # initial abort
        MagicMock(),  # transaction start
        list_mock_empty,  # list app
        MagicMock(),  # add app
        list_mock_empty,  # list api
        MagicMock(),  # add api
        list_mock_empty,  # list grpc
        MagicMock(),  # add grpc
        MagicMock(),  # execute
    ]

    deploy_dev_env.apply_dns_records(
        "1.2.3.4", self.valid_config_dict, self.dns_project, self.dns_zone
    )

    # Verify add calls
    mock_run.assert_any_call([
        "gcloud",
        "dns",
        "record-sets",
        "transaction",
        "add",
        "1.2.3.4",
        "--name=dev.example.com.",
        "--ttl=300",
        "--type=A",
        f"--zone={self.dns_zone}",
        f"--project={self.dns_project}",
    ])
    # Verify execute call
    mock_run.assert_any_call([
        "gcloud",
        "dns",
        "record-sets",
        "transaction",
        "execute",
        f"--zone={self.dns_zone}",
        f"--project={self.dns_project}",
    ])

  @patch("deploy_dev_env.load_and_validate_config")
  @patch("deploy_dev_env.run_checks")
  @patch("deploy_dev_env.step_check")
  @patch("deploy_dev_env.step_compile")
  @patch("deploy_dev_env.step_images")
  @patch("deploy_dev_env.step_apis")
  @patch("deploy_dev_env.step_primary")
  @patch("deploy_dev_env.step_compute")
  @patch("deploy_dev_env.step_gateway_api")
  @patch("deploy_dev_env.step_prereqs")
  @patch("deploy_dev_env.step_app")
  @patch("deploy_dev_env.step_verify")
  def test_main_flow_all_steps(
      self,
      mock_verify,
      mock_app,
      mock_prereqs,
      mock_gw_api,
      mock_compute,
      mock_primary,
      mock_apis,
      mock_images,
      mock_compile,
      mock_check,
      mock_run_checks,
      mock_load_config,
  ):
    mock_load_config.return_value = self.valid_config_dict

    # Run with no special step args (should run all)
    deploy_dev_env.main([
        "--config",
        "config.yaml",
        "--dns-project",
        "p",
        "--dns-zone",
        "z",
    ])

    mock_run_checks.assert_called_once()
    mock_check.assert_called_once()
    mock_compile.assert_called_once()
    mock_images.assert_called_once()
    mock_apis.assert_called_once()
    mock_primary.assert_called_once()
    mock_compute.assert_called_once()
    mock_gw_api.assert_called_once()
    mock_prereqs.assert_called_once()
    mock_app.assert_called_once()
    mock_verify.assert_called_once()

  @patch("deploy_dev_env.load_and_validate_config")
  @patch("deploy_dev_env.run_checks")
  @patch("deploy_dev_env.step_check")
  @patch("deploy_dev_env.step_compile")
  @patch("deploy_dev_env.step_images")
  @patch("deploy_dev_env.step_apis")
  @patch("deploy_dev_env.step_primary")
  @patch("deploy_dev_env.step_compute")
  @patch("deploy_dev_env.step_gateway_api")
  @patch("deploy_dev_env.step_prereqs")
  @patch("deploy_dev_env.step_app")
  @patch("deploy_dev_env.step_verify")
  def test_main_flow_start_from(
      self,
      mock_verify,
      mock_app,
      mock_prereqs,
      mock_gw_api,
      mock_compute,
      mock_primary,
      mock_apis,
      mock_images,
      mock_compile,
      mock_check,
      mock_run_checks,
      mock_load_config,
  ):
    mock_load_config.return_value = self.valid_config_dict

    # Start from 'primary' (should skip check, compile, images, apis)
    deploy_dev_env.main([
        "--config",
        "config.yaml",
        "--dns-project",
        "p",
        "--dns-zone",
        "z",
        "--start-from",
        "primary",
    ])

    mock_run_checks.assert_called_once()
    mock_check.assert_not_called()
    mock_compile.assert_not_called()
    mock_images.assert_not_called()
    mock_apis.assert_not_called()

    mock_primary.assert_called_once()
    mock_compute.assert_called_once()
    mock_gw_api.assert_called_once()
    mock_prereqs.assert_called_once()
    mock_app.assert_called_once()
    mock_verify.assert_called_once()

  @patch("deploy_dev_env.load_and_validate_config")
  @patch("deploy_dev_env.run_checks")
  @patch("deploy_dev_env.step_check")
  @patch("deploy_dev_env.step_compile")
  @patch("deploy_dev_env.step_images")
  @patch("deploy_dev_env.step_apis")
  @patch("deploy_dev_env.step_primary")
  @patch("deploy_dev_env.step_compute")
  @patch("deploy_dev_env.step_gateway_api")
  @patch("deploy_dev_env.step_prereqs")
  @patch("deploy_dev_env.step_app")
  @patch("deploy_dev_env.step_verify")
  def test_main_flow_only(
      self,
      mock_verify,
      mock_app,
      mock_prereqs,
      mock_gw_api,
      mock_compute,
      mock_primary,
      mock_apis,
      mock_images,
      mock_compile,
      mock_check,
      mock_run_checks,
      mock_load_config,
  ):
    mock_load_config.return_value = self.valid_config_dict

    # Run ONLY 'compute'
    deploy_dev_env.main([
        "--config",
        "config.yaml",
        "--dns-project",
        "p",
        "--dns-zone",
        "z",
        "--only",
        "compute",
    ])

    mock_run_checks.assert_called_once()
    mock_check.assert_not_called()
    mock_compile.assert_not_called()
    mock_images.assert_not_called()
    mock_apis.assert_not_called()
    mock_primary.assert_not_called()

    mock_compute.assert_called_once()

    mock_gw_api.assert_not_called()
    mock_prereqs.assert_not_called()
    mock_app.assert_not_called()
    mock_verify.assert_not_called()

  @patch("deploy_dev_env.load_and_validate_config")
  @patch("deploy_dev_env.run_checks")
  @patch("deploy_dev_env.step_check")
  @patch("deploy_dev_env.step_compile")
  @patch("sys.stdout", new_callable=io.StringIO)
  def test_main_error_handling_resume_suggest(
      self,
      mock_stdout,
      mock_compile,
      mock_check,
      mock_run_checks,
      mock_load_config,
  ):
    mock_load_config.return_value = self.valid_config_dict
    # Simulate failure in step_compile
    mock_compile.side_effect = Exception("Compile failed")

    with self.assertRaises(SystemExit) as cm:
      deploy_dev_env.main([
          "--config",
          "config.yaml",
          "--dns-project",
          "p",
          "--dns-zone",
          "z",
      ])

    self.assertEqual(cm.exception.code, 1)
    output = mock_stdout.getvalue()
    self.assertIn("You can resume from this step using:", output)
    self.assertIn(
        "python3 tools/deploy_dev_env.py --config config.yaml --dns-project p"
        " --dns-zone z --start-from compile",
        output,
    )

  # --- Individual Step Tests ---

  @patch("deploy_dev_env.run_command")
  def test_step_compile(self, mock_run):
    deploy_dev_env.step_compile(
        self.valid_config_dict,
        self.dns_project,
        self.dns_zone,
        "my-config.yaml",
    )
    mock_run.assert_called_once_with([
        './cluster-toolkit/gcluster',
        'create',
        'deploy/ctk_v2/xmanager-cloud.yaml',
        '-d',
        'my-config.yaml',
        '-o',
        'ctk-out',
        '-l',
        'WARNING',
        '-w',
        '--force',
    ])

  @patch("deploy_dev_env.run_command")
  @patch("deploy_dev_env.apply_dns_records")
  def test_step_primary(self, mock_apply_dns, mock_run):
    deploy_res = MagicMock()
    ip_res = MagicMock()
    ip_res.stdout = '10.0.0.1\n'

    mock_run.side_effect = [deploy_res, ip_res]

    deploy_dev_env.step_primary(
        self.valid_config_dict, self.dns_project, self.dns_zone
    )

    mock_run.assert_has_calls([
        call([
            './cluster-toolkit/gcluster',
            'deploy',
            'ctk-out/xmc-dev',
            '--only',
            'primary',
            '--auto-approve',
        ]),
        call([
            'gcloud',
            'compute',
            'addresses',
            'describe',
            'xmc-gateway-static-ip-dev',
            '--global',
            '--project=my-project',
            '--format=value(address)',
        ], capture_output=True),
    ])
    mock_apply_dns.assert_called_once_with(
        '10.0.0.1', self.valid_config_dict, self.dns_project, self.dns_zone
    )

  @patch("deploy_dev_env.run_command")
  def test_step_gateway_api_not_enabled(self, mock_run):
    describe_res = MagicMock()
    describe_res.stdout = 'false\n'
    update_res = MagicMock()

    mock_run.side_effect = [describe_res, update_res]

    deploy_dev_env.step_gateway_api(
        self.valid_config_dict, self.dns_project, self.dns_zone
    )

    mock_run.assert_has_calls([
        call([
            'gcloud',
            'container',
            'clusters',
            'describe',
            'xmc-xmc-dev-dev',
            '--region=us-central1',
            '--project=my-project',
            '--format=value(gatewayConfig.enabled)',
        ], capture_output=True),
        call([
            'gcloud',
            'container',
            'clusters',
            'update',
            'xmc-xmc-dev-dev',
            '--gateway-api=standard',
            '--region=us-central1',
            '--project=my-project',
        ]),
    ])

  @patch("deploy_dev_env.run_command")
  def test_step_gateway_api_already_enabled(self, mock_run):
    describe_res = MagicMock()
    describe_res.stdout = 'true\n'
    mock_run.return_value = describe_res

    deploy_dev_env.step_gateway_api(
        self.valid_config_dict, self.dns_project, self.dns_zone
    )

    mock_run.assert_called_once()
    self.assertIn('describe', mock_run.call_args[0][0])

  @patch("deploy_dev_env.run_command")
  def test_step_images(self, mock_run):
    auth_res = MagicMock()
    bazel_res = MagicMock()

    mock_run.side_effect = [auth_res] + [bazel_res] * 6

    deploy_dev_env.step_images(
        self.valid_config_dict, self.dns_project, self.dns_zone
    )

    self.assertEqual(mock_run.call_count, 7)
    mock_run.assert_any_call([
        'gcloud',
        'auth',
        'configure-docker',
        'us-central1-docker.pkg.dev',
        '--quiet',
    ])
    mock_run.assert_any_call(
        ['bazel', 'run', '//cmd/actuator:actuator_image_push'],
        env=ANY,
    )

  @patch("deploy_dev_env.run_command")
  @patch("time.sleep")
  def test_step_verify_success(self, mock_sleep, mock_run):
    cred_res = MagicMock()
    verify_res = MagicMock()
    wait_res = MagicMock()
    cert_res = MagicMock()
    cert_res.stdout = 'ACTIVE\n'

    mock_run.side_effect = [cred_res, verify_res, wait_res, cert_res]

    deploy_dev_env.step_verify(
        self.valid_config_dict, self.dns_project, self.dns_zone
    )

    self.assertEqual(mock_run.call_count, 4)
    mock_run.assert_any_call([
        'gcloud',
        'container',
        'clusters',
        'get-credentials',
        'xmc-xmc-dev-dev',
        '--region=us-central1',
        '--project=my-project',
    ])
    mock_run.assert_any_call(
        [sys.executable, './deploy/ctk_v2/verify_xmc.py'], env=ANY, log_on_error=False
    )
    mock_run.assert_any_call([
        'kubectl',
        'wait',
        '--for=condition=Programmed',
        'gateway/xmc-gateway',
        '-n',
        'xmc',
        '--timeout=300s',
    ])
    mock_run.assert_any_call([
        'gcloud',
        'certificate-manager',
        'certificates',
        'describe',
        'xmc-cert-dev',
        '--project=my-project',
        '--format=value(managed.state)',
    ], capture_output=True, log_on_error=False)

  @patch("deploy_dev_env.run_command")
  def test_wait_for_gateway_programmed_success(self, mock_run):
    deploy_dev_env._wait_for_gateway_programmed()
    mock_run.assert_called_once_with([
        "kubectl",
        "wait",
        "--for=condition=Programmed",
        "gateway/xmc-gateway",
        "-n",
        "xmc",
        "--timeout=300s",
    ])

  @patch(
      "deploy_dev_env.run_command",
      side_effect=subprocess.CalledProcessError(1, "cmd"),
  )
  def test_wait_for_gateway_programmed_failure(self, mock_run):
    with self.assertRaises(RuntimeError):
      deploy_dev_env._wait_for_gateway_programmed()

  @patch("deploy_dev_env.run_command")
  @patch("time.sleep")
  def test_wait_for_cert_active_success_first_attempt(
      self, mock_sleep, mock_run
  ):
    mock_res = MagicMock()
    mock_res.stdout = "ACTIVE\n"
    mock_run.return_value = mock_res
    deploy_dev_env._wait_for_cert_active("my-project", "my-cert")
    mock_run.assert_called_once()
    mock_sleep.assert_not_called()

  @patch("deploy_dev_env.run_command")
  @patch("time.sleep")
  def test_wait_for_cert_active_success_after_retries(
      self, mock_sleep, mock_run
  ):
    pending_res = MagicMock()
    pending_res.stdout = "PENDING\n"
    active_res = MagicMock()
    active_res.stdout = "ACTIVE\n"
    mock_run.side_effect = [pending_res, pending_res, active_res]
    deploy_dev_env._wait_for_cert_active("my-project", "my-cert")
    self.assertEqual(mock_run.call_count, 3)
    self.assertEqual(mock_sleep.call_count, 2)

  @patch("deploy_dev_env.run_command")
  @patch("time.sleep")
  def test_wait_for_cert_active_transient_error_retry(
      self, mock_sleep, mock_run
  ):
    # Simulate a NOT_FOUND error (transient) then success
    err = subprocess.CalledProcessError(1, "cmd")
    err.stderr = "Resource not found"
    active_res = MagicMock()
    active_res.stdout = "ACTIVE\n"
    mock_run.side_effect = [err, active_res]
    deploy_dev_env._wait_for_cert_active("my-project", "my-cert")
    self.assertEqual(mock_run.call_count, 2)
    mock_sleep.assert_called_once_with(10)

  @patch("deploy_dev_env.run_command")
  @patch("time.sleep")
  def test_wait_for_cert_active_fatal_error_propagation(
      self, mock_sleep, mock_run
  ):
    # Simulate a Permission Denied error (fatal)
    err = subprocess.CalledProcessError(1, "cmd")
    err.stderr = "Permission denied"
    mock_run.side_effect = [err]
    with self.assertRaises(subprocess.CalledProcessError):
      deploy_dev_env._wait_for_cert_active("my-project", "my-cert")
    mock_sleep.assert_not_called()

  @patch("deploy_dev_env.run_command")
  @patch("time.sleep")
  def test_wait_for_cert_active_timeout(self, mock_sleep, mock_run):
    pending_res = MagicMock()
    pending_res.stdout = "PENDING\n"
    mock_run.return_value = pending_res
    with self.assertRaises(RuntimeError):
      deploy_dev_env._wait_for_cert_active("my-project", "my-cert")
    self.assertEqual(mock_run.call_count, 90)
    self.assertEqual(mock_sleep.call_count, 90)


if __name__ == "__main__":
  unittest.main(argv=['first_arg_ignored', '-v'])
