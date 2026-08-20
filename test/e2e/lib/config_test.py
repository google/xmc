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

"""Unit tests for E2EConfig data models and CLI argument parsing."""

import os
import unittest
from unittest.mock import patch

from test.e2e.lib.config import E2EConfig, parse_args


class TestE2EConfig(unittest.TestCase):
    """Tests for E2EConfig initialization."""

    def test_post_init_generates_unique_db_names_and_temp_dir(self):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )

        self.assertTrue(cfg.ess_db.startswith("e2e-ess-"))
        self.assertTrue(cfg.xid_db.startswith("e2e-xid-"))
        # Verify 6-character suffix format (e.g. e2e-ess-<ts>-<suffix>)
        ess_parts = cfg.ess_db.split("-")
        self.assertEqual(len(ess_parts), 4)
        self.assertEqual(len(ess_parts[3]), 6)

        xid_parts = cfg.xid_db.split("-")
        self.assertEqual(len(xid_parts), 4)
        self.assertEqual(len(xid_parts[3]), 6)

        import tempfile

        self.assertTrue(cfg.temp_dir.startswith(os.path.join(tempfile.gettempdir(), "xmc_e2e_")))
        self.assertTrue(os.path.isdir(cfg.temp_dir))
        # Cleanup
        os.rmdir(cfg.temp_dir)

    def test_post_init_preserves_explicit_db_names(self):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            ess_db="custom-ess",
            xid_db="custom-xid",
            temp_dir="/tmp/custom",
        )
        self.assertEqual(cfg.ess_db, "custom-ess")
        self.assertEqual(cfg.xid_db, "custom-xid")
        self.assertEqual(cfg.temp_dir, "/tmp/custom")


class TestParseArgs(unittest.TestCase):
    """Tests for parse_args CLI helper."""

    def test_parse_args_with_explicit_flags(self):
        args = [
            "--project-id=my-proj",
            "--spanner-instance=my-inst",
            "--cluster-name=my-kind",
            "--kind-bin=/bin/kind",
            "--workspace-root=/my/workspace",
            "--screenshots-dir=/my/screenshots",
            "--keep-cluster",
            "--skip-build",
        ]
        cfg = parse_args(args)
        self.assertEqual(cfg.project_id, "my-proj")
        self.assertEqual(cfg.spanner_instance, "my-inst")
        self.assertEqual(cfg.cluster_name, "my-kind")
        self.assertEqual(cfg.kind_bin, "/bin/kind")
        self.assertEqual(cfg.workspace_root, "/my/workspace")
        self.assertEqual(cfg.screenshots_dir, "/my/screenshots")
        self.assertTrue(cfg.keep_cluster)
        self.assertTrue(cfg.skip_build)

    @patch.dict(
        os.environ,
        {
            "GCP_PROJECT_ID": "env-proj",
            "SPANNER_INSTANCE": "env-inst",
            "KIND_CLUSTER_NAME": "env-kind",
            "KIND_BIN": "/env/kind",
            "WORKSPACE_ROOT": "/env/root",
            "XMC_SCREENSHOTS_DIR": "/env/screenshots",
        },
        clear=True,
    )
    def test_parse_args_defaults_from_env(self):
        cfg = parse_args([])
        self.assertEqual(cfg.project_id, "env-proj")
        self.assertEqual(cfg.spanner_instance, "env-inst")
        self.assertEqual(cfg.cluster_name, "env-kind")
        self.assertEqual(cfg.kind_bin, "/env/kind")
        self.assertEqual(cfg.workspace_root, "/env/root")
        self.assertEqual(cfg.screenshots_dir, "/env/screenshots")
        self.assertFalse(cfg.keep_cluster)
        self.assertFalse(cfg.skip_build)


if __name__ == "__main__":
    unittest.main()
