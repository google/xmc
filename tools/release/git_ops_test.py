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

"""Unit tests for Git operations, tag discovery, and commit extraction."""

import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.common.tool_utils import HandledError
    from tools.release.git_ops import (
        _create_commit_from_header,
        get_git_commits,
        get_git_file_content,
        get_latest_tag_before,
        parse_remote_url,
    )
except ImportError:
    from common.tool_utils import HandledError  # type: ignore[no-redef]
    from git_ops import (  # type: ignore[no-redef]
        _create_commit_from_header,
        get_git_commits,
        get_git_file_content,
        get_latest_tag_before,
        parse_remote_url,
    )


class GitOpsTest(unittest.TestCase):
    """Tests for Git operations utilities."""

    @patch("subprocess.run")
    def test_get_git_file_content_success_and_error(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="file content", returncode=0)
        content = get_git_file_content("0.5.0", "path/to/file.yaml", Path("."))
        self.assertEqual(content, "file content")

        mock_subprocess.side_effect = subprocess.CalledProcessError(1, "git", stderr="not found")
        self.assertEqual(get_git_file_content("0.5.0", "missing.yaml", Path(".")), "")

    @patch("subprocess.run")
    def test_get_latest_tag_before(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(stdout="0.5.0\n", returncode=0)
        self.assertEqual(get_latest_tag_before("0.6.0", Path(".")), "0.5.0")

        # Fallback to tag list
        def mock_side_effect(cmd, **_kwargs):
            if "describe" in cmd:
                raise subprocess.CalledProcessError(1, "git")
            return MagicMock(stdout="0.6.0\n0.5.0\n0.4.0\n", returncode=0)

        mock_subprocess.side_effect = mock_side_effect
        self.assertEqual(get_latest_tag_before("0.6.0", Path(".")), "0.5.0")
        self.assertEqual(get_latest_tag_before("HEAD", Path(".")), "0.6.0")

    @patch("subprocess.run")
    def test_parse_remote_url(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.return_value = MagicMock(
            stdout="https://github.com/my-org/my-repo.git\n", returncode=0
        )
        owner, repo = parse_remote_url(Path("."))
        self.assertEqual(owner, "my-org")
        self.assertEqual(repo, "my-repo")

        mock_subprocess.return_value = MagicMock(
            stdout="git@github.com:google/xmc.git\n", returncode=0
        )
        owner, repo = parse_remote_url(Path("."))
        self.assertEqual(owner, "google")
        self.assertEqual(repo, "xmc")

        mock_subprocess.side_effect = Exception("git error")
        owner, repo = parse_remote_url(Path("."))
        self.assertEqual(owner, "google")
        self.assertEqual(repo, "xmc")

    def test_create_commit_from_header_invalid(self) -> None:
        self.assertIsNone(_create_commit_from_header("COMMIT:short"))

    @patch("subprocess.run")
    def test_get_git_commits_error(self, mock_subprocess: MagicMock) -> None:
        mock_subprocess.side_effect = subprocess.CalledProcessError(
            1, "git", stderr="fatal: bad revision"
        )
        with self.assertRaises(HandledError):
            get_git_commits("invalid_base", "invalid_target", Path("."))


if __name__ == "__main__":
    unittest.main()
