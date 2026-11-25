#!/usr/bin/env python3
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

"""Lint rejecting out-of-order ESS migrations (version <= max on base ref)."""

from __future__ import annotations

import argparse
import re
import sys
from collections.abc import Sequence
from pathlib import Path, PurePosixPath
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parents[2]
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_command, run_tool  # noqa: E402

DEFAULT_MIGRATION_DIRS: tuple[str, ...] = (
    "internal/storage/spanner/migrations/ess",
    "internal/storage/postgres/migrations/ess",
)

_MIGRATION_FILE_RE = re.compile(r"^(\d+)_.*\.sql$")
_MIGRATION_DIRECTION_RE = re.compile(r"\.(?:up|down)\.sql$")


def parse_migration_version(path: str) -> int | None:
    """Parse the numeric version prefix from a migration SQL filename.

    Args:
        path: Repository-relative POSIX path or bare filename.

    Returns:
        Integer migration version if the filename matches ``<digits>_*.sql``,
        otherwise ``None``.
    """
    match = _MIGRATION_FILE_RE.match(PurePosixPath(path).name)
    if match is None:
        return None
    return int(match.group(1))


def _group_added_version_names(added_paths: Sequence[str]) -> dict[int, set[str]]:
    """Group added migration base names (without `.up.sql`/`.down.sql`) by version."""
    by_version: dict[int, set[str]] = {}
    for path in added_paths:
        version = parse_migration_version(path)
        if version is None:
            continue
        base_name = _MIGRATION_DIRECTION_RE.sub("", PurePosixPath(path).name)
        by_version.setdefault(version, set()).add(base_name)
    return by_version


def _build_violation(
    migration_dir: str,
    path: str,
    version: int,
    base_max: int,
    kind: str,
    reason: str,
) -> dict[str, Any]:
    """Construct a structured migration-ordering violation entry."""
    return {
        "dir": migration_dir,
        "path": path,
        "version": version,
        "base_max": base_max,
        "kind": kind,
        "reason": reason,
    }


def _check_added_path(
    migration_dir: str,
    path: str,
    base_max: int,
    has_base_versions: bool,
    added_by_version: dict[int, set[str]],
) -> dict[str, Any] | None:
    """Return a violation dict if an added path conflicts or is out of order."""
    version = parse_migration_version(path)
    if version is None:
        return None
    bases = added_by_version.get(version, set())
    if len(bases) > 1:
        return _build_violation(
            migration_dir,
            path,
            version,
            base_max,
            "duplicate_version",
            f"Duplicate migration version {version} detected with different names: "
            f"{', '.join(sorted(bases))}.",
        )
    if has_base_versions and version <= base_max:
        return _build_violation(
            migration_dir,
            path,
            version,
            base_max,
            "out_of_order",
            f"Added migration version {version} must be strictly greater than "
            f"max version {base_max} on base ref.",
        )
    return None


def find_out_of_order_migrations(
    migration_dir: str,
    base_paths: Sequence[str],
    current_paths: Sequence[str],
) -> tuple[int, list[dict[str, Any]]]:
    """Identify added migrations that conflict with or precede the base ref maximum.

    Existing numbering gaps on the base ref are tolerated (no contiguity check).
    Modifications to or deletions of existing migration files are ignored.

    Args:
        migration_dir: Repository-relative migration directory being checked.
        base_paths: Migration file paths present at the base ref.
        current_paths: Migration file paths present in the current git index.

    Returns:
        Tuple of ``(base_max, violations)`` where ``base_max`` is the highest
        migration version on the base ref (``0`` if none) and ``violations`` is
        a list of structured violation dictionaries.
    """
    base_versions = [
        version for path in base_paths if (version := parse_migration_version(path)) is not None
    ]
    base_max = max(base_versions, default=0)
    added_paths = sorted(set(current_paths) - set(base_paths))
    added_by_version = _group_added_version_names(added_paths)
    has_base = bool(base_versions)

    violations = [
        violation
        for path in added_paths
        if (
            violation := _check_added_path(
                migration_dir, path, base_max, has_base, added_by_version
            )
        )
        is not None
    ]
    return base_max, violations


def _run_git(
    repo_root: Path,
    git_args: Sequence[str],
    *,
    handled_instruction: str,
) -> str:
    """Execute a git subcommand and return stdout without polluting stderr on error."""
    proc = run_command(
        ["git", *git_args],
        cwd=repo_root,
        capture_output=True,
        log_cmd=False,
        log_on_error=False,
        errors="replace",
        handled_instruction=handled_instruction,
    )
    return proc.stdout


def _validate_repo_and_base(
    repo_root: Path,
    base_ref: str,
    migration_dirs: Sequence[str],
) -> None:
    """Validate git repository root, target directories, and base ref."""
    if not base_ref or base_ref.startswith("-"):
        raise HandledError(
            f"Invalid base ref: {base_ref!r}",
            "Pass a non-empty --base-ref that does not start with a hyphen.",
        )
    if not repo_root.is_dir() or not (repo_root / ".git").exists():
        raise HandledError(
            f"Invalid git repository root: {repo_root}",
            "Pass --repo-root pointing to the top-level git working tree.",
        )
    if any(not d for d in migration_dirs):
        raise HandledError(
            "Migration directory path cannot be empty or '/'.",
            "Pass non-empty repository-relative paths to --dir.",
        )
    missing_dirs = [d for d in migration_dirs if not (repo_root / d).is_dir()]
    if missing_dirs:
        raise HandledError(
            f"Migration directory not found under {repo_root}: {', '.join(missing_dirs)}",
            "Verify --repo-root and --dir paths point to existing migration directories.",
            details={"missing_dirs": missing_dirs},
        )
    _run_git(
        repo_root,
        ["rev-parse", "--verify", f"{base_ref}^{{commit}}"],
        handled_instruction=(
            f"Base ref '{base_ref}' could not be resolved. Run 'git fetch origin main' "
            "or pass a valid --base-ref."
        ),
    )


def _list_base_paths(repo_root: Path, base_ref: str, migration_dir: str) -> list[str]:
    """List repository-relative paths under ``migration_dir`` at ``base_ref``."""
    git_args = ["-c", "core.quotePath=false", "ls-tree", "-r", "--name-only", "-z", base_ref]
    stdout = _run_git(
        repo_root,
        [*git_args, "--", migration_dir],
        handled_instruction=f"Failed to list '{migration_dir}' at base ref '{base_ref}'.",
    )
    return [entry for entry in stdout.split("\0") if entry]


def _list_current_paths(repo_root: Path, migration_dir: str) -> list[str]:
    """List git-indexed repository-relative paths under ``migration_dir``."""
    stdout = _run_git(
        repo_root,
        ["-c", "core.quotePath=false", "ls-files", "-z", "--", migration_dir],
        handled_instruction=f"Failed to list indexed files under '{migration_dir}'.",
    )
    return [entry for entry in stdout.split("\0") if entry]


def _build_violation_error(
    base_ref: str,
    checked_dirs: dict[str, dict[str, int]],
    violations: list[dict[str, Any]],
) -> HandledError:
    """Build a HandledError whose message and instruction match violation kinds."""
    count = len(violations)
    kinds = {v["kind"] for v in violations}
    lockstep = "(keeping Spanner and PostgreSQL versions in lockstep)."
    if kinds == {"duplicate_version"}:
        msg = f"Found {count} duplicate-version migration file(s) added in this change."
        instr = f"Assign each newly added migration a unique version number {lockstep}"
    elif kinds == {"out_of_order"}:
        msg = f"Found {count} out-of-order migration file(s) (version <= max on '{base_ref}')."
        instr = f"Renumber added migration(s) strictly above max on '{base_ref}' {lockstep}"
    else:
        msg = (
            f"Found {count} migration ordering/duplicate-version violation(s) against '{base_ref}'."
        )
        instr = f"Assign each added migration a unique version strictly above max on '{base_ref}' {lockstep}"
    return HandledError(
        msg,
        instr,
        details={"base_ref": base_ref, "checked_dirs": checked_dirs, "violations": violations},
    )


def check_migration_order(args: argparse.Namespace) -> dict[str, Any]:
    """Verify no added ESS migration has a version ``<=`` the base ref maximum.

    Args:
        args: Parsed CLI namespace with ``repo_root``, ``base_ref``, and
            optional ``dirs``.

    Returns:
        Summary dictionary containing ``base_ref``, per-directory ``checked_dirs``
        metadata, and ``violations`` count (``0`` on success).

    Raises:
        HandledError: If repository/base validation fails or any added migration
            is numbered ``<=`` the maximum version on ``base_ref`` or duplicates
            another added migration's version.
    """
    repo_root = Path(args.repo_root).resolve()
    raw_dirs = args.dirs if args.dirs else DEFAULT_MIGRATION_DIRS
    migration_dirs = tuple(d.strip("/") for d in raw_dirs)
    _validate_repo_and_base(repo_root, args.base_ref, migration_dirs)

    checked_dirs: dict[str, dict[str, int]] = {}
    all_violations: list[dict[str, Any]] = []
    for migration_dir in migration_dirs:
        base_paths = _list_base_paths(repo_root, args.base_ref, migration_dir)
        current_paths = _list_current_paths(repo_root, migration_dir)
        base_max, violations = find_out_of_order_migrations(
            migration_dir, base_paths, current_paths
        )
        added_count = len(set(current_paths) - set(base_paths))
        checked_dirs[migration_dir] = {"base_max": base_max, "added_files": added_count}
        all_violations.extend(violations)

    if all_violations:
        raise _build_violation_error(args.base_ref, checked_dirs, all_violations)
    return {
        "base_ref": args.base_ref,
        "checked_dirs": checked_dirs,
        "violations": 0,
    }


def build_parser() -> argparse.ArgumentParser:
    """Build the CLI argument parser."""
    parser = argparse.ArgumentParser(
        description="Reject added ESS migrations with version <= max on the base ref.",
    )
    parser.add_argument("--repo-root", default=str(_REPO_ROOT), help="Repository root directory.")
    parser.add_argument(
        "--base-ref", default="origin/main", help="Target branch git ref (default: origin/main)."
    )
    parser.add_argument(
        "--dir",
        action="append",
        dest="dirs",
        help="Migration dir relative to repo root (repeatable; defaults to ESS migration dirs).",
    )
    return parser


def main() -> None:
    """Run the migration ordering linter CLI."""
    run_tool(build_parser(), check_migration_order)


if __name__ == "__main__":
    main()
