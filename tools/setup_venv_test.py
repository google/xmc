#!/usr/bin/env python3
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

"""Unit tests for tools/setup_venv.py."""

import io
import tempfile
import unittest
from pathlib import Path
from unittest.mock import call, patch

import setup_venv


class SetupVenvTest(unittest.TestCase):
    @patch("sys.exit")
    @patch("sys.stderr", new_callable=io.StringIO)
    def test_error_exit(self, mock_stderr, mock_exit):
        setup_venv.error_exit("Test failure")
        self.assertIn("Error: Test failure\n", mock_stderr.getvalue())
        mock_exit.assert_called_once_with(1)

    @patch("shutil.which")
    def test_check_prerequisites_success(self, mock_which):
        mock_which.return_value = "/usr/bin/python3"
        setup_venv.check_prerequisites()
        mock_which.assert_called_once_with("python3")

    @patch("shutil.which")
    @patch("setup_venv.error_exit")
    def test_check_prerequisites_missing(self, mock_error_exit, mock_which):
        mock_which.return_value = None
        mock_error_exit.side_effect = SystemExit(1)
        with self.assertRaises(SystemExit):
            setup_venv.check_prerequisites()
        mock_error_exit.assert_called_once_with("python3 is required but not installed.")

    @patch("venv.create")
    def test_setup_virtualenv_creates_when_missing(self, mock_venv_create):
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir) / "venv"
            python_bin = setup_venv.setup_virtualenv(venv_dir)
            mock_venv_create.assert_called_once_with(venv_dir, with_pip=True)
            self.assertEqual(python_bin, venv_dir / "bin" / "python")

    @patch("venv.create")
    def test_setup_virtualenv_skips_when_exists(self, mock_venv_create):
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir)
            python3_bin = venv_dir / "bin" / "python3"
            python3_bin.parent.mkdir(parents=True, exist_ok=True)
            python3_bin.touch()

            python_bin = setup_venv.setup_virtualenv(venv_dir)
            mock_venv_create.assert_not_called()
            self.assertEqual(python_bin, python3_bin)

    @patch("setup_venv.error_exit")
    def test_install_requirements_missing_file(self, mock_error_exit):
        mock_error_exit.side_effect = SystemExit(1)
        with tempfile.TemporaryDirectory() as tmp_dir:
            missing_req = Path(tmp_dir) / "missing.txt"
            with self.assertRaises(SystemExit):
                setup_venv.install_requirements(Path("/mock/python3"), missing_req)
            mock_error_exit.assert_called_once_with(
                f"Requirements file not found at: {missing_req}"
            )

    @patch("subprocess.run")
    def test_install_requirements_success(self, mock_run):
        with tempfile.TemporaryDirectory() as tmp_dir:
            req_file = Path(tmp_dir) / "requirements.txt"
            req_file.touch()
            venv_python = Path("/mock/venv/bin/python3")

            setup_venv.install_requirements(venv_python, req_file)
            mock_run.assert_has_calls(
                [
                    call(
                        [str(venv_python), "-m", "pip", "install", "--upgrade", "pip"],
                        check=True,
                    ),
                    call(
                        [str(venv_python), "-m", "pip", "install", "-r", str(req_file)],
                        check=True,
                    ),
                ]
            )

    @patch("setup_venv.install_requirements")
    @patch("setup_venv.setup_virtualenv")
    @patch("setup_venv.check_prerequisites")
    @patch("sys.stdout", new_callable=io.StringIO)
    def test_main(
        self,
        mock_stdout,
        mock_check_prereqs,
        mock_setup_venv,
        mock_install_reqs,
    ):
        mock_setup_venv.return_value = Path("/mock/venv/bin/python3")
        with tempfile.TemporaryDirectory() as tmp_dir:
            venv_dir = Path(tmp_dir) / "my_venv"
            req_file = Path(tmp_dir) / "reqs.txt"
            req_file.touch()

            setup_venv.main(
                [
                    "--venv-dir",
                    str(venv_dir),
                    "--requirements",
                    str(req_file),
                ]
            )

            mock_check_prereqs.assert_called_once()
            mock_setup_venv.assert_called_once_with(venv_dir.resolve())
            mock_install_reqs.assert_called_once_with(
                Path("/mock/venv/bin/python3"), req_file.resolve()
            )
            self.assertIn("Tooling virtual environment ready at:", mock_stdout.getvalue())


if __name__ == "__main__":
    unittest.main()
