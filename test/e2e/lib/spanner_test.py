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

"""Unit tests for SpannerManager."""

import subprocess
import time
import unittest
from unittest.mock import call, patch

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.spanner import SpannerManager


class TestSpannerManager(unittest.TestCase):
    """Tests for SpannerManager ephemeral database handling."""

    def setUp(self):
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            ess_db="e2e-ess-1000-abcdef",
            xid_db="e2e-xid-1000-abcdef",
            temp_dir="/tmp/e2e_test",
        )
        self.mgr = SpannerManager(self.cfg)
        self.mgr._cached_bazel_bin = "/workspace/bazel-bin"

    @patch("test.e2e.lib.spanner.run_cmd")
    def test_cleanup_stale_databases_deletes_expired_databases(self, mock_run):
        now = int(time.time())
        old_ts = now - 5000  # > 3600s old
        fresh_ts = now - 500  # < 3600s old

        mock_res = subprocess.CompletedProcess(
            args=[],
            returncode=0,
            stdout=(
                f"projects/test-proj/instances/test-inst/databases/e2e-ess-{old_ts}-123456\n"
                f"projects/test-proj/instances/test-inst/databases/e2e-xid-{fresh_ts}-abcdef\n"
                f"projects/test-proj/instances/test-inst/databases/prod-db\n"
                f"projects/test-proj/instances/test-inst/databases/e2e-invalid-format\n"
            ),
        )
        mock_run.return_value = mock_res

        self.mgr._cleanup_stale_databases()

        # Should query DB list
        mock_run.assert_any_call(
            [
                "gcloud",
                "spanner",
                "databases",
                "list",
                "--instance=test-inst",
                "--project=test-proj",
                "--format=value(NAME)",
            ],
            capture_output=True,
            check=True,
        )

        # Should delete only the stale DB
        mock_run.assert_any_call(
            [
                "gcloud",
                "spanner",
                "databases",
                "delete",
                f"e2e-ess-{old_ts}-123456",
                "--instance=test-inst",
                "--project=test-proj",
                "--quiet",
            ],
            check=False,
        )

    @patch("os.path.isfile", return_value=False)
    @patch.object(SpannerManager, "_cleanup_stale_databases")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_setup_creates_dbs_with_ddl_and_proto_descriptors(
        self, mock_run, mock_cleanup, mock_isfile
    ):
        self.mgr.setup()

        mock_cleanup.assert_called_once()

        # Check prerequisites build call
        mock_run.assert_any_call(
            [
                "bazel",
                "build",
                "//api/protos/experiment:experiment_messages_descriptor",
                "//api/protos/xid:xid_messages_descriptor",
                "//internal/storage/spanner/migrations:migrations",
            ],
            cwd="/workspace",
        )

        # Check DB creation calls with --ddl-file and --proto-descriptors-file
        mock_run.assert_any_call(
            [
                "gcloud",
                "spanner",
                "databases",
                "create",
                "e2e-ess-1000-abcdef",
                "--instance=test-inst",
                "--project=test-proj",
                "--ddl-file=/workspace/internal/storage/spanner/schema/ess-schema.sql",
                "--proto-descriptors-file=/workspace/bazel-bin/api/protos/experiment/experiment_messages_descriptor.pb",
            ],
            capture_output=True,
            timeout=120,
        )
        mock_run.assert_any_call(
            [
                "gcloud",
                "spanner",
                "databases",
                "create",
                "e2e-xid-1000-abcdef",
                "--instance=test-inst",
                "--project=test-proj",
                "--ddl-file=/workspace/internal/storage/spanner/schema/xid-schema.sql",
                "--proto-descriptors-file=/workspace/bazel-bin/api/protos/xid/xid_messages_descriptor.pb",
            ],
            capture_output=True,
            timeout=120,
        )

        # Check migration version initialization calls (-command=init)
        mock_run.assert_any_call(
            [
                "bazel",
                "run",
                "//internal/storage/spanner/migrations:migrations",
                "--",
                "-project=test-proj",
                "-instance=test-inst",
                "-database=e2e-ess-1000-abcdef",
                "-migrations_dir=ess",
                "-command=init",
            ],
            cwd="/workspace",
            capture_output=True,
            timeout=45,
        )
        mock_run.assert_any_call(
            [
                "bazel",
                "run",
                "//internal/storage/spanner/migrations:migrations",
                "--",
                "-project=test-proj",
                "-instance=test-inst",
                "-database=e2e-xid-1000-abcdef",
                "-migrations_dir=xid",
                "-command=init",
            ],
            cwd="/workspace",
            capture_output=True,
            timeout=45,
        )

    @patch("random.uniform", return_value=0.0)
    @patch("time.sleep")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_create_database_retries_on_resource_exhausted(self, mock_run, mock_sleep, _):
        mock_run.side_effect = [
            subprocess.CalledProcessError(
                returncode=1,
                cmd=["gcloud", "spanner", "databases", "create"],
                stderr="status: RESOURCE_EXHAUSTED, maximum database limit (20) reached",
            ),
            subprocess.CompletedProcess(args=[], returncode=0),
        ]

        self.mgr._create_database("test-db", "schema.sql", "desc.pb")

        self.assertEqual(mock_run.call_count, 2)
        mock_sleep.assert_called_once_with(15)

    @patch("random.uniform", return_value=0.0)
    @patch("time.sleep")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_create_database_retries_failed_operation_message_until_attempts_run_out(
        self, mock_run, mock_sleep, _
    ):
        # A failed long-running operation is reported by gcloud without a status code.
        error = subprocess.CalledProcessError(
            returncode=1,
            cmd=["gcloud", "spanner", "databases", "create"],
            stderr="ERROR: (gcloud.spanner.databases.create) Deadline exceeded",
        )
        mock_run.side_effect = error

        with self.assertLogs("xmc_e2e_runner", level="WARNING") as logs:
            with self.assertRaises(subprocess.CalledProcessError) as ctx:
                self.mgr._create_database("test-db", "schema.sql", "desc.pb")

        self.assertIs(ctx.exception, error)
        self.assertEqual(mock_run.call_count, 4)
        self.assertEqual(mock_sleep.call_args_list, [call(15), call(30), call(60)])
        self.assertEqual(len(logs.records), 4)
        self.assertIn("not retrying", logs.output[-1])
        self.assertIn("Deadline exceeded", logs.output[-1])

    @patch("random.uniform", return_value=0.0)
    @patch("time.sleep")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_create_database_accepts_ready_database_after_timed_out_attempt(
        self, mock_run, mock_sleep, _
    ):
        mock_run.side_effect = [
            subprocess.TimeoutExpired(["gcloud"], 120),
            subprocess.CalledProcessError(1, ["gcloud"], stderr="ALREADY_EXISTS: Database d"),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="READY\n"),
        ]

        self.mgr._create_database("test-db", "schema.sql", "desc.pb")

        self.assertEqual(mock_run.call_args_list[0].kwargs["timeout"], 120)
        self.assertEqual(
            mock_run.call_args_list[2].args[0],
            [
                "gcloud",
                "spanner",
                "databases",
                "describe",
                "test-db",
                "--instance=test-inst",
                "--project=test-proj",
                "--format=value(state)",
            ],
        )
        mock_sleep.assert_called_once_with(15)

    @patch("time.sleep")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_create_database_fails_on_already_exists_of_first_attempt(self, mock_run, mock_sleep):
        exists = subprocess.CalledProcessError(1, ["gcloud"], stderr="ALREADY_EXISTS: Database d")
        mock_run.side_effect = [exists]

        with self.assertRaises(subprocess.CalledProcessError) as ctx:
            self.mgr._create_database("test-db", "schema.sql", "desc.pb")

        self.assertIs(ctx.exception, exists)
        mock_sleep.assert_not_called()

    @patch("random.uniform", return_value=0.0)
    @patch("time.sleep")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_create_database_polls_creating_database_until_ready(self, mock_run, mock_sleep, _):
        creating = subprocess.CompletedProcess(args=[], returncode=0, stdout="CREATING\n")
        mock_run.side_effect = [
            subprocess.TimeoutExpired(["gcloud"], 120),
            subprocess.CalledProcessError(1, ["gcloud"], stderr="ALREADY_EXISTS: Database d"),
            creating,
            subprocess.CompletedProcess(args=[], returncode=1, stdout="", stderr="UNAVAILABLE"),
            subprocess.CompletedProcess(args=[], returncode=0, stdout="READY\n"),
        ]

        self.mgr._create_database("test-db", "schema.sql", "desc.pb")

        self.assertEqual(mock_run.call_count, 5)
        self.assertEqual(mock_sleep.call_args_list, [call(15), call(5), call(5)])

    @patch("random.uniform", return_value=0.0)
    @patch("time.sleep")
    @patch("time.monotonic")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_create_database_retries_database_that_stays_creating_within_budget(
        self, mock_run, mock_monotonic, mock_sleep, _
    ):
        clock = {"now": 0.0}
        mock_monotonic.side_effect = lambda: clock["now"]
        mock_sleep.side_effect = lambda seconds: clock.update(now=clock["now"] + seconds)

        def _gcloud(cmd, **kwargs):
            if "describe" in cmd:
                return subprocess.CompletedProcess(args=cmd, returncode=0, stdout="CREATING\n")
            if clock["now"] == 0:
                clock["now"] += kwargs["timeout"]
                raise subprocess.TimeoutExpired(cmd, kwargs["timeout"])
            raise subprocess.CalledProcessError(1, cmd, stderr="ALREADY_EXISTS: Database d")

        mock_run.side_effect = _gcloud

        with self.assertRaises(subprocess.TimeoutExpired) as ctx:
            self.mgr._create_database("test-db", "schema.sql", "desc.pb")

        # Attempt 1 times out at 120 s and waits 15 s; attempt 2 polls every 5 s for its
        # remaining 105 s. A retry after that would not get 30 s of the 240 s budget.
        self.assertIn("test-db is CREATING, not READY", ctx.exception.stderr)
        self.assertEqual(clock["now"], 240)
        self.assertEqual(mock_sleep.call_args_list, [call(15)] + [call(5)] * 21)

    @patch("random.uniform", side_effect=lambda low, high: high)
    @patch("time.sleep")
    @patch("time.monotonic")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_create_database_gives_up_within_total_budget_when_attempts_time_out(
        self, mock_run, mock_monotonic, mock_sleep, _
    ):
        clock = {"now": 0.0}
        mock_monotonic.side_effect = lambda: clock["now"]
        mock_sleep.side_effect = lambda seconds: clock.update(now=clock["now"] + seconds)

        def _time_out(cmd, **kwargs):
            clock["now"] += kwargs["timeout"]
            raise subprocess.TimeoutExpired(cmd, kwargs["timeout"])

        mock_run.side_effect = _time_out

        with self.assertRaises(subprocess.TimeoutExpired):
            self.mgr._create_database("test-db", "schema.sql", "desc.pb")

        # Attempt 1 times out at 120 s and waits the longest jittered 18 s; attempt 2 gets the
        # remaining 102 s, after which no wait fits the 240 s cap.
        self.assertEqual([c.kwargs["timeout"] for c in mock_run.call_args_list], [120, 102])
        mock_sleep.assert_called_once_with(18)
        self.assertEqual(clock["now"], 240)

    @patch("os.access", return_value=True)
    @patch("os.path.isfile", return_value=True)
    def test_resolve_migration_binary_finds_executable(self, mock_isfile, mock_access):
        binary = self.mgr._resolve_migration_binary()
        self.assertIsNotNone(binary)
        self.assertTrue(binary.endswith(("migrations", "migrations.exe")))

    @patch("os.path.isdir", return_value=False)
    @patch("os.path.isfile", return_value=False)
    def test_resolve_migration_binary_returns_none_when_missing(self, mock_isfile, mock_isdir):
        binary = self.mgr._resolve_migration_binary()
        self.assertIsNone(binary)

    @patch("time.sleep")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_init_migration_version_retries_on_grpc_resource_exhausted(self, mock_run, mock_sleep):
        mock_run.side_effect = [
            subprocess.CalledProcessError(
                returncode=1,
                cmd=["migrations"],
                stderr=b"rpc error: code = ResourceExhausted desc = Quota exceeded for quota metric ...",
            ),
            subprocess.CompletedProcess(args=[], returncode=0),
        ]

        with patch.object(
            self.mgr, "_resolve_migration_binary", return_value="/workspace/bazel-bin/migrations"
        ):
            self.mgr._init_migration_version("test-db", "ess")

        self.assertEqual(mock_run.call_count, 2)
        mock_sleep.assert_called_once_with(5)

    @patch("time.sleep")
    @patch("time.monotonic")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_init_migration_version_retries_timed_out_attempt_within_total_budget(
        self, mock_run, mock_monotonic, mock_sleep
    ):
        clock = {"now": 0.0}
        mock_monotonic.side_effect = lambda: clock["now"]
        mock_sleep.side_effect = lambda seconds: clock.update(now=clock["now"] + seconds)

        def _time_out(cmd, **kwargs):
            clock["now"] += kwargs["timeout"]
            raise subprocess.TimeoutExpired(cmd, kwargs["timeout"])

        mock_run.side_effect = _time_out

        with (
            patch.object(
                self.mgr,
                "_resolve_migration_binary",
                return_value="/workspace/bazel-bin/migrations",
            ),
            self.assertRaises(subprocess.TimeoutExpired),
        ):
            self.mgr._init_migration_version("test-db", "ess")

        # Attempt 1 times out at 45 s and waits 5 s, which leaves 40 s of the 90 s cap, more
        # than the 30 s a retry needs; attempt 2 then times out at the cap.
        self.assertEqual([c.kwargs["timeout"] for c in mock_run.call_args_list], [45, 40])
        mock_sleep.assert_called_once_with(5)
        self.assertEqual(clock["now"], 90)

    @patch("os.path.getsize", return_value=1024)
    @patch("os.path.isfile", return_value=True)
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_build_prerequisites_skips_when_all_targets_present(
        self, mock_run, mock_isfile, mock_getsize
    ):
        with patch.object(
            self.mgr, "_resolve_migration_binary", return_value="/workspace/bazel-bin/migrations"
        ):
            self.mgr._build_prerequisites()
            mock_run.assert_not_called()

    @patch("test.e2e.lib.spanner.run_cmd")
    def test_get_bazel_bin_queries_bazel_info(self, mock_run):
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout="/custom/bazel-bin\n"
        )
        mgr = SpannerManager(self.cfg)
        self.assertEqual(mgr._get_bazel_bin(), "/custom/bazel-bin")

    @patch("test.e2e.lib.spanner.run_cmd", side_effect=RuntimeError("bazel not found"))
    def test_get_bazel_bin_falls_back_on_error(self, mock_run):
        mgr = SpannerManager(self.cfg)
        self.assertEqual(mgr._get_bazel_bin(), "/workspace/bazel-bin")

    @patch("test.e2e.lib.spanner.run_cmd")
    def test_teardown_drops_databases(self, mock_run):
        self.mgr.teardown()

        expected_calls = [
            call(
                [
                    "gcloud",
                    "spanner",
                    "databases",
                    "delete",
                    "e2e-ess-1000-abcdef",
                    "--instance=test-inst",
                    "--project=test-proj",
                    "--quiet",
                ],
                check=False,
            ),
            call(
                [
                    "gcloud",
                    "spanner",
                    "databases",
                    "delete",
                    "e2e-xid-1000-abcdef",
                    "--instance=test-inst",
                    "--project=test-proj",
                    "--quiet",
                ],
                check=False,
            ),
        ]
        mock_run.assert_has_calls(expected_calls, any_order=True)

    def test_prepare_e2e_ddl_file_appends_schema_migrations_table(self):
        import tempfile

        with tempfile.TemporaryDirectory() as tmp_dir:
            self.cfg.temp_dir = tmp_dir
            src_ddl = f"{tmp_dir}/base-schema.sql"
            with open(src_ddl, "w", encoding="utf-8") as f:
                f.write("CREATE TABLE Experiments (Id INT64 NOT NULL) PRIMARY KEY(Id);")

            combined_path = self.mgr._prepare_e2e_ddl_file("e2e-ess-123", src_ddl)
            self.assertNotEqual(combined_path, src_ddl)
            with open(combined_path, encoding="utf-8") as f:
                content = f.read()
            self.assertIn("CREATE TABLE Experiments", content)
            self.assertIn("CREATE TABLE SchemaMigrations", content)

            # Second call on already combined file returns unchanged path
            self.assertEqual(
                self.mgr._prepare_e2e_ddl_file("e2e-ess-123", combined_path), combined_path
            )

    @patch("time.sleep")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_init_migration_version_fails_fast_on_duplicate_name_error(self, mock_run, mock_sleep):
        mock_run.side_effect = subprocess.CalledProcessError(
            returncode=1,
            cmd=["migrations"],
            stderr=b"rpc error: code = FailedPrecondition desc = Duplicate name in schema: SchemaMigrations",
        )

        with (
            patch.object(
                self.mgr,
                "_resolve_migration_binary",
                return_value="/workspace/bazel-bin/migrations",
            ),
            self.assertRaises(subprocess.CalledProcessError),
        ):
            self.mgr._init_migration_version("test-db", "ess")

        self.assertEqual(mock_run.call_count, 1)
        mock_sleep.assert_not_called()


if __name__ == "__main__":
    unittest.main()
