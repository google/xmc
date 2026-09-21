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

"""Unit tests for GitHub release client transports."""

import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.release.github_release_client import _build_gh_release_cmd, publish_via_gh_cli


class GithubReleaseClientTest(unittest.TestCase):
    """Test suite for github_release_client."""

    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.notes_file = Path(self.temp_dir.name) / "notes.md"
        self.notes_file.write_text("# Release Notes\n- Some changes\n", encoding="utf-8")

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def test_build_gh_release_cmd(self) -> None:
        cmd = _build_gh_release_cmd(
            tag="0.6.0",
            title="Release 0.6.0",
            notes_path=self.notes_file,
            owner="google",
            repo="xmc",
            draft=True,
            prerelease=True,
            target_commit="a1b2c3d4",
            force=True,
            skip_push=False,
        )
        self.assertIn("--draft", cmd)
        self.assertIn("--prerelease", cmd)
        self.assertIn("--target", cmd)
        self.assertIn("--clobber", cmd)
        self.assertNotIn("--verify-tag", cmd)

        cmd_skip = _build_gh_release_cmd(
            tag="0.6.0",
            title="Release 0.6.0",
            notes_path=self.notes_file,
            owner="google",
            repo="xmc",
            draft=False,
            prerelease=False,
            target_commit="a1b2c3d4",
            force=False,
            skip_push=True,
        )
        self.assertIn("--verify-tag", cmd_skip)
        self.assertNotIn("--target", cmd_skip)

    @patch("tools.release.github_release_client.resolve_repo_owner_and_name")
    @patch("subprocess.run")
    def test_publish_via_gh_cli(
        self, mock_subprocess: MagicMock, mock_resolve_repo: MagicMock
    ) -> None:
        mock_resolve_repo.return_value = ("myorg", "myrepo")
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


if __name__ == "__main__":
    unittest.main()
