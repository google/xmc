# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
"""Tests for the Angular UI unit test runner."""

import io
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

_DIR = os.path.dirname(os.path.abspath(__file__))
if _DIR not in sys.path:
    sys.path.insert(0, _DIR)

import run_ui_tests  # noqa: E402


class MissingDependenciesErrorTest(unittest.TestCase):
    """Tests for run_ui_tests.missing_dependencies_error."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.ui_dir = tmp.name

    def test_returns_none_when_angular_cli_is_installed(self):
        bin_dir = Path(self.ui_dir, "node_modules", ".bin")
        bin_dir.mkdir(parents=True)
        (bin_dir / "ng").touch()

        self.assertIsNone(run_ui_tests.missing_dependencies_error(self.ui_dir))

    def test_names_npm_ci_when_node_modules_is_missing(self):
        error = run_ui_tests.missing_dependencies_error(self.ui_dir)

        self.assertEqual(
            error,
            f"The UI dependencies are not installed: {self.ui_dir}/node_modules/.bin/ng is "
            f"missing. Run `cd {self.ui_dir} && npm ci --legacy-peer-deps`, then re-run the "
            "UI tests.",
        )

    def test_reports_partial_install_without_angular_cli(self):
        Path(self.ui_dir, "node_modules").mkdir()

        self.assertIsNotNone(run_ui_tests.missing_dependencies_error(self.ui_dir))


class MainTest(unittest.TestCase):
    """Tests for run_ui_tests.main."""

    @mock.patch.object(run_ui_tests.subprocess, "run")
    @mock.patch.object(run_ui_tests, "missing_dependencies_error", return_value="install deps")
    def test_exits_before_running_tests_when_dependencies_are_missing(self, _, mock_run):
        stderr = io.StringIO()
        with mock.patch.object(sys, "stderr", stderr), self.assertRaises(SystemExit) as cm:
            run_ui_tests.main()

        self.assertEqual(cm.exception.code, 1)
        self.assertEqual(stderr.getvalue(), "install deps\n")
        mock_run.assert_not_called()

    @mock.patch.object(run_ui_tests.subprocess, "run")
    @mock.patch.object(run_ui_tests, "missing_dependencies_error", return_value=None)
    def test_exits_with_ng_test_return_code_when_dependencies_are_installed(self, _, mock_run):
        mock_run.return_value.returncode = 3
        with mock.patch.object(sys, "stdout", io.StringIO()), self.assertRaises(SystemExit) as cm:
            run_ui_tests.main()

        self.assertEqual(cm.exception.code, 3)
        mock_run.assert_called_once()
        cmd_args = mock_run.call_args.args[0]
        joined = cmd_args if isinstance(cmd_args, str) else " ".join(cmd_args)
        self.assertIn("ng test --watch=false", joined)


if __name__ == "__main__":
    unittest.main()
