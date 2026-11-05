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
import os
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
    """Records git invocations and serves canned stdout per git subcommand.

    Leading `-c key=value` pairs are stripped before dispatch: `calls` holds the
    stripped argv and `raw_calls` the argv as passed. `git cat-file -e <sha>^{commit}`
    fails for SHAs in `missing`. Subcommands in `fail` exit non-zero, raised like
    `run_command` does: as `HandledError` when a `handled_instruction` is given, else
    as `CalledProcessError`.
    """

    def __init__(
        self,
        status: str = "",
        diff: str = "diff --git a/a.py b/a.py\n",
        missing: frozenset[str] = frozenset(),
        fail: frozenset[str] = frozenset(),
    ):
        self.calls: list[list[str]] = []
        self.raw_calls: list[list[str]] = []
        self._stdout = {"status": status, "diff": diff}
        self._missing = missing
        self._fail = fail

    def __call__(self, raw: list[str], **kwargs: Any) -> subprocess.CompletedProcess:
        self.raw_calls.append(raw)
        cmd = raw[:1]
        rest = raw[1:]
        while rest[:1] == ["-c"]:
            rest = rest[2:]
        cmd += rest
        self.calls.append(cmd)
        if cmd[1] == "cat-file":
            code = 1 if cmd[3].removesuffix("^{commit}") in self._missing else 0
            return subprocess.CompletedProcess(cmd, code, stdout="")
        if cmd[1] in self._fail:
            if kwargs.get("handled_instruction") is not None:
                raise HandledError(f"Command failed with exit code 1: {' '.join(cmd)}", "fix")
            raise subprocess.CalledProcessError(1, cmd)
        return subprocess.CompletedProcess(cmd, 0, stdout=self._stdout.get(cmd[1], ""))

    def subcommands(self) -> list[str]:
        """Returns the git subcommands run, in order, excluding object lookups."""
        return [c[1] for c in self.calls if c[1] != "cat-file"]


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
        self.assertEqual(
            result["checkout_command"], "git -c core.hooksPath=/dev/null checkout --detach head1"
        )
        self.assertNotIn("checkout", git.subcommands())

    def test_refused_checkout_returns_command_instead_of_raising(self):
        git = FakeGit(fail=frozenset({"checkout"}))
        with patch(f"{_MODULE}.run_command", git):
            result = checkout_pr_head("head1")
        self.assertFalse(result["checked_out"])
        self.assertEqual(
            result["checkout_command"], "git -c core.hooksPath=/dev/null checkout --detach head1"
        )
        self.assertIn("git refused the checkout", result["reason"])

    def test_every_git_call_disables_hooks(self):
        git = FakeGit()
        with patch(f"{_MODULE}.run_command", git):
            checkout_pr_head("head1")
        self.assertEqual(len(git.raw_calls), 2)
        for raw in git.raw_calls:
            self.assertEqual(raw[:3], ["git", "-c", "core.hooksPath=/dev/null"])


_HOOK_SCRIPT = """#!/bin/sh
echo ran > "{marker}"
exit 1
"""


class TestCheckoutPrHeadRealGit(unittest.TestCase):
    """Regression: PR-authored hooks under a relative core.hooksPath never run."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.repo = self.root / "repo"
        self.marker = self.root / "HOOK_RAN"
        env = patch.dict(
            "os.environ",
            {"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1"},
        )
        env.start()
        self.addCleanup(env.stop)
        self.base_sha, self.pr_sha = self._make_repo()
        cwd = Path.cwd()
        self.addCleanup(os.chdir, cwd)
        os.chdir(self.repo)

    def _run(self, *args: str, check: bool = True) -> subprocess.CompletedProcess:
        cmd = ["git", "-c", "user.name=t", "-c", "user.email=t@example.com", *args]
        return subprocess.run(cmd, cwd=self.repo, check=check, capture_output=True, text=True)

    def _make_repo(self) -> tuple[str, str]:
        """Creates a base commit and a PR commit adding a failing post-checkout hook."""
        self.repo.mkdir()
        self._run("init", "-q")
        (self.repo / "a.txt").write_text("base\n", encoding="utf-8")
        self._run("add", "a.txt")
        self._run("commit", "-q", "-m", "base")
        base = self._run("rev-parse", "HEAD").stdout.strip()
        hook = self.repo / ".githooks" / "post-checkout"
        hook.parent.mkdir()
        hook.write_text(_HOOK_SCRIPT.format(marker=self.marker), encoding="utf-8")
        hook.chmod(0o755)
        self._run("add", ".githooks")
        self._run("commit", "-q", "-m", "pr adds hook")
        pr = self._run("rev-parse", "HEAD").stdout.strip()
        self._run("-c", "core.hooksPath=/dev/null", "checkout", "-q", "--detach", base)
        self._run("config", "core.hooksPath", ".githooks")
        return base, pr

    def test_fixture_hook_runs_under_plain_checkout(self):
        """Control: a plain checkout runs the PR's hook and reports failure."""
        proc = self._run("checkout", "-q", "--detach", self.pr_sha, check=False)
        self.assertTrue(self.marker.exists())
        self.assertNotEqual(proc.returncode, 0)

    def test_checkout_pr_head_does_not_run_pr_hooks(self):
        result = checkout_pr_head(self.pr_sha)
        self.assertEqual(result, {"checked_out": True})
        self.assertFalse(self.marker.exists())
        self.assertEqual(self._run("rev-parse", "HEAD").stdout.strip(), self.pr_sha)


class TestPrepareReviewWorkspace(unittest.TestCase):
    """Tests for prepare_review_workspace."""

    def test_writes_inputs_and_checks_out(self):
        git = FakeGit(missing=frozenset({"base1"}))
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
        self.assertIn(
            ["git", "fetch", "--no-tags", "origin", "pull/7/head", "base1"],
            git.calls,
        )
        self.assertEqual(git.subcommands(), ["fetch", "diff", "status", "checkout"])
        self.assertIn(["git", "diff", "base1...head1"], git.calls)
        self.assertEqual(git.calls[-1], ["git", "checkout", "--detach", "head1"])
        self.assertEqual(meta["base_sha"], "base1")
        self.assertEqual(meta["head_sha"], "head1")
        self.assertEqual(meta["rereview_delta"], delta)
        self.assertEqual(threads, [{"id": "PRRT_1", "isResolved": False}])
        self.assertTrue(result["checked_out"])
        self.assertTrue(result["diff_file"].endswith("pr.diff"))

    def test_local_base_is_not_fetched_by_sha(self):
        git = FakeGit()
        with tempfile.TemporaryDirectory() as tmpdir, patch(f"{_MODULE}.run_command", git):
            prepare_review_workspace("google", "xmc", 7, _data(), Path(tmpdir))
        self.assertIn(["git", "fetch", "--no-tags", "origin", "pull/7/head"], git.calls)

    def test_head_moved_during_fetch_raises_before_diff(self):
        git = FakeGit(missing=frozenset({"head1"}))
        with tempfile.TemporaryDirectory() as tmpdir, patch(f"{_MODULE}.run_command", git):
            with self.assertRaisesRegex(HandledError, "PR changed during the run"):
                prepare_review_workspace("google", "xmc", 7, _data(), Path(tmpdir))
            self.assertFalse((Path(tmpdir) / "pr.diff").exists())
        self.assertEqual(git.subcommands(), ["fetch"])

    def test_without_checkout_leaves_head_alone(self):
        git = FakeGit()
        with tempfile.TemporaryDirectory() as tmpdir, patch(f"{_MODULE}.run_command", git):
            result = prepare_review_workspace("google", "xmc", 7, _data(), Path(tmpdir))
            meta = json.loads((Path(tmpdir) / "pr_meta.json").read_text(encoding="utf-8"))
        self.assertFalse(result["checked_out"])
        self.assertNotIn("checkout", git.subcommands())
        self.assertNotIn("rereview_delta", meta)
        self.assertNotIn("threads_error", meta)

    def test_threads_error_is_recorded_in_meta(self):
        data = _data() | {"threads": [], "threads_error": "GraphQL down"}
        with tempfile.TemporaryDirectory() as tmpdir, patch(f"{_MODULE}.run_command", FakeGit()):
            prepare_review_workspace("google", "xmc", 7, data, Path(tmpdir))
            meta = json.loads((Path(tmpdir) / "pr_meta.json").read_text(encoding="utf-8"))
        self.assertEqual(meta["threads_error"], "GraphQL down")

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
    """Tests for find_last_reviewed_sha and measure_rereview_delta."""

    @patch(f"{_MODULE}.list_all_pages")
    @patch(f"{_MODULE}.get_authenticated_login", return_value="me")
    def test_last_reviewed_sha_is_latest_own_review(self, _login, mock_pages):
        mock_pages.return_value = [
            {"user": {"login": "me"}, "commit_id": "c1"},
            {"user": {"login": "other"}, "commit_id": "c2"},
            {"user": {"login": "me"}, "commit_id": "c3"},
            {"user": {"login": "me"}, "commit_id": None},
            {"user": {"login": "me"}, "commit_id": "draft", "state": "PENDING"},
        ]
        self.assertEqual(find_last_reviewed_sha("google", "xmc", 7), "c3")
        self.assertEqual(mock_pages.call_args.args[0], "/repos/google/xmc/pulls/7/reviews")

    @patch(f"{_MODULE}.list_all_pages")
    @patch(f"{_MODULE}.get_authenticated_login", return_value="me")
    def test_last_reviewed_sha_skips_reply_only_wrappers(self, _login, mock_pages):
        """A reply-only wrapper on a newer head is skipped; an empty review with roots is kept."""
        me = {"login": "me"}
        mock_pages.return_value = [
            {"id": 1, "user": me, "commit_id": "reviewed", "state": "COMMENTED", "body": "R"},
            {"id": 2, "user": me, "commit_id": "inline", "state": "COMMENTED", "body": ""},
            {"id": 3, "user": me, "commit_id": "newer", "state": "COMMENTED", "body": ""},
        ]
        comments = [
            {"id": 10, "pull_request_review_id": 1},
            {"id": 11, "pull_request_review_id": 2},
            {"id": 12, "pull_request_review_id": 3, "in_reply_to_id": 10},
        ]
        self.assertEqual(find_last_reviewed_sha("google", "xmc", 7, comments), "inline")
        self.assertEqual(find_last_reviewed_sha("google", "xmc", 7), "newer")

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
        small = measure_rereview_delta("google", "xmc", 7, "head1", "old1", threshold=200)
        large = measure_rereview_delta("google", "xmc", 7, "head1", "old1", threshold=199)
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
