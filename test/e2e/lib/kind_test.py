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

"""Unit tests for KindClusterManager."""

import os
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.kind import KindClusterManager


class TestKindClusterManager(unittest.TestCase):
    """Tests for KindClusterManager."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            temp_dir=self.tmp_dir.name,
        )
        self.mgr = KindClusterManager(self.cfg)

    def tearDown(self):
        self.tmp_dir.cleanup()

    @patch("test.e2e.lib.kind.run_cmd")
    def test_exists_returns_true_when_cluster_found(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout="other-cluster\ntest-cluster\n",
            stderr="",
        )
        self.assertTrue(self.mgr.exists())

    @patch("test.e2e.lib.kind.run_cmd")
    def test_exists_returns_false_when_cluster_not_found(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout="other-cluster\n",
            stderr="",
        )
        self.assertFalse(self.mgr.exists())

    @patch.dict(os.environ, {"GOOGLE_APPLICATION_CREDENTIALS": "/nonexistent/key.json"})
    @patch.object(KindClusterManager, "exists", return_value=False)
    @patch("test.e2e.lib.kind.run_cmd")
    def test_setup_raises_file_not_found_when_credentials_missing(self, mock_run, mock_exists):
        with self.assertRaises(FileNotFoundError) as ctx:
            self.mgr.setup()
        self.assertIn("GCP credentials file not found", str(ctx.exception))

    @patch.dict(os.environ, {"GOOGLE_APPLICATION_CREDENTIALS": "/valid/key.json"})
    @patch("os.path.isfile", return_value=True)
    @patch.object(KindClusterManager, "exists", return_value=False)
    @patch("test.e2e.lib.kind.run_cmd")
    def test_setup_provisions_cluster_and_secrets(self, mock_run, mock_exists, mock_isfile):
        self.mgr.setup()

        # Check cluster creation
        mock_run.assert_any_call(
            [
                "kind",
                "create",
                "cluster",
                "--name",
                "test-cluster",
                f"--config={os.path.join('/workspace', 'test', 'e2e', 'manifests', 'kind_config.yaml')}",
            ]
        )

        # Check secret creations
        mock_run.assert_any_call(
            [
                "kubectl",
                "create",
                "secret",
                "generic",
                "gcp-sa-key",
                "-n",
                "xmc",
                "--from-file=key.json=/valid/key.json",
            ]
        )

    @patch("test.e2e.lib.kind.run_cmd")
    def test_load_images(self, mock_run):
        self.mgr.load_images(["img1:latest", "img2:latest"])
        mock_run.assert_any_call(
            [
                "kind",
                "load",
                "docker-image",
                "img1:latest",
                "--name",
                "test-cluster",
            ]
        )
        mock_run.assert_any_call(
            [
                "kind",
                "load",
                "docker-image",
                "img2:latest",
                "--name",
                "test-cluster",
            ]
        )

    @patch.object(KindClusterManager, "exists", return_value=True)
    @patch("test.e2e.lib.kind.run_cmd")
    def test_teardown_deletes_cluster_when_not_kept(self, mock_run, mock_exists):
        self.mgr.teardown()
        mock_run.assert_called_once_with(
            ["kind", "delete", "cluster", "--name", "test-cluster"],
            check=False,
        )

    @patch("test.e2e.lib.kind.run_cmd")
    def test_teardown_skips_deletion_when_keep_cluster_is_true(self, mock_run):
        self.cfg.keep_cluster = True
        self.mgr.teardown()
        mock_run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
