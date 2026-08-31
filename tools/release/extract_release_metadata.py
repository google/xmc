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

"""Release metadata extractor CLI and conventional commit aggregator."""

import argparse
import json
import sys
from dataclasses import asdict
from pathlib import Path
from typing import cast

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.common.tool_utils import HandledError, run_tool
    from tools.release.commit_classifier import (
        classify_commit,
        parse_conventional_commit,
    )
    from tools.release.git_ops import get_git_commits, get_latest_tag_before
    from tools.release.models import CommitDict, CommitInfo, ReleaseMetadataResult
except ImportError:
    from commit_classifier import (  # type: ignore[no-redef]
        classify_commit,
        parse_conventional_commit,
    )
    from common.tool_utils import HandledError, run_tool  # type: ignore[no-redef]
    from git_ops import (  # type: ignore[no-redef]
        get_git_commits,
        get_latest_tag_before,
    )
    from models import (  # type: ignore[no-redef]
        CommitDict,
        CommitInfo,
        ReleaseMetadataResult,
    )

__all__ = [
    "CommitDict",
    "CommitInfo",
    "ReleaseMetadataResult",
    "classify_commit",
    "extract_release_metadata",
    "get_git_commits",
    "parse_conventional_commit",
]


def _group_commits_by_category(
    commits: list[CommitInfo],
) -> dict[str, list[dict[str, Any]]]:
    """Groups categorized commits into release sections."""
    categorized: dict[str, list[dict[str, Any]]] = {
        "user_facing_features": [],
        "user_facing_fixes": [],
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

    # 1. Extract and classify commits
    commits = get_git_commits(base_ref, target_ref, repo_dir)
    for commit in commits:
        classify_commit(commit)

    # 2. Group commits by category
    categorized = _group_commits_by_category(commits)

    result = {
        "metadata": {
            "base_ref": base_ref,
            "target_ref": target_ref,
            "commit_count": len(commits),
            "pr_count": len([c for c in commits if c.pr_number is not None]),
        },
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
        description="Extract release metadata and categorized commits for XMC."
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
