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

import os
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.pr_review.github_utils import GithubAPIError
from tools.release.github_release_client import (
    _build_gh_release_cmd,
    publish_via_gh_cli,
    publish_via_rest_api,
)


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
            skip_push=False,
        )
        self.assertIn("--draft", cmd)
        self.assertIn("--prerelease", cmd)
        self.assertIn("--target", cmd)
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

    @patch("tools.release.github_release_client.resolve_repo_owner_and_name")
    @patch("tools.release.github_release_client.call_github_api")
    def test_publish_via_rest_api(self, mock_api: MagicMock, mock_resolve_repo: MagicMock) -> None:
        mock_resolve_repo.return_value = ("google", "xmc")
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

        mock_api.side_effect = None
        mock_api.return_value = ["unexpected_list_not_dict"]
        with self.assertRaises(HandledError) as ctx:
            publish_via_rest_api(
                "0.6.0",
                "Release 0.6.0",
                "# Notes",
                draft=True,
                prerelease=False,
                target_commit="HEAD",
                repo_dir=Path("."),
            )
        self.assertIn("unexpected response format: list", str(ctx.exception))

    @patch("tools.release.github_release_client.resolve_repo_owner_and_name")
    @patch("tools.release.github_release_client.call_github_api")
    @patch.dict(
        "os.environ",
        {"GITHUB_OWNER": "custom_owner", "GITHUB_TOKEN": "", "GH_TOKEN": "fallback_gh_token"},
        clear=True,
    )
    def test_publish_via_rest_api_single_env_var(
        self, mock_api: MagicMock, mock_resolve_repo: MagicMock
    ) -> None:
        mock_resolve_repo.return_value = ("custom_owner", "xmc")
        captured_token: list[str | None] = []

        def _fake_api(*_args: object, **_kwargs: object) -> dict[str, str]:
            captured_token.append(os.environ.get("GITHUB_TOKEN"))
            return {"html_url": "https://github.com/custom_owner/xmc/releases/1"}

        mock_api.side_effect = _fake_api
        publish_via_rest_api(
            "0.6.0",
            "Release 0.6.0",
            "# Notes",
            draft=False,
            prerelease=False,
            target_commit="HEAD",
            repo_dir=Path("."),
        )
        mock_api.assert_called_once_with(
            "/repos/custom_owner/xmc/releases",
            method="POST",
            payload={
                "tag_name": "0.6.0",
                "target_commitish": "HEAD",
                "name": "Release 0.6.0",
                "body": "# Notes",
                "draft": False,
                "prerelease": False,
            },
            user_agent="XMC-Release-Publisher",
        )
        self.assertEqual(captured_token, ["fallback_gh_token"])
        self.assertEqual(os.environ.get("GITHUB_TOKEN"), "")


if __name__ == "__main__":
    unittest.main()
