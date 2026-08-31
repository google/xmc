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

"""Unit tests for SemVer parsing and recommendation calculation."""

import sys
import unittest
from pathlib import Path

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.models import CommitInfo, FlagTransition
    from tools.release.semver_engine import (
        calculate_version_recommendation,
        format_bumped_version,
        parse_semver,
    )
except ImportError:
    from models import CommitInfo, FlagTransition  # type: ignore[no-redef]
    from semver_engine import (  # type: ignore[no-redef]
        calculate_version_recommendation,
        format_bumped_version,
        parse_semver,
    )


class SemverEngineTest(unittest.TestCase):
    """Tests for SemVer parser and recommendation engine."""

    def test_parse_semver(self) -> None:
        self.assertEqual(parse_semver("0.5.0"), (0, 5, 0))
        self.assertEqual(parse_semver("v1.2.3"), (1, 2, 3))
        self.assertEqual(parse_semver("invalid"), (0, 0, 0))

    def test_format_bumped_version(self) -> None:
        self.assertEqual(format_bumped_version(0, 5, 0, "minor"), "0.6.0")
        self.assertEqual(format_bumped_version(0, 5, 0, "patch"), "0.5.1")
        self.assertEqual(format_bumped_version(0, 5, 0, "major"), "1.0.0")

    def test_calculate_version_recommendation_major(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="1111",
                short_hash="1111",
                subject="feat(api)!: remove legacy endpoint (#500)",
                author="Dev",
                date="2026-08-21",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [], {"user_facing_features": [{}]})
        self.assertEqual(rec.recommended_bump, "major")
        self.assertEqual(rec.recommended_version, "1.0.0")
        self.assertTrue(rec.has_breaking_changes)

    def test_calculate_version_recommendation_minor_promotions(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="2222",
                short_hash="2222",
                subject="feat(components): enable by default (#501)",
                author="Dev",
                date="2026-08-21",
                files_changed=["pkg/spanner/schema.sql"],
            )
        ]
        transitions = [
            FlagTransition(
                flag_name="enable_components",
                source_file="deploy/ctk_v2/xmanager-cloud.yaml",
                base_value=False,
                target_value=True,
                is_flip=True,
                is_promotion_to_default=True,
                description="Promoted enable_components to default true",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, transitions, {})
        self.assertEqual(rec.recommended_bump, "minor")
        self.assertEqual(rec.recommended_version, "0.6.0")
        self.assertTrue(rec.has_promotions)
        self.assertTrue(rec.has_migrations)

    def test_calculate_version_recommendation_patch_fixes(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="3333",
                short_hash="3333",
                subject="fix: handle connection timeout (#502)",
                author="Dev",
                date="2026-08-21",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [], {"user_facing_fixes": [{}]})
        self.assertEqual(rec.recommended_bump, "patch")
        self.assertEqual(rec.recommended_version, "0.5.1")
        self.assertTrue(rec.has_ootb_fixes)

    def test_calculate_version_recommendation_patch_maintenance(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="4444",
                short_hash="4444",
                subject="chore: update CI scripts (#503)",
                author="Dev",
                date="2026-08-21",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [], {"developer_velocity": [{}]})
        self.assertEqual(rec.recommended_bump, "patch")
        self.assertEqual(rec.recommended_version, "0.5.1")
        self.assertIn("maintenance", rec.rationale)


if __name__ == "__main__":
    unittest.main()
