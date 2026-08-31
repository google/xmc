#!/usr/bin/env python3
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
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.extract_release_metadata import (
        CommitInfo,
        FlagTransition,
        calculate_version_recommendation,
        classify_commit,
        extract_flag_transitions,
        extract_release_metadata,
        get_current_feature_states,
        parse_conventional_commit,
        parse_semver,
        parse_yaml_booleans,
    )
except ImportError:
    from extract_release_metadata import (  # type: ignore[no-redef]
        CommitInfo,
        FlagTransition,
        calculate_version_recommendation,
        classify_commit,
        extract_flag_transitions,
        extract_release_metadata,
        get_current_feature_states,
        parse_conventional_commit,
        parse_semver,
        parse_yaml_booleans,
    )


class ExtractReleaseMetadataTest(unittest.TestCase):
    """Test suite for release metadata extraction and classification."""

    def test_parse_conventional_commit_standard(self) -> None:
        commit_type, scope, desc, pr = parse_conventional_commit(
            "feat(ui): implement live resource picker queue filtering (#436)"
        )
        self.assertEqual(commit_type, "feat")
        self.assertEqual(scope, "ui")
        self.assertEqual(desc, "implement live resource picker queue filtering")
        self.assertEqual(pr, 436)

    def test_parse_conventional_commit_no_scope(self) -> None:
        commit_type, scope, desc, pr = parse_conventional_commit(
            "fix: self-closing app-root (#425)"
        )
        self.assertEqual(commit_type, "fix")
        self.assertEqual(scope, "")
        self.assertEqual(desc, "self-closing app-root")
        self.assertEqual(pr, 425)

    def test_parse_conventional_commit_breaking_syntax(self) -> None:
        commit_type, scope, desc, pr = parse_conventional_commit(
            "feat(api)!: remove deprecated v1 endpoints (#500)"
        )
        self.assertEqual(commit_type, "feat")
        self.assertEqual(scope, "api")
        self.assertEqual(desc, "remove deprecated v1 endpoints")
        self.assertEqual(pr, 500)

    def test_parse_conventional_commit_non_conventional(self) -> None:
        commit_type, scope, desc, pr = parse_conventional_commit("Feature/data engine (#427)")
        self.assertEqual(commit_type, "other")
        self.assertEqual(scope, "")
        self.assertEqual(desc, "Feature/data engine")
        self.assertEqual(pr, 427)

    def test_parse_yaml_booleans(self) -> None:
        sample_yaml = """
        blueprint_name: xmc
        vars:
          enable_components: false
          enable_metrics: true
          # Commented out
          # health_service_deployment_enabled: true
          use_redis_queue: true
          taskSignatureTtlSeconds: 86400
        """
        flags = parse_yaml_booleans(sample_yaml)
        self.assertEqual(flags.get("enable_components"), False)
        self.assertEqual(flags.get("enable_metrics"), True)
        self.assertEqual(flags.get("use_redis_queue"), True)
        self.assertNotIn("blueprint_name", flags)
        self.assertNotIn("taskSignatureTtlSeconds", flags)

    @patch("tools.release.extract_release_metadata.get_git_file_content")
    def test_extract_flag_transitions(self, mock_get_content: MagicMock) -> None:
        base_yaml = """
        vars:
          enable_components: false
          rod_deployment_enabled: false
          health_service_deployment_enabled: false
        """
        target_yaml = """
        vars:
          enable_components: true
          rod_deployment_enabled: false
          health_service_deployment_enabled: false
        """

        def mock_content(ref: str, path: str, _repo: Path) -> str:
            if "ctk_v2" in path:
                return base_yaml if ref == "0.5.0" else target_yaml
            return ""

        mock_get_content.side_effect = mock_content

        transitions = extract_flag_transitions("0.5.0", "0.6.0", Path("."))
        self.assertTrue(any(t.flag_name == "enable_components" for t in transitions))
        comp_trans = next(t for t in transitions if t.flag_name == "enable_components")
        self.assertTrue(comp_trans.is_flip)
        self.assertTrue(comp_trans.is_promotion_to_default)
        self.assertFalse(comp_trans.base_value)
        self.assertTrue(comp_trans.target_value)

    @patch("tools.release.extract_release_metadata.get_git_file_content")
    def test_get_current_feature_states(self, mock_get_content: MagicMock) -> None:
        ctk_content = """
        vars:
          enable_components: false
          rod_deployment_enabled: true
          health_service_deployment_enabled: false
        """
        helm_content = """
        componentsDeployment:
          enabled: false
        rodDeployment:
          enabled: true
        """

        def mock_content(_ref: str, path: str, _repo: Path) -> str:
            if "ctk_v2" in path:
                return ctk_content
            return helm_content

        mock_get_content.side_effect = mock_content

        states = get_current_feature_states("0.6.0", Path("."))
        self.assertFalse(states["components"])
        self.assertTrue(states["rod"])
        self.assertFalse(states["health_service"])

    def test_classify_commit_experimental_feature(self) -> None:
        commit = CommitInfo(
            commit_hash="abc1",
            short_hash="abc1",
            subject="feat(components): implement controller skeleton (#470)",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="components",
            description="implement controller skeleton",
            pr_number=470,
            files_changed=["pkg/components/controller.go"],
        )
        active_states = {"components": False, "rod": True}
        promoted_flags: set[str] = set()
        approved_flags: set[str] = set()

        classify_commit(commit, active_states, promoted_flags, approved_flags)
        self.assertEqual(commit.category, "experimental_preview_features")
        self.assertEqual(commit.gated_subsystem, "components")
        self.assertEqual(commit.gated_flag, "enable_components")

    def test_classify_commit_approved_feature(self) -> None:
        commit = CommitInfo(
            commit_hash="abc1b",
            short_hash="abc1b",
            subject="feat(rod): implement secondary metrics (#474)",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="rod",
            description="implement secondary metrics",
            pr_number=474,
            files_changed=["pkg/rod/metrics.go"],
        )
        active_states = {"components": False, "rod": False}
        promoted_flags: set[str] = set()
        approved_flags = {"rod_deployment_enabled"}

        classify_commit(commit, active_states, promoted_flags, approved_flags)
        self.assertEqual(commit.category, "approved_preview_features")
        self.assertEqual(commit.gated_subsystem, "rod")
        self.assertEqual(commit.gated_flag, "rod_deployment_enabled")

    def test_classify_commit_active_feature(self) -> None:
        commit = CommitInfo(
            commit_hash="abc2",
            short_hash="abc2",
            subject="feat(rod): sort listSnapshot by key name (#435)",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="rod",
            description="sort listSnapshot by key name",
            pr_number=435,
            files_changed=["pkg/rod/cache.go"],
        )
        active_states = {"components": False, "rod": True}
        promoted_flags: set[str] = set()

        classify_commit(commit, active_states, promoted_flags)
        self.assertEqual(commit.category, "user_facing_features")
        self.assertEqual(commit.gated_subsystem, "rod")

    def test_classify_commit_promotion_highlight(self) -> None:
        commit = CommitInfo(
            commit_hash="abc3",
            short_hash="abc3",
            subject="feat(ctk): enable health_service_deployment_enabled by default (#500)",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="ctk",
            description="enable health_service_deployment_enabled by default",
            pr_number=500,
            files_changed=["deploy/ctk_v2/xmanager-cloud.yaml"],
        )
        active_states = {"health_service": True}
        promoted_flags = {"health_service_deployment_enabled"}

        classify_commit(commit, active_states, promoted_flags)
        self.assertEqual(commit.category, "highlights_and_promotions")
        self.assertTrue(commit.is_promotion)

    def test_classify_commit_operational_infra(self) -> None:
        commit = CommitInfo(
            commit_hash="abc4",
            short_hash="abc4",
            subject="feat(helm): support OAuth overrides (#467)",
            author="Dev",
            date="2026-08-21",
            commit_type="feat",
            scope="helm",
            description="support OAuth overrides",
            pr_number=467,
            files_changed=["deploy/helm/templates/gateway.yaml"],
        )
        active_states: dict[str, bool] = {}
        promoted_flags: set[str] = set()

        classify_commit(commit, active_states, promoted_flags)
        self.assertEqual(commit.category, "operational_and_infrastructure")

    def test_classify_commit_developer_velocity(self) -> None:
        commit = CommitInfo(
            commit_hash="abc5",
            short_hash="abc5",
            subject="perf(ci): optimize bazel cache pruning (#368)",
            author="Dev",
            date="2026-08-21",
            commit_type="perf",
            scope="ci",
            description="optimize bazel cache pruning",
            pr_number=368,
            files_changed=[".github/workflows/unit-tests.yml"],
        )
        active_states: dict[str, bool] = {}
        promoted_flags: set[str] = set()

        classify_commit(commit, active_states, promoted_flags)
        self.assertEqual(commit.category, "developer_velocity")

    def test_classify_commit_user_facing_fix(self) -> None:
        commit = CommitInfo(
            commit_hash="abc6",
            short_hash="abc6",
            subject="fix: self-closing app-root (#425)",
            author="Dev",
            date="2026-08-21",
            commit_type="fix",
            scope="",
            description="self-closing app-root",
            pr_number=425,
            files_changed=["ui/src/index.html"],
        )
        active_states: dict[str, bool] = {}
        promoted_flags: set[str] = set()

        classify_commit(commit, active_states, promoted_flags)
        self.assertEqual(commit.category, "user_facing_fixes")

    @patch("tools.release.extract_release_metadata.get_git_commits")
    @patch("tools.release.extract_release_metadata.get_current_feature_states")
    @patch("tools.release.extract_release_metadata.extract_flag_transitions")
    def test_extract_release_metadata_handler(
        self,
        mock_transitions: MagicMock,
        mock_states: MagicMock,
        mock_commits: MagicMock,
    ) -> None:
        mock_transitions.return_value = []
        mock_states.return_value = {"components": False, "rod": False}
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
                subject="feat(components): initial proto schemas (#470)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat",
                scope="components",
                description="initial proto schemas",
                pr_number=470,
                files_changed=["pkg/components/proto.go"],
            ),
        ]

        args = argparse.Namespace(
            base_ref="0.5.0",
            target_ref="0.6.0",
            repo_dir=".",
            approved_flags=["enable_components"],
            output=None,
        )

        res = extract_release_metadata(args)
        self.assertEqual(res["metadata"]["commit_count"], 2)
        self.assertEqual(res["metadata"]["pr_count"], 2)
        self.assertEqual(len(res["categorized_commits"]["user_facing_features"]), 1)
        self.assertEqual(len(res["categorized_commits"]["approved_preview_features"]), 1)
        self.assertEqual(len(res["categorized_commits"]["experimental_preview_features"]), 0)
        self.assertEqual(res["version_recommendation"]["recommended_version"], "0.6.0")
        self.assertEqual(res["version_recommendation"]["recommended_bump"], "minor")

    def test_parse_semver(self) -> None:
        self.assertEqual(parse_semver("0.5.0"), (0, 5, 0))
        self.assertEqual(parse_semver("v1.2.3"), (1, 2, 3))
        self.assertEqual(parse_semver("invalid"), (0, 0, 0))

    def test_calculate_version_recommendation_patch_bump(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="111",
                short_hash="111",
                subject="fix: crash on nil pointer (#100)",
                author="Dev",
                date="2026-08-21",
                commit_type="fix",
                scope="",
                description="crash on nil pointer",
                pr_number=100,
            )
        ]
        transitions: list[FlagTransition] = []
        categorized = {"user_facing_features": [], "user_facing_fixes": [{"commit": "111"}]}

        rec = calculate_version_recommendation("0.5.0", commits, transitions, categorized)
        self.assertEqual(rec.recommended_bump, "patch")
        self.assertEqual(rec.recommended_version, "0.5.1")
        self.assertFalse(rec.has_breaking_changes)
        self.assertFalse(rec.has_ootb_features)

    def test_calculate_version_recommendation_minor_feature_bump(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="222",
                short_hash="222",
                subject="feat: add new feature (#101)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat",
                scope="",
                description="add new feature",
                pr_number=101,
            )
        ]
        transitions: list[FlagTransition] = []
        categorized = {"user_facing_features": [{"commit": "222"}], "user_facing_fixes": []}

        rec = calculate_version_recommendation("0.5.0", commits, transitions, categorized)
        self.assertEqual(rec.recommended_bump, "minor")
        self.assertEqual(rec.recommended_version, "0.6.0")
        self.assertTrue(rec.has_ootb_features)

    def test_calculate_version_recommendation_major_breaking_bump(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="333",
                short_hash="333",
                subject="feat!: breaking change to auth API (#102)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat!",
                scope="",
                description="breaking change to auth API",
                pr_number=102,
            )
        ]
        transitions: list[FlagTransition] = []
        categorized = {"user_facing_features": [{"commit": "333"}], "user_facing_fixes": []}

        rec = calculate_version_recommendation("1.2.0", commits, transitions, categorized)
        self.assertEqual(rec.recommended_bump, "major")
        self.assertEqual(rec.recommended_version, "2.0.0")
        self.assertTrue(rec.has_breaking_changes)

    def test_calculate_version_recommendation_approved_flags(self) -> None:
        commits = [
            CommitInfo(
                commit_hash="444",
                short_hash="444",
                subject="feat(rod): implement secondary metrics API (#474)",
                author="Dev",
                date="2026-08-21",
                commit_type="feat",
                scope="rod",
                description="implement secondary metrics API",
                pr_number=474,
            )
        ]
        transitions: list[FlagTransition] = []
        categorized = {
            "user_facing_features": [],
            "user_facing_fixes": [],
            "approved_preview_features": [{"commit": "444"}],
        }

        rec = calculate_version_recommendation("0.6.0", commits, transitions, categorized)
        self.assertEqual(rec.recommended_bump, "minor")
        self.assertEqual(rec.recommended_version, "0.7.0")
        self.assertTrue(rec.has_approved_features)


if __name__ == "__main__":
    unittest.main()
