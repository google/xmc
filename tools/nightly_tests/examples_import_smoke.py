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

Discovers every matrix entry of the nightly workflow and, each in a dedicated
subprocess of the given virtualenv interpreter:
- plainly imports the entrypoint module (imports and absl flag definitions run),
  mirroring the nightly launch step's working directory, PYTHONPATH and argv[0];
- for entrypoints that define absl flags, parses the matrix `args` against them, so
  renamed or removed flags fail;
- requires a top-level `if __name__ == "__main__":` guard, so importing never runs
  `main()`;
- imports each `verify_module` from the repo root, as the nightly verify step's
  `python -m <module>` would.
No workload is launched and no cluster or credentials are required.

Exit codes: 0 when every check passes, 1 when any check fails, 2 on unexpected
errors (e.g. no entrypoints discovered).
"""

from __future__ import annotations

import argparse
import ast
import dataclasses
import functools
import json
import os
import pathlib
import re
import subprocess
import sys
import textwrap
from collections.abc import Iterator, Sequence

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
DEFAULT_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "nightly-examples-e2e.yml"

# Matrix entry keys the tool reads.
_MATRIX_KEYS = ("id", "example_path", "entrypoint", "args", "verify_module")
# Matches a tracked matrix key, optionally opening a YAML list item.
_KEY_RE = re.compile(rf"^\s*(?:-\s+)?(?P<key>{'|'.join(_MATRIX_KEYS)}):(?P<value>.*)$")
# Matches the first line of a YAML list item (`- key: ...`).
_ITEM_START_RE = re.compile(r"^\s*-\s")
# `${VAR}` placeholders in matrix args (expanded by the nightly step's `eval echo`).
_PLACEHOLDER_RE = re.compile(r"\$\{(?P<name>[A-Za-z_][A-Za-z0-9_]*)\}")
# Placeholders the nightly launch step defines, with the value used when unset here.
_KNOWN_PLACEHOLDERS = {"QUEUE_NAME": "cpu-queue"}
# Number of trailing non-empty stderr lines kept as failure context.
_STDERR_TAIL_LINES = 5
# Per-check budget. Observed checks take about 0.6s; 30s x ~30 checks stays under the
# 25-minute job timeout, so a hang still yields a JSON report.
_IMPORT_TIMEOUT_SECONDS = 30
# Plainly imports the entrypoint module (e.g. `import launch`) inside a per-example
# subprocess whose cwd/PYTHONPATH is the example dir. The module body (imports, absl flag
# definitions) runs with `__name__ == "launch"`, so `app.run(main)` is never reached.
# One subprocess per example is required: 12 of 13 entrypoints share the module name
# `launch` (sys.modules would return the first one cached), absl's flag registry is
# process-global (simple_job and workunit_artifact_job both define --queue), and each
# import gets its own timeout. sys.argv is reset to [script] so argv[0] matches
# `python3 <script>`. When the module itself defined absl flags, the matrix args are
# parsed against them; argparse-based entrypoints parse inside main() and are skipped.
_RUN_SNIPPET = textwrap.dedent(
    """\
    import importlib, shlex, sys
    script, raw_args = sys.argv[1], sys.argv[2]
    sys.argv = [script]
    module = script.removesuffix(".py")
    importlib.import_module(module)
    args = shlex.split(raw_args)
    if args:
        from absl import flags
        if module in flags.FLAGS.flags_by_module_dict():
            flags.FLAGS([script, *args])
    """
)
# Imports a verify module by name from the repo root (cwd), like `python -m <module>`.
_VERIFY_SNIPPET = "import importlib, sys; importlib.import_module(sys.argv[1])"


@dataclasses.dataclass(frozen=True, order=True)
class Entrypoint:
    """An example entrypoint launched by the nightly suite, with its matrix args."""

    example_path: str
    entrypoint: str
    args: str = ""


@dataclasses.dataclass(frozen=True)
class SmokeResult:
    """Outcome of one check: an entrypoint import (+ args) or a verify-module import."""

    example_path: str
    entrypoint: str
    ok: bool
    error: str = ""
    args: str = ""
    kind: str = "entrypoint"


def _unquote(raw: str) -> str:
    """Return a scalar YAML value without surrounding quotes or a trailing comment."""
    value = raw.strip()
    if value[:1] in ("'", '"'):
        end = value.find(value[0], 1)
        return value[1:end] if end > 0 else value[1:]
    return value.split(" #", 1)[0].strip()


def _entrypoint_from_item(item: dict[str, str]) -> Entrypoint | None:
    """Build an entrypoint from a matrix item, rejecting half-specified items.

    Items with both keys empty or absent are UI-only checks and yield None.
    """
    path = item.get("example_path", "")
    entrypoint = item.get("entrypoint", "")
    if not path and not entrypoint:
        return None
    if not path or not entrypoint:
        raise ValueError(
            f"matrix entry has example_path={path!r} and entrypoint={entrypoint!r}; "
            "both must be set (or both empty for UI-only entries)"
        )
    return Entrypoint(path, entrypoint, item.get("args", ""))


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


def parse_matrix_items(workflow_text: str) -> list[dict[str, str]]:
    """Return the tracked keys of every YAML list item under `matrix:` blocks.

    Only lines under `matrix:` blocks are considered, so same-named keys in step
    inputs or other jobs are ignored. Keys are grouped per YAML list item, so
    their order, quoting style (single, double, or none), trailing comments, and
    interleaved keys do not matter.
    """
    items: list[dict[str, str]] = []
    # Indentation of the `-` opening the active list item; None when outside an item,
    # so keys after a list ends (or before the first item of a later matrix block)
    # are never attributed to the previous item.
    item_indent: int | None = None
    for line in _matrix_lines(workflow_text):
        indent = len(line) - len(line.lstrip())
        if _ITEM_START_RE.match(line):
            items.append({})
            item_indent = indent
        elif item_indent is not None and indent <= item_indent:
            item_indent = None
        key_match = _KEY_RE.match(line)
        if key_match and item_indent is not None:
            items[-1][key_match.group("key")] = _unquote(key_match.group("value"))
    return [item for item in items if item]


def _selected_items(
    workflow_text: str, examples_filter: frozenset[str]
) -> Iterator[dict[str, str]]:
    """Yield matrix items, keeping only filtered `id`s when a filter is given."""
    for item in parse_matrix_items(workflow_text):
        if examples_filter and item.get("id", "").lower() not in examples_filter:
            continue
        yield item


def parse_entrypoints(
    workflow_text: str, examples_filter: frozenset[str] = frozenset()
) -> list[Entrypoint]:
    """Extract unique, non-empty example entrypoints (with args) from the workflow.

    Args:
        workflow_text: Contents of the nightly examples workflow YAML.
        examples_filter: Optional lower-cased matrix `id`s to keep, matching the
            nightly `examples_filter` input; empty keeps every entry.

    Returns:
        De-duplicated `(example_path, entrypoint, args)` entries in first-seen
        order; entries with an empty `example_path` (UI-only checks) are skipped.

    Raises:
        ValueError: If a matrix entry sets only one of `example_path` and
            `entrypoint`, so matrix drift cannot silently drop examples.
    """
    entries: dict[Entrypoint, None] = {}
    for item in _selected_items(workflow_text, examples_filter):
        entry = _entrypoint_from_item(item)
        if entry is not None:
            entries[entry] = None
    return list(entries)


def parse_verify_modules(
    workflow_text: str, examples_filter: frozenset[str] = frozenset()
) -> list[str]:
    """Return unique matrix `verify_module`s (including UI-only entries), in order."""
    modules = (
        item.get("verify_module", "") for item in _selected_items(workflow_text, examples_filter)
    )
    return list(dict.fromkeys(module for module in modules if module))


def expand_args(raw_args: str) -> str:
    """Expand the `${VAR}` placeholders the nightly launch step defines.

    Raises:
        ValueError: For any placeholder the nightly step does not define, so it
            cannot pass here as a literal string and fail nightly (or vice versa).
    """

    def _substitute(match: re.Match[str]) -> str:
        name = match.group("name")
        if name not in _KNOWN_PLACEHOLDERS:
            raise ValueError(f"unknown placeholder ${{{name}}} in matrix args {raw_args!r}")
        return os.environ.get(name, _KNOWN_PLACEHOLDERS[name])

    return _PLACEHOLDER_RE.sub(_substitute, raw_args)


def _stderr_tail(text: str) -> str:
    """Return the last few non-empty stderr lines to keep traceback context."""
    lines = [line for line in text.strip().splitlines() if line.strip()]
    return "\n".join(lines[-_STDERR_TAIL_LINES:])


def _pythonpath_env(*paths: pathlib.Path) -> dict[str, str]:
    """Return os.environ with the given paths prepended to PYTHONPATH."""
    env = dict(os.environ)
    env["PYTHONPATH"] = os.pathsep.join(
        p for p in (*map(str, paths), env.get("PYTHONPATH", "")) if p
    )
    return env


def _has_main_guard(script: pathlib.Path) -> bool:
    """Return whether a module has a top-level `if __name__ == "__main__":` block."""
    for node in ast.parse(script.read_text(encoding="utf-8"), filename=str(script)).body:
        if not isinstance(node, ast.If) or not isinstance(node.test, ast.Compare):
            continue
        if len(node.test.ops) != 1 or not isinstance(node.test.ops[0], ast.Eq):
            continue
        operands = [node.test.left, *node.test.comparators]
        names = {o.id for o in operands if isinstance(o, ast.Name)}
        values = {o.value for o in operands if isinstance(o, ast.Constant)}
        if names == {"__name__"} and values == {"__main__"}:
            return True
    return False


def _entrypoint_problem(example_dir: pathlib.Path, entrypoint: str) -> str | None:
    """Return why an entrypoint cannot be import-checked safely, or None if it can."""
    module = entrypoint.removesuffix(".py")
    if module == entrypoint or not module.isidentifier():
        return f"entrypoint {entrypoint!r} is not an importable module file (<identifier>.py)"
    script = example_dir / entrypoint
    if not script.is_file():
        return f"entrypoint not found: {script}"
    try:
        has_guard = _has_main_guard(script)
    except (SyntaxError, UnicodeDecodeError) as err:
        return f"failed to parse {script}: {err}"
    if not has_guard:
        return f'{script} has no top-level `if __name__ == "__main__":` guard'
    return None


def _run_probe(cmd: list[str], cwd: pathlib.Path, env: dict[str, str]) -> str | None:
    """Run one time-boxed probe subprocess; return its failure reason, or None if ok."""
    try:
        proc = subprocess.run(
            cmd,
            cwd=cwd,
            env=env,
            capture_output=True,
            text=True,
            check=False,
            timeout=_IMPORT_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired:
        return f"import timed out after {_IMPORT_TIMEOUT_SECONDS}s"
    except OSError as err:
        return f"failed to start {cmd[0]}: {err}"
    if proc.returncode == 0:
        return None
    return _stderr_tail(proc.stderr) or f"exit code {proc.returncode}"


def smoke_entrypoint(
    venv_python: pathlib.Path, repo_root: pathlib.Path, entry: Entrypoint
) -> SmokeResult:
    """Import one entrypoint (and parse its absl args) in an isolated subprocess.

    Args:
        venv_python: Absolute path of the virtualenv interpreter.
        repo_root: Absolute repository root containing the examples.
        entry: Entrypoint to check.

    Returns:
        The smoke result, carrying the stderr tail (or the reason) on failure.
    """
    example_dir = repo_root / entry.example_path
    result = functools.partial(SmokeResult, entry.example_path, entry.entrypoint, args=entry.args)
    problem = _entrypoint_problem(example_dir, entry.entrypoint)
    if problem is None:
        try:
            expanded = expand_args(entry.args)
        except ValueError as err:
            return result(ok=False, error=str(err))
        cmd = [str(venv_python), "-c", _RUN_SNIPPET, entry.entrypoint, expanded]
        # Mirrors the nightly launch step: PYTHONPATH=<example dir>:<repo root>.
        problem = _run_probe(cmd, example_dir, _pythonpath_env(example_dir, repo_root))
    return result(ok=problem is None, error=problem or "")


def smoke_verify_module(
    venv_python: pathlib.Path, repo_root: pathlib.Path, module: str
) -> SmokeResult:
    """Import one verify module from the repo root in an isolated subprocess.

    Nightly runs `python -m <module>` with cwd = repo root, which puts the repo root
    on sys.path; it is added explicitly here because `-c` with a safe-path
    interpreter (PYTHONSAFEPATH) would not.
    """
    cmd = [str(venv_python), "-c", _VERIFY_SNIPPET, module]
    problem = _run_probe(cmd, repo_root, _pythonpath_env(repo_root))
    return SmokeResult(".", module, ok=problem is None, error=problem or "", kind="verify_module")


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
    parser.add_argument(
        "--list-example-paths",
        action="store_true",
        help="Print the unique matrix example paths (one per line) and exit; used by CI to "
        "install only the requirements of examples the nightly suite actually runs.",
    )
    parser.add_argument(
        "--examples-filter",
        default="",
        help="Comma-separated matrix ids to check (case-insensitive), mirroring the "
        "nightly `examples_filter` input; empty checks every entry.",
    )
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
    examples_filter = frozenset(
        part.strip().lower() for part in args.examples_filter.split(",") if part.strip()
    )
    try:
        workflow_text = args.workflow.read_text(encoding="utf-8")
        entries = parse_entrypoints(workflow_text, examples_filter)
        verify_modules = parse_verify_modules(workflow_text, examples_filter)
    except (ValueError, OSError) as err:
        print(json.dumps({"error": f"{args.workflow}: {err}"}))
        return 2
    if not entries and not examples_filter:
        print(json.dumps({"error": f"no example entrypoints found in {args.workflow}"}))
        return 2
    if args.list_example_paths:
        # May be empty when the filter selects only UI-only entries (no entrypoint).
        print("\n".join(dict.fromkeys(e.example_path for e in entries)))
        return 0
    results = [smoke_entrypoint(args.venv_python, args.repo_root, e) for e in entries]
    results += [smoke_verify_module(args.venv_python, args.repo_root, m) for m in verify_modules]
    print(json.dumps({"results": [dataclasses.asdict(r) for r in results]}, indent=2))
    return 0 if all(r.ok for r in results) else 1


if __name__ == "__main__":
    sys.exit(main())
