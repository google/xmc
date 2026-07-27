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

"""Unit tests for teardown_benchmark tool."""

import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from teardown_benchmark import teardown_resources


class TeardownBenchmarkTest(unittest.TestCase):
    """Test suite for teardown_benchmark module functions."""

    @patch("subprocess.run")
    def test_teardown_resources_success(self, mock_run):
        mock_res = MagicMock()
        mock_res.returncode = 0
        mock_run.return_value = mock_res

        with tempfile.NamedTemporaryFile(delete=False) as f:
            tmp_path = f.name

        res = teardown_resources(
            prs=["https://github.com/google/xmc/pull/1"],
            branches=["test/branch-1"],
            temp_files=[tmp_path],
        )

        self.assertTrue(res["success"])
        self.assertIn("https://github.com/google/xmc/pull/1", res["cleaned_prs"])
        self.assertIn("test/branch-1", res["cleaned_branches"])
        self.assertFalse(Path(tmp_path).exists())

    @patch("subprocess.run")
    def test_teardown_resources_failed_pr(self, mock_run):
        mock_res = MagicMock()
        mock_res.returncode = 1
        mock_res.stderr = "PR not found"
        mock_run.return_value = mock_res

        res = teardown_resources(
            prs=["https://github.com/google/xmc/pull/999"],
            branches=[],
            temp_files=[],
        )

        self.assertFalse(res["success"])
        self.assertEqual(len(res["failed_prs"]), 1)


if __name__ == "__main__":
    unittest.main()
