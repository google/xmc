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

"""Unit tests for LocalRunner orchestrator."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.dev_env.config import LocalDevConfig
from tools.dev_env.local_runner import LocalRunner


class TestLocalRunner(unittest.TestCase):
    """Test suite for LocalRunner."""

    def setUp(self) -> None:
        super().setUp()
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.temp_path = Path(self.tmp_dir.name)
        self.config = LocalDevConfig(
            project_id="test-proj",
            environment="local-dev",
            spanner_instance="test-instance",
            ess_database="test_ess_db",
            xid_database="test_xid_db",
            cluster_name="test-kind-cluster",
        )
        self.runner = LocalRunner(config=self.config, repo_root=self.temp_path)

    def tearDown(self) -> None:
        self.tmp_dir.cleanup()
        super().tearDown()

    @patch("tools.dev_env.local_runner.check_preflight_tools")
    def test_up_orchestration(self, mock_preflight: MagicMock) -> None:
        self.runner.spanner_mgr = MagicMock()
        self.runner.kind_mgr = MagicMock()
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()

        self.runner.image_builder.build_images.return_value = ["img1:latest", "img2:latest"]

        self.runner.up(recreate=True, skip_build=False)

        mock_preflight.assert_called_once()
        self.runner.spanner_mgr.setup.assert_called_once()
        self.runner.kind_mgr.create_cluster.assert_called_once_with(recreate=True)
        self.runner.kind_mgr.ensure_secrets.assert_called_once()
        self.runner.kind_mgr.apply_manifests.assert_called_once()
        self.runner.image_builder.build_images.assert_called_once_with(skip_build=False)
        self.runner.image_builder.load_images_into_kind.assert_called_once_with(
            ["img1:latest", "img2:latest"]
        )
        self.runner.helm_deployer.deploy.assert_called_once()

    @patch("tools.dev_env.local_runner.check_preflight_tools")
    def test_up_orchestration_skip_build(self, mock_preflight: MagicMock) -> None:
        self.runner.spanner_mgr = MagicMock()
        self.runner.kind_mgr = MagicMock()
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()

        self.runner.image_builder.build_images.return_value = ["img1:latest"]
        self.runner.up(skip_build=True)

        mock_preflight.assert_called_once()
        self.runner.image_builder.build_images.assert_called_once_with(skip_build=True)
        self.runner.image_builder.load_images_into_kind.assert_not_called()

    @patch("tools.dev_env.local_runner.check_preflight_tools")
    def test_up_orchestration_skip_helm(self, mock_preflight: MagicMock) -> None:
        self.runner.spanner_mgr = MagicMock()
        self.runner.kind_mgr = MagicMock()
        self.runner.image_builder = MagicMock()
        self.runner.helm_deployer = MagicMock()

        self.runner.image_builder.build_images.return_value = ["img1:latest"]
        self.runner.up(skip_helm=True)

        mock_preflight.assert_called_once()
        self.runner.helm_deployer.deploy.assert_not_called()

    @patch("tools.dev_env.local_runner.log_success")
    @patch("tools.dev_env.local_runner.log_info")
    def test_down_without_drop_db(self, mock_info: MagicMock, mock_success: MagicMock) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.spanner_mgr = MagicMock()

        self.runner.down(drop_db=False)

        self.runner.kind_mgr.delete_cluster.assert_called_once()
        self.runner.spanner_mgr.drop_databases.assert_not_called()
        mock_success.assert_called_once_with(
            "Local KinD development environment teardown complete."
        )

    @patch("tools.dev_env.local_runner.log_success")
    @patch("tools.dev_env.local_runner.log_info")
    def test_down_with_drop_db(self, mock_info: MagicMock, mock_success: MagicMock) -> None:
        self.runner.kind_mgr = MagicMock()
        self.runner.spanner_mgr = MagicMock()

        self.runner.down(drop_db=True)

        self.runner.kind_mgr.delete_cluster.assert_called_once()
        self.runner.spanner_mgr.drop_databases.assert_called_once()
        mock_success.assert_called_once_with(
            "Local KinD development environment teardown complete."
        )

    @patch("tools.dev_env.local_runner.log_info")
    def test_display_ready_banner(self, mock_log_info: MagicMock) -> None:
        self.runner._display_ready_banner()
        mock_log_info.assert_called_once()
        output = mock_log_info.call_args[0][0]
        self.assertIn("XMC Local KinD Development Environment is Ready!", output)
        self.assertIn("http://localhost:8080", output)
        self.assertIn("http://localhost:5556/dex", output)
        self.assertIn("admin@xmc.local", output)


if __name__ == "__main__":
    unittest.main()
