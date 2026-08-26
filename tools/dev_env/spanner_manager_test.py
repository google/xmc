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
            auto_migrate=True,
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
    def test_list_databases_failure_raises_error(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(
            returncode=1,
            cmd=["gcloud", "spanner", "databases", "list"],
            stderr="Instance not found",
        )
        with self.assertRaises(subprocess.CalledProcessError):
            self.manager.list_databases()

    @mock.patch.object(Path, "exists", return_value=True)
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_ensure_databases_exist_missing(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        with mock.patch.object(self.manager, "list_databases", return_value=["existing-db"]):
            newly_created = self.manager.ensure_databases_exist()

        # Build descriptors (1), create 2 databases (2), and init 2 migration versions (2)
        self.assertEqual(mock_run.call_count, 5)
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
    def test_ensure_databases_exist_legacy_3_tuples(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        with mock.patch.object(self.manager, "list_databases", return_value=[]):
            newly_created = self.manager.ensure_databases_exist(
                databases=[("legacy-db", "schema.sql", "desc.pb")]
            )
        # 1 build descriptors, 1 create database, 0 init migration version (empty mig_dir)
        self.assertEqual(mock_run.call_count, 2)
        self.assertEqual(newly_created, {"legacy-db"})

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_ensure_databases_exist_descriptor_build_failure(
        self, mock_run: mock.MagicMock
    ) -> None:
        with mock.patch.object(self.manager, "list_databases", return_value=[]):
            with mock.patch.object(
                self.manager,
                "_build_protobuf_descriptors",
                side_effect=RuntimeError("bazel error"),
            ):
                with self.assertRaises(RuntimeError):
                    self.manager.ensure_databases_exist()

    @mock.patch.object(Path, "exists", return_value=True)
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_create_database_with_ddl_and_descriptors(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        self.manager._create_database(
            instance="inst",
            db_name="db",
            ddl_file="schema.sql",
            proto_desc_file="desc.pb",
        )
        mock_run.assert_called_once()
        cmd = mock_run.call_args[0][0]
        self.assertIn("gcloud", cmd)
        self.assertIn("create", cmd)
        self.assertIn("db", cmd)
        self.assertTrue(any(arg.startswith("--ddl-file=") for arg in cmd))
        self.assertTrue(any(arg.startswith("--proto-descriptors-file=") for arg in cmd))

    @mock.patch.object(Path, "exists", return_value=True)
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_create_database_with_ddl_only(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        self.manager._create_database(
            instance="inst",
            db_name="db",
            ddl_file="schema.sql",
            proto_desc_file=None,
        )
        mock_run.assert_called_once()
        cmd = mock_run.call_args[0][0]
        self.assertTrue(any(arg.startswith("--ddl-file=") for arg in cmd))
        self.assertFalse(any(arg.startswith("--proto-descriptors-file=") for arg in cmd))

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_create_database_without_schema_or_descriptors(self, mock_run: mock.MagicMock) -> None:
        self.manager._create_database(
            instance="inst",
            db_name="db",
            ddl_file=None,
            proto_desc_file=None,
        )
        mock_run.assert_called_once()
        cmd = mock_run.call_args[0][0]
        self.assertFalse(any(arg.startswith("--ddl-file=") for arg in cmd))
        self.assertFalse(any(arg.startswith("--proto-descriptors-file=") for arg in cmd))

    @mock.patch.object(Path, "exists", return_value=False)
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_create_database_missing_ddl_file_raises_error(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        with self.assertRaises(FileNotFoundError):
            self.manager._create_database(
                instance="inst",
                db_name="db",
                ddl_file="missing_schema.sql",
            )
        mock_run.assert_not_called()

    @mock.patch.object(Path, "exists", side_effect=[True, False])
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_create_database_missing_proto_desc_file_raises_error(
        self, mock_run: mock.MagicMock, mock_exists: mock.MagicMock
    ) -> None:
        with self.assertRaises(FileNotFoundError):
            self.manager._create_database(
                instance="inst",
                db_name="db",
                ddl_file="schema.sql",
                proto_desc_file="missing_desc.pb",
            )
        mock_run.assert_not_called()

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_get_migration_version(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout="Version: 42\nDirty: false\n",
        )
        ver, dirty = self.manager.get_migration_version(database="test-db", migrations_dir="ess")
        self.assertEqual(ver, 42)
        self.assertFalse(dirty)

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_get_migration_version_dirty_true(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout="Version: 15\nDirty: true\n",
        )
        ver, dirty = self.manager.get_migration_version(database="test-db", migrations_dir="ess")
        self.assertEqual(ver, 15)
        self.assertTrue(dirty)

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_get_migration_version_unmatched_output(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout="unexpected output",
        )
        ver, dirty = self.manager.get_migration_version(database="test-db", migrations_dir="ess")
        self.assertEqual(ver, 0)
        self.assertFalse(dirty)

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_apply_migrations_auto_migrate_true(self, mock_run: mock.MagicMock) -> None:
        self.manager.apply_migrations()
        self.assertEqual(mock_run.call_count, 2)
        cmd1 = mock_run.call_args_list[0][0][0]
        self.assertIn("-database=test-ess-db", cmd1)
        self.assertIn("-migrations_dir=ess", cmd1)
        self.assertIn("-command=up", cmd1)
        cmd2 = mock_run.call_args_list[1][0][0]
        self.assertIn("-database=test-xid-db", cmd2)
        self.assertIn("-migrations_dir=xid", cmd2)
        self.assertIn("-command=up", cmd2)

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_apply_migrations_resolve_migrations_dir(self, mock_run: mock.MagicMock) -> None:
        self.manager.apply_migrations(database="test-ess-db")
        self.assertEqual(mock_run.call_count, 1)
        cmd = mock_run.call_args[0][0]
        self.assertIn("-database=test-ess-db", cmd)
        self.assertIn("-migrations_dir=ess", cmd)
        self.assertIn("-command=up", cmd)

    def test_apply_migrations_unresolvable_database_raises(self) -> None:
        with self.assertRaises(ValueError) as cm:
            self.manager.apply_migrations(database="nonexistent-db")
        self.assertIn("Could not resolve migrations directory", str(cm.exception))

    def test_apply_migrations_migrations_dir_without_database_raises(self) -> None:
        with self.assertRaises(ValueError) as cm:
            self.manager.apply_migrations(migrations_dir="ess")
        self.assertIn("database must be specified", str(cm.exception))

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_apply_migrations_skips_newly_created(self, mock_run: mock.MagicMock) -> None:
        self.manager.apply_migrations(newly_created={"test-ess-db", "test-xid-db"})
        mock_run.assert_not_called()

    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_apply_migrations_auto_migrate_false(self, mock_run: mock.MagicMock) -> None:
        self.config.auto_migrate = False
        self.manager.apply_migrations()
        mock_run.assert_not_called()

    @mock.patch("tools.dev_env.spanner_manager.time.sleep")
    @mock.patch("tools.dev_env.spanner_manager.run_command")
    def test_apply_migrations_retries_on_failure(
        self, mock_run: mock.MagicMock, mock_sleep: mock.MagicMock
    ) -> None:
        mock_run.side_effect = [
            RuntimeError("transient lock"),
            subprocess.CompletedProcess(args=[], returncode=0),
            subprocess.CompletedProcess(args=[], returncode=0),
        ]
        self.manager.apply_migrations()
        self.assertEqual(mock_run.call_count, 3)
        mock_sleep.assert_called_once_with(5)

    def test_setup_orchestration(self) -> None:
        with (
            mock.patch.object(
                self.manager, "ensure_databases_exist", return_value={"test-ess-db"}
            ) as mock_ensure,
            mock.patch.object(self.manager, "apply_migrations") as mock_migrate,
        ):
            self.manager.setup()
            mock_ensure.assert_called_once()
            mock_migrate.assert_called_once_with(newly_created={"test-ess-db"})

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
