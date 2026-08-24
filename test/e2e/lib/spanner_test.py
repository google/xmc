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

    @patch.object(SpannerManager, "_cleanup_stale_databases")
    @patch("test.e2e.lib.spanner.run_cmd")
    def test_setup_creates_dbs_with_ddl_and_proto_descriptors(self, mock_run, mock_cleanup):
        self.mgr.setup()

        mock_cleanup.assert_called_once()

        # Check proto descriptors build call
        mock_run.assert_any_call(
            [
                "bazel",
                "build",
                "//api/protos/experiment:experiment_messages_descriptor",
                "//api/protos/xid:xid_messages_descriptor",
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
            ]
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
            ]
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
        )

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


if __name__ == "__main__":
    unittest.main()
