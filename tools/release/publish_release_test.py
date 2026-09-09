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
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.common.tool_utils import HandledError
    from tools.pr_review.github_utils import GithubAPIError
    from tools.release.publish_release import (
        create_git_tag,
        create_parser,
        publish_release,
        publish_via_gh_cli,
        publish_via_rest_api,
        push_git_tag,
        read_notes_file,
        resolve_repo_owner_and_name,
        resolve_target_commit,
        verify_local_tag,
    )
except ImportError:
    from common.tool_utils import HandledError  # type: ignore[no-redef]
    from github_utils import GithubAPIError  # type: ignore[no-redef]
    from publish_release import (  # type: ignore[no-redef]
        create_git_tag,
        create_parser,
        publish_release,
        publish_via_gh_cli,
        publish_via_rest_api,
        push_git_tag,
        read_notes_file,
        resolve_repo_owner_and_name,
        resolve_target_commit,
        verify_local_tag,
    )


class PublishReleaseTest(unittest.TestCase):
    """Test suite for publish_release tool."""

    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.notes_file = Path(self.temp_dir.name) / "notes.md"
        self.notes_file.write_text("# Release Notes\n\n* Feature 1", encoding="utf-8")

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def test_read_notes_file_valid_and_errors(self) -> None:
        content = read_notes_file(self.notes_file)
        self.assertIn("# Release Notes", content)

        empty_file = Path(self.temp_dir.name) / "empty.md"
        empty_file.write_text("", encoding="utf-8")
        with self.assertRaises(HandledError):
            read_notes_file(empty_file)

        missing_file = Path(self.temp_dir.name) / "nonexistent.md"
        with self.assertRaises(HandledError):
            read_notes_file(missing_file)

    @patch("subprocess.run")
    def test_verify_local_tag(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="0.6.0\n", returncode=0)
        with self.assertRaises(HandledError):
            verify_local_tag("0.6.0", Path("."), force=False)
        # Should not raise when force=True
        verify_local_tag("0.6.0", Path("."), force=True)

        mock_subprocess.return_value = MagicMock(stdout="", returncode=0)
        verify_local_tag("0.7.0", Path("."), force=False)

        # Test non-zero return code (e.g., not a git repository)
        mock_subprocess.return_value = MagicMock(
            stdout="", stderr="fatal: not a git repository", returncode=128
        )
        with self.assertRaises(HandledError) as ctx:
            verify_local_tag("0.7.0", Path("."), force=False)
        self.assertIn("Failed to check local git tags", str(ctx.exception))

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError) as ctx:
            verify_local_tag("0.7.0", Path("."), force=False)
        self.assertIn("git command not found", str(ctx.exception))

    @patch("subprocess.run")
    def test_create_git_tag_and_error(self, mock_subprocess: MagicMock) -> None:
        create_git_tag("0.6.0", "HEAD", "Release 0.6.0", Path("."), force=False)
        self.assertTrue(mock_subprocess.called)

        mock_subprocess.side_effect = subprocess.CalledProcessError(1, "git", stderr="tag error")
        with self.assertRaises(HandledError):
            create_git_tag("0.6.0", "HEAD", "Release 0.6.0", Path("."), force=False)

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError) as ctx:
            create_git_tag("0.6.0", "HEAD", "Release 0.6.0", Path("."), force=False)
        self.assertIn("git command not found", str(ctx.exception))

    @patch("subprocess.run")
    def test_push_git_tag_and_errors(self, mock_subprocess: MagicMock) -> None:
        push_git_tag("0.6.0", Path("."), force=False, remote="upstream")
        self.assertEqual(mock_subprocess.call_args[0][0], ["git", "push", "upstream", "0.6.0"])

        mock_subprocess.side_effect = subprocess.CalledProcessError(
            1, "git", stderr="remote tag already exists"
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

        mock_subprocess.side_effect = FileNotFoundError()
        with self.assertRaises(HandledError) as ctx:
            push_git_tag("0.6.0", Path("."), force=False)
        self.assertIn("git command not found", str(ctx.exception))

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch("subprocess.run")
    def test_publish_via_gh_cli(
        self, mock_subprocess: MagicMock, mock_remote_url: MagicMock
    ) -> None:
        mock_remote_url.return_value = "git@github.com:myorg/myrepo.git"
        mock_subprocess.return_value = MagicMock(
            stdout="https://github.com/myorg/myrepo/releases/tag/0.6.0\n",
            returncode=0,
        )
        url = publish_via_gh_cli(
            "0.6.0",
            "Release 0.6.0",
            self.notes_file,
            draft=False,
            prerelease=False,
            target_commit="HEAD",
            repo_dir=Path("."),
            remote="origin",
        )
        self.assertEqual(url, "https://github.com/myorg/myrepo/releases/tag/0.6.0")
        self.assertIn("--repo", mock_subprocess.call_args[0][0])
        self.assertIn("myorg/myrepo", mock_subprocess.call_args[0][0])

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

    @patch("tools.release.publish_release.get_git_remote_url")
    @patch("tools.release.publish_release.call_github_api")
    def test_publish_via_rest_api(self, mock_api: MagicMock, mock_remote_url: MagicMock) -> None:
        mock_remote_url.return_value = "https://github.com/google/xmc"
        mock_api.return_value = {
            "html_url": "https://github.com/google/xmc/releases/tag/0.6.0",
            "id": 12345,
        }
        url, rel_id = publish_via_rest_api(
            "0.6.0",
            "Release 0.6.0",
            "# Notes",
            draft=True,
            prerelease=False,
            target_commit="HEAD",
            repo_dir=Path("."),
        )
        self.assertEqual(url, "https://github.com/google/xmc/releases/tag/0.6.0")
        self.assertEqual(rel_id, 12345)

        mock_api.side_effect = GithubAPIError("API Error")
        with self.assertRaises(HandledError):
            publish_via_rest_api(
                "0.6.0",
                "Release 0.6.0",
                "# Notes",
                draft=True,
                prerelease=False,
                target_commit="HEAD",
                repo_dir=Path("."),
            )

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
        mock_subprocess.return_value = MagicMock(
            stdout="https://github.com/google/xmc/releases/tag/0.6.0\n",
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
            skip_push=False,
            force=False,
        )
        res = publish_release(args)
        self.assertEqual(res["status"], "success")
        self.assertEqual(res["tag"], "0.6.0")

    def test_publish_release_empty_tag(self) -> None:
        args = argparse.Namespace(
            tag="   ",
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
    @patch("tools.release.publish_release.call_github_api")
    @patch.dict("os.environ", {"GITHUB_OWNER": "custom_owner"}, clear=True)
    def test_publish_via_rest_api_single_env_var(
        self, mock_api: MagicMock, mock_remote_url: MagicMock
    ) -> None:
        mock_remote_url.return_value = "https://github.com/google/xmc"
        mock_api.return_value = {"html_url": "https://github.com/custom_owner/xmc/releases/1"}
        publish_via_rest_api(
            "0.6.0",
            "Release 0.6.0",
            "# Notes",
            draft=False,
            prerelease=False,
            target_commit="HEAD",
            repo_dir=Path("."),
        )
        self.assertEqual(mock_api.call_args[0][0], "/repos/custom_owner/xmc/releases")

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
