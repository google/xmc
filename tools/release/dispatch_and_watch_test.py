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

"""Unit tests for tools.release.dispatch_and_watch."""

import json
import subprocess
import unittest
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.release.dispatch_and_watch import (
    EXIT_CANCELLED,
    EXIT_SHA_MISMATCH,
    EXIT_SUCCESS,
    create_parser,
    dispatch_and_watch,
)


class DispatchAndWatchTest(unittest.TestCase):
    """Focused test suite for dispatch_and_watch."""

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value=None)
    def test_gh_missing(self, mock_which):
        p = create_parser().parse_args(["--workflow", "build.yml"])
        with self.assertRaises(HandledError):
            dispatch_and_watch(p)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_find_existing_success(self, mock_run, mock_which):
        run_list = json.dumps(
            [
                {
                    "databaseId": 101,
                    "headSha": "abc12345",
                    "url": "https://github.com/google/xmc/actions/runs/101",
                }
            ]
        )
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout=run_list, stderr=""
        )
        p = create_parser().parse_args(
            [
                "--workflow",
                "build.yml",
                "--find-existing",
                "--target-sha",
                "abc12345",
            ]
        )
        res = dispatch_and_watch(p)
        self.assertEqual(res["run_id"], 101)
        self.assertEqual(res["conclusion"], "success")
        self.assertEqual(res["_exit_code"], EXIT_SUCCESS)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_dispatch_and_watch_success(self, mock_run, mock_which):
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps([{"databaseId": 202, "headSha": "target_sha", "url": "run_url"}]),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps({"conclusion": "success", "jobs": []}),
            ),  # view
        ]
        p = create_parser().parse_args(["--workflow", "build.yml", "--target-sha", "target_sha"])
        res = dispatch_and_watch(p)
        self.assertEqual(res["run_id"], 202)
        self.assertEqual(res["conclusion"], "success")
        self.assertEqual(res["_exit_code"], EXIT_SUCCESS)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_dispatch_sha_mismatch(self, mock_run, mock_which):
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps([{"databaseId": 303, "headSha": "wrong_sha", "url": "run_url"}]),
            ),  # list
        ]
        p = create_parser().parse_args(["--workflow", "build.yml", "--target-sha", "expected_sha"])
        res = dispatch_and_watch(p)
        self.assertEqual(res["conclusion"], "sha_mismatch")
        self.assertEqual(res["_exit_code"], EXIT_SHA_MISMATCH)

    @patch("tools.release.dispatch_and_watch.shutil.which", return_value="/bin/gh")
    @patch("tools.release.dispatch_and_watch.subprocess.run")
    def test_dispatch_cancelled(self, mock_run, mock_which):
        mock_run.side_effect = [
            subprocess.CompletedProcess(args=[], returncode=0, stdout=""),  # dispatch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps([{"databaseId": 404, "headSha": "sha", "url": "run_url"}]),
            ),  # list
            subprocess.CompletedProcess(args=[], returncode=1, stdout=""),  # watch
            subprocess.CompletedProcess(
                args=[],
                returncode=0,
                stdout=json.dumps({"conclusion": "cancelled", "jobs": []}),
            ),  # view
        ]
        p = create_parser().parse_args(["--workflow", "build.yml", "--target-sha", "sha"])
        res = dispatch_and_watch(p)
        self.assertEqual(res["conclusion"], "cancelled")
        self.assertEqual(res["_exit_code"], EXIT_CANCELLED)


if __name__ == "__main__":
    unittest.main()
