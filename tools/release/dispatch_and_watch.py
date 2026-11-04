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
import shutil
import subprocess
import sys
import time
from datetime import UTC, datetime
from functools import partial
from pathlib import Path
from typing import Any

from tools.common.tool_utils import HandledError, run_command, run_tool
from tools.nightly_tests import examples_import_smoke as smoke

_REF_RE = re.compile(r"^[A-Za-z0-9](?!.*\.\.)[A-Za-z0-9._-]*(?:/[A-Za-z0-9._-]+)*$")
_INPUT_KEY_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_-]*$")
_EXAMPLES_WORKFLOWS = frozenset({"nightly-examples-e2e.yml", "examples-smoke.yml"})


def _run_gh(args: list[str], repo_dir: Path | None = None) -> subprocess.CompletedProcess[str]:
    """Runs GitHub CLI command with handled error for missing binary."""
    gh = partial(run_command, capture_output=True, check=False, log_cmd=False, log_on_error=False)
    hint = "Ensure 'gh' is on PATH and authenticate with 'gh auth login'."
    return gh(["gh", *args], cwd=repo_dir, handled_instruction=hint)


def _extract_page_url(view_data: dict[str, Any]) -> str:
    """Extracts GitHub Pages URL from run view data or jobs step outputs if available."""
    if not isinstance(view_data, dict):
        return ""

    def _get_url(obj: Any) -> str:
        if isinstance(obj, dict):
            return str(obj.get("page_url") or obj.get("pageUrl") or "")
        return ""

    url = _get_url(view_data)
    if url:
        return url

    for job in view_data.get("jobs", []):
        if not isinstance(job, dict):
            continue
        url = _get_url(job)
        if url:
            return url
        env = job.get("environment")
        if isinstance(env, dict) and env.get("url"):
            return str(env["url"])
        for step in job.get("steps", []):
            if not isinstance(step, dict):
                continue
            url = _get_url(step)
            if url:
                return url
            outputs = step.get("outputs") or step.get("output")
            if isinstance(outputs, str) and outputs.strip().startswith("{"):
                try:
                    outputs = json.loads(outputs)
                except json.JSONDecodeError:
                    outputs = None
            url = _get_url(outputs)
            if url:
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
    res = _run_gh(cmd, repo_dir)
    if res.returncode != 0 or not res.stdout.strip():
        return None
    try:
        existing_runs = json.loads(res.stdout)
        if not isinstance(existing_runs, list):
            return None
    except json.JSONDecodeError:
        return None

    for run in existing_runs:
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


def _validate_ref(ref: str) -> str:
    """Validates a target Git ref and rejects 'origin/' prefixes or refspec syntax."""
    cleaned = ref.strip()
    if cleaned.startswith("origin/") or not _REF_RE.match(cleaned):
        raise HandledError(f"Invalid --ref '{ref}'.", "Pass a branch or tag name on origin.")
    return cleaned


def _validate_inputs(workflow: str, raw_inputs: list[str], repo_dir: Path | None) -> None:
    """Validates repeatable KEY=VALUE workflow_dispatch inputs and examples_filter IDs."""
    parsed: dict[str, str] = {}
    for item in raw_inputs:
        key, sep, value = item.partition("=")
        if not sep or not _INPUT_KEY_RE.match(key):
            raise HandledError(f"Invalid workflow input '{item}'.", "Use --input KEY=VALUE.")
        if key in parsed:
            raise HandledError(f"Duplicate workflow input key '{key}'.", "Specify each key once.")
        parsed[key] = value
    if Path(workflow).name not in _EXAMPLES_WORKFLOWS or "examples_filter" not in parsed:
        return
    try:
        wf_text = smoke.resolve_workflow_path(repo_dir).read_text(encoding="utf-8")
        smoke.validate_examples_filter(parsed["examples_filter"], wf_text)
    except (OSError, ValueError) as exc:
        hint = "Pass comma-separated matrix.example.id values without spaces."
        raise HandledError(f"Invalid examples_filter for '{workflow}': {exc}", hint) from exc


def _resolve_remote_sha(ref: str, cwd: Path | None) -> str:
    """Fetches origin/<ref> and returns the resolved remote commit SHA via FETCH_HEAD."""
    git = partial(run_command, cwd=cwd, capture_output=True, log_cmd=False, log_on_error=False)
    hint = f"Ensure 'git' is on PATH and '{ref}' is pushed (git push -u origin {ref})."
    git(["git", "fetch", "--", "origin", ref], handled_instruction=hint)
    rev_cmd = ["git", "rev-parse", "--verify", "FETCH_HEAD^{commit}"]
    return git(rev_cmd, handled_instruction="Verify remote ref commit.").stdout.strip()


def _dispatch_workflow(workflow: str, ref: str, inputs: list[str], repo_dir: Path | None) -> str:
    """Dispatches workflow run with optional inputs and returns ISO timestamp of dispatch."""
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
        res = _run_gh(list_cmd, repo_dir)
        if res.returncode == 0 and res.stdout.strip():
            try:
                runs = json.loads(res.stdout)
                if not isinstance(runs, list):
                    runs = []
            except json.JSONDecodeError:
                runs = []
            for r in runs:
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
    view_res = _run_gh(["run", "view", str(run_id), "--json=conclusion,jobs"], repo_dir)
    view_data: dict[str, Any] = {}
    if view_res.returncode == 0 and view_res.stdout.strip():
        try:
            loaded_view = json.loads(view_res.stdout)
            if isinstance(loaded_view, dict):
                view_data = loaded_view
        except json.JSONDecodeError:
            view_data = {}

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


def dispatch_and_watch(args: argparse.Namespace) -> dict[str, Any]:
    """Dispatches a workflow, verifies target SHA, watches run, and returns structured summary."""
    workflow, ref = args.workflow, _validate_ref(args.ref)
    target_sha = (getattr(args, "target_sha", "") or "").strip()
    state_file = getattr(args, "state_file", None)
    repo_dir = Path(args.repo_dir) if getattr(args, "repo_dir", None) else None
    if args.find_existing and args.input:
        raise HandledError("--find-existing cannot be combined with --input.", "Omit flag.")
    _validate_inputs(workflow, args.input, repo_dir)
    if not shutil.which("gh"):
        raise HandledError("GitHub CLI ('gh') is not in PATH.", "Run 'gh auth login'.")

    # 1. Check for existing successful run if --find-existing
    if getattr(args, "find_existing", False):
        existing = _find_existing_run(workflow, ref, target_sha, state_file, repo_dir)
        if existing:
            return existing

    # 2. Dispatch workflow run
    if not target_sha:
        target_sha = _resolve_remote_sha(ref, repo_dir)
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
