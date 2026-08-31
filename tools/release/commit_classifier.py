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

"""Conventional commit parsing and audience-segmented classification."""

import re
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.feature_flags import (
        derive_display_name,
        derive_flag_tokens,
        normalize_flag_name,
    )
    from tools.release.models import CommitInfo, FeatureFlagInfo
except ImportError:
    from feature_flags import (  # type: ignore[no-redef]
        derive_display_name,
        derive_flag_tokens,
        normalize_flag_name,
    )
    from models import CommitInfo, FeatureFlagInfo  # type: ignore[no-redef]

_DEV_VELOCITY_TYPES = {"chore", "ci", "perf", "test", "docs", "refactor"}
_DEV_VELOCITY_SCOPES = {
    "lint",
    "deps",
    "ci",
    "bazel",
    "rules",
    "absence",
    "test",
    "linting",
    "benchmark",
    "e2e",
}
_INFRA_SCOPES = {
    "auth",
    "security",
    "infra",
    "infrastructure",
    "helm",
    "ctk",
    "terraform",
    "cloud",
}


def parse_conventional_commit(
    subject: str,
) -> tuple[str, str, str, int | None]:
    """Parses conventional commit headers: type(scope): description (#123)."""
    pr_number: int | None = None
    pr_match = re.search(r"\(#(\d+)\)\s*$", subject)
    if pr_match:
        pr_number = int(pr_match.group(1))
        clean_subject = subject[: pr_match.start()].strip()
    else:
        clean_subject = subject.strip()

    conv_match = re.match(r"^([a-zA-Z]+)(?:\(([^)]+)\))?!?:\s*(.*)$", clean_subject)
    if conv_match:
        commit_type = conv_match.group(1).lower()
        scope = conv_match.group(2).lower() if conv_match.group(2) else ""
        desc = conv_match.group(3).strip()
        return commit_type, scope, desc, pr_number

    return "other", "", clean_subject, pr_number


def _build_flag_info_list(active_states: dict[str, Any]) -> list[FeatureFlagInfo]:
    """Constructs a list of FeatureFlagInfo objects from active state inputs."""
    flag_infos: list[FeatureFlagInfo] = []
    seen: set[str] = set()

    for k, v in active_states.items():
        if isinstance(v, FeatureFlagInfo):
            if v.flag_name not in seen:
                flag_infos.append(v)
                seen.add(v.flag_name)
        else:
            if k not in seen:
                flag_infos.append(
                    FeatureFlagInfo(
                        flag_name=k,
                        display_name=derive_display_name(k),
                        tokens=derive_flag_tokens(k),
                        is_active=bool(v),
                    )
                )
                seen.add(k)
    return flag_infos


def _match_feature_flag(
    commit: CommitInfo, flag_infos: list[FeatureFlagInfo]
) -> FeatureFlagInfo | None:
    """Finds the first feature flag matching a commit's scope or file paths."""
    for info in flag_infos:
        if commit.scope and any(commit.scope == tok or tok in commit.scope for tok in info.tokens):
            return info
        for f in commit.files_changed:
            parts = Path(f).parts
            if any(tok in parts or any(tok in part for part in parts) for tok in info.tokens):
                return info
    return None


def _is_dev_velocity_files(files_changed: list[str]) -> bool:
    """Checks if modified files are exclusively developer tooling or tests."""
    if not files_changed:
        return False
    return all(
        f.startswith(
            (
                ".github/",
                ".agents/",
                "tools/",
                "scripts/",
                "docs/",
            )
        )
        or f.endswith(("_test.go", "_test.py"))
        or f in ("BUILD.bazel", "MODULE.bazel", "WORKSPACE", "ABSENCE.md")
        for f in files_changed
    )


def _is_infra_or_auth_files(files_changed: list[str]) -> bool:
    """Checks if modified files touch infrastructure, auth, or storage."""
    return any(
        f.startswith(
            (
                "deploy/",
                "pkg/auth/",
                "pkg/spanner/",
                "pkg/storage/",
                "config/",
            )
        )
        for f in files_changed
    )


def classify_commit(
    commit: CommitInfo,
    active_states: dict[str, Any],
    promoted_flags: set[str],
    approved_flags: set[str] | None = None,
) -> None:
    """Classifies a commit into release categories based on files, type, and active flags."""
    # 1. Flag promotions
    for flag in promoted_flags:
        if flag in commit.subject or flag in commit.description:
            commit.category = "highlights_and_promotions"
            commit.is_promotion = True
            return

    # 2. Developer velocity types & scopes
    if commit.commit_type in _DEV_VELOCITY_TYPES or commit.scope in _DEV_VELOCITY_SCOPES:
        commit.category = "developer_velocity"
        return

    # 3. Gated feature flags
    flag_infos = _build_flag_info_list(active_states)
    matched_flag = _match_feature_flag(commit, flag_infos)
    if matched_flag:
        commit.gated_subsystem = normalize_flag_name(matched_flag.flag_name)
        commit.gated_flag = matched_flag.flag_name
        if matched_flag.is_active:
            commit.category = (
                "user_facing_fixes" if commit.commit_type == "fix" else "user_facing_features"
            )
        else:
            approved_set = approved_flags or set()
            normalized_approved = {normalize_flag_name(a) for a in approved_set}
            is_approved = (
                matched_flag.flag_name in approved_set
                or normalize_flag_name(matched_flag.flag_name) in normalized_approved
                or any(
                    tok in approved_set or tok in normalized_approved for tok in matched_flag.tokens
                )
            )
            commit.category = (
                "approved_preview_features" if is_approved else "experimental_preview_features"
            )
        return

    # 4. Pure tooling or documentation files
    if _is_dev_velocity_files(commit.files_changed):
        commit.category = "developer_velocity"
        return

    # 5. Infrastructure, deployment, and auth files
    if commit.scope in _INFRA_SCOPES or _is_infra_or_auth_files(commit.files_changed):
        commit.category = "operational_and_infrastructure"
        return

    # 6. Core fixes and features
    if commit.commit_type == "fix":
        commit.category = "user_facing_fixes"
        return
    if commit.commit_type in ("feat", "other"):
        commit.category = "user_facing_features"
        return

    commit.category = "developer_velocity"
