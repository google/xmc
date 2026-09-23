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

"""Evaluates pre-registered benchmark scenario predicates and derives the verdict.

The trajectory-comparison benchmark grades a candidate rule change on three scenarios: the
originating incident, a held-out member of the positive evidence set, and a member of the negative
set. Each scenario's pass predicate is registered as a machine-checkable assertion *before* the arms
are launched, so that the verdict cannot be fitted to the transcripts after the fact.
"""

import argparse
import dataclasses
import enum
import json
import re
import sys
from collections.abc import Mapping
from pathlib import Path
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

ORIGIN = "origin"
TRANSFER = "transfer"
NEGATIVE = "negative"

# Rendering order for the verdict matrix; also the set of accepted spec keys.
SCENARIOS = (ORIGIN, TRANSFER, NEGATIVE)


class Verdict(enum.StrEnum):
    """Outcome of the three-scenario matrix."""

    SHIP = "SHIP"
    SHIP_ORIGIN_ONLY = "SHIP (ORIGIN ONLY - GENERALIZATION UNTESTED)"
    OVER_GENERALIZED = "OVER-GENERALIZED"
    UNDER_GENERALIZED = "UNDER-GENERALIZED"
    WRONG_PREDICATE = "WRONG PREDICATE"
    REJECTED = "REJECTED"


@dataclasses.dataclass(frozen=True, kw_only=True)
class AssertionOutcome:
    """Result of evaluating a single pre-registered predicate."""

    description: str
    passed: bool
    detail: str


@dataclasses.dataclass(frozen=True, kw_only=True)
class ScenarioOutcome:
    """Aggregate result for one scenario; passes only when every assertion passes."""

    scenario: str
    passed: bool
    assertions: tuple[AssertionOutcome, ...]


def _search_pattern(target: Path, pattern: str | None) -> tuple[bool, str]:
    """Reports whether a regex matches the target file's contents.

    Matching is multiline, so `^` and `$` anchor to source lines rather than to the whole file.
    """
    if pattern is None:
        raise HandledError(
            f"assertion on '{target}' is missing the 'pattern' field",
            "file_contains and file_omits both require a regular expression.",
        )
    if not target.is_file():
        return False, f"file absent, cannot match /{pattern}/"
    text = target.read_text(encoding="utf-8", errors="replace")
    matched = re.search(pattern, text, re.MULTILINE) is not None
    return matched, f"/{pattern}/ {'matched' if matched else 'not matched'}"


def _file_exists(target: Path, _pattern: str | None) -> tuple[bool, str]:
    """Passes when the target file is present."""
    present = target.is_file()
    return present, "present" if present else "absent"


def _file_absent(target: Path, _pattern: str | None) -> tuple[bool, str]:
    """Passes when the target file is not present."""
    present = target.is_file()
    return not present, "present" if present else "absent"


def _file_contains(target: Path, pattern: str | None) -> tuple[bool, str]:
    """Passes when the pattern matches the target file's contents."""
    return _search_pattern(target, pattern)


def _file_omits(target: Path, pattern: str | None) -> tuple[bool, str]:
    """Passes when the pattern does not match the target file's contents."""
    matched, detail = _search_pattern(target, pattern)
    return not matched, detail


_PREDICATES = {
    "file_exists": _file_exists,
    "file_absent": _file_absent,
    "file_contains": _file_contains,
    "file_omits": _file_omits,
}


def evaluate_assertion(workspace: Path, assertion: Mapping[str, Any]) -> AssertionOutcome:
    """Evaluates one pre-registered predicate against a scenario's workspace."""
    kind = str(assertion.get("kind", ""))
    predicate = _PREDICATES.get(kind)
    if predicate is None:
        raise HandledError(
            f"unknown assertion kind: {kind!r}",
            f"Use one of: {', '.join(sorted(_PREDICATES))}.",
        )
    relative_path = assertion.get("path")
    if not relative_path:
        raise HandledError(
            f"assertion of kind '{kind}' is missing the 'path' field",
            "Every predicate names a file relative to the scenario workspace.",
        )
    passed, detail = predicate(workspace / str(relative_path), assertion.get("pattern"))
    return AssertionOutcome(description=f"{kind} {relative_path}", passed=passed, detail=detail)


def evaluate_scenario(scenario: str, spec: Mapping[str, Any]) -> ScenarioOutcome:
    """Evaluates every assertion registered for one scenario."""
    workspace = Path(str(spec.get("workspace", ".")))
    if not workspace.is_dir():
        raise HandledError(
            f"workspace for scenario '{scenario}' is not a directory: {workspace}",
            "Point 'workspace' at the arm's worktree root.",
        )
    assertions = spec.get("assertions") or []
    if not assertions:
        raise HandledError(
            f"scenario '{scenario}' registers no assertions",
            "A scenario that cannot fail measures nothing; register at least one predicate.",
        )
    outcomes = tuple(evaluate_assertion(workspace, item) for item in assertions)
    return ScenarioOutcome(
        scenario=scenario,
        passed=all(outcome.passed for outcome in outcomes),
        assertions=outcomes,
    )


def derive_verdict(outcomes: Mapping[str, ScenarioOutcome]) -> Verdict:
    """Maps per-scenario results onto the benchmark verdict matrix."""
    origin = outcomes.get(ORIGIN)
    if origin is None:
        raise HandledError(
            "the 'origin' scenario is mandatory",
            "Without it there is no evidence the change addresses the friction at all.",
        )
    if not origin.passed:
        return Verdict.REJECTED

    transfer = outcomes.get(TRANSFER)
    negative = outcomes.get(NEGATIVE)
    if transfer is None or negative is None:
        return Verdict.SHIP_ORIGIN_ONLY
    if transfer.passed and negative.passed:
        return Verdict.SHIP
    if transfer.passed:
        return Verdict.OVER_GENERALIZED
    if negative.passed:
        return Verdict.UNDER_GENERALIZED
    return Verdict.WRONG_PREDICATE


def _status_mark(outcome: ScenarioOutcome | None) -> str:
    """Renders a scenario's status, distinguishing 'failed' from 'not run'."""
    if outcome is None:
        return "—"
    return "✅" if outcome.passed else "❌"


def render_report(outcomes: Mapping[str, ScenarioOutcome], verdict: Verdict) -> str:
    """Renders the verdict matrix and per-assertion detail as markdown."""
    lines = [
        "### 🏆 Pre-Registered Scenario Verdict",
        "",
        "| Origin | Transfer | Negative | Verdict |",
        "| :---: | :---: | :---: | :--- |",
        f"| {_status_mark(outcomes.get(ORIGIN))} | {_status_mark(outcomes.get(TRANSFER))} "
        f"| {_status_mark(outcomes.get(NEGATIVE))} | **{verdict}** |",
        "",
        "#### Assertion Detail",
        "",
        "| Scenario | Assertion | Result | Detail |",
        "| :--- | :--- | :---: | :--- |",
    ]
    for scenario in SCENARIOS:
        outcome = outcomes.get(scenario)
        if outcome is None:
            continue
        for item in outcome.assertions:
            mark = "✅" if item.passed else "❌"
            lines.append(f"| {scenario} | `{item.description}` | {mark} | {item.detail} |")
    return "\n".join(lines) + "\n"


def load_spec(spec_path: Path) -> dict[str, Any]:
    """Loads the assertion spec that was registered before the arms were launched."""
    if not spec_path.is_file():
        raise HandledError(
            f"assertion spec not found: {spec_path}",
            "Pre-register the spec before launching the benchmark arms.",
        )
    try:
        spec = json.loads(spec_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as e:
        raise HandledError(
            f"assertion spec is not valid JSON: {e}",
            "Each top-level key is a scenario name mapping to workspace and assertions.",
        ) from e
    if not isinstance(spec, dict):
        raise HandledError(
            "assertion spec must be a JSON object keyed by scenario name",
            f"Valid scenario keys: {', '.join(SCENARIOS)}.",
        )
    unknown = sorted(set(spec) - set(SCENARIOS))
    if unknown:
        raise HandledError(
            f"unknown scenario keys: {', '.join(unknown)}",
            f"Valid scenario keys: {', '.join(SCENARIOS)}.",
        )
    return spec


def evaluate_spec(spec: Mapping[str, Any]) -> tuple[dict[str, ScenarioOutcome], Verdict]:
    """Evaluates every registered scenario and derives the resulting verdict."""
    outcomes = {name: evaluate_scenario(name, spec[name]) for name in SCENARIOS if name in spec}
    return outcomes, derive_verdict(outcomes)


def check_scenario_assertions(args: argparse.Namespace) -> dict[str, Any]:
    """Main orchestrator for the pre-registered assertion checker."""
    outcomes, verdict = evaluate_spec(load_spec(Path(args.spec)))
    report = render_report(outcomes, verdict)

    if args.output:
        try:
            Path(args.output).write_text(report, encoding="utf-8")
        except OSError as e:
            raise HandledError(
                f"failed to write report to '{args.output}': {e}",
                "Check file path permissions.",
            ) from e

    return {
        "verdict": str(verdict),
        "scenarios": {
            name: {
                "passed": outcome.passed,
                "assertions": [dataclasses.asdict(item) for item in outcome.assertions],
            }
            for name, outcome in outcomes.items()
        },
        "markdown_report": report,
    }


def main() -> None:
    """Main entrypoint for the scenario_assertions tool."""
    parser = argparse.ArgumentParser(
        description="Evaluate pre-registered benchmark scenario assertions and derive a verdict."
    )
    parser.add_argument(
        "spec",
        help="Path to the pre-registered assertion spec JSON file.",
    )
    parser.add_argument(
        "-o",
        "--output",
        help="Optional path to write the markdown verdict matrix.",
    )
    run_tool(parser, check_scenario_assertions)


if __name__ == "__main__":
    main()
