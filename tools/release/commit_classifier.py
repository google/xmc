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

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

try:
    from tools.release.models import CommitInfo
except ImportError:
    from models import CommitInfo  # type: ignore[no-redef]

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


def _is_dev_file(f: str) -> bool:
    """Checks if a single file pertains to developer velocity / internal tooling."""
    dev_paths = (
        ".github/",
        ".agents/",
        ".githooks/",
        "tools/",
        "scripts/",
        "docs/",
        "test/",
    )
    path = Path(f)
    return (
        f.startswith(dev_paths)
        or path.name
        in {
            "BUILD.bazel",
            "WORKSPACE",
            "MODULE.bazel",
            "go.mod",
            "go.sum",
            "pyproject.toml",
        }
        or path.suffix == ".bzl"
        or f.endswith(("_test.go", "_test.py", ".spec.ts", "_test.ts"))
    )


def _is_infra_file(f: str) -> bool:
    """Checks if a single file pertains to deployment, auth, security, or storage infrastructure."""
    infra_paths = (
        "deploy/",
        "pkg/auth/",
        "pkg/spanner/",
        "pkg/storage/",
        "config/",
    )
    return f.startswith(infra_paths)


def _is_dev_velocity_files(files: list[str]) -> bool:
    """Checks if all modified files strictly pertain to developer velocity / internal tooling."""
    if not files:
        return False
    return all(_is_dev_file(f) for f in files)


def _is_infra_or_auth_files(files: list[str]) -> bool:
    """Checks if touched files pertain to deployment, auth, security, or storage infrastructure."""
    if not files:
        return False
    has_infra = False
    for f in files:
        if _is_infra_file(f):
            has_infra = True
        elif not _is_dev_file(f):
            return False
    return has_infra


def classify_commit(commit: CommitInfo) -> None:
    """Classifies a commit into an audience-segmented release category."""
    c_type, scope, desc, pr = parse_conventional_commit(commit.subject)
    commit.commit_type = c_type
    commit.scope = scope
    commit.description = desc
    if pr is not None and commit.pr_number is None:
        commit.pr_number = pr

    if c_type in _DEV_VELOCITY_TYPES or scope in _DEV_VELOCITY_SCOPES:
        commit.category = "developer_velocity"
        return

    if _is_dev_velocity_files(commit.files_changed):
        commit.category = "developer_velocity"
        return

    if scope in _INFRA_SCOPES or _is_infra_or_auth_files(commit.files_changed):
        commit.category = "operational_and_infrastructure"
        return

    if c_type == "feat":
        commit.category = "user_facing_features"
        return

    if c_type == "fix":
        commit.category = "user_facing_fixes"
        return

    commit.category = "developer_velocity"
