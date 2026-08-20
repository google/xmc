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

    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_build_all_executes_parallel_bazel_and_docker_load(self, mock_run):
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

        # Check that Bazel build was invoked with all 5 image tarballs
        mock_run.assert_any_call(
            [
                "bazel",
                "build",
                "//cmd/experiment_state_server:experiment_state_server_image_tarball",
                "//cmd/xid_service:xid_service_image_tarball",
                "//cmd/ui:ui_image_tarball",
                "//cmd/actuator:actuator_image_tarball",
                "//cmd/reconciler:reconciler_image_tarball",
            ],
            cwd="/workspace",
        )

        # Check docker load calls
        self.assertEqual(len(images), 5)
        self.assertEqual(mock_run.call_count, 6)  # 1 bazel build + 5 docker load


if __name__ == "__main__":
    unittest.main()
