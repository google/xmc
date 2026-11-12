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

"""Streamlined helper tool to dispatch GitHub Actions release workflows, poll, and watch outcomes."""

import argparse
import json
import re
import subprocess
import sys
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, run_command, run_tool
from tools.nightly_tests import workflow_matrix

_REF_RE = re.compile(r"[A-Za-z0-9](?!.*\.\.)[A-Za-z0-9._-]*(?:/[A-Za-z0-9._-]+)*")
_INPUT_KEY_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_-]*")
_EXAMPLES_WORKFLOWS = frozenset({"nightly-examples-e2e.yml", "examples-smoke.yml"})


def _run_gh(args: list[str], repo_dir: Path | None = None) -> subprocess.CompletedProcess[str]:
    """Runs GitHub CLI command with handled error for missing binary."""
    return run_command(
        ["gh", *args],
        capture_output=True,
        check=False,
        log_cmd=False,
        log_on_error=False,
        cwd=repo_dir,
        handled_instruction="Ensure 'gh' is on PATH and authenticate with 'gh auth login'.",
    )


def _gh_json(args: list[str], repo_dir: Path | None, expected_type: type) -> Any:
    """Runs a `gh` command and returns its JSON output if it is `expected_type`, else None."""
    res = _run_gh(args, repo_dir)
    if res.returncode != 0 or not res.stdout.strip():
        return None
    try:
        parsed = json.loads(res.stdout)
    except json.JSONDecodeError:
        return None
    return parsed if isinstance(parsed, expected_type) else None


def _extract_step_page_url(step: Any, get_url: Any) -> str:
    if not isinstance(step, dict):
        return ""
    if url := get_url(step):
        return url
    outputs = step.get("outputs") or step.get("output")
    if isinstance(outputs, str) and outputs.strip().startswith("{"):
        try:
            outputs = json.loads(outputs)
        except json.JSONDecodeError:
            outputs = None
    return get_url(outputs)


def _extract_page_url(view_data: Any) -> str:
    """Extracts GitHub Pages URL from run view data or jobs step outputs if available."""
    if not isinstance(view_data, dict):
        return ""

    def _get_url(obj: Any) -> str:
        if isinstance(obj, dict):
            return str(obj.get("page_url") or obj.get("pageUrl") or "")
        return ""

    if url := _get_url(view_data):
        return url

    for job in view_data.get("jobs", []):
        if not isinstance(job, dict):
            continue
        env = job.get("environment")
        if url := (
            _get_url(job) or (str(env["url"]) if isinstance(env, dict) and env.get("url") else "")
        ):
            return url
        for step in job.get("steps", []):
            if url := _extract_step_page_url(step, _get_url):
                return url
    return ""


def _update_state_file(
    state_file_path: str | None,
    target_sha: str,
    run_sha: str,
    run_id: int,
    conclusion: str,
) -> None:
    """Updates and persists release state to state_file if provided."""
    if not state_file_path:
        return
    p = Path(state_file_path)
    state: dict[str, Any] = {}
    if p.is_file():
        try:
            loaded = json.loads(p.read_text(encoding="utf-8"))
            if isinstance(loaded, dict):
                state = loaded
        except (json.JSONDecodeError, OSError):
            state = {}
    state.update(
        {
            "target_sha": target_sha or run_sha or state.get("target_sha", ""),
            "last_run_id": run_id,
            "conclusion": conclusion,
        }
    )
    try:
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(json.dumps(state, indent=2) + "\n", encoding="utf-8")
    except OSError as e:
        print(f"Warning: Failed to persist release state to {p}: {e}", file=sys.stderr)


def _find_existing_run(
    workflow: str,
    ref: str,
    target_sha: str,
    state_file: str | None,
    repo_dir: Path | None,
) -> dict[str, Any] | None:
    """Finds existing successful run matching target SHA if requested."""
    cmd = [
        "run",
        "list",
        f"--workflow={workflow}",
        f"--branch={ref}",
        "--status=success",
        "--limit=5",
        "--json=databaseId,headSha,url,conclusion",
    ]
    for run in _gh_json(cmd, repo_dir, list) or []:
        if not target_sha or run.get("headSha", "").startswith(target_sha):
            run_id = int(run["databaseId"])
            run_sha = run.get("headSha", "")
            _update_state_file(
                state_file,
                target_sha=target_sha,
                run_sha=run_sha,
                run_id=run_id,
                conclusion="success",
            )
            return {
                "run_id": run_id,
                "url": run.get("url", ""),
                "head_sha": run_sha,
                "conclusion": "success",
                "failed_jobs": [],
                "page_url": "",
            }
    return None


def _validate_inputs(workflow: str, raw_inputs: list[str], repo_dir: Path | None) -> None:
    """Validates repeatable KEY=VALUE workflow_dispatch inputs and examples_filter IDs."""
    parsed: dict[str, str] = {}
    for item in raw_inputs:
        key, sep, value = item.partition("=")
        if not sep or not _INPUT_KEY_RE.fullmatch(key):
            raise HandledError(f"Invalid workflow input '{item}'.", "Use --input KEY=VALUE.")
        if key in parsed:
            raise HandledError(f"Duplicate workflow input key '{key}'.", "Specify each key once.")
        parsed[key] = value
    if Path(workflow).name not in _EXAMPLES_WORKFLOWS or "examples_filter" not in parsed:
        return
    try:
        workflow_text = workflow_matrix.resolve_workflow_path(repo_dir).read_text(encoding="utf-8")
        matrix_items = workflow_matrix.parse_matrix_items(workflow_text)
        workflow_matrix.validate_examples_filter(parsed["examples_filter"], matrix_items)
    except (OSError, ValueError) as exc:
        hint = "Pass comma-separated matrix.example.id values without spaces."
        raise HandledError(f"Invalid examples_filter for '{workflow}': {exc}", hint) from exc


def _run_git(args: list[str], cwd: Path | None, hint: str) -> str:
    res = run_command(
        ["git", *args],
        cwd=cwd,
        capture_output=True,
        log_cmd=False,
        log_on_error=False,
        handled_instruction=hint,
    )
    return res.stdout.strip()


def _resolve_remote_sha(ref: str, cwd: Path | None) -> str:
    """Fetches origin/<ref> and returns the resolved remote commit SHA via FETCH_HEAD."""
    hint = f"Ensure 'git' is on PATH and '{ref}' is pushed (git push -u origin {ref})."
    _run_git(["fetch", "--", "origin", ref], cwd, hint)
    return _run_git(["rev-parse", "--verify", "FETCH_HEAD^{commit}"], cwd, "Invalid remote commit.")


def _dispatch_workflow(workflow: str, ref: str, inputs: list[str], repo_dir: Path | None) -> str:
    """Dispatches workflow run and returns ISO timestamp of dispatch."""
    dispatched_at = datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    input_args = [arg for item in inputs for arg in ("-f", item)]
    res = _run_gh(["workflow", "run", workflow, "--ref", ref, *input_args], repo_dir)
    if res.returncode != 0:
        raise HandledError(
            f"Failed to dispatch workflow '{workflow}': {res.stderr.strip()}",
            "Verify workflow name and GitHub permissions.",
        )
    return dispatched_at


def _poll_for_run(
    workflow: str,
    ref: str,
    target_sha: str,
    dispatched_at: str,
    poll_attempts: int,
    poll_interval: float,
    repo_dir: Path | None,
) -> dict[str, Any]:
    """Polls GitHub Actions API for registered workflow run matching target criteria."""
    list_cmd = [
        "run",
        "list",
        f"--workflow={workflow}",
        f"--branch={ref}",
        "--event=workflow_dispatch",
        "--limit=5",
        "--json=databaseId,headSha,url,createdAt",
    ]
    matched_run: dict[str, Any] | None = None
    for attempt in range(poll_attempts):
        if attempt > 0:
            time.sleep(poll_interval)
        for r in _gh_json(list_cmd, repo_dir, list) or []:
            r_sha = r.get("headSha", "")
            r_created = r.get("createdAt", "")
            if r_created >= dispatched_at:
                if target_sha and r_sha.startswith(target_sha):
                    return r
                if not matched_run:
                    matched_run = r
        if matched_run and not target_sha:
            return matched_run

    if matched_run:
        return matched_run

    raise HandledError(
        f"Timed out waiting for '{workflow}' run to register on ref '{ref}'.",
        "Check GitHub Actions status.",
    )


def _watch_run_outcome(
    run_id: int,
    run_sha: str,
    run_url: str,
    target_sha: str,
    state_file: str | None,
    repo_dir: Path | None,
) -> dict[str, Any]:
    """Watches run to completion, parses conclusion and page_url, and updates state."""
    res = _run_gh(["run", "watch", str(run_id), "--exit-status"], repo_dir)
    view_cmd = ["run", "view", str(run_id), "--json=conclusion,jobs"]
    view_data: dict[str, Any] = _gh_json(view_cmd, repo_dir, dict) or {}

    conclusion = view_data.get("conclusion") or ("success" if res.returncode == 0 else "failure")
    page_url = _extract_page_url(view_data)
    failed_jobs = [
        {"name": j.get("name", ""), "conclusion": j.get("conclusion", "")}
        for j in view_data.get("jobs", [])
        if j.get("conclusion") == "failure"
    ]

    _update_state_file(
        state_file,
        target_sha=target_sha,
        run_sha=run_sha,
        run_id=run_id,
        conclusion=conclusion,
    )

    return {
        "run_id": run_id,
        "url": run_url,
        "head_sha": run_sha,
        "conclusion": conclusion,
        "failed_jobs": failed_jobs,
        "page_url": page_url,
    }


def _validate_args(args: argparse.Namespace, repo_dir: Path | None) -> str:
    """Validates CLI flags and workflow inputs; returns cleaned ref."""
    ref = args.ref.strip()
    if ref.startswith("origin/") or not _REF_RE.fullmatch(ref):
        raise HandledError(f"Invalid --ref '{args.ref}'.", "Pass a branch or tag name on origin.")
    if args.find_existing and args.input:
        raise HandledError("--find-existing cannot be combined with --input.", "Omit flag.")
    _validate_inputs(args.workflow, args.input, repo_dir)
    return ref


def dispatch_and_watch(args: argparse.Namespace) -> dict[str, Any]:
    """Dispatches a workflow, verifies target SHA, watches run, and returns structured summary."""
    workflow, state_file = args.workflow, args.state_file
    repo_dir = Path(args.repo_dir) if args.repo_dir else None
    ref = _validate_args(args, repo_dir)
    target_sha = args.target_sha.strip()

    # 1. Check for existing successful run if --find-existing
    if args.find_existing:
        existing = _find_existing_run(workflow, ref, target_sha, state_file, repo_dir)
        if existing:
            return existing

    # 2. Dispatch workflow run
    target_sha = target_sha or _resolve_remote_sha(ref, repo_dir)
    dispatched_at = _dispatch_workflow(workflow, ref, args.input, repo_dir)

    # 3. Poll for registered workflow run matching target_sha & timestamp
    run_data = _poll_for_run(
        workflow,
        ref,
        target_sha,
        dispatched_at,
        args.poll_attempts,
        args.poll_interval,
        repo_dir,
    )

    run_id = int(run_data["databaseId"])
    run_sha = run_data.get("headSha", "")
    run_url = run_data.get("url", f"https://github.com/google/xmc/actions/runs/{run_id}")

    # 4. Strict commit SHA verification
    if target_sha and not run_sha.startswith(target_sha):
        _update_state_file(
            state_file,
            target_sha=target_sha,
            run_sha=run_sha,
            run_id=run_id,
            conclusion="sha_mismatch",
        )
        return {
            "run_id": run_id,
            "url": run_url,
            "head_sha": run_sha,
            "conclusion": "sha_mismatch",
            "failed_jobs": [],
            "page_url": "",
        }

    # 5. Watch to completion via `gh run watch`
    return _watch_run_outcome(run_id, run_sha, run_url, target_sha, state_file, repo_dir)


def create_parser() -> argparse.ArgumentParser:
    """Constructs CLI argument parser."""
    p = argparse.ArgumentParser(
        description="Dispatches a GitHub Actions workflow and watches outcome."
    )
    p.add_argument("--workflow", "-w", required=True, help="Workflow filename.")
    p.add_argument("--target-sha", "--sha", "-s", default="", help="Expected commit SHA.")
    p.add_argument("--ref", "-r", default="main", help="Git branch or ref.")
    p.add_argument(
        "--find-existing",
        action="store_true",
        help="Reuse existing successful run.",
    )
    p.add_argument("--state-file", help="Path to JSON state file.")
    p.add_argument("--input", action="append", default=[], metavar="KEY=VALUE", help="Input.")
    p.add_argument("--poll-interval", type=float, default=5.0, help="Poll interval.")
    p.add_argument("--poll-attempts", type=int, default=12, help="Max poll attempts.")
    p.add_argument("--repo-dir", default=".", help="Repository root.")
    return p


def main() -> None:
    run_tool(create_parser(), dispatch_and_watch)


if __name__ == "__main__":
    main()
