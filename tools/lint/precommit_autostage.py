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

"""Restages pre-commit formatter fixes for paths that were fully staged.

.githooks/pre-commit runs `snapshot` before its fixers and `restage` after them.
`restage` re-adds only the paths that were staged without unstaged changes when
the hook started and that a fixer then modified, so unrelated unstaged work is
never staged. Any other path a fixer modified (partially staged, unstaged, or
previously clean) is left untouched and fails the hook so the developer can
review and stage it. Paths with unstaged changes that no fixer touched are
ignored. Progress goes to stderr; stdout carries only the JSON result.
"""

import argparse
import dataclasses
import json
import subprocess
import sys
from collections.abc import Iterable, Mapping
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402


@dataclasses.dataclass(frozen=True)
class Snapshot:
    """Index state captured before the pre-commit fixers run.

    staged holds the paths staged for commit. unstaged_hashes maps each path whose
    working tree differs from the index to the blob hash of its working tree
    content, or None when the working tree file is missing.
    """

    staged: frozenset[str]
    unstaged_hashes: Mapping[str, str | None]

    def to_json(self) -> dict[str, Any]:
        return {"staged": sorted(self.staged), "unstaged_hashes": dict(self.unstaged_hashes)}

    @classmethod
    def from_json(cls, data: Mapping[str, Any]) -> "Snapshot":
        return cls(frozenset(data["staged"]), dict(data["unstaged_hashes"]))


@dataclasses.dataclass(frozen=True)
class RestagePlan:
    """Fixer-modified paths split into those safe to restage and those that are not."""

    restage: tuple[str, ...]
    blocked: tuple[str, ...]


def plan_restage(
    snapshot: Snapshot,
    modified: Iterable[str],
    current_hashes: Mapping[str, str | None],
) -> RestagePlan:
    """Splits the paths whose working tree now differs from the index.

    Args:
        snapshot: Index state captured before the fixers ran.
        modified: Paths whose working tree differs from the index after the fixers ran.
        current_hashes: Current working tree blob hashes of the snapshot's unstaged paths.

    Returns:
        Fully staged paths a fixer changed (restage) and every other path a fixer
        changed (blocked). Unstaged paths whose content is unchanged are omitted.
    """
    restage: list[str] = []
    blocked: list[str] = []
    for path in sorted(set(modified)):
        if path in snapshot.unstaged_hashes:
            if current_hashes.get(path) != snapshot.unstaged_hashes[path]:
                blocked.append(path)
        elif path in snapshot.staged:
            restage.append(path)
        else:
            blocked.append(path)
    return RestagePlan(tuple(restage), tuple(blocked))


def _git(repo: Path, *args: str, stdin: str | None = None) -> str:
    return subprocess.run(
        ["git", *args], cwd=repo, check=True, capture_output=True, text=True, input=stdin
    ).stdout


def _diff_names(repo: Path, *args: str) -> frozenset[str]:
    output = _git(repo, "diff", "-z", "--name-only", *args)
    return frozenset(name for name in output.split("\0") if name)


def _worktree_hashes(repo: Path, paths: Iterable[str]) -> dict[str, str | None]:
    hashes: dict[str, str | None] = dict.fromkeys(paths)
    # Paths go over stdin so repo-wide fixer changes cannot exceed ARG_MAX.
    existing = sorted(path for path in hashes if "\n" not in path and (repo / path).is_file())
    if existing:
        output = _git(repo, "hash-object", "--stdin-paths", stdin="\n".join(existing) + "\n")
        hashes.update(zip(existing, output.split(), strict=True))
    return hashes


def _stage(repo: Path, paths: Iterable[str]) -> None:
    _git(
        repo,
        "--literal-pathspecs",
        "add",
        "--pathspec-from-file=-",
        "--pathspec-file-nul",
        stdin="\0".join(paths),
    )


def take_snapshot(repo: Path) -> Snapshot:
    """Captures the staged paths and the working tree hashes of unstaged paths."""
    staged = _diff_names(repo, "--cached")
    return Snapshot(staged, _worktree_hashes(repo, _diff_names(repo)))


def restage_fixes(repo: Path, snapshot: Snapshot) -> RestagePlan:
    """Stages fixer changes to fully staged paths and reports the remaining ones."""
    modified = _diff_names(repo)
    current = _worktree_hashes(repo, modified & snapshot.unstaged_hashes.keys())
    plan = plan_restage(snapshot, modified, current)
    if plan.restage:
        _stage(repo, plan.restage)
        print("--> Restaged formatter fixes to staged files:", file=sys.stderr)
        for path in plan.restage:
            print(f"      {path}", file=sys.stderr)
    return plan


def load_snapshot(state: Path) -> Snapshot:
    """Reads the snapshot state file written by the snapshot subcommand."""
    try:
        return Snapshot.from_json(json.loads(state.read_text(encoding="utf-8")))
    except (OSError, ValueError, KeyError, TypeError) as e:
        raise HandledError(
            f"Cannot read pre-commit snapshot state {state}: {e}",
            "Run the snapshot subcommand before restage; .githooks/pre-commit does this "
            "automatically. Set XMC_PRECOMMIT_NO_AUTOSTAGE=1 to bypass auto-restaging.",
        ) from e


def run(args: argparse.Namespace) -> dict[str, Any]:
    """Executes the snapshot or restage subcommand."""
    repo = Path(args.repo_root)
    state = Path(args.state)
    if args.command == "snapshot":
        snapshot = take_snapshot(repo)
        state.write_text(json.dumps(snapshot.to_json()), encoding="utf-8")
        return {"staged": len(snapshot.staged), "unstaged": len(snapshot.unstaged_hashes)}

    plan = restage_fixes(repo, load_snapshot(state))
    result = {"restaged": list(plan.restage), "blocked": list(plan.blocked)}
    if plan.blocked:
        raise HandledError(
            "Linters modified files that were not fully staged: " + ", ".join(plan.blocked),
            "Review the modified files (git diff), stage them, and re-run git commit. Files "
            "with both staged and unstaged changes are never restaged automatically.",
            details=result,
        )
    return result


def main() -> None:
    """CLI entrypoint."""
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("command", choices=("snapshot", "restage"), help="Hook phase to run.")
    parser.add_argument("--state", required=True, help="Snapshot JSON file path.")
    parser.add_argument("--repo-root", default=".", help="Repository root (default: cwd).")
    run_tool(parser, run)


if __name__ == "__main__":
    main()
