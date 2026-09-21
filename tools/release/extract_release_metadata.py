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

"""Release metadata extractor CLI and git commit aggregator."""

import argparse
import json
from dataclasses import asdict
from pathlib import Path
from typing import cast

from tools.common.tool_utils import HandledError, run_tool
from tools.release.feature_flags import (
    discover_feature_flags,
    extract_flag_transitions,
)
from tools.release.git_ops import get_git_commits, get_latest_tag_before
from tools.release.models import (
    STATUS_APPROVED_PREVIEW_TOGGLE,
    STATUS_ENABLED_BY_DEFAULT,
    STATUS_EXPERIMENTAL_PREVIEW_TOGGLE,
    CommitDict,
    CommitInfo,
    FeatureFlagInfo,
    FlagTransition,
    FlagTransitionDict,
    GatedSubsystemStatus,
    ReleaseMetadataResult,
    VersionRecommendationDict,
)
from tools.release.semver_engine import calculate_version_recommendation

__all__ = [
    "STATUS_APPROVED_PREVIEW_TOGGLE",
    "STATUS_ENABLED_BY_DEFAULT",
    "STATUS_EXPERIMENTAL_PREVIEW_TOGGLE",
    "CommitDict",
    "CommitInfo",
    "FeatureFlagInfo",
    "FlagTransition",
    "FlagTransitionDict",
    "GatedSubsystemStatus",
    "ReleaseMetadataResult",
    "VersionRecommendationDict",
    "calculate_version_recommendation",
    "discover_feature_flags",
    "extract_flag_transitions",
    "extract_release_metadata",
    "get_git_commits",
    "get_latest_tag_before",
]


def _build_gated_status_matrix(
    discovered_flags: dict[str, FeatureFlagInfo],
    approved_flags: set[str],
) -> list[GatedSubsystemStatus]:
    """Builds an aggregated status matrix for all discovered feature toggles."""
    matrix: list[GatedSubsystemStatus] = []
    seen_displays: set[str] = set()

    for flag_name, info in sorted(discovered_flags.items()):
        if info.display_name in seen_displays:
            continue
        seen_displays.add(info.display_name)

        if info.is_active:
            status = STATUS_ENABLED_BY_DEFAULT
        elif info.flag_name in approved_flags or flag_name in approved_flags:
            status = STATUS_APPROVED_PREVIEW_TOGGLE
        else:
            status = STATUS_EXPERIMENTAL_PREVIEW_TOGGLE

        matrix.append(
            {
                "subsystem": info.display_name,
                "flag_name": info.flag_name,
                "status": status,
                "ctk_value": info.ctk_value,
                "helm_value": info.helm_value,
            }
        )

    return matrix


def extract_release_metadata(args: argparse.Namespace) -> ReleaseMetadataResult:
    """Primary execution handler orchestrating release metadata extraction."""
    repo_dir = Path(args.repo_dir).resolve()
    target_ref = args.target_ref

    base_ref = args.base_ref
    if not base_ref:
        base_ref = get_latest_tag_before(target_ref, repo_dir)
        if not base_ref:
            raise HandledError(
                f"Could not automatically determine previous tag for '{target_ref}'.",
                "Explicitly provide --base-ref (e.g. --base-ref 0.5.0).",
            )

    approved_flags = set(getattr(args, "approved_flags", None) or [])

    # 1. Extract flag transitions
    transitions = extract_flag_transitions(base_ref, target_ref, repo_dir)

    # 2. Discover flags & active states at target ref
    discovered_flags = discover_feature_flags(target_ref, repo_dir)
    gated_matrix = _build_gated_status_matrix(discovered_flags, approved_flags)

    # 3. Extract git commits
    commits = get_git_commits(base_ref, target_ref, repo_dir)
    raw_commits: list[CommitDict] = [cast(CommitDict, asdict(c)) for c in commits]

    # 4. Calculate recommended semantic version increment
    rec = calculate_version_recommendation(base_ref, commits, transitions, approved_flags)

    result: ReleaseMetadataResult = {
        "metadata": {
            "base_ref": base_ref,
            "target_ref": target_ref,
            "commit_count": len(commits),
            "pr_count": len([c for c in commits if c.pr_number is not None]),
            "approved_flags": sorted(approved_flags),
        },
        "recommended_version": cast(VersionRecommendationDict, asdict(rec)),
        "flag_transitions": [cast(FlagTransitionDict, asdict(t)) for t in transitions],
        "gated_subsystems_status": gated_matrix,
        "raw_commits": raw_commits,
    }

    if args.output:
        out_path = Path(args.output).resolve()
        out_path.parent.mkdir(parents=True, exist_ok=True)
        with open(out_path, "w", encoding="utf-8") as f:
            json.dump(result, f, indent=2)

    return result


def create_parser() -> argparse.ArgumentParser:
    """Creates CLI arguments parser."""
    parser = argparse.ArgumentParser(
        description="Extract release metadata and git commit history for XMC."
    )
    parser.add_argument(
        "--target-ref",
        default="HEAD",
        help="Target git commit/tag/branch (default: HEAD).",
    )
    parser.add_argument(
        "--base-ref",
        default="",
        help="Base git commit/tag to diff against (default: auto-detected previous tag).",
    )
    parser.add_argument(
        "--repo-dir",
        default=".",
        help="Root path to git repository (default: .).",
    )
    parser.add_argument(
        "--approved-flags",
        nargs="*",
        default=[],
        help="Feature flags approved for production deployment in this release.",
    )
    parser.add_argument(
        "--output",
        "-o",
        help="Path to write output JSON file (default: stdout).",
    )
    return parser


def main() -> None:
    """Entry point executing the metadata extraction CLI tool."""
    parser = create_parser()
    run_tool(parser, extract_release_metadata)


if __name__ == "__main__":
    main()
