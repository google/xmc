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
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.release.publish_release import (
    create_parser,
    publish_release,
    read_notes_file,
)


class PublishReleaseTest(unittest.TestCase):
    """Test suite for publish_release CLI orchestrator."""

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
        self.assertIn("pushed to", ctx.exception.instruction)

        mock_publish_gh.side_effect = HandledError("gh failed", "fix gh")
        args.skip_push = True
        with self.assertRaises(HandledError) as ctx:
            publish_release(args)
        self.assertIn("created locally", ctx.exception.instruction)

        # When tag already existed and skip_push is True, no git mutations occurred;
        # deletion advice should NOT be appended.
        mock_publish_gh.side_effect = HandledError("gh failed", "fix gh")
        with patch("tools.release.publish_release.verify_local_tag", return_value=True):
            with self.assertRaises(HandledError) as ctx:
                publish_release(args)
            self.assertNotIn("created locally", ctx.exception.instruction)
            self.assertNotIn("pushed to", ctx.exception.instruction)

    @patch("tools.release.git_ops.get_git_remote_url")
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

    @patch("tools.release.git_ops.get_git_remote_url")
    @patch("shutil.which")
    @patch("subprocess.run")
    @patch("tools.release.github_release_client.call_github_api")
    def test_publish_release_fallback_to_rest(
        self,
        mock_api: MagicMock,
        mock_subprocess: MagicMock,
        mock_which: MagicMock,
        mock_remote_url: MagicMock,
    ) -> None:
        mock_remote_url.return_value = "https://github.com/google/xmc"
        mock_which.return_value = None
        mock_subprocess.return_value = MagicMock(returncode=0, stdout="")
        mock_api.return_value = {
            "html_url": "https://github.com/google/xmc/releases/tag/0.6.0",
            "id": 9999,
        }

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
        self.assertEqual(res["release_url"], "https://github.com/google/xmc/releases/tag/0.6.0")
        self.assertEqual(res["release_id"], 9999)

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

    @patch("tools.release.git_ops.get_git_remote_url")
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
        self.assertIn("created locally", ctx.exception.instruction)

        args.skip_push = False
        with self.assertRaises(HandledError) as ctx:
            publish_release(args)
        self.assertIn("Release URL is empty", str(ctx.exception))
        self.assertIn("pushed to", ctx.exception.instruction)

    @patch("tools.release.git_ops.get_git_remote_url")
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

    def test_create_parser(self) -> None:
        parser = create_parser()
        self.assertIsNotNone(parser)
        args = parser.parse_args(
            ["--tag", "0.6.0", "--notes-file", "notes.md", "--remote", "upstream"]
        )
        self.assertEqual(args.remote, "upstream")


if __name__ == "__main__":
    unittest.main()
