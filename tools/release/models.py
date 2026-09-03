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

"""Data models for git commits and release metadata extraction."""

from dataclasses import dataclass, field
from typing import TypedDict


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


class ReleaseMetadataSummary(TypedDict):
    """Summary of release extraction metadata."""

    base_ref: str
    target_ref: str
    commit_count: int
    pr_count: int


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


class CategorizedCommits(TypedDict):
    """Audience-categorized commits dictionary."""

    user_facing_features: list[CommitDict]
    user_facing_fixes: list[CommitDict]
    operational_and_infrastructure: list[CommitDict]
    developer_velocity: list[CommitDict]


class ReleaseMetadataResult(TypedDict):
    """Structured release metadata output."""

    metadata: ReleaseMetadataSummary
    categorized_commits: CategorizedCommits
    raw_commits: list[CommitDict]
