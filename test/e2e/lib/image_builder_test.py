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


if __name__ == "__main__":
    unittest.main()
