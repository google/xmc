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

"""Data models for release metadata, commits, and feature flag transitions."""

from dataclasses import dataclass, field
from typing import TypedDict

STATUS_ENABLED_BY_DEFAULT = "enabled_by_default"
STATUS_APPROVED_PREVIEW_TOGGLE = "approved_preview_toggle"
STATUS_EXPERIMENTAL_PREVIEW_TOGGLE = "experimental_preview_toggle"


@dataclass
class CommitInfo:
    """Represents a single parsed git commit."""

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
    is_promotion: bool = False
    gated_flag: str | None = None


@dataclass
class FlagTransition:
    """Represents a configuration flag value transition between two releases."""

    flag_name: str
    source_file: str
    base_value: bool | None
    target_value: bool | None
    is_flip: bool = False
    is_promotion_to_default: bool = False
    description: str = ""


@dataclass
class FeatureFlagInfo:
    """Represents discovered metadata for a feature flag."""

    flag_name: str
    display_name: str
    tokens: set[str]
    ctk_value: bool | None = None
    helm_value: bool | None = None
    is_active: bool = False


class ReleaseMetadataSummary(TypedDict, total=False):
    """Summary of release extraction metadata."""

    base_ref: str
    target_ref: str
    commit_count: int
    pr_count: int
    approved_flags: list[str]


class CommitDict(TypedDict, total=False):
    """Serialized git commit dictionary."""

    commit_hash: str
    short_hash: str
    subject: str
    author: str
    date: str
    commit_type: str
    scope: str
    description: str
    pr_number: int | None
    files_changed: list[str]
    category: str
    is_promotion: bool
    gated_flag: str | None


class FlagTransitionDict(TypedDict):
    """Serialized flag transition dictionary."""

    flag_name: str
    source_file: str
    base_value: bool | None
    target_value: bool | None
    is_flip: bool
    is_promotion_to_default: bool
    description: str


class GatedSubsystemStatus(TypedDict):
    """Serialized gated subsystem status dictionary."""

    subsystem: str
    flag_name: str
    status: str
    ctk_value: bool | None
    helm_value: bool | None


@dataclass
class VersionRecommendation:
    """Calculated semantic version bump recommendation."""

    current_version: str
    recommended_version: str
    recommended_bump: str  # major, minor, patch, none
    has_breaking_changes: bool = False
    has_promotions: bool = False
    has_approved_features: bool = False
    has_ootb_features: bool = False
    has_ootb_fixes: bool = False
    has_migrations: bool = False
    rationale: list[str] = field(default_factory=list)


class VersionRecommendationDict(TypedDict):
    """Serialized semantic version recommendation dictionary."""

    current_version: str
    recommended_version: str
    recommended_bump: str
    has_breaking_changes: bool
    has_promotions: bool
    has_approved_features: bool
    has_ootb_features: bool
    has_ootb_fixes: bool
    has_migrations: bool
    rationale: list[str]


class ReleaseMetadataResult(TypedDict, total=False):
    """Structured release metadata output."""

    metadata: ReleaseMetadataSummary
    recommended_version: VersionRecommendationDict
    flag_transitions: list[FlagTransitionDict]
    gated_subsystems_status: list[GatedSubsystemStatus]
    raw_commits: list[CommitDict]


class PublishReleaseResult(TypedDict):
    """Structured output from publishing a GitHub release."""

    status: str
    tag: str
    title: str
    release_url: str
    release_id: int | None
    is_draft: bool
    notes_file: str


class FailedJobSummary(TypedDict, total=False):
    """Summarized failed job information."""

    name: str
    conclusion: str
    failed_steps: list[str]
    url: str


class DispatchWatchResult(TypedDict, total=False):
    """Structured output from workflow dispatch and watch operations."""

    run_id: int
    url: str
    head_sha: str
    event: str
    conclusion: str
    superseded_by: int | None
    failed_jobs: list[FailedJobSummary]
    page_url: str | None
    _exit_code: int
