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

import json
import os
import subprocess
import unittest
from unittest.mock import MagicMock, mock_open, patch

from cleanup_env import cleanup_env
from tool_utils import HandledError


class TestCleanupEnv(unittest.TestCase):

  def setUp(self):
    self.args = MagicMock()
    self.args.env = "my-dev"
    self.args.environment_name = None
    self.args.dns_zone = None
    self.args.dns_project = None
    self.args.skip_xmc_app = False
    self.args.skip = None
    self.args.dry_run = False
    self.args.force = False

    # Common mocks
    self.mock_exists = patch("os.path.exists").start()
    self.mock_run = patch("subprocess.run").start()
    self.mock_rmtree = patch("shutil.rmtree").start()
    
    # Default behavior for existence checks
    # We want to default to everything exists to test the main path,
    # and override in specific tests.
    self.mock_exists.side_effect = self.default_exists

    # Default behavior for subprocess.run (success)
    self.mock_run.return_value = subprocess.CompletedProcess(
        args=[], returncode=0, stdout="", stderr=""
    )

    self.addCleanup(patch.stopall)

  def default_exists(self, path):
    # Simulate that deployment dir, tfvars, and gcluster exist
    if "ctk-out/my-dev" in path:
      return True
    if "terraform.tfvars" in path:
      return True
    if "gcluster" in path:
      return True
    return False

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = false\n",
  )
  def test_cleanup_success_minimal(self, mock_file):
    # Minimal successful cleanup (no backups, no DNS, no kubeconfig matches)
    self.mock_run.side_effect = [
        # List backups (empty response)
        subprocess.CompletedProcess(
            args=[], returncode=0, stdout="[]", stderr=""
        ),
        # gcluster destroy
        subprocess.CompletedProcess(
            args=[], returncode=0, stdout="Destroyed", stderr=""
        ),
        # List kubeconfig contexts (empty)
        subprocess.CompletedProcess(
            args=[], returncode=0, stdout="", stderr=""
        ),
    ]

    res = cleanup_env(self.args)

    self.assertIn("cleaned up successfully", res)
    self.mock_rmtree.assert_called_once()
    
    # Check that gcluster destroy was called
    self.assertTrue(any("destroy" in call.args[0] for call in self.mock_run.call_args_list))

  def test_deployment_dir_not_found(self):
    self.mock_exists.side_effect = lambda path: False # Nothing exists
    
    with self.assertRaises(HandledError) as ctx:
      cleanup_env(self.args)
    
    self.assertIn("Deployment directory not found", ctx.exception.message)
    self.assertIn("No active cloud environment", ctx.exception.instruction)

  @patch("builtins.open", new_callable=mock_open, read_data="deletion_protection = false\n")
  def test_project_id_missing(self, mock_file):
    # tfvars exists but has no project_id
    with self.assertRaises(HandledError) as ctx:
      cleanup_env(self.args)
    
    self.assertIn("project_id not found", ctx.exception.message)

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = true\n",
  )
  def test_deletion_protection_enabled_no_force(self, mock_file):
    with self.assertRaises(HandledError) as ctx:
      cleanup_env(self.args)
    
    self.assertIn("Deletion protection is enabled", ctx.exception.message)
    self.assertIn("Disable deletion protection", ctx.exception.instruction)

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = true\n",
  )
  def test_deletion_protection_enabled_with_force(self, mock_file):
    self.args.force = True
    self.mock_run.side_effect = [
        # List backups (empty)
        subprocess.CompletedProcess(args=[], returncode=0, stdout="[]", stderr=""),
        # gcluster destroy
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # List kubeconfig
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
    ]
    
    res = cleanup_env(self.args)
    self.assertIn("cleaned up successfully", res)

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = false\n",
  )
  def test_spanner_backup_deletion(self, mock_file):
    # Simulate finding one backup and successfully deleting it
    backups_json = json.dumps([
        {"name": "projects/my-project/instances/xmc-my-dev/backups/my-backup-1"}
    ])
    
    self.mock_run.side_effect = [
        # List backups
        subprocess.CompletedProcess(args=[], returncode=0, stdout=backups_json, stderr=""),
        # Delete backup
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # gcluster destroy
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # List kubeconfig
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
    ]

    cleanup_env(self.args)

    # Verify delete backup command was run
    delete_call = self.mock_run.call_args_list[1]
    self.assertIn("delete", delete_call.args[0])
    self.assertIn("my-backup-1", delete_call.args[0])

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = false\n",
  )
  def test_spanner_backup_deletion_failure(self, mock_file):
    backups_json = json.dumps([
        {"name": "projects/my-project/instances/xmc-my-dev/backups/my-backup-1"}
    ])
    
    self.mock_run.side_effect = [
        # List backups
        subprocess.CompletedProcess(args=[], returncode=0, stdout=backups_json, stderr=""),
        # Delete backup (fails)
        subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="Permission denied"),
    ]

    with self.assertRaises(HandledError) as ctx:
      cleanup_env(self.args)
    
    self.assertIn("Failed to delete backup my-backup-1", ctx.exception.message)
    self.assertIn("Manual intervention required", ctx.exception.instruction)

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = false\n",
  )
  def test_gcluster_destroy_fails_xmc_app(self, mock_file):
    self.mock_run.side_effect = [
        # List backups (empty)
        subprocess.CompletedProcess(args=[], returncode=0, stdout="[]", stderr=""),
        # gcluster destroy (fails with xmc_app error)
        subprocess.CompletedProcess(
            args=[],
            returncode=1,
            stdout="",
            stderr="Error: resource xmc_app failed to delete: timeout",
        ),
    ]

    with self.assertRaises(HandledError) as ctx:
      cleanup_env(self.args)

    self.assertIn("gcluster destroy failed on xmc_app", ctx.exception.message)
    self.assertIn("Retry with --skip-xmc-app", ctx.exception.instruction)

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = false\n",
  )
  def test_gcluster_destroy_fails_generic(self, mock_file):
    self.mock_run.side_effect = [
        # List backups (empty)
        subprocess.CompletedProcess(args=[], returncode=0, stdout="[]", stderr=""),
        # gcluster destroy (fails with generic error)
        subprocess.CompletedProcess(
            args=[],
            returncode=1,
            stdout="",
            stderr="Error: some random terraform error",
        ),
    ]

    with self.assertRaises(HandledError) as ctx:
      cleanup_env(self.args)

    self.assertIn("gcluster destroy failed", ctx.exception.message)
    self.assertIn("force deleting the GKE cluster", ctx.exception.instruction)

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = false\n",
  )
  def test_dns_cleanup(self, mock_file):
    self.args.dns_zone = "my-zone"
    
    dns_records = json.dumps([
        {"name": "my-dev.xmc.google.com.", "type": "A"},
        {"name": "api.my-dev.xmc.google.com.", "type": "A"},
        {"name": "other-record.com.", "type": "A"},
    ])

    self.mock_run.side_effect = [
        # List backups (empty)
        subprocess.CompletedProcess(args=[], returncode=0, stdout="[]", stderr=""),
        # gcluster destroy
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # List DNS
        subprocess.CompletedProcess(args=[], returncode=0, stdout=dns_records, stderr=""),
        # Delete DNS 1
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # Delete DNS 2
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # List kubeconfig
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
    ]

    cleanup_env(self.args)

    # Verify DNS list was called
    dns_list_call = self.mock_run.call_args_list[2]
    self.assertIn("dns", dns_list_call.args[0])
    self.assertIn("record-sets", dns_list_call.args[0])
    self.assertIn("list", dns_list_call.args[0])

    # Verify DNS deletes were called
    dns_delete_1 = self.mock_run.call_args_list[3]
    self.assertIn("delete", dns_delete_1.args[0])
    self.assertIn("my-dev.xmc.google.com.", dns_delete_1.args[0])

    dns_delete_2 = self.mock_run.call_args_list[4]
    self.assertIn("delete", dns_delete_2.args[0])
    self.assertIn("api.my-dev.xmc.google.com.", dns_delete_2.args[0])

  @patch(
      "builtins.open",
      new_callable=mock_open,
      read_data="project_id = \"my-project\"\ndeletion_protection = false\n",
  )
  def test_kubeconfig_cleanup(self, mock_file):
    kube_contexts = "gke_my-project_us-central1_xmc-my-dev\nother-context\ngke_my-project_us-central1_xmc-other-env"

    self.mock_run.side_effect = [
        # List backups (empty)
        subprocess.CompletedProcess(args=[], returncode=0, stdout="[]", stderr=""),
        # gcluster destroy
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # List kubeconfig
        subprocess.CompletedProcess(args=[], returncode=0, stdout=kube_contexts, stderr=""),
        # Delete context
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
        # Delete cluster
        subprocess.CompletedProcess(args=[], returncode=0, stdout="", stderr=""),
    ]

    cleanup_env(self.args)

    # Verify kubeconfig delete was called for matching context
    delete_context_call = self.mock_run.call_args_list[3]
    self.assertIn("delete-context", delete_context_call.args[0])
    self.assertIn("gke_my-project_us-central1_xmc-my-dev", delete_context_call.args[0])


if __name__ == "__main__":
  unittest.main()
