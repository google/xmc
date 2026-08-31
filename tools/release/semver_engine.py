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

"""Semantic Versioning (SemVer) calculation engine and recommendation logic."""

import re
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.models import (
        CommitInfo,
        FlagTransition,
        VersionRecommendation,
    )
except ImportError:
    from models import (  # type: ignore[no-redef]
        CommitInfo,
        FlagTransition,
        VersionRecommendation,
    )


def parse_semver(version_str: str) -> tuple[int, int, int]:
    """Parses 'vX.Y.Z' or 'X.Y.Z' into (major, minor, patch) integers."""
    clean = version_str.lstrip("v").strip()
    match = re.match(r"^(\d+)\.(\d+)\.(\d+)", clean)
    if match:
        return int(match.group(1)), int(match.group(2)), int(match.group(3))
    return 0, 0, 0


def format_bumped_version(major: int, minor: int, patch: int, bump: str) -> str:
    """Computes the target version string from base numbers and bump increment."""
    if bump == "major":
        return f"{major + 1}.0.0"
    if bump == "minor":
        return f"{major}.{minor + 1}.0"
    return f"{major}.{minor}.{patch + 1}"


def _has_breaking_changes(commits: list[CommitInfo]) -> bool:
    """Checks if any commits introduce breaking changes."""
    for c in commits:
        if "BREAKING CHANGE" in c.subject or "BREAKING-CHANGE" in c.subject:
            return True
        if re.search(r"^[a-zA-Z]+(?:\([^)]+\))?!:", c.subject):
            return True
    return False


def _has_database_migrations(commits: list[CommitInfo]) -> bool:
    """Checks if any commits contain database migrations."""
    for c in commits:
        if any(
            "migrations/" in f or "pkg/spanner/schema" in f or "deploy/clickhouse/" in f
            for f in c.files_changed
        ):
            return True
    return False


def _evaluate_bump_triggers(
    has_breaking: bool,
    has_promotions: bool,
    has_ootb_features: bool,
    has_approved_features: bool,
    has_ootb_fixes: bool,
) -> tuple[str, list[str], str]:
    """Determines bump type, triggers, and rationale based on release signals."""
    triggers: list[str] = []
    if has_breaking:
        bump = "major"
        triggers.append("Breaking API/schema changes detected")
        rationale = "Major bump recommended due to breaking changes."
    elif has_promotions or has_ootb_features or has_approved_features:
        bump = "minor"
        if has_promotions:
            triggers.append("Feature flags promoted to default true out-of-the-box")
        if has_approved_features:
            triggers.append("Approved preview features ready for enablement")
        if has_ootb_features:
            triggers.append("New public user-facing features merged")
        rationale = "Minor bump recommended due to new features / promoted flags."
    elif has_ootb_fixes:
        bump = "patch"
        triggers.append("User-facing bug fixes merged")
        rationale = "Patch bump recommended due to bug fixes."
    else:
        bump = "patch"
        triggers.append("Developer velocity and maintenance changes only")
        rationale = "Patch bump recommended for internal maintenance and tooling."

    return bump, triggers, rationale


def calculate_version_recommendation(
    base_ref: str,
    commits: list[CommitInfo],
    transitions: list[FlagTransition],
    categorized_commits: dict[str, list[dict[str, Any]]],
) -> VersionRecommendation:
    """Calculates semantic version recommendation based on commit taxonomy."""
    has_breaking = _has_breaking_changes(commits)
    has_promotions = any(t.is_promotion_to_default for t in transitions)
    has_ootb_features = bool(categorized_commits.get("user_facing_features"))
    has_ootb_fixes = bool(categorized_commits.get("user_facing_fixes"))
    has_approved_features = bool(categorized_commits.get("approved_preview_features"))
    has_migrations = _has_database_migrations(commits)

    bump, triggers, rationale = _evaluate_bump_triggers(
        has_breaking,
        has_promotions,
        has_ootb_features,
        has_approved_features,
        has_ootb_fixes,
    )

    major, minor, patch = parse_semver(base_ref)
    target_version = format_bumped_version(major, minor, patch, bump)

    return VersionRecommendation(
        current_base_version=base_ref,
        recommended_version=target_version,
        recommended_bump=bump,
        has_breaking_changes=has_breaking,
        has_promotions=has_promotions,
        has_ootb_features=has_ootb_features,
        has_ootb_fixes=has_ootb_fixes,
        has_approved_features=has_approved_features,
        has_migrations=has_migrations,
        rationale=rationale,
        bump_triggers=triggers,
    )
