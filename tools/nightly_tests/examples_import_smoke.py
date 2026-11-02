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
and plainly imports each entrypoint module (imports and absl flag definitions run) in
a dedicated subprocess of the given virtualenv interpreter, mirroring the nightly
launch step's working directory and PYTHONPATH. `app.run(main)` is never reached
because an imported module's `__name__` is not `__main__`, so no workload is
launched and no cluster or credentials are required.

Exit codes: 0 when all entrypoints import cleanly, 1 when any entrypoint fails,
2 on unexpected errors (e.g. no entrypoints discovered).
"""

from __future__ import annotations

import argparse
import dataclasses
import functools
import json
import os
import pathlib
import re
import subprocess
import sys
from collections.abc import Iterator, Sequence

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
DEFAULT_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "nightly-examples-e2e.yml"

# Matches an `example_path` / `entrypoint` key, optionally opening a YAML list item.
_KEY_RE = re.compile(r"^\s*(?:-\s+)?(?P<key>example_path|entrypoint):(?P<value>.*)$")
# Matches the first line of a YAML list item (`- key: ...`).
_ITEM_START_RE = re.compile(r"^\s*-\s")
# Number of trailing non-empty stderr lines kept as failure context.
_STDERR_TAIL_LINES = 5
# Per-entrypoint import budget, so one blocking import cannot hang the whole report.
_IMPORT_TIMEOUT_SECONDS = 120
# Plainly imports the entrypoint module (e.g. `import launch`) inside a per-example
# subprocess whose cwd/PYTHONPATH is the example dir. The module body (imports, absl flag
# definitions) runs with `__name__ == "launch"`, so `app.run(main)` is never reached.
# One subprocess per example keeps the absl flag registry and sibling modules (several
# examples ship their own `worker.py`) isolated, as in the nightly suite. sys.argv is
# reset to [script] so argv[0] matches `python3 <script>`.
_RUN_SNIPPET = (
    "import importlib, sys; script = sys.argv[1]; sys.argv = [script]; "
    "importlib.import_module(script.removesuffix('.py'))"
)


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


def _unquote(raw: str) -> str:
    """Return a scalar YAML value without surrounding quotes or a trailing comment."""
    value = raw.strip()
    if value[:1] in ("'", '"'):
        end = value.find(value[0], 1)
        return value[1:end] if end > 0 else value[1:]
    return value.split(" #", 1)[0].strip()


def _flush_item(item: dict[str, str], entries: dict[Entrypoint, None]) -> None:
    """Record a parsed list item as an entrypoint, rejecting half-specified items.

    Items with both keys empty or absent are UI-only checks and are skipped.
    """
    path = item.get("example_path", "")
    entrypoint = item.get("entrypoint", "")
    if not path and not entrypoint:
        return
    if not path or not entrypoint:
        raise ValueError(
            f"matrix entry has example_path={path!r} and entrypoint={entrypoint!r}; "
            "both must be set (or both empty for UI-only entries)"
        )
    entries[Entrypoint(path, entrypoint)] = None


def _matrix_lines(workflow_text: str) -> Iterator[str]:
    """Yield only lines nested under a `matrix:` key, using indentation to scope blocks."""
    matrix_indent: int | None = None
    for line in workflow_text.splitlines():
        stripped = line.lstrip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(stripped)
        if matrix_indent is not None and indent <= matrix_indent:
            matrix_indent = None
        if matrix_indent is not None:
            yield line
        elif stripped.startswith("matrix:"):
            matrix_indent = indent


def parse_entrypoints(workflow_text: str) -> list[Entrypoint]:
    """Extract unique, non-empty example entrypoints from the nightly workflow.

    Only lines under `matrix:` blocks are considered, so same-named keys in step
    inputs or other jobs are ignored. Keys are grouped per YAML list item, so
    their order, quoting style (single, double, or none), trailing comments, and
    interleaved keys do not matter.

    Args:
        workflow_text: Contents of the nightly examples workflow YAML.

    Returns:
        De-duplicated entrypoints in first-seen order; entries with an empty
        `example_path` (UI-only checks) are skipped.

    Raises:
        ValueError: If a matrix entry sets only one of `example_path` and
            `entrypoint`, so matrix drift cannot silently drop examples.
    """
    entries: dict[Entrypoint, None] = {}
    item: dict[str, str] = {}
    for line in _matrix_lines(workflow_text):
        if _ITEM_START_RE.match(line):
            _flush_item(item, entries)
            item = {}
        key_match = _KEY_RE.match(line)
        if key_match:
            item[key_match.group("key")] = _unquote(key_match.group("value"))
    _flush_item(item, entries)
    return list(entries)


def _stderr_tail(text: str) -> str:
    """Return the last few non-empty stderr lines to keep traceback context."""
    lines = [line for line in text.strip().splitlines() if line.strip()]
    return "\n".join(lines[-_STDERR_TAIL_LINES:])


def _entrypoint_env(example_dir: pathlib.Path, repo_root: pathlib.Path) -> dict[str, str]:
    """Return the environment mirroring the nightly launch step's PYTHONPATH."""
    env = dict(os.environ)
    env["PYTHONPATH"] = os.pathsep.join(
        p for p in (str(example_dir), str(repo_root), env.get("PYTHONPATH", "")) if p
    )
    return env


def _entrypoint_problem(example_dir: pathlib.Path, entrypoint: str) -> str | None:
    """Return why an entrypoint cannot be import-checked, or None if it can."""
    module = entrypoint.removesuffix(".py")
    if module == entrypoint or not module.isidentifier():
        return f"entrypoint {entrypoint!r} is not an importable module file (<identifier>.py)"
    if not (example_dir / entrypoint).is_file():
        return f"entrypoint not found: {example_dir / entrypoint}"
    return None


def smoke_entrypoint(
    venv_python: pathlib.Path, repo_root: pathlib.Path, entry: Entrypoint
) -> SmokeResult:
    """Execute one entrypoint's module body in an isolated subprocess.

    Args:
        venv_python: Absolute path of the virtualenv interpreter.
        repo_root: Absolute repository root containing the examples.
        entry: Entrypoint to check.

    Returns:
        The smoke result, carrying the stderr tail (or the reason) on failure.
    """
    example_dir = repo_root / entry.example_path
    failed = functools.partial(SmokeResult, entry.example_path, entry.entrypoint, False)
    problem = _entrypoint_problem(example_dir, entry.entrypoint)
    if problem:
        return failed(problem)
    try:
        proc = subprocess.run(
            [str(venv_python), "-c", _RUN_SNIPPET, entry.entrypoint],
            cwd=example_dir,
            env=_entrypoint_env(example_dir, repo_root),
            capture_output=True,
            text=True,
            check=False,
            timeout=_IMPORT_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired:
        return failed(f"import timed out after {_IMPORT_TIMEOUT_SECONDS}s")
    except OSError as err:
        return failed(f"failed to start {venv_python}: {err}")
    if proc.returncode == 0:
        return SmokeResult(entry.example_path, entry.entrypoint, ok=True)
    return failed(_stderr_tail(proc.stderr) or f"exit code {proc.returncode}")


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
    args = parser.parse_args(argv)
    # Subprocesses run with cwd=<example dir>, so relative paths must be anchored now.
    # absolute() (not resolve()) keeps the venv's python symlink and its site-packages.
    args.venv_python = args.venv_python.absolute()
    args.repo_root = args.repo_root.resolve()
    return args


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
    except (ValueError, OSError) as err:
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
