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
import shlex
import subprocess
import sys
import textwrap
from collections.abc import Iterator, Sequence
from typing import Literal

REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from tools.nightly_tests.workflow_matrix import (  # noqa: E402
    DEFAULT_WORKFLOW,
    parse_matrix_items,
    validate_examples_filter,
)

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
# `python3 <script>`. Tokens after the script are the matrix args, already tokenized as
# nightly would. When the entrypoint module itself defined absl flags, they are parsed
# like `app.run` does (always, so flag validators run even without args), and leftover
# positional args fail, as the absl entrypoints' `main()` rejects them. argparse-based
# entrypoints parse inside main() and are skipped.
_RUN_SNIPPET = textwrap.dedent(
    """\
    import importlib, sys
    script, args = sys.argv[1], sys.argv[2:]
    sys.argv = [script]
    module = script.removesuffix(".py")
    importlib.import_module(module)
    absl_flags = sys.modules.get("absl.flags")
    if absl_flags and module in absl_flags.FLAGS.flags_by_module_dict():
        rest = absl_flags.FLAGS([script, *args])
        if len(rest) > 1:
            sys.exit(f"unexpected positional args: {rest[1:]}")
    """
)
# Runs a verify module's body from the repo root the way `python -m <module>` does
# (via runpy, not cached under its import name), but without its `__main__` block.
_VERIFY_SNIPPET = "import runpy, sys; runpy.run_module(sys.argv[1], run_name='__examples_smoke__')"


@dataclasses.dataclass(frozen=True)
class Entrypoint:
    """An example entrypoint launched by the nightly suite, with its matrix args."""

    example_path: str
    entrypoint: str
    args: str = ""


@dataclasses.dataclass(frozen=True)
class SmokeResult:
    """Outcome of one check: an entrypoint import (+ args) or a verify-module import.

    For `kind == "verify_module"`, `example_path` is empty and `entrypoint` holds the
    module name.
    """

    example_path: str
    entrypoint: str
    ok: bool
    error: str = ""
    args: str = ""
    kind: Literal["entrypoint", "verify_module"] = "entrypoint"


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


def _selected_items(
    matrix_items: list[dict[str, str]], examples_filter: frozenset[str]
) -> Iterator[dict[str, str]]:
    """Yield matrix items, keeping only filtered `id`s when a filter is given."""
    for item in matrix_items:
        if examples_filter and item.get("id", "").lower() not in examples_filter:
            continue
        yield item


def _entrypoints_from_items(
    matrix_items: list[dict[str, str]], examples_filter: frozenset[str]
) -> list[Entrypoint]:
    """Return unique, non-empty entrypoints from pre-parsed matrix items."""
    entries: dict[Entrypoint, None] = {}
    for item in _selected_items(matrix_items, examples_filter):
        entry = _entrypoint_from_item(item)
        if entry is not None:
            entries[entry] = None
    return list(entries)


def _verify_modules_from_items(
    matrix_items: list[dict[str, str]], examples_filter: frozenset[str]
) -> list[str]:
    """Return unique `verify_module`s from pre-parsed matrix items, in order."""
    modules = (
        item.get("verify_module", "") for item in _selected_items(matrix_items, examples_filter)
    )
    return list(dict.fromkeys(module for module in modules if module))


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
    return _entrypoints_from_items(parse_matrix_items(workflow_text), examples_filter)


def parse_verify_modules(
    workflow_text: str, examples_filter: frozenset[str] = frozenset()
) -> list[str]:
    """Return unique matrix `verify_module`s (including UI-only entries), in order."""
    return _verify_modules_from_items(parse_matrix_items(workflow_text), examples_filter)


def expand_args(raw_args: str) -> list[str]:
    """Expand and tokenize matrix args the way the nightly launch step does.

    Nightly runs `EVAL_ARGS=$(eval echo "${RAW_ARGS}")` and passes `${EVAL_ARGS}`
    unquoted: quotes are removed, `echo` joins the words, and the shell splits again
    on whitespace (so a quoted value containing spaces becomes several tokens).

    Raises:
        ValueError: For a placeholder the nightly step does not define, any other
            shell expansion (e.g. `$VAR`, `${VAR:-x}`, `$(...)`), or unbalanced
            quotes, so nothing passes here as a literal and differs in nightly.
    """

    def _substitute(match: re.Match[str]) -> str:
        name = match.group("name")
        if name not in _KNOWN_PLACEHOLDERS:
            raise ValueError(f"unknown placeholder ${{{name}}} in matrix args {raw_args!r}")
        return os.environ.get(name, _KNOWN_PLACEHOLDERS[name])

    expanded = _PLACEHOLDER_RE.sub(_substitute, raw_args)
    if "$" in expanded or "`" in expanded:
        raise ValueError(f"unsupported shell expansion in matrix args {raw_args!r}")
    return " ".join(shlex.split(expanded)).split()


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
    except (SyntaxError, OSError, UnicodeDecodeError) as err:
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
    if problem := _entrypoint_problem(example_dir, entry.entrypoint):
        return result(ok=False, error=problem)
    try:
        tokens = expand_args(entry.args)
    except ValueError as err:
        return result(ok=False, error=str(err))
    cmd = [str(venv_python), "-c", _RUN_SNIPPET, entry.entrypoint, *tokens]
    # Mirrors the nightly launch step: PYTHONPATH=<example dir>:<repo root>.
    problem = _run_probe(cmd, example_dir, _pythonpath_env(example_dir, repo_root))
    return result(ok=problem is None, error=problem or "")


def smoke_verify_module(
    venv_python: pathlib.Path, repo_root: pathlib.Path, module: str
) -> SmokeResult:
    """Run one verify module's body from the repo root in an isolated subprocess.

    Nightly runs `python -m <module>` with cwd = repo root, which puts the repo root
    on sys.path; it is added explicitly here because `-c` with a safe-path
    interpreter (PYTHONSAFEPATH) would not.
    """
    cmd = [str(venv_python), "-c", _VERIFY_SNIPPET, module]
    problem = _run_probe(cmd, repo_root, _pythonpath_env(repo_root))
    return SmokeResult("", module, ok=problem is None, error=problem or "", kind="verify_module")


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
        help="Comma-separated matrix.example.id values to check (case-insensitive, without spaces); "
        "empty checks every entry.",
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
    err_stream = sys.stderr if args.list_example_paths else sys.stdout
    try:
        matrix_items = parse_matrix_items(args.workflow.read_text(encoding="utf-8"))
        # Matches the nightly filter case-insensitively while rejecting unknown or malformed tokens.
        examples_filter = validate_examples_filter(args.examples_filter, matrix_items)
        entries = _entrypoints_from_items(matrix_items, examples_filter)
        verify_modules = _verify_modules_from_items(matrix_items, examples_filter)
        if not entries and not examples_filter:
            raise ValueError("no example entrypoints found")
    except (ValueError, OSError) as err:
        print(json.dumps({"error": f"{args.workflow}: {err}"}), file=err_stream)
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
