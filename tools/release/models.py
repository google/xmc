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
