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

"""Unit tests for wait_for_gemini_review."""

import subprocess
import unittest
from unittest import mock

from tools.pr_review import wait_for_gemini_review


class WaitForGeminiReviewTest(unittest.TestCase):
    @mock.patch("subprocess.run")
    def test_run_gh_json_success(self, mock_run: mock.MagicMock) -> None:
        mock_run.return_value = subprocess.CompletedProcess(
            args=[], returncode=0, stdout='{"key": "value"}'
        )
        result = wait_for_gemini_review.run_gh_json(["gh", "api"])
        self.assertEqual(result, {"key": "value"})

    @mock.patch("subprocess.run")
    def test_run_gh_json_error(self, mock_run: mock.MagicMock) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(1, ["gh"])
        result = wait_for_gemini_review.run_gh_json(["gh", "api"])
        self.assertIsNone(result)

    @mock.patch("tools.pr_review.wait_for_gemini_review.run_gh_json")
    def test_get_latest_review_trigger_time_found(self, mock_gh: mock.MagicMock) -> None:
        mock_gh.return_value = "2026-09-02T07:50:05Z"
        result = wait_for_gemini_review.get_latest_review_trigger_time("google", "xmc", 622)
        self.assertEqual(result, "2026-09-02T07:50:05Z")

    @mock.patch("tools.pr_review.wait_for_gemini_review.run_gh_json")
    def test_get_latest_review_trigger_time_none(self, mock_gh: mock.MagicMock) -> None:
        mock_gh.return_value = ""
        result = wait_for_gemini_review.get_latest_review_trigger_time("google", "xmc", 622)
        self.assertIsNone(result)

    @mock.patch("tools.pr_review.wait_for_gemini_review.run_gh_json")
    def test_get_matching_review_matches_timestamp(self, mock_gh: mock.MagicMock) -> None:
        mock_gh.return_value = [
            {
                "id": 1,
                "user": {"login": "gemini-code-assist[bot]"},
                "submitted_at": "2026-09-02T07:30:00Z",
                "state": "COMMENTED",
                "body": "Old review",
            },
            {
                "id": 2,
                "user": {"login": "gemini-code-assist[bot]"},
                "submitted_at": "2026-09-02T07:55:00Z",
                "state": "COMMENTED",
                "body": "Latest review",
                "html_url": "https://example.com/2",
            },
        ]
        review = wait_for_gemini_review.get_matching_review(
            "google", "xmc", 622, "2026-09-02T07:50:00Z"
        )
        self.assertIsNotNone(review)
        assert review is not None
        self.assertEqual(review["id"], 2)
        self.assertEqual(review["body"], "Latest review")

    @mock.patch("tools.pr_review.wait_for_gemini_review.run_gh_json")
    def test_get_matching_review_older_than_since_returns_none(
        self, mock_gh: mock.MagicMock
    ) -> None:
        mock_gh.return_value = [
            {
                "id": 1,
                "user": {"login": "gemini-code-assist[bot]"},
                "submitted_at": "2026-09-02T07:30:00Z",
                "state": "COMMENTED",
            }
        ]
        review = wait_for_gemini_review.get_matching_review(
            "google", "xmc", 622, "2026-09-02T07:50:00Z"
        )
        self.assertIsNone(review)

    @mock.patch("time.sleep")
    @mock.patch("tools.pr_review.wait_for_gemini_review.get_matching_review")
    @mock.patch("tools.pr_review.wait_for_gemini_review.get_latest_review_trigger_time")
    def test_wait_for_gemini_review_success_on_second_poll(
        self, mock_trigger: mock.MagicMock, mock_review: mock.MagicMock, mock_sleep: mock.MagicMock
    ) -> None:
        mock_trigger.return_value = "2026-09-02T07:50:00Z"
        mock_review.side_effect = [
            None,
            {"id": 123, "user": "gemini-code-assist[bot]", "submitted_at": "2026-09-02T07:55:00Z"},
        ]
        res = wait_for_gemini_review.wait_for_gemini_review(
            "google", "xmc", 622, timeout_seconds=60, interval_seconds=5
        )
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["result"]["id"], 123)
        mock_sleep.assert_called_once_with(5)

    @mock.patch("time.sleep")
    @mock.patch("tools.pr_review.wait_for_gemini_review.get_matching_review", return_value=None)
    @mock.patch(
        "tools.pr_review.wait_for_gemini_review.get_latest_review_trigger_time", return_value=None
    )
    def test_wait_for_gemini_review_timeout(
        self, mock_trigger: mock.MagicMock, mock_review: mock.MagicMock, mock_sleep: mock.MagicMock
    ) -> None:
        res = wait_for_gemini_review.wait_for_gemini_review(
            "google", "xmc", 622, timeout_seconds=0, interval_seconds=1
        )
        self.assertEqual(res["status"], "error")
        self.assertIn("Timed out", res["message"])

    @mock.patch(
        "tools.pr_review.wait_for_gemini_review.wait_for_gemini_review",
        return_value={"status": "success", "result": {"id": 10}},
    )
    def test_main_success(self, mock_wait: mock.MagicMock) -> None:
        with mock.patch("sys.argv", ["wait_for_gemini_review.py", "google", "xmc", "622"]):
            code = wait_for_gemini_review.main()
        self.assertEqual(code, 0)

    @mock.patch(
        "tools.pr_review.wait_for_gemini_review.wait_for_gemini_review",
        return_value={"status": "error", "message": "Timed out"},
    )
    def test_main_error(self, mock_wait: mock.MagicMock) -> None:
        with mock.patch("sys.argv", ["wait_for_gemini_review.py", "google", "xmc", "622"]):
            code = wait_for_gemini_review.main()
        self.assertEqual(code, 1)


if __name__ == "__main__":
    unittest.main()
