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

"""Import smoke check for the example entrypoints run by the Nightly Examples E2E suite.

Discovers every `(example_path, entrypoint)` pair from the nightly workflow matrix
and executes each entrypoint's module body (imports and absl flag definitions) in a
dedicated subprocess of the given virtualenv interpreter, mirroring the nightly
launch step's working directory and PYTHONPATH. `app.run(main)` is never reached
because the module is executed under a non-`__main__` name, so no workload is
launched and no cluster or credentials are required.

Exit codes: 0 when all entrypoints import cleanly, 1 when any entrypoint fails,
2 on unexpected errors (e.g. no entrypoints discovered).
"""

from __future__ import annotations

import argparse
import dataclasses
import json
import os
import pathlib
import re
import subprocess
import sys
from collections.abc import Sequence

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
DEFAULT_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "nightly-examples-e2e.yml"

# Matches every non-empty `example_path` key of a nightly matrix entry.
_PATH_RE = re.compile(r"example_path:\s*'(?P<path>[^']+)'")
# Matches the `entrypoint` key that must directly follow an `example_path` key.
_ENTRYPOINT_RE = re.compile(r"\s*\n\s*entrypoint:\s*'(?P<entrypoint>[^']+)'")
# Executes the module body without triggering `if __name__ == "__main__"`.
_RUN_SNIPPET = "import runpy, sys; runpy.run_path(sys.argv[1], run_name='__examples_smoke__')"


@dataclasses.dataclass(frozen=True, order=True)
class Entrypoint:
    """An example entrypoint launched by the nightly suite."""

    example_path: str
    entrypoint: str


@dataclasses.dataclass(frozen=True)
class SmokeResult:
    """Outcome of import-checking a single entrypoint."""

    example_path: str
    entrypoint: str
    ok: bool
    error: str = ""


def parse_entrypoints(workflow_text: str) -> list[Entrypoint]:
    """Extract unique, non-empty example entrypoints from the nightly workflow.

    Args:
        workflow_text: Contents of the nightly examples workflow YAML.

    Returns:
        De-duplicated entrypoints in first-seen order; entries with an empty
        `example_path` (UI-only checks) are skipped.

    Raises:
        ValueError: If a non-empty `example_path` is not directly followed by an
            `entrypoint` key, so matrix layout drift cannot silently drop examples.
    """
    entries: dict[Entrypoint, None] = {}
    for path_match in _PATH_RE.finditer(workflow_text):
        entry_match = _ENTRYPOINT_RE.match(workflow_text, path_match.end())
        if entry_match is None:
            raise ValueError(
                f"example_path '{path_match.group('path')}' is not directly followed by "
                "an 'entrypoint' key"
            )
        entries[Entrypoint(path_match.group("path"), entry_match.group("entrypoint"))] = None
    return list(entries)


def _last_line(text: str) -> str:
    lines = [line for line in text.strip().splitlines() if line.strip()]
    return lines[-1] if lines else ""


def smoke_entrypoint(
    venv_python: pathlib.Path, repo_root: pathlib.Path, entry: Entrypoint
) -> SmokeResult:
    """Execute one entrypoint's module body in an isolated subprocess.

    Args:
        venv_python: Interpreter of the virtualenv built by install_xmanager.py.
        repo_root: Repository root containing the examples.
        entry: Entrypoint to check.

    Returns:
        The smoke result, carrying the last stderr line on failure.
    """
    example_dir = repo_root / entry.example_path
    env = dict(os.environ)
    env["PYTHONPATH"] = os.pathsep.join(
        p for p in (str(example_dir), str(repo_root), env.get("PYTHONPATH", "")) if p
    )
    proc = subprocess.run(
        [str(venv_python), "-c", _RUN_SNIPPET, entry.entrypoint],
        cwd=example_dir,
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode == 0:
        return SmokeResult(entry.example_path, entry.entrypoint, ok=True)
    error = _last_line(proc.stderr) or f"exit code {proc.returncode}"
    return SmokeResult(entry.example_path, entry.entrypoint, ok=False, error=error)


def _parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--venv-python",
        type=pathlib.Path,
        default=pathlib.Path(sys.executable),
        help="Virtualenv interpreter used to import each entrypoint (default: current).",
    )
    parser.add_argument("--repo-root", type=pathlib.Path, default=REPO_ROOT)
    parser.add_argument("--workflow", type=pathlib.Path, default=DEFAULT_WORKFLOW)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    """Run the import smoke check and print a JSON report.

    Args:
        argv: Optional CLI arguments.

    Returns:
        Process exit code (0 success, 1 import failure, 2 unexpected error).
    """
    args = _parse_args(argv)
    try:
        entries = parse_entrypoints(args.workflow.read_text(encoding="utf-8"))
    except ValueError as err:
        print(json.dumps({"error": f"{args.workflow}: {err}"}))
        return 2
    if not entries:
        print(json.dumps({"error": f"no example entrypoints found in {args.workflow}"}))
        return 2
    results = [smoke_entrypoint(args.venv_python, args.repo_root, e) for e in entries]
    print(json.dumps({"results": [dataclasses.asdict(r) for r in results]}, indent=2))
    return 0 if all(r.ok for r in results) else 1


if __name__ == "__main__":
    sys.exit(main())
