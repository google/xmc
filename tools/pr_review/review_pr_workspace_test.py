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

"""Unit tests for review_pr_workspace."""

import json
import subprocess
import tempfile
import unittest
from pathlib import Path
from typing import Any
from unittest.mock import patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError
from tools.pr_review.review_pr_workspace import (
    build_pr_meta,
    checkout_pr_head,
    find_last_reviewed_sha,
    measure_delta,
    measure_rereview_delta,
    prepare_review_workspace,
)

_MODULE = "tools.pr_review.review_pr_workspace"


def _data(**pr_overrides: Any) -> dict[str, Any]:
    """Builds baseline PR audit data; `pr_overrides` replace `pr_info` keys."""
    pr_info = {
        "title": "feat: x",
        "user": {"login": "author"},
        "base": {"ref": "main", "sha": "base1"},
        "head": {"ref": "feat/x", "sha": "head1"},
    } | pr_overrides
    return {
        "pr_info": pr_info,
        "files": [{"filename": "a.py", "status": "modified", "additions": 3, "deletions": 1}],
        "comments": [],
        "threads": [{"id": "PRRT_1", "isResolved": False}],
    }


class FakeGit:
    """Records git invocations and serves canned stdout per git subcommand."""

    def __init__(self, status: str = "", diff: str = "diff --git a/a.py b/a.py\n"):
        self.calls: list[list[str]] = []
        self._stdout = {"status": status, "diff": diff}

    def __call__(self, cmd: list[str], **kwargs: Any) -> subprocess.CompletedProcess:
        self.calls.append(cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout=self._stdout.get(cmd[1], ""))


class TestCheckoutPrHead(unittest.TestCase):
    """Tests for checkout_pr_head."""

    def test_clean_worktree_checks_out_detached(self):
        git = FakeGit()
        with patch(f"{_MODULE}.run_command", git):
            self.assertEqual(checkout_pr_head("head1"), {"checked_out": True})
        self.assertEqual(git.calls[-1], ["git", "checkout", "--detach", "head1"])

    def test_dirty_worktree_returns_command_without_checkout(self):
        git = FakeGit(status=" M tools/a.py\n")
        with patch(f"{_MODULE}.run_command", git):
            result = checkout_pr_head("head1")
        self.assertFalse(result["checked_out"])
        self.assertEqual(result["checkout_command"], "git checkout --detach head1")
        self.assertNotIn("checkout", [c[1] for c in git.calls])


class TestPrepareReviewWorkspace(unittest.TestCase):
    """Tests for prepare_review_workspace."""

    def test_writes_inputs_and_checks_out(self):
        git = FakeGit()
        delta = {"since_sha": "old1", "changed_lines": 4}
        with tempfile.TemporaryDirectory() as tmpdir, patch(f"{_MODULE}.run_command", git):
            out = Path(tmpdir) / "session"
            result = prepare_review_workspace(
                "google", "xmc", 7, _data(), out, checkout=True, rereview_delta=delta
            )
            self.assertEqual(
                (out / "pr.diff").read_text(encoding="utf-8"), "diff --git a/a.py b/a.py\n"
            )
            meta = json.loads((out / "pr_meta.json").read_text(encoding="utf-8"))
            threads = json.loads((out / "review_threads.json").read_text(encoding="utf-8"))
        self.assertEqual(
            git.calls[0],
            ["git", "fetch", "--no-tags", "origin", "pull/7/head", "base1"],
        )
        self.assertIn(["git", "diff", "base1...head1"], git.calls)
        self.assertEqual(git.calls[-1], ["git", "checkout", "--detach", "head1"])
        self.assertEqual(meta["base_sha"], "base1")
        self.assertEqual(meta["head_sha"], "head1")
        self.assertEqual(meta["rereview_delta"], delta)
        self.assertEqual(threads, [{"id": "PRRT_1", "isResolved": False}])
        self.assertTrue(result["checked_out"])
        self.assertTrue(result["diff_file"].endswith("pr.diff"))

    def test_without_checkout_leaves_head_alone(self):
        git = FakeGit()
        with tempfile.TemporaryDirectory() as tmpdir, patch(f"{_MODULE}.run_command", git):
            result = prepare_review_workspace("google", "xmc", 7, _data(), Path(tmpdir))
            meta = json.loads((Path(tmpdir) / "pr_meta.json").read_text(encoding="utf-8"))
        self.assertFalse(result["checked_out"])
        self.assertNotIn("checkout", [c[1] for c in git.calls])
        self.assertNotIn("rereview_delta", meta)

    def test_missing_shas_raise_before_git(self):
        git = FakeGit()
        with tempfile.TemporaryDirectory() as tmpdir, patch(f"{_MODULE}.run_command", git):
            with self.assertRaises(HandledError):
                prepare_review_workspace("google", "xmc", 7, _data(head={}), Path(tmpdir))
        self.assertEqual(git.calls, [])


class TestBuildPrMeta(unittest.TestCase):
    """Tests for build_pr_meta."""

    def test_meta_fields(self):
        meta = build_pr_meta("google", "xmc", 7, _data())
        self.assertEqual(meta["author"], "author")
        self.assertEqual(meta["base_ref"], "main")
        self.assertEqual(meta["head_ref"], "feat/x")
        self.assertEqual(
            meta["files"],
            [{"filename": "a.py", "status": "modified", "additions": 3, "deletions": 1}],
        )


class TestReReviewDelta(unittest.TestCase):
    """Tests for find_last_reviewed_sha, measure_delta, and measure_rereview_delta."""

    @patch(f"{_MODULE}.list_all_pages")
    @patch(f"{_MODULE}.get_authenticated_login", return_value="me")
    def test_last_reviewed_sha_is_latest_own_review(self, _login, mock_pages):
        mock_pages.return_value = [
            {"user": {"login": "me"}, "commit_id": "c1"},
            {"user": {"login": "other"}, "commit_id": "c2"},
            {"user": {"login": "me"}, "commit_id": "c3"},
            {"user": {"login": "me"}, "commit_id": None},
        ]
        self.assertEqual(find_last_reviewed_sha("google", "xmc", 7), "c3")
        self.assertEqual(mock_pages.call_args.args[0], "/repos/google/xmc/pulls/7/reviews")

    @patch(f"{_MODULE}.list_all_pages", return_value=[])
    @patch(f"{_MODULE}.get_authenticated_login", return_value="me")
    def test_no_earlier_review_recommends_full_review(self, _login, _pages):
        delta = measure_rereview_delta("google", "xmc", 7, "head1")
        self.assertIsNone(delta["since_sha"])
        self.assertIsNone(delta["changed_lines"])
        self.assertTrue(delta["full_review_recommended"])

    @patch(f"{_MODULE}.get_authenticated_login", side_effect=GithubAPIError("no auth"))
    def test_login_failure_recommends_full_review(self, _login):
        delta = measure_rereview_delta("google", "xmc", 7, "head1")
        self.assertTrue(delta["full_review_recommended"])
        self.assertIn("no auth", delta["reason"])

    @patch(f"{_MODULE}.call_github_api")
    def test_delta_below_and_above_threshold(self, mock_api):
        mock_api.return_value = {
            "files": [
                {"filename": "a.py", "additions": 100, "deletions": 50},
                {"filename": "b.py", "additions": 50, "deletions": 0},
            ]
        }
        small = measure_delta("google", "xmc", "old1", "head1", threshold=200)
        large = measure_delta("google", "xmc", "old1", "head1", threshold=199)
        self.assertEqual(mock_api.call_args.args[0], "/repos/google/xmc/compare/old1...head1")
        self.assertEqual(small["changed_lines"], 200)
        self.assertEqual(small["changed_files"], ["a.py", "b.py"])
        self.assertFalse(small["full_review_recommended"])
        self.assertTrue(large["full_review_recommended"])

    @patch(f"{_MODULE}.get_authenticated_login")
    @patch(f"{_MODULE}.call_github_api", side_effect=GithubAPIError("404 Not Found"))
    def test_uncomparable_since_sha_recommends_full_review(self, _api, mock_login):
        delta = measure_rereview_delta("google", "xmc", 7, "head1", since_sha="gone1")
        mock_login.assert_not_called()
        self.assertEqual(delta["since_sha"], "gone1")
        self.assertIsNone(delta["changed_lines"])
        self.assertTrue(delta["full_review_recommended"])


if __name__ == "__main__":
    unittest.main()
