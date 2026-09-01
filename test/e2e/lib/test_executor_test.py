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
from unittest.mock import patch

from test.e2e.lib.config import E2EConfig
from test.e2e.lib.test_executor import PlaywrightTestExecutor


class TestPlaywrightTestExecutor(unittest.TestCase):
    """Tests for PlaywrightTestExecutor."""

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
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
                "playwright",
                "requests",
            ]
        )

    @patch("os.listdir")
    @patch("os.path.exists")
    @patch("test.e2e.lib.test_executor.sys")
    def test_setup_venv_uses_prebaked_environment(self, mock_sys, mock_exists, mock_listdir):
        mock_exists.return_value = True
        mock_listdir.return_value = ["chromium-1234"]
        mock_sys.executable = "/usr/bin/python3"

        from unittest.mock import MagicMock

        with patch.dict("sys.modules", {"playwright": MagicMock(), "requests": MagicMock()}):
            py_bin = self.executor.setup_venv()
            self.assertEqual(py_bin, "/usr/bin/python3")

    @patch.object(PlaywrightTestExecutor, "setup_venv", return_value="/mock/venv/bin/python")
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_execute_runs_test_script(self, mock_run, mock_venv):
        self.executor.execute()

        test_script = os.path.join("/workspace", "test", "e2e", "test_e2e_flow.py")
        mock_run.assert_called_once()
        args, kwargs = mock_run.call_args
        self.assertEqual(args[0], ["/mock/venv/bin/python", test_script])
        self.assertEqual(kwargs["env"]["XMC_SCREENSHOTS_DIR"], "/screenshots")

    @patch.object(PlaywrightTestExecutor, "setup_venv", return_value="/mock/venv/bin/python")
    @patch("test.e2e.lib.test_executor.run_cmd")
    def test_execute_with_custom_test_script(self, mock_run, mock_venv):
        custom_script = "/custom/path/to/test.py"
        self.executor.execute(test_script=custom_script)

        mock_run.assert_called_once()
        args, kwargs = mock_run.call_args
        self.assertEqual(args[0], ["/mock/venv/bin/python", custom_script])
        self.assertEqual(kwargs["cwd"], "/workspace")


if __name__ == "__main__":
    unittest.main()
