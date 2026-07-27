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

"""Tool for parsing and comparing subagent trajectories and outcomes from transcript.jsonl files."""

import argparse
import json
from pathlib import Path
from typing import Any

from tool_utils import HandledError, run_tool


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
    tool_counts: dict[str, int] = {}
    last_response = ""
    modified_files: list[str] = []

    try:
        with open(transcript_path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                total_steps += 1
                try:
                    step = json.loads(line)
                    if not isinstance(step, dict):
                        continue
                except json.JSONDecodeError:
                    continue

                step_type = step.get("type", "")
                status = step.get("status", "")

                if step_type == "PLANNER_RESPONSE":
                    turns += 1
                    content = step.get("content", "")
                    if content:
                        last_response = content

                if status == "ERROR":
                    error_count += 1

                tool_calls = step.get("tool_calls", [])
                if isinstance(tool_calls, list):
                    for tc in tool_calls:
                        if isinstance(tc, dict):
                            name = tc.get("name") or tc.get("tool_name") or "unknown_tool"
                            tool_counts[name] = tool_counts.get(name, 0) + 1
                            args = tc.get("args") or {}
                            if isinstance(args, dict):
                                target = args.get("TargetFile") or args.get("AbsolutePath")
                                if target and target not in modified_files:
                                    modified_files.append(str(target))
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
        "last_response_snippet": last_response[:500] if last_response else "N/A",
        "modified_files": modified_files,
    }


def generate_markdown_report(
    baseline: dict[str, Any], candidate: dict[str, Any], prompt: str | None = None
) -> str:
    """Generates a Markdown report comparing baseline vs candidate trajectories and outcomes."""
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

### 🏆 Final Benchmark Verdict
*[Main Agent Verdict: PASSED / PASSED WITH RESERVATIONS (NEEDS EFFICIENCY TUNING) / REJECTED]*
*[Rationale: Evaluate whether Candidate improved output quality without causing unnecessary turn/token bloat for the given prompt.]*
"""


def compare_trajectories(args: argparse.Namespace) -> dict[str, Any]:
    """Main orchestrator for trajectory and outcome comparison tool."""
    baseline = parse_transcript(Path(args.baseline_transcript))
    candidate = parse_transcript(Path(args.candidate_transcript))

    report = generate_markdown_report(baseline, candidate, prompt=args.prompt)

    result = {
        "baseline": baseline,
        "candidate": candidate,
        "delta": {
            "turn_diff": candidate["turns"] - baseline["turns"],
            "error_diff": candidate["error_count"] - baseline["error_count"],
        },
        "markdown_report": report,
    }

    if args.output:
        try:
            with open(args.output, "w", encoding="utf-8") as f:
                f.write(report)
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
        "-o",
        "--output",
        help="Optional path to write markdown output report.",
    )
    run_tool(parser, compare_trajectories)


if __name__ == "__main__":
    main()
