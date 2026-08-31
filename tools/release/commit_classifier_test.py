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

"""Unit tests for commit classification and conventional commit parser."""

import sys
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.commit_classifier import (
        classify_commit,
        parse_conventional_commit,
    )
    from tools.release.models import CommitInfo
except ImportError:
    from commit_classifier import (  # type: ignore[no-redef]
        classify_commit,
        parse_conventional_commit,
    )
    from models import CommitInfo  # type: ignore[no-redef]


class CommitClassifierTest(unittest.TestCase):
    """Tests for commit classification into release categories."""

    def test_parse_conventional_commit(self) -> None:
        c_type, scope, desc, pr = parse_conventional_commit("feat(ui): add dashboard (#123)")
        self.assertEqual(c_type, "feat")
        self.assertEqual(scope, "ui")
        self.assertEqual(desc, "add dashboard")
        self.assertEqual(pr, 123)

    def test_classify_commit_promotion(self) -> None:
        commit = CommitInfo(
            commit_hash="1111",
            short_hash="1111",
            subject="feat(config): enable_components is now default",
            author="Dev",
            date="2026-08-21",
        )
        classify_commit(commit, {}, {"enable_components"})
        self.assertEqual(commit.category, "highlights_and_promotions")
        self.assertTrue(commit.is_promotion)

    def test_classify_commit_developer_velocity(self) -> None:
        commit = CommitInfo(
            commit_hash="2222",
            short_hash="2222",
            subject="chore(deps): bump go-cmp (#400)",
            author="Dev",
            date="2026-08-21",
            commit_type="chore",
            scope="deps",
        )
        classify_commit(commit, {}, set())
        self.assertEqual(commit.category, "developer_velocity")

    def test_classify_commit_gated_preview(self) -> None:
        commit = CommitInfo(
            commit_hash="3333",
            short_hash="3333",
            subject="feat(rod): add metric aggregation (#405)",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="rod",
            files_changed=["pkg/rod/metrics.go"],
        )
        active_states = {"rod_deployment_enabled": False}
        classify_commit(commit, active_states, set(), approved_flags=set())
        self.assertEqual(commit.category, "experimental_preview_features")

        classify_commit(
            commit,
            active_states,
            set(),
            approved_flags={"rod_deployment_enabled"},
        )
        self.assertEqual(commit.category, "approved_preview_features")

    def test_classify_commit_public_feature_and_fix(self) -> None:
        commit_feat = CommitInfo(
            commit_hash="4444",
            short_hash="4444",
            subject="feat(ui): add search filter (#410)",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="ui",
            files_changed=["ui/app/search.ts"],
        )
        classify_commit(commit_feat, {}, set())
        self.assertEqual(commit_feat.category, "user_facing_features")

        commit_fix = CommitInfo(
            commit_hash="5555",
            short_hash="5555",
            subject="fix(ui): handle null queue (#411)",
            author="Dev",
            date="2026-08-21",
            commit_type="fix",
            scope="ui",
            files_changed=["ui/app/search.ts"],
        )
        classify_commit(commit_fix, {}, set())
        self.assertEqual(commit_fix.category, "user_facing_fixes")


if __name__ == "__main__":
    unittest.main()
