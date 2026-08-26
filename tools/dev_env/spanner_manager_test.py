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

"""Unit tests for tools.dev_env.spanner_manager."""

import subprocess
import unittest
from pathlib import Path
from unittest import mock

from tools.dev_env.config import LocalDevConfig
from tools.dev_env.spanner_manager import SpannerManager


class SpannerManagerTest(unittest.TestCase):
    """Test suite for SpannerManager database lifecycle operations."""

    def setUp(self) -> None:
        super().setUp()
        self.config = LocalDevConfig(
            project_id="test-proj",
            spanner_instance="test-instance",
            ess_database="test-ess-db",
            xid_database="test-xid-db",
        )
        self.manager = SpannerManager(config=self.config)

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_list_databases_success(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout="projects/p/instances/i/databases/test-ess-db\nprojects/p/instances/i/databases/other-db\n",
        )
        dbs = self.manager.list_databases()
        self.assertEqual(dbs, ["test-ess-db", "other-db"])

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_list_databases_failure_returns_empty(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=1,
            stdout="",
            stderr="Instance not found",
        )
        dbs = self.manager.list_databases()
        self.assertEqual(dbs, [])

    @mock.patch.object(Path, "exists", return_value=True)
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_ensure_databases_exist_missing(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        with mock.patch.object(self.manager, "list_databases", return_value=["existing-db"]):
            newly_created = self.manager.ensure_databases_exist()

        self.assertEqual(mock_run.call_count, 3)
        self.assertEqual(newly_created, {"test-ess-db", "test-xid-db"})

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_ensure_databases_exist_already_present(self, mock_run: mock.MagicMock) -> None:
        with mock.patch.object(
            self.manager,
            "list_databases",
            return_value=["test-ess-db", "test-xid-db"],
        ):
            newly_created = self.manager.ensure_databases_exist()

        mock_run.assert_not_called()
        self.assertEqual(newly_created, set())

    @mock.patch.object(Path, "exists", return_value=True)
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_ensure_databases_exist_descriptor_build_failure(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        with mock.patch.object(self.manager, "list_databases", return_value=[]):
            with mock.patch.object(
                self.manager,
                "_build_protobuf_descriptors",
                side_effect=RuntimeError("bazel error"),
            ):
                newly_created = self.manager.ensure_databases_exist()

        self.assertEqual(newly_created, {"test-ess-db", "test-xid-db"})
        self.assertEqual(mock_run.call_count, 2)

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_drop_databases(self, mock_run: mock.MagicMock) -> None:
        self.manager.drop_databases()
        self.assertEqual(mock_run.call_count, 2)
        cmd = mock_run.call_args[0][0]
        self.assertIn("gcloud", cmd)
        self.assertIn("delete", cmd)
        self.assertIn("--quiet", cmd)

    def test_drop_databases_empty(self) -> None:
        self.config.ess_database = ""
        self.config.xid_database = ""
        with mock.patch("tools.dev_env.spanner_manager.run_command") as mock_run:
            self.manager.drop_databases()
            mock_run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
