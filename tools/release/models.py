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

"""Data models for release metadata, feature flags, and SemVer recommendations."""

from dataclasses import dataclass, field


@dataclass(kw_only=True)
class CommitInfo:
    """Represents metadata for a single git commit in the release range."""

    commit_hash: str
    short_hash: str
    subject: str
    author: str
    date: str
    commit_type: str = "other"
    scope: str = ""
    description: str = ""
    pr_number: int | None = None
    files_changed: list[str] = field(default_factory=list)
    category: str = "developer_velocity"
    gated_flag: str | None = None
    gated_subsystem: str | None = None
    is_promotion: bool = False


@dataclass(kw_only=True)
class FlagTransition:
    """Represents a feature flag transition between base and target refs."""

    flag_name: str
    source_file: str
    base_value: bool | str | None
    target_value: bool | str | None
    is_flip: bool
    is_promotion_to_default: bool
    description: str


@dataclass(kw_only=True)
class FeatureFlagInfo:
    """Represents a dynamically discovered feature flag in deployment configurations."""

    flag_name: str
    display_name: str
    tokens: set[str] = field(default_factory=set)
    ctk_value: bool | None = None
    helm_value: bool | None = None
    is_active: bool = False


@dataclass(kw_only=True)
class VersionRecommendation:
    """Represents the recommended semantic version increment and rationale."""

    current_base_version: str
    recommended_version: str
    recommended_bump: str  # 'major', 'minor', 'patch'
    has_breaking_changes: bool
    has_promotions: bool
    has_ootb_features: bool
    has_ootb_fixes: bool
    has_approved_features: bool = False
    has_migrations: bool = False
    rationale: str
    bump_triggers: list[str] = field(default_factory=list)
