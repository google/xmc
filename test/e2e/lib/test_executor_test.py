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

"""Unit tests for PlaywrightTestExecutor."""

import os
import tempfile
import unittest
from unittest.mock import MagicMock, patch

from test.e2e.lib.config import E2E_VENV_DEPENDENCIES, E2EConfig
from test.e2e.lib.test_executor import PlaywrightTestExecutor


class TestPlaywrightTestExecutor(unittest.TestCase):
    """Tests for PlaywrightTestExecutor."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.orig_environ = os.environ.copy()
        self.cfg = E2EConfig(
            project_id="test-proj",
            spanner_instance="test-inst",
            cluster_name="test-cluster",
            kind_bin="kind",
            workspace_root="/workspace",
            screenshots_dir="/screenshots",
            temp_dir=self.tmp_dir.name,
        )
        self.executor = PlaywrightTestExecutor(self.cfg)

    def tearDown(self):
        os.environ.clear()
        os.environ.update(self.orig_environ)
        self.tmp_dir.cleanup()

    @patch("os.path.exists")
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_setup_venv_creates_venv_and_installs_when_fallback_needed(self, mock_run, mock_exists):
        # Simulate browser path missing so fallback venv path is exercised
        mock_exists.side_effect = lambda p: False
        py_bin = self.executor.setup_venv()
        expected_py = os.path.join(self.executor.venv_dir, "bin", "python")
        self.assertEqual(py_bin, expected_py)

        # Check pip install call
        mock_run.assert_any_call(
            [
                os.path.join(self.executor.venv_dir, "bin", "pip"),
                "install",
                "--upgrade",
                "pip",
                *E2E_VENV_DEPENDENCIES,
            ]
        )
        # Check playwright install chromium call
        mock_run.assert_any_call(
            [
                os.path.join(self.executor.venv_dir, "bin", "playwright"),
                "install",
                "chromium",
            ]
        )

    @patch("os.path.isdir")
    @patch("os.listdir")
    @patch("os.path.exists")
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_setup_venv_discovers_browser_path_during_fallback(
        self, mock_run, mock_exists, mock_listdir, mock_isdir
    ):
        mock_exists.return_value = False
        mock_isdir.return_value = True
        mock_listdir.return_value = ["chromium-1234"]

        with patch.dict(os.environ, {}, clear=False):
            os.environ.pop("PLAYWRIGHT_BROWSERS_PATH", None)
            self.executor.setup_venv()
            self.assertIn("PLAYWRIGHT_BROWSERS_PATH", os.environ)

    @patch("os.path.isdir")
    @patch("os.listdir")
    @patch("os.path.exists")
    @patch("test.e2e.lib.test_executor.sys")
    def test_setup_venv_uses_prebaked_environment(
        self, mock_sys, mock_exists, mock_listdir, mock_isdir
    ):
        mock_exists.return_value = True
        mock_isdir.return_value = True
        mock_listdir.return_value = ["chromium-1234"]
        mock_sys.executable = "/usr/bin/python3"

        with (
            patch.dict(os.environ, {"GITHUB_ACTIONS": "true"}, clear=False),
            patch.dict("sys.modules", {"playwright": MagicMock(), "requests": MagicMock()}),
        ):
            os.environ.pop("PLAYWRIGHT_BROWSERS_PATH", None)
            py_bin = self.executor.setup_venv()
            self.assertEqual(py_bin, "/usr/bin/python3")
            self.assertIn("PLAYWRIGHT_BROWSERS_PATH", os.environ)

    @patch("os.path.isdir")
    @patch("os.listdir")
    @patch("os.path.exists")
    @patch("test.e2e.lib.test_executor.sys")
    def test_setup_venv_continues_when_first_browser_path_raises_oserror(
        self, mock_sys, mock_exists, mock_listdir, mock_isdir
    ):
        mock_exists.return_value = True
        mock_isdir.return_value = True
        mock_listdir.side_effect = [PermissionError("access denied"), ["chromium-1234"]]
        mock_sys.executable = "/usr/bin/python3"

        with (
            patch.dict(os.environ, {"GITHUB_ACTIONS": "true"}, clear=False),
            patch.dict("sys.modules", {"playwright": MagicMock(), "requests": MagicMock()}),
        ):
            os.environ.pop("PLAYWRIGHT_BROWSERS_PATH", None)
            py_bin = self.executor.setup_venv()
            self.assertEqual(py_bin, "/usr/bin/python3")
            self.assertIn("PLAYWRIGHT_BROWSERS_PATH", os.environ)

    @patch("os.path.isdir")
    @patch("os.listdir")
    @patch("os.path.exists")
    @patch("test.e2e.lib.test_executor.sys")
    def test_setup_venv_uses_browsers_path_env_override(
        self, mock_sys, mock_exists, mock_listdir, mock_isdir
    ):
        mock_exists.return_value = True
        mock_isdir.return_value = True
        mock_listdir.return_value = ["chromium-1234"]
        mock_sys.executable = "/usr/bin/python3"

        from unittest.mock import MagicMock

        with (
            patch.dict(os.environ, {"PLAYWRIGHT_BROWSERS_PATH": "/custom/browsers"}),
            patch.dict("sys.modules", {"playwright": MagicMock(), "requests": MagicMock()}),
        ):
            py_bin = self.executor.setup_venv()
            self.assertEqual(py_bin, "/usr/bin/python3")

    @patch("os.path.exists")
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_setup_venv_skips_pip_install_when_venv_already_exists(self, mock_run, mock_exists):
        # Browser path missing (forces venv path), but venv binary already exists
        def mock_exists_fn(path):
            return path.endswith("python")

        mock_exists.side_effect = mock_exists_fn

        py_bin = self.executor.setup_venv()
        expected_py = os.path.join(self.executor.venv_dir, "bin", "python")
        self.assertEqual(py_bin, expected_py)
        mock_run.assert_not_called()

    @patch("os.listdir", side_effect=PermissionError("Permission denied"))
    @patch("os.path.exists", return_value=True)
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_setup_venv_handles_permission_error_gracefully(
        self, mock_run, mock_exists, mock_listdir
    ):
        from unittest.mock import MagicMock

        with (
            patch.dict("sys.modules", {"playwright": MagicMock(), "requests": MagicMock()}),
        ):
            py_bin = self.executor.setup_venv()
            expected_py = os.path.join(self.executor.venv_dir, "bin", "python")
            self.assertEqual(py_bin, expected_py)

    @patch("test.e2e.lib.test_executor.fetch_dex_oidc_token", return_value="mock.token")
    @patch.object(PlaywrightTestExecutor, "setup_venv", return_value="/mock/venv/bin/python")
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_execute_runs_test_script(self, mock_run, mock_venv, mock_token):
        self.executor.execute()

        test_script = os.path.join("/workspace", "test", "e2e", "test_e2e_flow.py")
        mock_run.assert_called_once()
        args, kwargs = mock_run.call_args
        self.assertEqual(args[0], ["/mock/venv/bin/python", test_script])
        self.assertEqual(kwargs["env"]["XMC_SCREENSHOTS_DIR"], "/screenshots")
        self.assertEqual(kwargs["env"]["XMC_AUTH_TOKEN"], "mock.token")

    @patch("test.e2e.lib.test_executor.fetch_dex_oidc_token", return_value="mock.token")
    @patch.object(PlaywrightTestExecutor, "setup_venv", return_value="/mock/venv/bin/python")
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_execute_with_custom_test_script(self, mock_run, mock_venv, mock_token):
        custom_script = "/custom/path/to/test.py"
        self.executor.execute(test_script=custom_script)

        mock_run.assert_called_once()
        args, kwargs = mock_run.call_args
        self.assertEqual(args[0], ["/mock/venv/bin/python", custom_script])
        self.assertEqual(kwargs["cwd"], "/workspace")
        self.assertEqual(kwargs["env"]["XMC_AUTH_TOKEN"], "mock.token")

    @patch("os.path.exists", return_value=True)
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_ensure_xmanager_installed_runs_for_workload_tier_only(self, mock_run, mock_exists):
        import subprocess

        from test.e2e.lib.config import E2ETier

        # UI tier should not invoke install_xmanager.py
        self.executor._ensure_xmanager_installed("/mock/python")
        mock_run.assert_not_called()

        # WORKLOAD tier should probe import and invoke install_xmanager.py on failure
        self.cfg.tier = E2ETier.WORKLOAD
        mock_run.side_effect = [
            subprocess.CalledProcessError(returncode=1, cmd=["python", "-c"]),
            None,
        ]
        self.executor._ensure_xmanager_installed("/mock/python")
        self.assertEqual(mock_run.call_count, 2)

    @patch("os.path.exists", return_value=False)
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_setup_venv_falls_back_to_venv_for_workload_tier_when_deps_missing(
        self, mock_run, mock_exists
    ):
        from test.e2e.lib.config import E2ETier

        self.cfg.tier = E2ETier.WORKLOAD
        with (
            patch.dict(os.environ, {"PLAYWRIGHT_BROWSERS_PATH": "/custom/browsers"}),
            patch.dict(
                "sys.modules",
                {"playwright": MagicMock(), "requests": MagicMock(), "xmanager": None},
            ),
        ):
            py_bin = self.executor.setup_venv()
            expected_py = os.path.join(self.executor.venv_dir, "bin", "python")
            self.assertEqual(py_bin, expected_py)


if __name__ == "__main__":
    unittest.main()
