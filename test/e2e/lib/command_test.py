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

"""Unit tests for subprocess execution command helper."""

import subprocess
import unittest
from unittest.mock import patch

from test.e2e.lib.command import run_cmd


class TestRunCmd(unittest.TestCase):
    """Tests for run_cmd utility."""

    @patch("subprocess.run")
    def test_run_cmd_invokes_subprocess(self, mock_run):
        mock_res = subprocess.CompletedProcess(
            args=["echo", "hello"],
            returncode=0,
            stdout="hello\n",
            stderr="",
        )
        mock_run.return_value = mock_res

        res = run_cmd(
            ["echo", "hello"],
            cwd="/tmp",
            env={"KEY": "VAL"},
            timeout=10.0,
            check=True,
            capture_output=True,
        )

        mock_run.assert_called_once_with(
            ["echo", "hello"],
            cwd="/tmp",
            env={"KEY": "VAL"},
            timeout=10.0,
            check=True,
            capture_output=True,
            text=True,
        )
        self.assertEqual(res.stdout, "hello\n")


if __name__ == "__main__":
    unittest.main()
