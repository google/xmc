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

import unittest
from typing import Any

from tools.release.models import CommitInfo, FlagTransition
from tools.release.semver_engine import (
    calculate_version_recommendation,
    format_bumped_version,
    is_semver_tag,
    parse_semver,
)


class SemverEngineTest(unittest.TestCase):
    """Tests for SemVer parser and recommendation engine."""

    def test_is_semver_tag_accepts_release_tags(self) -> None:
        for tag in ("0.17.0", "v1.2.3", " 10.20.30\n"):
            with self.subTest(tag=tag):
                self.assertTrue(is_semver_tag(tag))

    def test_is_semver_tag_rejects_non_release_tags(self) -> None:
        for tag in ("nightly-2026-11-09.115", "1.2.3-rc1", "1.2", "x1.2.3", "", None, 123):
            with self.subTest(tag=tag):
                self.assertFalse(is_semver_tag(tag))  # type: ignore[arg-type]

    def test_parse_semver(self) -> None:
        self.assertEqual(parse_semver("0.5.0"), (0, 5, 0))
        self.assertEqual(parse_semver("v1.2.3"), (1, 2, 3))
        self.assertEqual(parse_semver("invalid"), (0, 0, 0))
        self.assertEqual(parse_semver(""), (0, 0, 0))
        self.assertEqual(parse_semver(None), (0, 0, 0))  # type: ignore[arg-type]
        self.assertEqual(parse_semver(123), (0, 0, 0))  # type: ignore[arg-type]

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
                commit_type="feat",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [])
        self.assertEqual(rec.recommended_bump, "major")
        self.assertEqual(rec.recommended_version, "1.0.0")
        self.assertTrue(rec.has_breaking_changes)

    def test_calculate_version_recommendation_breaking_in_description(self) -> None:
        commits: list[Any] = [
            None,
            CommitInfo(
                commit_hash="1112",
                short_hash="1112",
                subject="refactor: overhaul client interface (#500)",
                description="BREAKING CHANGE: changes authentication callback signature",
                author="Dev",
                date="2026-08-21",
                commit_type="refactor",
            ),
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [])
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
                commit_type="feat",
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
        rec = calculate_version_recommendation("0.5.0", commits, transitions)
        self.assertEqual(rec.recommended_bump, "minor")
        self.assertEqual(rec.recommended_version, "0.6.0")
        self.assertTrue(rec.has_promotions)
        self.assertTrue(rec.has_migrations)

    def test_calculate_version_recommendation_minor_features(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="2223",
                short_hash="2223",
                subject="feat(ui): add dashboard (#501)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [])
        self.assertEqual(rec.recommended_bump, "minor")
        self.assertEqual(rec.recommended_version, "0.6.0")
        self.assertTrue(rec.has_ootb_features)

    def test_calculate_version_recommendation_minor_approved_flags(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="2224",
                short_hash="2224",
                subject="chore: prepare rod (#501)",
                author="Dev",
                date="2026-08-21",
                commit_type="chore",
            )
        ]
        rec = calculate_version_recommendation(
            "0.5.0", commits, [], approved_flags=["rod_deployment_enabled"]
        )
        self.assertEqual(rec.recommended_bump, "minor")
        self.assertEqual(rec.recommended_version, "0.6.0")
        self.assertTrue(rec.has_approved_features)

    def test_calculate_version_recommendation_patch_fixes(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="3333",
                short_hash="3333",
                subject="fix: handle connection timeout (#502)",
                author="Dev",
                date="2026-08-21",
                commit_type="fix",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [])
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
                commit_type="chore",
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [])
        self.assertTrue(any("maintenance" in r.lower() for r in rec.rationale))

    def test_calculate_version_recommendation_uppercase_migrations(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="4445",
                short_hash="4445",
                subject="chore(db): add init schema (#504)",
                author="Dev",
                date="2026-08-21",
                commit_type="chore",
                files_changed=["pkg/schema/001_INIT.SQL"],
            )
        ]
        rec = calculate_version_recommendation("0.5.0", commits, [])
        self.assertTrue(rec.has_migrations)
        self.assertTrue(any("migration" in r.lower() for r in rec.rationale))

    def test_calculate_version_recommendation_empty_changes(self) -> None:
        rec = calculate_version_recommendation("0.5.0", [], [])
        self.assertEqual(rec.recommended_bump, "none")
        self.assertEqual(rec.recommended_version, "0.5.0")
        self.assertEqual(rec.rationale, ["No changes detected between releases."])

    def test_parse_semver_whitespace(self) -> None:
        self.assertEqual(parse_semver("  v1.2.3  "), (1, 2, 3))
        self.assertEqual(parse_semver("  2.4.6\n"), (2, 4, 6))

    def test_parse_semver_git_refs(self) -> None:
        self.assertEqual(parse_semver("refs/tags/v1.2.3"), (1, 2, 3))
        self.assertEqual(parse_semver("origin/tags/v2.4.6"), (2, 4, 6))
        self.assertEqual(parse_semver("refs/tags/0.5.0"), (0, 5, 0))

    def test_format_bumped_version_explicit_and_invalid(self) -> None:
        self.assertEqual(format_bumped_version(1, 2, 3, "major"), "2.0.0")
        self.assertEqual(format_bumped_version(1, 2, 3, "minor"), "1.3.0")
        self.assertEqual(format_bumped_version(1, 2, 3, "patch"), "1.2.4")
        self.assertEqual(format_bumped_version(1, 2, 3, "none"), "1.2.3")
        with self.assertRaises(ValueError):
            format_bumped_version(1, 2, 3, "invalid_bump")

    def test_calculate_version_recommendation_preserves_v_prefix(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="3333",
                short_hash="3333",
                subject="feat: add new feature (#502)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat",
            )
        ]
        rec = calculate_version_recommendation("v0.5.0", commits, [])
        self.assertEqual(rec.recommended_bump, "minor")
        self.assertEqual(rec.recommended_version, "v0.6.0")

        # Also test with git ref base tag
        rec_ref = calculate_version_recommendation("refs/tags/v0.5.0", commits, [])
        self.assertEqual(rec_ref.recommended_bump, "minor")
        self.assertEqual(rec_ref.recommended_version, "v0.6.0")

    def test_calculate_version_recommendation_migration_heuristics(self) -> None:
        # Non-migration file that happens to contain "migration" in name
        commits_non_migration = [
            CommitInfo(
                commit_hash="5555",
                short_hash="5555",
                subject="chore: refactor migration engine (#505)",
                author="Dev",
                date="2026-08-21",
                commit_type="chore",
                files_changed=["tools/release/migration_engine.py"],
            )
        ]
        rec_non = calculate_version_recommendation("0.5.0", commits_non_migration, [])
        self.assertFalse(rec_non.has_migrations)

        # Migration file in migrations directory
        commits_migration_dir = [
            CommitInfo(
                commit_hash="5556",
                short_hash="5556",
                subject="chore: add migration file (#506)",
                author="Dev",
                date="2026-08-21",
                commit_type="chore",
                files_changed=["db/migrations/001_create_users.py"],
            )
        ]
        rec_dir = calculate_version_recommendation("0.5.0", commits_migration_dir, [])
        self.assertTrue(rec_dir.has_migrations)

        # DDL file
        commits_ddl = [
            CommitInfo(
                commit_hash="5557",
                short_hash="5557",
                subject="chore: add schema DDL (#507)",
                author="Dev",
                date="2026-08-21",
                commit_type="chore",
                files_changed=["pkg/db/schema.ddl"],
            )
        ]
        rec_ddl = calculate_version_recommendation("0.5.0", commits_ddl, [])
        self.assertTrue(rec_ddl.has_migrations)

        # Windows backslash paths
        commits_win = [
            CommitInfo(
                commit_hash="5558",
                short_hash="5558",
                subject="chore: add windows migration path (#508)",
                author="Dev",
                date="2026-08-21",
                commit_type="chore",
                files_changed=["db\\migrations\\002_win.sql"],
            )
        ]
        rec_win = calculate_version_recommendation("0.5.0", commits_win, [])
        self.assertTrue(rec_win.has_migrations)

    def test_parse_semver_prefix_not_stripped_from_words(self) -> None:
        self.assertEqual(parse_semver("validation-1.0.0"), (0, 0, 0))
        self.assertEqual(parse_semver("value-1.0.0"), (0, 0, 0))

    def test_calculate_version_recommendation_none_base_tag(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="3333",
                short_hash="3333",
                subject="feat: initial feature (#502)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat",
            )
        ]
        rec = calculate_version_recommendation("", commits, [])
        self.assertEqual(rec.current_version, "")
        self.assertEqual(rec.recommended_version, "0.1.0")
        self.assertEqual(rec.recommended_bump, "minor")


if __name__ == "__main__":
    unittest.main()
