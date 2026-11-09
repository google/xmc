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

"""Unit tests for git operations and log streaming."""

import fnmatch
import subprocess
import unittest
from collections.abc import Callable
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.release.git_ops import (
    _MAX_DESCRIBE_ATTEMPTS,
    create_git_tag,
    get_git_commits,
    get_git_file_content,
    get_git_remote_url,
    get_latest_tag_before,
    parse_remote_url,
    push_git_tag,
    resolve_repo_owner_and_name,
    resolve_target_commit,
    verify_local_tag,
    verify_remote_tag,
)


def _fake_git_describe(tags_newest_first: list[str]) -> Callable[..., MagicMock]:
    """Returns a subprocess.run double emulating `git describe --match/--exclude` over tags."""

    def run(cmd: list[str], **_: object) -> MagicMock:
        globs = _flag_values(cmd, "--match")
        excludes = _flag_values(cmd, "--exclude")
        for tag in tags_newest_first:
            if globs and not any(fnmatch.fnmatchcase(tag, g) for g in globs):
                continue
            if any(fnmatch.fnmatchcase(tag, e) for e in excludes):
                continue
            return MagicMock(stdout=f"{tag}\n", returncode=0)
        raise subprocess.CalledProcessError(128, cmd)

    return run


def _flag_values(cmd: list[str], flag: str) -> list[str]:
    """Collects values of flag given as either `flag VALUE` or `flag=VALUE`."""
    values = [cmd[i + 1] for i, arg in enumerate(cmd[:-1]) if arg == flag]
    values.extend(arg.removeprefix(f"{flag}=") for arg in cmd if arg.startswith(f"{flag}="))
    return values


class GitOpsTest(unittest.TestCase):
    """Tests for Git subprocess log extractor and tag discovery."""

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_describe(self, mock_run: MagicMock) -> None:
        mock_run.return_value = MagicMock(stdout="0.5.0\n", returncode=0)
        tag = get_latest_tag_before("0.6.0", Path("."))
        self.assertEqual(tag, "0.5.0")

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_not_found(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(1, ["git", "describe"])
        tag = get_latest_tag_before("HEAD", Path("."))
        self.assertIsNone(tag)

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_skips_newer_nightly_tag(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = _fake_git_describe(["nightly-2026-11-09.115", "0.17.0", "0.16.0"])
        tag = get_latest_tag_before("HEAD", Path("."))
        self.assertEqual(tag, "0.17.0")

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_skips_rc_tag(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = _fake_git_describe(["1.2.3-rc1", "1.2.2", "1.2.1"])
        tag = get_latest_tag_before("HEAD", Path("."))
        self.assertEqual(tag, "1.2.2")
        self.assertEqual(mock_run.call_count, 1)
        self.assertIn("*-*", mock_run.call_args[0][0])

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_retries_past_non_semver_tag(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = _fake_git_describe(["1.2.3.4", "1.2.2", "1.2.1"])
        tag = get_latest_tag_before("HEAD", Path("."))
        self.assertEqual(tag, "1.2.2")
        self.assertEqual(mock_run.call_count, 2)
        self.assertIn("1.2.3.4", mock_run.call_args[0][0])

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_git_not_found(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError) as ctx:
            get_latest_tag_before("HEAD", Path("."))
        self.assertIn("git command not found", str(ctx.exception))

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_no_semver_tag(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = _fake_git_describe(
            ["nightly-2026-11-09.115", "1.2.3-rc1", "1.2.2-rc2"]
        )
        tag = get_latest_tag_before("HEAD", Path("."))
        self.assertIsNone(tag)

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_latest_tag_before_retry_cap_exhausted(self, mock_run: MagicMock) -> None:
        counter = iter(range(1000))
        mock_run.side_effect = lambda cmd, **_: MagicMock(
            stdout=f"0.17.0.{next(counter)}\n", returncode=0
        )
        tag = get_latest_tag_before("HEAD", Path("."))
        self.assertIsNone(tag)
        self.assertEqual(mock_run.call_count, _MAX_DESCRIBE_ATTEMPTS)

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_git_file_content(self, mock_run: MagicMock) -> None:
        mock_run.return_value = MagicMock(stdout="version: 0.6.0\n", returncode=0)
        content = get_git_file_content("HEAD", "deploy/helm/values.yaml")
        self.assertEqual(content, "version: 0.6.0\n")

        mock_run.side_effect = subprocess.CalledProcessError(1, ["git"])
        self.assertIsNone(get_git_file_content("HEAD", "missing.yaml"))

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_git_commits_parsing(self, mock_run: MagicMock) -> None:
        raw_log = """---XMC_COMMIT_DELIMITER---
11111111---XMC_FIELD_SEP---1111---XMC_FIELD_SEP---Author One---XMC_FIELD_SEP---2026-08-21---XMC_FIELD_SEP---feat(ui): add dashboard (#123)
ui/app.ts
ui/index.html
---XMC_COMMIT_DELIMITER---
22222222---XMC_FIELD_SEP---2222---XMC_FIELD_SEP---Author Two---XMC_FIELD_SEP---2026-08-20---XMC_FIELD_SEP---fix(core): resolve race: delimiter check
pkg/core/server.go
---XMC_COMMIT_DELIMITER---
33333333---XMC_FIELD_SEP---3333---XMC_FIELD_SEP---Author Three---XMC_FIELD_SEP---2026-08-19---XMC_FIELD_SEP---feat(api)!: breaking api change (#124)
pkg/api/v1.go
---XMC_COMMIT_DELIMITER---
malformed_line_with_missing_separators
"""
        mock_run.return_value = MagicMock(stdout=raw_log, returncode=0)
        commits = get_git_commits("0.5.0", "0.6.0", Path("."))

        self.assertEqual(len(commits), 3)
        self.assertEqual(commits[0].short_hash, "1111")
        self.assertEqual(commits[0].author, "Author One")
        self.assertEqual(commits[0].commit_type, "feat")
        self.assertEqual(commits[0].scope, "ui")
        self.assertEqual(commits[0].description, "add dashboard")
        self.assertEqual(commits[0].pr_number, 123)
        self.assertEqual(commits[0].files_changed, ["ui/app.ts", "ui/index.html"])

        self.assertEqual(commits[1].short_hash, "2222")
        self.assertEqual(commits[1].subject, "fix(core): resolve race: delimiter check")
        self.assertEqual(commits[1].commit_type, "fix")
        self.assertEqual(commits[1].scope, "core")
        self.assertEqual(commits[1].description, "resolve race: delimiter check")
        self.assertIsNone(commits[1].pr_number)

        self.assertEqual(commits[2].short_hash, "3333")
        self.assertEqual(commits[2].commit_type, "feat")
        self.assertEqual(commits[2].scope, "api")
        self.assertEqual(commits[2].description, "breaking api change")
        self.assertEqual(commits[2].pr_number, 124)

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_git_commits_error(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(
            128, ["git"], stderr="fatal: bad revision"
        )
        with self.assertRaises(HandledError):
            get_git_commits("invalid", "invalid", Path("."))

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_git_remote_url_success(self, mock_run: MagicMock) -> None:
        mock_run.return_value = MagicMock(stdout="git@github.com:myorg/myrepo.git\n", returncode=0)
        self.assertEqual(get_git_remote_url(Path(".")), "git@github.com:myorg/myrepo.git")
        self.assertEqual(
            get_git_remote_url(Path("."), remote="upstream"), "git@github.com:myorg/myrepo.git"
        )
        self.assertEqual(mock_run.call_args[0][0], ["git", "remote", "get-url", "upstream"])

    @patch("tools.release.git_ops.subprocess.run")
    def test_get_git_remote_url_fallback(self, mock_run: MagicMock) -> None:
        mock_run.side_effect = subprocess.CalledProcessError(1, ["git"])
        with self.assertRaises(HandledError):
            get_git_remote_url(Path("."))

        mock_run.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError):
            get_git_remote_url(Path("."))

    def test_parse_remote_url(self) -> None:
        self.assertEqual(parse_remote_url("git@github.com:google/xmc.git"), ("google", "xmc"))
        self.assertEqual(parse_remote_url("https://github.com/google/xmc/"), ("google", "xmc"))
        self.assertEqual(parse_remote_url("https://github.com/google/xmc"), ("google", "xmc"))
        self.assertEqual(parse_remote_url("invalid"), ("google", "xmc"))

    @patch("tools.release.git_ops.subprocess.run")
    def test_verify_local_tag(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="", returncode=0)
        self.assertFalse(verify_local_tag("0.6.0", Path("."), force=False))

        mock_subprocess.return_value = MagicMock(stdout="0.6.0\n", returncode=0)
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False)

        def side_effect(cmd: list[str], **_kwargs: object) -> MagicMock:
            if cmd == ["git", "tag", "-l", "0.6.0"]:
                return MagicMock(stdout="0.6.0\n", returncode=0)
            if cmd == ["git", "rev-parse", "refs/tags/0.6.0^{commit}"]:
                return MagicMock(stdout="a1b2c3d4\n", returncode=0)
            return MagicMock(stdout="", returncode=0)

        mock_subprocess.side_effect = side_effect
        self.assertTrue(verify_local_tag("0.6.0", Path("."), force=False, target_commit="a1b2c3d4"))

        def side_effect_diff(cmd: list[str], **_kwargs: object) -> MagicMock:
            if cmd == ["git", "tag", "-l", "0.6.0"]:
                return MagicMock(stdout="0.6.0\n", returncode=0)
            if cmd == ["git", "rev-parse", "refs/tags/0.6.0^{commit}"]:
                return MagicMock(stdout="different_sha\n", returncode=0)
            return MagicMock(stdout="", returncode=0)

        mock_subprocess.side_effect = side_effect_diff
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False, target_commit="a1b2c3d4")

        mock_subprocess.side_effect = None
        mock_subprocess.return_value = MagicMock(stdout="0.6.0\n", returncode=0)
        self.assertFalse(verify_local_tag("0.6.0", Path("."), force=True))

        mock_subprocess.return_value = MagicMock(stderr="error", returncode=1)
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False)

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False)

    @patch("tools.release.git_ops.subprocess.run")
    def test_verify_remote_tag(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="", returncode=0)
        verify_remote_tag("0.6.0", Path("."), force=False)

        mock_subprocess.return_value = MagicMock(stdout="a1b2c3d4\trefs/tags/0.6.0\n", returncode=0)
        with self.assertRaises(HandledError) as ctx:
            verify_remote_tag("0.6.0", Path("."), force=False, remote="origin")
        self.assertIn("already exists on remote repository 'origin'", str(ctx.exception))

        mock_subprocess.return_value = MagicMock(stderr="remote error", returncode=128)
        with self.assertRaises(HandledError) as ctx:
            verify_remote_tag("0.6.0", Path("."), force=False, remote="origin")
        self.assertIn("Failed to check remote git tags", str(ctx.exception))

        mock_subprocess.reset_mock()
        verify_remote_tag("0.6.0", Path("."), force=True)
        mock_subprocess.assert_not_called()

    @patch("tools.release.git_ops.subprocess.run")
    def test_create_git_tag_and_error(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(returncode=0)
        create_git_tag("0.6.0", "HEAD", "Release 0.6.0", Path("."), force=False)
        self.assertIn("-a", mock_subprocess.call_args[0][0])
        self.assertNotIn("--force", mock_subprocess.call_args[0][0])

        create_git_tag("0.6.0", "HEAD", "Release 0.6.0", Path("."), force=True)
        self.assertIn("--force", mock_subprocess.call_args[0][0])

        mock_subprocess.side_effect = subprocess.CalledProcessError(1, "git")
        with self.assertRaises(HandledError):
            create_git_tag("0.6.0", "HEAD", "Release 0.6.0", Path("."), force=False)

    @patch("tools.release.git_ops.subprocess.run")
    def test_push_git_tag_and_errors(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(returncode=0)
        push_git_tag("0.6.0", Path("."), force=False, remote="origin")
        self.assertIn("push", mock_subprocess.call_args[0][0])
        self.assertIn("origin", mock_subprocess.call_args[0][0])
        self.assertNotIn("--force", mock_subprocess.call_args[0][0])

        push_git_tag("0.6.0", Path("."), force=True, remote="upstream")
        self.assertIn("--force", mock_subprocess.call_args[0][0])
        self.assertIn("upstream", mock_subprocess.call_args[0][0])

        mock_subprocess.side_effect = subprocess.CalledProcessError(
            1, "git", stderr="already exists"
        )
        with self.assertRaises(HandledError) as ctx:
            push_git_tag("0.6.0", Path("."), force=False)
        self.assertIn("already exist", ctx.exception.instruction)

        mock_subprocess.side_effect = subprocess.CalledProcessError(
            1, "git", stderr="permission denied"
        )
        with self.assertRaises(HandledError) as ctx:
            push_git_tag("0.6.0", Path("."), force=False)
        self.assertIn("write permissions", ctx.exception.instruction)

    @patch.dict("os.environ", {"GITHUB_OWNER": "env_owner", "GITHUB_REPO": "env_repo"}, clear=True)
    def test_resolve_repo_owner_and_name_from_env(self) -> None:
        owner, repo = resolve_repo_owner_and_name(Path("."))
        self.assertEqual(owner, "env_owner")
        self.assertEqual(repo, "env_repo")

    @patch.dict("os.environ", {"GITHUB_REPOSITORY": "env_owner/env_repo"}, clear=True)
    def test_resolve_repo_owner_and_name_from_github_repository_env(self) -> None:
        owner, repo = resolve_repo_owner_and_name(Path("."))
        self.assertEqual(owner, "env_owner")
        self.assertEqual(repo, "env_repo")

    @patch("tools.release.git_ops.get_git_remote_url")
    @patch.dict("os.environ", {}, clear=True)
    def test_resolve_repo_owner_and_name_fallback(self, mock_remote_url: MagicMock) -> None:
        mock_remote_url.return_value = "https://github.com/fallback_owner/fallback_repo.git"
        owner, repo = resolve_repo_owner_and_name(Path("."), remote="custom")
        self.assertEqual(owner, "fallback_owner")
        self.assertEqual(repo, "fallback_repo")

    @patch("tools.release.git_ops.subprocess.run")
    def test_resolve_target_commit_success(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="a1b2c3d4e5f6\n", returncode=0)
        commit = resolve_target_commit("HEAD", Path("."))
        self.assertEqual(commit, "a1b2c3d4e5f6")
        self.assertIn("HEAD^{commit}", mock_subprocess.call_args[0][0])

    @patch("tools.release.git_ops.subprocess.run")
    def test_resolve_target_commit_errors(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.side_effect = subprocess.CalledProcessError(1, ["git"], stderr="bad ref")
        with self.assertRaises(HandledError) as ctx:
            resolve_target_commit("nonexistent", Path("."))
        self.assertIn("Failed to resolve target commit", str(ctx.exception))

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError) as ctx:
            resolve_target_commit("HEAD", Path("."))
        self.assertIn("Failed to resolve target commit", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
