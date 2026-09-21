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

"""Unit tests for publish_release tool."""

import argparse
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.release.publish_release import (
    create_git_tag,
    create_parser,
    publish_release,
    publish_via_gh_cli,
    push_git_tag,
    read_notes_file,
    resolve_repo_owner_and_name,
    resolve_target_commit,
    verify_local_tag,
    verify_remote_tag,
)


class PublishReleaseTest(unittest.TestCase):
    """Test suite for publish_release tool."""

    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.notes_file = Path(self.temp_dir.name) / "notes.md"
        self.notes_file.write_text("# Release Notes\n- Some changes\n", encoding="utf-8")

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def test_read_notes_file_valid_and_errors(self) -> None:
        content = read_notes_file(self.notes_file)
        self.assertIn("# Release Notes", content)

        missing = Path(self.temp_dir.name) / "nonexistent.md"
        with self.assertRaises(HandledError):
            read_notes_file(missing)

        empty = Path(self.temp_dir.name) / "empty.md"
        empty.write_text("   \n", encoding="utf-8")
        with self.assertRaises(HandledError):
            read_notes_file(empty)

    @patch("subprocess.run")
    def test_verify_local_tag(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="", returncode=0)
        self.assertFalse(verify_local_tag("0.6.0", Path("."), force=False))

        # Tag exists but no target_commit provided -> raises HandledError
        mock_subprocess.return_value = MagicMock(stdout="0.6.0\n", returncode=0)
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False)

        # Tag exists and matches target_commit -> returns True (no raise)
        def side_effect(cmd: list[str], **_kwargs: object) -> MagicMock:
            if cmd == ["git", "tag", "-l", "0.6.0"]:
                return MagicMock(stdout="0.6.0\n", returncode=0)
            if cmd == ["git", "rev-parse", "refs/tags/0.6.0^{commit}"]:
                return MagicMock(stdout="a1b2c3d4\n", returncode=0)
            return MagicMock(stdout="", returncode=0)

        mock_subprocess.side_effect = side_effect
        self.assertTrue(verify_local_tag("0.6.0", Path("."), force=False, target_commit="a1b2c3d4"))

        # Tag exists but points to different commit -> raises HandledError
        def side_effect_diff(cmd: list[str], **_kwargs: object) -> MagicMock:
            if cmd == ["git", "tag", "-l", "0.6.0"]:
                return MagicMock(stdout="0.6.0\n", returncode=0)
            if cmd == ["git", "rev-parse", "refs/tags/0.6.0^{commit}"]:
                return MagicMock(stdout="different_sha\n", returncode=0)
            return MagicMock(stdout="", returncode=0)

        mock_subprocess.side_effect = side_effect_diff
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False, target_commit="a1b2c3d4")

        # Reset side_effect for remaining tests
        mock_subprocess.side_effect = None

        # When force is True, tag collision should not raise
        mock_subprocess.return_value = MagicMock(stdout="0.6.0\n", returncode=0)
        self.assertFalse(verify_local_tag("0.6.0", Path("."), force=True))

        mock_subprocess.return_value = MagicMock(stderr="error", returncode=1)
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False)

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False)

    @patch("subprocess.run")
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

        # When force is True, remote check is skipped
        mock_subprocess.reset_mock()
        verify_remote_tag("0.6.0", Path("."), force=True)
        mock_subprocess.assert_not_called()

    @patch("subprocess.run")
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

    @patch("subprocess.run")
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

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch("subprocess.run")
    def test_publish_via_gh_cli(
        self, mock_subprocess: MagicMock, mock_remote_url: MagicMock
    ) -> None:
        mock_remote_url.return_value = "https://github.com/myorg/myrepo"
        mock_subprocess.return_value = MagicMock(
            stdout="https://github.com/myorg/myrepo/releases/tag/0.6.0\n",
            returncode=0,
        )
        url = publish_via_gh_cli(
            tag="0.6.0",
            title="Release 0.6.0",
            notes_path=self.notes_file,
            draft=False,
            prerelease=False,
            target_commit="HEAD",
            repo_dir=Path("."),
            remote="origin",
            force=True,
        )
        self.assertEqual(url, "https://github.com/myorg/myrepo/releases/tag/0.6.0")
        self.assertIn("--repo", mock_subprocess.call_args[0][0])
        self.assertIn("myorg/myrepo", mock_subprocess.call_args[0][0])
        self.assertIn("--clobber", mock_subprocess.call_args[0][0])

        mock_subprocess.side_effect = subprocess.CalledProcessError(1, "gh", stderr="gh error")
        with self.assertRaises(HandledError):
            publish_via_gh_cli(
                "0.6.0",
                "Release 0.6.0",
                self.notes_file,
                draft=False,
                prerelease=False,
                target_commit="HEAD",
                repo_dir=Path("."),
            )

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError):
            publish_via_gh_cli(
                "0.6.0",
                "Release 0.6.0",
                self.notes_file,
                draft=False,
                prerelease=False,
                target_commit="HEAD",
                repo_dir=Path("."),
            )

    @patch("tools.release.publish_release.publish_via_gh_cli")
    @patch("shutil.which")
    @patch("subprocess.run")
    def test_publish_release_post_push_failure_adds_rollback_note(
        self,
        mock_subprocess: MagicMock,
        mock_which: MagicMock,
        mock_publish_gh: MagicMock,
    ) -> None:
        mock_which.return_value = "/usr/bin/gh"
        mock_subprocess.return_value = MagicMock(stdout="", returncode=0)
        mock_publish_gh.side_effect = HandledError("gh failed", "fix gh")

        args = argparse.Namespace(
            tag="0.6.0",
            notes_file=str(self.notes_file),
            title="Release 0.6.0",
            target_commit="HEAD",
            repo_dir=".",
            remote="origin",
            draft=False,
            prerelease=False,
            skip_push=False,
            force=False,
        )
        with self.assertRaises(HandledError) as ctx:
            publish_release(args)
        self.assertIn("git tag -d 0.6.0", ctx.exception.instruction)
        self.assertIn("already pushed", ctx.exception.instruction)

        mock_publish_gh.side_effect = HandledError("gh failed", "fix gh")
        args.skip_push = True
        with self.assertRaises(HandledError) as ctx:
            publish_release(args)
        self.assertIn("already created locally", ctx.exception.instruction)

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch("shutil.which")
    @patch("subprocess.run")
    def test_publish_release_end_to_end(
        self,
        mock_subprocess: MagicMock,
        mock_which: MagicMock,
        mock_remote_url: MagicMock,
    ) -> None:
        mock_remote_url.return_value = "https://github.com/google/xmc"
        mock_which.return_value = "/usr/bin/gh"

        def side_effect(cmd: list[str], **_kwargs: object) -> MagicMock:
            if cmd[:2] == ["gh", "release"]:
                return MagicMock(
                    stdout="https://github.com/google/xmc/releases/tag/0.6.0\n",
                    returncode=0,
                )
            return MagicMock(stdout="", returncode=0)

        mock_subprocess.side_effect = side_effect

        args = argparse.Namespace(
            tag="0.6.0",
            notes_file=str(self.notes_file),
            title="Release 0.6.0",
            target_commit="HEAD",
            repo_dir=".",
            remote="origin",
            draft=False,
            prerelease=False,
            skip_push=False,
            force=False,
        )
        res = publish_release(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["tag"], "0.6.0")

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch("shutil.which")
    @patch("subprocess.run")
    def test_publish_release_gh_missing(
        self,
        mock_subprocess: MagicMock,
        mock_which: MagicMock,
        mock_remote_url: MagicMock,
    ) -> None:
        mock_remote_url.return_value = "https://github.com/google/xmc"
        mock_which.return_value = None
        mock_subprocess.return_value = MagicMock(
            stdout="",
            returncode=0,
        )

        args = argparse.Namespace(
            tag="0.6.0",
            notes_file=str(self.notes_file),
            title="Release 0.6.0",
            target_commit="HEAD",
            repo_dir=".",
            remote="origin",
            draft=False,
            prerelease=False,
            skip_push=True,
            force=False,
        )
        with self.assertRaises(HandledError) as ctx:
            publish_release(args)
        self.assertIn("GitHub CLI ('gh') is not installed", str(ctx.exception))
        mock_subprocess.assert_not_called()

    def test_publish_release_empty_tag(self) -> None:
        for invalid_tag in ["   ", "-v0.6.0"]:
            args = argparse.Namespace(
                tag=invalid_tag,
                notes_file=str(self.notes_file),
                title=None,
                target_commit="HEAD",
                repo_dir=".",
                remote="origin",
                draft=False,
                prerelease=False,
                skip_push=True,
                force=False,
            )
            with self.assertRaises(HandledError):
                publish_release(args)

    def test_publish_release_invalid_repo_dir(self) -> None:
        args = argparse.Namespace(
            tag="0.6.0",
            notes_file=str(self.notes_file),
            title=None,
            target_commit="HEAD",
            repo_dir="/path/to/nonexistent/directory",
            remote="origin",
            draft=False,
            prerelease=False,
            skip_push=True,
            force=False,
        )
        with self.assertRaises(HandledError) as ctx:
            publish_release(args)
        self.assertIn("does not exist", str(ctx.exception))

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch("shutil.which")
    @patch("subprocess.run")
    def test_publish_release_empty_url(
        self,
        mock_subprocess: MagicMock,
        mock_which: MagicMock,
        mock_remote_url: MagicMock,
    ) -> None:
        mock_remote_url.return_value = "https://github.com/google/xmc"
        mock_which.return_value = "/usr/bin/gh"
        mock_subprocess.return_value = MagicMock(
            stdout="",
            returncode=0,
        )

        args = argparse.Namespace(
            tag="0.6.0",
            notes_file=str(self.notes_file),
            title="Release 0.6.0",
            target_commit="HEAD",
            repo_dir=".",
            remote="origin",
            draft=False,
            prerelease=False,
            skip_push=True,
            force=False,
        )
        with self.assertRaises(HandledError) as ctx:
            publish_release(args)
        self.assertIn("Release URL is empty", str(ctx.exception))

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

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch("subprocess.run")
    def test_publish_release_dry_run(
        self,
        mock_subprocess: MagicMock,
        mock_remote_url: MagicMock,
    ) -> None:
        mock_remote_url.return_value = "https://github.com/google/xmc"
        mock_subprocess.return_value = MagicMock(stdout="", returncode=0)
        args = argparse.Namespace(
            tag="0.6.0",
            notes_file=str(self.notes_file),
            title="Release 0.6.0",
            target_commit="HEAD",
            repo_dir=".",
            remote="origin",
            draft=False,
            prerelease=False,
            skip_push=False,
            dry_run=True,
            force=False,
        )
        res = publish_release(args)
        self.assertEqual(res["status"], "dry_run")
        self.assertEqual(res["release_url"], "https://github.com/google/xmc/releases/tag/0.6.0")

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch.dict("os.environ", {}, clear=True)
    def test_resolve_repo_owner_and_name_fallback(self, mock_remote_url: MagicMock) -> None:
        mock_remote_url.return_value = "https://github.com/fallback_owner/fallback_repo.git"
        owner, repo = resolve_repo_owner_and_name(Path("."), remote="custom")
        self.assertEqual(owner, "fallback_owner")
        self.assertEqual(repo, "fallback_repo")

    @patch("subprocess.run")
    def test_resolve_target_commit_success(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="a1b2c3d4e5f6\n", returncode=0)
        commit = resolve_target_commit("HEAD", Path("."))
        self.assertEqual(commit, "a1b2c3d4e5f6")
        self.assertIn("HEAD^{commit}", mock_subprocess.call_args[0][0])

    @patch("subprocess.run")
    def test_resolve_target_commit_errors(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.side_effect = subprocess.CalledProcessError(1, ["git"], stderr="bad ref")
        with self.assertRaises(HandledError) as ctx:
            resolve_target_commit("nonexistent", Path("."))
        self.assertIn("Failed to resolve target commit", str(ctx.exception))

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError) as ctx:
            resolve_target_commit("HEAD", Path("."))
        self.assertIn("Failed to resolve target commit", str(ctx.exception))

    def test_create_parser(self) -> None:
        parser = create_parser()
        self.assertIsNotNone(parser)
        args = parser.parse_args(
            ["--tag", "0.6.0", "--notes-file", "notes.md", "--remote", "upstream"]
        )
        self.assertEqual(args.remote, "upstream")


if __name__ == "__main__":
    unittest.main()
