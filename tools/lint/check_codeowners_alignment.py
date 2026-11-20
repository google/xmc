#!/usr/bin/env python3
# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Verifies that the repository CODEOWNERS matches the OWNERS reviewer pool."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402
from tools.components.check_profile_lock import (  # noqa: E402
    LOCKED_PROFILES_REL_PATH,
    parse_locked_profiles_yaml,
)

STATIC_PROTECTED_PATHS: tuple[str, ...] = (
    "/CODEOWNERS",
    "/OWNERS",
    "/.githooks/pre-commit",
    "/.github/workflows/lint.yml",
    "/deploy/profiles/locked_profiles.yaml",
    "/deploy/profiles/BUILD.bazel",
    "/tools/components/check_profile_lock.py",
    "/tools/components/BUILD.bazel",
    "/tools/lint/check_codeowners_alignment.py",
    "/tools/lint/BUILD.bazel",
    "/test/helm/profiles/profilecontract/",
)


def _default_repo_root() -> Path:
    """Resolves the repo root from Bazel runfiles first, then the symlink target."""
    for base in (
        Path(__file__).absolute().parents[2],
        Path(__file__).resolve().parents[2],
    ):
        if (base / "OWNERS").is_file() and (base / "CODEOWNERS").is_file():
            return base
    return Path(__file__).resolve().parents[2]


def _read_active_lines(path: Path) -> list[str]:
    """Reads non-empty, comment-stripped lines with normalized whitespace."""
    return [
        cleaned
        for raw in path.read_text(encoding="utf-8").splitlines()
        if (cleaned := " ".join(raw.split("#", 1)[0].split()))
    ]


def _resolve_required_paths(repo_root: Path) -> list[str]:
    """Builds required CODEOWNERS paths from static surfaces and locked_profiles.yaml."""
    required = list(STATIC_PROTECTED_PATHS)
    lock_file = repo_root / LOCKED_PROFILES_REL_PATH
    if not lock_file.is_file():
        raise HandledError(
            f"Missing locked profiles registry: {lock_file}.",
            f"Ensure {LOCKED_PROFILES_REL_PATH} exists under {repo_root}.",
            details={"missing_files": [str(lock_file)]},
        )
    locked_profiles = parse_locked_profiles_yaml(lock_file.read_text(encoding="utf-8"))
    for name in sorted(locked_profiles):
        required.append(f"/deploy/profiles/{name}/")
        test_pkg_name = name.replace("-", "")
        test_dir = repo_root / "test/helm/profiles" / test_pkg_name
        profile_dir = repo_root / "deploy/profiles" / name
        if test_dir.is_dir() or not profile_dir.is_dir():
            required.append(f"/test/helm/profiles/{test_pkg_name}/")
    return required


def _parse_codeowners_rules(lines: list[str]) -> dict[str, str]:
    """Parses normalized CODEOWNERS lines into a path -> owners string mapping."""
    rules: dict[str, str] = {}
    for line in lines:
        parts = line.split(" ", 1)
        if len(parts) != 2 or not parts[1].strip():
            raise HandledError(
                f"Malformed CODEOWNERS rule: '{line}'.",
                "Format each rule as '/<path> @owner1 @owner2 ...'.",
            )
        pattern, owners_str = parts[0], parts[1].strip()
        if pattern in ("*", "/*", "/"):
            raise HandledError(
                f"Repo-wide wildcard rule '{pattern}' is not allowed in CODEOWNERS.",
                "Scope CODEOWNERS rules to explicit governance and locked-profile paths so auto-assign-reviewers.yml can filter ABSENCE.md.",
            )
        if pattern in rules:
            raise HandledError(
                f"Duplicate path pattern '{pattern}' in CODEOWNERS.",
                f"Define '{pattern}' at most once in CODEOWNERS.",
            )
        rules[pattern] = owners_str
    return rules


def _verify_owned_paths_exist(
    repo_root: Path,
    rules: dict[str, str],
    required_set: set[str],
) -> None:
    """Verifies owned paths exist on disk when running against a full checkout or custom rules."""
    enforce_all = (repo_root / ".git").exists()
    missing_paths: list[str] = []
    for pattern in rules:
        if not enforce_all and pattern in required_set:
            continue
        rel = pattern.lstrip("/")
        if not rel or not (repo_root / rel).exists():
            missing_paths.append(pattern)
    if missing_paths:
        raise HandledError(
            f"CODEOWNERS references nonexistent repository path(s): {', '.join(missing_paths)}.",
            "Remove or update stale paths in CODEOWNERS so every owned path exists on disk.",
            details={"missing_owned_paths": missing_paths},
        )


def check_codeowners_alignment(args: argparse.Namespace) -> dict[str, Any]:
    """Validate that CODEOWNERS rules cover required paths and match the OWNERS pool."""
    repo_root = Path(args.repo_root).absolute()
    owners_file = repo_root / "OWNERS"
    codeowners_file = repo_root / "CODEOWNERS"

    missing = [str(p) for p in (owners_file, codeowners_file) if not p.is_file()]
    if missing:
        raise HandledError(
            f"Missing required ownership file(s): {', '.join(missing)}.",
            "Ensure both OWNERS and CODEOWNERS exist at the repository root.",
            details={"missing_files": missing},
        )

    owners = _read_active_lines(owners_file)
    if not owners:
        raise HandledError(
            "OWNERS contains no active reviewer handles.",
            "Populate OWNERS with at least one GitHub username.",
        )
    expected_owners = " ".join(f"@{u.lstrip('@')}" for u in owners)
    required_paths = _resolve_required_paths(repo_root)
    rules = _parse_codeowners_rules(_read_active_lines(codeowners_file))

    missing_rules = [p for p in required_paths if p not in rules]
    mismatched_rules = {p: actual for p, actual in rules.items() if actual != expected_owners}
    if missing_rules or mismatched_rules:
        raise HandledError(
            "CODEOWNERS does not align with required governance paths or the OWNERS reviewer pool.",
            "Update CODEOWNERS so every required path is present and lists the exact @<user> pool from OWNERS.",
            details={
                "expected_owners": expected_owners,
                "missing_required_paths": missing_rules,
                "mismatched_owners": mismatched_rules,
            },
        )

    _verify_owned_paths_exist(repo_root, rules, set(required_paths))
    return {
        "owners_count": len(owners),
        "required_paths_count": len(required_paths),
        "configured_rules_count": len(rules),
    }


def build_parser() -> argparse.ArgumentParser:
    """Builds the CLI argument parser."""
    parser = argparse.ArgumentParser(
        description=(__doc__ or "").splitlines()[0],
    )
    parser.add_argument(
        "--repo-root",
        default=str(_default_repo_root()),
        help="Repository root directory containing OWNERS and CODEOWNERS.",
    )
    return parser


def main() -> None:
    """CLI entrypoint."""
    run_tool(build_parser(), check_codeowners_alignment)


if __name__ == "__main__":
    main()
