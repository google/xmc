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

"""Unit tests for ImageBuilder."""

import unittest
from unittest.mock import patch

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.image_builder import ImageBuilder


class TestImageBuilder(unittest.TestCase):
    """Tests for ImageBuilder."""

    def test_build_all_with_skip_build(self):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            skip_build=True,
        )
        builder = ImageBuilder(cfg)
        images = builder.build_all()
        self.assertEqual(len(images), 5)
        self.assertIn("experiment_state_server_image:latest", images)

    @patch.object(ImageBuilder, "_maybe_precompile_ui")
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_build_all_executes_parallel_bazel_and_docker_load(self, mock_run, mock_precompile):
        mock_run.return_value.stdout = "/workspace/bazel-bin"
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            skip_build=False,
        )
        builder = ImageBuilder(cfg)
        images = builder.build_all()

        mock_precompile.assert_called_once()
        expected_targets = [f"//{pkg}:{target}" for pkg, target, _ in ImageBuilder._SERVICES]
        mock_run.assert_any_call(
            ["bazel", "build", *expected_targets],
            cwd="/workspace",
        )

        # Check docker load calls
        self.assertEqual(len(images), 5)
        self.assertEqual(mock_run.call_count, 7)  # 1 bazel build + 1 bazel info + 5 docker load

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isfile", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_invokes_npm_when_node_present(
        self, mock_run, mock_isfile, mock_which
    ):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_any_call(
            ["npm", "ci", "--legacy-peer-deps"],
            cwd="/workspace/cmd/ui",
        )
        mock_run.assert_any_call(
            ["npx", "ng", "build", "--configuration", "production"],
            cwd="/workspace/cmd/ui",
        )

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isfile", return_value=True)
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_when_dist_is_not_stale(
        self, mock_run, mock_stale, mock_isfile, mock_which
    ):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_not_called()

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isfile", return_value=True)
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=True)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_recompiles_when_dist_is_stale(
        self, mock_run, mock_stale, mock_isfile, mock_which
    ):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_any_call(
            ["npm", "ci", "--legacy-peer-deps"],
            cwd="/workspace/cmd/ui",
        )

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("shutil.rmtree")
    @patch("os.path.isfile", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd", side_effect=RuntimeError("npm failed"))
    def test_maybe_precompile_ui_cleans_dist_on_failure(
        self, mock_run, mock_isfile, mock_rmtree, mock_which
    ):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        builder._maybe_precompile_ui()
        mock_rmtree.assert_called_once_with("/workspace/cmd/ui/dist", ignore_errors=True)

    @patch("os.path.getmtime")
    @patch("os.walk")
    def test_is_ui_dist_stale_ignores_python_and_bazel_files(self, mock_walk, mock_mtime):
        mock_walk.return_value = [
            ("/workspace/cmd/ui", ["app"], ["BUILD.bazel", "build_ui_oci.py", "server.py"]),
            ("/workspace/cmd/ui/app", [], ["app.component.ts"]),
        ]

        # index.html mtime = 100
        # BUILD.bazel and .py files mtime = 200 (newer, but should be ignored)
        # app.component.ts mtime = 50 (older, not stale)
        def mtime_side_effect(path):
            if path.endswith("index.html"):
                return 100
            if path.endswith((".py", "BUILD.bazel")):
                return 200
            return 50

        mock_mtime.side_effect = mtime_side_effect
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        is_stale = builder._is_ui_dist_stale(
            "/workspace/cmd/ui", "/workspace/cmd/ui/dist/browser/index.html"
        )
        self.assertFalse(is_stale)

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isdir", return_value=True)
    @patch("os.path.isfile")
    @patch("os.path.getmtime")
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=True)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_npm_ci_when_node_modules_fresh(
        self, mock_run, mock_stale, mock_mtime, mock_isfile, mock_isdir, mock_which
    ):
        # index.html exists, package-lock exists
        mock_isfile.return_value = True
        # node_modules mtime (200) > package-lock.json mtime (100)
        mock_mtime.side_effect = lambda p: 100 if "package-lock.json" in p else 200
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        builder._maybe_precompile_ui()

        # npm ci should NOT be called
        for call in mock_run.call_args_list:
            self.assertNotIn("npm", call[0][0])
        # ng build SHOULD be called
        mock_run.assert_called_once_with(
            ["npx", "ng", "build", "--configuration", "production"],
            cwd="/workspace/cmd/ui",
        )

    @patch("shutil.which", return_value=None)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_when_node_missing(self, mock_run, mock_which):
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_not_called()

    @patch("shutil.which")
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_when_npx_missing(self, mock_run, mock_which):
        # node and npm exist, but npx is missing
        mock_which.side_effect = lambda tool: "/usr/bin/" + tool if tool != "npx" else None
        cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )
        builder = ImageBuilder(cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
