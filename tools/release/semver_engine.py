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

"""Semantic version calculation and release increment recommendation engine."""

import re

from tools.release.models import (
    CommitInfo,
    FlagTransition,
    VersionRecommendation,
)


def parse_semver(tag: str) -> tuple[int, int, int]:
    """Parses a semantic version string (e.g. 0.5.0 or v1.2.3) into (major, minor, patch)."""
    if not tag or not isinstance(tag, str):
        return 0, 0, 0
    clean = tag.strip().split("/")[-1].removeprefix("v")
    match = re.match(r"^(\d+)\.(\d+)\.(\d+)", clean)
    if not match:
        return 0, 0, 0
    return int(match.group(1)), int(match.group(2)), int(match.group(3))


def format_bumped_version(major: int, minor: int, patch: int, bump: str) -> str:
    """Calculates the target semantic version string given the bump level."""
    if bump == "major":
        return f"{major + 1}.0.0"
    if bump == "minor":
        return f"{major}.{minor + 1}.0"
    if bump == "patch":
        return f"{major}.{minor}.{patch + 1}"
    if bump == "none":
        return f"{major}.{minor}.{patch}"
    raise ValueError(f"Unknown bump level: {bump}")


def _has_breaking_changes(commits: list[CommitInfo]) -> bool:
    """Checks if any commits include breaking change indicators."""
    for c in commits:
        if not c:
            continue
        subject = c.subject or ""
        description = c.description or ""
        if (
            re.search(r"^[a-zA-Z0-9_-]+(?:\([^)]+\))?!:", subject)
            or re.search(r"^BREAKING[ -]CHANGE:", description, re.MULTILINE | re.IGNORECASE)
            or re.search(r"^BREAKING[ -]CHANGE:", subject, re.MULTILINE | re.IGNORECASE)
        ):
            return True
    return False


def _has_database_migrations(commits: list[CommitInfo]) -> bool:
    """Checks if any touched files contain database migrations or schema alterations."""
    migration_keywords = {"migrations", "migration", "migrate"}
    for c in commits:
        if not c or not c.files_changed:
            continue
        for f in c.files_changed:
            if not f:
                continue
            f_lower = f.lower()
            parts = f_lower.replace("\\", "/").split("/")
            if not migration_keywords.isdisjoint(parts) or f_lower.endswith((".sql", ".ddl")):
                return True
    return False


def _evaluate_bump_triggers(
    commits: list[CommitInfo],
    transitions: list[FlagTransition],
    approved_flags: set[str] | list[str] | None = None,
) -> tuple[dict[str, bool], list[str]]:
    """Evaluates triggers determining whether a release requires a major, minor, or patch bump."""
    commits = commits or []
    transitions = transitions or []
    approved_set = set(approved_flags or [])

    has_breaking = _has_breaking_changes(commits)
    has_promotions = any(t and t.is_promotion_to_default for t in transitions)
    has_approved = bool(approved_set)
    has_ootb_features = any(c and c.commit_type == "feat" for c in commits)
    has_ootb_fixes = any(c and c.commit_type == "fix" for c in commits)
    has_migrations = _has_database_migrations(commits)

    rationale: list[str] = []
    if has_breaking:
        rationale.append("Breaking changes detected in commit history.")
    if has_promotions:
        rationale.append("Feature flags promoted to default true.")
    if has_approved:
        rationale.append("Behind-the-flag features approved for deployment.")
    if has_ootb_features:
        rationale.append("New out-of-the-box user-facing features introduced.")
    if has_ootb_fixes:
        rationale.append("User-facing bug fixes resolved.")
    if has_migrations:
        rationale.append("Database schema migrations or DDL changes included.")

    triggers = {
        "has_breaking": has_breaking,
        "has_promotions": has_promotions,
        "has_approved": has_approved,
        "has_ootb_features": has_ootb_features,
        "has_ootb_fixes": has_ootb_fixes,
        "has_migrations": has_migrations,
    }
    return triggers, rationale


def calculate_version_recommendation(
    base_tag: str,
    commits: list[CommitInfo],
    transitions: list[FlagTransition],
    approved_flags: set[str] | list[str] | None = None,
) -> VersionRecommendation:
    """Computes the recommended semantic version increment based on release changes."""
    base_tag = base_tag or ""
    commits = commits or []
    transitions = transitions or []

    has_v = base_tag.strip().split("/")[-1].startswith("v")
    major, minor, patch = parse_semver(base_tag)
    triggers, rationale = _evaluate_bump_triggers(commits, transitions, approved_flags)

    if not commits and not transitions:
        bump = "none"
        rec_version = base_tag
        rationale = ["No changes detected between releases."]
    else:
        if triggers["has_breaking"]:
            bump = "major"
        elif (
            triggers["has_promotions"] or triggers["has_approved"] or triggers["has_ootb_features"]
        ):
            bump = "minor"
        elif triggers["has_ootb_fixes"]:
            bump = "patch"
        else:
            bump = "patch"
            if not rationale:
                rationale.append("Internal tooling, infrastructure, or maintenance updates.")
        rec_version = format_bumped_version(major, minor, patch, bump)
        if has_v:
            rec_version = f"v{rec_version}"
    return VersionRecommendation(
        current_version=base_tag,
        recommended_version=rec_version,
        recommended_bump=bump,
        has_breaking_changes=triggers["has_breaking"],
        has_promotions=triggers["has_promotions"],
        has_approved_features=triggers["has_approved"],
        has_ootb_features=triggers["has_ootb_features"],
        has_ootb_fixes=triggers["has_ootb_fixes"],
        has_migrations=triggers["has_migrations"],
        rationale=rationale,
    )
