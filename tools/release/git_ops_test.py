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

import subprocess
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

from tools.common.tool_utils import HandledError
from tools.release.git_ops import (
    get_git_commits,
    get_git_file_content,
    get_latest_tag_before,
    parse_remote_url,
)


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

    def test_parse_remote_url(self) -> None:
        self.assertEqual(parse_remote_url("git@github.com:google/xmc.git"), ("google", "xmc"))
        self.assertEqual(parse_remote_url("https://github.com/google/xmc/"), ("google", "xmc"))
        self.assertEqual(parse_remote_url("https://github.com/google/xmc"), ("google", "xmc"))
        self.assertEqual(parse_remote_url("invalid"), ("google", "xmc"))


if __name__ == "__main__":
    unittest.main()
