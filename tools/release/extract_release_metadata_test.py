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

"""Unit tests for extract_release_metadata tool."""

import argparse
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
    from tools.release.extract_release_metadata import (
        CommitInfo,
        create_parser,
        extract_release_metadata,
        parse_conventional_commit,
    )
except ImportError:
    from common.tool_utils import HandledError  # type: ignore[no-redef]
    from extract_release_metadata import (  # type: ignore[no-redef]
        CommitInfo,
        create_parser,
        extract_release_metadata,
        parse_conventional_commit,
    )


class ExtractReleaseMetadataTest(unittest.TestCase):
    """Test suite for release metadata extraction and classification."""

    def test_parse_conventional_commit(self) -> None:
        commit_type, scope, desc, pr = parse_conventional_commit(
            "feat(ui): implement live resource picker (#436)"
        )
        self.assertEqual(commit_type, "feat")
        self.assertEqual(scope, "ui")
        self.assertEqual(desc, "implement live resource picker")
        self.assertEqual(pr, 436)

    @patch("tools.release.extract_release_metadata.get_git_commits")
    def test_extract_release_metadata_handler(
        self,
        mock_commits: MagicMock,
    ) -> None:
        mock_commits.return_value = [
            CommitInfo(
                commit_hash="1111",
                short_hash="1111",
                subject="feat(ui): add live queue picker (#436)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat",
                scope="ui",
                description="add live queue picker",
                pr_number=436,
                files_changed=["ui/src/app/queue-picker.ts"],
            ),
            CommitInfo(
                commit_hash="2222",
                short_hash="2222",
                subject="fix(core): resolve race (#470)",
                author="Dev",
                date="2026-08-21",
                commit_type="fix",
                scope="core",
                description="resolve race",
                pr_number=470,
                files_changed=["pkg/core/server.go"],
            ),
        ]

        args = argparse.Namespace(
            base_ref="0.5.0",
            target_ref="0.6.0",
            repo_dir=".",
            output=None,
        )

        res = extract_release_metadata(args)
        self.assertEqual(res["metadata"]["commit_count"], 2)
        self.assertEqual(res["metadata"]["pr_count"], 2)
        self.assertEqual(len(res["categorized_commits"]["user_facing_features"]), 1)
        self.assertEqual(len(res["categorized_commits"]["user_facing_fixes"]), 1)
        self.assertEqual(len(res["raw_commits"]), 2)

    @patch("tools.release.extract_release_metadata.get_git_commits")
    def test_extract_release_metadata_with_output_file(
        self,
        mock_commits: MagicMock,
    ) -> None:
        mock_commits.return_value = []

        with tempfile.NamedTemporaryFile(suffix=".json", delete=False) as tmp:
            tmp_path = tmp.name

        try:
            args = argparse.Namespace(
                base_ref="0.5.0",
                target_ref="0.6.0",
                repo_dir=".",
                output=tmp_path,
            )
            res = extract_release_metadata(args)
            self.assertEqual(res["metadata"]["commit_count"], 0)
            self.assertTrue(Path(tmp_path).exists())
        finally:
            Path(tmp_path).unlink(missing_ok=True)

    @patch("tools.release.extract_release_metadata.get_latest_tag_before")
    def test_extract_release_metadata_auto_base_ref_error(self, mock_get_latest: MagicMock) -> None:
        mock_get_latest.return_value = None
        args = argparse.Namespace(
            base_ref="",
            target_ref="0.6.0",
            repo_dir=".",
            output=None,
        )
        with self.assertRaises(HandledError):
            extract_release_metadata(args)

    def test_create_parser(self) -> None:
        parser = create_parser()
        self.assertIsNotNone(parser)


if __name__ == "__main__":
    unittest.main()
