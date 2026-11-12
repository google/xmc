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

"""Tool for parsing and comparing subagent trajectories and outcomes from transcript.jsonl files."""

import argparse
import collections
import json
import sys
from collections.abc import Mapping
from pathlib import Path
from types import MappingProxyType
from typing import Any

_REPO_ROOT = Path(__file__).resolve().parent.parent.parent
if str(_REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(_REPO_ROOT))

from tools.benchmark.scenario_assertions import (  # noqa: E402
    add_workspace_override_arguments,
    apply_workspace_overrides,
    evaluate_spec,
    load_spec,
    render_report,
)
from tools.common.tool_utils import HandledError, run_tool  # noqa: E402

# Maps each file-mutating tool name to the argument holding the target file path. Covers both
# the legacy transcript schema (`write_to_file`/`TargetFile`) and the current one
# (`write_file`/`edit_file` with `Path`).
_MUTATING_TOOL_PATH_ARGS: Mapping[str, str] = MappingProxyType(
    {
        "write_to_file": "TargetFile",
        "replace_file_content": "TargetFile",
        "multi_replace_file_content": "TargetFile",
        "write_file": "Path",
        "edit_file": "Path",
    }
)

# Step types that represent an error regardless of their `status` (current schema logs, e.g.,
# model API failures as `ERROR_MESSAGE` steps with status `DONE`).
_ERROR_STEP_TYPES: frozenset[str] = frozenset({"ERROR_MESSAGE"})


def _is_error_step(step: dict[str, Any]) -> bool:
    """Returns whether a transcript step records an error in either transcript schema.

    The legacy schema marks failed steps with status `ERROR`; the current schema may also attach
    a non-empty `error` field or emit a dedicated `ERROR_MESSAGE` step.
    """
    if step.get("status") == "ERROR":
        return True
    if step.get("type") in _ERROR_STEP_TYPES:
        return True
    return bool(step.get("error"))


def _record_tool_call(
    tc: Any,
    tool_counts: collections.Counter[str],
    modified_files: list[str],
) -> None:
    """Records tool usage metrics and tracks files touched by mutating tools."""
    if not isinstance(tc, dict):
        return
    name = tc.get("name") or tc.get("tool_name") or "unknown_tool"
    tool_counts[name] += 1
    path_arg = _MUTATING_TOOL_PATH_ARGS.get(name)
    if path_arg is None:
        return
    args = tc.get("args") or {}
    if isinstance(args, dict):
        target = args.get(path_arg)
        if target and target not in modified_files:
            modified_files.append(str(target))


def _parse_step_line(line: str) -> dict[str, Any] | None:
    """Parses a single JSONL line into a step dictionary if valid."""
    stripped = line.strip()
    if not stripped:
        return None
    try:
        step = json.loads(stripped)
        return step if isinstance(step, dict) else None
    except json.JSONDecodeError:
        return None


def parse_transcript(transcript_path: Path) -> dict[str, Any]:
    """Parses a transcript.jsonl file and extracts trajectory metrics and outcome artifacts."""
    if not transcript_path.is_file():
        raise HandledError(
            f"Transcript file not found: {transcript_path}",
            "Ensure the subagent execution finished and transcript log path is valid.",
        )

    file_bytes = transcript_path.stat().st_size
    est_tokens = file_bytes // 4
    total_steps = 0
    turns = 0
    error_count = 0
    tool_counts: collections.Counter[str] = collections.Counter()
    last_response = ""
    modified_files: list[str] = []

    try:
        with open(transcript_path, encoding="utf-8") as f:
            for line in f:
                step = _parse_step_line(line)
                if step is None:
                    continue
                total_steps += 1
                if step.get("type") == "PLANNER_RESPONSE":
                    turns += 1
                    content = step.get("content", "")
                    if content:
                        last_response = content

                if _is_error_step(step):
                    error_count += 1

                tool_calls = step.get("tool_calls", [])
                if isinstance(tool_calls, list):
                    for tc in tool_calls:
                        _record_tool_call(tc, tool_counts, modified_files)
    except OSError as e:
        raise HandledError(
            f"Failed to read transcript file '{transcript_path}': {e}",
            "Check file permissions and path.",
        ) from e

    return {
        "transcript_path": str(transcript_path),
        "file_bytes": file_bytes,
        "est_tokens": est_tokens,
        "total_steps": total_steps,
        "turns": turns,
        "error_count": error_count,
        "tool_counts": tool_counts,
        "last_response": last_response,
        "last_response_snippet": (
            last_response[:500].replace("```", "'''") if last_response else "N/A"
        ),
        "modified_files": modified_files,
    }


def generate_markdown_report(
    baseline: dict[str, Any],
    candidate: dict[str, Any],
    prompt: str | None = None,
    verdict_section: str | None = None,
) -> str:
    """Generates a Markdown report comparing baseline vs candidate trajectories and outcomes.

    When `verdict_section` is supplied it replaces the agent-authored verdict placeholder with a
    machine-derived one, produced by evaluating pre-registered scenario assertions.
    """
    turn_diff = candidate["turns"] - baseline["turns"]
    error_diff = candidate["error_count"] - baseline["error_count"]

    if turn_diff < 0:
        turn_summary = f"🟢 **{abs(turn_diff)} fewer turns** (More efficient)"
    elif turn_diff > 0:
        turn_summary = f"🔴 **{turn_diff} extra turns** (Deeper execution/overhead)"
    else:
        turn_summary = "⚪ **Equal turn count**"

    prompt_header = f'- **Test Prompt**: *"{prompt}"*\n' if prompt else ""
    b_kb = baseline["file_bytes"] // 1024
    c_kb = candidate["file_bytes"] // 1024
    kb_diff = c_kb - b_kb
    tok_diff = candidate["est_tokens"] - baseline["est_tokens"]
    step_diff = candidate["total_steps"] - baseline["total_steps"]

    all_tools = sorted(set(baseline["tool_counts"].keys()) | set(candidate["tool_counts"].keys()))
    tool_rows = []
    for tool in all_tools:
        b_cnt = baseline["tool_counts"].get(tool, 0)
        c_cnt = candidate["tool_counts"].get(tool, 0)
        tool_rows.append(f"| `{tool}` | {b_cnt} | {c_cnt} | {c_cnt - b_cnt:+d} |")

    tool_table = "\n".join(tool_rows) if tool_rows else "| *No tools called* | - | - | - |"

    verdict_block = verdict_section or (
        "### 🏆 Final Benchmark Verdict\n"
        "*[Main Agent Verdict: SHIP / SHIP (NO NEGATIVE REGRESSION SCENARIO) / "
        "SHIP (ORIGIN ONLY - GENERALIZATION UNTESTED) / "
        "SHIP WITH RESERVATIONS (NEEDS EFFICIENCY TUNING) / "
        "UNDER-GENERALIZED / OVER-GENERALIZED / WRONG PREDICATE / REJECTED]*\n"
        "*[No pre-registered assertions were supplied, so this verdict is a judgement call. "
        "Prefer `--assertions` so the matrix is derived mechanically.]*\n"
    )

    return f"""### 📊 Trajectory & Outcome Comparison Report

{prompt_header}
---

### Pillar 1: Trajectory Telemetry (Efficiency & Behavior)

| Metric | Baseline (Clean) | Candidate (Modified) | Delta & LLM Assessment |
| :--- | :--- | :--- | :--- |
| **Turns (LLM Calls)** | {baseline["turns"]} | {candidate["turns"]} | {turn_summary} |
| **Context Footprint** | ~{b_kb} KB (~{baseline["est_tokens"]:,} tokens) | ~{c_kb} KB (~{candidate["est_tokens"]:,} tokens) | {kb_diff:+d} KB ({tok_diff:+d} tokens) |
| **Error / Retry Steps** | {baseline["error_count"]} | {candidate["error_count"]} | {error_diff:+d} errors |
| **Total Steps** | {baseline["total_steps"]} | {candidate["total_steps"]} | {step_diff:+d} steps |

#### 🛠️ Tool Call Breakdown
| Tool Name | Baseline | Candidate | Delta |
| :--- | :--- | :--- | :--- |
{tool_table}

---

### Pillar 2: Outcome & Quality Evaluation

#### 📝 Side-by-Side Final Output Comparison

<details open>
<summary><b>Baseline Output Preview</b></summary>

```text
{baseline["last_response_snippet"]}
```
</details>

<details open>
<summary><b>Candidate Output Preview</b></summary>

```text
{candidate["last_response_snippet"]}
```
</details>

#### 🎯 LLM-as-a-Judge Outcome Scorecard
*(Evaluated by Main Agent applying Prompt Relevance & Pragmatism Filter: Do NOT reward extra turns or test execution if irrelevant to prompt intent)*

| Evaluation Dimension | Baseline Output | Candidate Output | Superior Output |
| :--- | :--- | :--- | :--- |
| **Rule / Skill Adherence** | *[Evaluated by Agent]* | *[Evaluated by Agent]* | *[Baseline / Candidate / Tie]* |
| **Functional Correctness** | *[Evaluated by Agent or N/A]* | *[Evaluated by Agent or N/A (Irrelevant)]* | *[Baseline / Candidate / N/A]* |
| **Completeness & Accuracy**| *[Evaluated by Agent]* | *[Evaluated by Agent]* | *[Baseline / Candidate / Tie]* |

---

{verdict_block}"""


def compare_trajectories(args: argparse.Namespace) -> dict[str, Any]:
    """Main orchestrator for trajectory and outcome comparison tool."""
    baseline = parse_transcript(Path(args.baseline_transcript))
    candidate = parse_transcript(Path(args.candidate_transcript))

    verdict_section = None
    verdict = None
    if args.assertions:
        spec = apply_workspace_overrides(
            load_spec(Path(args.assertions)),
            origin_workspace=getattr(args, "origin_workspace", None),
            origin_baseline_workspace=getattr(args, "origin_baseline_workspace", None),
            transfer_workspace=getattr(args, "transfer_workspace", None),
            negative_workspace=getattr(args, "negative_workspace", None),
        )
        outcomes, derived = evaluate_spec(spec)
        verdict_section = render_report(outcomes, derived)
        verdict = str(derived)

    report = generate_markdown_report(
        baseline, candidate, prompt=args.prompt, verdict_section=verdict_section
    )

    result = {
        "baseline": baseline,
        "candidate": candidate,
        "delta": {
            "turn_diff": candidate["turns"] - baseline["turns"],
            "error_diff": candidate["error_count"] - baseline["error_count"],
        },
        "verdict": verdict,
        "markdown_report": report,
    }

    if args.output:
        try:
            Path(args.output).write_text(report, encoding="utf-8")
        except OSError as e:
            raise HandledError(
                f"Failed to write output report to '{args.output}': {e}",
                "Check file path permissions.",
            ) from e

    return result


def main() -> None:
    """Main entrypoint for compare_trajectories tool."""
    parser = argparse.ArgumentParser(
        description="Compare subagent trajectories and outcome artifacts from transcript.jsonl files."
    )
    parser.add_argument(
        "baseline_transcript",
        help="Path to baseline transcript.jsonl file.",
    )
    parser.add_argument(
        "candidate_transcript",
        help="Path to candidate transcript.jsonl file.",
    )
    parser.add_argument(
        "--prompt",
        help="Optional test prompt text executed by the subagents.",
    )
    parser.add_argument(
        "--assertions",
        help=(
            "Optional path to a pre-registered scenario assertion spec. When supplied, the verdict "
            "matrix is derived mechanically instead of being left to the reviewing agent."
        ),
    )
    parser.add_argument(
        "-o",
        "--output",
        help="Optional path to write markdown output report.",
    )
    add_workspace_override_arguments(parser)
    run_tool(parser, compare_trajectories)


if __name__ == "__main__":
    main()
