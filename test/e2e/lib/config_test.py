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
import tempfile
import unittest
from unittest.mock import patch

from test.e2e.lib.config import (
    DEFAULT_ADMIN_PASSWORD,
    DEFAULT_ADMIN_USER,
    DEFAULT_BASE_URL,
    DEFAULT_DEV_EMAIL,
    DEFAULT_DEV_PASSWORD,
    DEFAULT_DEX_CLIENT_ID,
    DEFAULT_DEX_CLIENT_SECRET,
    DEFAULT_DEX_URL,
    DEFAULT_UNAUTHORIZED_PASSWORD,
    DEFAULT_UNAUTHORIZED_USER,
    DEFAULT_VIEWER_PASSWORD,
    DEFAULT_VIEWER_USER,
    E2EConfig,
    parse_args,
    resolve_kind_binary,
)


class TestE2EConfig(unittest.TestCase):
    """Tests for E2EConfig initialization."""

    def test_centralized_auth_and_url_constants(self):
        self.assertEqual(DEFAULT_ADMIN_USER, "admin@xmc.local")
        self.assertEqual(DEFAULT_ADMIN_PASSWORD, "password123")
        self.assertEqual(DEFAULT_VIEWER_USER, "viewer@xmc.local")
        self.assertEqual(DEFAULT_VIEWER_PASSWORD, "password123")
        self.assertEqual(DEFAULT_UNAUTHORIZED_USER, "unauthorized@xmc.local")
        self.assertEqual(DEFAULT_UNAUTHORIZED_PASSWORD, "password123")
        self.assertEqual(DEFAULT_DEV_EMAIL, DEFAULT_ADMIN_USER)
        self.assertEqual(DEFAULT_DEV_PASSWORD, DEFAULT_ADMIN_PASSWORD)
        self.assertEqual(DEFAULT_DEX_CLIENT_ID, "xmc-client")
        self.assertEqual(DEFAULT_DEX_CLIENT_SECRET, "xmc-client-secret")
        self.assertIn("http", DEFAULT_BASE_URL)
        self.assertIn("http", DEFAULT_DEX_URL)

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

        self.assertTrue(cfg.temp_dir.startswith(os.path.join(tempfile.gettempdir(), "xmc_e2e_")))

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


class TestResolveKindBinary(unittest.TestCase):
    """Tests for resolve_kind_binary resolution hierarchy."""

    def test_explicit_bin_provided(self):
        result = resolve_kind_binary("/explicit/path/to/kind")
        self.assertEqual(result, "/explicit/path/to/kind")

    def test_explicit_bin_expands_user(self):
        result = resolve_kind_binary("~/bin/kind")
        self.assertEqual(result, os.path.expanduser("~/bin/kind"))

    @patch.dict(os.environ, {"KIND_BIN": "/env/kind"}, clear=True)
    @patch("os.path.isfile")
    def test_env_var_kind_bin_used_when_file_exists(self, mock_isfile):
        mock_isfile.side_effect = lambda p: p == "/env/kind"
        result = resolve_kind_binary()
        self.assertEqual(result, "/env/kind")

    @patch.dict(os.environ, {"KIND_BIN": "/nonexistent/kind"}, clear=True)
    @patch("os.path.isfile")
    @patch("shutil.which")
    def test_env_var_kind_bin_skipped_when_nonexistent(self, mock_which, mock_isfile):
        mock_isfile.side_effect = lambda p: p == "/path/bin/kind"
        mock_which.return_value = "/path/bin/kind"
        result = resolve_kind_binary()
        self.assertEqual(result, "/path/bin/kind")

    @patch.dict(os.environ, {}, clear=True)
    @patch("os.path.isfile")
    @patch("shutil.which")
    def test_path_which_kind_used(self, mock_which, mock_isfile):
        mock_isfile.side_effect = lambda p: p == "/usr/bin/kind"
        mock_which.return_value = "/usr/bin/kind"
        result = resolve_kind_binary()
        self.assertEqual(result, "/usr/bin/kind")

    @patch.dict(os.environ, {}, clear=True)
    @patch("shutil.which", return_value=None)
    @patch("os.path.isfile", return_value=False)
    def test_not_found_raises_file_not_found_error(self, _mock_isfile, _mock_which):
        with self.assertRaises(FileNotFoundError) as ctx:
            resolve_kind_binary()
        self.assertIn("Failed to locate 'kind' executable", str(ctx.exception))


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
            "--kind-node-image=us-docker.pkg.dev/proj/xmc/kindest-node:v1.33.1",
            "--keep-cluster",
            "--skip-build",
            "--test-script=/path/to/custom_test.py",
        ]
        cfg = parse_args(args)
        self.assertEqual(cfg.project_id, "my-proj")
        self.assertEqual(cfg.spanner_instance, "my-inst")
        self.assertEqual(cfg.cluster_name, "my-kind")
        self.assertEqual(cfg.kind_bin, "/bin/kind")
        self.assertEqual(cfg.workspace_root, "/my/workspace")
        self.assertEqual(cfg.screenshots_dir, "/my/screenshots")
        self.assertEqual(cfg.kind_node_image, "us-docker.pkg.dev/proj/xmc/kindest-node:v1.33.1")
        self.assertTrue(cfg.keep_cluster)
        self.assertTrue(cfg.skip_build)
        self.assertEqual(cfg.test_script, "/path/to/custom_test.py")

    @patch.dict(
        os.environ,
        {
            "GCP_PROJECT_ID": "env-proj",
            "SPANNER_INSTANCE": "env-inst",
            "KIND_CLUSTER_NAME": "env-kind",
            "KIND_BIN": "/env/kind",
            "WORKSPACE_ROOT": "/env/root",
            "XMC_SCREENSHOTS_DIR": "/env/screenshots",
            "XMC_KIND_NODE_IMAGE": "us-docker.pkg.dev/env/xmc/node:v1.33.1",
            "XMC_E2E_TEST_SCRIPT": "/env/custom_test.py",
        },
        clear=True,
    )
    @patch("os.path.isfile")
    def test_parse_args_defaults_from_env(self, mock_isfile):
        mock_isfile.side_effect = lambda p: p == "/env/kind"
        cfg = parse_args([])
        self.assertEqual(cfg.project_id, "env-proj")
        self.assertEqual(cfg.spanner_instance, "env-inst")
        self.assertEqual(cfg.cluster_name, "env-kind")
        self.assertEqual(cfg.kind_bin, "/env/kind")
        self.assertEqual(cfg.workspace_root, "/env/root")
        self.assertEqual(cfg.screenshots_dir, "/env/screenshots")
        self.assertEqual(cfg.kind_node_image, "us-docker.pkg.dev/env/xmc/node:v1.33.1")
        self.assertFalse(cfg.keep_cluster)
        self.assertFalse(cfg.skip_build)
        self.assertEqual(cfg.test_script, "/env/custom_test.py")


if __name__ == "__main__":
    unittest.main()
