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

"""Unit tests for recover_closed_stacked_pr tool."""

import argparse
import subprocess
import unittest
from unittest.mock import MagicMock, call, patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError, GithubAuthError
from tools.stacks.recover_closed_stacked_pr import (
    PullRequestIdentifier,
    _is_valid_git_commit,
    _resolve_merge_base,
    _resolve_target_base_sha,
    delete_remote_branch,
    discover_upstream_prs,
    fetch_pr_details,
    parse_pr_identifier,
    rebuild_and_sync_stack,
    recover_stacked_pr,
    recreate_remote_branch,
    remote_branch_exists,
    reopen_pr,
    resolve_base_commit_sha,
    retarget_pr_base,
    unstack_stack,
)


class TestParsePrIdentifier(unittest.TestCase):
    """Test cases for parse_pr_identifier."""

    def test_parse_numeric_id(self):
        result = parse_pr_identifier("753")
        self.assertEqual(result.owner, "google")
        self.assertEqual(result.repo, "xmc")
        self.assertEqual(result.pull_number, 753)
        self.assertEqual(result["owner"], "google")
        self.assertEqual(result["pull_number"], 753)

    def test_parse_full_url(self):
        result = parse_pr_identifier("https://github.com/google/xmc/pull/753")
        self.assertEqual(result.owner, "google")
        self.assertEqual(result.repo, "xmc")
        self.assertEqual(result.pull_number, 753)

    def test_parse_custom_org_and_repo_url(self):
        result = parse_pr_identifier("https://github.com/my-org/custom-repo/pull/999")
        self.assertEqual(result.owner, "my-org")
        self.assertEqual(result.repo, "custom-repo")
        self.assertEqual(result.pull_number, 999)

    def test_parse_url_with_fragment_and_trailing_slash(self):
        result = parse_pr_identifier("https://github.com/google/xmc/pull/753/#discussion_r12345")
        self.assertEqual(result.pull_number, 753)

    def test_parse_malformed_string_raises_handled_error(self):
        with self.assertRaises(HandledError) as ctx:
            parse_pr_identifier("not-a-valid-pr")
        self.assertIn("Invalid PR identifier", ctx.exception.message)

    def test_parse_invalid_url_raises_handled_error(self):
        with self.assertRaises(HandledError) as ctx:
            parse_pr_identifier("https://github.com/google/xmc/issues/753")
        self.assertIn("Invalid PR identifier", ctx.exception.message)


class TestDiscoverUpstreamPrs(unittest.TestCase):
    """Test cases for recursive upstream stacked PR discovery."""

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_discover_upstream_empty(self, mock_api):
        mock_api.return_value = []
        result = discover_upstream_prs("google", "xmc", "feat/base-branch")
        self.assertEqual(result, [])
        mock_api.assert_called_once_with(
            "/repos/google/xmc/pulls?state=open&base=feat/base-branch&per_page=100",
            method="GET",
            user_agent="XMC-Stack-Recovery-Agent",
        )

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_discover_upstream_single_child(self, mock_api):
        child_pr = {
            "number": 754,
            "head": {"ref": "feat/child-branch"},
            "base": {"ref": "feat/parent-branch"},
        }
        mock_api.side_effect = [[child_pr], []]
        result = discover_upstream_prs("google", "xmc", "feat/parent-branch")
        self.assertEqual(result, [child_pr])
        self.assertEqual(mock_api.call_count, 2)

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_discover_upstream_multi_level(self, mock_api):
        child1 = {
            "number": 754,
            "head": {"ref": "feat/layer-1"},
            "base": {"ref": "feat/base-layer"},
        }
        child2 = {"number": 755, "head": {"ref": "feat/layer-2"}, "base": {"ref": "feat/layer-1"}}
        child3 = {"number": 756, "head": {"ref": "feat/layer-3"}, "base": {"ref": "feat/layer-2"}}
        mock_api.side_effect = [[child1], [child2], [child3], []]
        result = discover_upstream_prs("google", "xmc", "feat/base-layer")
        self.assertEqual(result, [child1, child2, child3])
        self.assertEqual(mock_api.call_count, 4)

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_discover_upstream_cycle_protection(self, mock_api):
        cycle_pr = {
            "number": 754,
            "head": {"ref": "feat/base-layer"},
            "base": {"ref": "feat/base-layer"},
        }
        mock_api.return_value = [cycle_pr]
        result = discover_upstream_prs("google", "xmc", "feat/base-layer")
        self.assertEqual(result, [cycle_pr])

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_discover_upstream_api_error_raises_handled_error(self, mock_api):
        mock_api.side_effect = GithubAPIError("Rate limit exceeded")
        with self.assertRaises(HandledError) as ctx:
            discover_upstream_prs("google", "xmc", "feat/branch")
        self.assertIn("Failed to query upstream PRs", ctx.exception.message)


class TestResolveBaseCommitSha(unittest.TestCase):
    """Test cases for commit SHA resolution for deleted base branch."""

    @patch("tools.stacks.recover_closed_stacked_pr._is_valid_git_commit")
    def test_resolve_from_base_sha(self, mock_valid):
        mock_valid.return_value = True
        pr_data = {
            "base": {"ref": "deleted-branch", "sha": "1111222233334444555566667777888899990000"},
            "head": {"ref": "head-branch", "sha": "aaaa"},
        }
        sha = resolve_base_commit_sha(pr_data, target_base="main", remote="origin")
        self.assertEqual(sha, "1111222233334444555566667777888899990000")
        mock_valid.assert_called_once_with("1111222233334444555566667777888899990000")

    @patch("tools.stacks.recover_closed_stacked_pr._resolve_merge_base")
    @patch("tools.stacks.recover_closed_stacked_pr._is_valid_git_commit")
    def test_resolve_from_merge_base_when_base_sha_missing_or_invalid(
        self, mock_valid, mock_merge_base
    ):
        mock_valid.return_value = False
        mock_merge_base.return_value = "mergebase1234567890abcdef"
        pr_data = {
            "base": {"ref": "deleted-branch", "sha": "invalidsha"},
            "head": {"ref": "head-branch", "sha": "aaaa"},
        }
        sha = resolve_base_commit_sha(pr_data, target_base="main", remote="origin")
        self.assertEqual(sha, "mergebase1234567890abcdef")
        mock_merge_base.assert_called_once_with("origin", "main", "head-branch")

    @patch("tools.stacks.recover_closed_stacked_pr._resolve_target_base_sha")
    @patch("tools.stacks.recover_closed_stacked_pr._resolve_merge_base")
    @patch("tools.stacks.recover_closed_stacked_pr._is_valid_git_commit")
    def test_resolve_from_target_base_when_merge_base_fails(
        self, mock_valid, mock_merge_base, mock_target_sha
    ):
        mock_valid.return_value = False
        mock_merge_base.return_value = None
        mock_target_sha.return_value = "targetbasecommitsha1234"
        pr_data = {
            "base": {"ref": "deleted-branch", "sha": ""},
            "head": {"ref": "head-branch", "sha": "aaaa"},
        }
        sha = resolve_base_commit_sha(pr_data, target_base="main", remote="origin")
        self.assertEqual(sha, "targetbasecommitsha1234")
        mock_target_sha.assert_called_once_with("origin", "main")

    @patch("tools.stacks.recover_closed_stacked_pr._resolve_target_base_sha")
    @patch("tools.stacks.recover_closed_stacked_pr._resolve_merge_base")
    @patch("tools.stacks.recover_closed_stacked_pr._is_valid_git_commit")
    def test_resolve_failure_raises_handled_error(
        self, mock_valid, mock_merge_base, mock_target_sha
    ):
        mock_valid.return_value = False
        mock_merge_base.return_value = None
        mock_target_sha.return_value = None
        pr_data = {
            "base": {"ref": "deleted-branch", "sha": ""},
            "head": {"ref": "head-branch", "sha": "aaaa"},
        }
        with self.assertRaises(HandledError) as ctx:
            resolve_base_commit_sha(pr_data, target_base="main", remote="origin")
        self.assertIn("Failed to resolve commit SHA", ctx.exception.message)


class TestGitSubprocessHelpers(unittest.TestCase):
    """Test cases for git helper functions."""

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_is_valid_git_commit(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0)
        self.assertTrue(_is_valid_git_commit("abcdef1234567"))
        mock_run.assert_called_once_with(
            ["git", "rev-parse", "--verify", "--quiet", "abcdef1234567^{commit}"], check=False
        )

    def test_is_valid_git_commit_invalid_format(self):
        self.assertFalse(_is_valid_git_commit("xyz"))
        self.assertFalse(_is_valid_git_commit(""))

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_resolve_merge_base_remote_candidate_success(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0, stdout="abc123\n")
        res = _resolve_merge_base("origin", "main", "feature")
        self.assertEqual(res, "abc123")
        mock_run.assert_called_once_with(
            ["git", "merge-base", "origin/main", "feature"], check=False
        )

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_resolve_merge_base_local_fallback(self, mock_run):
        mock_run.side_effect = [
            MagicMock(returncode=1, stdout=""),
            MagicMock(returncode=0, stdout="def456\n"),
        ]
        res = _resolve_merge_base("origin", "main", "feature")
        self.assertEqual(res, "def456")
        self.assertEqual(mock_run.call_count, 2)

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_resolve_target_base_sha_remote_success(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0, stdout="sha999\n")
        res = _resolve_target_base_sha("origin", "main")
        self.assertEqual(res, "sha999")
        mock_run.assert_called_once_with(
            ["git", "rev-parse", "--verify", "--quiet", "origin/main^{commit}"], check=False
        )

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_recreate_remote_branch(self, mock_run):
        recreate_remote_branch("origin", "sha123", "deleted-branch", dry_run=False)
        mock_run.assert_called_once_with(
            ["git", "push", "origin", "sha123:refs/heads/deleted-branch"]
        )

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_recreate_remote_branch_dry_run(self, mock_run):
        recreate_remote_branch("origin", "sha123", "deleted-branch", dry_run=True)
        mock_run.assert_not_called()

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_delete_remote_branch(self, mock_run):
        delete_remote_branch("origin", "temp-branch", dry_run=False)
        mock_run.assert_called_once_with(["git", "push", "origin", "--delete", "temp-branch"])

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_delete_remote_branch_dry_run(self, mock_run):
        delete_remote_branch("origin", "temp-branch", dry_run=True)
        mock_run.assert_not_called()

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_remote_branch_exists_true(self, mock_run):
        mock_run.return_value = MagicMock(
            returncode=0, stdout="1234567\trefs/heads/feature-branch\n"
        )
        self.assertTrue(remote_branch_exists("origin", "feature-branch"))
        mock_run.assert_called_once_with(
            ["git", "ls-remote", "--heads", "origin", "feature-branch"], check=False
        )

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_remote_branch_exists_false(self, mock_run):
        mock_run.return_value = MagicMock(returncode=0, stdout="")
        self.assertFalse(remote_branch_exists("origin", "deleted-branch"))


class TestUnstack(unittest.TestCase):
    """Test cases for unstacking stacks with fallback."""

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_unstack_default_tries_remote_and_succeeds(self, mock_run):
        unstack_stack(unstack_local_only=False, dry_run=False)
        mock_run.assert_called_once_with(["gh", "stack", "unstack"])

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_unstack_fallback_when_remote_fails(self, mock_run):
        mock_run.side_effect = [
            subprocess.CalledProcessError(1, ["gh", "stack", "unstack"]),
            MagicMock(returncode=0),
        ]
        unstack_stack(unstack_local_only=False, dry_run=False)
        self.assertEqual(mock_run.call_count, 2)
        mock_run.assert_has_calls(
            [call(["gh", "stack", "unstack"]), call(["gh", "stack", "unstack", "--local"])]
        )

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_unstack_local_only_enforced(self, mock_run):
        unstack_stack(unstack_local_only=True, dry_run=False)
        mock_run.assert_called_once_with(["gh", "stack", "unstack", "--local"])

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_unstack_dry_run_skips_execution(self, mock_run):
        unstack_stack(unstack_local_only=False, dry_run=True)
        mock_run.assert_not_called()


class TestRebuildAndSyncStack(unittest.TestCase):
    """Test cases for re-initializing and submitting stack."""

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_rebuild_and_sync_stack_submit_success(self, mock_run):
        rebuild_and_sync_stack("main", ["feat/a", "feat/b"], dry_run=False)
        self.assertEqual(mock_run.call_count, 2)
        mock_run.assert_has_calls(
            [
                call(["gh", "stack", "init", "--base", "main", "feat/a", "feat/b"]),
                call(["gh", "stack", "submit", "--auto"]),
            ]
        )

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_rebuild_and_sync_stack_fallback_to_sync(self, mock_run):
        mock_run.side_effect = [
            MagicMock(returncode=0),
            subprocess.CalledProcessError(1, ["gh", "stack", "submit", "--auto"]),
            MagicMock(returncode=0),
        ]
        rebuild_and_sync_stack("main", ["feat/a"], dry_run=False)
        self.assertEqual(mock_run.call_count, 3)
        mock_run.assert_has_calls(
            [
                call(["gh", "stack", "init", "--base", "main", "feat/a"]),
                call(["gh", "stack", "submit", "--auto"]),
                call(["gh", "stack", "sync"]),
            ]
        )

    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_rebuild_and_sync_stack_dry_run(self, mock_run):
        rebuild_and_sync_stack("main", ["feat/a"], dry_run=True)
        mock_run.assert_not_called()


class TestReopenAndRetargetPr(unittest.TestCase):
    """Test cases for reopening and retargeting PR base via GitHub API."""

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_reopen_pr_success(self, mock_api):
        reopen_pr("google", "xmc", 753, dry_run=False)
        mock_api.assert_called_once_with(
            "/repos/google/xmc/pulls/753",
            method="PATCH",
            payload={"state": "open"},
            user_agent="XMC-Stack-Recovery-Agent",
        )

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_reopen_pr_dry_run(self, mock_api):
        reopen_pr("google", "xmc", 753, dry_run=True)
        mock_api.assert_not_called()

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_reopen_pr_api_error_raises_handled_error(self, mock_api):
        mock_api.side_effect = GithubAPIError("Reopen forbidden")
        with self.assertRaises(HandledError) as ctx:
            reopen_pr("google", "xmc", 753, dry_run=False)
        self.assertIn("Failed to reopen PR #753", ctx.exception.message)

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_retarget_pr_base_success(self, mock_api):
        retarget_pr_base("google", "xmc", 753, "main", dry_run=False)
        mock_api.assert_called_once_with(
            "/repos/google/xmc/pulls/753",
            method="PATCH",
            payload={"base": "main"},
            user_agent="XMC-Stack-Recovery-Agent",
        )

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_retarget_pr_base_dry_run(self, mock_api):
        retarget_pr_base("google", "xmc", 753, "main", dry_run=True)
        mock_api.assert_not_called()

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_retarget_pr_base_api_error_raises_handled_error(self, mock_api):
        mock_api.side_effect = GithubAPIError("Retarget failed")
        with self.assertRaises(HandledError) as ctx:
            retarget_pr_base("google", "xmc", 753, "main", dry_run=False)
        self.assertIn("Failed to retarget PR #753", ctx.exception.message)


class TestFullRecoveryWorkflow(unittest.TestCase):
    """End-to-end unit tests for recover_stacked_pr function."""

    def _create_args(self, **kwargs):
        defaults = {
            "pr": "753",
            "target_base": "main",
            "remote": "origin",
            "dry_run": False,
            "unstack_local_only": False,
        }
        return argparse.Namespace(**(defaults | kwargs))

    def _run_workflow_mocks(self, fetch_val, branch_exists=False, discover_val=None, args=None):
        with (
            patch(
                "tools.stacks.recover_closed_stacked_pr.fetch_pr_details", return_value=fetch_val
            ) as mock_fetch,
            patch(
                "tools.stacks.recover_closed_stacked_pr.resolve_base_commit_sha",
                return_value=fetch_val.get("base", {}).get("sha", "1234sha"),
            ) as mock_sha,
            patch("tools.stacks.recover_closed_stacked_pr.unstack_stack") as mock_unstack,
            patch(
                "tools.stacks.recover_closed_stacked_pr.remote_branch_exists",
                return_value=branch_exists,
            ) as mock_exists,
            patch("tools.stacks.recover_closed_stacked_pr.recreate_remote_branch") as mock_recreate,
            patch("tools.stacks.recover_closed_stacked_pr.reopen_pr") as mock_reopen,
            patch("tools.stacks.recover_closed_stacked_pr.retarget_pr_base") as mock_retarget,
            patch("tools.stacks.recover_closed_stacked_pr.delete_remote_branch") as mock_delete,
            patch(
                "tools.stacks.recover_closed_stacked_pr.discover_upstream_prs",
                return_value=discover_val or [],
            ) as mock_discover,
            patch("tools.stacks.recover_closed_stacked_pr.rebuild_and_sync_stack") as mock_rebuild,
        ):
            res = recover_stacked_pr(args or self._create_args())
            return res, {
                "fetch": mock_fetch,
                "sha": mock_sha,
                "unstack": mock_unstack,
                "exists": mock_exists,
                "recreate": mock_recreate,
                "reopen": mock_reopen,
                "retarget": mock_retarget,
                "delete": mock_delete,
                "discover": mock_discover,
                "rebuild": mock_rebuild,
            }

    def test_full_recovery_execution_workflow(self):
        pr = {
            "state": "closed",
            "base": {"ref": "del-branch", "sha": "1234sha"},
            "head": {"ref": "feat/h", "sha": "5678sha"},
        }
        children = [
            {"number": 754, "head": {"ref": "feat/c1"}},
            {"number": 755, "head": {"ref": "feat/c2"}},
        ]
        res, m = self._run_workflow_mocks(pr, branch_exists=False, discover_val=children)

        self.assertEqual(res["status"], "success")
        self.assertFalse(res["dry_run"])
        self.assertEqual(res["pull_number"], 753)
        self.assertEqual(res["target_base"], "main")
        self.assertEqual(res["deleted_base_branch"], "del-branch")
        self.assertEqual(res["recreated_commit_sha"], "1234sha")
        self.assertEqual(res["recovered_head_branch"], "feat/h")
        self.assertEqual(res["upstream_pr_numbers"], [754, 755])
        self.assertEqual(res["stack_branches"], ["feat/h", "feat/c1", "feat/c2"])

        m["unstack"].assert_called_once_with(unstack_local_only=False, dry_run=False)
        m["recreate"].assert_called_once_with("origin", "1234sha", "del-branch", dry_run=False)
        m["reopen"].assert_called_once_with("google", "xmc", 753, dry_run=False)
        m["retarget"].assert_called_once_with("google", "xmc", 753, "main", dry_run=False)
        m["delete"].assert_called_once_with("origin", "del-branch", dry_run=False)
        m["discover"].assert_called_once_with("google", "xmc", "feat/h")
        m["rebuild"].assert_called_once_with(
            "main", ["feat/h", "feat/c1", "feat/c2"], dry_run=False
        )

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    @patch("tools.stacks.recover_closed_stacked_pr._run_subprocess_command")
    def test_dry_run_mode_ensures_no_mutating_calls(self, mock_subprocess, mock_api):
        mock_api.side_effect = [
            {
                "state": "closed",
                "base": {"ref": "del", "sha": "c1"},
                "head": {"ref": "h", "sha": "h1"},
            },
            [],
        ]
        mock_subprocess.return_value = MagicMock(returncode=0)

        args = self._create_args(dry_run=True)
        result = recover_stacked_pr(args)

        self.assertEqual(result["status"], "dry_run")
        self.assertTrue(result["dry_run"])
        self.assertEqual(result["stack_branches"], ["h"])

        for api_call in mock_api.call_args_list:
            self.assertEqual(api_call.kwargs.get("method", "GET"), "GET")

        for sub_call in mock_subprocess.call_args_list:
            cmd = sub_call.args[0]
            self.assertNotIn("push", cmd)
            self.assertNotIn("submit", cmd)
            self.assertNotIn("unstack", cmd)

    def test_full_recovery_when_remote_branch_exists_skips_recreation_and_deletion(self):
        pr = {
            "state": "closed",
            "base": {"ref": "exist-branch", "sha": "1234sha"},
            "head": {"ref": "feat/h", "sha": "5678sha"},
        }
        res, m = self._run_workflow_mocks(pr, branch_exists=True, discover_val=[])

        self.assertEqual(res["status"], "success")
        self.assertFalse(res["remote_branch_was_recreated"])
        m["recreate"].assert_not_called()
        m["delete"].assert_not_called()
        m["reopen"].assert_called_once()
        m["retarget"].assert_called_once()

    @patch("tools.stacks.recover_closed_stacked_pr.fetch_pr_details")
    def test_pr_already_open_raises_handled_error(self, mock_fetch):
        mock_fetch.return_value = {
            "state": "open",
            "base": {"ref": "main"},
            "head": {"ref": "feat/my-branch"},
        }
        args = self._create_args()
        with self.assertRaises(HandledError) as ctx:
            recover_stacked_pr(args)
        self.assertIn("already open", ctx.exception.message)

    @patch("tools.stacks.recover_closed_stacked_pr.fetch_pr_details")
    def test_pr_already_merged_raises_handled_error(self, mock_fetch):
        mock_fetch.return_value = {
            "state": "closed",
            "merged": True,
            "merged_at": "2026-09-04T08:00:00Z",
            "base": {"ref": "main"},
            "head": {"ref": "feat/my-branch"},
        }
        args = self._create_args()
        with self.assertRaises(HandledError) as ctx:
            recover_stacked_pr(args)
        self.assertIn("already merged", ctx.exception.message)

    @patch("tools.stacks.recover_closed_stacked_pr.fetch_pr_details")
    def test_pr_missing_branch_refs_raises_handled_error(self, mock_fetch):
        mock_fetch.return_value = {"state": "closed", "base": {}, "head": {}}
        args = self._create_args()
        with self.assertRaises(HandledError) as ctx:
            recover_stacked_pr(args)
        self.assertIn("missing head or base ref", ctx.exception.message)

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_fetch_pr_details_auth_error(self, mock_api):
        mock_api.side_effect = GithubAuthError("Authentication token expired")
        pr_id = PullRequestIdentifier("google", "xmc", 753)
        with self.assertRaises(HandledError) as ctx:
            fetch_pr_details(pr_id)
        self.assertIn("Authentication token expired", ctx.exception.message)
        self.assertIn("gh auth login", ctx.exception.instruction)

    @patch("tools.stacks.recover_closed_stacked_pr.call_github_api")
    def test_fetch_pr_details_api_error(self, mock_api):
        mock_api.side_effect = GithubAPIError("Not Found (404)")
        pr_id = PullRequestIdentifier("google", "xmc", 753)
        with self.assertRaises(HandledError) as ctx:
            fetch_pr_details(pr_id)
        self.assertIn("Not Found (404)", ctx.exception.message)
        self.assertIn("Ensure the pull request exists", ctx.exception.instruction)


if __name__ == "__main__":
    unittest.main()
