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

from test.e2e.lib.command import TEARDOWN_TIMEOUT_SECONDS
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

    @patch.dict(os.environ, {"SPANNER_EMULATOR_HOST": "localhost:9010"}, clear=True)
    @patch("os.path.isfile", return_value=False)
    @patch.object(KindClusterManager, "exists", return_value=False)
    @patch("test.e2e.lib.kind.run_cmd")
    def test_setup_creates_dummy_credentials_when_emulator_host_set(
        self, mock_run, mock_exists, mock_isfile
    ):
        self.mgr.setup()
        dummy_cred = os.path.join(self.tmp_dir.name, "dummy-credentials.json")
        self.assertTrue(os.path.exists(dummy_cred))
        mock_run.assert_any_call(
            [
                "kubectl",
                "create",
                "secret",
                "generic",
                "gcp-sa-key",
                "-n",
                "xmc",
                f"--from-file=key.json={dummy_cred}",
            ]
        )

    @patch("test.e2e.lib.kind.run_cmd")
    def test_load_images_batches_all_images(self, mock_run):
        self.mgr.load_images(["img1:latest", "img2:latest"])
        mock_run.assert_called_once_with(
            [
                "kind",
                "load",
                "docker-image",
                "img1:latest",
                "img2:latest",
                "--name",
                "test-cluster",
            ],
            timeout=300.0,
        )

    @patch.dict(os.environ, {"GOOGLE_APPLICATION_CREDENTIALS": "/valid/key.json"})
    @patch("os.path.isfile", return_value=True)
    @patch.object(KindClusterManager, "exists", return_value=False)
    @patch("test.e2e.lib.kind.run_cmd")
    def test_setup_provisions_cluster_with_custom_node_image(
        self, mock_run, mock_exists, mock_isfile
    ):
        self.cfg.kind_node_image = "us-docker.pkg.dev/proj/xmc/kindest-node:v1.33.1"
        self.mgr.setup()
        mock_run.assert_any_call(
            [
                "kind",
                "create",
                "cluster",
                "--name",
                "test-cluster",
                f"--config={os.path.join('/workspace', 'test', 'e2e', 'manifests', 'kind_config.yaml')}",
                "--image=us-docker.pkg.dev/proj/xmc/kindest-node:v1.33.1",
            ]
        )

    @patch.dict(os.environ, {"GOOGLE_APPLICATION_CREDENTIALS": "/valid/key.json"})
    @patch("os.path.isfile", return_value=True)
    @patch.object(KindClusterManager, "exists", return_value=False)
    @patch("test.e2e.lib.kind.run_cmd")
    def test_setup_falls_back_to_default_image_when_custom_node_image_fails(
        self, mock_run, mock_exists, mock_isfile
    ):
        self.cfg.kind_node_image = "us-docker.pkg.dev/proj/xmc/kindest-node:v1.33.1"

        def side_effect(cmd, *args, **kwargs):
            if any(arg.startswith("--image=") for arg in cmd):
                raise subprocess.CalledProcessError(1, cmd)
            return subprocess.CompletedProcess(args=cmd, returncode=0)

        mock_run.side_effect = side_effect
        self.mgr.setup()
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

    @patch("test.e2e.lib.kind.run_cmd")
    def test_load_images_skips_when_empty(self, mock_run):
        self.mgr.load_images([])
        mock_run.assert_not_called()

    @patch("test.e2e.lib.kind.run_cmd")
    def test_load_images_raises_on_fatal_error(self, mock_run):
        mock_run.side_effect = subprocess.CalledProcessError(1, ["kind", "load"])
        with self.assertRaises(subprocess.CalledProcessError):
            self.mgr.load_images(["img1:latest"])

    @patch("test.e2e.lib.kind.run_cmd")
    def test_apply_manifests_batches_arguments(self, mock_run):
        self.mgr._apply_manifests()
        mock_run.assert_called_once_with(
            [
                "kubectl",
                "apply",
                "-f",
                os.path.join("/workspace", "test", "e2e", "manifests", "dummy_crds.yaml"),
                "-f",
                os.path.join("/workspace", "test", "e2e", "manifests", "redis.yaml"),
                "-f",
                os.path.join("/workspace", "test", "e2e", "manifests", "dex.yaml"),
                "-f",
                os.path.join("/workspace", "test", "e2e", "manifests", "gateway_proxy.yaml"),
            ]
        )

    @patch("test.e2e.lib.kind.run_cmd")
    def test_teardown_deletes_cluster_when_not_kept(self, mock_run):
        os.makedirs(self.cfg.temp_dir, exist_ok=True)
        self.mgr.teardown()
        mock_run.assert_called_once_with(
            ["kind", "delete", "cluster", "--name", "test-cluster"],
            check=False,
            timeout=TEARDOWN_TIMEOUT_SECONDS,
            stoppable=False,
        )
        self.assertFalse(os.path.exists(self.cfg.temp_dir))

    @patch("test.e2e.lib.kind.run_cmd")
    def test_teardown_deletes_cluster_even_if_existence_probe_would_time_out(self, mock_run):
        def time_out_existence_probe(cmd, *args, **kwargs):
            if cmd[1:] == ["get", "clusters"]:
                raise subprocess.TimeoutExpired(cmd, TEARDOWN_TIMEOUT_SECONDS)
            return subprocess.CompletedProcess(args=cmd, returncode=0)

        mock_run.side_effect = time_out_existence_probe
        self.mgr.teardown()
        commands = [args[0] for args, _ in mock_run.call_args_list]
        self.assertEqual(commands, [["kind", "delete", "cluster", "--name", "test-cluster"]])

    @patch("test.e2e.lib.kind.run_cmd")
    def test_teardown_skips_deletion_when_keep_cluster_is_true(self, mock_run):
        self.cfg.keep_cluster = True
        self.mgr.teardown()
        mock_run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
