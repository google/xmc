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

"""CLI Orchestrator for extracting release metadata, commits, and SemVer recommendations."""

import argparse
import json
import sys
from dataclasses import asdict
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.common.tool_utils import HandledError, run_tool
    from tools.release.commit_classifier import (
        classify_commit,
        parse_conventional_commit,
    )
    from tools.release.feature_flags import (
        derive_display_name,
        derive_flag_tokens,
        discover_feature_flags,
        extract_flag_transitions,
        get_current_feature_states,
        normalize_flag_name,
    )
    from tools.release.git_ops import (
        get_git_commits,
        get_git_file_content,
        get_latest_tag_before,
    )
    from tools.release.models import (
        CommitInfo,
        FeatureFlagInfo,
        FlagTransition,
        VersionRecommendation,
    )
    from tools.release.semver_engine import (
        calculate_version_recommendation,
        parse_semver,
    )
    from tools.release.yaml_parser import parse_yaml_booleans
except ImportError:
    from commit_classifier import (  # type: ignore[no-redef]
        classify_commit,
        parse_conventional_commit,
    )
    from common.tool_utils import HandledError, run_tool  # type: ignore[no-redef]
    from feature_flags import (  # type: ignore[no-redef]
        derive_display_name,
        derive_flag_tokens,
        discover_feature_flags,
        extract_flag_transitions,
        get_current_feature_states,
        normalize_flag_name,
    )
    from git_ops import (  # type: ignore[no-redef]
        get_git_commits,
        get_git_file_content,
        get_latest_tag_before,
    )
    from models import (  # type: ignore[no-redef]
        CommitInfo,
        FeatureFlagInfo,
        FlagTransition,
        VersionRecommendation,
    )
    from semver_engine import (  # type: ignore[no-redef]
        calculate_version_recommendation,
        parse_semver,
    )
    from yaml_parser import parse_yaml_booleans  # type: ignore[no-redef]

__all__ = [
    "CommitInfo",
    "FeatureFlagInfo",
    "FlagTransition",
    "VersionRecommendation",
    "calculate_version_recommendation",
    "classify_commit",
    "derive_display_name",
    "derive_flag_tokens",
    "discover_feature_flags",
    "extract_flag_transitions",
    "extract_release_metadata",
    "get_current_feature_states",
    "get_git_commits",
    "get_git_file_content",
    "get_latest_tag_before",
    "normalize_flag_name",
    "parse_conventional_commit",
    "parse_semver",
    "parse_yaml_booleans",
]


def _build_gated_status_matrix(
    discovered_flags: dict[str, FeatureFlagInfo],
    approved_flags: set[str],
) -> list[dict[str, Any]]:
    """Constructs status matrix entries for all discovered feature flags."""
    matrix: list[dict[str, Any]] = []
    seen: set[str] = set()
    normalized_approved = {normalize_flag_name(a) for a in approved_flags}

    for flag_info in discovered_flags.values():
        if flag_info.flag_name in seen:
            continue
        seen.add(flag_info.flag_name)

        is_active = flag_info.is_active
        is_approved = (
            flag_info.flag_name in approved_flags
            or normalize_flag_name(flag_info.flag_name) in normalized_approved
            or any(tok in approved_flags or tok in normalized_approved for tok in flag_info.tokens)
        )
        if is_active:
            status = "Active (Out-of-the-Box)"
        elif is_approved:
            status = "Production-Ready / Approved (Behind Flag)"
        else:
            status = "Experimental / In Development (Behind Flag)"

        matrix.append(
            {
                "subsystem": normalize_flag_name(flag_info.flag_name),
                "display_name": flag_info.display_name,
                "ctk_flag": flag_info.flag_name,
                "helm_flag": flag_info.flag_name,
                "is_active_ootb": is_active,
                "is_approved": is_approved,
                "status": status,
            }
        )
    return matrix


def _group_commits_by_category(
    commits: list[CommitInfo],
) -> dict[str, list[dict[str, Any]]]:
    """Groups categorized commits into release sections."""
    categorized: dict[str, list[dict[str, Any]]] = {
        "highlights_and_promotions": [],
        "user_facing_features": [],
        "user_facing_fixes": [],
        "approved_preview_features": [],
        "experimental_preview_features": [],
        "operational_and_infrastructure": [],
        "developer_velocity": [],
    }
    for c in commits:
        cat_key = c.category if c.category in categorized else "developer_velocity"
        categorized[cat_key].append(asdict(c))
    return categorized


def extract_release_metadata(args: argparse.Namespace) -> dict[str, Any]:
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
    promoted_flags = {t.flag_name for t in transitions if t.is_promotion_to_default}

    # 2. Discover flags & active states at target ref
    discovered_flags = discover_feature_flags(target_ref, repo_dir)

    # 3. Extract and classify commits
    commits = get_git_commits(base_ref, target_ref, repo_dir)
    for commit in commits:
        classify_commit(commit, discovered_flags, promoted_flags, approved_flags)

    # 4. Group commits and construct status matrix
    categorized = _group_commits_by_category(commits)
    gated_matrix = _build_gated_status_matrix(discovered_flags, approved_flags)

    # 5. Calculate SemVer recommendation
    version_rec = calculate_version_recommendation(base_ref, commits, transitions, categorized)

    result = {
        "metadata": {
            "base_ref": base_ref,
            "target_ref": target_ref,
            "commit_count": len(commits),
            "pr_count": len([c for c in commits if c.pr_number is not None]),
            "approved_flags": sorted(approved_flags),
        },
        "version_recommendation": asdict(version_rec),
        "flag_transitions": [asdict(t) for t in transitions],
        "gated_subsystems_status": gated_matrix,
        "categorized_commits": categorized,
        "raw_commits": [asdict(c) for c in commits],
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
        description="Extract release metadata, flag transitions, and categorized commits for XMC."
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


def main(argv: list[str] | None = None) -> None:
    """Entry point executing the metadata extraction CLI tool."""
    parser = create_parser()
    run_tool(parser, extract_release_metadata)


if __name__ == "__main__":
    main()
