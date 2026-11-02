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
from unittest.mock import mock_open, patch

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.image_builder import ImageBuilder


class TestImageBuilder(unittest.TestCase):
    """Tests for ImageBuilder."""

    def setUp(self) -> None:
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
        )

    def test_build_all_with_skip_build(self) -> None:
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
        self.assertEqual(len(images), 7)
        self.assertIn("experiment_state_server_image:latest", images)
        self.assertIn("xmc_bridge_image:latest", images)
        self.assertIn("postgres_migrator_image:latest", images)

    @patch.object(ImageBuilder, "_maybe_precompile_ui")
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_build_all_executes_parallel_bazel_and_docker_load(
        self, mock_run, mock_precompile
    ) -> None:
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
        expected_targets = [
            f"//{pkg}:{target}" for pkg, target, _ in ImageBuilder._SERVICES
        ] + ImageBuilder._EXTRA_BUILD_TARGETS
        mock_run.assert_any_call(
            ["bazel", "build", *expected_targets],
            cwd="/workspace",
        )
        self.assertIn("//test/e2e/manifests:jobset_manifests", expected_targets)
        self.assertIn("//test/e2e/manifests:kueue_manifests", expected_targets)
        self.assertEqual(len(images), 7)
        self.assertEqual(mock_run.call_count, 9)  # 1 bazel build + 1 bazel info + 7 docker load

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isfile", return_value=False)
    @patch("os.makedirs")
    @patch("builtins.open", new_callable=mock_open)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_invokes_npm_when_node_present(
        self, mock_run, mock_file, mock_makedirs, mock_isfile, mock_which
    ):
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_any_call(
            ["npm", "ci", "--legacy-peer-deps"],
            cwd="/workspace/cmd/ui",
        )
        mock_run.assert_any_call(
            ["npx", "ng", "build", "--configuration", "production"],
            cwd="/workspace/cmd/ui",
        )
        mock_makedirs.assert_called_once_with("/workspace/cmd/ui/node_modules", exist_ok=True)
        mock_file.assert_called_once_with(
            "/workspace/cmd/ui/node_modules/.install-success", "w", encoding="utf-8"
        )
        mock_file().write.assert_called_once_with("ok")

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isfile", return_value=True)
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_when_dist_is_not_stale(
        self, mock_run, mock_stale, mock_isfile, mock_which
    ):
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_not_called()

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isfile", return_value=True)
    @patch("os.path.getmtime")
    @patch("os.makedirs")
    @patch("builtins.open", new_callable=mock_open)
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=True)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_reinstalls_dependencies_when_package_lock_newer(
        self, mock_run, mock_stale, mock_file, mock_makedirs, mock_mtime, mock_isfile, mock_which
    ):
        # package-lock.json mtime (200) > .install-success mtime (100) -> reinstall needed
        mock_mtime.side_effect = lambda p: (
            200
            if p.endswith("package-lock.json")
            else (100 if p.endswith(".install-success") else 50)
        )
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_any_call(
            ["npm", "ci", "--legacy-peer-deps"],
            cwd="/workspace/cmd/ui",
        )
        mock_run.assert_any_call(
            ["npx", "ng", "build", "--configuration", "production"],
            cwd="/workspace/cmd/ui",
        )
        mock_file.assert_called_once_with(
            "/workspace/cmd/ui/node_modules/.install-success", "w", encoding="utf-8"
        )
        mock_file().write.assert_called_once_with("ok")

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("shutil.rmtree")
    @patch("os.path.isfile", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd", side_effect=RuntimeError("npm failed"))
    def test_maybe_precompile_ui_cleans_dist_on_failure(
        self, mock_run, mock_isfile, mock_rmtree, mock_which
    ):
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_rmtree.assert_called_once_with("/workspace/cmd/ui/dist")

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
        builder = ImageBuilder(self.cfg)
        is_stale = builder._is_ui_dist_stale(
            "/workspace/cmd/ui", "/workspace/cmd/ui/dist/browser/index.html"
        )
        self.assertFalse(is_stale)

    @patch("os.path.realpath")
    @patch("os.path.getmtime")
    @patch("os.walk")
    def test_is_ui_dist_stale_ignores_symlinks_pointing_to_ignored_dirs(
        self, mock_walk, mock_mtime, mock_realpath
    ):
        mock_walk.return_value = [
            ("/workspace/cmd/ui", ["custom_symlink"], ["app.component.ts"]),
        ]
        mock_realpath.side_effect = lambda p: (
            "/workspace/cmd/ui/node_modules" if "custom_symlink" in p else p
        )
        mock_mtime.return_value = 50
        builder = ImageBuilder(self.cfg)
        is_stale = builder._is_ui_dist_stale(
            "/workspace/cmd/ui", "/workspace/cmd/ui/dist/browser/index.html"
        )
        self.assertFalse(is_stale)

    @patch("shutil.which", return_value=None)
    @patch("shutil.rmtree", side_effect=OSError("locked file"))
    @patch("os.path.isfile", return_value=True)
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=True)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_handles_rmtree_os_error_gracefully(
        self, mock_run, mock_stale, mock_isfile, mock_rmtree, mock_which
    ):
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_rmtree.assert_called_once_with("/workspace/cmd/ui/dist")

    @patch("shutil.which", return_value="/usr/bin/node")
    @patch("os.path.isfile")
    @patch("os.path.getmtime")
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=True)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_npm_ci_when_node_modules_fresh(
        self, mock_run, mock_stale, mock_mtime, mock_isfile, mock_which
    ):
        # index.html exists, package-lock exists, .install-success exists
        mock_isfile.return_value = True
        # node_modules/.install-success mtime (200) > package-lock.json mtime (100)
        mock_mtime.side_effect = lambda p: (
            200 if p.endswith(".install-success") else (100 if "package" in p else 50)
        )
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()

        mock_run.assert_called_once_with(
            ["npx", "ng", "build", "--configuration", "production"],
            cwd="/workspace/cmd/ui",
        )

    @patch("shutil.which", return_value=None)
    @patch("shutil.rmtree")
    @patch("os.path.isfile", return_value=True)
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=True)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_wipes_stale_dist_when_node_missing(
        self, mock_run, mock_stale, mock_isfile, mock_rmtree, mock_which
    ):
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_rmtree.assert_called_once_with("/workspace/cmd/ui/dist")
        mock_run.assert_not_called()

    @patch("shutil.which", return_value=None)
    @patch("shutil.rmtree")
    @patch("os.path.isfile", return_value=True)
    @patch.object(ImageBuilder, "_is_ui_dist_stale", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_preserves_fresh_dist_when_node_missing(
        self, mock_run, mock_stale, mock_isfile, mock_rmtree, mock_which
    ):
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_rmtree.assert_not_called()
        mock_run.assert_not_called()

    @patch("shutil.which", return_value=None)
    @patch("os.path.isfile", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_when_node_missing(self, mock_run, mock_isfile, mock_which):
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_not_called()

    @patch("shutil.which")
    @patch("os.path.isfile", return_value=False)
    @patch("test.e2e.lib.image_builder.run_cmd")
    def test_maybe_precompile_ui_skips_when_npx_missing(self, mock_run, mock_isfile, mock_which):
        # node and npm exist, but npx is missing
        mock_which.side_effect = lambda tool: "/usr/bin/" + tool if tool != "npx" else None
        builder = ImageBuilder(self.cfg)
        builder._maybe_precompile_ui()
        mock_run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
